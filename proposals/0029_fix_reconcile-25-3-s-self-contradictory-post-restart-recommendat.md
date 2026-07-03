# Proposal: Reconcile §25.3's self-contradictory post-restart recommendations contract: surface the empty-window / data-starved state through the canonical §25.4 degradation envelope (level: degraded) rather than per-item entries, and align the gateway recommendations service and its tests to emit that signal (closes F-25.3.16)

- **Status:** Draft for review.
- **Date:** 2026-07-03.
- **Scope:** Reconciles §25.3's internally contradictory account of what `GET /v1/admin/recommendations` returns when the in-memory sliding windows are empty (the post-restart case) to a single coherent behavior, and aligns the gateway recommendations service and its tests to emit it. The finding is F-25.3.16 (`BUILD-GAPS.md:42417`, Medium, OPEN, DEFERRED on the spec contradiction). §25.3 line 586 prescribes a per-item degraded entry (`"confidence": 0.0` and `"dataAvailable": false`) while §25.3 line 605 prescribes no entry, for the identical post-restart trigger; three further passages (`spec/25_agent-operability.md:1432`, `:1470`, `:4989`) assume a surfaced post-restart signal. The resolution keeps `recommendations[]` to triggered, actionable entries only and surfaces the empty-window state through the response-level §25.4 degradation envelope (`level: degraded`), which the response already carries and already stamps. It rewrites the two contradictory §25.3 passages, widens the §25.4 envelope trigger to admit an endpoint whose data quality depends on sufficient in-process history, reconciles the three cross-references, flips one branch in `pkg/gateway/operability/recommendations/service.go`, and updates two tests plus adds one. It adds no new endpoint, RPC, wire field, migration, or store method, and it touches only `spec/25_agent-operability.md`, `pkg/gateway/operability/recommendations/service.go`, `pkg/gateway/operability/recommendations/service_test.go`, and `BUILD-GAPS.md`.

This document stages the proposed spec and code changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

F-25.3.16 (`BUILD-GAPS.md:42417`, "`/v1/admin/recommendations` does not surface the `degradation` / `dataAvailable` post-restart contract", Medium, OPEN) is DEFERRED with the recorded blocker that "the spec is internally contradictory here and resolving it would require a spec change." The contradiction is real and confirmed against the tree, and it is the reason the finding could not be implemented in the build loop.

### 1.1 The §25.3 self-contradiction (line 586 versus line 605)

§25.3 gives two opposite prescriptions for the identical post-restart empty-window state:

- `spec/25_agent-operability.md:586` (the Sliding window aggregation paragraph): "After a gateway restart, windows are empty and recommendations include `\"confidence\": 0.0` and `\"dataAvailable\": false`." `dataAvailable` exists only as a per-item field on `Recommendation` (`service.go:137`, `pkg/gateway/externalapi/openapi/openapi.json:2869`), so this can only mean an emitted per-item entry.
- `spec/25_agent-operability.md:605` (the Degradation subsection): "If metrics are stale (gateway recently restarted): recommendations include `\"confidence\": 0.0`. No recommendations are generated for categories with insufficient data." This forbids the entry that line 586 requires.

Both passages name the same trigger (a gateway restart), so "empty windows" and "insufficient data" are the same state with opposite prescribed outcomes. Line 586 requires an emitted entry; line 605 forbids it. The two cannot both hold for the metric-absent case.

### 1.2 The three cross-references that assume a surfaced post-restart signal

Three further passages promise a surfaced post-restart signal, phrased as a per-response `confidence: 0.0`:

- `spec/25_agent-operability.md:1432` (§25.4 Prometheus Requirement, capacity-recommendations bullet): "Without Prometheus, recommendations return `confidence: 0.0` for hours after any restart and are based on partial samples between restarts."
- `spec/25_agent-operability.md:1470` (§25.4 Operational consequences summary table, Capacity recommendations row, permanent-absence column): "`confidence: 0.0` after every restart; ring buffers only have ~1/N replica's worth of recent data".
- `spec/25_agent-operability.md:4989` (§25.15 Failure Mode Analysis table, Prometheus permanently absent row): "capacity recommendations return `confidence: 0.0` after every restart".

These three are mutually consistent with each other and with line 586; line 605 is the lone dissenter. They presuppose a surfaced per-response signal.

### 1.3 The code implements the no-signal reading and surfaces nothing

The live gateway service implements the line-605 no-entry reading and surfaces no post-restart signal at all:

- `service.go:285-287` drops every non-triggered evaluation (`if !e.Triggered { continue }`).
- Each metric-absent evaluator returns a zero-value `Evaluation{}` (Triggered false, DataAvailable false, at `service.go:371,389,406,418,432,444`), and the empty `WindowStore` returns `ok=false` for every series, so after a restart every rule is non-triggered and dropped.
- `service.go:307-310` hard-stamps the response `Degradation` envelope with `Level: healthy` / `ThresholdSource: compiled-in-defaults` unconditionally.

