package main

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phase 10 follow-up — live Hagga sandstorm tracking.
//
// The game-server doesn't persist storm state anywhere external to its UE
// process. With LogSandStorm{,Manager}=VeryVerbose in UserEngine.ini, the
// manager emits enough on stdout to reconstruct every spawn:
//
//   LogSandStormManager: Verbose: Spawning Sandstorm, Start X Y End X Y
//   LogSandStormManager: VeryVerbose: Sandstorm Lifetime N
//   LogSandStorm:        Log:         Sandstorm BeginPlay on [HaggaBasin] ...
//   LogSandStormManager: Verbose: The next sandstorm is scheduled for: TS UTC
//   LogSandStormManager: Verbose: There will be no new sandstorms between A and B UTC
//   LogCoriolis:         Display: This Coriolis Cycle start date UTC: TS
//   LogCoriolis:         Display: Next Coriolis Cycle start date UTC: TS
//   LogCoriolis:         Display: Current Coriolis World Seed: N
//
// We tail the survival container, parse those, and keep an in-memory snapshot
// the map handler can serialize. Storms self-expire after spawn_time + lifetime.

type Storm struct {
	SpawnTime time.Time `json:"spawn_time"`
	StartX    float64   `json:"start_x"`
	StartY    float64   `json:"start_y"`
	EndX      float64   `json:"end_x"`
	EndY      float64   `json:"end_y"`
	Lifetime  float64   `json:"lifetime_seconds"`
	Map       string    `json:"map"`
}

type StormSnapshot struct {
	Active             []Storm    `json:"active"`
	NextScheduledAt    *time.Time `json:"next_scheduled_at"`
	BlackoutStart      *time.Time `json:"blackout_start"`
	BlackoutEnd        *time.Time `json:"blackout_end"`
	CoriolisCycleStart *time.Time `json:"coriolis_cycle_start"`
	CoriolisCycleEnd   *time.Time `json:"coriolis_cycle_end"`
	CoriolisWorldSeed  *int       `json:"coriolis_world_seed"`
}

var (
	stormMu     sync.RWMutex
	stormActive []Storm
	stormNext   *time.Time
	stormBlackoutStart *time.Time
	stormBlackoutEnd   *time.Time
	coriolisStart *time.Time
	coriolisEnd   *time.Time
	coriolisSeed  *int

	// Spawning + Lifetime arrive on adjacent lines. Buffer the pending
	// half-built storm here until BeginPlay commits it with its timestamp.
	pendingStormMu sync.Mutex
	pendingStorm   *Storm
)

func GetStormSnapshot() StormSnapshot {
	stormMu.RLock()
	defer stormMu.RUnlock()
	now := time.Now().UTC()
	alive := make([]Storm, 0, len(stormActive))
	for _, s := range stormActive {
		if now.Before(s.SpawnTime.Add(time.Duration(s.Lifetime * float64(time.Second)))) {
			alive = append(alive, s)
		}
	}
	return StormSnapshot{
		Active:             alive,
		NextScheduledAt:    stormNext,
		BlackoutStart:      stormBlackoutStart,
		BlackoutEnd:        stormBlackoutEnd,
		CoriolisCycleStart: coriolisStart,
		CoriolisCycleEnd:   coriolisEnd,
		CoriolisWorldSeed:  coriolisSeed,
	}
}

// startStormTailer launches a goroutine that subscribes to the survival
// game-server's docker logs and parses storm-related lines. It re-attaches
// with backoff if the container restarts or the stream errors out.
func startStormTailer(ctx context.Context) {
	go func() {
		backoff := 2 * time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			if globalDocker == nil {
				time.Sleep(backoff)
				continue
			}
			name, err := findSurvivalContainer(ctx)
			if err != nil || name == "" {
				time.Sleep(backoff)
				continue
			}
			if err := tailStormLogs(ctx, name); err != nil && ctx.Err() == nil {
				log.Printf("storm tailer: %v (reattaching in %s)", err, backoff)
				time.Sleep(backoff)
			}
		}
	}()
}

func findSurvivalContainer(ctx context.Context) (string, error) {
	cs, err := globalDocker.ListContainers(ctx, "game-server-survival")
	if err != nil {
		return "", err
	}
	for _, c := range cs {
		if c.State == "running" {
			return c.Name, nil
		}
	}
	return "", nil
}

func tailStormLogs(ctx context.Context, container string) error {
	// "all" replays the entire log buffer before following — needed so
	// that on a dune-admin restart we can still recover the startup-only
	// LogCoriolis cycle anchors (they fire once at game-server boot and
	// scroll off the default 300-line tail within minutes of busy play).
	ch, cancel, err := globalDocker.LogsStreamWithTail(ctx, container, true, "all")
	if err != nil {
		return err
	}
	defer cancel()
	for line := range ch {
		parseStormLine(line.Text)
	}
	return nil
}

