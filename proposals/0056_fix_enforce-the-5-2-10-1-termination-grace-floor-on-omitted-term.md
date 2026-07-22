# Proposal: Enforce the §5.2/§10.1 termination-grace floor on omitted terminationGracePeriodSeconds and reconcile the agent-pod floor with the gateway-pod floor

- **Status:** Implemented (2026-07-22). Spec applied and code landed on `proposal-b/pool-grace-floor-nil-default`; tiers 0/1/2 green (independently verified, validator+webhook under -race, tier-2 admission envtest incl. the two new omitted-field cases), 100% changed-line coverage, review clean, CRD codegen overlay intact. Ready for `--no-ff` integration. Verified (2026-07-22) after 4 adversarial review rounds (7 findings fixed). Renumbered 0054→0056 on integration (another runner landed 0054/0055). Both open decisions ratified on sign-off: (1) the `checkpointBarrierAckTimeoutSeconds` term is dropped from the agent-pod floor (agent floor = `maxConcurrentSessions × max_tiered_checkpoint_cap + 30`, keeping the `+30` stream-drain term); (2) an omitted `terminationGracePeriodSeconds` whose effective 120s default is below the reconciled floor is rejected fail-closed (the deployer must set the field; the podspec does not auto-provision).
- **Date:** 2026-07-22.
- **Scope:** An admission-validation fix in `pkg/admission/pool_config_validator` with coupled spec amendments (§5.2, §10.1, §16.1, and §17.2), a reconciled `SandboxTemplate` CRD field doc comment, and regenerated CRD manifests, all of which reconcile the agent-pod `terminationGracePeriodSeconds` floor. The `SandboxWarmPool`/`SandboxTemplate` pool-config webhook fails to enforce the floor when `terminationGracePeriodSeconds` is omitted (a nil-bypass), and it computes that floor with `checkpointBarrierAckTimeoutSeconds`, a term that belongs to the gateway pod rather than to the agent pod the CRD field renders onto. This proposal closes the nil-bypass by evaluating the floor against the effective grace period (the declared value, else the §4.6.1 120s agent default), drops the gateway-side `checkpointBarrierAckTimeoutSeconds` term from the agent floor so the §4.6.1 120s default is valid for the default single-slot pool, and stages the reconciliation in the spec (§5.2, §10.1, §16.1, and §17.2), in the CRD field doc comment, and in the regenerated CRD manifests rather than dropping it silently in code. The fix is admission-only. It does not build or touch the C-22 eviction trigger (T-4.4.14) or C-22 prereq 4, and it does not change the gateway-pod grace period (§17.8).

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

The `SandboxWarmPool` pool-config admission webhook computes a per-pod `terminationGracePeriodSeconds` floor but fails to enforce it when the field is omitted, and it computes that floor with a term that belongs to a different pod. Two coupled defects follow.

**1. Nil-bypass.** `decideTerminationBudget` computes a floor (`pkg/admission/pool_config_validator/validator.go:635`) but guards the floor-versus-grace rejection behind `spec.TerminationGracePeriodSeconds != nil` (`validator.go:644`). The CRD field carries no `+kubebuilder:default` (`pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:177-188`) and no defaulting webhook sets it (there is no `MutatingWebhookConfiguration` for this field in the chart or the embedded manifests), so an omitted field is nil at admission and the invariant is never evaluated. The controller then renders the §4.6.1 120s default onto the agent pod (`pkg/controller/sandbox/podspec/podspec.go:75`), which bounds the agent preStop drain (`podspec.go:82` `preStopDrainMarginSeconds`). §5.2 (the "Checkpoint granularity" paragraph, `spec/05_runtime-registry-and-pool-model.md:542`) states that the webhook enforces the inequality `… ≤ terminationGracePeriodSeconds`; the omitted-field path leaves the inequality unenforced. The existing unit case at `validator_test.go:432` pins the omitted case to admit ("session-mode pool with no grace period set → admitted (no comparison)"), which locks in the gap.

**2. Gateway-versus-agent conflation.** The `SandboxTemplate` `terminationGracePeriodSeconds` field configures the agent pod (`podspec.go` renders it into the agent pod spec, per §4.6.1), rather than the gateway pod. The gateway pod's grace period is a separate Helm value (`spec/17_deployment-topology.md:971`, `terminationGracePeriodSeconds (gateway pod)` 240/240/300). §10.1 frames its inequality `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds` explicitly "on gateway pods" (`spec/10_gateway-internals.md:116-122`), where `checkpointBarrierAckTimeoutSeconds` is the gateway's wall-clock wait for `CheckpointBarrierAck` from the pods it coordinates (`spec/10_gateway-internals.md:167`). §5.2 ports that same BarrierAck-inclusive formula to the agent pod's serialized per-slot preStop budget (`spec/05_runtime-registry-and-pool-model.md:542`), and the validator applies `maxConcurrent × tierCap + barrierAck + drain` (`validator.go:635`) to the agent-pod CRD field. The agent pod's own preStop drain performs no BarrierAck wait (`cmd/lenny-adapter/prestop.go:68` `runPreStop` SIGTERMs the local adapter and polls for its exit; it opens no gateway `Checkpoint` stream and waits for no ack), so the BarrierAck term over-provisions the agent floor. Dropping it yields `maxConcurrentSessions × tierCap + 30`, which for a single-slot default-tier pool is `90 + 30 = 120s`, exactly the §4.6.1 agent default. Keeping BarrierAck rejects even a trivially-configured session pool (floor 210s against the 120s default), which is not the intent, and contradicts the §4.6.1 default the podspec renders.

**Scope of the consequence.** The consequence is an admission-validation correctness defect rather than live data loss today. §5.2 states that service mode has no checkpoint support (`spec/05_runtime-registry-and-pool-model.md:530`), so a service-mode drain truncates no workspace state. The agent-preStop-to-coordinating-gateway eviction-checkpoint routing that §4.6.1 describes (`spec/04_system-components.md:489`, the preStop hook signalling the coordinating replica which drives the `Checkpoint` RPC with the `TriggerEviction` trigger) is unbuilt in code: `runPreStop` only SIGTERMs the local adapter, and there is no `TriggerEviction` or `CheckpointWithTrigger` call in `pkg/adapter` or `cmd/lenny-adapter`. That routing is the C-22 eviction trigger (finding T-4.4.14), still unbuilt. The workspace-loss path the floor protects therefore becomes load-bearing only once the trigger lands. This proposal hardens admission now so the invariant holds when the trigger is built, without building or touching the trigger.

## 2. Decisions

- **The CRD `terminationGracePeriodSeconds` field is the agent pod's grace period, and the floor the pool-config validator vets against it is the agent-pod floor.** The field renders onto the agent pod through the podspec (§4.6.1). The gateway pod's grace period is a separate Helm value (`spec/17_deployment-topology.md:971`, 240/240/300) and is out of scope.
- **The agent-pod floor is `maxConcurrentSessions × max_tiered_checkpoint_cap + minStreamDrainSeconds`; the `checkpointBarrierAckTimeoutSeconds` term is dropped.** BarrierAck is the gateway's wait for `CheckpointBarrierAck` (§10.1), a gateway-pod-side budget the agent pod's own preStop drain does not incur. The reconciled agent floor makes the §4.6.1 120s default (tier cap 90 + drain 30) valid for the default single-slot pool, resolving the §4.6.1-versus-floor contradiction, and it still catches genuine under-provisioning for multi-slot or large-workspace pools.
- **The webhook evaluates the agent floor against the effective grace period.** The effective grace period is the declared field value when set, otherwise the §4.6.1 120s agent default. This closes the nil-bypass without re-opening the 120s default value itself.
- **An omitted field that under-provisions is rejected fail-closed** with `422 INVALID_POOL_CONFIGURATION`, consistent with the §10.1 reject-on-guaranteed-SIGKILL posture and the §5.2 requirement that deployers set `terminationGracePeriodSeconds` accordingly. The flat 120s podspec default stays; the podspec does not auto-provision the computed floor. No default is left unvetted, because the 120s effective grace period is itself checked against the floor.
- **The `checkpointBarrierAckTimeoutSeconds ≥ max_tiered_checkpoint_cap` BarrierAck-floor rule (§10.1, `validator.go:628`) is a separate, correct rule that does not involve the grace field and is unchanged.** `checkpointBarrierAckTimeoutSeconds` is still parsed and still vetted against the tier cap; it is removed only from the grace floor.
- **This is a standalone admission-validation fix.** It does not build or touch the C-22 eviction trigger (T-4.4.14) or C-22 prereq 4, and it does not change the gateway-pod grace period (§17.8). The BarrierAck reconciliation is staged in the spec (§5.2, §10.1, §16.1, and §17.2), in the `SandboxTemplate` CRD field doc comment, and in the regenerated CRD manifests rather than dropped silently in code, so the source of truth, the operator-facing CRD schema, and the validator agree.

