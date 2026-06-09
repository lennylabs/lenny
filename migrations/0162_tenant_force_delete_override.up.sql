-- §12.8 lines 880-889 ("`--acknowledge-hold-override` admin action,
-- tenant scope"): a platform-admin force-delete of a tenant under active
-- legal hold authorizes the Phase 3.5 escrow segregation rather than
-- blocking. The override intent is durable on the tenant row so the
-- §12.8 tenant-deletion controller, which reconstructs its deletion job
-- from the persisted TenantState after a restart, still segregates held
-- evidence into the region-scoped escrow instead of re-blocking.
--
-- force_delete_hold_override   — set true by POST .../force-delete with
--                                acknowledgeHoldOverride.
-- force_delete_justification   — the required free-text override reason
--                                (override_justification on the
--                                gdpr.legal_hold_overridden_tenant event).
-- force_delete_by              — the authorizing platform-admin subject
--                                (override_by).
-- force_delete_at              — the instant the override was authorized.
--
-- F-12.8.2, F-24.10.2.

ALTER TABLE tenants
    ADD COLUMN force_delete_hold_override BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN force_delete_justification TEXT NOT NULL DEFAULT '',
    ADD COLUMN force_delete_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN force_delete_at TIMESTAMPTZ;
