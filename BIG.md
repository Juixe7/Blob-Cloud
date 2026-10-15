# SECTION 0: OPENING PITCH & RESUME BULLET DEFENSE
*Part 1 of 7 of the Blob-Cloud Interview Master Series*

---

## 0.1 — The 90-Second Opening Pitch ("Tell Me About Blob-Cloud")

### 💡 Coaching Note: How to Deliver This Naturally
Don't rush through this like you're reciting a memorized paragraph. Think of it as **5 short beats**:
1. **What it is + Why Go** (Concurrency & constant-memory streaming)
2. **How uploads work** (Client-side FastCDC in Web Workers + direct-to-R2 Presigned URLs)
3. **How we keep it secure & consistent** (Zero-trust 32 KB streaming SHA-256 verification + Postgres advisory locks)
4. **How background work happens** (SQS + serverless AWS Lambda for ClamAV, thumbnails, and 768-dim `pgvector` search)
5. **How real-time sync works** (Postgres monotonic `BIGSERIAL` journal + Redis Pub/Sub WebSocket hub)

### 🎙️ Verbatim Script (Speak at a calm, conversational pace)

> *"Blob-Cloud is a distributed cloud storage platform I built from scratch — modeled on the core storage, sync, and security architecture behind Dropbox and Google Drive.*
>
> *On the backend I used Go, mainly for two reasons: goroutines let me stream multi-gigabyte file chunks using a tiny 32-kilobyte buffer per request without high memory overhead, and Go's standard HTTP server can hold thousands of concurrent WebSocket connections without blocking an event loop.*
>
> *For the upload pipeline, I wanted to make sure raw file bytes never bottleneck the API server. So when a user drops a file in the browser, a Web Worker slices the file on the client side using FastCDC — which is a rolling Gear hash algorithm that cuts variable 1-to-8 megabyte blocks based on the actual byte content. The browser hashes each chunk with SHA-256 and asks the Go API which chunks already exist in our Content-Addressable Store. Any chunk we already have is skipped completely, and for the missing ones, the browser uploads them directly to Cloudflare R2 staging using S3 Presigned URLs. Because FastCDC cuts boundaries based on content rather than fixed 4-megabyte offsets, editing the start of a large file still reuses over 98% of the existing chunks instead of dropping to 0% like fixed-size chunking.*
>
> *Before we actually commit those uploaded chunks, the server enforces a zero-trust quarantine. Since a client could lie about a chunk's SHA-256 hash and poison the shared block store, the Go server streams each newly staged chunk from R2 through a 32-kilobyte buffer in O(1) memory, verifies its real SHA-256 hash, promotes it to permanent CAS storage, and commits the file metadata inside an ACID transaction protected by PostgreSQL advisory locks.*
>
> *Anything CPU-heavy happens asynchronously: once the upload commits, the Go server pushes an event to Amazon SQS, and a serverless AWS Lambda worker picks it up to run a ClamAV malware scan, generate thumbnails, and compute 768-dimensional pgvector embeddings for hybrid semantic search.*
>
> *Finally, to keep multiple browser tabs and shared collaborators in sync, I implemented Dropbox's 'tickle-and-fetch' delta sync pattern using an append-only PostgreSQL journal with monotonic cursors and a multi-node Redis Pub/Sub WebSocket hub.*
>
> *I can walk you through the whole system from the database schema up, or we can jump straight into any part that sounds most interesting — like the FastCDC chunker, the zero-trust upload flow, the Lambda worker, or the delta sync engine."*

---

## 0.2 — Line-by-Line Resume Bullet Defense

Below are the exact **5 bullets from your resume**, broken down into:
1. **Plain-English Intuition** (so you deeply understand *why* it works)
2. **What to Say Out Loud** when the interviewer points at that bullet
3. **Every Follow-Up Question** they will ask to test you

---

### BULLET 1: FastCDC Content-Defined Chunking & CAS Deduplication

**Your Resume Line:**
> • *Built client-side **FastCDC** chunking with **1–8 MB** variable blocks, **Web Workers**, and rolling Gear hashing; achieved **98%+** chunk reuse in Content-Addressable Storage (**CAS**) across file edits vs. **0%** under fixed-size byte shifts.*

#### 🧠 1. Plain-English Intuition (Understand This First)
* **What is CAS (Content-Addressable Storage)?**
  Instead of storing a file in R2 as `users/ashish/video.mp4`, we chop the file into chunks, compute the `SHA-256` hash of each chunk's bytes, and store the chunk in R2 under its hash name: `blocks/9f86d08...`. In PostgreSQL, the `blocks` table stores unique `sha256` hashes, and `file_blocks` maps `(file_id, sequence_number) -> block_id`. If two files (or two versions of the same file) share the same chunk, they just point to the same row in `blocks`!
* **Why does Fixed-Size chunking fail on edits (`0%` reuse)?**
  Suppose you have a `100 MB` file and you slice it every `4 MB` (`0–4 MB`, `4–8 MB`, `8–12 MB`... `25` chunks total). Now you insert `64 KB` of new data at byte `0` (the very beginning of the file). Because everything shifted right by `64 KB`, the old bytes `0..64 KB` of Chunk #2 are now pushed out of Chunk #1 into Chunk #2! Every single 4 MB boundary from start to end now contains different bytes, so all 25 SHA-256 hashes change. You get **0 out of 25 chunks reused (`0%`)**.
* **How does FastCDC fix this (`98%+` reuse)?**
  Instead of cutting blindly at `4,194,304` bytes, FastCDC slides a 1-byte pointer across the data and updates a 64-bit number (`hash`) using a lookup table (`GEAR_TABLE[byte]`). Whenever the bottom bits of `hash` are all zeros (`hash & mask == 0`), it declares: *"Cut the chunk right here!"*
  Because that `hash` only depends on the last ~64 bytes of content right before the cut point, inserting `64 KB` at the start of the file only changes **Chunk #1**. As soon as the pointer slides into the untouched bytes of Chunk #1, the rolling hash hits the exact same zero-bits pattern at the end of Chunk #1 and cuts at the **exact same place**! Chunks #2 through #26 are 100% identical to before.

#### 🎙️ 2. How to Explain Bullet 1 Out Loud in 30 Seconds
> *"If you slice a 100 MB file into fixed 4 MB blocks and insert even 1 kilobyte at the top of the file, every single 4 MB window shifts to the right — which changes the SHA-256 hash of every chunk and forces you to re-upload the entire 100 MB file. To fix that, I wrote a FastCDC chunker inside a browser Web Worker. It slides a 64-bit rolling Gear hash across the file and cuts variable-size blocks between 1 MB and 8 MB (averaging 4 MB) whenever the hash matches a bitmask. Because cut boundaries depend on local byte content instead of fixed byte offsets, editing the start of a file only mutates that first chunk — the rolling hash immediately resynchronizes on the first boundary and reuses 98%+ of the existing chunks in our Content-Addressable Store."*

#### 🛡️ 3. Follow-Up Questions & Exact Answers

| Interviewer Question | Human, Convincing Answer |
| :--- | :--- |
| **"How does the rolling Gear hash formula actually work in code?"** | *"It's a very fast 3-line loop. We have a hardcoded array `GEAR_TABLE` of 256 random 64-bit unsigned integers — one for each possible byte `0` to `255`. For every chunk, we first skip the first `1 MB` (`MinSize`) without hashing at all — that prevents tiny chunks and skips 25% of the CPU work. Then, as we loop byte-by-byte from `1 MB` up to `8 MB` (`MaxSize`), we update our 64-bit hash: `hash = (hash << 1) + GEAR_TABLE[byte]`. Left-shifting by 1 bit means the oldest byte from 64 steps ago naturally falls off the left side of the 64-bit integer, so the hash always represents a sliding 64-byte window."* |
| **"Why do you have two different bitmasks (`MASK_SMALL` and `MASK_LARGE`)?"** | *"That's the 'Normalized Chunking' part of FastCDC. If you use just one 22-bit mask (`0x3fffff`) for a 4 MB average, chunk sizes are skewed — you get lots of small 1.1 MB chunks and lots of chunks hitting the 8 MB hard wall. So between `1 MB` and `4 MB`, I apply a stricter **23-bit mask (`0x7fffff`)** — requiring 23 zero bits makes early cuts twice as rare. Once the chunk crosses `4 MB`, I switch to a looser **21-bit mask (`0x1fffff`)** — requiring only 21 zero bits makes it 4 times easier to find a cut boundary before hitting the 8 MB ceiling. That keeps chunk sizes tightly clustered around 4 MB."* |
| **"Why Gear hashing instead of Rabin-Karp / Rabin fingerprinting?"** | *"Rabin fingerprinting uses polynomial division over a Galois Field (`GF(2)`), which requires multiple XORs and table lookups per byte to subtract the outgoing byte. Gear hashing relies on simple 64-bit integer overflow (`hash << 1`) to drop the outgoing byte automatically, so it only needs one bit-shift, one array lookup, and one addition per byte. It gives the exact same deduplication ratio at 3× to 5× higher CPU throughput."* |
| **"Why did you run FastCDC in a Browser Web Worker instead of on the Go server?"** | *"Two reasons. First, if the Go server did the chunking, the user would have to upload the entire 100 MB file over their home Wi-Fi to the server first just to find out 98% of it was already stored! By chunking and SHA-256 hashing in the browser, the client only sends a tiny JSON list of hashes to `/api/upload/initiate` and skips uploading existing chunks completely. Second, hashing 100+ MB in JavaScript takes a couple of seconds of CPU work — running it inside a background Web Worker (`fastcdc.worker.ts`) keeps the main React thread at a smooth 60fps."* |
| **"How did you measure and verify the `98%+` vs `0%` number?"** | *"I tested it both in Chrome DevTools and with a 90-line Go test (`cmd/bench-chunker/main.go`). On a 100 MB file (26 chunks) or 256 MB file (58 chunks), when I prepend 48–64 KB at the beginning of the file, fixed 4 MB slicing reuses `0` chunks (`0%`). FastCDC only changes the very first chunk and reuses `25/26` chunks on 100 MB (`97.9%` of bytes saved) and `57/58` chunks on 256 MB (`98.3%` of chunks / `99.3%` of bytes saved). In production, I also track live dedup ratio via Prometheus counters (`blobcloud_block_dedup_hits_total` and `misses_total`)."* |

