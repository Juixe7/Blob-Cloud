-- 000006_add_shortcuts.up.sql
ALTER TABLE files ADD COLUMN target_id UUID REFERENCES files(id) ON DELETE CASCADE;
