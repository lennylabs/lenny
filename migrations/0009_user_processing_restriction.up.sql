-- The §12.8 / GDPR Article 18 processing-restriction flags on the
-- per-tenant user registry.
--
-- processing_restricted is set when a GDPR erasure job is initiated for
-- the user (POST /v1/admin/users/{user_id}/erase) and cleared when that
-- job completes. While set, new session creation is rejected with
-- ERASURE_IN_PROGRESS. A failed erasure job leaves the flag set.
-- erasure_job_id records the job that raised the restriction so the
-- rejection can surface it to the caller.

ALTER TABLE users
    ADD COLUMN processing_restricted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN erasure_job_id        TEXT    NOT NULL DEFAULT '';
