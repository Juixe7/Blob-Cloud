...AWS Free Tier limits (or Cloudflare R2). 

Here is the complete, production-ready `README.md` file for your GitHub repository. It is written from your perspective as the engineer, emphasizing design trade-offs, architecture, and system resilience.

---

# Blob-Cloud

[![Go Version](https://img.shields.io/github/go-mod/go-version/yourusername/blob-cloud?filename=backend%2Fgo.mod)](https://golang.org)
[![React](https://img.shields.io/badge/React-20232A?style=flat&logo=react&logoColor=61DAFB)](https://react.dev)
[![AWS](https://img.shields.io/badge/AWS-%23FF9900.svg?style=flat&logo=amazon-aws&logoColor=white)](https://aws.amazon.com)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-316192?style=flat&logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Blob-Cloud is a high-performance, cloud-native file storage and collaboration platform (Google Drive clone) designed for secure, resilient, and highly optimized operations. The system is architected as a modular monolith in **Go**, utilizing a modern **React** frontend, and designed to run entirely within the **AWS Free Tier** limits (or Cloudflare R2 for zero egress fees).

Rather than routing heavy file traffic through our Go server, this project implements a **direct-to-cloud storage pipeline** with **global block-level deduplication** and an **asynchronous event-driven worker architecture**.

---

## 🛠️ System Architecture

The following diagram illustrates how file uploads, metadata tracking, deduplication, and background jobs interact seamlessly across the stack, bypassing the Go API gateway for data transfers:

```mermaid
sequenceDiagram
    autonumber
    actor Client as React Frontend
    participant API as Go Backend (EC2)
    participant DB as PostgreSQL (RDS)
    participant SQS as AWS SQS
    participant S3 as AWS S3 / Cloudflare R2 (via CDN)
    participant Worker as Go Concurrent Workers

    %% 1. Upload Initiation & Deduplication Check
    Client->>API: POST /api/upload/initiate (filename, size, block hashes)
    API->>DB: Check global blocks table for pre-existing hashes
    DB-->>API: Return existing block IDs (deduplicated)
    
    %% 2. Presigned URLs
    API->>Client: Return Session ID + Presigned PUT URLs for missing blocks only
    
    %% 3. Direct Storage Transfer
    opt For Missing Blocks Only
        Client->>S3: Direct HTTP PUT to CDN Edge (CloudFront/Cloudflare)
        S3-->>Client: 200 OK (Block stored securely)
    end

    %% 4. Transactional Finalization
    Client->>API: POST /api/upload/complete (session_id)
    API->>API: Verify missing blocks physically exist in S3/R2
    rect rgb(240, 240, 240)
        Note over API, DB: Database Transaction
        API->>DB: Write global blocks, write files metadata, link file_blocks, set owner permissions
        DB-->>API: Commit Transaction
    end
    API-->>Client: 200 OK (Upload Complete)

    %% 5. Asynchronous Background Pipeline
    API->>SQS: Publish "Upload Completed" event payload
    loop Concurrent Long-Polling
        Worker->>SQS: SQS ReceiveMessage (20s long-poll)
    end
    SQS-->>Worker: Dispatch file metadata payload
    
    %% 6. Thumbnailing
    opt If File is Image
        Worker->>S3: Download blocks
        Worker->>Worker: Generate 200x200 PNG Thumbnail in-memory
        Worker->>S3: PutObject to /thumbnails/{fileID}.png
        Worker->>SQS: Delete SQS Message (Mark Job Success)
    end
```

---

## 🚀 Key Engineering Features

### 1. Direct-to-Cloud Uploads via CDN Presigned URLs
To protect the Go backend from network I/O and memory saturation, the server never streams file data. 
* The Go backend generates short-lived, secure S3 presigned PUT URLs.
* The client performs direct binary uploads via **AWS CloudFront** or **Cloudflare**, terminating SSL handshakes at the edge.
* Data is routed to storage over the cloud provider’s high-speed private backbone, completely shielding the application server from data transfer loads.

### 2. Global Block-Level Deduplication (Single-Instance Storage)
Files are sliced into **4MB blocks** on the client side, and each block is fingerprinted using **SHA-256**. 
* The backend maintains a unique index of block hashes.
* If multiple users upload files containing identical blocks (e.g., standard document templates or shared assets), only one copy is physically stored in S3/R2.
* Multiple user files are linked dynamically to the same physical blocks, dramatically lowering storage costs and client bandwidth.

### 3. Resumable Upload Session State Machine
To handle flaky network connections gracefully:
* Upload lifecycles are tracked via an `upload_sessions` and `session_blocks` state machine.
* If a 500MB upload is interrupted, the client polls `GET /api/upload/session/{id}`.
* The backend returns a list of blocks that have already landed securely in storage. The client skips those and resumes uploading exactly from the block where the connection failed.

### 4. Hierarchical Access Control (Permissions Sharing)
Rather than simple object storage, this system implements collaborative file sharing.
* A `permissions` table maps users and roles (`VIEWER`, `EDITOR`, `OWNER`) to files and folders.
* Access verification utilizes **Recursive Common Table Expressions (CTEs)** in PostgreSQL. If a user tries to access a deeply nested file, the database efficiently walks up the folder tree to authorize permissions dynamically without expensive application-side processing.

### 5. Event-Driven Background Worker Pool (SQS + Go Concurrency)
Post-upload tasks (like image thumbnail generation) are fully decoupled.
* Successful uploads trigger a message to **AWS SQS** using cost-efficient long-polling.
* A concurrent pool of Go workers monitors the queue, fetches images, resizes them in-memory, and writes thumbnails back to the CDN.
* The workers are wired with **graceful shutdown** listeners to ensure active jobs finish processing before the server shuts down.

---

## 💻 Tech Stack

* **Backend:** Go (Golang), `go-chi` (Router), `pgx` (PostgreSQL Driver), AWS SDK for Go v2
* **Frontend:** React, TailwindCSS, Axios
* **Database:** PostgreSQL (Transactional metadata, indexing, and CTEs)
* **Cloud Infrastructure:** AWS S3 / Cloudflare R2 (Object storage), AWS CloudFront (CDN), AWS SQS (Message Queue), AWS EC2 (Hosting)

---

## ⚙️ Local Setup & Running Guide

### Prerequisites
* Go 1.21 or higher
* Node.js 18 or higher
* Docker (for running local PostgreSQL)

### 1. Run the Database
Spin up the local PostgreSQL database in Docker:
```bash
docker run --name blobcloud-db \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=blobcloud \
  -p 5432:5432 \
  -d postgres:16-alpine
```

### 2. Configure Backend Environment
Create a `/backend/.env` file:
```env
PORT=8080
ENV=development
LOCAL_STORAGE_DIR=./tmp/storage
BASE_URL=http://localhost:8080

# DB Config
DB_DSN=postgres://postgres:postgres@localhost:5432/blobcloud?sslmode=disable

# Storage config (Change to "s3" for AWS/R2 testing)
STORAGE_PROVIDER=local 
AWS_REGION=us-east-1
AWS_S3_BUCKET=your-bucket-name
AWS_ACCESS_KEY_ID=your-access-key
AWS_SECRET_ACCESS_KEY=your-secret-key

# SQS Queue config
SQS_QUEUE_URL=https://sqs.us-east-1.amazonaws.com/your-account/your-queue
SQS_NUM_WORKERS=3
SQS_POLL_TIMEOUT_SEC=20
```

### 3. Start the Backend
```bash
cd backend
go run cmd/api/main.go
```
*Your database migrations will run programmatically on boot using Go's standard `embed` library.*

### 4. Start the Frontend
```bash
cd frontend
npm install
npm run dev
```

---

## 📈 System Design Trade-offs & Scale Limits

Designing a production-grade system means knowing where the limits of your architecture lie. During development, the following trade-offs were made:

1. **Relational Database Bottleneck:** Choosing PostgreSQL allowed for fast recursive ACL calculations (via CTEs). However, under extreme write loads (billions of files), a single database will experience index locking. Scalability would require sharding the database by `user_id` or migrating to a distributed database like CockroachDB.
2. **WebSocket Synchronization scaling:** WebSocket notifications are handled locally in-memory on the Go instance. If horizontally scaled across multiple EC2 instances behind a load balancer, instances would need to be bridged together using a **Redis Pub/Sub** backplane to synchronize notifications globally.
3. **Orphaned Block Garbage Collection:** Because we only link files to blocks once the complete API call succeeds, aborted or abandoned uploads will leave unreferenced binary objects in S3/R2. In a commercial environment, a background garbage collection cron job must run daily to compare S3 keys against active Postgres block records and prune orphaned storage data.

---

## 📺 Demo & Deployment

* 🔗 **Live URL:** *To be updated soon*
* 🎥 **Walkthrough Video:** *To be updated soon*
