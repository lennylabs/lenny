-- §5.1 / §5.2 — the runtime registry mirror of the derived restartable
-- flag the RuntimeReconciler computes from Runtime.spec.
--
-- restartable is true for a runtime that carries command, has
-- executionMode session, and does not declare capabilities.preConnect
-- (§5.1 command). Gateway pool admission reads it to refuse
-- sessionPolicy.recycle.runtimeProcess: restart on a runtime that cannot
-- launch a fresh runtime process at each reused boundary (§5.2). The
-- RuntimeReconciler is the only writer: command is settable only on the
-- Runtime CRD, so a row the reconciler has not mirrored, including one
-- registered only through the admin API, keeps the false default and
-- refuses restart. Stored as a plain BOOLEAN column beside
-- require_so_peercred so the admission lookup is a single-column read.
ALTER TABLE runtime_definitions
    ADD COLUMN restartable BOOLEAN NOT NULL DEFAULT false;
