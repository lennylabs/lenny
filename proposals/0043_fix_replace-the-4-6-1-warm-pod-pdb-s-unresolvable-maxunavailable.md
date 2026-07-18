# Proposal: Replace the §4.6.1 warm-pod PDB's unresolvable maxUnavailable:1 with an integer minAvailable of minWarm-1

- **Status:** Verified (2026-07-18). Converged after 2 adversarial review rounds (0 findings fixed); awaiting sign-off.
- **Date:** 2026-07-18.
- **Scope:** A spec-first correction of the §4.6.1 warm-pod PodDisruptionBudget mechanism in `spec/04_system-components.md` (:491) and the matching row in `spec/17_deployment-topology.md` (:11), plus the reconciler that builds the PDB (`pkg/controller/warmpool/pdb.go`), its unit test (`pkg/controller/warmpool/pdb_test.go`), and the tier-8 chaos test that exercises the eviction path (`tests/tier8_chaos/warm_pod_eviction_test.go`). The current spec mandates `maxUnavailable: 1`, which the Kubernetes disruption controller cannot resolve for Sandbox-owned warm pods and which therefore deadlocks every warm-pod eviction at `disruptionsAllowed: 0`. This proposal changes the mechanism to an integer `minAvailable` of `minWarm - 1`, which is evaluated from the selected healthy-pod count with no scale-subresource resolution. It closes the findings T-4.6.5 (Medium) and T-4.6.18 (High). It adds no `/scale` subresource, changes no CRD, and changes no other PDB.

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off, spec edit first.

## 1. Problem

§4.6.1 (`spec/04_system-components.md:491`) requires the warm pool controller's per-pool PodDisruptionBudget to use `maxUnavailable: 1` and explicitly forbids `minAvailable: minWarm`. It selects a pool's idle warm pods via `lenny.dev/state: idle` and asserts this "allows voluntary disruptions one pod at a time" with proactive replacement so a node drain does not stall. `reconcilePDB` implements exactly this: it builds a `<pool>-warm` PDB with `MaxUnavailable: intstr.FromInt(1)` and an idle selector, never setting `MinAvailable` (`pkg/controller/warmpool/pdb.go:46-68`), and `pdb_test.go` asserts `maxUnavailable == 1` with `minAvailable` unset (`pkg/controller/warmpool/pdb_test.go:44-49`).

This cannot work as written. A `maxUnavailable` PDB requires the Kubernetes disruption controller to compute `expectedPods` by resolving each selected pod's controlling `ownerReference` to a controller that exposes a `/scale` subresource. The selected warm pods are controller-owned by per-pod `Sandbox` CRs (`ctrl.SetControllerReference(sb, pod)` at `pkg/controller/sandbox/controller.go:493`), and `Sandbox` declares only `+kubebuilder:subresource:status` with no scale subresource (`pkg/apis/lenny/v1alpha1/sandbox_types.go:168`; rendered CRD `charts/lenny/crds/lenny.dev_sandboxes.yaml:453-454`). With no scale subresource on the selected pods' controller, the disruption controller pins the PDB at `disruptionsAllowed: 0` and the eviction subresource rejects every warm-pod eviction with HTTP 429, deadlocking node drains of warm pods. This is the exact node-drain stall the `maxUnavailable: 1` choice was made to avoid.

The tier-8 chaos test that exercises this path documents the deadlock in its OPEN DEFECT comment and is kept skipped because of it (`tests/tier8_chaos/warm_pod_eviction_test.go:138-152`). Two findings record the mechanism: T-4.6.5 (Medium, TEST-GAPS.md:819) records the eviction-path coverage gap and asks which disruption fix to take, and T-4.6.18 (High, TEST-GAPS.md:896) records the observed live failure (all nine warm-pool PDBs report `ALLOWED DISRUPTIONS 0`, condition `SyncFailed`, message `"sandboxes.lenny.dev does not implement the scale subresource"`).

