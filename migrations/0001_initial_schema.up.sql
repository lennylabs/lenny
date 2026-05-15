-- Initial Lenny schema (Phase 1.5, build-sequence §18.5).
--
-- Covers the foundational §12 storage tables that the early build
-- phases depend on: the tenant registry, the runtime registry, the
-- session tables, the append-only audit and billing ledgers, the
-- issued-token revocation index, and the pod-state mirror.
--
-- RLS policies, the lenny_tenant_guard trigger, the immutability
-- triggers, and the lenny_app / lenny_erasure role separation land in
-- migration 0002. Tables only here so 0002 can attach to them.
--
-- §12.3 R-01: every tenant-scoped table has tenant_id as the leading
-- column of its primary index, or is annotated -- session-sharded
-- (sessions, session_messages — distributed by the session-ID routing
-- prefix per §12.6) or -- platform-global (agent_pod_state — not a
-- tenant isolation boundary per §12.6).

-- tenants is the tenant registry. It is the RLS anchor: every
-- tenant-scoped row's tenant_id references tenants(id). The registry
-- itself is platform-global.
-- platform-global
CREATE TABLE tenants (
    id                    TEXT        PRIMARY KEY,
    display_name          TEXT        NOT NULL DEFAULT '',
    compliance_profile    TEXT        NOT NULL DEFAULT 'none',
    data_residency_region TEXT        NOT NULL DEFAULT '',
    workspace_tier        TEXT        NOT NULL DEFAULT '',
    -- genesis_nonce seeds the per-tenant audit hash chain (§11.7
    -- item 3). 256 bits from crypto/rand, generated at tenant create.
    genesis_nonce         BYTEA       NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- deleted_at marks a soft-deleted tenant (§12.8 tenant lifecycle).
    -- The row stays queryable for audit until the deletion controller
    -- fully erases it. NULL for active tenants.
    deleted_at            TIMESTAMPTZ
);

-- runtime_definitions is the §5.1 runtime registry. Runtimes are
-- platform-global records (§5.1); which tenants may use a runtime is
-- a separate access-grant concern.
-- platform-global
CREATE TABLE runtime_definitions (
    name              TEXT        PRIMARY KEY,
    type              TEXT        NOT NULL DEFAULT 'agent',
    image             TEXT        NOT NULL,
    execution_mode    TEXT        NOT NULL,
    isolation_profile TEXT        NOT NULL,
    integration_level TEXT        NOT NULL,
    description       TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,
    CONSTRAINT runtime_definitions_type_check
        CHECK (type IN ('agent', 'mcp')),
    CONSTRAINT runtime_definitions_execution_mode_check
        CHECK (execution_mode IN ('session', 'task', 'concurrent')),
    CONSTRAINT runtime_definitions_isolation_check
        CHECK (isolation_profile IN ('standard', 'sandboxed', 'microvm')),
    -- integration_level is empty for type: mcp runtimes (§5.1: the
    -- field is meaningful only on type: agent runtimes).
    CONSTRAINT runtime_definitions_level_check
        CHECK (integration_level IN ('', 'basic', 'standard', 'full'))
);

-- sessions is the §4.2 SessionStore root table, distributed by the
-- 32-bit routing prefix embedded in the UUIDv8 session ID (§12.6).
-- The (tenant_id, created_at) secondary index serves the tenant-wide
-- scatter-gather path (GET /v1/sessions, GDPR erasure).
-- session-sharded-justification: primary access is single-session
--   lookup by the embedded routing prefix; a delegation tree shares
--   one prefix and co-locates on a single shard.
-- session-sharded
CREATE TABLE sessions (
    id                        UUID        PRIMARY KEY,
    tenant_id                 TEXT        NOT NULL REFERENCES tenants(id),
    user_id                   TEXT        NOT NULL DEFAULT '',
    state                     TEXT        NOT NULL,
    runtime_ref               TEXT        NOT NULL,
    pool_ref                  TEXT        NOT NULL DEFAULT '',
    isolation_profile         TEXT        NOT NULL DEFAULT '',
    -- parent_session_id is the §7.1 lineage pointer set on derived and
    -- delegated child sessions. Self-referential FK; ON DELETE SET
    -- NULL because a parent may be retention-GC'd before its children.
    parent_session_id         UUID        REFERENCES sessions(id) ON DELETE SET NULL,
    root_session_id           UUID        NOT NULL,
    failure_class             TEXT        NOT NULL DEFAULT '',
    failure_reason            TEXT        NOT NULL DEFAULT '',
    workspace_snapshot_ref    TEXT        NOT NULL DEFAULT '',
    workspace_snapshot_source TEXT        NOT NULL DEFAULT '',
    workspace_snapshot_at     TIMESTAMPTZ,
    parent_workspace_ref      TEXT        NOT NULL DEFAULT '',
    retention_expires_at      TIMESTAMPTZ,
    upload_token_digest       TEXT        NOT NULL DEFAULT '',
    upload_token_expiry       TIMESTAMPTZ,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sessions_tenant_created ON sessions (tenant_id, created_at DESC);
CREATE INDEX idx_sessions_root ON sessions (root_session_id);

-- session_messages holds the §7.2 transcript.
-- session-sharded-justification: every row carries session_id with a
--   foreign key to sessions(id); rows co-locate with the parent
--   session on the same shard.
-- session-sharded
CREATE TABLE session_messages (
    id         UUID        PRIMARY KEY,
    session_id UUID        NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    seq        BIGINT      NOT NULL,
    role       TEXT        NOT NULL,
    content    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT session_messages_role_check
        CHECK (role IN ('user', 'assistant', 'system')),
    CONSTRAINT session_messages_seq_unique UNIQUE (session_id, seq)
);

CREATE INDEX idx_session_messages_tenant_session ON session_messages (tenant_id, session_id);

-- audit_log is the §11.7 append-only audit ledger. sequence_number is
-- the per-tenant authoritative total order for hash-chain
-- verification; prev_hash links each row to its predecessor.
-- payload_canonical_json holds the RFC 8785 JCS canonical form: the
-- Phase 13 migration converts it to a generated column once
-- pg_jcs_canonicalize() is installed; v1 application code computes the
-- canonical form before insert.
CREATE TABLE audit_log (
    -- tenant_id is the §11.7 per-tenant chain selector. It is NOT a
    -- foreign key to tenants: platform-admin actions are recorded on
    -- the "platform" chain, a pseudo-tenant that is not a registered
    -- tenant row. RLS and lenny_tenant_guard still scope every row.
    tenant_id              TEXT        NOT NULL,
    sequence_number        BIGINT      NOT NULL,
    id                     UUID        NOT NULL DEFAULT gen_random_uuid(),
    prev_hash              BYTEA,
    event_type             TEXT        NOT NULL,
    event_schema_version   TEXT        NOT NULL DEFAULT 'v1',
    payload                JSONB       NOT NULL,
    payload_canonical_json JSONB       NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    ocsf_translation_state TEXT        NOT NULL DEFAULT 'pending',
    eventbus_publish_state TEXT        NOT NULL DEFAULT 'pending',
    retry_count            INT         NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, sequence_number),
    CONSTRAINT audit_log_id_unique UNIQUE (id),
    CONSTRAINT audit_log_ocsf_state_check
        CHECK (ocsf_translation_state IN
            ('pending', 'retry_pending', 'succeeded', 'dead_lettered')),
    CONSTRAINT audit_log_eventbus_state_check
        CHECK (eventbus_publish_state IN
            ('pending', 'retry_pending', 'published', 'failed'))
);

