package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Phase 8 — Storage tab.
//
// Browses dune.inventories regardless of owner type. The schema lets one
// inventory anchor to any of: actor (player or NPC), dune_exchange,
// container item, or vehicle module. We surface the owner FK columns
// plus best-effort human labels for actor-owned (character name) and
// item-anchored (container template) inventories.
//
// Endpoints:
//   GET  /api/v1/storage                       list with filters
//   GET  /api/v1/storage/{id}                  one inventory + its items
//   POST /api/v1/storage/{id}/give-item        add item; reuses the same
//                                              insert helper as the
//                                              Players tab but logs as
//                                              storage.give-item.

func handleStorageList(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	ownerType := r.URL.Query().Get("type") // "actor" | "exchange" | "item" | "vmodule" | "orphan" | ""
	limit := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
		if limit > 1000 {
			limit = 1000
		}
	}

	args := []any{limit}
	var conds []string

	switch ownerType {
	case "actor":
		conds = append(conds, "i.actor_id IS NOT NULL")
	case "exchange":
		conds = append(conds, "i.exchange_id IS NOT NULL")
	case "item":
		conds = append(conds, "i.item_id IS NOT NULL")
	case "vmodule":
		conds = append(conds, "i.vehicle_module_id IS NOT NULL")
	case "orphan":
		conds = append(conds,
			"i.actor_id IS NULL AND i.exchange_id IS NULL AND i.item_id IS NULL AND i.vehicle_module_id IS NULL")
	}

	if q != "" {
		if id, err := strconv.ParseInt(q, 10, 64); err == nil {
			args = append(args, id)
			conds = append(conds, fmt.Sprintf("i.id = $%d", len(args)))
		} else {
			args = append(args, "%"+q+"%")
			conds = append(conds,
				fmt.Sprintf("(COALESCE(ps.character_name, root_ps.character_name, vehicle_ps.character_name, perm_ps.character_name, pl_ps.character_name) ILIKE $%d OR parent_item.template_id ILIKE $%d OR a.class ILIKE $%d)",
					len(args), len(args), len(args)))
		}
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// Owner labelling: four chains, in priority order.
	//   1. Direct: i.actor_id → actors.owner_account_id → player_state.
	//      Works for player character / vehicle / pawn actors that
	//      actually carry owner_account_id (only player chars do).
	//   2. Item-anchored: inv → item → parent_inv → parent_actor → player.
	//      Surfaces the root owner of a tool's sub-inventory, a weapon's
	//      mod slots, etc. (parent_item.template_id is also exposed so
	//      the sidebar can render 'MiningTool_1h_Standard · 0/8' inside
	//      that player's group.)
	//   3. Vehicle-module: inv → module → vehicle → owner. Vehicle
	//      ownership doesn't flow through actors.owner_account_id, so we
	//      chase the vehicle through permission_actor_rank too.
	//   4. Permission: i.actor_id → permission_actor_rank.player_id →
	//      player controller actor → owner_account_id. Catches vehicles,
	//      placeables (totems, etc.), and any other actor a player owns
	//      via the build/permission system rather than via being the
	//      character actor itself. rank=1 filter keeps it to primary
	//      owners (guild members at lower ranks aren't surfaced).
	sql := fmt.Sprintf(`
		SELECT i.id,
		       i.inventory_type,
		       i.max_item_count,
		       i.max_item_volume,
		       i.actor_id,
		       i.exchange_id,
		       i.item_id,
		       i.vehicle_module_id,
		       ai.component_name_hash,
		       (SELECT COUNT(*) FROM dune.items WHERE inventory_id = i.id) AS item_count,
		       COALESCE(a.class, root_actor.class, vehicle_actor.class)        AS owner_actor_class,
		       COALESCE(ps.character_name, root_ps.character_name, vehicle_ps.character_name, perm_ps.character_name, pl_ps.character_name) AS owner_player_name,
		       parent_item.template_id                                          AS owner_item_template,
		       COALESCE(root_ps.character_name, vehicle_ps.character_name, perm_ps.character_name, pl_ps.character_name) AS root_player_name
		FROM dune.inventories i
		LEFT JOIN dune.actor_inventories ai ON ai.inventory_id = i.id
		LEFT JOIN dune.actors a       ON a.id = i.actor_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		-- Item-anchored chain: inv → item → parent_inv → parent_actor → player.
		LEFT JOIN dune.items parent_item       ON parent_item.id = i.item_id
		LEFT JOIN dune.inventories root_inv    ON root_inv.id = parent_item.inventory_id
		LEFT JOIN dune.actors root_actor       ON root_actor.id = root_inv.actor_id
		LEFT JOIN dune.player_state root_ps    ON root_ps.account_id = root_actor.owner_account_id
		-- Vehicle-module chain: inv → module → vehicle → permission → player.
		LEFT JOIN dune.vehicle_modules vmod    ON vmod.id = i.vehicle_module_id
		LEFT JOIN dune.actors vehicle_actor    ON vehicle_actor.id = vmod.vehicle_id
		LEFT JOIN dune.permission_actor_rank vpar
		       ON vpar.permission_actor_id = vmod.vehicle_id AND vpar.rank = 1
		LEFT JOIN dune.actors vehicle_owner    ON vehicle_owner.id = vpar.player_id
		LEFT JOIN dune.player_state vehicle_ps
		       ON vehicle_ps.account_id = COALESCE(vehicle_actor.owner_account_id, vehicle_owner.owner_account_id)
		-- Permission chain: inv's actor → permission_actor_rank → player.
		-- Covers any actor whose ownership flows through the permission
		-- system rather than owner_account_id (vehicles, placeables, …).
		LEFT JOIN dune.permission_actor_rank par
		       ON par.permission_actor_id = i.actor_id AND par.rank = 1
		LEFT JOIN dune.actors perm_actor       ON perm_actor.id = par.player_id
		LEFT JOIN dune.player_state perm_ps    ON perm_ps.account_id = perm_actor.owner_account_id
		-- Child-placeable chain: inv on a placeable that inherits perms
		-- from its parent totem. The placeable carries owner_entity_id
		-- pointing at the totem's fgl entity; actor_fgl_entities maps
		-- that back to the totem's actor; permission_actor_rank gives
		-- the player. Catches generators, doors, lights, etc. that don't
		-- have their own permission_actor_rank entry.
		LEFT JOIN dune.placeables pl_anchor    ON pl_anchor.id = i.actor_id
		LEFT JOIN dune.actor_fgl_entities pl_afe
		       ON pl_afe.entity_id = pl_anchor.owner_entity_id
		LEFT JOIN dune.permission_actor_rank pl_par
		       ON pl_par.permission_actor_id = pl_afe.actor_id AND pl_par.rank = 1
		LEFT JOIN dune.actors pl_owner_actor   ON pl_owner_actor.id = pl_par.player_id
		LEFT JOIN dune.player_state pl_ps      ON pl_ps.account_id = pl_owner_actor.owner_account_id
		%s
		ORDER BY i.id DESC
		LIMIT $1
	`, where)

	rows, _, err := queryAll(r.Context(), globalDB, sql, args...)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	for _, row := range rows {
		annotateComponentName(row)
	}
	jsonOK(w, rows)
}

