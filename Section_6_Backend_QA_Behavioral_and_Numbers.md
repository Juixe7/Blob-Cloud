# SECTION 6: BACKEND Q&A BANK, BEHAVIORAL SCRIPTS & 1-PAGE NUMBERS CHEAT SHEET
*Part 7 of 7 of the Blob-Cloud Interview Master Series*

---

> **How to use this final section:**
> - **Part A (20-Question Backend Q&A Bank):** Rapid-fire, 3-to-5 sentence answers for standard backend technical rounds covering Database, Concurrency, Distributed Systems, Go/API Design, and Security/Observability.
> - **Part B (Fresher Openers & EM Behavioral Scripts):** Word-for-word natural answers for Engineering Manager and HR/Hiring Manager rounds (including the honest answer if asked about using AI tools).
> - **Part C (The 1-Page Numbers & Constants Cheat Sheet):** Read this table 10 minutes before your interview — it explains every single number in your resume and codebase in one plain sentence.

---

# PART A: THE 20-QUESTION BACKEND TECHNICAL Q&A BANK

---

## 1. Database & SQL Architecture

**Q1: Why did you choose PostgreSQL instead of MongoDB or DynamoDB?**
> *"A cloud file system is fundamentally relational and requires strict ACID transactions across multiple entities. When an upload completes, we have to atomically upsert `blocks`, insert or version the row in `files`, link ordered chunks in `file_blocks`, mark `upload_sessions` completed, and append a `journal_entries` sync event — either all five succeed or none do. Beyond ACID transactions, Postgres gives us `text_pattern_ops` B-Tree indexes for Materialized Path subtrees, recursive CTEs for permission inheritance, `pg_advisory_xact_lock` for concurrency, and `pgvector` for 768-dim semantic search in a single database engine."*

**Q2: How would you scale PostgreSQL when you hit its connection or write limits?**
> *"In three stages: First, put **PgBouncer** in front of Postgres in transaction-pooling mode so thousands of application goroutines can multiplex onto 25 physical Postgres connections. Second, add **Read Replicas** and route read-heavy queries — folder listings, permission checks, and `pgvector` semantic searches — to replicas while keeping transactional writes on the primary. Third, **hash-partition** the `files` and `journal_entries` tables by `user_id` (`PARTITION BY HASH (user_id)`) since every tree and delta-sync query is already scoped to a single `user_id`."*

**Q3: Why use `BIGSERIAL` for the sync journal cursor instead of a `UUID` or `TIMESTAMPTZ`?**
> *"A `UUIDv4` is completely random, so you cannot run range queries like `WHERE cursor > X`. And `TIMESTAMPTZ` suffers from two fatal issues in distributed systems: clock skew across different API servers, and microsecond collisions when two transactions commit at the same instant. PostgreSQL's `BIGSERIAL` is a monotonic sequence generator that guarantees strictly increasing, collision-free 64-bit integers. With a composite B-Tree index on `(user_id, cursor)`, fetching missed sync events is a sub-millisecond $O(1)$ index seek."*

**Q4: How did you prevent $N+1$ database queries in your backend?**
> *"Three places: First, in `/api/upload/initiate`, instead of querying Postgres in a `for` loop for each of the 50 chunk hashes, I pass the entire slice into one batch query: `SELECT sha256 FROM blocks WHERE sha256 = ANY($1::text[])`. Second, when permanently deleting a folder subtree (`HardDelete`), I collect all candidate block hashes across all descendant files and check for orphaned blocks in a single batch query (`findOrphanedHashes`) rather than per file. Third, folder listings fetch file metadata and shortcut target information in a single joined query."*

**Q5: If a user deletes `report.pdf` (moving it to Trash) and then uploads a new `report.pdf` in the same folder, why doesn't that violate your `UNIQUE` filename constraint?**
> *"Because in migration `000020`, I defined the uniqueness rule as a **partial unique index**: `CREATE UNIQUE INDEX idx_files_unique_active_name ON files (user_id, parent_id, name) WHERE deleted_at IS NULL` (plus a matching index for root files where `parent_id IS NULL`). Rows in the Trash have a non-null `deleted_at` timestamp, so Postgres excludes them from the unique index — allowing multiple trashed versions of `report.pdf` while enforcing strict uniqueness among active files."*