A post-restart caller therefore receives `{"recommendations": [], "degradation": {"level":"healthy",...}}`, byte-identical to a genuinely healthy platform whose data is present but below threshold (both paths produce non-triggered evaluations dropped at 285-287). The "recommendations ran but had no data" signal that §25.3 and F-25.3.16 require is lost: an agent cannot distinguish "no warm-pool issue" from "the warm-pool rule could not evaluate because the ring buffers are empty".

`TestGetRecommendationsEmptyWhenNoData` (`service_test.go:14-24`) pins the no-entry reading (`len(resp.Recommendations) == 0`), and `TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848` (`service_test.go:155-173`) pins the unconditional `Level: healthy` on an empty store.

### 1.4 The response types already carry the fields to express the signal

The response already carries the §25.4 canonical envelope (`RecommendationsResponse.Degradation`, `service.go:152`) and already stamps it (`service.go:307-310`). `conventions.Degradation` carries `Level`, `Confidence`, `Warnings`, and `Since` (`conventions.go:133-148`), and the `DegradationDegraded = "degraded"` constant already exists (`conventions.go:106`). The reconciliation reuses this envelope; it introduces no new wire field.

## 2. Decisions

- **Resolve the contradiction in favor of the response-level §25.4 degradation envelope (Option A), rather than per-item degraded entries in `recommendations[]` (Option B).** §25.4 (`spec/25_agent-operability.md:203-224`) defines the envelope as "the canonical response-level signal for every endpoint whose data quality depends on external dependency availability" and carries `confidence` ("how much the agent should trust it") and `warnings`. The recommendations endpoint's data quality depends on the ring buffers being populated, which is exactly this case. The response already carries and stamps the envelope, so the fix flips its `level` to `degraded` when the windows are empty rather than inventing a new surface. Option A is chosen on design merits (envelope reuse, unchanged `recommendations[]` semantics, a single code path), not on a serialization constraint.
- **The post-restart condition is response-wide, not per-rule.** A gateway restart empties every ring buffer at once (`WindowStore` holds no persisted state; `NewWindowStore` returns an empty store, `metricreader.go:53`). Per-rule granularity therefore adds nothing over a response-level signal for the case the spec describes. The envelope goes `degraded` only when no evaluated rule reported data (every window empty); when at least one rule has data the envelope stays `healthy` and a starved rule simply produces no entry, which keeps a partially-warmed steady state from reading as degraded.
- **Keep `recommendations[]` to triggered, actionable entries only.** Option B (emitting a per-item entry for every data-starved rule) would overturn the committed, deliberate `TestGetRecommendationsEmptyWhenNoData` (`service_test.go:14-24`), change the array's wire semantics from "actions to take" to "includes non-actionable starved entries" (forcing every agent to filter on `dataAvailable`), and duplicate the signal the §25.4 envelope already expresses. Option A preserves the `len == 0` assertion and the array's meaning.
- **The machine-readable post-restart signal is `degradation.level == "degraded"` plus a warning, not a literal `confidence: 0.0` on the envelope.** On the envelope, `Degradation.Confidence` carries `json:"confidence,omitempty"` (`conventions.go:138`), so a literal `0.0` on the envelope would be dropped from the wire; the level plus a `warnings` entry is the honest envelope signal. Option B's per-item field is a different matter: `Recommendation.Confidence` is `json:"confidence"` with no `omitempty` (`service.go:134`), so a per-item `confidence: 0.0` would serialize verbatim. The `omitempty` constraint is therefore a consequence of choosing the envelope route (A), not a reason to prefer A over B. Under Option A the spec text is rewritten to describe `level: degraded` rather than a literal per-item `confidence: 0.0`.
- **Widen the §25.4 envelope trigger to admit insufficient in-process history.** The envelope trigger at `spec/25_agent-operability.md:205` is scoped to "Any response whose data quality depends on the availability of an external dependency," and line 233 says endpoints "serving from their primary source ... return `\"level\": \"healthy\"`." For the gateway, the ring buffers are in-process, so an empty post-restart buffer is an internal warm-up state, and the gateway is serving from its primary source. To keep the gateway's degraded stamp from contradicting lines 205 and 233, the envelope trigger and the omitted-when-healthy clause are widened to include insufficient in-process history (the ring buffers still refilling), so the reconciliation extends the one canonical envelope rather than conflicting with its stated scope.
- **F-25.3.16 is narrowed to the all-empty (post-restart) case; the per-rule partial-starvation ambiguity remains and the reviewer ratifies that narrowing.** The finding frames the defect per-rule ("cannot distinguish 'no warm-pool issue' from 'warm-pool rule cannot evaluate because ring buffer is empty'"). Option A closes the wholesale post-restart case (every window empty). A partially-warmed gateway where some source metrics are absent while others have data still reads `healthy` with the starved rule silently producing no entry. Resolving that per-rule case is a distinct, wider concern (see Non-goals and Open decisions).
- **Detect the empty-window state by aggregating the per-rule `Evaluation.DataAvailable` the evaluators already return.** The `WindowStore` exposes no store-level emptiness method, but the service already reads `e.DataAvailable` (`service.go:298`); reading it before the `if !e.Triggered { continue }` drop lets the service track whether any evaluated rule had data, reusing an existing signal with no new interface method.
- **This is a reconciliation, not a new capability.** The envelope field, the `DegradationDegraded` constant, and the stamping site all ship. The change flips one branch, rewrites the contradictory spec prose, widens the §25.4 envelope-scope wording, reconciles three cross-references, and updates two tests plus adds one. No new endpoint, RPC, field, migration, or store method.
- **The wired evaluation path is the gateway's in-process evaluator.** The gateway constructs the `CapacityService` against its in-process `WindowStore` reader (`cmd/lenny-gateway/adminrouter.go:306`), and the fix lands there with no tier-dependent split. `lenny-ops` compiles the same shared rule package (`pkg/recommendations/rules`) into a Prometheus-backed evaluator, but that path is not yet wired into a production `CapacityService`: `metrics.NewReader` is constructed only in tests, and the `PrometheusClient` built in `cmd/lenny-ops/httpsurface.go` is discarded pending the aggregator consumer. When `lenny-ops`'s aggregation is wired, it inherits the same envelope logic through the shared package, so no per-binary code split is introduced.

