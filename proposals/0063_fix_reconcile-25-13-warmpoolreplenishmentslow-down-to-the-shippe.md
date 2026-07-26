# Proposal: Reconcile §25.13 WarmPoolReplenishmentSlow down to the shipped §16.5 fixed non-tiered Warning, delete the inert chart threshold, and add the documented static alert-event labels payload

- **Status:** Verified (2026-07-26). Converged after 2 adversarial review rounds (0 findings fixed); awaiting sign-off.
- **Date:** 2026-07-26.
- **Scope:** A spec-and-code reconciliation across three §25 alerting divergences, all verified against the tree. Problem 1 (T-25.13.9, Medium) corrects the §25.13 "Tier-Aware Defaults" table and its tier-preset excerpts so `WarmPoolReplenishmentSlow` reads as the fixed non-tiered §16.5 Warning the shipped rule and rendered chart already implement, and deletes the inert `warmPoolReplenishmentSlow` chart-threshold value that nothing consumes. The rule behavior, the rendered `PrometheusRule` fragment, `docs/alerting/rules.yaml`, and the reference docs stay byte-identical. Problem 2 (T-25.17.4, Medium) merges each rule's static `Rule.Labels` into the `alert_fired` CloudEvents payload the §25 Event Types table documents, scoped to the firing edge so `alert_resolved` stays "Alert name, duration". Problem 3 (T-25.17.5, Low) reconciles the §25.17 Step-1 `alert_fired` example down to the static catalog `suggestedAction` the emit path ships, leaving the runtime-enriched remediation in the §25.3 diagnostic response the same walkthrough already carries in Step 3. Two tests are added or corrected in the tier-3 CloudEvents contract suite. The matched-series `labels:{pool:...}` form and any tier-dependent rule variant stay deferred with their reasons recorded.

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

Three §25 alerting divergences, each verified against the current tree.

### Problem 1 (T-25.13.9, Medium): WarmPoolReplenishmentSlow has two incompatible definitions

§16.5 defines `WarmPoolReplenishmentSlow` as a non-tiered Warning: P95 of `lenny_warmpool_pod_startup_duration_seconds` exceeds 2× the pool's `scalingPolicy.podWarmupSecondsBaseline` for more than 5 minutes (`spec/16_observability.md:495`).

§25.13 classifies the same alert as tier-dependent with an incompatible `ratioBelow` (replenishment-rate-versus-claim-rate) metric. The "Tier-Aware Defaults" table lists it among the tier-dependent examples (`spec/25_agent-operability.md:4734`), and the preset excerpts tighten it from `ratioBelow: 0.5`, `10m`, `warning` at Tier 2 (`spec/25_agent-operability.md:4751-4754`) to `ratioBelow: 0.7`, `5m`, `critical` at Tier 3 (`spec/25_agent-operability.md:4767-4770`).

The code implements the §16.5 form verbatim. The catalog entry sets `Expr: histogram_quantile(0.95, sum by (le, pool) (rate(lenny_warmpool_pod_startup_duration_seconds_bucket[5m]))) > 2 * lenny_pool_warmup_seconds_baseline`, `For: 5 * time.Minute`, `Severity: SeverityWarning`, and `SpecRef: "§16.5"` (`pkg/alerting/rules/rules.go:1085-1098`). The rendered chart fragment matches, generated from the same Go `Catalog()`. §16.5 is the authoritative catalog: the code cites it as its `SpecRef`, and the spec names it "authored once as a shared Go package `pkg/alerting/rules`" with the Helm manifests and the in-process tracker both derived from it (`spec/16_observability.md:573`).

The §25.13 tier semantics ship nowhere. The Tier-2 and Tier-3 preset `alertThresholds` blocks carry only `gatewayQueueDepthHigh`, `gatewayLatencyHigh`, and `credentialPoolLow` (`charts/lenny/presets/values-tier2.yaml`, `values-tier3.yaml`); no preset carries `warmPoolReplenishmentSlow` or `ratioBelow`, and `ratioBelow` appears only in `spec/25`. The reference docs already describe the §16.5 form: `docs/reference/metrics.md:527` ("P95 startup duration > 2x baseline for > 5 min", Warning), `docs/reference/configuration.md:129` ("2x this value"), and `docs/runbooks/warm-pool-exhaustion.md:12-13` (`severity: warning`). Reconciling §25.13 down to §16.5 is therefore behavior-neutral.

### Problem 2 (T-25.17.4, Medium): the alert_fired payload never carries a labels key

