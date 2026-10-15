# SECTION 5: SENIOR EM "CHOKEHOLD" TRAPS & 8 REAL-LIFE DEBUGGING WAR STORIES
*Part 6 of 7 of the Blob-Cloud Interview Master Series*

---

> **Why this is the most important section for sounding 100% genuine:**
> Any candidate can ask an AI to write impressive resume bullets. What separates a candidate who gets hired on the spot from one who gets rejected is **how they answer when pushed into a corner**:
> 1. **Part A (The 7 Senior EM Chokeholds):** When an interviewer spots a sophisticated term on your resume (`TCP-style AIMD`, `Zero-Trust SHA-256 over Presigned URLs`, `PostgreSQL advisory locks`) and challenges whether it actually makes sense.
> 2. **Part B (The 8 Real-Life Debugging Stories):** When an interviewer asks: *"Tell me about a bug or problem you ran into while building this and walk me through how you figured it out."*
>
> Every story in Part B is based on **real code and real bugs from your repository**, told from your first-person perspective at your laptop (what you saw break in Chrome DevTools or terminal logs, what your first guess was, how you narrowed it down, and the exact fix you wrote).

---

# PART A: THE 7 SENIOR EM "CHOKEHOLD" TRAP QUESTIONS

---

### Chokehold 1: The Browser AIMD vs. OS TCP Trap
* **The Interviewer's Question:**
  *"In Bullet 3, you wrote 'TCP-style AIMD (1–8 parallel streams)'. Wait — TCP at the OS kernel level ALREADY runs congestion control! Why did you reinvent AIMD in JavaScript inside your React app? Aren't you fighting the OS TCP stack?"*
* **Why They Ask This:** They want to see if you just threw the acronym "AIMD" onto your resume without understanding the difference between transport-layer packet congestion and application-layer HTTP request concurrency.
* **❌ Weak Answer (Do NOT say this):** *"To make uploads faster and prevent network congestion."*
* **✅ Your Winning Answer:**
  > *"OS TCP only manages packet window size on a single socket, whereas in the browser we are deciding **how many parallel HTTP `PUT` requests (`1 to 8` separate chunk streams)** to open simultaneously to Cloudflare R2.*
  >
  > *If I hardcode 8 parallel 4 MB chunk uploads on a weak home Wi-Fi or mobile hotspot, the browser saturates the uplink buffer, individual chunks time out, and the storage gateway starts returning HTTP 429 or 503 errors. On the flip side, if I hardcode 1 stream on a fast fiber connection, we waste bandwidth waiting on HTTP round-trip overhead between chunks.*
  >
  > *So my AIMD loop in `UploadContext.tsx` controls **worker-pool concurrency**: it starts with `3` parallel chunk workers and times each `PUT` with `performance.now()`. If a chunk finishes in under `1.5 seconds` (`LATENCY_THRESHOLD_MS`), it adds `+1` worker up to `8` (Additive Increase). If a chunk times out or hits a `429`, it cuts the active worker count in half (`Math.floor(cwnd / 2)`) down to `1` (Multiplicative Decrease) and backs off exponentially."*

---

### Chokehold 2: The Zero-Trust 4 GB Re-Streaming Latency Trap
* **The Interviewer's Question:**
  *"Look at Bullet 2: you use S3 Presigned URLs so upload bytes bypass your Go server, **but** then on `/upload/complete` your Go server streams the staged chunks back from R2 to verify their SHA-256! Wait — if I upload a 4 GB movie, doesn't your Go server have to download all 4 GB from R2 just to hash it before returning 200 OK?! Won't that bottleneck your server and time out the HTTP request?"*
