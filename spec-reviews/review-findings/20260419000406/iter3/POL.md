# Iter3 POL Review

Two findings. POL-014 iter2 fix is mostly correct (the pre-chain gate framing and the `admission.circuit_breaker_rejected` event are properly introduced at §4.8, §11.6, and §16.7) but introduced (a) a broken anchor link and (b) a chain-ordering contradiction between §11.6 and §4.8.

One additional policy/RBAC issue (audit amplification for circuit-breaker floods) was previously not called out and is worth flagging.

---

### POL-015 POL-014 fix introduces contradictory chain-ordering statements for AdmissionController vs. AuthEvaluator [Medium]

**Files:** `spec/11_policy-and-controls.md:297`, `spec/04_system-components.md:952,1036`, `spec/16_observability.md:542`

The iter2 POL-014 fix added a "Pre-chain gate" callout in two places whose wording disagrees:

- **§11.6 line 297** (new): "Circuit-breaker evaluation is a **pre-chain gate** and is NOT an interceptor. It runs **before `AuthEvaluator`** in both the `PostAuth` and `PreDelegation` admission phases..."
- **§4.8 line 952** (new): "`AdmissionController` ... is evaluated **BEFORE the `PostAuth` and `PreDelegation` interceptor chains run**. It is NOT registered in the built-in interceptor priority table above... External interceptors at priorities 101–999 run AFTER circuit-breaker evaluation..."

These are inconsistent. §4.8's built-in phase table (line 1036) and the clarifying prose at line 1032 establish that `AuthEvaluator` **fires at `PreAuth`** (priority 100), not `PostAuth`. `PreAuth` completes before `PostAuth` starts — so "runs before `AuthEvaluator`" (§11.6) means runs before `PreAuth`, while "runs before the `PostAuth` and `PreDelegation` chains" (§4.8) means runs **after** `PreAuth` (after AuthEvaluator).

The §16.7 audit event payload (line 542) further confirms §4.8 is the intended semantics: `admission.circuit_breaker_rejected` payload includes `caller_sub` and `caller_tenant_id`, which are only available after `AuthEvaluator` has run. If the gate actually ran before `AuthEvaluator` as §11.6 claims, those fields would be unpopulated at rejection time.

This matters because:

1. A deployer reading §11.6 first (the subsystem docs) will wire admission checks expecting unauthenticated context, and then see the audit event include authenticated identity — silently splitting implementations between readers who happen to hit §4.8 first versus §11.6 first.
2. Security teams auditing the circuit-breaker REJECT path will not know whether `CIRCUIT_BREAKER_OPEN` can be triggered by an unauthenticated caller (pre-Auth) — which changes the risk model (open breaker name and reason are exposed in the error body to an unauthenticated caller, potentially probing infrastructure state).

**Recommendation.** Make §11.6 match §4.8. Replace "runs before `AuthEvaluator`" with "runs after `AuthEvaluator` completes at `PreAuth` and **before the `PostAuth` and `PreDelegation` interceptor chains run**". While there, state explicitly that the admission gate requires the authenticated caller identity to populate the audit payload and to evaluate any tenant-scoped circuit-breaker rules (currently the spec says breakers are scoped to runtime/pool/connector/operation_type — not tenant — but the audit event carries caller_tenant_id anyway; pin this down).

---

### POL-016 Broken anchor in POL-014 audit-catalog cross-reference [Low]

**Files:** `spec/11_policy-and-controls.md:299`

The iter2 POL-014 fix added the line:

> "Both events are written to the append-only audit tables ([§11.7](#117-audit-logging)) and appear in the catalogued audit event list in [§16.7](16_observability.md#167-audit-event-catalogue) / equivalent."

The anchor `#167-audit-event-catalogue` does not resolve — the actual §16.7 heading is "Section 25 Audit Events" and its slug is `#167-section-25-audit-events`. Every other cross-reference to §16.7 in the spec uses the correct slug (e.g., `spec/11_policy-and-controls.md:382`, `spec/12_storage-architecture.md:908`, `spec/13_security-model.md:508`, `spec/README.md:105`).

The trailing "/ equivalent" reads as placeholder authoring — it suggests the author was unsure where the audit event list actually lives.

**Recommendation.** Change the anchor to `#167-section-25-audit-events`, and drop "/ equivalent":

```
and appear in the catalogued audit event list in [§16.7](16_observability.md#167-section-25-audit-events).
```

Separately, consider renaming §16.7 from "Section 25 Audit Events" to "Audit Event Catalog" in a future cleanup — the §16.7 list already includes events originating in §4.3, §4.8, §11.6, §11.7, and §13.3, so the "Section 25" framing is misleading. Not a blocker for iter3; filing here as an observation.

---

