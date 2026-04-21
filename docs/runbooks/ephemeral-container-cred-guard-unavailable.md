---
layout: default
title: "ephemeral-container-cred-guard-unavailable"
parent: "Runbooks"
triggers:
  - alert: EphemeralContainerCredGuardUnavailable
    severity: warning
components:
  - admission
symptoms:
  - "lenny-ephemeral-container-cred-guard webhook unreachable > 5 min"
  - "pods/ephemeralcontainers updates denied fail-closed in agent namespaces"
  - "kubectl debug attach attempts rejected"
tags:
  - admission
  - webhooks
  - credentials
  - ephemeral-containers
  - cred-readers
requires:
  - admin-api
  - cluster-access
related:
  - admission-webhook-outage
  - admission-plane-feature-flag-downgrade
  - cert-manager-outage
  - credential-pool-exhaustion
---

# ephemeral-container-cred-guard-unavailable

The `lenny-ephemeral-container-cred-guard` ValidatingAdmissionWebhook is the fail-closed gate that prevents an actor with `update` on `pods/ephemeralcontainers` from attaching a debug container that could read the credential file at `/run/lenny/credentials.json`. While the webhook is unreachable, `failurePolicy: Fail` denies every `update` to `pods/ephemeralcontainers` in every agent namespace — the credential-boundary invariant remains protected, but `kubectl debug` and similar ephemeral-container workflows will fail until the webhook recovers.

See SPEC §13.1 "`lenny-cred-readers` membership boundary" for the four rejection conditions the webhook enforces; SPEC §17.2 admission-policies inventory item 13 for the webhook's baseline placement.

## Trigger

- `EphemeralContainerCredGuardUnavailable` — `up{job="lenny-ephemeral-container-cred-guard"} == 0` for more than 5 minutes.
- Operator reports: `kubectl debug <pod> -n lenny-agents …` returns an admission-webhook error even though the pod is running.
- Platform-internal workflows that attach ephemeral containers (SRE tooling, debug sidecars) similarly fail fail-closed.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Webhook pod health

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n lenny-system -l app=lenny-ephemeral-container-cred-guard
kubectl describe deployment lenny-ephemeral-container-cred-guard -n lenny-system
```

Look for `CrashLoopBackOff`, `Pending`, `ImagePullBackOff`, or fewer than the expected 2 ready replicas (SPEC §17.2 HA contract: `replicas: 2`, `podDisruptionBudget.minAvailable: 1`).

### Step 2 — Webhook Service endpoints

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get endpoints lenny-ephemeral-container-cred-guard -n lenny-system
```

If empty, the backing Deployment has no Ready pods.

### Step 3 — Webhook configuration

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl describe validatingwebhookconfiguration lenny-ephemeral-container-cred-guard
```

Confirm the `caBundle` is populated, the `service` reference is correct, and the `failurePolicy: Fail` and `rules.resources: [pods/ephemeralcontainers]` scoping are intact. A `caBundle` that does not match the serving cert is a common cause after a cert-manager-driven cert rotation.

### Step 4 — TLS serving certificate

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get certificate -n lenny-system | grep ephemeral-container-cred-guard
```

`Ready=False` means cert-manager has not been able to rotate the webhook serving certificate; see [cert-manager-outage](cert-manager-outage.html).

### Step 5 — Webhook pod logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -n lenny-system -l app=lenny-ephemeral-container-cred-guard --since=5m | tail -80
```

Typical errors:

- `x509: certificate has expired` → cert-manager lag; fix via Remediation Step 2.
- `OOMKilled` in the preceding container state → raise memory limits.
- `context deadline exceeded` from the apiserver-side event → the webhook is slow; profile the admission-handler path and/or scale replicas.

### Step 6 — Confirm fail-closed is the observed behavior

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl debug <pod-in-agent-namespace> -n lenny-agents --image=busybox -- sh -c 'true' 2>&1
```

