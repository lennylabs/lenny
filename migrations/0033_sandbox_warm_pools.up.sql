-- The §5.2 SandboxWarmPool registry.
--
-- Pools are platform-global records keyed by name, like connectors
-- and runtime_definitions: per §5.1 a pool carries no tenant_id (the
-- §10.6 pool_tenant_access join table expresses per-tenant
-- visibility), so this table carries no lenny_tenant_guard trigger
-- and no RLS.
-- platform-global
CREATE TABLE sandbox_warm_pools (
    name                     TEXT        PRIMARY KEY,
    runtime_ref              TEXT        NOT NULL DEFAULT '',
    -- isolation_profile is the §5.3 profile override (standard,
    -- sandboxed, microvm); empty means inherit the runtime default.
    isolation_profile        TEXT        NOT NULL DEFAULT '',
    -- execution_mode is the §5.2 mode (session, task, concurrent).
    execution_mode           TEXT        NOT NULL DEFAULT '',
    resource_class           TEXT        NOT NULL DEFAULT '',
    warm_count               INTEGER     NOT NULL DEFAULT 0,
    max_session_age_seconds  INTEGER     NOT NULL DEFAULT 0,
    allow_standard_isolation BOOLEAN     NOT NULL DEFAULT false,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at               TIMESTAMPTZ
);

GRANT SELECT, INSERT, UPDATE, DELETE ON sandbox_warm_pools TO lenny_app;