The §25 Event Types table lists `labels` among the `alert_fired` payload highlights (`spec/25_agent-operability.md:693`), and the worked examples carry `labels:{pool:...}` (`spec/25_agent-operability.md:3255,5177`). The shared emit closure builds the `alert_fired` and `alert_resolved` payload from `a.Rule` fields (`ruleName`, `alertName`, `severity`, `summary`, `sinceUnix`, plus conditional `runbook`, `runbookUrl`, and `suggestedAction`) and never sets a `labels` key (`pkg/alerting/evaluator/emit.go:51-73`).

`Rule.Labels` exists as static per-rule labels (`pkg/alerting/rules/rules.go:115-124`), and `AdmissionPlaneFeatureFlagDowngrade` populates `flag_name` and `expected_webhook_name` on it (`rules.go:254-257`). So the payload omits `labels` even for the one rule that carries static labels. `WarmPoolExhausted` sets no static labels (`rules.go:279-303`) and aggregates `by (pool)`, so the examples' `{pool:default-gvisor}` is a firing-series label rather than a static one. The `ExprEvaluator` contract cannot supply per-series labels: `Active` returns a bool (`pkg/alerting/evaluator/evaluator.go`), and `Alert` carries `Rule`, `State`, and `Since` only. The confirming skipped test is `TestCloudEventsAlertFiredPayloadCarriesLabels` (`tests/tier3_contract/cloudevents/cloudevents_test.go:430`), whose skip reason states the payload emits no labels field and the evaluator surfaces no matched-series labels.

### Problem 3 (T-25.17.5, Low): the §25.17 alert_fired example diverges from the shipped static suggestedAction

The §25.17 Step-1 `alert_fired` example carries a `suggestedAction` with a pool-substituted endpoint (`PUT /v1/admin/pools/default-gvisor/warm-count`), `body:{minWarm:15}`, and a runtime reasoning ("Pool exhausted for 3 minutes. Peak claim rate: 4.2/min.") (`spec/25_agent-operability.md:5177`). The shipped emit path copies the static catalog `SuggestedAction` verbatim (`emit.go:71-72`), and `WarmPoolExhausted`'s catalog entry sets `Action`, an unsubstituted `Endpoint: "PUT /v1/admin/pools/{name}/warm-count"`, a static `Reasoning`, and a `Runbook`, with no `Body` (`rules.go:297-302`). `SuggestedAction.Body` is `omitempty` (`pkg/ops/conventions/conventions.go:300`), so the emitted `suggestedAction` omits `body`. The §25.17 example is therefore a fully runtime-enriched instance rather than the static template the code emits.

The authoritative runtime-enriched `suggestedAction` with `body:{minWarm:15}` already lives in the §25.3 diagnostic-API contract (`spec/25_agent-operability.md:474-500`), and the same §25.17 walkthrough re-shows it in the Step-3 diagnostic response (`spec/25_agent-operability.md:5206-5207`). The §25.7 Path B canonical `alert_fired` example omits `suggestedAction` entirely (`spec/25_agent-operability.md:3244-3258`), which leaves an internal question of whether `alert_fired` carries `suggestedAction` at all. The confirming skipped test is `TestCloudEventsAlertFiredPayloadSuggestedActionBody` (`cloudevents_test.go:354-355`), whose skip comment frames the reconciliation as a spec-versus-implementation human decision.

## 2. Decisions

