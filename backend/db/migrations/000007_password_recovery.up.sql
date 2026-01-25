-- 000007_password_recovery.up.sql
-- Table tracking password reset requests and reset token lifecycle.

CREATE TABLE IF NOT EXISTS password_resets (
    id         UUID                     PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID                     NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      VARCHAR(64) UNIQUE       NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    used       BOOLEAN                  DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_password_resets_token ON password_resets(token);
CREATE INDEX IF NOT EXISTS idx_password_resets_user_id ON password_resets(user_id);
