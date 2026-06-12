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
// OpsBridge op name + the call args the dune-admin Go client will
// hand to `globalOpsBridge.Call(opName, callArgs)`.
//
// For Kind="native":
//   opName  = "GMCommand"
//   callArgs = {"Envelope": "<JSON dispatcher envelope>", "Description": "..."}
//
// For Kind="synth":
//   opName  = command name handled by DuneOpsBridgeMod (e.g. "PrintPos")
//   callArgs = command-specific {key: value, ...} the Lua handler parses
//
// The HTTP execute handler doesn't need to know which is which — it
// just calls OpsBridge with whatever the builder returns.
type GMEntry struct {
	Name    string    `json:"name"`
	Tier    string    `json:"tier"`               // comms|safe|movement|inventory|progression|spawn|player|destructive|console
	Kind    string    `json:"kind"`               // "native" | "synth"
	Status  string    `json:"status"`             // "live" | "needs-probe" | "deferred"
	Notes   string    `json:"notes,omitempty"`
	Params  []GMParam `json:"params"`
	builder func(args map[string]any) (opName string, callArgs map[string]any, err error)
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
		Name: "PrintAllowedCommands", Tier: "safe", Kind: "synth", Status: "live",
		Notes:   "Dumps the engine's registered command list. Synth via DuneOpsBridgeMod registry walk; returns the JSON array as the reply.",
		Params:  nil,
		builder: buildPrintAllowedCommands,
	},
	"PrintPos": {
		Name: "PrintPos", Tier: "safe", Kind: "synth", Status: "live",
		Notes:   "Reads the target player's Pawn position and returns it as \"X=... Y=... Z=...\" (also written to the server log).",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true, Help: "Whose position to print."}},
		builder: buildPrintPos,
	},

	// ── movement ───────────────────────────────────────────────────
	"TeleportToExact": {
		Name: "TeleportToExact", Tier: "movement", Kind: "native", Status: "live",
		Notes: "Native — moves PlayerId to (X,Y,Z). Coordinates are world units; UE5 LWC mode (doubles).",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "X", Type: "float", Required: true},
			{Name: "Y", Type: "float", Required: true},
			{Name: "Z", Type: "float", Required: true},
		},
		builder: buildTeleportToExact,
	},
	"TeleportTo": {
		Name: "TeleportTo", Tier: "movement", Kind: "native", Status: "live",
		Notes: "Like TeleportToExact but also sets character yaw + optional camera rotation. " +
			"All positional fields verified from 1988751 binary RE 2026-06-12.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "X", Type: "float", Required: true},
			{Name: "Y", Type: "float", Required: true},
			{Name: "Z", Type: "float", Required: true},
			{Name: "Yaw", Type: "float", Required: true,
				Help: "Character yaw in degrees (0 = +X, 90 = +Y)."},
			{Name: "CamPitch", Type: "float", Required: false,
				Placeholder: "0", Help: "Optional camera pitch override (default 0)."},
			{Name: "CamYaw", Type: "float", Required: false,
				Placeholder: "0", Help: "Optional camera yaw override (default 0)."},
			{Name: "CamRoll", Type: "float", Required: false,
				Placeholder: "0", Help: "Optional camera roll override (default 0)."},
		},
		builder: buildTeleportTo,
	},
	"TeleportToPlayer": {
		Name: "TeleportToPlayer", Tier: "movement", Kind: "synth", Status: "live",
		Notes: "Reads dest player's coords and dispatches TeleportToExact for the source player. Both args required and must differ.",
		Params: []GMParam{
			{Name: "SourcePlayerId", Type: "player", Required: true, Help: "Who moves."},
			{Name: "DestPlayerId", Type: "player", Required: true, Help: "Whose position they move to."},
		},
		builder: buildTeleportToPlayer,
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
		Notes: "Sandworms aren't actor-classed — they're spline-managed by SandwormSubsystem, " +
			"with SandwormRootComponent + SandwormDisplacementSplineComponent attached to a manager " +
			"singleton. To target a 'live worm' we'd need to walk SandwormSubsystem's internal " +
			"spawn array and pick a spline anchor point. Deferred until the use case justifies " +
			"that RE pass.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},
	"TeleportToVehicleSpawner": {
		Name: "TeleportToVehicleSpawner", Tier: "movement", Kind: "synth", Status: "live",
		Notes: "Finds the closest vehicle spawner (any of BP_VehicleSpawner_C / " +
			"BP_VehicleSpawner_Buggy_C / BP_VehicleSpawner_SandBike_C) to the target PC's " +
			"pawn and teleports them to it.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildSinglePlayerSynth("TeleportToVehicleSpawner"),
	},
	"TeleportToPersonalMarker": {
		Name: "TeleportToPersonalMarker", Tier: "movement", Kind: "synth", Status: "live",
		Notes: "Reads PlayerMapMarkerComponent.m_PersonalMarkerActor on the target PC's " +
			"DunePlayerController and teleports to its world location. Errors out if the player " +
			"hasn't placed a personal marker.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildSinglePlayerSynth("TeleportToPersonalMarker"),
	},
	"PatrolShipTeleportToNearest": {
		Name: "PatrolShipTeleportToNearest", Tier: "movement", Kind: "synth", Status: "live",
		Notes: "Finds the closest BP_PatrolShip_C instance to the target PC's pawn and " +
			"teleports them to it. 10 patrol ships are placed across the Survival map.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildSinglePlayerSynth("PatrolShipTeleportToNearest"),
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
		Name: "SkillsSetUnspentSkillPoints", Tier: "progression", Kind: "native", Status: "live",
		Notes: "Sets the player's unspent skill-point pool. Field name verified from " +
			"1988751 binary RE 2026-06-12: dispatcher reads SkillPoints (int), not Amount.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "SkillPoints", Type: "int", Required: true, Min: 0,
				Placeholder: "10"},
		},
		builder: buildSkillsSetUnspentSkillPoints,
	},
	"SkillsSetModuleLevel": {
		Name: "SkillsSetModuleLevel", Tier: "progression", Kind: "native", Status: "live",
		Notes: "Sets a skill module's level on the target. Module is an FGameplayTag — " +
			"the binary hardcodes 10 Skills.Key.* tags (5 base classes + advanced 'Key1' " +
			"variants). Look up via UGameplayTagsManager; passing an unknown tag silently " +
			"no-ops with 'No Module data found' logged at LogVerbose. RE'd 2026-06-12.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "Module", Type: "module", Required: true,
				Placeholder: "Skills.Key.Mentat",
				Help: "Skill-module gameplay tag. Autocomplete pulls from DT_TrainingModules (145 tags: Abilities, Attributes, Keys, Perks, Spice). Unknown tags silently no-op."},
			{Name: "Level", Type: "int", Required: true, Min: 0,
				Placeholder: "5"},
		},
		builder: buildSkillsSetModuleLevel,
	},

	// ── inventory ──────────────────────────────────────────────────
	"AddItemToInventory": {
		Name: "AddItemToInventory", Tier: "inventory", Kind: "native", Status: "live",
		Notes: "Grants an item to the target player. Template names autocomplete from " +
			"the catalog regenerated via /workspace/dune-pak-tools/dump_templates_v2.py.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "ItemName", Type: "item", Required: true,
				Placeholder: "SalvageMetal"},
			{Name: "Quantity", Type: "int", Required: false,
				Placeholder: "1", Min: 1},
			{Name: "Durability", Type: "float", Required: false,
				Placeholder: "1.0",
				Help: "0.0-1.0 fraction of max durability. Default 1.0 (full)."},
		},
		builder: buildAddItemToInventory,
	},
	"AddBasicInventoryToCharacter": {
		Name: "AddBasicInventoryToCharacter", Tier: "inventory", Kind: "synth", Status: "deferred",
		Notes:  "Wrapper that grants the canonical starter kit via repeated AddItemToInventory calls.",
		Params: []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
	},

	// ── spawn ──────────────────────────────────────────────────────
	"SpawnVehicleAt": {
		Name: "SpawnVehicleAt", Tier: "spawn", Kind: "native", Status: "live",
		Notes: "Spawns ClassName/TemplateName at (X,Y,Z) for PlayerId. Shape RE'd via " +
			"[[dune-gm-command-envelope]]. Rotation defaults to 1 (yaw degrees), TemplateName to " +
			"'Default', Persistent to 1.0, Faction to the binary default.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "ClassName", Type: "string", Required: true,
				Placeholder: "BP_Ornithopter_Scout_Atreides_C",
				Help: "Vehicle class FName (BP_Ornithopter_*_Atreides_C / BP_Sandbike_*_C etc.)."},
			{Name: "X", Type: "float", Required: false, Placeholder: "0"},
			{Name: "Y", Type: "float", Required: false, Placeholder: "0"},
			{Name: "Z", Type: "float", Required: false, Placeholder: "0"},
			{Name: "Rotation", Type: "float", Required: false, Placeholder: "0",
				Help: "Yaw in degrees (default 0)."},
			{Name: "TemplateName", Type: "string", Required: false, Placeholder: "Default"},
			{Name: "Persistent", Type: "float", Required: false, Placeholder: "1.0",
				Help: "1.0 = persisted to DB; 0.0 = transient (despawns)."},
			{Name: "Faction", Type: "string", Required: false, Placeholder: "(binary default)"},
		},
		builder: buildSpawnVehicleAt,
	},
	"SpawnVehicle": {
		Name: "SpawnVehicle", Tier: "spawn", Kind: "synth", Status: "live",
		Notes: "Reads target player's pawn position and dispatches SpawnVehicleAt there. " +
			"Same vehicle parameters as SpawnVehicleAt minus X/Y/Z.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true,
				Help: "Whose pawn position to spawn next to."},
			{Name: "ClassName", Type: "string", Required: true,
				Placeholder: "BP_Ornithopter_Scout_Atreides_C"},
			{Name: "Rotation", Type: "float", Required: false, Placeholder: "0"},
			{Name: "TemplateName", Type: "string", Required: false, Placeholder: "Default"},
			{Name: "Persistent", Type: "float", Required: false, Placeholder: "1.0"},
			{Name: "Faction", Type: "string", Required: false},
		},
		builder: buildSpawnVehicle,
	},

	// ── player ─────────────────────────────────────────────────────
	"KickPlayer": {
		Name: "KickPlayer", Tier: "player", Kind: "native", Status: "live",
		Notes:   "Disconnects the target player. Engine native — registry slot 1; envelope shape is single-PC. Client gets a 'connection lost' on their side.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildSinglePlayerNative("KickPlayer"),
	},

	// ── destructive ────────────────────────────────────────────────
	"ResetProgression": {
		Name: "ResetProgression", Tier: "destructive", Kind: "native", Status: "live",
		Notes:   "Wipes the target player's progression. Irrecoverable. UI requires typing CONFIRM in the destructive-confirm field before Execute enables.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildSinglePlayerNative("ResetProgression"),
	},
	"CleanPlayerInventory": {
		Name: "CleanPlayerInventory", Tier: "destructive", Kind: "native", Status: "live",
		Notes:   "Empties the target player's inventory. Irrecoverable. UI requires typing CONFIRM in the destructive-confirm field before Execute enables.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildSinglePlayerNative("CleanPlayerInventory"),
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

	// ── journey ────────────────────────────────────────────────────
	//
	// All five route through OpsBridge handlers in DuneOpsBridgeMod
	// added 2026-06-12. Path A1 (cheat-mgr UFunctions) for the first
	// four; Path E (dispatcher route with unguarded SQL) for delete.
	// Works on connected players — no relog required. See
	// [[dune-journey-system-re]] for the full RE.
	//
	// StoryNodeId format: <DataAsset>.<UniqueName>[.<UniqueName>...]
	// (e.g. "DA_MQ_FindTheFremen.SecondTest.SecondQuestion"). Autocomplete
	// pulls the canonical list via /api/v1/gm/v2/journey/nodes which
	// walks live JourneyStoryData -> root -> ChildNodes in mini-UE4SS.
	// Completion cascades to descendants — operators can target the
	// questline root and don't need to enumerate leaves.
	"JourneyComplete": {
		Name: "JourneyComplete", Tier: "journey", Kind: "synth", Status: "live",
		Notes: "Marks node + all descendants complete in live state. " +
			"Replicates to client UI; binary's natural save flush persists to DB.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "StoryNodeId", Type: "node", Required: true,
				Placeholder: "DA_MQ_FindTheFremen.SecondTest.SecondQuestion"},
		},
		builder: buildJourneyOp("JourneyComplete"),
	},
	"JourneyReveal": {
		Name: "JourneyReveal", Tier: "journey", Kind: "synth", Status: "live",
		Notes: "Reveals one node (does not reveal ancestors — use JourneyRevealTree to skip ahead).",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "StoryNodeId", Type: "node", Required: true,
				Placeholder: "DA_MQ_FindTheFremen.SecondTest.SecondQuestion"},
		},
		builder: buildJourneyOp("JourneyReveal"),
	},
	"JourneyRevealTree": {
		Name: "JourneyRevealTree", Tier: "journey", Kind: "synth", Status: "live",
		Notes: "Reveals node + every ancestor up to the questline root. " +
			"Use for skip-ahead unlocks when the player is gated behind upstream content.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "StoryNodeId", Type: "node", Required: true,
				Placeholder: "DA_MQ_FindTheFremen.SecondTest.SecondQuestion"},
		},
		builder: buildJourneyOp("JourneyRevealTree"),
	},
	"JourneyReset": {
		Name: "JourneyReset", Tier: "journey", Kind: "synth", Status: "live",
		Notes: "Clears completion on the node + all descendants. " +
			"Reveal state is preserved (the player still sees them as available).",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "StoryNodeId", Type: "node", Required: true,
				Placeholder: "DA_MQ_FindTheFremen.SecondTest.SecondQuestion"},
		},
		builder: buildJourneyOp("JourneyReset"),
	},
	"JourneyDelete": {
		Name: "JourneyDelete", Tier: "journey", Kind: "synth", Status: "live",
		Notes: "Routes through GM dispatcher (JourneyDeleteStoryNode). " +
			"Deletes the DB row outright; on next session start the node returns to its base state. " +
			"The only journey op whose underlying SQL is unguarded against online players.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "StoryNodeId", Type: "node", Required: true,
				Placeholder: "DA_MQ_FindTheFremen.SecondTest.SecondQuestion"},
		},
		builder: buildJourneyOp("JourneyDelete"),
	},

	// ── global ─────────────────────────────────────────────────────
	"UpdateAllWaterFillables": {
		Name: "UpdateAllWaterFillables", Tier: "global", Kind: "native", Status: "live",
		Notes: "Refills every water-fillable actor owned by the target PC up to WaterAmount. " +
			"Pass PlayerId='*' to refill for ALL connected players. Shape RE'd via [[dune-gm-command-envelope]].",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true,
				Help: "Player whose owned water-fillables to refill. Use '*' to target everyone."},
			{Name: "WaterAmount", Type: "int", Required: false, Min: 0,
				Placeholder: "1000000", Help: "Amount to refill to (default 1000000)."},
		},
		builder: buildUpdateAllWaterFillables,
	},

	// ── cheat (stock UE UCheatManager — Fly/Ghost/Walk) ────────────
	//
	// These ride stock UE's UCheatManager (retained in the Funcom 1979201
	// dedicated server binary — symbols ClientCheatFly/Ghost/Walk and
	// CheatFly/bCheatFlying all present). NOT Funcom's m_PlayerCheats
	// ECheatMode system (God/DemiGod/InfiniteAmmo/etc., which lives in
	// DuneCheatEnablerMod). The two systems are independent.
	//
	// Implementation path: dispatch to DuneOpsBridgeMod's cheat_manager_op
	// helper which resolves PC by FLS → pc.CheatManager → calls Fly()/
	// Ghost()/Walk() exec UFunction via reflection. ChangeState +
	// ClientCheat* fan-out replicates to the owning client.
	"Fly": {
		Name: "Fly", Tier: "cheat", Kind: "synth", Status: "live",
		Notes:   "Puts the target player into stock UE flying state. Replicates via ClientCheatFly.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildCheatManagerOp("Fly"),
	},
	"Ghost": {
		Name: "Ghost", Tier: "cheat", Kind: "synth", Status: "live",
		Notes:   "Puts the target player into stock UE noclip/ghost state (collision off). Replicates via ClientCheatGhost.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildCheatManagerOp("Ghost"),
	},
	"Walk": {
		Name: "Walk", Tier: "cheat", Kind: "synth", Status: "live",
		Notes:   "Returns the target player to stock UE walking state — cancels Fly/Ghost. Replicates via ClientCheatWalk.",
		Params:  []GMParam{{Name: "PlayerId", Type: "player", Required: true}},
		builder: buildCheatManagerOp("Walk"),
	},

	// ── console (power tools — confirm-gate required) ──────────────
	"CheatScript": {
		Name: "CheatScript", Tier: "console", Kind: "native", Status: "live",
		Notes: "Runs a NAMED server-side cheat script for the target player. " +
			"ScriptName is a curated identifier (NOT freeform Lua/UE code), looked up by the engine. " +
			"Shape RE'd via [[dune-gm-command-envelope]].",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "ScriptName", Type: "string", Required: true,
				Help: "Name of the cheat script as registered with the engine."},
		},
		builder: buildPlayerScript("CheatScript", "ScriptName"),
	},
	"RunLuaScriptFile": {
		Name: "RunLuaScriptFile", Tier: "console", Kind: "native", Status: "live",
		Notes: "Wraps to 'Auto.RunLuaScriptFile <ScriptName>' then dispatches via the target PC's " +
			"console-exec. Same shape as CheatScript. ScriptName is the file/identifier, not raw code.",
		Params: []GMParam{
			{Name: "PlayerId", Type: "player", Required: true},
			{Name: "ScriptName", Type: "string", Required: true,
				Help: "Script identifier — engine looks up the file."},
		},
		builder: buildPlayerScript("RunLuaScriptFile", "ScriptName"),
	},
	"ServerExec": {
		Name: "ServerExec", Tier: "console", Kind: "native", Status: "live",
		Notes: "Runs a freeform UE console command against the server's UWorld. " +
			"No PlayerId — applies globally. Most powerful and dangerous command in the registry; " +
			"verified live with envelopes like 'slomo 0.5'. Shape RE'd via [[dune-gm-command-envelope]].",
		Params: []GMParam{
			{Name: "Exec", Type: "string", Required: true,
				Placeholder: "slomo 1.0",
				Help: "Raw UE console command. Be sure."},
		},
		builder: buildServerExec,
	},
	"obj": {
		Name: "obj", Tier: "console", Kind: "synth", Status: "deferred",
		Notes:  "Synth — pipes through a live PC's ProcessConsoleExec. Different invocation path than ServerExec; deferred until use case warrants.",
		Params: nil,
	},
}

