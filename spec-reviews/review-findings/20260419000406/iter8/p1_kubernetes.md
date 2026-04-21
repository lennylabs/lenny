# Iter8 Review — Perspective 1: Kubernetes-Native Design (KIN)

**Scope directive:** iter8+ regressions-only relative to the previous fix commit `bed7961`. Only the surfaces touched by the fix are inspected; pre-existing KIN issues and long-running carry-forwards (including KIN-021 through KIN-027 and KIN-029 through KIN-038 from iter7) are out of scope.

**Fix commit reviewed:** `bed7961` ("Fix iteration 7: applied fixes for 13 Medium findings + docs sync").

**KIN-relevant surfaces inspected:**
- `spec/13_security-model.md` §13.1 ("`lenny-cred-readers` membership boundary" — new ephemeral-container cred-guard webhook narrative) and §13.2 (Admission-Webhook NetworkPolicy row — count bumped from 8 → 9; `lenny-ephemeral-container-cred-guard` added to selector list).
- `spec/17_deployment-topology.md` §17.2 — new admission-policies inventory item 13; updated HA paragraph; new "Feature-flag downgrade enforcement" sub-section (phase-stamp ConfigMap + render-time validation + preflight mismatch check + runtime alert); preflight row 514 "Phase-stamp consistency"; new §17.7 runbook entry "Admission-plane feature-flag downgrade".
- `spec/16_observability.md` §16.5 — new alerts `EphemeralContainerCredGuardUnavailable` (line 479) and `AdmissionPlaneFeatureFlagDowngrade` (line 480).
- `docs/operator-guide/configuration.md` — new "Admission-plane feature flags" subsection (lines 487–517).
- `docs/operator-guide/namespace-and-isolation.md` — new item 8 "lenny-ephemeral-container-cred-guard webhook" (line 102).

**ID-namespace note:** The task brief indicates the last KIN finding is KIN-028 and that new findings should start from KIN-029. However, the iter7 `p1_kubernetes.md` file has already allocated KIN-026 through KIN-038 (not all of which were fix-envelope items — most are iter7 originals). To avoid ID collisions with live iter7 findings, this iter8 sweep starts new finding IDs at **KIN-039** instead of KIN-029.

**Convergence assessment:** Three regressions identified — two Medium, one Medium. No Critical or High.

---

### KIN-039 Preflight "Admission webhook inventory" baseline list missing the new `lenny-ephemeral-container-cred-guard` webhook [Medium]
**Section:** `spec/17_deployment-topology.md` §17.9 `Checks performed` row 513 ("Admission webhook inventory"); cross-reference §17.2 "Feature-gated chart inventory (single source of truth)".

The iter7 fix added `lenny-ephemeral-container-cred-guard` as item 13 to §17.2's admission-policies inventory and explicitly classifies it as one of the **five baseline (always-rendered) webhooks** ("Interaction with the baseline" paragraph at §17.2: *"The five baseline webhooks (`lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, `lenny-crd-conversion`, `lenny-ephemeral-container-cred-guard`) have no feature-flag gate and therefore have no phase-stamp entry — they are always expected from Phase 3.5 onward"*), with the companion sentence *"the SEC-017 addition of `lenny-ephemeral-container-cred-guard` (item 13) is part of this baseline set; … its absence is caught by the existing `lenny-preflight` admission-webhook inventory check"*. The §17.2 integration-test paragraph also bumps the Phase 3.5 baseline expected-set from four to five entries (`all flags false … five entries expected`).

However, preflight row 513 ("Admission webhook inventory") was NOT updated to list the new webhook in the baseline enumeration. Row 513 still reads, verbatim: *"baseline entries `lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, and the `lenny-crd-conversion` conversion webhook for each Lenny CRD are always expected"* (four entries) and then describes the feature-flagged additions without including `lenny-ephemeral-container-cred-guard`. The §17.2 narrative ("five baseline entries") and the §17.9 preflight row (four baseline entries) directly contradict.

The consequence is the exact class of regression §17.2 says the preflight gate is designed to prevent ("its absence is caught by the existing `lenny-preflight` admission-webhook inventory check"): a Helm chart that accidentally drops the `lenny-ephemeral-container-cred-guard` template will pass the preflight inventory check, because row 513's expected-set computation never includes that webhook — the rendered set narrows, the expected set also narrows (it is derived from the same stale list), and the credential-boundary fail-closed surface vanishes silently. The §13.1 reference to "item 13" and the §17.2 narrative become aspirational rather than enforced.

**Recommendation:** In row 513 ("Admission webhook inventory"), update the baseline enumeration to: *"baseline entries `lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, `lenny-ephemeral-container-cred-guard`, and the `lenny-crd-conversion` conversion webhook for each Lenny CRD are always expected"*. No feature-flag gating is needed for the new entry — it matches the baseline treatment already given to the other four.

---

