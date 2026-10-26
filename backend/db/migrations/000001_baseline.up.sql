-- Blob-Cloud's canonical database schema.
--
-- This baseline intentionally resets the app-owned schema. The project owner
-- approved discarding deployed application data before this schema is applied.
-- Keep this file immutable after release; future changes must be additive
-- migrations with new version numbers.

-- Reset only tables and helper objects owned by this application. Do not drop
-- the PostgreSQL database or shared extensions.
DROP TABLE IF EXISTS file_processing_outbox, user_blocks, journal_entries,
    audit_logs, file_chunks, user_file_views, shareable_links,
    file_version_blocks, file_versions, permissions, session_blocks,
    upload_sessions, user_sessions, password_resets, verification_codes,
    file_blocks, blocks, files, users CASCADE;
DROP FUNCTION IF EXISTS touch_upload_session_updated_at();

-- This is a new schema lineage, so discard the old filename-based migration
-- records. RunMigrations records this baseline after this transaction commits.
DELETE FROM schema_migrations;

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uq_users_email_ci ON users(LOWER(email));

-- Files and folders use an adjacency list. `path` is a transactionally
-- maintained materialized path used for indexed subtree operations.
CREATE TABLE files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    parent_id UUID REFERENCES files(id) ON DELETE CASCADE,
    path TEXT NOT NULL DEFAULT '/',
    is_directory BOOLEAN NOT NULL DEFAULT FALSE,
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    target_id UUID REFERENCES files(id) ON DELETE SET NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream',
    is_encrypted BOOLEAN NOT NULL DEFAULT FALSE,
    encryption_salt VARCHAR(32),
    tags VARCHAR(512),
    summary TEXT,
    -- Gemini embeddings are 768 dimensional. Keep file and chunk vectors
    -- consistent so semantic metadata writes do not fail dimension checks.
    embedding VECTOR(768),
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('PROCESSING', 'ACTIVE', 'QUARANTINED', 'TRASHED'))
);

CREATE INDEX idx_files_user_parent ON files(user_id, parent_id);
CREATE INDEX idx_files_user_deleted ON files(user_id, deleted_at);
CREATE INDEX idx_files_parent_id ON files(parent_id);
CREATE INDEX idx_files_user_path_pattern ON files(user_id, path text_pattern_ops);
CREATE UNIQUE INDEX uq_files_root_active
    ON files(user_id, name) WHERE parent_id IS NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_files_folder_active
    ON files(user_id, parent_id, name) WHERE parent_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX files_embedding_hnsw_idx
    ON files USING hnsw (embedding vector_cosine_ops);

CREATE TABLE blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sha256 CHAR(64) NOT NULL UNIQUE,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE file_blocks (
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    block_id UUID NOT NULL REFERENCES blocks(id) ON DELETE RESTRICT,
    sequence_number INTEGER NOT NULL CHECK (sequence_number >= 0),
    PRIMARY KEY (file_id, sequence_number)
);
CREATE INDEX idx_file_blocks_block_id ON file_blocks(block_id);

