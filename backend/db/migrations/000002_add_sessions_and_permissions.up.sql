-- 000002_add_sessions_and_permissions.up.sql
-- Adds Resumable Upload Sessions (Upgrade A) and Sharing & Permissions
-- (Upgrade B) on top of the base schema from 000001.
--
-- NOTE on session_blocks.size_bytes: the Phase 3 "complete" flow inserts each
-- uploaded session block into the global `blocks` table, whose `size_bytes`
-- column is NOT NULL. The original spec omitted size_bytes from session_blocks,
-- which would make completion impossible for a newly uploaded chunk. We add it
-- here so the completion transaction has everything it needs in one place.

-- ---------------------------------------------------------------------------
-- upload_sessions (Upgrade A): a resumable, multi-chunk upload in progress.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS upload_sessions (
    id          UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID           NOT NULL,
    filename    VARCHAR(255)   NOT NULL,
    parent_id   UUID,
    total_size  BIGINT         NOT NULL,
    status      VARCHAR(50)    NOT NULL DEFAULT 'INITIATED', -- INITIATED | COMPLETED | ABORTED
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_upload_sessions_user_id ON upload_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_status  ON upload_sessions(status);

-- ---------------------------------------------------------------------------
-- session_blocks (Upgrade A): the per-chunk status of a session.
--   - is_uploaded flips true once the client has PUT the chunk to storage.
--   - size_bytes carries the chunk size so completion can populate `blocks`.
-- Composite PK guarantees one block per sequence position per session.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS session_blocks (
    session_id      UUID      NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    block_hash      VARCHAR(64) NOT NULL,
    sequence_number INTEGER   NOT NULL,
    size_bytes      INTEGER   NOT NULL,
    is_uploaded     BOOLEAN   NOT NULL DEFAULT FALSE,
    PRIMARY KEY (session_id, sequence_number)
);

CREATE INDEX IF NOT EXISTS idx_session_blocks_hash ON session_blocks(block_hash);

-- ---------------------------------------------------------------------------
-- permissions (Upgrade B): who may do what to a file/folder. A grantee is
-- identified by email (kept loosely coupled from users.id so a share can be
-- issued before the recipient has signed in). UNIQUE(file_id, grantee_email)
-- prevents duplicate grants.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS permissions (
    id            UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id       UUID           NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    grantee_email VARCHAR(255)   NOT NULL,
    role          VARCHAR(20)    NOT NULL, -- VIEWER | EDITOR | OWNER
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (file_id, grantee_email)
);

CREATE INDEX IF NOT EXISTS idx_permissions_file_id  ON permissions(file_id);
CREATE INDEX IF NOT EXISTS idx_permissions_grantee  ON permissions(grantee_email);

-- Bump updated_at whenever an upload_session changes status. Centralising this
-- in the DB keeps the row's updated_at honest regardless of which client wrote.
CREATE OR REPLACE FUNCTION touch_upload_session_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_touch_upload_session ON upload_sessions;
CREATE TRIGGER trg_touch_upload_session
BEFORE UPDATE ON upload_sessions
FOR EACH ROW
EXECUTE FUNCTION touch_upload_session_updated_at();
