# Blob-Cloud

Blob-Cloud is a browser-based file storage application with a Go API, PostgreSQL metadata, and local or S3-compatible object storage. The repository includes a React/TypeScript frontend, background file processing, sharing, and synchronization features.

> **Project status:** core upload, sharing/ACL, sync, processing, and local-storage garbage-collection paths have been implemented and exercised with PostgreSQL-backed tests. This is a portfolio project, not a claim of production security certification. See the limitations below for the areas that remain unverified or incomplete.

## Features

- Resumable uploads split into content-defined chunks, with SHA-256 validation and content-addressed block storage.
- Deduplication through reusable content-addressed blocks.
- File and folder operations, trash/restore, version history, and streaming downloads with HTTP range support.
- Collaborator roles, invitation workflows, public links, and shortcuts.
- Delta synchronization backed by a PostgreSQL journal, with WebSocket notifications as update hints.
- Asynchronous file processing with a PostgreSQL outbox and antivirus scanning for plaintext uploads.
- Browser-side AES-GCM encryption and passphrase-based key derivation.

## Architecture

```mermaid
flowchart LR
  Browser[React + TypeScript] -->|REST, WebSocket, staged upload| API[Go API]
  Browser -->|chunking and optional encryption| Worker[Browser Web Worker]
  API --> Services[Upload, file, sharing, sync services]
  Services --> DB[(PostgreSQL)]
  Services --> Storage[Local or S3-compatible storage]
  API --> Outbox[(Processing outbox)]
  Outbox --> Scanner[Worker + antivirus scanner]
  API --> Redis[(Optional Redis: rate limits and notifications)]
```

The Go backend is organized into HTTP handlers, services, domain interfaces, PostgreSQL repositories, storage providers, and background workers. Upload completion validates staged bytes before associating blocks with file metadata. Plaintext files remain unavailable until a successful scan; encrypted ciphertext cannot be inspected by the server and is an explicit antivirus limitation.

The garbage collector uses storage object age as a grace period and PostgreSQL coordination to recheck block references while holding a per-hash lock through deletion. Upload-session creation uses the same lock, and inactive sessions are aborted after 24 hours without upload progress. The grace period supplements that coordination; it is not the sole data-safety mechanism.

## Technology

- **Backend:** Go version declared in `backend/go.mod`, Chi, PostgreSQL, and optional Redis.
- **Frontend:** React, TypeScript, Vite, Web Workers, and WebCrypto.
- **Storage:** local filesystem or S3-compatible object storage.
- **Background work:** worker process, queue integration, transactional processing outbox, and ClamAV scanning for plaintext files.

## Local development

Prerequisites: Go version declared in `backend/go.mod`, Node.js/npm, Docker Desktop, and Docker Compose.

1. Start local dependencies from the repository root:

   ```sh
   docker compose up -d postgres redis clamav
   ```

2. Configure the backend environment. At minimum, set `DB_DSN` to the Compose PostgreSQL database (`postgres://postgres:password@localhost:5432/blobcloud?sslmode=disable`) and set a development-only `JWT_SECRET` of at least 32 characters. Storage defaults to local; configure S3 variables only when using an S3-compatible backend.
3. Start the API from a terminal:

   ```sh
   cd backend
   go run ./cmd/api
   ```

4. Start the frontend in another terminal:

   ```sh
   cd frontend
   npm install
   npm run dev
   ```

A background worker can be run with `go run ./cmd/worker` when its database, scanner, and queue configuration are available. Never use development secrets or local Compose credentials in a public deployment. Configure production credentials through the deployment environment or a secret manager.

## Verification

From `backend/`:

```sh
go test -race ./...
go vet ./...
go build ./...
```

PostgreSQL integration tests use `DB_DSN`; provide a disposable migrated database with the required `vector` extension. Some integration tests skip when PostgreSQL is unreachable, so confirm the integration packages actually ran. The ACL matrix and GC/upload coordination tests have been exercised against disposable PostgreSQL. The complete Go race suite, `go vet`, and `go build` have passed in that setup.

From `frontend/`:

```sh
npm run lint
npm run build
```

There is currently no frontend test script. S3 integration tests require explicit test credentials and are separate from the default Go test command.

## Current limitations

- **ACL coverage is strong but not a security certification.** Role precedence, nested resources, shortcut authorization, link revocation, collaborator-roster disclosure, and selected mutation races have PostgreSQL-backed regressions. This is not a penetration test or proof that every route has an individually enumerated adversarial test.
- **Journal retention is not implemented.** Journal rows are not pruned, so the table grows over time; cursors do not currently expire.
- **Encryption needs a documented threat model and independent review.** The current browser implementation derives deterministic salt/IV material in some upload paths. This can reveal equality patterns; do not describe the design as zero-knowledge or as providing randomized unique IVs.
- **Encrypted uploads are not virus-scanned server-side.** The server sees ciphertext, not plaintext.
- **GC has PostgreSQL/local-filesystem evidence, not live S3 evidence.** Multipart-upload abort/cleanup and S3-specific behavior have not been verified. Do not treat local tests as proof of AWS or S3-compatible provider behavior.
- **Inactive upload sessions expire after 24 hours without recorded block-upload progress.** GC marks them aborted and later removes eligible staging data; clients must start a new session after expiration.

## Repository layout

- `backend/` — Go API, services, repositories, migrations, and workers.
- `frontend/` — React application and browser workers.
- `deploy/` — monitoring and deployment manifests.

## License

No license file is currently present. Add a license before describing this repository as open source.
