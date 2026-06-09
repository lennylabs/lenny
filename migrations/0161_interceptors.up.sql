-- §4.8 lines 1034-1040 / §8.3 lines 205-224 (SEC-013) — the
-- external-interceptor registry. Each row is the admin-mutable,
-- cross-replica source of truth for a deployer-registered
-- RequestInterceptor: the registration fields (endpoint, priority,
-- fail_policy, timeout_ms, phases) plus the two server-minted,
-- admin-API-immutable SEC-013 fields recording the most recent
-- fail-closed -> fail-open transition. The delegation service reads this
-- table per delegate_task / lenny/send_message to enforce the
-- INTERCEPTOR_WEAKENING_COOLDOWN keyed on contentPolicy.interceptorRef.
--
-- Interceptors are platform-scoped cluster infrastructure (no tenant_id),
-- like the runtime and pool registries; the platform-admin role owns the
-- /v1/admin/interceptors CRUD surface. F-4.8.17.
CREATE TABLE IF NOT EXISTS interceptors (
    name                            TEXT        PRIMARY KEY,
    endpoint                        TEXT        NOT NULL,
    priority                        INTEGER     NOT NULL,
    fail_policy                     TEXT        NOT NULL,
    timeout_ms                      INTEGER     NOT NULL DEFAULT 0,
    -- the §4.8 phase set, stored as a jsonb string array.
    phases                          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    -- §8.3 SEC-013 server-minted, admin-API-immutable transition fields.
    -- The admin write path never sets these from a request body; the
    -- gateway mints fail_open_transition_at at the instant a
    -- fail-closed -> fail-open transition is persisted and records the
    -- cluster cooldown_seconds then in force (the §8.3 meta-cooldown rule
    -- pins a pending cooldown to this recorded value). NULL means no
    -- active weakening cooldown.
    fail_open_transition_at         TIMESTAMPTZ,
    cooldown_seconds_at_transition  INTEGER,
    created_at                      TIMESTAMPTZ NOT NULL,
    updated_at                      TIMESTAMPTZ NOT NULL,
    -- §15.1 line 1207 optimistic-concurrency counter; starts at 1.
    version                         BIGINT      NOT NULL DEFAULT 1,
    -- §4.8 line 1031 closed fail-policy enum.
    CONSTRAINT interceptors_fail_policy_check
        CHECK (fail_policy IN ('fail-closed', 'fail-open')),
    -- §4.8 line 1020 reserved-priority ceiling: external interceptors
    -- must register above 100.
    CONSTRAINT interceptors_priority_check
        CHECK (priority > 100)
);

-- The registry holds no personal data (cluster configuration only), so
-- it is exempt from the §12.8 DeleteByUser / DeleteByTenant erasure path.
-- The platform-admin-driven CRUD surface needs the full DML set.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON interceptors TO lenny_app;
    END IF;
END $$;
