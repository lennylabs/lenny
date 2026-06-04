-- §12.8 lines 843-850 ("`erasure_salt` key management"): the per-tenant
-- billing-pseudonymization salt is stored in the tenant configuration
-- record in Postgres, encrypted at rest using KMS envelope encryption
-- (the same pattern used for OAuth refresh tokens). The salt is never
-- stored in plaintext.
--
-- The column holds the §4 envelope.Encode blob (KEK version + KMS-wrapped
-- DEK + GCM nonce + ciphertext), so a database dump exposes only the
-- wrapped form. A NULL value is the §12.8 line 850 destroyed state: the
-- immediate-deletion-after-pseudonymization sequence sets it to NULL once
-- the user's billing events are pseudonymized.

ALTER TABLE tenants
    ADD COLUMN erasure_salt BYTEA;
