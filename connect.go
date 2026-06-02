package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Global handles initialized by connect(). nil-checked at every API call
// site so the server stays up even if one backend is unreachable.
var (
	globalDB           *pgxpool.Pool
	globalDocker       *DockerClient
	globalOrchestrator *OrchestratorClient
)

// connect bootstraps the docker socket + orchestrator HTTPS clients + the
// postgres pool. Each is optional — the server stays up even if one fails;
// the per-handler nil checks just return 503 for affected endpoints.
func connect(ctx context.Context) error {
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

	// Postgres pool.
	if dbPass == "" {
		errs = append(errs, fmt.Errorf("DB_PASS not set; players/database tabs disabled"))
	} else {
		pool, err := connectDB(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("postgres: %w", err))
		} else {
			globalDB = pool
			log.Printf("postgres connected (%s:%d/%s schema=%s)", dbHost, dbPort, dbName, dbSchema)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("connect: %v", errs)
	}
	return nil
}

// connectDB opens a pgxpool, sets search_path on every new connection so
// callers can use unqualified table names against our schema by default.
func connectDB(ctx context.Context) (*pgxpool.Pool, error) {
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPass, dbName)
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 8
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx,
			fmt.Sprintf("SET search_path TO %s, public", pgx.Identifier{dbSchema}.Sanitize()))
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// closeConnections releases any held resources before the server exits.
func closeConnections() {
	if globalDB != nil {
		globalDB.Close()
		globalDB = nil
	}
}