// ── Builders ───────────────────────────────────────────────────────────

// wrapNative wraps a per-command envelope into the GMCommand call
// args envelope. Centralized so each native builder doesn't repeat
// the {"Envelope": <json>, "Description": <name>} boilerplate.
func wrapNative(command string, envelope any) (string, map[string]any, error) {
	out, err := json.Marshal(envelope)
	if err != nil {
		return "", nil, fmt.Errorf("marshal envelope: %w", err)
	}
	return "GMCommand", map[string]any{
		"Envelope":    string(out),
		"Description": "gm.execute " + command,
	}, nil
}

// coerceInt accepts float64 (from json.Unmarshal of plain numbers),
// int, or a stringified integer (from form input). Returns the int
// and an error tagged with the parameter name on failure.
func coerceInt(name string, v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case string:
		var n int
		if _, err := fmt.Sscanf(x, "%d", &n); err != nil {
			return 0, fmt.Errorf("%s: not a valid integer", name)
		}
		return n, nil
	}
	return 0, fmt.Errorf("%s: missing or wrong type", name)
}

// coerceFloat — same idea for floats.
func coerceFloat(name string, v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case string:
		var n float64
		if _, err := fmt.Sscanf(x, "%g", &n); err != nil {
			return 0, fmt.Errorf("%s: not a valid number", name)
		}
		return n, nil
	}
	return 0, fmt.Errorf("%s: missing or wrong type", name)
}

