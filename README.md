# dune-admin

Web admin panel for a self-hosted Dune Awakening battlegroup running on
docker-compose.

Built as a sibling service to [dune-awakening-docker-compose](https://github.com/asdfmonster261/dune-awakening-docker-compose).
Talks directly to postgres, dune-orchestrator, RabbitMQ, and the Docker
socket on the dune-server compose network — no SSH layer.

## Stack

- Backend: Go (HTTP server + docker socket client + dune-orchestrator
  HTTPS client + postgres via pgx)
- Frontend: React 19 + TypeScript + Vite (embedded into the Go binary via
  `go:embed`)
- One image, one port (8080 inside the container, mapped to host 6847)

## Build + run (development)

```
docker compose up -d --build
```

Then open `http://localhost:6847`.

## Configuration

See `.env.example`. Defaults assume sibling-service deployment on
`dune-server_default` network.

## Status

Scaffolded (Phase 1). Tabs land in subsequent phases — see commit history
and task tracker.
