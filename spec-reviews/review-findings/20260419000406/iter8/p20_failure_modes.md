# Iteration 8 — Perspective 20: Failure Modes & Resilience Engineering (regressions-only)

- Category: **FMR**
- Spec root: `/Users/joan/projects/lenny/spec/`
- Prior review: `spec-reviews/review-findings/20260419000406/iter7/p20_failure_modes.md`
- Iter7 fix commit under review: `bed7961`
- Scope directive (iter8+): Review ONLY regressions introduced by `bed7961`. Last FMR finding was FMR-021 (closed by DOC-031); new IDs start at FMR-022.
- Severity calibration: anchored to iter4–iter7 rubric per `feedback_severity_calibration_iter5.md` — "runbook references undefined API/runbook-entry" → Medium when the runbook is in the hot path of a fail-closed admission regression; "PromQL expression drift between two authoritative sites" → Medium when the drift is on a new alert that is the *sole* runtime signal for the failure mode; prose/pseudocode-instead-of-PromQL → Low.

---

## 1. bed7961 FMR surface touched

Per the caller directive, the surfaces reviewed are:

1. `spec/17_deployment-topology.md` §17.2 — `lenny-ephemeral-container-cred-guard` webhook added as item 13; three-layer feature-flag downgrade enforcement added (phase-stamp ConfigMap, render-time validation, preflight check, runtime alert); baseline-webhook tally updated from 4 → 5 / 8 → 9.
2. `spec/17_deployment-topology.md` §17.2 preflight table row 514 — new `Phase-stamp consistency (KIN-028)` check.
3. `spec/17_deployment-topology.md` §17.7 — new runbook stub `Admission-plane feature-flag downgrade` (lines 838–841).
4. `spec/16_observability.md` §16.5 — new Warning alerts `EphemeralContainerCredGuardUnavailable` (line 479) and `AdmissionPlaneFeatureFlagDowngrade` (line 480); `LegalHoldCheckpointAccumulationProjectedBreach` expression corrected (OBS-042); paired `lenny_elicitation_content_tamper_detected_total` counter / `ElicitationContentTamperDetected` Critical alert / `elicitation.content_tamper_detected` audit event (SEC-018).
5. `docs/operator-guide/observability.md` — docs-sync mirror of the two new warning alerts (lines 187–188); updated `QuotaFailOpenUserFractionInoperative` and `LegalHoldCheckpointAccumulationProjectedBreach` descriptions (OBS-041/042 closure of POL-034).

The KIN-028 three-layer enforcement (phase-stamp CM + render-time gate + runtime alert) is the dominant FMR-category delta and is where all new regressions cluster.

---

## 2. New findings (iter8, bed7961 regressions only)

### FMR-022 (NEW, Medium) — Runbook remediation step references non-existent "Admission webhook storm" runbook entry and undefined `admin.freezeAdmission` control

- Location: `/Users/joan/projects/lenny/spec/17_deployment-topology.md:841` (Remediation step 4 of the new `Admission-plane feature-flag downgrade` runbook stub)
- Observation: The Remediation section ends with:

  > (4) For emergency rollout halts while investigating, pair with `admin.freezeAdmission` from the "Admission webhook storm" runbook entry in this section to stop all admission traffic until remediation completes.

  Two dangling references that do not resolve anywhere in the spec or the committed docs:
  1. **No "Admission webhook storm" runbook entry** exists in §17.7. The closest entry is titled `Admission webhook outage` (lines 792–795, file `docs/runbooks/admission-webhook-outage.md`), and it does not describe any admission-freeze control — it covers webhook pod restart, cert rotation, and a `failurePolicy: Ignore` emergency bypass. `grep -rn "Admission webhook storm" spec/ docs/` returns only the self-reference in the new runbook.
  2. **No `admin.freezeAdmission` API / Helm value / control is defined** anywhere in the spec (verified across `spec/15_external-api-surface.md` admin endpoints, `spec/11_policy-and-controls.md` admission controls, `spec/17_deployment-topology.md` emergency procedures, and `docs/api/admin.md`). `grep -rn "freezeAdmission\|freeze-admission\|freeze admission" spec/ docs/` returns only the self-reference at line 841.

