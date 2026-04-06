# Execution Modes & Concurrent Workloads Review Findings — 2026-04-04

**Document reviewed:** `docs/technical-design.md` (5,277 lines)
**Perspective:** 25. Execution Modes & Concurrent Workloads
**Category code:** EXM
**Reviewer focus:** Design soundness of the three execution modes (session, task, concurrent), security implications of pod reuse, concurrent-workspace slot failure semantics, the stateless-vs-connector distinction, graph mode elimination, and interaction of execution modes with warm pool strategy and pod lifecycle.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High     | 4 |
| Medium   | 4 |
| Low      | 3 |
| Info     | 2 |

---

## Critical

### EXM-001 Concurrent-Workspace Mode Lacks a Deployer Acknowledgment Field [Critical]
**Section:** 5.2

Task mode requires an explicit `acknowledgeBestEffortScrub: true` field that the pool controller validates at admission time. If absent, the pool definition is rejected. The spec text says `concurrencyStyle: workspace` also requires "deployer acknowledgment" — the security exposure is materially comparable (process-level cross-slot isolation, shared `/tmp`, shared cgroup memory) — but no acknowledgment field is defined, no YAML schema is shown, and there is no statement that the controller enforces anything at validation time. The sentence "Deployer acknowledgment required." appears inline in the description with no corresponding configuration artifact.

A deployer enabling `concurrencyStyle: workspace` with `maxConcurrent: 8` across a multi-user pool gets no explicit gate, no forced acknowledgment of the weaker-than-session isolation model, and no controller-enforced validation — in sharp contrast to task mode. This asymmetry is a design soundness gap and a security communication failure.

**Recommendation:**
1. Define a `concurrentWorkspacePolicy` block (analogous to `taskPolicy`) with a required `acknowledgeProcessLevelIsolation: true` field.
2. Have the pool controller reject any pool definition with `concurrencyStyle: workspace` if this field is absent or `false`, with a descriptive error that cites section 5.2 and lists the specific isolation properties the deployer is accepting.
3. Add a YAML example block in section 5.2 showing the full concurrent-workspace configuration including the acknowledgment, symmetrically with the task mode example.

---

## High

### EXM-002 Task Mode Pod Scrub: "warn" Default Allows Residual State on Repeated Failures [High]
**Section:** 5.2

The `onCleanupFailure: warn` behavior is the default. When the Lenny scrub fails its own verification step (step 6 — stat-checking workspace path, `/tmp`, and `/dev/shm` are non-empty), the pod is returned to the available pool with only a `scrub_warning` annotation. The spec acknowledges this as "best-effort, not a security boundary" and the deployer acknowledgment flag makes this explicit for tenants.

However, the spec does not define a threshold or policy for repeated scrub failures on the same pod. A pod that accumulates multiple sequential `scrub_warning` annotations remains in the pool indefinitely under `onCleanupFailure: warn`. The spec says the deployer "accepts residual state risk," but no mechanism limits accumulated risk over the pod's lifetime. A pod that fails its scrub on every task becomes progressively more contaminated. Tenant-pinning is a mitigation (same tenant across tasks) but does not cover the case where the same tenant has multiple users whose data should be isolated from one another.

Additionally, the "best-effort, not a security boundary" phrasing, while technically correct, is insufficiently specific about what the scrub does and does not guarantee. There is no enumeration of what residual state vectors survive a scrub failure (process-to-process shared memory, `/proc` side channels, kernel caches, network socket TIME_WAIT state, inotify watchers, `POSIX_FADV_WILLNEED` cache priming).