## 3. The reconciled agent-pod floor

There are two distinct grace-period floors, and the defect is that the validator applied the gateway floor's formula to the agent-pod field.

- **Gateway-pod floor (§10.1, unchanged).** `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds`. The gateway coordinates the pool and waits for `CheckpointBarrierAck` under a single wall-clock deadline, so BarrierAck is part of its drain budget. The gateway pod's grace period is the Helm value in §17.8 (240/240/300). No per-pool CRD field carries the gateway grace period, so nothing in the validator vets this floor.
- **Agent-pod floor (§5.2, reconciled).** `maxConcurrentSessions × max_tiered_checkpoint_cap + 30 ≤ terminationGracePeriodSeconds`. The agent pod's preStop drain uploads its own per-slot checkpoints serialized in slot-ID order and performs no BarrierAck wait, so the floor omits `checkpointBarrierAckTimeoutSeconds`. `terminationGracePeriodSeconds` here is the agent pod's grace period on the `SandboxWarmPool`/`SandboxTemplate` CRD, and the validator vets it. When the field is omitted, the effective grace period is the §4.6.1 120s agent default.

For the default single-slot default-tier pool the agent floor is `1 × 90 + 30 = 120s`, which equals the §4.6.1 agent default, so the default pool admits. A pool that raises `maxConcurrentSessions` or `workspaceSizeLimitBytes` above the default pushes the floor above 120s and must declare a matching `terminationGracePeriodSeconds`, or it is rejected.

## 4. Proposed changes

### SPEC-1. Reconcile the §5.2 agent-pod webhook inequality

**Target:** `spec/05_runtime-registry-and-pool-model.md`, the "Checkpoint granularity" bullet in §5.2 (the `SandboxWarmPool` CRD validation enforcement clause and the node-drain worked example, currently at `:542`).

**Rationale:** §5.2 states that the webhook enforces `maxConcurrentSessions × max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds` on the agent pod's serialized per-slot preStop budget, but `checkpointBarrierAckTimeoutSeconds` is a gateway-side wait (§10.1) the agent pod's own drain does not incur, and the clause does not state how an omitted field is vetted. The reconciled agent floor `maxConcurrentSessions × max_tiered_checkpoint_cap + 30` equals the §4.6.1 120s default for a single-slot default-tier pool, aligning the two sections.

**Change (staged spec text).** In the "Checkpoint granularity" bullet, replace the enforcement sentence:

```markdown
The total preStop budget for a pod with `maxConcurrentSessions > 1` is the **sum** of per-slot caps across all active slots; the `SandboxWarmPool` CRD validation webhook enforces that `maxConcurrentSessions × max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds`. Deployers must set `terminationGracePeriodSeconds` accordingly when `maxConcurrentSessions > 1` — the Helm chart provides a helper formula in `values.yaml` comments.
```

with:

```markdown
The total preStop budget for a pod with `maxConcurrentSessions > 1` is the **sum** of per-slot caps across all active slots; the `SandboxWarmPool` CRD validation webhook enforces that `maxConcurrentSessions × max_tiered_checkpoint_cap + 30 ≤ terminationGracePeriodSeconds`, where `terminationGracePeriodSeconds` is the **agent pod's** grace period ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). This floor omits `checkpointBarrierAckTimeoutSeconds`: that term is the gateway's wall-clock wait for `CheckpointBarrierAck` from the pods it coordinates and belongs to the gateway pod's grace period ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)), which the agent pod's own preStop drain does not incur. The webhook evaluates the floor against the pool's **effective** `terminationGracePeriodSeconds`: the declared value when the field is set, otherwise the [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) agent default of 120s. A pool whose effective grace period is below this floor is rejected with `422 INVALID_POOL_CONFIGURATION`. For a single-slot default-tier pool the floor is `1 × 90 + 30 = 120s`, which equals the agent default, so the default pool admits without any declared value. Deployers must set `terminationGracePeriodSeconds` accordingly when the floor exceeds the agent default — the Helm chart provides a helper formula in `values.yaml` comments.
```

In the same bullet, replace the node-drain worked example:

```markdown
(e.g., `maxConcurrentSessions: 8` with 512 MB workspaces yields 8 × 90 + 90 + 30 = 840s, or 14 minutes)
```

with:

```markdown
(e.g., `maxConcurrentSessions: 8` with 512 MB workspaces yields 8 × 90 + 30 = 750s, or 12.5 minutes; the BarrierAck-inclusive figure is the gateway-pod budget in [Section 10.1](10_gateway-internals.md#101-horizontal-scaling))
```

Keep the serialized-per-slot and sum-of-per-slot-caps language, the `maxTerminationGracePeriodSeconds` hard-ceiling sentence, and the >600s node-drain warning unchanged.

### SPEC-2. Reframe §10.1's gateway-pod budget and name the agent-pod floor the validator enforces

**Target:** `spec/10_gateway-internals.md`, the "CRD validation rule — tiered cap + BarrierAck budget" paragraph, its inequality, and its rejection message in §10.1 (currently at `:116-122`), the CheckpointBarrier "why the formula adds BarrierAck once" sentence (currently at `:167`), and the BarrierAck-timeout partial-capture paragraph's "CRD-validated … budget" reference (currently at `:177`).

**Rationale:** §10.1 attributes the BarrierAck-inclusive inequality `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds` to `lenny-pool-config-validator`, which vets the `SandboxTemplate`/`SandboxWarmPool` CRD `terminationGracePeriodSeconds` field. That field renders onto the agent pod (§4.6.1). After SPEC-1 and CODE-1 the validator enforces the agent floor `maxConcurrentSessions × max_tiered_checkpoint_cap + 30` with the BarrierAck term dropped. Leaving §10.1's rule unchanged would make the section attribute the BarrierAck-inclusive rejection to the validator while SPEC-1 and CODE-1 give the validator the BarrierAck-free agent floor, an internal contradiction that also re-rejects every default single-slot 120s pool (90 + 90 + 30 = 210s against the 120s default). The gateway-pod budget `cap + barrierAck + 30` is a real requirement, but it is sized by the §17.8 Helm gateway-pod value and no CRD field carries it, so no admission webhook enforces it. This section reframes the paragraph so the webhook enforces the agent floor and the gateway budget is named as the separate Helm concern.

**Change (staged spec text).** Rename the rule and its opening sentence. Replace:

```markdown
**CRD validation rule — tiered cap + BarrierAck budget:** The `SandboxWarmPool` CRD admission webhook (`lenny-pool-config-validator` — see [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)) enforces that the worst-case preStop budget does not exceed `terminationGracePeriodSeconds`.
```

with:

```markdown
**CRD validation rule — agent-pod grace floor:** The `SandboxWarmPool` CRD admission webhook (`lenny-pool-config-validator` — see [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)) enforces that the worst-case **agent-pod** preStop budget does not exceed the pool's `terminationGracePeriodSeconds`, the field that renders onto the agent pod ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).
```

