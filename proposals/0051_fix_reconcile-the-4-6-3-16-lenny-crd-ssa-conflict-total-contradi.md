# Proposal: Reconcile the §4.6.3/§16 lenny_crd_ssa_conflict_total contradiction and wire the stuck-conflict counter and log into retryOnConflictSSA

- **Status:** Verified (2026-07-19). Converged after 4 adversarial review rounds (3 findings fixed); awaiting sign-off.
- **Date:** 2026-07-19.
- **Scope:** A spec-first reconciliation of the `lenny_crd_ssa_conflict_total` SSA-conflict retry signal across `spec/16_observability.md` (the §16.1 counter row at `:271` and the §16.5 `CRDSSAConflictStuck` alert row at `:535`) and `spec/04_system-components.md` (§4.6.3 item 3 at `:607`), plus the code that must emit the signal. Both packages that carry a byte-divergent copy of `retryOnConflictSSA` (`pkg/controller/sandbox/controller.go:48-76` and `pkg/controller/warmpool/controller.go:47-74`) implement neither the `crd_ssa_conflict_stuck` structured log nor the counter, and they give up after five attempts rather than continuing. The counter exists only as a catalog metadata row (`pkg/observability/metrics/catalog.go:356`) with no backing collector, so the `CRDSSAConflictStuck` alert (`pkg/alerting/rules/rules.go:1458-1465`) can never fire. This proposal consolidates the two copies into a single `pkg/controller/ssaretry` helper that registers the counter and emits the log and counter at the five-consecutive-409 boundary, amends the three spec sections to one consistent reading, updates the alert description in the rule source and its three generated copies, and adds the counter to the metrics reference. It closes findings T-4.6.7 (Medium), T-4.6.17 (Medium), and T-16.1.10 (Low).

This document stages the proposed spec, code, doc, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off, spec edit first.

## 1. Problem

The SSA-conflict retry policy in §4.6.3 and its `lenny_crd_ssa_conflict_total` counter are defined incompatibly across two spec sections, and the shipped `retryOnConflictSSA` implements neither reading.

§4.6.3 item 3 (`spec/04_system-components.md:607`) mandates that after 5 consecutive 409s without progress the controller emits a `crd_ssa_conflict_stuck` structured log event (labeled `controller`, `resource`, `name`), increments the counter, then "continues with exponential backoff." §16.1 (`spec/16_observability.md:271`) defines the same counter as incrementing "on every Server-Side Apply conflict observed," labeled `crd`, `controller`. The two sections disagree on increment timing (once per stuck episode versus once per conflict) and on the increment condition. The §16.5 `CRDSSAConflictStuck` alert (`spec/16_observability.md:535`; `pkg/alerting/rules/rules.go:1460`) fires when the counter "for any single resource exceeds 10 in a 5-minute window," a per-resource granularity that the `crd`/`controller` label set does not carry and that the shipped bare-series expression `increase(lenny_crd_ssa_conflict_total[5m]) > 10` does not provide.

Both copies of `retryOnConflictSSA` (`pkg/controller/sandbox/controller.go:48-76` and `pkg/controller/warmpool/controller.go:47-74`) loop `for attempt := 0; attempt < maxAttempts` with `const maxAttempts = 5` and `return lastErr` on exhaustion. They give up after five attempts rather than continuing, and they emit neither the log nor the counter. The two copies are byte-divergent: the sandbox copy names an intermediate `sleep` variable while the warmpool copy inlines it. There are nine call sites across the two packages (`pkg/controller/sandbox/controller.go:660`, `:684`, `:828`; `pkg/controller/warmpool/controller.go:734`, `:832`; `pkg/controller/warmpool/gc.go:437`; `pkg/controller/warmpool/occupancy.go:234`; `pkg/controller/warmpool/pod_reconciler.go:338`, `:650`).

The counter exists only as a catalog metadata row (`pkg/observability/metrics/catalog.go:356`) with no backing collector, so nothing increments it and the alert that references it (`pkg/alerting/rules/rules.go:1460`, `pkg/embedded/manifests/manifests.yaml:2568`, `docs/alerting/rules.yaml:1084`, `charts/lenny/files/alerting-rules.yaml:1081`) has no series to fire on. `docs/reference/metrics.md` has no entry for the counter.

TEST-GAPS records the state as three OPEN findings. T-4.6.7 (Medium, `TEST-GAPS.md:843`) records that the SSA conflict retry policy including the stuck counter has no direct test. T-4.6.17 (Medium, `TEST-GAPS.md:898`) records that `retryOnConflictSSA` implements neither the stuck signal nor the continue-past-five behavior and that the counter is unregistered. T-16.1.10 (Low, `TEST-GAPS.md:7112`) records the §4.6/§16.1 increment-semantics contradiction and defers it to the proposal pipeline. `pkg/controller/warmpool/ssa_retry_internal_test.go:251` carries a skipped test, `TestRetryOnConflictSSAEmitsStuckSignalAfterFiveConflicts`, that documents the intended assertions and is blocked on this decision.

## 2. Decisions

