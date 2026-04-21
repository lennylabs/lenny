# Technical Design Review Findings — 2026-04-21 (Iteration 8)

**Document reviewed:** `spec/` (28 chapter files)
**Review framework:** 25 review perspectives (iter1 baseline)
**Iteration:** 8 of N
**Scope:** **Regressions-only** relative to iter7 fix commit `bed7961`. Pre-existing issues and long-lived Low/Info carry-forwards are out of scope per `feedback_iter8_regressions_only.md`.
**Perspectives dispatched:** 11 of 25 (those whose primary surfaces intersect the `bed7961` fix envelope). The remaining 14 perspectives (NET, SCP, DEV, OPS, MTI, STO, DEL, COM, WPL, PHS/BLD, EXP, MSG, EXM, and the already-converged CMP/SEC/POL that reported 0) are considered 0-finding under the scope directive.
**Total new regressions:** 15 Medium (0 Critical, 0 High, 15 Medium). Some findings are cross-perspective duplicates of the same underlying defect.

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 15    |
| Low      | 0 (suppressed) |
| Info     | 0 (suppressed) |

### Medium Findings

| #   | ID      | Title                                                                                          | Section                                              | Dup of |
| --- | ------- | ---------------------------------------------------------------------------------------------- | ---------------------------------------------------- | ------ |
| 1   | KIN-039 | Preflight row 513 "Admission webhook inventory" baseline missing `lenny-ephemeral-container-cred-guard` | `spec/17_deployment-topology.md` §17.9              |        |
| 2   | KIN-040 | `AdmissionPlaneFeatureFlagDowngrade` PromQL fires false positives (LHS uses `kube_configmap_info` without `flag_enabled` label) and §16/§17 expressions diverge | `spec/16_observability.md` §16.5 / `spec/17_deployment-topology.md` §17.2 |        |
| 3   | KIN-041 | Render-time guard overclaims `helm template --dry-run` enforcement (Helm `lookup` returns empty map outside install/upgrade) | `spec/17_deployment-topology.md` §17.2 layer 2 + `docs/operator-guide/configuration.md:498` |        |
| 4   | SES-011 | §9.2 elicitation content-integrity paragraph uses `{title, description, schema, inputs}` vocabulary and `lenny/request_elicitation` re-issuance wire model unreconcilable with §8.5 authoritative tool schema `{message, schema}` | `spec/09_mcp-integration.md` §9.2                     |        |
| 5   | CRD-029 | `lenny-ephemeral-container-cred-guard` webhook rejection conditions only inspect container-level `securityContext`; pod-level `fsGroup` unconditionally grants `lenny-cred-readers` supplementary-group to ephemeral containers regardless | `spec/13_security-model.md` §13.1                     |        |
| 6   | OBS-043 | `AdmissionPlaneFeatureFlagDowngrade` alert defined twice in §16.5 and §17.2 with materially inconsistent PromQL (empty `on()` vs `on(webhook_name)`; `name=` vs `webhook_name=~`; literal `label_replace(..., ...)` ellipsis) | `spec/16_observability.md:480` / `spec/17_deployment-topology.md:80` | KIN-040, FMR-023 |
| 7   | OBS-044 | `docs/reference/metrics.md:510` still carries the pre-fix `LegalHoldCheckpointAccumulationProjectedBreach` PromQL with invalid `0.9 * on(tenant_id) group_left lenny_tenant_storage_quota_bytes` scalar-vector modifier that `bed7961` removed everywhere else | `docs/reference/metrics.md:510`                       |        |
| 8   | OBS-045 | Three new §16.5 alerts (`ElicitationContentTamperDetected`, `EphemeralContainerCredGuardUnavailable`, `AdmissionPlaneFeatureFlagDowngrade`) absent from `docs/reference/metrics.md` alert catalog | `docs/reference/metrics.md` §441/§478                 |        |
| 9   | API-029 | `POST /v1/admin/circuit-breakers/{name}/close` 404-on-unknown-name documented in docs & §15.1 dryRun but authoritative §15.1 real-call row and §11.6 row silent about it | `spec/15_external-api-surface.md:887`, `spec/11_policy-and-controls.md:313`, `docs/api/admin.md:764` |        |
| 10  | API-030 | `POST /v1/admin/circuit-breakers/{name}/open` dryRun row asserts "mirrors the real-call shape" then enumerates only 5 of 8 real-call fields (missing `opened_at`, `opened_by_sub`, `opened_by_tenant_id`) | `spec/15_external-api-surface.md:1192`                |        |
| 11  | CNT-030 | §17.9 preflight check lists 4 baseline webhooks; §17.2 narrative (`line 68/82/84`) says baseline is 5 — one-side staleness after SEC-017 promoted cred-guard to baseline | `spec/17_deployment-topology.md:513` vs §17.2        | KIN-039 |
| 12  | CNT-031 | `docs/runbooks/admission-plane-feature-flag-downgrade.md` referenced 3× but file does not exist; `docs/runbooks/index.md` not updated with the 3 new iter7 alerts | `spec/17_deployment-topology.md:80`, `:201` & `docs/operator-guide/observability.md:188` |        |
| 13  | DOC-033 | §17.2 "Feature-flag downgrade enforcement" heading parenthetical and leader paragraph say "three layers" but enumerate 4 items (phase-stamp + render-time + preflight + runtime alert) | `spec/17_deployment-topology.md` §17.2 lines 68–80    |        |
| 14  | FMR-022 | §17.7 runbook Remediation step 4 references non-existent "Admission webhook storm" runbook entry and undefined `admin.freezeAdmission` control | `spec/17_deployment-topology.md:841`                  |        |
| 15  | FMR-024 | Runbook Diagnosis step 4 pivots on audit event `deployment.feature_flag_downgrade_acknowledged` that is referenced 6× across spec + docs but never defined in §16.7 or §11.7 | `spec/17_deployment-topology.md:840`                  |        |