---

## 2. Concurrency, Transactions & Crash Recovery

**Q6: How do you handle two browser tabs uploading the same filename into the same folder at the exact same time?**
> *"Inside `CompleteSession`, right after opening the SQL transaction, we call `AcquireFileNameLock` which executes `SELECT pg_advisory_xact_lock(hashtext(user_id || ':' || COALESCE(parent_id, 'root') || ':' || filename))`. That transaction-scoped advisory lock serializes concurrent completions for that exact filename tuple — even before any row exists in `files`. The second transaction waits ~15–20ms until the first commits, sees the newly created file, and cleanly executes the user's `conflict_mode` (`version` to increment `COALESCE(MAX(version_number), 0) + 1`, `copy` to auto-rename to `report (1).pdf`, or `fail` to return HTTP 409)."*

**Q7: What happens if the user's Wi-Fi drops or the Go server restarts when a 50-chunk upload is halfway done?**
> *"Every upload session is persisted in Postgres (`upload_sessions` and `upload_session_blocks`), and every uploaded chunk is staged in Cloudflare R2 under `staging/{session_id}/{sequence_number}`. If the transfer is interrupted at chunk 25, the session remains in `INITIATED` status. When the client reconnects, the backend reconciles which chunk sequence numbers are already staged in R2 and returns Presigned PUT URLs only for chunks `25..49` — resuming with zero redundant bytes re-uploaded."*

**Q8: How do you prevent database deadlocks when two collaborators move folders at the same time?**
> *"Suppose User A moves Folder X into Folder Y at the exact same millisecond User B moves Folder Y into Folder X. If Transaction 1 locks X then Y while Transaction 2 locks Y then X, they deadlock — or worse, create a circular directory cycle! In `MoveFile` (`file.go`), I prevent both: first, I lock the source and destination folder rows in **deterministic lexicographical UUID order** (`if srcID < dstID` lock `src` then `dst`, else lock `dst` then `src`), which makes lock-order deadlocks mathematically impossible; second, I check `strings.HasPrefix(dest.Path, src.Path)` and reject any move that would place a folder inside its own descendant."*

**Q9: How do you prevent a Time-of-Check to Time-of-Use (TOCTOU) bug if a client calls `/api/upload/complete` twice concurrently for the same `session_id`?**
> *"We validate `session.Status == 'INITIATED'` twice: first as a fast pre-check before running R2 chunk verification, and a second time **inside the PostgreSQL transaction under lock** before updating the status to `'COMPLETED'`. If a duplicate `/complete` request arrives concurrently, only the first transaction sees `'INITIATED'` and commits; the second sees `'COMPLETED'` and returns immediately without creating duplicate files."*

---

## 3. Distributed Systems & Scaling

**Q10: How would you scale Blob-Cloud to 10 million users?**
> *"Because our Control Plane and Data Plane are already decoupled, each tier scales independently:*
> *1. **Data Plane (Cloudflare R2 / S3):** Already scales horizontally; for 50 GB+ files, we would enforce S3's native `x-amz-checksum-sha256` signed header at the edge during `PUT` so the Go server doesn't even need to re-stream cache-miss chunks on `/complete`.*
> *2. **API Tier (Go):** Stateless — auto-scale instances behind an AWS Application Load Balancer.*
> *3. **Database (PostgreSQL):** PgBouncer connection pooling + read replicas + hash partitioning on `user_id`.*
> *4. **Real-Time Sync (Redis):** Shard the Pub/Sub channels by `user_id` across a Redis Cluster (`sync:events:{shard}`).*
> *5. **Background Compute (SQS + Lambda):** Scales concurrency automatically with queue depth."*

