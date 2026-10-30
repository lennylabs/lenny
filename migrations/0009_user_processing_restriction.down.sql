-- Reverses 0009_user_processing_restriction.
ALTER TABLE users
    DROP COLUMN IF EXISTS processing_restricted,
    DROP COLUMN IF EXISTS erasure_job_id;
