-- Migration 000018: Add Materialized Path to files table for O(1) folder tree operations.

-- 1. Add path column with default '/'
ALTER TABLE files ADD COLUMN IF NOT EXISTS path TEXT NOT NULL DEFAULT '/';

-- 2. Backfill existing hierarchical paths using recursive CTE
WITH RECURSIVE file_hierarchy AS (
    -- Anchor: Root items (parent_id is NULL)
    SELECT id, '/' || id || '/' AS computed_path
    FROM files
    WHERE parent_id IS NULL
    
    UNION ALL
    
    -- Recursive: Children
    SELECT f.id, fh.computed_path || f.id || '/' AS computed_path
    FROM files f
    INNER JOIN file_hierarchy fh ON f.parent_id = fh.id
)
UPDATE files
SET path = fh.computed_path
FROM file_hierarchy fh
WHERE files.id = fh.id;

-- 3. Fallback for any orphaned rows whose parent_id did not resolve to root
UPDATE files
SET path = '/' || id || '/'
WHERE path = '/' OR path IS NULL;

-- 4. Create B-tree index using text_pattern_ops for index-backed prefix matching (LIKE 'prefix%')
CREATE INDEX IF NOT EXISTS idx_files_user_id_path_pattern ON files (user_id, path text_pattern_ops);