In the same paragraph's "Scope of enforcement" sentence, replace the phrase `both the tiered-cap + BarrierAck budget rule below and the BarrierAck floor rule that follows` with `both the agent-pod grace floor rule below and the BarrierAck floor rule that follows`.

Replace the inequality block:

```markdown
max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds
```

with:

```markdown
maxConcurrentSessions × max_tiered_checkpoint_cap + 30 > terminationGracePeriodSeconds
```

Replace the "where …" explanation, the defaults sentence, and the rejection message:

```markdown
where `max_tiered_checkpoint_cap` is the largest cap tier applicable to the pool's `workspaceSizeLimitBytes` (e.g., 90s for pools allowing up to 512 MB workspaces), `checkpointBarrierAckTimeoutSeconds` is the configured BarrierAck wait (default: 90s), and 30s is the minimum stream-drain budget (stage 3). With defaults (90s + 90s + 30s = 210s), `terminationGracePeriodSeconds` must be set to at least 210s on gateway pods coordinating pools that allow large workspaces. The Helm chart sets `terminationGracePeriodSeconds: 240` by default to provide a 30s safety margin. Rejection error: `422 INVALID_POOL_CONFIGURATION` with message `"tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 exceeds terminationGracePeriodSeconds; increase terminationGracePeriodSeconds or reduce checkpointBarrierAckTimeoutSeconds / workspaceSizeLimitBytes"`.  Metric: `lenny_pool_termination_budget_exceeded_total` (counter, labeled by `pool`) incremented when the webhook rejects a configuration.
```

with:

```markdown
where `max_tiered_checkpoint_cap` is the largest cap tier applicable to the pool's `workspaceSizeLimitBytes` (e.g., 90s for pools allowing up to 512 MB workspaces), `maxConcurrentSessions` is the per-pod slot count (1 for a session-mode pool), and 30s is the minimum stream-drain budget (stage 3). This floor omits `checkpointBarrierAckTimeoutSeconds`: that term is the gateway's wall-clock wait for `CheckpointBarrierAck` from the pods it coordinates and belongs to the gateway pod's grace period (see the following paragraph), which the agent pod's own preStop drain does not incur. The webhook evaluates the floor against the pool's **effective** `terminationGracePeriodSeconds`: the declared value when the field is set, otherwise the [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) agent default of 120s. For a single-slot default-tier pool the floor is `1 × 90 + 30 = 120s`, which equals the agent default, so the default pool admits without any declared value. Rejection error: `422 INVALID_POOL_CONFIGURATION` with message `"the pool's effective terminationGracePeriodSeconds is below the Section 5.2 agent-pod floor (maxConcurrentSessions × max_tiered_checkpoint_cap + 30); increase terminationGracePeriodSeconds or reduce maxConcurrentSessions / workspaceSizeLimitBytes"`.  Metric: `lenny_pool_termination_budget_exceeded_total` (counter, labeled by `pool`) incremented when the webhook rejects a configuration.

The **gateway pod's** grace period is a separate budget. The gateway coordinates the pool and waits for `CheckpointBarrierAck` under a single wall-clock deadline, so its grace period must satisfy `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds` (with defaults, 90s + 90s + 30s = 210s). This budget is sized by the Helm gateway-pod value ([Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)); the Helm chart sets the gateway pod's `terminationGracePeriodSeconds` to 240 by default to provide a 30s safety margin. No per-pool CRD field carries the gateway grace period, so no admission webhook vets this budget.
```

Then reframe the CheckpointBarrier "why the formula adds BarrierAck once" sentence (`:167`). Replace:

```markdown
This is why the CRD validation formula (below) adds `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` rather than multiplying by session count.
```

with:

```markdown
This is why the **gateway pod's** grace-period budget (see the CRD validation subsection above) adds `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` once rather than multiplying by session count. The agent-pod grace floor the `lenny-pool-config-validator` enforces against the CRD `terminationGracePeriodSeconds` field instead multiplies the per-slot cap by `maxConcurrentSessions` and omits the BarrierAck term.
```

Then reword the BarrierAck-timeout partial-capture paragraph's reference to the same formula (`:177`), which currently labels it "CRD-validated." After this change the CRD field carries the agent-pod floor and no admission webhook vets the `cap + barrierAck + 30` figure, so calling it "CRD-validated" contradicts the reframed rule. Replace:

```markdown
It also does not extend the drain budget: rules 1–5 operate on Postgres state the gateway has already committed to, so the BarrierAck-timeout handler completes within milliseconds and does not perturb the CRD-validated `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` budget.
```

with:

```markdown
It also does not extend the drain budget: rules 1–5 operate on Postgres state the gateway has already committed to, so the BarrierAck-timeout handler completes within milliseconds and does not perturb the gateway pod's `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` drain budget, which is sized by the Helm gateway-pod value ([Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)) rather than vetted by an admission webhook.
```

Leave the separate "BarrierAck floor" rule (`checkpointBarrierAckTimeoutSeconds ≥ max_tiered_checkpoint_cap`, `:124`) and its rejection message unchanged.

### SPEC-3. Reconcile the §16 metric reject condition and validator-unavailable alert to the agent-pod floor

**Target:** `spec/16_observability.md`, the `lenny_pool_termination_budget_exceeded_total` inventory row (currently at `:133`) and the `PoolConfigValidatorUnavailable` alert description (currently at `:486`), both of which name the §10.1 rule by the heading SPEC-2 renames.

**Rationale:** spec/16 is the single-sourced observability inventory. Its description of the metric CODE-1 keeps emitting states the reject condition as the BarrierAck-inclusive, single-cap formula `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds`. The validator is the only emitter, so after the fix it rejects on the agent floor `maxConcurrentSessions × max_tiered_checkpoint_cap + 30 > terminationGracePeriodSeconds`, and the inventory's stated condition is doubly wrong: it carries the BarrierAck term the fix removes and omits the `maxConcurrentSessions` multiplier the fix keeps. The row must state the reconciled agent floor so the metric catalog agrees with §5.2, §10.1, and the validator. The `PoolConfigValidatorUnavailable` alert in the same file names the §10.1 rules the validator applies by the "tiered-cap + BarrierAck budget and BarrierAck floor" heading SPEC-2 renames, so it must be renamed in lockstep or it dangles on a removed rule name and re-attributes a BarrierAck budget to the webhook.

**Change (staged spec text).** In the `lenny_pool_termination_budget_exceeded_total` row, replace the reject-condition phrase:

```markdown
increments each time the `lenny-pool-config-validator` admission webhook rejects a `SandboxTemplate`/`SandboxWarmPool` write because `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds`; see [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) CRD validation rule — tiered cap + BarrierAck budget
```

with:

```markdown
increments each time the `lenny-pool-config-validator` admission webhook rejects a `SandboxTemplate`/`SandboxWarmPool` write because the pool's effective `terminationGracePeriodSeconds` is below the agent-pod floor `maxConcurrentSessions × max_tiered_checkpoint_cap + 30`; see [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) CRD validation rule — agent-pod grace floor
```

The `barrierAck`-inclusive `cap + checkpointBarrierAckTimeoutSeconds + 30` condition is the gateway-pod budget, which no admission surface vets, so it is not a reject condition of this metric.

In the `PoolConfigValidatorUnavailable` alert description (`:486`), replace the rule name so the alert names the reconciled §10.1 rule set the validator applies. Replace the phrase:

```markdown
the semantic budget rules ([§10.1](10_gateway-internals.md#101-horizontal-scaling) tiered-cap + BarrierAck budget and BarrierAck floor) apply to every writer including the PSC
```

with:

```markdown
the semantic budget rules ([§10.1](10_gateway-internals.md#101-horizontal-scaling) agent-pod grace floor and BarrierAck floor) apply to every writer including the PSC
```

