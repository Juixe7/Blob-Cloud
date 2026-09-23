# 02. Backend Engineering & API Design

## 1. Backend Architecture & Design Patterns

The backend is built in **Go (Golang 1.21+)** using a **Clean Architecture / Layered Monolith** pattern. The codebase strictly separates HTTP transport concerns, core business logic, domain entities, and data persistence drivers.

### Codebase Organization (`backend/internal/`)

```
backend/internal/
├── domain/            # Domain models and entities (User, File, Block, Permission)
├── transport/http/    # HTTP handlers, Chi router, middleware, DTO decoding
├── service/           # Core application business logic (Upload, File, Zip processing)
├── repository/        # Data access interfaces and PostgreSQL implementations (`pgx`)
├── storage/           # Physical storage abstraction (Local FS, AWS S3 / Cloudflare R2)
├── queue/             # AWS S3 / SQS producer & consumer worker pipeline
├── sync/              # Real-Time WebSocket pub/sub notification hub
├── auth/              # JWT token generation, parsing, password hashing (bcrypt)
└── database/          # Database connection pool setup & boot-time migrations
```

---

## 2. Complete REST API Specification

### Authentication & Session Endpoints (`/api/auth`)

| Method | Endpoint | Description | Request Body / Query | Success Response |
| :--- | :--- | :--- | :--- | :--- |
| `POST` | `/api/auth/register` | Register new user account | `{ "email", "password", "name" }` | `201 Created`: `{ "user": {...}, "access_token" }` |
| `POST` | `/api/auth/login` | Authenticate user & issue tokens | `{ "email", "password" }` | `200 OK`: `{ "user", "access_token" }` + Cookie |
| `POST` | `/api/auth/refresh` | Rotate access token via refresh cookie | *(HTTP-Only Refresh Cookie)* | `200 OK`: `{ "access_token" }` |
| `GET` | `/api/auth/verify` | Verify email token | `?token=XYZ` | `200 OK`: `{ "message": "Email verified" }` |
| `POST` | `/api/auth/forgot-password`| Trigger password recovery email | `{ "email" }` | `200 OK`: `{ "message": "Recovery sent" }` |
| `POST` | `/api/auth/reset-password` | Reset password using token | `{ "token", "new_password" }` | `200 OK`: `{ "message": "Password updated" }` |

---

### File & Folder Management Endpoints (`/api/files`)

