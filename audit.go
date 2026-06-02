package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phase 5 — audit log.
//
// Every state-changing handler appends a JSONL event to /data/audit.jsonl.
// Rotation happens at 10 MiB: audit.jsonl → audit.1.jsonl → audit.2.jsonl …
// up to 5 backups. /data is a named volume in the dune-server compose so
// the trail survives container rebuilds.
//
// Format: one JSON object per line, fields:
//   ts        ISO8601 timestamp
//   action    short identifier (e.g. "players.give-item")
//   ok        bool — false when the underlying handler errored
//   actor     client remote address (we have no auth yet)
//   error     present + populated when ok=false
//   fields    handler-specific payload (sanitized, no secrets)

const (
	auditPath        = "/data/audit.jsonl"
	auditMaxBytes    = 10 * 1024 * 1024
	auditMaxBackups  = 5
	auditReadLimit   = 10000
)

var auditMu sync.Mutex

// AuditEvent is the serialized shape of one log entry.
type AuditEvent struct {
	TS     string         `json:"ts"`
	Action string         `json:"action"`
	OK     bool           `json:"ok"`
	Actor  string         `json:"actor,omitempty"`
	Error  string         `json:"error,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
}

// writeAudit appends an event to /data/audit.jsonl. Rotates on size.
// All errors are swallowed and logged — audit failures must not break the
// underlying handler.
func writeAudit(e AuditEvent) {
	auditMu.Lock()
	defer auditMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "audit: mkdir:", err)
		return
	}
	if info, err := os.Stat(auditPath); err == nil && info.Size() > auditMaxBytes {
		rotateAuditLocked()
	}
	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit: open:", err)
		return
	}
	defer f.Close()
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, _ := json.Marshal(e)
	b = append(b, '\n')
	if _, err := f.Write(b); err != nil {
		fmt.Fprintln(os.Stderr, "audit: write:", err)
	}
}

// rotateAuditLocked shifts audit.{N-1}.jsonl → audit.N.jsonl, then renames
// the current file to .1. Caller must hold auditMu.
func rotateAuditLocked() {
	for i := auditMaxBackups; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", auditPath, i-1)
		if i == 1 {
			src = auditPath
		}
		dst := fmt.Sprintf("%s.%d", auditPath, i)
		_ = os.Remove(dst)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
}

// auditFromRequest pulls a best-effort actor identifier off the request.
// X-Forwarded-For wins if set (reverse-proxy case); otherwise RemoteAddr's
// host portion.
func auditActor(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if host, _, err := splitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// splitHostPort is net.SplitHostPort but bypasses the error when the port
// is missing — we still want the host as the actor.
func splitHostPort(s string) (string, string, error) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return s, "", nil
	}
	if j := strings.LastIndexByte(s[:i], ']'); j > -1 {
		return s[1:j], s[i+1:], nil
	}
	return s[:i], s[i+1:], nil
}

// auditOK records a successful action with the given fields.
func auditOK(r *http.Request, action string, fields map[string]any) {
	writeAudit(AuditEvent{
		Action: action,
		OK:     true,
		Actor:  auditActor(r),
		Fields: fields,
	})
}

// auditErr records a failed action.
func auditErr(r *http.Request, action string, fields map[string]any, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	writeAudit(AuditEvent{
		Action: action,
		OK:     false,
		Actor:  auditActor(r),
		Error:  msg,
		Fields: fields,
	})
}

// ── HTTP handler ──────────────────────────────────────────────────────────

func handleAuditList(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
		if limit > auditReadLimit {
			limit = auditReadLimit
		}
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	events, err := readAuditTail(limit, q)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, events)
}

// readAuditTail reads the last N events matching q (substring across
// action+actor+fields). Files are read in reverse: current → .1 → .2 …
// until we hit the limit.
func readAuditTail(limit int, q string) ([]AuditEvent, error) {
	auditMu.Lock()
	defer auditMu.Unlock()

	out := make([]AuditEvent, 0, limit)
	paths := []string{auditPath}
	for i := 1; i <= auditMaxBackups; i++ {
		paths = append(paths, fmt.Sprintf("%s.%d", auditPath, i))
	}

	for _, p := range paths {
		if len(out) >= limit {
			break
		}
		lines, err := tailJSONL(p, limit-len(out)+512)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return out, err
		}
		// lines come in chronological order; we want newest first → reverse.
		for i := len(lines) - 1; i >= 0; i-- {
			var e AuditEvent
			if err := json.Unmarshal([]byte(lines[i]), &e); err != nil {
				continue
			}
			if q != "" && !auditMatches(&e, q) {
				continue
			}
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func auditMatches(e *AuditEvent, q string) bool {
	if strings.Contains(strings.ToLower(e.Action), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Actor), q) {
		return true
	}
	if e.Error != "" && strings.Contains(strings.ToLower(e.Error), q) {
		return true
	}
	for k, v := range e.Fields {
		if strings.Contains(strings.ToLower(k), q) {
			return true
		}
		if strings.Contains(strings.ToLower(fmt.Sprintf("%v", v)), q) {
			return true
		}
	}
	return false
}

// tailJSONL reads up to `max` lines from the end of a file. For our log
// sizes (capped at 10 MiB per file) we just slurp it; rotation keeps each
// file bounded.
func tailJSONL(path string, max int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var lines []string
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			lines = append(lines, strings.TrimRight(line, "\n"))
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return lines, err
		}
	}
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines, nil
}
