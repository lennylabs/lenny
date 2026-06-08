-- §8.8 per-session token metering. The §4.9 LLM proxy extracts the
-- authoritative input/output token counts of every proxied request
-- (pkg/gateway/llmproxy); pkg/gateway/sessionusage folds them into this
-- table keyed by the originating session. The §8.8 TaskResult.usage and
-- TaskResult.treeUsage rollups read these per-session totals: a settling
-- task's usage.inputTokens / usage.outputTokens come from its own row,
-- and treeUsage sums the per-session totals across a settled subtree.
--
-- A row is keyed by (tenant_id, session_id). input_tokens / output_tokens
-- are the session's running lifetime totals, advanced atomically on each
-- proxied request so concurrent proxy calls from several gateway replicas
-- serialize on the row rather than racing a read-modify-write. The other
-- three §8.8 usage dimensions (wallClockSeconds, podMinutes,
-- credentialLeaseMinutes) are derived from the session row's timestamps
-- and pod binding at materialization time and are not stored here.
--
-- Direct-mode sessions never reach the proxy hot path, so they accumulate
-- no token rows; their token dimensions surface as zero until the
-- direct-mode usage-pull path lands (tracked separately). Proxy mode (the
-- §4.9 default) records authoritative counts here.
--
-- updated_at is stamped server-side with clock_timestamp() so a future
-- retention or staleness sweep measures the row's age against the database
-- clock rather than a replica's local Go clock.
--
-- spec: §8.8 lines 897-917; §4.9 line 1468 (proxy-extracted counts are
-- authoritative).

CREATE TABLE session_usage (
    tenant_id     TEXT        NOT NULL REFERENCES tenants(id),
    session_id    UUID        NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    input_tokens  BIGINT      NOT NULL DEFAULT 0,
    output_tokens BIGINT      NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, session_id)
);

-- Standard §12.3 tenant-isolation machinery: the lenny_tenant_guard
-- trigger rejects any write whose transaction has not set
-- app.current_tenant to the row's tenant, and the lenny_tenant_isolation
-- RLS policy filters reads to the current tenant (or the __all__ sentinel
-- a cross-tenant sweep runs under).
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_usage
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_usage
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );

-- §12.3 role separation: lenny_app is the non-superuser the gateway
-- connects as; without an explicit GRANT it cannot touch the table even
-- when RLS would admit the row.
GRANT SELECT, INSERT, UPDATE, DELETE ON session_usage TO lenny_app;
