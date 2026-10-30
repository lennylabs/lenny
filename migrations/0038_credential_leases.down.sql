-- Reverses 0038_credential_leases. Dropping the table cascades the
-- idx_credential_leases_token index and the lenny_app grants.
DROP TABLE IF EXISTS credential_leases;
