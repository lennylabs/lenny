-- The §4.9 per-tenant CredentialPool registry.
--
-- credential_pools is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- The scalar fields the credentialpoolstore.CredentialPool struct
-- exposes directly (provider, the assignment strategy, the lease
-- limits, the audit timestamps) are typed columns. The nested fields —
-- the credential set and the §4.9 VCS host-pattern list — are stored
-- as jsonb documents, mirroring how sessions stores its workspace
-- plan. Structural invariants (the name pattern, non-negative limits,
-- unique credential ids) are validated in application code.

CREATE TABLE credential_pools (
    tenant_id                      TEXT        NOT NULL REFERENCES tenants(id),
    -- name is the §4.9 pool identifier, unique within the tenant. The
    -- structural format ^[a-z0-9][a-z0-9_-]{0,127}$ is validated in
    -- application code.
    name                           TEXT        NOT NULL,
    -- provider names the §4.9 credential provider (anthropic_direct,
    -- aws_bedrock, github, vertex_ai, azure_openai, vault_transit, or a
    -- custom provider).
    provider                       TEXT        NOT NULL DEFAULT '',
    -- credentials holds the §4.9 credential set as a jsonb array. Each
    -- entry carries an id and either a secret reference or the
    -- aws_bedrock role-arn / region pair.
    credentials                    JSONB       NOT NULL DEFAULT '[]',
    -- assignment_strategy is the §4.9 strategy (least-loaded,
    -- round-robin, sticky-until-failure). Empty selects the
    -- admin-handler default.
    assignment_strategy            TEXT        NOT NULL DEFAULT '',
    -- max_concurrent_sessions caps the active leases per credential.
    max_concurrent_sessions        INTEGER     NOT NULL DEFAULT 0,
    -- cooldown_on_rate_limit_seconds is how long a rate-limited
    -- credential is held out of assignment.
    cooldown_on_rate_limit_seconds INTEGER     NOT NULL DEFAULT 0,
    -- lease_ttl_seconds optionally overrides the provider-default lease
    -- TTL. Zero selects the provider default.
    lease_ttl_seconds              INTEGER     NOT NULL DEFAULT 0,
    -- renew_before_buffer_seconds is the §4.9 proactive-renewal lead
    -- time. Zero selects the 300-second default.
    renew_before_buffer_seconds    INTEGER     NOT NULL DEFAULT 0,
    -- host_patterns holds the §4.9 VCS-pool host matchers as a jsonb
    -- array of strings, used to route a gitClone URL to the pool.
    host_patterns                  JSONB       NOT NULL DEFAULT '[]',
    created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- deleted_at is the §4.9 soft-delete tombstone.
    deleted_at                     TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, name)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON credential_pools
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE credential_pools ENABLE ROW LEVEL SECURITY;
ALTER TABLE credential_pools FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON credential_pools
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON credential_pools TO lenny_app;