**Recommendation:**
1. Add a `maxScrubFailures` field to `taskPolicy` (suggested default: `3`). When a pod's cumulative scrub failure count reaches the limit, the pod is retired and terminated regardless of `onCleanupFailure` setting. Document this in the schema.
2. Expand the "best-effort, not a security boundary" statement to enumerate the specific residual state vectors that scrub cannot address (named pipes, shared memory segments not under managed paths, kernel TCP state, inotify). This gives deployers accurate information to decide whether task mode is appropriate for their workload's sensitivity.
3. Add `lenny_task_pod_scrub_failure_count` as a per-pod gauge metric (distinct from `lenny_task_scrub_failure_total`) to let operators identify specific pods accumulating failures before manual intervention is needed.

---

### EXM-003 Concurrent-Workspace Slot Failure: Gateway Retry Semantics Unspecified [High]
**Section:** 5.2

Section 5.2 states that when a slot fails, "The gateway is notified via the lifecycle channel and may retry or report the failure to the client." The operative word "may" leaves retry behavior unspecified. The following questions are unanswered:

- Does the gateway retry the slot's task on a new slot (within the same pod or a different pod)?
- If retried on the same pod, does the retry slot get a clean workspace or the previous slot's workspace (since cleanup may be in progress)?
- If retried on a different pod, is a new pod claimed from the warm pool (consuming warm inventory)?
- What is the max retry count for per-slot failures, and does exponential backoff apply?
- Is a slot OOM failure (mentioned as a trigger) retried at all, or treated as non-retryable (since the same input would likely OOM again)?

The pre-attached session retry policy (Section 6.2) is fully specified: max 2 retries, exponential backoff, fresh pod each time, non-retryable categories enumerated. There is no equivalent policy section for per-slot failures in concurrent-workspace mode. This is an implementation ambiguity that will result in divergent behavior across implementations.

**Recommendation:**
1. Add a "Concurrent-workspace slot retry policy" subsection in section 5.2 specifying: retry max count (suggested: 1 retry, on a new slot within the same pod), backoff, non-retryable failure categories (OOM, workspace validation error), and the condition under which a slot failure triggers whole-pod replacement (suggested: when `maxConcurrent / 2` or more slots fail within a configured window).
2. Specify whether a retried slot gets a fresh workspace (staging materialized from scratch) or inherits any state from the failed slot. The spec should say "always fresh workspace for retried slots."
3. Define the error returned to the client when a slot fails and either no retry is attempted or all retries are exhausted, including the error category and whether it is marked retryable from the client's perspective.

---

### EXM-004 Task Mode Pod Lifecycle: No Maximum Pod Age or Maximum Reuse Count [High]
**Section:** 5.2, 6.2

The spec describes task mode pods being reused across "multiple sequential tasks" and the `mode_factor` formula references `avg_tasks_per_pod_lifetime` — implying some limit exists — but there is no `maxTasksPerPod`, `maxPodAge`, or retirement policy defined anywhere in the spec. The pod state machine (Section 6.2) has no transition that accounts for task-mode pod retirement after accumulated reuse.

This creates two problems:

First, a practical security problem: even within a single tenant, repeated task reuse can accumulate memory fragmentation patterns, process-local caches (kernel buffer cache, DNS resolver cache), and timing-observable side channels that increase within-tenant data leakage risk between successive users' tasks — even with successful scrubs.

Second, the `mode_factor = avg_tasks_per_pod_lifetime` scaling formula has no grounding without a corresponding operational limit. The warm pool sizing assumes the pod serves a predictable number of tasks. Without a `maxTasksPerPod` cap, the actual `avg_tasks_per_pod_lifetime` value is undefined until the pod is retired for external reasons (node eviction, `maxSessionAge`, or manual intervention) — making the formula aspirational rather than operational.

**Recommendation:**
1. Add a `maxTasksPerPod` field to `taskPolicy` (required with no default, forcing deployer to make an explicit choice). When the count is reached, the pod transitions to `draining` after its current task completes, is not assigned new tasks, and is replaced by a fresh warm pod.
2. Add a `maxPodUptimeSeconds` field to `taskPolicy` as an additional retirement trigger for long-lived task-mode pools.
3. Update the pod state machine diagram (Section 6.2) to include a `task_cleanup` intermediate state between task completion and return to `idle`, and a `draining` transition from `idle` when `maxTasksPerPod` is reached.
4. Document the formula assumption explicitly: "the `mode_factor` estimate converges toward the configured `maxTasksPerPod` for predictable workloads; for variable workloads, use observed `lenny_task_reuse_count` p50."

