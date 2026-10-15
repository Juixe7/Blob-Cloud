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
