---
layout: default
title: "total-outage"
parent: "Runbooks"
triggers:
  - alert: GatewayNoHealthyReplicas
    severity: critical
  - alert: OpsPlaneUnavailable
    severity: critical
components:
  - gateway
  - controlPlane
symptoms:
  - "both gateway and lenny-ops unreachable"
  - "all API surfaces down: client, admin, MCP"
  - "no ops tooling responding"
tags:
  - total-outage
  - emergency
  - escape-hatch
  - control-plane
requires:
  - cluster-access
related:
  - gateway-replica-failure
  - postgres-failover
  - dns-outage
  - etcd-operations
---

# total-outage

Both `lenny-ops` and the gateway are unreachable. Client traffic is failing and the normal operability surface is gone. This runbook covers the escape hatches for when the Admin API itself is down — they assume only `kubectl` and cloud-provider console access.

## Trigger

- `GatewayNoHealthyReplicas` AND `lenny-ops` Ingress health check both failing past their configured sustain windows.
- Client requests returning 503 at the Ingress for the gateway **and** no response on `/v1/admin/*`.
- `lenny-ctl diagnose *` returning `ENDPOINT_UNAVAILABLE`.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Confirm dependency state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n lenny-system -l 'app in (gateway,lenny-ops)' -o wide
kubectl get pods -n lenny-system -l 'app in (postgres,redis,minio)' -o wide
kubectl get events -n lenny-system --sort-by='.lastTimestamp' | tail -30
```

Pin down whether the outage is in the Lenny Deployments themselves, their dependencies, or the cluster underneath them.

### Step 2 — Dependency plane

If Postgres or Redis is down, follow [postgres-failover](postgres-failover.html) or [redis-failure](redis-failure.html) first. The gateway and `lenny-ops` will not recover until their backing stores are.

### Step 3 — Cluster layer

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get nodes
kubectl get apiservice | grep -v "True"
kubectl -n kube-system get pods | grep -v Running
```

If the API server, CoreDNS, or core controllers are down, you are in a cluster-layer incident — follow [dns-outage](dns-outage.html) / [etcd-operations](etcd-operations.html) or escalate to the cluster admin.

## Remediation

### Step 1 — Escape hatch to `lenny-ops`

If the Ingress is down but the `lenny-ops` pod is running, reach it via port-forward. This bypasses the Ingress and NetworkPolicy and is the supported emergency access path:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl port-forward -n lenny-system svc/lenny-ops 8090:8090
```

Then call `/v1/admin/diagnostics/*` against `http://localhost:8090`. `lenny-ctl --server http://localhost:8090 ...` works the same way.

Port-forward access still requires a valid admin token — the bypass is of the network path, not of authentication.

### Step 2 — Restart the gateway

If the gateway is crashlooping, follow [gateway-replica-failure](gateway-replica-failure.html). Do **not** force-delete Deployments before reading that runbook.

### Step 3 — Restart `lenny-ops`

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment/lenny-ops -n lenny-system
kubectl rollout status deployment/lenny-ops -n lenny-system --timeout=2m
```

If the restart fails repeatedly, inspect pod logs for a startup-dependency error (typically Postgres or KMS). Fix the dependency; do not loosen `lenny-ops` startup checks.

### Step 4 — Restore client traffic

Once either the gateway or `lenny-ops` is up:

- Gateway back: client traffic resumes — verify with a session create.
- `lenny-ops` back: you regain the full operability surface; resume the normal alert-to-runbook workflow.

### Step 5 — Post-incident

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin incidents record \
  --title "total-outage <short-reason>" \
  --duration <minutes> \
  --affected-components gateway,lenny-ops
```

Capture:

- Root cause(s) — usually a dependency (Postgres/Redis/KMS) or a cluster-layer event.
- Whether the escape-hatch path worked on first try. If not, fix the documented steps here.
- Whether alerting fired on both surfaces simultaneously, or only one of them.

## Escalation

Escalate to:

- **Cluster admin** — immediately, in parallel with Step 1, for any symptoms of cluster-layer failure (Node NotReady, CoreDNS down, API server unreachable).
- **Cloud provider support** — for managed-dependency outages (RDS, ElastiCache, object store, KMS).
- **Lenny platform on-call** — if the gateway and `lenny-ops` remain down with dependencies healthy: the control plane itself has a bug.
- **Security on-call** — if the outage is preceded by or coincident with any compromise indicator (anomalous auth attempts, credential alerts). Do not assume coincidence.

A total outage should also trigger the tenant-facing status-page update. Follow the Deployer Comms Runbook (org-specific) in parallel with this one.
