package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// liveOnlineSet calls OpsBridge.ListPlayers and returns the set of FLS
// hex strings of players currently in-world on the survival container.
// Returns (nil, false) if OpsBridge is unavailable so callers can fall
// back to the DB online_status column.
func liveOnlineSet(ctx context.Context) (map[string]bool, bool) {
	if globalOpsBridge == nil || !globalOpsBridge.Connected() {
		return nil, false
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	reply, err := globalOpsBridge.Call(callCtx, "ListPlayers", nil)
	if err != nil {
		return nil, false
	}
	var innerJSON string
	if err := json.Unmarshal(reply, &innerJSON); err != nil {
		return nil, false
	}
	type row struct {
		PlayerId string `json:"PlayerId"`
	}
	var rows []row
	if err := json.Unmarshal([]byte(innerJSON), &rows); err != nil {
		return nil, false
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		fls := strings.ToUpper(strings.TrimSpace(r.PlayerId))
		if fls != "" {
			out[fls] = true
		}
	}
	return out, true
}

// overlayOnlineStatus mutates rows' online_status based on the live
// online set, falling back to whatever the DB column said when
// OpsBridge isn't available. Matches on accounts.user (FLS hex)
// which is selected as fls_id in handleListPlayers / handleGetPlayer.
func overlayOnlineStatus(rows []map[string]any, live map[string]bool) {
	if live == nil {
		return
	}
	for _, row := range rows {
		var fls string
		if v, ok := row["fls_id"].(string); ok {
			fls = strings.ToUpper(strings.TrimSpace(v))
		}
		if fls == "" {
			continue
		}
		if live[fls] {
			row["online_status"] = "Online"
		} else {
			row["online_status"] = "Offline"
		}
	}
}

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
		       a.funcom_id           AS funcom_id,
		       a."user"              AS fls_id
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
	if live, ok := liveOnlineSet(r.Context()); ok {
		overlayOnlineStatus(rows, live)
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

	// Core record. The "id" param is player_state_id (1:1 per character);
	// most other tables key off account_id or player_controller_id, both
	// of which we surface from this same row so the UI can render forms.
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
	if live, ok := liveOnlineSet(ctx); ok {
		overlayOnlineStatus(playerRows, live)
	}
	player := playerRows[0]
	accountID := toInt64(player["account_id"])
	controllerID := toInt64(player["player_controller_id"])

	// Currencies — keyed by player_controller_id (FK → actors.id).
	currencies, _, err := queryAll(ctx, globalDB, `
		SELECT currency_id, balance
		FROM dune.player_virtual_currency_balances
		WHERE player_controller_id = $1
		ORDER BY currency_id
	`, controllerID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	// Faction reputations — keyed by actor_id (= player_controller_id).
	factions, _, err := queryAll(ctx, globalDB, `
		SELECT pfr.faction_id,
		       f.name,
		       pfr.reputation_amount AS reputation
		FROM dune.player_faction_reputation pfr
		LEFT JOIN dune.factions f ON f.id = pfr.faction_id
		WHERE pfr.actor_id = $1
		ORDER BY pfr.faction_id
	`, controllerID)
	if err != nil {
		factions = []map[string]any{}
	}

	// Inventories — snapetech's admin SRF takes account_id. Fall back to a
	// plain join if the function isn't installed (different DB versions).
	inv, _, err := queryAll(ctx, globalDB,
		"SELECT * FROM dune.admin_get_inventory_details($1)", accountID)
	if err != nil {
		inv, _, err = queryAll(ctx, globalDB, `
			SELECT i.id, i.inventory_id, i.template_id, i.stack_size,
			       i.position_index, i.quality_level
			FROM dune.items i
			JOIN dune.inventories inv ON inv.id = i.inventory_id
			WHERE inv.actor_id = $1
			ORDER BY inv.id, i.position_index
		`, controllerID)
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

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// addItemToInventory is the shared insert used by both Players and Storage
// give-item flows. Picks the next free position_index in the target
// inventory and inserts with stats='{}' so the NOT-NULL jsonb constraint
// is satisfied (the game accepts an empty object as the neutral value).
func addItemToInventory(ctx context.Context, inventoryID int64, templateID string, stackSize int) error {
	return txOne(ctx, globalDB, func(tx pgx.Tx) error {
		var nextPos int64
		if err := tx.QueryRow(ctx,
			"SELECT COALESCE(MAX(position_index), -1) + 1 FROM dune.items WHERE inventory_id = $1",
			inventoryID).Scan(&nextPos); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO dune.items
				(inventory_id, template_id, stack_size, position_index, stats)
			VALUES
				($1, $2, $3, $4, '{}'::jsonb)
		`, inventoryID, templateID, stackSize, nextPos)
		return err
	})
}

// callOpsBridgeWrite is the shared shape for the three write handlers
// migrated off direct DB. Routes through globalOpsBridge.Call and
// returns 503 if OpsBridge is unavailable so the operator gets a clean
// signal instead of a half-applied state.
func callOpsBridgeWrite(w http.ResponseWriter, r *http.Request, op, audit string, envelope map[string]any) {
	if globalOpsBridge == nil || !globalOpsBridge.Connected() {
		jsonErr(w, fmt.Errorf("OpsBridge disconnected"), 503)
		return
	}
	callCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := globalOpsBridge.Call(callCtx, op, envelope); err != nil {
		auditErr(r, audit, envelope, err)
		jsonErr(w, err, 500)
		return
	}
	auditOK(r, audit, envelope)
	jsonOK(w, map[string]any{"ok": true})
}

func handleGiveItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlayerId   string  `json:"player_id"`
		ItemName   string  `json:"item_name"`
		Quantity   int     `json:"quantity"`
		Durability float64 `json:"durability"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.PlayerId == "" || req.ItemName == "" || req.Quantity <= 0 {
		jsonErr(w, fmt.Errorf("player_id, item_name, and positive quantity required"), 400)
		return
	}
	durability := req.Durability
	if durability <= 0 {
		durability = 1.0
	}
	envelope := []map[string]any{
		{
			"ServerCommand": "AddItemToInventory",
			"PlayerId":      req.PlayerId,
			"ItemName":      req.ItemName,
			"Quantity":      req.Quantity,
			"Durability":    durability,
		},
	}
	envJSON, _ := json.Marshal(envelope)
	callArgs := map[string]any{
		"Envelope":    string(envJSON),
		"Description": fmt.Sprintf("give-item %s x%d", req.ItemName, req.Quantity),
	}
	callOpsBridgeWrite(w, r, "GMCommand", "players.give-item", callArgs)
}

func handleGiveCurrency(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlayerId string `json:"player_id"`
		Quantity int    `json:"quantity"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.PlayerId == "" {
		jsonErr(w, fmt.Errorf("player_id required"), 400)
		return
	}
	callOpsBridgeWrite(w, r, "AddSolarisToAccount", "players.give-currency",
		map[string]any{
			"PlayerId": req.PlayerId,
			"Quantity": req.Quantity,
		})
}

func handleSetFactionRep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlayerId    string `json:"player_id"`
		FactionName string `json:"faction_name"`
		Amount      int    `json:"amount"`
		Mode        string `json:"mode"` // "set" (default) or "add"
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.PlayerId == "" || req.FactionName == "" {
		jsonErr(w, fmt.Errorf("player_id and faction_name required"), 400)
		return
	}
	op := "FactionSetReputationAmount"
	if strings.EqualFold(req.Mode, "add") {
		op = "FactionAddReputationAmount"
	}
	callOpsBridgeWrite(w, r, op, "players.set-faction-rep",
		map[string]any{
			"PlayerId":         req.PlayerId,
			"FactionName":      req.FactionName,
			"ReputationAmount": req.Amount,
		})
}
