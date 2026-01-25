-- 000001_init_schema.up.sql
-- Initial schema for the Google Drive / Dropbox clone.
--
-- Tables (in dependency order):
--   users        - account records
--   files        - hierarchical file/folder tree (adjacency list via parent_id)
--   blocks       - content-addressed physical storage units (deduplicated)
--   file_blocks  - ordered many-to-many map: which blocks compose which file

-- uuid-ossp gives us uuid_generate_v4() for client-supplied IDs / fallbacks.
-- (We default to gen_random_uuid() from pgcrypto when available, but keep
-- uuid-ossp as the spec requests it for broader compatibility.)
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ---------------------------------------------------------------------------
-- users
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id            UUID           PRIMARY KEY DEFAULT uuid_generate_v4(),
    email         VARCHAR(255)   UNIQUE NOT NULL,
    password_hash VARCHAR(255)   NOT NULL,
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ---------------------------------------------------------------------------
-- files (files AND folders share this table; is_directory discriminates)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS files (
    id           UUID           PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID           NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         VARCHAR(255)   NOT NULL,
    parent_id    UUID           REFERENCES files(id) ON DELETE CASCADE,
    is_directory BOOLEAN        NOT NULL DEFAULT FALSE,
    size_bytes   BIGINT         NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_files_user_id       ON files(user_id);
CREATE INDEX IF NOT EXISTS idx_files_parent_id     ON files(parent_id);
CREATE INDEX IF NOT EXISTS idx_files_user_parent   ON files(user_id, parent_id);

-- ---------------------------------------------------------------------------
-- blocks (content-addressed storage: the S3 object key IS the sha256)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS blocks (
    id          UUID           PRIMARY KEY DEFAULT uuid_generate_v4(),
    sha256      VARCHAR(64)    UNIQUE NOT NULL,
    size_bytes  INTEGER        NOT NULL,
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ---------------------------------------------------------------------------
-- file_blocks (ordered reconstruction map)
--   Composite PK on (file_id, sequence_number) guarantees each file has at most
--   one block per position. We index block_id for reverse lookups
--   ("which files reference this block?").
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS file_blocks (
    file_id          UUID      NOT NULL REFERENCES files(id)  ON DELETE CASCADE,
    block_id         UUID      NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
    sequence_number  INTEGER   NOT NULL,
    PRIMARY KEY (file_id, sequence_number)
);

CREATE INDEX IF NOT EXISTS idx_file_blocks_block_id ON file_blocks(block_id);
