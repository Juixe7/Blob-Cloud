# 01. System Architecture & High-Level Design

## Executive Summary & System Pitch

**Blob-Cloud** is a high-performance, cloud-native file storage and collaboration platform (Google Drive clone) engineered to handle large-scale binary transfers, global data deduplication, fine-grained access control, and asynchronous media processing.

The core architectural principle of Blob-Cloud is **Zero-Proxy Storage Ingestion**: the application backend (Go) acts purely as an API control plane for authentication, metadata indexing, permission checking, and orchestration. Binary file payloads are never proxied through the Go server during upload or download, protecting server CPU and RAM from network I/O saturation.

---

## 1. High-Level Architecture Pattern

```
                       +-----------------------------------+
                       |        React + TypeScript         |
                       |       Single-Page Application     |
                       +-----------------+-----------------+
                                         |
               +-------------------------+-------------------------+
               | (1) Metadata/Auth/APIs  | (2) Direct Cloud Uploads| (3) WebSockets
               v                         v                         v
+-------------------------------+ +-------------+ +-------------------------------+
|      Go API Control Plane     | |  AWS S3 /   | |   Real-Time Notification Hub   |
|   (Modular Monolith on EC2)   | |Cloudflare R2| |      (In-Memory Pub/Sub)      |
+---------------+---------------+ +------+------+ +-------------------------------+
                |                        ^
                | (4) SQL Queries        | (5) Download Blocks / Put Thumbnails
                v                        |
+---------------+---------------+ +------+------+
|     PostgreSQL Database       | |  AWS SQS    | <--- (6) Upload Completed Event
| (Metadata, ACLs, Block Index) | | Queue Service
+-------------------------------+ +------+------+
                                         |
                                         | (7) SQS Long-Polling (20s)
                                         v
                                +--------+--------+
                                |  Go Worker Pool |
                                |  (Thumbnailing) |
                                +-----------------+
```

### Architectural Choice: Modular Monolith in Go
- **Decision**: Architected as a **Modular Monolith** rather than microservices.
- **Rationale**:
  - Eliminates network overhead between microservices (e.g. gRPC/REST IPC calls).
  - Maintains strict internal boundaries via Go packages (`internal/transport`, `internal/service`, `internal/repository`, `internal/domain`, `internal/storage`, `internal/queue`).
  - Simplifies deployments (single compiled binary, low memory footprint ~30-40MB RAM).
  - Can be split into microservices easily in the future because service domain interfaces are decoupled.

---

## 2. Core Architectural Subsystems & Pipelines

### A. Direct-to-Cloud Storage Pipeline (Presigned URLs + CDN Edge)

#### Problem
Traditional cloud storage platforms buffer incoming multipart form requests on the app server before writing to S3. Under heavy load (e.g. 100 users uploading 1GB files simultaneously), app servers quickly suffer from:
1. Garbage Collection (GC) pressure due to memory buffer allocations.
2. Network thread saturation (socket starvation).
3. Increased cloud hosting costs (high EC2 ingress bandwidth).

