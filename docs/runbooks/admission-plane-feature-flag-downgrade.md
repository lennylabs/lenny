---
layout: default
title: "admission-plane-feature-flag-downgrade"
parent: "Runbooks"
triggers:
  - alert: AdmissionPlaneFeatureFlagDowngrade
    severity: warning
components:
  - admission
symptoms:
  - "feature-flag phase-stamp recorded enabled but gated ValidatingWebhookConfiguration is absent"
  - "admission-plane is no longer enforcing a policy it previously enforced"
  - "paired *Unavailable webhook alert does NOT fire (PrometheusRule gated on the same missing flag)"
tags:
  - admission
  - feature-flags
  - phase-stamp
  - downgrade
requires:
  - admin-api
  - cluster-access
related:
  - admission-webhook-outage
  - ephemeral-container-cred-guard-unavailable
---

# admission-plane-feature-flag-downgrade

A feature-flag-gated admission webhook has been removed without clearing its entry in the `lenny-deployment-phase-stamp` ConfigMap. The phase-stamp still records the flag as `enabled: true` but the corresponding `ValidatingWebhookConfiguration` is no longer present in the cluster, so a policy that was previously enforced is silently dark. This alert is the SOLE runtime signal for this class of drift, because the paired per-webhook `*Unavailable` alert does NOT fire — its gating `PrometheusRule` is removed by the same feature-flag flip that removed the webhook.

## Trigger

- `AdmissionPlaneFeatureFlagDowngrade` — phase-stamp ConfigMap records `features.<flag>.enabled=true` (with an `enabledAt` RFC3339 timestamp) but the corresponding `ValidatingWebhookConfiguration` is absent from the cluster for > 2 minutes.

The alert emits one firing per missing `(flag, webhook)` pair, so a full `features.compliance=false` flip produces two firings (one for `lenny-data-residency-validator`, one for `lenny-t4-node-isolation`) carrying identical `flag_name="features.compliance"` with distinct `expected_webhook_name` labels.

See SPEC §16.5 for the canonical PromQL expression body and full four-pair rule decomposition; SPEC §17.2 "Feature-flag downgrade enforcement" for the phase-stamp append-only invariant and chart-render-time guard; `docs/operator-guide/configuration.md` for the mandatory `kube-state-metrics --metric-labels-allowlist=configmaps=[lenny.dev/flag-*]` operator precondition.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Read the committed phase-stamp

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-deployment-phase-stamp \
  -o jsonpath='{.data}'
```

Each key is a Helm feature-flag path (e.g., `features.llmProxy`); each value is a JSON object `{ "enabled": true, "enabledAt": "<RFC3339>" }`. The phase-stamp is append-only and owned by the Helm chart — no component mutates it at runtime.

### Step 2 — List currently-installed webhooks

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get validatingwebhookconfiguration -l app.kubernetes.io/name=lenny
```

Cross-reference against the flag-to-webhook mapping (SPEC §17.2 Feature-gated chart inventory):

| Flag | Gated webhook(s) |
|:-----|:-----------------|
| `features.llmProxy` | `lenny-direct-mode-isolation` |
| `features.drainReadiness` | `lenny-drain-readiness` |
| `features.compliance` | `lenny-data-residency-validator` AND `lenny-t4-node-isolation` (both required when true) |

Use the alert's `flag_name` and `expected_webhook_name` labels to identify the specific missing webhook.

### Step 3 — Was the downgrade acknowledged?

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=deployment.feature_flag_downgrade_acknowledged&since=24h
```

- If an acknowledgement is present AND the webhook is still missing, the chart render committed but the admission object failed to install — retry the upgrade (Remediation Step 2).
- If no acknowledgement is present, the divergence is unauthorized; proceed to Remediation Step 1.

### Step 4 — Inspect recent `helm` history and GitOps sync state

<!-- access: kubectl requires=cluster-access -->
```bash
helm -n lenny-system history lenny
```

If a GitOps operator (ArgoCD / Flux Helm Controller) reconciles the chart, inspect its recent sync events and the values file diff that correlates to the flag-flip time (`enabledAt` in the phase-stamp is the lower bound).

## Remediation

### Step 1 — Unintentional downgrade (e.g., CI pipeline regressed `values.yaml`)

Restore the webhook by re-enabling the flag:

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lenny/lenny -n lenny-system \
  --reuse-values --set features.<flag>=true
```

Chart render-time validation (`PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` check at `helm install`/`helm upgrade`) passes because the phase-stamp already records the flag as enabled; the webhook `Deployment`, `ValidatingWebhookConfiguration`, and gated `PrometheusRule` are rendered again and the admission plane returns to the enforcing state.