### KIN-040 `AdmissionPlaneFeatureFlagDowngrade` PromQL fires false positives because the LHS vector `kube_configmap_info` does not encode per-flag `enabled` state [Medium]
**Section:** `spec/16_observability.md` §16.5 row `AdmissionPlaneFeatureFlagDowngrade` (line 480); `spec/17_deployment-topology.md` §17.2 "Feature-flag downgrade enforcement" layer 4 (line 80).

The alert is intended to fire only when the phase-stamp ConfigMap records `enabled: true` for a specific flag **and** the flag's webhook is absent. The §16.5 expression as specified is:

> `(kube_configmap_info{namespace="{{ .Release.Namespace }}", configmap="lenny-deployment-phase-stamp"} == 1) unless on() (kube_validatingwebhookconfiguration_info{name="<flag-gated-webhook-name>"} == 1)`

The `kube_configmap_info` metric from `kube-state-metrics` samples `1` per ConfigMap that exists; it has no label encoding the phase-stamp's JSON `data.<flag>.enabled` value. Therefore the LHS predicate ("the flag has been enabled and recorded in the phase-stamp") is replaced at evaluation time by ("the phase-stamp ConfigMap exists"). On a Phase 3.5 install — where the ConfigMap is rendered (per §17.2 layer 1, `helm.sh/hook-weight: "-20"` puts it earlier than preflight, and it exists on fresh installs even when no flag has been flipped) but no feature-gated webhooks have been installed yet — the alert fires **one instance per (flag, webhook) pair in the mapping** immediately on install, with no flag actually enabled. That is four spurious Warning alerts (`features.llmProxy → lenny-direct-mode-isolation`, `features.drainReadiness → lenny-drain-readiness`, `features.compliance → {lenny-data-residency-validator, lenny-t4-node-isolation}`) on every baseline Phase 3.5 install that persist until Phase 13.

The §16.5 prose acknowledges the intent ("the left-hand side uses the phase-stamp's recorded `true` entries (exported via a chart-rendered `kube_configmap_labels` annotation `lenny.dev/flag-<flag>-enabled: "true"` so that `kube-state-metrics` surfaces the flag set as labels)"), but the **expression itself references `kube_configmap_info`, not `kube_configmap_labels`**. `kube_configmap_info` does not expose user-defined labels in its label set; `kube_configmap_labels` (an independent metric emitted only when kube-state-metrics is launched with `--metric-labels-allowlist=configmaps=[...]` containing the `lenny.dev/flag-<flag>-enabled` label key) is the only metric that exposes the labels.

The §17.2 variant of the expression is distinct but suffers the same root cause:

> `(kube_configmap_info{namespace="{{ .Release.Namespace }}", configmap="lenny-deployment-phase-stamp"} * on() group_right() label_replace(..., "flag_enabled", "true", "flag_name", "<flag>")) unless on(webhook_name) kube_validatingwebhookconfiguration_info{webhook_name=~"<flag-to-webhook regex>"}`

`label_replace(vector, dst, replacement, src, regex)` requires the source label (here `flag_name`) to be present on the input vector. `kube_configmap_info` has no `flag_name` label, so the `label_replace` call is a no-op and does not conditionalize on flag state; the resulting LHS is still "phase-stamp CM exists". Additionally, the RHS selector uses `name=` in §16.5 but `webhook_name=~` in §17.2 — `kube_validatingwebhookconfiguration_info` exposes the webhook name under the `name` label per kube-state-metrics schema, so the §17.2 variant matches zero samples at all times, which would silently suppress the alert entirely.

This pair of issues is the only runtime signal for the feature-flag-downgrade class of drift (per §16.5: *"This alert is the SOLE runtime signal for feature-flag downgrade drift because the paired per-webhook `*Unavailable` alert … does NOT fire when its gated `PrometheusRule` has been removed alongside the webhook Deployment"*), so both false positives on installation and silent suppression on §17.2's variant defeat the stated iter7 KIN-028 fix goal.

**Recommendation:** Converge on a single, working expression in both §16.5 and §17.2. Two viable shapes:

1. **Label-exposure via `kube_configmap_labels`.** Chart renders the phase-stamp ConfigMap with a label per enabled flag (e.g., `lenny.dev/flag-llmProxy-enabled: "true"`). Configure kube-state-metrics with `--metric-labels-allowlist=configmaps=[lenny.dev/flag-*]` in the values.yaml documented acknowledgement. Alert expression:
   `kube_configmap_labels{namespace="{{ .Release.Namespace }}", configmap="lenny-deployment-phase-stamp", label_lenny_dev_flag_<flag>_enabled="true"} unless on() kube_validatingwebhookconfiguration_info{name="<webhook>"}`
2. **Expose a Lenny-emitted per-flag gauge.** Have `lenny-ops` (or the webhook-inventory controller) publish a `lenny_admission_phase_stamp_flag_enabled{flag="<flag>"} 1` gauge derived from the ConfigMap contents it already reads for preflight; alert on that gauge's presence minus the corresponding `kube_validatingwebhookconfiguration_info`. This sidesteps the kube-state-metrics label-allowlist requirement entirely.