- **Adopt §4.6.3's increment-after-five-consecutive-no-progress semantics as authoritative; amend §16.1 to match.** Under this reading the counter measures stuck episodes, so the §16.5 alert fires only on a genuine inability to converge. §16.1's every-conflict reading would count the benign 409s that normal crash-recovery retries resolve within a few attempts, making the alert noisy at a rate driven by ordinary reconcile contention rather than by an ownership dispute. Adopting the stuck-episode reading also co-locates the counter increment with the `crd_ssa_conflict_stuck` log emission at one site (loop exhaustion), so the metric and the log stay correlated.
- **Keep the counter's label set at §16.1's `crd`, `controller`.** Both labels are bounded: `crd` ranges over `{Sandbox, SandboxTemplate, SandboxWarmPool, SandboxClaim}` and `controller` over the two field managers. Do not add a per-resource `name` label. §16.1.1 (`spec/16_observability.md:310`) forbids per-resource identifiers as metric labels and permits only `tenant_id` as an unbounded label. Sandbox conflict counts are one series per pod, which would produce time-series explosion. Per-resource attribution lives on the `crd_ssa_conflict_stuck` structured log (labeled `controller`, `resource`, `name`), which §16.1.1 sanctions as the place for per-resource identity.
- **Amend the §16.5 `CRDSSAConflictStuck` alert description to drop the "any single resource" claim.** The counter carries no per-instance dimension, so the description must match the `crd`/`controller` label set. The bare-series expression `increase(lenny_crd_ssa_conflict_total[5m]) > 10` stays valid and fires per (`crd`, `controller`) series; the description directs operators to the correlated `crd_ssa_conflict_stuck` log to identify the specific resource. The threshold and the 5-minute window are unchanged.
- **Reconcile "continues with exponential backoff" as continuation via controller-runtime requeue.** `retryOnConflictSSA` emits the stuck log and increments the counter at the fifth consecutive no-progress 409, then returns `lastErr`. Controller-runtime re-enqueues the reconcile through its exponential rate-limiter, which re-enters `retryOnConflictSSA` on the next reconcile from a fresh read. This keeps the existing give-up-at-five loop structure, avoids starving the reconcile work queue by holding one item in a hot in-process retry loop, and satisfies §4.6.3's continuation requirement through the requeue. §4.6.3 item 3 is amended to state the continuation mechanism explicitly.
- **Register the counter as a live `CounterVec` inside the retry helper's own package and consolidate both copies of `retryOnConflictSSA` into one shared helper.** The helper carries the conflict identity (`controller`, `crd`, `namespace`, `name`) so the emission wiring exists once and the eight CRD call sites emit the counter and log identically. The ninth call site (`pkg/controller/warmpool/pod_reconciler.go:338`, `reconcileHostSchedulable`) SSA-applies the host-schedulable label to a `corev1.Pod` under the sole `WarmPoolController` field manager, which is not a CRD field owned by another field manager, so it passes an empty CRD kind and shares the retry loop without emitting either signal. There is a single canonical implementation per concern.

## 3. The retry and emit path after the change

All SSA-conflict retry logic moves to a new `pkg/controller/ssaretry` package, the structural analog of `pkg/controller/poolscaling/admission_retry.go`. `RetryOnConflictSSA(ctx, id, apply)` keeps the existing loop body: `maxAttempts = 5`, jittered backoff (100ms initial, 2s ceiling), context-cancellation honoring, `IsConflict` gating, and non-conflict early return. The `ConflictID{Controller, CRD, Namespace, Name}` value passed by each caller supplies the labels for both the log and the counter.

When all five attempts are no-progress 409s and the `ConflictID` carries a CRD kind, before returning `lastErr`, the helper emits one `crd_ssa_conflict_stuck` structured log event (fields `controller`, `resource`, `name`, with `namespace` as context) and increments `lenny_crd_ssa_conflict_total{crd, controller}` by exactly one. It then returns `lastErr` so controller-runtime re-enqueues the reconcile with its exponential rate-limiter. The `pod_reconciler.go:338` host-schedulable apply targets a `corev1.Pod` under the sole `WarmPoolController` field manager rather than a CRD field owned by another field manager, so it passes an empty CRD kind and reaches loop exhaustion without emitting either signal. The counter is a package-level `*prometheus.CounterVec` registered against `ctrlmetrics.Registry`, so it appears on each controller's existing `/metrics` endpoint. The counter increment and the log emission share one call site, so a stuck episode always produces exactly one of each with matching identity.

At steady state (no ownership dispute) a controller resolves a 409 within a few attempts, so the loop returns success before exhaustion and neither the counter nor the log fires. The counter's value is therefore the count of stuck episodes per (`crd`, `controller`), and the alert fires only when a controller cannot converge on a CRD field it owns.

## 4. Proposed changes

### SPEC-1. Reconcile the §16.1 counter definition to §4.6.3 increment-after-five semantics

**Target:** `spec/16_observability.md` §16.1, the **CRD Ownership** counter row (`:271`).

**Rationale:** §16.1 currently says the counter "increments on every Server-Side Apply conflict observed," which contradicts §4.6.3's increment-after-five-consecutive-no-progress rule (`spec/04_system-components.md:607`). §16.1 is the counter's authoritative definition table, so it must state the adopted semantics. The label set (`crd`, `controller`) is already correct and stays. The row's "per-resource elevated counts trigger the alert" phrasing implies a per-resource counter label the counter does not carry, so it is replaced with a pointer to the `crd_ssa_conflict_stuck` log for per-resource attribution.

**Anchor:** Replace the entire parenthetical body of the `lenny_crd_ssa_conflict_total` row. Keep the leading `CRD SSA conflicts (` label and the `[Section 16.5]` and `[Section 4.6]` cross-reference links.

**Change (staged text).** Replace:

```
CRD SSA conflicts (`lenny_crd_ssa_conflict_total`, counter labeled by `crd`, `controller` — increments on every Server-Side Apply conflict observed by a Lenny controller when writing to a CRD field owned by another field manager; per-resource elevated counts trigger the `CRDSSAConflictStuck` warning alert ([Section 16.5](#165-alerting-rules-and-slos)); see [Section 4.6](04_system-components.md#46-pod-lifecycle-controllers) CRD field-ownership rules)
```

