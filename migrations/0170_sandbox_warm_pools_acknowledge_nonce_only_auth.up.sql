-- §4.7 / §5.3 — the pool-level nonce-only-mode acknowledgment flag,
-- alongside allow_standard_isolation (migration 0033).
--
-- acknowledge_nonce_only_auth is the §5.3 deployer opt-in of the same
-- class as allow_standard_isolation: a pool referencing a runtime with
-- require_so_peercred = false is rejected by the gateway pool admission
-- path unless the pool sets this flag true. The check is unconditional
-- and applies in every tenancy mode (§4.7). The PoolScalingController
-- mirrors the flag onto SandboxTemplate.spec per the §4.6.3 ownership
-- table, and the WarmPoolController renders nonce-only mode only for
-- acknowledged pools, so a Runtime resource applied directly with
-- kubectl cannot activate nonce-only mode on a pool that has not
-- acknowledged it. Defaults to false: a pool that never opts in keeps
-- full SO_PEERCRED enforcement.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN acknowledge_nonce_only_auth BOOLEAN NOT NULL DEFAULT false;
