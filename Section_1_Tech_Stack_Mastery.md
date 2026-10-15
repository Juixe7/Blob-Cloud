# SECTION 1: TECH STACK MASTERY (UNDER THE HOOD + INTERVIEW Q&A)
*Part 2 of 7 of the Blob-Cloud Interview Master Series*

---

> **Why this section exists:**
> When an interviewer reads your header — `Go, React, TypeScript, PostgreSQL, Redis, Cloudflare R2, AWS (SQS, Lambda), WebSockets, Docker` — they won't just ask about your business logic. They will test whether you actually understand **how each tool works under the hood** (e.g., *"How does S3 generate a Presigned URL?"*, *"How does SQS know if a Lambda worker crashed?"*, *"Why did you use a Lua script in Redis?"*, *"How is a Go goroutine different from an OS thread?"*).
>
> Read this section so you can answer any standalone or project-specific question on every single technology on your resume.

---

## 1. Go (Golang) — Backend API & Worker Language

### 🧠 How It Works Under the Hood (What You Need to Know)
1. **Goroutines vs. OS Threads (`M:N` Scheduler):**
   - An OS thread (like in Java or C++) allocates a fixed **1 MB to 2 MB stack** in RAM and requires a slow kernel context switch.
   - A Go **goroutine** starts with a tiny **2 KB stack** in user space and grows/shrinks dynamically. Go's runtime multiplexes thousands of goroutines (`M`) onto a handful of OS threads (`N` = number of CPU cores). When a goroutine waits on network I/O (like reading from Cloudflare R2 or waiting on a WebSocket), Go parks that goroutine at near-zero cost and runs another goroutine on the same OS thread.
2. **Channels & The `select` Statement (CSP Concurrency):**
   - Instead of sharing memory and locking it with `Mutex` everywhere (*"Don't communicate by sharing memory; share memory by communicating"*), Go uses typed **channels** (`chan T`) to pass data safely between goroutines, and `select` blocks to wait on multiple channels at once.
3. **`io.Reader`, `io.Writer`, and `io.Copy` (Constant-Memory Streaming):**
   - In Go, an `http.Request.Body`, an S3 `GetObject` stream, a `sha256.Hash`, a `zip.Writer`, and an `http.ResponseWriter` all implement the exact same 1-method interfaces: `Read(p []byte)` or `Write(p []byte)`.
   - Calling `io.Copy(dst, src)` allocates a single **`32 KB` byte slice** (`make([]byte, 32*1024)`) and loops `Read` → `Write` until `io.EOF`. That is why our server can hash or stream a 5 GB file using only 32 KB of RAM.
4. **`context.Context` (`ctx.Done()`):**
   - Every HTTP request in Go carries a `ctx context.Context`. If a user closes their browser tab in the middle of a download or search query, `ctx.Done()` closes immediately, canceling downstream PostgreSQL queries and closing R2 network streams so the server never wastes CPU or leaks sockets.

### 🔧 Where We Use Go in Blob-Cloud
* **Stateless API Server (`cmd/api/main.go`):** Handles REST routes, WebSocket connections, and streaming I/O.
* **Bounded Worker Pool (`internal/service/upload_service.go`):** Spawns 4 goroutines on `/upload/complete` to verify staged R2 chunks in parallel using `io.Copy(sha256Hasher, rc)`.
* **Actor-Model WebSocket Hub (`internal/sync/hub.go`):** Uses a single goroutine with a `for { select { ... } }` loop over `register`, `unregister`, and `broadcast` channels so the `clients` map never suffers a `concurrent map read and map write` panic.
* **Serverless Lambda Binary (`cmd/worker-lambda/main.go`):** Compiled as a static Linux binary (`CGO_ENABLED=0 GOOS=linux`) for fast AWS Lambda cold starts (< 50ms).

### 🛡️ Interview Questions They Will Ask You About Go

