DROP TABLE IF EXISTS shareable_links;
ALTER TABLE files DROP COLUMN IF EXISTS is_encrypted;
ALTER TABLE files DROP COLUMN IF EXISTS encryption_salt;
