# Perspective 12 — Observability & Operational Monitoring — Iteration 7 Findings

Scope: Re-review of `spec/16_observability.md` and adjacent surfaces (`spec/12_storage-architecture.md`, `spec/17_deployment-topology.md`, `spec/04_system-components.md`, `docs/reference/metrics.md`, `docs/operator-guide/observability.md`, `docs/runbooks/*`) for iteration 7, focusing on:
1. Verification of iter6 fixes for OBS-037 (QuotaFailOpenUserFractionInoperative alert/gauge), OBS-038 (LegalHoldCheckpointAccumulationProjectedBreach PromQL + `lenny_tenant_storage_quota_bytes` gauge), and the cross-perspective CRD-020 user-credential gauge/alert.
2. Drift detection across spec and docs surfaces.
3. New defects in iter6 fixes themselves.

Severity rubric anchored to iter1–iter5 baseline per `feedback_severity_calibration_iter5.md`.

---

## Prior-iteration carry-forwards (verification of iter6 fixes)

| ID | iter6 verdict | iter7 status | Evidence |
|---|---|---|---|
| OBS-037 (QuotaFailOpenUserFractionInoperative) | Fixed | **Fixed in spec, drift in docs** | Spec `16_observability.md:451` defines alert `lenny_quota_user_failopen_fraction >= 0.5`; `16_observability.md:203` defines the gauge. `docs/reference/metrics.md:428-430` and `:491` correctly list the gauge and alert. **But** `docs/operator-guide/observability.md:189` still describes the alert as "Gateway startup warning emitted when `quotaUserFailOpenFraction >= 0.5` (default `0.25`)", not the actual PromQL alert expression now in §16.5. Tracked as new finding **OBS-041** below. |
| OBS-038 (LegalHoldCheckpointAccumulationProjectedBreach) | Fixed | **Partially fixed — defective PromQL introduced, drift in docs and spec/17** | Spec `16_observability.md:488` now evaluates against an exported gauge (`lenny_tenant_storage_quota_bytes`, `16_observability.md:202`) instead of the bare `storageQuotaBytes` identifier. **But** the iter6 rewrite introduced a new PromQL well-formedness defect (`0.9 * on(tenant_id) group_left lenny_tenant_storage_quota_bytes`) — tracked as **OBS-039** below. `docs/operator-guide/observability.md:190` still carries the old `lenny_legal_hold_checkpoint_projected_growth_bytes / (storageQuotaBytes - lenny_storage_quota_bytes_used) > 0.9` expression — tracked as **OBS-040** below. `spec/17_deployment-topology.md:820` still uses a bare-identifier `0.9 * storageQuotaBytes` expression in the runbook description — tracked as **OBS-042** below. |
| CRD-020 (Emergency revocation user-scoped gauge & alert, cross-perspective carry-forward from Credential review) | Fixed | **Fixed** | Spec `16_observability.md:59` defines `lenny_user_credential_revoked_with_active_leases{tenant_id, provider}` gauge. Spec `16_observability.md:412` extends `CredentialCompromised` to cover both pool-scoped and user-scoped cases via an `or` clause. `docs/reference/metrics.md:275,509` list the new gauge and extended alert coverage. `docs/runbooks/credential-revocation.md:222` cites the new gauge. Minor gap tracked as **OBS-045** (Low) below. |

---

## New findings

### OBS-039 — Malformed PromQL in `LegalHoldCheckpointAccumulationProjectedBreach` alert expression [Medium]

**Location:** `spec/16_observability.md:488`.

**Issue:** The iter6 rewrite of the alert expression reads:

```
(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes))
  > 0.9 * on(tenant_id) group_left lenny_tenant_storage_quota_bytes
```

The `on(tenant_id) group_left` vector-matching modifier is attached to `0.9 *`, i.e., to a **scalar-vector product**. PromQL's `on()` / `ignoring()` / `group_left` / `group_right` matching modifiers are only meaningful on **vector-vector binary operations**; PromQL rejects them on scalar-vector arithmetic at parse time (and on the comparison `>` in this position they're redundant because both sides of `>` already carry the same `{tenant_id}` labelset after the `sum by (tenant_id)` on the left and the gauge's own `{tenant_id}` labelset on the right). As written the expression is not a valid PrometheusRule body; rendering the §16.5 alert catalog to a PrometheusRule CRD (per the spec's single-source-of-truth rendering requirement) will fail validation.