---

### BULLET 2: Zero-Trust Quarantine & O(1) Memory Streaming Verification

**Your Resume Line:**
> • *Enforced a **zero-trust** quarantine over **S3 presigned URLs**, streaming **SHA-256** verification in **O(1)** memory with a **32 KB** buffer to prevent **CAS poisoning** and tampering before **ACID** storage promotion.*

#### 🧠 1. Plain-English Intuition (Understand This First)
* **What is the security hole with normal S3 Presigned URLs + CAS?**
  In a normal CAS system, the browser says: *"Hey server, I have a chunk whose SHA-256 hash is `aaaa1111...`. Give me a Presigned PUT URL to upload it."*
  If the server blindly gives the browser a Presigned PUT URL to `blocks/aaaa1111...`, the browser can upload **any garbage or malware bytes it wants** to that URL! Now `blocks/aaaa1111...` in Cloudflare R2 contains corrupted bytes under a valid hash name. If another user uploads a real file that happens to contain chunk `aaaa1111...`, the server says *"Oh, we already have `aaaa1111...`!"* and links their file to the attacker's corrupted block. That is **CAS Poisoning**.
  Even without an attacker, if a normal user edits and saves their local file on disk *after* the Web Worker hashed it in Phase 1, but *before* the browser uploads Chunk #20 in Phase 2, the uploaded bytes won't match the hash!
* **How does our Quarantine fix it?**
  1. Presigned PUT URLs **never** point to `blocks/{sha256}`. They only point to a temporary quarantine folder: `staging/{session_id}/{sequence_number}`.
  2. When the browser finishes uploading and calls `POST /api/upload/complete`, the Go server opens a read stream from R2 (`staging/{session_id}/{seq}`), pipes the bytes through `sha256.New()` using `io.Copy` (which uses a fixed `32 KB` buffer in RAM), and checks if the real SHA-256 matches what the client claimed.
  3. Only if the hash and byte size match does the Go server issue an S3 `CopyObject` from `staging/{session_id}/{seq}` to `blocks/{sha256}`, delete the staging object, and open the PostgreSQL transaction to save the file metadata.

#### 🎙️ 2. How to Explain Bullet 2 Out Loud in 30 Seconds
> *"When you combine S3 Presigned URLs with Content-Addressable Storage, you can't trust the client's SHA-256 hash — if a client claims a chunk has hash X but uploads corrupted bytes directly to `blocks/X`, they poison that block globally. So I built a two-phase quarantine: clients only get Presigned PUT URLs to `staging/{session_id}/{seq}`. On `/upload/complete`, my Go server streams each staged chunk from R2 through Go's `crypto/sha256` using `io.Copy`, which reuses a single 32 KB buffer in O(1) memory. Once the server confirms the size and SHA-256 match, it promotes the object to `blocks/{sha256}` via `CopyObject` and commits the file metadata inside a Postgres ACID transaction."*

#### 🛡️ 3. Follow-Up Questions & Exact Answers

| Interviewer Question | Human, Convincing Answer |
| :--- | :--- |
| **"Where does the exact `32 KB` buffer number come from?"** | *"That's the internal buffer size of Go's standard library `io.Copy`. In `upload_service.go`, if I called `io.ReadAll(rc)` to hash an 8 MB chunk in memory, 20 concurrent chunk verifications would spike server RAM by 160 MB and crash a small EC2 instance. Instead, I pass the R2 `GetObject` stream directly into `io.Copy(sha256Hasher, rc)`. Under the hood, `io.Copy` allocates a single `32 * 1024` byte (`32 KB`) slice and loops over the stream until EOF, so memory per worker stays flat at 32 KB regardless of chunk size."* |
| **"Wait — if you stream chunks back from R2 to EC2 on `/complete` to hash them, doesn't that slow down upload completion?"** | *"That's the deliberate trade-off for zero-trust CAS integrity, and I did two things to keep it fast: first, we only verify brand-new **cache-miss** chunks — any deduplicated chunk already in `blocks/` is skipped completely. Second, I run a bounded 4-goroutine worker pool that streams and hashes the staged chunks concurrently over cloud-to-cloud bandwidth **before** opening the Postgres transaction. And if we ever needed to scale this to 50 GB+ files where re-streaming would take too long, we could enforce S3's native `x-amz-checksum-sha256` signed header inside the Presigned PUT URL so R2/S3 verifies the hash at the storage edge during the upload itself."* |
| **"Why do you verify and `CopyObject` in R2 *before* opening the PostgreSQL transaction?"** | *"Because database connections are scarce — our Postgres pool is capped at `MaxOpenConns = 25` per node. If you open `db.BeginTx()` first and then make slow S3 network calls inside the transaction, you hold DB locks and exhaust the connection pool in seconds. Doing all R2 verification and `CopyObject` promotion in Phase 1 keeps our Phase 2 SQL transaction purely metadata-only, so it commits in 20 to 50 milliseconds."* |
| **"What if `CopyObject` succeeds in R2 in Phase 1, and then the Postgres transaction fails or the server crashes in Phase 2?"** | *"If the server crashes right in that gap, the upload session stays in `INITIATED` status so the client can safely retry `/complete`. And if the user abandons the upload completely, the block sitting in R2 has zero references in `file_blocks` or `file_versions`. Our background Garbage Collector (`cmd/gc` and `CleanupExpiredSessions`) sweeps expired sessions and deletes any R2 block that has zero references in Postgres."* |

---

### BULLET 3: Resumable Multipart Uploads, TCP-Style AIMD, Advisory Locks & HTTP 206 Range Streaming

**Your Resume Line:**
> • *Engineered resumable **multipart uploads** with TCP-style **AIMD** (**1–8 parallel streams**), PostgreSQL **advisory locks**, and **HTTP 206** Range requests, resuming interrupted transfers with **0-byte** redundant re-uploads.*

#### 🧠 1. Plain-English Intuition (Understand This First)
This bullet covers **4 concrete engineering pieces** across the upload completion and download path:
1. **TCP-Style AIMD (`1–8 parallel streams` in `UploadContext.tsx`):**
   When uploading 50 chunks to R2, how many parallel `fetch(presignedUrl, {method: 'PUT'})` calls should the browser run at the same time?
   - If you hardcode `1`, a fast fiber connection is wasted waiting on HTTP latency.
   - If you hardcode `8`, a slow Wi-Fi connection chokes, times out, or hits `429` rate limits.
   So in `UploadContext.tsx`, we start with `3` parallel workers (`INITIAL_CONCURRENCY = 3`). Every time a chunk finishes uploading, we check how long it took (`performance.now()`). If it took **under `1500 ms`**, the network is fast, so we **add `+1` worker** (up to a max of `8` — *Additive Increase*). If a chunk fails, times out, or hits a `429`, the network is congested, so we **cut the worker count in half** (`Math.floor(cwnd / 2)`, down to a minimum of `1` — *Multiplicative Decrease*) and retry with exponential backoff.
2. **Resumable Transfers (`0-byte` redundant re-uploads):**
   Every chunk in an upload session is tracked in Postgres (`upload_session_blocks`) and staged at `staging/{session_id}/{seq}`. If Wi-Fi drops after 25 out of 50 chunks are uploaded, reconnecting checks which chunks are already confirmed/staged and only uploads the remaining 25 chunks.
3. **PostgreSQL Advisory Locks (`pg_advisory_xact_lock`):**
   What if a user uploads `report.pdf` in two tabs at the exact same time, or uploads a new version while another request is finishing? Both requests check *"Does `report.pdf` exist in this folder?"* at the same millisecond. Without a lock, both try to insert `version 1` and crash with a unique constraint error (`HTTP 500`). Calling `SELECT pg_advisory_xact_lock(hashtext(user_id || parent_id || filename))` inside the transaction forces the second request to wait ~15ms for the first to commit, after which it sees `version 1` and cleanly creates `version 2` (`MAX(version_number) + 1`).
4. **HTTP 206 Range Streaming (`DownloadFile`):**
   When you play a 1 GB MP4 video in the browser and click the timeline to jump to minute 40, the browser sends `Range: bytes=500000000-`. Because our 1 GB video isn't stored as a single 1 GB file in R2 (it's split into ~250 variable-sized `1–8 MB` FastCDC chunks!), we can't just redirect the browser to a single S3 URL. Instead, our Go API loops through the file's `file_blocks` metadata, adds up the chunk sizes until it finds the exact chunk containing byte `500,000,000`, jumps to that byte offset inside the chunk, and streams the bytes out with `HTTP 206 Partial Content`.

#### 🎙️ 2. How to Explain Bullet 3 Out Loud in 30 Seconds
> *"For large files, I built an adaptive worker pool in React modeled on TCP's AIMD algorithm: it starts with 3 parallel chunk uploads, scales up by +1 stream up to 8 when chunks finish in under 1.5 seconds, and halves concurrency down to 1 if a chunk hits a timeout or 429 error. Because each chunk's progress is tracked in `upload_session_blocks` and staged in R2, an interrupted upload resumes from the exact missing chunks with 0 bytes re-uploaded. On finalize, I use a transaction-scoped PostgreSQL advisory lock (`pg_advisory_xact_lock`) keyed on `(user_id, parent_id, filename)` so concurrent uploads of the same file cleanly increment `version_number` instead of crashing. And on the read path, the Go server implements RFC 7233 HTTP 206 Range requests by stitching arbitrary byte ranges across variable-size FastCDC chunks in a 32 KB streaming loop."*

