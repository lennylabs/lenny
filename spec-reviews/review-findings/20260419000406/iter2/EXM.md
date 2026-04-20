### EXM-004 Concurrent-stateless residualStateWarning omitted despite same-tenant process cotenancy [MEDIUM]

**Files:** `07_session-lifecycle.md` (line 73), `05_runtime-registry-and-pool-model.md` (lines 483, 485)

The `sessionIsolationLevel.residualStateWarning` field (7.1:73) is set `true` "when `executionMode` is `task` (any scrub variant) or `concurrent` with `concurrencyStyle: workspace`" — concurrent-stateless is explicitly excluded. However, 5.2:485 says concurrent-stateless pods "share a network namespace and process space across all concurrent requests" and 5.2:483 routes same-tenant requests to pinned pod IPs. Within a tenant, concurrent-stateless has the same residual vectors as concurrent-workspace (shared `/tmp`, cgroup memory, network stack, page cache) plus stronger ones (no per-request workspace reset, no slot cleanup clearing `/tmp` between requests). The field as documented sets `residualStateWarning: false` for concurrent-stateless, signaling cleaner isolation than concurrent-workspace — opposite of actual posture. Clients that "reject sessions where this field is `true`" (5.2:458) will accept concurrent-stateless sessions with weaker per-request isolation than concurrent-workspace.

**Recommendation:** Extend 7.1:73 to `` `true` when `executionMode` is `task` (any scrub variant) or `concurrent` (either `concurrencyStyle`) `` and enumerate the concurrent-stateless vectors (process space, network stack, `/tmp`, page cache — none cleared since there is no per-request scrub). Alternatively, add a companion `stateTracking: false` field so `residualStateWarning: false` for stateless doesn't read as "no residual state".

---

### EXM-005 preConnect pool invariant violated for task-mode pods with scrub_warning [MEDIUM]

**Files:** `06_warm-pod-model.md` (lines 32, 128, 132), `05_runtime-registry-and-pool-model.md` (line 429)

Section 6.1:32 establishes the invariant: "**All pods are SDK-warm when the runtime supports it.** Pools referencing a `preConnect`-capable runtime warm **all** pods to SDK-warm state." The task-mode state machine (6.2:121-135) specifies the post-scrub transitions:

- 6.2:127 — `task_cleanup → idle` when scrub succeeds (non-preConnect path)
- 6.2:128 — `task_cleanup → idle [scrub_warning]` when scrub fails with `onCleanupFailure: warn` (no preConnect guard)
- 6.2:132 — `task_cleanup → sdk_connecting` only when **scrub succeeds** and preConnect is true

The gap: a preConnect=true task-mode pod that experiences a scrub **failure** in `warn` mode bypasses the `sdk_connecting` re-warm transition (line 132 requires "scrub succeeds") and returns to `idle` directly (line 128). The returned pod has no SDK process running, violating the "all pods are SDK-warm" invariant for preConnect pools. 5.2:429 describes the `scrub_warning` path ("the pod is returned to the available pool with a `scrub_warning` annotation ... accepts the next task") without reconciling it with preConnect re-warm. The next task's claim will then either (a) hit an idle pod that should have been SDK-warm but is pod-warm, silently forfeiting the SDK-warm latency benefit for that claim, or (b) require implicit on-claim SDK connect that isn't in the documented state machine.

**Recommendation:** Either add a new state transition `task_cleanup → sdk_connecting` when "preConnect: true, scrub fails (warn), maxScrubFailures not reached, uptime limit not reached" so scrub_warning pods also re-warm SDK before returning to idle — or explicitly document in 6.2 that `scrub_warning` pods on preConnect pools skip SDK re-warm and are returned as pod-warm, with an accompanying note that the `lenny_warmpool_sdk_warm_pods` inventory metric (if tracked) may drop transiently. The first option preserves the invariant; the second documents an explicit exception.

---

### EXM-006 mode_factor bootstrap fallback omits task-mode retirement-config-change guard [LOW]

**Files:** `05_runtime-registry-and-pool-model.md` (line 554)

5.2:554 specifies cold-start fallback to `mode_factor = 1.0` until 100 completed tasks are observed. 5.2:532 says "for variable workloads where early retirement is common, use observed `lenny_task_reuse_count` p50 rather than `maxTasksPerPod` as the estimate". There is no guidance for when deployer config changes (`maxTasksPerPod`, `maxScrubFailures`, `maxPodUptimeSeconds`) take effect: the converged `mode_factor` lags the config change by up to 100 samples, during which the pool is sized according to the stale (possibly too-large) prior estimate, causing under-provisioning until the histogram catches up.

**Recommendation:** Add a caveat that retirement-policy config changes reset the `mode_factor` to `min(maxTasksPerPod_new, mode_factor_current)` and re-enter the 100-sample window. Alternatively, clamp `mode_factor ≤ maxTasksPerPod` at all times (5.2:554 implies this for the converged case but not post-config-change).

---

No other real issues found on must-check items. Graph mode elimination is clean (single canonical mention at 5.2:362; LangGraph references at 14/26 are the runtime, not the execution mode). Task mode "best-effort, not security boundary" language is sufficient: deployer acknowledgment (5.2:440, 456), tenant pinning (5.2:372-379), microvm/cross-tenant guardrails (5.2:377-379, 421-425), and client visibility (5.2:458, 7.1:73) collectively communicate the non-boundary posture. Concurrent-workspace slotId per-slot failure threshold is correctly handled ("fail or leak", 5.2:514; 6.2:156). Concurrent-stateless vs connector distinction is clearly stated (5.2:487-493).
