-- §15.1 line 797 pool drain. POST /v1/admin/pools/{name}/drain transitions
-- a pool into the `draining` phase: the gateway stops admitting new sessions
-- to the pool and rejects session creation that would select it with
-- 503 POOL_DRAINING. draining_since records when the pool entered the phase
-- so the admin GET can report `phase: draining` and the drain estimate can
-- be derived. A NULL value is the `active` phase (the default). See
-- spec/15_external-api-surface.md §15.1 line 797.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS draining_since TIMESTAMPTZ;
