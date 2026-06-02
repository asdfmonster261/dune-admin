# Multi-stage build for dune-admin.
#
# Stage 1: build the React/Vite frontend → web/dist/
# Stage 2: compile the Go backend with web/dist embedded via go:embed
# Stage 3: minimal runtime

FROM node:20-alpine AS web-builder

WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build


FROM golang:1.23 AS go-builder

WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
COPY --from=web-builder /web/dist ./web/dist

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o dune-admin .


FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /build/dune-admin .

EXPOSE 8080
ENTRYPOINT ["./dune-admin"]