#### 🛡️ 3. Follow-Up Questions & Exact Answers

| Interviewer Question | Human, Convincing Answer |
| :--- | :--- |
| **"Doesn't OS TCP already do AIMD congestion control? Why do it in JavaScript?"** | *"OS TCP controls packet congestion on a single socket, whereas our browser uploader opens multiple concurrent HTTP `PUT` requests (`1 to 8` parallel chunk streams). Opening 8 parallel 4 MB streams on a weak connection causes bufferbloat, request timeouts, and HTTP 429/503 throttling. Doing AIMD at the worker-pool level in `UploadContext.tsx` dynamically tunes **how many parallel HTTP requests** are in flight based on real chunk completion time (`1500ms` threshold)."* |
| **"Why use `pg_advisory_xact_lock` instead of standard SQL `INSERT ... ON CONFLICT`?"** | *"Two reasons. First, our active filename uniqueness uses a partial unique index (`WHERE deleted_at IS NULL`) and `parent_id` is nullable at the root folder. Second, when a filename collision happens on `/upload/complete`, we don't just update a single row — we execute a multi-table branching workflow based on `conflict_mode`: `version` snapshots the old blocks into `file_versions` and queries `COALESCE(MAX(version_number), 0) + 1`; `copy` finds the next free name like `report (1).pdf`; `fail` returns HTTP 409. `pg_advisory_xact_lock` locks that virtual `(user, folder, filename)` key even before any row exists and releases automatically when the transaction ends."* |
| **"How do you calculate which chunk to fetch on an HTTP 206 Range request when chunks are variable-sized?"** | *"Because FastCDC chunks vary between 1 MB and 8 MB, you can't just divide `rangeStart / 4MB`. In `DownloadFile`, I query the ordered list of chunks (`SELECT sequence_number, sha256, size FROM file_blocks JOIN blocks ... ORDER BY sequence_number ASC`). In Go, I walk that slice with a running `offset` counter: I skip chunks where `offset + chunk.Size <= rangeStart`, compute the intra-block start offset `startInBlock = rangeStart - offset` on the first overlapping chunk, open the R2 stream, discard `startInBlock` bytes, wrap it in `io.LimitReader(rc, bytesNeeded)`, and stream across subsequent chunks until `rangeEnd` is satisfied."* |
| **"What happens if a user revokes a shared link while someone is streaming a 2 GB video?"** | *"Inside the multi-block streaming loop in `DownloadFile`, every `16 blocks` (~64 MB) the server calls `verifyStreamAccess(ctx)` to re-check Postgres permissions and checks `ctx.Done()`. If the file was trashed or the share link was revoked mid-stream, the server immediately closes the R2 stream and terminates the HTTP response."* |

---

### BULLET 4: Decoupled Background Compute via Amazon SQS, AWS Lambda, ClamAV & 768-Dim `pgvector`

**Your Resume Line:**
> • *Decoupled background compute via **Amazon SQS** and serverless **AWS Lambda**, running **ClamAV** scans, thumbnails, and **768-dim pgvector** embeddings for hybrid semantic search.*

#### 🧠 1. Plain-English Intuition (Understand This First)
* **Why decouple with SQS + AWS Lambda?**
  When a user finishes uploading a PDF or video, three heavy tasks need to happen:
  1. Scan the file bytes with **ClamAV** for viruses/malware.
  2. Render a preview **thumbnail** (image resize / PDF cover / video frame).
  3. Extract the text, split it into paragraphs, call Google's **Gemini Embedding API** to get **768-dimensional float vectors**, and save them in PostgreSQL (`vector(768)`) for semantic search.
  If the Go API server did all that synchronously inside `/api/upload/complete`, the user's upload spinner would hang for 10 seconds, and 5 simultaneous uploads would max out the EC2 server's CPU and RAM!
  Instead, `/api/upload/complete` commits the DB transaction, pushes a tiny JSON message (`{file_id, user_id}`) to **Amazon SQS**, and returns `200 OK` in milliseconds. Our serverless **AWS Lambda** worker (`cmd/worker-lambda`) wakes up, pulls the message from SQS, streams the blocks from R2, and runs the heavy pipeline in an isolated container.

#### 🎙️ 2. How to Explain Bullet 4 Out Loud in 30 Seconds
> *"To keep upload completion under 50 milliseconds and protect the Go API server from CPU spikes, I decoupled all heavy post-upload processing through Amazon SQS and a serverless AWS Lambda worker (`cmd/worker-lambda`). When an upload commits, the API publishes a job event to SQS. Lambda consumes the event, streams the file's CAS blocks from R2 over TCP `INSTREAM` to ClamAV for malware scanning, generates thumbnails, and chunks extracted document text into 768-dimensional Gemini embeddings stored in PostgreSQL `pgvector` — allowing users to search files by combining keyword matching with cosine vector similarity."*

#### 🛡️ 3. Follow-Up Questions & Exact Answers

| Interviewer Question | Human, Convincing Answer |
| :--- | :--- |
| **"Why not just run `go processFile()` in a background goroutine inside the EC2 Go server instead of using SQS + Lambda?"** | *"Two reasons: resource isolation and durability. First, running ClamAV, image/video decoding, and PDF text extraction inside the API server process starves CPU and memory from live HTTP and WebSocket connections on a small EC2 instance. Second, if the EC2 server restarts during a deployment while an in-memory goroutine is halfway through a job, that job is lost forever. With SQS + Lambda, compute scales independently, and if a Lambda worker crashes or times out, SQS's visibility timeout expires and automatically retries the message."* |
| **"How do you embed a large 50-page document into a 768-dim vector without losing detail?"** | *"If you compress an entire 50-page PDF into a single 768-float vector, the specific details get averaged out into noise. So in `internal/queue/processor.go`, after extracting text, we split the document into overlapping text segments (`chunkText` with `chunk_index = 0, 1, 2...`), pace calls to Gemini's embedding model at `200ms` per chunk (`GEMINI_EMBEDDING_PACE_MS`) using reusable `time.NewTimer` handles so we don't hit 429 rate limits, and insert multiple rows per file into `file_embeddings (file_id, chunk_index, content_chunk, embedding vector(768))`."* |
| **"How does the hybrid search query actually combine keyword search and `pgvector`?"** | *"In `SearchFiles` (`internal/repository/postgres/file.go`), we embed the user's search query into a 768-dim vector and query PostgreSQL combining Full-Text / `ILIKE` matching on filenames and AI summaries with `pgvector` cosine distance (`1 - (embedding <=> $queryVector)`). We group by `file_id` taking `MAX(similarity)` across all chunks of a file and filter by a minimum cosine similarity threshold, so searching for a concept like 'tax invoice' finds `scan_004.pdf` even if the filename doesn't contain the word 'tax'."* |
| **"How does ClamAV scan a file that is split into multiple R2 blocks without filling up Lambda's local disk?"** | *"In `internal/queue/clamav.go`, we open a TCP connection to the ClamAV daemon and use ClamAV's streaming `zINSTREAM` protocol. As we iterate through the file's ordered CAS blocks from R2, we pipe each 32 KB chunk directly over the TCP socket prefixed with its 4-byte big-endian length, terminated by a zero-length packet. The file never touches disk. If ClamAV replies `FOUND`, the worker immediately deletes the file record and purges its unreferenced blocks."* |

---

### BULLET 5: Real-Time Delta Sync Engine (Monotonic Cursors, Actor Hub, Redis Pub/Sub)

**Your Resume Line:**
> • *Implemented real-time **delta sync** with a PostgreSQL append-only journal, **monotonic cursors**, and a **Redis Pub/Sub WebSocket hub** for **O(1)** cursor catch-up and live event fan-out.*

#### 🧠 1. Plain-English Intuition (Understand This First)
* **Why is pushing file updates directly over WebSockets bad?**
  Suppose User A renames 3 files while User B's laptop Wi-Fi flickers for 2 seconds. If the server just pushed `{event: "RENAMED", file: "a.pdf"}` over the WebSocket, User B missed those packets while offline. When User B's WebSocket reconnects, User B's screen is permanently out of sync unless they refresh the whole page!
* **How does "Tickle-and-Fetch" Delta Sync solve this?**
  1. Every time any file is created, renamed, moved, or deleted, we insert a row into an append-only PostgreSQL table called `journal_entries` inside the same ACID transaction. The primary key of `journal_entries` is `cursor BIGSERIAL` (`1, 2, 3, ... 104, 105`).
  2. The browser remembers the last cursor number it has processed (say `syncCursorRef.current = 102`).
  3. When mutation `105` happens, the server sends a tiny WebSocket "tickle" message: `{type: "SYNC_DELTA", cursor: 105}`.
  4. The browser sees `105 > 102` and makes a normal HTTP request: `GET /api/sync/delta?cursor=102`.
  5. Postgres runs `SELECT * FROM journal_entries WHERE user_id = $1 AND cursor > 102 ORDER BY cursor ASC` using a B-Tree index on `(user_id, cursor)` ($O(1)$ index seek) and returns events `103, 104, 105` in exact order! Even if a tab was offline for an hour, the moment it reconnects it asks for `cursor > 102` and catches up on everything it missed!

#### 🎙️ 2. How to Explain Bullet 5 Out Loud in 30 Seconds
> *"Pushing full file state directly over WebSockets is fragile because a brief network drop silently loses events and leaves the UI out of sync. Instead, I implemented Dropbox's 'tickle-and-fetch' delta sync architecture. Every file mutation writes an entry to an append-only PostgreSQL table (`journal_entries`) whose primary key is a monotonic `BIGSERIAL` cursor. The Go server then broadcasts a lightweight `{type: 'SYNC_DELTA', cursor}` tickle through an actor-model WebSocket Hub backed by Redis Pub/Sub across API nodes. When a client receives a tickle — or reconnects after being offline — it compares the cursor to its local cursor and calls `GET /api/sync/delta?cursor=X`, doing an O(1) B-Tree index seek in Postgres to fetch every missed mutation in strict order."*

