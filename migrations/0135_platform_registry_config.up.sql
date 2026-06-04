-- §25.8 runtime registry configuration. platform_registry_config is the
-- singleton row PUT /v1/admin/platform/registry writes so the effective
-- image-registry configuration can be changed at runtime without a chart
-- redeploy (§25.8 line 3362: the registry runtime API is "stored in
-- Postgres, takes effect on next image resolution"). The chart-rendered
-- platform.registry.* Helm values are the base configuration; when this
-- row is present it overlays the base, so a later image resolution
-- (upgrade preflight, warm-pool image reference) reads the operator's
-- runtime override.
--
-- The pull-secret value is never stored here: the row carries only the
-- Secret name (pull_secret_name), matching §25.8 ("pull secret name is
-- included, secret contents are not"). The Kubernetes Secret itself is
-- mounted by downstream components.
--
-- The table is platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists the
-- platform-upgrade tables among the PlatformPostgres() tables), so no
-- tenant column or RLS policy applies. It holds at most one row
-- (id='singleton').
--
-- spec: §25.8 lines 3300-3301, 3360-3362.

CREATE TABLE platform_registry_config (
    id               TEXT PRIMARY KEY DEFAULT 'singleton',
    url              TEXT NOT NULL DEFAULT '',
    overrides        JSONB NOT NULL DEFAULT '{}',   -- component short name -> full image reference
    pull_secret_name TEXT NOT NULL DEFAULT '',
    require_digest   BOOLEAN NOT NULL DEFAULT false,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by       TEXT NOT NULL DEFAULT ''
);
