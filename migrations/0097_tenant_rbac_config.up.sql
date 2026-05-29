-- The §10.6 tenant RBAC configuration beyond the no_environment_policy
-- column (line 665: identityProvider, tokenPolicy, capabilities taxonomy,
-- mcpAnnotationMapping overrides).
--
-- rbac_config holds these fields as a JSON object, modeled on
-- tenantstore.RBACConfig. The empty object {} is the default: the tenant
-- configured no identity provider, token policy, custom capability
-- taxonomy, or capability-inference override.

ALTER TABLE tenants
    ADD COLUMN rbac_config JSONB NOT NULL DEFAULT '{}'::jsonb;
