# Document-quality Review — Iteration 8 (Regressions-only scope)

**Scope directive (iter8+):** Review ONLY regressions introduced by the previous fix commit `bed7961`. Pre-existing issues surviving from prior iterations are out of scope for this pass.

**Previous fix commit reviewed:** `bed7961` (iter7 fix — addressed SEC-017, SEC-018, KIN-028, OBS-039/040/041/042, CMP-063, API-026/027/028, DOC-031/032).

**Regression check performed:** verified intra-file and cross-file anchor resolution for all new anchor references; confirmed section numbering and layer-count narratives in newly added subsections; validated table column counts in newly added or expanded rows (§11.6 and §15.1 circuit-breakers tables, admin.md circuit-breakers table, §16.1 metric tables, §16.5 alert tables, error-catalog PERMANENT / POLICY tables); checked markdown link syntax in new cross-references (`#124-redis-ha-and-failure-modes`, `#141-workspaceplan-schema-versioning`, `#165-alerting-rules-and-slos`, `#177-operational-runbooks`, `#179-deployment-answer-files`, `#1785-mandatory-lenny-ops-deployment`); confirmed new narrative additions are anchored in the correct subsections (SEC-017 ephemeral-container cred guard in §13 and §17.2; SEC-018 elicitation content integrity in §9.2; KIN-028 feature-flag downgrade enforcement in §17.2; circuit-breaker admin tooling in §11.6, §15.1, §25).

## Findings

### DOC-033 (Medium) — §17.2 "Feature-flag downgrade enforcement" subsection: narrative says "three cooperating layers" but enumerates four numbered items

**File:** `spec/17_deployment-topology.md` §17.2 "Namespace Layout", sub-section "Feature-flag downgrade enforcement" (lines 72–80)

**Introduced by:** iter7 KIN-028 fix (commit `bed7961`).

**Regression:** The newly added sub-section describing the KIN-028 enforcement mechanism contains an internal counting / narrative inconsistency between the prose framing and the numbered list that follows it.

Specifically:

- The **section heading** at line 72 is framed as a triad:
  > **Feature-flag downgrade enforcement (phase-stamp ConfigMap + render-time validation + runtime alert).**
  — three elements are named in the parenthetical.

- The **leader paragraph** at line 68 (just above the sub-section) is also framed as a triad:
  > …the downgrade prohibition is **actively enforced at three layers** — persisted phase-stamp ConfigMap, fail-closed chart render-time validation, and a runtime `AdmissionPlaneFeatureFlagDowngrade` Warning alert — per the "Feature-flag downgrade enforcement" sub-section below.

- The **sub-section lead** at line 72 then reiterates the same count twice:
  > …is actively enforced by **three cooperating layers**; an operator attempting a downgrade hits the render-time fail-closed gate before the upgrade reaches the cluster, and any silent drift that bypasses the chart… is caught by the runtime alert. **The three layers are:**

- The **numbered list** at lines 74–80 then enumerates **four** items:
  1. Persisted phase-stamp ConfigMap (`lenny-deployment-phase-stamp`)
  2. Fail-closed chart render-time validation
  3. `lenny-preflight` mismatch check (catches out-of-band mutation between phase-stamp and Helm render)
  4. `AdmissionPlaneFeatureFlagDowngrade` Warning alert

A reader counting the numbered items reaches 4, which contradicts the declared count of 3 that appears in three separate places in the surrounding narrative and in the sub-section heading itself. Neither the section heading nor the prose mentions the `lenny-preflight` mismatch check as one of the layers, yet it is listed as item (3) and is clearly structurally parallel to the other three (it has its own error code `PREFLIGHT_PHASE_STAMP_MISMATCH`, a distinct runtime trigger, and an independent failure mode — "narrow race window where `helm template` / `helm install --dry-run` succeeds... but the live cluster's phase-stamp still recorded a `true` flag at the moment preflight runs").