### POL-017 `admission.circuit_breaker_rejected` has no audit-sampling discipline for breaker-storm scenarios [Medium]

**Files:** `spec/11_policy-and-controls.md:299`, `spec/16_observability.md:542`, `spec/13_security-model.md:530`

The iter2 fix introduced "Every request REJECTed by a tripped breaker emits an `admission.circuit_breaker_rejected` audit event" (§11.6, line 299). Circuit breakers are operator-tripped reactions to a degraded runtime, pool, connector, or external provider outage. In exactly those situations, session-creation and delegation calls against the affected resource **continue to arrive** — often from misbehaving retry loops in clients unaware that the breaker is open — while the gate rejects them all.

This path has no sampling. The comparable `token.exchange_rate_limited` path (§13.3 line 530) *is* audit-sampled precisely because saturated rejection streams saturate the per-tenant advisory-locked audit write path (§11.7 item 3) and starve legitimate audit writes. A tripped breaker on a popular runtime during an outage produces the same saturation pattern: every inbound session creation against `runtime=sonnet` writes an audit row, all serialized on the per-tenant advisory lock.

Concretely: §11.7 item 3 specifies `audit.lock.acquireTimeoutMs` (default 5000), `audit.lock.maxRetries` (default 3), and a P99 lock-acquisition SLO of 50 ms. An audit storm on one tenant will contend this lock and surface as `AUDIT_CONCURRENCY_TIMEOUT` on legitimate audit writes for the same tenant (including `session.created`, `token.exchanged`, and `interceptor.rejected`). Because breaker trips are the most likely trigger for sustained high-volume admission rejections, skipping sampling here means the very incident that tripped the breaker can cascade into an audit-pipeline backpressure incident.

**Recommendation.** Extend §11.6's audit-events paragraph with a sampling rule mirroring §13.3's `token.exchange_rate_limited` approach:

> "**Sampling under breaker storms.** When a breaker is open, rejection volume for the affected runtime/pool/connector can be arbitrarily large. To protect the per-tenant advisory-locked audit write path ([§11.7](#117-audit-logging) item 3), `admission.circuit_breaker_rejected` is sampled: the first rejection per `(tenant_id, circuit_name, caller_sub)` tuple within any rolling 10-second window is written as a full audit row; subsequent rejections in the same window increment `lenny_circuit_breaker_rejections_suppressed_total{tenant_id, circuit_name, limit_tier}` but do not write individual rows. The sampling window is per-replica in-memory (same locality discipline as §13.3 'Sampling window locality')."

Add the suppressed-counter metric to §16.1. Also wire it into the `CircuitBreakerActive` alert body so on-call can see both `lenny_circuit_breaker_open` and `lenny_circuit_breaker_rejections_suppressed_total{}` to size the rejection storm.

---

### Verification notes (no findings)

- **POL-014 pre-chain gate recognized across spec.** §4.8 line 952 (new) and §16.7 line 542 (new) correctly treat `AdmissionController` as a pre-chain gate with a distinct audit event type. The taxonomy in §4.8's evaluators table (line 927–931) still lists `AdmissionController` as a policy-engine module but no longer obligates placement in the built-in priority table — the POL-014 fix resolves the iter2 ambiguity.
- **`derive.isolation_downgrade` (SEC-001 fix) audit wiring.** Properly added to §11.2.1 billing event table (line 65), with full payload schema (lines 101–106). Cross-referenced from §7.1 (derive semantics) and §15.1 (`allowIsolationDowngrade` replay-path wording). RBAC gate (`platform-admin` only, `403 FORBIDDEN` for non-admin with the flag set) is consistently declared. Not listed in §16.7 Section-25 audit catalog, but §16.7's scope is nominally §25 events plus a few cross-cut inclusions — and this one is correctly declared in §11.2.1 which is the canonical location for the billing event stream. No action.
- **`noEnvironmentPolicy` validation.** §11.1 line 13 matches §10.3 startup validation — iter2 TNT-001 fix holds.
- **Interceptor priority reservation.** §4.8 lines 994–996 reserves priorities 1–100 for built-in security-critical interceptors; the new POL-014 callout at line 952 states external interceptors at 101–999 run after circuit-breaker evaluation, which is consistent with the reservation (and removes the iter2 concern about priority collisions).
- **Billing event stream sampling.** §11.2.1 does not repeat §13.3's sampling discipline because billing events are issue-at-completion (per-session) events, not per-request. The `interceptor.fail_policy_weakened` / `strengthened` events are state-change events (rare), so no sampling needed — only `admission.circuit_breaker_rejected` (POL-017 above) has the storm problem.
- **Circuit-breaker Admin API RBAC.** §11.6 line 286 correctly scopes open/close to `platform-admin`; consistent with the rest of the admin-RBAC model in §15.1.

---

Word count: ~1,020.