**Q11: What is your CAP theorem trade-off across PostgreSQL and Redis?**
> *"We deliberately use **CP (Consistency + Partition Tolerance)** for file metadata in PostgreSQL, and **AP (Availability + Partition Tolerance)** for real-time notifications and rate limiting in Redis. If Postgres can't guarantee consistency, we must fail the write rather than corrupting the directory tree or CAS block links. Conversely, if Redis experiences a network partition, our rate limiter fails open to an in-memory fallback and our sync layer degrades gracefully from instant WebSocket push to HTTP cursor polling (`GET /api/sync/delta?cursor=X`) against Postgres — so availability is preserved with zero data loss."*

**Q12: How does Client-Side End-to-End Encryption (AES-256-GCM) interact with FastCDC deduplication?**
> *"In Blob-Cloud, users can optionally lock sensitive files with a passphrase (`crypto.ts` using WebCrypto PBKDF2 key derivation + AES-256-GCM). When client-side encryption is enabled, chunks are encrypted with a random IV **before** hashing, which intentionally disables cross-user deduplication for that file (since two users encrypting the same file produce different ciphertexts). For normal unencrypted files, FastCDC deduplicates globally across the CAS store while strict RBAC and zero-trust SHA-256 verification isolate access."*

**Q13: Why did you use Cloudflare R2 instead of storing chunks on the EC2 server's disk?**
> *"Three reasons: First, local EC2 disk (`EBS`) is tied to a single machine — if you run 2 or 3 stateless Go API servers behind a load balancer, Server 2 cannot read files written to Server 1's local disk. Second, using S3 Presigned URLs with R2 lets browsers upload and download directly to cloud object storage without consuming EC2 network bandwidth. Third, Cloudflare R2 provides 11 nines of durability with `$0` egress fees."*

---

## 4. Go, Streaming I/O & API Design

**Q14: How do you stream a ZIP download of an entire folder without running out of server RAM?**
> *"In `zip_service.go`, I attach Go's standard `archive/zip.NewWriter(w)` directly to the `http.ResponseWriter`. I query the folder's descendants in one Materialized Path scan (`WHERE path LIKE $prefix || '%'`), loop through each file, create a zip header entry using its relative path, and stream its ordered FastCDC blocks from R2 directly into the zip writer via `io.Copy` (32 KB buffer). The zip bytes are flushed to the user's browser on the fly — so downloading a 2 GB folder uses only ~32 KB of heap memory."*

**Q15: How does your authentication and device session revocation system work?**
> *"When a user logs in, we issue a short-lived `HS256` Access JWT and create a row in `user_sessions` tracking their refresh token, device User-Agent, and IP. Our auth middleware validates the Bearer JWT, checks that `claims.IsRefresh == false` (so refresh tokens can never be used as access tokens), and verifies the session is active. If a user opens 'Active Sessions' in Settings and revokes a device (`DELETE /api/auth/sessions/{id}`), we delete that session row in Postgres and push a `FORCE_LOGOUT` WebSocket event to that specific `session_id` via our Hub, kicking that device out in real time."*

**Q16: Why do WebSocket connections pass `?token=<jwt>` in the URL query string instead of the `Authorization: Bearer` header?**
> *"The browser's native `new WebSocket(url)` JavaScript API does not allow setting custom HTTP headers like `Authorization` during the initial HTTP `101 Switching Protocols` upgrade handshake. Passing the short-lived access JWT in the `?token=` query parameter over `wss://` (TLS) allows the `websocket_handlers.go` upgrade handler to run the exact same `auth.ValidateToken` and session check before upgrading the socket."*

---

# PART B: FRESHER PROJECT OPENERS & EM BEHAVIORAL SCRIPTS

---

### 1. *"Why did you build Blob-Cloud? Why not just use Google Drive or build a simple S3 wrapper?"*
> *"Anyone can build a basic S3 CRUD wrapper in a weekend where you upload a whole file to S3 and save one URL in a database — but you don't learn real backend engineering from that. I built Blob-Cloud because I wanted to understand and implement the actual distributed systems mechanics behind Dropbox and Google Drive from scratch: how content-defined chunking (FastCDC) avoids re-uploading 98% of a large file when bytes shift, how to verify untrusted client uploads in 32 KB constant memory without CAS poisoning, how to coordinate concurrent writes with PostgreSQL advisory locks, and how to keep multiple clients in sync using a monotonic `BIGSERIAL` journal and Redis Pub/Sub."*

