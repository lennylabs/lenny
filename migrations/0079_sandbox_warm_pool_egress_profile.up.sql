-- §13.2 per-pool egress profile for sandbox_warm_pools.
--
-- egress_profile is the §13.2 profile the warm pool controller stamps
-- onto agent pods (via the lenny.dev/egress-profile label) so the
-- pre-created NetworkPolicy for that profile takes effect:
-- 'restricted' (gateway + DNS proxy only), 'provider-direct' (adds LLM
-- provider CIDRs), or 'internet' (adds broad public-internet egress).
-- Empty inherits the §13.2 default ('restricted') at admission. The
-- §13.2 cross-control forbidding 'internet' on a 'standard' (runc) pool
-- is enforced in application code (poolstore.ValidateEgressIsolation).
ALTER TABLE sandbox_warm_pools
    ADD COLUMN egress_profile TEXT NOT NULL DEFAULT '';