- Regression context: bed7961 introduced this runbook stub wholesale (lines 838–841). The "Admission webhook storm" / `admin.freezeAdmission` cross-reference is net-new text and refers to nothing.
- Impact: An on-call operator following this runbook during an active admission-plane downgrade (a fail-closed security regression — a policy-enforcing webhook has vanished) reaches remediation step 4, attempts to locate the "Admission webhook storm" entry and the `admin.freezeAdmission` control, finds neither, and stalls. The runbook is the sole operator-facing document for this alert (per §16.5 `AdmissionPlaneFeatureFlagDowngrade` row: "this alert is the sole signal for the class of drift described in the iter7 KIN-028 finding"), so an incomplete remediation path materially delays recovery during a security-salient event. The fail-closed nature of the underlying webhook means the affected admission policy is no longer enforced during the outage, putting rollout-halt as a legitimate operational option — which the runbook gestures at but cannot deliver.
- Severity: **Medium** — consistent with the iter5/iter6/iter7 rubric for "runbook hot-path reference to undefined API/runbook-entry on a fail-closed security regression". If only one of the two references were missing it would be Low; the combined dangling reference is a single textual regression affecting both, and both dangle.
- Suggested fix (proposal only — do not edit in this review): Either (a) replace step 4 with a concrete emergency containment step that references existing controls (e.g., `helm upgrade --set <flag>=true` to restore the webhook, paired with the existing `admission-webhook-outage` runbook for webhook-pod-level remediation), or (b) add a new "Admission webhook storm" runbook entry (and the associated `admin.freezeAdmission` surface in §15.1) before the next release — but since `feedback_no_backward_compat.md` precludes adding mode-split controls without a concrete product decision, (a) is the safer narrow fix.

---

### FMR-023 (NEW, Medium) — `AdmissionPlaneFeatureFlagDowngrade` PromQL expression differs materially between the §17.2 enforcement-layer description and the §16.5 alert-rules row

- Location: `/Users/joan/projects/lenny/spec/17_deployment-topology.md:80` (§17.2 "Feature-flag downgrade enforcement" layer 4) vs `/Users/joan/projects/lenny/spec/16_observability.md:480` (§16.5 alert row)
- Observation: The two canonical definition sites disagree on the alert expression and on the metric labels it joins.

  §17.2 version (line 80):
  ```
  (kube_configmap_info{namespace="{{ .Release.Namespace }}", configmap="lenny-deployment-phase-stamp"}
     * on() group_right() label_replace(..., "flag_enabled", "true", "flag_name", "<flag>"))
   unless on(webhook_name)
     kube_validatingwebhookconfiguration_info{webhook_name=~"<flag-to-webhook regex>"}
  ```

  §16.5 version (line 480):
  ```
  (kube_configmap_info{namespace="{{ .Release.Namespace }}", configmap="lenny-deployment-phase-stamp"} == 1)
    unless on()
      (kube_validatingwebhookconfiguration_info{name="<flag-gated-webhook-name>"} == 1)
  ```

  Three substantive divergences:
  1. **Label name**: §17.2 joins on `webhook_name` (both in the `unless on(...)` matcher and in the selector `webhook_name=~"..."`); §16.5 uses `name`. `kube-state-metrics` exports the label as `name`, not `webhook_name` — so the §17.2 expression would not match any series. The §16.5 version is the correct one.
  2. **Join vs selector shape**: §17.2 uses `* on() group_right() label_replace(...)` (a vector-vector multiplication that carries the right-hand labels through) to inject `flag_name`; §16.5 relies on a per-flag materialised label exported by a chart-rendered ConfigMap annotation (`lenny.dev/flag-<flag>-enabled: "true"`) so the labels are already on the left-hand series. These are materially different implementation strategies, not alternative phrasings of the same rule.
  3. **Regex vs exact match**: §17.2's `kube_validatingwebhookconfiguration_info{webhook_name=~"<flag-to-webhook regex>"}` implies one rule with a regex-union over all gated webhooks; §16.5's `{name="<flag-gated-webhook-name>"}` is an exact match, paired with the §16.5 prose statement that the alert "emits one firing per missing webhook" (i.e., one rule per `(flag, webhook)` pair, which means `features.compliance` produces two alert instances). The §17.2 regex form would produce a single firing per flag, conflating the two-webhook case that §16.5 explicitly disambiguates.