This keeps the alert consistent with the reframed §10.1, which states the validator enforces the agent-pod grace floor rather than a BarrierAck budget on the CRD grace field.

### SPEC-4. Reconcile the §17.2 webhook-inventory description of the validator's rules

**Target:** `spec/17_deployment-topology.md`, the `lenny-pool-config-validator` entry in the §17.2 admission-webhook inventory (currently at `:50`), which names the §10.1 rules the validator applies.

**Rationale:** The §17.2 component inventory states that `lenny-pool-config-validator` "applies the semantic budget rules ([§10.1] tiered-cap + BarrierAck budget and BarrierAck floor) to every `SandboxTemplate.spec`/`SandboxWarmPool.spec` write." After SPEC-2 renames the §10.1 rule to the agent-pod grace floor and states that no admission webhook vets the BarrierAck budget on the CRD grace field, this entry names a §10.1 rule ("tiered-cap + BarrierAck budget") that no longer exists and attributes enforcement of a BarrierAck budget to the validator, the exact conflation the proposal removes. The entry must name the reconciled rule set.

**Change (staged spec text).** Replace the phrase:

```markdown
applies the semantic budget rules ([§10.1](10_gateway-internals.md#101-horizontal-scaling) tiered-cap + BarrierAck budget and BarrierAck floor) to every `SandboxTemplate.spec`/`SandboxWarmPool.spec` write
```

with:

```markdown
applies the semantic budget rules ([§10.1](10_gateway-internals.md#101-horizontal-scaling) agent-pod grace floor and BarrierAck floor) to every `SandboxTemplate.spec`/`SandboxWarmPool.spec` write
```

Leave the rest of the entry, including the `userInfo`-based authorization-denial clause, unchanged.

### CODE-1. Fix `decideTerminationBudget`: agent floor without BarrierAck, evaluated against the effective grace period

**Target:** `pkg/admission/pool_config_validator/validator.go` (the constant block at `:568-583`, the doc comment at `:585-617`, `decideTerminationBudget` at `:618-660`, the floor computation at `:635`, and the nil-guarded rejection at `:644`).

**Rationale:** The nil guard at `:644` skips the floor-versus-grace rejection for an omitted field, and `:635` folds the gateway-side BarrierAck into the agent-pod floor. Both must change so the webhook enforces the reconciled agent floor against the grace period the podspec will render.

**Change (staged description).**

1. Add a local constant to the `:568-583` block, mirroring the existing spec-cited local constants (`minStreamDrainSeconds`, `nodeDrainWarnSeconds`):

```go
// agentDefaultTerminationGraceSeconds is the §4.6.1 agent-pod
// terminationGracePeriodSeconds default the podspec renders when the
// SandboxWarmPool/SandboxTemplate field is omitted. The webhook uses it
// as the effective grace period against which the §5.2 agent floor is
// evaluated for an omitted field. spec: §4.6.1
const agentDefaultTerminationGraceSeconds = 120
```

2. Compute the agent floor without the BarrierAck term. Change `:635` from

```go
floor := int64(maxConcurrent)*tierCap + barrierAck + int64(minStreamDrainSeconds)
```

to

```go
floor := int64(maxConcurrent)*tierCap + int64(minStreamDrainSeconds)
```

Keep the `barrierAck` parse (`:624-627`) and the BarrierAck-floor rule (`:628`) unchanged; `barrierAck` is still vetted against the tier cap, it is only removed from the grace floor. Keep the `maxTerminationGracePeriodSeconds` ceiling check (`:636`) unchanged; it now compares the reconciled floor against the ceiling.

3. Replace the nil-guarded comparison at `:644` with an effective-grace evaluation that closes the nil-bypass:

```go
effGrace := int64(agentDefaultTerminationGraceSeconds)
graceDefaulted := true
if spec.TerminationGracePeriodSeconds != nil && *spec.TerminationGracePeriodSeconds > 0 {
    effGrace = *spec.TerminationGracePeriodSeconds
    graceDefaulted = false
}
if effGrace < floor {
    return rejectBudget(/* reconciled message, see step 4 */)
}
```

4. Rewrite the rejection message to name the effective grace period, whether it was declared or defaulted, and the reconciled floor terms without the ack term. For example: `"the pool's effective terminationGracePeriodSeconds (%ds, %s) is below the Section 5.2 agent-pod floor for this pool (%ds = %d x %d tier cap + %d drain); set spec.terminationGracePeriodSeconds at or above the floor, or reduce spec.maxConcurrent / spec.workspaceSizeLimitBytes"`, where the `%s` renders `"declared"` or `"the §4.6.1 default"` from `graceDefaulted`, and the floor terms are `floor, maxConcurrent, tierCap, minStreamDrainSeconds`. Preserve `rejectBudget` so the rejection sets `BudgetExceeded` and the §16.1 `lenny_pool_termination_budget_exceeded_total` counter increments.

5. Update the `decideTerminationBudget` doc comment (`:585-617`) and its `// spec:` citations to the reconciled clauses: the floor formula drops `+ checkpointBarrierAckTimeoutSeconds`, the rejection-rules list states that the floor is evaluated against the effective grace period (declared value, else the §4.6.1 120s default), and the BarrierAck-floor rule is described as an independent rule that does not enter the grace floor.

Run `gofumpt` and `goimports`. Carry a `// spec:` tie on the reconciled §5.2/§10.1 clauses.

### CODE-2. Reconcile the `SandboxTemplate` CRD field doc comments and regenerate the CRD manifests

**Target:** `pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go` (the `TerminationGracePeriodSeconds` doc comment at `:177-188` and the `CheckpointBarrierAckTimeoutSeconds` doc comment at `:202-212`), and the two generated CRD manifests that mirror those comments (`charts/lenny/crds/lenny.dev_sandboxtemplates.yaml` and `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml`).

**Rationale:** The `TerminationGracePeriodSeconds` field's Go doc comment states the agent-pod floor as `maxConcurrent × max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` and carries a `// spec: §5.2 line 516` citation (`sandboxtemplate_types.go:177-188`). The `CheckpointBarrierAckTimeoutSeconds` comment states that "The pool-config webhook adds it to the per-pod `terminationGracePeriodSeconds` floor" (`:202-212`). controller-gen renders the comments into the operator-facing CRD schema descriptions: the `CheckpointBarrierAckTimeoutSeconds` description is present verbatim in both generated manifests (`charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:76-85` and `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml:76-85`), and both manifests carry the `controller-gen.kubebuilder.io/version: v0.16.5` header (`:6`). After SPEC-1 and CODE-1 land, the field comment cites §5.2 for a formula §5.2 no longer contains, contradicts the reconciled validator, overstates the required grace by the BarrierAck term, and the shipped CRD description (a `kubectl explain` surface) instructs deployers with the wrong formula. `.claude/rules/code-best-practices.md` requires the `// spec:` citation to trace to correct behavior and the generated manifests to be regenerated rather than hand-edited.

**Change (staged description).**

1. Rewrite the `TerminationGracePeriodSeconds` doc comment (`:177-188`) to the reconciled agent-pod floor `maxConcurrent × max_tiered_checkpoint_cap + 30`, stating that `checkpointBarrierAckTimeoutSeconds` belongs to the gateway pod's grace period rather than the agent-pod floor, and keeping the `// spec: §5.2` tie so the citation traces to the reconciled §5.2 clause.
2. Rewrite the `CheckpointBarrierAckTimeoutSeconds` doc comment (`:202-212`) so it no longer states the pool-config webhook adds the term to the grace floor. Keep the description of the independent §10.1 BarrierAck-floor rule (`checkpointBarrierAckTimeoutSeconds ≥ max_tiered_checkpoint_cap`), which is unchanged, and note that the term sizes the gateway pod's grace period.
3. Regenerate `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml` and `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml` with `make generate` (controller-gen `v0.16.5`) rather than hand-editing them, so the operator-facing CRD descriptions match the reconciled comments.

