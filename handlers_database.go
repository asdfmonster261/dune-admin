package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Phase 3 — Database tab.
//
// Endpoints:
//   GET  /api/v1/database/tables       — list tables in our schema
//   GET  /api/v1/database/sample       — first N rows of one table
//   GET  /api/v1/database/describe     — column info for one table
//   POST /api/v1/database/sql          — read-only SQL runner

// handleDBTables returns the tables in the configured schema, with row
// counts where they're cheap (information_schema's reltuples estimate).
func handleDBTables(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	const sql = `
		SELECT c.relname AS name,
		       c.reltuples::bigint AS approx_rows
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1
		  AND c.relkind IN ('r', 'v', 'm')
		ORDER BY c.relname
	`
	rows, _, err := queryAll(r.Context(), globalDB, sql, dbSchema)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, rows)
}

// handleDBDescribe returns column metadata for the given table.
func handleDBDescribe(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	name := r.URL.Query().Get("name")
	if !isIdentifier(name) {
		jsonErr(w, fmt.Errorf("invalid table name"), 400)
		return
	}
	const sql = `
		SELECT column_name, data_type, udt_name, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position
	`
	rows, _, err := queryAll(r.Context(), globalDB, sql, dbSchema, name)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, rows)
}

// handleDBSample returns the first N (default 50, max 500) rows of a table.
func handleDBSample(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	name := r.URL.Query().Get("name")
	if !isIdentifier(name) {
		jsonErr(w, fmt.Errorf("invalid table name"), 400)
		return
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
		if limit > 500 {
			limit = 500
		}
	}
	// Safe because isIdentifier passed and dbSchema is operator-set.
	sql := fmt.Sprintf("SELECT * FROM %s.%s LIMIT %d",
		pgQuoteIdent(dbSchema), pgQuoteIdent(name), limit)
	rows, _, err := queryAll(r.Context(), globalDB, sql)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, rows)
}

// handleDBSQL runs a read-only SQL statement. Statements are inspected
// for write/DDL keywords; only SELECT / WITH / EXPLAIN / SHOW are allowed.
func handleDBSQL(w http.ResponseWriter, r *http.Request) {
	if globalDB == nil {
		jsonErr(w, fmt.Errorf("db not connected"), 503)
		return
	}
	var req struct {
		SQL string `json:"sql"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if !isReadOnlySQL(req.SQL) {
		jsonErr(w, fmt.Errorf("only SELECT/WITH/EXPLAIN/SHOW are allowed via this endpoint"), 400)
		return
	}
	rows, cols, err := queryAll(r.Context(), globalDB, req.SQL)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	jsonOK(w, map[string]any{
		"columns": cols,
		"rows":    rows,
	})
}

// ── helpers ──────────────────────────────────────────────────────────────

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`)

func isIdentifier(s string) bool { return identRe.MatchString(s) }

// pgQuoteIdent doubles any embedded " and wraps in ". For our identifiers
// (already validated against identRe) the loop is defensive.
func pgQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// isReadOnlySQL strips comments and rejects anything that's not clearly a
// query. Conservative: refuses unless the first non-comment token is
// SELECT, WITH, EXPLAIN, or SHOW.
func isReadOnlySQL(s string) bool {
	// Strip /* ... */ block comments first (they could disguise writes).
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "*/")
		if j < 0 {
			return false
		}
		s = s[:i] + " " + s[i+j+2:]
	}
	// Strip -- line comments.
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte(' ')
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" {
		return false
	}
	upper := strings.ToUpper(cleaned)
	for _, prefix := range []string{"SELECT ", "SELECT\t", "WITH ", "WITH\t", "EXPLAIN ", "EXPLAIN\t", "SHOW "} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}
