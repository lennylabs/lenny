---
layout: default
title: "cert-manager-outage"
parent: "Runbooks"
triggers:
  - alert: CertExpiryImminent
    severity: warning
  - alert: AdmissionWebhookUnavailable
    severity: critical
components:
  - certManager
symptoms:
  - "warm pool warming stalls -- new pods cannot get certificates"
  - "kubectl get certificates shows Ready=False"
  - "admission-webhook TLS verification fails"
tags:
  - mtls
  - certificates
  - admission
  - cert-manager
requires:
  - cluster-access
related:
  - admission-webhook-outage
  - warm-pool-exhaustion
---

# cert-manager-outage

cert-manager cannot issue or rotate certificates. Lenny's internal mTLS chain depends on it: new agent pods cannot join the warm pool without a valid certificate, and admission webhook serving certs rotate through it. Existing running pods are unaffected until their cert TTL expires.

## Trigger

- `AdmissionWebhookUnavailable` when the webhook's serving cert is expired or missing.
- `CertExpiryImminent` (mTLS cert expiry < 1 hour).
- Pod certificate rotation failures in agent-pool logs.
- `kubectl get certificates -n lenny-system` showing `Ready=False` on one or more resources.
- Warm pool warming stalls with events mentioning `waiting for certificate`.

## Diagnosis

### Step 1 — cert-manager pod health

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n cert-manager
kubectl logs -l app=cert-manager -n cert-manager --since=5m | tail -50
kubectl logs -l app=webhook -n cert-manager --since=5m | tail -50
```

Look for ACME / DNS / Kubernetes API errors.

### Step 2 — Certificate state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get certificates -n lenny-system
kubectl get certificaterequests -n lenny-system --sort-by=.metadata.creationTimestamp | tail -20
kubectl describe certificate <failing-name> -n lenny-system
```

Look for the `Reason` field in `describe` -- `Failed`, `Pending`, `Issuing` each dictate different next steps.

### Step 3 — Issuer health

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get clusterissuer
kubectl describe clusterissuer <issuer-name>
```

Ready status must be `True`. For ACME issuers, the `Conditions` section tells you whether the challenge provider (DNS-01 or HTTP-01) is working.

### Step 4 — Downstream impact

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_warmpool_warmup_failure_total&groupBy=error_type&window=10m
```

`error_type=certificate_pending` points directly at this runbook.

## Remediation

### Step 1 — cert-manager pods down

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment cert-manager -n cert-manager
kubectl rollout restart deployment cert-manager-webhook -n cert-manager
kubectl rollout status deployment cert-manager -n cert-manager --timeout=2m
```

cert-manager re-reconciles all Certificates on startup and re-issues anything expiring soon.

### Step 2 — Issuer failure

If diagnosis shows the `ClusterIssuer` as not ready:

- **ACME (Let's Encrypt):** verify the DNS-01 or HTTP-01 challenge provider is reachable and credentials are valid.
- **CA issuer / SelfSigned:** verify the signing key Secret exists and matches what the Issuer references.
- **Vault issuer:** verify Vault reachability and the AppRole or ServiceAccount token used by cert-manager.

Re-apply the fixed issuer:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl apply -f clusterissuer.yaml
```

### Step 3 — Stuck Certificate

If a Certificate is stuck in `Pending`:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl delete certificaterequest -n lenny-system -l cert-manager.io/certificate-name=<name>
kubectl delete certificate <name> -n lenny-system
# re-apply from Helm or Git source of truth
```

cert-manager re-requests the cert and the latest CertificateRequest should transition through `Pending` -> `Approved` -> `Issued` within 2 minutes.

### Step 4 — Already-expired webhook cert (emergency)

If an admission-webhook serving cert has expired and cluster operations are blocked:

<!-- access: kubectl requires=cluster-access -->
```bash
# Manual issuance as last resort. Replace with the real Secret name.
kubectl cert-manager renew <certificate-name> -n lenny-system
```

This triggers a forced re-issuance. Do not bypass the webhook by setting `failurePolicy: Ignore` unless absolutely required -- it temporarily admits unsigned images and unvalidated pods.

### Step 5 — Verify recovery

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get certificate -n lenny-system
kubectl get certificaterequests -n lenny-system --sort-by=.metadata.creationTimestamp | tail
```

- All Certificates `Ready=True`.
- Recent CertificateRequests show `Approved=True`, `Ready=True`.

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose cert-manager
```

Verifies the chain end-to-end: cert-manager pods healthy, all Lenny Certificates ready, no expiring certs inside the `certManager.warningWindow` (default 24h), admission webhook serving certs valid.

Warm pool replenishment resumes automatically once new pods can be issued certs; confirm:

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_warmpool_idle_pods&window=5m
```

## Escalation

Escalate if:

- cert-manager pods cannot start cleanly and restarting does not help -- cluster admin for deeper Kubernetes issues.
- ACME provider is returning persistent challenge failures (DNS propagation, rate limits on issuance).
- More than 4 hours have elapsed since cert-manager failed -- existing pod certs reach the default 4 h TTL and new session pods start failing.
- You needed to set `failurePolicy: Ignore` on a webhook to restore service. Log the bypass window in the incident record, restore `failurePolicy: Fail` immediately after recovery, and page security on-call to audit what was admitted during the bypass.