CREATE TABLE upload_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    parent_id UUID REFERENCES files(id) ON DELETE SET NULL,
    total_size BIGINT NOT NULL CHECK (total_size >= 0),
    status VARCHAR(20) NOT NULL DEFAULT 'INITIATED'
        CHECK (status IN ('INITIATED', 'COMPLETED', 'ABORTED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_upload_sessions_user_status ON upload_sessions(user_id, status);
CREATE INDEX idx_upload_sessions_stale ON upload_sessions(updated_at)
    WHERE status = 'INITIATED';

CREATE TABLE session_blocks (
    session_id UUID NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    block_hash CHAR(64) NOT NULL,
    block_md5 VARCHAR(32),
    sequence_number INTEGER NOT NULL CHECK (sequence_number >= 0),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    is_uploaded BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (session_id, sequence_number)
);
CREATE INDEX idx_session_blocks_hash ON session_blocks(block_hash);

CREATE FUNCTION touch_upload_session_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_touch_upload_session
BEFORE UPDATE ON upload_sessions
FOR EACH ROW EXECUTE FUNCTION touch_upload_session_updated_at();

CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    grantee_email VARCHAR(255) NOT NULL,
    role VARCHAR(20) NOT NULL CHECK (role IN ('OWNER', 'EDITOR', 'VIEWER')),
    status VARCHAR(20) NOT NULL DEFAULT 'ACCEPTED'
        CHECK (status IN ('PENDING', 'ACCEPTED', 'DECLINED', 'EXPIRED')),
    message TEXT,
    expires_at TIMESTAMPTZ,
    responded_at TIMESTAMPTZ,
    invited_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_permissions_file_id ON permissions(file_id);
CREATE INDEX idx_permissions_grantee ON permissions(grantee_email);
CREATE INDEX idx_permissions_status_grantee ON permissions(grantee_email, status);
CREATE INDEX idx_permissions_pending_expiry ON permissions(expires_at)
    WHERE status = 'PENDING';
CREATE UNIQUE INDEX uq_permissions_file_grantee_ci
    ON permissions(file_id, LOWER(grantee_email));
CREATE INDEX idx_permissions_lower_grantee_status
    ON permissions(LOWER(grantee_email), status);

CREATE TABLE user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_id VARCHAR(255) NOT NULL UNIQUE,
    ip_address VARCHAR(45) NOT NULL DEFAULT '',
    user_agent VARCHAR(512) NOT NULL DEFAULT '',
    device_name VARCHAR(255) NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_active_expiry ON user_sessions(user_id, expires_at)
    WHERE is_active;

CREATE TABLE verification_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code VARCHAR(6) NOT NULL CHECK (code ~ '^[0-9]{6}$'),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_verification_codes_user_expiry
    ON verification_codes(user_id, expires_at);

CREATE TABLE password_resets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash CHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_password_resets_user_id ON password_resets(user_id);

CREATE TABLE file_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL CHECK (version_number > 0),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (file_id, version_number)
);
CREATE INDEX idx_file_versions_file_id ON file_versions(file_id, version_number DESC);

-- A normalized ordered relation lets the database enforce block references and
-- lets GC find historical references with indexed joins instead of array scans.
CREATE TABLE file_version_blocks (
    version_id UUID NOT NULL REFERENCES file_versions(id) ON DELETE CASCADE,
    sequence_number INTEGER NOT NULL CHECK (sequence_number >= 0),
    block_id UUID NOT NULL REFERENCES blocks(id) ON DELETE RESTRICT,
    PRIMARY KEY (version_id, sequence_number)
);
CREATE INDEX idx_file_version_blocks_block_id ON file_version_blocks(block_id);

CREATE TABLE shareable_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    link_token_hash CHAR(64) NOT NULL UNIQUE,
    access_tier VARCHAR(20) NOT NULL DEFAULT 'VIEWER'
        CHECK (access_tier IN ('VIEWER', 'EDITOR')),
    password_hash VARCHAR(255),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_shareable_links_file_id ON shareable_links(file_id);

CREATE TABLE user_file_views (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    viewed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, file_id)
);
CREATE INDEX idx_user_file_views_user_viewed_at
    ON user_file_views(user_id, viewed_at DESC);

CREATE TABLE file_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL CHECK (chunk_index >= 0),
    chunk_text TEXT NOT NULL,
    embedding VECTOR(768) NOT NULL,
    UNIQUE (file_id, chunk_index)
);
CREATE INDEX idx_file_chunks_embedding
    ON file_chunks USING hnsw (embedding vector_cosine_ops);

CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(64) NOT NULL,
    resource_type VARCHAR(32) NOT NULL,
    resource_id UUID NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    client_ip VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_audit_resource
    ON audit_logs(resource_type, resource_id, created_at DESC);
CREATE INDEX idx_audit_user ON audit_logs(user_id, created_at DESC);

CREATE TABLE journal_entries (
    cursor BIGSERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    file_id UUID NOT NULL,
    action VARCHAR(32) NOT NULL,
    parent_id UUID,
    name VARCHAR(255) NOT NULL,
    is_directory BOOLEAN NOT NULL DEFAULT FALSE,
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    mime_type VARCHAR(127) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    thumbnail_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_journal_user_cursor ON journal_entries(user_id, cursor);
CREATE INDEX idx_journal_file_id ON journal_entries(file_id);

CREATE TABLE user_blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_email VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE user_blocks ADD CONSTRAINT uq_user_blocks_user_email
    UNIQUE (user_id, blocked_email);

CREATE TABLE file_processing_outbox (
    file_id UUID PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_file_processing_outbox_due
    ON file_processing_outbox(next_attempt_at, created_at);