#### 🛡️ 3. Follow-Up Questions & Exact Answers

| Interviewer Question | Human, Convincing Answer |
| :--- | :--- |
| **"Why use `BIGSERIAL` for the cursor instead of a `UUID` or `TIMESTAMPTZ`?"** | *"A `UUIDv4` is completely random, so you can't query `WHERE cursor > X`. And timestamps (`TIMESTAMPTZ`) have two problems: clock skew across multiple API servers, and two concurrent transactions committing in the exact same millisecond getting identical timestamps, which breaks strict ordering. PostgreSQL's `BIGSERIAL` is a sequence generator that guarantees strictly increasing, collision-free 64-bit integers. With a composite B-Tree index on `(user_id, cursor)`, `WHERE user_id = $1 AND cursor > $2 ORDER BY cursor ASC` is a direct $O(1)$ index seek."* |
| **"What does 'Actor Model' mean in your Go WebSocket Hub (`internal/sync/hub.go`)?"** | *"In Go, regular `map` types are not thread-safe — if two HTTP handler goroutines read and write the `clients` map at the same time, the Go runtime crashes with a fatal `concurrent map read and map write` panic. Instead of wrapping the map in a `sync.RWMutex` that every handler has to lock, the Hub runs a single dedicated background goroutine with a `for { select { ... } }` loop over Go channels (`register`, `unregister`, `broadcast`). Because only that single goroutine ever touches the `clients` map, data races are impossible without needing locks."* |
| **"How does Redis Pub/Sub work across multiple Go servers, and how did you avoid duplicate messages on the publishing server?"** | *"When User A mutates a file on API Node 1, Node 1 immediately pushes the WebSocket tickle to its local in-memory `Hub`, and also calls `redis.Publish` so API Node 2 can notify any of User A's (or collaborators') tabs connected to Node 2. However, because Node 1 is also subscribed to that same Redis channel, Redis echoes Node 1's own message right back to Node 1! To stop Node 1 from sending the WebSocket event twice, each server generates a random `node_id` UUID on startup, tags every Redis JSON envelope with `node_id`, and drops any incoming Redis message where `env.NodeID == b.nodeID`."* |
| **"What happens if a slow or dead browser tab stops reading from its WebSocket? Won't that block the Hub goroutine?"** | *"Inside the Hub's broadcast loop, each client has a buffered `send` channel (`make(chan []byte, 256)`). When the Hub pushes a message to `client.send`, it uses a non-blocking `select` with a `default:` branch. If a stalled tab's 256-message buffer is full, the `default:` branch immediately closes and unregisters that dead socket so it can never block the Hub goroutine for other connected users."* |

---

## 0.3 — Bonus Defenses (Not on Your Resume, But Built in Your Code!)
*Bring these two up naturally during follow-ups — interviewers love discovering that you built more than what fit on your 1-page resume.*

### Bonus 1: Materialized Path Directory Tree (`text_pattern_ops`, `< 3ms` Subtree Queries)
* **How to bring it up:** When asked *"How do you store folders and subfolders in PostgreSQL?"*
* **What to say:**
  > *"Alongside `parent_id` for listing direct children, every file and folder stores its full ancestry string in a `path` column like `'/rootUUID/folderAUUID/thisUUID/'`, indexed with a PostgreSQL `text_pattern_ops` B-Tree index (`CREATE INDEX idx_files_user_id_path_pattern ON files (user_id, path text_pattern_ops)`).*
  > *Why `text_pattern_ops`? Standard Postgres B-Tree indexes use locale-aware collation (`en_US.UTF-8`), which cannot use an index for `LIKE '/prefix/%'` prefix queries! Declaring `text_pattern_ops` forces raw byte-by-byte comparison, turning any deep folder subtree query (for folder size, zip download, or bulk delete) from an $O(D)$ recursive CTE join into a single $O(1)$ B-Tree range scan under 3ms. And moving a folder with 5,000 nested files is a single atomic `UPDATE` rewriting the path prefix."*

### Bonus 2: Prometheus & Grafana Observability (`/metrics` & Four Golden Signals)
* **How to bring it up:** When asked *"How do you monitor the backend or verify deduplication in production?"*
* **What to say:**
  > *"I didn't have space to fit it in the 5 resume bullets, but I instrumented the Go API with Prometheus (`/metrics` in `internal/observability/metrics.go`) and Grafana dashboards tracking Google SRE's Four Golden Signals: HTTP request latency histograms (`blobcloud_http_request_duration_seconds`), traffic counters (`blobcloud_http_requests_total`), 5xx & integrity error rates, and saturation gauges (`blobcloud_ws_active_connections`), plus live storage counters (`blobcloud_block_dedup_hits_total` vs `misses_total`)."*


---

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


---

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


---

# SECTION 3: THE 30-MINUTE WALKTHROUGH & WHITEBOARD DIAGRAMS LIBRARY
*Part 4 of 7 of the Blob-Cloud Interview Master Series*

---

> **How to use this in a live interview:**
> When the interviewer says *"Walk me through your project's architecture"*, **never talk for 20 minutes straight without pausing**.
> Instead, walk through the system in **5 short phases (4–6 minutes each)**. At the end of each phase, draw the small box diagram, state the 3 key design decisions, and ask the **Transition Question** (*"Does this layer make sense, or would you like to drill deeper here before I move to [next layer]?"*). That turns a monologue into a senior-level engineering conversation.

---

# PART A: THE 30-MINUTE TIMED WALKTHROUGH

---

## ⏱ Phase 1 (0–5 min): The Problem, Control vs. Data Plane & Stack Choices

### 🎙️ What to Say Out Loud
> *"At a high level, Blob-Cloud solves four core distributed systems problems: shift-resistant deduplication for large files, zero-trust constant-memory upload verification, decoupled background compute for malware scanning and AI search, and fault-tolerant real-time sync across multiple devices.*
>
> *The most important architectural decision I made on day one was **strictly separating the Control Plane from the Data Plane**. Raw file upload payloads never stream through the Go API server. Instead, the browser chunks files locally inside a Web Worker using FastCDC and uploads missing blocks directly to Cloudflare R2 via time-limited S3 Presigned PUT URLs.*
>
> *On the Control Plane, I used **Go** because lightweight goroutines and `io.Copy`'s 32 KB buffer let me verify chunks, stream HTTP 206 video ranges, and hold thousands of open WebSocket connections with flat memory usage. **PostgreSQL (with `pgvector`)** is the single ACID source of truth for file trees, CAS block mappings, permissions, and vector embeddings. **Redis** powers our multi-node WebSocket Pub/Sub backplane and distributed token-bucket rate limiter. And **Amazon SQS + serverless AWS Lambda** handle heavy background jobs so our API server never gets CPU-starved."*

### ✏️ What to Draw on the Whiteboard
```text
                  ┌──────────────────────────────────────┐
                  │   BROWSER (React + FastCDC Worker)   │
                  └───────┬──────────────────────┬───────┘
        Control Plane     │                      │  Data Plane (Presigned PUT)
        (HTTPS / WSS)     ▼                      ▼  (1–8 Parallel AIMD Streams)
              ┌───────────────────────┐    ┌───────────────────────────┐
              │   NGINX -> GO API     │    │       CLOUDFLARE R2       │
              │  (Stateless on EC2)   │───►│ • staging/{session}/{seq} │
              └───┬───────┬───────┬───┘    │ • blocks/{sha256} (CAS)   │
                  │       │       │        └─────────────▲─────────────┘
        ┌─────────┘       │       └──────────┐           │
        ▼                 ▼                  ▼           │ Stream Blocks
  ┌───────────┐     ┌───────────┐      ┌───────────┐     │
  │POSTGRESQL │     │   REDIS   │      │AMAZON SQS │     │
  │+ pgvector │     │ Pub/Sub & │      └─────┬─────┘     │
  │(Metadata) │     │Rate Limit │            ▼           │
  └───────────┘     └───────────┘      ┌───────────┐     │
                                       │AWS LAMBDA │─────┘
                                       │(ClamAV/AI)│
                                       └───────────┘
```

### 🎯 3 Key Decisions to Highlight
1. **Zero Upload Bytes Through the API:** Presigned URLs keep upload bandwidth off the Go server.
2. **Why Hybrid Compute (EC2 + Lambda):** EC2 holds long-lived stateful WebSockets and HTTP 206 video streams; AWS Lambda scales from `0` to handle bursty CPU-heavy ClamAV scans, thumbnails, and Gemini embeddings.
3. **Why Cloudflare R2 over S3:** 100% S3-API compatible (`aws-sdk-go-v2`), but `$0` egress fees for video streaming and file downloads.

> **⏸️ Transition Question:** *"Before I draw the PostgreSQL database schema and how we model the folder tree and CAS blocks, do you have any questions on the high-level stack?"*

---

## ⏱ Phase 2 (5–12 min): The Database Schema & Materialized Path Tree

### 🎙️ What to Say Out Loud
> *"Everything in Blob-Cloud rests on PostgreSQL's ACID guarantees. Rather than storing files as single monolithic blobs, we separate **logical files** (`files`) from **physical content chunks** (`blocks`) using a many-to-many junction table (`file_blocks`).*
>
> *In the `blocks` table, `sha256` has a global `UNIQUE` constraint — so even if 500 users upload the same 4 MB block, it is stored once in R2 under `blocks/{sha256}`. The `file_blocks` table has a composite primary key `(file_id, sequence_number)` that records the exact order of chunks needed to reconstruct the file.*
>
> *For nested folders, a naive schema only stores `parent_id`, which forces you to run an expensive recursive CTE join across every depth level just to calculate folder size, check permissions, or zip a folder. Instead, I added a **Materialized Path** column (`path = '/rootUUID/parentUUID/thisUUID/'`) indexed with PostgreSQL's `text_pattern_ops` byte-collation B-Tree index. That turns any deep subtree query (`WHERE path LIKE '/prefix/%'`) into a single O(1) index range scan under 3 milliseconds, and moving a folder with 5,000 nested files is a single atomic SQL `UPDATE`."*

