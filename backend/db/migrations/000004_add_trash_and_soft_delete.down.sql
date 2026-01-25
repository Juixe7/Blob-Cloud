-- 000004_add_trash_and_soft_delete.down.sql
DROP INDEX IF EXISTS idx_files_user_deleted;
ALTER TABLE files DROP COLUMN IF EXISTS deleted_at;
