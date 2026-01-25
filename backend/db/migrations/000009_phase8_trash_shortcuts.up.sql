-- 1. Soft-Delete Support (if not exists)
ALTER TABLE files ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE DEFAULT NULL;

-- 2. Shortcuts & MIME Types Support
ALTER TABLE files ADD COLUMN IF NOT EXISTS mime_type VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream';
ALTER TABLE files ADD COLUMN IF NOT EXISTS shortcut_target_id UUID REFERENCES files(id) ON DELETE SET NULL DEFAULT NULL;

-- 3. Update existing folder records to match MIME type standards
UPDATE files SET mime_type = 'application/vnd.google-apps.folder' WHERE is_directory = true;
