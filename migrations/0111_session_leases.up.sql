-- §12.4 Postgres advisory-lock fallback for the §12.2 LeaseStore.
--
-- spec: §12.4 line 206 "Distributed session leases | Fall back to
-- Postgres advisory locks (higher latency)" and §12.4 line 181
-- "t:{tenant_id}:lease:session:{session_id} | SET NX with TTL; falls
-- back to Postgres on miss".
--
-- The Redis lease store (pkg/gateway/leasestore) is the primary
-- coordination surface. When Redis is unavailable the failover wrapper
-- routes Acquire/Renew/Release/Get to the Postgres-backed store, which
-- stores the lease as a row (holder + expires_at) and serializes the
-- compare-and-set across replicas with a per-(tenant, session)
-- transaction-level advisory lock (pg_advisory_xact_lock). The advisory
-- lock provides the cross-replica mutual exclusion the Redis Lua scripts
-- provide on the fast path; expires_at carries the TTL the Redis key
-- carries via PX. A lease whose expires_at has passed is treated as
-- absent, mirroring Redis TTL expiry.
--
-- session_id is TEXT (not a sessions(id) FK): it mirrors the untyped
-- Redis key space, and §12.8 step 1 releases the lease before the
-- session row is deleted, so no FK ordering coupling is wanted.

CREATE TABLE session_leases (
    tenant_id   TEXT        NOT NULL REFERENCES tenants(id),
    session_id  TEXT        NOT NULL,
    holder      TEXT        NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, session_id)
);

-- The expiry index lets a future reaper drop lapsed rows; the active
-- store treats expires_at <= now as absent on read regardless.
CREATE INDEX idx_session_leases_expires ON session_leases (expires_at);

-- Attach the standard tenant-isolation machinery per §12.3: the
-- lenny_tenant_guard trigger rejects any write whose transaction has
-- not set app.current_tenant to the row's tenant, and the
-- lenny_tenant_isolation RLS policy filters reads to the current tenant
-- (or the platform-admin __all__ sentinel used by the erasure job).
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_leases
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_leases
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );

-- §12.3 role separation: lenny_app is the non-superuser the gateway
-- connects as; without an explicit GRANT it cannot touch the table even
-- when RLS would admit the row.
GRANT SELECT, INSERT, UPDATE, DELETE ON session_leases TO lenny_app;