| Question | Your Answer |
| :--- | :--- |
| **"Why did you choose Go over Node.js or Python for Blob-Cloud?"** | *"Three reasons specific to a cloud storage system: First, constant-memory streaming with `io.Copy` and `io.LimitReader` — streaming or SHA-256 hashing a multi-GB chunked file takes one lightweight goroutine and a 32 KB buffer, without Node.js stream backpressure callbacks or Python's high memory overhead. Second, WebSocket scale — Go's runtime holds thousands of idle WebSocket connections at ~2 KB per goroutine. Third, CPU-bound work like our FastCDC rolling Gear hash runs at compiled machine-code speed without blocking a single-threaded event loop like Node.js."* |
| **"What happens in Go if two goroutines write to a `map` at the same time?"** | *"Unlike some languages that silently corrupt the map, Go's runtime detects concurrent map writes and immediately crashes the entire process with an unrecoverable `fatal error: concurrent map read and map write`. You cannot even catch it with `recover()`. That's why in my WebSocket Hub (`hub.go`), I used the Actor pattern — only one dedicated goroutine owns the `clients` map and all HTTP handlers communicate with it over channels."* |
| **"How do you prevent goroutine and file-descriptor leaks when streaming from S3/R2?"** | *"Two rules I followed strictly across `upload_service.go`, `file_handlers.go`, and `zip_service.go`: First, always pass the request's `ctx` into `s3Client.GetObject(ctx, ...)` and check `ctx.Err()` inside multi-block loops so if the user disconnects, the stream aborts immediately. Second, never put `defer rc.Close()` directly inside a `for` loop over 50 blocks — because `defer` only runs when the outer function exits, which would hold 50 sockets open simultaneously! Instead, I wrap each block read in an anonymous helper closure so `defer rc.Close()` closes each block's HTTP body the millisecond that block finishes streaming."* |

---

## 2. PostgreSQL + `pgvector` — Primary ACID Database & Vector Store

### 🧠 How It Works Under the Hood (What You Need to Know)
1. **ACID Transactions & MVCC:**
   - PostgreSQL uses **MVCC (Multi-Version Concurrency Control)**: readers never block writers, and writers never block readers. Each transaction sees a consistent snapshot of the database.
   - When we call `db.BeginTx()` on upload completion, inserting into `blocks`, `files`, `file_blocks`, `file_versions`, and `journal_entries` is **all-or-nothing**.
2. **B-Tree Indexes & `text_pattern_ops`:**
   - Standard Postgres indexes are **B-Trees** ($O(\log N)$ lookup, which is effectively $O(1)$ — 3 to 4 memory page reads for millions of rows).
   - **Important nuance:** A normal B-Tree index in `en_US.UTF-8` locale **cannot** speed up `WHERE path LIKE '/folderA/%'` because language sorting rules are complex. Adding `text_pattern_ops` tells Postgres to sort the index by raw ASCII byte values, turning `LIKE '/prefix/%'` into a fast range scan (`path >= '/prefix/' AND path < '/prefix0'`).
3. **PostgreSQL Advisory Locks (`pg_advisory_xact_lock`):**
   - Sometimes you need to lock a concept that **doesn't have a row in the database yet** (like *"User A is currently creating a file named `report.pdf` in Folder X"*). You can't use `SELECT ... FOR UPDATE` if the row doesn't exist yet!
   - `pg_advisory_xact_lock(int64_key)` is an ultra-fast, application-defined lock stored in Postgres's internal lock table in RAM. It automatically releases the instant the transaction `COMMIT`s or `ROLLBACK`s, so it can never stay stuck if your Go server crashes.
4. **`pgvector` (`vector(768)` and Cosine Distance `<=>`):**
   - `pgvector` is an official PostgreSQL extension that adds a `vector(N)` column type to store AI embeddings (arrays of $N$ floating-point numbers) directly inside Postgres tables, along with distance operators like `<=>` (Cosine Distance) and `<->` (L2 Euclidean Distance).

### 🔧 Where We Use PostgreSQL in Blob-Cloud
* **Global CAS Metadata (`blocks` & `file_blocks`):** `blocks.sha256` has a `UNIQUE` index; `file_blocks` has composite primary key `(file_id, sequence_number)`.
* **Materialized Path Hierarchy (`files.path`):** Stores `/rootUUID/childUUID/` with `idx_files_user_id_path_pattern`.
* **Concurrency Serialization (`AcquireFileNameLock`):** Uses `pg_advisory_xact_lock(hashtext(...))` during `/upload/complete`.
* **Delta Sync Log (`journal_entries`):** Append-only table with `cursor BIGSERIAL PRIMARY KEY` and composite index `(user_id, cursor)`.
* **Semantic + Keyword Search (`file_embeddings`):** Stores `vector(768)` chunks from Gemini alongside `tsvector` GIN full-text search indexes.

### 🛡️ Interview Questions They Will Ask You About PostgreSQL

