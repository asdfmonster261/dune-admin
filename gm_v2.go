package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Phase 10 Stage D — GM command execute path through OpsBridge.
//
// This file is the new (live-execute) surface. The legacy gm_catalog.go
// (Snapetech speculation + 27 envelope-mode preview pane) coexists
// until Stage D1 rewrites AdminActionsTab and removes it.
//
// Catalog is hand-curated against the actual 19-command engine
// registry (probed via DuneConsoleEnablerMod's !gmregistry handler on
// 2026-06-10) plus mod-synthesized wrappers shipped by DuneOpsBridgeMod
// (TeleportToPlayer, PrintPos, etc.). The UI doesn't distinguish
// native vs synth — the Kind field exists for diagnostics.

// ── Types ──────────────────────────────────────────────────────────────

type GMParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`              // "string" | "int" | "float" | "player"
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	Min         int    `json:"min,omitempty"`     // for int
	Max         int    `json:"max,omitempty"`     // for int
	Help        string `json:"help,omitempty"`
}

// GMEntry describes one executable command. `builder` returns the
// envelope JSON the Lua-side GMCommand handler will dispatch via
// Native.Call3(0xda59f60, ...).
type GMEntry struct {
	Name    string    `json:"name"`
	Tier    string    `json:"tier"`               // comms|safe|movement|inventory|progression|spawn|player|destructive|console
	Kind    string    `json:"kind"`               // "native" | "synth"
	Status  string    `json:"status"`             // "live" | "needs-probe" | "deferred"
	Notes   string    `json:"notes,omitempty"`
	Params  []GMParam `json:"params"`
	builder func(args map[string]any) (string, error)
}

// ── Catalog ────────────────────────────────────────────────────────────
//
// D0 seeds with the ONE entry we already know how to build end-to-end
// (ServiceBroadcast, proved live by C0). D2/D3/D4/D6 fill the rest.

var gmCatalog = map[string]*GMEntry{
	"ServiceBroadcast": {
		Name:   "ServiceBroadcast",
		Tier:   "comms",
		Kind:   "native",
		Status: "live",
		Notes:  "HUD popup. Already wired via the Ops tab Announce path; this entry mirrors it for the Admin tab.",
		Params: []GMParam{
			{Name: "Title", Type: "string", Required: false, Placeholder: "Server Announcement"},
			{Name: "Body", Type: "string", Required: true, Placeholder: "Server restarting in 5 min"},
			{Name: "DurationSec", Type: "int", Required: false, Placeholder: "10", Min: 1, Max: 600},
		},
		builder: buildServiceBroadcast,
	},
}

// ── Builders ───────────────────────────────────────────────────────────

func buildServiceBroadcast(args map[string]any) (string, error) {
	title, _ := args["Title"].(string)
	body, _ := args["Body"].(string)
	if strings.TrimSpace(title) == "" && strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("at least one of Title or Body must be set")
	}
	duration := 10
	if v, ok := args["DurationSec"].(float64); ok && v > 0 {
		duration = int(v)
	}
	if duration < 1 || duration > 600 {
		return "", fmt.Errorf("DurationSec must be 1..600")
	}
	// Same shape DuneOpsBridgeMod's Broadcast handler builds locally.
	envelope := []map[string]any{
		{
			"ServerCommand":  "ServiceBroadcast",
			"BroadcastType":  "Generic",
			"BroadcastPayload": map[string]any{
				"BroadcastDuration": duration,
				"LocalizedText": []map[string]any{
					{"Key": "en", "Title": title, "Body": body},
				},
			},
		},
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}
	return string(out), nil
}

// ── HTTP handlers ──────────────────────────────────────────────────────

func handleGMv2Catalog(w http.ResponseWriter, r *http.Request) {
	out := make([]*GMEntry, 0, len(gmCatalog))
	for _, e := range gmCatalog {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tier != out[j].Tier {
			return out[i].Tier < out[j].Tier
		}
		return out[i].Name < out[j].Name
	})
	jsonOK(w, out)
}

func handleGMv2Execute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string         `json:"command"`
		Args    map[string]any `json:"args"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	entry, ok := gmCatalog[req.Command]
	if !ok {
		jsonErr(w, fmt.Errorf("unknown command: %s", req.Command), 400)
		return
	}
	if entry.builder == nil || entry.Status == "needs-probe" || entry.Status == "deferred" {
		jsonErr(w, fmt.Errorf("command %s not yet implemented (status=%s)", entry.Name, entry.Status), 501)
		return
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}
	envelope, err := entry.builder(req.Args)
	if err != nil {
		jsonErr(w, fmt.Errorf("build envelope: %w", err), 400)
		return
	}

	if globalOpsBridge == nil || !globalOpsBridge.Connected() {
		writeAudit(AuditEvent{
			Action: "gm.execute",
			OK:     false,
			Error:  "OpsBridge disconnected",
			Fields: map[string]any{
				"command": req.Command, "args": req.Args, "envelope": envelope,
			},
		})
		jsonErr(w, fmt.Errorf("OpsBridge disconnected"), 503)
		return
	}

	callCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	reply, err := globalOpsBridge.Call(callCtx, "GMCommand", map[string]any{
		"Envelope":    envelope,
		"Description": "gm.execute " + req.Command,
	})
	if err != nil {
		writeAudit(AuditEvent{
			Action: "gm.execute",
			OK:     false,
			Error:  err.Error(),
			Fields: map[string]any{
				"command": req.Command, "args": req.Args, "envelope": envelope,
			},
		})
		jsonErr(w, fmt.Errorf("opsbridge: %w", err), 500)
		return
	}

	writeAudit(AuditEvent{
		Action: "gm.execute",
		OK:     true,
		Fields: map[string]any{
			"command":  req.Command,
			"args":     req.Args,
			"envelope": envelope,
			"reply":    json.RawMessage(reply),
		},
	})
	jsonOK(w, map[string]any{
		"ok":      true,
		"command": req.Command,
		"reply":   json.RawMessage(reply),
	})
}