* **Why They Ask This:** This is the #1 architectural tension in your upload pipeline. If you pretend there is zero trade-off, you fail. If you own the trade-off and explain how you bounded it (plus how to push it to the S3 edge at 50 GB+ scale), you sound like a Senior Engineer.
* **✅ Your Winning Answer:**
  > *"You spotted the exact trade-off I had to weigh when designing Zero-Trust CAS. In a naive presigned-URL flow, the server never touches the bytes, which is fast — **but** a client can lie about a chunk's SHA-256 hash and upload corrupted bytes into `blocks/{Hash_A}`, poisoning that block for every user in the system.*
  >
  > *To make server-side verification fast in practice, three things happen:*
  > *1. **Only cache-miss chunks are verified:** Any deduplicated chunk already in `blocks/{sha256}` is skipped completely.*
  > *2. **Bounded 4-goroutine parallel verification outside the DB transaction:** In `upload_service.go`, I run a 4-goroutine worker pool that streams and hashes the newly staged chunks concurrently over high-speed cloud-to-cloud bandwidth **before** `db.BeginTx` is ever called, using a 32 KB `io.Copy` buffer per worker (`~128 KB` total RAM).*
  > *3. **How I'd evolve it for 50 GB+ files:** For multi-gigabyte raw video files where synchronous verification on `/complete` would eventually hit HTTP gateway timeouts, the next evolution is embedding S3's native `x-amz-checksum-sha256` signed header directly into the Presigned PUT signature — that forces R2/S3 to compute and verify the SHA-256 at the storage edge during the client's `PUT` itself, eliminating the server re-read pass entirely."*

---

### Chokehold 3: The S3 + SQL Dual-Write Crash (Orphaned R2 Blocks)
* **The Interviewer's Question:**
  *"You said you run `CopyObject` in R2 (promoting `staging/` to `blocks/`) **before** opening the PostgreSQL transaction so you don't hold DB locks during network I/O. Okay — what happens if `CopyObject` succeeds in R2, and 5 milliseconds later your Postgres transaction fails or EC2 loses power? Now you have orphaned data sitting in R2 forever!"*
* **✅ Your Winning Answer:**
  > *"That's the classic dual-write gap whenever you coordinate Object Storage with an SQL database without distributed two-phase commit. I intentionally chose to risk a temporary orphan in R2 rather than holding a Postgres transaction open during S3 network calls, because Postgres connections are scarce (`MaxOpenConns = 25`) while R2 storage is cheap.*
  >
  > *Here is how the system recovers cleanly:*
  > *1. If EC2 crashes after `CopyObject` but before the DB commit, the `upload_sessions` row in Postgres is still in `INITIATED` status. When the client retries `/complete`, `PromoteObject` is idempotent (`blocks/{sha256}` is content-addressed) and the transaction commits cleanly.*
  > *2. If the user closes their browser and never retries, that promoted block in `blocks/{sha256}` has zero rows referencing it in `file_blocks` or `file_versions`. Our background Garbage Collector (`cmd/gc` / `collector.go`) sweeps expired sessions older than 24 hours and deletes any R2 block that has zero references in Postgres."*

---

### Chokehold 4: Why `pg_advisory_xact_lock` Instead of `INSERT ... ON CONFLICT`?
* **The Interviewer's Question:**
  *"You put 'PostgreSQL advisory locks' on your resume. Postgres already has `UNIQUE` constraints and `INSERT ... ON CONFLICT DO UPDATE`! Why didn't you just use a normal UPSERT?"*
* **✅ Your Winning Answer:**
  > *"Standard `INSERT ... ON CONFLICT` only works when you are upserting a single row into a single table. When two browser tabs upload `report.pdf` into the same folder simultaneously, our `/upload/complete` transaction has to coordinate across **four tables** (`files`, `file_versions`, `file_blocks`, `journal_entries`) and execute a branching workflow based on the user's `conflict_mode`:*
  > *- If `conflict_mode == 'version'`, we have to snapshot the existing file's block hashes into `file_versions`, query `SELECT COALESCE(MAX(version_number), 0) + 1`, replace `file_blocks`, and update `files`.*
  > *- If `conflict_mode == 'copy'`, we have to loop to find the next available name (`report (1).pdf`) and insert a new file.*
  > *- Furthermore, our uniqueness rule is a partial index (`WHERE deleted_at IS NULL`) where `parent_id` can be `NULL` at the root folder.*
  >
  > *Without `pg_advisory_xact_lock(hashtext(user_id || parent_id || filename))` at the very start of the transaction, both tabs read the current state simultaneously before either commits, and one crashes on a constraint violation. The advisory lock serializes just that specific `(user, folder, filename)` key for ~20ms and releases automatically on commit."*

