-- 000002_add_sessions_and_permissions.down.sql
-- Reverse of 000002: drops the trigger/function then the three new tables in
-- reverse dependency order (session_blocks references upload_sessions;
-- permissions references files which still exists after this migration).

DROP TRIGGER IF EXISTS trg_touch_upload_session ON upload_sessions;
DROP FUNCTION IF EXISTS touch_upload_session_updated_at();

DROP TABLE IF EXISTS session_blocks;
DROP TABLE IF EXISTS upload_sessions;
DROP TABLE IF EXISTS permissions;
