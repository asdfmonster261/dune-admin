package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Phase 3 — Players tab.
//
// Endpoints:
//   GET  /api/v1/players                  — paginated list with search
//   GET  /api/v1/players/{id}             — one character's detail bundle
//   POST /api/v1/players/give-item        — add an item stack to an inventory
//   POST /api/v1/players/give-currency    — set a virtual currency balance
//   POST /api/v1/players/award-xp         — bump a specialization track XP
//
// Player identity in the DB:
//   dune.accounts.id           account-level (one per Steam/Funcom login)
//   dune.player_state.player_state_id   per-character
//   dune.player_state.account_id        fk to accounts
//   dune.player_state.player_controller_id  controller actor for the live session

func handleListPlayers(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
		if limit > 1000 {
			limit = 1000
		}
	}

	args := []any{limit}
	var where string
	if q != "" {
		args = append(args, "%"+q+"%")
		where = "WHERE ps.character_name ILIKE $2 OR a.platform_name ILIKE $2"
	}
	sql := fmt.Sprintf(`
		SELECT ps.player_state_id    AS id,
		       ps.account_id         AS account_id,
		       ps.character_name     AS name,
		       ps.online_status::text AS online_status,
		       ps.life_state::text   AS life_state,
		       ps.server_id          AS server_id,
		       ps.last_login_time    AS last_login,
		       a.platform_name       AS platform_name,
		       a.platform_id         AS platform_id,
		       a.funcom_id           AS funcom_id
		FROM dune.player_state ps
		LEFT JOIN dune.accounts a ON a.id = ps.account_id
		%s
		ORDER BY ps.last_login_time DESC NULLS LAST
		LIMIT $1
	`, where)
	rows, _, err := queryAll(r.Context(), globalDB, sql, args...)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, rows)
}

func handleGetPlayer(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid player id"), 400)
		return
	}
	ctx := r.Context()

	// Core record
	playerRows, _, err := queryAll(ctx, globalDB, `
		SELECT ps.player_state_id    AS id,
		       ps.account_id,
		       ps.character_name     AS name,
		       ps.online_status::text AS online_status,
		       ps.life_state::text   AS life_state,
		       ps.server_id,
		       ps.player_controller_id,
		       ps.player_pawn_id,
		       ps.last_login_time    AS last_login,
		       a.platform_name,
		       a.platform_id,
		       a.funcom_id,
		       a."user"              AS fls_id
		FROM dune.player_state ps
		LEFT JOIN dune.accounts a ON a.id = ps.account_id
		WHERE ps.player_state_id = $1
	`, id)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if len(playerRows) == 0 {
		jsonErr(w, fmt.Errorf("player not found"), 404)
		return
	}
	player := playerRows[0]

	// Currencies — virtual currency balances keyed by currency id.
	currencies, _, err := queryAll(ctx, globalDB,
		"SELECT currency_id, balance FROM dune.player_virtual_currency_balances WHERE player_id = $1 ORDER BY currency_id",
		id)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// Faction reputations
	factions, _, err := queryAll(ctx, globalDB, `
		SELECT pfr.faction_id, f.name, pfr.reputation, pfr.scrips
		FROM dune.player_faction_reputation pfr
		LEFT JOIN dune.factions f ON f.id = pfr.faction_id
		WHERE pfr.actor_id = $1
		ORDER BY pfr.faction_id
	`, id)
	if err != nil {
		// Non-fatal — pfr may legitimately have no rows for this id.
		factions = []map[string]any{}
	}

	// Inventories — uses snapetech's admin SRF if available, otherwise plain join.
	inv, _, err := queryAll(ctx, globalDB,
		"SELECT * FROM dune.admin_get_inventory_details($1)", id)
	if err != nil {
		// Fall back to direct query if the admin function isn't installed.
		inv, _, err = queryAll(ctx, globalDB, `
			SELECT i.id, i.inventory_id, i.template_id, i.stack_size,
			       i.position_index, i.quality, i.durability, i.max_durability
			FROM dune.items i
			JOIN dune.inventories inv ON inv.id = i.inventory_id
			WHERE inv.owner_id = $1
			ORDER BY inv.id, i.position_index
		`, id)
		if err != nil {
			inv = []map[string]any{}
		}
	}

	jsonOK(w, map[string]any{
		"player":     player,
		"currencies": currencies,
		"factions":   factions,
		"inventory":  inv,
	})
}

func handleGiveItem(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	var req struct {
		InventoryID int64  `json:"inventory_id"`
		TemplateID  string `json:"template_id"`
		StackSize   int    `json:"stack_size"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.InventoryID == 0 || req.TemplateID == "" || req.StackSize <= 0 {
		jsonErr(w, fmt.Errorf("inventory_id, template_id, and positive stack_size required"), 400)
		return
	}
	err := txOne(r.Context(), globalDB, func(tx pgx.Tx) error {
		// Find next free position_index in this inventory.
		var nextPos int
		if err := tx.QueryRow(r.Context(),
			"SELECT COALESCE(MAX(position_index), -1) + 1 FROM dune.items WHERE inventory_id = $1",
			req.InventoryID).Scan(&nextPos); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `
			INSERT INTO dune.items (inventory_id, template_id, stack_size, position_index)
			VALUES ($1, $2, $3, $4)
		`, req.InventoryID, req.TemplateID, req.StackSize, nextPos)
		return err
	})
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

func handleGiveCurrency(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	var req struct {
		PlayerID   int64 `json:"player_id"`
		CurrencyID int   `json:"currency_id"`
		Balance    int64 `json:"balance"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.PlayerID == 0 {
		jsonErr(w, fmt.Errorf("player_id required"), 400)
		return
	}
	_, err := globalDB.Exec(r.Context(), `
		INSERT INTO dune.player_virtual_currency_balances (player_id, currency_id, balance)
		VALUES ($1, $2, $3)
		ON CONFLICT (player_id, currency_id) DO UPDATE SET balance = EXCLUDED.balance
	`, req.PlayerID, req.CurrencyID, req.Balance)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

func handleSetFactionRep(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	var req struct {
		ActorID    int64 `json:"actor_id"`
		FactionID  int   `json:"faction_id"`
		Reputation int   `json:"reputation"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	// Use the canonical SRF — guarantees side-effects (tier evals) fire.
	_, err := globalDB.Exec(r.Context(),
		"SELECT dune.set_player_faction_reputation($1, $2, $3)",
		req.ActorID, req.FactionID, req.Reputation)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}
