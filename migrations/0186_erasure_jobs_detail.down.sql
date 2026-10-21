-- Reverses 0186_erasure_jobs_detail: drops the one-in-progress-job-per-user
-- partial unique index and the erasure_jobs.detail column.
DROP INDEX IF EXISTS idx_erasure_jobs_in_progress_per_user;
ALTER TABLE erasure_jobs DROP COLUMN IF EXISTS detail;