### CODE-3. Reconcile the `lenny-pool-config-validator` webhook wrapper doc comments

**Target:** `pkg/admission/webhook/pool_config_validator.go` (the `PoolConfigMetricsSink` doc comment at `:17-22`, the `PoolConfigValidator` doc comment at `:34-43`, and the inline `// spec:` note at `:68-71`).

**Rationale:** The webhook wrapper routes `SandboxTemplate` writes into the validator's decision function (`pool_config_validator.go:67` `ruleSet1 = pcv.DecideTemplate(&tpl)`, which reaches `decideTerminationBudget`). Its doc comments describe the rule the wrapper counts and rejects on as "the §10.1 line 119 tiered-cap + BarrierAck termination-budget inequality" (`:19-20`, `:42-43`, `:68-69`). After SPEC-2 and CODE-1 the validator enforces the BarrierAck-free agent-pod floor, so these comments cite §10.1 line 119 for a formula that section no longer contains and name a BarrierAck term the fix removes. `.claude/rules/code-best-practices.md` requires the `// spec:` tie to trace to correct behavior.

**Change (staged description).** Reword the "§10.1 line 119 tiered-cap + BarrierAck termination-budget inequality" phrasing in the three comment sites to the reconciled agent-pod grace floor (§5.2 / §10.1 CRD validation rule — agent-pod grace floor), dropping the "BarrierAck" term, and update the line-number references to the reconciled clauses so the `// spec:` tie traces to the BarrierAck-free agent floor the wrapper counts and rejects on. Change no wrapper logic; the doc comments alone are reconciled. Run `gofumpt` and `goimports`.

### TEST-1. Correct the tier-1 validator unit tests for the omitted-field contract and the reconciled floor

**Target:** `pkg/admission/pool_config_validator/validator_test.go` (`TestDecideTemplate_TerminationGraceFloor_spec_5_2_516` at `:308-460`, in particular the omitted-field subtest at `:432` and every floor number in the table, which currently folds in the BarrierAck term).

**Rationale:** The omitted-field subtest at `:432` currently asserts that an omitted-grace pool is admitted with no comparison, which pins the nil-bypass; and every floor number in the table (300, 840, 180, 240, 120, 210) includes the BarrierAck term this fix removes. The tests must encode the new contract and the reconciled floor. This subtest is a pure dependent of CODE-1 and SPEC-1: it inherits the BarrierAck-drop those changes stage, so its literals must not be pre-baked ahead of sign-off.

**Change (staged description).**

1. Retarget the omitted-field subtest at `:432`. Replace "session-mode pool with no grace period set → admitted (no comparison)" with two subtests derived from the same exported timing constants CODE-1 uses (`agentDefaultTerminationGraceSeconds`, the tier cap from `MaxTieredCheckpointCapSeconds`, `minStreamDrainSeconds`) rather than hand-computed literals, so the table survives whichever way the open decision on the BarrierAck reconciliation resolves:
   - **Omitted grace, effective default below the floor → rejected.** A multi-slot service pool (`maxConcurrent = 2`, default tier) whose effective grace period is the 120s default is below the reconciled floor (`2 × 90 + 30 = 210s`). Assert `assertRejected`, code 422, `BudgetExceeded` set, and the reason naming the §5.2 agent-pod floor and that the grace period was the default.
   - **Omitted grace, effective default equals the floor → admitted.** A single-slot default-tier pool whose effective grace period is the 120s default equals the reconciled floor (`1 × 90 + 30 = 120s`). Assert `assertAllowed` with no warning.
2. Recompute every explicit-grace subtest to the BarrierAck-free floor, deriving expected values from the constants: the at-floor service case (`maxConcurrent = 2`, default tier) becomes 210s rather than 300s; the 300 MB case becomes `2 × 60 + 30 = 150s`; the 100 MB case becomes `2 × 30 + 30 = 90s`; the session-mode 1s-grace case has floor `1 × 90 + 30 = 120s` rather than 210s; and the >600s warning case (`maxConcurrent = 8`) becomes `8 × 90 + 30 = 750s`.
3. Keep the BarrierAck-floor subtest ("checkpointBarrierAckTimeoutSeconds below tier cap → rejected") unchanged, since that rule is unchanged by this fix. Keep the `maxTerminationGracePeriodSeconds` breach subtest, recomputing the floor it compares against.
4. Cover the empty (`RuntimeRef`-only) pool, the boundary at exactly the floor, and the boundary one second below it. Keep the `// spec:` annotation on the reconciled §5.2/§10.1 sections and update the class doc comment (`:300-307`) to drop the BarrierAck term from the described formula.

### TEST-2. Correct the existing tier-2 envtest case and add absent-field coverage against a real kube-apiserver

**Target:** `tests/tier2_component/admission/pool_config_validator_test.go` (`TestDecideTemplateRejectsBelowGraceFloor_spec_5_2_516` at `:182-216`, currently asserting the 840s BarrierAck-inclusive floor for an explicit below-floor grace).

**Rationale:** The tier-2 component test exercises only an explicitly-set below-floor grace (`grace = 120`, `maxConcurrent = 8`). The defect is the absent field: `validator.go:644` guards the rejection behind `spec.TerminationGracePeriodSeconds != nil`, and `sandboxtemplate_types.go:188` declares the field `*int64` with `omitempty` and no `+kubebuilder:default`, so an omitted field is nil at admission. The distinct tier-2 property is the CRD-codec round-trip: an omitted field must come back nil (no defaulting) so the webhook applies the 120s effective default. A nil-pointer Go struct built directly at tier 1 cannot detect a regression where a `+kubebuilder:default` is later added to the CRD, which would make the round-tripped field non-nil and flip the result. This file exists to catch such codec field regressions, and `.claude/rules/test-coverage.md` maps anything reading or writing the kube-apiserver to tier 2. CODE-1 also invalidates the existing case, which hard-codes the BarrierAck-inclusive 840s result, so leaving it untouched would turn the tier-2 suite red.

**Change (staged description).**

1. Correct `TestDecideTemplateRejectsBelowGraceFloor_spec_5_2_516` to the reconciled BarrierAck-free floor: the rationale comment (`:185-189`) and the `d.Reason` assertion (`:213-215`) change from 840s to `8 × 90 + 30 = 750s`, and the `// spec:` and `// diagnosis:` header (`:172-181`) drop the "BarrierAck budget" phrasing so it reflects the reconciled §5.2 floor. Keep the code-422 and `BudgetExceeded` assertions.
2. Add an absent-field rejection case: a schema-valid `SandboxTemplate` with `terminationGracePeriodSeconds` absent whose 120s effective default is below the reconciled floor (a multi-slot service pool, `maxConcurrent = 2`, floor `2 × 90 + 30 = 210s`), created and read back through envtest, asserting the round-tripped field is nil, then rejection with code 422, the §5.2 agent-pod floor named, and `BudgetExceeded` set.
3. Add an absent-field admit case as the positive control (mirroring `TestDecideTemplateAdmitsInPlaceWithAck_spec_5_2` at `:148`): a single-slot default-tier pool with `terminationGracePeriodSeconds` absent whose 120s default equals the floor, asserting the round-tripped field is nil and the pool is admitted. This pins CODE-1's core §4.6.1-versus-floor reconciliation, that the default single-slot pool's 120s must now be valid.
4. Keep the `// spec:` and `// diagnosis:` comments matching the file convention.

### TEST-3. Correct the tier-1 webhook-wrapper tests that hard-code the BarrierAck-inclusive floor and the omitted-field admit

**Target:** `pkg/admission/webhook/pool_config_validator_test.go` (`TestPoolConfigValidatorPropagatesTerminationGraceWarning_spec_5_2_516` at `:191-210`, and the stale floor comment in `TestPoolConfigValidatorEmitsBudgetCounter_spec_16_1_129` at `:253`).

