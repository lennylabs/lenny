## KIN iter9 review — regressions from iter8 fix commit `df0e675`

**Scope:** Kubernetes-native surfaces touched by iter8 fix: `spec/17_deployment-topology.md` §17.2 four-layer enforcement narrative, §17.2 item 13 cred-guard four conditions, §17.7 runbook stub, §17.9 preflight row 513 baseline five-entry correction, `spec/16_observability.md` §16.5 `AdmissionPlaneFeatureFlagDowngrade` PromQL.

**Note on agent availability:** The dispatched KIN subagent hit a usage-rate-limit failure before producing a findings file (`"You've hit your limit · resets 4am (America/Los_Angeles)"`); this review was performed inline by the parent agent against the same regression-only scope brief.

### Inspection results — no regressions detected

1. **§17.2 "four layers" narrative** — four layers are enumerated consistently: (1) persisted phase-stamp ConfigMap, (2) fail-closed chart render-time validation scoped to `helm install` / `helm upgrade`, (3) `lenny-preflight` mismatch check as GitOps primary, (4) `AdmissionPlaneFeatureFlagDowngrade` Warning alert. Layer-2 scope limitation correctly explains Helm's `lookup` returning empty under `helm template`; layer-3 description correctly elevated to the "sole fail-closed downgrade gate for GitOps deployments". No internal contradiction.
2. **§17.2 item 13 four conditions** — conditions (i)–(iv) enumerated coherently with the fsGroup side-channel closure rationale. Cross-references to §13.1 and §15.1 intact.
3. **§17.7 runbook stub** — phantom "step 4" (ex-`admin.freezeAdmission`) removed; remediation ends at step 3 and appends the cross-reference to `docs/runbooks/admission-plane-feature-flag-downgrade.md`. No dangling references.
4. **§17.9 preflight row 513 (Admission webhook inventory)** — baseline now enumerates five entries (`lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, `lenny-crd-conversion`, `lenny-ephemeral-container-cred-guard`), with forward-reference text "the five-entry Phase 3.5 baseline per §17.2 lines 68/82/84". Consistent with §17.2 narrative ("four validating webhooks plus the conversion webhook").
5. **§17.9 row 514 (Phase-stamp consistency)** — rewritten as "sole fail-closed downgrade gate for GitOps deployments", consistent with §17.2 layer-3 elevation.
6. **§16.5 `AdmissionPlaneFeatureFlagDowngrade` PromQL** — four per-`(flag, webhook)` rule expressions each using `kube_configmap_labels{... label_lenny_dev_flag_<slug>_enabled="true"} unless on() kube_validatingwebhookconfiguration_info{name="..."}`. `unless on()` with empty label set produces the intended "LHS series unless any RHS series exists" semantics for vector-vector operation. `--metric-labels-allowlist` operator precondition documented in the alert rule and in `docs/operator-guide/configuration.md`. `name` label used on `kube_validatingwebhookconfiguration_info` (kube-state-metrics actual label, not the hypothetical `webhook_name` from prior regression).
7. **§17.2 vs §16.5 division of responsibility** — §17.2 layer 4 correctly defers PromQL body to §16.5 ("§16.5 is the single source of truth"); no duplicated expression body.

No Kubernetes-integration regressions detected in the iter8 fix envelope.
