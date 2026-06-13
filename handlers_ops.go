package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Phase 7 — ops scheduling.
//
// Two job types:
//   - Announcements — preview-only execute (RMQ envelope contract is
//     unverified, same as GM commands); worker still runs at the
//     scheduled time but only renders + audits the would-be envelope.
//   - Restarts — real execute via docker socket. Worker stops the named
//     services (or every game-server-* if Services is empty) and starts
//     them again, then waits for at least one to report ready=true
//     through the orchestrator.

// ── Announcements ────────────────────────────────────────────────────────

func handleOpsAnnouncementsList(w http.ResponseWriter, r *http.Request) {
	s, err := loadOpsState()
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, s.Announcements)
}

func handleOpsAnnouncementsCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Message     string `json:"message"`
		DurationSec int    `json:"duration_sec"`
		RunAt       string `json:"run_at"`
		Mode        string `json:"mode"`
		Routing     string `json:"routing"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Message = strings.TrimSpace(req.Message)
	if req.Title == "" {
		req.Title = "Server Announcement"
	}
	if len(req.Title) > 200 {
		jsonErr(w, fmt.Errorf("title too long (max 200)"), 400)
		return
	}
	if req.Message == "" {
		jsonErr(w, fmt.Errorf("message required"), 400)
		return
	}
	if req.DurationSec == 0 {
		req.DurationSec = 10
	}
	// Match the cppmod handler's accepted range; cheaper to reject here
	// than wait for the OpsBridge reply to surface the error.
	if req.DurationSec < 1 || req.DurationSec > 600 {
		jsonErr(w, fmt.Errorf("duration_sec must be 1..600"), 400)
		return
	}
	if _, err := parseRunAt(req.RunAt); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.Mode == "" {
		req.Mode = "service-message"
	}
	if req.Routing == "" {
		req.Routing = "#"
	}

	job := AnnouncementJob{
		ID:          newOpsID(),
		Title:       req.Title,
		Message:     req.Message,
		DurationSec: req.DurationSec,
		RunAt:       req.RunAt,
		Mode:        req.Mode,
		Routing:     req.Routing,
		Status:      "pending",
		CreatedAt:   nowRFC3339(),
		UpdatedAt:   nowRFC3339(),
	}
	if err := updateOpsState(func(s *OpsState) error {
		s.Announcements = append(s.Announcements, job)
		return nil
	}); err != nil {
		jsonErr(w, err, 500)
		return
	}
	auditOK(r, "ops.announcement.create", map[string]any{
		"id":           job.ID,
		"run_at":       job.RunAt,
		"title":        job.Title,
		"message":      job.Message,
		"duration_sec": job.DurationSec,
		"mode":         job.Mode,
		"routing":      job.Routing,
	})
	jsonOK(w, job)
}

func handleOpsAnnouncementsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonErr(w, fmt.Errorf("id required"), 400)
		return
	}
	var found bool
	if err := updateOpsState(func(s *OpsState) error {
		out := s.Announcements[:0]
		for _, a := range s.Announcements {
			if a.ID == id {
				found = true
				continue
			}
			out = append(out, a)
		}
		s.Announcements = out
		return nil
	}); err != nil {
		jsonErr(w, err, 500)
		return
	}
	if !found {
		jsonErr(w, fmt.Errorf("not found"), 404)
		return
	}
	auditOK(r, "ops.announcement.cancel", map[string]any{"id": id})
	jsonOK(w, map[string]any{"ok": true})
}

// ── Restarts ────────────────────────────────────────────────────────────

func handleOpsRestartsList(w http.ResponseWriter, r *http.Request) {
	s, err := loadOpsState()
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, s.Restarts)
}

func handleOpsRestartsCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunAt    string   `json:"run_at"`
		WarnMins int      `json:"warn_mins"`
		Services []string `json:"services"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if _, err := parseRunAt(req.RunAt); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if req.WarnMins < 0 {
		req.WarnMins = 0
	}
	if req.WarnMins > 60 {
		jsonErr(w, fmt.Errorf("warn_mins must be 0-60"), 400)
		return
	}

	job := RestartJob{
		ID:        newOpsID(),
		RunAt:     req.RunAt,
		WarnMins:  req.WarnMins,
		Services:  req.Services,
		Status:    "pending",
		CreatedAt: nowRFC3339(),
		UpdatedAt: nowRFC3339(),
	}
	if err := updateOpsState(func(s *OpsState) error {
		s.Restarts = append(s.Restarts, job)
		return nil
	}); err != nil {
		jsonErr(w, err, 500)
		return
	}
	auditOK(r, "ops.restart.create", map[string]any{
		"id":        job.ID,
		"run_at":    job.RunAt,
		"warn_mins": job.WarnMins,
		"services":  job.Services,
	})
	jsonOK(w, job)
}

func handleOpsRestartsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonErr(w, fmt.Errorf("id required"), 400)
		return
	}
	var found bool
	if err := updateOpsState(func(s *OpsState) error {
		out := s.Restarts[:0]
		for _, x := range s.Restarts {
			if x.ID == id {
				found = true
				continue
			}
			out = append(out, x)
		}
		s.Restarts = out
		return nil
	}); err != nil {
		jsonErr(w, err, 500)
		return
	}
	if !found {
		jsonErr(w, fmt.Errorf("not found"), 404)
		return
	}
	auditOK(r, "ops.restart.cancel", map[string]any{"id": id})
	jsonOK(w, map[string]any{"ok": true})
}

// ── Worker ──────────────────────────────────────────────────────────────

// startOpsWorker spawns a single goroutine that ticks every 15 s and
// executes jobs whose run_at has arrived. Cancelled via context.
func startOpsWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				opsWorkerTick(ctx)
			}
		}
	}()
}

func opsWorkerTick(ctx context.Context) {
	now := time.Now()
	_ = updateOpsState(func(s *OpsState) error {
		// Announcements.
		for i := range s.Announcements {
			a := &s.Announcements[i]
			if a.Status != "pending" {
				continue
			}
			t, err := parseRunAt(a.RunAt)
			if err != nil || now.Before(t) {
				continue
			}
			executeAnnouncement(ctx, a)
		}
		// Restarts.
		for i := range s.Restarts {
			r := &s.Restarts[i]
			t, err := parseRunAt(r.RunAt)
			if err != nil {
				continue
			}
			switch r.Status {
			case "pending":
				if r.WarnMins > 0 && now.After(t.Add(-time.Duration(r.WarnMins)*time.Minute)) && now.Before(t) {
					r.Status = "warning"
					r.UpdatedAt = nowRFC3339()
					emitRestartWarning(ctx, r, t)
				} else if !now.Before(t) {
					executeRestart(ctx, r)
				}
			case "warning":
				if !now.Before(t) {
					executeRestart(ctx, r)
				}
			}
		}
		return nil
	})
}