- **§16.5 is the authoritative alert catalog.** It is the `SpecRef` the code cites (`rules.go:1098`) and the single `Catalog()` source both the rendered manifests and the in-process tracker derive from (`spec/16_observability.md:573`). §25.13's tier overlay never reached the presets. `WarmPoolReplenishmentSlow` reconciles down to §16.5.
- **The shipped rule does not change.** The rule stays a fixed 2× per-pool baseline, Warning, 5-minute, non-tiered alert. Problem 1 is a spec-prose and inert-config reconciliation with no rule-behavior change. `rules.go`, the rendered `PrometheusRule` fragment, `docs/alerting/rules.yaml`, and the reference docs stay untouched; the docs already match §16.5.
- **v1 ships one canonical implementation per concern with no tier-dependent code path.** Promoting §16.5 to a genuinely tiered rule (Tier 3 to `critical`) would introduce a tier-dependent code path and cascade into the runbook trigger severity and the metrics-reference rows. It is recorded as an open decision rather than the recommended edit.
- **Problem 2 splits into a shippable half and a deferred half.** The shippable half merges each rule's static `Rule.Labels` into the `alert_fired` payload, which closes the missing-labels-key gap for rules that carry static labels. The deferred half (Option B) supplies matched-series labels such as `{pool:...}` and requires extending the `ExprEvaluator` contract and a production PromQL backend.
- **The static-label merge is scoped to the firing edge.** `emit` is a shared closure used by both `onFired` and `onResolved` (`emit.go:88-101`). §25:694 documents the `alert_resolved` payload as "Alert name, duration" only. The `labels` merge is applied on the `onFired` transition alone so `alert_resolved` gains no labels key.
- **The static-label merge is a forward-looking wire-contract correction.** A non-Noop `ExprEvaluator` ships and is wired into the gateway: `inproceval` is instantiated at `cmd/lenny-gateway/runserver.go:97`. The sole static-label rule, `AdmissionPlaneFeatureFlagDowngrade`, uses `unless on()` in its expression (`rules.go:246`), and both ` unless ` and ` on(` are in `inproceval`'s `unsupportedTokens` (`pkg/alerting/inproceval/inproceval.go:92`), so `inproceval` returns `ErrUnsupportedExpr` and the rule fires only under a real Prometheus backend. The merge changes the emitted payload for that one rule, exercised today by the tier-3 CloudEvents contract test that constructs an `Alert` directly. It closes none of the spec's illustrated `{pool}` examples, which need Option B.
- **The finding's own labels test stays skipped.** `TestCloudEventsAlertFiredPayloadCarriesLabels` (`cloudevents_test.go:430`) asserts `data.labels.pool`, the firing-series value the static merge cannot produce. Option A is pinned instead by a new test on `AdmissionPlaneFeatureFlagDowngrade`, which carries static labels. This corrects the problem statement's earlier claim that Option A un-skips line 430.
- **emit.go is the single shared firing path for the whole `Catalog()`**, including C-44's §25.4 self-health alerts (`rules.go` `OpsSelfHealthDegraded` and siblings). The payload change coordinates with the C-44 lane through the Integrator inbox; the queue already sequences C-36 before C-44 to keep `pkg/alerting/rules` single-worker (`PROPOSAL-QUEUE.md:426-427`).
- **Problem 3 reconciles the §25.17 example down to what ships.** The §25.17 Step-1 `alert_fired` example is corrected to the static catalog `suggestedAction` the emit path emits (endpoint template `{name}`, static reasoning, no body). The runtime-enriched remediation (pool-substituted endpoint, `body:{minWarm:15}`, live reasoning) stays only in the §25.3 diagnostic response the same walkthrough already carries in Step 3. This matches Problem 1's reconcile-down direction and avoids documenting a payload no code path produces. Adding a static `Body:{minWarm:15}` to the catalog entry is recorded as an open decision, since 15 is arbitrary and it would still not match the example's endpoint or reasoning.

## 3. The three reconciliations in one view

`WarmPoolReplenishmentSlow` is governed by §16.5 as a fixed non-tiered Warning. The §25.13 tier table and its preset excerpts are the divergent prose, and the `warmPoolReplenishmentSlow` chart value is inert config; both are corrected without touching the rule, the rendered manifest, or the reference docs (SPEC-1, CONFIG-1).

The `alert_fired` payload gains a `labels` key sourced from each rule's static `Rule.Labels` on the firing edge only (CODE-1). This makes the wire contract at §25:693 real for static-label rules while leaving `alert_resolved` minimal per §25:694. The firing-series `{pool}` label stays deferred to Option B, so `TestCloudEventsAlertFiredPayloadCarriesLabels` stays skipped and a new test pins the static-label case on `AdmissionPlaneFeatureFlagDowngrade` (TEST-1).

The §25.17 Step-1 `alert_fired` example is corrected to the static catalog `suggestedAction` (SPEC-2), so the shipped event and the spec example agree, the §25.7-versus-§25.17 question is resolved, and the runtime-enriched remediation is documented once in the §25.3 diagnostic that Step 3 already re-shows.

## 4. Edge cases and accepted failure modes

Each row names the observable outcome, the spec text that states it, and the reader-facing docs page that carries it. Rows for behavior this proposal leaves unchanged record the accepted or deferred outcome and the sentence that documents it.