| Question | Your Answer |
| :--- | :--- |
| **"Why did you use PostgreSQL instead of MongoDB or DynamoDB?"** | *"A cloud drive has strict relational and transactional invariants that NoSQL struggles with: when a user finalizes an upload, we have to atomically link CAS blocks, increment version numbers, update file metadata, and append a sync journal entry inside a single ACID transaction. Furthermore, Postgres gives us recursive CTEs for walking folder permissions, `text_pattern_ops` for Materialized Path subtree moves, `pg_advisory_xact_lock` for concurrency, and `pgvector` for semantic search — meaning I didn't have to bolt on a separate vector database like Pinecone."* |
| **"Why couldn't you just use `SELECT ... FOR UPDATE` instead of `pg_advisory_xact_lock` when two tabs upload the same filename?"** | *"Because on the very first upload of `notes.pdf`, **no row for `notes.pdf` exists in the `files` table yet!** `SELECT ... FOR UPDATE` can only lock rows that already exist — if two transactions run `SELECT ... FOR UPDATE` on a non-existent filename, both get 0 rows back, neither blocks, and both try to `INSERT`, crashing on the unique index. `pg_advisory_xact_lock` locks the hash of `(user_id, parent_id, filename)` in memory even before any row exists."* |
| **"How did you configure the database connection pool in Go, and why?"** | *"In `cmd/api/main.go`, I configured `db.SetMaxOpenConns(25)`, `db.SetMaxIdleConns(25)`, and `db.SetConnMaxLifetime(5 * time.Minute)`. Small cloud Postgres instances (like RDS `t3.micro`) only allow ~80 max connections total. Capping each Go API instance at 25 connections means even if 1,000 goroutines spike at once across 3 API nodes (75 connections total), excess goroutines wait cleanly in Go's local pool queue instead of crashing Postgres with `too many clients already`."* |

---

## 3. Redis — Distributed Pub/Sub Backplane & Atomic Rate Limiter

### 🧠 How It Works Under the Hood (What You Need to Know)
1. **Why Redis Is So Fast (Single-Threaded Event Loop in RAM):**
   - Redis keeps all data in RAM and executes commands on a **single main thread** using an `epoll`/`kqueue` non-blocking event loop. Because it is single-threaded, individual commands (and Lua scripts) execute **atomically** without lock contention, typically in `< 0.5 ms`.
2. **Redis Pub/Sub vs. Durable Queues (SQS / Kafka):**
   - **Redis Pub/Sub (`PUBLISH` / `SUBSCRIBE`)** is a **fire-and-forget** in-memory broadcast bus. When you publish a message to channel `sync:events`, Redis immediately pushes it to every currently connected subscriber socket and does **not** save it to disk. If a subscriber is disconnected for 1 second, it misses that message.
   - That makes Redis Pub/Sub **bad** for background job queues (which is why we use **Amazon SQS** for Lambda jobs!), but **perfect** for WebSocket wake-up "tickles" (because the durable data is already saved in PostgreSQL `journal_entries`, and Redis is just ringing the doorbell across our API servers!).
3. **Why We Use a Redis Lua Script for Rate Limiting (`rate_limit.go`):**
   - Suppose you write a rate limiter in Go that does:
     1. `count := redis.Get(ip)`
     2. `if count < 300 { redis.Incr(ip) }`
   - If 10 requests arrive from the same IP at the exact same millisecond across 2 API servers, all 10 run `redis.Get(ip)` before any of them run `Incr`, so all 10 see the old count (a classic **TOCTOU race condition**)!
   - By sending a **Lua script (`EVAL`)** to Redis, Redis executes the entire *read tokens → calculate elapsed time → refill tokens → deduct 1 token* logic as a **single atomic operation** on its single thread. No race condition is possible.

### 🔧 Where We Use Redis in Blob-Cloud
1. **Multi-Node WebSocket Sync Backplane (`internal/sync/backplane.go`):**
   - Broadcasts `{node_id, user_id, event}` across stateless Go API servers on channel `sync:events`, using `node_id` to drop self-echoes.
2. **Three-Zone Token-Bucket Rate Limiter (`internal/transport/http/rate_limit.go`):**
   - Enforces per-IP limits across the cluster: `AUTH` (`10 req/min`), `API` (`120 req/min`), and `UPLOAD` (`300 req/min`), with automatic fail-open fallback to an in-memory limiter if Redis is unreachable.

### 🛡️ Interview Questions They Will Ask You About Redis

