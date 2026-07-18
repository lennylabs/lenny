-- §10.1 lines 141-151: the checkpoint_manifest table carries the full
-- per-attempt upload record the gateway writes intent-row-first before
-- the first chunk PutObject and updates as chunks commit. It supersedes
-- the migration 0062 session_partial_checkpoint_manifest table, whose
-- seven-column set omitted the §10.1 pipeline columns (checkpoint_id,
-- coordination_generation, chunk_count, workspace_bytes_uploaded,
-- reserved_bytes, reservation_released_at, baseline_full_checkpoint_bytes,
-- chunk_size_bytes, chunk_object_key_prefix, checkpoint_started_at,
-- checkpoint_timeout_at, manifest_reason), and the migration 0150 partial
-- unique index, whose (tenant_id, session_id) scoping degraded the
-- §10.1 line 147 (session_id, slot_id) key for lack of a slot_id column.
-- There are no deployments carrying the old table, so this drops and
-- recreates rather than chaining ALTERs, a renamed column, a swapped
-- primary key, and a rebuilt index.
--
-- The table is tenant-scoped and carries the same §12.3 apparatus every
-- tenant-scoped table uses: the lenny_tenant_guard trigger fires on every
-- write, ENABLE/FORCE ROW LEVEL SECURITY with the lenny_tenant_isolation
-- policy filters SELECT/UPDATE/DELETE through
-- current_setting('app.current_tenant', false), and lenny_app gets the
-- standard grants.
--
-- spec: §10.1 lines 141-151, §12.3.

DROP INDEX IF EXISTS partial_manifest_active_uniq;
DROP POLICY IF EXISTS lenny_tenant_isolation ON session_partial_checkpoint_manifest;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_partial_checkpoint_manifest;
DROP TABLE IF EXISTS session_partial_checkpoint_manifest;