## 3. Design constraints and what already ships

The §25.4 canonical degradation envelope is the surface every option lives inside. The `RecommendationsResponse` already carries it (`service.go:140-153`), and `GetRecommendations` already constructs and stamps it on every response (`service.go:307-310`). The envelope's `Level` field already admits `degraded` (`conventions.go:104-108`). The evaluators already report per-rule data presence: each metric-present branch sets `DataAvailable: true` and each metric-absent branch returns a zero-value `Evaluation{}` with `DataAvailable` false (`service.go:368-453`), and the loop already reads `e.DataAvailable` when it emits a triggered entry (`service.go:298`).

The reconciliation stays inside this envelope. It resolves the §25.3 contradiction to the response-level reading and widens the §25.4 envelope-scope wording so the gateway's warm-up degradation fits the stated trigger (C1), reconciles the three cross-references that describe the deleted `confidence: 0.0` reading (C2), flips the gateway service's unconditional healthy stamp to a conditional degraded stamp driven by the aggregated per-rule `DataAvailable` (C3), and updates the two committed tests plus adds a contrast test (C4). No new store method, wire field, or endpoint is introduced.

## 4. Proposed changes

### C1. Reconcile the §25.3 self-contradiction (line 586 versus line 605) and widen the §25.4 envelope trigger to admit insufficient in-process history

**Target:** `spec/25_agent-operability.md`, §25.3 Sliding window aggregation paragraph (line 586) and the §25.3 Degradation subsection (line 605); and §25.4 Canonical Degradation Envelope, the trigger sentence (line 205) and the "Omitted when healthy" clause (line 233). Reference (aligned after C3, no change): `pkg/gateway/operability/recommendations/service.go`.

Lines 586 and 605 prescribe opposite outcomes (a per-item degraded entry versus no entry) for the identical post-restart empty-window state, and `dataAvailable` exists only as a per-item field, so line 586 cannot be satisfied without an emitted entry that line 605 forbids. Both are rewritten to the single coherent behavior: no per-category entries, and a response-level `degradation` envelope reporting `level: degraded`. The §25.4 trigger and omitted-when-healthy wording are widened so the gateway's in-process warm-up degradation fits the envelope's stated scope, since the empty ring buffers are an internal history-accumulation state rather than an external-dependency outage.

**Anchor and change (§25.3 Sliding window aggregation, line 586):** Replace

```markdown
**Sliding window aggregation.** The rules engine maintains in-memory ring buffers per metric (no Postgres, no Redis). Window sizes are configurable per rule (default: 24h for pool sizing, 7d for credential sizing). After a gateway restart, windows are empty and recommendations include `"confidence": 0.0` and `"dataAvailable": false`.
```

with

```markdown
**Sliding window aggregation.** The rules engine maintains in-memory ring buffers per metric (no Postgres, no Redis). Window sizes are configurable per rule (default: 24h for pool sizing, 7d for credential sizing). After a gateway restart the ring buffers are empty. Until they refill, no per-category recommendations are generated and the response's `degradation` envelope reports `"level": "degraded"` with a warning that the windows are still filling (see **Degradation** below).
```

**Anchor and change (§25.3 Degradation subsection, line 605):** Replace

```markdown
If metrics are stale (gateway recently restarted): recommendations include `"confidence": 0.0`. No recommendations are generated for categories with insufficient data.
```

with

```markdown
When no evaluated rule has samples (for example shortly after a gateway restart, when every ring buffer is empty), no per-category recommendations are generated and the response's canonical [§25.4](#canonical-degradation-envelope) `degradation` envelope reports `"level": "degraded"` (with `"thresholdSource": "compiled-in-defaults"` and a warning that the windows are still filling). An agent therefore distinguishes an empty `recommendations` array caused by starved windows from one caused by a healthy platform with no capacity issues. When at least one rule has samples the envelope reports `"level": "healthy"` and a rule whose own metric is still absent simply produces no entry; the response-level envelope signals the wholesale-empty (post-restart) case rather than per-rule starvation. The `recommendations` array always carries triggered, actionable entries only.
```

**Anchor and change (§25.4 envelope trigger, line 205):** Replace

