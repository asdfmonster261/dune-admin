package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Phase 6 — settings.
//
// Three storage backends:
//   - env  → /host/.env (key=value)
//   - ini  → /host/UserEngine.ini (section/key/value)
//   - role → derived/computed (e.g. a single "effective" knob that touches
//     both env and ini)
//
// All editable settings go through a hand-curated registry below so we
// can:
//   - hide secrets (never expose the value, refuse writes)
//   - typecheck values before writing
//   - tag fields that need a service restart to take effect

type settingsKind string

const (
	kindString settingsKind = "string"
	kindInt    settingsKind = "int"
	kindFloat  settingsKind = "float"
	kindBool   settingsKind = "bool"
	kindEnum   settingsKind = "enum"
)

type settingDef struct {
	ID            string       `json:"id"`               // unique within registry
	Backend       string       `json:"backend"`          // "env" or "ini"
	Group         string       `json:"group"`            // UI section
	Label         string       `json:"label"`            // display name
	Kind          settingsKind `json:"kind"`
	EnumValues    []string     `json:"enum_values,omitempty"`
	Min           *float64     `json:"min,omitempty"`
	Max           *float64     `json:"max,omitempty"`
	Readonly      bool         `json:"readonly"`         // never writable
	Secret        bool         `json:"secret"`           // mask value on read
	NeedsRestart  string       `json:"needs_restart,omitempty"` // hint about which service
	Description   string       `json:"description,omitempty"`

	// Implementation pointers — not serialized.
	envKey      string
	iniSection  string
	iniKey      string
	iniQuoted   bool
}

func float64Ptr(v float64) *float64 { return &v }