| Scenario | Observable outcome | Spec text and docs page |
|:--|:--|:--|
| `WarmPoolReplenishmentSlow` firing condition | Fires as a fixed non-tiered Warning when P95 pod-startup duration exceeds 2× the pool's `podWarmupSecondsBaseline` for more than 5 minutes; identical at every tier | `spec/16_observability.md:495`; `docs/reference/metrics.md:527` |
| Operator sets a higher tier preset | No `warmPoolReplenishmentSlow` threshold knob exists; the tier preset changes only `gatewayQueueDepthHigh`, `gatewayLatencyHigh`, and `credentialPoolLow` | §25.13 Tier-Aware Defaults (SPEC-1); `docs/reference/configuration.md:129` |
| `alert_resolved` emitted for a static-label rule | Payload carries `alertName` and duration only, no `labels` key (the merge is scoped to `onFired`) | `spec/25_agent-operability.md:694`; §25.5 event stream |
| `alert_fired` emitted for `AdmissionPlaneFeatureFlagDowngrade` | Payload carries `labels:{flag_name, expected_webhook_name}` from the rule's static `Rule.Labels` | `spec/25_agent-operability.md:693` |
| `AdmissionPlaneFeatureFlagDowngrade` on the in-process fallback backend | Never fires under `inproceval` because its `unless on()` expression is `ErrUnsupportedExpr`; the static labels are observable only under a Prometheus backend or the tier-3 contract test | §25.13 in-process tracker fallback (`spec/25_agent-operability.md:4795-4799`) |
| `alert_fired` for `WarmPoolExhausted` firing-series pool identity | The `labels:{pool:...}` value stays deferred (Option B); the static merge supplies no pool label, and `TestCloudEventsAlertFiredPayloadCarriesLabels` stays skipped | `spec/25_agent-operability.md:693,5177` |
| `alert_fired` `suggestedAction` for `WarmPoolExhausted` | Carries the static catalog template (endpoint `PUT /v1/admin/pools/{name}/warm-count`, static reasoning, no `body`); the runtime-computed `body:{minWarm:15}` and pool-substituted endpoint come from the §25.3 pool diagnostic shown in §25.17 Step 3 | `spec/25_agent-operability.md:474-500,5206` (SPEC-2) |
| OpenSLO export of `WarmPoolReplenishmentSlow` | Unaffected; the rule carries an empty SLO field, so the §16.10 export emits no SLO document for it | §16.10 OpenSLO export (`pkg/alerting/rules/openslo.go`) |

## 5. Proposed changes

### SPEC-1. Reconcile the §25.13 Tier-Aware Defaults so WarmPoolReplenishmentSlow is a fixed non-tiered §16.5 Warning

**Target:** `spec/25_agent-operability.md`, the "Tier-Aware Defaults" section: the tier-dependent examples cell (`:4734`) and the `values-tier2.yaml` and `values-tier3.yaml` excerpts (`:4751-4754` and `:4767-4770`).

**Rationale:** §16.5 is authoritative, and the shipped rule and rendered chart implement its fixed 2× per-pool, Warning, non-tiered definition. The §25.13 `ratioBelow` tier-tightening is realized nowhere: `ratioBelow` appears only in `spec/25`, and the tier presets carry only `gatewayQueueDepthHigh`, `gatewayLatencyHigh`, and `credentialPoolLow`. This is a spec-prose correction with no rule-behavior change.

**Anchor 1:** In the Tier-Aware Defaults table, the tier-dependent row. Replace:

```markdown
| **Tier-dependent** | `GatewayQueueDepthHigh`, `GatewayLatencyHigh`, `WarmPoolReplenishmentSlow`, `CredentialPoolLow` | Defaults set by tier preset; tighter thresholds at higher tiers (stricter SLAs). |
```

with:

```markdown
| **Tier-dependent** | `GatewayQueueDepthHigh`, `GatewayLatencyHigh`, `CredentialPoolLow` | Defaults set by tier preset; tighter thresholds at higher tiers (stricter SLAs). |
```

**Anchor 2:** In the `values-tier2.yaml` excerpt, remove the `warmPoolReplenishmentSlow` block. Replace:

```yaml
    gatewayLatencyHigh:
      p99Seconds: 2.0
      duration: "5m"
      severity: "warning"
    warmPoolReplenishmentSlow:
      ratioBelow: 0.5      # replenishment rate < 50% of claim rate
      duration: "10m"
      severity: "warning"
```

with:

```yaml
    gatewayLatencyHigh:
      p99Seconds: 2.0
      duration: "5m"
      severity: "warning"
```

**Anchor 3:** In the `values-tier3.yaml` excerpt, remove the `warmPoolReplenishmentSlow` block. Replace:

```yaml
    gatewayLatencyHigh:
      p99Seconds: 1.0
      duration: "2m"
      severity: "warning"
    warmPoolReplenishmentSlow:
      ratioBelow: 0.7
      duration: "5m"
      severity: "critical"
```

with:

```yaml
    gatewayLatencyHigh:
      p99Seconds: 1.0
      duration: "2m"
      severity: "warning"
```

**Anchor 4:** After the `values-tier3.yaml` excerpt fence (before "Universal thresholds are also exposed…"), add one sentence naming where `WarmPoolReplenishmentSlow` is owned:

```markdown
`WarmPoolReplenishmentSlow` is a fixed, non-tiered Warning owned by Section 16.5: it fires when the P95 of `lenny_warmpool_pod_startup_duration_seconds` exceeds 2× the pool's `scalingPolicy.podWarmupSecondsBaseline` for more than 5 minutes. It carries no tier-dependent threshold and no tier-preset override.
```