### ✏️ What to Draw on the Whiteboard
```text
  [USERS]
     │ 1:N
     ▼
  [FILES] ── path: '/rootUUID/parentUUID/thisUUID/' (Materialized Path, text_pattern_ops)
     │    ── UNIQUE (user_id, parent_id, name) WHERE deleted_at IS NULL
     │
     ├── (1:N) [FILE_BLOCKS] PK: (file_id, sequence_number)
     │              │
     │              └── (N:1) [BLOCKS] sha256 UNIQUE ──► Global CAS Store
     │
     ├── (1:N) [UPLOAD_SESSION_BLOCKS] (tracks staged chunks & protects from CAS GC)
     │
     ├── (1:N) [FILE_VERSIONS] (version_number, historical chunk_hashes[])
     │
     ├── (1:N) [FILE_CHUNKS / EMBEDDINGS] (chunk_index, embedding vector(768))
     │
     ├── (1:N) [PERMISSIONS] (grantee_email, role: OWNER/EDITOR/VIEWER, status)
     │
     └── (1:N) [JOURNAL_ENTRIES] (cursor BIGSERIAL PK ──► monotonic delta sync log)
```

### 🎯 3 Key Decisions to Highlight
1. **`text_pattern_ops` on `(user_id, path)`:** Required because default `en_US.UTF-8` collation cannot use B-Tree indexes for `LIKE 'prefix%'` queries.
2. **Partial Unique Index on Active Filenames:** `UNIQUE (user_id, parent_id, name) WHERE deleted_at IS NULL` lets users have multiple trashed files with the same name while enforcing strict uniqueness among active files in a folder.
3. **`upload_session_blocks` GC Protection:** Prevents Garbage Collection from deleting an R2 block if User A empties their trash while User B has an in-flight upload session deduplicating against that exact block.

> **⏸️ Transition Question:** *"Would you like to dive deeper into the SQL schema or Materialized Path moves, or should I walk through the Zero-Trust FastCDC upload pipeline next?"*

---

## ⏱ Phase 3 (12–20 min): The Zero-Trust FastCDC Upload Pipeline

### 🎙️ What to Say Out Loud
> *"Let's walk through what happens when a user uploads a 100 MB file.*
>
> *First, inside the browser, a Web Worker slices the file using **FastCDC** (`Min: 1 MB, Avg: 4 MB, Max: 8 MB`) with a 64-bit rolling Gear hash and computes the SHA-256 of each chunk (~26 chunks). The browser sends just that JSON hash list to `POST /api/upload/initiate`.*
>
> *Second, the Go server runs a single batch query (`SELECT sha256 FROM blocks WHERE sha256 = ANY($1)`) to see which chunks already exist in R2. If the user just edited the header of a 100 MB file, 25 out of 26 chunks are already in CAS (`~98%` reuse) and are skipped immediately! For the 1 missing chunk, the server generates an S3 Presigned PUT URL pointing **not** to `blocks/`, but to an isolated quarantine key: `staging/{session_id}/{seq}`.*
>
> *Third, the browser's **AIMD worker pool** (`1–8` parallel streams) uploads the missing chunks to R2 staging and calls `POST /api/upload/complete`.*
>
> *Now comes the critical security step: **Zero-Trust Verification**. If we trusted the client's hash and let them write directly to `blocks/{sha256}`, a malicious client could upload corrupted bytes under a valid hash and poison the global CAS store. So **before** opening a Postgres transaction, our Go server uses a 4-goroutine worker pool to stream each staged chunk from R2 through `crypto/sha256` using Go's `io.Copy` (`32 KB` buffer — `O(1)` memory). Once verified, it runs `CopyObject` to promote `staging/{session_id}/{seq}` into `blocks/{sha256}` and deletes the staging object.*
>
> *Finally, it opens a fast 20–50ms PostgreSQL transaction, acquires a transaction-scoped advisory lock (`pg_advisory_xact_lock`) on `(user_id, parent_id, filename)` so concurrent tabs never race, increments `version_number` (`MAX(version) + 1`), links `file_blocks`, and appends a `journal_entries` row."*

### ✏️ What to Draw on the Whiteboard
```text
  1. Browser Worker : FastCDC (1–8 MB, Gear hash) ──► SHA-256 manifest
  2. POST /initiate : Batch check `WHERE sha256 = ANY($1)` ──► skip existing CAS hits!
  3. Presigned PUTs : Issued ONLY to quarantine `staging/{session_id}/{seq}`
  4. Browser AIMD   : PUT missing chunks (1–8 streams, 1500ms RTT threshold) ──► R2
  5. POST /complete :
       ├── Phase 1 (Outside DB TX, 4-goroutine pool, 32 KB io.Copy buffer):
       │     • HeadObject (check size) + Stream GetObject ──► verify real SHA-256
       │     • CopyObject `staging/{id}/{seq}` ──► `blocks/{sha256}` & delete staging
       │
       └── Phase 2 (Inside Atomic Postgres TX, 20–50ms):
             • pg_advisory_xact_lock(user_id, parent_id, filename)
             • Upsert blocks -> Insert/Version file (MAX(v)+1) -> Link file_blocks -> Journal
```

### 🎯 3 Key Decisions to Highlight
1. **Why Quarantine (`staging/` → `blocks/`):** Prevents CAS poisoning and catches the "local disk edit after hashing" race condition.
2. **Why Phase 1 Is Outside the DB Transaction:** Slow S3/R2 network streams never hold PostgreSQL locks or exhaust the 25-connection pool.
3. **Why `pg_advisory_xact_lock`:** Serializes concurrent uploads of the same filename even when no row exists yet, supporting `version`, `copy`, and `fail` conflict modes cleanly.

> **⏸️ Transition Question:** *"Does the two-phase quarantine and advisory lock flow make sense, or should we look at how the SQS/Lambda worker and real-time Delta Sync pick up after the transaction commits?"*

---

## ⏱ Phase 4 (20–26 min): Real-Time Delta Sync & Serverless SQS/Lambda Worker

### 🎙️ What to Say Out Loud
> *"Once that upload transaction commits, two asynchronous fan-outs happen immediately:*
>
> *First, **Background Compute via SQS + AWS Lambda**: the API publishes `{file_id, user_id}` to Amazon SQS. Our serverless AWS Lambda worker (`cmd/worker-lambda`) pulls the job, streams the file's CAS blocks from R2 over TCP `zINSTREAM` to **ClamAV** for zero-disk malware scanning, renders a compressed **thumbnail**, and splits extracted document text into overlapping chunks to generate **768-dimensional Gemini embeddings** paced at 200ms per chunk into PostgreSQL `pgvector`.*
>
> *Second, **Real-Time Delta Sync**: instead of pushing full file objects over WebSockets — which silently drops data if a user's Wi-Fi flickers — every mutation writes to `journal_entries` with a strictly increasing `BIGSERIAL` primary key (`cursor = 105`). The originating API node pushes a lightweight `{type: 'SYNC_DELTA', cursor: 105}` tickle to its local Actor-Model WebSocket Hub AND publishes it to **Redis Pub/Sub** tagged with its startup `node_id` UUID (so the publishing node ignores its own Redis echo while other API nodes notify their connected clients). When the browser sees `cursor 105 > localCursor 100`, it calls `GET /api/sync/delta?cursor=100` over HTTP, doing an O(1) B-Tree index seek in Postgres to fetch mutations `101..105` in exact order."*

### ✏️ What to Draw on the Whiteboard
```text
  Upload / Rename TX Commits in Postgres ──► writes journal_entries (cursor = 105)
     │
     ├──► 1. AMAZON SQS ──► AWS LAMBDA WORKER (`cmd/worker-lambda`)
     │                        • ClamAV TCP `zINSTREAM` Malware Scan
     │                        • Thumbnail Generation ──► R2 `thumbnails/{file_id}`
     │                        • 768-dim Gemini Embeddings (200ms pacer) ──► `pgvector`
     │
     └──► 2. REAL-TIME DELTA SYNC (`RedisBackplane`)
               ├──► Local Actor Hub ──► WS Tickle `{cursor: 105}` (Node 1 tabs)
               └──► Redis PUBLISH `{node_id, user_id, cursor: 105}`
                       ├──► Node 1 drops own echo (`env.NodeID == b.nodeID`)
                       └──► Node 2 Hub ──► WS Tickle `{cursor: 105}` (Node 2 tabs)
                                              │
                                              ▼
                                Browser: `GET /api/sync/delta?cursor=100`
                                ──► Postgres B-Tree Index Seek (`cursor > 100`)
```

---

## ⏱ Phase 5 (26–30 min): RBAC Permissions, HTTP 206 Streaming, Rate Limiting & Observability

### 🎙️ What to Say Out Loud
> *"To wrap up the walkthrough, here is how we handle sharing, media streaming, and production resilience:*
>
> *1. **Hierarchical RBAC & Public Shares:** Sharing is email-based (`OWNER`, `EDITOR`, `VIEWER`) so you can invite collaborators before they even sign up. A recursive CTE walks up the `parent_id` chain so granting `EDITOR` on a top-level folder automatically grants `EDITOR` on every nested subfolder. Public share links support Bcrypt password protection and expiry dates.*
> *2. **HTTP 206 Video Range Streaming:** Because a 1 GB video is stored across ~250 variable-size FastCDC blocks in R2, a single presigned URL can't serve cross-chunk video scrubbing. Our `DownloadFile` handler maps any `Range: bytes=X-Y` header across the ordered `file_blocks` list, seeks to the intra-block offset, streams through a 32 KB `io.LimitReader`, and re-verifies share permissions every 16 blocks (~64 MB) so revoking a link kills active streams mid-download.*
> *3. **3-Zone Redis Rate Limiting & Prometheus Metrics:** An atomic Redis Lua token-bucket limiter protects `AUTH` (`10 RPM`), `API` (`120 RPM`), and `UPLOAD` (`300 RPM`), while `/metrics` exposes Prometheus histograms and CAS dedup hit/miss counters for Grafana."*