---

### Chokehold 5: The Rolling Gear Hash Bit-Math Test
* **The Interviewer's Question:**
  *"Don't give me a high-level summary — write down the exact line of code that updates the Rolling Gear Hash for each byte, and tell me why a single bitmask isn't good enough."*
* **✅ Your Winning Answer:**
  > *"The per-byte update is one line:*
  > ```go
  > hash = (hash << 1) + gearTable[b]
  > ```
  > *(And in JavaScript Web Workers, `hash = ((hash << 1n) + GEAR_TABLE[b]) & 0xffffffffffffffffn`).*
  > *Because `hash` is a 64-bit unsigned integer, shifting left by 1 bit (`<< 1`) pushes the contribution of the byte from 64 positions ago off the top of the register automatically — so we don't need Rabin-Karp's expensive subtraction/XOR step.*
  >
  > *Why aren't we using a single mask like `hash & 0x3fffff == 0` (22 bits for a 4 MB average)? Because memoryless Bernoulli trials follow an exponential distribution: you end up with lots of chunks cut right after the 1 MB minimum and lots of chunks hitting the 8 MB max ceiling. By using a stricter **23-bit mask (`0x7fffff`)** between `1 MB` and `4 MB`, we suppress early cuts; by switching to a looser **21-bit mask (`0x1fffff`)** between `4 MB` and `8 MB`, we make cuts 4× more likely after 4 MB, creating a tight bell-like curve centered around 4 MB."*

---

### Chokehold 6: Multi-Chunk `HTTP 206 Range` Seeking Math
* **The Interviewer's Question:**
  *"Suppose a 100 MB video was cut by FastCDC into variable-sized chunks: Chunk 0 is `3.2 MB`, Chunk 1 is `5.1 MB`, Chunk 2 is `2.8 MB`, etc. The video player sends `Range: bytes=6000000-10000000`. Walk me through the exact math your Go server does to serve that request."*
* **✅ Your Winning Answer:**
  > *"In `DownloadFile`, I fetch the ordered chunk list from Postgres (`ORDER BY sequence_number ASC`). Then I iterate through the chunks keeping a cumulative byte counter `currentOffset = 0`:*
  > *1. **Chunk 0 (`3.2 MB`):** `currentOffset` is `0`, chunk ends at `3.2 MB`. Since `3.2 MB <= 6.0 MB` (`rangeStart`), I advance `currentOffset = 3.2 MB` and skip Chunk 0 completely.*
  > *2. **Chunk 1 (`5.1 MB`):** Starts at `3.2 MB`, ends at `8.3 MB`. This overlaps `6.0 MB`! I compute the intra-chunk start offset: `skip = 6.0 MB - 3.2 MB = 2.8 MB`. The remaining bytes in Chunk 1 are `5.1 MB - 2.8 MB = 2.3 MB`. I open `blocks/{Chunk1_SHA256}` from R2, discard the first `2.8 MB`, and pipe the remaining `2.3 MB` to `http.ResponseWriter` via `io.Copy`.*
  > *3. **Chunk 2 (`2.8 MB`):** Starts at `8.3 MB`. We still need `4.0 MB - 2.3 MB = 1.7 MB` to reach `10.0 MB` (`rangeEnd`). I open `blocks/{Chunk2_SHA256}`, wrap it in `io.LimitReader(rc, 1.7 MB)`, pipe those `1.7 MB` to the response, and break out of the loop!"*

---

### Chokehold 7: Complete Redis Outage Behavior
* **The Interviewer's Question:**
  *"If your Redis instance crashes at 2 AM, what happens when a user tries to log in, upload a file, or collaborate?"*