var (
	reSpawning   = regexp.MustCompile(`Spawning Sandstorm, Start (-?[\d.]+) (-?[\d.]+) End (-?[\d.]+) (-?[\d.]+)`)
	reLifetime   = regexp.MustCompile(`Sandstorm Lifetime ([\d.]+)`)
	reBeginPlay  = regexp.MustCompile(`Sandstorm BeginPlay on \[(\w+)\]`)
	reNextStorm  = regexp.MustCompile(`The next sandstorm is scheduled for: (\d{4}\.\d{2}\.\d{2}-\d{2}\.\d{2}\.\d{2}) UTC`)
	reBlackout   = regexp.MustCompile(`There will be no new sandstorms between (\d{4}\.\d{2}\.\d{2}-\d{2}\.\d{2}\.\d{2}) and (\d{4}\.\d{2}\.\d{2}-\d{2}\.\d{2}\.\d{2}) UTC`)
	reCycleStart = regexp.MustCompile(`This Coriolis Cycle start date UTC: (\d{4}\.\d{2}\.\d{2}-\d{2}\.\d{2}\.\d{2})`)
	reCycleNext  = regexp.MustCompile(`Next Coriolis Cycle start date UTC: (\d{4}\.\d{2}\.\d{2}-\d{2}\.\d{2}\.\d{2})`)
	reSeed       = regexp.MustCompile(`Current Coriolis World Seed: (\d+)`)
	reLogStamp   = regexp.MustCompile(`\[(\d{4}\.\d{2}\.\d{2}-\d{2}\.\d{2}\.\d{2}):\d{3}\]`)
)

const stormTimeFmt = "2006.01.02-15.04.05"

func parseStormLine(text string) {
	if m := reSpawning.FindStringSubmatch(text); m != nil {
		sx, _ := strconv.ParseFloat(m[1], 64)
		sy, _ := strconv.ParseFloat(m[2], 64)
		ex, _ := strconv.ParseFloat(m[3], 64)
		ey, _ := strconv.ParseFloat(m[4], 64)
		pendingStormMu.Lock()
		pendingStorm = &Storm{StartX: sx, StartY: sy, EndX: ex, EndY: ey}
		pendingStormMu.Unlock()
		return
	}
	if m := reLifetime.FindStringSubmatch(text); m != nil {
		lt, _ := strconv.ParseFloat(m[1], 64)
		pendingStormMu.Lock()
		if pendingStorm != nil {
			pendingStorm.Lifetime = lt
		}
		pendingStormMu.Unlock()
		return
	}
	if m := reBeginPlay.FindStringSubmatch(text); m != nil {
		// Use the in-line UE timestamp as ground truth; falls back to
		// wall-clock if we somehow miss it.
		ts := parseStampOrNow(text)
		pendingStormMu.Lock()
		if pendingStorm != nil {
			s := *pendingStorm
			s.SpawnTime = ts
			s.Map = m[1]
			pendingStorm = nil
			pendingStormMu.Unlock()
			stormMu.Lock()
			stormActive = append(stormActive, s)
			pruneExpiredLocked()
			stormMu.Unlock()
		} else {
			pendingStormMu.Unlock()
		}
		return
	}
	if m := reNextStorm.FindStringSubmatch(text); m != nil {
		if ts, err := time.Parse(stormTimeFmt, m[1]); err == nil {
			stormMu.Lock()
			stormNext = &ts
			stormMu.Unlock()
		}
		return
	}
	if m := reBlackout.FindStringSubmatch(text); m != nil {
		a, errA := time.Parse(stormTimeFmt, m[1])
		b, errB := time.Parse(stormTimeFmt, m[2])
		if errA == nil && errB == nil {
			stormMu.Lock()
			stormBlackoutStart = &a
			stormBlackoutEnd = &b
			stormMu.Unlock()
		}
		return
	}
	if m := reCycleStart.FindStringSubmatch(text); m != nil {
		if ts, err := time.Parse(stormTimeFmt, m[1]); err == nil {
			stormMu.Lock()
			coriolisStart = &ts
			stormMu.Unlock()
		}
		return
	}
	if m := reCycleNext.FindStringSubmatch(text); m != nil {
		if ts, err := time.Parse(stormTimeFmt, m[1]); err == nil {
			stormMu.Lock()
			coriolisEnd = &ts
			stormMu.Unlock()
		}
		return
	}
	if m := reSeed.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			stormMu.Lock()
			coriolisSeed = &n
			stormMu.Unlock()
		}
		return
	}
}

func parseStampOrNow(text string) time.Time {
	if m := reLogStamp.FindStringSubmatch(text); m != nil {
		// UE log stamps are UTC by convention (the LogCoriolis cycle lines
		// say "...UTC" explicitly).
		if ts, err := time.ParseInLocation(stormTimeFmt, m[1], time.UTC); err == nil {
			return ts
		}
	}
	return time.Now().UTC()
}

// pruneExpiredLocked removes storms whose spawn_time + lifetime has passed.
// Caller holds stormMu write lock. We prune on every commit so the active
// list never grows past a couple entries even on long-running tailers.
func pruneExpiredLocked() {
	now := time.Now().UTC()
	live := stormActive[:0]
	for _, s := range stormActive {
		end := s.SpawnTime.Add(time.Duration(s.Lifetime * float64(time.Second)))
		if now.Before(end) {
			live = append(live, s)
		}
	}
	stormActive = live
}

// parseStormLineForTest exposes the parser to unit tests.
func parseStormLineForTest(text string) { parseStormLine(strings.TrimSpace(text)) }
