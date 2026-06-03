-- §25.4 escalation Tier 1 (Postgres) durable store. lenny-ops records a
-- structured escalation when an agent's problem exceeds its remediation
-- capabilities; the create path is tiered Postgres -> Redis -> in-memory
-- so an escalation can always be recorded, including during the storage
-- outages most likely to trigger one. This table is the durable tier.
--
-- created_at is deliberately NOT defaulted to now(): the application
-- supplies the original authoring timestamp so an escalation flushed up
-- from the in-memory buffer (Section 25.4 Reconciliation) preserves its
-- real creation time rather than the flush time. The two indexes back
-- the §25.4 list filters (status + severity) and the newest-first
-- created_at ordering.
--
-- The table is platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists ops_escalations
-- among the PlatformPostgres() tables), so no tenant column or RLS
-- policy applies.
--
-- spec: §25.4 lines 2433-2455.

CREATE TABLE ops_escalations (
    id              TEXT PRIMARY KEY,
    severity        TEXT NOT NULL,            -- 'critical', 'warning', 'info'
    source          TEXT NOT NULL,            -- service account / agent name
    operation_id    TEXT,
    alert_name      TEXT,
    runbook_name    TEXT,
    summary         TEXT NOT NULL,
    diagnostic_data JSONB,
    failed_actions  JSONB NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'open',  -- 'open', 'acknowledged', 'resolved'
    persistence     TEXT NOT NULL,            -- 'durable-postgres', 'durable-redis', 'buffered-memory'
    emitted         BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL,     -- preserved across tier flushes (NOT defaulted)
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX ops_escalations_status_severity ON ops_escalations (status, severity);
CREATE INDEX ops_escalations_created_at ON ops_escalations (created_at DESC);