// The registry. Anything not in here is unreachable from the API.
var settingsRegistry = []settingDef{
	// .env — World / Display
	{
		ID: "world.name", Backend: "env", envKey: "WORLD_NAME",
		Group: "World", Label: "World name", Kind: kindString,
		Description: "Used by the gateway as the title sent to FLS.",
	},
	{
		ID: "world.region", Backend: "env", envKey: "WORLD_REGION",
		Group: "World", Label: "Region", Kind: kindEnum, Readonly: true,
		EnumValues: []string{"Asia", "Europe", "North America", "Oceania", "South America"},
		Description: "Set at install time. Changing post-install would re-register the battlegroup with FLS as a new server.",
	},
	{
		ID: "world.host_ip", Backend: "env", envKey: "HOST_IP",
		Group: "World", Label: "Public/LAN host IP", Kind: kindString,
		NeedsRestart: "all",
		Description: "Address clients use to reach the game servers.",
	},
	{
		ID: "world.display_name", Backend: "env", envKey: "BROWSER_DISPLAY_NAME",
		Group: "World", Label: "Browser display name", Kind: kindString,
		NeedsRestart: "game-servers",
		Description: "Player-facing sietch name. Also baked into UserEngine.ini → Bgd.ServerDisplayName at install; remember to sync that one too.",
	},

	// Hidden secrets — readable as masked, never writable through the API.
	{ID: "secret.fls_token", Backend: "env", envKey: "FLS_TOKEN", Group: "_secrets", Label: "FLS token", Kind: kindString, Readonly: true, Secret: true},
	{ID: "secret.fls_api_key", Backend: "env", envKey: "FLS_API_KEY", Group: "_secrets", Label: "FLS API key", Kind: kindString, Readonly: true, Secret: true},
	{ID: "secret.postgres_super", Backend: "env", envKey: "POSTGRES_SUPER_PASS", Group: "_secrets", Label: "Postgres superuser pass", Kind: kindString, Readonly: true, Secret: true},
	{ID: "secret.postgres_dune", Backend: "env", envKey: "POSTGRES_DUNE_PASS", Group: "_secrets", Label: "Postgres dune pass", Kind: kindString, Readonly: true, Secret: true},
	{ID: "secret.rmq_token", Backend: "env", envKey: "RMQ_HTTP_TOKEN_AUTH_SECRET", Group: "_secrets", Label: "RMQ HTTP token secret", Kind: kindString, Readonly: true, Secret: true},
	{ID: "secret.world_unique_name", Backend: "env", envKey: "WORLD_UNIQUE_NAME", Group: "_secrets", Label: "WORLD_UNIQUE_NAME (battlegroup id)", Kind: kindString, Readonly: true},

	// .env — Backup sidecar
	{
		ID: "backup.interval_seconds", Backend: "env", envKey: "BACKUP_INTERVAL_SECONDS",
		Group: "Backup", Label: "Backup interval (seconds)", Kind: kindInt,
		Min: float64Ptr(60), Max: float64Ptr(86400 * 7),
		NeedsRestart: "postgres-backup",
		Description: "How often the postgres-backup sidecar takes a pg_dump.",
	},
	{
		ID: "backup.retention", Backend: "env", envKey: "BACKUP_RETENTION",
		Group: "Backup", Label: "Backup retention (count)", Kind: kindInt,
		Min: float64Ptr(1), Max: float64Ptr(720),
		NeedsRestart: "postgres-backup",
		Description: "How many snapshots to keep on disk before the rolling pruner kicks in.",
	},

	// UserEngine.ini — Console variables
	{
		ID: "gameplay.display_name", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Bgd.ServerDisplayName", iniQuoted: true,
		Group: "Display", Label: "Bgd.ServerDisplayName", Kind: kindString,
		NeedsRestart: "game-servers",
		Description: "Per-server display name reported via FLS. Should match BROWSER_DISPLAY_NAME above.",
	},
	{
		ID: "gameplay.login_password", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Bgd.ServerLoginPassword", iniQuoted: true,
		Group: "Display", Label: "Server login password", Kind: kindString,
		NeedsRestart: "game-servers",
		Description: "Empty = no password. Players must enter this to connect.",
	},

	// Gameplay tuning
	{
		ID: "tune.mining_multiplier", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Dune.GlobalMiningOutputMultiplier",
		Group: "Gameplay", Label: "Global mining output multiplier", Kind: kindFloat,
		Min: float64Ptr(0), Max: float64Ptr(100),
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.vehicle_mining_multiplier", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Dune.GlobalVehicleMiningOutputMultiplier",
		Group: "Gameplay", Label: "Global vehicle mining multiplier", Kind: kindFloat,
		Min: float64Ptr(0), Max: float64Ptr(100),
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.pvp_resource_multiplier", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "SecurityZones.PvpResourceMultiplier",
		Group: "Gameplay", Label: "PvP zone resource multiplier", Kind: kindFloat,
		Min: float64Ptr(0), Max: float64Ptr(100),
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.vehicle_durability_mult", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "dw.VehicleDurabilityDamageMultiplier",
		Group: "Gameplay", Label: "Vehicle durability damage multiplier", Kind: kindFloat,
		Min: float64Ptr(0), Max: float64Ptr(10),
		NeedsRestart: "game-servers",
	},

	// Sandstorm / Sandworm
	{
		ID: "tune.sandstorm_enabled", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Sandstorm.Enabled",
		Group: "Environment", Label: "Sandstorms enabled", Kind: kindInt,
		EnumValues: []string{"0", "1"}, Min: float64Ptr(0), Max: float64Ptr(1),
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.treasure_enabled", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Sandstorm.Treasure.Enabled",
		Group: "Environment", Label: "Sandstorm treasure spawns", Kind: kindInt,
		EnumValues: []string{"0", "1"}, Min: float64Ptr(0), Max: float64Ptr(1),
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.sandworm_enabled", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "sandworm.dune.Enabled",
		Group: "Environment", Label: "Sandworm spawns", Kind: kindInt,
		EnumValues: []string{"0", "1"}, Min: float64Ptr(0), Max: float64Ptr(1),
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.sandworm_danger_zones", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Sandworm.SandwormDangerZonesEnabled",
		Group: "Environment", Label: "Sandworm danger zones", Kind: kindBool,
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.vehicle_sandworm_collision", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Vehicle.SandwormCollisionInteraction",
		Group: "Environment", Label: "Vehicle/sandworm collisions", Kind: kindBool,
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.sandworm_inv_secs_exit", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Vehicle.SandwormInvulnerabilitySecondsOnExit",
		Group: "Environment", Label: "Sandworm invulnerability (secs) on exit", Kind: kindFloat,
		Min: float64Ptr(0), Max: float64Ptr(86400),
		NeedsRestart: "game-servers",
	},
	{
		ID: "tune.sandworm_inv_secs_restart", Backend: "ini",
		iniSection: "ConsoleVariables", iniKey: "Vehicle.SandwormInvulnerabilitySecondsOnServerRestart",
		Group: "Environment", Label: "Sandworm invulnerability (secs) on server restart", Kind: kindFloat,
		Min: float64Ptr(0), Max: float64Ptr(86400),
		NeedsRestart: "game-servers",
	},
}

type settingValueOut struct {
	Def   settingDef `json:"def"`
	Value any        `json:"value"`
}

func hostEnvPath() string {
	return envOr("HOST_ENV_FILE", "/host/.env")
}
func hostIniPath() string {
	return envOr("HOST_USERENGINE_INI", "/host/UserEngine.ini")
}

