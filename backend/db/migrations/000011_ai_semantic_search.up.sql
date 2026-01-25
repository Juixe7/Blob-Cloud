-- 1. Enable the pgvector extension (standard in PostgreSQL 15+)
CREATE EXTENSION IF NOT EXISTS vector;

-- 2. Add tags and summary columns to support automatic AI-generated indexing
ALTER TABLE files ADD COLUMN IF NOT EXISTS tags VARCHAR(512) DEFAULT NULL;
ALTER TABLE files ADD COLUMN IF NOT EXISTS summary TEXT DEFAULT NULL;

-- 3. Add a vector column for 384-dimension text embeddings (matching all-MiniLM-L6-v2)
ALTER TABLE files ADD COLUMN IF NOT EXISTS embedding vector(384) DEFAULT NULL;

-- 4. Create an HNSW index to allow highly optimized, sub-millisecond cosine similarity queries
CREATE INDEX IF NOT EXISTS files_embedding_hnsw_idx ON files USING hnsw (embedding vector_cosine_ops);
