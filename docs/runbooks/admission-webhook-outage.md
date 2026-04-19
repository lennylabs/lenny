---
layout: default
title: "admission-webhook-outage"
parent: "Runbooks"
triggers:
  - alert: AdmissionWebhookUnavailable
    severity: critical
  - alert: CosignWebhookUnavailable
    severity: critical
components:
  - admission
symptoms:
  - "warm pool replenishment halts"
  - "kubectl events: failed calling webhook"
  - "no new sandboxes being created"
tags:
  - admission
  - webhooks
  - cosign
  - cert-manager
requires:
  - admin-api
  - cluster-access
related:
  - cert-manager-outage
  - warm-pool-exhaustion
  - pool-bootstrap-mode
---

# admission-webhook-outage

Lenny's admission webhooks (OPA/Gatekeeper or Kyverno policy, and the cosign image-verification webhook) are configured with `failurePolicy: Fail`. When they are unreachable, pod admission is blocked — warm pool replenishment halts.

## Trigger

- `AdmissionWebhookUnavailable` — Lenny policy webhook unreachable past the configured sustain window.
- `CosignWebhookUnavailable` — image-verification webhook erroring past the configured sustain window.
- `WarmPoolBootstrapping` alert follows shortly after.
- `kubectl get sandbox` shows no new pods being created.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Which webhook is failing?

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get validatingwebhookconfigurations
kubectl describe validatingwebhookconfiguration lenny-admission-policy
kubectl describe validatingwebhookconfiguration lenny-cosign-verify
```

Check the `caBundle` and `service` references; if the Service has no endpoints, admission fails.

### Step 2 — Webhook pod health

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n lenny-system -l app=lenny-admission-webhook
kubectl get pods -n lenny-system -l app=lenny-cosign-webhook
```

Look for `CrashLoopBackOff`, `Pending`, or `ImagePullBackOff`.

### Step 3 — Webhook logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-admission-webhook -n lenny-system --since=5m | tail -50
kubectl logs -l app=lenny-cosign-webhook -n lenny-system --since=5m | tail -50
```

Typical errors:

- `x509: certificate has expired` → cert-manager lagging; see [cert-manager-outage](cert-manager-outage.html).
- `OOMKilled` → raise memory limits.
- `sig verification failed` (cosign) → an image without a valid signature is being admitted. Do NOT bypass; rotate the offending image.

### Step 4 — TLS serving cert

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get certificate -n lenny-system | grep -i webhook
```

`Ready=False` means cert-manager cannot rotate the webhook serving certificate.

### Step 5 — Admission blocking evidence

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl describe replicaset -n lenny-agents | grep -A3 "failed calling webhook"
```

Confirms the webhook — not some other admission controller — is the blocker.

## Remediation

### Step 1 — Pod restart

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-admission-webhook -n lenny-system
kubectl rollout restart deployment lenny-cosign-webhook -n lenny-system
kubectl rollout status deployment/lenny-admission-webhook -n lenny-system --timeout=2m
```

Resolves transient panics or deadlocks.

### Step 2 — Certificate rotation

If the TLS cert is expired or `Ready=False`:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment cert-manager -n cert-manager
kubectl delete certificate <failing-cert-name> -n lenny-system
```

cert-manager re-requests and re-issues. See [cert-manager-outage](cert-manager-outage.html) for the full procedure.

### Step 3 — No endpoints

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get endpoints <webhook-service-name> -n lenny-system
```

If empty, the Deployment has no Ready pods — diagnose Step 2 errors and fix the pod first.

### Step 4 — Emergency bypass (LAST RESORT)

Only if warm pool exhaustion is imminent and you cannot restore the webhook quickly:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl patch validatingwebhookconfiguration lenny-cosign-verify \
  --type=json -p '[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]'
```

**Only acceptable with no untrusted images in the pipeline during the bypass window.**

Restore `failurePolicy: Fail` immediately after the webhook is back:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl patch validatingwebhookconfiguration lenny-cosign-verify \
  --type=json -p '[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Fail"}]'
```

Log the bypass window in the incident record and notify security on-call.

### Step 5 — Verify recovery

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get endpoints <webhook-service-name> -n lenny-system
kubectl get validatingwebhookconfiguration lenny-cosign-verify -o yaml | grep failurePolicy
```

- Both webhooks return Ready endpoints.
- `failurePolicy: Fail` restored (bypass window closed).
- `lenny_warmpool_idle_pods` returns to `minWarm` within the pool's configured replenishment window.
- `AdmissionWebhookUnavailable` and `CosignWebhookUnavailable` alerts clear.

### Step 6 — Root cause

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl describe pod <webhook-pod> -n lenny-system | grep -A5 "Last State"
```

If OOM-killed, raise `admissionWebhook.resources.limits.memory` (and the cosign equivalent) in Helm values.

## Escalation

Escalate to:

- **Security on-call** if the cosign webhook was bypassed — a post-incident audit of what was admitted is mandatory.
- **Platform engineering** if webhook pods cannot start cleanly after restart and memory increase — likely a bug.
- **Cert-manager specialists** if TLS rotation is the root cause and recurs — investigate issuer health separately.
