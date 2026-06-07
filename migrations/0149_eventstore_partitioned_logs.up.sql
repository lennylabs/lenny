-- §16.4 line 378: the EventStore tables (audit events, session logs,
-- stream cursors) are partitioned by time using native Postgres range
-- partitioning so a background job can drop whole partitions beyond the
-- retention window (30 days for session logs, 7 days for stream
-- cursors). This migration creates the two greenfield EventStore tables
-- the spec names but that did not exist; both are declaratively
-- range-partitioned on created_at.
--
-- audit_log is the third EventStore table. It is NOT converted to
-- native range partitioning here: §12.8 line 815 mandates a foreign key
-- audit_redaction_receipts.audit_event_id -> audit_log.id, which
-- requires a single-column UNIQUE (id) on audit_log; Postgres forbids a
-- unique constraint that omits the partition key (created_at) on a
-- range-partitioned table, so native partitioning of audit_log
-- contradicts §12.8. The audit retention regime stays on the §16.4
-- DELETE-based pruner (auditstore.PruneRetention + auditretention.Pruner,
-- migration 0107 / commit 0ddce6a9). See BUILD-GAPS F-16.4.6.
--
-- Partition lifecycle: the partitionmaint maintainer (leader-gated, run
-- by lenny-gateway) creates the current + N-ahead daily partitions and
-- drops partitions whose entire range is older than the retention
-- window. Each parent carries a DEFAULT partition as a safety net so an
-- insert that arrives before the maintainer has created the dated
-- partition still lands durably; the maintainer logs a warning when the
-- default partition is non-empty.
--
-- Both tables carry the standard §12.3 tenant-scoped posture: the
-- lenny_tenant_guard BEFORE-ROW trigger (cascades to every partition on
-- PG 13+), RLS filtered through current_setting('app.current_tenant'),
-- and the lenny_app DML grants (cascade to partitions through the
-- parent). tenant_id is a plain column rather than a foreign key,
-- matching the other append-only EventStore ledgers.
--
-- spec: §16.4 line 378 (EventStore partitioning + retention windows);
-- §12.2 line 16 (EventStore stores session logs and stream cursors);
-- §16.4 line 373 (setup stdout/stderr captured in EventStore).

-- --- session_logs -------------------------------------------------------
-- The durable per-session operational log (setup command stdout/stderr,
-- runtime stderr, and session event records). Retention window: 30 days
-- (§16.4 line 378; §17.8 line 877). seq is the per-session monotonic
-- ordering the §15.1 GET /v1/sessions/{id}/logs cursor pages over.
CREATE TABLE session_logs (
    tenant_id   TEXT        NOT NULL,
    session_id  TEXT        NOT NULL,
    seq         BIGINT      NOT NULL,
    -- stream distinguishes the captured source: stdout | stderr | event.
    stream      TEXT        NOT NULL DEFAULT 'stdout',
    event_type  TEXT        NOT NULL DEFAULT 'log',
    payload     JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- created_at is part of the primary key because Postgres requires the
    -- range-partition key to appear in every unique constraint.
    PRIMARY KEY (tenant_id, session_id, seq, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_session_logs_tenant_session
    ON session_logs (tenant_id, session_id, seq);

CREATE TABLE session_logs_default PARTITION OF session_logs DEFAULT;

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_logs
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_logs
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON session_logs TO lenny_app;

-- --- stream_cursors -----------------------------------------------------
-- SSE / streaming resumption cursors: the last delivered sequence number
-- per (session, consumer) so a reconnecting consumer resumes without
-- gap or duplication. Retention window: 7 days (§16.4 line 378); a
-- cursor older than the window belongs to a connection that will not
-- resume.
CREATE TABLE stream_cursors (
    tenant_id    TEXT        NOT NULL,
    session_id   TEXT        NOT NULL,
    consumer_id  TEXT        NOT NULL,
    last_seq     BIGINT      NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, session_id, consumer_id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_stream_cursors_tenant_session
    ON stream_cursors (tenant_id, session_id, consumer_id);

CREATE TABLE stream_cursors_default PARTITION OF stream_cursors DEFAULT;

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON stream_cursors
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE stream_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE stream_cursors FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON stream_cursors
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON stream_cursors TO lenny_app;
