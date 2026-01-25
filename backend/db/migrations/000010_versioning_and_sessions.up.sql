-- 000010_versioning_and_sessions.up.sql

-- Drop the old table since we are replacing it entirely with strict constraints
DROP TABLE IF EXISTS user_sessions;

-- Track physical device sessions mapped to refresh tokens
CREATE TABLE IF NOT EXISTS user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_id VARCHAR(255) UNIQUE NOT NULL, -- Tracks the JTI or hashed token
    ip_address VARCHAR(45) NOT NULL,
    user_agent VARCHAR(512) NOT NULL,
    device_name VARCHAR(255) NOT NULL, -- e.g., "Chrome on macOS"
    is_active BOOLEAN DEFAULT TRUE,
    last_active_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_refresh_token_id ON user_sessions(refresh_token_id);

-- Track historical file versions (excluding current active version in 'files')
CREATE TABLE IF NOT EXISTS file_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    version_number INT NOT NULL,
    size_bytes BIGINT NOT NULL,
    chunk_hashes TEXT[] NOT NULL, -- The specific block blueprint for this revision
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_file_versions_file_id ON file_versions(file_id);