**Impact:** The alert cannot deploy as written. Because the iter6 fix claimed to close OBS-038 by making the expression "a concrete PromQL comparison rather than a bare config identifier," the defect regresses the fix — the expression is now evaluable *in form* (all operands are metrics) but still unparseable *in content*.

**Recommended fix:** Drop the `on(tenant_id) group_left` modifier entirely and let the natural label matching succeed:

```
(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes))
  > 0.9 * lenny_tenant_storage_quota_bytes
  and on(tenant_id) lenny_tenant_legal_hold_active_count > 0
```

Both sides of the `>` now carry `{tenant_id}` naturally (the left from `sum by (tenant_id)`, the right from the gauge's own label). The trailing `and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` guard makes the §16.5 prose restriction ("For any tenant with `lenny_tenant_legal_hold_active_count > 0`") load-bearing in the expression itself — currently the prose restricts the scope but the expression does not, so the alert fires for any tenant meeting the growth threshold regardless of whether any legal hold is active (see **OBS-043** below for that issue as a standalone finding).

**Severity rationale:** Medium. Parity with iter6 OBS-038 (Medium) — the defect is in the body of an alert that blocks rendering rather than in a metric name or prose description.

---

### OBS-040 — Docs drift: `LegalHoldCheckpointAccumulationProjectedBreach` description in operator guide carries stale PromQL [Medium]

**Location:** `docs/operator-guide/observability.md:190`.

**Issue:** The operator guide row for `LegalHoldCheckpointAccumulationProjectedBreach` still reads:

```
`lenny_legal_hold_checkpoint_projected_growth_bytes / (storageQuotaBytes - lenny_storage_quota_bytes_used) > 0.9`
— predictive projection that a legal-hold-protected session's checkpoint growth will consume 90% of remaining tenant storage headroom before the hold is cleared
```

This is the pre-iter6 expression that OBS-038 identified as broken (bare `storageQuotaBytes` identifier; also semantically different from the §16.5 definition, which compares current+projected against 90% of total quota, not projected against 90% of remaining headroom). The spec-side expression has been rewritten twice (iter6 and the recommended iter7 fix per OBS-039 above); the operator guide has not tracked either change. This violates `feedback_docs_sync_after_spec_changes.md`.

**Impact:** Operators reading the docs will see an alert description that does not match what the platform actually fires on. During incident response they may diagnose based on the wrong semantics ("remaining headroom" vs. "total quota with projected growth added").

**Recommended fix:** Replace line 190 with the corrected §16.5 expression (per OBS-039's recommended form):

```
`(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes` for any tenant with `lenny_tenant_legal_hold_active_count > 0` — predictive projection that a legal-hold-protected session's checkpoint growth will push the tenant's shared `storageQuotaBytes` bucket past 90% utilization before the hold is cleared
```

**Severity rationale:** Medium. Docs-drift on a compliance-relevant alert falls under the `feedback_docs_sync_after_spec_changes.md` invariant; parity with iter4/iter5 Medium-severity docs drift findings.

---

### OBS-041 — Docs drift: `QuotaFailOpenUserFractionInoperative` description in operator guide describes startup warning, not PromQL alert [Medium]

**Location:** `docs/operator-guide/observability.md:189`.

**Issue:** The operator guide row reads:

```
`QuotaFailOpenUserFractionInoperative` | Gateway startup warning emitted when `quotaUserFailOpenFraction >= 0.5` (default `0.25`) | …
```

The iter6 OBS-037 fix promoted this from a startup warning to a PromQL alert (`lenny_quota_user_failopen_fraction >= 0.5`) backed by a new gauge (`lenny_quota_user_failopen_fraction`, `spec/16_observability.md:203`). The spec `16_observability.md:451` row is explicit about the two emission channels: "The gateway additionally emits a structured log warning with the same name at startup … the alert fires continuously while the condition holds so it is visible to operators who joined after startup." The operator guide only describes the startup warning, giving a misleading impression that the signal is one-shot at startup.

**Impact:** Operators who joined after gateway startup (common in incident handover) will not realize the alert is continuously available from Prometheus; they may miss the signal if they didn't catch the startup log line.

**Recommended fix:** Replace line 189 with:

```
`QuotaFailOpenUserFractionInoperative` | `lenny_quota_user_failopen_fraction >= 0.5` — the gateway's configured per-user fail-open fraction (default `0.25`) is substantially weakened; at `>= 0.5` a single user can consume at least half the tenant's per-replica fail-open allocation during a Redis outage. The gateway additionally emits a matching structured log warning at startup. | Lower `quotaUserFailOpenFraction` below 0.5 to keep the per-user fail-open cap meaningful; values at or above 0.5 let a single runaway user consume the tenant ceiling during a Redis outage
```

**Severity rationale:** Medium. Parity with OBS-040 — same docs-drift invariant, same class of mis-described alert.

---

### OBS-042 — Spec drift: `spec/17_deployment-topology.md` runbook row carries bare-identifier PromQL [Medium]

**Location:** `spec/17_deployment-topology.md:820`.

**Issue:** The operational-runbooks section row for `legal-hold-quota-pressure` still describes the alert expression as:

```
`(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * storageQuotaBytes`
```

`storageQuotaBytes` is the configuration-field name, not a Prometheus series. The iter6 OBS-038 fix explicitly addressed this by introducing the `lenny_tenant_storage_quota_bytes` gauge as the exported denominator, but §17.7 was not updated in the same iteration. This is the same defect iter6 OBS-038 identified in §16.5 — now repaired there but still present here.

**Impact:** A reader following the "Trigger" field to deploy a local PrometheusRule (or to add the alert to an external alerting system) will copy an un-parseable expression. Because §16.5 is the canonical source, the §17.7 mismatch is "only" internal documentation drift — but the drift violates `feedback_docs_sync_after_spec_changes.md` applied to spec-internal cross-references.

**Recommended fix:** Update `spec/17_deployment-topology.md:820` to cite the §16.5 alert expression (corrected per OBS-039 — both the `on(tenant_id) group_left` removal and the `and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` guard fold-in):

```
`(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0`
```

Also replace the subsequent bare-`storageQuotaBytes` reference in the same paragraph ("would cross 90% of `storageQuotaBytes`") with "would cross 90% of `lenny_tenant_storage_quota_bytes`" for internal consistency — or, where `storageQuotaBytes` is deliberately the *config field* under discussion (the raise-the-quota path), keep the config-field form but clarify.

**Severity rationale:** Medium. Parity with OBS-038 from iter6 — same defect class; here flagged on a distinct surface that iter6 missed.

---

### OBS-043 — Missing PromQL guard for active-legal-hold precondition in `LegalHoldCheckpointAccumulationProjectedBreach` [Low]

**Location:** `spec/16_observability.md:488`.

**Issue:** The §16.5 prose preamble restricts the alert to "any tenant with `lenny_tenant_legal_hold_active_count > 0`", but the expression itself does not encode that restriction — it relies on `lenny_legal_hold_checkpoint_projected_growth_bytes` being 0 (and the summed term being 0) for tenants without holds. If the gauge controller ever emits stale series for tenants that used to have holds (e.g., slow cleanup after hold release), the alert will fire for a tenant with no active holds, contradicting the prose and the runbook's action script (which assumes active holds exist).

**Impact:** Low — the dependency on the gauge controller being strictly hold-scoped is implicit and subtle; the `lenny_tenant_legal_hold_active_count` gauge exists (spec `16_observability.md:204`) precisely so this guard can be encoded. Not encoding it is a latent bug that could surface during a controller refactor.

**Recommended fix:** Fold into the OBS-039 fix — the suggested expression `(… growth) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` encodes the guard.

**Severity rationale:** Low — robustness improvement; the current expression is *functionally correct under current controller semantics*, merely not defensive against future regressions. Parity with iter4/iter5 Low findings classified as "latent" defects.

---

### OBS-044 — `StorageQuotaHigh` and `CheckpointStorageHigh` remain prose-only despite iter6 dependency fix [Low]

**Location:** `spec/16_observability.md` §16.5 table rows for `StorageQuotaHigh` (line 459) and `CheckpointStorageHigh` (line 460).

**Issue:** The §16.1 gauge row for `lenny_tenant_storage_quota_bytes` (line 202) explicitly names `StorageQuotaHigh` and `CheckpointStorageHigh` among the alerts intended to use it as their denominator ("`StorageQuotaHigh`, `CheckpointStorageHigh`, and `LegalHoldCheckpointAccumulationProjectedBreach` alert expressions"). However, the §16.5 rows for `StorageQuotaHigh` and `CheckpointStorageHigh` still describe the trigger in prose ("bytes used exceeds 80% of `storageQuotaBytes`" and similar) rather than as a PromQL expression leveraging the new gauge. Iter6 OBS-038 unblocked this rewrite; the rewrite was not done.

**Impact:** Low — the PromQL is straightforward (`lenny_storage_quota_bytes_used > 0.8 * lenny_tenant_storage_quota_bytes` and analogous for checkpoint) and deploying the alert catalog will not fail rendering because these rows are already prose (not claimed as PromQL). But it leaves two §16.5 rows inconsistent with their §16.1 gauge-declaration premise and perpetuates the prose-vs-PromQL ambiguity that OBS-038 set out to resolve.

**Recommended fix:** Rewrite the Trigger fields for `StorageQuotaHigh` and `CheckpointStorageHigh` to explicit PromQL (e.g., `lenny_storage_quota_bytes_used > 0.8 * lenny_tenant_storage_quota_bytes` sustained for > N seconds) citing the new gauge.

**Severity rationale:** Low — cleanup/consistency improvement, no deployment blocker. Parity with iter4/iter5 "prose-only alert trigger" Low findings.

---

### OBS-045 — §4.9 user-revoke handler prose does not cite `lenny_user_credential_revoked_with_active_leases` emission site [Low]

**Location:** `spec/04_system-components.md` §4.9 (around line 1348 and the `user_credential_revoked` handler block starting ~line 1658).

**Issue:** The pool-scoped `lenny_credential_revoked_with_active_leases` gauge has its emission site documented in the revoke handler prose; the new user-scoped counterpart `lenny_user_credential_revoked_with_active_leases` (introduced in iter6 per CRD-020) does not have a parallel emission citation in the user-revoke handler block. The gauge itself is defined in §16.1.1 (`spec/16_observability.md:59`) and the alert in §16.5 (line 412), so the contract is unambiguous — but the §4.9 prose should document the emission site symmetrically with the pool-scoped gauge.

**Impact:** Low — purely a documentation-symmetry issue. The alert still fires correctly from the existing handler because the code-level emission is implicit from the gauge definition; this finding only flags that §4.9 prose doesn't explicitly call it out.

**Recommended fix:** Add a one-sentence cross-reference in the user-credential revoke handler block stating that on revocation the handler decrements the user's active-lease count and any non-zero residual is reflected by `lenny_user_credential_revoked_with_active_leases{tenant_id, provider}`, citing §16.1.1 and §16.5 `CredentialCompromised`.

**Severity rationale:** Low — parity with prior iter5 §4.x cross-citation Low findings.

---

## Convergence assessment

**Not converged** for perspective 12.

- **4 Medium** findings remain: **OBS-039** (malformed PromQL in OBS-038's iter6 fix — deployment-blocker), **OBS-040** (docs drift on `LegalHoldCheckpointAccumulationProjectedBreach`), **OBS-041** (docs drift on `QuotaFailOpenUserFractionInoperative`), **OBS-042** (spec/17 drift on same alert). OBS-039 is a regression in the iter6 fix itself and must be corrected before the §16.5 → PrometheusRule single-source-of-truth rendering can succeed. OBS-040/041/042 are drift sites that violate `feedback_docs_sync_after_spec_changes.md`.
- **3 Low** findings: OBS-043 (robustness guard), OBS-044 (prose-only alert cleanup), OBS-045 (§4.9 cross-citation symmetry) — non-blocking but should be cleared before convergence declaration.

Delta vs. iter6: 0 Critical, 0 High, 4 Medium (vs. iter6's 2 Medium OBS-037/OBS-038 + 1 Medium docs-drift), 3 Low. Distribution is consistent with the iter5→iter6 severity rubric (no drift). Next iteration should block on OBS-039 and can close OBS-040/041/042 alongside it as co-located edits.

---

## Report

- **PERSPECTIVE:** 12 — Observability & Operational Monitoring
- **CATEGORY:** OBS
- **NEW FINDINGS:** 7 (0 Critical, 0 High, 4 Medium, 3 Low)
- **FILE:** `/Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter7/p12_observability.md`
- **CONVERGED:** No — 4 Medium issues open (OBS-039 PromQL regression, OBS-040/041 docs drift, OBS-042 spec/17 drift).