**Rationale:** These tests exercise the same decision path through the webhook wrapper (`pool_config_validator.go:67` → `DecideTemplate` → `decideTerminationBudget`) and hard-code the BarrierAck-inclusive floor and the old omitted-field admit behavior. `TestPoolConfigValidatorPropagatesTerminationGraceWarning_spec_5_2_516` builds a service pool with `MaxConcurrent: 8` and no `terminationGracePeriodSeconds`, asserts the write is admitted with a warning, and asserts the warning text contains `"840s"` (`:197`, `:201`, `:207`). After CODE-1 drops the BarrierAck term, the floor for that pool becomes `8 × 90 + 30 = 750s` rather than `8 × 90 + 90 + 30 = 840s`, so the `"840s"` assertion fails; and the omitted grace now defaults to the §4.6.1 120s effective value, which is below the 750s floor, so the pool is rejected rather than admitted with a warning and the `if !resp.Allowed` assertion also fails. The stale `// … → floor 210s` comment at `:253` becomes `1 × 90 + 30 = 120s` under the reconciled floor. This file is not covered by TEST-1 or TEST-2, so leaving it untouched turns the tier-1 admission suite red after CODE-1.

**Change (staged description).**

1. Retarget `TestPoolConfigValidatorPropagatesTerminationGraceWarning_spec_5_2_516` to exercise the >600s advisory-warning path under the BarrierAck-free floor. Set an explicit `terminationGracePeriodSeconds` at or above the recomputed 750s floor for the `MaxConcurrent: 8` pool so the write admits and the advisory warning fires, and assert the warning names `"750s"` (derived from the tier cap and `minStreamDrainSeconds` timing constants rather than a literal) rather than `"840s"`. Update the `// 8*90 + 90 + 30 = 840s` inline comment to the BarrierAck-free `8*90 + 30 = 750s`.
2. Add an omitted-grace multi-slot case asserting rejection, so the wrapper test encodes the new contract that an omitted field whose 120s effective default under-provisions is rejected with code 422 and the `INVALID_POOL_CONFIGURATION` reason.
3. Recompute the stale floor comment at `:253` (`// … → floor 210s > 1s`) to `1 × 90 + 30 = 120s > 1s`; the 1s explicit grace stays below the reconciled floor, so the rejection and counter assertions are unchanged.
4. Keep the `maxTerminationGracePeriodSeconds` ceiling-breach case and the BarrierAck-floor and execution-mode cases unchanged except for any floor literal they name, recomputing it to the BarrierAck-free form. Keep the `// spec:` annotations, updating the line references to the reconciled §5.2 / §10.1 clauses.

## 5. Non-goals

- **Building or touching the C-22 eviction trigger (T-4.4.14) or C-22 prereq 4.** The agent-preStop-to-gateway eviction-checkpoint routing stays as it is; this fix is admission-only. The floor it hardens becomes load-bearing when that trigger lands, which is a separate line of work.
- **Sharing the §4.6.1 agent-default grace constant between the podspec and the validator (the constant-sharing change, dropped during the challenge round).** The premise that both packages already depend on a common home is false: `pkg/controller/sandbox/podspec` does not import `pkg/apis/lenny/v1alpha1` (`podspec.go:33-44`; `go list -deps ./pkg/controller/sandbox/podspec` returns no `apis/lenny/v1alpha1`), and it reads `in.TerminationGraceSeconds` from its own `Inputs` struct (`podspec.go:1303`) rather than the CRD type. Introducing a shared exported constant in the apis package would create a new `podspec → apis` import edge purely to share one literal, which is the opposite of the stated goal. The established pattern is per-package local constants each carrying a `// spec:` citation: the validator already owns `defaultCheckpointBarrierAckTimeoutSeconds` (`validator.go:571`), `minStreamDrainSeconds` (`:577`), and `nodeDrainWarnSeconds` (`:583`), and the podspec independently owns `defaultTerminationGraceSeconds = 120` with a §4.6.1 citation (`podspec.go:75`). The shared source of truth that keeps the two packages aligned is the `// spec: §4.6.1` citation rather than a Go constant. There is no `+kubebuilder:default` on the field, so the apis package owns no default a shared constant would represent. CODE-1 adds a single local `agentDefaultTerminationGraceSeconds` constant inside `validator.go`, mirroring its own `minStreamDrainSeconds`, and leaves the podspec untouched.
- **Changing the gateway-pod grace period (`spec/17_deployment-topology.md:971`, `terminationGracePeriodSeconds (gateway pod)` 240/240/300) or adding admission validation of the gateway-pod floor.** The gateway grace period is a Helm value rather than a per-pool CRD field, so no admission surface vets it.
- **Re-opening the §4.6.1 120s agent-default value.** The default is kept and reused as the omitted-field effective grace period; CODE-1 adds a local mirror constant only.
- **Changing the §10.1 BarrierAck-floor rule (`checkpointBarrierAckTimeoutSeconds ≥ max_tiered_checkpoint_cap`).** It does not involve the grace field and is correct as-is; `checkpointBarrierAckTimeoutSeconds` is still parsed and vetted against the tier cap.
- **Auto-provisioning the computed floor onto the agent pod when the field is omitted.** The contract is to reject the omission that under-provisions; the podspec keeps the flat 120s default.
- **Adding a `+kubebuilder:default` to the CRD field.** A static default cannot express the pool-dependent floor, and the fix relies on the effective-grace evaluation in the webhook. Keeping the field nil-on-omission is also what the tier-2 codec round-trip in TEST-2 asserts.
- **Any backward-compatibility shim or dual mode.** No deployments exist in the wild; the validator changes in place and every test is updated.

## 6. Testing

The change reaches tier 0 (static), tier 1 (the pure `decideTerminationBudget` decision function), and tier 2 (the admission webhook against a real kube-apiserver-backed CRD codec via envtest), per `.claude/rules/test-coverage.md`: the validator is in-process pure logic (tier 1), and admission that reads a CRD object round-tripped through the API server is tier 2. There is no wire-contract, multi-service, cluster, or security-boundary surface beyond these, so no higher tier is reached. Each test below covers a non-happy path and carries a `// spec:` tie; the tier-2 test carries a `// diagnosis:` comment.