Expect a webhook-denial error. This confirms the credential boundary is still enforced (the webhook's absence denies admission, the invariant is intact, SPEC §13.1 conditions (i)–(iv) continue to hold because every attach is blocked at the apiserver).

## Remediation

### Step 1 — Pod restart

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-ephemeral-container-cred-guard -n lenny-system
kubectl rollout status deployment/lenny-ephemeral-container-cred-guard -n lenny-system --timeout=2m
```

Resolves transient panics, deadlocks, or cert-cache staleness.

### Step 2 — Certificate rotation recovery

If the TLS cert is expired or `Ready=False`, follow the [cert-manager-outage](cert-manager-outage.html) runbook and force re-issuance of the webhook serving cert:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl delete certificate lenny-ephemeral-container-cred-guard -n lenny-system
```

cert-manager re-requests and re-issues. When the new cert is injected into the `caBundle` (cert-manager's `ca-injector` on the `ValidatingWebhookConfiguration`), validate the webhook accepts traffic again via Step 5 below.

### Step 3 — No endpoints / replica count below PDB

If `kubectl get endpoints` returns empty, or the number of ready replicas is below `podDisruptionBudget.minAvailable`, the backing Deployment cannot serve — diagnose the pod-health failure first (Step 1 of Diagnosis), fix the pod, and re-check endpoints.

### Step 4 — Do NOT bypass to `failurePolicy: Ignore`

Unlike the `lenny-cosign-verify` emergency-bypass path in the [admission-webhook-outage](admission-webhook-outage.html) runbook, this webhook MUST NOT be flipped to `failurePolicy: Ignore` — doing so would let an actor with `update` on `pods/ephemeralcontainers` attach a debug container, mount the credential tmpfs volume, and read the credential file. The credential-boundary invariant is the whole point of this webhook (SPEC §13.1 conditions (i)–(iv)); accepting an admission gap is strictly worse than the operational impact of denied `kubectl debug` attempts.

If ephemeral-container attach is urgently needed for another incident (e.g., diagnosing a separate session outage), use the pod's existing containers (`kubectl exec`, `kubectl logs`) instead of attaching a new one.

### Step 5 — Verify recovery

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get endpoints lenny-ephemeral-container-cred-guard -n lenny-system
kubectl get validatingwebhookconfiguration lenny-ephemeral-container-cred-guard -o yaml | grep failurePolicy
```

- The webhook Service has ready endpoints matching the expected replica count.
- `failurePolicy: Fail` is intact (it should never have been changed; Step 4).
- `EphemeralContainerCredGuardUnavailable` alert clears within one evaluation cycle.
- A controlled `kubectl debug` attach that does NOT touch the credential tmpfs succeeds (verifies the webhook is admitting correctly, not just returning fail-closed for everything).

### Step 6 — Root cause

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl describe pod -l app=lenny-ephemeral-container-cred-guard -n lenny-system | grep -A5 "Last State"
```

If OOM-killed, raise the webhook Deployment's `resources.limits.memory` in Helm values. If the root cause is cert rotation, audit the cert-manager issuer and ACME challenge configuration.

## Escalation

Escalate to:

- **Security on-call** if the webhook was unreachable for a prolonged period AND there is any evidence (audit log, node-local container runtime logs) that an ephemeral container was attached to an agent pod during the gap — the `failurePolicy: Fail` contract should have denied every such attempt, but an incident where the webhook was bypassed requires immediate investigation of the `lenny-cred-readers` membership and the credential file's integrity.
- **Cert-manager specialists** if TLS rotation is the root cause and recurs — investigate issuer health, ACME rate limits, or the `ca-injector` pod as separate upstream problems.
- **Platform engineering** if the webhook pod cannot start cleanly after restart and memory increase — likely a bug in the webhook binary.

Cross-reference: [SPEC §13.1](https://github.com/lennylabs/lenny/blob/main/spec/13_security-model.md#131-pod-security) "`lenny-cred-readers` membership boundary"; [SPEC §17.2](https://github.com/lennylabs/lenny/blob/main/spec/17_deployment-topology.md#172-namespace-layout) admission-policies inventory item 13; [SPEC §16.5](https://github.com/lennylabs/lenny/blob/main/spec/16_observability.md#165-alerting-rules-and-slos) `EphemeralContainerCredGuardUnavailable`; [Metrics Reference](../reference/metrics.html#alert-rules).
