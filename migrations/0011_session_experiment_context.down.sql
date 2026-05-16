-- Reverses 0011_session_experiment_context.
ALTER TABLE sessions
    DROP COLUMN IF EXISTS experiment_id,
    DROP COLUMN IF EXISTS experiment_variant_id,
    DROP COLUMN IF EXISTS experiment_inherited;
