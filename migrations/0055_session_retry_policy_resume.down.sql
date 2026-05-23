-- Reverses 0055_session_retry_policy_resume.

ALTER TABLE sessions
    DROP COLUMN IF EXISTS retry_count,
    DROP COLUMN IF EXISTS policy_enforcement_state,
    DROP COLUMN IF EXISTS resume_eligible_until;
