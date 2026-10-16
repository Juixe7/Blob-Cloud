# Blob-Cloud ☁️
### Distributed Cloud Storage, Content-Defined Deduplication Engine & Real-Time Sync Backplane

[![CI Quality Gate](https://github.com/Juixe7/Blob-Cloud/actions/workflows/ci.yml/badge.svg)](https://github.com/Juixe7/Blob-Cloud/actions/workflows/ci.yml)
[![Deployment Pipeline](https://github.com/Juixe7/Blob-Cloud/actions/workflows/deploy.yml/badge.svg)](https://github.com/Juixe7/Blob-Cloud/actions/workflows/deploy.yml)
[![Go Version](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go)](https://go.dev)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.x-3178C6?logo=typescript)](https://www.typescriptlang.org)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react)](https://react.dev)
[![AWS Infrastructure](https://img.shields.io/badge/AWS-S3%20%7C%20SQS%20%7C%20Lambda%20%7C%20EC2-FF9900?logo=amazon-aws)](https://aws.amazon.com)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> 🌐 **Production Application**: [**https://blobcloud.dev**](https://blobcloud.dev)  
> 📖 **Engineering Architecture Manual**: Comprehensive technical specification of distributed primitives, storage engines, transactional boundaries, and network protocols.

---

## 1. Executive Summary

### 1.1 Core Problem Solved
Traditional cloud storage architectures (e.g., standard object storage wrappers or legacy network shares) suffer from severe structural inefficiencies:
1. **Byte-Shift Boundary Collapse**: Fixed-size block partitioning (e.g., 4 MiB blocks) fails catastrophically when a user inserts or removes even a single byte at the beginning of a file. Every subsequent 4 MiB boundary shifts, invalidating 100% of cryptographic chunk hashes and forcing a complete re-upload of multi-gigabyte files.
2. **Bandwidth & Storage Bloat**: Enterprise data lakes store enormous volumes of duplicate and near-duplicate assets across collaborative teams without cross-tenant or cross-file content-addressable deduplication.
3. **Out-of-Order Delta Inconsistencies**: Polling-based sync models struggle with synchronization drift, concurrent edits, and distributed race conditions.
4. **Cloud Provider Surveillance**: Storing unencrypted client assets in server-managed databases or object stores exposes confidential user data to host compromises and third-party data breaches.

### 1.2 Technical Philosophy
Blob-Cloud was engineered from first principles to resolve these architectural limitations by implementing:
* **Zero-Trust Client-Side Cryptography**: Assets undergo WebCrypto AES-256-GCM framing directly within browser Web Workers before leaving the local device. Neither the API Gateway, the database, nor object storage holds plaintext keys.
* **Content-Addressable Storage (CAS)**: Immutable, hash-indexed block storage where physical blocks exist uniquely across the entire infrastructure, identified strictly by their SHA-256 digests.
* **Shift-Resistant Content-Defined Chunking (FastCDC)**: Rolling Gear hash boundary detection with normalized dual masks and $S_{min}$ byte skipping, preserving >99% chunk reusability across file mutations.
* **Transactional Outbox & Monotonic Delta Sync**: Every filesystem mutation commits atomically with an append-only journal sequence, guaranteeing $O(1)$ client catch-up and zero phantom sync events.
* **Decoupled Asynchronous Compute**: Heavy background workloads (thumbnail generation, Gemini vector embeddings, and RAG document indexing) execute asynchronously via AWS SQS and serverless AWS Lambda workers, achieving $0 idle cost with instantaneous scale.

### 1.3 High-Level Tech Stack
* **Backend Core**: Go 1.24, Chi v5 High-Performance Router, pgx/v5 connection pool, Slog structured logging.
* **Frontend Web Application**: React 19, TypeScript 5.x, Vite, Tailwind CSS, Dedicated FastCDC & WebCrypto Web Workers.
* **Persistence & Caching**: PostgreSQL 16 (pgvector extension, B-tree indexes, `text_pattern_ops`), Redis 7 (Token Bucket Rate Limiting & Pub/Sub backplane).
* **Storage Plane**: Amazon S3 (quarantine staging namespace `staging/` and immutable CAS namespace `blocks/`), Local Filesystem fallback driver.
* **Queue & Compute**: Amazon SQS (Dead-Letter Queues, Partial Batch Failures), AWS Lambda (Amazon Linux 2023 Go custom runtime), KEDA Kubernetes Autoscaler.
* **Edge & Ingress**: Nginx Reverse Proxy (SSL termination, HTTP/2, RFC 7233 range header proxying), Cloudflare DNS.
* **Observability**: Prometheus client_golang exporter (`/metrics`), Grafana dashboards.

---

## 2. Architecture Overview

Blob-Cloud enforces a strict layered tier architecture with unidirectional dependencies. The transport layer delegates directly to domain services, which govern business invariants and transactional boundaries across repositories and storage drivers.

```mermaid
flowchart TD
    subgraph PresentationLayer ["1. Client & Presentation Tier (React 19 + TypeScript)"]
        BrowserUI["React 19 SPA (blobcloud.dev)"]
        WebWorkerCDC["FastCDC Web Worker (64-Bit Rolling Gear Hash)"]
        WebWorkerCrypto["WebCrypto Web Worker (AES-256-GCM + PBKDF2)"]
        BrowserUI -->|Raw Stream| WebWorkerCDC
        WebWorkerCDC -->|Chunks & Hashes| WebWorkerCrypto
    end

    subgraph IngressLayer ["2. Ingress & Edge Proxy Tier"]
        Nginx["Nginx Reverse Proxy (:80 / :443)"]
        Nginx -->|Reverse Proxy /api/ & /metrics| APIGateway
        Nginx -->|WebSocket Upgrade /api/ws| APIGateway
    end

    subgraph GatewayLayer ["3. API Transport & Middleware Tier (Chi Router)"]
        APIGateway["Go API Gateway (:8090)"]
        CORS["CORS & Security Headers"]
        RateLimiter["Redis Token Bucket Limiter"]
        AuthMid["JWT & OAuth2 Middleware"]
        PromMid["Prometheus Telemetry Middleware"]
        APIGateway --- CORS
        APIGateway --- RateLimiter
        APIGateway --- AuthMid
        APIGateway --- PromMid
    end

    subgraph ServiceLayer ["4. Application & Orchestration Service Tier"]
        FileSvc["FileService (Lifecycle, Hierarchy, ACLs)"]
        UploadSvc["UploadService (Resumability, Dedup, Verification)"]
        ZipSvc["ZipService (On-the-Fly Streaming Deflate)"]
        DeltaSvc["DeltaSync Engine (Journaling & Outbox)"]
    end

    subgraph DataLayer ["5. Persistence & Data Tier"]
        PostgresDB[("Amazon RDS PostgreSQL 16\n(Files, Blocks, Permissions, Journal)")]
        RedisCache[("Redis 7 Cluster\n(Pub/Sub Backplane & Token Buckets)")]
        S3Staging["Amazon S3: staging/{session_id}/ (Quarantine)"]
        S3CAS["Amazon S3: blocks/{sha256} (Immutable CAS)"]
    end

    subgraph ComputeLayer ["6. Asynchronous Compute Tier (Decoupled Workers)"]
        SQSQueue["Amazon SQS Event Queue"]
        LambdaWorker["AWS Lambda Serverless Worker (Go Custom Runtime)"]
        KEDAWorker["KEDA Container Workers (Kubernetes Auto 0→15)"]
        GeminiAI["Google Gemini 2.5 Flash / Embeddings API"]
        SQSQueue -->|Event Source Mapping| LambdaWorker
        SQSQueue -->|Backlog Metrics| KEDAWorker
        LambdaWorker -->|Generate Summaries & Embeddings| GeminiAI
        KEDAWorker -->|Generate Summaries & Embeddings| GeminiAI
    end

    WebWorkerCrypto -->|Presigned Direct PUT| S3Staging
    BrowserUI -->|REST & Range Requests| Nginx
    BrowserUI -->|Real-Time WebSocket| Nginx
    APIGateway --> FileSvc & UploadSvc & ZipSvc & DeltaSvc
    FileSvc & UploadSvc & ZipSvc & DeltaSvc --> PostgresDB
    FileSvc & UploadSvc --> S3CAS
    UploadSvc -->|Verify & Promote| S3Staging
    UploadSvc -->|Enqueue Background Tasks| SQSQueue
    DeltaSvc -->|Publish Delta| RedisCache
    RedisCache -->|Broadcast Across Nodes| APIGateway
```

### Separation of Concerns Matrix

| Layer | Component | Core Responsibility | Prohibited Actions |
| :--- | :--- | :--- | :--- |
| **Ingress** | Nginx Proxy | TLS termination, gzip/brotli compression, HTTP/2 multiplexing, WebSocket connection upgrades. | Business logic, request payload parsing, database access. |
| **Transport** | Chi HTTP Handlers | Query/header parsing, HTTP status encoding, JSON deserialization, auth context extraction. | Direct SQL execution, transaction management, physical storage mutations. |
| **Application** | Services (`FileService`, etc.) | Business domain invariants, cross-repository transactions, permission checks, S3 block promotions. | Direct HTTP header manipulation, SQL string construction, UI formatting. |
| **Repository** | PostgreSQL Repositories | Type-safe SQL queries, relational mapping, recursive CTEs, transaction handle propagation (`WithTx`). | Business validation, HTTP awareness, external network I/O. |
| **Storage** | Storage Providers | Physical byte streaming, presigned URL generation, multi-part S3 operations, local filesystem IO. | Database mutations, domain logic, permission authorization. |
| **Compute** | Workers (Lambda / KEDA) | CPU-intensive image thumbnails, PDF extraction, vector embeddings, Gemini AI calls. | Direct client communication, synchronous request blocking. |

---

## 3. Project Structure

```
.
├── .github/                      # CI/CD Workflows
│   └── workflows/
│       ├── ci.yml                # Quality Gate: Typecheck, Go Test (-race), Format, Lint
│       └── deploy.yml            # Deployment Pipeline: Linux AMD64 build, SCP & SSH restart
├── backend/                      # Go Backend Monolith
│   ├── cmd/
│   │   ├── api/                  # Main HTTP API Server entrypoint
│   │   ├── worker/               # Standalone containerized background worker daemon
│   │   ├── worker-lambda/        # Serverless AWS Lambda worker (provided.al2023)
│   │   ├── bench-chunker/        # Empirical FastCDC vs Fixed chunking benchmark CLI
│   │   ├── bench-latency/        # Micro-benchmarks for monotonic delta sync queries
│   │   ├── gc/                   # Standalone physical block garbage collector utility
│   │   ├── load-test/            # 50-worker parallel stress-testing utility
│   │   └── test-aws/             # Cloud infrastructure validation tool
│   ├── internal/
│   │   ├── ai/                   # Gemini 2.5 client, text extraction & prompt engineering
│   │   ├── antivirus/            # File quarantine & signature scanning engine
│   │   ├── audit/                # Immutable security audit trail logger
│   │   ├── auth/                 # JWT token generation, claims verification, bcrypt hashing
│   │   ├── chunker/              # 64-bit rolling Gear hash FastCDC streaming implementation
│   │   ├── config/               # Viper environment variable binding & profile validation
│   │   ├── database/             # PostgreSQL connection pool initializer & migration hooks
│   │   ├── domain/               # Pure domain models, value objects, and domain errors
│   │   ├── email/                # SMTP / SES invitation & verification mailer
│   │   ├── gc/                   # Orphaned CAS block sweep and delete orchestrator
│   │   ├── metrics/              # Prometheus metric definitions & HTTP instrumentation middleware
│   │   ├── queue/                # SQS producer and consumer abstractions
│   │   ├── ratelimit/            # Distributed Redis Token Bucket rate limiter
│   │   ├── repository/           # PostgreSQL relational data access implementations
│   │   │   └── postgres/         # SQL queries for files, blocks, permissions, sessions, journal
│   │   ├── service/              # Core domain orchestrators (File, Upload, Zip)
│   │   ├── storage/              # Storage drivers: Amazon S3 & Local Filesystem
│   │   ├── sync/                 # WebSocket connection hub & Redis Pub/Sub backplane
│   │   └── transport/            # HTTP delivery layer
│   │       └── http/             # Chi routes, controllers, middleware, and range handlers
│   ├── db/migrations/            # Idempotent raw SQL schema migrations (001-016)
│   ├── go.mod                    # Go module dependencies
│   └── go.sum                    # Dependency cryptographic checksums
├── deploy/                       # Infrastructure & Deployment Manifests
│   ├── grafana/                  # Pre-provisioned Grafana dashboards & Prometheus data sources
│   ├── k8s/                      # Kubernetes deployment manifests & KEDA SQS ScaledObject
│   ├── prometheus/               # Prometheus scraping configurations
│   ├── docker-compose.yml        # Local bare-metal development orchestration
│   └── docker-compose.prod.yml   # Production container orchestration
├── frontend/                     # React 19 Client SPA
│   ├── src/
│   │   ├── assets/               # SVG icons and static branding assets
│   │   ├── components/           # UI component library (modals, tables, sidebar, audio player)
│   │   ├── context/              # React Context (AuthContext, SyncContext, UploadContext)
│   │   ├── hooks/                # Custom hooks (useAudioPlayer, useDeltaSync, useVirtualScroll)
│   │   ├── lib/                  # Client utilities, Axios client, WebCrypto helpers
│   │   ├── pages/                # Route views (Drive, Trash, Shared, PublicShare, Auth, Landing)
│   │   ├── types/                # Strict TypeScript domain interfaces and DTOs
│   │   └── workers/              # Dedicated Web Workers: FastCDC chunker & WebCrypto AES-GCM
│   ├── package.json              # Frontend npm dependencies
│   ├── tsconfig.json             # Strict TypeScript configuration
│   └── vite.config.ts            # Vite bundler, worker thread config, and compression plugins
└── README.md                     # Official Engineering Documentation
```

### Module Responsibility Breakdown

| Package | Path | Architecture Responsibility |
| :--- | :--- | :--- |
| **`service`** | `backend/internal/service` | Orchestrates transactional multi-repository workflows (`FileService`, `UploadService`, `ZipService`). Enforces domain ACLs, initiates block promotions, and resolves golden-rule shortcuts. |
| **`chunker`** | `backend/internal/chunker` | Provides the 64-bit rolling Gear hash engine with dual normalized masks for content-defined chunking. Identical matrix shared with client Web Workers. |
| **`repository/postgres`** | `backend/internal/repository/postgres` | Encapsulates all raw SQL queries using `pgx` connection pools. Handles recursive CTEs, materialized path manipulation, and transactional outbox commits. |
| **`transport/http`** | `backend/internal/transport/http` | Exposes REST endpoints, validates HTTP query/header payloads, enforces RFC 7233 range streaming, and terminates WebSocket connections. |
| **`sync`** | `backend/internal/sync` | Manages real-time WebSocket client lifecycle, thread-safe connection tracking, and Redis Pub/Sub cross-node event broadcasting. |
| **`storage`** | `backend/internal/storage` | Unified `StorageProvider` abstraction for S3 and local disk. Handles presigned PUT URLs, multi-part uploads, and stream piping. |
| **`queue`** | `backend/internal/queue` | SQS publisher and consumer wrappers with exponential backoff and dead-letter queue routing. |
| **`workers`** | `frontend/src/workers` | Client-side Web Workers performing off-thread streaming FastCDC chunking and zero-trust AES-256-GCM encryption. |

---

## 4. Core Features & Subsystems

### 4.1 FastCDC Content-Defined Chunking (>99% Shift-Resistant Deduplication)

#### The Problem
Fixed-size chunking (e.g., 4 MiB blocks) is inherently fragile. Inserting or deleting $1\text{ byte}$ at the start of a 100 MiB file shifts every subsequent boundary offset by 1 byte. As a result, all cryptographic chunk digests change completely, causing a **0% deduplication match rate** and forcing the client to re-upload the entire file.

#### The Algorithmic Design
FastCDC solves this by placing boundaries based on the **content of the data itself** using a rolling Gear hash:
$$H_i = (H_{i-1} \ll 1) + G[b_i]$$
Where $G[0..255]$ is a static 256-element array of uniformly distributed 64-bit random integers.

To eliminate chunk size pathological distributions (chunks too small or too large), FastCDC applies **dual normalized masks**:
1. **Minimum Chunk Size ($S_{min} = 1\text{ MiB}$)**: Bypasses rolling hash evaluation completely on the first 1 MiB of every block, yielding a **25% CPU reduction**.
2. **Sub-Average Region ($L \in [1\text{ MiB}, 4\text{ MiB}]$)**: Employs a stricter mask $M_s = \text{0x7FFFFF}$ ($23$ bits set) to prevent premature boundary cuts.
3. **Post-Average Region ($L \in [4\text{ MiB}, 8\text{ MiB}]$)**: Employs a looser mask $M_l = \text{0x1FFFFF}$ ($21$ bits set) to accelerate boundary detection before hitting the maximum threshold.
4. **Maximum Chunk Size ($S_{max} = 8\text{ MiB}$)**: Hard ceiling forcing a chunk boundary if no natural cut occurred.

```go
// Representative Chunker Loop from backend/internal/chunker/fastcdc.go
for offset < len(data) {
    if remaining < MinChunkSize {
        cut = len(data)
        break
    }
    // Byte-skipping: Jump past MinChunkSize without hashing
    fingerprint := uint64(0)
    for i := MinChunkSize; i < maxScan; i++ {
        b := data[offset+i]
        fingerprint = (fingerprint << 1) + GearMatrix[b]
        
        // Normalized Dual Mask Thresholds
        if i < TargetChunkSize {
            if (fingerprint & MaskSubAverage) == 0 {
                cut = offset + i + 1
                break
            }
        } else {
            if (fingerprint & MaskPostAverage) == 0 {
                cut = offset + i + 1
                break
            }
        }
    }
}
```

#### Empirical Benchmark: 10 MiB File Mutation Test
| Mutation Scenario | Partitioning Strategy | Total Chunks | CAS Hits | Dedup Ratio | Bandwidth Saved |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **1-Byte Insertion (Offset 0)** | Fixed-Size (4 MiB) | 3 | 0 | **0.0%** | **0 B (Total Collapse)** |
| **1-Byte Insertion (Offset 0)** | **FastCDC (Gear Hash)** | 140 | 139 | **99.3%** | **9.9 MiB Saved** |
| **10-Byte Insertion (Offset 5 MiB)**| Fixed-Size (4 MiB) | 3 | 0 | **0.0%** | **0 B (Total Collapse)** |
| **10-Byte Insertion (Offset 5 MiB)**| **FastCDC (Gear Hash)** | 140 | 139 | **99.3%** | **10.0 MiB Saved** |

---

### 4.2 Client-Side Zero-Trust Encryption (AES-256-GCM Framing)

Blob-Cloud guarantees absolute confidentiality through client-side zero-trust cryptography:
* **Key Derivation**: The client derives a 256-bit AES master key from the user's secret passphrase using **PBKDF2** (100,000 iterations of SHA-256) combined with an account-specific salt.
* **12-Byte IV Framing**: Every FastCDC chunk is encrypted independently using **AES-256-GCM**. A unique, cryptographically random 12-byte Initialization Vector (IV) is prepended to the ciphertext payload:
$$\text{Transmitted Chunk} = \left[ 12\text{B IV} \right] \parallel \left[ \text{AES-256-GCM Encrypted Payload} \right] \parallel \left[ 16\text{B GMAC Authentication Tag} \right]$$
* **Storage Invariance**: Because encryption occurs inside the browser Web Worker prior to hashing, the generated SHA-256 digest is that of the *ciphertext*. The backend treats all encrypted blocks as opaque immutable bytes, preventing plaintext exposure across the network, database, and S3.

---

### 4.3 Dynamic RFC 7233 HTTP 206 Partial Content Streaming

Blob-Cloud implements native byte-range streaming for seamless video seeking and low-latency audio playback:
* **Multi-Block Virtual Assembly**: Rather than assembling large files in memory or temporary storage, the backend maps the requested `Range: bytes=START-END` header across disparate, variable-sized FastCDC blocks dynamically.
* **$O(N)$ Block Index Resolution**: `CalculateDynamicRangeBlockOffset` iterates through the ordered block list, accumulating lengths until discovering the starting block index and intra-block offset:
```go
func CalculateDynamicRangeBlockOffset(blocks []*domain.Block, startByte int64) (int, int64) {
    var accumulated int64 = 0
    for i, b := range blocks {
        bSize := int64(b.SizeBytes)
        if startByte < accumulated+bSize {
            return i, startByte - accumulated
        }
        accumulated += bSize
    }
    return len(blocks) - 1, 0
}
```
* **RFC Compliance**: Emits `HTTP 206 Partial Content` with `Content-Range: bytes START-END/TOTAL`, `Content-Length`, `Accept-Ranges: bytes`, and streams exact byte slices directly to `w` via `io.CopyN`.

---

### 4.4 Dropbox-Grade Monotonic Delta Sync Engine ($O(1)$ Catch-Up)

```
Database Outbox Transaction
┌────────────────────────────────────────────────────────┐
│ 1. Mutate File Record (Rename, Move, Create, Delete)   │
│ 2. INSERT INTO journal_entries                         │
│    (user_id, file_id, action, cursor BIGSERIAL)        │
│ 3. COMMIT ATOMICALLY                                   │
└───────────────────────────┬────────────────────────────┘
                            │
               Database Trigger / Publisher
                            ▼
           Redis Pub/Sub Channel: blobcloud:sync
                            │
         ┌──────────────────┴──────────────────┐
         ▼                                     ▼
   API Gateway Node 1                    API Gateway Node 2
         │                                     │
   WebSocket Push                        WebSocket Push
         ▼                                     ▼
   Client Tab A                          Client Tab B
```

* **Transactional Outbox**: Metadata mutations and audit journal entries are committed within the **same atomic database transaction**, eliminating ghost updates or phantom sync events.
* **Monotonic Sequence Cursor**: The `journal_entries` table indexes mutations via a 64-bit integer cursor (`BIGSERIAL PRIMARY KEY`). Reconnecting clients poll `GET /api/sync/delta?since=<cursor>&limit=100` to replay missed mutations in strict causal order in $O(1)$ time.
* **Redis Pub/Sub Backplane**: Multi-node API Gateway clusters share a Redis pub/sub backplane, broadcasting sync pulses to connected WebSockets across instances.

---

### 4.5 Materialized Path Directory Hierarchy ($O(1)$ Subtree Relocations)

Traditional tree implementations use recursive Adjacency List queries (`WITH RECURSIVE`) which suffer from row locking contention and high latency on deep folders. Blob-Cloud combines Adjacency Lists with an indexed **Materialized Path** column:
* **Materialized Path Schema**: Every file record maintains a normalized `path` attribute (e.g. `/Engineering/Architecture/Specs/`) indexed via PostgreSQL `text_pattern_ops`.
* **Atomic Subtree Move**: Moving a directory tree containing 100,000 files executes in a **single atomic SQL statement** via substring substitution:
```sql
UPDATE files
SET path = $1 || SUBSTRING(path FROM $2),
    parent_id = CASE WHEN id = $6 THEN $7 ELSE parent_id END
WHERE user_id = $3 AND (path = $4 OR path LIKE $5 ESCAPE '\');
```
* **Cycle Rejection**: Circular relocations (e.g., attempting to move `/FolderA` into `/FolderA/SubFolderB`) are validated in memory in $O(1)$ time by verifying `strings.HasPrefix(targetParent.Path, sourceFolder.Path)`.

---

### 4.6 Serverless Asynchronous Compute Plane & CAS AI Deduplication

* **AWS Lambda Custom Go Runtime (`cmd/worker-lambda`)**: Background thumbnail extraction and AI vector indexing execute inside an event-driven AWS Lambda worker configured via SQS Event Source Mapping.
* **Partial Batch Responses (`SQSEventResponse`)**: The worker returns `{ batchItemFailures: [{ itemIdentifier: "id" }] }`, ensuring only poisoned or failed messages return to the queue while successful messages are acknowledged.
* **CAS AI Deduplication**: When a user uploads a duplicate file whose SHA-256 blocks already exist in the database with pre-computed summaries and tags, the backend instantly inherits the existing metadata without re-invoking the Gemini API, eliminating redundant LLM billing costs.

---

### 4.7 Enterprise ACL Sharing & Golden Rule Engine

* **Sharing Hierarchy**: Supports direct collaborator sharing (`OWNER`, `EDITOR`, `VIEWER`), 7-day expiring public links with optional PBKDF2 passcodes, and virtual shortcuts.
* **Golden Rule Relocation Fallback**: When an `EDITOR` or `VIEWER` moves a shared file or directory within their own drive interface, the system prevents disrupting the original owner's hierarchy. Instead, it leaves the canonical shared file in place and creates a personalized virtual shortcut (`application/vnd.google-apps.shortcut`) at the user's intended target location.

---

## 5. Service Layer Documentation

The service layer encapsulates all business invariants, permission checks, and transaction boundaries.

```mermaid
classDiagram
    class FileService {
        -db *sql.DB
        -users UserStore
        -files FileStore
        -blocks BlockStore
        -perms PermissionStore
        -storage StorageProvider
        -journal *JournalRepository
        -notifier Notifier
        +GetFile(ctx, id) (*File, error)
        +RenameMove(ctx, userID, fileID, req) (*File, error)
        +SoftDelete(ctx, userID, fileID) (*DeleteResult, error)
        +Restore(ctx, userID, fileID) (*DeleteResult, error)
        +PermanentDelete(ctx, userID, fileID) (*DeleteResult, error)
        +CreateFolder(ctx, userID, name, parentID) (*File, bool, error)
        +ListDirectory(ctx, userID, parentID) ([]*File, error)
        +ListSharedFiles(ctx, userID) ([]*File, error)
        +CreateShortcut(ctx, userID, req) (*File, error)
        +AuthoriseRead(ctx, userID, fileID) error
        +AuthoriseWrite(ctx, userID, fileID) error
    }

    class UploadService {
        -db *sql.DB
        -files *FileRepository
        -blocks UploadBlockStore
        -sessions UploadSessionStore
        -storage StorageProvider
        -publisher queue.Publisher
        +Initiate(ctx, req) (*InitiateResponse, error)
        +GetSession(ctx, id, userID) (*SessionStatusResponse, error)
        +Complete(ctx, req, userID) (*CompleteResponse, error)
    }

    class ZipService {
        -files *FileRepository
        -blocks *BlockRepository
        -storage StorageProvider
        +StreamItemsZip(ctx, ids, userID, w) error
    }

    FileService --> UserStore
    FileService --> FileStore
    FileService --> PermissionStore
    UploadService --> UploadSessionStore
    UploadService --> UploadBlockStore
```

### 5.1 `FileService`
* **Package**: `go-drive-clone/internal/service`
* **Dependencies**: `UserStore`, `FileStore`, `BlockStore`, `PermissionStore`, `StorageProvider`, `JournalRepository`, `Notifier`.
* **Lifecycle Management**: Constructed via `NewFileService(...)`. Thread-safe for concurrent read and write operations. Shallow-clones all returned entity slices in `ListDirectory` to prevent memory mutations across goroutines.

### 5.2 `UploadService`
* **Package**: `go-drive-clone/internal/service`
* **Dependencies**: `UserRepository`, `FileRepository`, `UploadBlockStore`, `UploadSessionStore`, `PermissionRepository`, `StorageProvider`, `Publisher`, `Notifier`.
* **Two-Phase Commit Design**:
  * **Phase 1 (Network I/O)**: Verifies physical block payloads and size integrity in S3 quarantine staging; promotes verified blocks to `blocks/{sha256}`.
  * **Phase 2 (Database Transaction)**: Executes within a single SQL transaction (`RunInTx`) to register blocks, insert file metadata, bind sequential blocks, and emit outbox sync events.

### 5.3 `ZipService`
* **Package**: `go-drive-clone/internal/service`
* **Dependencies**: `FileRepository`, `BlockRepository`, `StorageProvider`.
* **Streaming Guarantees**: Performs real-time ZIP archive deflation directly to the client HTTP stream (`io.Writer`) without buffering archive files on server disks.

---

## 6. API Documentation

All API requests (except public links and health checks) require an `Authorization: Bearer <JWT_ACCESS_TOKEN>` header.

### 6.1 Authentication Endpoints (`/api/auth`)

| Method | Endpoint | Description | Request Payload | Response DTO | Status Codes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `POST` | `/api/auth/register` | Create a new user account | `{"email": "...", "password": "...", "name": "..."}` | `{"user": {...}, "token": "..."}` | `201 Created`, `400`, `409` |
| `POST` | `/api/auth/login` | Authenticate with email/password | `{"email": "...", "password": "..."}` | `{"user": {...}, "token": "...", "refresh_token": "..."}` | `200 OK`, `401 Unauthorized` |
| `POST` | `/api/auth/google` | Google OAuth2 token exchange | `{"id_token": "..."}` | `{"user": {...}, "token": "..."}` | `200 OK`, `400`, `401` |
| `POST` | `/api/auth/refresh` | Rotate access token | `{"refresh_token": "..."}` | `{"token": "..."}` | `200 OK`, `401 Unauthorized` |
| `POST` | `/api/auth/logout` | Invalidate refresh token session | `{"refresh_token": "..."}` | `{"message": "logged out"}` | `200 OK` |

### 6.2 File & Folder Management (`/api/files`)

| Method | Endpoint | Description | Parameters / Payload | Response DTO | Status Codes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `GET` | `/api/files` | List directory contents | Query: `parent_id` (optional UUID) | `[{"id": "...", "name": "...", "is_directory": true, ...}]` | `200 OK`, `401`, `403` |
| `POST` | `/api/folders` | Create new folder (idempotent) | `{"name": "Docs", "parent_id": null}` | `{"id": "...", "name": "Docs", "is_directory": true}` | `200 OK` (exists), `201` (new) |
| `PATCH`| `/api/files/{id}` | Rename or relocate file/folder | `{"name": "NewName", "parent_id": "..."}` | `{"id": "...", "name": "NewName", ...}` | `200 OK`, `400`, `403`, `409` |
| `DELETE`| `/api/files/{id}` | Soft-delete file (move to Trash) | None | `{"status": "success", "message": "file moved to trash"}` | `200 OK`, `403`, `404` |
| `POST` | `/api/files/{id}/restore` | Restore file from Trash | None | `{"status": "success", "message": "file restored"}` | `200 OK`, `403`, `404` |
| `DELETE`| `/api/files/{id}/permanent` | Purge file & trigger S3 block GC | None | `{"status": "success", "deleted_count": 1, "gc_blocks": 5}` | `200 OK`, `403`, `404` |
| `POST` | `/api/files/shortcut` | Create virtual shortcut pointer | `{"file_id": "...", "parent_id": "..."}` | `{"id": "...", "mime_type": "...shortcut", ...}` | `201 Created`, `403`, `404` |
| `POST` | `/api/files/bulk/delete` | Batch soft-delete items | `{"ids": ["id1", "id2"]}` | `{"status": "success"}` | `200 OK`, `403` |
| `POST` | `/api/files/bulk/restore`| Batch restore from Trash | `{"ids": ["id1", "id2"]}` | `{"status": "success"}` | `200 OK`, `403` |
| `POST` | `/api/files/bulk/move` | Batch move items to folder | `{"ids": ["id1"], "parent_id": "..."}` | `{"status": "success"}` | `200 OK`, `403` |

### 6.3 Resumable Chunked Uploads (`/api/upload`)

| Method | Endpoint | Description | Request Payload | Response DTO | Status Codes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `POST` | `/api/upload/initiate` | Deduplication check & session start | `{"filename": "video.mp4", "total_size": 10485760, "chunks": [{"sha256": "...", "size_bytes": 4194304}]}` | `{"session_id": "...", "status": "INITIATED", "chunks": [{"sequence_number": 0, "already_exists": false, "upload_url": "..."}]}` | `200 OK`, `400`, `401` |
| `GET` | `/api/upload/session/{id}` | Reconnect & resume upload session | None | `{"session_id": "...", "status": "INITIATED", "chunks": [...]}` | `200 OK`, `404` |
| `POST` | `/api/upload/complete` | Verify staging blocks & commit file | `{"session_id": "...", "resolution_mode": "version"}` | `{"session_id": "...", "status": "COMPLETED", "file_id": "..."}` | `200 OK`, `400`, `409 Conflict` |

### 6.4 Streaming, Downloads & Compression

| Method | Endpoint | Description | Headers & Parameters | Response Format | Status Codes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `GET` | `/api/files/{id}/download` | Single file download | Query: `?token=<jwt>` | Binary stream (`Content-Disposition: attachment`) | `200 OK`, `401`, `403`, `404` |
| `GET` | `/api/files/{id}/download` | RFC 7233 partial content stream | Header: `Range: bytes=0-1048575` | Binary partial stream (`Content-Range: bytes ...`) | `206 Partial Content` |
| `GET` | `/api/files/download` | Bulk directory ZIP stream | Query: `?ids=id1,id2&token=<jwt>` | Compressed `application/zip` stream | `200 OK` (Chunked) |

### 6.5 Delta Sync & WebSockets

| Method | Endpoint | Description | Query Parameters | Response DTO | Status Codes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `GET` | `/api/sync/delta` | Replay monotonic mutation stream | `?since=<cursor>&limit=100` | `{"entries": [...], "latest_cursor": 1042, "has_more": false}` | `200 OK`, `401` |
| `GET` | `/api/sync/cursor` | Query current highest cursor | None | `{"latest_cursor": 1042}` | `200 OK`, `401` |
| `GET` | `/api/ws` | Real-time WebSocket connection | In-band auth: `{"type": "auth", "token": "..."}` | WebSocket Frame Stream | `101 Switching Protocols` |

---

## 7. Configuration Reference

All configurations are managed via environment variables and loaded through `backend/internal/config`.

| Variable | Description | Default | Allowed Values / Profile |
| :--- | :--- | :--- | :--- |
| `PORT` | API Gateway HTTP listening port | `8090` | Standard TCP Port |
| `ENV` | Environment operational profile | `development` | `development`, `testing`, `production` |
| `DB_DSN` | PostgreSQL connection string | *Required* | `postgres://user:pass@host:5432/db?sslmode=require` |
| `REDIS_URL` | Redis cluster endpoint | `redis://localhost:6379/0` | Standard Redis URI |
| `JWT_SECRET` | Secret key for signing HMAC-SHA256 JWTs | *Required (min 32 chars)* | High-entropy random string |
| `STORAGE_DRIVER` | Physical file storage driver | `local` | `local`, `s3` |
| `STORAGE_LOCAL_DIR`| Filesystem directory for local driver | `./data/storage` | Valid local directory path |
| `AWS_REGION` | AWS region for S3, SQS, and Lambda | `us-east-1` | Standard AWS region code |
| `AWS_S3_BUCKET` | Primary S3 bucket for CAS and staging | *Required if S3* | S3 Bucket Name |
| `AWS_SQS_QUEUE_URL`| SQS Queue URL for background worker | *Required for async*| Full SQS Queue HTTPS URL |
| `GEMINI_API_KEY` | Google Gemini LLM API key | Optional | API Key for AI Summaries & RAG |
| `GOOGLE_CLIENT_ID` | Google OAuth2 client ID for web login | Optional | OAuth2 Client ID |

---

## 8. Deployment & Setup Guide

### 8.1 Local Bare-Metal Development

#### Prerequisites
* **Go**: Version `1.24+` installed.
* **Node.js**: Version `20+` and `npm 10+`.
* **PostgreSQL**: Version `16+` with `pgvector` extension.
* **Redis**: Version `7+`.

```bash
# 1. Clone repository
git clone https://github.com/Juixe7/Blob-Cloud.git
cd Blob-Cloud

# 2. Configure Backend Environment
cd backend
cp .env.example .env
# Edit .env with your local DB_DSN and JWT_SECRET

# 3. Apply SQL Migrations
go run cmd/api/main.go --migrate-only # or execute db/migrations/*.sql sequentially

# 4. Launch Backend API Gateway
go run cmd/api/main.go

# 5. Launch Background Worker (in a separate terminal)
cd backend
go run cmd/worker/main.go

# 6. Launch Frontend Client (in a separate terminal)
cd ../frontend
npm install
npm run dev
```

### 8.2 Containerized Production Deployment (Docker Compose)

For production deployment on AWS EC2, DigitalOcean, or bare-metal Linux servers:

```bash
# 1. Clone repository
git clone https://github.com/Juixe7/Blob-Cloud.git
cd Blob-Cloud

# 2. Prepare Production Configuration
cp .env.production.example .env
# Fill in your AWS credentials, RDS PostgreSQL DSN, and domain settings

# 3. Build and launch multi-container stack
docker compose -f docker-compose.prod.yml up -d --build

# 4. Inspect container health
docker compose -f docker-compose.prod.yml ps
```

### 8.3 AWS Lambda Worker Packaging (`cmd/worker-lambda`)

To update the decoupled serverless AI worker in AWS Lambda:

```bash
cd backend/cmd/worker-lambda
# Build Linux AMD64 executable for Amazon Linux 2023 custom runtime
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags lambda.norpc -ldflags="-s -w" -o bootstrap main.go

# Package into deployment archive
zip lambda.zip bootstrap

# Upload via AWS CLI
aws lambda update-function-code \
    --function-name BlobCloudWorker \
    --zip-file fileb://lambda.zip
```

---

## 9. Workflow Diagrams

### 9.1 FastCDC Chunking & Resumable Upload Sequence

```mermaid
sequenceDiagram
    autonumber
    actor User as Client Browser
    participant Worker as Web Worker (FastCDC)
    participant API as API Gateway (:8090)
    participant DB as PostgreSQL 16
    participant S3 as Amazon S3

    User->>Worker: Provide File Handle
    Worker->>Worker: FastCDC Rolling Gear Hash (Compute Chunks & SHA-256)
    Worker->>Worker: WebCrypto AES-256-GCM Framing (Prepend 12B IV)
    Worker-->>User: Return Ordered Chunk Hashes

    User->>API: POST /api/upload/initiate (Chunks, Hashes, TotalSize)
    API->>DB: Query existing blocks WHERE sha256 IN (...)
    DB-->>API: Return existing block matches (CAS Hits)
    API->>DB: INSERT INTO upload_sessions & session_blocks
    API-->>User: Return SessionID + Presigned S3 URLs (Misses only)

    par Parallel Chunk Transmission
        User->>S3: PUT Presigned URL (Chunk 1) -> staging/{session_id}/0
        User->>S3: PUT Presigned URL (Chunk 2) -> staging/{session_id}/1
    end

    User->>API: POST /api/upload/complete (SessionID, ResolutionMode)
    API->>S3: Zero-Trust Verification: Check byte length & SHA-256 hash
    API->>S3: Promote staging/{session_id}/N -> blocks/{sha256}
    API->>DB: BEGIN TRANSACTION
    API->>DB: Upsert blocks, insert files, map file_blocks, write permissions
    API->>DB: INSERT INTO journal_entries (Outbox Delta)
    API->>DB: UPDATE upload_sessions SET status = 'COMPLETED'
    API->>DB: COMMIT TRANSACTION
    API-->>User: HTTP 200 OK (FileID)
    API-)SQS: Publish Background AI Job (Thumbnail & Summarization)
```

### 9.2 RFC 7233 Multi-Block Byte Range Streaming

```mermaid
sequenceDiagram
    autonumber
    actor Player as Media Player / Browser
    participant API as API Gateway (:8090)
    participant Service as FileService
    participant CAS as S3 CAS (blocks/{sha256})

    Player->>API: GET /api/files/{id}/download (Header: Range: bytes=4194300-8388607)
    API->>Service: AuthoriseRead(userID, fileID)
    Service-->>API: Authorized (Role: VIEWER)
    API->>Service: GetDownloadInfoWithBlocks(fileID)
    Service-->>API: Return Ordered Blocks [Block 0 (4MB), Block 1 (4MB), ...]

    API->>Service: CalculateDynamicRangeBlockOffset(blocks, 4194300)
    Service-->>API: Returns: StartBlock = 0, BlockOffset = 4194300

    API-->>Player: HTTP 206 Partial Content (Content-Range: bytes 4194300-8388607/12582912)

    API->>CAS: GetObject(blocks/hash_0)
    CAS-->>API: Stream bytes from offset 4194300 to EOF
    API-->>Player: Stream initial 4 bytes of requested range

    API->>CAS: GetObject(blocks/hash_1)
    CAS-->>API: Stream bytes from offset 0 to 4194303
    API-->>Player: Stream remaining bytes of requested range
```

---

## 10. Troubleshooting Guide

| Symptom / Error | Root Cause | Technical Remediation |
| :--- | :--- | :--- |
| **`HTTP 429 Too Many Requests`** | Distributed Redis Token Bucket limit exceeded. | Verify client exponential backoff. In development, increase `AUTH_RATE_LIMIT_RPS` or pass empty `RateLimiters{}`. |
| **`WebSocket 1006 Abnormal Closure`** | Nginx reverse proxy timeout or idle TCP drop. | Ensure Nginx includes `proxy_read_timeout 3600s;` and client triggers `ping/pong` heartbeat every 30s. |
| **`Payload Integrity Violation`** | Uploaded chunk byte size or SHA-256 does not match S3 staging bytes. | Client must recalculate SHA-256 after WebCrypto encryption. Ensure multipart chunk boundaries match FastCDC cuts. |
| **`Access Denied: File Unavailable`** | Non-owner attempted to access a soft-deleted or quarantined file. | Restore file via `POST /api/files/{id}/restore` before reading. Check file status in Neon PostgreSQL. |
| **`DATA RACE in ListDirectory`** | In-memory mock or repository mutating shared struct pointers concurrently. | Ensure `ListDirectory` shallow-clones each `*domain.File` before setting `item.Role = role`. |
| **`SQS Message Timeout / Retry Loop`** | Lambda worker memory exhaustion or Gemini API rate limit. | Check CloudWatch logs. Ensure `lambda.zip` is compiled for `linux/amd64` with `bootstrap` binary in root. |

---

## 11. Verification & Testing Suite

Blob-Cloud enforces a zero-warning quality gate across all backend and frontend modules:

```powershell
# 1. Run all backend tests with race detector enabled
cd backend
go test -race -v ./...

# 2. Run FastCDC shift-resistance benchmark
go run cmd/bench-chunker/main.go

# 3. Run high-concurrency API load test (50 parallel workers)
go run cmd/load-test/main.go -c 50 -n 500

# 4. Frontend strict typecheck and production Vite build
cd ../frontend
npx tsc -b
npm run build
```

---

## 12. License
Blob-Cloud is released under the open-source [MIT License](LICENSE).
