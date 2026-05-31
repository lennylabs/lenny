DROP INDEX IF EXISTS idx_eval_results_idempotency;
ALTER TABLE eval_results DROP COLUMN IF EXISTS idempotency_key;
