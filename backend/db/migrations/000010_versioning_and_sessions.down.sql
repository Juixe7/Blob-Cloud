-- 000010_versioning_and_sessions.down.sql

DROP TABLE IF EXISTS file_versions;
DROP TABLE IF EXISTS user_sessions;

-- Recreate old user_sessions table if we downgrade (for safety)
CREATE TABLE IF NOT EXISTS user_sessions (
    id            UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID           NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_info   VARCHAR(255)   NOT NULL,
    ip_address    VARCHAR(45)    NOT NULL,
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at  TIMESTAMPTZ    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);
