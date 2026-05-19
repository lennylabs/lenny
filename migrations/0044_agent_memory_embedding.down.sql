-- Reverses 0044_agent_memory_embedding. Dropping the embedding column
-- cascades agent_memory_embedding_idx. The `vector` extension is left
-- installed: another object may depend on it, and DROP EXTENSION would
-- fail or cascade unexpectedly.
ALTER TABLE agent_memory
    DROP COLUMN IF EXISTS embedding;