* **✅ Your Winning Answer:**
  > *"Everything continues working without throwing HTTP 500s because both Redis consumers are built to degrade gracefully:*
  > *1. **Rate Limiter (`redis_limiter.go`):** If the Redis Lua `EVAL` call returns a network error, the limiter logs the error and falls back to an in-memory per-node token bucket (`limiter.go`), so logins and uploads keep working.*
  > *2. **WebSocket Sync (`backplane.go`):** `NotifyUser` delivers WebSocket events to the local in-memory `Hub` **before** publishing to Redis. Users on the same API node still get instant WebSockets, and users on other nodes catch up automatically via HTTP delta polling (`GET /api/sync/delta?cursor=X`) against PostgreSQL."*

---

# PART B: 8 REAL-LIFE DEBUGGING & PROBLEM-SOLVING STORIES
*(Use these whenever an interviewer asks: "Tell me about a time you ran into a difficult bug / production issue / performance bottleneck and how you solved it.")*

---

## Story 1: Why 500 KB Files Worked in FastCDC, But 15 MB Files Failed Verification!
*(Best for: "Tell me about the trickiest bug you debugged" or "Tell me about a cross-language bug.")*

* **What I Was Doing:**
  *"I first wrote and unit-tested my FastCDC chunker in Go (`internal/chunker/fastcdc.go`), and then ported the exact same 256-entry `GEAR_TABLE` and sliding hash loop to TypeScript inside my browser Web Worker (`frontend/src/workers/fastcdc.worker.ts`)."*
* **What Broke on My Screen:**
  *"When I tested uploading small files in the browser — like a 300 KB image or a 700 KB PDF — everything worked on the very first try! So I thought the chunker was done. Then I dragged in a 15 MB video file, and suddenly my Go backend's parity check and chunk boundaries didn't match what the browser Web Worker was producing at all!"*
* **How I Investigated It:**
  *"First, I wondered why files under 1 MB worked while 15 MB files failed. Then I remembered: `MinSize` is `1 MB`! Any file under 1 MB is smaller than `MinSize`, so the chunker just returns the whole file as 1 chunk without ever entering the Gear hash loop!*
  *Next, I thought I must have had a typo in one of the 256 hex numbers in `GEAR_TABLE`. I wrote a tiny test that fed the exact same byte array into Go and TypeScript and printed the `hash` variable after each byte. Bytes 1 to 63 matched identically — and on **Byte 64**, the TypeScript hash diverged from Go!"*
* **The Root Cause & Exact Fix:**
  *"Byte 64 was the giveaway! In Go, `uint64` is a fixed 64-bit register: every time you do `hash << 1`, the 65th bit falls off the left side automatically. In JavaScript, standard `Number` only has 53 bits of integer precision, so I had used `BigInt`. What I didn't realize is that JavaScript `BigInt` has **arbitrary, infinite precision** — it never overflows at 64 bits! After 64 shifts, my JS hash was 65 bits wide, then 66 bits wide! Adding `& 0xFFFFFFFFFFFFFFFFn` on every single shift in `fastcdc.worker.ts` forced 64-bit wraparound in JS, and the browser and Go chunkers matched down to the exact byte."*

---

## Story 2: Worked in Local Docker, Spammed `403 InvalidClientTokenId` on AWS EC2!
*(Best for: "Tell me about a deployment / cloud issue where something worked locally but broke in production.")*

* **What I Was Doing:**
  *"Locally in Docker Compose, I didn't use AWS Lambda — my Go API server had an in-process worker goroutine (`ENABLE_INPROCESS_WORKER=true`) that processed background jobs directly. When I deployed the Go server to AWS EC2 and set up Amazon SQS with my serverless AWS Lambda worker (`cmd/worker-lambda`), I tested uploading a file on the live deployment."*
