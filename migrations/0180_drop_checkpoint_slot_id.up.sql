-- §10.1 / §12.5 — drop the persisted duplicate slot identifier from the two
-- checkpoint tables and re-key the three indexes that carried it.
--
-- Every session is bound to a slot on every pod and a session-mode slot's
-- identifier is its session's identifier, so session_checkpoints.slot_id and
-- checkpoint_manifest.slot_id hold either a sentinel ('' and 'default'
-- respectively) or a copy of session_id. Neither spelling carries information
-- beyond session_id, so the retention cap, the at-most-one-active-partial
-- invariant, and the resume-selection walk are all keyed on session_id alone
-- after this migration. There is no backfill: no row carries a value worth
-- preserving. Re-keying the at-most-one-active-partial index does narrow the
-- uniqueness scope, so a third gate refuses the migration when any session
-- holds more than one active partial row, naming the sessions an operator
-- must retire first.
--
-- The migration also rewrites sessions.workspace_root, which recorded the
-- retired pod-global /workspace/current path. Under the per-slot layout the
-- adapter reports a slot root, and the §7.3 step (d) guard compares the
-- persisted value against the root the replacement pod resolves, so a row
-- still holding /workspace/current would fail every resume. The rewrite
-- derives each row's slot root from the session's own identifier. Rows
-- holding the empty column default and rows already holding a slot root are
-- left untouched.
--
-- spec: §6.4, §7.3, §10.1, §12.5.

-- §10.5 Phase 3 column drops. Each drop is fronted by a PL/pgSQL DO $$
-- preflight gate that counts rows whose slot_id carries anything other than
-- the table's sentinel or a copy of session_id, and RAISE EXCEPTIONs when any
-- remain: such a row would be the only place a slot dimension the session
-- identifier does not express survives, and dropping the column would lose it.
-- The whole up-file runs in one transaction (pkg/schemamigrate,
-- migratepg.WithInstance), so a RAISE EXCEPTION rolls the migration back
-- without partially applying it. The whole file is idempotent: the drops are
-- DROP COLUMN IF EXISTS, every statement that names a dropped column sits
-- inside an information_schema guard, each re-keyed index is created with
-- IF NOT EXISTS, and the uniqueness gate below reads without writing. A
-- re-run after the gates pass therefore changes nothing.

-- gate-index: idx_session_checkpoints_slot_id_unmigrated
DO $$
DECLARE remaining bigint;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'session_checkpoints'
          AND column_name = 'slot_id'
    ) THEN
        -- The column is already gone, so this is a re-run: nothing to count
        -- and nothing to index. Every statement below names slot_id, so the
        -- gate index is created and dropped inside the guard rather than
        -- beside it; an unguarded CREATE INDEX would abort the re-run with an
        -- undefined-column error, and IF NOT EXISTS suppresses only a
        -- duplicate index name.
        RETURN;
    END IF;
    EXECUTE 'CREATE INDEX IF NOT EXISTS idx_session_checkpoints_slot_id_unmigrated
        ON session_checkpoints (slot_id)
        WHERE slot_id <> '''' AND slot_id <> session_id';
    EXECUTE 'SELECT COUNT(*) FROM session_checkpoints
        WHERE slot_id <> '''' AND slot_id <> session_id' INTO remaining;
    -- The covering gate index is dropped together with the column it guards.
    EXECUTE 'DROP INDEX IF EXISTS idx_session_checkpoints_slot_id_unmigrated';
    IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in session_checkpoints.slot_id. Resolve data migration before retrying.', remaining;
    END IF;
END $$;

-- Re-point the rotation index from (tenant_id, session_id, slot_id,
-- created_at DESC) to (tenant_id, session_id, created_at DESC): the §12.5
-- "latest 2" cap applies per session.
DROP INDEX IF EXISTS idx_session_checkpoints_slot_age;
ALTER TABLE session_checkpoints
    DROP COLUMN IF EXISTS slot_id;
CREATE INDEX IF NOT EXISTS idx_session_checkpoints_session_age
    ON session_checkpoints (tenant_id, session_id, created_at DESC);