with:

```
CRD SSA conflicts (`lenny_crd_ssa_conflict_total`, counter labeled by `crd`, `controller` — increments once when a Lenny controller's Server-Side Apply hits five consecutive HTTP 409 conflicts on the same resource without progress (a stuck episode), co-emitted with the `crd_ssa_conflict_stuck` structured log event ([Section 4.6](04_system-components.md#46-pod-lifecycle-controllers), SSA conflict retry policy). The counter carries no per-resource label; per-resource attribution is on that log (labeled `controller`, `resource`, `name`). The `CRDSSAConflictStuck` warning alert ([Section 16.5](#165-alerting-rules-and-slos)) fires when the counter exceeds 10 in a 5-minute window for any (`crd`, `controller`) series; see [Section 4.6](04_system-components.md#46-pod-lifecycle-controllers) CRD field-ownership rules)
```

### SPEC-2. Amend §4.6.3 item 3 to state the continuation mechanism explicitly

**Target:** `spec/04_system-components.md` §4.6.3 item 3, "Bounded retry with backoff" (`:607`).

**Rationale:** §4.6.3 item 3 says the controller "continues with exponential backoff" after the fifth conflict without stating whether continuation is an in-process loop or a requeue. The adopted continuation model is a controller-runtime requeue, which the spec must state so an implementer does not read "continues" as an unbounded in-process loop that would hold a reconcile worker on one disputed resource. The item's alert sentence also repeats §16.5's "any single resource" wording, which the reconciled `crd`/`controller` label set does not support and which SPEC-3 removes from the alert row.

**Anchor:** Replace item 3 in full. Items 1, 2, and 4 and the paragraph introducing the list are unchanged.

**Change (staged text).** Replace:

```
3. **Bounded retry with backoff.** After re-reading and re-computing the patch, the controller retries the apply. If a second 409 occurs (concurrent apply from the other controller), the controller backs off with jitter (initial 100ms, max 2s) and re-reads again. After 5 consecutive 409s without progress, the controller emits a `crd_ssa_conflict_stuck` structured log event (labeled by `controller`, `resource`, `name`) and increments the `lenny_crd_ssa_conflict_total` counter, then continues with exponential backoff. A `CRDSSAConflictStuck` warning alert fires when this counter exceeds 10 in a 5-minute window on any single resource, indicating an abnormal ownership dispute that requires operator investigation.
```

with:

```
3. **Bounded retry with backoff, then requeue.** After re-reading and re-computing the patch, the controller retries the apply. If a second 409 occurs (concurrent apply from the other controller), the controller backs off with jitter (initial 100ms, max 2s) and re-reads again. After 5 consecutive 409s without progress, the controller emits a `crd_ssa_conflict_stuck` structured log event (labeled by `controller`, `resource`, `name`), increments the `lenny_crd_ssa_conflict_total` counter (labeled by `crd`, `controller`), and returns the conflict error so controller-runtime re-enqueues the reconcile through its exponential rate-limiter. The next reconcile re-enters the retry from a fresh read; this is the continuation with exponential backoff, and it holds no reconcile worker on a single disputed resource. A `CRDSSAConflictStuck` warning alert fires when this counter exceeds 10 in a 5-minute window for any (`crd`, `controller`) series, indicating an abnormal ownership dispute that requires operator investigation; the `crd_ssa_conflict_stuck` log identifies the specific resource.
```

**Preserved unchanged:** items 1 (always re-read), 2 (never force-conflicts), and 4 (post-crash list-and-sync), and the 100ms/2s jittered-backoff parameters and the `maxAttempts = 5` loop bound.

### SPEC-3. Amend the §16.5 CRDSSAConflictStuck alert description to drop the per-resource claim

**Target:** `spec/16_observability.md` §16.5, the `CRDSSAConflictStuck` alert row (`:535`).

**Rationale:** The alert description says the counter "for any single resource exceeds 10," but the counter has no per-instance label and §16.1.1 forbids adding one for `Sandbox`. The description must match the `crd`/`controller` label set and route operators to the log for per-resource identity.

**Change (staged text).** In the `CRDSSAConflictStuck` row's description cell, replace:

```
The SSA conflict counter (`lenny_crd_ssa_conflict_total`) for any single resource exceeds 10 in a 5-minute window. Indicates an abnormal ownership dispute between controllers on the same CRD field — requires operator investigation. See [Section 4.6](04_system-components.md#46-pod-lifecycle-controllers).
```

with:

```
The `lenny_crd_ssa_conflict_total` counter for any (`crd`, `controller`) series exceeds 10 in a 5-minute window (each increment is one five-consecutive-conflict stuck episode). Indicates an abnormal ownership dispute between controllers on the same CRD field that requires operator investigation. Consult the correlated `crd_ssa_conflict_stuck` structured log (labeled `controller`, `resource`, `name`) to identify the specific resource. See [Section 4.6](04_system-components.md#46-pod-lifecycle-controllers).
```

**Preserved unchanged:** the alert name, severity (Warning), the `> 10` threshold, and the 5-minute window.

### CODE-1. Consolidate retryOnConflictSSA into a shared ssaretry helper that registers the counter and emits the stuck log and counter at five consecutive 409s

**Target:** `pkg/controller/ssaretry/ssaretry.go` (new); the two package-local `retryOnConflictSSA` copies (`pkg/controller/sandbox/controller.go:42-76`, `pkg/controller/warmpool/controller.go:43-74`); and the nine call sites (`pkg/controller/sandbox/controller.go:660`, `:684`, `:828`; `pkg/controller/warmpool/controller.go:734`, `:832`; `pkg/controller/warmpool/gc.go:437`; `pkg/controller/warmpool/occupancy.go:234`; `pkg/controller/warmpool/pod_reconciler.go:338`, `:650`).

