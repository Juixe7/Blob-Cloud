# BLOB-CLOUD: ONE-PAGE COMPLETE SYSTEM OVERVIEW

### 1. What It Is & Core Architectural Rule
**Blob-Cloud** is a distributed cloud storage and collaboration platform built from scratch in **Go** and **React/TypeScript**, modeled on the core storage, sync, and security engineering principles behind **Dropbox** and **Google Drive**.
Its central architectural rule is **strict separation between the Control Plane and the Data Plane**: raw file upload payloads never stream through the Go API server. Instead, the browser chunks and hashes files locally inside a background **Web Worker**, queries the Go API for deduplication hits, and transfers only missing blocks directly to **Cloudflare R2 (S3-compatible object storage)** via time-limited **S3 Presigned URLs**.

---

### 2. Why This Tech Stack
* **Go (Backend API & Workers):** Lightweight goroutines (`2 KB` stacks) and `io.Copy` (`32 KB` fixed buffers) allow streaming multi-GB files in $O(1)$ memory while holding thousands of concurrent WebSocket connections.
* **React, TypeScript & Web Workers (Frontend):** Offloads CPU-heavy binary chunking, rolling Gear hashing, WebCrypto SHA-256, and optional client-side **AES-256-GCM** encryption off the main UI thread to preserve 60fps rendering.
* **PostgreSQL + `pgvector` (ACID Metadata & Vector Store):** Single relational source of truth for directory trees, CAS block mappings, RBAC permissions, monotonic sync journals, and 768-dimensional semantic search vectors.
* **Cloudflare R2 / AWS S3 (Data Plane):** Immutable object storage (`blocks/{sha256}`) with zero egress bandwidth fees.
* **Redis (Distributed Coordination):** Powers the multi-node WebSocket Pub/Sub backplane and atomic Lua token-bucket rate limiter.
* **Amazon SQS + Serverless AWS Lambda (Async Compute):** Offloads CPU-heavy post-upload tasks (ClamAV antivirus, thumbnails, and LLM embeddings) from the EC2 API server, scaling to zero when idle.
* **Docker, GitHub Actions CI/CD & Prometheus/Grafana:** Automated race-detector testing (`go test -race`), zero-downtime EC2 deployment, and `/metrics` observability tracking the Four Golden Signals.

---

### 3. The 6 Core Engineering Pillars (How It Works End-to-End)

1. **Client-Side FastCDC Deduplication & Global CAS (`1–8 MB` Blocks, `98%+` Reuse):**
   * Fixed-size chunking (`4 MB` blocks) fails on file edits because inserting 1 byte at the top shifts every subsequent boundary (`0%` reuse).
   * Our Web Worker implements **FastCDC** using a 64-bit **rolling Gear hash** (`hash = (hash << 1) + GEAR[b]`) with normalized dual bitmasks (`23-bit` mask below `4 MB`, `21-bit` mask above `4 MB`) to cut content-defined `1–8 MB` chunks (`4 MB` average).
   * On upload initiation, the backend checks all chunk SHA-256 hashes in one batch SQL query (`WHERE sha256 = ANY($1)`). Existing blocks in **Content-Addressable Storage (`blocks/{sha256}`)** are skipped immediately — reusing **98%+** of chunks across file revisions.

2. **Zero-Trust Upload Quarantine & $O(1)$ Memory (`32 KB`) Verification:**
   * Trusting client-declared SHA-256 hashes lets a malicious or buggy client overwrite shared CAS blocks (**CAS Poisoning**).
   * Presigned PUT URLs only ever point to an isolated quarantine key: `staging/{session_id}/{seq}`.
   * On `/api/upload/complete`, **before** opening a database transaction (so network I/O never holds DB locks), a bounded 4-goroutine worker pool streams each newly staged chunk from R2 through `crypto/sha256` using Go's `io.CopyBuffer` (**fixed `32 KB` buffer = $O(1)$ RAM**, benchmarked at **`998.7 MB/s`** and **`40.4 KiB` total heap** across a `64 MiB` multi-block stream). Verified chunks are promoted to `blocks/{sha256}` via `CopyObject`, while tampered chunks are rejected and purged from staging.

