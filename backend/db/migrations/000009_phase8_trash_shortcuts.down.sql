ALTER TABLE files DROP COLUMN IF EXISTS shortcut_target_id;
ALTER TABLE files DROP COLUMN IF EXISTS mime_type;
-- Note: intentionally leaving deleted_at intact as it was likely added by an earlier migration.
