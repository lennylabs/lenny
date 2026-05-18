-- The §15.1 usage / metering accumulator.
--
-- usage_events is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- The table is append-only: Record inserts a usage event and there is
-- no update or delete. id is a synthetic per-row UUID so the table can
-- carry a (tenant_id, id) primary key with tenant_id leading, matching
-- the tenant-scoped convention while the rows themselves are not
-- otherwise uniquely keyed.

CREATE TABLE usage_events (
    tenant_id     TEXT        NOT NULL REFERENCES tenants(id),
    id            UUID        NOT NULL DEFAULT gen_random_uuid(),
    -- runtime is the §15.1 per-runtime grouping key. Empty means the
    -- event is not attributed to a runtime and is excluded from the
    -- by-runtime rollup.
    runtime       TEXT        NOT NULL DEFAULT '',
    -- sessions is the session count this event contributes.
    sessions      BIGINT      NOT NULL DEFAULT 0,
    -- tokens_input / tokens_output are the §15.1 input/output token
    -- pair this event contributes.
    tokens_input  BIGINT      NOT NULL DEFAULT 0,
    tokens_output BIGINT      NOT NULL DEFAULT 0,
    -- pod_minutes is the §15.1 pod-runtime minutes this event
    -- contributes. DOUBLE PRECISION mirrors the Go float64 field.
    pod_minutes   DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

-- idx_usage_events_tenant_created indexes the time column for the
-- §15.1 range scan, with tenant_id leading so a per-tenant report
-- reads only that tenant's slice.
CREATE INDEX idx_usage_events_tenant_created
    ON usage_events (tenant_id, created_at);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON usage_events
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE usage_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_events FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON usage_events
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON usage_events TO lenny_app;
