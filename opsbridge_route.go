package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Multi-bridge routing. As of Phase 10 we run mini-UE4SS in both the
// survival container (Hagga Basin) and the deep desert container. Each
// container's OpsBridgeCppMod only sees the players physically
// connected to its own world, so per-player ops have to land on the
// right bridge.
//
// The strategy here is fan-out-first, treat "no PC with FLS" errors as
// soft-misses, and union live state across bridges. This is dumb but
// it works on a 1-sietch stack with 2 bridges total; per-bridge cost
// of a missed call is the OpsBridge socket round-trip (~1ms inside the
// docker network) which is fine.

// opsBridges returns the configured + connected bridges in priority
// order (survival first, DD second). Empty when nothing's available.
func opsBridges() []*OpsBridgeClient {
	var bridges []*OpsBridgeClient
	if globalOpsBridge != nil && globalOpsBridge.Connected() {
		bridges = append(bridges, globalOpsBridge)
	}
	if globalOpsBridgeDD != nil && globalOpsBridgeDD.Connected() {
		bridges = append(bridges, globalOpsBridgeDD)
	}
	return bridges
}

// opsAnyConnected — quick "is at least one bridge reachable?" check
// used by the system-status indicator and the per-handler 503 gate.
func opsAnyConnected() bool { return len(opsBridges()) > 0 }

// isPCNotFoundError matches the Lua-side "no PC with FLS=..." pattern
// emitted by the per-player handlers (find_pc_by_player_id returning
// nil). Bridges that don't have the player return this string;
// callers treat it as a soft miss and try the next bridge.
func isPCNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "no PC with FLS=") ||
		strings.Contains(s, "no live PC with PlayerId=") ||
		strings.Contains(s, "no PC with PlayerId")
}

// opsAnyCall fans out to every connected bridge in parallel and
// returns the first success. Per-player ops succeed on exactly one
// bridge (whichever holds the PC); server-wide ops (ServiceBroadcast,
// console exec) succeed on every bridge. Either way we return the
// first non-error reply.
//
// All bridges receive the call regardless of who answers first — a
// real broadcast ends up firing on all of them, which is what we want
// for HUD popups so DD players see them too. Per-player ops return a
// "no PC" soft-error from the non-matching bridge; we discard it.
//
// Returns a wrapped error only when EVERY bridge errored AND none of
// the errors looked like a soft "no PC" miss (i.e. the player is not
// online anywhere).
func opsAnyCall(ctx context.Context, op string, args any) (json.RawMessage, error) {
	bridges := opsBridges()
	if len(bridges) == 0 {
		return nil, fmt.Errorf("no OpsBridge connected")
	}
	type result struct {
		reply json.RawMessage
		err   error
	}
	results := make(chan result, len(bridges))
	for _, b := range bridges {
		b := b
		go func() {
			r, e := b.Call(ctx, op, args)
			results <- result{r, e}
		}()
	}
	var firstHardErr, anySoftErr error
	for range bridges {
		r := <-results
		if r.err == nil {
			return r.reply, nil
		}
		if isPCNotFoundError(r.err) {
			anySoftErr = r.err
			continue
		}
		if firstHardErr == nil {
			firstHardErr = r.err
		}
	}
	if firstHardErr != nil {
		return nil, firstHardErr
	}
	return nil, anySoftErr // "no PC anywhere" — return the soft miss as the surface error
}

// opsBroadcastCall fans out and waits for every bridge to finish,
// even when some succeed. Useful for ops whose side effect is what
// matters (e.g. ServiceBroadcast) — we don't want to short-circuit
// and skip the slow bridge. Returns the first success's reply but
// surfaces an error only when ALL bridges fail.
func opsBroadcastCall(ctx context.Context, op string, args any) (json.RawMessage, error) {
	bridges := opsBridges()
	if len(bridges) == 0 {
		return nil, fmt.Errorf("no OpsBridge connected")
	}
	type result struct {
		reply json.RawMessage
		err   error
	}
	results := make(chan result, len(bridges))
	for _, b := range bridges {
		b := b
		go func() {
			r, e := b.Call(ctx, op, args)
			results <- result{r, e}
		}()
	}
	var firstReply json.RawMessage
	var firstErr error
	successes := 0
	for range bridges {
		r := <-results
		if r.err == nil {
			successes++
			if firstReply == nil {
				firstReply = r.reply
			}
		} else if firstErr == nil {
			firstErr = r.err
		}
	}
	if successes > 0 {
		return firstReply, nil
	}
	return nil, firstErr
}

// liveOnlineSetUnion calls ListPlayers on every connected bridge and
// returns the union of FLS hex strings of players currently in-world
// anywhere. Used by the Players-tab online_status overlay so a player
// on DD shows as Online too.
//
// Brief in-process cache (1s) keeps this O(per-handler) instead of
// O(per-player-row) when a tab paint triggers many concurrent calls.
var (
	g_onlineSetMu       sync.Mutex
	g_onlineSetCache    map[string]bool
	g_onlineSetCacheUntil time.Time
)

func liveOnlineSet(ctx context.Context) (map[string]bool, bool) {
	g_onlineSetMu.Lock()
	if g_onlineSetCache != nil && time.Now().Before(g_onlineSetCacheUntil) {
		out := g_onlineSetCache
		g_onlineSetMu.Unlock()
		return out, true
	}
	g_onlineSetMu.Unlock()

	bridges := opsBridges()
	if len(bridges) == 0 {
		return nil, false
	}
	type wireRow struct {
		PlayerId string `json:"PlayerId"`
	}
	out := make(map[string]bool)
	anyOK := false
	for _, b := range bridges {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		reply, err := b.Call(callCtx, "ListPlayers", nil)
		cancel()
		if err != nil {
			continue
		}
		var innerJSON string
		if err := json.Unmarshal(reply, &innerJSON); err != nil {
			continue
		}
		var rows []wireRow
		if err := json.Unmarshal([]byte(innerJSON), &rows); err != nil {
			continue
		}
		for _, r := range rows {
			fls := strings.ToUpper(strings.TrimSpace(r.PlayerId))
			if fls != "" {
				out[fls] = true
			}
		}
		anyOK = true
	}
	if !anyOK {
		return nil, false
	}
	g_onlineSetMu.Lock()
	g_onlineSetCache = out
	g_onlineSetCacheUntil = time.Now().Add(1 * time.Second)
	g_onlineSetMu.Unlock()
	return out, true
}

// opsBridgeForMap returns the bridge that owns a given map's world
// state. HaggaBasin → survival; DeepDesert (and the partition-id'd
// DeepDesert_1) → DD. Unknown maps fall back to survival so callers
// don't blow up on a typo.
func opsBridgeForMap(mapName string) *OpsBridgeClient {
	mapName = strings.TrimSpace(mapName)
	// Strip _<digits> partition suffix so DeepDesert_1 also routes.
	if i := strings.LastIndex(mapName, "_"); i > 0 {
		suf := mapName[i+1:]
		if len(suf) > 0 && suf[0] >= '0' && suf[0] <= '9' {
			mapName = mapName[:i]
		}
	}
	switch mapName {
	case "DeepDesert":
		if globalOpsBridgeDD != nil && globalOpsBridgeDD.Connected() {
			return globalOpsBridgeDD
		}
	}
	if globalOpsBridge != nil && globalOpsBridge.Connected() {
		return globalOpsBridge
	}
	return nil
}
