-- §4.9 — drop the persisted duplicate slot identifier from the two
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
-- uniqueness scope, so the migration first collapses each session's
-- pre-existing active partial rows onto one survivor the way supersede-on-
-- write does.
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
-- spec: §4.9, §6.4, §7.3, §10.1, §12.5.

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
-- IF NOT EXISTS, and the collapse below is a no-op once one active partial
-- row per session is all that remains. A re-run after the gate passes
-- therefore changes nothing.

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
-- Collapse the pre-existing active partial rows of each session onto one
-- survivor before the re-keyed unique index lands (§10.1.7 supersede).
--
-- The old unique index was scoped on (session_id, slot_id), so one session
-- could legitimately hold two active partial rows under different slot_id
-- values: a crashed attempt that wrote slot_id = 'default' is never superseded
-- by a later attempt on a pod that wrote slot_id = session_id. Both rows pass
-- the gate above, because each value is the table's sentinel or a copy of
-- session_id, and the re-keyed index would then fail with a bare unique
-- violation that names nothing an operator can act on. The supersede rule
-- already states how the state resolves: the highest
-- (coordination_generation, created_at) row survives and every other active
-- partial row of that session is soft-deleted as superseded, which is what
-- supersede-on-write does for every attempt after this migration.
--
-- checkpoint_manifest carries the §12.3 tenant-guard trigger, so the
-- cross-tenant rewrite takes the platform sentinel with the explicit opt-in
-- migration 0057 requires. Both settings are SET LOCAL and end with this
-- migration's transaction.
SET LOCAL lenny.allow_all_sentinel = 'true';
SET LOCAL app.current_tenant = '__all__';
WITH survivor AS (
    SELECT DISTINCT ON (tenant_id, session_id) tenant_id, session_id, checkpoint_id
    FROM checkpoint_manifest
    WHERE partial = TRUE AND deleted_at IS NULL
    ORDER BY tenant_id, session_id, coordination_generation DESC, created_at DESC
)
UPDATE checkpoint_manifest m
    SET deleted_at = now(), manifest_reason = 'superseded'
    FROM survivor s
    WHERE m.tenant_id = s.tenant_id
      AND m.session_id = s.session_id
      AND m.partial = TRUE
      AND m.deleted_at IS NULL
      AND m.checkpoint_id <> s.checkpoint_id;
SET LOCAL app.current_tenant TO DEFAULT;
SET LOCAL lenny.allow_all_sentinel TO DEFAULT;

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
