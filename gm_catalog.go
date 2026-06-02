package main

import (
	"fmt"
	"net/http"
	"strings"
)

// Phase 5 — GM command catalog + payload preview.
//
// Snapetech catalogued ~28 native GM commands but their notes explicitly
// say "the live UDuneServerCommandSubsystem RabbitMQ payload contract is
// still unverified" — they have probe scripts but no canonical send
// envelope. We mirror their catalog and offer payload preview against the
// candidate envelope modes, but DO NOT publish to RabbitMQ. Hooking up
// execute is a follow-up after someone reverse-engineers the actual
// payload contract.

// GMCommand mirrors the metadata snapetech keeps per command.
type GMCommand struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Status string `json:"status"`
	Syntax string `json:"syntax"`
	Chat   string `json:"chat,omitempty"`
	Notes  string `json:"notes,omitempty"`
}

var gmCommands = []GMCommand{
	{Name: "PrintAllowedCommands", Tier: "safe", Status: "probe-only", Syntax: "PrintAllowedCommands", Notes: "Best command-list probe after the native payload envelope is proven."},
	{Name: "PrintPos", Tier: "safe", Status: "wired-preview", Syntax: "PrintPos", Chat: "&gm pos", Notes: "Safest live route probe; should not mutate player state."},
	{Name: "AddItemToInventory", Tier: "inventory", Status: "wired-preview", Syntax: "AddItemToInventory <player> <template> [count] [quality]", Chat: "&gm item <player> <template> [count] [quality]", Notes: "Native inventory grant path."},
	{Name: "AddBasicInventoryToCharacter", Tier: "inventory", Status: "wired-preview", Syntax: "AddBasicInventoryToCharacter <player>", Chat: "&gm kit <player> [basic]", Notes: "Basic kit wrapper only."},
	{Name: "SpawnVehicle", Tier: "spawn", Status: "wired-preview", Syntax: "SpawnVehicle <template> [args...]", Chat: "&gm vehicle <template> [args...]", Notes: "Vehicle template and argument behavior still need live validation."},
	{Name: "PatrolShipTeleportToNearest", Tier: "movement", Status: "wired-preview", Syntax: "PatrolShipTeleportToNearest", Chat: "&gm patrol"},
	{Name: "TeleportTo", Tier: "movement", Status: "cataloged", Syntax: "TeleportTo <args...>", Notes: "Allowed by native config, but exact argument contract is not mapped yet."},
	{Name: "TeleportToMap", Tier: "movement", Status: "wired-preview", Syntax: "TeleportToMap <map> [dimension]", Chat: "&gm map <map> [dimension]"},
	{Name: "TeleportToExact", Tier: "movement", Status: "wired-preview", Syntax: "TeleportToExact <x> <y> <z>", Chat: "&gm tp <x> <y> <z>"},
	{Name: "TeleportToPlayer", Tier: "movement", Status: "wired-preview", Syntax: "TeleportToPlayer <player>", Chat: "&gm goto <player>"},
	{Name: "TeleportToVehicleSpawner", Tier: "movement", Status: "cataloged", Syntax: "TeleportToVehicleSpawner <args...>"},
	{Name: "TeleportToSandworm", Tier: "movement", Status: "wired-preview", Syntax: "TeleportToSandworm", Chat: "&gm sandworm"},
	{Name: "TeleportToPersonalMarker", Tier: "movement", Status: "wired-preview", Syntax: "TeleportToPersonalMarker", Chat: "&gm marker"},
	{Name: "TravelTo", Tier: "movement", Status: "wired-preview", Syntax: "TravelTo <map> [location]", Chat: "&gm travel <map> [location]"},
	{Name: "TravelToDimension", Tier: "movement", Status: "wired-preview", Syntax: "TravelToDimension <map> <dimension>", Chat: "&gm dimension <map> <dimension>"},
	{Name: "Fly", Tier: "movement", Status: "wired-preview", Syntax: "Fly", Chat: "&gm fly"},
	{Name: "Ghost", Tier: "movement", Status: "wired-preview", Syntax: "Ghost", Chat: "&gm ghost"},
	{Name: "Walk", Tier: "movement", Status: "wired-preview", Syntax: "Walk", Chat: "&gm walk"},
	{Name: "RemoveSessionMember", Tier: "player", Status: "gated-preview", Syntax: "RemoveSessionMember <player>", Chat: "&disconnect <player>", Notes: "Targeted disconnect candidate."},
	{Name: "KickLobbyMember", Tier: "player", Status: "gated-preview", Syntax: "KickLobbyMember <player>", Notes: "Fallback targeted disconnect candidate."},
	{Name: "BattlEyeMegaKick", Tier: "player", Status: "opt-in-only", Syntax: "BattlEyeMegaKick <player>", Notes: "Harsher kick behavior is not verified."},
	{Name: "DestroyTargetVehicle", Tier: "destructive", Status: "blocked", Syntax: "DestroyTargetVehicle", Notes: "Do not expose as casual chat command; needs explicit confirmation/audit."},
	{Name: "DestroyTotem", Tier: "destructive", Status: "blocked", Syntax: "DestroyTotem"},
	{Name: "DestroyPlaceable", Tier: "destructive", Status: "blocked", Syntax: "DestroyPlaceable"},
	{Name: "DestroyEntireBuilding", Tier: "destructive", Status: "blocked", Syntax: "DestroyEntireBuilding"},
	{Name: "DestroyBuildingPiece", Tier: "destructive", Status: "blocked", Syntax: "DestroyBuildingPiece"},
	{Name: "obj", Tier: "console", Status: "cataloged", Syntax: "obj <args...>"},
	{Name: "FGL.ComponentAuditRequested", Tier: "console", Status: "cataloged", Syntax: "FGL.ComponentAuditRequested <args...>"},
}

