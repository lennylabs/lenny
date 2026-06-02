-- §25.4 idempotency-key registry for lenny-ops mutating endpoints. The
-- §25.4 control-plane idempotency contract keys records by (key, caller_id)
-- where caller_id is the OIDC sub claim of the requesting service account,
-- so two callers using the same UUID receive independent behavior and one
-- caller cannot replay another's operation by guessing their key.
--
-- The table is platform-scoped (the lenny-ops control plane is not
-- multi-tenanted at the §25 boundary; §25.4 line 1492 lists it among the
-- PlatformPostgres() tables that must be reachable without a tenant or
-- session id), so no tenant column or RLS policy applies. status moves
-- in_progress -> completed | failed; response carries the cached response
-- envelope replayed on a completed-row hit. expires_at drives the §25.4
-- retention DELETE (daily off-peak plus lazy cleanup on acquire); the
-- index on it keeps that sweep cheap. The two TTL classes (24h standard,
-- 7d long-running) are encoded in expires_at by the writer, not in a
-- separate column.
--
-- spec: §25.4 lines 2011-2130.

CREATE TABLE ops_idempotency_keys (
    key         TEXT        NOT NULL,
    caller_id   TEXT        NOT NULL,              -- OIDC sub claim
    endpoint    TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'in_progress',  -- 'in_progress', 'completed', 'failed'
    response    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (key, caller_id)
);

CREATE INDEX ops_idempotency_keys_expires_at ON ops_idempotency_keys (expires_at);
