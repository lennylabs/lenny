-- §4.4 lines 236, 281 mandate soft-delete (UPDATE ... SET deleted_at =
-- now() ... WHERE deleted_at IS NULL) for the eviction-state row's
-- terminal-state cleanup so idempotent re-runs (stale-leader retries,
-- §12.5 GC backstop racing the primary cleanup) converge on a single
-- state mutation. The Postgres-backed EvictionStateStore previously
-- issued hard DELETE, violating the spec's monotonicity guard
-- (rows_affected == 0 on the second writer); the second writer would
-- silently re-issue MinIO deletes on the eviction-context object.
--
-- This migration adds the deleted_at tombstone column and the
-- supporting index for the §12.5 hard-prune sweep. The store layer
-- (pkg/gateway/evictionstatestore/pgstore) converts the Delete /
-- DeleteByUser / DeleteByTenant paths to soft-delete with the
-- `deleted_at IS NULL` predicate; the GC sweep walks rows whose
-- deleted_at is older than the tombstone retention window and issues
-- the hard DELETE.
--
-- spec: §4.4 lines 236, 281.

ALTER TABLE session_eviction_state
    ADD COLUMN deleted_at TIMESTAMPTZ;

-- §12.5 backstop sweep: walk every soft-deleted row that has aged out
-- of the tombstone window so the sweep can hard-prune it.
CREATE INDEX idx_session_eviction_state_deleted_at
    ON session_eviction_state (deleted_at)
    WHERE deleted_at IS NOT NULL;
