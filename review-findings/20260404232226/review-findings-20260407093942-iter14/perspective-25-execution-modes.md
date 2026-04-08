# Perspective 25: Execution Modes & Concurrent Workloads — Iteration 14

**Spec file:** `technical-design.md` (8,691 lines)
**Category prefix:** EXM (starting at 035)
**Prior iteration status:** Clean in iteration 13

## Findings

### EXM-035 — `maxTaskRetries` missing from all `taskPolicy` YAML examples (Medium)

**Location:** Lines 2218 vs 1634-1643, 1844-1854, 1893-1901

**Issue:** Line 2218 states: "`maxTaskRetries` is a `taskPolicy` field (default: `1`)." However, none of the three `taskPolicy` YAML blocks in the spec include this field. The field is referenced in the task-mode pod state machine (line 2202: `retryCount < maxTaskRetries`) and its prose description (lines 2215-2218), but an implementer reading the `taskPolicy` YAML schema has no indication that `maxTaskRetries` exists or what its default is.

**Fix:** Add `maxTaskRetries: 1 # default — pod crash retries; 0 disables` to all three `taskPolicy` YAML blocks (lines 1634-1643, 1844-1854, 1893-1901).

---

### EXM-036 — Per-slot cgroup referenced in failure isolation but denied in resource contention (Medium)

**Location:** Lines 1940 vs 1943

**Issue:** Line 1940 (failure isolation) states: "OOM within the slot's cgroup" as a slot failure trigger. Line 1943 (resource contention), four lines later in the same subsection, states: "no per-slot cgroup subdivision in v1." If there is no per-slot cgroup, an OOM cannot be attributed to a specific slot's cgroup — the OOM kill would be at the pod-level cgroup, potentially killing processes from any slot.

**Fix:** In line 1940, replace "OOM within the slot's cgroup" with "OOM kill of a process in the slot's process group" (consistent with the v1 no-per-slot-cgroup constraint). The adapter can attribute an OOM to a specific slot by matching the killed PID to the slot's process group, without requiring per-slot cgroups.

---

### EXM-037 — `cleanupTimeoutSeconds` undefined for concurrent-workspace pools (Medium)

**Location:** Lines 1941, 6399 vs 1921-1923

**Issue:** The concurrent-workspace slot cleanup formula (line 1941) references `cleanupTimeoutSeconds / maxConcurrent` and the CRD validation rule (line 6399) rejects pools where `cleanupTimeoutSeconds / maxConcurrent < 5`. However, `cleanupTimeoutSeconds` is defined exclusively within `taskPolicy` (lines 1639, 1850, 1897). The `concurrentWorkspacePolicy` YAML (lines 1921-1923) contains only `acknowledgeProcessLevelIsolation` and `maxConcurrent` — no `cleanupTimeoutSeconds`. For a pool with `executionMode: concurrent`, there is no `taskPolicy`, so the field has no defined source.

**Fix:** Add `cleanupTimeoutSeconds: 40 # required — per-slot timeout = cleanupTimeoutSeconds / maxConcurrent (min 5s)` to the `concurrentWorkspacePolicy` YAML block. Update the CRD validation rule description to reference `concurrentWorkspacePolicy.cleanupTimeoutSeconds` (not just the generic field name).

---

### EXM-038 — `scrubPolicy` values undefined for concurrent execution mode (Medium)

**Location:** Lines 2465-2466

**Issue:** `sessionIsolationLevel.podReuse` is `true` when `executionMode` is `task` or `concurrent` (line 2465). `scrubPolicy` is "Present only when `podReuse: true`" (line 2466). But the enumerated `scrubPolicy` values (`"best-effort"`, `"vm-restart"`, `"best-effort-in-place"`) only cover task-mode scenarios. No `scrubPolicy` value is defined for `executionMode: concurrent`, even though the field's presence condition (`podReuse: true`) includes concurrent mode. An implementer cannot produce a valid `scrubPolicy` for concurrent-mode sessions.

**Fix:** Either (a) add a `scrubPolicy` value for concurrent mode (e.g., `"per-slot-cleanup"` for concurrent-workspace, `"none"` for concurrent-stateless), or (b) change the presence condition to `"Present only when executionMode is task"` if concurrent mode intentionally has no scrub policy.

---

### EXM-039 — Task-mode `mode_factor` incorrectly applied to burst term in scaling formula (Medium)

**Location:** Lines 1969, 1975-1976

**Issue:** The adjusted scaling formula (lines 1975-1976) divides both the steady-state term and the burst term by `mode_factor`. For task mode, `mode_factor = avg_tasks_per_pod_lifetime` (line 1969), which converges toward `maxTasksPerPod` (e.g., 50 per line 1983). This means the burst term becomes `burst_p99_claims x pod_warmup_seconds / 50`, implying a task-mode pod can absorb 50x more burst than a session-mode pod.

This is incorrect. Task-mode pods serve tasks **sequentially** — at any given instant, each pod handles exactly one task. A pod with `maxTasksPerPod: 50` cannot serve 50 burst claims simultaneously; it serves them one at a time over its lifetime. The `mode_factor` correctly reduces the **long-term provisioning** need (fewer total pods because each is reused), but it does not increase instantaneous burst absorption capacity. Dividing the burst term by `mode_factor = 50` would under-provision warm pods by ~50x during bursts, causing claim queue saturation.

For concurrent mode, `mode_factor = maxConcurrent` is correct because pods truly serve that many tasks simultaneously.

**Fix:** Split the `mode_factor` application: for task mode, apply `mode_factor` only to the steady-state term (long-term demand amortization), not the burst term. The burst term should use `mode_factor = 1` for task mode. For concurrent mode, both terms can use `mode_factor = maxConcurrent` as currently specified. One approach:

```
burst_mode_factor:
  session: 1.0
  task: 1.0  (sequential — no instantaneous multiplexing)
  concurrent: maxConcurrent  (true simultaneous capacity)
```
