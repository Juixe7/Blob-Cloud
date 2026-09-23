CREATE TABLE IF NOT EXISTS journal_entries (
    cursor BIGSERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    file_id UUID NOT NULL,
    action VARCHAR(32) NOT NULL,
    parent_id UUID,
    name VARCHAR(255) NOT NULL,
    is_directory BOOLEAN NOT NULL DEFAULT FALSE,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    mime_type VARCHAR(127) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    thumbnail_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_journal_user_cursor ON journal_entries(user_id, cursor);
CREATE INDEX IF NOT EXISTS idx_journal_file_id ON journal_entries(file_id);
