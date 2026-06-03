-- §11.2 durable checkpoint of the Redis token-usage counters. The fast
-- path lives in Redis under the §12.4 key
-- t:{tenant_id}:quota:tokens:{user_id}:{window} (pkg/gateway/quotastore);
-- those counters are volatile and a Redis restart empties them. This
-- table is the periodic Postgres checkpoint §11.2 line 44 mandates:
-- "Postgres is updated periodically at a configurable sync interval
-- (quotaSyncIntervalSeconds) as a durable checkpoint, and on session
-- completion as final reconciliation." On Redis recovery the §11.2 line 48
-- two-source reconstruction restores each counter via the MAX rule
-- (restored = MAX(in_memory_counter, postgres_checkpoint)) rather than
-- resuming against a stale-zero value that would silently un-enforce a
-- budget the window had already exhausted.
--
-- A row is keyed by (tenant_id, scope, subject_id, period, window_label):
--   * scope is 'user' (a per-user window) or 'tenant' (the per-tenant
--     rollup window the §11.2 hierarchy keeps alongside the per-user one);
--     the spec restores "each active session and tenant scope".
--   * subject_id is the OIDC subject for the user scope and '' for the
--     tenant rollup (Postgres TEXT cannot hold the NUL-byte rollup
--     sentinel quotastore uses for the Redis key, so the scope column
--     carries the distinction here).
--   * period is the §11.2 reset period the window belongs to (hourly,
--     daily, monthly).
--   * window_label is the §12.4 bucket label (e.g. 'hourly-2026060214');
--     storing it lets the reconcile restore only a counter whose window is
--     still current and skip one that has already rolled over.
--
-- token_total is the recorded window total at checkpoint time. The
-- platform-wide global rollup is deliberately not checkpointed here: the
-- §11.2 line 48 MAX rule is scoped to "each active session and tenant
-- scope", and the global rollup lives under a synthetic tenant slot that
-- has no tenants(id) row to satisfy the foreign key.
--
-- checkpoint_at is stamped with clock_timestamp() server-side so a future
-- staleness or retention sweep measures the row's age against the
-- database clock rather than a replica's local Go clock.
--
-- spec: §11.2 lines 42-48; §12.4 (Redis key layout).

CREATE TABLE token_usage_checkpoint (
    tenant_id     TEXT        NOT NULL REFERENCES tenants(id),
    scope         TEXT        NOT NULL CHECK (scope IN ('user', 'tenant')),
    subject_id    TEXT        NOT NULL DEFAULT '',
    period        TEXT        NOT NULL,
    window_label  TEXT        NOT NULL,
    token_total   BIGINT      NOT NULL DEFAULT 0,
    checkpoint_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, scope, subject_id, period, window_label)
);

-- The checkpoint_at index lets a retention sweep find stale checkpoints
-- without scanning the whole table.
CREATE INDEX idx_token_usage_checkpoint_checkpoint
    ON token_usage_checkpoint (checkpoint_at);

-- Standard §12.3 tenant-isolation machinery: the lenny_tenant_guard
-- trigger rejects any write whose transaction has not set
-- app.current_tenant to the row's tenant, and the lenny_tenant_isolation
-- RLS policy filters reads to the current tenant (or the __all__ sentinel
-- the cross-tenant recovery reconstruction runs under).
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON token_usage_checkpoint
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE token_usage_checkpoint ENABLE ROW LEVEL SECURITY;
ALTER TABLE token_usage_checkpoint FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON token_usage_checkpoint
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );

-- §12.3 role separation: lenny_app is the non-superuser the gateway
-- connects as; without an explicit GRANT it cannot touch the table even
-- when RLS would admit the row.
GRANT SELECT, INSERT, UPDATE, DELETE ON token_usage_checkpoint TO lenny_app;
