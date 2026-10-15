# SECTION 2: CODEBASE MAP, FILE-BY-FILE CHEAT SHEET & END-TO-END REQUEST TRACES
*Part 3 of 7 of the Blob-Cloud Interview Master Series*

---

> **Why this section is critical for your Technical Resume Round:**
> In technical interviews, interviewers often ask:
> - *"How did you structure your Go backend packages and why?"*
> - *"If we open your GitHub repository right now, which file has the FastCDC algorithm? Which file handles the database transaction for uploads?"*
> - *"Trace what happens in your code from the moment a user drops a file in React until the row commits in Postgres and Lambda wakes up."*
>
> Having this mental map lets you talk about your codebase like the architect who knows where every single bolt and wire lives.

---

## 2.1 — How to Explain Your Project Architecture (Clean / Layered Architecture)

### 🧠 Plain-English Intuition
If you write SQL queries and S3 API calls directly inside your HTTP handler functions, your codebase quickly turns into spaghetti: you can't unit-test business logic without spinning up an HTTP server, and you can't reuse upload/delete logic inside a CLI tool or a background worker.

Instead, your Go backend is organized in **5 strict inward-pointing layers** under `backend/`:
1. **Entrypoints (`cmd/`):** Tiny `main.go` files that read environment variables (`internal/config`), connect to Postgres/Redis/R2/SQS, and wire the dependencies together.
2. **HTTP Transport Layer (`internal/transport/http/`):** Chi router (`router.go`), middleware (JWT auth, rate limiting, Prometheus metrics), and HTTP handlers. Handlers **only** parse HTTP requests/headers, call a `Service` or `Repository`, and write JSON/stream responses.
3. **Application / Service Layer (`internal/service/`):** Where multi-step orchestration and **database transaction boundaries (`db.BeginTx`)** live (`upload_service.go`, `file_service.go`, `zip_service.go`).
4. **Domain Layer (`internal/domain/`):** Pure Go structs (`File`, `Block`, `UploadSession`, `JournalEntry`) and interfaces (`StorageProvider`). Has **zero** external dependencies.
5. **Infrastructure & Repository Adapters (`internal/repository/postgres/`, `internal/storage/`, `internal/queue/`, `internal/sync/`, `internal/ratelimit/`):** Concrete implementations for PostgreSQL SQL queries, Cloudflare R2 / S3, Amazon SQS + ClamAV + Gemini AI, WebSockets + Redis Pub/Sub, and Redis Lua rate limiting.

### 🎙️ Verbatim Script: "How Is Your Codebase Structured?"
> *"I structured the Go backend using Clean Architecture under `internal/` so HTTP handlers, business orchestration, and infrastructure adapters are cleanly separated.*
>
> *Under `cmd/`, I have four separate entrypoints compiled from the same Go module: `cmd/api` for the main stateless HTTP and WebSocket server, `cmd/worker-lambda` for the serverless AWS Lambda SQS consumer, `cmd/gc` for the standalone CAS garbage collector, and `cmd/bench-chunker` for benchmarking FastCDC deduplication.*
>
> *Inside `internal/`, requests flow through five layers: `transport/http` handles the Chi router, JWT validation, and Redis rate-limiting middleware; `service` (`upload_service.go`, `file_service.go`, `zip_service.go`) holds the core business workflows and ACID transaction boundaries; `domain` defines pure structs and interfaces like `StorageProvider`; `repository/postgres` contains all parameterized SQL queries; and specialized packages (`chunker`, `storage`, `sync`, `queue`, `antivirus`, `ai`, `ratelimit`, `metrics`) encapsulate R2, SQS, ClamAV, Gemini embeddings, WebSockets, and Prometheus metrics."*

---

## 2.2 — Complete Annotated Directory Tree (What Every File Does)

