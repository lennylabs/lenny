# Perspective 12 — Observability & Operational Monitoring — Iteration 8 Findings (regressions-only)

**Scope (per iter8 directive):** Review regressions introduced by fix commit `bed7961` only. Prior-iteration baselines are out of scope.

**Commit surfaces reviewed:**
- `spec/16_observability.md` — §16.1 new metric `lenny_elicitation_content_tamper_detected_total` (line 64); §16.5 new alerts `ElicitationContentTamperDetected` (line 434), `EphemeralContainerCredGuardUnavailable` (line 479), `AdmissionPlaneFeatureFlagDowngrade` (line 480); OBS-039 canonical PromQL fix at line 492; DOC-031 anchor at line 204; §16.7 new audit event `elicitation.content_tamper_detected` (line 664)
- `spec/17_deployment-topology.md` — §17.2 `lenny-ephemeral-container-cred-guard` webhook (line 54); "Feature-flag downgrade enforcement" subsection including second copy of `AdmissionPlaneFeatureFlagDowngrade` expression (line 80); §17.7 canonical PromQL re-sync (line 834) and new runbook stub (lines 838–841)
- `docs/operator-guide/observability.md` — new alert rows at lines 187–189; canonical PromQL (line 193); POL-034 row (line 192)
- `docs/reference/metrics.md` — new metric row at line 251

Severity rubric anchored to iter1–iter7 baseline per `feedback_severity_calibration_iter5.md`.

---

## New findings (bed7961 regressions only)

### OBS-043 — `AdmissionPlaneFeatureFlagDowngrade` alert defined twice with materially inconsistent PromQL expressions (spec/16 vs. spec/17) [Medium] **[Fixed]**