3. **Resumable AIMD Uploads (`1–8` Streams), Advisory Locks & `HTTP 206` Range Streaming:**
   * **Browser AIMD Controller:** Starts with `3` parallel chunk `PUT` streams, adds `+1` up to `8` when chunk RTT `< 1500ms`, and halves concurrency down to `1` on timeouts/429s. Interrupted sessions reconcile against `upload_session_blocks` to resume with **0-byte** redundant re-uploads.
   * **PostgreSQL Advisory Locks:** Acquires `pg_advisory_xact_lock(hash(user_id, parent_id, filename))` inside `/complete` so concurrent uploads of the same filename (verified under **50 simultaneous goroutines** with `-race`) serialize cleanly and atomically increment `MAX(version_number) + 1` (`1..49` with zero duplicates).
   * **RFC 7233 `HTTP 206 Partial Content`:** Maps arbitrary `Range: bytes=X-Y` (and suffix `bytes=-500`) requests across variable-size FastCDC blocks using `io.LimitReader`, re-verifying file status, RBAC permissions, and public link expiry every **`16 blocks` (~64 MB)** to cut off revoked streams mid-flight.

4. **Materialized Path Directory Tree (`text_pattern_ops`, `< 3ms` Subtrees, `112x` Speedup):**
   * Stores full ancestry paths (`'/rootUUID/parentUUID/thisUUID/'`) indexed with `CREATE INDEX idx_files_user_id_path_pattern ON files (user_id, path text_pattern_ops)`.
   * Converts deep folder subtree queries (folder size, bulk trash, streaming folder ZIP) from $O(D \cdot N)$ recursive parent joins (`8.70 ms` at 50k rows) into an **$O(\log N + K)$ B-Tree byte-prefix range scan (`77 µs` at 50k rows — `112.8x` faster, `1.21 ms` live Postgres `EXPLAIN ANALYZE`)**, and moves subtrees in a single atomic `UPDATE` with `HasPrefix` cycle guards.

5. **Decoupled SQS + AWS Lambda Worker (ClamAV, Thumbnails & `768-dim pgvector` Search):**
   * Upload completion enqueues a job to **Amazon SQS** (`20s` long-polling cuts empty polls by `97%`).
   * **AWS Lambda** (`cmd/worker-lambda`) streams blocks from R2 over TCP `zINSTREAM` to **ClamAV** (zero local disk usage), generates WebP/JPEG **thumbnails**, and chunks extracted document text into **768-dimensional Gemini embeddings** (paced at `200ms`) stored in PostgreSQL `pgvector` for hybrid keyword (`tsvector`) + semantic cosine-similarity (`<=>`) search.

6. **Real-Time "Tickle-and-Fetch" Delta Sync (`< 5ms p95` Read Latency, `BIGSERIAL` Journal + Redis Pub/Sub):**
   * Every file mutation appends a row to `journal_entries` with a strictly monotonic `BIGSERIAL` primary key (`cursor`) indexed by `idx_journal_user_cursor (user_id, cursor)`.
   * An **Actor-Model WebSocket Hub** (single goroutine owning the client map via channels) and **Redis Pub/Sub backplane** (with per-node `node_id` UUID echo suppression) push lightweight `{type: 'SYNC_DELTA', cursor}` tickles across all API nodes.
   * **How `< 5ms p95` is measured:** Instrumented via Prometheus histogram `blobcloud_http_request_duration_seconds_bucket{le="0.005"}` (`5ms` lowest bucket) and benchmarked (`cmd/bench-latency`) under **50 concurrent workers against a 10,000-row dataset** (`GET /api/sync/delta?limit=50`: **`2.94 ms p95`**, **`437 µs mean`**, **`98.24% <= 5ms`**, **`0.037 ms` live Postgres `Index Scan` on `idx_journal_user_cursor`**; `GET /api/files`: **`1.01 ms mean`**, **`95.20% <= 5ms`**).

---

### 4. Security, Concurrency Torture Suite & Production Hardening (`go test -race ./...`)
* **Hierarchical RBAC, IDOR Guards & Public Shares:** Recursive CTE (`WITH RECURSIVE chain`) resolves `OWNER`, `EDITOR`, and `VIEWER` inheritance. Strict IDOR/BOLA isolation across files, upload sessions, versions, and journal cursors, plus JWT `alg: none` & refresh-token replay rejection.
* **Race-Detector Torture Suite (`-race` in CI):** Verified with 50-goroutine same-filename/same-block upload serialization, 60-goroutine WebSocket Hub slow-consumer backpressure & zero goroutine leaks, 3-node Redis Pub/Sub cluster echo-suppression, 100-goroutine token-bucket atomicity, and concurrent 3-table CAS Garbage Collection (`file_blocks`, `file_versions`, `upload_session_blocks`) vs in-flight uploads.
