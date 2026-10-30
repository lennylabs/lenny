-- Reverses 0127_ops_restore_state_completion.up.sql.
ALTER TABLE ops_restore_state
    DROP COLUMN IF EXISTS ledger_confirmed_justification,
    DROP COLUMN IF EXISTS ledger_confirmed_by,
    DROP COLUMN IF EXISTS ledger_confirmed_at,
    DROP COLUMN IF EXISTS job_id;