`rules.go`, the rendered `PrometheusRule` fragment, `docs/alerting/rules.yaml`, and the reference docs are left untouched; they already reflect §16.5.

### CONFIG-1. Delete the inert warmPoolReplenishmentSlow chart-threshold value

**Target:** `charts/lenny/values.yaml`, the `warmPoolReplenishmentSlow` block in `monitoring.alertThresholds` (`:972-979`).

**Rationale:** `warmPoolReplenishmentSlow.multiplierOverPodWarmupBaseline: 2` is the sole grep hit for that key across `charts/`, `pkg/`, and `cmd/`; nothing consumes it. The rule's `2` multiplier is hardcoded in the Go expression (`rules.go:1093`), and the rendered chart fragment derives from the Go `Catalog()` rather than from `values.yaml`. The block's comment claims "tier presets tighten N" though no preset carries the key, and no `values.schema.json` entry references it.

**Anchor:** Remove the comment lines and the `warmPoolReplenishmentSlow` key with its children. Delete:

```yaml
    # WarmPoolReplenishmentSlow: §16.5 line 488 — the multiplier
    # applied to per-pool lenny_pool_warmup_seconds_baseline before
    # the catalogue's `> N * baseline` comparison. The implementation
    # uses pod-startup p95 vs N × baseline; tier presets tighten N.
    warmPoolReplenishmentSlow:
      multiplierOverPodWarmupBaseline: 2
      duration: 5m
      severity: warning
```

This leaves `gatewayLatencyHigh` above and `credentialPoolLow` below. No chart regeneration is needed, since the field feeds no template. Implementation confirms `helm template` still renders and the tier-preset chart tests stay green.

### CODE-1. Merge each rule's static Rule.Labels into the alert_fired payload on the firing edge

**Target:** `pkg/alerting/evaluator/emit.go`, the shared `emit` closure and the `onFired` hook (`:51-96`).