| Question | Your Answer |
| :--- | :--- |
| **"Why did you use both Redis Pub/Sub AND Amazon SQS? Couldn't Redis do both, or SQS do both?"** | *"They solve opposite problems. **Redis Pub/Sub** is an ephemeral, sub-millisecond fan-out broadcast (`1 publisher → N API nodes`) needed to wake up WebSockets across all running API servers at once — SQS cannot broadcast a single message to all API servers simultaneously because SQS is a competing-consumer queue where only 1 worker gets each message. Conversely, **Amazon SQS** is a durable queue with visibility timeouts and retries (`1 job → 1 Lambda worker`), whereas Redis Pub/Sub drops messages if no worker is listening."* |
| **"Why did you use a Lua script in Redis for rate limiting instead of standard `INCR` and `EXPIRE` commands?"** | *"If you run `INCR` and `EXPIRE` (or `GET` and `SET`) as separate network commands from Go, two concurrent requests can interleave between the commands — or if the Go process crashes after `INCR` but before `EXPIRE`, that IP's key never expires and the user is permanently blocked! A Redis Lua script runs atomically on Redis's single-threaded engine, so token refill calculation, deduction, and `PEXPIRE` happen as one indivisible step."* |
| **"What happens to your system if the Redis container crashes?"** | *"I built both Redis integrations to fail open gracefully. If Redis goes down, `rate_limit.go` catches the connection error and falls back to an in-memory per-node token bucket so API requests don't fail with 500s. And in `backplane.go`, local WebSocket notifications still dispatch via the in-memory `Hub`, while users on other nodes catch up via HTTP delta polling (`GET /api/sync/delta?cursor=X`) against PostgreSQL."* |

---

## 4. Cloudflare R2 & S3 Presigned URLs — Object Storage Data Plane

### 🧠 How It Works Under the Hood (What You Need to Know)
1. **Object Storage (S3 / R2) vs. Block/File Storage (EBS / Local Disk):**
   - A local disk (or AWS EBS) lets you open a file and overwrite 100 bytes in the middle (`f.Seek(500); f.Write(data)`).
   - **Object Storage (AWS S3 / Cloudflare R2)** is a flat HTTP key-value blob store (`Key -> Immutable Bytes + Metadata`). You **cannot** edit the middle of an S3 object — you can only `PUT` a whole object, `GET` (or `GET` a byte range of) an object, `COPY` an object, or `DELETE` an object.
   - **Aha! Connection to FastCDC:** Because S3 objects are immutable, if you stored a 1 GB file as a single S3 object, editing 10 KB in the middle would force you to re-upload the entire 1 GB object! Because we split files into `1–8 MB` FastCDC chunks stored as separate S3 objects (`blocks/{sha256}`), editing 10 KB only requires `PUT`ting **one 4 MB object**!
2. **How S3 Presigned URLs Actually Work Under the Hood (Crucial Interview Question!):**
   - Many candidates think generating a Presigned URL makes a network call to AWS S3 or Cloudflare R2. **It does NOT!**
   - Generating a Presigned URL (`s3Presigner.PresignPutObject`) is **100% local CPU math** taking `< 0.1 ms` on the Go server:
     1. The Go server builds a canonical string containing: the HTTP method (`PUT`), the bucket name, the exact object key (`staging/{session_id}/{seq}`), the current timestamp (`X-Amz-Date`), and the expiry (`X-Amz-Expires=1800` for 30 minutes).
     2. The Go server signs that string using **HMAC-SHA256 (AWS Signature Version 4)** with its secret `R2_SECRET_ACCESS_KEY` and appends the signature to the URL query string (`?X-Amz-Signature=...`).
     3. When the browser `PUT`s the chunk to that URL, Cloudflare R2 recomputes the HMAC-SHA256 signature on its end. If the browser tampers with the key name, method, or expiry time by even 1 character, the HMAC signature fails with `403 Forbidden`!
3. **Why Cloudflare R2 Instead of AWS S3?**
   - Cloudflare R2 implements the **exact AWS S3 REST API** (so our Go code uses the standard `aws-sdk-go-v2/service/s3` package with a custom endpoint URL), **but Cloudflare R2 charges `$0.00` (zero) egress fees** for downloads, whereas AWS S3 charges `$0.09 per GB` downloaded. For a cloud drive that streams videos and downloads files, zero egress fees eliminates runaway bandwidth bills.

### 🛡️ Interview Questions They Will Ask You About R2 & S3 Presigned URLs

