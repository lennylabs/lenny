ALTER TABLE session_eviction_state
    DROP COLUMN IF EXISTS context_truncated,
    DROP COLUMN IF EXISTS workspace_lost,
    DROP COLUMN IF EXISTS evicted_at,
    DROP COLUMN IF EXISTS conversation_cursor,
    DROP COLUMN IF EXISTS coordination_generation,
    DROP COLUMN IF EXISTS recovery_generation;
