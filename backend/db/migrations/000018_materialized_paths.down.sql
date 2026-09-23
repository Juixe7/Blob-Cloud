-- Migration 000018 Down: Revert Materialized Path

DROP INDEX IF EXISTS idx_files_user_id_path_pattern;
ALTER TABLE files DROP COLUMN IF EXISTS path;