---

# PART B: COMPLETE WHITEBOARD DIAGRAMS LIBRARY
*(Practice drawing these 6 ASCII diagrams — each takes under 60–90 seconds and covers every question an interviewer can ask.)*

---

## Diagram 1: Full System Architecture — Control Plane vs. Data Plane (60s)
```text
┌──────────────────────────────────────────────────────────────────────┐
│                   CLIENT (React + Web Worker FastCDC)                │
└───────────────┬──────────────────────────────────────┬───────────────┘
                │ Control Plane (HTTPS / WSS)          │ Data Plane: Presigned PUT
                ▼                                      ▼ (1–8 Parallel AIMD Streams)
┌───────────────────────────────┐        ┌─────────────────────────────┐
│     NGINX Ingress (EC2)       │        │        CLOUDFLARE R2        │
└───────────────┬───────────────┘        │  • staging/{session}/{seq}  │
                │                        │  • blocks/{sha256} (CAS)    │
                ▼                        └──────────────▲──────────────┘
┌───────────────────────────────┐                       │
│   GO API SERVER (Stateless)   │───────────────────────┘
│  (O(1) 32KB SHA-256 Verify)   │   Stream Verify & PromoteObject
└───┬───────────┬───────────┬───┘
    │           │           │
    ▼           ▼           ▼
┌──────────┐ ┌────────┐ ┌──────────┐     ┌─────────────────────────────┐
│PostgreSQL│ │ Redis  │ │Amazon SQS│────►│   AWS LAMBDA WORKER         │
│+pgvector │ │Pub/Sub │ │Job Queue │     │ • ClamAV Malware Scan       │
│(Metadata)│ │& Limit │ └──────────┘     │ • Thumbnail Generation      │
└──────────┘ └────────┘                  │ • 768-dim Gemini Embeddings │
                                         └─────────────────────────────┘
```

---

## Diagram 2: Zero-Trust Upload Quarantine & ACID Promotion Sequence (90s)
```text
CLIENT (Worker)       GO API                  POSTGRES             CLOUDFLARE R2
  │                     │                        │                       │
  │─1. FastCDC (1–8MB)─►│                        │                       │
  │ POST /initiate      │─2. SELECT existing────►│                       │
  │ {sha256 list}       │◄───dedup hits──────────│                       │
  │◄─3. Presigned PUTs──│                        │                       │
  │ (staging/{id}/{seq})│                        │                       │
  │                     │                        │                       │
  │─4. AIMD PUT chunks (1–8 parallel streams)───────────────────────────►│
  │◄───200 OK ───────────────────────────────────────────────────────────│
  │                     │                        │                       │
  │─5. POST /complete──►│                        │                       │
  │                     │─6. HeadObject + 32KB Stream SHA-256 Verify────►│
  │                     │─7. CopyObject staging -> blocks/{sha256}──────►│
  │                     │                        │                       │
  │                     │─8. BEGIN ATOMIC TX────►│                       │
  │                     │  pg_advisory_xact_lock │                       │
  │                     │  upsert blocks & file  │                       │
  │                     │  MAX(version)+1        │                       │
  │                     │  write journal_entries │                       │
  │◄─9. 200 OK {id}─────│◄─COMMIT (20-50ms)──────│                       │
```

---

## Diagram 3: Real-Time Delta Sync & Redis Echo Suppression (60s)
```text
USER A     API NODE 1      POSTGRES    REDIS (node_id)   API NODE 2    USER B
  │             │               │             │               │            │
  │─mutation───►│               │             │               │            │
  │             │─write journal►│             │               │            │
  │             │◄─cursor=105───│             │               │            │
  │◄─WS:{105}──│               │             │               │            │
  │             │────────────────────────────►PUBLISH         │            │
  │             │◄──echo dropped (node_id)────│──────────────►│            │
  │             │               │             │               │─WS:{105}──►│
  │             │               │             │               │            │
  │             │               │        ◄─GET /api/sync/delta?cursor=104──│
  │             │               │◄─SELECT (B-Tree Seek)────────────────────│
  │             │               │─entries 105─────────────────────────────►│
```

---

## Diagram 4: Database Schema & CAS Relationships (45s)
```text
  ┌─────────┐
  │  USERS  │
  └────┬────┘
       │ 1:N
       ▼
  ┌────────────────────────────────────────────────────────┐
  │                         FILES                          │
  │ path: '/rootUUID/childUUID/' (Materialized Path)       │
  │ UNIQUE (user_id, parent_id, name) WHERE deleted_at NULL│
  └──┬──────────┬───────────┬───────────┬───────────────┬──┘
     │          │           │           │               │
  1:N│       1:N│        1:N│        1:N│            1:N│
     ▼          ▼           ▼           ▼               ▼
┌─────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌───────────────┐
│FILE     │ │ JOURNAL  │ │PERMISSION│ │ FILE     │ │ FILE_CHUNKS   │
│BLOCKS   │ │ ENTRIES  │ │grantee   │ │ VERSIONS │ │ (Embeddings)  │
│(file_id,│ │cursor    │ │email     │ │version   │ │chunk_index    │
│ seq_num)│ │BIGSERIAL │ │role      │ │number    │ │vector(768)    │
└────┬────┘ └──────────┘ └──────────┘ └──────────┘ └───────────────┘
     │ N:1
     ▼
┌─────────────────┐
│     BLOCKS      │
│ sha256 (UNIQUE) │ ◄── Global Content-Addressed Store (CAS)
└─────────────────┘
```

---

## Diagram 5: HTTP 206 Multi-Chunk Byte-Range Stitching (45s)
```text
  Browser Video Player requests: `Range: bytes=6000000-10000000` (4 MB slice)

  File in DB (`file_blocks` ordered by `sequence_number`):
  ┌───────────────────┬───────────────────────────┬─────────────────────┐
  │ Chunk 0 (3.5 MB)  │     Chunk 1 (4.5 MB)      │   Chunk 2 (5.0 MB)  │
  │ Bytes: 0 .. 3.5M  │  Bytes: 3.5M .. 8.0M      │  Bytes: 8.0M .. 13M │
  └───────────────────┴──────────────┬────────────┴──────────┬──────────┘
           ▲                         │                       │
      Skipped!             1. Seek to +2.5M offset   2. Read first 2.0M
   (3.5M <= 6.0M)             Read remaining 2.0M       via io.LimitReader
                                     │                       │
                                     └───────────┬───────────┘
                                                 ▼
                           Streamed via 32 KB `io.Copy` ──► `206 Partial Content`
                           (Re-verifies share access every 16 blocks!)
```

---

## Diagram 6: Recursive CTE Folder Permission Inheritance (30s)
```text
  "Can alice@email.com EDIT /Shared/Engineering/Specs/API.pdf?"

       API.pdf        (no direct permission row for Alice)
          │ walk parent_id ↑
       Specs/         (no direct permission row for Alice)
          │ walk parent_id ↑
       Engineering/   (no direct permission row for Alice)
          │ walk parent_id ↑
       Shared/        (Alice has role = 'EDITOR', status = 'ACCEPTED') ──► ALLOWED!
```


---

# SECTION 4: TECHNICAL DEEP DIVES (4A – 4G)
*Part 5 of 7 of the Blob-Cloud Interview Master Series*

---

> **When to use this section:**
> During a technical interview, after you give your high-level explanation, the interviewer will pick **one or two subsystems** and say: *"Let's go deep on how you implemented that — show me the exact math, SQL queries, or Go patterns."*
>
> Each subsection below gives you the **exact formulas, SQL queries, Go code patterns, and "Why X over Y & Z" trade-off tables** so you can go as deep as a Staff Engineer on any component.

---

## Deep Dive 4A: FastCDC Storage Engine & Cross-Language Parity Math

### 1. The Exact FastCDC Parameters (`internal/chunker/fastcdc.go` & `fastcdc.worker.ts`)
- **`MinSize = 1 MiB` (`1,048,576` bytes):** The chunker immediately skips the first 1 MB of every chunk without computing any hash.
  - *Why?* Two reasons: (1) It guarantees we never generate tiny 4 KB S3 objects that inflate metadata rows and S3 PUT request costs; (2) Skipping the first 1 MB of a 4 MB average chunk bypasses hashing on **~25% of all bytes**, boosting CPU throughput above 120 MB/s.
- **`AvgSize = 4 MiB` (`4,194,304` bytes = $2^{22}$ bytes):** The normalization inflection point ($k = \log_2(2^{22}) = 22$ bits).
- **`MaxSize = 8 MiB` (`8,388,608` bytes):** The hard ceiling. If no hash match occurs by 8 MiB, we force a cut at 8 MiB.

### 2. The Rolling Gear Hash & Normalized Dual Bitmasks
We maintain a 256-entry lookup table `GEAR_TABLE` of deterministic pseudo-random 64-bit unsigned integers. For each byte `b` from `offset = 1 MiB` to `8 MiB`:

```go
// In Go (backend/internal/chunker/fastcdc.go):
hash = (hash << 1) + gearTable[b]
```
```typescript
// In TypeScript Web Worker (frontend/src/workers/fastcdc.worker.ts):
// Explicit 64-bit mask required because JS BigInt has arbitrary precision!
hash = ((hash << 1n) + GEAR_TABLE[b]) & 0xffffffffffffffffn
```

