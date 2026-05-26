-- §5.2 line 396 / §12.9: the runtime's data-classification tier,
-- previously dropped at the gateway boundary.
--
-- workspace_tier records the §12.9 data classification a runtime
-- processes: '' (implicit T3 — Confidential, the default) or 'T4'
-- (Restricted). The pool controller rejects allowCrossTenantReuse: true
-- on any pool whose associated Runtime is T4, because T4 workloads
-- require dedicated node pools for per-tenant key isolation (§6.4) and
-- cross-tenant pod reuse would co-locate two tenants' Restricted data on
-- a shared host. Stored as a plain text column alongside
-- integration_level rather than in a JSONB blob so the admission lookup
-- is a single-column read.
ALTER TABLE runtime_definitions
    ADD COLUMN workspace_tier TEXT NOT NULL DEFAULT '';