Expected outcome: the firing alert clears within one evaluation cycle (typically ≤ 2 min) as `kube_validatingwebhookconfiguration_info{name="<webhook>"}` reappears.

### Step 2 — Intentional, auditable downgrade

If the downgrade is deliberate and compliance has approved it, commit an explicit acknowledgement:

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lenny/lenny -n lenny-system \
  --reuse-values \
  --set features.<flag>=false \
  --set acceptFeatureFlagDowngrade.<flag>=true \
  --set acceptFeatureFlagDowngrade.<flag>.justification="<change-request or ticket reference>"
```

This emits a `deployment.feature_flag_downgrade_acknowledged` audit event (catalogued in SPEC §16.7 with payload fields `flag_name`, `expected_webhook_name`, `acknowledged_by_sub`, `acknowledged_by_tenant_id`, `justification`, `acknowledged_at`) and rewrites the phase-stamp entry in place (retaining the record of the downgrade posture). The webhook stops rendering but the phase-stamp flags the posture as degraded so the runtime alert continues to fire — which is the intended steady-state signal that the admission plane is operating in a reduced-enforcement mode.

To fully clear the phase-stamp and the alert (for example, after the degraded posture has been normalized and should no longer page), issue an explicit audited reset:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system delete configmap lenny-deployment-phase-stamp
helm upgrade lenny lenny/lenny -n lenny-system --reuse-values
```

The next `helm upgrade` re-renders a fresh phase-stamp reflecting the new current state (the flags that are `true` at that moment, with new `enabledAt` timestamps). Only an operator with cluster-admin permission can perform this reset; no Lenny component has `delete` on the ConfigMap.

### Step 3 — Do NOT hand-edit `lenny-deployment-phase-stamp`

The Helm chart is the sole writer of the phase-stamp. Manual `kubectl edit` on the ConfigMap violates the append-only contract and will be reverted on the next chart upgrade. The only supported modifications are:

1. `helm upgrade` with the `acceptFeatureFlagDowngrade.<flag>=true` override (Step 2), which rewrites an existing entry in place.
2. Full reset via `kubectl delete configmap` + `helm upgrade` (Step 2, second block), which rebuilds the phase-stamp from scratch.

### Step 4 — Verify recovery

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get validatingwebhookconfiguration -l app.kubernetes.io/name=lenny
kubectl -n lenny-system get configmap lenny-deployment-phase-stamp -o jsonpath='{.data}'
```

- The missing `ValidatingWebhookConfiguration` is present again (Step 1 path) or the phase-stamp entry has been rewritten to reflect the acknowledged-degraded posture (Step 2 path).
- The `AdmissionPlaneFeatureFlagDowngrade` alert clears (Step 1) or continues firing intentionally as the degraded-posture signal (Step 2).
- No paired per-webhook `*Unavailable` alert fires spuriously (the `PrometheusRule` for the gated webhook re-renders only in Step 1).

### Step 5 — Post-incident drift-snapshot refresh

Per the [drift-snapshot-refresh](#) tail-of-every-hotfix-runbook convention described in §17.7, call:

<!-- access: api method=POST path=/v1/admin/drift/snapshot/refresh -->
```
POST /v1/admin/drift/snapshot/refresh
{ "desired": {...}, "confirm": true }
```

so that the drift snapshot reflects the current desired state after the flag flip (or acknowledged downgrade). Skip only if no admin-API mutation beyond the Helm-values change was made.

## Escalation

Escalate to:

- **Security on-call** if the downgrade was unauthorized AND included a removal of the `lenny-ephemeral-container-cred-guard` class of control — the credential-read boundary (SPEC §13.1) may have been briefly weakened; correlate against `pods/ephemeralcontainers` audit events during the gap window.
- **Compliance officer / DPO** if the downgrade affected `features.compliance` (residency validator or T4 node isolation) and the tenant has a regulated `complianceProfile`.
- **Platform engineering** if the chart render completed but the `ValidatingWebhookConfiguration` object still did not install (cluster-scoped RBAC, apiserver error, or admission-controller cycle on the webhook's own admission) — this is a platform bug, not an operator mistake.
- **GitOps pipeline owner** if the flag-flip traced to a CI regression or Git reviewer approval gap; partner on values-file review controls to prevent recurrence.

Cross-reference: [SPEC §17.2](https://github.com/lennylabs/lenny/blob/main/spec/17_deployment-topology.md#172-namespace-layout) "Feature-flag downgrade enforcement"; [SPEC §16.5](https://github.com/lennylabs/lenny/blob/main/spec/16_observability.md#165-alerting-rules-and-slos) `AdmissionPlaneFeatureFlagDowngrade`; [Metrics Reference](../reference/metrics.html#alert-rules).
