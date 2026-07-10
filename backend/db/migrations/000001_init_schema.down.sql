-- 000001_init_schema.down.sql
-- Reverse of the init schema. Drops tables in reverse dependency order so
-- foreign keys never dangle during teardown.

DROP TABLE IF EXISTS file_blocks;
DROP TABLE IF EXISTS blocks;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS users;

-- Extension is intentionally left installed; other databases/objects may use it.