| Question | Your Answer |
| :--- | :--- |
| **"When your Go server generates 50 Presigned PUT URLs in `/upload/initiate`, does it have to make 50 network calls to S3/R2?"** | *"No — zero network calls! Presigning in AWS SigV4 is purely local cryptographic signing on the Go server using HMAC-SHA256 over the HTTP method (`PUT`), bucket, key (`staging/{session_id}/{seq}`), and expiry window (`30 minutes`). Generating 50 presigned URLs takes less than a millisecond of local CPU time."* |
| **"Can a user take a Presigned PUT URL for `staging/sessionA/0` and use it to overwrite `blocks/some_hash` or read another user's file?"** | *"No. In AWS Signature v4, the HTTP verb (`PUT` vs `GET`), the bucket name, and the exact object key (`staging/sessionA/0`) are all baked into the HMAC-SHA256 signature. If a client changes the URL path or changes `PUT` to `GET`, the signature verification at R2 immediately rejects the request with `403 SignatureDoesNotMatch`."* |
| **"Why did you use Cloudflare R2 instead of AWS S3?"** | *"R2 is S3-API compatible — so our Go backend uses the official `aws-sdk-go-v2/service/s3` client without vendor lock-in — while offering **zero egress fees**. On AWS S3, streaming a 1 GB video 100 times costs $9 in egress bandwidth alone; on Cloudflare R2, egress is free, and server-side `CopyObject` from `staging/` to `blocks/` happens entirely inside R2's storage fabric."* |

---

## 5. AWS (Amazon SQS, AWS Lambda, AWS EC2) — Cloud Compute & Messaging

### 🧠 How It Works Under the Hood (What You Need to Know)
1. **Amazon SQS (Simple Queue Service) & Visibility Timeouts:**
   - SQS is a distributed, pull-based message queue.
   - **How Visibility Timeout guarantees no lost jobs:** When our AWS Lambda worker pulls a message from SQS, SQS **does not delete the message**. Instead, SQS starts a **Visibility Timeout** timer (e.g., 60 seconds) during which that message is hidden from other workers.
   - If the Lambda worker finishes ClamAV + Thumbnail + Gemini Embeddings successfully, it tells SQS: `DeleteMessage`.
   - If the Lambda worker crashes, runs out of memory, or times out halfway through, `DeleteMessage` is never called! When the Visibility Timeout expires, the message **automatically reappears in the queue** and another worker retries it. After `maxReceiveCount` failures, SQS moves the poison message to a **Dead-Letter Queue (DLQ)**.
2. **SQS Long Polling (`WaitTimeSeconds = 20`):**
   - With Short Polling (`WaitTimeSeconds = 0`), if the queue is empty, SQS returns an empty response immediately, causing a poller loop to make millions of useless API calls per month.
   - Setting `WaitTimeSeconds = 20` tells SQS to hold the HTTP connection open for up to 20 seconds until a message arrives, reducing empty poll calls (and cost) by **97%**.
3. **Why Hybrid Compute: Why EC2 for the API Server + AWS Lambda for Background Workers?**
   - **Why EC2 for the Go API (`cmd/api`):** Our API server maintains persistent, long-lived **WebSocket connections** and in-memory `Hub` state, and streams large `HTTP 206 Range` video responses. AWS Lambda charges per millisecond of open connection time and has a 15-minute execution limit and payload size limits — making Lambda a poor fit for long-lived WebSocket hubs and multi-GB video range streams. A persistent EC2 instance behind Nginx handles long-lived sockets effortlessly.
   - **Why AWS Lambda for the Worker (`cmd/worker-lambda`):** Background tasks (ClamAV scanning, image/video thumbnailing, PDF parsing, 768-dim Gemini embeddings) are **spiky and CPU/memory-heavy**. When nobody is uploading, Lambda scales to **zero (`$0` cost)**. If 20 files are uploaded at once, Lambda automatically spins up isolated execution containers triggered directly by SQS without touching a single CPU cycle on our EC2 API server!

### 🛡️ Interview Questions They Will Ask You About AWS (SQS, Lambda, EC2)

