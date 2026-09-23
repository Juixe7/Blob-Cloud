# Phase 3 Walkthrough: Zero-Trust Staging & Promotion Pipeline

## Overview
In Phase 3, Blob-Cloud has eliminated the critical **Content-Addressed Storage (CAS) Poisoning** vulnerability. In typical naive storage implementations, presigned upload URLs write directly to `blocks/<claimed_sha256>`. If a malicious client uploads arbitrary bytes claiming a legitimate hash, future uploads of legitimate files deduplicate against the poisoned block, compromising data integrity for all users.

Phase 3 implements a **Zero-Trust Staging & Promotion Pipeline**:
1. **Quarantine Namespace**: Presigned upload URLs target an isolated staging prefix (`staging/{session_id}/{index}`). Unverified client data is forbidden from landing in `blocks/`.
2. **Cryptographic Integrity Verification**: Upon completion (`POST /api/upload/complete`), the server cryptographically streams and computes the true SHA-256 hash of the staged bytes.
3. **CAS Poisoning Detection & Rejection**: Any hash or size discrepancy immediately aborts the transaction, deletes the poisoned staging object, emits a security alert, and returns an HTTP 400 error.
4. **Zero-Egress Server-Side Promotion**: Verified authentic blocks are promoted to `blocks/<hash>`:
   - For AWS S3 / Cloudflare R2: Uses `s3.CopyObject` inside AWS's data plane (zero egress bandwidth cost) followed by staging deletion.
   - For Local Storage: Uses atomic filesystem move (`os.Rename`).
5. **Safe Deduplication**: If a block was already committed to CAS (e.g. concurrent upload by another client), the redundant staging object is safely deleted without re-copying.
6. **Orphaned Staging Garbage Collection**: The GC daemon now implements `StagingLister` to sweep abandoned staging objects older than the expiration window.

---

## Architecture & Data Flow

```mermaid
sequenceDiagram
    autonumber
    actor Client as Browser Client
    participant API as API Server
    participant Storage as Object Storage (S3 / Local)
    participant DB as PostgreSQL (files, blocks)

    Client->>API: POST /api/upload/initiate (declares chunks)
    API->>Storage: GenerateStagingUploadURL("staging/{session_id}/{index}")
    API-->>Client: 200 OK with staging upload URLs
    Client->>Storage: Direct PUT binary bytes to staging URL

    Client->>API: POST /api/upload/complete { session_id }
    activate API
    API->>Storage: HeadObject("blocks/{claimed_hash}")
    alt Already Exists in CAS (Concurrent Dedup Hit)
        API->>Storage: DeleteObject("staging/{session_id}/{index}")
    else New Block (Verification Required)
        API->>Storage: GetObject("staging/{session_id}/{index}")
        Storage-->>API: Stream bytes
        API->>API: hasher = sha256.New(); io.Copy(hasher, stream)
        alt Computed SHA-256 != Claimed SHA-256
            API->>Storage: DeleteObject("staging/{session_id}/{index}")
            API-->>Client: 400 Bad Request ("checksum mismatch: CAS poisoning rejected")
        else Hash Matches (Genuine)
            API->>Storage: PromoteObject("staging/...", "blocks/{hash}")
            Note over API,Storage: S3 Server-Side Copy (Zero Egress Cost)
            API->>Storage: DeleteObject("staging/...")
        end
    end
    API->>DB: Atomically link blocks to file record
    API-->>Client: 200 OK (file created)
    deactivate API
```

---

## What Was Built & Modified

| Component | Path | Description |
| :--- | :--- | :--- |
| **Domain Interface** | [`internal/domain/storage.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/domain/storage.go) | Added `GenerateStagingUploadURL(ctx, stagingKey, expires)` and `PromoteObject(ctx, srcKey, destKey)` contracts to `StorageProvider`. |
| **Local Storage Driver** | [`internal/storage/local.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/storage/local.go) | Implemented `GenerateStagingUploadURL`, atomic rename / copy `PromoteObject`, and `ListStagingKeys` for GC. |
| **S3 Storage Driver** | [`internal/storage/s3.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/storage/s3.go) | Implemented presigned staging URLs, zero-egress `s3.CopyObject` promotion, and `ListStagingKeys`. |
| **HTTP Handlers & Router** | [`handlers.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/transport/http/handlers.go), [`router.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/transport/http/router.go) | Mounted `PUT /local-storage/staging/{session_id}/{index}` with path traversal safeguards. |
| **Service Layer Pipeline** | [`internal/service/upload_service.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/service/upload_service.go) | Refactored `Initiate` to generate staging URLs; implemented Zero-Trust verification, CAS poisoning detection, and safe promotion in `Complete`. |
| **Garbage Collection** | [`internal/gc/collector.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/gc/collector.go) | Added `StagingLister` interface; sweeps abandoned staging objects from cancelled/interrupted uploads. |
| **Automated Tests** | [`storage/local_test.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/storage/local_test.go), [`service/upload_zero_trust_test.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/service/upload_zero_trust_test.go), [`gc/collector_test.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/gc/collector_test.go) | Unit tests verifying staging URL generation, promotion cleanup, CAS poisoning rejection, and staging GC sweeps. |

---

## Verification & Build Results

### 1. Backend Verification
```powershell
cd backend
go build ./...
# Exit code: 0

go test ./...
# Result: 100% PASS across all packages
# ok  go-drive-clone/cmd/worker-lambda
# ok  go-drive-clone/internal/audit
# ok  go-drive-clone/internal/auth
# ok  go-drive-clone/internal/gc
# ok  go-drive-clone/internal/queue
# ok  go-drive-clone/internal/ratelimit
# ok  go-drive-clone/internal/repository/postgres
# ok  go-drive-clone/internal/service
# ok  go-drive-clone/internal/storage
# ok  go-drive-clone/internal/sync
# ok  go-drive-clone/internal/transport/http
```

### 2. Frontend Verification
```powershell
cd frontend
npx tsc -b
# Exit code: 0

npm run build
# Exit code: 0 (Vite built cleanly in 17.83s, 436 modules transformed)
```
