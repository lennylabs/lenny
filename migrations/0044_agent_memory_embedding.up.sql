-- The §9.4 pgvector semantic-search column for the agent-memory store.
--
-- Migration 0032 created agent_memory as the plain-Postgres backend and
-- left the §9.4 pgvector embedding column to a later wave. This
-- migration adds that column and the approximate-nearest-neighbour
-- index, completing the §9.4 "Postgres + pgvector" default backend.
--
-- The pgstore.Query path embeds the query text and ranks rows by
-- vector distance over this column; rows written before the embedding
-- column existed (NULL embedding) fall back to the case-insensitive
-- substring match. agent_memory keeps the lenny_tenant_guard trigger
-- and the RLS policy from migration 0032 unchanged — this migration is
-- purely additive (a nullable column plus an index), so the §12.3
-- tenant-guard and the R-01 / R-02 schema rules are unaffected.

-- pgvector ships the `vector` type and the ivfflat / hnsw index access
-- methods. The extension is idempotent; it is also created here rather
-- than in migration 0001 so the dependency is co-located with the only
-- table that uses it.
CREATE EXTENSION IF NOT EXISTS vector;

-- embedding is the §9.4 semantic-search vector for the memory content.
-- The dimension matches memorystore.EmbeddingDim — the deterministic
-- local Embedder produces a fixed-width feature-hash vector, and a
-- provider-backed Embedder is configured to the same width. The column
-- is nullable: a memory written before this migration carries no
-- embedding and the substring fallback path serves it.
ALTER TABLE agent_memory
    ADD COLUMN embedding vector(256);

-- An ivfflat index for approximate-nearest-neighbour search under the
-- cosine-distance operator class. agent_memory rows are tenant-scoped
-- and the RLS policy filters every scan, so the index serves the
-- post-RLS candidate set. lists=100 is the pgvector default starting
-- point for tables up to the low millions of rows; a deployer running
-- a far larger memory corpus can REINDEX with a higher list count.
-- Partial on `embedding IS NOT NULL` so substring-only legacy rows do
-- not enter the index.
CREATE INDEX agent_memory_embedding_idx
    ON agent_memory USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;
