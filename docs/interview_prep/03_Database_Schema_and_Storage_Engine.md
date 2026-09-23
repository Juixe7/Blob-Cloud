# 03. Database Schema & Storage Engine

## 1. Database Engine & Driver Choice

- **Database Engine**: **PostgreSQL 16 (Relational DB)**.
- **Go Driver**: **`pgx` v5** (High-performance PostgreSQL driver using native binary protocol support).
- **Rationale for Relational DB**:
  - Requires strict transactional consistency (`ACID`) when linking files to blocks and updating user storage quotas.
  - Efficient dynamic query optimization for tree structures using **Recursive Common Table Expressions (CTEs)**.

---

## 2. Complete Database Schema Specification

```
   +-------------------+              +-------------------+
   |       users       |              |       files       |
   +-------------------+              +-------------------+
   | id (PK, UUID)     |<------------ | owner_id (FK)     |
   | email (UNIQUE)    |              | id (PK, UUID)     |<---+ (Parent-Child Folder Self-FK)
   | password_hash     |              | parent_id (FK)    |----+
   | storage_used      |              | name, size        |
   +-------------------+              | is_folder, trash  |
             ^                        +-------------------+
             |                                  ^
             |                                  |
   +---------+---------+              +---------+---------+
   |   permissions     |              |    file_blocks    |
   +-------------------+              +-------------------+
   | id (PK, UUID)     |              | file_id (FK)      |
   | file_id (FK) ----+------------->| block_id (FK) ----+
   | user_id (FK)      |              | block_index       | |
   | role (EDITOR/...) |              +-------------------+ |
   +-------------------+                                    v
                                              +-------------------+
                                              |      blocks       |
                                              +-------------------+
                                              | id (PK, UUID)     |
                                              | hash (UNIQUE)     |
                                              | s3_key, size      |
                                              +-------------------+
```

---

### Table 1: `users`
Stores user identities, hashed passwords, and running storage utilization.

```sql
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    name VARCHAR(100) NOT NULL,
    storage_used BIGINT DEFAULT 0 NOT NULL,
    is_verified BOOLEAN DEFAULT FALSE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE INDEX idx_users_email ON users(email);
```

---

### Table 2: `files`
Stores metadata for both binary files and virtual folders (`is_folder = TRUE`).

```sql
CREATE TABLE files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    size BIGINT DEFAULT 0 NOT NULL,
    mime_type VARCHAR(127) NOT NULL,
    is_folder BOOLEAN DEFAULT FALSE NOT NULL,
    parent_id UUID REFERENCES files(id) ON DELETE CASCADE,
    owner_id UUID REFERENCES users(id) ON DELETE CASCADE NOT NULL,
    is_deleted BOOLEAN DEFAULT FALSE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE INDEX idx_files_owner_parent ON files(owner_id, parent_id) WHERE NOT is_deleted;
CREATE INDEX idx_files_parent_id ON files(parent_id);
```

---

### Table 3: `blocks` (Global Deduplication Storage Index)
Contains unique physical 4MB blocks stored in S3 or Local Disk.

```sql
CREATE TABLE blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hash VARCHAR(64) UNIQUE NOT NULL, -- SHA-256 Hex Hash
    s3_key VARCHAR(512) NOT NULL,
    size_bytes INT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX idx_blocks_hash ON blocks(hash);
```

---

### Table 4: `file_blocks` (Junction Table)
Maps logical files to ordered physical blocks.

```sql
CREATE TABLE file_blocks (
    file_id UUID REFERENCES files(id) ON DELETE CASCADE NOT NULL,
    block_id UUID REFERENCES blocks(id) ON DELETE RESTRICT NOT NULL,
    block_index INT NOT NULL,
    PRIMARY KEY (file_id, block_index)
);

CREATE INDEX idx_file_blocks_block_id ON file_blocks(block_id);
```

---

### Table 5: `permissions` (Hierarchical Sharing ACL)
Grants shared folder/file permissions to specific users.

```sql
CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id UUID REFERENCES files(id) ON DELETE CASCADE NOT NULL,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE NOT NULL,
    role VARCHAR(32) CHECK (role IN ('VIEWER', 'EDITOR', 'OWNER')) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE (file_id, user_id)
);
```

---

## 3. Dynamic Access Control via PostgreSQL Recursive CTEs

### The Problem
When User B attempts to access a file deeply nested inside `Folder A / Subfolder B / DeepFolder C`, simple SQL checks fail because User B might have inherited permissions granted on `Folder A`. Checking this in code requires N+1 queries.

### The Solution: Recursive CTE Query ([permission.go](file:///c:/Users/Asus/Desktop/z/backend/internal/repository/postgres/permission.go))

```sql
WITH RECURSIVE file_path AS (
    -- Anchor member: Select target file
    SELECT id, parent_id, owner_id
    FROM files
    WHERE id = $1 AND is_deleted = FALSE

    UNION ALL

    -- Recursive member: Walk up parent folders
    SELECT f.id, f.parent_id, f.owner_id
    FROM files f
    INNER JOIN file_path fp ON f.id = fp.parent_id
    WHERE f.is_deleted = FALSE
)
SELECT EXISTS (
    -- Direct Owner
    SELECT 1 FROM file_path WHERE owner_id = $2
    UNION
    -- Shared Permission on file OR any ancestor folder
    SELECT 1 FROM permissions p
    JOIN file_path fp ON p.file_id = fp.id
    WHERE p.user_id = $2
);
```

---

## 4. Polymorphic Storage Driver Abstraction

Go backend abstracts storage behind a clean Go interface ([`StorageProvider`](file:///c:/Users/Asus/Desktop/z/backend/internal/storage/s3.go)):

```go
type StorageProvider interface {
    PutObject(ctx context.Context, key string, data io.Reader, size int64) error
    GetObject(ctx context.Context, key string) (io.ReadCloser, error)
    DeleteObject(ctx context.Context, key string) error
    GeneratePresignedPutURL(ctx context.Context, key string, expireSec int) (string, error)
    GeneratePresignedGetURL(ctx context.Context, key string, expireSec int) (string, error)
}
```

- **Local Storage Driver** ([`local.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/storage/local.go)): Reads/writes directly to `./tmp/storage` for zero-dependency local dev.
- **S3 Storage Driver** ([`s3.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/storage/s3.go)): Uses AWS SDK v2 for S3 / Cloudflare R2 presigned URLs.

---

## 5. Interviewer Deep-Dive Q&A

### Q1: Why use PostgreSQL CTEs instead of a Graph Database (Neo4j) or Materialized Path pattern?
**Answer**:
- **Materialized Path** (`path = '/folderA/folderB/'`) requires updating all child paths whenever a parent folder is renamed or moved (expensive write locks).
- **Graph Databases** introduce operational complexity and break relational transactional guarantees (`ACID`) when linking files to blocks.
- PostgreSQL CTEs resolve hierarchy trees dynamically in **< 2 milliseconds** for typical folder depths (<20 levels) when `parent_id` is indexed.

### Q2: What prevents `blocks` records from being deleted while files still reference them?
**Answer**:
The `file_blocks` foreign key uses `ON DELETE RESTRICT` for `block_id`. If a user deletes a file, Postgres deletes `file_blocks` rows, but refuses to delete the physical `blocks` row if another user's file still references it. Physical S3 block deletion is managed safely by asynchronous Garbage Collection.