- Regression context: bed7961 introduced both sites wholesale (neither pre-existed in iter6 or earlier). The two were authored together but diverge; this is a net-new regression in the iter7 fix envelope, not a carry-forward. The caller's scope directive explicitly asks for this check: "PromQL in runbook row matches the alert definition in §16.5 and docs/operator-guide/observability.md."
- Impact: An operator rendering the Helm chart's `PrometheusRule` from the §17.2 pseudocode will produce a rule that never fires (wrong label name, wrong join syntax, unresolved `label_replace(..., ...)` first argument). An operator reading the §16.5 row produces a rule that fires correctly but emits per-webhook (not per-flag) instances. The §17.2 version also contains literal `...` as the first argument to `label_replace`, which is a placeholder rather than valid PromQL — so at minimum §17.2 is pseudocode, but it is prose-pseudocode that contradicts the authoritative §16.5 syntax. The chart author and SRE reading these two sections side-by-side have no way to know which is canonical.
- Severity: **Medium** — the alert is "the SOLE runtime signal for feature-flag downgrade drift" (§16.5 line 480, verbatim). A regression-rendered PromQL that never fires means an entire security-salient failure mode (fail-closed admission webhook silently vanished) goes undetected in production. Consistent with the iter5 FMR-018 Medium rubric for "admission-path failure mode where the monitoring signal is unreliable". If this were a purely cosmetic expression-formatting difference I would call it Low, but the label-name divergence is a concrete PromQL correctness bug, not a cosmetic one.
- Suggested fix (proposal only): Replace the §17.2 expression in layer 4 with the §16.5 expression verbatim (or a one-line reference "see §16.5 `AdmissionPlaneFeatureFlagDowngrade` for the canonical expression"), making §16.5 the single source of truth. Retain the §17.2 *narrative* description (phase-stamp records enabled but webhook absent) as the human-facing description.

---

### FMR-024 (NEW, Medium) — Runbook references undefined audit event `deployment.feature_flag_downgrade_acknowledged`

- Location: Multiple sites introduced by bed7961, collectively — the audit event `deployment.feature_flag_downgrade_acknowledged` is referenced 6 times across `spec/17_deployment-topology.md:76,80,840,841` and `docs/operator-guide/configuration.md:509` and `docs/operator-guide/observability.md:188`, but is never defined in the authoritative audit-event catalogues at §16.7 (audit events) or §11.7 (audit logging).
- Observation: The new runbook's Diagnosis step 4 (line 840) tells operators:

  > (4) Identify whether a legitimate downgrade acknowledgement was recorded: `GET /v1/admin/audit-events?event_type=deployment.feature_flag_downgrade_acknowledged&since=24h` — if the acknowledgement is present but the webhook is still missing, the chart render committed but the admission object failed to install (retry the upgrade); if no acknowledgement is present, the divergence is unauthorized and requires immediate remediation.

  This is the **primary diagnostic step that distinguishes intentional from unauthorized admission-plane downgrade** — the whole runbook branches on the presence/absence of this event. But `grep -n "deployment.feature_flag_downgrade_acknowledged" spec/16_observability.md spec/11_policy-and-controls.md` returns zero hits. The event is never defined, has no payload schema, no severity, and no routing (Postgres / SIEM / OCSF translation) specified. §16.7 defines an explicit audit-event catalogue for exactly this purpose (with event_type, payload fields, retention, sampling discipline); the new event should be listed there.
