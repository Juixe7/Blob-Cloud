# Blob-Cloud v2.0 Enterprise Architectural Overhaul: 5-Phase Master Plan

This document preserves the comprehensive architecture roadmap transforming Blob-Cloud into a **FAANG-caliber, production-grade distributed cloud storage system**.

---

## The 5 Phases

| Phase | Title | Core Objective | Key Deliverables | Status |
| :--- | :--- | :--- | :--- | :--- |
| **Phase 1** | **Distributed Service Decoupling & Serverless Compute** | Decouple Control Plane (API) from Compute Plane (Worker) via SQS. | `cmd/worker`, `cmd/worker-lambda`, multi-stage Dockerfile, `docker-compose.yml`, `deploy/k8s/` with KEDA autoscaling. | **COMPLETED** |
| **Phase 2** | **Dropbox-Grade Delta Sync Engine** | Replace full-page reload WebSockets with an append-only journal cursor feed (`/api/sync/delta`). | PostgreSQL `journal_entries` table, Delta Sync API, atomic state patching in React frontend. | **COMPLETED** |
| **Phase 3** | **Zero-Trust Staging & Promotion Pipeline** | Prevent unverified client data from reaching the immutable CAS store (`blocks/`). | Dual-bucket architecture (`staging` vs `clean`), worker hash verification, S3 Server-Side Copy. | **COMPLETED** |
| **Phase 4** | **Materialized Path Hierarchy for $O(1)$ Folder Moves** | Eliminate table-locking recursive CTEs for directory tree operations. | Materialized `path` column, indexed prefix subtree updates in 5ms. | **COMPLETED** |
| **Phase 5** | **FastCDC Content-Defined Chunking** | Prevent boundary-shift deduplication collapse across file edits. | FastCDC rolling Gear hash Web Worker, shift-resistant multi-version deduplication. | **COMPLETED** |
