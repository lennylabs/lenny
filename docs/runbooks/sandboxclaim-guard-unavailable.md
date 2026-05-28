---
layout: default
title: "sandboxclaim-guard-unavailable"
parent: "Runbooks"
triggers:
  - alert: SandboxClaimGuardUnavailable
    severity: critical
components:
  - admission
symptoms:
  - "SandboxClaim PATCH and PUT operations rejected by the failure policy"
  - "new pod claims blocked"
  - "session creation fails"
tags:
  - admission
  - webhooks
  - sandbox
requires:
  - cluster-access
related:
  - admission-webhook-outage
---

# sandboxclaim-guard-unavailable

The `lenny-sandboxclaim-guard` ValidatingAdmissionWebhook has been unreachable past its `failurePolicy: Fail` sustain window. SandboxClaim mutations are denied — new pod claims fail, halting session creation.

## Trigger

The `SandboxClaimGuardUnavailable` alert fires when `up{job="lenny-sandboxclaim-guard"} == 0` holds for more than 30s.

## Diagnosis

### Step 1 — Webhook reachability

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get pods -l app=lenny-sandboxclaim-guard
kubectl -n lenny-system describe validatingwebhookconfiguration lenny-sandboxclaim-guard
```

Look for missing endpoints, a stale `caBundle`, or pods in `CrashLoopBackOff`.

### Step 2 — Cert-manager-issued certificate

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get certificate -l app=lenny-sandboxclaim-guard
```

A `False` Ready condition points at cert-manager — follow [cert-manager-outage.md](./cert-manager-outage.md).

## Remediation

1. Restart the webhook Deployment: `kubectl -n lenny-system rollout restart deployment/lenny-sandboxclaim-guard`.
2. If the issuing certificate is expired or missing, repair the cert-manager pipeline first ([cert-manager-outage.md](./cert-manager-outage.md)).
3. Once the pods report Ready, confirm the alert clears within one evaluation cycle.

## Verification

`kubectl get sandboxclaim` and a fresh session create succeed without admission errors.

## Escalation

Page the on-call platform engineer when the webhook does not recover after one Deployment restart and the cert-manager pipeline is healthy.

Cross-reference: [§13.1](../../spec/13_security-model.md#131-pod-security), [admission-webhook-outage.md](./admission-webhook-outage.md).