Either approach, once chosen, must be reflected identically in §16.5 and §17.2 (both the expression and the LHS metric family). Align RHS label key with the actual kube-state-metrics schema (`name`, not `webhook_name`).

---

### KIN-041 Phase-stamp render-time guard overclaims that `helm template`/`--dry-run` enforces downgrade fail-closed [Medium]
**Section:** `spec/17_deployment-topology.md` §17.2 "Feature-flag downgrade enforcement" layer 2 (line 76); `docs/operator-guide/configuration.md` "Downgrade enforcement" paragraph (line 498).

§17.2 states: *"On every `helm install` / `helm upgrade` / `helm template` (including `--dry-run`), a pre-render template function reads the existing phase-stamp ConfigMap via Helm's `lookup "v1" "ConfigMap" .Release.Namespace "lenny-deployment-phase-stamp"` primitive and fails the render with the error code `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` if any feature flag that is recorded in the phase-stamp … is being rendered as `false`"*. `docs/operator-guide/configuration.md:498` makes the same claim ("On every `helm install` / `helm upgrade` / `helm template --dry-run`, a render-time guard reads the phase-stamp via the Helm `lookup` primitive and fails closed").

Helm's documented behaviour for the `lookup` function is: *"When you use the template command, `lookup` will always return an empty map."* (Helm Docs, Chart Template Guide / Accessing Resources During Templating). The `lookup` function reaches the live cluster only when a Kubernetes client is available to Helm — that is, during `helm install` and `helm upgrade`. `helm template --dry-run` (and `helm template` alone, which is the GitOps rendering path used by ArgoCD Server-Side Apply and Flux's Helm Controller) does not have cluster access and always returns an empty map from `lookup`, regardless of whether the phase-stamp exists. An empty map is the same result the chart sees on a first-ever install where no phase-stamp has been written yet — and the spec explicitly treats that as "proceed, then render the ConfigMap for the first time" (§17.2: *"The `lookup` returns an empty result on the initial `helm install` … in which case the render proceeds and the ConfigMap is rendered for the first time"*).

Net effect: under `helm template` and `helm template --dry-run`, the render-time guard is a **no-op**. A GitOps pipeline that flips `features.compliance=true → false` in `values.yaml`, re-renders the chart with `helm template`, and synchronises the rendered manifests via ArgoCD/Flux will not see the `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` error because `lookup` returned an empty map; the chart proceeds as if this were a first install, drops the two `features.compliance`-gated `ValidatingWebhookConfiguration` resources from the rendered set, and Argo/Flux reconcile them off the cluster. The cluster-side `lenny-preflight` Job would catch this later (per row 514 `PREFLIGHT_PHASE_STAMP_MISMATCH`), but only if the install uses Helm to drive preflight — the GitOps path typically runs preflight out-of-band or skips it entirely. The three-layer "persisted phase-stamp + render-time validation + runtime alert" defense reduces to two effective layers in the GitOps deployment style Lenny explicitly supports (§17.9: *"GitOps: The Helm chart supports `helm template` rendering for ArgoCD/Flux integration"*).

**Recommendation:** Either (a) remove the `helm template` / `--dry-run` claim from §17.2 layer 2 and the docs paragraph, and explicitly document that GitOps deployers MUST run `lenny-preflight` as a gate before Argo/Flux sync (which is where the `PREFLIGHT_PHASE_STAMP_MISMATCH` check actually works); or (b) implement the guard via a Helm `post-render` hook or a pre-sync admission-webhook-style validator (e.g., an `AdmissionPolicy` on `ValidatingWebhookConfiguration` DELETE that reads the phase-stamp directly from the cluster) that operates even when `helm template` is used out-of-band. Option (a) is the smaller edit; option (b) restores the three-layer defense property the spec currently claims.

---

## Summary

- **Critical / High:** 0
- **Medium:** 3 — KIN-039 (preflight inventory baseline omission); KIN-040 (alert PromQL false-positive / cross-section divergence); KIN-041 (GitOps render-time guard overclaim).
- **Low / Info:** Out of scope for iter8.

All three regressions are scoped to the iter7 fix envelope (phase-stamp ConfigMap + render-time validation + alert + preflight row + new baseline webhook). None is a pre-existing issue re-surfaced.

To reach iter9 convergence on the KIN perspective, row 513 must add the new baseline webhook (KIN-039); §16.5 and §17.2 must agree on an alert expression that conditionalizes on actual flag-enabled state via a metric with the right label schema (KIN-040); and the `helm template`/`--dry-run` claim must be either retracted or re-implemented with a cluster-side hook (KIN-041). None of these requires structural spec changes; all are local textual fixes plus (for KIN-040) a minor chart-rendering addition to expose flag state as kube-state-metrics labels or a Lenny-emitted gauge.