* **What Broke on My Screen:**
  *"The upload succeeded, but thumbnails weren't showing up consistently, and when I SSH'd into my EC2 instance and ran `journalctl -u blobcloud -f`, the logs were spamming `SQS ReceiveMessage failed: 403 InvalidClientTokenId` every few seconds!"*
* **How I Investigated It:**
  *"I inspected `cmd/api/main.go` and my `.env` file on EC2 and realized two things were colliding:*
  *1. Because Cloudflare R2 is S3-compatible, I had configured the standard `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` env vars on EC2 with my **Cloudflare R2** credentials. When the AWS SDK initialized the Amazon SQS client inside the same Go binary, it automatically read those default `AWS_*` env vars and tried to authenticate with Amazon SQS using my Cloudflare R2 keys!*
  *2. Even worse, `ENABLE_INPROCESS_WORKER` was still defaulting to `true` on EC2 — meaning once I fixed the credentials, both my EC2 API server AND my AWS Lambda function would be polling the same SQS queue and stealing messages from each other!"*
* **The Exact Fix:**
  *"I decoupled the credentials in `internal/config/config.go` by adding dedicated `SQS_AWS_ACCESS_KEY_ID` and `SQS_AWS_SECRET_ACCESS_KEY` fields specifically for the SQS client, and changed `ENABLE_INPROCESS_WORKER` to `false` on EC2 so the API server acts strictly as an SQS producer while AWS Lambda handles 100% of job consumption."*

---

## Story 3: How My Own Rate Limiter Broke Large Multi-Chunk Video Uploads (`HTTP 429`)!
*(Best for: "Tell me about a time a feature or security measure you built caused an unexpected bottleneck.")*

* **What I Was Doing:**
  *"To protect my backend from spam, I built a three-zone Redis token-bucket rate limiter (`internal/ratelimit/redis_limiter.go`). I set `AUTH` to `10 req/min`, `API` to `120 req/min`, and `UPLOAD` to `30 req/min`, figuring no human uploads more than 30 files a minute."*
* **What Broke on My Screen:**
  *"When I tested uploading normal 2 MB PDFs, everything worked fine. Then I tested dragging a folder of 15 files and a large multi-chunk video into the UI while my 8-stream AIMD parallel uploader was active. Within 10 seconds, uploads started failing and Chrome DevTools Network tab lit up with `429 Too Many Requests`!"*
* **How I Investigated It:**
  *"Looking at the Network waterfall in DevTools, I realized I had calculated the rate limit thinking '1 file = 1 request'. In reality, when a user drops a batch of files or a multi-chunk upload with 8 parallel workers, the browser fires `/upload/initiate`, session status checks, and `/upload/complete` calls in rapid bursts — easily exceeding 30 requests in 10 seconds for a single legitimate user!"*
* **The Exact Fix:**
  *"I fixed it on both ends: first, I made sure raw chunk `PUT` requests go directly to Cloudflare R2 Presigned URLs so they never hit my Go server's rate limiter; second, I raised the `UPLOAD` zone (`RL_UPLOAD_RPM`) from `30` to `300 req/min` (5 requests/sec sustained with a 300-token burst); and third, I updated `UploadContext.tsx` so if an HTTP 429 ever happens, the AIMD controller halves its concurrency window and retries with exponential backoff instead of failing the upload."*

---

## Story 4: Uploading the Same File in Two Browser Tabs at Once (`HTTP 500` Crash)!
*(Best for: "Tell me about a concurrency / database race condition you solved.")*

* **What I Was Doing:**
  *"While testing my real-time WebSocket delta sync, I had two browser tabs open side-by-side logged into the same account. To test conflict handling, I dragged the exact same file `report.pdf` into both tabs at the exact same time."*
* **What Broke on My Screen:**
  *"Tab 1 succeeded, and Tab 2 threw a raw `HTTP 500 Internal Server Error` toast! In my Go server terminal, PostgreSQL logged: `ERROR: duplicate key value violates unique constraint idx_files_unique_active_name`."*