CREATE INDEX idx_audit_log_tenant_created ON audit_log (tenant_id, created_at);

-- billing_events is the §11.2.1 append-only billing ledger.
CREATE TABLE billing_events (
    tenant_id       TEXT        NOT NULL REFERENCES tenants(id),
    sequence_number BIGINT      NOT NULL,
    schema_version  INT         NOT NULL DEFAULT 1,
    user_id         TEXT        NOT NULL DEFAULT '',
    session_id      UUID,
    experiment_id   TEXT        NOT NULL DEFAULT '',
    variant_id      TEXT        NOT NULL DEFAULT '',
    event_type      TEXT        NOT NULL,
    tokens_input    BIGINT      NOT NULL DEFAULT 0,
    tokens_output   BIGINT      NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, sequence_number)
);

CREATE INDEX idx_billing_events_tenant_session ON billing_events (tenant_id, session_id);

-- issued_tokens is the §13.3 issued-token metadata index for
-- revocation lookup and write-before-issue atomicity. The §12.2 schema
-- pins jti as the primary key and the three secondary indexes below.
CREATE TABLE issued_tokens (
    jti            TEXT        PRIMARY KEY,
    -- tenant_id is the token's operating tenant and the RLS scoping
    -- column. It is NOT a foreign key to tenants: a §13.3 service
    -- token can carry a platform or service identity that is not a
    -- registered tenant row.
    tenant_id      TEXT        NOT NULL,
    sub            TEXT        NOT NULL,
    -- token_hash is SHA-256 of the raw token bytes — never the token.
    token_hash     BYTEA       NOT NULL,
    scope          TEXT[]      NOT NULL DEFAULT '{}',
    audience       TEXT        NOT NULL DEFAULT '',
    issued_at      TIMESTAMPTZ NOT NULL,
    exp            TIMESTAMPTZ NOT NULL,
    revoked_at     TIMESTAMPTZ,
    revoked_reason TEXT,
    act_sub        TEXT,
    parent_jti     TEXT
);

CREATE INDEX idx_issued_tokens_tenant_sub ON issued_tokens (tenant_id, sub);
CREATE INDEX idx_issued_tokens_revoked ON issued_tokens (revoked_at) WHERE revoked_at IS NOT NULL;
CREATE INDEX idx_issued_tokens_exp ON issued_tokens (exp);

-- agent_pod_state mirrors Sandbox CRD status (§12.6). Platform-global:
-- tenant_id is a denormalized convenience column set on claim, not an
-- isolation boundary, so this table carries no RLS.
-- platform-global
CREATE TABLE agent_pod_state (
    pod_id            TEXT        PRIMARY KEY,
    pool_id           TEXT        NOT NULL,
    state             TEXT        NOT NULL,
    tenant_id         TEXT,
    session_id        TEXT,
    isolation_profile TEXT        NOT NULL,
    execution_mode    TEXT        NOT NULL,
    resource_version  BIGINT      NOT NULL,
    node_name         TEXT,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_pod_state_pool_state ON agent_pod_state (pool_id, state);
CREATE INDEX idx_agent_pod_state_session ON agent_pod_state (session_id) WHERE session_id IS NOT NULL;
CREATE INDEX idx_agent_pod_state_tenant ON agent_pod_state (tenant_id) WHERE tenant_id IS NOT NULL;
