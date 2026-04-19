---
layout: default
title: "token-service-outage"
parent: "Runbooks"
triggers:
  - alert: TokenServiceUnavailable
    severity: critical
  - alert: TokenServiceCircuitOpen
    severity: critical
components:
  - tokenService
symptoms:
  - "CREDENTIAL_MATERIALIZATION_ERROR on new sessions"
  - "lenny_gateway_token_service_circuit_state == 2 (open)"
  - "token-service pods CrashLoopBackOff"
tags:
  - tokens
  - kms
  - credentials
  - circuit-breaker
requires:
  - admin-api
  - cluster-access
related:
  - credential-pool-exhaustion
  - credential-revocation
  - postgres-failover
---

# token-service-outage

The Token Service (the process that materializes leased credentials by decrypting Secrets with KMS) is unavailable. New sessions that require a credential materialization fail with `CREDENTIAL_MATERIALIZATION_ERROR`. Existing sessions holding active leases are unaffected until lease expiry.

## Trigger

- `TokenServiceUnavailable` alert.
- Gateway circuit breaker sustained in `open` state (`lenny_gateway_token_service_circuit_state == 2`) past the configured sustain window.
- New sessions return `CREDENTIAL_MATERIALIZATION_ERROR`.
- `/v1/admin/health` returns `tokenService: unhealthy`.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Pod health

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -l app=lenny-token-service -n lenny-system
kubectl describe pod <failing-pod> -n lenny-system | tail -30
```

Look for `CrashLoopBackOff`, OOM kills, or panics.

### Step 2 — Recent logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-token-service -n lenny-system --since=5m | tail -100
```

Common error classes:

- `kms: AccessDenied` / `PermissionDenied` — IAM or ServiceAccount misconfiguration.
- `kms: dial tcp ...` — KMS endpoint unreachable (network policy, VPC routing, provider outage).
- `secrets "..." is forbidden` — RBAC on the Token Service ServiceAccount broken.
- `panic:` — bug; capture the stack trace and restart.

### Step 3 — KMS reachability

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -it <token-service-pod> -n lenny-system -- \
  <provider-specific-kms-check>
# AWS example:
#   aws kms describe-key --key-id <key-arn>
# GCP example:
#   gcloud kms keys describe <key> --keyring <ring> --location <loc>
```

### Step 4 — RBAC on the Token Service SA

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl auth can-i get secret -n lenny-system \
  --as=system:serviceaccount:lenny-system:lenny-token-service
```

A `no` indicates RBAC drift from the installed chart.

### Step 5 — Circuit breaker state

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_token_service_circuit_state&window=15m
```

`0` = closed (healthy), `1` = half-open, `2` = open (circuit tripped).

## Remediation

### Step 1 — Active sessions buy you time

Sessions with active leases continue running until lease expiry (`credentialLeaseTTL`, minutes to hours). Prioritize restoring Token Service *before* leases expire.

### Step 2 — Restart the Token Service

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-token-service -n lenny-system
kubectl rollout status deployment lenny-token-service -n lenny-system --timeout=2m
```

Resolves transient panics, deadlocks, or stale KMS connections.

### Step 3 — KMS unreachable

1. Check the cloud provider KMS status page.
2. Verify ServiceAccount IAM binding (IRSA on AWS, Workload Identity on GCP, Azure AD Workload Identity on AKS) using provider event logs.
3. Test from a fresh pod in the same namespace — if it works, the Token Service pod has stale credentials; restart it (Step 2).

### Step 4 — RBAC fix

If Secret access is denied, re-apply Token Service RBAC from the Helm chart:

<!-- access: kubectl requires=cluster-access -->
```bash
helm template lenny lennylabs/lenny -f values.yaml | \
  kubectl apply -f - -l app.kubernetes.io/component=token-service
```

### Step 5 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose connectivity
```

- Token Service pods `Ready=True`.
- `lenny_gateway_token_service_circuit_state` returns to `0` (the circuit half-opens automatically after two consecutive successful health probes).
- New session creation succeeds end-to-end with a credential pool.

### Step 6 — Assess impact

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT session_id, state, termination_reason
   FROM sessions
   WHERE state IN ('failed','expired')
     AND termination_reason = 'CREDENTIAL_MATERIALIZATION_ERROR'
     AND created_at > now() - interval '1 hour';"
```

Inform affected tenants; credential-materialization failures are retryable (the client can create a new session once the service is healthy).

## Escalation

Escalate to:

- **Cloud provider support** for KMS outages that exceed the provider's documented RTO.
- **Security on-call** if the Token Service was observed decrypting with stale or incorrect key versions; key rotation may need to be verified against the key-version registry.
- **Platform engineering** if panics recur after a restart — capture a crashdump and file an incident; a Token Service bug can block all new session creation.