func handleSettingsList(w http.ResponseWriter, r *http.Request) {
	envF, errEnv := readEnvFile(hostEnvPath())
	iniF, errIni := readINIFile(hostIniPath())

	out := make([]settingValueOut, 0, len(settingsRegistry))
	for _, def := range settingsRegistry {
		var raw string
		var ok bool
		switch def.Backend {
		case "env":
			if envF != nil {
				raw, ok = envF.get(def.envKey)
			}
		case "ini":
			if iniF != nil {
				raw, ok = iniF.get(def.iniSection, def.iniKey)
				if def.iniQuoted && ok {
					raw = strings.Trim(raw, "\"")
				}
			}
		}
		val := any(raw)
		if !ok {
			val = nil
		} else if def.Secret {
			val = maskSecret(raw)
		} else if def.Kind == kindInt {
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				val = n
			}
		} else if def.Kind == kindFloat {
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				val = f
			}
		} else if def.Kind == kindBool {
			val = strings.EqualFold(raw, "true")
		}
		out = append(out, settingValueOut{Def: def, Value: val})
	}
	resp := map[string]any{"settings": out}
	if errEnv != nil {
		resp["env_error"] = errEnv.Error()
	}
	if errIni != nil {
		resp["ini_error"] = errIni.Error()
	}
	jsonOK(w, resp)
}

func handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Updates map[string]string `json:"updates"` // key = setting ID, value = new raw text
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if len(req.Updates) == 0 {
		jsonErr(w, fmt.Errorf("updates required"), 400)
		return
	}

	// Cache loaded files so we write each at most once.
	var envF *envFile
	var iniF *iniFile
	var errEnv, errIni error
	loadEnv := func() {
		if envF == nil && errEnv == nil {
			envF, errEnv = readEnvFile(hostEnvPath())
		}
	}
	loadIni := func() {
		if iniF == nil && errIni == nil {
			iniF, errIni = readINIFile(hostIniPath())
		}
	}

	applied := make([]string, 0, len(req.Updates))
	for id, raw := range req.Updates {
		def, ok := findSettingDef(id)
		if !ok {
			auditErr(r, "settings.save", map[string]any{"id": id}, fmt.Errorf("unknown setting"))
			jsonErr(w, fmt.Errorf("unknown setting: %s", id), 400)
			return
		}
		if def.Readonly || def.Secret {
			auditErr(r, "settings.save", map[string]any{"id": id}, fmt.Errorf("not writable"))
			jsonErr(w, fmt.Errorf("not writable: %s", id), 400)
			return
		}
		if err := validateSettingValue(def, raw); err != nil {
			auditErr(r, "settings.save", map[string]any{"id": id, "value": raw}, err)
			jsonErr(w, fmt.Errorf("%s: %w", id, err), 400)
			return
		}
		switch def.Backend {
		case "env":
			loadEnv()
			if errEnv != nil {
				jsonErr(w, errEnv, 500)
				return
			}
			if !envF.set(def.envKey, raw) {
				jsonErr(w, fmt.Errorf("env key not found: %s", def.envKey), 500)
				return
			}
		case "ini":
			loadIni()
			if errIni != nil {
				jsonErr(w, errIni, 500)
				return
			}
			value := raw
			if def.iniQuoted {
				value = fmt.Sprintf("\"%s\"", raw)
			}
			if !iniF.set(def.iniSection, def.iniKey, value) {
				jsonErr(w, fmt.Errorf("ini key not found: [%s] %s", def.iniSection, def.iniKey), 500)
				return
			}
		}
		applied = append(applied, id)
	}

	if envF != nil {
		if err := envF.save(); err != nil {
			jsonErr(w, fmt.Errorf("save .env: %w", err), 500)
			return
		}
	}
	if iniF != nil {
		if err := iniF.save(); err != nil {
			jsonErr(w, fmt.Errorf("save .ini: %w", err), 500)
			return
		}
	}
	auditOK(r, "settings.save", map[string]any{"ids": applied})
	jsonOK(w, map[string]any{"applied": applied})
}

func findSettingDef(id string) (settingDef, bool) {
	for _, def := range settingsRegistry {
		if def.ID == id {
			return def, true
		}
	}
	return settingDef{}, false
}

func validateSettingValue(def settingDef, raw string) error {
	switch def.Kind {
	case kindInt:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("not an integer")
		}
		if def.Min != nil && float64(n) < *def.Min {
			return fmt.Errorf("below minimum %v", *def.Min)
		}
		if def.Max != nil && float64(n) > *def.Max {
			return fmt.Errorf("above maximum %v", *def.Max)
		}
	case kindFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("not a number")
		}
		if def.Min != nil && f < *def.Min {
			return fmt.Errorf("below minimum %v", *def.Min)
		}
		if def.Max != nil && f > *def.Max {
			return fmt.Errorf("above maximum %v", *def.Max)
		}
	case kindBool:
		switch strings.ToLower(raw) {
		case "true", "false":
		default:
			return fmt.Errorf("not a boolean (true|false)")
		}
	case kindEnum:
		for _, v := range def.EnumValues {
			if v == raw {
				return nil
			}
		}
		return fmt.Errorf("not in allowed values")
	}
	return nil
}

func maskSecret(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("•", len(s))
	}
	return s[:2] + strings.Repeat("•", len(s)-4) + s[len(s)-2:]
}

// stubs in case future code wants them
var _ = os.Stat