-- gate-index: idx_checkpoint_manifest_slot_id_unmigrated
DO $$
DECLARE remaining bigint;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'checkpoint_manifest'
          AND column_name = 'slot_id'
    ) THEN
        -- Already dropped: this is a re-run. The gate index names the dropped
        -- column, so it is created and dropped inside the guard.
        RETURN;
    END IF;
    EXECUTE 'CREATE INDEX IF NOT EXISTS idx_checkpoint_manifest_slot_id_unmigrated
        ON checkpoint_manifest (slot_id)
        WHERE slot_id <> ''default'' AND slot_id <> session_id';
    EXECUTE 'SELECT COUNT(*) FROM checkpoint_manifest
        WHERE slot_id <> ''default'' AND slot_id <> session_id' INTO remaining;
    -- The covering gate index is dropped together with the column it guards.
    EXECUTE 'DROP INDEX IF EXISTS idx_checkpoint_manifest_slot_id_unmigrated';
    IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in checkpoint_manifest.slot_id. Resolve data migration before retrying.', remaining;
    END IF;
END $$;

-- Re-key the at-most-one-active-partial invariant and the resume-selection
-- walk on session_id alone (§10.1).
DROP INDEX IF EXISTS partial_manifest_active_uniq;
DROP INDEX IF EXISTS idx_checkpoint_manifest_active;
ALTER TABLE checkpoint_manifest
    DROP COLUMN IF EXISTS slot_id;
-- Gate the narrowed uniqueness scope: refuse the migration when any session
-- holds more than one active partial row (§10.1).
--
-- The old unique index was scoped on (session_id, slot_id), so one session
-- could hold two active partial rows under different slot_id values: a
-- crashed attempt that wrote slot_id = 'default' is never superseded by a
-- later attempt on a pod that wrote slot_id = session_id. Both rows pass the
-- gate above, because each value is the table's sentinel or a copy of
-- session_id, and the re-keyed index would then fail with a bare unique
-- violation that names nothing an operator can act on.
--
-- The gate refuses rather than retiring the extra rows itself. Retiring an
-- active partial attempt is three operations, and only one of them is SQL a
-- migration can issue: the §11.2 reservation release under the exactly-once
-- reservation_released_at guard with its tenant storage-counter decrement,
-- the release of the attempt's confirmed chunk objects and their
-- artifact_store rows under its chunk_object_key_prefix, and the soft-delete
-- of the manifest row itself. Soft-deleting alone is terminal: the §12.5
-- backstop selects partial = TRUE AND deleted_at IS NULL, so no later sweep
-- reaches a row a migration retired, and its reserved bytes and chunk objects
-- stay charged to the tenant forever. An operator retires the extra attempt
-- through the abort path, which performs all three, and then re-runs this
-- migration. The gate is keyed on session_id alone, the same column set as
-- the index below, so a duplicate held by two tenants under one session
-- identifier is refused here rather than surfacing as that unique violation.
DO $$
DECLARE duplicated TEXT;
BEGIN
    SELECT string_agg(session_id, ', ' ORDER BY session_id) INTO duplicated
    FROM (
        SELECT session_id
        FROM checkpoint_manifest
        WHERE partial = TRUE AND deleted_at IS NULL
        GROUP BY session_id
        HAVING count(*) > 1
    ) d;
    IF duplicated IS NOT NULL THEN
        RAISE EXCEPTION 'Phase 3 gate failed: more than one active partial checkpoint_manifest row for session(s) %. Abort the extra attempts before retrying.', duplicated;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS partial_manifest_active_uniq
    ON checkpoint_manifest (session_id)
    WHERE partial = TRUE AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_checkpoint_manifest_active
    ON checkpoint_manifest (tenant_id, session_id, coordination_generation DESC)
    WHERE deleted_at IS NULL;

-- Rewrite the recorded workspace root of every session still holding the
-- retired pod-global path onto its own slot root (§6.4, §7.3 step (d)).
--
-- sessions carries the §12.3 tenant-guard trigger, which rejects any write
-- made with no app.current_tenant set. A migration runs outside a tenant
-- context and rewrites rows across every tenant, so it takes the platform
-- cross-tenant sentinel with the explicit opt-in migration 0057 requires.
-- Both settings are SET LOCAL, so they are confined to this migration's own
-- transaction and no session inherits them.
SET LOCAL lenny.allow_all_sentinel = 'true';
SET LOCAL app.current_tenant = '__all__';
UPDATE sessions
    SET workspace_root = '/workspace/slots/' || id::text || '/current'
    WHERE workspace_root = '/workspace/current';
SET LOCAL app.current_tenant TO DEFAULT;
SET LOCAL lenny.allow_all_sentinel TO DEFAULT;