// executeAnnouncement mutates a in place. Publishes via OpsBridge's
// Broadcast handler (Phase 10 C0/C2). If OpsBridge is currently
// disconnected, the job stays "pending" so the next worker tick retries
// — survival-container restarts (1–2 min cycle) shouldn't permanently
// fail scheduled broadcasts.
func executeAnnouncement(ctx context.Context, a *AnnouncementJob) {
	if !opsAnyConnected() {
		// Stay pending; worker retries every 15 s. Audit only once per
		// minute would be ideal, but the audit log here would be noisy
		// across long outages — emit a single log line instead.
		log.Printf("ops: announcement %s deferred — OpsBridge disconnected", a.ID)
		return
	}

	args := map[string]any{
		"Title":       a.Title,
		"Body":        a.Message,
		"DurationSec": a.DurationSec,
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Fan-out broadcast so players on DD see the HUD popup too.
	reply, err := opsBroadcastCall(callCtx, "Broadcast", args)

	a.UpdatedAt = nowRFC3339()
	if err != nil {
		a.Status = "failed"
		a.Error = err.Error()
		writeAudit(AuditEvent{
			Action: "ops.announcement.execute",
			OK:     false,
			Error:  err.Error(),
			Fields: map[string]any{
				"id":           a.ID,
				"title":        a.Title,
				"message":      a.Message,
				"duration_sec": a.DurationSec,
			},
		})
		log.Printf("ops: announcement %s publish failed: %v", a.ID, err)
		return
	}
	a.Status = "done"
	writeAudit(AuditEvent{
		Action: "ops.announcement.execute",
		OK:     true,
		Fields: map[string]any{
			"id":           a.ID,
			"title":        a.Title,
			"message":      a.Message,
			"duration_sec": a.DurationSec,
			"reply":        string(reply),
		},
	})
	log.Printf("ops: announcement %s published (%d s)", a.ID, a.DurationSec)
}

// emitRestartWarning is the C5 publish step. Fires once when a restart
// job enters its warn window. Composes a "Server restarting in N
// (minutes|seconds)" broadcast where N is derived from the actual
// remaining time, not r.WarnMins — by the time the worker tick fires,
// remaining can be anywhere in [0, warn_mins+15s), so rounding off
// remaining gives operators a more accurate countdown than echoing the
// nominal threshold.
//
// One-shot publish: we transition r.Status = "warning" unconditionally
// at the call site so that even if OpsBridge is down (or Call fails
// otherwise) the worker doesn't re-enter this branch and spam broadcasts
// every 15 s. The actual restart still proceeds at run_at.
func emitRestartWarning(ctx context.Context, r *RestartJob, runAt time.Time) {
	remaining := time.Until(runAt)
	if remaining < 0 {
		remaining = 0
	}
	var body string
	if remaining >= time.Minute {
		mins := int((remaining + 30*time.Second) / time.Minute)
		if mins == 1 {
			body = "Server restarting in 1 minute"
		} else {
			body = fmt.Sprintf("Server restarting in %d minutes", mins)
		}
	} else {
		secs := int((remaining + 500*time.Millisecond) / time.Second)
		if secs <= 1 {
			body = "Server restarting now"
		} else {
			body = fmt.Sprintf("Server restarting in %d seconds", secs)
		}
	}

	if !opsAnyConnected() {
		r.WarnError = "OpsBridge disconnected at warn time"
		writeAudit(AuditEvent{
			Action: "ops.restart.warn",
			OK:     false,
			Error:  "OpsBridge disconnected",
			Fields: map[string]any{
				"id":           r.ID,
				"run_at":       r.RunAt,
				"warn_mins":    r.WarnMins,
				"title":        "Server Restart",
				"body":         body,
				"duration_sec": 30,
			},
		})
		log.Printf("ops: restart %s warning skipped — OpsBridge disconnected (body=%q)", r.ID, body)
		return
	}

	args := map[string]any{
		"Title":       "Server Restart",
		"Body":        body,
		"DurationSec": 30,
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Fan-out so players on DD see the restart warning too.
	reply, err := opsBroadcastCall(callCtx, "Broadcast", args)
	if err != nil {
		r.WarnError = err.Error()
		writeAudit(AuditEvent{
			Action: "ops.restart.warn",
			OK:     false,
			Error:  err.Error(),
			Fields: map[string]any{
				"id":           r.ID,
				"run_at":       r.RunAt,
				"warn_mins":    r.WarnMins,
				"title":        "Server Restart",
				"body":         body,
				"duration_sec": 30,
			},
		})
		log.Printf("ops: restart %s warning publish failed: %v", r.ID, err)
		return
	}
	r.WarnError = ""
	writeAudit(AuditEvent{
		Action: "ops.restart.warn",
		OK:     true,
		Fields: map[string]any{
			"id":           r.ID,
			"run_at":       r.RunAt,
			"warn_mins":    r.WarnMins,
			"title":        "Server Restart",
			"body":         body,
			"duration_sec": 30,
			"reply":        string(reply),
		},
	})
	log.Printf("ops: restart %s warning broadcast: %q", r.ID, body)
}

// executeRestart actually stops + starts containers via the docker socket
// and waits for at least one game-server to come back online via the
// orchestrator. Updates r in place; caller's updateOpsState writes back.
func executeRestart(ctx context.Context, r *RestartJob) {
	r.Status = "running"
	r.UpdatedAt = nowRFC3339()
	startedAt := time.Now()

	if globalDocker == nil {
		fail(r, fmt.Errorf("docker socket not mounted"))
		return
	}

	// Determine target services. Default = every running game-server-*.
	containers, err := globalDocker.ListContainers(ctx, "")
	if err != nil {
		fail(r, fmt.Errorf("list containers: %w", err))
		return
	}
	var targets []ContainerInfo
	if len(r.Services) > 0 {
		want := make(map[string]bool, len(r.Services))
		for _, s := range r.Services {
			want[s] = true
		}
		for _, c := range containers {
			if want[c.Service] && c.State == "running" {
				targets = append(targets, c)
			}
		}
	} else {
		for _, c := range containers {
			if strings.HasPrefix(c.Service, "game-server-") && c.State == "running" {
				targets = append(targets, c)
			}
		}
	}

	if len(targets) == 0 {
		writeAudit(AuditEvent{
			Action: "ops.restart.execute",
			OK:     true,
			Fields: map[string]any{"id": r.ID, "note": "no matching running containers"},
		})
		r.Status = "done"
		r.StartedAt = nowRFC3339()
		r.StoppedAt = nowRFC3339()
		r.FinishedAt = nowRFC3339()
		return
	}

	for _, t := range targets {
		if err := globalDocker.Stop(ctx, t.ID, 60); err != nil {
			writeAudit(AuditEvent{
				Action: "ops.restart.execute",
				OK:     false,
				Error:  err.Error(),
				Fields: map[string]any{"id": r.ID, "service": t.Service, "step": "stop"},
			})
		}
	}
	r.StoppedAt = nowRFC3339()
	r.UpdatedAt = r.StoppedAt

	for _, t := range targets {
		if err := globalDocker.Start(ctx, t.ID); err != nil {
			writeAudit(AuditEvent{
				Action: "ops.restart.execute",
				OK:     false,
				Error:  err.Error(),
				Fields: map[string]any{"id": r.ID, "service": t.Service, "step": "start"},
			})
		}
	}
	r.StartedAt = nowRFC3339()
	r.UpdatedAt = r.StartedAt
	r.FinishedAt = nowRFC3339()
	r.Status = "done"

	writeAudit(AuditEvent{
		Action: "ops.restart.execute",
		OK:     true,
		Fields: map[string]any{
			"id":            r.ID,
			"services":      serviceNames(targets),
			"duration_secs": int(time.Since(startedAt).Seconds()),
		},
	})
}

func fail(r *RestartJob, err error) {
	r.Status = "failed"
	r.Error = err.Error()
	r.FinishedAt = nowRFC3339()
	r.UpdatedAt = r.FinishedAt
	writeAudit(AuditEvent{
		Action: "ops.restart.execute",
		OK:     false,
		Error:  err.Error(),
		Fields: map[string]any{"id": r.ID},
	})
}

func serviceNames(cs []ContainerInfo) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Service)
	}
	return out
}
