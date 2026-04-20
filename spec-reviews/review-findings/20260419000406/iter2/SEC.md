# Iter2 Security & Threat Modeling Review (SEC)

Two genuine issues identified. Iter1 found none, so numbering starts at SEC-001. All other focus areas (SIGSTOP/SIGCONT races, adapter-agent boundary, prompt-injection via delegation/elicitation, upload safety, delegation-time isolation monotonicity) were re-examined and either hold up or are already documented trade-offs.

---

## SEC-001 [Medium] Derive / replay bypasses isolation monotonicity

**Files:** `spec/07_session-lifecycle.md` §7.1 (lines 81–96, "Derive copy semantics" / "Derive session semantics"); `spec/15_external-api-surface.md` §15.1 (lines 204, 309–324, 602–608); `spec/08_recursive-delegation.md` §8.3 (line 251).

**Issue.** §8.3 requires that a delegated child's pool use an isolation profile **at least as restrictive** as the parent (`standard < sandboxed < microvm`), enforced by `minIsolationProfile` on the lease and rejected with `ISOLATION_MONOTONICITY_VIOLATED`. Derive (`POST /v1/sessions/{id}/derive`) and replay (`POST /v1/sessions/{id}/replay` with `replayMode: workspace_derive`) reproduce the delegation-like data-flow outcome — they copy the source session's workspace snapshot into a **new** session — but the documented rules do not include any isolation-profile check:

- §7.1 "Derive session semantics" enumerates allowed source states, concurrent-derive serialization, credential lease handling, and connector state, but says nothing about `targetPool` / `targetRuntime` isolation.
- §15.1 replay defines `targetRuntime` as "required … Must be a registered runtime with the same `executionMode` as the source session" and notes `INCOMPATIBLE_RUNTIME` only on `executionMode` mismatch. No isolation-profile compatibility is asserted.
- The error catalog (§15.1 lines 606–608) for derive lists `DERIVE_ON_LIVE_SESSION`, `DERIVE_LOCK_CONTENTION`, `DERIVE_SNAPSHOT_UNAVAILABLE`, but no isolation error.

A user with access to a `microvm` (Kata) session that has processed sensitive material can call `POST /v1/sessions/{id}/derive` (or replay with `workspace_derive`) targeting a `standard` (runc) pool; the derived session inherits the full workspace tar — including secrets in workspace files, partial tool outputs, and `.env`-style artifacts — but runs under weaker kernel isolation. This sidesteps the threat model that motivated the original `microvm` placement.

**Recommendation.** Apply an equivalent isolation-monotonicity gate at derive/replay time. Add to §7.1 derive semantics: "the target pool's isolation profile MUST be at least as restrictive as the source session's `sessionIsolationLevel.isolationProfile`, else reject with `ISOLATION_MONOTONICITY_VIOLATED`." Optionally accept an explicit `allowIsolationDowngrade: true` flag that requires `platform-admin` or emits an audit event (`derive.isolation_downgrade`). Update §15.1 error catalog to list `ISOLATION_MONOTONICITY_VIOLATED` as a possible response for the derive and replay endpoints, and mirror the `pool.isolation_warning` audit event from §11.7 for affected derives.

---

## SEC-002 [Medium] `shareProcessNamespace` requirement in §4.4 contradicts §13.1 blanket prohibition

**Files:** `spec/04_system-components.md` §4.4 line 246 ("SIGCONT confirmation"); `spec/13_security-model.md` §13.1 lines 16–23.

**Issue.** §4.4 SIGCONT confirmation states: *"On Linux, `/proc/{pid}/stat` is only available when `shareProcessNamespace: true` is set on the pod spec; the embedded adapter mode requires this setting for SIGCONT confirmation to function."* §13.1 is categorical in the opposite direction: `shareProcessNamespace: true` is **forbidden** on every pod template Lenny generates, the admission webhook rejects any CR that would produce such a pod with `POD_SPEC_HOST_SHARING_FORBIDDEN`, and the startup preflight Job hard-fails in production if any Lenny-managed pod template has it set. There is no carve-out for agent pods using the embedded adapter.

Two problems:

1. **Factual error.** The embedded adapter, by its own definition (§4.4 line 242, §4.7 line 812–826), runs in the **same container** as the agent process. Same-container processes share the container's PID namespace irrespective of `shareProcessNamespace`, and `/proc/<adapter_self_view>/<pid>/stat` is readable without any pod-spec change. The cited prerequisite is not required for the described polling path. This misstates the kernel/cgroup requirement and will mislead implementers.

2. **If §4.4 is taken literally**, then SIGCONT confirmation polling is *unreachable* in any conformant Lenny deployment: admission blocks the required pod shape, the adapter falls through to the `sigcont_confirmation_unavailable` warning path, and liveness detection degrades silently to the 60-second watchdog on every embedded-adapter pod. That undermines the §4.4 liveness-integration claim that `checkpointStuck` is set "immediately … avoids waiting for the full 60-second watchdog timeout."

Either way the spec ships an internally inconsistent security-critical statement.

**Recommendation.** Correct §4.4 to reflect that same-container `/proc/{pid}/stat` access does not require `shareProcessNamespace: true`, and remove the conditional fallback text. If any variant of embedded adapter does run in a sibling container, document it in §13.1 as an explicit, admission-webhook-whitelisted exception with a threat-model justification covering Token Service cache exposure.