**Rationale:** Two byte-divergent copies of `retryOnConflictSSA` exist (the sandbox copy names an intermediate `sleep` variable; the warmpool copy inlines it) with nine call sites across the two packages. Wiring identical emission into both without a shared helper would duplicate the logic and let it drift. The counter is only a catalog metadata row (`pkg/observability/metrics/catalog.go:356`) with no backing collector, so it must be registered as a live `CounterVec`. The established placement for a §4.6 retry-policy counter is the retry concern's own package, next to its loop: the directly analogous §4.6.2 admission-denial counter `lenny_pool_scaling_admission_denied_total` is registered package-level in `pkg/controller/poolscaling/admission_retry.go:31-41` rather than in `controllermetrics`. `controllermetrics` is scoped by its package doc (`pkg/controller/controllermetrics/metrics.go:1-12`) to the §4.6.1 controller-runtime operability metrics plus a §10.3 cert gauge, so the SSA-conflict counter belongs in `ssaretry`, incremented directly at the emit site with no cross-package indirection. The current helper hides all resource identity behind the apply closure, so it must be given the conflict identity to emit the labels §4.6.3 and §16.1 require.

**Change (staged description).**

1. Create `pkg/controller/ssaretry/ssaretry.go` with a `ConflictID{Controller, CRD, Namespace, Name}` struct and `RetryOnConflictSSA(ctx context.Context, id ConflictID, apply func(attempt int) error) error`.
2. Register the counter package-level as a `*prometheus.CounterVec` named `lenny_crd_ssa_conflict_total` with labels `{crd, controller}` and a help string matching the reconciled §16.1 wording, registered via `ctrlmetrics.Registry.MustRegister`, mirroring `pkg/controller/poolscaling/admission_retry.go:31-41`.
3. Keep the existing loop body: `maxAttempts = 5`, `100ms` initial delay with a `2s` ceiling and jitter, `ctx.Done()` honoring, `apierrors.IsConflict` gating, and non-conflict early return.
4. On loop exhaustion (all five attempts were no-progress 409s), before `return lastErr`, emit the stuck signal only when the `ConflictID` carries a CRD kind (`id.CRD != ""`), which marks a CRD field-ownership dispute. When it does, call the structured logger obtained from `logf.FromContext(ctx)` at `Info` level with message `crd_ssa_conflict_stuck` and fields `controller=id.Controller`, `resource=id.CRD`, `name=id.Name` (with `namespace=id.Namespace` as context), and increment `WithLabelValues(id.CRD, id.Controller).Inc()`. When `id.CRD` is empty (the `pod_reconciler.go:338` host-schedulable Pod apply), skip both emissions because a Pod label conflict is not a CRD field owned by another field manager (`spec/16_observability.md:271`). Then return `lastErr` so controller-runtime requeues.
5. Delete both package-local `retryOnConflictSSA` copies and update all nine call sites to call `ssaretry.RetryOnConflictSSA`. Eight sites SSA-apply a Lenny CRD and pass a `ConflictID` carrying the owning CRD kind, `Controller` (the field manager), `Namespace`, and `Name` from the object in scope: `CRD: "Sandbox"` at `sandbox/controller.go:660`, `:684`, and `:828`, at `warmpool/gc.go:437`, at `warmpool/occupancy.go:234`, and at `warmpool/pod_reconciler.go:650`; `CRD: "SandboxWarmPool"` at `warmpool/controller.go:734`; and `CRD: "SandboxTemplate"` at `warmpool/controller.go:832`. The ninth site, `warmpool/pod_reconciler.go:338` (`reconcileHostSchedulable`), SSA-applies the `LabelHostSchedulable` label to a `corev1.Pod` (`TypeMeta` Kind `Pod`) under the sole `WarmPoolController` field manager; a Pod label is not a CRD field owned by another field manager, so this site passes an empty `CRD` and the helper retries with backoff without emitting the stuck signal (see step 4).
6. Run `gofumpt` and `goimports`.

The counter registration block reads:

```go
// crdSSAConflictTotal is the §16.1 lenny_crd_ssa_conflict_total counter
// the §16.5 CRDSSAConflictStuck alert evaluates against. Per §4.6.3 it
// increments once per five-consecutive-409 stuck episode, labeled by crd
// and controller. Per-resource identity is on the crd_ssa_conflict_stuck
// log, not on this counter (§16.1.1 forbids per-resource metric labels).
// Registration is package-level so the controller-runtime metrics
// registry exposes it on each controller's /metrics endpoint.
var crdSSAConflictTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_crd_ssa_conflict_total",
		Help: "SSA five-consecutive-409 stuck episodes on CRD fields, by crd and controller.",
	}, []string{"crd", "controller"})
	if err != nil {
		panic(fmt.Sprintf("ssaretry: build crd_ssa_conflict counter: %v", err))
	}
	ctrlmetrics.Registry.MustRegister(c)
	return c
}()
```

and the emit site at loop exhaustion reads:

```go
// All five attempts were no-progress 409s. A CRD field-ownership dispute
// emits the §4.6.3 stuck signal (one log + one counter increment,
// correlated by identity). The host-schedulable Pod apply carries no CRD
// kind and emits nothing, because a Pod label is not a CRD field owned by
// another field manager (§16.1). Either way return the conflict error so
// controller-runtime requeues with its exponential rate-limiter (the
// §4.6.3 "continues with exponential backoff").
if id.CRD != "" {
	logf.FromContext(ctx).Info("crd_ssa_conflict_stuck",
		"controller", id.Controller,
		"resource", id.CRD,
		"name", id.Name,
		"namespace", id.Namespace,
	)
	crdSSAConflictTotal.WithLabelValues(id.CRD, id.Controller).Inc()
}
return lastErr
```

