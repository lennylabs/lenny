-- Tier 2 migration fixture. Phase 1.5 of TESTING.md uses this schema to
-- exercise the migration framework end-to-end. The shape is intentionally
-- simple but exercises spec §12.3 R-01 (tenant_id leading column).

CREATE TABLE tenants (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE widgets (
    tenant_id  TEXT      NOT NULL REFERENCES tenants(id),
    id         UUID      NOT NULL,
    name       TEXT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
