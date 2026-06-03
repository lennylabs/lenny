-- §25.4 remediation-lock coordination schema: the Tier 1 (Postgres)
-- lock table plus the dual-store outage-epoch counter and the
-- split-brain conflict log. lenny-ops acquires remediation locks at
-- Tier 1 with INSERT ... ON CONFLICT (scope) DO NOTHING RETURNING *;
-- the unique constraint on scope is the compare-and-set primitive that
-- closes the check-then-write window. The outage epoch lives in BOTH
-- Postgres (ops_lock_epoch) and Redis so split-brain detection survives
-- a single-store outage and any out-of-order recovery; reconciliation
-- writes MAX(redis_epoch, postgres_epoch) back here. ops_lock_conflicts
-- records every resolved split-brain for post-incident audit, surfaced
-- alongside lenny_ops_lock_split_brain_detected_total.
--
-- All three tables are platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists them among the
-- PlatformPostgres() tables), so no tenant column or RLS policy applies.
--
-- spec: §25.4 lines 2160-2253.

CREATE TABLE ops_remediation_locks (
    id           TEXT PRIMARY KEY,
    scope        TEXT NOT NULL,
    operation    TEXT NOT NULL,
    acquired_by  TEXT NOT NULL,
    operation_id TEXT,
    acquired_at  TIMESTAMPTZ NOT NULL,        -- written from server clock via Postgres now()
    expires_at   TIMESTAMPTZ NOT NULL,
    epoch        BIGINT NOT NULL,
    revision     BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT unique_active_scope UNIQUE (scope)
);

CREATE TABLE ops_lock_epoch (
    id        TEXT PRIMARY KEY DEFAULT 'singleton',
    current   BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ops_lock_conflicts (
    id                 BIGSERIAL PRIMARY KEY,
    scope              TEXT NOT NULL,
    pre_outage_lock    JSONB NOT NULL,
    post_outage_lock   JSONB NOT NULL,
    winner             TEXT NOT NULL,         -- "pre_outage" | "post_outage"
    loser_was_active   BOOLEAN NOT NULL,      -- true if the loser was non-expired at resolution time
    detected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
