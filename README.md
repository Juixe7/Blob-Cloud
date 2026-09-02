# Blob-Cloud

[![Go Version](https://img.shields.io/github/go-mod/go-version/Hrushikesh-ramilla/Blob-Cloud?filename=backend%2Fgo.mod)](https://golang.org)
[![React](https://img.shields.io/badge/React-20232A?style=flat&logo=react&logoColor=61DAFB)](https://react.dev)
[![AWS](https://img.shields.io/badge/AWS-%23FF9900.svg?style=flat&logo=amazon-aws&logoColor=white)](https://aws.amazon.com)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-316192?style=flat&logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-DC382D?style=flat&logo=redis&logoColor=white)](https://redis.io)
[![Cloudflare](https://img.shields.io/badge/Cloudflare-F38020?style=flat&logo=Cloudflare&logoColor=white)](https://workers.cloudflare.com)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Blob-Cloud is a production-grade, cloud-native file storage and collaboration platform (Google Drive clone) built in **Go** with a **React** frontend. It runs on the AWS Free Tier (or Cloudflare R2 for zero egress fees) and implements enterprise patterns: direct-to-cloud uploads, global block-level deduplication, real-time WebSocket notifications, horizontal scalability via Redis Pub/Sub, and cryptographic edge integrity validation.

> **This repository documents an engineering upgrade campaign** applied to the original Blob-Cloud codebase. Every upgrade is a separate, verifiable commit with a test that proves the change works — aligned to Amazon's 16 Leadership Principles.

---

## System Architecture

```mermaid
sequenceDiagram
    autonumber
    actor Client as React Frontend
    participant CF as Cloudflare Worker (Edge Validator)
    participant API as Go Backend (EC2)
    participant Redis as Redis (Pub/Sub Backplane)
    participant DB as PostgreSQL (RDS)
    participant SQS as AWS SQS
    participant S3 as AWS S3 / Cloudflare R2
    participant Worker as Go Concurrent Workers

    Client->>API: POST /api/upload/initiate (filename, block hashes, sizes)
    API->>DB: Dedup check — blocks table (global SHA-256 index)
    DB-->>API: Existing hashes (dedup hits skip upload)
    Note over API: Small blocks (<=5 GiB): presigned PUT URL<br/>Large blocks (>5 GiB): MPU uploadID + part URLs
    API-->>Client: Session ID + Upload URL(s) per missing block

    opt Missing blocks only
        Client->>CF: PUT /blocks/<sha256> (direct upload)
        CF->>CF: Stream body → Web Crypto SHA-256 validation
        CF->>S3: Forward if hash matches (reject 400 on mismatch)
        S3-->>Client: 200 OK
    end

    Client->>API: POST /api/upload/complete (session_id, [etags for MPU])
    API->>DB: Verify blocks present → atomic tx: blocks+files+permissions
    DB-->>API: Commit
    API->>SQS: Publish thumbnail job
    API->>Redis: Publish UPLOAD_COMPLETED WS event
    API-->>Client: 200 OK + FILE_UPLOADED audit log entry

    loop Concurrent SQS workers
        Worker->>SQS: Long-poll (20s)
        SQS-->>Worker: Job payload
        Worker->>S3: Fetch blocks → thumbnail
        Worker->>S3: PUT /thumbnails/{fileID}.png
        Worker->>Redis: Publish THUMBNAIL_READY WS event
    end

    Redis-->>API: Fan-out to all connected nodes
    API-->>Client: WebSocket push notification
```

---

## Key Engineering Features

### 1. Direct-to-Cloud Uploads via CDN Presigned URLs

The Go backend never streams file data. It generates short-lived S3 presigned PUT URLs; the client uploads directly to the CDN edge. For files above 5 GiB (`commit 3e461ff`), the server initiates an S3 Multipart Upload and returns N presigned part URLs — the client PUTs each independently, then calls `complete` with the ETags.

### 2. Global Block-Level Deduplication

Files are sliced into 4 MB blocks client-side, fingerprinted with SHA-256. If two users upload a file containing identical blocks (shared templates, common libraries), only one physical copy lives in S3. The `E2E test (commit b82d643)` proves this: the second upload of the same file produces 0 block writes and triggers 2 dedup-hit metric increments.

### 3. Resumable Upload State Machine

Upload lifecycles are tracked as `(upload_sessions, session_blocks)` records. On reconnect, `GET /api/upload/session/{id}` returns fresh presigned URLs only for blocks not yet confirmed in storage — the client skips already-uploaded blocks and resumes exactly where it failed.

### 4. Hierarchical ACL with Recursive CTEs

A `permissions` table maps `(user_id, file_id, role)`. Access on a deeply-nested file walks up the folder tree using a PostgreSQL recursive CTE — O(depth) in the DB, not O(subtree) in application code.

### 5. Async Thumbnail Pipeline (SQS + Worker Pool)

Successful uploads publish to AWS SQS. A pool of Go workers long-polls, fetches the image blocks from S3, generates a 200×200 PNG in-memory using `golang.org/x/image`, and writes `thumbnails/{fileID}.png` back to S3. Workers honour graceful shutdown via `context.Context` cancellation.

### 6. Real-Time Notifications (WebSocket + Redis Backplane) `commit ea0f3bc`

Every client tab connects to `GET /api/ws`. The `Hub` tracks local connections. When an upload completes or a file is shared, the service calls `notifier.NotifyUser()`. In single-node mode this writes directly to the Hub; in multi-node mode (ECS, K8s) the `RedisBackplane` publishes to a Redis channel — every API pod subscribes and fans the event out to its local connections. One env var (`REDIS_URL`) switches modes; the server degrades gracefully when Redis is unreachable.

### 7. HTTP Rate Limiting `commit 80b6ef8`

Three rate-limit zones enforced in the chi router:

| Zone | Default | Protection target |
|------|---------|------------------|
| `/api/auth/*` | 10 req/min | Credential stuffing / brute force |
| `/api/upload/*` | 30 req/min | S3 presign cost |
| `/api/*` (general) | 120 req/min | General DoS |

Limits are per-IP, token-bucket in-memory (drop-in Redis sliding-window for multi-node). Env vars `RL_AUTH_RPM`, `RL_UPLOAD_RPM`, `RL_API_RPM` override defaults.

### 8. Immutable Audit Log `commit c488414`

Every destructive or sharing action writes an immutable row to `audit_logs`. `GET /api/files/{id}/history` exposes the paged trail. The audit write is fire-and-forget (background goroutine, own 5-second timeout) so it never adds latency to the API response. JSONB metadata column stores action-specific context without schema migrations.

### 9. Orphaned Block GC `commit aad248b`

The standalone `cmd/gc` binary compares S3 object keys against the live Postgres `blocks` table and deletes unreferenced objects. Runs safely with `--dry-run` by default. Designed to run as a nightly ECS scheduled task or Kubernetes CronJob.

### 10. Cloudflare Edge Integrity Validation `commit 3e461ff`

`workers/edge_validator.js` — a Cloudflare Worker that intercepts every PUT to `/blocks/<sha256>`. It streams the body through Web Crypto `SHA-256`, compares the digest against the hash in the URL, and rejects mismatches with 400 before a single byte reaches R2. This eliminates data-corruption and hash-swap attacks at zero application-server cost (~$0.30/million requests on Cloudflare's edge network).

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| **Backend** | Go 1.22+, `go-chi/chi` (router), `pgx` / `database/sql` (Postgres driver) |
| **Frontend** | React 18, TailwindCSS, Axios |
| **Database** | PostgreSQL 16 — transactional metadata, recursive CTEs for ACL |
| **Object Storage** | AWS S3 / Cloudflare R2 (zero-egress) |
| **CDN / Edge** | AWS CloudFront or Cloudflare — presigned URL termination + Workers |
| **Message Queue** | AWS SQS — async thumbnail pipeline |
| **Real-time** | WebSocket (`gorilla/websocket`) + Redis Pub/Sub backplane |
| **Observability** | Prometheus (`prometheus/client_golang`) |
| **Rate limiting** | `golang.org/x/time/rate` (in-memory) + Redis fixed-window (multi-node) |
| **Infrastructure** | AWS EC2 (or ECS), Docker, GitHub Actions CI |

---

## File & Package Map

```
Blob-Cloud/
├── backend/
│   ├── cmd/
│   │   ├── api/main.go          # entrypoint — wires all dependencies
│   │   └── gc/main.go           # standalone GC binary [commit aad248b]
│   ├── db/migrations/           # golang-migrate SQL files (9 migrations)
│   │   └── 000009_audit_log.up.sql  # audit_logs table [commit c488414]
│   └── internal/
│       ├── audit/               # audit.Logger interface + Entry type [commit c488414]
│       ├── config/              # env-var driven Config struct
│       ├── domain/              # StorageProvider + MultipartUploadProvider interfaces
│       ├── gc/                  # GC algorithm + interfaces [commit aad248b]
│       ├── metrics/             # Prometheus registry + middleware [commit 504249f]
│       ├── queue/               # SQS publisher + worker pool
│       ├── ratelimit/           # Limiter interface + InMemory/Redis impls [commit 80b6ef8]
│       ├── repository/postgres/ # all DB repositories (+ AuditRepository)
│       ├── service/             # UploadService (MPU-aware) [commit 3e461ff]
│       ├── storage/             # LocalStore + S3Storage (implements MPU)
│       ├── sync/                # Hub + RedisBackplane [commit ea0f3bc]
│       └── transport/http/      # chi router, all handlers (+ audit handlers)
└── workers/
    ├── edge_validator.js        # Cloudflare Worker — SHA-256 edge validation [commit 3e461ff]
    └── wrangler.toml            # Cloudflare Workers deploy config
```

---

## Local Setup

### Prerequisites
- Go 1.22+
- Node.js 18+
- Docker (local Postgres)
- Redis (optional — needed for backplane and Redis rate limiting)

### 1. Run the Database
```bash
docker run --name blobcloud-db \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=blobcloud \
  -p 5432:5432 \
  -d postgres:16-alpine
```

### 2. Configure Backend Environment

Create `backend/.env`:
```env
PORT=8080
ENV=development
LOCAL_STORAGE_DIR=./tmp/storage
BASE_URL=http://localhost:8080

# Database
DB_DSN=postgres://postgres:postgres@localhost:5432/blobcloud?sslmode=disable

# Storage (local for dev, s3 for AWS/R2)
STORAGE_PROVIDER=local
AWS_REGION=us-east-1
AWS_S3_BUCKET=your-bucket-name
AWS_ACCESS_KEY_ID=your-access-key
AWS_SECRET_ACCESS_KEY=your-secret-key

# SQS (leave empty to disable thumbnailing)
SQS_QUEUE_URL=https://sqs.us-east-1.amazonaws.com/your-account/your-queue
SQS_NUM_WORKERS=3
SQS_POLL_TIMEOUT_SEC=20

# Redis Pub/Sub backplane (leave empty for single-node mode)
REDIS_URL=redis://localhost:6379

# Rate limits (requests per minute; 0 = disabled)
RL_AUTH_RPM=10
RL_UPLOAD_RPM=30
RL_API_RPM=120

# JWT
JWT_SECRET=your-secret-key
```

### 3. Start the Backend
```bash
cd backend
go run cmd/api/main.go
# Migrations run automatically on first boot.
```

### 4. Run the GC Binary (dry-run by default)
```bash
cd backend
go run cmd/gc/main.go --dry-run
# To actually delete orphaned blocks:
go run cmd/gc/main.go --no-dry-run --min-age 24h
```

### 5. Start the Frontend
```bash
cd frontend
npm install
npm run dev
```

### 6. Deploy the Edge Validator Worker
```bash
cd workers
npm install -g wrangler
wrangler deploy
```

---

## Running Tests

```bash
cd backend

# Full suite (10 packages, ~12 seconds, no external services required)
go test ./... -count=1 -timeout 90s

# Package-level verbose
go test ./internal/ratelimit/... -v    # 7 rate-limit tests
go test ./internal/sync/...     -v    # 4 backplane tests
go test ./internal/gc/...       -v    # 5 GC tests
go test ./internal/audit/...    -v    # 5 audit tests
go test ./internal/service/...  -v    # E2E upload + 6 MPU tests

# Build both binaries
go build ./...
```

**Test results as of final verification:**

| Package | Tests | Status |
|---------|-------|--------|
| `internal/audit` | 5 | ✅ PASS |
| `internal/auth` | — | ✅ PASS |
| `internal/gc` | 5 | ✅ PASS |
| `internal/queue` | — | ✅ PASS |
| `internal/ratelimit` | 7 | ✅ PASS |
| `internal/repository/postgres` | — | ✅ PASS |
| `internal/service` | 6 + E2E | ✅ PASS |
| `internal/storage` | — | ✅ PASS |
| `internal/sync` | 4 | ✅ PASS |
| `internal/transport/http` | — | ✅ PASS |

---

## System Design Trade-offs & Scale Discussion

| Topic | Current State | Production Path |
|-------|--------------|----------------|
| **Database writes** | Single PostgreSQL instance | Shard by `user_id`; migrate to CockroachDB for global distribution |
| **WebSocket horizontal scale** | Redis Pub/Sub backplane (`commit ea0f3bc`) | Already solved — add more API pods and point `REDIS_URL` at a Redis cluster |
| **File size limit** | None — MPU handles terabyte files (`commit 3e461ff`) | Already solved — part count up to 10,000 × 100 MiB = ~1 TB per block |
| **Upload integrity** | SHA-256 validated at Cloudflare edge (`commit 3e461ff`) | Already solved — no corrupt bytes can reach R2 |
| **Orphaned block storage costs** | GC binary (`commit aad248b`) | Schedule as nightly ECS task / K8s CronJob |
| **Auth brute force** | Rate limiter at 10 req/min (`commit 80b6ef8`) | Add account lockout after N failures; CAPTCHA on auth endpoints |
| **Audit compliance** | Immutable `audit_logs` table (`commit c488414`) | Export to S3 Glacier for long-term retention; wire CloudTrail equivalent |

---

## Amazon Leadership Principles Alignment

Every upgrade commit was designed to demonstrate these principles concretely:

| LP | Evidence |
|----|---------|
| **Ownership** | GC binary (`aad248b`) closes a documented storage cost leak. Audit log (`c488414`) means "who deleted that?" has a 5-second answer. |
| **Insist on Highest Standards** | Every feature has a test for the error/edge case, not just the happy path. Cloudflare Worker validates SHA-256 at the edge — corruption can't survive to storage. |
| **Think Big** | Redis backplane (`ea0f3bc`) makes horizontal scale a one-env-var change. MPU (`3e461ff`) removes the file size ceiling with no API change. |
| **Invent & Simplify** | Backplane: 2 files, 130 lines, Hub unchanged. MPU: 30-line branch, LocalStore unmodified. Rate limiter: one interface, two implementations. |
| **Dive Deep** | Prometheus (`504249f`) instruments dedup hit/miss, worker duration, WS connections — not just HTTP status. Audit JSONB captures action-specific context without schema migrations. |
| **Frugality** | Edge validation costs ~$0.30/million requests on Cloudflare (not on EC2). MPU Abort on error prevents orphaned-parts storage cost. In-memory rate limiter (zero infra cost); Redis upgrade is one config line. |
| **Customer Obsession** | `GET /api/files/{id}/history` (`c488414`) exposes the audit trail as a user-facing timeline. Rate-limit headers (`80b6ef8`) let clients back off gracefully instead of hitting 429 loops. |

---

## Demo & Deployment

- 🔗 **Live URL:** *Coming soon*
- 🎥 **Walkthrough Video:** *Coming soon*
- 🐙 **Fork:** [Hrushikesh-ramilla/Blob-Cloud](https://github.com/Hrushikesh-ramilla/Blob-Cloud)
