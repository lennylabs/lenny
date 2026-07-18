-- §4.9 credential-delivery combination fields for sandbox_warm_pools,
-- alongside isolation_profile (migration 0033) and allow_standard_isolation.
--
-- delivery_mode and spiffe_binding are the §4.9 pool-definition
-- credential-delivery fields the PoolScalingController reconciles onto the
-- SandboxTemplate CRD. One warm-pool admin resource then carries the whole
-- combination (isolation_profile + delivery_mode + spiffe_binding) the
-- pool-registration and admission layers inspect for the two
-- cross-tenant-risky combinations: delivery_mode 'direct' + isolation_profile
-- 'standard', and delivery_mode 'proxy' + spiffe_binding 'disabled'. Both
-- default to '' (inherit the runtime default): a pool that never sets them
-- carries no explicit override.
--
-- allow_direct_mode_standard_isolation and
-- allow_proxy_mode_spiffe_binding_disabled are the §4.9 deployer opt-in
-- acknowledgments of the same class as allow_standard_isolation (migration
-- 0033). They permit their respective combination in single-tenant or
-- development mode; in multi-tenant mode the admission webhook rejects the
-- combination regardless of the flag. Both default to false (no
-- acknowledgment), so a pool that never opts in carries neither.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT '',
    ADD COLUMN spiffe_binding TEXT NOT NULL DEFAULT '',
    ADD COLUMN allow_direct_mode_standard_isolation BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN allow_proxy_mode_spiffe_binding_disabled BOOLEAN NOT NULL DEFAULT false;
