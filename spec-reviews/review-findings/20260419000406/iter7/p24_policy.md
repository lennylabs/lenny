# Iter7 Review — Perspective 24: Policy Engine & Admission Control

**Scope:** `spec/11_policy-and-controls.md` §11.6 (Circuit Breakers) with cross-file touchpoints in `spec/16_observability.md` §16.1/§16.5/§16.7, `spec/12_storage-architecture.md` §12.4 (`cb:{name}` key schema), `spec/04_system-components.md` §4.8 (pre-chain gate callout), `spec/25_agent-operability.md` §25.3 (Event Types table), `spec/15_external-api-surface.md` §15.4 (error catalog), and docs alignment (`docs/api/admin.md`, `docs/operator-guide/*`, `docs/reference/*`, `docs/runbooks/circuit-breaker-open.md`).

## Iter6 carry-forward verification

The iter6 fix commit (`8604ce9`) touched `spec/11_policy-and-controls.md` in only two places (cross-file anchor updates `§15.4` → `§15.1` and `§17.8.1` → `§17.6` on line 418 and one other cross-reference line per the commit diff), `spec/25_agent-operability.md` in one row (unrelated `PLATFORM_AUDIT_REGION_UNRESOLVABLE` Storage Routing line 1492, which was the API-022 POLICY→PERMANENT change applied to the Storage Routing table via §25.11), and `spec/16_observability.md` for OBS-037/038 backing-gauge additions. None of the five iter6 Low findings (POL-029 through POL-033) were addressed in the iter6 fix round — they are all deferred to iter7.

- **POL-023** (iter5 High, admin API body schema scope) — **Remains Fixed.**
  - `spec/11_policy-and-controls.md:312` still documents the body as `{ reason, limit_tier, scope }` with the tier-specific matcher table at lines 285-290; `INVALID_BREAKER_SCOPE` (HTTP 422) enumerated for missing tier/scope, cross-tier mismatch, out-of-enum `operation_type` value, and persisted-scope mismatch. The iter6-added `/v1/admin/circuit-breakers/*` endpoint rows in `spec/15_external-api-surface.md` carry the same body shape; no OPA-validation row regression introduced by API-020.
  - `cb:{name}` Redis value at `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192` remain `{state, reason, opened_at, opened_by_sub, opened_by_tenant_id, limit_tier, scope}`.
  - `docs/api/admin.md:717-751`, `docs/reference/error-catalog.md:161`, `docs/operator-guide/troubleshooting.md:249-272`, `docs/operator-guide/lenny-ctl.md:245,403-415` remain aligned.

- **POL-025** (iter5 Medium, operator-identity field drift) — **Remains Fixed at the three iter5 surfaces** (Redis `cb:{name}`, CloudEvents emission payload at §11.6 line 304, audit state-change event at §11.6 line 319 and §16.7 line 655). The sibling-drift discoveries POL-032 (§25.3 Event Types table) and POL-033 (`docs/reference/cloudevents-catalog.md`) persist — see carry-forward rows below.

- **POL-026** (iter5 Medium, `circuit_breaker.state_changed` in §16.7 catalogue) — **Remains Fixed.** `spec/16_observability.md:655` catalog entry and field set still align with `spec/11_policy-and-controls.md:319`.

- **POL-024/POL-027/POL-028** (iter4/iter5 Low carry-forwards) — Rolled forward as POL-029/POL-030/POL-031 in iter6; see below.

### POL-029 § 11.6 "AdmissionController evaluation" line 315 still reads "before quota and policy evaluation" — iter6 POL-029 NOT fixed [Low, carry-forward]

**Section:** `spec/11_policy-and-controls.md:315-317`.

The iter6 fix commit left line 315 unchanged. Current text:

> "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers at the start of every session-creation and delegation admission check, **before quota and policy evaluation**. ..."

Line 317 (the POL-014 callout, still unchanged) continues to use the canonical phase vocabulary (`PreAuth` → `AdmissionController` → `PostAuth`/`PreDelegation`). The vocabulary-redundancy friction iter4 POL-022 / iter5 POL-024 / iter6 POL-029 identified persists. Severity remains Low per iter4/iter5/iter6 calibration; recommendation identical to iter6 POL-029 (rewrite line 315 using canonical phase vocabulary).

### POL-030 § 11.6 "Running replica, Redis outage" bullet at line 298 still uses "REJECT / non-REJECT" — iter6 POL-030 NOT fixed [Low, carry-forward]

**Section:** `spec/11_policy-and-controls.md:298`, `spec/16_observability.md:211`, `spec/16_observability.md:657`.

Line 298 still reads "Each stale-serve admission decision (both **REJECT and non-REJECT outcomes**) increments `lenny_circuit_breaker_cache_stale_serves_total`...". The paired metric and audit event use `outcome: rejected | admitted`. Severity remains Low (vocabulary drift across sibling sections; behaviorally correct); recommendation identical to iter6 POL-030.

### POL-031 `admission.circuit_breaker_cache_stale` sampling discipline still only in §16.7 — iter6 POL-031 NOT fixed [Low, carry-forward]

