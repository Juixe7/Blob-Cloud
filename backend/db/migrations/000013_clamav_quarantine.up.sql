-- Track file processing & safety states
ALTER TABLE files ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'PROCESSING'; -- 'PROCESSING', 'ACTIVE', 'QUARANTINED'

-- Set existing files to 'ACTIVE'
UPDATE files SET status = 'ACTIVE' WHERE status = 'PROCESSING';
