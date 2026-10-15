-- 000021_fix_accepted_permissions_expiry.down.sql

DROP INDEX IF EXISTS idx_permissions_lower_grantee_status;

ALTER TABLE permissions
    ALTER COLUMN expires_at SET DEFAULT (CURRENT_TIMESTAMP + INTERVAL '7 days');
