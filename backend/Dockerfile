# ==============================================================================
# Multi-Stage Production Dockerfile for Blob-Cloud Microservices
# Targets:
#   - api: Stateless HTTP API Gateway & WebSocket Router
#   - worker: Standalone SQS / Async Background Worker Daemon
#   - gc: Orphaned Block Garbage Collector CronJob
# ==============================================================================

FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build target binaries with optimizations and stripped debug symbols
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/gc ./cmd/gc

# ------------------------------------------------------------------------------
# Target: API Gateway Service
# ------------------------------------------------------------------------------
FROM alpine:3.21 AS api

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=builder /bin/api /app/api
COPY db/migrations /app/db/migrations

# Run as non-root user
USER nobody:nobody

EXPOSE 8090
ENTRYPOINT ["/app/api"]

# ------------------------------------------------------------------------------
# Target: Background Worker Daemon Service
# ------------------------------------------------------------------------------
FROM alpine:3.21 AS worker

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=builder /bin/worker /app/worker
COPY db/migrations /app/db/migrations

USER nobody:nobody

ENTRYPOINT ["/app/worker"]

# ------------------------------------------------------------------------------
# Target: Orphaned Block Garbage Collector
# ------------------------------------------------------------------------------
FROM alpine:3.21 AS gc

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=builder /bin/gc /app/gc

USER nobody:nobody

ENTRYPOINT ["/app/gc"]