### CODE-2. Update the CRDSSAConflictStuck alert description in the rule source and regenerate the committed copies

**Target:** `pkg/alerting/rules/rules.go:1463`, and the three generated copies `docs/alerting/rules.yaml:1084`, `charts/lenny/files/alerting-rules.yaml:1081`, and `pkg/embedded/manifests/manifests.yaml:2568`.

**Rationale:** The alert `Description` in the Go rule source mirrors §16.5's "any single resource" wording that SPEC-3 removes. The expression is unchanged; a bare per-series `increase` already fires per (`crd`, `controller`) series. `rules.go` is the generator source and the three committed YAML files are generated artifacts kept in sync by the tier-2 currency check, so they must be regenerated after the source edit.

**Change (staged description).** In `rules.go`, change the `CRDSSAConflictStuck` `Description` from:

```
The SSA conflict counter for any single resource exceeds 10 in a 5-minute window. Indicates an abnormal ownership dispute between controllers on the same CRD field.
```

to:

```
The lenny_crd_ssa_conflict_total counter for any (crd, controller) series exceeds 10 in a 5-minute window; each increment is one five-consecutive-409 stuck episode. Indicates an abnormal ownership dispute between controllers on the same CRD field. Consult the correlated crd_ssa_conflict_stuck structured log (labeled controller, resource, name) to identify the specific resource.
```

Leave `Name`, `Expr`, `Severity`, and `SpecRef` unchanged. Regenerate `docs/alerting/rules.yaml`, `charts/lenny/files/alerting-rules.yaml`, and `pkg/embedded/manifests/manifests.yaml` via the alerting-rules generator so the description strings match. The expression line stays `increase(lenny_crd_ssa_conflict_total[5m]) > 10`.

### DOC-1. Add the counter to the metrics reference

**Target:** `docs/reference/metrics.md`, the "Pool and warm pool metrics" table (`:129`).

**Rationale:** `docs/reference/metrics.md` has no row for `lenny_crd_ssa_conflict_total` (grep count 0), so the reconciliation must add the deployer-facing reference entry alongside the other controller and pool counters.

**Change (staged text).** Add a table row:

```
| `lenny_crd_ssa_conflict_total` | Counter | `crd`, `controller` | Increments once per five-consecutive-409 SSA stuck episode on a CRD field a controller does not own; per-resource identity is on the `crd_ssa_conflict_stuck` structured log. | `CRDSSAConflictStuck` alert. |
```

### DOC-2. Mark the three findings resolved on application

**Target:** `TEST-GAPS.md`, T-4.6.7 (`:843`), T-4.6.17 (`:898`), and T-16.1.10 (`:7112`).

**Rationale:** T-4.6.7 (SSA retry policy including the stuck counter untested), T-4.6.17 (`retryOnConflictSSA` lacks the stuck signal and gives up at five), and T-16.1.10 (§4.6/§16.1 increment-semantics contradiction) all record this defect and were blocked pending the proposal-pipeline decision this proposal makes.

**Change (staged description).** On application, after code and tests land, flip the T-4.6.7, T-4.6.17, and T-16.1.10 checkboxes to resolved, referencing this proposal, the adopted §4.6.3 increment-after-five semantics, the give-up-at-five-then-requeue continuation, and the completed tier-1 test. The skip line at `pkg/controller/warmpool/ssa_retry_internal_test.go:252` is removed by TEST-1.

## 5. Non-goals

- No change to the `PoolScalingController` admission-denial handling policy (`spec/04_system-components.md:630-635`), its `lenny_pool_scaling_admission_denied_total` counter, or the `PoolScalingAdmissionStuck` alert. That is a distinct 4xx-denial policy; §4.6 scopes the SSA-conflict policy to HTTP 409 only.
- No per-resource (`name`/instance) label added to `lenny_crd_ssa_conflict_total`. §16.1.1 forbids per-resource identifiers as metric labels; per-resource attribution stays on the `crd_ssa_conflict_stuck` log.
- No change to the alert threshold (`> 10`), the 5-minute window, or the bare-series expression. Only the alert description wording changes.
- No change to §4.6.3 items 1, 2, and 4 (always re-read, never force-conflicts, post-crash list-and-sync), the 100ms/2s jittered-backoff parameters, or the `maxAttempts = 5` loop bound.
- No in-process unbounded retry loop that would hold a reconcile worker on a single stuck resource. Continuation is via controller-runtime requeue.
- No change to the controller-runtime requeue rate-limiter configuration.
- No edit to the `pkg/observability/metrics/catalog.go:356` help string. That string ("Server-Side Apply conflicts on CRD fields") makes no per-conflict-versus-after-five timing claim, so it does not participate in the contradiction this proposal reconciles, and the catalog header states the §16.1 spec table (not the catalog help field) is the source of truth for semantics. The catalog is a validation enumeration consumed only within its own package and tests; it registers no collector, so the live metric's Prometheus HELP line comes from CODE-1's `CounterVec`, and no test asserts the catalog string's content. Editing it would be churn with no consumer.
- No separate `controllermetrics` registration and no exported cross-package `RecordCRDSSAConflictStuck` emit function. The counter is registered inside `ssaretry` and incremented at its single emit site, following the §4.6.2 `poolscaling/admission_retry.go` precedent rather than the general controller-runtime `controllermetrics` package, which is scoped to §4.6.1 operability metrics.

## 6. Testing

