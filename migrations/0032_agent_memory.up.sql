-- The §9.4 agent-memory store: the records behind the lenny/memory_write
-- and lenny/memory_query platform MCP tools.
--
-- agent_memory is tenant-scoped AND user-scoped: every record carries
-- both a tenant_id and a user_id, and the §9.4 store enforces strict
-- tenant+user isolation. The table carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- This is the plain-Postgres backend. The §9.4 pgvector embedding
-- column and the semantic-search index are a later wave; this table
-- carries no vector column.

CREATE TABLE agent_memory (
    -- tenant_id leads the primary key: the table is tenant-scoped per
    -- §9.4 and the trigger plus RLS policy filter on it.
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    -- user_id is the §9.4 owning user. Memory is scoped per-user
    -- within a tenant; a user never sees another user's memory.
    user_id    TEXT        NOT NULL,
    -- id is the per-record memory key, the mem_-prefixed identifier
    -- assigned by the store on write.
    id         TEXT        NOT NULL,
    -- agent_type and session_id are the optional finer scope the
    -- memory was written under. Empty string means unset.
    agent_type TEXT        NOT NULL DEFAULT '',
    session_id TEXT        NOT NULL DEFAULT '',
    -- content is the memory text matched by lenny/memory_query.
    content    TEXT        NOT NULL DEFAULT '',
    -- metadata holds arbitrary caller key-value pairs as jsonb.
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- created_at is the server-stamped write time and the §9.4
    -- capacity-eviction order (oldest first).
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, id)
);

-- The §9.4 per-user capacity eviction and the query/list paths all
-- scan a single (tenant_id, user_id) partition newest-first.
CREATE INDEX agent_memory_owner_created_idx
    ON agent_memory (tenant_id, user_id, created_at DESC);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON agent_memory
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE agent_memory ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_memory FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON agent_memory
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON agent_memory TO lenny_app;
