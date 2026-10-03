-- 000020_add_unique_filename_indexes.up.sql
-- Enforce unique filenames per directory for active (non-deleted) files and folders.

-- 1. Uniqueness for root directory entries (parent_id IS NULL)
CREATE UNIQUE INDEX IF NOT EXISTS uq_files_root_active
ON files (user_id, name)
WHERE parent_id IS NULL AND deleted_at IS NULL;

-- 2. Uniqueness for nested folder entries (parent_id IS NOT NULL)
CREATE UNIQUE INDEX IF NOT EXISTS uq_files_folder_active
ON files (user_id, parent_id, name)
WHERE parent_id IS NOT NULL AND deleted_at IS NULL;
