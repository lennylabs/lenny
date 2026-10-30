-- Reverses 0042_erasure_jobs_processing_restriction_trigger: drops the
-- §12.8 processing-restriction trigger and the erasure_jobs registry,
-- and revokes the lenny_erasure grants this migration added.

REVOKE SELECT, DELETE, UPDATE (processing_restricted, erasure_job_id) ON users FROM lenny_erasure;

DROP TRIGGER IF EXISTS lenny_processing_restriction_guard ON users;
DROP FUNCTION IF EXISTS lenny_processing_restriction_guard();

-- erasure_jobs and its triggers, policy, and indexes. DROP TABLE
-- removes the lenny_tenant_guard trigger, the lenny_tenant_isolation
-- policy, the erasure_jobs_user_status_idx index, and the
-- erasure_jobs-scoped grants along with the table.
DROP TABLE IF EXISTS erasure_jobs;
