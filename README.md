# Blob-Cloud

Blob-Cloud is a browser-based file storage application with a Go API, PostgreSQL metadata, and local or S3-compatible object storage. The repository includes a React/TypeScript frontend, background file processing, sharing, and synchronization features.

> **Project status:** actively being audited and improved. ACL consistency work is not complete; PostgreSQL-backed role and race tests must run against a live database before those guarantees are considered verified. Review the limitations below before relying on security or production-readiness claims.

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

PostgreSQL integration tests use `DB_DSN`; provide a disposable migrated test database to execute them. Some integration tests skip when PostgreSQL is unreachable, so a green command alone does not prove those tests ran. Current Phase 4 authorization matrix and race regressions require live PostgreSQL verification.

From `frontend/`:

```sh
npm run lint
npm run build
```

There is currently no frontend test script. S3 integration tests require explicit test credentials and are separate from the default Go test command.

## Current limitations

- **ACL audit is ongoing.** Effective-role precedence, shortcut authorization, link revocation, transaction-time authorization, and several disclosure paths have been addressed, but the full nested-resource role matrix and revocation/read/write race suite still need live PostgreSQL execution.
- **Journal retention is not implemented.** Journal rows are not pruned, so the table grows over time; cursors do not currently expire.
- **Encryption needs a documented threat model and independent review.** The current browser implementation derives deterministic salt/IV material in some upload paths. This can reveal equality patterns; do not describe the design as zero-knowledge or as providing randomized unique IVs.
- **Encrypted uploads are not virus-scanned server-side.** The server sees ciphertext, not plaintext.
- **Garbage-collection safety and S3 behavior require their own production-like integration evidence.** Do not treat local tests as proof of AWS behavior.

## Repository layout

- `backend/` — Go API, services, repositories, migrations, and workers.
- `frontend/` — React application and browser workers.
- `deploy/` — monitoring and deployment manifests.

## License

No license file is currently present. Add a license before describing this repository as open source.