**Resolution:** Closed by KIN-040 fix. §16.5 canonical expression now uses `kube_configmap_labels{configmap="lenny-deployment-phase-stamp", label_lenny_dev_flag_<slug>_enabled="true"} unless on() kube_validatingwebhookconfiguration_info{name="<webhook>"}` with one explicit PrometheusRule per `(flag, webhook)` pair (four pairs total — `lenny-direct-mode-isolation`, `lenny-drain-readiness`, `lenny-data-residency-validator`, `lenny-t4-node-isolation`). The RHS label is `name` (kube-state-metrics' real label), not `webhook_name`. §17.2 layer 4 reduced to a forward-reference pointing at §16.5 as the single source of truth. Literal `label_replace(..., ...)` ellipsis removed. `docs/operator-guide/configuration.md` now documents the mandatory `kube-state-metrics --metric-labels-allowlist=configmaps=[lenny.dev/flag-*]` operator precondition. `docs/operator-guide/observability.md:188` row references the canonical §16.5 expression form and the allowlist requirement.

**Locations:**
- `spec/16_observability.md:480` — §16.5 alerts table row (canonical site for alert definitions per this spec's single-source-of-truth rendering rule).
- `spec/17_deployment-topology.md:80` — §17.2 "Feature-flag downgrade enforcement" point 4.

**Issue:** The KIN-028 fix in `bed7961` added the new `AdmissionPlaneFeatureFlagDowngrade` alert to both §16.5 and §17.2, but the two expressions are materially different and not PromQL-equivalent:

§16.5 (line 480):
```
(kube_configmap_info{namespace="{{ .Release.Namespace }}", configmap="lenny-deployment-phase-stamp"} == 1)
  unless on()
  (kube_validatingwebhookconfiguration_info{name="<flag-gated-webhook-name>"} == 1)
```
— exported-label mechanism described as a chart-rendered `kube_configmap_labels` annotation `lenny.dev/flag-<flag>-enabled: "true"`; webhook match on kube-state-metrics' real label `name`; flag-labels derived from `kube_configmap_labels` (not shown in the expression).

§17.2 (line 80):
```
(kube_configmap_info{namespace="{{ .Release.Namespace }}", configmap="lenny-deployment-phase-stamp"}
  * on() group_right() label_replace(..., "flag_enabled", "true", "flag_name", "<flag>"))
  unless on(webhook_name)
  (kube_validatingwebhookconfiguration_info{webhook_name=~"<flag-to-webhook regex>"})
```
— uses `label_replace(..., ...)` with an ellipsis in the first argument (not a valid PromQL expression — it's a placeholder, not a template substitution that a PrometheusRule renderer can resolve); webhook match uses `{webhook_name=~"..."}` and `unless on(webhook_name)`, but kube-state-metrics does **not** emit a `webhook_name` label on `kube_validatingwebhookconfiguration_info` — the label is `name`. The §16.5 form acknowledges this (and uses `name=`); the §17.2 form does not.

The two expressions disagree on (a) the match operator (`==1` + `unless on()` vs. `* on() group_right() label_replace`), (b) the vector-matching axis (empty `on()` vs. `on(webhook_name)`), (c) the kube-state-metrics label for the webhook name (`name` vs. the non-existent `webhook_name`), and (d) the mechanism for lifting the flag identity into alert labels (`kube_configmap_labels` side-channel annotations vs. `label_replace`).

**Impact:** Material regression. Two consequences:
1. If the PrometheusRule is rendered from §16.5 (the declared canonical source), the §17.2 expression is reader-facing documentation that does not match the rendered rule — operators following §17.2 to debug or mirror the alert into an external system will hit a non-existent `webhook_name` label and an un-renderable `label_replace(..., ...)` placeholder.
2. If a chart author reads §17.2 as the reference (because it's in the "Feature-flag downgrade enforcement" enforcement section — the place KIN-028 directed attention to), they will ship a PrometheusRule that fails validation (the `...` ellipsis is not valid PromQL) or, if substituted, queries a non-existent label.

Per the `feedback_docs_sync_after_spec_changes.md` invariant applied intra-spec, a single alert must have one authoritative expression.

**Recommended fix:** Designate §16.5 as canonical per existing convention and reduce the §17.2 point-4 block to a short forward-reference ("Expression: see §16.5 `AdmissionPlaneFeatureFlagDowngrade` — expression compares phase-stamp ConfigMap flag entries against installed ValidatingWebhookConfiguration resources using kube-state-metrics …"). Alternatively, if a concrete expression is required in §17.2 for in-section completeness, copy the §16.5 form verbatim, including the `name="<flag-gated-webhook-name>"` match and the `kube_configmap_labels` label-exporting mechanism. Replace `label_replace(..., "flag_enabled", "true", "flag_name", "<flag>")` ellipsis with either (a) the removed expression form or (b) a fully-expanded `label_replace(kube_configmap_info{...}, ...)` with all four arguments bound.

**Severity rationale:** Medium. Parity with iter7 OBS-039 (malformed PromQL in a spec-level alert body, Medium). This finding is the iter8 analogue: bed7961 introduced the alert in two places, and only one of the two copies is renderable PromQL. The defect is in the body of an alert expression that spec §17.2 directs an operator to inspect during KIN-028 remediation, so it is not cosmetic.

---

### OBS-044 — Docs drift: `docs/reference/metrics.md` still carries the iter6 pre-fix `LegalHoldCheckpointAccumulationProjectedBreach` PromQL with invalid `on(tenant_id) group_left` on a scalar-vector product [Medium] **[Fixed]**

**Resolution:** Updated `docs/reference/metrics.md:510` row for `LegalHoldCheckpointAccumulationProjectedBreach` to carry the canonical spec §16.5 form: `(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0`. Regression-checked: zero remaining occurrences of `0.9 * on(tenant_id) group_left` in `docs/`; canonical form matches `spec/16_observability.md:492`, `spec/17_deployment-topology.md:834`, and `docs/operator-guide/observability.md:193` character-for-character.

**Location:** `docs/reference/metrics.md:510`.

**Issue:** The fix commit `bed7961` rewrote the `LegalHoldCheckpointAccumulationProjectedBreach` alert expression in three places (spec §16.5 line 492, spec §17.7 line 834, and `docs/operator-guide/observability.md` line 193) to remove the invalid `0.9 * on(tenant_id) group_left lenny_tenant_storage_quota_bytes` scalar-vector modifier and add the `and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` guard — but it left the `docs/reference/metrics.md` Warning-alerts-table row carrying the **old, invalid** expression:

```
(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes))
  > 0.9 * on(tenant_id) group_left lenny_tenant_storage_quota_bytes
```

This is the same defect iter7 OBS-039 flagged in spec §16.5: `on()` / `group_left` matching modifiers are not permitted on scalar-vector arithmetic (parse-error in PromQL), and iter7 OBS-040 called out the operator-guide's stale expression as Medium docs drift. The commit synced the operator-guide but missed `docs/reference/metrics.md`. The spec now explicitly states "such modifiers are only valid on vector-vector binary operations and are not permitted on scalar-vector arithmetic" (spec §16.5 line 492), yet the reference metrics doc still carries the exact form the spec disavows.

The commit message advertises "Docs sync across docs/api, docs/operator-guide, docs/runtime-author-guide, docs/reference," and the commit itself touches `docs/reference/metrics.md` (to add the new `lenny_elicitation_content_tamper_detected_total` metric row), so this file was on the sync surface but was only partially updated.

**Impact:** Readers of the authoritative Lenny metrics reference will copy an un-parseable PromQL expression into external alerting systems, dashboards, or troubleshooting scripts. The alert will not deploy as written; the canonical spec form will have to be re-consulted. This is the same `feedback_docs_sync_after_spec_changes.md` invariant violation iter7 OBS-040 flagged on a neighbouring doc surface.

**Recommended fix:** Update `docs/reference/metrics.md:510` to carry the canonical spec §16.5 form:

```
| `LegalHoldCheckpointAccumulationProjectedBreach` | `(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` — predictive alert that a tenant's projected 24-hour legal-hold checkpoint growth plus current usage will cross 90% of the tenant's configured `storageQuotaBytes` bucket; gated on at least one active legal hold. See [legal-hold-quota-pressure](../runbooks/legal-hold-quota-pressure.html). | Warning |
```

**Severity rationale:** Medium. Direct parity with iter7 OBS-040 (same defect class: stale pre-fix PromQL in a docs surface the canonical spec has since corrected). The commit explicitly claimed `docs/reference` sync in its message; incomplete sync is the regression.

---

### OBS-045 — Docs drift: three new §16.5 alerts added in `bed7961` are absent from `docs/reference/metrics.md` Critical/Warning alerts catalog [Medium] [Fixed]

**Fix applied:** Added the three rows to `docs/reference/metrics.md`:
- `ElicitationContentTamperDetected` (Critical) inserted after `T4KmsKeyUnusable`; expression `increase(lenny_elicitation_content_tamper_detected_total[5m]) > 0` with `{message, schema}` vocabulary (post-SES-011) and `origin_pod` / `tampering_pod` label documentation.
- `EphemeralContainerCredGuardUnavailable` (Warning) inserted after `DrainReadinessWebhookUnavailable`; expression `up{job="lenny-ephemeral-container-cred-guard"} == 0` sustained > 5 min, with fail-closed credential-boundary rationale.
- `AdmissionPlaneFeatureFlagDowngrade` (Warning) inserted after `EphemeralContainerCredGuardUnavailable`; summary description with a pointer to spec §16.5 for the full four-pair expression introduced by the KIN-040 fix.

The pre-existing `lenny_elicitation_content_tamper_detected_total` metric row's "Used by" reference now resolves to an actual alert-catalog row.


**Location:** `docs/reference/metrics.md` §441 "Critical alerts" and §478 "Warning alerts" tables.

**Issue:** `bed7961` introduced three new alerts in spec §16.5:
- `ElicitationContentTamperDetected` (Critical) — line 434
- `EphemeralContainerCredGuardUnavailable` (Warning) — line 479
- `AdmissionPlaneFeatureFlagDowngrade` (Warning) — line 480

and added rows for all three to `docs/operator-guide/observability.md` (lines 187–189). However, none of the three appear in the `docs/reference/metrics.md` alert catalogue (neither the Critical alerts table, lines 444–476, nor the Warning alerts table, lines 482–525). The new metric `lenny_elicitation_content_tamper_detected_total` row at `docs/reference/metrics.md:251` references "`ElicitationContentTamperDetected` critical alert" in the "Used by" column, but following that pointer leads to an absent row in the same file.

Convention in this repository: every alert defined in spec §16.5 has a matching row in `docs/reference/metrics.md`'s Critical-alerts or Warning-alerts section. Existing analogues confirm this: `DrainReadinessWebhookUnavailable`, `CrdConversionWebhookUnavailable`, `T4NodeIsolationWebhookUnavailable`, `LegalHoldEscrowResidencyViolation`, `PlatformAuditResidencyViolation`, `CompliancePostureDecommissioned`, and `QuotaFailOpenUserFractionInoperative` — each added in earlier iterations — all carry catalog rows. The three `bed7961`-introduced alerts do not.

**Impact:** Operators treating `docs/reference/metrics.md` as the authoritative single-file alert catalog will not learn of:
- A Critical tamper-detection alert that pages on hostile/prompt-injected runtime attempts to rewrite elicitation text (security-salient, steady-state zero).
- A Warning webhook-availability alert for the new SEC-017 credential-boundary admission webhook.
- The sole runtime signal for KIN-028 admission-plane feature-flag downgrade drift (the paired `*Unavailable` alerts are silent by design in that failure mode).

This is the same class of defect as iter6/iter7 docs-sync findings on the same file surface.

**Recommended fix:** Add the three rows to `docs/reference/metrics.md`:

In the Critical alerts table (after the existing `T4KmsKeyUnusable` row at line 476):
```
| `ElicitationContentTamperDetected` | `increase(lenny_elicitation_content_tamper_detected_total[5m]) > 0` — intermediate pod attempted to forward an existing `elicitation_id` with diverging `{title, description, schema, inputs}`; forward dropped with `ELICITATION_CONTENT_TAMPERED` per the gateway-origin-binding invariant (§9.2). Fires immediately on any non-zero increment. Labels `origin_pod`, `tampering_pod`. | Critical |
```

In the Warning alerts table (grouped with the other `*WebhookUnavailable` / admission-plane entries around line 524):
```
| `EphemeralContainerCredGuardUnavailable` | `up{job="lenny-ephemeral-container-cred-guard"} == 0` sustained > 5 min; `pods/ephemeralcontainers` updates denied in agent namespaces until webhook recovers (credential-boundary invariant remains protected by fail-closed policy). | Warning |
| `AdmissionPlaneFeatureFlagDowngrade` | `lenny-deployment-phase-stamp` ConfigMap records `enabled: true` for a feature flag but the corresponding ValidatingWebhookConfiguration is absent > 2 min. Sole runtime signal for feature-flag downgrade drift because gated `*Unavailable` alerts fall silent with the flag. Labels `flag_name`, `expected_webhook_name`. | Warning |
```

**Severity rationale:** Medium. Parity with iter7 OBS-040/041 (docs-sync gaps on `docs/operator-guide/observability.md` for prior-iteration-introduced alerts). Three new alerts missing from the authoritative reference catalog is a larger surface than any single docs-drift finding in iter7, but each individual miss is a Medium-severity operator-visibility gap rather than a High-severity deployment-breaker. The commit explicitly claimed `docs/reference` sync; the missing catalog rows are the regression.

---

## Convergence assessment

**Not converged** for perspective 12.

- **3 Medium** regressions introduced by `bed7961`:
  - OBS-043: `AdmissionPlaneFeatureFlagDowngrade` PromQL duplicated with two materially inconsistent forms between spec §16.5 and §17.2.
  - OBS-044: `docs/reference/metrics.md:510` still carries the iter6 pre-fix `LegalHoldCheckpointAccumulationProjectedBreach` expression with the invalid `on(tenant_id) group_left` scalar-vector modifier that `bed7961` removed from the spec and operator-guide.
  - OBS-045: Three `bed7961`-introduced §16.5 alerts (`ElicitationContentTamperDetected`, `EphemeralContainerCredGuardUnavailable`, `AdmissionPlaneFeatureFlagDowngrade`) have no rows in the `docs/reference/metrics.md` Critical/Warning alerts catalog.

All three are `feedback_docs_sync_after_spec_changes.md` violations scoped to surfaces the commit's own message advertised as having been synced ("Docs sync across docs/api, docs/operator-guide, docs/runtime-author-guide, docs/reference"). OBS-043 additionally violates the single-source-of-truth rendering requirement for alert definitions.

No Critical or High regressions detected. No Low or Info regressions flagged per iter8 scope directive.

Next iteration should block on OBS-043 (PrometheusRule renderability) and close OBS-044/045 as co-located `docs/reference/metrics.md` edits.

---

## Report

- **PERSPECTIVE:** 12 — Observability & Operational Monitoring
- **CATEGORY:** OBS
- **NEW FINDINGS:** 3 (0 Critical, 0 High, 3 Medium; Low/Info suppressed per iter8 regressions-only scope)
- **FILE:** `/Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter8/p12_observability.md`
- **CONVERGED:** No — 3 Medium regressions open (OBS-043 duplicated-inconsistent alert expression, OBS-044 stale PromQL in metrics reference, OBS-045 missing alert rows in metrics reference).
