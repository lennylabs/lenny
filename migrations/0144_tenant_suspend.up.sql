-- §15.1 "POST /v1/admin/tenants/{id}/suspend" / "resume": an operator
-- may suspend a tenant, which rejects new session creation and message
-- injection with TENANT_SUSPENDED until the tenant is resumed.
-- Suspension is orthogonal to the §12.8 deletion lifecycle `state`
-- column: a tenant carries its suspension marker independently of
-- whether it is active, disabling, or deleting. The operator identity
-- and reason are recorded so the suspension can be reconstructed
-- alongside the audit-trail `tenant.suspended` event.
-- See spec/15_external-api-surface.md §15.1 lines 818-819.
ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS suspended        BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS suspended_reason TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS suspended_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS suspended_by     TEXT        NOT NULL DEFAULT '';
