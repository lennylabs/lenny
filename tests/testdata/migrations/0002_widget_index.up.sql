-- Adds a composite index keyed by tenant_id (R-01 compliant) so the
-- regression test can verify schema evolution + reversibility.

CREATE INDEX widgets_tenant_created_idx
    ON widgets (tenant_id, created_at);
