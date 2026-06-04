package main

import (
	"fmt"
	"net/http"
	"strings"
)

// Phase 10 — Map tab.
//
// Bundles five overlay layers shared between the Hagga and Deep Desert
// map views:
//
//   - players    live PlayerCharacter pawns (online + offline)
//   - deaths     each player's most-recent death location (no death_time
//                column exists, so the marker means "last died here",
//                not "died at this timestamp")
//   - buildings  totem land claims, with owner name resolved through
//                permission_actor_rank rank=1 — same chain we use in
//                the Storage tab for placeable ownership
//   - vehicles   parked / abandoned vehicles. Owner via the same
//                permission_actor_rank chain; the short label comes
//                from the BP_*_C class name stripped of its prefix.
//   - respawns   actor-backed respawn locators (Vehicle/BaseTotem/
//                RespawnBeacon).
//
// Endpoint:
//   GET /api/v1/map/players              -> HaggaBasin (default)
//   GET /api/v1/map/players?map=DeepDesert_1 -> DD partition
//
// Map naming: every `*.map` column in postgres uses the bare map name
// ("HaggaBasin", "DeepDesert", "Arrakeen", "HarkoVillage") — partition
// index lives in a separate `partition_id` column. The query param
// accepts either form ("DeepDesert" or "DeepDesert_1") because callers
// often have the partition name handy; we strip a trailing _<digits> so
// it normalises to the bare name before filtering.
//
// DD caveat: actors are ephemeral — rows in `dune.actors` only exist
// while the DD partition is loaded. With the DD container down, all four
// actor-derived layers return zero rows. Respawn locators persist
// regardless.