// ── Players list (engine-side, cached) ─────────────────────────────────

type GMPlayer struct {
	Name string `json:"name"`
	// PlayerId is the FLS hex string (e.g. "BF0F501CF45BC6EF") — the
	// canonical Funcom Live Services identity. This is the value the
	// engine's UDuneServerCommandSubsystem dispatcher expects in the
	// envelope's "PlayerId" field (see [[dune-gm-target-player-format]]).
	PlayerId string `json:"player_id"`
	// IdType is the FUniqueNetIdRepl type tag ("Fls" on Funcom). Useful
	// for diagnostics + a future build that ships multiple identity
	// providers (e.g. Steam on PC, FLS on console).
	IdType string `json:"id_type,omitempty"`
}

var (
	gmPlayersCacheMu    sync.Mutex
	gmPlayersCache      []GMPlayer
	gmPlayersCacheUntil time.Time
	gmPlayersCacheTTL   = 5 * time.Second
)

func handleGMv2Players(w http.ResponseWriter, r *http.Request) {
	gmPlayersCacheMu.Lock()
	if time.Now().Before(gmPlayersCacheUntil) {
		cached := gmPlayersCache
		gmPlayersCacheMu.Unlock()
		jsonOK(w, cached)
		return
	}
	gmPlayersCacheMu.Unlock()

	if globalOpsBridge == nil || !globalOpsBridge.Connected() {
		jsonErr(w, fmt.Errorf("OpsBridge disconnected"), 503)
		return
	}

	callCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	reply, err := globalOpsBridge.Call(callCtx, "ListPlayers", nil)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// The Lua handler can't return tables — the cppmod handler contract
	// is string-or-nil. We get back a JSON-encoded STRING whose contents
	// are themselves a JSON array. Unmarshal twice.
	//
	// Unmarshal-side tags MUST exactly match the keys the Lua mod emits
	// (PascalCase: "Name", "PlayerId", "IdType"). Go's case-insensitive
	// matcher fails on snake_case-vs-CamelCase pairs ("player_id" vs
	// "playerid"), so the public GMPlayer tags can't double as parse
	// tags. We parse into a transient struct + copy.
	type wireRow struct {
		Name     string `json:"Name"`
		PlayerId string `json:"PlayerId"`
		IdType   string `json:"IdType"`
	}
	var innerJSON string
	if err := json.Unmarshal(reply, &innerJSON); err != nil {
		jsonErr(w, fmt.Errorf("decode outer: %w", err), 500)
		return
	}
	var rows []wireRow
	if err := json.Unmarshal([]byte(innerJSON), &rows); err != nil {
		jsonErr(w, fmt.Errorf("decode inner: %w", err), 500)
		return
	}
	players := make([]GMPlayer, len(rows))
	for i, r := range rows {
		players[i] = GMPlayer{Name: r.Name, PlayerId: r.PlayerId, IdType: r.IdType}
	}

	gmPlayersCacheMu.Lock()
	gmPlayersCache = players
	gmPlayersCacheUntil = time.Now().Add(gmPlayersCacheTTL)
	gmPlayersCacheMu.Unlock()

	jsonOK(w, players)
}