CREATE TABLE checkpoint_manifest (
    tenant_id                      TEXT        NOT NULL REFERENCES tenants(id),
    checkpoint_id                  UUID        NOT NULL,
    session_id                     TEXT        NOT NULL,
    -- slot_id is the §5.2 per-slot identifier, carried when the pool sets
    -- sessionPolicy.maxConcurrentSessions > 1; the sentinel 'default'
    -- keeps the (session_id, slot_id) scoping key well-defined for pools
    -- with maxConcurrentSessions: 1.
    slot_id                        TEXT        NOT NULL DEFAULT 'default',
    -- coordination_generation is the coordinator's fenced generation at
    -- intent-row INSERT time; the resume-selection query filters on
    -- max(coordination_generation) so a late-committed older-generation
    -- row cannot win against a fenced newer-generation writer under
    -- split-brain (§10.1 line 141, 155).
    coordination_generation        BIGINT      NOT NULL DEFAULT 0,
    -- recovery_generation is the session's recovery counter at the time
    -- the manifest row was written; captured so audit reconstruction and
    -- the §7.2 mid-resume terminal transitions can reason about the
    -- (coordination, recovery) tuple that authored the partial chunks.
    recovery_generation            BIGINT      NOT NULL DEFAULT 0,
    partial                        BOOLEAN     NOT NULL DEFAULT TRUE,
    -- manifest_reason is the §10.1 line 141 closed enum of the row's
    -- disposition. It carries 'in_progress' from intent-row INSERT until a
    -- terminal arm overwrites it with 'complete', 'timeout',
    -- 'stream_truncated', 'superseded', or 'quota_exceeded'. NOT NULL: the
    -- intent row always has a disposition. Cleanup predicates are
    -- reason-agnostic (they key on partial = TRUE AND deleted_at IS NULL),
    -- so the column is carried for audit and the §16.1
    -- lenny_checkpoint_partial_total label domain.
    manifest_reason                TEXT        NOT NULL DEFAULT 'in_progress',
    -- chunk_object_key_prefix is the
    -- /{tenant_id}/checkpoints/{session_id}/{checkpoint_id}/ prefix under
    -- which chunks are listed; captured at intent-row INSERT and never
    -- modified after.
    chunk_object_key_prefix        TEXT        NOT NULL,
    -- chunk_size_bytes is the partialChunkSizeBytes value in effect for
    -- this manifest, captured at intent-row INSERT for forward-
    -- compatibility if the default changes.
    chunk_size_bytes               BIGINT      NOT NULL,
    -- chunk_encoding is 'tar' or 'tar.gz', captured at intent-row INSERT;
    -- the resume path selects the decoder strictly from this column.
    chunk_encoding                 TEXT        NOT NULL DEFAULT 'tar',
    -- chunk_count is the number of chunks committed (highest {n} + 1);
    -- initialised to 0 at intent-row INSERT and updated monotonically as
    -- chunks commit. chunk_count = 0 is a valid initial state the §12.5
    -- backstop treats as "no chunks to delete, only the row to
    -- soft-delete".
    chunk_count                    INTEGER     NOT NULL DEFAULT 0,
    -- workspace_bytes_uploaded is the sum of chunk sizes successfully
    -- committed to MinIO; initialised to 0 at intent-row INSERT and
    -- updated monotonically as chunks commit.
    workspace_bytes_uploaded       BIGINT      NOT NULL DEFAULT 0,
    -- reserved_bytes is the §11.2 storage reservation taken from the
    -- adapter's probe at intent-row INSERT, so any party that finalises or
    -- sweeps the row can release it.
    reserved_bytes                 BIGINT      NOT NULL DEFAULT 0,
    -- reservation_released_at is the exactly-once release guard for that
    -- reservation across the in-process finalisation arms and the §12.5
    -- backstop; set once by the first releasing arm under a
    -- WHERE reservation_released_at IS NULL guard.
    reservation_released_at        TIMESTAMPTZ,
    -- baseline_full_checkpoint_bytes is the session's
    -- last_checkpoint_workspace_bytes at intent-row INSERT time, frozen
    -- for the life of the row and used as the denominator for both the
    -- resume-time threshold check and the post-extraction
    -- workspaceRecoveryFraction. NULL is load-bearing: it denotes a
    -- session with no prior successful full checkpoint, in which case
    -- §10.1 line 155 degenerates the threshold check to "any non-zero
    -- workspace_bytes_uploaded is eligible" and §7.2 omits
    -- workspaceRecoveryFraction from session.resumed. It carries no
    -- DEFAULT so the IS NULL predicate stays reachable and the resume path
    -- never divides by zero.
    baseline_full_checkpoint_bytes BIGINT      NULL,
    checkpoint_started_at          TIMESTAMPTZ NOT NULL,
    checkpoint_timeout_at          TIMESTAMPTZ NOT NULL,
    created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- deleted_at is the §12.5 soft-delete tombstone. The primary cleanup
    -- path and the backstop sweep both issue
    -- UPDATE ... SET deleted_at = now() ... WHERE ... AND deleted_at IS NULL
    -- so stale-leader retries and GC-backstop races converge to a single
    -- state mutation; the artifact_store tombstone sweep hard-prunes rows
    -- whose deleted_at is older than the retention window.
    deleted_at                     TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, checkpoint_id)
);

-- §10.1 lines 143-151: the at-most-one-active-partial-manifest invariant.
-- Scoped on (session_id, slot_id) and restricted to partial rows so it
-- does not conflict with coexisting full-checkpoint rows in the same
-- table; the deleted_at IS NULL predicate lets soft-deleted rows remain
-- until the §12.5 hard-prune sweep removes them.
CREATE UNIQUE INDEX partial_manifest_active_uniq
    ON checkpoint_manifest (session_id, slot_id)
    WHERE partial = TRUE AND deleted_at IS NULL;

-- Resume-selection index: walk the active manifest for a
-- (tenant, session, slot) by descending coordination_generation so the
-- resume path reads the highest-fenced row.
CREATE INDEX idx_checkpoint_manifest_active
    ON checkpoint_manifest (tenant_id, session_id, slot_id, coordination_generation DESC)
    WHERE deleted_at IS NULL;

-- §12.5 backstop sweep: walk every soft-deleted row that has aged out of
-- the tombstone window so the sweep can hard-prune it in tandem with
-- artifact_store.
CREATE INDEX idx_checkpoint_manifest_deleted_at
    ON checkpoint_manifest (deleted_at)
    WHERE deleted_at IS NOT NULL;

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON checkpoint_manifest
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE checkpoint_manifest ENABLE ROW LEVEL SECURITY;
ALTER TABLE checkpoint_manifest FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON checkpoint_manifest
    USING (tenant_id = current_setting('app.current_tenant', false));

GRANT SELECT, INSERT, UPDATE, DELETE ON checkpoint_manifest TO lenny_app;