The change reaches tier 0 (static: build, vet, lint), tier 1 (unit), tier 2 (the alerting-rules currency crosscheck), and tier 11 (documentation and alert-rule consistency) per `.claude/rules/test-coverage.md`. `RetryOnConflictSSA` is a pure in-process helper with no kube-apiserver surface at the emit site, so no envtest reconciler tier is required for the helper itself. The spec edits (SPEC-1, SPEC-2, SPEC-3) carry no runtime behavior and are covered by the tier-0 static suite plus spec-map validation. CODE-2 regenerates the three committed alert-rule copies and DOC-1 adds a metrics-reference row, which `.claude/rules/test-coverage.md:42` ("Documentation, alert rules, or runbooks: tier 11.") maps to tier 11, while the bundled fragment is gated at tier 2; the concrete tier-2 and tier-11 checks those regenerated artifacts and the new metrics row must satisfy are listed below. Each unit test below covers a non-happy path and carries a `// spec:` tie. Tests move to the new `pkg/controller/ssaretry` package with the helper.

- **tier-1 helper, stuck signal at five (TEST-1, spec-named-failure):** In `pkg/controller/ssaretry/ssaretry_test.go`, un-skip and complete `TestRetryOnConflictSSAEmitsStuckSignalAfterFiveConflicts`. An apply that returns an SSA 409 on every attempt drives exactly five attempts (give-up-at-five), the helper returns the wrapped conflict `lastErr` rather than nil, `lenny_crd_ssa_conflict_total{crd, controller}` increments by exactly 1 (read via `testutil.ToFloat64` or a registry gather), and one `crd_ssa_conflict_stuck` log event carrying `controller`, `resource`, and `name` is emitted (assert via a captured `logr` sink). The non-happy path is the disputed apply that never converges. Reset or use a fresh registry per test so the increment count is deterministic. `// spec: §4.6.3 / §16.1 (stuck log + counter after 5 consecutive 409s)`.
- **tier-1 helper, four-then-succeed emits nothing (TEST-1, boundary):** A resource that returns a 409 four times then succeeds on the fifth attempt drives no stuck emission: the helper returns nil, the counter does not increment, and no `crd_ssa_conflict_stuck` log is emitted. The non-happy path is the near-boundary run that resolves before exhaustion and must stay quiet. `// spec: §4.6.3 (increment only after 5 consecutive no-progress 409s)`.
- **tier-1 helper, non-conflict early return (TEST-1, error):** Move `TestRetryOnConflictSSADoesNotRetryNonConflict` to the `ConflictID` signature. A non-409 error is returned immediately with a single apply invocation and no counter increment or stuck log, because only a 409 indicates a stale cached `resourceVersion` worth re-reading. `// spec: §4.6.3 item 1 (re-read is keyed on 409 conflicts)`.
- **tier-1 helper, context cancellation (TEST-1, concurrent):** Move `TestRetryOnConflictSSAHonorsContextCancellation` to the `ConflictID` signature. A context cancelled during backoff aborts the loop with the context error, emits no stuck signal, and does not hang. The non-happy path is the shutting-down controller cancelling mid-dispute. `// spec: §4.6.3 (bounded retry with backoff); code-best-practices.md (honor context cancellation)`.
- **tier-1 helper, base case and backoff (TEST-1):** Move `TestRetryOnConflictSSAReturnsOnFirstSuccess` and `TestRetryOnConflictSSARetriesAndBacksOffOnConflict` to the `ConflictID` signature, asserting the first-success path invokes the apply once and the three-attempt path advances the re-read counter and actually sleeps at least 100ms between attempts. `// spec: §4.6.3 item 3 (jittered backoff, initial 100ms)`.
- **tier-1 helper, empty CRD kind is silent (TEST-1, boundary):** An apply that returns an SSA 409 on every attempt under a `ConflictID` with an empty `CRD` (the `pod_reconciler.go:338` host-schedulable Pod path) drives five attempts, returns the wrapped conflict `lastErr`, and emits no `crd_ssa_conflict_stuck` log and no counter increment. The non-happy path is the non-CRD Pod apply that exhausts the loop and must stay silent. `// spec: §16.1 (counter fires only when writing to a CRD field owned by another field manager)`.
- **tier-0 spec-map repoint (TEST-1):** The §4.6.3 section of the maintained test registry names the five moved functions at their old warmpool path (`tests/spec-map.json:367`, `:368`, `:369`, `:370`, `:372`). Because TEST-1 moves those functions into `pkg/controller/ssaretry/ssaretry_test.go` while `pkg/controller/warmpool/ssa_retry_internal_test.go` continues to exist (it retains `TestUpdateStatusReReadsAndNeverForcesOnSSAConflict` at `:371`), the tier-0 file-existence gate `validateSpecMapTestFiles` (`cmd/lenny-test/cmd_validate.go:492`) does not hard-fail: it strips the `::TestName` selector (`cmd/lenny-test/cmd_validate.go:510-512`) and only stats the still-present `.go` file (`:513`), so the five entries silently reference functions that the file no longer defines and `lenny-test --spec 4.6.3` would run `go test -run` against a package where they are gone. TEST-1 repoints the five entries (`spec-map.json:367`, `:368`, `:369`, `:370`, `:372`) from `pkg/controller/warmpool/ssa_retry_internal_test.go::...` to `pkg/controller/ssaretry/ssaretry_test.go::...`, leaves the sixth entry (`:371`, `TestUpdateStatusReReadsAndNeverForcesOnSSAConflict`) at the warmpool path where that test stays, and adds `pkg/controller/ssaretry` to the section's `packages` array (`spec-map.json:374-377`). `// spec: §4.6.3 (spec-map references resolve to extant functions)`.
- **tier-2 alerting-rules currency (CODE-2):** After editing the `CRDSSAConflictStuck` `Description` in `pkg/alerting/rules/rules.go` and running `make generate`, `tests/tier2_component/observability/catalog_crosscheck_test.go`'s `TestBundledAlertingRulesFragmentIsCurrent` re-runs `gen-alerting-rules -check` and fails if the committed fragment is stale, so the three regenerated copies must match the source. `// spec: §16.5 (bundled alerting-rules fragment is current)`.
- **tier-11 embedded-manifest sync (CODE-2):** `tests/tier11_docs/embedded_manifests_sync_test.go`'s `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4` byte-compares `pkg/embedded/manifests/manifests.yaml` against a fresh dev-profile chart render, so the regenerated alert description at `manifests.yaml:2568` must match the chart copy. `// spec: §16.5 / §17.4 (embedded manifests match the rendered chart)`.
- **tier-11 alerting-rules reference (CODE-2):** `tests/tier11_docs/alerting_docs_reference_test.go`'s `TestDocsAlertingRulesYAMLIsLoadablePrometheusRuleFile` loads and validates `docs/alerting/rules.yaml`, so the regenerated description there must parse as a well-formed PrometheusRule file. `// spec: §16.5 (docs alerting-rules reference is loadable)`.
- **tier-11 metrics reference (DOC-1):** The new `lenny_crd_ssa_conflict_total` row in `docs/reference/metrics.md` is a tier-11 documentation surface covered by the tier-11 documentation suite; the added row must state the reconciled `crd`/`controller` labels and the stuck-episode semantics. `// spec: §16.1 (metrics reference mirrors the §16.1 catalog row)`.

