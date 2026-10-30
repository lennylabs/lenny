-- Tenant policy columns the Postgres tenant store did not persist.
--
-- elicitation_content_integrity is the §9.2 tenant-stored elicitation
-- content-integrity mode (off, detect-only, enforce). billing_erasure_policy
-- is the §12.8 policy governing GDPR erasure of the tenant's billing
-- events (pseudonymize, exempt). no_environment_policy is the §10.6
-- RBAC policy for an authenticated caller who is a member of no
-- environment (deny-all, allow-all). Each is modeled on the
-- tenantstore.Tenant struct but had no column; an empty value carries
-- the same platform-default semantics as the in-memory store.

ALTER TABLE tenants
    ADD COLUMN elicitation_content_integrity TEXT NOT NULL DEFAULT '',
    ADD COLUMN billing_erasure_policy        TEXT NOT NULL DEFAULT '',
    ADD COLUMN no_environment_policy         TEXT NOT NULL DEFAULT '';
