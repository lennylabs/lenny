-- Reverses 0192_issued_tokens_impersonation_index.
-- spec: §12.2 (TokenIssuanceStore indexes).
DROP INDEX IF EXISTS idx_issued_tokens_impersonation;
