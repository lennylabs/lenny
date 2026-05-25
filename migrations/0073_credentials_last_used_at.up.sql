-- §4.9 / §15.1 last_used_at on the end-user credential registry.
--
-- last_used_at records the last instant the credential was resolved for
-- a lease. It is NULL until the credential is first used. The §15.1
-- GET /v1/credentials response carries it (spec line 1349, 1365). The
-- §4.9 lease-resolution path updates it via credentialstore.MarkUsed.
ALTER TABLE credentials
    ADD COLUMN last_used_at TIMESTAMPTZ;
