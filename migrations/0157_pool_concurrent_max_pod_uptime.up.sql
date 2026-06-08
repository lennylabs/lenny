-- §6.2 lines 166-167 concurrent-workspace pod-uptime retirement.
-- concurrent_max_pod_uptime_seconds caps a concurrent-workspace pod's
-- wall-clock uptime since first boot: the gateway slot-claim path drains
-- an over-uptime pod (slot_active → draining, no new slots accepted; idle
-- → draining before the next assignment) and skips it as a slot candidate.
-- It is the concurrent-mode counterpart of the task-mode
-- taskPolicy.maxPodUptimeSeconds (carried inside the task_policy JSONB).
-- A NULL or 0 value leaves uptime retirement off — the pod is retired only
-- by the §5.2 unhealthy-slot threshold. See
-- spec/06_warm-pod-model.md §6.2 lines 166-167.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS concurrent_max_pod_uptime_seconds INTEGER
        CHECK (concurrent_max_pod_uptime_seconds IS NULL OR concurrent_max_pod_uptime_seconds >= 0);