- Regression context: bed7961 is the commit that introduces every reference to this event. Before iter7, the feature-flag downgrade mechanism did not exist. The audit-event definition was omitted from the docs-sync pass.
- Impact: An operator running the Diagnosis step 4 query against a running Lenny deployment is querying for an `event_type` that the platform never emits (the emission site is described narratively but not wired into the audit-event pipeline). The query returns empty for both the "intentional" and "unauthorized" branches — meaning the runbook's binary decision point collapses, and the operator must treat every downgrade as unauthorized (or conversely, treat every downgrade as intentional). Combined with FMR-022, the runbook's decision tree has **two** broken branches in the diagnosis-remediation handoff.
- Severity: **Medium** — the event is the key signal that drives branch selection in the runbook's remediation tree, and its absence from §16.7 means operators cannot trust the query result. Consistent with iter5 FMR-018 Medium calibration for "runbook relies on a signal that is described but not wired into the audit pipeline". Lower than FMR-022 in terms of blast radius because the narrative chain (chart emits event on override) is plausibly implementable from the text, whereas "Admission webhook storm" entry truly does not exist anywhere. But both are runbook-hotpath regressions on the same fail-closed admission failure mode.
- Suggested fix (proposal only): Add `deployment.feature_flag_downgrade_acknowledged` to the §16.7 audit-event catalogue with payload fields (`flag_name`, `operator_sub`, `operator_tenant_id`, `acknowledgement_timestamp`, `phase_stamp_enabled_at`, `pre_downgrade_webhook_set`, `post_downgrade_webhook_set`), emit-site (§17.2 layer 2 chart render-time), non-sampled discipline (symmetric with `circuit_breaker.state_changed` per §16.7), and route through the standard append-only audit path. No protocol change required — the event is already referenced, only its catalogue entry is missing.

---

## 3. Other bed7961 FMR deltas verified clean

Per the caller directive, the following were checked and are CLEAN:

### 3.1 Preflight row 514 exists and structurally matches the runbook cross-reference — VERIFIED

- `/Users/joan/projects/lenny/spec/17_deployment-topology.md:514` — `Phase-stamp consistency (KIN-028)` row present; sits between row 513 (Admission webhook inventory) and row 515 (SIEM endpoint warning); structurally aligned with neighbouring rows (description | failure message columns).
- The runbook's Diagnosis section does not directly name row 514, but §17.2 "Feature-flag downgrade enforcement" layer 3 (line 78) cross-references it as "The `lenny-preflight` Job gains a new `Phase-stamp consistency` check" — anchor text matches the row's column-1 label. No dangling reference.

### 3.2 Runbook three-part structure consistent with §17.7 pattern — VERIFIED

- §17.7's structural note (line 733) enumerates Trigger / Diagnosis / Remediation.
- The new `Admission-plane feature-flag downgrade` runbook (lines 838–841) uses exactly those three section headings in that order, matching the 20+ other runbook stubs in §17.7.
- (The caller's scope directive mentioned "trigger → symptoms → diagnosis → resolution" but §17.7's canonical structure is three-part, not four-part; §17.7 has consistently used Trigger/Diagnosis/Remediation since the section was introduced and bed7961 preserves that.)

### 3.3 Runbook Trigger PromQL aligns with §16.5 narrative — VERIFIED (but see FMR-023)

- The runbook's Trigger section references "phase-stamp ConfigMap records `features.<flag>.enabled=true` (with an `enabledAt` RFC3339 timestamp) but the corresponding `ValidatingWebhookConfiguration` is absent from the cluster for > 2 minutes". The `> 2 minutes` duration matches the §16.5 row's `sustained for more than 2 minutes` exactly, and the "phase-stamp ConfigMap records `enabled: true`" description matches §16.5 syntactically. The prose-level alignment is fine; the divergence is purely at the PromQL layer and is captured in FMR-023.

### 3.4 Alert severity and routing align with FMR-category conventions — VERIFIED

- `EphemeralContainerCredGuardUnavailable` (Warning, line 479): consistent with every other `*WebhookUnavailable` fail-closed admission webhook alert in §16.5 (DrainReadinessWebhookUnavailable, CrdConversionWebhookUnavailable, T4NodeIsolationWebhookUnavailable, DirectModeIsolationWebhookUnavailable, DataResidencyWebhookUnavailable, SandboxClaimGuardUnavailable, PoolConfigValidatorUnavailable, LabelImmutabilityWebhookUnavailable — all Warning, all 5-minute durations, all fail-closed). The SEC-017 addition follows this convention.
- `AdmissionPlaneFeatureFlagDowngrade` (Warning, line 480): the in-row rationale ("Fires Warning (not Critical) because the typical root cause is an operator mistake on `helm upgrade` ... the chart render-time fail-closed validation and preflight Job prevent the chart-path regression, and this alert catches the out-of-band-mutation path") is internally coherent with the §11.6 circuit-breaker, §16.5 `LegalHoldOverrideUsed`, and §16.5 `CompliancePostureDecommissioned` precedent for post-hoc-detect-and-notify alerts on operator-mistake failure modes. Warning (not Critical) is correct given the enforcement layers 1–3 are the Critical-path defenses; this alert is the final drift-catch layer.
- The paired `ElicitationContentTamperDetected` Critical alert (line 481 region of the added Critical row, covered in SEC-018) is correctly Critical (security-salient hostile-runtime detection). This is in the Critical block, distinct from the Warning block where the two feature-flag-related alerts live. Routing by severity alone is correct.