| Question | Your Answer |
| :--- | :--- |
| **"Why did you deploy the Go API on EC2, but the background worker on AWS Lambda? Why not put everything on EC2 or everything on Lambda?"** | *"Because their workload profiles are completely different. The Go API server manages persistent **WebSocket connections** and continuous **HTTP 206 byte-range video streams** — workloads that require a long-running stateful process and would be expensive or hit payload/timeout limits on API Gateway + Lambda. On the other hand, post-upload processing (ClamAV scans, thumbnail rendering, and PDF embedding) is bursty and CPU-intensive. Running that on EC2 would starve the API server during upload bursts, whereas offloading it to **SQS + AWS Lambda** gives us zero idle cost and instant horizontal burst scaling."* |
| **"SQS provides 'at-least-once' delivery, which means the same upload job could occasionally be delivered twice to Lambda. Is your Lambda worker idempotent?"** | *"Yes! Every step in `processor.go` is designed to be idempotent if retried: (1) ClamAV scanning is a read-only check; (2) writing the thumbnail overwrites the deterministic `thumbnails/{file_id}.webp` key in R2; and (3) before inserting `file_embeddings` rows into PostgreSQL, the worker deletes any existing embeddings for that `file_id` (`DELETE FROM file_embeddings WHERE file_id = $1`) and inserts the fresh chunks inside a transaction. So even if SQS delivers a message twice, you never get duplicate vector rows."* |
| **"What happens if the Gemini API is down or rate-limits your Lambda worker?"** | *"First, for multi-chunk documents, `processor.go` paces Gemini embedding requests at `200ms` (`GEMINI_EMBEDDING_PACE_MS`) using a context-aware timer so we don't trigger 429 burst limits. Second, if a transient outage occurs and the worker returns an error, Lambda does not acknowledge the SQS message — so SQS holds the message until the visibility timeout expires and automatically retries the job."* |

---

## 6. WebSockets & Browser Web Workers — Real-Time & Client Parallelism

### 🧠 How They Work Under the Hood
1. **WebSockets (`RFC 6455`):**
   - Starts as a normal HTTP `GET /ws?token=<jwt>` request with headers `Connection: Upgrade` and `Upgrade: websocket`.
   - The server validates the JWT and replies `HTTP/1.1 101 Switching Protocols`. From that moment on, the underlying TCP socket stays open as a full-duplex binary/text framing channel where either side can push frames with only 2–14 bytes of header overhead (no HTTP headers or TLS handshakes per message).
   - **Why `?token=<jwt>` in the query string?** The browser's native `new WebSocket(url)` API does **not** allow setting custom `Authorization: Bearer` headers during the handshake, so passing the short-lived JWT via query parameter (or cookie) over `wss://` (TLS) is required.
2. **Browser Web Workers (`fastcdc.worker.ts`):**
   - JavaScript in the browser is single-threaded — the same thread that runs React state updates also paints pixels at 60fps (16.6ms per frame).
   - If you run a tight `for` loop over `100,000,000` bytes computing Gear hashes and SHA-256 on the main thread, the entire browser tab freezes for several seconds.
   - A **Web Worker** spawns a real, separate OS-level thread in the browser with its own isolated V8 memory space. We pass the `File` reference to `fastcdc.worker.ts` via `postMessage`; the Worker slices the `File` in `16 MB` windows, computes FastCDC cut-points and WebCrypto SHA-256 hashes in the background, and posts chunk progress back to React while the UI stays buttery smooth.

---

## 7. Docker & GitHub Actions CI/CD — Containerization & Deployment

### 🧠 How We Use Them in Blob-Cloud
1. **Multi-Stage Docker Builds (`backend/Dockerfile`):**
   - **Stage 1 (`golang:alpine` builder):** Compiles the Go source code into a single static binary (`CGO_ENABLED=0 GOOS=linux go build -o api ./cmd/api`).
   - **Stage 2 (`alpine` runtime):** Copies *only* the compiled binary and SQL migrations into a minimal Alpine image (~20 MB instead of an 800 MB Go SDK image), reducing attack surface and memory footprint.
2. **GitHub Actions CI/CD (`.github/workflows/ci.yml` & `deploy.yml`):**
   - **CI Quality Gate (`ci.yml`):** Spins up real **PostgreSQL 16 (`pgvector/pgvector:pg16`)** and **Redis 7** service containers in GitHub Actions, runs `go vet ./...`, executes `go test -race -v ./...` (with Go's race detector enabled!), compiles all 4 Go binaries (`api`, `worker-lambda`, `gc`, `bench-chunker`), and runs `npx tsc -b` + `vite build` on the frontend with zero warnings.
   - **Automated EC2 Deployment (`deploy.yml`):** Compiles the Linux AMD64 binary and production Vite bundle, uploads them to AWS EC2 over SCP, and restarts the `systemd` unit (`sudo systemctl restart blobcloud`) with a health-check verification against `http://localhost:8080/health`.