* **How I Investigated It:**
  *"I traced `CompleteSession` in `upload_service.go`. Both requests had finished verifying their R2 chunks at the same millisecond and entered the Postgres transaction simultaneously. Inside the transaction, both ran `GetFileByNameAndParent('report.pdf')` **before** either transaction committed. Both got back `sql.ErrNoRows` ('file does not exist yet'), so both tried to `INSERT` version 1 of `report.pdf`! And I couldn't use `SELECT ... FOR UPDATE` to lock the row because **there was no row in the `files` table to lock yet**!"*
* **The Exact Fix:**
  *"I solved it using PostgreSQL's transaction-scoped advisory lock: `SELECT pg_advisory_xact_lock(hashtext(user_id || ':' || COALESCE(parent_id, 'root') || ':' || filename))` right at the start of the transaction (`AcquireFileNameLock` in `file.go`). Because advisory locks lock an integer hash in Postgres RAM rather than a table row, you can lock a filename **before** the row even exists! Now Tab 2 waits ~15ms at the advisory lock until Tab 1 commits, then reads the newly created file and cleanly increments `version_number` via `COALESCE(MAX(version_number), 0) + 1`."*

---

## Story 5: Why Every File Upload Fired the WebSocket Sync Notification Twice!
*(Best for: "Tell me about a bug in your real-time / distributed system.")*

* **What I Was Doing:**
  *"Right after wiring up my multi-node Redis Pub/Sub backplane (`internal/sync/backplane.go`) on top of my local WebSocket Hub, I opened Chrome DevTools → Network → WS (WebSockets) → Messages to watch the live `SYNC_DELTA` frames."*
* **What Broke on My Screen:**
  *"Every time I renamed or uploaded a single file, I saw **two identical `SYNC_DELTA` frames** arrive in the browser WebSocket inspector a millisecond apart, causing my frontend to trigger two delta-sync fetches!"*
* **How I Investigated It:**
  *"I looked at `RedisBackplane.NotifyUser(userID, event)`. To minimize latency for users connected to the same server, `NotifyUser` first called `b.hub.NotifyUser(userID, event)` (local delivery) and then called `b.redis.Publish(ctx, 'sync:events', payload)` so other API servers would receive it. The bug was obvious once I traced the subscriber goroutine: **the publishing server was also subscribed to `sync:events`!** So Redis echoed the message right back to the server that just published it, and `dispatch()` delivered it to the local Hub a second time!"*
* **The Exact Fix:**
  *"In `NewRedisBackplane`, I generated a random 128-bit UUID (`nodeID: uuid.NewString()`) when the server starts and added a `NodeID` field to the Redis JSON envelope. In `dispatch()`, I added a 2-line check: `if env.NodeID == b.nodeID { return }`. That immediately suppressed self-echoes while letting all other API nodes broadcast the event."*

---

## Story 6: The "Empty Trash While Someone Else Is Uploading" CAS Corruption Bug!
*(Best for: "Tell me about a subtle edge case you caught that could have caused data loss.")*

* **What I Was Doing:**
  *"I was auditing how permanent deletion (`HardDelete` / 'Empty Trash') cleans up orphaned blocks from Cloudflare R2 in a Content-Addressable Storage system."*
* **What I Realized Could Break:**
  *"Suppose User A has a file in their Trash containing Block `Hash_X`, and no other committed file in the database uses `Hash_X`. At 12:00:00, User B starts uploading a file that happens to contain that same chunk `Hash_X`. User B's `/upload/initiate` call checks the `blocks` table, sees `Hash_X` is already in R2, and tells User B's browser: 'Skip uploading `Hash_X`, we already have it!'*
  *Five seconds later — while User B is still uploading the rest of their 50 chunks — User A clicks 'Empty Trash'. Originally, my orphan query (`findOrphanedHashes` in `file.go`) only checked if `Hash_X` was referenced in `file_blocks` or `file_versions`. Because User B hasn't called `/upload/complete` yet, User B has **zero rows in `file_blocks`**! So User A's 'Empty Trash' saw zero references and deleted `blocks/Hash_X` from Cloudflare R2! When User B finished uploading 10 seconds later, their file was permanently missing a chunk!"*