// coerceString — just typed string assertion with required check.
func coerceString(name string, v any, required bool) (string, error) {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if required && s == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return s, nil
}

// buildAwardXP wraps the AwardXP envelope. Category is required by the
// dispatcher but inert — we hard-code "Combat" since it's also the
// canonical entry in both ESpecializationTrack and EXPEarnArea and the
// engine ignores the string anyway (see [[dune-gm-awardxp]] memory).
func buildAwardXP(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	experience, err := coerceInt("Experience", args["Experience"])
	if err != nil {
		return "", nil, err
	}
	if experience < 1 {
		return "", nil, fmt.Errorf("Experience must be >= 1")
	}
	return wrapNative("AwardXP", []map[string]any{
		{
			"ServerCommand": "AwardXP",
			"PlayerId":      playerId,
			"Category":      "Combat",
			"Experience":    experience,
		},
	})
}

func buildServiceBroadcast(args map[string]any) (string, map[string]any, error) {
	title, _ := coerceString("Title", args["Title"], false)
	body, _ := coerceString("Body", args["Body"], false)
	if title == "" && body == "" {
		return "", nil, fmt.Errorf("at least one of Title or Body must be set")
	}
	duration := 10
	if v, ok := args["DurationSec"]; ok && v != nil && v != "" {
		n, err := coerceInt("DurationSec", v)
		if err != nil {
			return "", nil, err
		}
		duration = n
	}
	if duration < 1 || duration > 600 {
		return "", nil, fmt.Errorf("DurationSec must be 1..600")
	}
	return wrapNative("ServiceBroadcast", []map[string]any{
		{
			"ServerCommand": "ServiceBroadcast",
			"BroadcastType": "Generic",
			"BroadcastPayload": map[string]any{
				"BroadcastDuration": duration,
				"LocalizedText": []map[string]any{
					{"Key": "en", "Title": title, "Body": body},
				},
			},
		},
	})
}

