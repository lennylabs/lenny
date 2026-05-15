-- The §9.3 connector registry (build-sequence §18.13).
--
-- Connectors are platform-global records keyed by id, like
-- runtime_definitions: the registry is not tenant-scoped (a
-- connector's reach is expressed by its visibility and labels, not a
-- tenant_id column), so this table carries no lenny_tenant_guard
-- trigger and no RLS.
-- platform-global
CREATE TABLE connectors (
    id             TEXT        PRIMARY KEY,
    display_name   TEXT        NOT NULL DEFAULT '',
    mcp_server_url TEXT        NOT NULL DEFAULT '',
    transport      TEXT        NOT NULL DEFAULT '',
    -- auth holds the §9.3 OAuth2 block (ConnectorAuth) or NULL for a
    -- public connector. The client secret is stored only as a
    -- namespace/name reference, never inline.
    auth           JSONB,
    visibility     TEXT        NOT NULL DEFAULT 'tenant',
    -- labels is the §9.3 environment-selector label map.
    labels         JSONB       NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);

GRANT SELECT, INSERT, UPDATE, DELETE ON connectors TO lenny_app;
