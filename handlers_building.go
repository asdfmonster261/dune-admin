package main

import (
	"fmt"
	"net/http"
)

// Phase 9 — Building tab.
//
// Surfaces all the player-built / player-stored content that lives outside
// of the live world:
//
//   - backup_vehicles      — saved vehicles (one per account)
//   - recovered_vehicles   — vehicles destroyed but recoverable
//   - base_backups         — saved snapshots of a player's base
//   - building_blueprints  — saved blueprints
//   - building_favorites   — per-account favorite piece types
//
// Plus a small "live builds" rollup of buildings + placeables so the
// operator can see "this player has 12 buildings + 47 placeables placed."
//
// All data is server-wide; the per-row player name comes from joining
// through actors → accounts → player_state.
//
// Endpoint:
//   GET /api/v1/building   one JSON bundle with each section.

func handleBuildingOverview(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	ctx := r.Context()

	out := map[string]any{}

	// Vehicle backups — one row per saved vehicle. account_id → accounts
	// → owner_account_id on vehicles' actor → player_state.character_name.
	vehicleBackups, _, err := queryAll(ctx, globalDB, `
		SELECT bv.account_id,
		       bv.vehicle_id,
		       bv.customization_id,
		       a.class           AS vehicle_class,
		       ps.character_name AS owner_player_name
		FROM dune.backup_vehicles bv
		LEFT JOIN dune.actors a        ON a.id = bv.vehicle_id
		LEFT JOIN dune.player_state ps ON ps.account_id = bv.account_id
		ORDER BY bv.account_id, bv.vehicle_id
	`)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	out["vehicle_backups"] = vehicleBackups

	// Recovered vehicles — destroyed but recoverable, with chassis_durability
	// + time_stored so the operator can see "this got blown up 3 days ago".
	recovered, _, err := queryAll(ctx, globalDB, `
		SELECT rv.account_id,
		       rv.vehicle_id,
		       rv.vehicle_name,
		       rv.customization_id,
		       rv.chassis_durability,
		       rv.time_stored,
		       rv.migrated,
		       a.class           AS vehicle_class,
		       ps.character_name AS owner_player_name
		FROM dune.recovered_vehicles rv
		LEFT JOIN dune.actors a        ON a.id = rv.vehicle_id
		LEFT JOIN dune.player_state ps ON ps.account_id = rv.account_id
		ORDER BY rv.time_stored DESC
	`)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	out["recovered_vehicles"] = recovered

	// Base backups — name + the count of linked actors that compose the base.
	baseBackups, _, err := queryAll(ctx, globalDB, `
		SELECT bb.id,
		       bb.base_backup_name,
		       bb.player_id,
		       ps.character_name AS owner_player_name,
		       (SELECT COUNT(*) FROM dune.base_backup_linked_actors la
		         WHERE la.id = bb.id) AS linked_actor_count
		FROM dune.base_backups bb
		LEFT JOIN dune.actors a        ON a.id = bb.player_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		ORDER BY bb.id DESC
	`)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	out["base_backups"] = baseBackups

	// Building blueprints — map name + counts of instances/placeables/
	// pentashields so the operator sees blueprint "size" at a glance.
	blueprints, _, err := queryAll(ctx, globalDB, `
		SELECT bp.id,
		       bp.item_id,
		       bp.player_id,
		       bp.building_blueprint_map,
		       ps.character_name AS owner_player_name,
		       (SELECT COUNT(*) FROM dune.building_blueprint_instances bi
		         WHERE bi.blueprint_id = bp.id) AS instance_count,
		       (SELECT COUNT(*) FROM dune.building_blueprint_placeables bpl
		         WHERE bpl.blueprint_id = bp.id) AS placeable_count
		FROM dune.building_blueprints bp
		LEFT JOIN dune.actors a        ON a.id = bp.player_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		ORDER BY bp.id DESC
	`)
	if err != nil {
		// Non-fatal: if the blueprint schema differs across builds, fall back.
		blueprints = []map[string]any{}
	}
	out["building_blueprints"] = blueprints

	// Building favorites — flat list keyed to account.
	favorites, _, err := queryAll(ctx, globalDB, `
		SELECT bf.account_id,
		       bf.building_types,
		       ps.character_name AS owner_player_name
		FROM dune.building_favorites bf
		LEFT JOIN dune.player_state ps ON ps.account_id = bf.account_id
		ORDER BY bf.account_id
	`)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	out["building_favorites"] = favorites

	// Live builds rollup — per-player counts of placed stuff. Ownership
	// does NOT flow through actors.owner_account_id for buildings or
	// placeables (always NULL there); it goes through
	// permission_actor_rank → player controller actor → owner_account_id.
	// And a building's owner is its TOTEM entity (one per landclaim),
	// referenced by building_instances.owner_entity_id, which we resolve
	// back to an actor via actor_fgl_entities.
	liveBuilds, _, err := queryAll(ctx, globalDB, `
		WITH player_owned_actors AS (
		    SELECT pa.owner_account_id    AS account_id,
		           par.permission_actor_id AS owned_actor_id
		    FROM dune.permission_actor_rank par
		    JOIN dune.actors pa ON pa.id = par.player_id
		    WHERE pa.owner_account_id IS NOT NULL
		)
		SELECT ps.character_name AS owner_player_name,
		       ps.account_id,
		       (SELECT COUNT(*) FROM dune.placeables pl
		           JOIN player_owned_actors poa ON poa.owned_actor_id = pl.id
		         WHERE poa.account_id = ps.account_id) AS placeable_count,
		       (SELECT COUNT(DISTINCT b.id) FROM dune.buildings b
		           JOIN dune.building_instances bi   ON bi.building_id = b.id
		           JOIN dune.actor_fgl_entities afe  ON afe.entity_id = bi.owner_entity_id
		           JOIN player_owned_actors poa      ON poa.owned_actor_id = afe.actor_id
		         WHERE poa.account_id = ps.account_id) AS building_count,
		       (SELECT COUNT(*) FROM dune.building_instances bi
		           JOIN dune.actor_fgl_entities afe ON afe.entity_id = bi.owner_entity_id
		           JOIN player_owned_actors poa     ON poa.owned_actor_id = afe.actor_id
		         WHERE poa.account_id = ps.account_id) AS piece_count
		FROM dune.player_state ps
		WHERE EXISTS (
		    SELECT 1 FROM player_owned_actors poa WHERE poa.account_id = ps.account_id
		)
		ORDER BY ps.character_name
	`)
	if err != nil {
		liveBuilds = []map[string]any{}
	}
	out["live_builds"] = liveBuilds

	jsonOK(w, out)
}
