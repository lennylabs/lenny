-- §11.2 / §12.4 durable checkpoint of the §8.2 delegation tree budget
-- counters. The fast path lives in Redis under the tree-scoped
-- {root_session_id}:dlg:* keys (pkg/gateway/treebudget); those counters
-- are volatile and a Redis restart empties them. This table is the
-- periodic Postgres checkpoint §11.2 line 29 mandates: every
-- quotaSyncIntervalSeconds the gateway persists each active tree's
-- structural budget (active node count, consumed token budget, and the
-- separately-tracked aggregate in-memory footprint) so that on Redis
-- recovery the §11.2 line 48 two-source reconstruction can restore the
-- counters via the MAX rule rather than resuming against a stale-zero
-- value that would silently un-enforce a budget the tree had already hit.
--
-- The row is keyed by (tenant_id, root_session_id): a delegation tree is
-- identified tree-wide by its root session, and the Redis keys carry no
-- tenant prefix (the §12.4 R-04 intentional exception), so the tenant is
-- recovered here from the SessionStore under RLS, exactly as the §12.4
-- line 193 application-layer isolation rule requires.
--
-- tree_memory_bytes is checkpointed separately from tree_size because
-- §8.2 offloads completed subtrees and reclaims their memory, so the two
-- counters diverge; the memory counter is the binding constraint for
-- gateway memory protection (§11.2 line 29).
--
-- extension_denied / cool_off_expiry are the §8.6 lines 730-733
-- rejection-denial durability columns. The §11.2 counter checkpoint does
-- not write them (it touches only the counter columns and checkpoint_at
-- on conflict, leaving the denial state intact); a Postgres-backed
-- BudgetSource is the consumer that persists and reads them inside the
-- budget-increment transaction. They default to the not-denied state so
-- the table shape matches the spec without the checkpoint having to own
-- the denial lifecycle.
--
-- checkpoint_at is stamped with clock_timestamp() server-side so the
-- §11.2 line 48 irrecoverability test (checkpoint older than
-- 2 x quotaSyncIntervalSeconds) compares against the database clock
-- rather than a replica's local Go clock.
--
-- spec: §11.2 lines 29, 48; §12.4 lines 193, 218; §8.6 lines 730-733.

CREATE TABLE delegation_tree_budget (
    tenant_id             TEXT        NOT NULL REFERENCES tenants(id),
    root_session_id       TEXT        NOT NULL,
    tree_size             BIGINT      NOT NULL DEFAULT 0,
    token_budget_consumed BIGINT      NOT NULL DEFAULT 0,
    tree_memory_bytes     BIGINT      NOT NULL DEFAULT 0,
    extension_denied      BOOLEAN     NOT NULL DEFAULT FALSE,
    cool_off_expiry       TIMESTAMPTZ,
    checkpoint_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, root_session_id)
);

-- The checkpoint_at index lets the recovery reconstruction and a future
-- reaper find stale checkpoints without scanning the whole table.
CREATE INDEX idx_delegation_tree_budget_checkpoint
    ON delegation_tree_budget (checkpoint_at);

-- Standard §12.3 tenant-isolation machinery: the lenny_tenant_guard
-- trigger rejects any write whose transaction has not set
-- app.current_tenant to the row's tenant, and the lenny_tenant_isolation
-- RLS policy filters reads to the current tenant (or the __all__
-- sentinel the cross-tenant recovery reconstruction runs under).
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON delegation_tree_budget
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE delegation_tree_budget ENABLE ROW LEVEL SECURITY;
ALTER TABLE delegation_tree_budget FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON delegation_tree_budget
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );

-- §12.3 role separation: lenny_app is the non-superuser the gateway
-- connects as; without an explicit GRANT it cannot touch the table even
-- when RLS would admit the row.
GRANT SELECT, INSERT, UPDATE, DELETE ON delegation_tree_budget TO lenny_app;