**Section:** `spec/11_policy-and-controls.md:321` (home-section "Sampling under breaker storms" paragraph), `spec/16_observability.md:657` (catalogue entry carrying the `(tenant_id, caller_sub, outcome)` sampling rule).

The §11.6 "Sampling under breaker storms" paragraph still documents sampling for `admission.circuit_breaker_rejected` (key: `(tenant_id, circuit_name, caller_sub)`, per-replica 10 s window) but omits the sibling event. The cache-stale sampling rule — differently keyed by `(tenant_id, caller_sub, outcome)` to reflect the cache-as-a-whole scope rather than per-breaker scope — lives exclusively in §16.7. Severity remains Low (subsystem-home documentation completeness; the behavior is authoritative at §16.7); recommendation identical to iter6 POL-031.

### POL-032 `spec/25_agent-operability.md:688-689` Event Types table still describes breaker payload as "opener identity"/"closer identity" — iter6 POL-032 NOT fixed [Low, carry-forward]

**Section:** `spec/25_agent-operability.md:688-689`, `spec/11_policy-and-controls.md:304`, `spec/16_observability.md:655`.

Confirmed unchanged. The §25.3 Event Types table at lines 688-689 still carries the pre-iter5 framing:

> "| `circuit_breaker_opened` | Circuit breaker opened | Name, reason, opener identity |
> | `circuit_breaker_closed` | Circuit breaker closed | Name, closer identity |"

The `close` row also still omits `reason` while §11.6 line 304 emits it for both transitions. Severity remains Low per the iter6 POL-032 extension-family calibration (non-home surface, mechanical text edit); recommendation identical to iter6 POL-032.

### POL-033 `docs/reference/cloudevents-catalog.md:55-56` still uses `opener`/`closer` — iter6 POL-033 NOT fixed [Low, carry-forward]

**Section:** `docs/reference/cloudevents-catalog.md:55-56`.

Confirmed unchanged. Lines 55-56 still read:

> "| `dev.lenny.circuit_breaker_opened` | Circuit opened | `name`, `reason`, `opener` |
> | `dev.lenny.circuit_breaker_closed` | Circuit closed | `name`, `closer` |"

The docs-sync drift from iter5 POL-025 persists. Severity remains Low per iter6 POL-033 (docs-sync companion to POL-032). Recommendation identical to iter6 POL-033.

## Iter7 new findings

### POL-034 `docs/operator-guide/observability.md:189` describes `QuotaFailOpenUserFractionInoperative` as "Gateway startup warning emitted" — stale against iter6 OBS-037 fix that made the alert a continuously-firing Prometheus rule [Low] — **Fixed**

**Status:** Fixed alongside OBS-041 (Medium, same docs surface and same remediation). The `docs/operator-guide/observability.md` row for `QuotaFailOpenUserFractionInoperative` now describes the continuously-firing Prometheus alert expression `lenny_quota_user_failopen_fraction >= 0.5` with the supplementary mention of the startup log warning and `lenny-ops` config-validation warning, matching `spec/16_observability.md` §16.5 verbatim on the continuous-firing semantics. See iter7 OBS-041 Fixed entry in `summary.md` for the full remediation.


**Section:** `docs/operator-guide/observability.md:189`, `spec/16_observability.md:451`, `docs/reference/metrics.md:491`.

The iter6 OBS-037 fix (commit `8604ce9`) introduced the `lenny_quota_user_failopen_fraction` backing gauge (`spec/16_observability.md:203`) specifically so that `QuotaFailOpenUserFractionInoperative` could be a continuously-firing Prometheus alert rather than a one-shot startup warning. `spec/16_observability.md:451` now defines the alert:

> "`QuotaFailOpenUserFractionInoperative` | `lenny_quota_user_failopen_fraction >= 0.5`. ... The gateway additionally emits a structured log warning with the same name at startup when this condition is first observed, and `lenny-ops` emits the same warning during config validation — the alert fires continuously while the condition holds so it is visible to operators who joined after startup. ..."

`docs/reference/metrics.md:491` also documents the alert as Prometheus-based.

But `docs/operator-guide/observability.md:189` still describes the triggering condition using the pre-iter6 startup-warning framing:

> "| `QuotaFailOpenUserFractionInoperative` | Gateway startup warning emitted when `quotaUserFailOpenFraction >= 0.5` (default `0.25`) | Lower `quotaUserFailOpenFraction` below 0.5 ..."

The "Gateway startup warning emitted when ..." phrase describes the **pre-iter6** behavior (one-shot log warning at startup) rather than the iter6 **post-fix** behavior (continuously-firing Prometheus alert with log-warning parity at startup). The two surfaces disagree on when operators see the signal:

- `spec/16_observability.md:451` and the metric table at `docs/reference/metrics.md`: alert fires continuously while the condition holds.
- `docs/operator-guide/observability.md:189`: alert is a one-shot startup warning.