// ── Wave A: comms + movement primitives (Phase 10 D2) ──────────────────

// buildTeleportToExact — native, single-PC, three coords. Verified live
// via REPL earlier this session; envelope is just the four PlayerId
// plus X/Y/Z fields the dispatcher takes.
func buildTeleportToExact(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	x, err := coerceFloat("X", args["X"])
	if err != nil {
		return "", nil, err
	}
	y, err := coerceFloat("Y", args["Y"])
	if err != nil {
		return "", nil, err
	}
	z, err := coerceFloat("Z", args["Z"])
	if err != nil {
		return "", nil, err
	}
	return wrapNative("TeleportToExact", []map[string]any{
		{
			"ServerCommand": "TeleportToExact",
			"PlayerId":      playerId,
			"X":             x,
			"Y":             y,
			"Z":             z,
		},
	})
}

// buildPrintAllowedCommands — synth. Dispatched to DuneOpsBridgeMod's
// PrintAllowedCommands op, which walks the engine's gmregistry and
// returns a JSON array of the registered command names.
func buildPrintAllowedCommands(args map[string]any) (string, map[string]any, error) {
	return "PrintAllowedCommands", map[string]any{}, nil
}

// buildPrintPos — synth. Routes to DuneOpsBridgeMod's PrintPos op
// which looks up the target PC's Pawn position and returns it as
// a "X=... Y=... Z=..." string (also written to the server log).
func buildPrintPos(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	return "PrintPos", map[string]any{"PlayerId": playerId}, nil
}

