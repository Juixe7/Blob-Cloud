-- 000021_fix_accepted_permissions_expiry.up.sql
-- Fixes the 7-day invitation expiry column default on permissions so ACCEPTED
-- and OWNER permissions do not expire after 7 days, and adds a case-insensitive
-- index on LOWER(grantee_email) for fast RBAC lookups.

ALTER TABLE permissions
    ALTER COLUMN expires_at DROP DEFAULT;

UPDATE permissions
SET expires_at = NULL
WHERE status = 'ACCEPTED' AND expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_permissions_lower_grantee_status
    ON permissions (LOWER(grantee_email), status);
