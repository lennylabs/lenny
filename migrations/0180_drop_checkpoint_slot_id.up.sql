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
-- preserving.
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
-- without partially applying it. Both drops are idempotent
-- (DROP COLUMN IF EXISTS), so a re-run after the gate passes is a no-op.

-- gate-index: idx_session_checkpoints_slot_id_unmigrated
CREATE INDEX IF NOT EXISTS idx_session_checkpoints_slot_id_unmigrated
    ON session_checkpoints (slot_id)
    WHERE slot_id <> '' AND slot_id <> session_id;
DO $$
DECLARE remaining bigint;
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'session_checkpoints'
          AND column_name = 'slot_id'
    ) THEN
        SELECT COUNT(*) INTO remaining
        FROM session_checkpoints
        WHERE slot_id <> '' AND slot_id <> session_id;
        IF remaining > 0 THEN
            RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in session_checkpoints.slot_id. Resolve data migration before retrying.', remaining;
        END IF;
    END IF;
END $$;
-- The covering gate index is dropped together with the column it guards.
DROP INDEX IF EXISTS idx_session_checkpoints_slot_id_unmigrated;

-- Re-point the rotation index from (tenant_id, session_id, slot_id,
-- created_at DESC) to (tenant_id, session_id, created_at DESC): the §12.5
-- "latest 2" cap applies per session.
DROP INDEX IF EXISTS idx_session_checkpoints_slot_age;
ALTER TABLE session_checkpoints
    DROP COLUMN IF EXISTS slot_id;
CREATE INDEX idx_session_checkpoints_session_age
    ON session_checkpoints (tenant_id, session_id, created_at DESC);

-- gate-index: idx_checkpoint_manifest_slot_id_unmigrated
CREATE INDEX IF NOT EXISTS idx_checkpoint_manifest_slot_id_unmigrated
    ON checkpoint_manifest (slot_id)
    WHERE slot_id <> 'default' AND slot_id <> session_id;
DO $$
DECLARE remaining bigint;
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'checkpoint_manifest'
          AND column_name = 'slot_id'
    ) THEN
        SELECT COUNT(*) INTO remaining
        FROM checkpoint_manifest
        WHERE slot_id <> 'default' AND slot_id <> session_id;
        IF remaining > 0 THEN
            RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in checkpoint_manifest.slot_id. Resolve data migration before retrying.', remaining;
        END IF;
    END IF;
END $$;
-- The covering gate index is dropped together with the column it guards.
DROP INDEX IF EXISTS idx_checkpoint_manifest_slot_id_unmigrated;

-- Re-key the at-most-one-active-partial invariant and the resume-selection
-- walk on session_id alone (§10.1).
DROP INDEX IF EXISTS partial_manifest_active_uniq;
DROP INDEX IF EXISTS idx_checkpoint_manifest_active;
ALTER TABLE checkpoint_manifest
    DROP COLUMN IF EXISTS slot_id;
CREATE UNIQUE INDEX partial_manifest_active_uniq
    ON checkpoint_manifest (session_id)
    WHERE partial = TRUE AND deleted_at IS NULL;
CREATE INDEX idx_checkpoint_manifest_active
    ON checkpoint_manifest (tenant_id, session_id, coordination_generation DESC)
    WHERE deleted_at IS NULL;

-- Rewrite the recorded workspace root of every session still holding the
-- retired pod-global path onto its own slot root (§6.4, §7.3 step (d)).
UPDATE sessions
    SET workspace_root = '/workspace/slots/' || id::text || '/current'
    WHERE workspace_root = '/workspace/current';
