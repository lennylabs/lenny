-- §17.8.2 cold-start bootstrap procedure. bootstrap_min_warm is the static
-- warm-pod override an operator sets immediately after pool creation (admin
-- API `PUT {bootstrapMinWarm: N}` or the `bootstrap.pools[].minWarm` Helm
-- value). While it is set the PoolScalingController pins the pool's warm-pod
-- floor to this value (status.scalingMode: bootstrap) instead of the scaling
-- formula, until the §17.8.2 step-4 convergence criteria are met. A NULL value
-- means no override is in force (the formula-driven default). The override is
-- cleared by `DELETE /v1/admin/pools/{name}/bootstrap-override`. See
-- spec/17_deployment-topology.md §17.8.2 "Cold-start bootstrap procedure".
ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS bootstrap_min_warm INTEGER
        CHECK (bootstrap_min_warm IS NULL OR bootstrap_min_warm >= 0);
