-- 000019_add_share_invitations.up.sql
-- Adds opt-in Share Invitation states (PENDING, ACCEPTED, DECLINED, EXPIRED)
-- and sender blocking table to protect against unsolicited spam sharing.

ALTER TABLE permissions 
    ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'ACCEPTED',
    ADD COLUMN IF NOT EXISTS message TEXT,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ DEFAULT (CURRENT_TIMESTAMP + INTERVAL '7 days'),
    ADD COLUMN IF NOT EXISTS responded_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS invited_by UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_permissions_status_grantee ON permissions(grantee_email, status);
CREATE INDEX IF NOT EXISTS idx_permissions_expires_at ON permissions(expires_at) WHERE status = 'PENDING';

-- user_blocks: tracks blocked emails per user to prevent unwanted shares
CREATE TABLE IF NOT EXISTS user_blocks (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_email VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, blocked_email)
);

CREATE INDEX IF NOT EXISTS idx_user_blocks_user_email ON user_blocks(user_id, blocked_email);