```markdown
Any response whose data quality depends on the availability of an external dependency includes a top-level `degradation` object with a uniform schema:
```

with

```markdown
Any response whose data quality depends on the availability of an external dependency, or on sufficient in-process history having accumulated (for example the capacity-recommendation ring buffers, which are empty after a gateway restart until they refill — Section 25.3), includes a top-level `degradation` object with a uniform schema:
```

**Anchor and change (§25.4 "Omitted when healthy" clause, line 233):** Replace

```markdown
**Omitted when healthy.** Endpoints serving from their primary source omit `degradation` entirely (or return `"level": "healthy"` with no other fields set). Agents should treat an absent envelope as equivalent to healthy.
```

with

```markdown
**Omitted when healthy.** Endpoints serving from their primary source omit `degradation` entirely (or return `"level": "healthy"` with no other fields set), except that an endpoint whose data quality also depends on sufficient in-process history reports `"level": "degraded"` while that history is still accumulating (the capacity-recommendation ring buffers after a gateway restart, Section 25.3). Agents should treat an absent envelope as equivalent to healthy.
```

**Rationale:** The contradiction is confirmed by the finding itself (`BUILD-GAPS.md:42417`), which flags it as needing a spec change. `dataAvailable` exists only as a per-item field (`service.go:137`, `Evaluation.DataAvailable` at `service.go:163`) with no response-level equivalent, so line 586 can only mean an emitted entry that line 605 forbids. The code implements the no-entry reading (`service.go:285-287`, `307-310`). Both passages are rewritten to the response-level envelope reading so they agree with each other and with the code after C3. The §25.4 trigger and omitted-when-healthy wording are widened because the gateway serves recommendations from its in-process ring buffers (its primary source), so an empty post-restart buffer is an internal warm-up state rather than an external-dependency outage; without the widening the gateway's `level: degraded` stamp would contradict lines 205 and 233. The line-605 prose states explicitly that the envelope signals the wholesale-empty case and that per-rule starvation on a partially-warmed gateway is not surfaced per-rule, so the reviewer ratifies narrowing F-25.3.16 to the all-empty case.

### C2. Reconcile the three §25.4/§25.15 cross-references that describe the deleted `confidence: 0.0` reading

**Target:** `spec/25_agent-operability.md`, §25.4 Prometheus Requirement capacity-recommendations bullet (line 1432), the §25.4 Operational consequences summary table Capacity recommendations row (line 1470), and the §25.15 Failure Mode Analysis Prometheus-permanently-absent row (line 4989). Reference (aligned, no change): the §25.15 Prometheus-transiently-down row (line 4988), which specifies the fan-out "highest-confidence merge".