// candidate envelope modes that snapetech ships. We render the same shape
// JS-side; this list is the authoritative one.
var gmEnvelopeModes = []string{
	"jsonrpc-notify-array",
	"jsonrpc-send-dune-array",
	"jsonrpc-serverexec-array",
	"service-message",
	"send-dune-server-command",
	"server-exec",
	"server-exec-rpc",
	"command-object",
	"rpc-task-array",
	"rpc-task-object",
	"rpc-api-positional-one",
	"rpc-api-positional-two",
	"rpc-api-positional-method-one",
	"rpc-api-positional-method-two",
	"rpc-api-object-one",
	"rpc-api-object-two",
	"dw-notification-message",
	"dw-rpc-task-method-params",
	"dw-rpc-task-api-args",
	"dw-rpc-task-api-arguments",
	"dw-rpc-task-upper-api-args",
	"dw-rpc-task-name-arguments",
	"dw-rpc-task-commandline",
	"dw-rpc-task-command",
	"ue-fstring-array",
	"plain",
	"plain-serverexec",
}

func handleGMCatalog(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{
		"commands": gmCommands,
		"modes":    gmEnvelopeModes,
		"execution": map[string]any{
			"default": "preview-only",
			"reason":  "The live RabbitMQ payload contract for UDuneServerCommandSubsystem is unverified. Preview shows the envelope shape that would be published if execute were wired.",
		},
	})
}

