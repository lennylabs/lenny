-- Reverses 0175_credential_leases_expires_at.
--
-- Drops the expires_at projection index and column. The lease body's
-- envelope-encrypted ExpiresAt is unaffected, so no data is lost.
DROP INDEX IF EXISTS credential_leases_expires_at_idx;

ALTER TABLE credential_leases
    DROP COLUMN IF EXISTS expires_at;
