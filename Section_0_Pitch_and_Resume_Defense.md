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