#### Solution in Blob-Cloud
1. The client breaks files into **4MB blocks** and hashes them using SHA-256.
2. The client requests presigned upload URLs from Go: `POST /api/upload/initiate`.
3. The Go backend checks which block hashes are missing from the global `blocks` table.
4. Go generates **AWS S3 Presigned PUT URLs** (valid for 15 minutes) only for missing blocks using the AWS SDK for Go v2 ([s3.go](file:///c:/Users/Asus/Desktop/z/backend/internal/storage/s3.go)).
5. The React client uploads missing binary chunks directly to the S3 bucket via CDN edge nodes (AWS CloudFront / Cloudflare).
6. Once all blocks are stored, client sends `POST /api/upload/complete`. Go executes a single PostgreSQL transaction linking file metadata to the global blocks.

---

### B. Global Block-Level Deduplication Engine (Single-Instance Storage)

#### How It Works
- **Chunk Size**: Fixed 4MB boundary.
- **Fingerprint**: SHA-256 hash (256-bit cryptographic digest formatted as a 64-character hex string).
- **Database Schema**:
  - `blocks`: Maps `hash` (UNIQUE string key) -> `s3_key`, `size_bytes`, `created_at`.
  - `file_blocks`: Junction table mapping `file_id` + `block_index` -> `block_id`.

```
User A Uploads: "Document_v1.pdf" (8MB) -> Blocks [Hash1, Hash2]
  -> Hash1 uploaded to S3 ("blocks/Hash1.bin")
  -> Hash2 uploaded to S3 ("blocks/Hash2.bin")

User B Uploads: "Shared_Doc.pdf"  (8MB) -> Blocks [Hash1, Hash2]
  -> Backend checks blocks table: Hash1 & Hash2 already exist!
  -> Direct S3 Upload SKIPPED.
  -> Instant complete! File metadata linked to existing Hash1 & Hash2.
```

#### Savings & Impact
- **Storage Cost Reduction**: Up to 60-80% for enterprise teams sharing duplicate assets, templates, and archives.
- **Bandwidth & Latency**: Instant uploads for files already present in the system (Zero-Byte Transmission).

---

### C. Resumable Upload Session State Machine

#### Problem
Uploading large files over flaky connections (mobile networks, weak Wi-Fi) leads to dropped connections. Restarting an entire 1GB upload from 0% degrades user experience and wastes bandwidth.

#### State Machine Solution
1. Initiating upload creates an `upload_sessions` record (`status = 'IN_PROGRESS'`) along with expected `session_blocks` (`block_index`, `hash`, `is_uploaded`).
2. As each 4MB chunk completes direct upload to S3, the client calls `PATCH /api/upload/session/:id/block`.
3. If network drops, client queries `GET /api/upload/session/:id`.
4. Backend returns array of uploaded block indices.
5. Client resumes uploading **only** the missing chunk indices.

---

## 3. Trade-Offs & Architectural Alternatives

| Architecture Choice | Implemented in Blob-Cloud | Alternative Approach | Why We Chose Our Approach |
| :--- | :--- | :--- | :--- |
| **Data Ingestion** | **Direct-to-Cloud Presigned PUT URLs** | Proxy payload through Go server | Protects Go server memory/bandwidth; offloads TLS handshakes to CDN edge. |
| **Deduplication Strategy** | **Fixed 4MB Block Chunking** | Whole-file fingerprinting OR Content-Defined Chunking (CDC / Rabin Fingerprints) | Fixed 4MB is fast to calculate in browser Web Workers without heavy CPU overhead of rolling CDC hashes. |
| **Cloud Storage SDK** | **AWS S3 Presigned URLs** | AWS S3 Multipart Upload API | S3 Multipart requires initiating uploads via S3 API first; block deduplication operates on independent 4MB S3 objects key-named by hash, enabling true multi-file cross-user deduplication. |
| **System Model** | **Modular Monolith** | Microservices Architecture | Avoids network latency overhead of inter-service RPCs, simplifies deployment, while maintaining clean package separation. |

---

## 4. Interviewer Deep-Dive Q&A

### Q1: What happens if two users upload the exact same file at the exact same time? Does deduplication cause race conditions?
**Answer**: 
No. We handle concurrent block creation gracefully using PostgreSQL `INSERT ... ON CONFLICT (hash) DO NOTHING` in [block.go](file:///c:/Users/Asus/Desktop/z/backend/internal/repository/postgres/block.go). 
Both upload transactions try to insert block hashes. Whichever completes first creates the row; the second gets the existing `block_id`. Physical write to S3 key `blocks/{hash}` is idempotent because S3 PUT requests are atomic overwrite operations.

### Q2: Is SHA-256 block deduplication vulnerable to collision attacks or privacy risks?
**Answer**:
- **Collisions**: SHA-256 has a hash space of $2^{256}$ (approx $1.15 \times 10^{77}$). The probability of a random hash collision is practically zero.
- **Privacy (Convergent Encryption / Hash Side-Channel)**: If User A knows User B uploaded a sensitive file, User A could check if hash exists. To mitigate this in strict enterprise environments, client-side encryption keys derived from user passwords can be combined with hashes (convergent encryption).

### Q3: What happens to orphaned blocks in S3 if an upload session is abandoned mid-way?
**Answer**:
If a user starts an upload, presigned URLs write blocks to S3, but the user closes the tab before calling `/api/upload/complete`, those S3 objects exist without being linked to any `file_blocks`.
**Solution**: We design an asynchronous daily **Garbage Collection (GC) Cron Worker** that queries S3 keys not present in `blocks` table or unreferenced `blocks` with zero `file_blocks` links and purges them.

### Q4: How does presigned URL security work? Can a user alter the file path or size during upload?
**Answer**:
No. S3 presigned URLs encode the exact bucket name, S3 object key (`blocks/{hash}`), HTTP method (`PUT`), signature algorithm (HMAC-SHA256), and expiry timestamp into the URL parameters signed by AWS Secret Credentials. Any attempt to modify the key or headers invalidates the signature, causing AWS S3 to return `403 Forbidden`.
