# BLOB-CLOUD: SENIOR ENGINEERING MANAGER "BRUTAL GRILL" & CHOKEHOLD GUIDE

*Tailored specifically to your exact 5-bullet resume section. Written from the perspective of a skeptical Senior Engineering Manager / Staff Backend Engineer whose job is to find out within 5 minutes whether you genuinely understand every word on your resume or if you are reciting AI buzzwords.*

---

## PART 0: RESUME VERDICT & THE PROMETHEUS / GRAFANA QUESTION

### 1. Is your current resume screenshot good enough?
**Yes — do not change the 5 bullets.**
Looking at your rendered PDF screenshot:
* Every bullet is a clean **2-line block** with zero awkward 1-word orphan lines.
* It hits every high-value backend ATS keyword (`Go`, `PostgreSQL`, `Redis`, `AWS SQS`, `AWS Lambda`, `S3 Presigned URLs`, `WebSockets`, `Docker`, `ACID`, `HTTP 206`, `pgvector`, `CAS`, `O(1)`).
* Both `98%+` (from the `256 MB` header-edit test: `57/58 = 98.3%` chunks, `99.3%` bytes; or `97.9%` bytes saved on `100 MB`) and `32 KB` (`io.Copy` buffer) are 100% real and defensible.

### 2. Should you add Prometheus & Grafana to the resume bullets?
**No — keep them out of the 5 project bullets, and use them as your "Ace Up the Sleeve" in the interview.**
* **Why not in the bullets?** Adding Prometheus/Grafana into those 5 bullets will spill a bullet onto a 3rd line and dilute the core distributed-systems story. (If you want the ATS keyword, just put `Prometheus` in your **Skills / DevOps** row at the bottom/top of your resume).
* **Why it's better to mention verbally:** When an interviewer asks, *"How do you know your deduplication ratio or rate limiter is working in production?"*, you casually say:
  > *"I didn't have space to fit it in the 5 resume bullets, but I instrumented the Go API with Prometheus metrics (`/metrics`) — specifically tracking `blobcloud_block_dedup_hits_total`, `blobcloud_block_dedup_misses_total`, `blobcloud_upload_sessions_total`, and HTTP latency histograms."*
  Pulling out an extra production feature that *wasn't even on your resume* makes you look 10x more genuine than cramming every tool into the bullet text.

---