```text
Blob-Cloud/
├── backend/
│   ├── cmd/                                        # 4 EXECUTABLE ENTRYPOINTS
│   │   ├── api/main.go                             # Stateless HTTP/WS API server (wires DB, Redis, R2, SQS, Hub)
│   │   ├── worker-lambda/main.go                   # Serverless AWS Lambda handler triggered by Amazon SQS batches
│   │   ├── gc/main.go                              # Standalone Garbage Collector CLI for expired sessions & orphan blocks
│   │   └── bench-chunker/main.go                   # 90-line FastCDC vs Fixed-Size 4MB deduplication test on a 100 MB file
│   │
│   ├── db/migrations/                              # 20 SEQUENTIAL SQL MIGRATIONS (000001 to 000020)
│   │   ├── 000001..000007_*.sql                    # Users, files, blocks, file_blocks, sessions, trash, OAuth, shortcuts
│   │   ├── 000008_zero_trust_verification.up.sql   # Quarantine staging & upload_session_blocks tracking
│   │   ├── 000010_versioning_and_sessions.up.sql   # file_versions table (version_number, chunk_hashes[])
│   │   ├── 000011_ai_semantic_search.up.sql        # pgvector extension & file_embeddings / file_chunks (vector(768))
│   │   ├── 000013_clamav_quarantine.up.sql         # Malware scan status tracking
│   │   ├── 000017_journal_entries.up.sql           # Append-only journal_entries table with BIGSERIAL cursor
│   │   ├── 000018_materialized_paths.up.sql        # Materialized path column & text_pattern_ops B-Tree index
│   │   └── 000020_add_unique_filename_indexes.sql  # Partial unique index on active (user_id, parent_id, name)
│   │
│   └── internal/
│       ├── chunker/                                # FASTCDC CONTENT-DEFINED CHUNKING ENGINE
│       │   ├── fastcdc.go                          # Rolling Gear hash chunker (1MB min, 4MB avg, 8MB max, dual masks)
│       │   ├── gear_table.go                       # 256-entry randomized uint64 lookup table
│       │   └── fastcdc_test.go                     # Cross-language hash parity & boundary determinism tests
│       │
│       ├── domain/                                 # PURE DOMAIN MODELS & INTERFACES
│       │   ├── file.go, block.go, upload.go        # Core structs for File, Block, UploadSession, ConflictMode
│       │   ├── journal.go, user.go                 # Sync journal event types, User & Session models
│       │   └── storage.go                          # StorageProvider interface (PresignPut, GetObject, PromoteObject)
│       │
│       ├── service/                                # APPLICATION ORCHESTRATION & TRANSACTION BOUNDARIES
│       │   ├── upload_service.go                   # InitiateSession (CAS dedup) & CompleteSession (32KB verify + ACID TX)
│       │   ├── file_service.go                     # Subtree moves, bulk trash/restore/hard-delete, CAS orphan sweep
│       │   └── zip_service.go                      # Constant-memory streaming ZIP archive of entire folder subtrees
│       │
│       ├── transport/http/                         # HTTP ROUTER, MIDDLEWARE & REST/WS HANDLERS
│       │   ├── router.go & handlers.go             # Chi router setup, CORS, rate limit wiring, auth middleware, notifyUser
│       │   ├── upload_handlers.go                  # POST /api/upload/initiate, POST /api/upload/complete, session status
│       │   ├── file_handlers.go                    # File/folder CRUD, HTTP 206 Range streaming, 16-block access re-check
│       │   ├── filelist_handlers.go                # Paginated folder listing, trash listing, recent & starred views
│       │   ├── file_version_handlers.go            # Version history listing & atomic version restore
│       │   ├── share_handlers.go                   # Email RBAC invitations (OWNER/EDITOR/VIEWER) & accept/decline flow
│       │   ├── public_handlers.go                  # Public share links, Bcrypt password check, public HTTP 206 streaming
│       │   ├── sync_handlers.go                    # GET /api/sync/delta?cursor=X & cursor drift auto-healing
│       │   ├── websocket_handlers.go               # GET /ws?token=<jwt> upgrade & client read/write pumps
│       │   ├── ai_handlers.go                      # Hybrid semantic search (/api/files/search) & AI document summaries
│       │   └── auth_handlers.go & session_handlers.go # Login, Register, OTP, Google OAuth, JWT refresh & device revoke
│       │
│       ├── repository/postgres/                    # RAW PARAMETERIZED SQL REPOSITORIES
│       │   ├── dbtx.go                             # Common DBTX interface so queries work on both *sql.DB and *sql.Tx
│       │   ├── file.go                             # Materialized path moves, pg_advisory_xact_lock, findOrphanedHashes, search
│       │   ├── block.go                            # Batch CAS lookup (`WHERE sha256 = ANY($1)`) and block upserts
│       │   ├── upload_session.go                   # Upload session & upload_session_blocks state machine queries
│       │   ├── journal.go                          # BIGSERIAL cursor append & indexed delta range fetch
│       │   ├── permission.go & shareable_link.go   # Recursive CTE ancestry permission checks & public link tokens
│       │   └── user.go, session_repository.go, audit.go # User accounts, device sessions, and security audit logs
│       │
│       ├── storage/                                # OBJECT STORAGE PROVIDERS
│       │   ├── s3.go                               # Cloudflare R2 / AWS S3 client (Presign, Head, GetStream, PromoteObject)
│       │   └── local.go                            # Local disk fallback for offline unit testing
│       │
│       ├── sync/                                   # REAL-TIME WEBSOCKET & REDIS PUB/SUB ENGINE
│       │   ├── hub.go                              # Actor-model WebSocket Hub (single-goroutine channel select loop)
│       │   └── backplane.go                        # Multi-node Redis Pub/Sub wrapper with `node_id` UUID echo suppression
│       │
│       ├── queue/                                  # ASYNC BACKGROUND WORKER PIPELINE
│       │   ├── publisher.go & worker.go            # Amazon SQS publisher & 20s long-polling consumer
│       │   └── processor.go                        # Job orchestrator: ClamAV scan -> Thumbnail -> Text -> Gemini pgvector
│       │
│       ├── antivirus/clamav.go                     # TCP `zINSTREAM` streaming malware scanner client
│       ├── ai/gemini.go                            # Google Gemini client for 768-dim embeddings & document summaries
│       ├── ratelimit/redis_limiter.go              # Atomic Redis Lua token-bucket rate limiter (+ in-memory fallback)
│       ├── gc/collector.go                         # Sweeps expired staging objects & unreferenced CAS blocks
│       └── metrics/metrics.go & middleware.go      # Prometheus counters, histograms & `/metrics` HTTP middleware
│
└── frontend/src/
    ├── workers/fastcdc.worker.ts                   # Web Worker: slices File into 1–8MB FastCDC chunks & hashes SHA-256
    ├── context/UploadContext.tsx                   # TCP-style AIMD worker pool (1–8 streams), retry backoff, session resume
    ├── lib/uploadConcurrency.ts & crypto.ts        # Concurrency helpers & optional client-side AES-256-GCM encryption
    ├── hooks/useWebSocket.ts                       # WebSocket connection manager with reconnect & SYNC_DELTA dispatch
    └── pages/Dashboard.tsx                         # Main cloud drive UI, delta sync cursor loop, previewers & modals
```

