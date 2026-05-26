-- §11.5 idempotency-key GC sweep index: replace the single-column
-- stored_at index with a (tenant_id, stored_at) composite so the
-- per-tenant DELETE in pgstore.Store.DeleteExpired
-- (WHERE tenant_id = $1 AND stored_at < $2) can index-scan only the
-- tenant's rows. The sweep is per-tenant by design (the §12.3
-- lenny_tenant_guard trigger fires on every DELETE, requiring a
-- per-tenant transaction); the prior single-column index supported
-- only a hypothetical cross-tenant sweep that the codebase never
-- emits. spec: §11.5 line 277.

DROP INDEX IF EXISTS idempotency_keys_stored_at_idx;
CREATE INDEX idempotency_keys_tenant_stored_at_idx
    ON idempotency_keys (tenant_id, stored_at);