### 2. *"What would you add next if you had 2 more months?"*
*(⚠️ Remember: NEVER say AI search, expiring share links, or device session management — you already built all three!)*
> *"Three things in priority order:*
> *1. **Edge-Enforced S3 `x-amz-checksum-sha256` on Presigned PUTs:** Right now, our Go server streams newly staged cache-miss chunks from R2 on `/complete` to verify their SHA-256 before `CopyObject`. Embedding the SHA-256 checksum constraint directly into the Presigned PUT signature would let R2/S3 reject tampered chunks at the storage edge during the upload itself, eliminating the server re-read pass for 50 GB+ files.*
> *2. **CRDT-Based Real-Time Collaborative Document Editing:** Currently, concurrent binary uploads use `pg_advisory_xact_lock` to create atomic versions (`v2`, `v3`) or branched copies, which is right for PDFs and videos, but integrating Yjs CRDTs over our existing WebSocket + Redis Pub/Sub hub would allow live multi-cursor text editing.*
> *3. **WebRTC LAN Peer-to-Peer Sync:** Similar to Dropbox LAN Sync — if two collaborators on the same office Wi-Fi sync the same CAS block, one browser could fetch the verified SHA-256 chunk directly from the peer over WebRTC DataChannels before falling back to R2."*

### 3. *"What is the one architectural decision you'd change if you started over?"*
> *"I would wire up Prometheus metrics and multi-tab concurrency tests on Day 1 rather than adding them after building the core features. When I first tested 8-stream parallel AIMD uploads and two-tab simultaneous uploads, I immediately hit my own `30 RPM` upload rate limit (`HTTP 429`) and hit a filename race condition before adding `pg_advisory_xact_lock`. Having observability counters and parallel integration tests from the very first commit surfaces those bottlenecks immediately."*

### 4. *"How did you validate correctness working solo without a QA team?"*
> *"Three ways:*
> *1. **Automated CI Pipeline with Race Detection:** In GitHub Actions (`ci.yml`), every push spins up live PostgreSQL 16 (`pgvector`) and Redis 7 containers and runs `go test -race -v ./...` — testing FastCDC cross-language hash parity (`TestCrossPlatformParity`), zero-trust upload verification, Materialized Path subtree moves, and Redis echo suppression.*
> *2. **Empirical Deduplication Benchmark (`cmd/bench-chunker/main.go`):** A 90-line Go test comparing Fixed 4 MB chunking against FastCDC on 100 MB file mutations.*
> *3. **Manual Fault Injection in Chrome DevTools:** Toggling 'Offline' mid-upload to verify 0-byte redundant resume, dragging the same file into two tabs at once to verify `pg_advisory_xact_lock`, and checking `docker stats` during 500 MB streams to confirm memory stayed flat."*

### 5. *"Did you use AI tools (like ChatGPT / Cursor / Copilot) while building this project?"*
> *"Yes — I used AI to speed up repetitive boilerplate like React modal scaffolding, SQL migration templates, and initial test harnesses. However, I designed the architecture myself and had to debug all the real integration bugs firsthand — like figuring out why JavaScript `BigInt` (`<< 1n`) diverged from Go's `uint64` on Byte 64 in FastCDC, fixing duplicate WebSocket frames caused by Redis Pub/Sub self-echoes, isolating R2 vs. SQS AWS credentials on EC2, and protecting in-flight upload sessions from CAS garbage collection."*

---

# PART C: THE 1-PAGE NUMBERS & CONSTANTS CHEAT SHEET
*(Review this table right before your interview — you can explain every single number in 10 seconds.)*