// buildAddItemToInventory wraps the AddItemToInventory dispatcher envelope.
// Field names confirmed via [[dune-gm-command-envelope]] memory:
// PlayerId, ItemName, Quantity (default 1), Durability (default 1.0).
func buildAddItemToInventory(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	itemName, err := coerceString("ItemName", args["ItemName"], true)
	if err != nil {
		return "", nil, err
	}
	quantity := 1
	if v, ok := args["Quantity"]; ok && v != nil && v != "" {
		n, err := coerceInt("Quantity", v)
		if err != nil {
			return "", nil, err
		}
		quantity = n
	}
	if quantity < 1 {
		return "", nil, fmt.Errorf("Quantity must be >= 1")
	}
	durability := 1.0
	if v, ok := args["Durability"]; ok && v != nil && v != "" {
		f, err := coerceFloat("Durability", v)
		if err != nil {
			return "", nil, err
		}
		durability = f
	}
	if durability < 0.0 || durability > 1.0 {
		return "", nil, fmt.Errorf("Durability must be in [0.0, 1.0]")
	}
	return wrapNative("AddItemToInventory", []map[string]any{
		{
			"ServerCommand": "AddItemToInventory",
			"PlayerId":      playerId,
			"ItemName":      itemName,
			"Quantity":      quantity,
			"Durability":    durability,
		},
	})
}

