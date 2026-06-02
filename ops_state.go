package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Phase 7 — ops scheduling + worker.
//
// State lives at /data/ops-state.json. Single file owned by a mutex; the
// worker re-reads on every tick (cheap, the file is small) so a manual
// edit is picked up on the next iteration.

const opsStatePath = "/data/ops-state.json"

type OpsState struct {
	Announcements []AnnouncementJob `json:"announcements"`
	Restarts      []RestartJob      `json:"restarts"`
}

type AnnouncementJob struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	RunAt     string `json:"run_at"`      // RFC3339
	Mode      string `json:"mode"`        // RMQ envelope mode (preview only)
	Routing   string `json:"routing"`     // e.g. "PlayerOnlineState", "#"
	Status    string `json:"status"`      // pending | preview-skipped | failed
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Error     string `json:"error,omitempty"`
}

type RestartJob struct {
	ID          string   `json:"id"`
	RunAt       string   `json:"run_at"`
	WarnMins    int      `json:"warn_mins"`     // 0 = no warning
	Services    []string `json:"services"`      // empty = all game-servers
	Status      string   `json:"status"`        // pending | warning | running | done | failed
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	StoppedAt   string   `json:"stopped_at,omitempty"`
	StartedAt   string   `json:"started_at,omitempty"`
	FinishedAt  string   `json:"finished_at,omitempty"`
	Error       string   `json:"error,omitempty"`
}

var opsMu sync.Mutex

func newOpsID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func loadOpsState() (*OpsState, error) {
	opsMu.Lock()
	defer opsMu.Unlock()
	return loadOpsStateLocked()
}

func loadOpsStateLocked() (*OpsState, error) {
	s := &OpsState{
		Announcements: []AnnouncementJob{},
		Restarts:      []RestartJob{},
	}
	f, err := os.Open(opsStatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(s); err != nil {
		return nil, err
	}
	if s.Announcements == nil {
		s.Announcements = []AnnouncementJob{}
	}
	if s.Restarts == nil {
		s.Restarts = []RestartJob{}
	}
	return s, nil
}

func saveOpsState(s *OpsState) error {
	opsMu.Lock()
	defer opsMu.Unlock()
	return saveOpsStateLocked(s)
}

func saveOpsStateLocked(s *OpsState) error {
	if err := os.MkdirAll(filepath.Dir(opsStatePath), 0o755); err != nil {
		return err
	}
	tmp := opsStatePath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, opsStatePath)
}

// updateOpsState reads + lets fn modify + writes the result under the lock.
func updateOpsState(fn func(*OpsState) error) error {
	opsMu.Lock()
	defer opsMu.Unlock()
	s, err := loadOpsStateLocked()
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return saveOpsStateLocked(s)
}

func parseRunAt(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid run_at (need RFC3339, e.g. %s): %w", time.Now().UTC().Format(time.RFC3339), err)
	}
	return t, nil
}
