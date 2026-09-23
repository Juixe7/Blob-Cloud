-- 000009_audit_log.up.sql
-- Immutable append-only audit log table for Blob-Cloud.
--
-- Every significant user action (upload complete, file share, file delete)
-- writes one row here. The table is intentionally append-only — no UPDATE or
-- DELETE is ever issued by the application. This gives a tamper-evident trail
-- useful for compliance audits and debugging.
--
-- resource_type discriminates the kind of entity referenced by resource_id:
--   'file'    — a files.id UUID
--   'session' — an upload_sessions.id UUID (future)
--
-- metadata is JSONB so action-specific context (filename, grantee, etc.) can
-- be stored without schema changes.

CREATE TABLE IF NOT EXISTS audit_logs (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action        VARCHAR(64)  NOT NULL,       -- e.g. 'FILE_UPLOADED', 'FILE_SHARED'
    resource_type VARCHAR(32)  NOT NULL,       -- 'file', 'session', …
    resource_id   UUID         NOT NULL,       -- FK into the relevant table
    metadata      JSONB        NOT NULL DEFAULT '{}',
    client_ip     VARCHAR(64)  NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Query pattern: "show me the history of file X"
CREATE INDEX IF NOT EXISTS idx_audit_resource
    ON audit_logs(resource_type, resource_id, created_at DESC);

-- Query pattern: "show me all actions by user Y"
CREATE INDEX IF NOT EXISTS idx_audit_user
    ON audit_logs(user_id, created_at DESC);
