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
				fmt.Sprintf("(ps.character_name ILIKE $%d OR parent_item.template_id ILIKE $%d OR a.class ILIKE $%d)",
					len(args), len(args), len(args)))
		}
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// Owner labelling: most inventories anchor to an actor; that actor's
	// owner_account_id points back at dune.accounts, which is what
	// player_state joins through. So character_name is available even when
	// the inventory is on a player's pawn, a vehicle they own, or a
	// container they placed — none of which equal player_controller_id.
	sql := fmt.Sprintf(`
		SELECT i.id,
		       i.inventory_type,
		       i.max_item_count,
		       i.max_item_volume,
		       i.actor_id,
		       i.exchange_id,
		       i.item_id,
		       i.vehicle_module_id,
		       (SELECT COUNT(*) FROM dune.items WHERE inventory_id = i.id) AS item_count,
		       a.class                          AS owner_actor_class,
		       ps.character_name                AS owner_player_name,
		       parent_item.template_id          AS owner_item_template
		FROM dune.inventories i
		LEFT JOIN dune.actors a       ON a.id = i.actor_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		LEFT JOIN dune.items parent_item ON parent_item.id = i.item_id
		%s
		ORDER BY i.id DESC
		LIMIT $1
	`, where)

	rows, _, err := queryAll(r.Context(), globalDB, sql, args...)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, rows)
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
		       a.class                 AS owner_actor_class,
		       ps.character_name       AS owner_player_name,
		       parent_item.template_id AS owner_item_template
		FROM dune.inventories i
		LEFT JOIN dune.actors a        ON a.id = i.actor_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		LEFT JOIN dune.items parent_item ON parent_item.id = i.item_id
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