// buildJourneyOp returns a builder for one of the five Journey synth
// handlers in DuneOpsBridgeMod. All five take the same two args
// (PlayerId + StoryNodeId); the difference is which OpsBridge handler
// name we route to. The Lua handler validates both args server-side
// (and re-checks that the PC is connected); we just type-coerce + pass
// through here.
func buildJourneyOp(handlerName string) func(map[string]any) (string, map[string]any, error) {
	return func(args map[string]any) (string, map[string]any, error) {
		playerId, err := coerceString("PlayerId", args["PlayerId"], true)
		if err != nil {
			return "", nil, err
		}
		nodeId, err := coerceString("StoryNodeId", args["StoryNodeId"], true)
		if err != nil {
			return "", nil, err
		}
		return handlerName, map[string]any{
			"PlayerId":    playerId,
			"StoryNodeId": nodeId,
		}, nil
	}
}

// buildSkillsSetUnspentSkillPoints — verified shape from Ghidra RE
// of handler @ 0xda64690 (1988751 binary). Reads PlayerId (string,
// required) + SkillPoints (int, required) via FUN_0da63930.
func buildSkillsSetUnspentSkillPoints(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	skillPoints, err := coerceInt("SkillPoints", args["SkillPoints"])
	if err != nil {
		return "", nil, err
	}
	if skillPoints < 0 {
		return "", nil, fmt.Errorf("SkillPoints must be >= 0")
	}
	return wrapNative("SkillsSetUnspentSkillPoints", []map[string]any{
		{
			"ServerCommand": "SkillsSetUnspentSkillPoints",
			"PlayerId":      playerId,
			"SkillPoints":   skillPoints,
		},
	})
}

// buildSkillsSetModuleLevel — verified shape from Ghidra RE of
// handler @ 0xda64350. Reads PlayerId (str, req) + Module (str, req,
// via FUN_0da63040) + Level (int, req, via FUN_0da644e0).
func buildSkillsSetModuleLevel(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	module, err := coerceString("Module", args["Module"], true)
	if err != nil {
		return "", nil, err
	}
	level, err := coerceInt("Level", args["Level"])
	if err != nil {
		return "", nil, err
	}
	if level < 0 {
		return "", nil, fmt.Errorf("Level must be >= 0")
	}
	return wrapNative("SkillsSetModuleLevel", []map[string]any{
		{
			"ServerCommand": "SkillsSetModuleLevel",
			"PlayerId":      playerId,
			"Module":        module,
			"Level":         level,
		},
	})
}

// buildTeleportTo — verified shape from Ghidra RE of handler @
// 0xda65230. PlayerId (str, req) + X/Y/Z/Yaw (float, req) +
// CamPitch/CamYaw/CamRoll (float, optional, default 0).
func buildTeleportTo(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	requiredFloat := func(name string) (float64, error) {
		v, ok := args[name]
		if !ok || v == nil || v == "" {
			return 0, fmt.Errorf("%s is required", name)
		}
		return coerceFloat(name, v)
	}
	x, err := requiredFloat("X")
	if err != nil {
		return "", nil, err
	}
	y, err := requiredFloat("Y")
	if err != nil {
		return "", nil, err
	}
	z, err := requiredFloat("Z")
	if err != nil {
		return "", nil, err
	}
	yaw, err := requiredFloat("Yaw")
	if err != nil {
		return "", nil, err
	}
	envelope := map[string]any{
		"ServerCommand": "TeleportTo",
		"PlayerId":      playerId,
		"X":             x,
		"Y":             y,
		"Z":             z,
		"Yaw":           yaw,
	}
	for _, cam := range []string{"CamPitch", "CamYaw", "CamRoll"} {
		if v, ok := args[cam]; ok && v != nil && v != "" {
			f, err := coerceFloat(cam, v)
			if err != nil {
				return "", nil, err
			}
			envelope[cam] = f
		}
	}
	return wrapNative("TeleportTo", []map[string]any{envelope})
}

// buildUpdateAllWaterFillables — PlayerId + optional WaterAmount.
// Per [[dune-gm-command-envelope]], default WaterAmount is 1000000.
func buildUpdateAllWaterFillables(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	envelope := map[string]any{
		"ServerCommand": "UpdateAllWaterFillables",
		"PlayerId":      playerId,
	}
	if v, ok := args["WaterAmount"]; ok && v != nil && v != "" {
		n, err := coerceInt("WaterAmount", v)
		if err != nil {
			return "", nil, err
		}
		if n < 0 {
			return "", nil, fmt.Errorf("WaterAmount must be >= 0")
		}
		envelope["WaterAmount"] = n
	}
	return wrapNative("UpdateAllWaterFillables", []map[string]any{envelope})
}

