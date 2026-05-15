-- The §11.5 idempotency-key store (build-sequence §18.21, Phase 7).
--
-- idempotency_keys is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- A row caches the response of a completed idempotent operation
-- (CreateSession, FinalizeWorkspace, StartSession, SpawnChild,
-- Approve/DenyDelegation, and Resume) keyed by (tenant_id,
-- idempotency_key). body_hash is the §11.5 SHA-256 request-body digest
-- used to reject same-key-different-body reuse. Records age out of the
-- §11.5 24-hour TTL; stored_at drives garbage collection.

CREATE TABLE idempotency_keys (
    tenant_id        TEXT        NOT NULL REFERENCES tenants(id),
    idempotency_key  TEXT        NOT NULL,
    body_hash        TEXT        NOT NULL,
    response_status  INTEGER     NOT NULL,
    response_headers JSONB       NOT NULL DEFAULT '{}',
    response_body    BYTEA       NOT NULL DEFAULT '\x',
    stored_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, idempotency_key)
);

-- The stored_at index supports the §11.5 24-hour TTL garbage-collection
-- sweep, which deletes rows older than the retention window.
CREATE INDEX idempotency_keys_stored_at_idx ON idempotency_keys (stored_at);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON idempotency_keys
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON idempotency_keys
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON idempotency_keys TO lenny_app;
