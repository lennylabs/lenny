-- A plain expires_at projection column on the §4.9 credential-lease
-- store.
--
-- Migration 0129 moved the lease body to an AES-256-GCM envelope-encrypted
-- BYTEA blob, so the lease's expiry can no longer be read without decrypting
-- every row through a KMS unwrap. Two §4.9 deny-list paths need to reason
-- about lease expiry without that decrypt:
--
--   * the bounded expired-lease sweep, which deletes rows whose lease is past
--     ExpiresAt (spec §4.9 line 1671 — deny-list entries "expire when the
--     credential's natural lease TTL lapses"), and
--   * the fail-closed lease-existence count that gates deny-list-entry
--     removal and the startup rebuild filter, which counts leases still
--     active as of a cutoff.
--
-- Neither can query the encrypted blob, so expires_at is projected out of the
-- lease body as a plain indexed column. Write.Put populates it from
-- lease.ExpiresAt.
--
-- The column is nullable: a row written before this migration carries a NULL
-- expires_at (backfilled by a one-time pass in a later migration). A NULL is
-- an unknown-expiry sentinel, counted as active so the existence guard fails
-- closed rather than treating a pre-backfill lease as already expired.
ALTER TABLE credential_leases
    ADD COLUMN expires_at TIMESTAMPTZ;

-- The sweep's DeleteExpired and the LeasesByCredentialCount existence query
-- both filter on expires_at, so it carries an index.
CREATE INDEX credential_leases_expires_at_idx
    ON credential_leases (expires_at);
