package main

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/gorilla/websocket"
)

// Phase 4 — Logs tab.
//
// Endpoints:
//   GET /api/v1/logs/pods    — containers available for log streaming
//   GET /api/v1/logs/stream  — WebSocket; query: name=<container-name>

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: originPermitted,
}

// Container name validator. Docker allows alphanumerics, dash, underscore,
// period, and leading alphanumeric or underscore.
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.\-]*$`)

func isValidContainerName(name string) bool {
	return name != "" && len(name) <= 253 && containerNameRe.MatchString(name)
}

// LogPod describes one container the UI can tail.
type LogPod struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	State   string `json:"state"`
	Status  string `json:"status"`
}

func handleLogsPods(w http.ResponseWriter, r *http.Request) {
	if globalDocker == nil {
		jsonErr(w, fmt.Errorf("docker socket not mounted"), 503)
		return
	}
	containers, err := globalDocker.ListContainers(r.Context(), "")
	if err != nil {
		jsonErr(w, fmt.Errorf("docker list: %w", err), 500)
		return
	}
	out := make([]LogPod, 0, len(containers))
	for _, c := range containers {
		// Postgres backups produce a lot of routine noise; skip them by
		// default. The user can still tail them via the SQL "open in SQL"
		// flow if they really want to.
		if c.Service == "postgres" || c.Service == "postgres-backup" {
			continue
		}
		out = append(out, LogPod{
			Name:    c.Name,
			Service: c.Service,
			State:   c.State,
			Status:  c.Status,
		})
	}
	jsonOK(w, out)
}

// handleLogsStream upgrades to a WebSocket and pumps decoded log lines.
// Each WS message is a JSON object {"stream":"stdout","text":"..."} so the
// client can color-code stderr.
func handleLogsStream(w http.ResponseWriter, r *http.Request) {
	if globalDocker == nil {
		http.Error(w, "docker socket not mounted", 503)
		return
	}
	name := r.URL.Query().Get("name")
	if !isValidContainerName(name) {
		http.Error(w, "missing or invalid name", 400)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Cancel the underlying docker stream when the client disconnects.
	// gorilla's reader pump runs while we write to keep the connection alive
	// and react to client closes.
	ch, cancelStream, err := globalDocker.LogsStream(r.Context(), name, true)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(`{"stream":"error","text":"`+err.Error()+`"}`))
		return
	}
	defer cancelStream()

	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			payload := map[string]string{"stream": line.Stream, "text": line.Text}
			if err := conn.WriteJSON(payload); err != nil {
				return
			}
		case <-clientGone:
			return
		}
	}
}
