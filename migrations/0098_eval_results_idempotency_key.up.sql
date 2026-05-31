-- §10.7 lines 939-940 — the eval-submission contract accepts an
-- optional `idempotency_key` (≤128 bytes) on POST /v1/sessions/{id}/eval.
-- A repeat submission carrying the same key for the same session within
-- 24h resolves to the originally-stored record rather than inserting a
-- duplicate. The key is persisted on the eval row so the gateway's dedup
-- lookup can match a later submission; a keyless submission stores NULL.
-- F-10.7.4.
ALTER TABLE eval_results
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

-- idx_eval_results_idempotency backs the dedup lookup: newest in-window
-- row for (tenant_id, session_id, idempotency_key). Partial on the
-- non-null keys so keyless submissions never enter the index.
CREATE INDEX IF NOT EXISTS idx_eval_results_idempotency
    ON eval_results (tenant_id, session_id, idempotency_key, created_at)
    WHERE idempotency_key IS NOT NULL;
