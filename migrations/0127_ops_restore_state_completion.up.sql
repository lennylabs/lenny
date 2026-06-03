-- §25.11 Restore Execution steps 4-8 added columns to ops_restore_state
-- after migration 0123 shipped: the restore Kubernetes Job name the
-- completion reconciler polls (job_id, §25.11 line 4145), and the
-- legal-hold ledger confirmation watermark an operator records via
-- POST /v1/admin/restore/{id}/confirm-legal-hold-ledger to clear a
-- gdpr.backup_reconcile_blocked stall (§25.11 line 4147, §12.8 phase 2).
-- The Postgres-backed backup Store round-trips these on the
-- RestoreState row; without them the completion reconciler cannot
-- correlate a restore with its Job and the operator confirmation is lost
-- on a lenny-ops restart.
--
-- ops_restore_state is platform-scoped (§25.4 line 1492), so no tenant
-- column or RLS policy applies.
--
-- spec: §25.11 lines 4145-4149, 4194.

ALTER TABLE ops_restore_state
    ADD COLUMN job_id                          TEXT NOT NULL DEFAULT '',
    ADD COLUMN ledger_confirmed_at             TIMESTAMPTZ,
    ADD COLUMN ledger_confirmed_by             TEXT NOT NULL DEFAULT '',
    ADD COLUMN ledger_confirmed_justification  TEXT NOT NULL DEFAULT '';
