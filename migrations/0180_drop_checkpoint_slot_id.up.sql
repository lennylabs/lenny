-- §10.1 / §12.5 — drop the persisted duplicate slot identifier from the two
-- checkpoint tables and re-key the three indexes that carried it.
--
-- Every session is bound to a slot on every pod and a session-mode slot's
-- identifier is its session's identifier, so session_checkpoints.slot_id and
-- checkpoint_manifest.slot_id carry nothing beyond session_id. The drop is
-- unconditional and there is no backfill: no row carries a value worth
-- preserving, whatever spelling it holds, so no column value can block the
-- contract. The retention cap, the at-most-one-active-partial invariant, and
-- the resume-selection walk are all keyed on session_id alone afterwards.
--
-- Re-keying the at-most-one-active-partial index does narrow the uniqueness
-- scope, so the one preflight gate this migration carries refuses when a
-- session holds more than one active partial row, naming the sessions an
-- operator must abort first.
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

-- The whole up-file runs in one transaction (pkg/schemamigrate,
-- migratepg.WithInstance), so a RAISE EXCEPTION rolls the migration back
-- without partially applying it. The whole file is idempotent: the drops are
-- DROP COLUMN IF EXISTS, each re-keyed index is created with IF NOT EXISTS,
-- the policy is restated through DROP POLICY IF EXISTS, and the gate reads
-- without writing. A re-run therefore changes nothing.

-- checkpoint_manifest carries the §12.3 strict isolation policy, whose USING
-- clause matches tenant_id against app.current_tenant with no cross-tenant
-- form. The gate below reads across every tenant, and a migration role that
-- is not a superuser is subject to the policy under FORCE ROW LEVEL SECURITY,
-- so that one read needs the platform cross-tenant sentinel the §4.2 / §12.3
-- policies elsewhere already admit. Widen the policy to the sentinel form for
-- the duration of the gate read only: the '__all__' bypass is admitted only
-- inside a transaction that has also taken the lenny.allow_all_sentinel
-- opt-in, and the strict form migration 0178 created is put back as soon as
-- the gate has run. The steady-state schema this migration leaves behind
-- differs from the pre-0180 schema only in the columns and indexes §10.1
-- names; the isolation policy of this table is not one of its effects.
DROP POLICY IF EXISTS lenny_tenant_isolation ON checkpoint_manifest;
CREATE POLICY lenny_tenant_isolation ON checkpoint_manifest
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR (
            current_setting('app.current_tenant', false) = '__all__'
            AND current_setting('lenny.allow_all_sentinel', true) = 'true'
        )
    );

-- Take the cross-tenant sentinel for the rest of the migration: the gate
-- below reads checkpoint_manifest across every tenant, and the sessions
-- rewrite at the foot writes across every tenant through the §12.3 tenant
-- guard trigger, which rejects any write made with no app.current_tenant set.
-- Both settings are SET LOCAL, so they are confined to this migration's own
-- transaction and no session inherits them.
SET LOCAL lenny.allow_all_sentinel = 'true';
SET LOCAL app.current_tenant = '__all__';

-- §10.5 preflight gate: refuse the contract when any session holds more than
-- one active partial checkpoint_manifest row (§10.1).
--
-- gate-index: idx_checkpoint_manifest_active_partial_gate
--
-- The old unique index was scoped on (session_id, slot_id), so one session
-- could hold two active partial rows under different slot_id values: a
-- crashed attempt that wrote slot_id = 'default' is never superseded by a
-- later attempt on a pod that wrote slot_id = session_id. The re-keyed index
-- admits one such row per session and would otherwise fail with a bare unique
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
    CREATE INDEX IF NOT EXISTS idx_checkpoint_manifest_active_partial_gate
        ON checkpoint_manifest (session_id)
        WHERE partial = TRUE AND deleted_at IS NULL;
    SELECT string_agg(session_id, ', ' ORDER BY session_id) INTO duplicated
    FROM (
        SELECT session_id
        FROM checkpoint_manifest
        WHERE partial = TRUE AND deleted_at IS NULL
        GROUP BY session_id
        HAVING count(*) > 1
    ) d;
    -- The gate index covers this migration's own read only.
    DROP INDEX IF EXISTS idx_checkpoint_manifest_active_partial_gate;
    IF duplicated IS NOT NULL THEN
        RAISE EXCEPTION 'Phase 3 gate failed: more than one active partial checkpoint_manifest row for session(s) %. Abort the extra attempts before retrying.', duplicated;
    END IF;
END $$;

-- The gate read is done. Restore checkpoint_manifest's §12.3 isolation policy
-- to the strict form migration 0178 created, so this migration leaves no
-- durable widening of the table's tenant predicate behind. Nothing below
-- reads checkpoint_manifest: the index re-key is DDL, which RLS does not
-- filter, and the sessions rewrite at the foot needs only the §12.3 tenant
-- guard, which the app.current_tenant sentinel still in force satisfies.
DROP POLICY IF EXISTS lenny_tenant_isolation ON checkpoint_manifest;
CREATE POLICY lenny_tenant_isolation ON checkpoint_manifest
    USING (tenant_id = current_setting('app.current_tenant', false));

-- Re-point the rotation index from (tenant_id, session_id, slot_id,
-- created_at DESC) to (tenant_id, session_id, created_at DESC): the §12.5
-- "latest 2" cap applies per session.
DROP INDEX IF EXISTS idx_session_checkpoints_slot_age;
ALTER TABLE session_checkpoints
    DROP COLUMN IF EXISTS slot_id;
CREATE INDEX IF NOT EXISTS idx_session_checkpoints_session_age
    ON session_checkpoints (tenant_id, session_id, created_at DESC);

-- Re-key the at-most-one-active-partial invariant and the resume-selection
-- walk on session_id alone (§10.1).
DROP INDEX IF EXISTS partial_manifest_active_uniq;
DROP INDEX IF EXISTS idx_checkpoint_manifest_active;
ALTER TABLE checkpoint_manifest
    DROP COLUMN IF EXISTS slot_id;
CREATE UNIQUE INDEX IF NOT EXISTS partial_manifest_active_uniq
    ON checkpoint_manifest (session_id)
    WHERE partial = TRUE AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_checkpoint_manifest_active
    ON checkpoint_manifest (tenant_id, session_id, coordination_generation DESC)
    WHERE deleted_at IS NULL;

-- Rewrite the recorded workspace root of every session still holding the
-- retired pod-global path onto its own slot root (§6.4, §7.3 step (d)).
UPDATE sessions
    SET workspace_root = '/workspace/slots/' || id::text || '/current'
    WHERE workspace_root = '/workspace/current';

SET LOCAL app.current_tenant TO DEFAULT;
SET LOCAL lenny.allow_all_sentinel TO DEFAULT;