## 7. Findings closed on application

This proposal closes T-4.6.7 (SSA conflict retry policy including the stuck counter untested, Medium), T-4.6.17 (`retryOnConflictSSA` lacks the stuck signal and gives up at five, Medium), and T-16.1.10 (§4.6/§16.1 increment-semantics contradiction, Low). All three record this defect and defer it to the proposal pipeline; this proposal resolves the increment semantics in favor of §4.6.3's stuck-episode reading, adds the continue-via-requeue continuation, registers the counter, and wires the emit site. The findings are flipped to resolved at implementation time, after the code and the un-skipped tier-1 test land.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revision carried in the draft folded the standalone counter-registration change into CODE-1. The earlier draft placed the counter in `pkg/controller/controllermetrics/metrics.go` and added an exported `RecordCRDSSAConflictStuck` emit function. Review found that the established placement for a §4.6 retry-policy counter is the retry concern's own package, following the §4.6.2 precedent in `pkg/controller/poolscaling/admission_retry.go:31-41`, and that `controllermetrics` is scoped by its package doc (`pkg/controller/controllermetrics/metrics.go:1-12`) to the §4.6.1 controller-runtime operability metrics. Because CODE-1 already creates `pkg/controller/ssaretry` as the single shared surface both controllers call, the counter belongs inside that package, incremented directly at the emit site, which removes the second touched package and the cross-package emit indirection. Review also dropped a proposed edit to the `pkg/observability/metrics/catalog.go:356` help string: that string makes no timing claim and does not participate in the contradiction, and the catalog header names the §16.1 spec table as the semantics source of truth, so the edit would be churn with no consumer. Both dropped alternatives are recorded in Non-goals with their reasons.

### Pass 1 (2026-07-19, automated)

- Corrected the CODE-1 emit path so it does not false-fire the counter on the `pkg/controller/warmpool/pod_reconciler.go:338` (`reconcileHostSchedulable`) call site. That site SSA-applies the `LabelHostSchedulable` label to a `corev1.Pod` under the sole `WarmPoolController` field manager (`pkg/controller/warmpool/pod_reconciler.go:330-340`), which is not a CRD field owned by another field manager and is outside §16.1's counter scope (`spec/16_observability.md:271`). The helper now gates the `crd_ssa_conflict_stuck` log and the `lenny_crd_ssa_conflict_total` increment on a non-empty CRD kind (`id.CRD != ""`); the Pod site passes an empty CRD and reaches loop exhaustion silently. The gate is propagated to Decision 5, section 3, CODE-1 steps 4 and 5, the emit-site code block, the files-touched list, and a new tier-1 "empty CRD kind is silent" test. The CODE-1 step-5 illustrative list was rewritten to enumerate each site's actual CRD kind, correcting the prior grouping that mislabeled the Pod site as a Sandbox and omitted `warmpool/controller.go:832`, which targets a `SandboxTemplate` (`pkg/controller/warmpool/controller.go:833`).
- Corrected the Testing section, which claimed the change reaches only tier 0 and tier 1. CODE-2 regenerates the three committed alert-rule copies and DOC-1 adds a `docs/reference/metrics.md` row, which `.claude/rules/test-coverage.md:42` maps to tier 11, and the bundled fragment is gated at tier 2. The section now states the change reaches tiers 0, 1, 2, and 11 and lists the concrete gating checks: `tests/tier2_component/observability/catalog_crosscheck_test.go` `TestBundledAlertingRulesFragmentIsCurrent`, `tests/tier11_docs/embedded_manifests_sync_test.go` `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4`, and `tests/tier11_docs/alerting_docs_reference_test.go` `TestDocsAlertingRulesYAMLIsLoadablePrometheusRuleFile`, plus the tier-11 metrics-reference surface for DOC-1.

### Pass 2 (2026-07-19, automated)

