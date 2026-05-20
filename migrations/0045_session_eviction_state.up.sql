-- §12.2.1 EvictionStateStore. Holds minimal session state written
-- during the §4.4 eviction-checkpoint fallback path: when MinIO is
-- unreachable mid-checkpoint and the adapter cannot persist the
-- full workspace + transcript, the gateway writes a minimal-state
-- row carrying the conversation cursor and the last-message
-- context so a resumed session can replay its conversation even
-- without the workspace bytes.
--
-- `last_message_context` is either an inline JSON blob (small
-- contexts under the §12.5 2 KB threshold) or a MinIO object key
-- under `/{tenant_id}/eviction/` (large contexts). The GC sweep
-- (§12.5 terminal-state cleanup) keys off the prefix to decide
-- whether a MinIO delete is required when removing the row.
--
-- The table is tenant-scoped and carries the same RLS policy every
-- tenant-scoped table uses: a SELECT/UPDATE/DELETE goes through
-- `current_setting('app.current_tenant', false)`, and every query
-- runs inside a transaction preceded by `SET LOCAL
-- app.current_tenant`. The §12.8 GDPR erasure orchestrator targets
-- this table in the same step it covers other per-user state.

CREATE TABLE session_eviction_state (
    tenant_id            TEXT        NOT NULL,
    session_id           TEXT        NOT NULL,
    last_message_context BYTEA       NOT NULL,
    is_minio_key         BOOLEAN     NOT NULL DEFAULT false,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, session_id)
);

ALTER TABLE session_eviction_state ENABLE ROW LEVEL SECURITY;

CREATE POLICY session_eviction_state_tenant_isolation
    ON session_eviction_state
    USING (tenant_id = current_setting('app.current_tenant', false));
