-- §25.9 deferred audit-write tracking. When lenny-ops generates audit
-- events during a Postgres outage (buffered escalations, flushed locks),
-- the events are recorded here with their full payload (including the
-- original timestamp) and reconciled into audit_log in original-timestamp
-- order on Postgres recovery. The chain hash is re-computed for the
-- affected range, which intentionally moves those events from
-- chainIntegrity: verified to chainIntegrity: rechained_post_outage so
-- operators can distinguish a legitimate post-outage rechain from a
-- tamper-broken chain.
--
-- The table is platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists it among the
-- PlatformPostgres() tables), so no tenant column or RLS policy applies.
--
-- spec: §25.9 lines 3684-3691.

CREATE TABLE audit_log_deferred_writes (
    id             BIGSERIAL PRIMARY KEY,
    event_payload  JSONB NOT NULL,     -- full audit event including original timestamp
    deferred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at     TIMESTAMPTZ,        -- when reconciled into audit_log
    replica_id     TEXT NOT NULL       -- which lenny-ops replica generated this
);