- Added the `tests/spec-map.json` update that TEST-1 omitted. The §4.6.3 section of the maintained test registry names the five moved helper functions at `pkg/controller/warmpool/ssa_retry_internal_test.go` (`tests/spec-map.json:367`, `:368`, `:369`, `:370`, `:372`). TEST-1 moves those functions into `pkg/controller/ssaretry/ssaretry_test.go` but the source warmpool file continues to exist because it retains `TestUpdateStatusReReadsAndNeverForcesOnSSAConflict` (`spec-map.json:371`; `pkg/controller/warmpool/ssa_retry_internal_test.go:141`), so the tier-0 gate `validateSpecMapTestFiles` does not hard-fail: it strips the `::TestName` selector and only stats the still-present `.go` file (`cmd/lenny-test/cmd_validate.go:510-513`), leaving five entries that reference functions the file no longer defines and dropping `lenny-test --spec 4.6.3` coverage for the exact section this proposal changes. TEST-1 now repoints those five entries to `pkg/controller/ssaretry/ssaretry_test.go::...`, leaves the sixth entry at the warmpool path where its test stays, and adds `pkg/controller/ssaretry` to the section `packages` array. The change is recorded in a new tier-0 spec-map-repoint testing bullet and in the Section 10 files-touched list.

## 9. Open decisions for review

- **Ratify the authoritative increment semantics.** This proposal adopts §4.6.3's increment-once-per-five-consecutive-no-progress-409 (stuck-episode) reading over §16.1's every-conflict reading, on the grounds that the stuck-episode reading keeps the `CRDSSAConflictStuck` alert quiet during benign crash-recovery 409s. The alternative (every-conflict) would make the counter a raw 409 rate and require re-tuning the `> 10`-in-5m threshold.
- **Ratify the continuation model.** This proposal keeps the give-up-at-five loop and treats §4.6.3's "continues with exponential backoff" as continuation via controller-runtime requeue (return the conflict error). The alternative is an in-process loop with a larger or unbounded bound honoring the context deadline, which continues faster but holds a reconcile worker on one resource.
- **Ratify dropping the alert's "any single resource" granularity.** This proposal keeps the counter at `crd`/`controller` labels (cardinality-safe) and moves per-resource attribution to the `crd_ssa_conflict_stuck` log. If per-resource alert granularity is required, the alternative is to restrict the counter to bounded CRDs only (for example `SandboxWarmPool` and `SandboxTemplate`, excluding per-pod `Sandbox`) and add a `name` label there, which complicates the emit path and leaves `Sandbox`-field disputes unalerted.

## 10. Files touched on application

- `spec/16_observability.md`: SPEC-1 (§16.1 `lenny_crd_ssa_conflict_total` row, every-conflict to five-consecutive-409 stuck-episode semantics, `:271`) and SPEC-3 (§16.5 `CRDSSAConflictStuck` description, drop "any single resource", `:535`).
- `spec/04_system-components.md`: SPEC-2 (§4.6.3 item 3, state continuation via controller-runtime requeue and the (`crd`, `controller`) alert series, `:607`).
- `pkg/controller/ssaretry/ssaretry.go` (new): CODE-1 (`ConflictID`, `RetryOnConflictSSA`, the registered `lenny_crd_ssa_conflict_total` `CounterVec`, and the stuck log plus counter emit at loop exhaustion).
- `pkg/controller/sandbox/controller.go`: CODE-1 (delete the local `retryOnConflictSSA` copy, `:42-76`; update call sites `:660`, `:684`, `:828`, each `CRD: "Sandbox"`).
- `pkg/controller/warmpool/controller.go`: CODE-1 (delete the local `retryOnConflictSSA` copy, `:43-74`; update call sites `:734` (`CRD: "SandboxWarmPool"`) and `:832` (`CRD: "SandboxTemplate"`)).
- `pkg/controller/warmpool/gc.go`, `pkg/controller/warmpool/occupancy.go`, `pkg/controller/warmpool/pod_reconciler.go`: CODE-1 (update call sites `gc.go:437` (`CRD: "Sandbox"`), `occupancy.go:234` (`CRD: "Sandbox"`), `pod_reconciler.go:650` (`CRD: "Sandbox"`), and `pod_reconciler.go:338` (the host-schedulable Pod apply, empty CRD kind, non-emitting)).
- `pkg/alerting/rules/rules.go`: CODE-2 (`CRDSSAConflictStuck` description, `:1463`).
- `docs/alerting/rules.yaml`, `charts/lenny/files/alerting-rules.yaml`, `pkg/embedded/manifests/manifests.yaml`: CODE-2 (regenerated description copies, `:1084`, `:1081`, `:2568`).
- `docs/reference/metrics.md`: DOC-1 (add the `lenny_crd_ssa_conflict_total` row, `:129`).
- `pkg/controller/warmpool/ssa_retry_internal_test.go`: TEST-1 (move the five helper tests to `pkg/controller/ssaretry/ssaretry_test.go` on the `ConflictID` signature; the skip at `:252` is removed; `TestUpdateStatusReReadsAndNeverForcesOnSSAConflict` stays in this file).
- `pkg/controller/ssaretry/ssaretry_test.go` (new): TEST-1 (the completed stuck-signal test plus the four moved helper tests).
- `tests/spec-map.json`: TEST-1 (repoint the five §4.6.3 entries for the moved functions from `pkg/controller/warmpool/ssa_retry_internal_test.go::...` to `pkg/controller/ssaretry/ssaretry_test.go::...`, `:367`, `:368`, `:369`, `:370`, `:372`; leave the sixth entry at `:371` unchanged; add `pkg/controller/ssaretry` to the section `packages` array, `:374-377`).
- `TEST-GAPS.md`: DOC-2 (mark T-4.6.7, T-4.6.17, T-16.1.10 resolved, `:843`, `:898`, `:7112`).
</content>
</invoke>
