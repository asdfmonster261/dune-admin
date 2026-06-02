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
				fmt.Sprintf("(COALESCE(ps.character_name, root_ps.character_name) ILIKE $%d OR parent_item.template_id ILIKE $%d OR a.class ILIKE $%d)",
					len(args), len(args), len(args)))
		}
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// Owner labelling: most inventories anchor to an actor; that actor's
	// owner_account_id points back at dune.accounts, which is what
	// player_state joins through. Item-anchored inventories (a weapon's
	// mod slots, a mining tool's storage, a container item) follow a
	// chain — the item lives inside SOME inventory, which usually
	// belongs to a player actor. We chase one hop up the chain
	// (parent_item.inventory_id → that inventory's actor → that actor's
	// player) and COALESCE so the root player surfaces in the sidebar.
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
		       COALESCE(a.class, root_actor.class)              AS owner_actor_class,
		       COALESCE(ps.character_name, root_ps.character_name) AS owner_player_name,
		       parent_item.template_id                          AS owner_item_template,
		       root_ps.character_name                           AS root_player_name
		FROM dune.inventories i
		LEFT JOIN dune.actor_inventories ai ON ai.inventory_id = i.id
		LEFT JOIN dune.actors a       ON a.id = i.actor_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		LEFT JOIN dune.items parent_item       ON parent_item.id = i.item_id
		LEFT JOIN dune.inventories root_inv    ON root_inv.id = parent_item.inventory_id
		LEFT JOIN dune.actors root_actor       ON root_actor.id = root_inv.actor_id
		LEFT JOIN dune.player_state root_ps    ON root_ps.account_id = root_actor.owner_account_id
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
		       COALESCE(a.class, root_actor.class)              AS owner_actor_class,
		       COALESCE(ps.character_name, root_ps.character_name) AS owner_player_name,
		       parent_item.template_id                          AS owner_item_template,
		       root_ps.character_name                           AS root_player_name
		FROM dune.inventories i
		LEFT JOIN dune.actor_inventories ai ON ai.inventory_id = i.id
		LEFT JOIN dune.actors a        ON a.id = i.actor_id
		LEFT JOIN dune.player_state ps ON ps.account_id = a.owner_account_id
		LEFT JOIN dune.items parent_item       ON parent_item.id = i.item_id
		LEFT JOIN dune.inventories root_inv    ON root_inv.id = parent_item.inventory_id
		LEFT JOIN dune.actors root_actor       ON root_actor.id = root_inv.actor_id
		LEFT JOIN dune.player_state root_ps    ON root_ps.account_id = root_actor.owner_account_id
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
