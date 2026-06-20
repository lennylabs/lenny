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
--
-- The extension is created only when it is available in this server's
-- extension catalog, which it always is on a production Postgres. On
-- the §17.4 Embedded Mode stock PostgreSQL 16 bundle pgvector is not
-- installed and `CREATE EXTENSION` would raise "extension is not
-- available"; there the embedded stack has already installed a
-- pure-SQL `vector` shim before this migration runs, so the guard skips
-- the extension and the shim's type, casts, and `<=>` operator carry
-- the column below. The guard is inert in production.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector') THEN
        CREATE EXTENSION IF NOT EXISTS vector;
    END IF;
END $$;

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
--
-- The index is created only when the `ivfflat` access method is
-- present, which it always is on a production Postgres carrying the
-- pgvector extension. §17.4 Embedded Mode runs against a stock
-- PostgreSQL 16 bundle that does not ship pgvector; there the embedded
-- stack installs a pure-SQL `vector` shim that supplies the type, the
-- casts, and the `<=>` operator this column and the §9.4 query path
-- need, but not the `ivfflat` access method (which requires the C
-- extension). Guarding the index on the access method lets this
-- migration, and every migration after it, apply on the embedded
-- bundle without pgvector; semantic ranking then degrades to the
-- recency-ordered substring fallback (§9.4). The guard is inert in
-- production, where ivfflat is always present.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_am WHERE amname = 'ivfflat') THEN
        CREATE INDEX agent_memory_embedding_idx
            ON agent_memory USING ivfflat (embedding vector_cosine_ops)
            WITH (lists = 100)
            WHERE embedding IS NOT NULL;
    END IF;
END $$;