The intent iter6 OBS-037 resolved was exactly that operators joining **after** startup would still see the alert — the stale `docs/operator-guide/observability.md` row describes the scenario the fix addressed, not the fix itself.

Admission-control relevance: the per-user fail-open cap is a quota-admission control (bounds a single user's share of the fail-open allocation during Redis outage; see `spec/12_storage-architecture.md:222` and §12.4 Per-user fail-open ceiling). Its alert-operational-posture drift is on the admission-control observability surface and within scope for the Policy Engine & Admission Control perspective's iter7 must-check list ("`QuotaFailOpenUserFractionInoperative` alert PromQL backing-gauge integration with admission-control path").

Severity anchoring: iter6 OBS-037 landed at Medium (backing-gauge missing → alert PromQL did not resolve). The docs-sync drift discovered here is one mechanical text edit to a docs operator-guide row on an already-Medium-resolved fix. Iter5/iter6 docs-sync findings on non-home surfaces consistently landed at Low (POL-033, DOC-024/025, CRD-021). Low is the calibrated severity.

**Recommendation:** Rewrite `docs/operator-guide/observability.md:189` to describe the Prometheus-alert behavior:

> "| `QuotaFailOpenUserFractionInoperative` | `lenny_quota_user_failopen_fraction >= 0.5` — continuous Prometheus alert (default fraction `0.25`). A log warning with the same name is also emitted at gateway startup, and `lenny-ops` emits the warning during config validation. | Lower `quotaUserFailOpenFraction` below 0.5 to keep the per-user fail-open cap meaningful; values at or above 0.5 let a single runaway user consume the tenant ceiling during a Redis outage. |"

Per `feedback_docs_sync_after_spec_changes.md` (reconcile docs with spec changes after each review-fix iteration before declaring convergence), this is a mandatory docs-sync remediation for the iter6 OBS-037 fix.

## Convergence assessment

Iter7 on the Policy Engine & Admission Control perspective **does not converge**. Six findings remain:

- **0 Critical / High / Medium.** No new correctness or subsystem-architecture findings at High or Medium. The iter5 High (POL-023), two iter5 Mediums (POL-025, POL-026), and iter6 resolved-but-not-landed items are all at expected state except the five iter6 Low carry-forwards POL-029/POL-030/POL-031/POL-032/POL-033 and the one iter7 new Low docs-sync gap POL-034 on the OBS-037 admission-control observability surface.

- **6 Low:**
  - POL-029 (iter5 POL-024 → iter6 POL-029 → iter7 carry-forward; §11.6 line 315 prose framing).
  - POL-030 (iter5 POL-027 → iter6 POL-030 → iter7 carry-forward; §11.6 line 298 "REJECT / non-REJECT" vs. `rejected`/`admitted`).
  - POL-031 (iter5 POL-028 → iter6 POL-031 → iter7 carry-forward; §11.6 "Sampling under breaker storms" omits cache-stale sibling).
  - POL-032 (iter6 new → iter7 carry-forward; `spec/25_agent-operability.md:688-689` "opener/closer identity").
  - POL-033 (iter6 new → iter7 carry-forward; `docs/reference/cloudevents-catalog.md:55-56` `opener`/`closer`).
  - POL-034 (iter7 new; `docs/operator-guide/observability.md:189` describes `QuotaFailOpenUserFractionInoperative` as startup-only warning after iter6 OBS-037 made it continuous) — **Fixed** (closed by OBS-041 fix on the same docs row).

All six Lows are consistent-severity anchored against iter4/iter5/iter6 Low carry-forwards (POL-022 → POL-024 → POL-029; POL-027 → POL-030; POL-028 → POL-031) and against the POL-018/POL-025 identity-field alignment family reduced-from-Medium-for-non-home-surface rationale (POL-032, POL-033, POL-034). POL-034 aligns with the iter5/iter6 Low calibration for docs-sync follow-up findings on already-resolved Medium fixes (DOC-024/025, CRD-021 parallels).

Iter4 Fixed items (POL-018, POL-019, POL-020) and iter5 Fixed items (POL-023, POL-025, POL-026) remain correctly fixed at their spec-level surfaces. iter6 API-020/021/022/023/024 did not regress any policy-perspective correctness surface: the new `/v1/admin/circuit-breakers/*` endpoint rows in `spec/15_external-api-surface.md` carry the `{reason, limit_tier, scope}` body shape defined at `spec/11_policy-and-controls.md:312`, and the circuit-breaker `limit_tier` scope-domain table entry in §15 aligns with the POL-023 enum. Iter6 OBS-037/OBS-038 correctly landed the backing gauges at `spec/16_observability.md:203` (`lenny_quota_user_failopen_fraction`, no labels) and at `spec/12_storage-architecture.md:222` (log warning + gauge emission) with alert PromQL at `spec/16_observability.md:451` wired to the backing gauge — the one docs-surface miss surfaces as POL-034 above.

An eighth iteration can close POL-029/POL-030/POL-031/POL-032/POL-033/POL-034 with purely mechanical text edits — no behavior change is required on any finding — so convergence is within reach once this iteration's fixes apply.
