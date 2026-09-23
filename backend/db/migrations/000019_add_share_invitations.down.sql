-- 000019_add_share_invitations.down.sql

DROP TABLE IF EXISTS user_blocks;

DROP INDEX IF EXISTS idx_permissions_expires_at;
DROP INDEX IF EXISTS idx_permissions_status_grantee;

ALTER TABLE permissions
    DROP COLUMN IF EXISTS invited_by,
    DROP COLUMN IF EXISTS responded_at,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS message,
    DROP COLUMN IF EXISTS status;
