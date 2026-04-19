### EXM-001 Concurrent-workspace leaked slots not counted in failure threshold [CRITICAL]

**Files:** `06_warm-pod-model.md` (lines 142, 156), `05_runtime-registry-and-pool-model.md` (line 514)

The spec states leaked slots "count as a failed slot for the purposes of the `ceil(maxConcurrent/2)` unhealthy threshold" (6.2:156). However, in 5.2:514, the whole-pod replacement trigger is defined as "When `ceil(maxConcurrent / 2)` or more **slots on the same pod fail**" — using the term "fail", not "unhealthy" or "failed/leaked". This creates ambiguity about whether leaked slots (which remain `slot_cleanup → leaked`, never reaching `failed` state) are properly counted toward triggering the pod replacement. In 6.2:142, the state diagram shows `slot_active → draining` when "ceil(maxConcurrent/2) slots fail within 5-min window" — again, using the past-tense "fail" rather than an inclusive enumeration.

**Recommendation:** Explicitly enumerate the failure categories that trigger pod replacement in 5.2:514 to clarify that leaked slots (failed + leaked_slots >= ceil(maxConcurrent/2)) triggers the threshold, not just cleanly-failed slots. Suggest: "When `ceil(maxConcurrent / 2)` or more slots on the same pod **fail or leak** within a rolling 5-minute window..."

---

### EXM-002 Task mode scrub-for-stateless conflation in sessionIsolationLevel [MEDIUM]

**Files:** `07_session-lifecycle.md` (line 72), `05_runtime-registry-and-pool-model.md` (lines 468, 487)

The sessionIsolationLevel response includes `scrubPolicy` with distinct values: `"best-effort"` for task mode, `"best-effort-per-slot"` for concurrent-workspace, and `"none"` for concurrent-stateless (7.1:72). However, 5.2 states concurrent-stateless has **no workspace materialization** (5.2:483-484: "no workspace materialization... Gateway routes through Kubernetes Service") and is explicitly styled as "just a connector" (5.2:488-490 disclaimers). The `scrubPolicy: "none"` response correctly represents this, but the field inclusion itself may mislead clients into believing concurrent-stateless pods undergo the same state lifecycle as the other modes, when in reality the gateway does not track individual slot state for stateless mode — it is merely a load-balanced Service router.

**Recommendation:** Clarify in 7.1 sessionIsolationLevel documentation that `scrubPolicy: "none"` for concurrent-stateless mode indicates not just "no cleanup" but "no per-request state tracking or lifecycle management by Lenny" — suggest a note: "For concurrent-stateless mode, the gateway does not track per-request state or lifecycle; pods are treated as stateless request routers. See Section 5.2 for deployment model limitations." Alternatively, consider whether `scrubPolicy` field is the right vehicle for communicating this distinction or whether a separate field (`stateTracking: false` or `workspaceTracking: false`) would reduce confusion.

---

### EXM-003 Concurrent-workspace preConnect incompatibility gap [LOW]

**Files:** `06_warm-pod-model.md` (line 64), `05_runtime-registry-and-pool-model.md` (line 361)

Section 6.1:64 states "The pool controller rejects pool definitions that combine `executionMode: concurrent`, `concurrencyStyle: workspace`, and `capabilities.preConnect: true`" — this is a pool-level validation. However, 5.2:361 states "Graph mode is removed as a separate concept — graph-aware runtimes are session-mode runtimes." This implies graph-aware runtimes (which may have preConnect for single-session latency savings) could be registered with any execution mode. The incompatibility between preConnect and concurrent-workspace is correctly specified, but the broader design decision about whether graph-aware runtimes should be restricted to session mode (not just documented as incompatible at pool validation time) is not explicit.

**Recommendation:** Add a clarifying statement in 5.2 Execution Modes section immediately after the graph-mode elimination note: "Graph-aware runtimes that benefit from SDK pre-connection (`preConnect: true`) are designed for session mode exclusively. Pool definitions combining concurrent-workspace mode with preConnect runtimes are rejected at validation time; operators should use session mode pools for such runtimes." This makes the design intent (not just the validation constraint) clear.

---

No other real errors or cross-section inconsistencies detected. Spec is internally consistent on: task-mode cleanup as best-effort, concurrent slot multiplexing retry semantics, distinction between concurrent-workspace and concurrent-stateless modes, graph mode removal.
