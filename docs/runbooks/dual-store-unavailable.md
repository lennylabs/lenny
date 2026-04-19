---
layout: default
title: "dual-store-unavailable"
parent: "Runbooks"
triggers:
  - alert: DualStoreUnavailable
    severity: critical
components:
  - postgres
  - redis
symptoms:
  - "Postgres and Redis simultaneously unreachable"
  - "platform-wide session creation blocked"
  - "quota enforcement fully offline"
tags:
  - postgres
  - redis
  - dual-failure
  - critical
requires:
  - admin-api
  - cluster-access
related:
  - postgres-failover
  - redis-failure
---

# dual-store-unavailable

Both Postgres and Redis are unavailable at the same time. This is the highest-severity stateful-dependency event: quota enforcement cannot fail open (Redis down) and session state cannot be written (Postgres down). The platform fails closed across the board.

## Trigger

- `DualStoreUnavailable` alert.
- `/v1/admin/health` reports both `postgres: unhealthy` and `redis: unhealthy`.
- Session creation rejects platform-wide.

## Diagnosis

### Step 1 — Confirm both are down

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c "SELECT 1;"
redis-cli -h <redis-host> -a "$REDIS_PASSWORD" --tls PING
```

Rule out a false-positive alert.

### Step 2 — Common cause?

Dual-store failure often has a shared root cause:

- **Cluster network partition** — both backends unreachable from the gateway subnet.
- **DNS outage** — gateway cannot resolve either service name; see [dns-outage](dns-outage.html).
- **Shared node pool failure** (if both are co-located) — the node pool hosting both went down.
- **Simultaneous planned maintenance** — check with cluster admin.

### Step 3 — Narrow the scope

<!-- access: kubectl requires=cluster-access -->
```bash
# Test reachability from a different pod, different node
kubectl run -it --rm net-test --image=busybox:stable --restart=Never -- \
  sh -c "nc -zv <postgres-host> 5432; nc -zv <redis-host> 6379"
```

If reachable from one pod but not another, the issue is gateway-pod-scoped (likely NetworkPolicy or node-local).

## Remediation

### Step 1 — Do NOT bypass fail-closed

There is no fail-open mode for both stores simultaneously. Quota, rate-limit, session, and token operations are all blocked. Accept the outage and focus on restoring one store.

### Step 2 — Restore the easiest store first

Priority order:

1. **Postgres** — session state, tokens, and delegations are Postgres-authoritative. Restoring Postgres unblocks new session creation. Follow [postgres-failover](postgres-failover.html).
2. **Redis** — quota and rate-limit enforcement. Restoring Redis restores enforcement. Follow [redis-failure](redis-failure.html).

Once one store is healthy, `/v1/admin/health` returns `degraded` (not `unhealthy`), and the gateway begins accepting traffic under the single-store fail-open path specific to whichever store is still down.

### Step 3 — Investigate shared root cause

Before closing the incident, determine whether the dual failure has a common root cause. If so, document in the incident record and file a remediation item (e.g., spread Postgres and Redis across different node pools or availability zones).

### Step 4 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose connectivity
```

- Both stores reachable and Ready.
- `DualStoreUnavailable` cleared.
- `lenny_quota_redis_fallback_total` flat.
- Session creation succeeds end-to-end.

## Escalation

**Escalate immediately** on `DualStoreUnavailable`:

- **Platform on-call** — this is a platform-wide outage; full incident handling required.
- **Cluster admin** — for shared infrastructure causes (network, DNS, node pools, provider-side maintenance).
- **Cloud provider support** — for managed-service dual failures.
- **Security on-call** — in the rare case that dual failure correlates with suspected intrusion, preserve evidence before any destructive recovery action.
