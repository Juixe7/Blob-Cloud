-- Track if a file is encrypted and its salt
ALTER TABLE files ADD COLUMN IF NOT EXISTS is_encrypted BOOLEAN DEFAULT FALSE;
ALTER TABLE files ADD COLUMN IF NOT EXISTS encryption_salt VARCHAR(32) DEFAULT NULL;

-- Public Link Sharing Table
CREATE TABLE IF NOT EXISTS shareable_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    link_token VARCHAR(64) UNIQUE NOT NULL, -- Cryptographically secure random string
    access_tier VARCHAR(20) NOT NULL DEFAULT 'VIEWER', -- 'VIEWER' or 'EDITOR'
    password_hash VARCHAR(255) DEFAULT NULL, -- Optional password hash (Bcrypt)
    expires_at TIMESTAMP WITH TIME ZONE DEFAULT NULL, -- Optional expiration
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