### 3.5 Other bed7961 observability fixes carry no new FMR regressions

- OBS-042 `LegalHoldCheckpointAccumulationProjectedBreach` PromQL correction: the new expression `(... ) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` is valid PromQL (scalar-vector arithmetic on the `>` side, vector-vector `and on(tenant_id)` for gating). The fix correctly removes the invalid `on()/group_left` modifier from the scalar-vector arithmetic. Runbook at line 834 updates its Trigger expression to match. VERIFIED clean.
- OBS-041 `QuotaFailOpenUserFractionInoperative` description correction: the new description "`lenny_quota_user_failopen_fraction >= 0.5` — continuously-firing Prometheus alert" correctly replaces the prior prose "Gateway startup warning emitted when …" with the actual metric-based alert form. Matches the metric definition at §16.1. VERIFIED clean.
- DOC-031 `#124-redis-ha-and-failure-modes` anchor correction: the §16.1 gauge row now uses the correct target anchor. Verifies that FMR-021 is closed. VERIFIED clean.

---

## 4. Carry-forwards (Low, not re-scored under iter8 regressions-only scope)

Not evaluated per iter8 scope. Carry-forward prior-iteration Low findings (FLR-014 / FLR-015 / FLR-016 / FLR-017) are out-of-scope for iter8 per `feedback_iter8_regressions_only.md`.

---

## 5. Convergence assessment

- **New Critical findings: 0.**
- **New High findings: 0.**
- **New Medium findings: 3** — FMR-022 (runbook hot-path dangling references), FMR-023 (PromQL expression divergence between §17.2 and §16.5), FMR-024 (runbook references undefined audit event).
- All three regressions cluster on the iter7 KIN-028 fix (three-layer feature-flag downgrade enforcement). The feature-flag downgrade runbook and its associated signal pipeline are the net-new FMR surface in bed7961, and they triple-fault:
  1. Diagnosis references an undefined audit event (FMR-024).
  2. Remediation references a non-existent runbook entry and undefined admin control (FMR-022).
  3. The alert expression diverges between its two canonical sites, with a label-name error that prevents rule firing (FMR-023).
- Mitigation: **None of the three regressions block the chart-render-time gate** (§17.2 layer 2 `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE`) nor the preflight gate (§17.2 layer 3 `PREFLIGHT_PHASE_STAMP_MISMATCH`). So the chart-path downgrade attempt is still fail-closed-enforced. The three findings attack only the runtime-drift detection path (the out-of-band-mutation scenario where an operator `kubectl delete`s a ValidatingWebhookConfiguration manually). Blast radius is therefore bounded but real.

**Verdict: Not converged for Perspective 20 under iter8 regressions-only scope. Three Medium regressions require fix before next iteration.**

---

## 6. Findings index

| ID      | Severity | Status           | Location                                                                              |
|---------|----------|------------------|---------------------------------------------------------------------------------------|
| FMR-022 | Medium   | NEW              | `spec/17_deployment-topology.md:841` (runbook remediation refs non-existent entry)    |
| FMR-023 | Medium   | NEW              | `spec/17_deployment-topology.md:80` vs `spec/16_observability.md:480` (PromQL drift)  |
| FMR-024 | Medium   | NEW              | `spec/17_deployment-topology.md:76,80,840,841` + docs (undefined audit event)         |
| FMR-021 | Low      | FIXED (iter7)    | Closed by DOC-031 in bed7961 (`#124-redis-ha-and-failure-modes` anchor corrected)     |