---

## 2.3 — "Feature-to-File" Instant Lookup Table
*(Memorize the left and middle columns — if an interviewer asks "Where is X in your repo?", you can name the exact file in 1 second.)*

| Feature on Your Resume | Exact File in Your Repo | Key Struct / Function Inside It |
| :--- | :--- | :--- |
| **Browser FastCDC (`1–8 MB`)** | [`frontend/src/workers/fastcdc.worker.ts`](file:///c:/Users/Asus/Desktop/z/frontend/src/workers/fastcdc.worker.ts) | `GEAR_TABLE`, `MASK_SMALL` (`0x7fffff`), `MASK_LARGE` (`0x1fffff`) |
| **Backend FastCDC Engine** | [`backend/internal/chunker/fastcdc.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/chunker/fastcdc.go) | `DefaultConfig()`, `NewChunker()`, `(*Chunker).Next()` |
| **Batch CAS Hash Lookup** | [`backend/internal/repository/postgres/block.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/repository/postgres/block.go) | `GetExistingHashes()` (`WHERE sha256 = ANY($1)`) |
| **Staging Quarantine URLs** | [`backend/internal/service/upload_service.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/service/upload_service.go) | `InitiateSession()` → `staging/{session_id}/{seq}` |
| **Zero-Trust `32 KB` SHA-256 Verify** | [`backend/internal/service/upload_service.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/service/upload_service.go) | `CompleteSession()` → `io.Copy(hasher, rc)` + `PromoteObject` |
| **TCP-Style AIMD (`1–8` Streams)** | [`frontend/src/context/UploadContext.tsx`](file:///c:/Users/Asus/Desktop/z/frontend/src/context/UploadContext.tsx) | `INITIAL_CONCURRENCY = 3`, `MAX_CONCURRENCY = 8`, `LATENCY_THRESHOLD_MS = 1500` |
| **PostgreSQL Advisory Locks** | [`backend/internal/repository/postgres/file.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/repository/postgres/file.go) | `AcquireFileNameLock()` (`pg_advisory_xact_lock`) |
| **HTTP 206 Range Streaming** | [`backend/internal/transport/http/file_handlers.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/transport/http/file_handlers.go) | `DownloadFile()`, `parseRangeHeader()`, `io.LimitReader` |
| **SQS + Lambda Background Worker** | [`backend/cmd/worker-lambda/main.go`](file:///c:/Users/Asus/Desktop/z/backend/cmd/worker-lambda/main.go) & [`internal/queue/processor.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/queue/processor.go) | `(*Processor).Process()` |
| **ClamAV Streaming Antivirus** | [`backend/internal/antivirus/clamav.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/antivirus/clamav.go) | `ScanStream()` (TCP `zINSTREAM` chunked protocol) |
| **`768-dim pgvector` Embeddings** | [`backend/internal/ai/gemini.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/ai/gemini.go) & [`internal/queue/processor.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/queue/processor.go) | `EmbedText()` + `GEMINI_EMBEDDING_PACE_MS` (200ms pacer) |
| **Materialized Path Subtree Moves** | [`backend/internal/repository/postgres/file.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/repository/postgres/file.go) | `MoveFile()` (UUID lock ordering + cycle guard + prefix `UPDATE`) |
| **Monotonic `BIGSERIAL` Journal** | [`backend/internal/repository/postgres/journal.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/repository/postgres/journal.go) | `RecordEntry()` & `ListSinceCursor()` |
| **Actor-Model WebSocket Hub** | [`backend/internal/sync/hub.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/sync/hub.go) | `(*Hub).Run()` (single-goroutine `select` loop) |
| **Redis Pub/Sub Backplane** | [`backend/internal/sync/backplane.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/sync/backplane.go) | `(*RedisBackplane).NotifyUser()` & `dispatch()` (`node_id` check) |
| **3-Zone Redis Lua Rate Limiter** | [`backend/internal/ratelimit/redis_limiter.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/ratelimit/redis_limiter.go) | Atomic token-bucket Lua `EVAL` (`AUTH: 10`, `API: 120`, `UPLOAD: 300 RPM`) |
| **Prometheus Metrics** | [`backend/internal/metrics/metrics.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/metrics/metrics.go) | `DedupHitsTotal`, `DedupMissesTotal`, `HTTPRequestDuration` |

---

## 2.4 — Step-by-Step Code Request Traces ("Follow a Request Through the Code")

### 🔄 Trace 1: Uploading a File from Browser Drop to Database Commit
If an interviewer asks *"Trace what happens in your code when a user uploads a file"*, walk through these 5 steps:

1. **Step 1 — Client-Side Chunking (`UploadContext.tsx` → `fastcdc.worker.ts`):**
   - When the user selects a file, `UploadContext.tsx` spawns `fastcdc.worker.ts` and passes the `File` object.
   - The Worker reads the file in `16 MB` chunks, runs the 64-bit rolling Gear hash (`(hash << 1n) + GEAR_TABLE[b] & 0xffffffffffffffffn`) with dual masks (`0x7fffff` below 4 MB, `0x1fffff` above 4 MB), cuts `1–8 MB` blocks, hashes each block with `crypto.subtle.digest('SHA-256')`, and posts the array of `{sequence_number, size, sha256}` back to `UploadContext.tsx`.
2. **Step 2 — Initiate & CAS Dedup Check (`POST /api/upload/initiate` → `upload_handlers.go` → `upload_service.go:InitiateSession`):**
   - The handler parses the chunk manifest and calls `uploadService.InitiateSession()`.
   - `upload_service.go` calls `blockRepo.GetExistingHashes(ctx, sha256List)` which executes a single batch SQL query:
     ```sql
     SELECT sha256 FROM blocks WHERE sha256 = ANY($1::text[]);
     ```
   - It increments Prometheus counters (`metrics.DedupHitsTotal` and `metrics.DedupMissesTotal`), creates a row in `upload_sessions` (`status = 'INITIATED'`), records all expected chunks in `upload_session_blocks`, and calls `storage.PresignPut` **only for the missing chunks**, generating URLs scoped to `staging/{session_id}/{seq}`.
3. **Step 3 — Browser AIMD Parallel Upload (`UploadContext.tsx` → Cloudflare R2):**
   - `UploadContext.tsx` skips every chunk marked as already in CAS (`0 bytes` uploaded).
   - For missing chunks, it runs `3` parallel `PUT` workers directly against the R2 Presigned URLs, timing each chunk with `performance.now()`. If a chunk finishes in `< 1500ms`, it increments concurrency up to `8`; if a chunk times out or hits `429`, it halves concurrency down to `1` and retries with exponential backoff.
4. **Step 4 — Zero-Trust Verification (`POST /api/upload/complete` → `upload_service.go:CompleteSession` Phase 1):**
   - Before opening any SQL transaction, `CompleteSession` spawns a bounded **4-goroutine worker pool** over the newly uploaded chunks:
     - Calls `storage.HeadObject` on `staging/{session_id}/{seq}` to verify the byte size.
     - Calls `storage.GetObject` and streams the body through `io.Copy(sha256Hasher, rc)` (`32 KB` buffer).
     - If the real SHA-256 doesn't match the manifest, it deletes the staging object and aborts with an integrity error.
     - If valid, it calls `storage.PromoteObject` (`CopyObject` from `staging/{session_id}/{seq}` to `blocks/{sha256}` + `DeleteObject` staging).
5. **Step 5 — ACID Metadata Commit & Async Fan-Out (`upload_service.go:CompleteSession` Phase 2):**
   - Opens `tx, err := s.db.BeginTx(ctx, nil)`:
     - Calls `fileRepo.AcquireFileNameLock(ctx, tx, userID, parentID, filename)` (`SELECT pg_advisory_xact_lock(...)`).
     - Upserts the verified hashes into `blocks` (`ON CONFLICT (sha256) DO NOTHING`).
     - Checks if an active file with that name exists in the folder and applies `conflict_mode` (`version` → snapshots old hashes into `file_versions` and increments `version_number` via `COALESCE(MAX(version_number), 0) + 1`; `copy` → auto-renames to `name (1).ext`; `fail` → returns `409 Conflict`).
     - Links `(file_id, sequence_number, block_id)` in `file_blocks`, marks `upload_sessions.status = 'COMPLETED'`, and writes a `journal_entries` row (`BIGSERIAL` cursor).
   - Commits `tx` (20–50ms hold time).
   - Publishes `{file_id, user_id}` to **Amazon SQS** (`publisher.Publish`) and triggers `notifier.NotifyUser(userID, cursor)` to broadcast the WebSocket delta tickle via `RedisBackplane`.

---

### 🔄 Trace 2: Playing / Scrubbing a Video via `HTTP 206 Partial Content`
1. **Request Arrival (`GET /api/files/{id}/download` with `Range: bytes=104857600-`):**
   - `file_handlers.go:DownloadFile` verifies the user's permission (`OWNER`, `EDITOR`, or `VIEWER` via `permissionRepo.HasAccess` recursive CTE).
   - Queries the file's ordered blocks from `file_blocks JOIN blocks ORDER BY sequence_number ASC`.
2. **Range Parsing & Header Setup:**
   - `parseRangeHeader` parses standard byte ranges (`bytes=X-Y`, `bytes=X-`) and RFC 7233 suffix ranges (`bytes=-500` for reading the MP4 `moov` index at the end of a file).
   - Sets headers `Content-Range: bytes start-end/total`, `Accept-Ranges: bytes`, `Content-Length`, and status `206 Partial Content`.
3. **Multi-Block Streaming Loop (`O(1)` 32 KB Memory):**
   - Walks the ordered blocks slice accumulating `currentOffset`:
     - Skips blocks before `rangeStart`.
     - For each overlapping block, opens a scoped closure (so `defer rc.Close()` runs immediately when that single block finishes!), seeks/discards to the intra-block start offset, wraps `rc` in `io.LimitReader(rc, bytesToReadFromBlock)`, and streams to `http.ResponseWriter` via `io.Copy`.
     - Every `16 blocks` (~64 MB), calls `verifyStreamAccess(ctx)` to ensure the file wasn't trashed or share-revoked mid-stream.

---

### 🔄 Trace 3: Real-Time Delta Sync Across Browser Tabs & API Nodes
1. **Mutation & Journal Append (`file_service.go` / `upload_service.go` → `journal.go:RecordEntry`):**
   - Inside any mutation transaction (upload, rename, move, trash, restore), `journalRepo.RecordEntry(ctx, tx, entry)` inserts a row into `journal_entries` and returns the new monotonic `cursor` (`BIGSERIAL`, e.g. `cursor = 240`).
2. **Local Hub + Redis Pub/Sub Broadcast (`router.go:notifyUser` → `backplane.go:NotifyUser`):**
   - After `tx.Commit()`, the server calls `s.notifyUser(userID, event)`:
     - Immediately sends `{type: "SYNC_DELTA", payload: {cursor: 240}}` to `hub.NotifyUser` so tabs connected to **this same API node** receive the WebSocket frame in `< 1ms`.
     - Publishes `{node_id: b.nodeID, user_id: userID, event}` to Redis channel `sync:events`.
   - Other API nodes receive the Redis message, verify `env.NodeID != b.nodeID` (dropping self-echoes on the originating node), and forward the frame to their local `Hub`.
3. **Browser Catch-Up (`useWebSocket.ts` → `Dashboard.tsx` → `GET /api/sync/delta?cursor=238`):**
   - `Dashboard.tsx` sees incoming `cursor (240) > syncCursorRef.current (238)` and calls `GET /api/sync/delta?cursor=238`.
   - `sync_handlers.go` queries `SELECT * FROM journal_entries WHERE user_id = $1 AND cursor > 238 ORDER BY cursor ASC` (an $O(1)$ B-Tree index seek on `idx_journal_user_cursor`), returning entries `239` and `240` so React updates the file list cleanly.

---

### 🔄 Trace 4: Asynchronous SQS + AWS Lambda + ClamAV + `pgvector` Search Pipeline
1. **SQS Trigger (`cmd/worker-lambda/main.go` → `internal/queue/processor.go:Process`):**
   - AWS Lambda wakes up when a message arrives in Amazon SQS and passes the job `{file_id, user_id}` to `processor.Process(ctx, job)`.
2. **Step 1 — Streaming ClamAV Malware Scan (`internal/antivirus/clamav.go:ScanStream`):**
   - The processor reconstructs a multi-block reader over the file's CAS blocks in R2 and streams the bytes over TCP `zINSTREAM` to ClamAV without writing the file to disk. If malware is detected, the file is quarantined/deleted immediately.
3. **Step 2 — Thumbnail Generation (`processor.go`):**
   - Generates a compressed preview thumbnail and uploads it to R2 under `thumbnails/{file_id}` (`thumbnail_status = 'COMPLETED'`).
4. **Step 3 — Text Extraction, Chunking & 768-Dim Gemini Embeddings (`internal/ai/gemini.go` & `processor.go`):**
   - Extracts document text, generates a concise AI summary (`ai_summary`), splits the text into overlapping segments (`chunkText`), and calls `geminiClient.EmbedText` paced at `200ms` per chunk (`GEMINI_EMBEDDING_PACE_MS`).
   - Stores the resulting `768-dimensional` vectors in `file_chunks` / `file_embeddings` (`vector(768)`).
5. **Step 4 — Hybrid Semantic Search (`GET /api/files/search?q=...` → `ai_handlers.go` → `file.go:SearchFiles`):**
   - Embeds the user's search query into a 768-dim vector and runs a single SQL query combining keyword/summary matching with `pgvector` cosine similarity (`1 - (embedding <=> $queryVec)`), returning the best-matching files even when exact keywords don't match the filename.
