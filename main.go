// Package main is the entrypoint for dune-admin — a web UI for operating
// a self-hosted Dune Awakening battlegroup running on docker-compose.
//
// Connects to:
//   - postgres (DB queries for Players, Database, Storage tabs)
//   - dune-orchestrator at /apis/igw.funcom.com/v1/... (Battlegroup status)
//   - rabbitmq-game / rabbitmq-admin (announcements, GM commands)
//   - /var/run/docker.sock (logs streaming, container start/stop, exec)
//
// Serves API at /api/v1/* and embeds the React UI built into web/dist at /.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	version = "dev"

	listenAddr string
	dbHost     string
	dbPort     int
	dbUser     string
	dbPass     string
	dbName     string
	dbSchema   string

	orchestratorURL      string
	orchestratorInsecure bool

	dockerProject  string
	battlegroupNS  string
	allowedOrigins string
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBoolOr(key string, def bool) bool {
	v := strings.ToLower(os.Getenv(key))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func init() {
	loadDotEnv()
	flag.StringVar(&listenAddr, "addr", envOr("LISTEN_ADDR", ":8080"), "HTTP listen address")
	flag.StringVar(&dbHost, "dbhost", envOr("DB_HOST", "postgres"), "Postgres host")
	flag.IntVar(&dbPort, "dbport", envIntOr("DB_PORT", 5432), "Postgres port")
	flag.StringVar(&dbUser, "dbuser", envOr("DB_USER", "dune"), "Postgres user")
	flag.StringVar(&dbPass, "dbpass", envOr("DB_PASS", ""), "Postgres password")
	flag.StringVar(&dbName, "dbname", envOr("DB_NAME", "dune"), "Postgres database")
	flag.StringVar(&dbSchema, "schema", envOr("DB_SCHEMA", "dune"), "Postgres schema")
	flag.StringVar(&orchestratorURL, "orchestrator", envOr("ORCHESTRATOR_URL", "https://dune-orchestrator:6443"), "dune-orchestrator base URL")
	orchestratorInsecure = envBoolOr("ORCHESTRATOR_INSECURE", true)
	flag.StringVar(&dockerProject, "docker-project", envOr("DOCKER_PROJECT", "dune-server"), "docker-compose project label to filter on")
	flag.StringVar(&battlegroupNS, "battlegroup-ns", envOr("BATTLEGROUP_NS", ""), "Battlegroup namespace (funcom-seabass-<world-unique-name>)")
	flag.StringVar(&allowedOrigins, "allowed-origins", envOr("ALLOWED_ORIGINS", ""), "Comma-separated extra CORS origins (same-origin always allowed)")
}

func main() {
	flag.Parse()

	rootCtx, rootCancel := context.WithCancel(context.Background())

	if err := connect(rootCtx); err != nil {
		log.Printf("connect: %v — server will start anyway; use /api/v1/reconnect to retry", err)
	}

	// Background ops worker (Phase 7): polls /data/ops-state.json for
	// scheduled announcements + restarts and executes them when their
	// run_at has arrived.
	startOpsWorker(rootCtx)

	mux := http.NewServeMux()
	registerRoutes(mux)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           corsMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("dune-admin %s listening on %s", version, listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	rootCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	closeConnections()
	fmt.Println("bye")
}
