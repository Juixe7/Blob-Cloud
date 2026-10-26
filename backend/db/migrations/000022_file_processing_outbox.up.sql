CREATE TABLE IF NOT EXISTS file_processing_outbox (
    file_id UUID PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_file_processing_outbox_due
    ON file_processing_outbox(next_attempt_at, created_at);
