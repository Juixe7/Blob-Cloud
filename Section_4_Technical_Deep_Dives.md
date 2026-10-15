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
