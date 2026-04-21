# Iter5 — Perspective 16: Warm Pool & Pod Lifecycle

**Scope:** `spec/06_warm-pod-model.md`, `spec/07_session-lifecycle.md`, `spec/04_system-components.md` §4.7/4.8/4.9 (incl. §4.6.1 / §4.6.3).

## Iter4 carry-over audit

Iter4 recorded exactly four warm-pool-lifecycle findings. All were reported **Fixed**. There were no Skipped or Deferred iter4 WPL findings; the single Deferred item in iter4's index (`SEC-009`) is delegation/file-export security, not warm-pool scope.

Verification of each iter4 Fixed item against the current spec:

| Iter4 finding | Status | Verification |
|---|---|---|
| **WPL-001** — Schedulability precondition missing on `scrub_warning` cleanup transition | **HELD** | `spec/06_warm-pod-model.md:153` now includes `host node is unschedulable → draining [scrub_warning]`; `spec/06_warm-pod-model.md:155` includes the schedulable counterpart for the `sdk_connecting [scrub_warning]` edge. The "Host-node schedulability precondition" paragraph (`spec/06_warm-pod-model.md:181`) explicitly states "the rule applies identically to the scrub-success and scrub-warning preConnect edges". |
| **WPL-002** — Gateway lacks Node RBAC + informer to evaluate schedulability | **HELD** | Evaluation was moved off the gateway entirely. `spec/04_system-components.md:481` ("Host-node schedulability labeling") makes the WarmPoolController the sole evaluator and surfaces the result as the pod label `lenny.dev/host-schedulable`; Gateway ServiceAccount RBAC (`spec/04_system-components.md:588`) grants only `get`/`patch` on `Pods` — no Node verbs — and §6.2 line 181 reads the label via that existing grant. `spec/04_system-components.md:586` adds `watch` on `Nodes` to the WPC ServiceAccount for the informer. |
| **WPL-003** — preStop cap-selection alert not computable from emitted metric labels | **HELD** | `spec/16_observability.md:41` emits `lenny_prestop_cap_selection_total` labeled by `pool`, `service_instance_id`, and `source ∈ {postgres, postgres_null, cache_hit, cache_miss_max_tier}`; the `PreStopCapFallbackRateHigh` rule at `spec/16_observability.md:438` groups `by (service_instance_id, pool)` to evaluate per-replica share. Producer side is wired at `spec/10_gateway-internals.md:114` (cap selection) and `spec/10_gateway-internals.md:165` (barrier-target source, separate counter). |
| **WPL-004** — Unschedulable-node branch missing from `task_cleanup` state diagram | **HELD** | `spec/06_warm-pod-model.md:152-155` enumerates all four preConnect re-warm edges: schedulable vs. unschedulable × scrub-success vs. scrub_warning, each cross-referring to the preCondition paragraph. |

## New findings

No new deployment-blocking or reliability-class bugs identified.

The following were considered and rejected as non-findings / out-of-scope / polish-class:

- **Label-staleness race at `task_cleanup` entry.** Gateway reads `lenny.dev/host-schedulable` at the moment of the `task_cleanup` decision and "does not re-check during SDK re-warm" (`spec/06_warm-pod-model.md:181`). A node that is cordoned in the gap between the WPC's last reconcile and the gateway's read could in principle be issued one more `sdk_connecting` transition. The spec bounds this window via WPC's "re-labels each affected pod within a single reconcile cycle (typically < 1 s per batch at Tier 3 concurrency)" (`spec/04_system-components.md:481`) and the absent-label fail-safe (`If the label reads "false" (or is absent, which is treated as unschedulable for fail-safe behavior)`). The worst-case consequence is a single extra SDK re-warm on a node whose eviction is imminent — the pod will then drain via the standard `idle → draining` eviction path. This is consistent with the severity calibration in iter3/iter4 (the original EXM-008 was scoped to the transition semantics, not the label-latency race). Not a new defect.
- **Gateway reads label via `get Pods` at the exact moment of transition, yet §4.6.3 gateway RBAC already lists `get`/`patch` on `Pods`.** No new RBAC gap. The spec text is self-consistent.
- **`lenny-label-immutability` exclusion for `lenny.dev/host-schedulable`.** Correctly carved out at `spec/04_system-components.md:481` ("explicitly omitted so that WPC can flip the value on every cordon/uncordon event"). WPC as writer + label immutability exclusion are both present.
- **Pre-attached failure retry policy (§6.2 line 296-303).** Invariants around not advancing `recovery_generation` and not surfacing warm-pool retries as client-visible transitions are preserved. No regression from iter4.
- **Circuit-breaker state persistence (§6.1 line 52-63).** PSC-owned status carve-out (`status.sdkWarmCircuitBreaker.*`) is explicitly enumerated in the CRD ownership table (`spec/04_system-components.md:571`) with `minOpenUntil` grace carried across PSC leader handoff. The ownership model is coherent.

## Convergence assessment

**Converged for warm-pool & pod-lifecycle scope.**

All four iter4 WPL findings are Fixed and the fixes hold in the current spec text, with cross-document consistency between `spec/06_warm-pod-model.md`, `spec/04_system-components.md` §4.6.*, `spec/10_gateway-internals.md` §10.1, and `spec/16_observability.md` §16.1/§16.5. No Skipped/Deferred iter4 items remain in this perspective. Iter5 surfaces no new correctness or reliability defects in warm-pool, pod state machine, preConnect re-warm, schedulability labeling, or preStop cap-selection semantics.
