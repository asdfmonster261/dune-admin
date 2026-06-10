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

	// ── comms ──────────────────────────────────────────────────────
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

	// ── safe (synth wrappers — no side effects) ────────────────────
	"PrintAllowedCommands": {
		Name: "PrintAllowedCommands", Tier: "safe", Kind: "synth", Status: "deferred",
		Notes:  "Dumps the engine's registered command list to the server log. Synth via DuneOpsBridgeMod registry walk.",
		Params: nil,
	},
	"PrintPos": {
		Name: "PrintPos", Tier: "safe", Kind: "synth", Status: "deferred",
		Notes:  "Reads target player's Pawn position and logs it. Synth via reflection.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true, Help: "Whose position to print."}},
	},

	// ── movement ───────────────────────────────────────────────────
	"TeleportToExact": {
		Name: "TeleportToExact", Tier: "movement", Kind: "native", Status: "deferred",
		Notes:  "Native — moves PlayerId to (X,Y,Z). Verified live earlier via REPL; needs Go builder.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "X", Type: "float", Required: true},
			{Name: "Y", Type: "float", Required: true},
			{Name: "Z", Type: "float", Required: true},
		},
	},
	"TeleportTo": {
		Name: "TeleportTo", Tier: "movement", Kind: "native", Status: "needs-probe",
		Notes:  "Arg shape never probed. Needs RE via !gmjson + gmverbose to learn required fields.",
		Params: nil,
	},
	"TeleportToPlayer": {
		Name: "TeleportToPlayer", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Reads dest player's coords and dispatches TeleportToExact for the source player.",
		Params: []GMParam{
			{Name: "SourcePlayerId", Type: "player", Required: true, Help: "Who moves."},
			{Name: "DestPlayerId", Type: "player", Required: true, Help: "Whose position they move to."},
		},
	},
	"TeleportToMap": {
		Name: "TeleportToMap", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Moves player to a named map. Synth via engine travel call; map ID list TBD.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "Map", Type: "string", Required: true, Placeholder: "HaggaBasin"},
		},
	},
	"TravelTo": {
		Name: "TravelTo", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Engine travel. Arg shape TBD.",
		Params: nil,
	},
	"TravelToDimension": {
		Name: "TravelToDimension", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Travel to a specific Deep Desert dimension. Arg shape TBD.",
		Params: nil,
	},
	"TeleportToSandworm": {
		Name: "TeleportToSandworm", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Find a live Sandworm actor, teleport target player to it.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"TeleportToVehicleSpawner": {
		Name: "TeleportToVehicleSpawner", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Find nearest vehicle spawner to target player. Class name TBD.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"TeleportToPersonalMarker": {
		Name: "TeleportToPersonalMarker", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Teleport to the player's own placed marker. Needs marker-state lookup.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"PatrolShipTeleportToNearest": {
		Name: "PatrolShipTeleportToNearest", Tier: "movement", Kind: "synth", Status: "deferred",
		Notes:  "Find nearest patrol ship and teleport target player to it.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},

	// ── progression ────────────────────────────────────────────────
	"AwardXP": {
		Name:   "AwardXP",
		Tier:   "progression",
		Kind:   "native",
		Status: "live",
		Notes:  "Grants Experience to a player. Category is required by the dispatcher but inert (engine accepts any string); we hard-code 'Combat'.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "Experience", Type: "int", Required: true, Min: 1, Placeholder: "100"},
		},
		builder: buildAwardXP,
	},
	"SkillsSetUnspentSkillPoints": {
		Name: "SkillsSetUnspentSkillPoints", Tier: "progression", Kind: "native", Status: "deferred",
		Notes:  "Sets the player's unspent skill-point pool to Amount.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "Amount", Type: "int", Required: true, Min: 0},
		},
	},
	"SkillsSetModuleLevel": {
		Name: "SkillsSetModuleLevel", Tier: "progression", Kind: "native", Status: "deferred",
		Notes:  "Sets a specific skill module's level. Module ID enum TBD.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "Module", Type: "string", Required: true},
			{Name: "Level", Type: "int", Required: true, Min: 0},
		},
	},

	// ── inventory ──────────────────────────────────────────────────
	"AddItemToInventory": {
		Name: "AddItemToInventory", Tier: "inventory", Kind: "native", Status: "deferred",
		Notes:  "Grants an item to the target player. Template names from the inventory_type enum (already RE'd).",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "Template", Type: "string", Required: true},
			{Name: "Count", Type: "int", Required: false, Min: 1, Placeholder: "1"},
			{Name: "Quality", Type: "int", Required: false, Min: 0, Placeholder: "0"},
		},
	},
	"AddBasicInventoryToCharacter": {
		Name: "AddBasicInventoryToCharacter", Tier: "inventory", Kind: "synth", Status: "deferred",
		Notes:  "Wrapper that grants the canonical starter kit via repeated AddItemToInventory calls.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},

	// ── spawn ──────────────────────────────────────────────────────
	"SpawnVehicleAt": {
		Name: "SpawnVehicleAt", Tier: "spawn", Kind: "native", Status: "deferred",
		Notes:  "Native — spawns ClassName/TemplateName at (X,Y,Z) for PlayerId. Vehicle catalog already RE'd this session.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "ClassName", Type: "string", Required: true, Placeholder: "OrnithopterLight"},
			{Name: "TemplateName", Type: "string", Required: true, Placeholder: "T6_Combat"},
			{Name: "X", Type: "float", Required: true},
			{Name: "Y", Type: "float", Required: true},
			{Name: "Z", Type: "float", Required: true},
		},
	},
	"SpawnVehicle": {
		Name: "SpawnVehicle", Tier: "spawn", Kind: "synth", Status: "deferred",
		Notes:  "Synth wrapper — reads target player's coords and dispatches SpawnVehicleAt there.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "ClassName", Type: "string", Required: true, Placeholder: "OrnithopterLight"},
			{Name: "TemplateName", Type: "string", Required: true, Placeholder: "T6_Combat"},
		},
	},

	// ── player ─────────────────────────────────────────────────────
	"KickPlayer": {
		Name: "KickPlayer", Tier: "player", Kind: "native", Status: "deferred",
		Notes:  "Disconnects the target player.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"BattlEyeMegaKick": {
		Name: "BattlEyeMegaKick", Tier: "player", Kind: "synth", Status: "needs-probe",
		Notes:  "Harsher disconnect through BattlEye. Probe-first: not clear if Funcom exposes a BattlEye kick API to UScript.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},

	// ── destructive ────────────────────────────────────────────────
	"ResetProgression": {
		Name: "ResetProgression", Tier: "destructive", Kind: "native", Status: "deferred",
		Notes:  "Wipes the target player's progression. Irrecoverable — D4 will gate with type-to-confirm.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"CleanPlayerInventory": {
		Name: "CleanPlayerInventory", Tier: "destructive", Kind: "native", Status: "deferred",
		Notes:  "Empties the target player's inventory. Irrecoverable.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"DestroyTargetVehicle": {
		Name: "DestroyTargetVehicle", Tier: "destructive", Kind: "synth", Status: "deferred",
		Notes:  "Synth — Destroy* family deferred to D8 entirely (targeting model on dedicated server still TBD; see dune-gm-commands plan).",
		Params: nil,
	},
	"DestroyTotem": {
		Name: "DestroyTotem", Tier: "destructive", Kind: "synth", Status: "deferred",
		Notes:  "Deferred to D8 with the rest of Destroy*.",
		Params: nil,
	},
	"DestroyPlaceable": {
		Name: "DestroyPlaceable", Tier: "destructive", Kind: "synth", Status: "deferred",
		Notes:  "Deferred to D8 with the rest of Destroy*.",
		Params: nil,
	},
	"DestroyEntireBuilding": {
		Name: "DestroyEntireBuilding", Tier: "destructive", Kind: "synth", Status: "deferred",
		Notes:  "Deferred to D8 with the rest of Destroy*.",
		Params: nil,
	},
	"DestroyBuildingPiece": {
		Name: "DestroyBuildingPiece", Tier: "destructive", Kind: "synth", Status: "deferred",
		Notes:  "Deferred to D8 with the rest of Destroy*.",
		Params: nil,
	},

	// ── journey (needs-probe — arg shapes unknown) ─────────────────
	"JourneyCompleteStoryNode": {
		Name: "JourneyCompleteStoryNode", Tier: "journey", Kind: "native", Status: "needs-probe",
		Notes:  "Arg shape unknown. Probe via !gmjson + gmverbose to learn required fields.",
		Params: nil,
	},
	"JourneyRevealStoryNode": {
		Name: "JourneyRevealStoryNode", Tier: "journey", Kind: "native", Status: "needs-probe",
		Params: nil,
	},
	"JourneyResetStoryNode": {
		Name: "JourneyResetStoryNode", Tier: "journey", Kind: "native", Status: "needs-probe",
		Params: nil,
	},
	"JourneyDeleteStoryNode": {
		Name: "JourneyDeleteStoryNode", Tier: "journey", Kind: "native", Status: "needs-probe",
		Params: nil,
	},

	// ── global ─────────────────────────────────────────────────────
	"UpdateAllWaterFillables": {
		Name: "UpdateAllWaterFillables", Tier: "global", Kind: "native", Status: "deferred",
		Notes:  "Engine-wide refresh of water-fillable actors. No player target; no args expected.",
		Params: nil,
	},

	// ── cheat (synth — wrap DuneCheatEnablerMod cheaton) ───────────
	"Fly": {
		Name: "Fly", Tier: "cheat", Kind: "synth", Status: "deferred",
		Notes:  "Toggles fly mode on the target player. Synth via DuneCheatEnablerMod's cheaton (already RE'd).",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"Ghost": {
		Name: "Ghost", Tier: "cheat", Kind: "synth", Status: "deferred",
		Notes:  "Toggles noclip/ghost mode on the target player. Same cheaton wrapper.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"Walk": {
		Name: "Walk", Tier: "cheat", Kind: "synth", Status: "deferred",
		Notes:  "Clears Fly/Ghost mode on the target player.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},

	// ── console (power tools — gated) ──────────────────────────────
	"CheatScript": {
		Name: "CheatScript", Tier: "console", Kind: "native", Status: "deferred",
		Notes:  "Runs a freeform cheat script string. D4 confirm-gate.",
		Params: []GMParam{{Name: "Script", Type: "string", Required: true}},
	},
	"ServerExec": {
		Name: "ServerExec", Tier: "console", Kind: "native", Status: "deferred",
		Notes:  "Runs a freeform UE console command. D4 confirm-gate.",
		Params: []GMParam{{Name: "Command", Type: "string", Required: true}},
	},
	"RunLuaScriptFile": {
		Name: "RunLuaScriptFile", Tier: "console", Kind: "synth", Status: "deferred",
		Notes:  "Synth — loadstrings the passed script body in the mod's hook_L. D4 confirm-gate.",
		Params: []GMParam{{Name: "Script", Type: "string", Required: true}},
	},
	"obj": {
		Name: "obj", Tier: "console", Kind: "synth", Status: "deferred",
		Notes:  "Synth — pipes through a live PC's ProcessConsoleExec.",
		Params: []GMParam{{Name: "Command", Type: "string", Required: true}},
	},
}

// ── Builders ───────────────────────────────────────────────────────────

// buildAwardXP wraps the AwardXP envelope. Category is required by the
// dispatcher but inert — we hard-code "Combat" since it's also the
// canonical entry in both ESpecializationTrack and EXPEarnArea and the
// engine ignores the string anyway (see [[dune-gm-awardxp]] memory).
func buildAwardXP(args map[string]any) (string, error) {
	playerId, _ := args["PlayerId"].(string)
	if strings.TrimSpace(playerId) == "" {
		return "", fmt.Errorf("PlayerId is required")
	}
	var experience int
	switch v := args["Experience"].(type) {
	case float64:
		experience = int(v)
	case int:
		experience = v
	case string:
		// Loose tolerance for stringified ints — UI might send either.
		if _, err := fmt.Sscanf(v, "%d", &experience); err != nil {
			return "", fmt.Errorf("Experience: not a valid integer")
		}
	}
	if experience < 1 {
		return "", fmt.Errorf("Experience must be >= 1")
	}
	envelope := []map[string]any{
		{
			"ServerCommand": "AwardXP",
			"PlayerId":      playerId,
			"Category":      "Combat",
			"Experience":    experience,
		},
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}
	return string(out), nil
}

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