- **tier-1 omitted-field rejection (spec-named-failure, boundary).** In `validator_test.go`, an omitted-grace multi-slot service pool whose 120s effective default is below the reconciled floor (`2 × 90 + 30 = 210s`) is rejected with code 422, `BudgetExceeded` set, and the §5.2 agent-pod floor named. The non-happy path is the omitted field that previously bypassed the comparison. Expected values are derived from the exported timing constants rather than literals. `// spec: 5.2 (agent-pod terminationGracePeriodSeconds floor, omitted-field evaluation), 10.1 (gateway-versus-agent floor)`.
- **tier-1 omitted-field admit at the floor (boundary).** An omitted-grace single-slot default-tier pool whose 120s default equals the reconciled floor is admitted with no warning. The non-happy path is the exact-floor boundary that must not reject. `// spec: 5.2 (agent floor equals §4.6.1 default for the single-slot pool)`.
- **tier-1 reconciled floor without BarrierAck (boundary, empty).** The explicit-grace subtests recomputed to the BarrierAck-free floor: the at-floor service case at 210s, the 300 MB case at 150s, the 100 MB case at 90s, and the session-mode 1s-grace case rejected against a 120s floor; plus the empty `RuntimeRef`-only pool and the one-second-below-floor boundary. The non-happy path is a floor that still carries the dropped BarrierAck term. `// spec: 5.2 (reconciled agent floor maxConcurrentSessions × cap + 30)`.
- **tier-1 BarrierAck-floor rule unchanged (spec-named-failure).** The `checkpointBarrierAckTimeoutSeconds < max_tiered_checkpoint_cap` rejection is retained unchanged, confirming the fix does not disturb the independent §10.1 BarrierAck-floor rule. The non-happy path is a BarrierAck below the tier cap. `// spec: 10.1 (BarrierAck floor)`.
- **tier-1 webhook-wrapper floor and omitted-field cases (spec-named-failure, boundary).** In `pkg/admission/webhook/pool_config_validator_test.go` (TEST-3), the advisory-warning case is corrected to the BarrierAck-free 750s floor for the `maxConcurrent = 8` pool, an omitted-grace multi-slot case is added asserting rejection with code 422, and the stale floor comment is recomputed to 120s. The non-happy path is the wrapper path admitting an omitted field that under-provisions. Expected values derive from the timing constants rather than literals. `// spec: 5.2 (agent floor through the webhook wrapper), 10.1 (agent-versus-gateway floor)`.
- **tier-2 absent-field admission against the CRD codec (spec-named-failure, boundary).** In `tests/tier2_component/admission/pool_config_validator_test.go`, a schema-valid `SandboxTemplate` with `terminationGracePeriodSeconds` absent is created and read back through envtest; the test asserts the round-tripped field is nil (no defaulting), then that a multi-slot service pool at the 210s floor is rejected with code 422 and `BudgetExceeded`, and that a single-slot default-tier pool at the 120s floor is admitted. The non-happy path is the absent field the tier-1 struct cannot exercise, plus a future `+kubebuilder:default` regression that would flip the result. `// spec: 5.2 (agent floor, omitted-field codec round-trip), 10.1 (agent-versus-gateway floor)`.
- **tier-2 existing below-floor case corrected (spec-named-failure).** `TestDecideTemplateRejectsBelowGraceFloor_spec_5_2_516` is corrected to the reconciled 750s floor (`8 × 90 + 30`) so the tier-2 suite stays green after CODE-1, with the "BarrierAck budget" phrasing dropped from its header. The non-happy path is an explicit below-floor grace on a large multi-slot pool. `// spec: 5.2 (reconciled agent floor)`.
- **tier-0 CRD codegen check (CODE-2).** The regenerated `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml` and `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml` must match `make generate` output for the reconciled `sandboxtemplate_types.go` doc comments; tier 0's schema and codegen checks fail if the manifests are stale or hand-edited. The non-happy path is a manifest left out of sync with the reconciled field comment.

## 7. Findings closed on application

This proposal fixes an admission-validation defect (the nil-bypass and the gateway-versus-agent floor conflation) surfaced during the C-22 prerequisite work. It closes no existing `TEST-GAPS.md` finding, and it deliberately leaves the C-22 eviction trigger finding T-4.4.14 and C-22 prereq 4 open, since the fix is admission-only and does not build the trigger the hardened floor protects. The T-4.4.21 capture-side note recorded during earlier C-22 integration is context rather than a finding this proposal closes. On application, the reconciled §5.2 and §10.1 spec text and the corrected validator remove the §4.6.1-versus-floor contradiction, so the agent default admits and an under-provisioned pool (declared or omitted) is rejected fail-closed.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions carried in the draft narrowed the proposal before the first automated pass:

- **The constant-sharing change was dropped.** The proposal to share the §4.6.1 agent-default constant between the podspec and the validator rested on a false dependency premise (`podspec` does not import `pkg/apis/lenny/v1alpha1`), contradicted the codebase's per-package spec-cited-constant pattern, and would have created a new import edge to share one literal. CODE-1 adds a local `agentDefaultTerminationGraceSeconds` constant instead, and the reasoning is recorded in Non-goals. (The `CODE-2` label is now used for the reconciliation of the CRD field doc comments, a distinct change.)
- **TEST-1 was reduced to the load-bearing correction.** The original TEST-1 pre-baked BarrierAck-free literals across a fully renumbered table while the BarrierAck reconciliation remained an open decision. It was rescoped to retarget the omitted-field subtest and to derive every expected floor value from the exported timing constants, so the table survives whichever way the open decision resolves, and the renumbered floor table is not duplicated across TEST-1 and TEST-2 ahead of sign-off.
- **TEST-2 was widened from "add two envtest cases" to also correct the existing below-floor case.** CODE-1 drops the BarrierAck term from the floor, which invalidates the untouched `TestDecideTemplateRejectsBelowGraceFloor_spec_5_2_516` (hard-coded to the 840s BarrierAck-inclusive result). TEST-2 now corrects that sibling case to 750s alongside the two absent-field cases, so the tier-2 suite stays green.

### Pass 1 (2026-07-22, automated)

- **SPEC-2 was rewritten from an appended paragraph to a reframe of §10.1's rule.** The prior SPEC-2 only inserted a clarifying paragraph and explicitly left the §10.1:116-122 enforcement sentence, the BarrierAck-inclusive inequality, and the rejection message unchanged, which left the section attributing the BarrierAck-inclusive rejection to `lenny-pool-config-validator` while SPEC-1 and CODE-1 give the validator the BarrierAck-free agent floor. Because `MaxTieredCheckpointCapSeconds` returns 90 for an unset workspace size, the unchanged inequality computed 90 + 90 + 30 = 210s and re-rejected every default single-slot 120s pool, defeating the proposal's core goal. SPEC-2 now renames the rule to the agent-pod grace floor, replaces the inequality with `maxConcurrentSessions × max_tiered_checkpoint_cap + 30 > terminationGracePeriodSeconds`, rewrites the rejection message to the BarrierAck-free form, names the gateway-pod `cap + barrierAck + 30` budget as a separate Helm-sized concern that no admission webhook vets, and reframes the `:167` "why the formula adds BarrierAck once" sentence, so §10.1 agrees with §5.2 and CODE-1.
- **The `SandboxTemplate` CRD field doc comments and the generated CRD manifests were added to the edit list as CODE-2.** The `TerminationGracePeriodSeconds` doc comment (`sandboxtemplate_types.go:177-188`) states the agent floor with `+ checkpointBarrierAckTimeoutSeconds` and carries a `// spec: §5.2` citation, and the `CheckpointBarrierAckTimeoutSeconds` comment (`:202-212`) states the webhook adds the term to the grace floor. controller-gen renders these into the operator-facing CRD schema (the `CheckpointBarrierAckTimeoutSeconds` description appears verbatim in both `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:76-85` and `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml:76-85`). After SPEC-1 and CODE-1 the comment would cite §5.2 for a formula it no longer contains and instruct deployers with the wrong `kubectl explain` surface. CODE-2 rewrites both comments to the reconciled agent floor and regenerates the two manifests via `make generate` rather than hand-editing them.
- **The §16.1 metric reject condition was added to the edit list as SPEC-3.** The `lenny_pool_termination_budget_exceeded_total` inventory row (`spec/16_observability.md:133`) states the reject condition as the BarrierAck-inclusive, single-cap formula, which after the fix is doubly wrong: it carries the BarrierAck term the fix removes and omits the `maxConcurrentSessions` multiplier the fix keeps. The validator is the only emitter, so SPEC-3 rewords the condition to the reconciled agent floor `maxConcurrentSessions × max_tiered_checkpoint_cap + 30 > terminationGracePeriodSeconds`.

### Pass 2 (2026-07-22, automated)

