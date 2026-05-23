-- §4.2 line 179 session_dlq_archive scaffold.
--
-- spec: §4.2 line 179 — "| session_dlq_archive | Tenant-scoped |
-- tenant_id column + RLS (current_setting('app.current_tenant')).
-- Keyed by (tenant_id, session_id, message_id)."
--
-- The table is the DLQ archive scaffold the future DLQ feature
-- writes to when a message exhausts every retry budget. v1 has no
-- consumer; this migration lands the §12.3 R-01 compliant schema
-- (tenant_id leads the primary index), the lenny_tenant_guard
-- trigger, and the lenny_tenant_isolation policy in the hard-error
-- current_setting(..., false) form per §12.3.

CREATE TABLE session_dlq_archive (
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    -- session_id references sessions(id) when the row is the
    -- still-live session it was dead-lettered from; the FK is
    -- ON DELETE CASCADE so a tenant erasure that drops sessions
    -- cleans up the DLQ archive at the same time.
    session_id   UUID        NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_id   TEXT        NOT NULL,
    -- payload is the original message body the consumer failed to
    -- process; JSONB so the schema evolves without re-migrating.
    payload      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- failure_reason is the classified cause the dispatcher attaches
    -- on archive (e.g., "max_retries_exhausted", "poison_message").
    failure_reason TEXT      NOT NULL DEFAULT '',
    -- retry_count is the number of attempts the dispatcher made
    -- before archiving.
    retry_count  BIGINT      NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    archived_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, session_id, message_id)
);

CREATE INDEX idx_session_dlq_archive_tenant_archived
    ON session_dlq_archive (tenant_id, archived_at DESC);

-- Attach the standard tenant-isolation machinery per §12.3.
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_dlq_archive
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_dlq_archive ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_dlq_archive FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_dlq_archive
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );

-- §12.3 role separation: the lenny_app role is the non-superuser
-- the gateway connects as; without an explicit GRANT it cannot
-- SELECT / INSERT / UPDATE / DELETE the table even when RLS would
-- otherwise admit the row.
GRANT SELECT, INSERT, UPDATE, DELETE ON session_dlq_archive TO lenny_app;