| Number / Metric | Where It Lives in Code | Plain-English 1-Sentence Explanation |
| :--- | :--- | :--- |
| **`1–8 MB` (`4 MB` avg) blocks** | `fastcdc.go` & `fastcdc.worker.ts` | *"1 MB minimum skips hashing on 25% of bytes and prevents tiny S3 objects; 4 MB average keeps Postgres metadata rows small; 8 MB ceiling bounds single-chunk retry cost."* |
| **`0x7fffff` & `0x1fffff` masks** | `fastcdc.go` & `fastcdc.worker.ts` | *"23-bit mask (`0x7fffff`) in `[1 MB, 4 MB)` prevents premature small cuts; 21-bit mask (`0x1fffff`) in `[4 MB, 8 MB)` makes finding a boundary 4× easier before hitting 8 MB."* |
| **`98%+` vs. `0%` chunk reuse** | `cmd/bench-chunker/main.go` & DevTools | *"Prepending 48–64 KB at byte 0 of a 100–256 MB file shifts every fixed 4 MB window (`0%` reuse), whereas FastCDC only changes Chunk #1 and reuses `25/26` (`97.9%` bytes) to `57/58` (`98.3%` chunks / `99.3%` bytes)."* |
| **`O(1)` memory / `32 KB` buffer** | `upload_service.go` (`io.Copy`) | *"Go's standard `io.Copy(sha256Hasher, r2Stream)` allocates a single fixed `32 * 1024` byte slice under the hood and reuses it until EOF instead of loading whole 8 MB chunks into RAM with `io.ReadAll`."* |
| **`4` Verification Goroutines** | `upload_service.go` (`CompleteSession`) | *"Bounded worker pool on `/upload/complete` that verifies up to 4 newly staged cache-miss chunks in parallel outside the DB transaction (`4 × 32 KB = 128 KB` total RAM)."* |
| **`1–8` parallel streams (AIMD)** | `UploadContext.tsx` | *"Browser worker pool starts at `3` parallel chunk `PUT` requests, adds `+1` up to `8` when chunk RTT `< 1500ms`, and halves concurrency down to `1` on timeout/429."* |
| **`0-byte` redundant re-uploads** | `upload_service.go` & `upload_session_blocks` | *"When resuming an interrupted upload, the backend checks `upload_session_blocks` in Postgres and only issues Presigned URLs for unconfirmed chunk sequence numbers."* |
| **`16 blocks` (~64 MB) check** | `file_handlers.go` (`DownloadFile`) | *"During HTTP 206 streaming, the server re-verifies share/file access every 16 chunks so revoked links terminate mid-stream without querying Postgres on every 32 KB read."* |
| **`< 3 ms` subtree queries** | `000018_materialized_paths.up.sql` | *"Composite B-Tree index `(user_id, path text_pattern_ops)` turns deep folder subtree queries (`LIKE '/prefix/%'`) into a single $O(1)$ byte-ordered index range scan."* |
| **`20–50 ms` DB TX hold time** | `upload_service.go` (`CompleteSession`) | *"Because all slow R2 network I/O (`HeadObject`, SHA-256 stream, `CopyObject`) runs in Phase 1 outside the transaction, the Phase 2 SQL transaction only writes metadata rows."* |
| **`10 / 120 / 300 RPM` limits** | `redis_limiter.go` & `config.go` | *"Atomic Redis Lua token-bucket rate limits per IP: `10 RPM` on `AUTH` (brute-force protection), `120 RPM` on `API` (browsing), and `300 RPM` on `UPLOAD` (for 8-stream AIMD)."* |
| **`25` Max DB Connections** | `database/postgres.go` (`MaxOpenConns = 25`) | *"Caps each Go API node at 25 Postgres connections so 3 API nodes (`75` total) never exhaust an 80-connection free-tier Postgres instance."* |
| **`768-dim` `pgvector` + `200ms`** | `gemini.go` & `processor.go` | *"Google's Gemini embedding model outputs a 768-float vector (`vector(768)`), and `GEMINI_EMBEDDING_PACE_MS = 200ms` paces multi-chunk PDF embeddings to avoid 429 quota errors."* |
| **`20s` SQS Long Polling** | `queue/worker.go` (`WaitTimeSeconds = 20`) | *"Holds SQS `ReceiveMessage` open for up to 20 seconds when the queue is empty, cutting empty SQS API requests by 97%."* |
