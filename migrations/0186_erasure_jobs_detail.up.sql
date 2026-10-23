-- spec: §12.8 (Compliance Interfaces: user-level erasure invocation, erasure
-- job phase field)
--
-- Erasure job record detail and one in-progress job per user.
--
-- The detail column holds the erasure-job fields no query filters on
-- (deadline, per-store deletion counts, billing outcome, phase log,
-- initiator, override receipt, and receipt timestamps), serialized by
-- the Postgres erasure-job store. The columns migration 0042 created
-- keep the fields the runner and the processing-restriction trigger
-- read.
--
-- §12.8 states that at most one erasure job per user is in progress at
-- a time. The partial unique index holds that at the database: a second
-- in-progress row for the same (tenant_id, user_id) fails with
-- unique_violation, and the store's start path inserts with
-- ON CONFLICT on this index. Completed and failed jobs fall outside the
-- predicate, so a user keeps any number of terminal jobs beside one
-- in-progress job.
--
-- The index predicate must stay identical to the in-progress values of
-- erasure_jobs_status_check and to the in-progress set the
-- lenny_processing_restriction_guard trigger tests (both in 0042). A
-- value added to one and not the others lets a second job start, or
-- lets the restriction clear while a job runs.
ALTER TABLE erasure_jobs ADD COLUMN detail JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE UNIQUE INDEX idx_erasure_jobs_in_progress_per_user
    ON erasure_jobs (tenant_id, user_id)
    WHERE status IN ('initiated', 'store_deleting', 'pseudonymizing', 'verifying');
