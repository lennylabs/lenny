-- §6.1 per-pool SDK-warm circuit-breaker operability config. The admin
-- pool API accepts the `sdkWarm.circuitBreakerOverride` operator override
-- (line 63: enabled | disabled | auto) and the
-- `sdkWarm.acknowledgeHighDemotionRate` flag (line 48). The
-- PoolScalingController reads both via the §4.6.2 PoolStoreSource and
-- applies them in its SDK-warm circuit-breaker decision and its
-- SDKWarmDemotionRateHigh warning-event gate. The column is JSONB so the
-- schema can absorb future SDK-warm config additions without a
-- forward-only column migration on every change.
--
-- A NULL row means the pool carries no explicit SDK-warm override: the
-- breaker runs under automatic control and the demotion-rate-high event
-- is not acknowledged. See spec/06_warm-pod-model.md §6.1 lines 48, 63-65.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS sdk_warm_config JSONB;
