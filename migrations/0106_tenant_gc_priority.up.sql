-- §12.5 line 317 "gcPriority: high" — each tenant carries a GC priority
-- (normal | high) persisted in Postgres and configurable per tenant via
-- the admin API. A 'high' tenant (intended for T4 erasure-SLA compliance)
-- triggers an immediate tenant-scoped incremental GC sweep whenever an
-- erasure job for the tenant completes, independent of the global cycle
-- interval. New rows default to 'normal'.
ALTER TABLE tenants
    ADD COLUMN gc_priority TEXT NOT NULL DEFAULT 'normal'
        CHECK (gc_priority IN ('normal', 'high'));
