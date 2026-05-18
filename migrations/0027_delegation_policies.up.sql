-- The §8.3 DelegationPolicy registry.
--
-- DelegationPolicies are platform-global records keyed by name, like
-- connectors and runtime_definitions: a policy is referenced by
-- runtimes and delegation leases across tenants, so the registry is
-- not tenant-scoped. This table carries no tenant_id column, no
-- lenny_tenant_guard trigger, and no RLS.
-- platform-global
CREATE TABLE delegation_policies (
    name       TEXT        PRIMARY KEY,
    -- policy holds the §8.3 policy body: the ordered tag-matched
    -- allow/deny Rules, the contentPolicy block (maxInputSize,
    -- interceptorRef, scanExportedFiles, maxExportedFileSize), and
    -- allowSelfRecursion. The scanExportedFiles / interceptorRef
    -- dependency (EXPORT_SCAN_REQUIRES_INTERCEPTOR) is validated in
    -- application code at the admin-API boundary.
    policy     JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

GRANT SELECT, INSERT, UPDATE, DELETE ON delegation_policies TO lenny_app;