This is consistent with the iter7 summary description of the KIN-028 fix, which refers to it as "three-layer enforcement" while separately listing "(4) added PREFLIGHT_PHASE_STAMP_MISMATCH preflight Job check" — suggesting the preflight check was added as an additional belt-and-braces measure without reconciling the layer-count narrative.

**Impact (Medium):**
- Factual ambiguity in newly added spec text — readers cannot determine whether there are 3 or 4 enforcement layers.
- Testing scope risk: a reader aligning integration tests with the "three layers" framing might omit a dedicated test for the preflight-mismatch layer, relying on the chart render-time validation and runtime alert tests to cover it; but the preflight layer has its own distinct error code (`PREFLIGHT_PHASE_STAMP_MISMATCH`) and race-window semantics ("helm template succeeds… but the live cluster's phase-stamp still recorded a true flag at the moment preflight runs") which the other two layers do NOT cover. The inconsistency can therefore leak into test-coverage planning.
- Cross-references from `docs/runbooks/admission-plane-feature-flag-downgrade.md` (mentioned at line 80) and the operator guide are likely to inherit whichever framing their author reads first, propagating the inconsistency into operator-facing material.
- The section heading parenthetical "(phase-stamp ConfigMap + render-time validation + runtime alert)" will mislead operators grepping the spec for the `lenny-preflight` Job's contribution.

This is a regression (not a pre-existing issue) because the entire "Feature-flag downgrade enforcement" sub-section is new content introduced by the iter7 fix commit `bed7961` — it did not exist in the iter6-post-fix spec.

**Recommended remediation (either path closes the regression):**

- **Option A — unify on four layers** (preferred; preserves all four structurally distinct enforcement surfaces):
  - Change the sub-section heading from "(phase-stamp ConfigMap + render-time validation + runtime alert)" to "(phase-stamp ConfigMap + render-time validation + preflight mismatch check + runtime alert)".
  - In the §17.2 leader paragraph at line 68, change "**actively enforced at three layers** — persisted phase-stamp ConfigMap, fail-closed chart render-time validation, and a runtime `AdmissionPlaneFeatureFlagDowngrade` Warning alert —" to "**actively enforced at four layers** — persisted phase-stamp ConfigMap, fail-closed chart render-time validation, `lenny-preflight` mismatch check, and a runtime `AdmissionPlaneFeatureFlagDowngrade` Warning alert —".
  - In the sub-section lead at line 72, change both occurrences ("three cooperating layers" and "The three layers are:") to "four cooperating layers" and "The four layers are:".
  - Audit `docs/runbooks/admission-plane-feature-flag-downgrade.md` and `docs/operator-guide/configuration.md` for the same framing and update correspondingly.

- **Option B — subsume preflight under render-time validation** (preserves the "three layers" framing by re-structuring the numbered list):
  - Merge current items (2) and (3) into a single composite layer (2) titled "Fail-closed render-time + preflight validation", split into two lettered sub-points (2a) chart render-time `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` gate and (2b) `lenny-preflight` `PREFLIGHT_PHASE_STAMP_MISMATCH` gate, explicitly noting they cover distinct race windows but share the same `acceptFeatureFlagDowngrade.<flag>=true` override. Renumber current item (4) to (3).
  - Rationale for preferring Option A: the iter7 summary explicitly describes the preflight check as "(4) added", and the preflight Job's race-window semantics are materially distinct from the `helm template` render-time check — treating them as a single composite layer understates the defense-in-depth structure.

**Scope note:** This is the only regression detected in the iter7 fix commit from the DOC perspective. All anchor references resolve (both the new `#124-redis-ha-and-failure-modes` and `#141-workspaceplan-schema-versioning` and existing cross-links); table column counts remain internally consistent across the expanded circuit-breakers and error-catalog tables; webhook-inventory count updates (8 → 9) are applied consistently in `spec/13_security-model.md` line 221 and `spec/17_deployment-topology.md` §17.2; new alert rows in §16.5 maintain the 3-column (name / expression / severity) shape; new error rows in the error catalog maintain the 4-column (code / HTTP status / category / description) shape.