---

### EXM-005 Cross-Tenant Task Mode Reuse via microvm: Missing Acknowledgment Gate [High]
**Section:** 5.2

Section 5.2 states: "Cross-tenant pod reuse is only permitted with `microvm` isolation, where the VM boundary provides a hardware-level security domain between assignments." This is an important policy exception — it creates a carve-out from the tenant-pinning rule. However:

1. There is no configuration field to explicitly enable cross-tenant reuse for a task-mode pool, even with `microvm` isolation. It appears to be an implicit permission based on the isolation profile alone. A deployer configuring a task-mode `microvm` pool has no explicit acknowledgment step for the cross-tenant reuse policy, no corresponding `allowCrossTenantReuse: true` flag, and no controller validation.

2. The scrub procedure (Section 5.2, Lenny scrub steps 1–6) is described without any Kata/VM-specific variant. The scrub kills user processes and removes workspace directories — but in a microvm (Kata) pod, the VM itself persists across tasks. The spec does not clarify whether the scrub restarts the guest OS, reclaims the guest kernel state, or relies solely on the filesystem-level scrub inside a continuing VM. If the VM guest continues running, DNS caches, TCP connection state, and kernel buffer cache may persist across tenant boundaries despite the hardware isolation claim.

3. The claim that "the VM boundary provides a hardware-level security domain between assignments" needs qualification: Kata Containers uses a lightweight VM but shares the host kernel's virtio devices. The isolation is stronger than runc/gVisor but is not equivalent to a full hardware boundary. The spec should not overstate this.

**Recommendation:**
1. Add an explicit `allowCrossTenantReuse: true` field to `taskPolicy` that is only accepted when `isolationProfile: microvm`. The pool controller must reject `allowCrossTenantReuse: true` on non-microvm pools at validation time.
2. Define a Kata-specific scrub variant in section 5.2 that specifies whether the guest VM is restarted between cross-tenant tasks. If the VM guest is not restarted, document the known residual state vectors. If a guest restart is required, add it to the Lenny scrub procedure steps and note the additional latency cost.
3. Qualify the "hardware-level security domain" claim to accurately reflect Kata's actual isolation model: "Kata provides a VM-level isolation boundary that is significantly stronger than runc or gVisor, but shares host virtio devices. It is appropriate for cross-tenant task reuse where tenants have been independently vetted but is not equivalent to dedicated hardware isolation."

---

## Medium

### EXM-006 Concurrent-Stateless Mode: Credential Assignment Model Unspecified [Medium]
**Section:** 5.2

Session mode pods receive a single `CredentialLease` via `AssignCredentials` at claim time (Section 7.1, step 6). Task mode pods receive a credential lease at first claim and hold it for the pod's lifetime across tasks (the spec says "on session end: lease released back to pool" but does not specify whether this means per-task or per-pod-lifetime for task mode).

For `concurrencyStyle: stateless`, there is no discussion of credential assignment at all. The spec states "Gateway routes through Kubernetes Service" — but the credential leasing model for pods behind a Kubernetes Service with multiple simultaneous slots is undefined. Key questions:

- Does each `stateless` pod hold a single credential lease shared across all concurrent slots?
- Is the credential a pod-level assignment or a slot-level assignment?
- When a `stateless` pod is not claimed via the warm pool model, when does credential assignment happen?
- For multi-tenant deployments, can different slots on the same pod hold credentials for different tenants?