**Rationale:** §25:693 documents `labels` as an `alert_fired` payload field, but the emit closure never sets a `labels` key, so even a rule with static labels (`AdmissionPlaneFeatureFlagDowngrade`'s `flag_name` and `expected_webhook_name`) emits none. Merging the static `Rule.Labels` closes the backend-independent half of the wire-contract gap. The merge is scoped to `onFired` so `alert_resolved` stays "Alert name, duration" per §25:694. The `alert_fired` CloudEvents payload is a distinct surface from the rendered `PrometheusRule` labels, which `Rule.Labels` documents as merged "at render time", so no existing surface covers it.

This is a forward-looking correction. `inproceval` ships and is wired at `runserver.go:97`, but the sole static-label rule uses `unless on()`, which `inproceval` rejects as `ErrUnsupportedExpr` (`inproceval.go:92`), so the rule fires only under a real Prometheus backend. The change is exercised today by the tier-3 contract test that constructs an `Alert` directly. It closes none of the spec's illustrated `{pool}` examples, which require Option B.

**Change (staged description).**

1. Give the `emit` closure a `labels map[string]string` parameter, and set `payload["labels"] = labels` only when `len(labels) > 0`, so a rule with no static labels emits no key:

```go
emit := func(a Alert, eventType events.EventType, severity string, labels map[string]string) {
	payload := map[string]any{
		"ruleName":  a.Rule.Name,
		"alertName": a.Rule.Name,
		"severity":  string(a.Rule.Severity),
		"summary":   a.Rule.Summary,
		"sinceUnix": a.Since.Unix(),
	}
	// spec: §25 Event Types (alert_fired lists `labels` among its payload
	// highlights). The static per-rule Rule.Labels ride the firing edge
	// only; alert_resolved stays "Alert name, duration" (§25 line 694). The
	// firing-series labels (e.g. {pool: ...}) are a deferred Option B that
	// needs a matched-series ExprEvaluator and a Prometheus backend.
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	// ... existing runbook / runbookUrl / suggestedAction handling unchanged ...
}
```

2. In `onFired`, pass the rule's static labels: `emit(a, events.EventAlertFired, sev, a.Rule.Labels)`.
3. In `onResolved`, pass `nil`: `emit(a, events.EventAlertResolved, "info", nil)`.

Run `gofumpt` and `goimports`.

### SPEC-2. Reconcile the §25.17 Step-1 alert_fired example to the shipped static suggestedAction

**Target:** `spec/25_agent-operability.md`, the §25.17 Step-1 `alert_fired` SSE example (`:5177`) and one clarifying sentence after it (near `:5178`), plus a one-sentence clarification near the §25.7 Path B example (`:3242`).

**Rationale:** The shipped `alert_fired` path copies the static catalog `SuggestedAction` verbatim (`emit.go:71-72`), and `WarmPoolExhausted`'s catalog entry has an unsubstituted `{name}` endpoint, a static reasoning, and no `Body` (`rules.go:297-302`). The §25.17 example instead shows a runtime-enriched instance (pool-substituted endpoint, `body:{minWarm:15}`, live reasoning), a payload no code path produces. The authoritative runtime-enriched form already lives in the §25.3 diagnostic contract (`:474-500`) and the same §25.17 walkthrough re-shows it in Step 3 (`:5206`). Reconciling Step 1 down to the static form matches Problem 1's reconcile-down direction, resolves the §25.7-versus-§25.17 question of whether `alert_fired` carries `suggestedAction`, and keeps the runtime enrichment documented once in the diagnostic response.

**Anchor 1:** Replace the Step-1 `data:` line. Replace:

```
data: {"specversion":"1.0","id":"01HN7Y0QW6S7X9ZP8M2F5K4R3B","source":"//lenny.dev/gateway/gw-7f4c2a1e","type":"dev.lenny.alert_fired","time":"2026-04-17T14:32:08Z","datacontenttype":"application/json","data":{"severity":"critical","alertName":"WarmPoolExhausted","labels":{"pool":"default-gvisor"},"runbook":"warm-pool-exhaustion","suggestedAction":{"action":"SCALE_WARM_POOL","endpoint":"PUT /v1/admin/pools/default-gvisor/warm-count","body":{"minWarm":15},"reasoning":"Pool exhausted for 3 minutes. Peak claim rate: 4.2/min."}}}
```

with:

```
data: {"specversion":"1.0","id":"01HN7Y0QW6S7X9ZP8M2F5K4R3B","source":"//lenny.dev/gateway/gw-7f4c2a1e","type":"dev.lenny.alert_fired","time":"2026-04-17T14:32:08Z","datacontenttype":"application/json","data":{"severity":"critical","alertName":"WarmPoolExhausted","labels":{"pool":"default-gvisor"},"runbook":"warm-pool-exhaustion","suggestedAction":{"action":"SCALE_WARM_POOL","endpoint":"PUT /v1/admin/pools/{name}/warm-count","reasoning":"Warm pool is exhausted; raise the warm-pod floor so new sessions stop blocking on pod claim.","runbook":"warm-pool-exhaustion"}}}
```

**Anchor 2:** After the Step-1 example fence, add a sentence tying the concrete remediation to the diagnostic:

```markdown
The `suggestedAction` on the `alert_fired` payload is the alert's static remediation template: the `endpoint` carries the `{name}` placeholder and there is no `body`, because the concrete warm-count and the pool-substituted endpoint depend on the pool's live claim rate. The runtime-computed remediation (the substituted endpoint, `body.minWarm`, and a live-rate `reasoning`) is produced by the Section 25.3 pool diagnostic and returned in the Step 3 diagnostic response below.
```

**Anchor 3:** Near the §25.7 Path B `alert_fired` example (before its JSON fence at `:3244`), add a sentence resolving the whether-`alert_fired`-carries-`suggestedAction` question:

```markdown
The `alert_fired` payload also carries the alert's static `suggestedAction` template (shown in full in the Section 25.17 Step 1 example); it is elided here to focus on `runbook` routing.
```

Leave the §25.7 example's `{severity, alertName, labels, runbook}` body unchanged.

### TEST-1. Pin static-label emission at tier-3 and correct the two skipped cloudevents-test attributions

**Target:** `tests/tier3_contract/cloudevents/cloudevents_test.go`.

**Rationale:** Option A emits static labels, which must be pinned by a rule that carries them; `WarmPoolExhausted` carries none. The finding's own labels test (`:430`) asserts the firing-series pool value, which Option A cannot supply, so it stays skipped pending Option B.

**Change (staged description).**

1. Add a tier-3 test asserting that an `AdmissionPlaneFeatureFlagDowngrade` `alert_fired` payload's `data.labels` carries `flag_name` and `expected_webhook_name` from the rule's static `Rule.Labels`, and that an `alert_resolved` payload for the same rule carries no `labels` key. Build the `Alert` from the catalog rule directly, matching the existing tests' `evaluator.New([]rules.Rule{rule}, alwaysActiveExpr{}, ...)` construction. Carry `// spec: 25.5 (Event Types — alert_fired labels; alert_resolved name+duration)` and a `// diagnosis:` line.
2. Leave `TestCloudEventsAlertFiredPayloadCarriesLabels` (`:430`) skipped, and update its skip reason to state that the `labels` key now exists on the wire for static-label rules and only the matched-series pool value is deferred (Option B, blocked on a production PromQL backend).
3. Leave `TestCloudEventsAlertFiredPayloadSuggestedActionBody` (`:354`) skipped, and update its skip reason to point at SPEC-2: the `alert_fired` `suggestedAction` is the static template with no `body`, and the runtime-computed `body:{minWarm:15}` is documented in the §25.3 diagnostic response.
4. Run tier 3 to confirm the `labels` addition breaks no existing payload-field assertion in the suite.

## 6. Non-goals

- Changing the `WarmPoolReplenishmentSlow` rule behavior. Its `Expr`, `For`, and `Severity` in `rules.go`, the rendered `PrometheusRule` fragment (`charts/lenny/files/alerting-rules.yaml`), and `docs/alerting/rules.yaml` stay byte-identical on the recommended path.
- Promoting §16.5 to a genuinely tiered rule or introducing any tier-dependent code path. This contradicts the v1 single-canonical-implementation principle and is surfaced as an open decision only.
- Option B matched-series labels: extending `ExprEvaluator.Active` to return matched series and labels and adding an `Alert.SeriesLabels` field. Blocked until a production PromQL backend exists; the firing-series pool value stays deferred and its test (`cloudevents_test.go:430`) stays skipped.
- Editing `docs/reference/metrics.md:527`, `docs/reference/configuration.md:129`, or `docs/runbooks/warm-pool-exhaustion.md`. They already describe the §16.5 form and are reached only by the promote-to-tiered alternative.
- Regenerating the alerting-rules chart fragment or `docs/alerting/rules.yaml` via `make generate` or `cmd/gen-alerting-rules`. The recommended path does not change `rules.go` `Catalog()`, so there is nothing to regenerate. Only the SPEC-2 add-`Body` alternative would touch `rules.go`.
- Any OpenSLO or C-19 (§16.10) work. `WarmPoolReplenishmentSlow` has an empty SLO field (`pkg/alerting/rules/openslo.go`), so the export is unaffected.
- Implementing the C-44 §25.4 self-health event reconciliation. This proposal only coordinates the shared `emit.go` payload change with that lane.
- Adding a static `SuggestedAction.Body` to the `WarmPoolExhausted` catalog entry and un-skipping `cloudevents_test.go:354`. This is recorded as an open decision, since 15 is arbitrary and it would still not match the example's pool-substituted endpoint or runtime reasoning.

## 7. Testing

The change reaches tier 0 (static: `gofumpt`, `goimports`, `golangci-lint`, and the schema and codegen checks), tier 1 (the `emit` closure's on-fired label merge and the on-resolved scoping, in-process with the catalog rules), and tier 3 (the CloudEvents wire-contract assertions in `tests/tier3_contract/cloudevents`) per `.claude/rules/test-coverage.md`. SPEC-1 and CONFIG-1 are spec-prose and inert-config edits with no runtime surface; CONFIG-1 is exercised by the existing chart-render regression. Each test below covers a non-happy path and carries a `// spec:` tie; the tier-3 CloudEvents tests already carry `// diagnosis:` comments.

- **tier-1 alert_resolved carries no labels key (spec-named-failure, boundary):** In `pkg/alerting/evaluator`, drive an `AdmissionPlaneFeatureFlagDowngrade` alert through firing then resolution with a fake emitter, and assert the `alert_resolved` payload has no `labels` key while the `alert_fired` payload does. The non-happy path is the resolution edge that must stay minimal per §25:694. `// spec: §25 Event Types (alert_resolved is name+duration; alert_fired carries labels)`.
- **tier-1 static-label merge is absent for a no-label rule (boundary):** Assert that a rule with an empty `Rule.Labels` (for example `WarmPoolExhausted`) emits an `alert_fired` payload with no `labels` key, so the merge adds a key only when static labels exist. The non-happy path is the empty-labels rule that must not gain an empty object. `// spec: §25 Event Types (alert_fired labels)`.
- **tier-3 AdmissionPlaneFeatureFlagDowngrade alert_fired carries static labels (spec-named-failure):** In `tests/tier3_contract/cloudevents`, assert the emitted `data.labels` carries `flag_name` and `expected_webhook_name` from the rule's static `Rule.Labels`, and that the same rule's `alert_resolved` carries no `labels` key. The non-happy path is the static-label rule whose payload the pre-change emit omitted entirely. `// spec: 25.5 (Event Types — alert_fired labels; alert_resolved name+duration)`.
- **tier-3 regression: existing payload-field assertions survive the labels addition:** Run the existing CloudEvents contract suite to confirm the added `labels` key breaks no `ruleName`, `severity`, `runbook`, or `suggestedAction` assertion. The two skipped tests (`:354`, `:430`) stay skipped with corrected reasons. `// spec: 25.5 (alert_fired payload contract)`.

## 8. Findings closed on application

- **T-25.13.9** (`TEST-GAPS.md`, Medium, OPEN): "WarmPoolReplenishmentSlow tier-dependent threshold is neither tier-overridden nor plumbed through the Helm chart, contradicting the §25.13 tier table." Its "Needs human input" note asks which section is authoritative. SPEC-1 and CONFIG-1 answer §16.5 and remove the §25.13 tier-tightening prose and the inert chart value, resolving the spec-versus-chart inconsistency. On application the finding is closed, and its suggested tier-preset helm-unittest becomes moot because no tier-dependent `warmPoolReplenishmentSlow` threshold exists.
- **T-25.17.4** (Medium): the `alert_fired` payload's missing `labels` field. CODE-1 and TEST-1 close the shippable static-label half; the deferred matched-series half stays recorded as Option B with `cloudevents_test.go:430` left skipped.
- **T-25.17.5** (Low): the §25.17 `alert_fired` `suggestedAction` divergence. SPEC-2 and TEST-1 reconcile the example to the shipped static template and update the skip reason on `cloudevents_test.go:354`.

## 9. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions carried in the draft narrowed the proposal from its original form:

- **CODE-1 was scoped to the firing edge and its premise corrected.** The original sketch merged the static labels in the shared `emit` closure, which both `onFired` and `onResolved` call, so `alert_resolved` would have gained a `labels` key against §25:694. The merge is now applied on `onFired` only. The original justification asserted "only `NoopExprEvaluator` ships" and that the firing path is effectively dead. That is false: `inproceval` ships and is wired at `runserver.go:97`. The accurate constraint is narrower and is now stated: the sole static-label rule, `AdmissionPlaneFeatureFlagDowngrade`, uses `unless on()`, which `inproceval` rejects as `ErrUnsupportedExpr`, so it fires only under a real Prometheus backend, and CODE-1 is exercised today by the tier-3 contract test.
- **TEST-1's attribution was corrected.** The original problem statement claimed Option A un-skips `cloudevents_test.go:430`. That test asserts the firing-series `data.labels.pool`, which the static merge cannot supply, so it stays skipped pending Option B. Option A is pinned instead by a new test on `AdmissionPlaneFeatureFlagDowngrade`, which carries static labels.
- **SPEC-2 was redirected from aspirational prose to a reconcile-down edit.** The original sketch kept the runtime-enriched `suggestedAction` in the §25.17 Step-1 `alert_fired` example and added a sentence declaring its values are "populated by the runtime once a production PromQL backend is present", which documents a payload no code path produces. The example is now reconciled to the static catalog `suggestedAction` the emit path emits, the runtime-enriched remediation is left in the §25.3 diagnostic that §25.17 Step 3 already carries, and a one-sentence clarification near the §25.7 Path B example resolves the whether-`alert_fired`-carries-`suggestedAction` question the original sketch left open.

## 10. Open decisions for review

- **Problem 1 direction.** Recommended: reconcile §25.13 down to §16.5, a no-behavior-change spec and inert-config correction because the code and chart already ship the fixed non-tiered Warning. Alternative: promote §16.5 to a genuinely tier-dependent rule (Tier 3 to `critical`), a design change that cascades into the runbook trigger severity and the metrics-reference rows and requires a tier-dependent code path the v1 no-tier-paths principle rules out by default.
- **Problem 2 scope.** Recommended: ship the static-label merge now and defer Option B matched-series labels. Alternative: build the matched-series contract now by extending `ExprEvaluator.Active` to return matched series and labels and adding `Alert.SeriesLabels`, which cannot be exercised until a production PromQL backend replaces the fallback path.
- **Problem 3 direction.** Recommended: reconcile the §25.17 Step-1 example to the shipped static `suggestedAction` (SPEC-2). Alternative: add a static template `SuggestedAction.Body:{minWarm:15}` to the `WarmPoolExhausted` catalog entry and un-skip `cloudevents_test.go:354`, a hardcoded value that still would not match the example's pool-substituted endpoint or runtime reasoning.

## 11. Files touched on application

- `spec/25_agent-operability.md`: SPEC-1 (the Tier-Aware Defaults tier-dependent row, the two preset excerpts, and the added ownership sentence) and SPEC-2 (the §25.17 Step-1 example, its clarifying sentence, and the §25.7 Path B clarification).
- `charts/lenny/values.yaml`: CONFIG-1 (delete the inert `warmPoolReplenishmentSlow` threshold block).
- `pkg/alerting/evaluator/emit.go`: CODE-1 (the `emit` closure `labels` parameter and the `onFired` / `onResolved` call sites).
- `tests/tier3_contract/cloudevents/cloudevents_test.go`: TEST-1 (the new static-label tier-3 test and the two corrected skip reasons).
- `pkg/alerting/evaluator/*_test.go`: TEST-1 tier-1 coverage (the on-resolved-no-labels and no-label-rule tests).