// annotateComponentName resolves the component_name_hash field on a row to
// the human-readable UE subobject name (BackpackInventory, PlayerBankInventory,
// etc.) and adds it as component_name. Empty when the hash isn't in the
// known table.
func annotateComponentName(row map[string]any) {
	v, ok := row["component_name_hash"]
	if !ok || v == nil {
		row["component_name"] = ""
		return
	}
	var h int32
	switch x := v.(type) {
	case int32:
		h = x
	case int64:
		h = int32(x)
	case int:
		h = int32(x)
	default:
		row["component_name"] = ""
		return
	}
	row["component_name"] = resolveComponentName(h)
}

func handleStorageGet(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid id"), 400)
		return
	}
	ctx := r.Context()

	invRows, _, err := queryAll(ctx, globalDB, `
		SELECT i.id,
		       i.inventory_type,
		       i.max_item_count,
		       i.max_item_volume,
		       i.actor_id,
		       i.exchange_id,
		       i.item_id,
		       i.vehicle_module_id,
		       ai.component_name_hash,
		       COALESCE(a.class, root_actor.class, vehicle_actor.class)              AS owner_actor_class,
		       COALESCE(ps.character_name, root_ps.character_name, vehicle_ps.character_name, perm_ps.character_name, pl_ps.character_name) AS owner_player_name,
		       parent_item.template_id                                                AS owner_item_template,
		       COALESCE(root_ps.character_name, vehicle_ps.character_name, perm_ps.character_name, pl_ps.character_name) AS root_player_name
		FROM dune.inventories i
		LEFT JOIN dune.actor_inventories ai ON ai.inventory_id = i.id
		LEFT JOIN dune.actors a        ON a.id = i.actor_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		LEFT JOIN dune.items parent_item       ON parent_item.id = i.item_id
		LEFT JOIN dune.inventories root_inv    ON root_inv.id = parent_item.inventory_id
		LEFT JOIN dune.actors root_actor       ON root_actor.id = root_inv.actor_id
		LEFT JOIN dune.player_state root_ps    ON root_ps.account_id = root_actor.owner_account_id
		LEFT JOIN dune.vehicle_modules vmod    ON vmod.id = i.vehicle_module_id
		LEFT JOIN dune.actors vehicle_actor    ON vehicle_actor.id = vmod.vehicle_id
		LEFT JOIN dune.permission_actor_rank vpar
		       ON vpar.permission_actor_id = vmod.vehicle_id AND vpar.rank = 1
		LEFT JOIN dune.actors vehicle_owner    ON vehicle_owner.id = vpar.player_id
		LEFT JOIN dune.player_state vehicle_ps
		       ON vehicle_ps.account_id = COALESCE(vehicle_actor.owner_account_id, vehicle_owner.owner_account_id)
		LEFT JOIN dune.permission_actor_rank par
		       ON par.permission_actor_id = i.actor_id AND par.rank = 1
		LEFT JOIN dune.actors perm_actor       ON perm_actor.id = par.player_id
		LEFT JOIN dune.player_state perm_ps    ON perm_ps.account_id = perm_actor.owner_account_id
		LEFT JOIN dune.placeables pl_anchor    ON pl_anchor.id = i.actor_id
		LEFT JOIN dune.actor_fgl_entities pl_afe
		       ON pl_afe.entity_id = pl_anchor.owner_entity_id
		LEFT JOIN dune.permission_actor_rank pl_par
		       ON pl_par.permission_actor_id = pl_afe.actor_id AND pl_par.rank = 1
		LEFT JOIN dune.actors pl_owner_actor   ON pl_owner_actor.id = pl_par.player_id
		LEFT JOIN dune.player_state pl_ps      ON pl_ps.account_id = pl_owner_actor.owner_account_id
		WHERE i.id = $1
	`, id)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if len(invRows) == 0 {
		jsonErr(w, fmt.Errorf("inventory not found"), 404)
		return
	}
	annotateComponentName(invRows[0])

	items, _, err := queryAll(ctx, globalDB, `
		SELECT id, template_id, stack_size, position_index, quality_level, acquisition_time
		FROM dune.items
		WHERE inventory_id = $1
		ORDER BY position_index, id
	`, id)
	if err != nil {
		items = []map[string]any{}
	}

	jsonOK(w, map[string]any{
		"inventory": invRows[0],
		"items":     items,
	})
}

func handleStorageGiveItem(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid id"), 400)
		return
	}
	var req struct {
		TemplateID string `json:"template_id"`
		StackSize  int    `json:"stack_size"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.TemplateID == "" || req.StackSize <= 0 {
		jsonErr(w, fmt.Errorf("template_id and positive stack_size required"), 400)
		return
	}
	fields := map[string]any{
		"inventory_id": id,
		"template_id":  req.TemplateID,
		"stack_size":   req.StackSize,
	}
	if err := addItemToInventory(r.Context(), id, req.TemplateID, req.StackSize); err != nil {
		auditErr(r, "storage.give-item", fields, err)
		jsonErr(w, err, 500)
		return
	}
	auditOK(r, "storage.give-item", fields)
	jsonOK(w, map[string]any{"ok": true})
}