* **The Exact Fix:**
  *"I updated `findOrphanedHashes` in `internal/repository/postgres/file.go` to check **three** tables in a single atomic query: `file_blocks`, `file_versions`, AND `upload_session_blocks` (which records every chunk — including deduplicated hits — belonging to an active `INITIATED` upload session). Now, as long as an active upload session references a block hash, Garbage Collection will never delete it from R2."*

---

## Story 7: Why Password-Protected Public Videos Failed to Play in HTML5 `<video>` Tags!
*(Best for: "Tell me about a full-stack / HTTP protocol issue you solved.")*

* **What I Was Doing:**
  *"I built password-protected public share links (`PublicShare.tsx` and `public_handlers.go`). When a visitor enters the password, the backend verifies it with Bcrypt and returns a token that the frontend sends in an `X-Share-Token` header."*
* **What Broke on My Screen:**
  *"Downloading normal files via `fetch()` worked great because `fetch()` lets you attach `headers: {'X-Share-Token': token}`. But when I shared an MP4 video with a password and clicked Play in the browser's `<video>` player, the video refused to play and DevTools showed `401 Unauthorized` on the `HTTP 206 Range` requests! Furthermore, on some MP4 files, seeking at the start failed when the browser sent a suffix range request `Range: bytes=-500` to read the MP4 `moov` atom at the end of the file."*
* **How I Investigated & Fixed It:**
  *"Native HTML5 `<video src='...'>` elements make their own internal HTTP 206 Range requests directly from the browser media engine — you **cannot** pass custom `X-Share-Token` headers into a `<video src>` tag! I fixed this in `public_handlers.go` and `file_handlers.go` in two ways:*
  *1. **Dual-Auth Support:** Updated the public share middleware to accept the share token via `X-Share-Token` header, `?share_token=` URL query parameter (for `<video>` / `<audio>` tags), OR an `HttpOnly` cookie with dynamic TLS detection.*
  *2. **RFC 7233 Suffix Range Support:** Updated `parseRangeHeader` so when a media player sends `Range: bytes=-500` (asking for the last 500 bytes of the file), it calculates `[totalSize - 500, totalSize - 1]` and streams the tail chunk properly."*

---

## Story 8: Fixing Socket & Timer Leaks in Multi-Block Loops (`defer` in Loop & `time.After`)
*(Best for: "Tell me about a memory leak or resource management issue you fixed in Go.")*

* **What I Was Doing:**
  *"I was reviewing our streaming folder ZIP generator (`zip_service.go`), multi-block HTTP 206 streamer (`file_handlers.go`), and multi-chunk Gemini embedding loop (`processor.go`)."*
* **What I Caught:**
  *"In Go, there are two classic resource-leak traps inside `for` loops:*
  *1. **`defer rc.Close()` inside a `for` loop:** When streaming a 100-block file from R2, if you write `rc, _ := storage.GetObject(...); defer rc.Close()` directly inside the `for` loop body, Go's `defer` does **not** run at the end of each loop iteration — it waits until the entire outer HTTP handler function returns! That means all 100 HTTP response body streams stay open in memory at the same time!*
  *2. **`time.After(200ms)` inside a `select` loop:** In `processor.go`, when pacing Gemini embedding calls across 50 text chunks, calling `case <-time.After(200 * time.Millisecond):` inside a loop allocates a new underlying `time.Timer` every iteration that isn't garbage-collected until it fires."*
* **The Exact Fix:**
  *"For the R2 streams, I wrapped each iteration's stream copy inside a scoped helper closure (`func() error { rc, err := ...; defer rc.Close(); ... }()`) so each R2 socket handle closes the exact millisecond that chunk finishes streaming, while also checking `ctx.Done()` if the user cancels the download. And in `processor.go`, I replaced `time.After` with a single reusable `timer := time.NewTimer(pace)` and `defer timer.Stop()`."*
