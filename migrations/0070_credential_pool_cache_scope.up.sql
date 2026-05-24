-- The §4.9 semantic-cache cacheScope for a credential pool.
--
-- cache_scope selects the identity granularity of the pool's semantic
-- cache (per-user, per-session, tenant). Empty selects the per-user
-- default. The `tenant` value is the deployer opt-in to cross-user
-- cache sharing within a tenant; the admin API rejects it for a tenant
-- with a regulated complianceProfile (hipaa, fedramp) with
-- 400 COMPLIANCE_CROSS_USER_CACHE_PROHIBITED. The closed value set is
-- validated in application code (credentialpoolstore.Validate).
ALTER TABLE credential_pools
    ADD COLUMN cache_scope TEXT NOT NULL DEFAULT '';
