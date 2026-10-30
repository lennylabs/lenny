-- §4.9 tenant credentialPolicy: which credential sources are available
-- and how they are selected. The §4.9 CredentialRouter intersects the
-- providerPools map with a Runtime's supportedProviders at session
-- creation. Modeled on tenantstore.Tenant.CredentialPolicy
-- (credential.CredentialPolicy) but had no column; an empty JSON object
-- carries the same "no credential sourcing configured" semantics as the
-- in-memory store's zero value.
ALTER TABLE tenants
    ADD COLUMN credential_policy JSONB NOT NULL DEFAULT '{}'::jsonb;