// buildSpawnVehicleAt — verified shape from [[dune-gm-command-envelope]].
// PlayerId + ClassName required; X/Y/Z/Rotation/TemplateName/Persistent/
// Faction are optional with binary defaults.
func buildSpawnVehicleAt(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	className, err := coerceString("ClassName", args["ClassName"], true)
	if err != nil {
		return "", nil, err
	}
	envelope := map[string]any{
		"ServerCommand": "SpawnVehicleAt",
		"PlayerId":      playerId,
		"ClassName":     className,
	}
	for _, name := range []string{"X", "Y", "Z", "Rotation", "Persistent"} {
		if v, ok := args[name]; ok && v != nil && v != "" {
			f, err := coerceFloat(name, v)
			if err != nil {
				return "", nil, err
			}
			envelope[name] = f
		}
	}
	for _, name := range []string{"TemplateName", "Faction"} {
		if v, ok := args[name]; ok && v != nil && v != "" {
			s, _ := v.(string)
			s = strings.TrimSpace(s)
			if s != "" {
				envelope[name] = s
			}
		}
	}
	return wrapNative("SpawnVehicleAt", []map[string]any{envelope})
}

// buildSpawnVehicle — synth wrapper. Routes to DuneOpsBridgeMod's
// SpawnVehicle handler which reads the target PC's pawn position and
// dispatches SpawnVehicleAt there.
func buildSpawnVehicle(args map[string]any) (string, map[string]any, error) {
	playerId, err := coerceString("PlayerId", args["PlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	className, err := coerceString("ClassName", args["ClassName"], true)
	if err != nil {
		return "", nil, err
	}
	out := map[string]any{
		"PlayerId":  playerId,
		"ClassName": className,
	}
	for _, name := range []string{"Rotation", "Persistent"} {
		if v, ok := args[name]; ok && v != nil && v != "" {
			f, err := coerceFloat(name, v)
			if err != nil {
				return "", nil, err
			}
			out[name] = f
		}
	}
	for _, name := range []string{"TemplateName", "Faction"} {
		if v, ok := args[name]; ok && v != nil && v != "" {
			s, _ := v.(string)
			s = strings.TrimSpace(s)
			if s != "" {
				out[name] = s
			}
		}
	}
	return "SpawnVehicle", out, nil
}

// buildPlayerScript — for CheatScript / RunLuaScriptFile. Both have
// the same envelope shape: PlayerId + ScriptName.
func buildPlayerScript(command, scriptField string) func(map[string]any) (string, map[string]any, error) {
	return func(args map[string]any) (string, map[string]any, error) {
		playerId, err := coerceString("PlayerId", args["PlayerId"], true)
		if err != nil {
			return "", nil, err
		}
		script, err := coerceString(scriptField, args[scriptField], true)
		if err != nil {
			return "", nil, err
		}
		return wrapNative(command, []map[string]any{
			{
				"ServerCommand": command,
				"PlayerId":      playerId,
				scriptField:     script,
			},
		})
	}
}

// buildServerExec — freeform UE console command. No PlayerId; runs on
// the server's UWorld. Verified envelope: {"Exec":"slomo 0.5"}.
func buildServerExec(args map[string]any) (string, map[string]any, error) {
	exec, err := coerceString("Exec", args["Exec"], true)
	if err != nil {
		return "", nil, err
	}
	return wrapNative("ServerExec", []map[string]any{
		{
			"ServerCommand": "ServerExec",
			"Exec":          exec,
		},
	})
}

// buildSinglePlayerSynth returns a builder for any synth OpsBridge
// handler whose envelope is just {"PlayerId":"<fls>"}. Covers the
// actor-query teleport synths (TeleportToVehicleSpawner / patrol
// ship / personal marker) which all need the same input from
// dune-admin and do the work Lua-side.
func buildSinglePlayerSynth(handlerName string) func(map[string]any) (string, map[string]any, error) {
	return func(args map[string]any) (string, map[string]any, error) {
		playerId, err := coerceString("PlayerId", args["PlayerId"], true)
		if err != nil {
			return "", nil, err
		}
		return handlerName, map[string]any{"PlayerId": playerId}, nil
	}
}

// buildSinglePlayerNative returns a builder for any native dispatcher
// command whose envelope shape is just { ServerCommand, PlayerId }.
// Covers KickPlayer / ResetProgression / CleanPlayerInventory and any
// future single-PC native we wire up.
func buildSinglePlayerNative(command string) func(map[string]any) (string, map[string]any, error) {
	return func(args map[string]any) (string, map[string]any, error) {
		playerId, err := coerceString("PlayerId", args["PlayerId"], true)
		if err != nil {
			return "", nil, err
		}
		return wrapNative(command, []map[string]any{
			{
				"ServerCommand": command,
				"PlayerId":      playerId,
			},
		})
	}
}

// buildNoArgNative returns a builder for any native dispatcher command
// that takes no parameters. The envelope still needs the ServerCommand
// field so the dispatcher's switch lands on the right case.
func buildNoArgNative(command string) func(map[string]any) (string, map[string]any, error) {
	return func(_ map[string]any) (string, map[string]any, error) {
		return wrapNative(command, []map[string]any{
			{"ServerCommand": command},
		})
	}
}

// buildCheatManagerOp returns a builder for Fly/Ghost/Walk that routes
// to DuneOpsBridgeMod's cheat_manager_op handler. Each takes a single
// PlayerId and calls the matching stock UE UCheatManager exec UFunction
// (Fly/Ghost/Walk) on the resolved PC's cheat manager.
func buildCheatManagerOp(handlerName string) func(map[string]any) (string, map[string]any, error) {
	return func(args map[string]any) (string, map[string]any, error) {
		playerId, err := coerceString("PlayerId", args["PlayerId"], true)
		if err != nil {
			return "", nil, err
		}
		return handlerName, map[string]any{
			"PlayerId": playerId,
		}, nil
	}
}

// buildTeleportToPlayer — synth. The cppmod's TeleportToPlayer handler
// reads the destination PC's coords and then dispatches TeleportToExact
// for the source PC via Native.Call3 internally — same plumbing as the
// PrintPos coord read but routed back to the dispatcher.
func buildTeleportToPlayer(args map[string]any) (string, map[string]any, error) {
	src, err := coerceString("SourcePlayerId", args["SourcePlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	dst, err := coerceString("DestPlayerId", args["DestPlayerId"], true)
	if err != nil {
		return "", nil, err
	}
	if src == dst {
		return "", nil, fmt.Errorf("SourcePlayerId and DestPlayerId must differ")
	}
	return "TeleportToPlayer", map[string]any{
		"SourcePlayerId": src,
		"DestPlayerId":   dst,
	}, nil
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
	opName, callArgs, err := entry.builder(req.Args)
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
				"command": req.Command, "args": req.Args,
				"op": opName, "call_args": callArgs,
			},
		})
		jsonErr(w, fmt.Errorf("OpsBridge disconnected"), 503)
		return
	}

	callCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	reply, err := globalOpsBridge.Call(callCtx, opName, callArgs)
	if err != nil {
		writeAudit(AuditEvent{
			Action: "gm.execute",
			OK:     false,
			Error:  err.Error(),
			Fields: map[string]any{
				"command": req.Command, "args": req.Args,
				"op": opName, "call_args": callArgs,
			},
		})
		jsonErr(w, fmt.Errorf("opsbridge: %w", err), 500)
		return
	}

	writeAudit(AuditEvent{
		Action: "gm.execute",
		OK:     true,
		Fields: map[string]any{
			"command":   req.Command,
			"args":      req.Args,
			"op":        opName,
			"call_args": callArgs,
			"reply":     json.RawMessage(reply),
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

// ── Journey node-id autocomplete ──────────────────────────────────────
//
// Walks the live JourneyStoryData tree via the OpsBridge ListJourneyNodes
// handler (added 2026-06-12). Design data is static between server
// restarts, so dune-admin caches 10 minutes — the same TTL the Lua-side
// cache uses, so we hit our cache before re-poking the server.

var (
	gmJourneyNodesCacheMu    sync.Mutex
	gmJourneyNodesCache      []string
	gmJourneyNodesCacheUntil time.Time
	gmJourneyNodesCacheTTL   = 10 * time.Minute
)

// handleGMv2Items serves the static item-template catalog embedded from
// /workspace/dune-admin/items.json at build time. The catalog is the
// source-of-truth for the AddItemToInventory's ItemName autocomplete.
// To regenerate after a Funcom build: re-run
// /workspace/dune-pak-tools/dump_templates_v2.py, copy the resulting
// items.json into this directory, and rebuild dune-admin.
func handleGMv2Items(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, itemsCatalogList)
}

// handleGMv2SkillModules serves the embedded list of valid module tags
// for SkillsSetModuleLevel's Module field. Source: DT_TrainingModules
// runtime dump (see skill_modules.txt).
func handleGMv2SkillModules(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, skillModulesList)
}

func handleGMv2JourneyNodes(w http.ResponseWriter, r *http.Request) {
	gmJourneyNodesCacheMu.Lock()
	if time.Now().Before(gmJourneyNodesCacheUntil) {
		cached := gmJourneyNodesCache
		gmJourneyNodesCacheMu.Unlock()
		jsonOK(w, cached)
		return
	}
	gmJourneyNodesCacheMu.Unlock()

	if globalOpsBridge == nil || !globalOpsBridge.Connected() {
		jsonErr(w, fmt.Errorf("OpsBridge disconnected"), 503)
		return
	}

	callCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	reply, err := globalOpsBridge.Call(callCtx, "ListJourneyNodes", nil)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// Lua handler returns a JSON-encoded STRING whose contents are a JSON
	// array of strings. Same double-unmarshal trick as ListPlayers.
	var innerJSON string
	if err := json.Unmarshal(reply, &innerJSON); err != nil {
		jsonErr(w, fmt.Errorf("decode outer: %w", err), 500)
		return
	}
	var nodes []string
	if err := json.Unmarshal([]byte(innerJSON), &nodes); err != nil {
		jsonErr(w, fmt.Errorf("decode inner: %w", err), 500)
		return
	}
	sort.Strings(nodes)

	gmJourneyNodesCacheMu.Lock()
	gmJourneyNodesCache = nodes
	gmJourneyNodesCacheUntil = time.Now().Add(gmJourneyNodesCacheTTL)
	gmJourneyNodesCacheMu.Unlock()

	jsonOK(w, nodes)
}