## PART 1: THE 7 "CHOKEHOLD" TRIGGERS ON YOUR RESUME
*(What immediately catches a Senior EM's eye and makes them think: "Let's see if this candidate actually knows what this means.")*

When I (as a Senior EM) read your resume, **your project sounds like a Staff Engineer's system design doc**. That is great for passing ATS and getting shortlisted, **but it puts a target on your back in the live interview**. If you use terms like `FastCDC`, `CAS poisoning`, `TCP-style AIMD`, or `PostgreSQL advisory locks` and cannot explain them in plain, simple code terms within 15 seconds, you fail the interview immediately.

Here are the **7 exact chokeholds** I will put you in based on your resume text, and how to escape every single one:

---

### Chokehold #1: *"You wrote 'TCP-style AIMD (1–8 parallel streams)'... Doesn't TCP already do AIMD congestion control at the OS transport layer? Why did you reinvent AIMD in JavaScript?!"*
* **Why this puts candidates in a chokehold:** 95% of candidates freeze here because they never thought about why application-layer concurrency control is different from OS TCP congestion control.
* **What fails:** *"Uh, to make uploads faster and handle network congestion."* (Too vague — sounds like you copied the word AIMD from ChatGPT).
* **The Answer That Passes with Flying Colors:**
  > *"TCP handles packet-level congestion on a single socket, but in the browser we are opening multiple parallel HTTP `PUT` requests to Cloudflare R2 (`1 to 8` separate streams). Even if TCP manages packets on one connection, opening 8 parallel 4 MB streams on a slow Wi-Fi link causes bufferbloat, chunk timeouts, and HTTP 429/503 throttling from the storage gateway. And if I hardcode it to 1 stream on a fast fiber connection, I waste bandwidth waiting on HTTP round-trip overhead between chunks.*
  >
  > *By "TCP-style AIMD," I mean a simple 20-line worker-pool loop in `UploadContext.tsx`: I start with `3` parallel chunk workers. I measure each chunk's `PUT` time with `performance.now()`. If a chunk finishes in under `1.5 seconds`, I add `+1` concurrent worker up to `8` (Additive Increase). If a chunk errors out or hits a 429/timeout, I cut the active worker count in half (`Math.floor(cwnd / 2)`) down to `1` (Multiplicative Decrease) and retry with exponential backoff."*

---

### Chokehold #2: *"You claim you stream staged chunks from R2 to verify SHA-256 on `/upload/complete`. Wait — if I upload a 4 GB movie, does your Go server have to download all 4 GB from R2 back to EC2 just to hash it before returning 200 OK?! Won't that take minutes and time out the HTTP request?!"*
* **Why this is the #1 System Design Chokehold on Bullet 2:** You bypassed the Go server during upload using Presigned URLs, *only to make the Go server read the chunks from R2 during `/complete`*! Any sharp Senior Engineer will spot this immediately.
* **What fails:** Pretending there is zero latency cost, or getting defensive.
* **The Answer That Passes with Flying Colors (Own the trade-off + show how you bounded it):**
  > *"That's the exact architectural trade-off I had to make for Zero-Trust CAS. In a naive presigned-URL architecture, the server never touches the bytes, which is fast, **but** a client can lie about a chunk's SHA-256 hash and poison a shared block for every user in the system.*
  >
  > *To make server-side verification fast enough for production, three things happen:*
  > *1. **Only brand-new (cache-miss) chunks are verified** — any deduplicated chunk already in `blocks/{sha256}` is skipped completely.*
  > *2. **Bounded Parallel Verification Outside the DB Transaction** — in `upload_service.go`, I run a bounded worker pool (up to 4 concurrent goroutines) that streams and hashes the staged chunks from R2 over cloud-to-cloud backbone bandwidth **before** opening the PostgreSQL transaction, each using a 32 KB `io.Copy` buffer (`~128 KB` total RAM).*
  > *3. **How I'd scale it for 50 GB+ files at Dropbox scale:** For multi-gigabyte uploads, synchronous verification in `/complete` would eventually hit HTTP gateway timeouts. At that scale, I would use S3's native `x-amz-checksum-sha256` signed header constraint directly inside the Presigned PUT signature so S3/R2 rejects mismatched hashes at the edge during the `PUT` itself, eliminating the server re-read entirely."*
  *(Note: Mentioning `x-amz-checksum-sha256` at the end proves you understand cloud storage deeply and aren't just reading a script!)*

---

### Chokehold #3: *"Walk me through the exact math of Rolling Gear Hashing in FastCDC. Don't give me high-level fluff — what is the bit operation per byte, and why do you have two masks?"*
* **Why this puts candidates in a chokehold:** Putting "rolling Gear hashing" on a resume invites the interviewer to ask for the exact formula.
* **What fails:** Confusing it with Rabin-Karp modulo arithmetic (`(hash * base + byte) % prime`) or not knowing what `1–8 MB` normalization means.
* **The Answer That Passes:**
  > *"It's a 3-line loop over the byte array. We have a precomputed array `GEAR_TABLE` of 256 random 64-bit unsigned integers — one for each possible byte value `0..255`.*
  >
  > *First, we immediately skip the first `1 MB` (`MinSize`) without hashing at all — that prevents tiny chunks and speeds up chunking by ~25%.*
  > *Then, for each byte `b` from `1 MB` up to `8 MB` (`MaxSize`), we update a 64-bit integer `hash`:*
  > ```go
  > hash = (hash << 1) + GEAR_TABLE[b]
  > ```
  > *(In JavaScript Web Workers, because `BigInt` has infinite bits instead of 64-bit overflow, I had to explicitly mask it with `& 0xFFFFFFFFFFFFFFFFn`).*
  >
  > *Why two masks? If you use a single mask (`hash & 0x3fffff == 0` for a 4 MB average), chunk sizes follow a skewed exponential curve with lots of 1.1 MB chunks and lots of 8 MB forced cuts. So FastCDC uses **normalized chunking**:*
  > *- Between `1 MB` and `4 MB`, we check `hash & 0x7fffff == 0` (**23 zero bits** — harder to trigger, so we don't cut too early).*
  > *- Between `4 MB` and `8 MB`, we switch to `hash & 0x1fffff == 0` (**21 zero bits** — 4x easier to trigger, so it smoothly finds a cut point before hitting the 8 MB wall)."*

---

### Chokehold #4: *"You said you do R2 `CopyObject` (promoting `staging/` to `blocks/`) BEFORE starting the PostgreSQL transaction so you don't hold DB locks. Okay — what happens if `CopyObject` succeeds, and 10ms later your Postgres transaction fails or the server crashes? Now you have orphaned files sitting in R2 forever!"*
* **Why this is a classic Senior EM failure-injection question:** Whenever you separate Object Storage I/O from Database ACID transactions, you create a distributed two-phase commit gap.
* **The Answer That Passes:**
  > *"That's the classic dual-write problem between S3 and SQL. I intentionally chose to risk a temporary orphan in R2 rather than holding a Postgres connection lock during network I/O, because DB connections are scarce (`max_conns = 25`) while R2 storage is cheap.*
  >
  > *To clean up those orphans, I built a two-layer garbage collection mechanism (`cmd/gc` and `upload_service.go`):*
  > *1. When `PromoteObject` runs, we still have the `blocks` table metadata check. If the Postgres transaction fails after R2 promotion, the block in `blocks/{sha256}` has no row in `file_blocks` or `file_versions`.*
  > *2. Our background Garbage Collector (`CleanupExpiredSessions` + orphan block sweep) cleans up expired `upload_sessions` older than 24 hours, deletes abandoned `staging/{session_id}/*` keys in R2, and purges any `blocks` that have zero references across `file_blocks`, `file_versions`, and active `upload_session_blocks`."*

---

### Chokehold #5: *"Why did you use `PostgreSQL advisory locks` (`pg_advisory_xact_lock`)? Postgres already has `UNIQUE` constraints and `INSERT ... ON CONFLICT`! Why didn't you just use `ON CONFLICT`?"*
* **Why this puts candidates in a chokehold:** Interviewers love asking why a simpler SQL feature (`UNIQUE` index / `ON CONFLICT`) wasn't enough.
* **The Answer That Passes:**
  > *"Two reasons:*
  > *First, our uniqueness rule on `files` is a **partial unique index**: `UNIQUE (user_id, parent_id, name) WHERE deleted_at IS NULL` (and `parent_id` can also be `NULL` at the root folder, where standard SQL `NULL != NULL` requires `COALESCE`).*
  > *Second, when a filename collision happens on `/upload/complete`, we don't just do a blind upsert on a single table — we have to execute a **multi-table branching workflow** based on the user's `conflict_mode`:*
  > *- If `conflict_mode == 'version'`, we have to read the existing `file_id`, query `SELECT COALESCE(MAX(version_number), 0) + 1 FROM file_versions`, snapshot the old blocks into `file_versions`, replace `file_blocks`, and update `files`.*
  > *- If `conflict_mode == 'copy'`, we have to loop to find the next free name (`report (1).pdf`, `report (2).pdf`) and insert a brand-new file.*
  >
  > *Without `pg_advisory_xact_lock(hash(user_id, parent_id, name))` at the start of the transaction, two tabs uploading `report.pdf` simultaneously both read `version = 1`, both compute `next_version = 2`, and one crashes with a 500 constraint violation. The transaction-scoped advisory lock serializes just that specific `(user, folder, filename)` tuple for ~15ms and automatically releases on `COMMIT` or `ROLLBACK`."*

---

### Chokehold #6: *"How does `HTTP 206 Range` streaming work when a file isn't a single file in S3, but 50 variable-sized FastCDC chunks? If the browser asks for `Range: bytes=15000000-25000000`, how do you stitch that together?"*
* **Why this puts candidates in a chokehold:** Standard S3 Range requests just pass the `Range` header to S3. Because your files are split into variable-size CAS chunks (`1–8 MB`), you can't just redirect the browser to a single S3 presigned URL for a byte range that spans multiple chunks!
* **The Answer That Passes:**
  > *"Because chunks are variable-sized (`1–8 MB`), I can't just do `chunk_index = offset / 4MB`. Instead, in `DownloadFile`, I query the ordered list of chunks (`SELECT sequence_number, size, sha256 FROM file_blocks JOIN blocks ... ORDER BY sequence_number ASC`) — which for a 200 MB file is only ~50 rows in Postgres.*
  >
  > *In Go, I keep a running byte counter `currentOffset = 0` as I walk that slice of 50 structs in memory:*
  > *1. I skip any block where `currentOffset + block.Size <= rangeStart`.*
  > *2. Once I hit the block that contains `rangeStart`, I calculate the intra-block start offset (`startInBlock = rangeStart - currentOffset`), open the R2 stream for `blocks/{sha256}`, discard/seek to `startInBlock`, and wrap it in an `io.LimitReader` for the exact number of bytes needed from that block.*
  > *3. I stream those bytes to `http.ResponseWriter` with status `206 Partial Content` and header `Content-Range: bytes start-end/totalSize`, and continue across the next block until `rangeEnd` is satisfied — all using the same 32 KB `io.Copy` buffer."*

---

### Chokehold #7: *"In Bullet 4, you mention '768-dim pgvector embeddings for hybrid semantic search.' How do you actually combine keyword search and vector similarity in SQL? And what happens if a user uploads a 100-page PDF — doesn't a single 768-dim vector lose all detail?"*
* **Why this catches candidates off-guard:** Many people slap `pgvector` on their resume without knowing how long documents are chunked for embeddings or how hybrid search ranks results.
* **The Answer That Passes:**
  > *"If you embed an entire 50-page PDF into a single 768-float vector, the meaning gets averaged out into noise. So in our Lambda worker (`processor.go`), after extracting text from the file, we split the text into overlapping character chunks (`chunkText` with `chunk_index = 0, 1, 2...`), call Gemini's `text-embedding-004` model (paced at 200ms per chunk so we don't hit 429 rate limits), and store multiple rows per file in `file_embeddings (file_id, chunk_index, content_chunk, embedding vector(768))`.*
  >
  > *At search time (`SearchFiles`), we run PostgreSQL Full-Text Search (`to_tsvector` / `ILIKE` on filenames and AI summaries) alongside `pgvector` cosine distance (`1 - (embedding <=> $queryVector)`), taking the `MAX` similarity chunk per file and filtering by a minimum cosine threshold so irrelevant files aren't returned."*

---

## PART 2: COMPLETE BULLET-BY-BULLET INTERVIEW QUESTION & FOLLOW-UP BANK

### Category A: FastCDC, Web Workers & Content-Addressable Storage (Bullet 1)

**Q1: Why did you do FastCDC chunking in the browser (Web Worker) instead of streaming the file to the Go server and chunking it in Go?**
* **Follow-up:** *"Wait — if you chunk in the browser, you also have a Go chunker (`internal/chunker/fastcdc.go`). Why did you write the chunker in both TypeScript and Go?"*
* **Ideal Answer:**
  > *"Chunking in the browser Web Worker gives us two massive wins:*
  > *1. **Zero upload bandwidth for duplicate chunks:** Before a single byte of file payload leaves the user's laptop, the Web Worker computes the FastCDC boundaries and SHA-256 hashes and sends just the lightweight hash manifest to `/api/upload/initiate`. If 25 out of 26 chunks already exist in R2, the user never uploads that 98 MB over their slow home Wi-Fi.*
  > *2. **60fps UI responsiveness:** Running Gear hashing and SHA-256 on the main browser thread would freeze React rendering for several seconds on a 500 MB file; running it inside a dedicated Web Worker keeps the UI thread completely non-blocking.*
  >
  > *Why did I also implement `fastcdc.go` on the backend? First, to verify deterministic cross-language parity (`TestCrossPlatformParity`) and run my Go deduplication benchmarks (`cmd/bench-chunker`); and second, so server-side streams or CLI tools can chunk data using the exact same `GEAR_TABLE` and masks."*

**Q2: What happens if two users upload the exact same file, and then User A deletes their file? Doesn't that delete the blocks from Cloudflare R2 and break User B's file?**
* **Ideal Answer:**
  > *"No, because physical `blocks` and logical `files` have a many-to-many relationship via the `file_blocks` junction table (and `file_versions.chunk_hashes`). When User A deletes their file:*
  > *1. A normal delete is a **soft delete** (`UPDATE files SET deleted_at = NOW()`), which moves it to the Trash and doesn't touch R2 at all.*
  > *2. When User A empties their Trash (**hard delete**), our SQL query `findOrphanedHashes` checks if any other row in `file_blocks`, `file_versions`, or in-flight `upload_session_blocks` across the entire database still references those SHA-256 hashes. It only issues an R2 `DeleteObject` for hashes whose reference count across all three tables is strictly zero."*

---

### Category B: Zero-Trust Quarantine & ACID Storage Transactions (Bullet 2)

**Q3: Explain what "CAS Poisoning" means as if I've never heard the term before.**
* **Ideal Answer:**
  > *"In a Content-Addressable Storage (CAS) system, every chunk is stored globally under its SHA-256 hash: `blocks/{sha256}`. Suppose Company A uploads a standard PDF template, and Chunk #1 has hash `abc123...`. Now suppose an attacker (User B) calls `/api/upload/initiate` and lies in their JSON request: they claim they have a chunk with hash `abc123...` (or another hash that a victim is about to upload), and if the server gives them a Presigned PUT URL directly to `blocks/abc123...`, the attacker can upload corrupted bytes or malware into `blocks/abc123...`. Now, anyone whose file references `abc123...` downloads the attacker's poisoned bytes!*
  >
  > *My Zero-Trust Quarantine stops this completely: Presigned PUT URLs only ever point to `staging/{session_id}/{sequence_number}`. The client can never write to `blocks/`. Only the Go server can copy an object into `blocks/{sha256}`, and it only does so after streaming the staged object through `crypto/sha256` and verifying that the real hash of the bytes matches what the client claimed."*

**Q4: What is the "Disk Edit After Hashing" bug, and how did your quarantine catch it?**
* **Ideal Answer:**
  > *"When a user selects a local file in the browser, the browser holds a lazy file reference (`File` / `Blob`), not an in-memory copy of the whole file. First, the Web Worker slices the file and computes the SHA-256 hashes (`Phase 1`). Thirty seconds later, the upload worker pool slices the file again to `PUT` Chunk #15 to R2 (`Phase 2`). If the user edited and saved that file on their local disk during those 30 seconds, the bytes uploaded in Phase 2 no longer match the SHA-256 computed in Phase 1! Our server-side streaming SHA-256 check catches this mismatch on `/complete` and aborts cleanly with an integrity error instead of silently corrupting the file."*

---

### Category C: Resumable Uploads, AIMD & Concurrency (Bullet 3)

**Q5: Walk me through step-by-step what happens when a user is uploading a 200 MB file (50 chunks), their laptop Wi-Fi drops at chunk 25, and they reconnect 2 minutes later.**
* **Ideal Answer:**
  > *"1. When the upload first started (`/api/upload/initiate`), the backend created an `upload_sessions` row (`status = 'INITIATED'`) and recorded all 50 expected chunks in `upload_session_blocks`.*
  > *2. As the first 25 chunks finished uploading to `staging/{session_id}/{0..24}`, those objects now sit durably in Cloudflare R2 staging (and are tracked per sequence number).*
  > *3. When Wi-Fi drops at chunk 25, our AIMD worker pool in `UploadContext.tsx` catches the network error, halves its concurrency window, and tries exponential backoff (`1s, 2s, 4s`).*
  > *4. When the user resumes the interrupted session, the backend checks which chunks for that session are already verified/staged in R2 and `upload_session_blocks`, skips chunks `0..24` (`0 bytes` re-uploaded), and only streams chunks `25..49`."*

**Q6: What happens if a user revokes a public share link while someone is in the middle of streaming a 2 GB video via `HTTP 206 Range`?**
* **Ideal Answer:**
  > *"Because our files are split into `1–8 MB` chunks, the server loops chunk-by-chunk as it streams the video. In `file_handlers.go` / `share_handlers.go`, every `16 blocks` (~64 MB) inside the streaming loop, the server re-checks the share permission/revocation status in Postgres (`verifyStreamAccess`). If the owner revoked the link or deleted the file mid-stream, the server immediately terminates the stream and closes the connection."*

---

### Category D: Amazon SQS, AWS Lambda, ClamAV & `pgvector` (Bullet 4)

**Q7: Why did you use Amazon SQS + AWS Lambda instead of just spawning a background goroutine `go processFile()` inside your EC2 Go server?**
* **Follow-up:** *"What happens if the Go server still had its background worker enabled while Lambda was also running?"*
* **Ideal Answer:**
  > *"Spawning `go processFile()` inside the API server has two fatal flaws on a small EC2 instance:*
  > *1. **CPU/Memory Starvation:** Running ClamAV scans, image/video thumbnailing, and PDF parsing inside the web server spikes CPU and RAM, causing p99 latency spikes and OOM crashes for active HTTP/WebSocket users.*
  > *2. **Zero Durability on Restart:** If I deploy a new version or the EC2 server restarts while `go processFile()` is halfway through, that background job is lost forever.*
  >
  > *By pushing a job message to **Amazon SQS** and consuming it with **AWS Lambda**, heavy compute scales independently in serverless containers, and SQS visibility timeouts guarantee at-least-once retry if a worker crashes.*
  >
  > *Funny enough, I actually ran into your follow-up question in production! Initially, my Go API binary also had an in-process SQS poller (`ENABLE_INPROCESS_WORKER`) enabled by default. When I deployed to EC2 alongside AWS Lambda, both EC2 and Lambda were polling SQS, and because EC2's default AWS credentials were scoped to Cloudflare R2 storage, EC2 kept logging 403 errors on SQS polling. I fixed this by separating `SQS_AWS_ACCESS_KEY_ID` from R2 credentials and defaulting `ENABLE_INPROCESS_WORKER=false` on EC2 so the API server is strictly an SQS producer."*

---

### Category E: Real-Time Delta Sync, Monotonic Cursors & Redis Pub/Sub (Bullet 5)

**Q8: Explain your "tickle-and-fetch" Delta Sync pattern. Why not just send the updated file JSON directly inside the WebSocket message?**
* **Ideal Answer:**
  > *"If you push the actual file JSON over WebSockets, you have no ordering guarantee and no recovery when a connection drops. If a user is on a train, loses signal for 5 seconds while 4 files are moved, and reconnects, those 4 WebSocket messages are gone forever — their screen is now lying to them unless they hit F5.*
  >
  > *Instead, every mutation (`CREATE`, `UPDATE`, `MOVE`, `DELETE`) inserts a row into an append-only PostgreSQL table `journal_entries` inside the same ACID transaction, getting a strictly increasing `BIGSERIAL` integer (`cursor = 101, 102, 103...`).*
  > *The WebSocket message is just a lightweight 'tickle' (`{type: 'SYNC_DELTA', cursor: 103}`). When the browser receives the tickle (or reconnects after being offline), it compares `103` to its local `lastSeenCursor` (say `99`) and calls `GET /api/sync/delta?cursor=99`. Postgres does an $O(1)$ B-Tree index seek (`WHERE user_id = $1 AND cursor > 99 ORDER BY cursor ASC`) and returns exact mutations `100, 101, 102, 103` in order."*

**Q9: You run multiple Go API nodes behind Redis Pub/Sub. When Node 1 publishes a message to Redis channel `sync:events`, isn't Node 1 also subscribed to `sync:events`? How did you stop Node 1 from sending duplicate WebSocket messages to its own clients?**
* **Ideal Answer:**
  > *"Yes! That was a real bug I fixed in `internal/sync/backplane.go`. In `NotifyUser`, Node 1 immediately delivers the WebSocket event to its local in-memory `Hub` for zero-latency local delivery, and then calls `redis.Publish` so Node 2 and Node 3 can notify their connected clients. Because Node 1 is also subscribed to the Redis channel, Redis echoes that message right back to Node 1.*
  >
  > *To prevent duplicate delivery, each Go server generates a random `nodeID` (`uuid.NewString()`) on startup and attaches it to every Redis JSON payload. In the Redis subscriber loop (`dispatch`), if `envelope.NodeID == b.nodeID`, it drops the echoed message immediately."*

---

## PART 3: TOUGH SYSTEM DESIGN & PRODUCTION SCENARIOS THEY WILL PUT YOU IN

### Scenario 1: "Suppose Redis goes down completely in production. What breaks in your app, and how does it behave?"
* **How to answer like a Senior Engineer:**
  > *"I designed both Redis consumers — the **Rate Limiter** and the **WebSocket Sync Backplane** — to **fail open / degrade gracefully** so a Redis outage never takes down core file uploads or downloads:*
  > *1. **Rate Limiter (`rate_limit.go`):** If the Redis Lua token-bucket script returns a connection error, the middleware logs a warning and falls back to an in-memory per-node token bucket (or fails open) rather than returning HTTP 500s on every API call.*
  > *2. **WebSocket Sync (`backplane.go`):** Because `NotifyUser` dispatches to the local in-memory `Hub` **before** publishing to Redis, users connected to the same API node still get instant WebSocket updates. Users on other nodes won't get the Redis Pub/Sub tickle until Redis reconnects, **but** their periodic/focus delta-sync poll (`GET /api/sync/delta?cursor=X`) hits PostgreSQL directly, so they still catch up cleanly without losing a single event."*

### Scenario 2: "How do you represent nested folders in PostgreSQL? What happens when a user moves a top-level folder that has 5,000 nested subfolders and files inside it?"
* **How to answer:**
  > *"I used the **Materialized Path** pattern (`path TEXT` storing `/rootUUID/parentUUID/thisUUID/`) indexed with a PostgreSQL `text_pattern_ops` B-Tree index (`CREATE INDEX idx_files_user_id_path_pattern ON files (user_id, path text_pattern_ops)`).*
  >
  > *If you only store `parent_id` (Adjacency List), finding all 5,000 descendants to check permissions, calculate folder size, or zip a directory requires an expensive `WITH RECURSIVE` CTE join across every depth level. With Materialized Path, fetching the entire 5,000-item subtree is a single $O(1)$ B-Tree prefix scan:*
  > ```sql
  > SELECT * FROM files WHERE user_id = $1 AND path LIKE '/rootUUID/parentUUID/%';
  > ```
  > *When moving that folder to a new parent, a single SQL statement updates all 5,000 descendant paths atomically using string prefix replacement (`SET path = $newPrefix || SUBSTRING(path FROM $oldLen)`). And before moving, I lock the source and destination folders in deterministic UUID order and check that `dest.path` does not start with `source.path` — preventing a circular folder cycle (`moving Folder A inside its own child Folder B`)."*

### Scenario 3: "How does your Go WebSocket Hub avoid `concurrent map read and map write` panics when 1,000 users connect, disconnect, and send messages at the same time?"
* **How to answer:**
  > *"In Go, standard `map` types are not goroutine-safe — reading and writing concurrently crashes the entire process with an unrecoverable fatal panic. Instead of sprinkling `sync.RWMutex` locks across every HTTP handler, I implemented the **Actor Model** in `internal/sync/hub.go`:*
  >
  > *The `Hub` owns the `clients map[string]map[*Client]bool` privately inside a single dedicated background goroutine running a `for { select { ... } }` loop over three channels: `register`, `unregister`, and `broadcast`. Because only that one goroutine ever touches the map, data races are structurally impossible. And when broadcasting to a client's buffered `send` channel, I use a non-blocking `select { case client.send <- msg: default: close(client.send) }` so a single stalled browser tab with a dead TCP socket can never block the Hub loop for other users."*

---

## PART 4: QUICK CHEAT SHEET — NUMBERS YOU CAN OWN 100% ON THE SPOT

Whenever an interviewer asks **"Where did you get that number on your resume?"**, keep your answer grounded in these dead-simple facts:

| Resume Number | Simple 1-Sentence Explanation You Can Own 100% |
| :--- | :--- |
| **`1–8 MB` variable blocks** | *"Defined in `DefaultConfig()` (`MinSize = 1 MB`, `AvgSize = 4 MB`, `MaxSize = 8 MB`) — 4 MB average keeps Postgres metadata rows small while avoiding S3 small-object request overhead."* |
| **`98%+` vs. `0%` chunk reuse** | *"Tested on a 100 MB–256 MB file (`cmd/bench-chunker/main.go` and Chrome DevTools): prepending 48–64 KB at byte 0 shifts every fixed 4 MB boundary (`0%` reused), whereas FastCDC only changes Chunk #1 and reuses `25/26` (`97.9%` bytes) to `57/58` (`98.3%` chunks / `99.3%` bytes)."* |
| **`O(1)` memory / `32 KB` buffer** | *"That's Go's standard `io.Copy(sha256Hasher, r2Stream)` in `upload_service.go` — `io.Copy` allocates a fixed `32 * 1024` byte slice under the hood instead of loading whole 8 MB chunks into RAM with `io.ReadAll`."* |
| **`1–8` parallel streams (AIMD)** | *"Constants in `UploadContext.tsx`: starts at `3` parallel chunk `PUT` workers, adds `+1` up to `8` when chunk RTT `< 1500ms`, and halves `cwnd` down to `1` on timeout/429."* |
| **`0-byte` redundant re-uploads** | *"Tested by toggling 'Offline' in Chrome DevTools mid-upload: `/complete` & session reconciliation checks `upload_session_blocks` in Postgres and skips every chunk sequence already staged in R2."* |
| **`768-dim pgvector`** | *"Google's Gemini text embedding model outputs a 768-float vector (`vector(768)` in `000010_add_ai_pipeline.up.sql`), which we index in Postgres for cosine similarity (`<=>`)."* |
| **`O(1)` cursor catch-up** | *"`journal_entries` uses a monotonic `BIGSERIAL` primary key with a B-Tree index on `(user_id, cursor)`, so `WHERE user_id = $1 AND cursor > $lastSeen` is a direct B-Tree index seek."* |
