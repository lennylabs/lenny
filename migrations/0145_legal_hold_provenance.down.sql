ALTER TABLE sessions
    DROP COLUMN IF EXISTS legal_hold_set_by,
    DROP COLUMN IF EXISTS legal_hold_set_at,
    DROP COLUMN IF EXISTS legal_hold_note;

ALTER TABLE artifact_store
    DROP COLUMN IF EXISTS legal_hold_set_by,
    DROP COLUMN IF EXISTS legal_hold_set_at,
    DROP COLUMN IF EXISTS legal_hold_note;