**FMR-023** is subsumed by KIN-040/OBS-043 (same underlying PromQL divergence) and will be addressed by the same fix.

---

## Findings by Perspective

### 1. Kubernetes-Native Design (KIN) — 3 Medium

See `p1_kubernetes.md`. **KIN-039** preflight baseline omission; **KIN-040** AdmissionPlaneFeatureFlagDowngrade PromQL defect (fires on fresh installs where ConfigMap exists but no flag is enabled, because `kube_configmap_info` has no label encoding per-flag `enabled` state; §17.2 variant references non-existent `webhook_name` label on `kube_validatingwebhookconfiguration_info`); **KIN-041** GitOps render-time guard overclaim.

### 2. Security (SEC) — 0 regressions

See `p2_security.md`. Cred-guard webhook narrative, elicitation content-integrity invariant, new metric/alert/audit-event, and two new PERMANENT error codes are internally consistent. All docs-sync surfaces reached.

### 11. Session / Protocol (SES) — 1 Medium

See `p11_session.md`. **SES-011** new §9.2 "Elicitation content integrity (gateway-origin binding)" paragraph introduces content vocabulary `{title, description, schema, inputs}` and forward-hop "`lenny/request_elicitation` re-issuance referencing an existing `elicitation_id`" wire model — neither reconciles with §8.5's authoritative `lenny/request_elicitation` tool schema (`{message, schema}` required, no `elicitation_id` input). The contradiction propagates to §15.1 error row, §16.1 metric, §16.5 alert, §16.7 audit event (all reference the undefined fields).

### 12. Observability (OBS) — 3 Medium

See `p12_observability.md`. **OBS-043** duplicated-inconsistent `AdmissionPlaneFeatureFlagDowngrade` PromQL between §16.5 and §17.2; **OBS-044** `docs/reference/metrics.md` still carries the pre-fix malformed `LegalHoldCheckpointAccumulationProjectedBreach` PromQL; **OBS-045** three `bed7961`-introduced §16.5 alerts absent from `docs/reference/metrics.md` catalog.

### 13. Compliance (CMP) — 0 regressions

See `p13_compliance.md`. Three residency fail-closed-mirror codes consolidated cleanly; cross-surface descriptions preserve PERMANENT semantics.