**Why Two Bitmasks (`NormDelta = 1`)?**
With $k = 22$ bits ($\text{AvgSize} = 2^{22} = 4\text{ MiB}$):
1. **In Region 1 (`1 MiB` $\le \text{pos} <$ `4 MiB`):**
   - We use **`MaskSmall = (1 << (22 + 1)) - 1 = 0x7fffff`** (**23 one-bits**).
   - Condition: `if (hash & 0x7fffff) == 0` → Cut!
   - Probability of cutting on any single byte is $2^{-23} \approx \frac{1}{8.38\text{M}}$. Making the mask stricter below 4 MiB prevents premature small chunks.
2. **In Region 2 (`4 MiB` $\le \text{pos} <$ `8 MiB`):**
   - We switch to **`MaskLarge = (1 << (22 - 1)) - 1 = 0x1fffff`** (**21 one-bits**).
   - Condition: `if (hash & 0x1fffff) == 0` → Cut!
   - Probability of cutting jumps 4× to $2^{-21} \approx \frac{1}{2.09\text{M}}$, smoothly pulling the chunk boundary in before it slams into the 8 MiB hard ceiling.

### 3. Single-Query Batch Deduplication Lookup (`internal/repository/postgres/block.go`)
Instead of running 50 separate `SELECT` queries for a 50-chunk file ($N+1$ problem), `GetExistingHashes` checks all chunk hashes in **one round-trip** using PostgreSQL's `ANY` array operator against the `UNIQUE (sha256)` B-Tree index:
```sql
SELECT sha256
FROM blocks
WHERE sha256 = ANY($1::text[]);
```

### 4. Empirical 100 MB Deduplication Benchmark (`cmd/bench-chunker/main.go`)

| Workload (`100 MB` Base File = `26` chunks) | Fixed `4 MB` Chunks Reused | Fixed `4 MB` Re-Uploaded | FastCDC (`1–8 MB`) Chunks Reused | FastCDC Re-Uploaded |
| :--- | :--- | :--- | :--- | :--- |
| **1. Prepend `64 KB` at byte `0`** | `0 / 26` (**`0.0%` saved**) | `100.1 MB` | **`25 / 26` (`97.9%` bytes saved)** | **`2.1 MB`** (only 1st chunk) |
| **2. Insert `64 KB` in middle (`50 MB`)** | `12 / 26` (**`48.0%` saved**) | `52.1 MB` | **`25 / 26` (`92.4%` bytes saved)** | **`7.6 MB`** (only middle chunk) |
| **3. Overwrite `64 KB` in-place (`50 MB`)** | `24 / 25` (`96.0%` saved) | `4.0 MB` | **`25 / 26` (`92.4%` bytes saved)** | **`7.6 MB`** |

---

## Deep Dive 4B: Materialized Path Hierarchy, `text_pattern_ops` & Cycle-Safe Moves

### 1. Why Materialized Path Over the 3 Alternatives?
Every interviewer asks: *"Why did you store `path = '/rootUUID/folderAUUID/thisUUID/'` instead of another tree pattern?"* Memorize this comparison table:

| Tree Pattern | Read Subtree Cost | Move Subtree Cost | Storage Overhead | Why We Accepted / Rejected It |
| :--- | :--- | :--- | :--- | :--- |
| **1. Adjacency List (`parent_id` only)** | $O(D)$ Recursive CTE joins (slow at deep nesting) | $O(1)$ (`UPDATE parent_id`) | Minimal (1 UUID) | **Rejected for subtree scans:** Zipping, deleting, or sizing a 10-level deep folder requires 10 recursive joins. |
| **2. Nested Sets (`lft`, `rgt` integers)** | $O(1)$ (`WHERE lft BETWEEN A AND B`) | **$O(N)$ Write Amplification!** | 2 integers | **Rejected:** Inserting 1 new file requires renumbering `lft`/`rgt` on thousands of unrelated rows to the right of the tree! |
| **3. Closure Table (`ancestor_id, descendant_id`)** | $O(1)$ index join | $O(K)$ delete/re-insert | **$O(N \times D)$ rows** | **Rejected:** A file at depth 10 creates 10 rows in the closure table — 1M files = 10M extra index rows. |
| **4. Materialized Path (`parent_id` + `path`)** | **$O(1)$ B-Tree Prefix Scan (`< 3ms`)** | **1 Atomic `UPDATE` statement** | 1 `TEXT` column | **Chosen:** Combines `parent_id` for fast direct-child listing with `path` (`text_pattern_ops`) for $O(1)$ subtree operations. |

### 2. Why `text_pattern_ops` Is Mandatory in PostgreSQL
```sql
CREATE INDEX idx_files_user_id_path_pattern
ON files (user_id, path text_pattern_ops);
```
- By default, PostgreSQL creates B-Tree indexes using the OS locale collation (e.g., `en_US.UTF-8`), where character sorting depends on complex language rules (hyphens, accents, case-folding). Because of that, the Postgres query planner **refuses** to use a standard B-Tree index for `WHERE path LIKE '/prefix/%'` and falls back to a sequential table scan!
- Specifying `text_pattern_ops` tells Postgres to index `path` using raw C byte-by-byte (`memcmp`) ordering. That allows the query planner to rewrite `path LIKE '/abc/%'` into a pure B-Tree range seek:
  ```sql
  path >= '/abc/' AND path < '/abc0'  -- ('0' is ASCII 48, right after '/' which is ASCII 47!)
  ```

### 3. All 5 Directory SQL Patterns (`internal/repository/postgres/file.go`)
1. **List Direct Children of a Folder:**
   ```sql
   SELECT * FROM files
   WHERE user_id = $1 AND parent_id = $2 AND deleted_at IS NULL;
   ```
2. **Fetch Full Subtree (for Folder Size, Bulk Trash, or Streaming ZIP):**
   ```sql
   SELECT * FROM files
   WHERE user_id = $1 AND path LIKE $2 || '%' AND deleted_at IS NULL;
   ```
3. **Atomic Subtree Move (Rewriting 5,000 Descendant Paths in 1 Statement):**
   ```sql
   UPDATE files
   SET path = $newPrefix || SUBSTRING(path FROM CHAR_LENGTH($oldPrefix) + 1),
       updated_at = NOW()
   WHERE user_id = $1 AND path LIKE $oldPrefix || '%';
   ```
4. **Move Item Back to Root (`parent_id = NULL`):**
   ```sql
   UPDATE files
   SET parent_id = NULL, path = '/' || id::text || '/'
   WHERE id = $1 AND user_id = $2;
   ```
5. **Deadlock-Free Locking & Circular Move Guard (`MoveFile`):**
   - What if User A tries to move `Folder X` *inside* its own subfolder `Folder X/Subfolder Y`? Or what if two requests move `X -> Y` and `Y -> X` simultaneously?
   - We lock the source and destination rows in **deterministic lexicographical UUID order** (`if srcID < dstID` lock `src` then `dst`, else lock `dst` then `src` to make deadlocks impossible) and verify:
     ```go
     if strings.HasPrefix(destFolder.Path, sourceFolder.Path) {
         return domain.ErrInvalidMoveCycle // Cannot move a folder into its own descendant!
     }
     ```

---

## Deep Dive 4C: Real-Time Delta Sync Engine, Actor Hub & Echo Suppression

### 1. The Monotonic Journal Schema (`000017_journal_entries.up.sql`)
```sql
CREATE TABLE journal_entries (
    cursor      BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    file_id     UUID NOT NULL,
    action      VARCHAR(32) NOT NULL, -- 'CREATE', 'UPDATE', 'MOVE', 'TRASH', 'RESTORE', 'DELETE'
    name        VARCHAR(255),
    parent_id   UUID,
    size_bytes  BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_journal_user_cursor ON journal_entries (user_id, cursor);
```

### 2. Paginated Delta Fetch & Cursor Drift Auto-Healing (`sync_handlers.go`)
When a browser wakes up or receives a WebSocket tickle, it calls `GET /api/sync/delta?cursor=100&limit=100`:
```sql
SELECT cursor, file_id, action, name, parent_id, size_bytes, created_at
FROM journal_entries
WHERE user_id = $1 AND cursor > $2
ORDER BY cursor ASC
LIMIT $3 + 1; -- Fetch 1 extra row (101) to detect `has_more` without a second COUNT(*) query!
```
- **Cursor Drift Recovery:** What if the database was restored from backup or reset, so the browser's `localStorage` cursor (`5000`) is ahead of the database's current `MAX(cursor)` (`120`)?
  In `sync_handlers.go`, we check the user's `max_cursor`. If `clientCursor > maxCursor`, we return `reset_cursor: true` along with `max_cursor: 120`. In `Dashboard.tsx`, the sync loop detects `reset_cursor`, re-anchors `syncCursorRef.current = 120`, and triggers a clean folder refresh.

### 3. Actor-Model WebSocket Hub (`internal/sync/hub.go`)
```go
func (h *Hub) Run() {
    for {
        select {
        case client := <-h.register:
            if h.clients[client.UserID] == nil {
                h.clients[client.UserID] = make(map[*Client]struct{})
            }
            h.clients[client.UserID][client] = struct{}{}

        case client := <-h.unregister:
            if conns, ok := h.clients[client.UserID]; ok {
                if _, exists := conns[client]; exists {
                    delete(conns, client)
                    close(client.send)
                }
            }

        case msg := <-h.notify:
            for _, client := range h.clientsForUser(msg.UserID) {
                select {
                case client.send <- msg.Payload:
                default:
                    // Non-blocking drop: if a dead tab's 256-message buffer is full,
                    // close it immediately so it NEVER blocks the Hub goroutine!
                    delete(h.clients[msg.UserID], client)
                    close(client.send)
                }
            }
        case <-h.done:
            return
        }
    }
}
```

