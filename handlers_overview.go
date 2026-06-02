package main

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Phase 2 — overview panel.
//
// Returns a single snapshot used to render the dashboard. We deliberately
// keep this one endpoint (rather than separate /system + /containers +
// /players) so the UI does one request per refresh.

type OverviewSnapshot struct {
	Host       HostSummary       `json:"host"`
	Containers []ContainerEntry  `json:"containers"`
	Players    PlayerSummary     `json:"players"`
	Peak       PlayerPeakSummary `json:"peak"`
}

type HostSummary struct {
	Name            string `json:"name"`
	OperatingSystem string `json:"operating_system"`
	KernelVersion   string `json:"kernel_version"`
	DockerVersion   string `json:"docker_version"`
	NCPU            int    `json:"ncpu"`
	MemTotal        int64  `json:"mem_total"`
}

type ContainerEntry struct {
	Service    string `json:"service"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Status     string `json:"status"`
	IsGame     bool   `json:"is_game"`      // game-server-*
	IsCore     bool   `json:"is_core"`      // postgres, rabbitmq, director, gateway, orchestrator
	IsOnDemand bool   `json:"is_on_demand"` // game-server-sh-* / cb-* / story-* etc.
}

type PlayerSummary struct {
	Online    int            `json:"online"`
	ByMap     map[string]int `json:"by_map"`
	ServersUp int            `json:"servers_up"`
}

type PlayerPeakSummary struct {
	SessionMax int    `json:"session_max"`
	SessionAt  string `json:"session_at,omitempty"`
}

// In-process peak tracking. Persistence comes in a later iteration.
var (
	peakMu    sync.Mutex
	peakValue int
	peakAt    time.Time
)

func updateSessionPeak(current int) (int, time.Time) {
	peakMu.Lock()
	defer peakMu.Unlock()
	if current > peakValue {
		peakValue = current
		peakAt = time.Now()
	}
	return peakValue, peakAt
}

func handleOverviewSnapshot(w http.ResponseWriter, r *http.Request) {
	if globalDocker == nil {
		jsonErr(w, fmt.Errorf("docker socket not mounted"), 503)
		return
	}
	ctx := r.Context()

	// Host info.
	info, err := globalDocker.Info(ctx)
	if err != nil {
		jsonErr(w, fmt.Errorf("docker info: %w", err), 502)
		return
	}
	host := HostSummary{
		Name:            info.Name,
		OperatingSystem: info.OperatingSystem,
		KernelVersion:   info.KernelVersion,
		DockerVersion:   info.DockerVersion,
		NCPU:            info.NCPU,
		MemTotal:        info.MemTotal,
	}

	// Container list (dune-server compose project only).
	raw, err := globalDocker.ListContainers(ctx, "")
	if err != nil {
		jsonErr(w, fmt.Errorf("docker containers: %w", err), 502)
		return
	}
	containers := make([]ContainerEntry, 0, len(raw))
	for _, c := range raw {
		entry := ContainerEntry{
			Service: c.Service,
			Name:    c.Name,
			State:   c.State,
			Status:  c.Status,
		}
		switch {
		case c.Service == "game-server-survival" || c.Service == "game-server-overmap":
			entry.IsGame = true
		case len(c.Service) > 12 && c.Service[:12] == "game-server-":
			entry.IsGame = true
			entry.IsOnDemand = true
		case c.Service == "postgres" || c.Service == "rabbitmq-admin" || c.Service == "rabbitmq-game" ||
			c.Service == "bg-director" || c.Service == "server-gateway" || c.Service == "text-router" ||
			c.Service == "dune-orchestrator":
			entry.IsCore = true
		}
		containers = append(containers, entry)
	}
	sort.Slice(containers, func(i, j int) bool {
		// Core services first, then always-on game-servers, then on-demand,
		// then everything else (alphabetical inside each group).
		ci, cj := containers[i], containers[j]
		rank := func(c ContainerEntry) int {
			switch {
			case c.IsCore:
				return 0
			case c.IsGame && !c.IsOnDemand:
				return 1
			case c.IsGame && c.IsOnDemand:
				return 2
			default:
				return 3
			}
		}
		ri, rj := rank(ci), rank(cj)
		if ri != rj {
			return ri < rj
		}
		return ci.Service < cj.Service
	})

	// Player counts — sum runtime.players across every ServerStats item the
	// orchestrator knows about. If a server has never PATCHed (cold on-demand
	// map), it isn't in the list; we just don't count it.
	players := PlayerSummary{ByMap: map[string]int{}}
	if globalOrchestrator != nil && battlegroupNS != "" {
		list, err := globalOrchestrator.ListServerStats(ctx, battlegroupNS)
		if err == nil {
			for _, item := range list.Items {
				area, _ := item.Spec["area"].(map[string]any)
				runtime, _ := item.Status["runtime"].(map[string]any)
				if runtime == nil {
					continue
				}
				ready, _ := runtime["ready"].(bool)
				mapName, _ := area["map"].(string)
				if mapName == "" {
					mapName = "(unknown)"
				}
				count := toInt(runtime["players"])
				players.Online += count
				players.ByMap[mapName] += count
				if ready {
					players.ServersUp++
				}
			}
		}
	}

	peak, peakTS := updateSessionPeak(players.Online)
	peakOut := PlayerPeakSummary{SessionMax: peak}
	if !peakTS.IsZero() {
		peakOut.SessionAt = peakTS.UTC().Format(time.RFC3339)
	}

	jsonOK(w, OverviewSnapshot{
		Host:       host,
		Containers: containers,
		Players:    players,
		Peak:       peakOut,
	})
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
