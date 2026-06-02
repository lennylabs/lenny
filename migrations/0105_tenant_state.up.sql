-- §12.8 "Tenant deletion lifecycle" — each tenant carries a TenantState
-- enum (active, disabling, deleting, deleted) persisted in Postgres and
-- exposed via the admin API. Phase actions key off it: the gateway
-- rejects new session creation once a tenant leaves the 'active' state,
-- and a 'deleted' tombstone row backs the 410 Gone response on
-- GET /v1/admin/tenants/{id}. The transitions are driven by the §12.8
-- tenant-deletion controller; the generic admin surface does not set it.
ALTER TABLE tenants
    ADD COLUMN state TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'disabling', 'deleting', 'deleted'));

-- Reconcile the pre-existing soft-delete marker: a tenant already
-- soft-deleted (deleted_at set) is a Phase-6 tombstone, so its state is
-- 'deleted'. New rows default to 'active'.
UPDATE tenants SET state = 'deleted' WHERE deleted_at IS NOT NULL;