An integer `minAvailable` is evaluated directly from the count of selected healthy pods with no scale resolution, so it neither deadlocks nor requires a CRD change. With `minAvailable = minWarm - 1`, at the steady state of exactly `minWarm` healthy idle pods (the normal operating condition) `disruptionsAllowed = minWarm - (minWarm - 1) = 1`: the first eviction is admitted and a concurrent second is blocked, delivering the one-at-a-time behavior the spec wants, while `minWarm - 1 < minWarm` avoids the steady-state deadlock the spec attributes specifically to `minAvailable: minWarm`. The `PDBConfig` SPI already carries both `MinAvailable` and `MaxUnavailable` fields (`pkg/podlifecycle/interfaces.go:331-338`), so no interface change is needed; the live PDB CRUD is solely in `reconcilePDB` (`ManagePDB` in `pkg/podlifecycle/agentsandbox.go:529-539` is a documented no-op stub).

## 2. Decisions

- **Adopt option (b): an integer `minAvailable = minWarm - 1`.** This is evaluated by Kubernetes purely from the count of selected healthy pods and requires no scale resolution, so it clears the `disruptionsAllowed: 0` deadlock without a CRD change. At steady state (exactly `minWarm` healthy idle pods) it admits exactly one eviction and blocks a concurrent second, matching the spec's one-at-a-time intent.
- **Reject option (a): add a `/scale` subresource to `Sandbox`.** It would clear the deadlock (the disruption controller sums scale across each selected pod's distinct controller, so N idle pods each owned by a scale-1 Sandbox yield `expectedPods = N` and `disruptionsAllowed = 1` under `maxUnavailable: 1`), but it introduces a new `/scale` API endpoint on a resource that represents a single pod. A `Sandbox` is never scalable, so the subresource is semantically misleading (`kubectl scale` and HPA would appear to target it) and it fails the project's minimal-new-surface test. It also requires `spec.replicas`/`status.replicas` fields, controller code to populate `status.replicas`, and a CRD regeneration across `pkg/apis` and `charts`. Option (b) reuses the existing `minAvailable` PDB semantics and the existing `PDBConfig.MinAvailable` field.
- **The strict one-at-a-time cap that `maxUnavailable: 1` provides is unnecessary for these pods.** The PDB selects only idle warm pods that carry no active session, so the only effect of evicting several at once is transient pool depletion, which proactive replacement heals. `minAvailable = minWarm - 1` relaxes the simultaneity cap above steady state in exchange for not needing a scale subresource, and the relaxation is operationally benign because the selected pods are session-less.
- **Tear the PDB down when `minWarm < 2`** (currently the threshold is `minWarm <= 0`). A pool with `minWarm == 1` cannot be given one-at-a-time protection by an integer `minAvailable` without either deadlocking (`minAvailable: 1` on a single-pod pool) or being a no-op (`minAvailable: 0`), so no PDB is created for it, matching the existing scaled-to-zero teardown behavior.
- **Use `intstr.FromInt` (an integer), never a percentage.** A percentage `minAvailable` re-enters the `getExpectedScale` path and would reintroduce the same scale-subresource deadlock; only an integer `minAvailable` is evaluated from the selected-pod count.
- **On the update path, set `MinAvailable` and explicitly clear `MaxUnavailable` (set it to nil).** A PDB spec must carry exactly one of `minAvailable`/`maxUnavailable`, so an existing object previously reconciled with `maxUnavailable: 1` must have that field cleared or the `Update` is rejected by API validation. No deployments exist in the wild, so this is a within-deployment convergence step rather than a migration shim.
- **Spec-first, per `spec-driven-development.md`.** Land the §4.6.1 and §17 amendments, then the `reconcilePDB` change and its unit test, then un-skip and align the tier-8 chaos test that already exists.
- **The named tier-8 test already exists.** The deliverable is to un-skip and align `tests/tier8_chaos/warm_pod_eviction_test.go::TestWarmPodEvictionProactiveReplacement` rather than author a new one. It already issues a `policy/v1` Eviction, asserts one-at-a-time admission with a second 429, and asserts proactive replacement restores the pre-eviction idle count.

## 3. The disruption mechanism after the change

All warm-pod PDB CRUD lives in `reconcilePDB` (`pkg/controller/warmpool/pdb.go`); `ManagePDB` is a no-op stub. After the change, for a pool with `minWarm >= 2` the reconciler owns a `<pool>-warm` PDB with:

- `spec.minAvailable = minWarm - 1` (an integer via `intstr.FromInt`).
- `spec.selector` matching `lenny.dev/pool: <pool>` and `lenny.dev/state: idle` (unchanged).
- No `spec.maxUnavailable` (cleared to nil on the convergence path).

Kubernetes evaluates `disruptionsAllowed = currentHealthy - minAvailable`, where `currentHealthy` is the count of selected Ready pods, with no scale resolution. At steady state (`currentHealthy == minWarm`), `disruptionsAllowed = minWarm - (minWarm - 1) = 1`. The first warm-pod eviction is admitted; while that pod is unavailable a concurrent second is rejected with 429. When the pool holds surplus idle pods above `minWarm`, `disruptionsAllowed` rises with the surplus and more than one concurrent eviction can be admitted; this is benign because the selected pods are session-less and the WarmPoolController replaces each evicted pod to restore `minWarm`. A pool with `minWarm < 2` gets no PDB.

## 4. Proposed changes

### SPEC-1a. Amend §4.6.1 to mandate integer minAvailable = minWarm-1

**Target:** `spec/04_system-components.md` §4.6.1 (Disruption protection for agent pods), the warm-pool PDB paragraph (`:491`).

**Rationale:** The current text mandates `maxUnavailable: 1` and forbids `minAvailable`, but `maxUnavailable` cannot resolve `expectedPods` for warm pods whose controlling owner is the status-only per-pod `Sandbox` CR (`pkg/controller/sandbox/controller.go:493`, `sandbox_types.go:168`, `charts/lenny/crds/lenny.dev_sandboxes.yaml:453-454`), so it deadlocks every warm-pod eviction at `disruptionsAllowed: 0`. The spec must describe a disruption mechanism the ownership graph supports. An integer `minAvailable` is evaluated from the selected healthy-pod count with no scale resolution, and `minWarm - 1 < minWarm` avoids the deadlock the spec pins on `minAvailable: minWarm`.

**Anchor:** Replace the sentences from "The PDB MUST use `maxUnavailable: 1`" through "to restore `minWarm`." Keep the opening sentence ("The warm pool controller can optionally create a PDB **per `SandboxTemplate`** for warm (unclaimed) pods.") and everything from "The PDB targets only unclaimed (warm) pods via a label selector" onward unchanged.

**Change (staged text).** Replace:

```
The PDB MUST use `maxUnavailable: 1` rather than `minAvailable: minWarm`. Using `minAvailable: minWarm` causes a deadlock at steady state: when exactly `minWarm` idle pods exist (the normal operating condition), the PDB allows zero evictions, permanently stalling node drains. `maxUnavailable: 1` allows voluntary disruptions one pod at a time while limiting simultaneous impact. When a warm pod is evicted, the WarmPoolController proactively creates a replacement pod immediately to restore `minWarm`.
```

with:

```
The PDB MUST use an integer `minAvailable` of `minWarm - 1` rather than `maxUnavailable: 1`. The warm pods are controller-owned by per-pod `Sandbox` custom resources, which expose only a status subresource. A `maxUnavailable` PDB requires the Kubernetes disruption controller to resolve a `/scale` subresource on each selected pod's controller to compute `expectedPods`; the `Sandbox` CR has no such subresource, so a `maxUnavailable` budget cannot compute `expectedPods`, sits permanently at `disruptionsAllowed: 0`, and stalls every warm-pod eviction. An integer `minAvailable` is evaluated directly from the count of selected healthy pods, with no scale resolution. The PDB MUST NOT use `minAvailable: minWarm`: at exactly `minWarm` idle pods (the normal operating condition) it allows zero evictions, permanently stalling node drains. `minAvailable = minWarm - 1` admits exactly one eviction at that steady state (`minWarm - (minWarm - 1) = 1`), providing one-at-a-time voluntary disruption without a scale subresource, and it keeps a warm-capacity floor available during voluntary disruption so new-session latency does not spike during a multi-node drain. Because an integer `minAvailable` caps disruptions relative to the current idle count rather than to a fixed one, a pool holding surplus idle pods above `minWarm` can admit more than one concurrent warm-pod eviction; this is accepted because the selected pods carry no active session (`lenny.dev/state: idle`) and proactive replacement restores `minWarm`. A pool with `minWarm` below 2 gets no PDB, because a single warm pod cannot be given one-at-a-time protection without deadlocking. When a warm pod is evicted, the WarmPoolController proactively creates a replacement pod immediately to restore `minWarm`.
```

**Preserved unchanged:** the "can optionally create" framing and the per-`SandboxTemplate` keying, the `lenny.dev/state: idle` selector, the proactive-replacement sentence, the recycle/hold-window exclusion paragraph, and the preStop-hook protection for active session pods (`:489`).

### SPEC-1b. Align the §17 topology row to the new mechanism

**Target:** `spec/17_deployment-topology.md` the "Agent Pods" row of the topology table (`:11`).

**Rationale:** §17:11 independently describes this same PDB as "optional PDB per pool on warm (idle) pods to enforce `minWarm` during voluntary disruption." After SPEC-1a the PDB enforces a floor of `minWarm - 1`, and a pool with `minWarm == 1` gets no PDB at all, so the "enforce `minWarm`" phrasing becomes false. An amendment that changes the enforced floor and the existence condition of the PDB must update the other spec section that states that floor, or the two sections disagree.

**Change (staged text).** In the "Agent Pods" row's third cell, replace:

```
optional PDB per pool on warm (idle) pods to enforce `minWarm` during voluntary disruption
```

with:

```
optional PDB per pool on warm (idle) pods holding a warm floor of `minWarm - 1` during voluntary disruption (no PDB below `minWarm` 2)
```

**Note (not changed here):** `spec/17_deployment-topology.md:979` states "the preStop checkpoint guarantees the PDB was introduced to preserve," which conflates the active-pod checkpoint PDB with the warm-idle PDB. That muddle is pre-existing and independent of this change; it is left untouched (see Non-goals).

### CODE-1. Change reconcilePDB to build minAvailable = minWarm-1 and tear down below minWarm 2

**Target:** `pkg/controller/warmpool/pdb.go`, `reconcilePDB` (`:24-92`).

**Rationale:** `reconcilePDB` is the sole live warm-pool PDB CRUD (`ManagePDB` at `pkg/podlifecycle/agentsandbox.go:529-539` is a documented no-op). It currently sets `MaxUnavailable: intstr.FromInt(1)` with a `// spec: §4.6.1` citation encoding the mandate this proposal amends. `pool.Spec.MinWarm` is already read in this function (`:35`), so the `minWarm - 1` value is in scope with no new inputs.

**Change (staged description).**

1. Teardown guard (`:35`): change `if pool.Spec.MinWarm <= 0` to `if pool.Spec.MinWarm < 2`, so a single-warm-pod pool imposes no PDB.
2. Value (`:46`): replace `maxUnavailable := intstr.FromInt(1)` with `minAvailable := intstr.FromInt(int(pool.Spec.MinWarm) - 1)`.
3. Spec build (`:56-67`): set `MinAvailable: &minAvailable` instead of `MaxUnavailable: &maxUnavailable`. Update the inline `// spec: §4.6.1` comment (`:57-59`) to state the integer-`minAvailable` rationale: no scale subresource is required, and `minWarm - 1` avoids the steady-state deadlock the spec attributes to `minAvailable: minWarm`.
4. Convergence path (`:86-87`): set `existing.Spec.MinAvailable = &minAvailable` and `existing.Spec.MaxUnavailable = nil`. Clearing the mutually-exclusive field is required so the `Update` passes API validation for an object previously reconciled with `maxUnavailable: 1`. Keep the existing `existing.Spec.Selector = pdb.Spec.Selector` line.
5. Function doc comment (`:24-31`): rewrite to describe `minAvailable = minWarm - 1`, the one-at-a-time-at-steady-state behavior, and the `minWarm < 2` teardown.

Run `gofumpt` and `goimports`. Use `intstr.FromInt` only, never a percentage.

The staged spec block for the object build reads:

```go
minAvailable := intstr.FromInt(int(pool.Spec.MinWarm) - 1)
pdb := &policyv1.PodDisruptionBudget{
	ObjectMeta: metav1.ObjectMeta{
		Name:      key.Name,
		Namespace: key.Namespace,
		Labels: map[string]string{
			LabelPool:    pool.Name,
			LabelManaged: "true",
		},
	},
	Spec: policyv1.PodDisruptionBudgetSpec{
		// spec: §4.6.1 — an integer minAvailable of minWarm-1. A
		// maxUnavailable budget cannot resolve expectedPods because the
		// selected warm pods are owned by a status-only Sandbox CR with no
		// /scale subresource; an integer minAvailable is evaluated from the
		// selected healthy-pod count. minWarm-1 admits exactly one eviction
		// at steady state (minWarm idle pods) and avoids the minAvailable:
		// minWarm deadlock.
		MinAvailable: &minAvailable,
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				LabelPool:        pool.Name,
				state.LabelState: string(state.Idle),
			},
		},
	},
}
```

and the convergence path reads:

```go
// Converge the spec in case minWarm/selector drifted; the selector,
// minAvailable, and the cleared maxUnavailable are the fields the
// controller owns. maxUnavailable is set to nil so an object previously
// reconciled with maxUnavailable: 1 passes the exactly-one-of validation.
existing.Spec.MinAvailable = &minAvailable
existing.Spec.MaxUnavailable = nil
existing.Spec.Selector = pdb.Spec.Selector
```

### TEST-1. Update the reconcilePDB unit test to assert minAvailable = minWarm-1 and the minWarm<2 teardown

**Target:** `pkg/controller/warmpool/pdb_test.go`, `TestReconcileCreatesWarmPodPDB` (`:34-57`) and `TestReconcileDeletesPDBWhenScaledToZero` (`:61-84`).

**Rationale:** `TestReconcileCreatesWarmPodPDB` currently asserts `MaxUnavailable.IntValue() == 1` and `MinAvailable == nil` (`:44-49`), the exact assertions the fix inverts. `TestReconcileDeletesPDBWhenScaledToZero` uses `pool(2, 10)`, which still creates a PDB (`minWarm == 2` gives `minAvailable == 1`) and still tears it down at zero, so its structure survives.

**Change (staged description).**

- In `TestReconcileCreatesWarmPodPDB` (`pool` is `pool(3, 10)`, so `minWarm == 3`): assert `pdb.Spec.MinAvailable` is non-nil and `IntValue() == 2` (`minWarm - 1`), and assert `pdb.Spec.MaxUnavailable == nil`. Update the spec-comment at `:31-33` to describe `minAvailable: minWarm - 1` rather than `maxUnavailable: 1`. Keep the idle-selector and single-`SandboxWarmPool`-owner assertions.
- Add a boundary case asserting a pool with `minWarm == 1` produces no PDB (mirroring the scaled-to-zero teardown), pinning the new `minWarm < 2` threshold. For example, reconcile `pool(1, 10)` and assert `getPDB` returns `ok == false`.
- Add a convergence case: seed an existing `<pool>-warm` PDB carrying `maxUnavailable: 1` (the pre-fix object), reconcile `pool(3, 10)`, and assert the converged object has `MinAvailable.IntValue() == 2` and `MaxUnavailable == nil`, pinning the exactly-one-of clearing on the update path.
- `TestReconcileDeletesPDBWhenScaledToZero` keeps `pool(2, 10)`; add an assertion that the created PDB now carries `MinAvailable.IntValue() == 1`, then the existing scale-to-zero teardown assertion is unchanged.

Every test keeps a `// spec: 4.6.1` annotation.

### TEST-2. Un-skip and align the tier-8 chaos eviction test to the minAvailable mechanism

**Target:** `tests/tier8_chaos/warm_pod_eviction_test.go`, `TestWarmPodEvictionProactiveReplacement` (`:134-235`), the `pdbMaxUnavailable` helper (`:107-120`), and the header and diagnosis comments (`:5-12`, `:122-133`).

**Rationale:** This is the tier-8 test the deliverable names. It already issues a `policy/v1` Eviction (`:48-63`), asserts the first is admitted and a concurrent second is 429'd (`:185-218`), and asserts proactive replacement restores the pre-eviction idle count (`:224-234`). It is gated inactive by `t.Skip` (`:150-152`) with an OPEN DEFECT comment (`:138-149`) naming the scale-subresource deadlock; once the mechanism is fixed the Skip must go. Under `minAvailable` the one-at-a-time property holds only when the pool is at steady state (idle count == `minWarm`), so the test must pin that precondition.

**Change (staged description).**

- Remove the OPEN DEFECT comment block and `t.Skip` (`:138-152`).
- Replace the `pdbMaxUnavailable` helper and its precondition check (`:107-120`, `:170-177`) with a read of `.spec.minAvailable`; assert it is set and equals `minWarm - 1`, reading `minWarm` from the `SandboxWarmPool` CR.
- Strengthen pool selection (`:154-167`) to require idle count == `minWarm >= 2` so `disruptionsAllowed` starts at exactly 1 and the second-eviction-blocked assertion is deterministic; skip on precondition-not-met as the test already does.
- Keep the eviction subresource call, the first-admitted assertion (`:185-190`), the concurrent-second-blocked assertion (`:200-218`), and the replenish-to-`preCount` assertion (`:224-234`) unchanged.
- Update the file header (`:5-12`) and the diagnosis comment (`:122-133`) to describe the `minAvailable = minWarm - 1` mechanism and the steady-state one-at-a-time property instead of `maxUnavailable: 1`.
- Confirm the `//go:build chaos` tag stays.

### DOC-1. Mark findings T-4.6.5 and T-4.6.18 resolved on application

**Target:** `TEST-GAPS.md`, T-4.6.5 (`:819-825`) and T-4.6.18 (`:896-902`).

**Rationale:** Both findings record this exact mechanism as OPEN and both point to a spec decision on which disruption fix to take. This proposal decides it (option b), and the tier-8 test that T-4.6.5 names becomes live via TEST-2. T-4.6.18 records the live `disruptionsAllowed: 0` / `SyncFailed` failure and asks the same question; it is resolved by the same change.

**Change (staged description).** Flip the T-4.6.5 and T-4.6.18 checkboxes to resolved. Reference this proposal and the now-unskipped `tests/tier8_chaos/warm_pod_eviction_test.go::TestWarmPodEvictionProactiveReplacement`, noting the fix chose integer `minAvailable = minWarm - 1` over a `Sandbox` `/scale` subresource. Applied at implementation time, consistent with how findings are closed.

## 5. Non-goals

- No `/scale` subresource added to `Sandbox` or `SandboxWarmPool` (option a rejected). No change to `pkg/apis/lenny/v1alpha1` type markers or to the rendered CRDs under `charts/lenny/crds`.
- No change to the `ManagePDB` SPI signature or the `PDBConfig` struct. Both `MinAvailable` and `MaxUnavailable` fields already exist (`pkg/podlifecycle/interfaces.go:331-338`), and `ManagePDB` remains the v1 no-op stub; the live change is confined to `reconcilePDB`.
- No percentage-based `minAvailable`. Only an integer value avoids the scale-resolution path; a percentage would reintroduce the deadlock.
- No change to the preStop-hook checkpoint protection for active session pods (`spec/04_system-components.md:489`) or to the recycle/hold-window exclusion behavior.
- No reconciliation of the pre-existing "can optionally create" wording against `reconcilePDB`'s unconditional creation, nor of the per-`SandboxTemplate` (spec) versus per-`SandboxWarmPool` (code) keying. Both are pre-existing and independent of the disruption-mechanism defect.
- No change to `spec/17_deployment-topology.md:979`, whose "the PDB was introduced to preserve [preStop checkpoint guarantees]" conflates the active-pod checkpoint PDB with the warm-idle PDB. The conflation is pre-existing and this proposal does not introduce it.
- No new node-drain or checkpoint-before-eviction coverage; that is the separate T-4.6.4 finding.
- No change to any rate-limit, quota, or unrelated PDB (the gateway and webhook PDBs exercised by `tests/tier8_chaos/pod_disruption_test.go` are untouched).

## 6. Testing

The change reaches tier 0 (static), tier 2 (the warm pool reconciler writing the kube-apiserver, exercised through the fake-client unit test in `pkg/controller/warmpool`), and tier 8 (chaos: an eviction against a live Kind warm pool) per `.claude/rules/test-coverage.md`. The spec edits (SPEC-1a, SPEC-1b) carry no runtime behavior and are covered by the tier-0 static suite plus spec-map validation. Each test below covers a non-happy path and carries a `// spec:` tie.

- **tier-2 reconciler, minAvailable value (TEST-1, boundary):** In `pkg/controller/warmpool/pdb_test.go`, `TestReconcileCreatesWarmPodPDB` reconciles `pool(3, 10)` and asserts `MinAvailable.IntValue() == 2` (`minWarm - 1`) with `MaxUnavailable == nil`, pinning that the reconciler builds the scale-free budget the spec now mandates. `// spec: 4.6.1 (warm-pool PDB minAvailable=minWarm-1)`.
- **tier-2 reconciler, single-pod teardown (TEST-1, boundary):** A case reconciling `pool(1, 10)` asserts no `<pool>-warm` PDB exists, pinning the `minWarm < 2` teardown threshold. The non-happy path is the below-threshold pool that must not receive a deadlocking `minAvailable: 1` budget. `// spec: 4.6.1 (no PDB below minWarm 2)`.
- **tier-2 reconciler, convergence clears maxUnavailable (TEST-1, spec-named-failure):** A case that seeds a pre-existing `maxUnavailable: 1` PDB and reconciles `pool(3, 10)` asserts the converged object carries `MinAvailable.IntValue() == 2` and `MaxUnavailable == nil`. The non-happy path is the exactly-one-of API validation that rejects an `Update` leaving both fields set. `// spec: 4.6.1 (PDB spec carries exactly one of minAvailable/maxUnavailable)`.
- **tier-8 chaos, eviction admitted and one-at-a-time (TEST-2, spec-named-failure):** `tests/tier8_chaos/warm_pod_eviction_test.go::TestWarmPodEvictionProactiveReplacement`, un-skipped, evicts a warm idle pod at steady state (idle count == `minWarm >= 2`), asserts the first eviction is admitted, asserts a concurrent second is 429'd while the first pod is unavailable, and asserts the WarmPoolController replenishes the pool to its pre-eviction idle count. The non-happy path is the node-drain deadlock the fix removes: a rejected first eviction means the budget still sits at `disruptionsAllowed: 0`. It also asserts `.spec.minAvailable == minWarm - 1` before injecting the disruption. `// spec: 4.6.1 (warm-pod eviction admitted one at a time, proactive replacement)`.

## 7. Findings closed on application

This proposal closes T-4.6.5 (PDB-mediated warm-pod eviction with proactive replacement is unexercised, Medium) and T-4.6.18 (warm-pod PDB sits at `disruptionsAllowed: 0` and deadlocks node drains, High). Both record the scale-subresource deadlock and ask which disruption fix to take; this proposal resolves that question in favor of integer `minAvailable = minWarm - 1` (option b), and TEST-2 un-skips the tier-8 test that both findings name. The changes are applied at spec-edit, code, and test time and need no operator hardware beyond the existing Kind cluster the tier-8 test already uses.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions carried in the draft made two changes that are folded into the staged changes above. First, SPEC-1 was split into SPEC-1a and SPEC-1b so the amendment also updates `spec/17_deployment-topology.md:11`, whose "enforce `minWarm`" phrasing would otherwise contradict the new `minWarm - 1` floor and the `minWarm < 2` teardown; SPEC-1a additionally names the retained purpose of the PDB (keeping a warm-capacity floor available during voluntary disruption) so the session-less-benign argument for relaxing the simultaneity cap is not mistaken for an argument against having a PDB at all. Second, DOC-1 was extended to close T-4.6.18 (High) alongside T-4.6.5, since both findings record the same scale-subresource deadlock and the same spec decision.

## 9. Open decisions for review

### Accepting the relaxed one-at-a-time cap — proposed: option (b)

This proposal chooses option (b) (integer `minAvailable = minWarm - 1`), which caps warm-pod evictions at one only at steady state and may admit several concurrent evictions when the pool holds surplus idle pods above `minWarm`. Option (a) (a `/scale` subresource on `Sandbox`) would preserve the strict one-at-a-time cap regardless of pool size, at the cost of a semantically misleading scale endpoint on a single-pod resource plus a CRD change. Confirm the relaxed cap is acceptable given the selected pods are session-less idle pods; if strict simultaneity capping is required, switch to option (a).

### minWarm == 1 handling — proposed: no PDB below minWarm 2

This proposal tears down the PDB for pools with `minWarm` below 2, because a single warm pod cannot get one-at-a-time protection without deadlocking. Confirm that no protection for single-warm-pool pools is acceptable, versus creating a no-op `minAvailable: 0` PDB.

## 10. Files touched on application

- `spec/04_system-components.md`: SPEC-1a (§4.6.1 warm-pool PDB paragraph, `maxUnavailable: 1` → integer `minAvailable = minWarm - 1`, `:491`).
- `spec/17_deployment-topology.md`: SPEC-1b (Agent Pods topology row, "enforce `minWarm`" → "warm floor of `minWarm - 1` (no PDB below `minWarm` 2)", `:11`).
- `pkg/controller/warmpool/pdb.go`: CODE-1 (`reconcilePDB` builds `minAvailable = minWarm - 1`, clears `maxUnavailable` on convergence, tears down below `minWarm` 2, `:24-92`).
- `pkg/controller/warmpool/pdb_test.go`: TEST-1 (assert `minAvailable = minWarm - 1`, the `minWarm < 2` teardown, and the convergence clearing of `maxUnavailable`, `:34-84`).
- `tests/tier8_chaos/warm_pod_eviction_test.go`: TEST-2 (un-skip and align the eviction test to the `minAvailable` mechanism and the steady-state precondition, `:134-235`).
- `TEST-GAPS.md`: DOC-1 (mark T-4.6.5 and T-4.6.18 resolved, `:819`, `:896`).
</content>
</invoke>
