-- §14 — the CreateSessionRequest envelope carries client-supplied fields
-- beyond runtimeRef / workspacePlan: `env` (environment variables), and
-- the §14.1 outer-envelope fields `pool`, `timeouts`, `credentialPolicy`,
-- `delegationLease`, and `runtimeOptions`. The gateway validates each at
-- admission and preserves it verbatim across the session lifetime so a
-- client that lost the create response can recover the request envelope
-- on GET /v1/sessions/{id}.
--
-- `env` is a flat string→string map stored as JSONB; every key passed
-- the deployer blocklist at admission (F-14.1.12). `request_envelope`
-- bundles the §14.1 envelope fields (pool, timeouts, credentialPolicy,
-- delegationLease, runtimeOptions) into a single JSONB object so adding
-- the surface costs one column rather than five (F-14.1.14 / F-14.1.15).
-- An absent value stores SQL NULL and the gateway decodes it as nil so
-- the read envelope omits the field.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS env JSONB NULL,
    ADD COLUMN IF NOT EXISTS request_envelope JSONB NULL;
