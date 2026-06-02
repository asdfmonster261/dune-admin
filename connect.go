package main

import (
	"context"
	"fmt"
	"log"
	"os"
)

// Global handles initialized by connect(). nil-checked at every API call
// site so the server stays up even if one backend is unreachable.
var (
	globalDocker       *DockerClient
	globalOrchestrator *OrchestratorClient
	// globalDB           *pgxpool.Pool   // filled in by Phase 2/3
)

// connect bootstraps the docker socket + orchestrator HTTPS clients. The
// postgres pool is added in Phase 2 (Overview tab) when the first DB query
// actually needs it.
func connect(_ context.Context) error {
	var errs []error

	// Docker socket — disabled if not mounted.
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		globalDocker = NewDockerClient(dockerProject)
		log.Printf("docker socket mounted (project=%s)", dockerProject)
	} else {
		errs = append(errs, fmt.Errorf("docker.sock not mounted"))
	}

	// Orchestrator client. We don't ping it here; the first GET will report.
	if orchestratorURL != "" {
		globalOrchestrator = NewOrchestratorClient(orchestratorURL, orchestratorInsecure)
		log.Printf("orchestrator client configured (url=%s insecure=%v)", orchestratorURL, orchestratorInsecure)
	} else {
		errs = append(errs, fmt.Errorf("ORCHESTRATOR_URL not set"))
	}

	if len(errs) > 0 {
		return fmt.Errorf("connect: %v", errs)
	}
	return nil
}

// closeConnections releases any held resources before the server exits.
func closeConnections() {
	// Nothing yet — phase 2 will add the pgxpool close.
}
