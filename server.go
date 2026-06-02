package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// registerRoutes wires up the HTTP handlers. Per-phase files (handlers_*.go)
// register their own routes here; this file owns only the shared scaffolding.
func registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", handleStatus)
	mux.HandleFunc("POST /api/v1/reconnect", handleReconnect)

	// Phase 2 — overview
	mux.HandleFunc("GET /api/v1/overview/snapshot", handleOverviewSnapshot)

	// Phase 3 — database
	mux.HandleFunc("GET /api/v1/database/tables", handleDBTables)
	mux.HandleFunc("GET /api/v1/database/describe", handleDBDescribe)
	mux.HandleFunc("GET /api/v1/database/sample", handleDBSample)
	mux.HandleFunc("POST /api/v1/database/sql", handleDBSQL)

	// Phase 3 — players
	mux.HandleFunc("GET /api/v1/players", handleListPlayers)
	mux.HandleFunc("GET /api/v1/players/{id}", handleGetPlayer)
	mux.HandleFunc("POST /api/v1/players/give-item", handleGiveItem)
	mux.HandleFunc("POST /api/v1/players/give-currency", handleGiveCurrency)
	mux.HandleFunc("POST /api/v1/players/set-faction-rep", handleSetFactionRep)

	// Phase 4 — logs
	mux.HandleFunc("GET /api/v1/logs/pods", handleLogsPods)
	mux.HandleFunc("GET /api/v1/logs/stream", handleLogsStream)

	// Phase 5 — audit + GM commands
	mux.HandleFunc("GET /api/v1/audit", handleAuditList)
	mux.HandleFunc("GET /api/v1/gm/catalog", handleGMCatalog)
	mux.HandleFunc("POST /api/v1/gm/preview", handleGMPreview)

	// Phase 6 — settings
	mux.HandleFunc("GET /api/v1/settings", handleSettingsList)
	mux.HandleFunc("POST /api/v1/settings", handleSettingsSave)

	// Phase 7 — ops (announcements + scheduled restarts)
	mux.HandleFunc("GET /api/v1/ops/announcements", handleOpsAnnouncementsList)
	mux.HandleFunc("POST /api/v1/ops/announcements", handleOpsAnnouncementsCreate)
	mux.HandleFunc("DELETE /api/v1/ops/announcements/{id}", handleOpsAnnouncementsDelete)
	mux.HandleFunc("GET /api/v1/ops/restarts", handleOpsRestartsList)
	mux.HandleFunc("POST /api/v1/ops/restarts", handleOpsRestartsCreate)
	mux.HandleFunc("DELETE /api/v1/ops/restarts/{id}", handleOpsRestartsDelete)

	// Embedded SPA — catch-all. Must be registered last (or have no overlap
	// with /api/v1/* which is fine since these patterns don't collide).
	mux.HandleFunc("/", serveWeb)
}

// ── CORS / origin handling ───────────────────────────────────────────────────

// originPermitted gates CORS and WebSocket upgrades. Same-origin is always
// allowed; cross-origin requires being in ALLOWED_ORIGINS.
func originPermitted(r *http.Request) bool {
	if sameOrigin(r) {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, o := range strings.Split(allowedOrigins, ",") {
		if strings.TrimSpace(o) == origin {
			return true
		}
	}
	return false
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Origin")
		if originPermitted(r) {
			if origin := r.Header.Get("Origin"); origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── JSON helpers ─────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ── Shared handlers ──────────────────────────────────────────────────────────

func handleStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{
		"version":                version,
		"docker_connected":       globalDocker != nil,
		"orchestrator_connected": globalOrchestrator != nil,
		"battlegroup_ns":         battlegroupNS,
	})
}

func handleReconnect(w http.ResponseWriter, r *http.Request) {
	closeConnections()
	if err := connect(r.Context()); err != nil {
		jsonErr(w, fmt.Errorf("connect: %w", err), 500)
		return
	}
	handleStatus(w, r)
}
