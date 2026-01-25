-- 000004_add_trash_and_soft_delete.up.sql
-- Adds soft deletion support (deleted_at timestamp) to the files table.

ALTER TABLE files ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_files_user_deleted ON files(user_id, deleted_at);