### 14. API Design (API) — 2 Medium

See `p14_api_design.md`. **API-029** `/close` 404 asymmetry; **API-030** `/open` dryRun "mirrors real-call shape" ↔ abbreviated field enumeration contradiction.

### 17. Credentials (CRD) — 1 Medium

See `p17_credential.md`. **CRD-029** cred-guard rejection conditions only inspect container-level `securityContext`; kubelet applies pod-level `fsGroup` to ephemeral containers unconditionally, so an attacker can set explicit non-matching values for `runAsUser`/`runAsGroup`/`supplementalGroups`, pass all three checks, and still inherit `lenny-cred-readers` GID via `fsGroup` — reading `/run/lenny/credentials.json` by group-read.

### 18. Content Consistency (CNT) — 2 Medium

See `p18_content.md`. **CNT-030** preflight baseline-count mismatch (duplicate of KIN-039); **CNT-031** runbook file referenced but missing, index not updated.

### 20. Failure Modes / Runbooks (FMR) — 3 Medium (1 subsumed)

See `p20_failure_modes.md`. **FMR-022** runbook references non-existent "Admission webhook storm" entry + undefined `admin.freezeAdmission` control; **FMR-023** PromQL divergence (subsumed by OBS-043/KIN-040); **FMR-024** audit event `deployment.feature_flag_downgrade_acknowledged` never defined in §16.7 catalog.

### 22. Document Quality (DOC) — 1 Medium

See `p22_document.md`. **DOC-033** §17.2 "three layers" narrative vs four-item enumeration.

### 24. Policy (POL) — 0 regressions

See `p24_policy.md`. Scope taxonomy `tools:circuit_breaker:read|write` consistent across §11.6 ↔ §15.1 ↔ §25.

---

## Cross-Cutting Themes

**1. `AdmissionPlaneFeatureFlagDowngrade` alert is the largest regression cluster.** Four findings (KIN-040, OBS-043, FMR-023, parts of CNT-031/DOC-033) point at the alert's §16.5 vs §17.2 PromQL divergence and the underlying label-schema mismatch. The alert is itself "the SOLE runtime signal for feature-flag downgrade drift" per §16.5, so correctness here is load-bearing.

**2. Feature-flag downgrade enforcement layers are inconsistently counted, enforced, and documented.** KIN-039/CNT-030 (preflight baseline), KIN-041 (`helm template` guard), DOC-033 (three vs four layers), FMR-022/024 (missing runbook entry, undefined audit event) all trace to the iter7 KIN-028 fix having accreted a multi-layer defense without tightening every cross-reference.

**3. `docs/reference/metrics.md` is the largest docs-sync miss.** OBS-044 + OBS-045 show the file was touched (for the new metric row) but not fully synced — the fix commit's message advertised `docs/reference` sync coverage that was only partial.

**4. Content-integrity invariant has a schema-vocabulary contradiction that propagates across five spec sections.** SES-011's `{title, description, schema, inputs}` vs §8.5's `{message, schema}` — and the new forward-hop mechanism has no home in the existing §8.5 or §15.2.1 wire-protocol prose. A faithful implementation is blocked on field-set canonicalization.

**5. Security invariant (cred-guard) is not fully closed by the rejection conditions as written.** CRD-029: pod-level `fsGroup` is kubelet-applied unconditionally and sidesteps container-level `securityContext` inspection. The three admission conditions do not constitute a complete boundary check.

---

## Severity Calibration Note

All 15 new findings calibrated as Medium against the iter1–iter7 rubric (see `feedback_severity_calibration_iter5.md`). No Critical/High because: (a) no deployment-blocker across all supported install modes (GitOps is affected by KIN-041 but cluster-side preflight still catches it eventually), (b) no data-integrity loss, (c) all defects have small textual or configuration fixes. Several findings would have been Low (docs-only stale rows) under iter5 calibration but are categorized Medium here because they (i) sit inside the iter7 fix envelope (regressions) and (ii) violate invariants the iter7 commit message explicitly claimed to have satisfied ("docs sync across docs/api, docs/operator-guide, docs/runtime-author-guide, docs/reference").
