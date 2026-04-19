---
layout: default
title: "crd-upgrade"
parent: "Runbooks"
triggers:
  - alert: CRDVersionSkew
    severity: critical
  - alert: StaleCRDDetected
    severity: warning
components:
  - controlPlane
symptoms:
  - "gateway fails to start: crd not found / schema mismatch"
  - "controllers reconcile errors: unknown field"
  - "Helm upgrade completed without updating CRDs"
tags:
  - crd
  - upgrade
  - helm
  - gitops
requires:
  - cluster-access
related:
  - gateway-replica-failure
  - schema-migration-failure
---

# crd-upgrade

CRDs in the cluster are out of sync with the deployed gateway/controller image. Common causes: Helm's default behavior of not upgrading CRDs on `helm upgrade`, or a GitOps tool applying CRDs out-of-order with the workload.

## Trigger

- `CRDVersionSkew` — controller observes a CRD whose schema does not match the version embedded in the binary.
- `StaleCRDDetected` — preflight or periodic check finds a CRD version older than the installed chart.
- Gateway logs at startup: `crd "sandboxes.lenny.dev" not found` or `unknown field in spec`.
- Controller logs: repeated `failed to convert ... spec.runtimeClass` or schema validation errors.

## Diagnosis

### Step 1 — Current CRD versions

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get crd -l app.kubernetes.io/part-of=lenny \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.labels.app\.kubernetes\.io/version}{"\n"}{end}'
```

Compare the version labels against the Helm chart version you intended to deploy.

### Step 2 — Chart vs cluster

<!-- access: kubectl requires=cluster-access -->
```bash
helm list -n lenny-system
helm get values lenny -n lenny-system | head -20
```

If `helm list` shows the new chart version but CRDs lag, the upgrade skipped CRDs (expected default behavior for Helm).

### Step 3 — Preflight

<!-- access: lenny-ctl -->
```bash
lenny-ctl preflight --config values.yaml
```

The preflight compares expected CRD versions against the cluster and fails loudly if there is drift.

### Step 4 — Controller reconcile errors

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-warm-pool-controller -n lenny-system --since=10m \
  | grep -Ei "crd|unknown field|schema"
```

## Remediation

### Step 1 — Apply CRDs from the chart

<!-- access: kubectl requires=cluster-access -->
```bash
helm template lenny lennylabs/lenny --version <chart-version> \
  --include-crds | kubectl apply -f -
```

Or apply the CRDs directly from your chart's `crds/` directory:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl apply -f charts/lenny/crds/
```

### Step 2 — Restart controllers and gateway

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment -n lenny-system \
  lenny-gateway lenny-warm-pool-controller lenny-runtime-controller
```

Controllers re-list CRDs on startup; gateway re-validates schemas.

### Step 3 — GitOps: fix sync-wave ordering

If you deploy with ArgoCD or Flux and CRDs landed out of order:

- ArgoCD: tag the CRD manifests with `argocd.argoproj.io/sync-wave: "-1"` (earlier than workloads).
- Flux: put CRDs in a Kustomization with `dependsOn` referenced by the workload Kustomization.

Re-sync after the annotation change.

### Step 4 — Conversion webhook drift

If the chart uses a conversion webhook (e.g., `v1alpha1` ↔ `v1beta1`):

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get crd <crd-name> -o jsonpath='{.spec.conversion}' | jq .
```

Verify the webhook service exists and has Ready endpoints. If not, follow [admission-webhook-outage](admission-webhook-outage.html).

### Step 5 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl preflight --config values.yaml
lenny-ctl diagnose connectivity
```

- Preflight passes.
- Controllers no longer log schema errors.
- Gateway pods Ready.
- Warm pool replenishes (see [warm-pool-exhaustion](warm-pool-exhaustion.html) if it doesn't).

## Escalation

Escalate to:

- **Release engineer** if CRD upgrades require hand-editing that isn't captured in the chart — indicates a chart bug.
- **Cluster admin** if CRD installation is restricted by cluster policy (e.g., only admins can install CRDs).
- **Platform engineering** for stuck conversion webhooks or schema-version migrations that fail to converge.

Cross-reference: Spec §17.6 (packaging and installation), §10.5 (upgrade and rollback strategy).