| Method | Endpoint | Description | Request Body / Query | Success Response |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/api/files` | List user files & folders | `?folder_id=UUID&search=text` | `200 OK`: `{ "files": [...] }` |
| `POST` | `/api/files/folder` | Create a new directory folder | `{ "name", "parent_id" }` | `201 Created`: `{ "folder": {...} }` |
| `PATCH` | `/api/files/:id/rename`| Rename file or folder | `{ "name": "new_name.png" }` | `200 OK`: `{ "file": {...} }` |
| `PATCH` | `/api/files/move` | Move multiple files to destination | `{ "file_ids": [...], "target_parent_id" }` | `200 OK`: `{ "moved_count": 3 }` |
| `DELETE`| `/api/files/:id` | Soft-delete file to trash | - | `200 OK`: `{ "message": "Moved to trash" }` |
| `DELETE`| `/api/files/:id/permanent`| Permanently purge file | - | `200 OK`: `{ "message": "File purged" }` |
| `GET` | `/api/files/:id/download` | Download single file (S3 presigned redirect)| - | `302 Found` -> `S3 Presigned GET URL` |
| `GET` | `/api/files/download-zip`| Stream entire folder as ZIP | `?folder_id=UUID` | `200 OK` (`Content-Type: application/zip`) |

---

### Deduplicated & Resumable Upload Endpoints (`/api/upload`)

#### 1. Initiate Upload: `POST /api/upload/initiate`
**Request Body**:
```json
{
  "file_name": "project_archive.zip",
  "total_size": 12582912,
  "parent_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "block_hashes": [
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "f2ca1bb6c7e907d06dafe4687e579fce76b37e4e93b7605022da52e6ccc26fd2",
    "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
  ]
}
```

**Response (`200 OK`)**:
```json
{
  "session_id": "7a3e819b-c40d-4001-9f12-000000000000",
  "deduplicated": false,
  "missing_blocks": [
    {
      "block_index": 1,
      "hash": "f2ca1bb6c7e907d06dafe4687e579fce76b37e4e93b7605022da52e6ccc26fd2",
      "presigned_url": "https://blob-bucket.s3.amazonaws.com/blocks/f2ca1bb6...?X-Amz-Signature=..."
    }
  ]
}
```
*(Notice: Block 0 and Block 2 already exist in the DB, so only Block 1 receives a presigned upload URL!)*

---

#### 2. Complete Upload: `POST /api/upload/complete`
**Request Body**: `{ "session_id": "7a3e819b-c40d-4001-9f12-000000000000" }`  
**Response (`200 OK`)**: `{ "file_id": "c56a4180-65aa-42ec-a945-5fd21dec0538", "status": "COMPLETED" }`

---

## 3. Advanced Engineering Implementations

### A. Real-Time WebSockets Synchronization Hub ([hub.go](file:///c:/Users/Asus/Desktop/z/backend/internal/sync/hub.go))
- **Pattern**: Central Thread-Safe Event Hub managing active WebSocket connections per `user_id`.
- **Implementation**: Uses Go channels (`chan []byte`) and `sync.RWMutex` to broadcast real-time events (e.g. `FILE_CREATED`, `FILE_DELETED`, `PROCESSING_COMPLETE`) across multiple browser sessions of the logged-in user.

### B. Zero-Buffer On-The-Fly Streaming Zip Archival ([zip_service.go](file:///c:/Users/Asus/Desktop/z/backend/internal/service/zip_service.go))
- **Problem**: Allowing users to download entire folders containing 500MB+ of files normally requires stitching files into a temp `.zip` file on disk before sending.
- **Solution**: Implements Go's `archive/zip` writer wrapping the `http.ResponseWriter`. Files are read chunk-by-chunk from S3 / Local storage and streamed directly into the zip output stream over HTTP response chunked transfer encoding (`Transfer-Encoding: chunked`), resulting in **O(1) memory usage**.

---

## 4. Trade-Offs & Architectural Alternatives

| Feature | Choice in Blob-Cloud | Alternative Option | Rationale |
| :--- | :--- | :--- | :--- |
| **HTTP Router** | **`go-chi/chi`** | `Gin`, `Fiber`, Standard `net/http` | `chi` is lightweight, 100% compatible with standard `http.Handler` interface, and has zero external dependencies. |
| **Auth Token Scheme** | **Dual Token (Access JWT + DB Refresh Token Cookie)** | Pure Stateless JWT | Stateless JWT cannot be revoked before expiration. Storing refresh session IDs in DB allows immediate remote session revocation. |
| **API Protocol** | **REST JSON API** | gRPC / WebSockets for all | REST over HTTP/2 provides native browser compatibility, standard CORS headers, and simple presigned URL integration. |

---

## 5. Interviewer Deep-Dive Q&A

### Q1: Why did you choose `go-chi` instead of `Gin` or `Fiber`?
**Answer**:
`Fiber` uses `fasthttp`, which breaks standard `net/http` compatibility and ecosystem middleware. `Gin` adds unnecessary allocations and custom contexts. `go-chi` provides standard Go `http.HandlerFunc` signatures, idiomatic context propagation (`r.Context()`), fast radix-tree route matching, and zero third-party lock-in.

### Q2: How do you handle HTTP request cancellations in Go handlers?
**Answer**:
Every handler inspects `r.Context()`. When database queries or S3 downloads execute, we pass `r.Context()` to `pgx` or AWS SDK calls (e.g. `s3Client.GetObject(ctx, input)`). If a user closes their browser tab mid-request, Go automatically triggers `ctx.Done()`, cancelling downstream SQL queries and network calls instantly, preventing resource leaks.