- **The §17.2 webhook-inventory description of the validator's rules was added to the edit list as SPEC-4.** The `lenny-pool-config-validator` entry in the §17.2 admission-webhook inventory (`spec/17_deployment-topology.md:50`) states that the validator "applies the semantic budget rules ([§10.1] tiered-cap + BarrierAck budget and BarrierAck floor)" to every `SandboxTemplate.spec`/`SandboxWarmPool.spec` write. After SPEC-2 renames the §10.1 rule to the agent-pod grace floor and states no admission webhook vets the BarrierAck budget on the CRD grace field, this entry named a §10.1 rule that no longer exists and re-attributed a BarrierAck budget to the validator, the exact conflation the proposal removes. SPEC-4 renames the cited rule set to "agent-pod grace floor and BarrierAck floor" so the component inventory matches the reframed §10.1.
- **The §16.1 `PoolConfigValidatorUnavailable` alert description was added to SPEC-3.** The alert (`spec/16_observability.md:486`) states that "the semantic budget rules ([§10.1] tiered-cap + BarrierAck budget and BarrierAck floor) apply to every writer including the PSC," naming the §10.1 rule by the heading SPEC-2 renames. Left unchanged it would dangle on a removed rule name and re-attribute a BarrierAck budget to the webhook. SPEC-3 now also renames that phrase to "agent-pod grace floor and BarrierAck floor," matching the reframed §10.1.
- **The §10.1 `:177` "CRD-validated … budget" reference was added to SPEC-2.** The BarrierAck-timeout partial-capture paragraph (`spec/10_gateway-internals.md:177`) calls the `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` formula "the CRD-validated … budget." After SPEC-2 replaces the CRD inequality with the BarrierAck-free agent floor and names the BarrierAck-inclusive figure the un-vetted gateway budget, that phrase attributed a formula to CRD validation the reconciled rule no longer contains. SPEC-2 now rewords `:177` to name it the gateway pod's Helm-sized drain budget rather than CRD-validated, keeping §10.1 internally consistent.
- **The tier-1 webhook-wrapper tests that hard-code the 840s floor and the omitted-field admit were added as CODE-3 and TEST-3.** `pkg/admission/webhook/pool_config_validator_test.go` exercises the same decision path through the wrapper (`pool_config_validator.go:67` `DecideTemplate` → `decideTerminationBudget`). `TestPoolConfigValidatorPropagatesTerminationGraceWarning_spec_5_2_516` builds a `MaxConcurrent: 8` pool with no grace set, asserts admission with a warning, and asserts the warning contains `"840s"` (`:197`, `:201`, `:207`); after CODE-1 the floor becomes 750s and the omitted 120s default falls below it, so both assertions fail and the tier-1 suite goes red. TEST-3 retargets the case to the BarrierAck-free 750s floor with an explicit at-or-above-floor grace, asserts `"750s"` derived from the timing constants, adds an omitted-grace multi-slot rejection case, and recomputes the stale `:253` floor comment to 120s. CODE-3 reconciles the wrapper doc comments (`:17-22`, `:34-43`, `:68-71`) that cite the "§10.1 line 119 tiered-cap + BarrierAck termination-budget inequality" so the `// spec:` tie traces to the reconciled agent floor.

## 9. Open decisions for review

- **RATIFIED on sign-off (2026-07-22).** Both decisions below are ratified as staged: (1) drop `checkpointBarrierAckTimeoutSeconds` from the agent-pod floor and keep the `+30` stream-drain term, so the agent floor is `maxConcurrentSessions × max_tiered_checkpoint_cap + 30` and the §4.6.1 120s default is exactly valid for the default single-slot pool; (2) reject an omitted `terminationGracePeriodSeconds` whose effective 120s default is below the reconciled floor (fail-closed; the deployer must set the field), rather than auto-provisioning the computed floor. Implement SPEC-1..4, CODE-1..3, and TEST-1..3 as staged.
- **The BarrierAck drop from the agent-pod floor changes the normative §5.2 inequality and clarifies §10.1.** Confirm this is the intended reconciliation, versus keeping `checkpointBarrierAckTimeoutSeconds` in the agent floor and accepting that trivially-configured session pools (120s default below a 210s floor) are rejected, or a different agent-floor definition (for example, whether the `+30` stream-drain term, also a gateway preStop stage in §10.1, belongs in the agent floor or should instead be the code's 10s `preStopDrainMarginSeconds`). The recommendation is to drop BarrierAck and keep `+30`, so the §4.6.1 120s default remains exactly valid for the default single-slot pool.
- **The reject-the-omission contract versus auto-provisioning.** Confirm that an omitted `terminationGracePeriodSeconds` whose 120s default is below the reconciled floor is rejected (the deployer must set the field), rather than the controller rendering the computed floor onto the agent pod. Reject is recommended as spec-literal (§5.2, "Deployers must set `terminationGracePeriodSeconds` accordingly") and surfaces the misconfiguration instead of silently papering over it.

## 10. Files touched on application

- `spec/05_runtime-registry-and-pool-model.md`: SPEC-1 (reconcile the §5.2 agent-pod webhook inequality; drop the BarrierAck term; evaluate an omitted field against the §4.6.1 default; correct the node-drain worked example to 750s).
- `spec/10_gateway-internals.md`: SPEC-2 (rename the CRD validation rule to the agent-pod grace floor and drop the BarrierAck term from the webhook inequality and rejection message; name the gateway-pod `cap + barrierAck + 30` budget as a separate Helm-sized concern no admission webhook vets; reframe the `:167` "why the formula adds BarrierAck once" sentence; reword the `:177` "CRD-validated … budget" reference to name it the gateway pod's Helm-sized drain budget; the separate BarrierAck-floor rule at `:124` is unchanged).
- `spec/16_observability.md`: SPEC-3 (reword the `lenny_pool_termination_budget_exceeded_total` reject condition at `:133` from the BarrierAck-inclusive formula to the reconciled agent-pod floor `maxConcurrentSessions × max_tiered_checkpoint_cap + 30`; rename the §10.1 rule the `PoolConfigValidatorUnavailable` alert at `:486` cites from "tiered-cap + BarrierAck budget and BarrierAck floor" to "agent-pod grace floor and BarrierAck floor").
- `spec/17_deployment-topology.md`: SPEC-4 (rename the §10.1 rule the `lenny-pool-config-validator` §17.2 inventory entry at `:50` cites from "tiered-cap + BarrierAck budget and BarrierAck floor" to "agent-pod grace floor and BarrierAck floor").
- `pkg/admission/pool_config_validator/validator.go`: CODE-1 (add the local `agentDefaultTerminationGraceSeconds` constant; drop `barrierAck` from the floor sum at `:635`; replace the nil-guarded comparison at `:644` with the effective-grace evaluation; rewrite the rejection message and the doc comment; keep the BarrierAck-floor rule and the ceiling check unchanged).
- `pkg/admission/webhook/pool_config_validator.go`: CODE-3 (reword the wrapper doc comments at `:17-22`, `:34-43`, and `:68-71` from the "§10.1 line 119 tiered-cap + BarrierAck termination-budget inequality" phrasing to the reconciled agent-pod grace floor, updating the `// spec:` line references; no wrapper logic changes).
- `pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go`: CODE-2 (rewrite the `TerminationGracePeriodSeconds` doc comment at `:177-188` to the reconciled agent floor `maxConcurrent × max_tiered_checkpoint_cap + 30`, keeping the `// spec: §5.2` tie; rewrite the `CheckpointBarrierAckTimeoutSeconds` comment at `:202-212` so it no longer states the webhook adds the term to the grace floor).
- `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml` and `pkg/embedded/crds/lenny.dev_sandboxtemplates.yaml`: CODE-2 (regenerated via `make generate` / controller-gen `v0.16.5` from the reconciled doc comments; not hand-edited).
- `pkg/admission/pool_config_validator/validator_test.go`: TEST-1 (retarget the omitted-field subtest to the reject-and-admit pair; recompute every floor literal to the BarrierAck-free floor derived from the exported constants; cover the empty pool and the at-floor and below-floor boundaries).
- `tests/tier2_component/admission/pool_config_validator_test.go`: TEST-2 (correct the existing below-floor case to 750s; add the absent-field rejection case and the absent-field admit positive control against the CRD codec round-trip).
- `pkg/admission/webhook/pool_config_validator_test.go`: TEST-3 (retarget the advisory-warning case to the BarrierAck-free 750s floor with an explicit at-or-above-floor grace; assert `"750s"` derived from the timing constants rather than `"840s"`; add an omitted-grace multi-slot rejection case; recompute the stale `:253` floor comment to 120s).