func handleMapPlayers(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	ctx := r.Context()

	// Strip a trailing "_<digits>" so callers can pass "DeepDesert_1"
	// (the partition name) and get the bare "DeepDesert" used by every
	// *.map column. "HaggaBasin" has no suffix and passes through.
	mapName := r.URL.Query().Get("map")
	if mapName == "" {
		mapName = "HaggaBasin"
	}
	if i := strings.LastIndex(mapName, "_"); i > 0 {
		suffix := mapName[i+1:]
		if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
			mapName = mapName[:i]
		}
	}

	// Pull every player whose PlayerCharacter pawn is currently on the Hagga
	// Basin partition. We surface online_status as well so the UI can dim
	// offline characters — useful when chasing "where did so-and-so log out".
	// `dune.actor_state.state` flips off `Default` whenever the actor is
	// physically not in the world — VehicleBackup / BaseBackup / Travel /
	// AbortedAuthorityTransfer / VehicleRecovery. The row stays in
	// `dune.actors` (with stale coords) until the backup is restored.
	// Filtering to `Default` (or no state row) is how we tell "actually
	// present right now" from "stored somewhere."
	players, _, err := queryAll(ctx, globalDB, `
		SELECT a.id                                  AS actor_id,
		       a.partition_id,
		       ps.account_id,
		       ps.player_state_id,
		       ps.character_name,
		       ps.online_status::text               AS online_status,
		       ps.last_login_time,
		       ((a.transform).location).x           AS world_x,
		       ((a.transform).location).y           AS world_y,
		       ((a.transform).location).z           AS world_z
		FROM dune.actors a
		JOIN dune.player_state ps
		  ON ps.player_pawn_id = a.id
		LEFT JOIN dune.actor_state ast ON ast.actor_id = a.id
		WHERE a.class = '/Game/Dune/Characters/Player/BP_DunePlayerCharacter.BP_DunePlayerCharacter_C'
		  AND a.map  = $1
		  AND (ast.state IS NULL OR ast.state = 'Default')
		ORDER BY ps.character_name
	`, mapName)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// Deaths — one row per character whose last-known death was in Hagga.
	// player_state.death_location is a `deathlocation` composite, accessed
	// via parenthesised dotted notation. No death_time column exists, so
	// the dot just shows "the spot where this player was last killed";
	// when the player respawns in a NEW location, this column may not
	// update until they die again. Good enough for "go retrieve so-and-so's
	// loot" workflows.
	deaths, _, err := queryAll(ctx, globalDB, `
		SELECT ps.account_id,
		       ps.character_name,
		       ((ps.death_location).location).x   AS world_x,
		       ((ps.death_location).location).y   AS world_y,
		       ((ps.death_location).location).z   AS world_z
		FROM dune.player_state ps
		WHERE (ps.death_location).map = $1
		ORDER BY ps.character_name
	`, mapName)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// Buildings — each TotemSmall actor is one landclaim. Owner resolution
	// follows the same permission_actor_rank rank=1 chain used in the
	// Storage tab; the totem itself never has owner_account_id, only the
	// player controller actor at the end of the chain does.
	buildings, _, err := queryAll(ctx, globalDB, `
		SELECT a.id                                AS actor_id,
		       a.partition_id,
		       ((a.transform).location).x         AS world_x,
		       ((a.transform).location).y         AS world_y,
		       ((a.transform).location).z         AS world_z,
		       ps.character_name                  AS owner_player_name,
		       owner_actor.owner_account_id       AS owner_account_id
		FROM dune.actors a
		LEFT JOIN dune.permission_actor_rank par
		       ON par.permission_actor_id = a.id AND par.rank = 1
		LEFT JOIN dune.actors owner_actor
		       ON owner_actor.id = par.player_id
		LEFT JOIN dune.player_state ps
		       ON ps.account_id = owner_actor.owner_account_id
		LEFT JOIN dune.actor_state ast ON ast.actor_id = a.id
		WHERE a.class = '/Game/Dune/Systems/Building/Pieces/BP_TotemSmall.BP_TotemSmall_C'
		  AND a.map = $1
		  AND (ast.state IS NULL OR ast.state = 'Default')
		ORDER BY ps.character_name NULLS LAST, a.id
	`, mapName)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// Vehicles — parked / abandoned vehicles. We surface the bare BP_*_C
	// class name as `vehicle_type` so the UI tooltip reads "Light
	// Ornithopter (CHOAM)" without having to ship a lookup table; the
	// frontend can prettify further if needed. Owner resolution mirrors
	// the buildings query.
	vehicles, _, err := queryAll(ctx, globalDB, `
		SELECT a.id                                AS actor_id,
		       a.partition_id,
		       a.class,
		       -- regexp_replace yanks just the BP_*_C leaf from the
		       -- full asset path so the frontend doesn't have to.
		       regexp_replace(a.class, '^.*/([^./]+)\.[^_]+_C$', '\1') AS vehicle_type,
		       ((a.transform).location).x         AS world_x,
		       ((a.transform).location).y         AS world_y,
		       ((a.transform).location).z         AS world_z,
		       ps.character_name                  AS owner_player_name,
		       owner_actor.owner_account_id       AS owner_account_id
		FROM dune.actors a
		LEFT JOIN dune.permission_actor_rank par
		       ON par.permission_actor_id = a.id AND par.rank = 1
		LEFT JOIN dune.actors owner_actor
		       ON owner_actor.id = par.player_id
		LEFT JOIN dune.player_state ps
		       ON ps.account_id = owner_actor.owner_account_id
		LEFT JOIN dune.actor_state ast ON ast.actor_id = a.id
		WHERE a.map = $1
		  AND a.class LIKE '/Game/Dune/Systems/Vehicles/%'
		  AND (ast.state IS NULL OR ast.state = 'Default')
		ORDER BY ps.character_name NULLS LAST, a.id
	`, mapName)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// Respawn locations — only the actor-backed groups (Vehicle, BaseTotem,
	// RespawnBeacon) have positions we can resolve. PlayerStart and
	// CheckpointSafe carry a `locator_name` that points at static world
	// markers baked into the level; their `locator_transform` is NULL and
	// we'd need pak extraction to surface them. Skip those for v1.
	// Group rows by actor so a tradepost used by N players renders as one
	// marker with N owners in the tooltip.
	respawns, _, err := queryAll(ctx, globalDB, `
		SELECT prl.locator_actor_id                AS actor_id,
		       MIN(prl.group)                      AS group_type,
		       ((a.transform).location).x          AS world_x,
		       ((a.transform).location).y          AS world_y,
		       array_agg(ps.character_name ORDER BY ps.character_name) FILTER (WHERE ps.character_name IS NOT NULL) AS owners
		FROM dune.player_respawn_locations prl
		JOIN dune.actors a ON a.id = prl.locator_actor_id
		LEFT JOIN dune.actor_state ast ON ast.actor_id = a.id
		LEFT JOIN dune.player_state ps ON ps.account_id = prl.account_id
		WHERE prl.map = $1
		  AND prl.locator_actor_id IS NOT NULL
		  AND (ast.state IS NULL OR ast.state = 'Default')
		GROUP BY prl.locator_actor_id, ((a.transform).location).x, ((a.transform).location).y
		ORDER BY prl.locator_actor_id
	`, mapName)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	jsonOK(w, map[string]any{
		"players":   players,
		"deaths":    deaths,
		"buildings": buildings,
		"vehicles":  vehicles,
		"respawns":  respawns,
		// Storm state comes from the docker-log tailer (storm_tailer.go),
		// not the DB — the game-server emits start/end coords + lifetime
		// per spawn at VeryVerbose. The frontend interpolates the current
		// wall position from spawn_time + lifetime.
		"storms": GetStormSnapshot(),
		// Affine mapping constants the frontend uses to project (world_x,
		// world_y) onto the 8192² Hagga texture. Initial guess: Hagga is
		// roughly 24 km × 24 km centered on (0,0); we'll calibrate against
		// known landmarks after seeing v1 in the browser. Exposed here so
		// the frontend doesn't need to hard-code them and we can iterate
		// without a UI rebuild.
		// Affine mapping. Hagga calibrated against the gaming.tools POI
		// dump: 5,899 landmarks span world X (-435k..+342k) and Y
		// (-437k..+335k) in centimetres. DD bounds come from gaming.tools'
		// tile-pyramid gameBounds (asymmetric -1270000..+1168400 both
		// axes). Y is NOT flipped on either — UE5's +Y is south and the
		// base textures have +Y going down. The DD frontend keeps its
		// own DD_PROJECTION constant for the static POI overlay; we send
		// the matching one here so the response stays self-describing.
		"projection": mapProjection(mapName),
	})
}

func mapProjection(partition string) map[string]any {
	if partition == "HaggaBasin" {
		return map[string]any{
			"world_x_min":  -440000.0,
			"world_x_max":   360000.0,
			"world_y_min":  -440000.0,
			"world_y_max":   360000.0,
			"texture_size": 8192,
			"flip_y":       false,
		}
	}
	// DD (and forward-compat default for any other partition).
	return map[string]any{
		"world_x_min": -1270000.0,
		"world_x_max":  1168400.0,
		"world_y_min": -1270000.0,
		"world_y_max":  1168400.0,
		"texture_size": 8192,
		"flip_y":       false,
	}
}