All three passages say recommendations "return `confidence: 0.0` after every restart", phrasing that presupposes a surfaced per-response `confidence` value. Under Option A the surfaced signal is the response-level `degradation` envelope at `level: degraded` (the envelope's `confidence` field is `omitempty`, so a literal `0.0` there is dropped, per §2). The three are edited in lockstep so the whole spec describes one mechanism. Each passage already frames the behavior as what the recommendations response returns, so the edits describe the response contract served by the wired gateway in-process evaluator; they do not assert that a wired `lenny-ops` `CapacityService` returns a degraded response, because that path is not yet connected (§2, last decision).

**Anchor and change (§25.4 capacity-recommendations bullet, line 1432):** Replace

```markdown
- **Capacity recommendations.** Many rules use multi-day sliding windows ("pool exhausted 3+ times in 24h", "credential utilization > 70% over 7d"). These require historical data. Per-replica in-memory ring buffers reset on every gateway restart and capture only ~1/N of total traffic per replica. Without Prometheus, recommendations return `confidence: 0.0` for hours after any restart and are based on partial samples between restarts.
```

with

```markdown
- **Capacity recommendations.** Many rules use multi-day sliding windows ("pool exhausted 3+ times in 24h", "credential utilization > 70% over 7d"). These require historical data. Per-replica in-memory ring buffers reset on every gateway restart and capture only ~1/N of total traffic per replica. Without Prometheus, the recommendations response reports a `degradation` envelope with `"level": "degraded"` for hours after any restart while the ring buffers refill, and the recommendations that do generate afterward are based on partial samples between restarts.
```

**Anchor and change (§25.4 Operational consequences summary table, Capacity recommendations row, line 1470):** Replace

```markdown
| Capacity recommendations | Aggregate metrics from PromQL, full window data | Fan-out, highest-confidence merge | `confidence: 0.0` after every restart; ring buffers only have ~1/N replica's worth of recent data |
```

with

```markdown
| Capacity recommendations | Aggregate metrics from PromQL, full window data | Fan-out, highest-confidence merge | `degradation.level: "degraded"` after every restart until the ring buffers refill; ring buffers only have ~1/N replica's worth of recent data |
```

**Anchor and change (§25.15 Failure Mode Analysis, Prometheus permanently absent row, line 4989):** Replace

```markdown
| **Prometheus permanently absent** | Acceptable at Tier 1; **strongly discouraged at Tier 2/3** (preflight emits a WARN). Beyond the transient-down behavior above, the long-term degradations described in Section 25.4, Prometheus Requirement, apply: capacity recommendations return `confidence: 0.0` after every restart; alert rules with `for: "15m"` clauses misfire because no historical data exists; humans receive no Alertmanager pages because bundled rules are never loaded; agents cannot investigate sessions/pools from past time windows. |
```

with

```markdown
| **Prometheus permanently absent** | Acceptable at Tier 1; **strongly discouraged at Tier 2/3** (preflight emits a WARN). Beyond the transient-down behavior above, the long-term degradations described in Section 25.4, Prometheus Requirement, apply: the capacity-recommendations response reports `degradation.level: "degraded"` after every restart until the ring buffers refill; alert rules with `for: "15m"` clauses misfire because no historical data exists; humans receive no Alertmanager pages because bundled rules are never loaded; agents cannot investigate sessions/pools from past time windows. |
```

**Rationale:** The three passages are not part of the line-586-versus-605 contradiction; they are consistent with line 586 and only require editing because C1 resolves the contradiction toward the envelope reading (Option A) and discards the `confidence: 0.0` phrasing. Leaving them would keep the spec internally inconsistent after C1. Each passage describes what the recommendations response returns, so the reworded text states the response-contract behavior served by the wired gateway in-process evaluator; it does not claim a wired `lenny-ops` degraded response, since `lenny-ops`'s Prometheus-backed `CapacityService` is not yet connected (`metrics.NewReader` is test-only, and `cmd/lenny-ops/httpsurface.go` discards the `PrometheusClient`). The reworded row at line 1470 leaves the transient-down "Fan-out, highest-confidence merge" column unchanged, and the edit introduces no new cross-replica degraded-merge mechanism: the per-response envelope logic (C3) runs on each replica's in-process evaluator, and the existing highest-confidence merge specified at line 4988 selects among those responses, so a merged recommendation reflects the most-confident (data-bearing) replica and stays `degraded` only while every replica's buffers are empty. C2 therefore does not alter the fan-out behavior; it only aligns the phrasing of the per-response signal.

### C3. Surface the degraded envelope in the gateway recommendations service by aggregating per-rule `DataAvailable`

**Target:** `pkg/gateway/operability/recommendations/service.go`, `GetRecommendations` (the evaluation loop at lines 271-303 and the envelope stamp at lines 307-310).

The service currently drops non-triggered evaluations (285-287) and stamps `Level: healthy` unconditionally (307-310), implementing the no-entry reading with no post-restart signal. Reading the `Evaluation.DataAvailable` the evaluators already return lets the service detect the all-windows-empty state and stamp `Level: degraded` without a new `WindowStore` method or wire field. This is the code half of the reconciliation and the concrete close of F-25.3.16.

**Anchor and change (declare the aggregation counters before the loop, line 271):** Replace

```go
	resp := &RecommendationsResponse{Recommendations: []Recommendation{}}
	for _, rule := range rules.Catalog() {
```

with

```go
	resp := &RecommendationsResponse{Recommendations: []Recommendation{}}
	// spec: §25.3 — aggregate whether any evaluated rule reported data.
	// An all-empty window set (for example shortly after a gateway
	// restart) surfaces below as a degraded §25.4 envelope so an agent
	// distinguishes a data-starved response from a healthy one.
	evaluated := 0
	anyData := false
	for _, rule := range rules.Catalog() {
```

**Anchor and change (record data presence before the drop, line 284):** Replace

```go
		e := ev(s.reader, s.windowFor(rule))
		if !e.Triggered {
			continue
		}
```

with

```go
		e := ev(s.reader, s.windowFor(rule))
		evaluated++
		if e.DataAvailable {
			anyData = true
		}
		if !e.Triggered {
			continue
		}
```

**Anchor and change (conditional envelope stamp, lines 304-311):** Replace

```go
	// spec: §25.13 line 4848 — the gateway's in-process recommendation
	// evaluator always runs the compiled-in defaults. lenny-ops layers
	// the operator-customized source on top when it serves the response.
	resp.Degradation = &conventions.Degradation{
		Level:           conventions.DegradationHealthy,
		ThresholdSource: conventions.ThresholdSourceCompiledInDefaults,
	}
	return resp, nil
```

with

```go
	// spec: §25.13 line 4848 — the gateway's in-process recommendation
	// evaluator always runs the compiled-in defaults. lenny-ops layers
	// the operator-customized source on top when it serves the response.
	resp.Degradation = &conventions.Degradation{
		Level:           conventions.DegradationHealthy,
		ThresholdSource: conventions.ThresholdSourceCompiledInDefaults,
	}
	// spec: §25.3 (post-restart degradation), §25.4 (canonical envelope)
	// — when at least one rule was evaluated but none reported data, every
	// ring buffer is empty (the post-restart, data-starved case). Report
	// the response-level envelope as degraded with a warning so an agent
	// distinguishes an empty recommendations array caused by starved
	// windows from one caused by a healthy platform with no issues. A
	// literal confidence: 0.0 is not set: Degradation.Confidence is
	// omitempty, so the level and the warning carry the signal.
	if evaluated > 0 && !anyData {
		resp.Degradation.Level = conventions.DegradationDegraded
		resp.Degradation.Warnings = []string{
			"recommendation metric windows are empty (gateway recently restarted); recommendations resume as the ring buffers refill",
		}
	}
	return resp, nil
```

**Rationale:** The evaluators already return `DataAvailable` on every branch (`service.go:368-453`), and the loop already reads it at `service.go:298`. Reading it before the `if !e.Triggered { continue }` drop and aggregating across the catalog yields the wholesale-empty signal with no new interface method or wire field. The stamp stays `healthy` when any rule had data (a below-threshold steady state) and flips to `degraded` only when every evaluated rule reported no data (the post-restart case). `recommendations[]` and the per-item `DataAvailable` field are unchanged; the degraded envelope is the response-level signal.

### C4. Align the tests to the degraded-envelope behavior and pin the new signal

**Target:** `pkg/gateway/operability/recommendations/service_test.go`, `TestGetRecommendationsEmptyWhenNoData` (lines 14-24), `TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848` (lines 155-173), and a new `TestGetRecommendationsHealthyEnvelopeWhenBelowThreshold`.

After C3, an empty `WindowStore` yields `Level: degraded`. `TestGetRecommendationsStampsCompiledInDefaults` uses an empty store and asserts `Level == DegradationHealthy` (169-172), so it breaks and must be updated; its real purpose (F-25.13.5, `thresholdSource` stays `compiled-in-defaults`) is preserved by giving it a below-threshold sample so a rule reports `DataAvailable`. `TestGetRecommendationsEmptyWhenNoData` still asserts `len == 0` (preserved) and is extended to pin the new degraded signal. A healthy-with-data case is added so the distinguishing behavior is covered on both sides.

**Anchor and change (`TestGetRecommendationsEmptyWhenNoData`, lines 14-24):** Replace

```go
func TestGetRecommendationsEmptyWhenNoData(t *testing.T) {
	// §25.3: with empty sliding windows, no rule triggers.
	svc := recommendations.NewCapacityService(recommendations.NewWindowStore(7 * 24 * time.Hour))
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) != 0 {
		t.Errorf("no-data recommendations: got %+v, want empty", resp.Recommendations)
	}
}
```

with

```go
func TestGetRecommendationsEmptyWhenNoData(t *testing.T) {
	// spec: §25.3 (post-restart degradation), §25.4 (canonical envelope)
	// — with empty sliding windows every rule is starved, so no rule
	// triggers and the response reports a degraded §25.4 envelope so an
	// agent can tell the empty array apart from a healthy platform.
	svc := recommendations.NewCapacityService(recommendations.NewWindowStore(7 * 24 * time.Hour))
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) != 0 {
		t.Errorf("no-data recommendations: got %+v, want empty", resp.Recommendations)
	}
	if resp.Degradation == nil {
		t.Fatal("expected degradation envelope on the empty-window response; got nil")
	}
	if resp.Degradation.Level != conventions.DegradationDegraded {
		t.Errorf("empty-window degradation level = %q, want %q",
			resp.Degradation.Level, conventions.DegradationDegraded)
	}
	if len(resp.Degradation.Warnings) == 0 {
		t.Error("empty-window response must carry a warning explaining the starved windows")
	}
}
```

**Anchor and change (`TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848`, lines 155-160):** Replace

```go
func TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848(t *testing.T) {
	svc := recommendations.NewCapacityService(recommendations.NewWindowStore(time.Hour))
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
```

with

```go
func TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848(t *testing.T) {
	// A below-threshold sample makes at least one rule report DataAvailable,
	// so the envelope stays healthy and this test isolates the
	// thresholdSource=compiled-in-defaults assertion from the empty-window
	// degraded path (spec: §25.13 line 4848, §25.4 canonical envelope).
	store := recommendations.NewWindowStore(time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.40)
	svc := recommendations.NewCapacityService(store)
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
```

The `Level == DegradationHealthy` assertion at lines 169-172 is preserved unchanged; it is now justified by the present-but-below-threshold sample rather than by the unconditional stamp.

**Anchor and change (new contrast test, appended after `TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848`):** Add

```go
// TestGetRecommendationsHealthyEnvelopeWhenBelowThreshold pins that a
// gateway with data present but below every rule's threshold reports a
// healthy envelope and an empty recommendations array, so the degraded
// signal is reserved for the wholesale-empty (post-restart) case and an
// agent can distinguish "no issue, data present" from "no data".
// spec: §25.3 (post-restart degradation), §25.4 (canonical envelope).
func TestGetRecommendationsHealthyEnvelopeWhenBelowThreshold(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.40)
	svc := recommendations.NewCapacityService(store)
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) != 0 {
		t.Errorf("below-threshold recommendations: got %+v, want empty", resp.Recommendations)
	}
	if resp.Degradation == nil || resp.Degradation.Level != conventions.DegradationHealthy {
		t.Errorf("below-threshold degradation = %+v, want level %q",
			resp.Degradation, conventions.DegradationHealthy)
	}
}
```

**Rationale:** The two committed tests encode the pre-reconciliation behavior; C3 changes the empty-store envelope level, so both are updated. The `conventions` import is already present (`service_test.go:11`). The new contrast test pins the both-sides distinction the finding requires: an empty array with a healthy envelope (data present, below threshold) is now observably different from an empty array with a degraded envelope (no data). This is tier-1 pure in-process logic, the tier F-25.3.16 reaches; no wire schema changes, so no tier-3 contract is newly reached.

### C5. Close F-25.3.16 on application

**Target:** `BUILD-GAPS.md`, F-25.3.16 (line 42417).

On application (after the spec edits land and the code and tests are green), mark F-25.3.16 CLOSED with a note that §25.3's self-contradictory post-restart recommendations contract is reconciled to the response-level §25.4 degradation envelope: line 586 and line 605 both describe an empty `recommendations` array with a `degradation` envelope at `level: degraded`, the §25.4 envelope trigger admits insufficient in-process history, the three §25.4/§25.15 cross-references are aligned, and the gateway service stamps the degraded envelope when every evaluated rule is starved. Record that the finding is closed for the wholesale-empty (post-restart) case and that per-rule partial-starvation surfacing is out of scope (see Open decisions). Reference this proposal id (0029).

## 5. Non-goals

- **Adopting Option B (per-item degraded entries in `recommendations[]`).** The array stays triggered and actionable-only, and the committed no-entry assertion (`TestGetRecommendationsEmptyWhenNoData`, `len == 0`) is preserved. Option B is presented as the reviewer's alternative in Open decisions.
- **Surfacing per-rule partial starvation on a partially-warmed gateway.** F-25.3.16 is closed for the wholesale-empty (post-restart) case. When some source metrics are present and others absent, the envelope stays `healthy` and a starved rule produces no entry; adding a per-rule starvation signal is a distinct, wider concern that would change `recommendations[]` semantics or add a per-rule surface.
- **Modeling the `degradation` envelope on the recommendations endpoint's OpenAPI 200 response.** This is orthogonal to the contradiction the finding names and rests on a mispositioned premise. The recommendations response already carried the envelope before this proposal (`service.go:152`, stamped at `service.go:307`); C3 only flips its `level`, it does not add the `degradation` object to the wire. The `degradation` envelope is a shared, canonical response-level object carried by many ops endpoints (`conventions.Degradation` is used across the ops surface: `pkg/ops/opsserver/me.go`, `diagnostics.go`, `locks.go`, `operations.go`; `pkg/gateway/operability/health/service.go`; and others), and it has never been modeled in `openapi.json` on any of them; the recommendations 200 schema is an open object with no `additionalProperties: false` (`pkg/gateway/externalapi/openapi/openapi.json:2855-2874`), so the unmodeled `degradation` field is already a valid response. Modeling it on this one endpoint alone would make it the sole endpoint advertising the envelope inline while every other envelope-carrying endpoint omits it, which is the parallel-surface inconsistency to avoid. If advertising the envelope in OpenAPI is wanted, that is a separate change adding one shared `Degradation` component schema referenced by every envelope-carrying endpoint, and it does not belong stapled to this reconciliation.
- **Removing `omitempty` from `Degradation.Confidence` or otherwise changing the shared envelope struct repo-wide.** The degraded state is signaled by `level: degraded` plus a warning; a literal `confidence: 0.0` on the envelope is not surfaced.
- **Populating the envelope `since` field with the process or restart timestamp.** The recommendations service does not track it today, and `level` is the signal. A follow-up could thread the gateway start time through if operators want it.
- **Removing or repurposing the per-item `DataAvailable` / `Confidence` fields on `Recommendation`.** They remain on emitted (triggered) entries and match the existing OpenAPI item schema.
- **Adding any new endpoint, RPC, wire field, migration, or store method, or introducing a tier-dependent code path.** The reconciliation reuses shipped surfaces only.

## 6. Testing

The change is pure in-process logic in `GetRecommendations`, so its behavior is pinned at tier 1 (`.claude/rules/test-coverage.md`: "Pure function, type, or in-process logic: tier 1"). No wire field or schema changes, so no tier-3 contract is newly reached. The spec-prose reconciliation is exercised at tier 11 (doc and spec consistency). Tier 0 builds the service and test edits.

- **tier-1 (empty-window degraded envelope, spec-named-failure path):** `TestGetRecommendationsEmptyWhenNoData` (updated). Against an empty `WindowStore`, assert `len(resp.Recommendations) == 0` (preserved), `resp.Degradation.Level == DegradationDegraded`, and a non-empty `Warnings` entry, so the post-restart data-starved state is observably distinct from a healthy response. `// spec: 25.3 (post-restart degradation), 25.4 (canonical envelope).`
- **tier-1 (healthy envelope with data below threshold, boundary and contrast path):** `TestGetRecommendationsHealthyEnvelopeWhenBelowThreshold` (new). Record a below-threshold metric (`lenny_credential_pool_utilization = 0.40`), assert `len == 0` and `Level == DegradationHealthy`, so an empty array with data present stays healthy and the degraded signal is reserved for the wholesale-empty case. `// spec: 25.3 (data-present distinction), 25.4 (canonical envelope).`
- **tier-1 (compiled-in-defaults threshold source preserved, regression path):** `TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848` (updated). Record a below-threshold sample so a rule reports `DataAvailable`, then assert `ThresholdSource == compiled-in-defaults` (preserved) and `Level == healthy` (now justified by present data), so the F-25.13.5 threshold-source contract still holds after C3. `// spec: 25.13 line 4848 (thresholdSource), 25.4 (canonical envelope).`
- **tier-11 doc and spec consistency (reconciled §25.3/§25.4/§25.15 text, cross-reference resolution):** Assert that no §25 surface says the recommendations response returns `confidence: 0.0` for the post-restart case; that the §25.3 Sliding window aggregation paragraph (line 586) and the §25.3 Degradation subsection (line 605) both describe an empty `recommendations` array with a `degradation` envelope at `level: degraded`; that the §25.4 envelope trigger (line 205) and the omitted-when-healthy clause (line 233) admit insufficient in-process history; that the §25.4 Prometheus-requirement bullet (line 1432), the §25.4 operational-consequences row (line 1470), and the §25.15 failure-mode row (line 4989) describe the `degradation.level: "degraded"` envelope; and that every cross-reference (§25.3 to §25.4, §25.4 to §25.3) resolves to a real anchor. `// spec: 25.3 (post-restart degradation), 25.4 (canonical envelope). // diagnosis: a failure means the reconciled §25.3/§25.4/§25.15 passages disagree about the post-restart signal, so an agent reading the spec cannot tell whether an empty recommendations array is starved or healthy.`

## Findings closed on application

- **F-25.3.16** (`BUILD-GAPS.md:42417`, "`/v1/admin/recommendations` does not surface the `degradation` / `dataAvailable` post-restart contract", Medium). Closed: §25.3's line-586-versus-605 contradiction is reconciled to the response-level §25.4 degradation envelope (C1), the §25.4 envelope trigger is widened to admit insufficient in-process history (C1), the three §25.4/§25.15 cross-references are aligned (C2), and the gateway service stamps `level: degraded` with a warning when every evaluated rule is starved (C3), pinned by the tests (C4). Closed for the wholesale-empty (post-restart) case; per-rule partial-starvation surfacing is out of scope (Non-goals, Open decisions).

## Resolved in adversarial review

Review rounds populate this section. It records each finding fixed and the converging change.

## Open decisions for review

1. **Option A (response-level envelope) versus Option B (per-item entries).** Recommended and staged: surface the post-restart signal through the response-level §25.4 degradation envelope at `level: degraded` (Option A). The finding text leaned toward Option B ("the rule emits a recommendation tagged `confidence: 0` / `dataAvailable: false` so an agent can see the rule exists but is starved"). This proposal chooses A because §25.4 designates the envelope as the response-level degradation signal, the post-restart condition is response-wide, and A preserves the committed no-entry test and the actionable-only meaning of `recommendations[]`. Option B's per-item `confidence: 0.0` is serializable (`Recommendation.Confidence` is not `omitempty`), so the choice is on design merits rather than a serialization constraint. If the reviewer requires per-rule granularity, the change set moves to Option B: rewrite line 605 to the emit-entry reading, overturn `TestGetRecommendationsEmptyWhenNoData`, and emit a starved entry per data-absent rule.
2. **`level: degraded` plus a warning as the signal, versus a literal `confidence` value.** Recommended and staged: signal the degraded state via `level: degraded` and a `warnings` entry. Surfacing a literal `confidence: 0.0` on the envelope would require dropping `omitempty` on `Degradation.Confidence`, a serialization change affecting every degradation envelope across the ops API; the proposal recommends against that and rewrites the spec prose to describe `level: degraded` instead.
3. **Narrowing F-25.3.16 to the all-empty (post-restart) case.** Recommended and staged: close the finding for the wholesale-empty case and leave per-rule partial starvation (some metrics present, others absent) unsurfaced. The reviewer ratifies this scope. Surfacing per-rule starvation would either change `recommendations[]` semantics (Option B) or add a per-rule field, both wider than the reconciliation this proposal makes.

## 7. Files touched on application

- `spec/25_agent-operability.md`: C1 (§25.3 Sliding window aggregation paragraph line 586; §25.3 Degradation subsection line 605; §25.4 envelope trigger line 205; §25.4 "Omitted when healthy" clause line 233), C2 (§25.4 Prometheus-requirement capacity-recommendations bullet line 1432; §25.4 operational-consequences Capacity recommendations row line 1470; §25.15 Prometheus-permanently-absent row line 4989).
- `pkg/gateway/operability/recommendations/service.go`: C3 (`GetRecommendations` aggregates per-rule `DataAvailable` and stamps `level: degraded` with a warning when every evaluated rule is starved).
- `pkg/gateway/operability/recommendations/service_test.go`: C4 (`TestGetRecommendationsEmptyWhenNoData` extended to pin the degraded envelope; `TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848` given a below-threshold sample; `TestGetRecommendationsHealthyEnvelopeWhenBelowThreshold` added).
- `BUILD-GAPS.md`: C5 (mark F-25.3.16 CLOSED, referencing proposal 0029).
