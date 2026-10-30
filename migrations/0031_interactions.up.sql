-- The §6 / §9.2 pending interactive-session prompt registry.
--
-- interactions is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- The scalar fields the interactionstore.Interaction struct exposes
-- directly (kind, the §15.1 session/user authorization triple, phase,
-- reason, the audit timestamps) are typed columns. The opaque nested
-- fields — the interaction detail map and the client's response — are
-- stored as jsonb documents, mirroring how sessions stores its
-- workspace plan.

CREATE TABLE interactions (
    tenant_id   TEXT        NOT NULL REFERENCES tenants(id),
    -- session_id is the §15.1 authorization-triple session component.
    session_id  TEXT        NOT NULL,
    -- id is the tool_call_id or elicitation_id. (tenant_id, session_id,
    -- id) is the registry key, matching the in-memory store.
    id          TEXT        NOT NULL,
    -- kind discriminates §9 tool-use from §9.2 elicitation.
    kind        TEXT        NOT NULL DEFAULT '',
    -- user_id is the §15.1 authorization-triple user the interaction is
    -- directed at.
    user_id     TEXT        NOT NULL DEFAULT '',
    -- phase is the resolution state (pending, approved, denied,
    -- responded, dismissed). Transitions are enforced in application
    -- code by the resolve/dismiss lifecycle.
    phase       TEXT        NOT NULL DEFAULT 'pending',
    -- detail holds the opaque interaction metadata: the tool name and
    -- arguments, or the elicitation prompt.
    detail      JSONB,
    -- response holds the client's answer once the interaction resolves.
    response    JSONB,
    -- reason holds the deny / dismiss reason once resolved.
    reason      TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- resolved_at is the resolution timestamp. It is null while the
    -- interaction is still pending.
    resolved_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, session_id, id)
);

-- idx_interactions_tenant_user indexes the §11.4 / §12.8 by-user sweep
-- (DismissByUser, DeleteByUser), with tenant_id leading so the scan
-- reads only the target tenant's slice.
CREATE INDEX idx_interactions_tenant_user
    ON interactions (tenant_id, user_id);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON interactions
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE interactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE interactions FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON interactions
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON interactions TO lenny_app;