// handleGMPreview renders the envelope shape for the requested mode +
// command. No publish; this exists so the operator can see what would be
// sent before any execute path is wired up.
func handleGMPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CommandText  string `json:"command_text"`
		Mode         string `json:"mode"`
		TargetPlayer string `json:"target_player"`
		AdminPlayer  string `json:"admin_player"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.CommandText == "" {
		jsonErr(w, fmt.Errorf("command_text required"), 400)
		return
	}
	if req.Mode == "" {
		req.Mode = "service-message"
	}
	env, err := buildGMEnvelope(req.Mode, req.CommandText, req.TargetPlayer, req.AdminPlayer)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	jsonOK(w, map[string]any{
		"mode":     req.Mode,
		"envelope": env,
	})
}

// buildGMEnvelope mirrors snapetech's dune_gm_command.py build_envelope().
// Returns the JSON-shaped envelope for the chosen mode.
func buildGMEnvelope(mode, commandText, targetPlayer, adminPlayer string) (any, error) {
	commandText = strings.TrimSpace(commandText)
	if commandText == "" {
		return nil, fmt.Errorf("command text is required")
	}
	parts := strings.SplitN(commandText, " ", 2)
	command := parts[0]
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}
	if targetPlayer == "" {
		targetPlayer = adminPlayer
	}

	switch mode {
	case "jsonrpc-notify-array":
		return map[string]any{"jsonrpc": "2.0", "method": "ServerCommand", "params": []string{commandText}}, nil
	case "jsonrpc-send-dune-array":
		return map[string]any{"jsonrpc": "2.0", "method": "SendDuneServerCommand", "params": []string{commandText, targetPlayer, adminPlayer}}, nil
	case "jsonrpc-serverexec-array":
		return map[string]any{"jsonrpc": "2.0", "method": "ServerExec", "params": []string{targetPlayer, commandText}}, nil
	case "service-message":
		return map[string]any{"Command": "ServerCommand", "CommandText": commandText, "TargetPlayer": targetPlayer, "AdminPlayer": adminPlayer}, nil
	case "send-dune-server-command":
		return map[string]any{"Command": "SendDuneServerCommand", "Params": []string{commandText, targetPlayer, adminPlayer}}, nil
	case "server-exec":
		return map[string]any{"Command": "ServerExec", "CommandText": commandText, "TargetPlayer": targetPlayer, "AdminPlayer": adminPlayer}, nil
	case "server-exec-rpc":
		return map[string]any{"Command": "ServerExecRPC", "TargetPlayer": targetPlayer, "ConsoleCommand": commandText, "AdminPlayer": adminPlayer}, nil
	case "command-object":
		return map[string]any{"Command": command, "Args": args, "TargetPlayer": targetPlayer, "AdminPlayer": adminPlayer}, nil
	case "rpc-task-array":
		return []string{"ServerCommand", commandText, targetPlayer, adminPlayer}, nil
	case "rpc-task-object":
		return map[string]any{"m_Command": "ServerCommand", "m_Args": []string{commandText, targetPlayer, adminPlayer}}, nil
	case "rpc-api-positional-one":
		return []string{commandText}, nil
	case "rpc-api-positional-two":
		return []string{targetPlayer, commandText}, nil
	case "rpc-api-positional-method-one":
		return []any{"ServerCommand", []string{commandText}}, nil
	case "rpc-api-positional-method-two":
		return []any{"ServerExec", []string{targetPlayer, commandText}}, nil
	case "rpc-api-object-one":
		return map[string]any{"Api": "ServerCommand", "Arguments": []string{commandText}}, nil
	case "rpc-api-object-two":
		return map[string]any{"Api": "ServerExec", "Arguments": []string{targetPlayer, commandText}}, nil
	case "dw-notification-message":
		return map[string]any{
			"m_Api":     "ServerCommand",
			"m_Method":  "ServerCommand",
			"m_Payload": []string{commandText},
			"m_Sender":  adminPlayer,
		}, nil
	case "dw-rpc-task-method-params":
		return map[string]any{"method": command, "params": optionalArgs(args)}, nil
	case "dw-rpc-task-api-args":
		return map[string]any{"api": command, "args": optionalArgs(args)}, nil
	case "dw-rpc-task-api-arguments":
		return map[string]any{"api": command, "arguments": optionalArgs(args)}, nil
	case "dw-rpc-task-upper-api-args":
		return map[string]any{"Api": command, "Args": optionalArgs(args)}, nil
	case "dw-rpc-task-name-arguments":
		return map[string]any{"name": command, "arguments": optionalArgs(args)}, nil
	case "dw-rpc-task-commandline":
		return map[string]any{"commandLine": commandText}, nil
	case "dw-rpc-task-command":
		return map[string]any{"command": command, "arguments": optionalArgs(args)}, nil
	case "ue-fstring-array":
		return []string{commandText, targetPlayer, adminPlayer}, nil
	case "plain":
		return commandText, nil
	case "plain-serverexec":
		return strings.TrimSpace(fmt.Sprintf("ServerExec %s %s", targetPlayer, commandText)), nil
	}
	return nil, fmt.Errorf("unknown GM envelope mode: %s", mode)
}

func optionalArgs(args string) []string {
	if args == "" {
		return []string{}
	}
	return []string{args}
}