Without this clarity, implementors must guess — and incorrect guesses can result in credential exhaustion (all slots share one lease's rate limits) or credential confusion (one slot's rate-limited credential affecting another slot's performance).

**Recommendation:**
1. Add a subsection or callout in section 5.2 specifying the credential model for `concurrencyStyle: stateless`: specifically, whether credentials are pod-level or slot-level, and how `maxConcurrentSessions` in the credential pool interacts with `maxConcurrent`.
2. State explicitly whether `concurrencyStyle: stateless` pods in multi-tenant deployments may carry credentials for multiple tenants simultaneously (and if so, what constraints apply).
3. If pod-level credentials are used for stateless mode, document the implication: all concurrent slots share the same API key's rate limits, and deployers must size credential pools accordingly.

---

### EXM-007 Concurrent-Stateless vs. External Connector Distinction Relies on Prose Only [Medium]
**Section:** 5.2

Section 5.2 ends with: "Truly stateless runtimes with no workspace and no expensive shared state should be registered as external connectors, not Lenny-managed pods." This is correct design guidance, but it is stated as a "should" recommendation with no enforceability mechanism. A deployer could register a stateless HTTP function as a `concurrencyStyle: stateless` Lenny runtime and incur warm pool cost, pod scheduling overhead, and CRD management for a workload that would be better served as a connector.

More importantly, the distinction between "stateless runtime appropriately managed by Lenny" (e.g., one that benefits from image caching, mTLS, audit logging, or pod-level network policy) and "truly stateless connector" (one that needs none of these) is not defined with any criteria. The prose guidance is ambiguous about which Lenny-specific benefits justify the overhead.

There is also no guidance on what capabilities a `concurrencyStyle: stateless` runtime is expected to provide vs. a connector — for example, whether it uses the binary stdin/stdout protocol or whether the gateway speaks to it differently (the spec says routing goes through a Kubernetes Service, but the application-layer protocol is not described for stateless mode).

**Recommendation:**
1. Add a decision aid (a short bulleted list or table) in section 5.2 or section 5.3 listing the criteria for choosing `concurrencyStyle: stateless` vs. external connector: e.g., "Use stateless mode if the runtime requires mTLS isolation, Lenny credential injection, per-runtime network policy, or audit-logged tool use. Use a connector if the runtime is a public API or already-hardened internal service with its own auth."
2. Specify the application-layer protocol contract for `concurrencyStyle: stateless` pods — does the adapter binary protocol (stdin/stdout with `slotId`) apply, or does the gateway route raw HTTP to the pod via the Kubernetes Service? If HTTP, what does the request/response format look like?
3. Change "should" to an enforceable constraint where possible: e.g., a pool validation warning if `concurrencyStyle: stateless` is set with `maxConcurrent: 1` and no workspace capabilities declared (strong signal the workload should be a connector).

---

### EXM-008 Graph Mode Elimination: "Observability Protocol" for Trace Spans Undefined [Medium]
**Section:** 5.2

Section 5.2 states: "Graph mode is removed as a separate concept — graph-aware runtimes are session-mode runtimes that optionally emit trace spans via the observability protocol." This is a sound design decision — forcing graph execution into the session model eliminates a special-case execution path. However, "the observability protocol" for graph trace span emission is never defined or referenced anywhere else in the spec. The relevant section on observability (Section 16 / tracing) covers gateway-emitted OTel traces, not runtime-emitted graph node spans.

As a result, a runtime author who wants to build a LangGraph-style runtime on top of Lenny has no spec to implement against for graph node visibility. The decision to eliminate graph mode is correctly justified, but the replacement mechanism (trace span emission from the runtime) is a specification placeholder rather than a defined contract.

**Recommendation:**
1. Add a subsection in section 5.2 (or in the observability section) defining the "observability protocol" for runtime-emitted trace spans: the transport (is it an OTel OTLP push from inside the pod? a lifecycle channel message? a dedicated sidecar?), the span schema, and how runtime spans are correlated with gateway-level traces (trace context propagation).
2. Alternatively, if the protocol is intentionally deferred to a future phase, state this explicitly: "The trace span emission protocol for graph-aware runtimes is out of scope for v1; session-mode runtimes that wish to emit execution graph spans should use OTel OTLP directly from within the pod, targeting an OTel Collector exposed via the cluster's standard OTLP endpoint."
3. Add the graph-runtime trace protocol to the roadmap section (Section 21) so that future contributors know it is intended work rather than an omission.

---

### EXM-009 Pod State Machine Missing Task-Mode States [Medium]
**Section:** 6.2, 5.2

The pod state machine diagram in Section 6.2 is defined for session mode only. The `attached → completed` transition represents a session completing and the pod entering `draining`. For task mode, this is incorrect: after `attached → completed`, the pod does not drain — it transitions through scrub and returns to an available state for the next task.

There is no `task_cleanup` intermediate state, no representation of the scrub in the state machine, no `scrub_failed` state, and no depiction of how a task-mode pod re-enters an `idle`-like state after scrub. The lifecycle description in Section 5.2 prose ("task completes → adapter sends terminate → cleanupCommands → Lenny scrub → pod available") is not reflected in the formal state machine, creating a spec inconsistency that will cause implementation divergence.

Additionally, for `concurrencyStyle: workspace`, the pod state machine does not distinguish between "pod active with N slots running" and "pod active with 0 slots running" — the pod's status in the pool (available for a new slot assignment vs. fully saturated) is not captured in the `attached` state.

**Recommendation:**
1. Add a task-mode overlay or variant to the pod state machine in section 6.2, showing the `attached → task_cleanup → [scrub_warning | idle]` path and the `attached → draining` path triggered by `onCleanupFailure: fail` or `maxTasksPerPod` reached.
2. For concurrent-workspace mode, add a state or substates under `attached` that represent slot occupancy: e.g., `attached[slots: 0/8]` (available for new slot) vs `attached[slots: 8/8]` (saturated). This affects whether the gateway can route a new task to the pod.
3. Update the state transition table accompanying the diagram to cover task mode and concurrent mode transitions explicitly, not just session mode.

---

## Low

### EXM-010 Execution Mode Not Validated Against Isolation Profile Combinations [Low]
**Section:** 5.2, 5.3

Section 5.3 establishes a taxonomy of isolation profiles with security implications. Section 5.2 establishes task mode with tenant-pinning as a required safety property. However, there is no cross-validation between execution mode and isolation profile at pool-definition time. Specifically:

- A task-mode pool with `isolationProfile: standard` (runc) and `onCleanupFailure: warn` is accepted. This combination (weakest isolation + best-effort scrub + no security boundary) is the highest-risk configuration and should require an additional explicit acknowledgment beyond `acknowledgeBestEffortScrub`.
- `concurrencyStyle: workspace` with `isolationProfile: standard` (runc) places multiple simultaneous tasks in a shared-namespace container with only process-level separation — this combination has no precedent acknowledgment requirement.

**Recommendation:**
1. Add a cross-validation rule at pool definition time: task mode + `isolationProfile: standard` + `onCleanupFailure: warn` requires an additional `acknowledgeStandardIsolationWithTaskReuse: true` field, distinct from `acknowledgeBestEffortScrub`.
2. Document in section 5.2 a recommended matrix of safe combinations: e.g., "task mode with `standard` isolation is only appropriate for single-user development environments."

---

### EXM-011 Warm Pool Sizing Formula Does Not Account for Task-Mode Scrub Dead Time [Low]
**Section:** 5.2 (Execution Mode Scaling Implications)

The `mode_factor` for task mode is `avg_tasks_per_pod_lifetime`. The adjusted formula divides `base_demand_p95` by `mode_factor`, reducing the number of pods needed. However, the formula does not account for "scrub dead time" — the period between task completion and pod availability during which the pod is executing `cleanupCommands` and the Lenny scrub. During this window (bounded by `cleanupTimeoutSeconds`), the pod cannot accept new tasks.

At high task arrival rates, if `cleanupTimeoutSeconds = 30` and `avg_task_duration = 10s`, a pod is idle-for-reuse only 25% of the time — but the formula treats it as continuously available (since `mode_factor` only accounts for reuse count, not reuse efficiency). The pool will be systematically undersized for short-duration, high-throughput task workloads.

**Recommendation:**
Add a `scrub_efficiency_factor` to the task-mode formula in section 5.2: `effective_mode_factor = avg_tasks_per_pod_lifetime × (avg_task_duration / (avg_task_duration + avg_scrub_duration))`. Document this as a correction factor for short-duration tasks. Expose `lenny_task_scrub_duration_seconds` as a histogram metric to enable measurement of `avg_scrub_duration`.

---

### EXM-012 SDK-Warm Mode Interaction with Task and Concurrent Modes Undocumented [Low]
**Section:** 6.1, 5.2

Section 6.1 defines SDK-warm mode (pre-connected agent process) and states it applies to all pods in a `preConnect`-capable pool. Section 5.2 defines task and concurrent execution modes. There is no statement about whether SDK-warm mode is compatible with task mode or concurrent mode.

For task mode, SDK-warm implies the agent process persists across tasks — which could be desirable (no SDK restart overhead between tasks) or dangerous (agent state leaks between tasks). The spec is silent on this combination.

For concurrent mode, SDK-warm with a single agent process serving multiple slots introduces per-slot dispatch complexity within the pre-connected process, which may conflict with runtimes that assume a one-shot initialization model.

**Recommendation:**
Add a compatibility table or note in section 6.1 specifying which combinations of execution mode and warm mode are supported: e.g., "SDK-warm is compatible with `executionMode: session` only in v1. Task-mode and concurrent-mode pools must use pod-warm mode. `preConnect: true` on a task-mode or concurrent-mode runtime is a validation error."

---

## Info

### EXM-013 "Three Execution Modes" Count in Opening Sentence Is Potentially Confusing [Info]
**Section:** 5.2

The section opens with "All three execution modes are implemented in v1." The three modes are `session`, `task`, and `concurrent`. However, `concurrent` has two distinct sub-variants (`stateless` and `workspace`) with significantly different semantics, security properties, and warm pool interactions. From a deployer perspective, there are effectively four distinct operational profiles. A deployer reading "three modes" and then discovering two sub-variants of `concurrent` may find the taxonomy misleading.

**Recommendation:**
Consider changing the framing to: "Lenny supports three execution modes: `session`, `task`, and `concurrent`. The `concurrent` mode has two sub-variants (`stateless` and `workspace`) that differ significantly in their isolation model and warm pool behavior — see below." This sets the correct expectation before the detailed descriptions.

---

### EXM-014 Graph Mode Elimination Statement Appears Only Once with Minimal Context [Info]
**Section:** 5.2

The sentence "Graph mode is removed as a separate concept — graph-aware runtimes are session-mode runtimes that optionally emit trace spans via the observability protocol" is the only place in the 5,277-line spec where graph mode is addressed. There is no forward reference to any previous design that included graph mode, no explanation of why it was removed (for readers unfamiliar with the design history), and no cross-reference to the resolved decisions section.

Section 19 (Resolved Decisions) does not appear to contain an entry for this decision (as noted in the section on non-goals and roadmap). For a reader who encounters the term "graph mode" in the design conversation history or elsewhere, this single sentence provides insufficient context.

**Recommendation:**
Add an entry to Section 19 (Resolved Decisions) for graph mode elimination, briefly stating: the prior design had a distinct `executionMode: graph` for DAG-structured workloads; this was removed because recursive delegation (Section 8) plus session-mode runtimes already provide equivalent capability; graph-aware runtimes emit execution graph spans via OTel rather than requiring platform-level graph semantics.