### 4. Multi-Node Redis Pub/Sub with Echo Suppression (`internal/sync/backplane.go`)
Each Go server generates `nodeID: uuid.NewString()` when starting up:
```go
func (b *RedisBackplane) NotifyUser(userID string, event Event) {
    // 1. Instant local delivery (< 1ms) to tabs connected to THIS node
    b.hub.NotifyUser(userID, event)

    // 2. Broadcast to Redis so OTHER API nodes can notify their connected tabs
    env := envelope{NodeID: b.nodeID, UserID: userID, Event: event}
    _ = b.redis.Publish(ctx, b.channel, marshal(env)).Err()
}

func (b *RedisBackplane) dispatch(rawPayload string) {
    var env envelope
    unmarshal(rawPayload, &env)
    if env.NodeID == b.nodeID {
        return // Drop self-echo! We already delivered locally in Step 1.
    }
    b.hub.NotifyUser(env.UserID, env.Event)
}
```

---

## Deep Dive 4D: Zero-Trust Quarantine, Advisory Locks & CAS Garbage Collection

### 1. Why Phase 1 (S3/R2 I/O) and Phase 2 (Postgres TX) Are Strictly Separated
In `CompleteSession` (`internal/service/upload_service.go`):
```text
PHASE 1 (Outside DB TX — 0 DB Locks Held):
  ├── Check session status == 'INITIATED'
  ├── Spawn bounded 4-goroutine worker pool for newly staged chunks:
  │     ├── storage.HeadObject("staging/{session}/{seq}") -> verify byte size
  │     ├── storage.GetObject("staging/{session}/{seq}") -> io.Copy(sha256, rc) [32 KB RAM]
  │     ├── Compare computed SHA-256 == manifest SHA-256 (abort on mismatch!)
  │     └── storage.PromoteObject -> CopyObject to "blocks/{sha256}" & Delete staging
  │
PHASE 2 (Inside Atomic Postgres TX — 20 to 50ms Hold Time):
  ├── tx := db.BeginTx(ctx, nil)
  ├── SELECT pg_advisory_xact_lock(hashtext(user_id || ':' || COALESCE(parent_id,'root') || ':' || filename))
  ├── Re-verify session status == 'INITIATED' inside TX (TOCTOU guard!)
  ├── Upsert `blocks` (ON CONFLICT (sha256) DO NOTHING)
  ├── Apply conflict_mode ('version' -> MAX(version_number)+1 | 'copy' -> auto-rename | 'fail' -> 409)
  ├── Insert `file_blocks` & `journal_entries`
  └── tx.Commit()
```

### 2. Preventing the CAS Garbage Collection Race Condition (`internal/repository/postgres/file.go`)
When User A permanently deletes a file from Trash (`HardDelete`), we must only delete physical R2 blocks (`blocks/{sha256}`) that have **zero references anywhere in the system**.
If we only checked `file_blocks` and `file_versions`, we would delete a block that User B is currently deduplicating against in an active `INITIATED` upload session!
Our `findOrphanedHashes` query checks **all three tables atomically**:
```sql
SELECT b.sha256
FROM blocks b
WHERE b.sha256 = ANY($1::text[])
  AND NOT EXISTS (
      SELECT 1 FROM file_blocks fb WHERE fb.block_id = b.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM file_versions fv WHERE b.sha256 = ANY(fv.chunk_hashes)
  )
  AND NOT EXISTS (
      SELECT 1 FROM upload_session_blocks usb WHERE usb.block_hash = b.sha256
  );
```

---

## Deep Dive 4E: Hierarchical RBAC, Shortcuts & Public Share Dual-Auth

### 1. Three-Role Permission Matrix
| Action | `OWNER` | `EDITOR` | `VIEWER` |
| :--- | :---: | :---: | :---: |
| View metadata, preview, download, stream HTTP 206 | ✅ | ✅ | ✅ |
| Upload new versions, upload into folder, rename, move | ✅ | ✅ | ❌ |
| Share with others, revoke permissions, trash / hard-delete | ✅ | ❌ | ❌ |

### 2. Recursive CTE Permission Check (`internal/repository/postgres/permission.go`)
Granting `EDITOR` on `/Engineering` automatically grants `EDITOR` on `/Engineering/Backend/Architecture.pdf` without duplicating permission rows on every child file:
```sql
WITH RECURSIVE chain AS (
    SELECT id, parent_id FROM files WHERE id = $1
    UNION ALL
    SELECT f.id, f.parent_id FROM files f JOIN chain c ON f.id = c.parent_id
)
SELECT EXISTS (
    SELECT 1 FROM permissions p
    JOIN chain c ON c.id = p.file_id
    WHERE LOWER(p.grantee_email) = LOWER($2)
      AND p.status = 'ACCEPTED'
      AND (p.expires_at IS NULL OR p.expires_at > NOW())
      AND p.role = ANY($3::text[])
) AS allowed;
```

### 3. Public Share Link Dual-Authentication (`public_handlers.go`)
When a public share link is password-protected (verified via `bcrypt.CompareHashAndPassword`), the server issues a signed share token. Because browser `<video src="...">` and `<audio>` tags cannot attach custom `X-Share-Token` HTTP headers when making HTTP 206 Range requests, `public_handlers.go` supports **three authentication channels** seamlessly:
1. `X-Share-Token` HTTP header (for `fetch` / Axios API calls)
2. `?share_token=<token>` URL query parameter (for `<video>` / `<audio>` range streaming and direct downloads)
3. `HttpOnly` cookie with dynamic `Secure` / `SameSite` flags based on TLS detection (`r.TLS != nil || X-Forwarded-Proto == "https"`).

---

## Deep Dive 4F: SQS + AWS Lambda + ClamAV `zINSTREAM` + 768-Dim `pgvector` Search

### 1. Zero-Disk ClamAV Streaming (`internal/antivirus/clamav.go`)
AWS Lambda only provides `/tmp` storage (512 MB by default), so writing a 2 GB file to local disk before scanning would crash the Lambda container. Instead, `ScanStream` streams CAS blocks directly from R2 over a TCP socket to the ClamAV daemon using the `zINSTREAM\x00` protocol:
```text
Client -> ClamAV TCP Socket:
  "zINSTREAM\x00"
  [4-byte Big-Endian uint32 length: 32768][32 KB raw bytes]
  [4-byte Big-Endian uint32 length: 32768][32 KB raw bytes]
  ...
  [4-byte Big-Endian uint32 length: 0]     <-- signals EOF!
ClamAV -> Client:
  "stream: OK\x00"  OR  "stream: Eicar-Test-Signature FOUND\x00"
```

### 2. Document Chunking, Paced Gemini Embeddings & Hybrid Search (`internal/queue/processor.go` & `file.go`)
- **Overlapping Text Chunking:** Long documents are split into overlapping text chunks (`chunk_index = 0, 1, 2...`) so specific paragraphs retain high cosine similarity.
- **Rate-Limit-Safe Pacing (`GEMINI_EMBEDDING_PACE_MS = 200ms`):** Between chunk embedding calls to Gemini (`text-embedding-004` returning `float32[768]`), the worker reuses a single `time.NewTimer(200 * time.Millisecond)` inside a `select { case <-ctx.Done(): ... case <-timer.C: }` block — avoiding both `time.After` memory leaks and HTTP 429 quota errors.
- **Hybrid Search Query (`SearchFiles`):**
  Combines PostgreSQL keyword/summary matching with `pgvector` cosine similarity (`1 - (embedding <=> $queryVector)`), grouping by `file_id` to score each file by its best-matching chunk (`MAX(similarity)`).

---

## Deep Dive 4G: Auth, 3-Zone Redis Lua Rate Limiting & Cloud Sizing

### 1. JWT + Server-Side Session Revocation (`internal/auth/jwt.go` & `session_handlers.go`)
- **Access Token (`HS256`):** Short-lived JWT containing `user_id`, `session_id`, and `is_refresh: false`.
- **Refresh Token & Device Management:** Every login creates a row in `user_sessions` recording device User-Agent, IP address, and expiry. Users can view all active devices in Settings (`ActiveSessionsModal.tsx`) and click **"Revoke"** on any device — which deletes that `user_sessions` row and dispatches a `FORCE_LOGOUT` WebSocket event through the Hub to kick that device off immediately.
- **Refresh Token Misuse Guard:** Middleware explicitly rejects any JWT where `claims.IsRefresh == true` on normal API routes so a refresh token can never be used directly as a bearer access token.

### 2. Atomic Redis Lua Token-Bucket Rate Limiter (`internal/ratelimit/redis_limiter.go`)
| Zone | Rate Limit | Burst | Target Routes |
| :--- | :--- | :--- | :--- |
| **`AUTH`** | `10 req/min` | `10` | `/api/auth/login`, `/register`, `/verify`, `/forgot-password` |
| **`API`** | `120 req/min` | `120` | Folder browsing, metadata, sharing, search |
| **`UPLOAD`** | `300 req/min` | `300` | `/api/upload/*` (sized for 8-stream AIMD parallel chunking) |

### 3. Production Resource & Free-Tier Sizing Math
| Resource | Limit | Our Configuration | Why This Matters |
| :--- | :--- | :--- | :--- |
| **PostgreSQL Connections** | `~80` max on `t3.micro` | `MaxOpenConns = 25` per API node | `3 API nodes × 25 = 75` connections, leaving 5 connections for migrations/admin without ever hitting `too many clients`. |
| **Server RAM per Stream** | `1 GB` on `t2.micro` / `t3.micro` | `32 KB` (`io.Copy` buffer) | 50 simultaneous chunk verifications or video streams consume only `50 × 32 KB = 1.6 MB` of heap RAM! |
| **Amazon SQS Requests** | `1,000,000` free req/month | `WaitTimeSeconds = 20` (Long Poll) | Empty queue polling makes only `~129,600` calls/month instead of `4.3M` calls/month (97% reduction). |


---

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


---

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

