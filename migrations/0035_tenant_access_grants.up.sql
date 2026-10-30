-- The §4 runtime/pool tenant-access join tables.
--
-- runtime_tenant_access and pool_tenant_access express per-tenant
-- visibility of platform-global resources. Runtimes (runtime_definitions)
-- and pools (sandbox_warm_pools) carry no tenant_id; a grant row records
-- that one tenant may see and use one named runtime or pool.
--
-- These join tables are platform-global, like connectors and
-- runtime_definitions. A grant's tenant_id is a foreign key to tenants,
-- not an RLS scoping column: the §15.1 admin List endpoint returns every
-- tenant granted access to a resource, so the table carries no
-- lenny_tenant_guard trigger and no RLS. A tenant-scoped policy would
-- restrict List to the caller's own tenant and break the contract.
--
-- tenant_id leads the primary key so the composite PK index satisfies
-- the §12.3 R-01 schema rule and serves the ListForTenant lookup; a
-- secondary index on the resource name serves the List(resource) path.
-- platform-global
CREATE TABLE runtime_tenant_access (
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    runtime_name TEXT        NOT NULL REFERENCES runtime_definitions(name),
    -- granted_by is the subject of the platform-admin who recorded the
    -- grant, surfaced in the §15.1 List response.
    granted_by   TEXT        NOT NULL DEFAULT '',
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, runtime_name)
);

CREATE TABLE pool_tenant_access (
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    pool_name  TEXT        NOT NULL REFERENCES sandbox_warm_pools(name),
    -- granted_by is the subject of the platform-admin who recorded the
    -- grant, surfaced in the §15.1 List response.
    granted_by TEXT        NOT NULL DEFAULT '',
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, pool_name)
);

-- List(resource) returns every tenant granted a named runtime or pool;
-- index the resource-name lookup.
CREATE INDEX runtime_tenant_access_runtime_idx
    ON runtime_tenant_access (runtime_name);
CREATE INDEX pool_tenant_access_pool_idx
    ON pool_tenant_access (pool_name);

GRANT SELECT, INSERT, UPDATE, DELETE ON runtime_tenant_access TO lenny_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON pool_tenant_access TO lenny_app;
