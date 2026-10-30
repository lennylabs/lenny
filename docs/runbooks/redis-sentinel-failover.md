---
layout: default
title: "redis-sentinel-failover"
parent: "Runbooks"
triggers:
  - alert: RedisMasterFailover
    severity: warning
components:
  - redis
  - gateway
symptoms:
  - "Redis Sentinel promoted a replica to master"
  - "the gateway's per-tenant quota counters returned 0 briefly during the handoff"
  - "the §11.2 MAX-rule reconciliation rolled the in-memory counter forward from the Postgres checkpoint"
tags:
  - chaos
  - store-failure
  - redis
related:
  - postgres-failover
  - audit-pipeline-degraded
  - emergency-credential-revocation
---

# redis-sentinel-failover

Redis Sentinel promoted a replica to master after the previous master became unreachable. The gateway's Sentinel-aware client (`pkg/redisconn`) reconnects to the new master without operator action; the §11.2 MAX-rule reconciliation pipeline replays the Postgres quota checkpoint onto the new master so per-tenant counters do not roll back.

## Trigger

`RedisMasterFailover` fires when Sentinel reports a `+switch-master` event or the gateway's Sentinel client emits the `lenny_redis_master_changed_total` counter.

## Diagnosis

### Step 1 — Identify the new master

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -n lenny-system statefulset/lenny-redis-sentinel -- \
  redis-cli -p 26379 SENTINEL get-master-addr-by-name mymaster
```

### Step 2 — Confirm the gateway is using the new master

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -n lenny-system deployment/lenny-gateway -- \
  curl -s localhost:9090/metrics | grep lenny_redis_current_master
```

### Step 3 — Inspect the previous master's logs for the failure cause

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -n lenny-system statefulset/lenny-redis --previous
```

Look for an out-of-memory kill, a network partition, or replication lag on the demoted master.

## Remediation

### Step 4 — Force a quota checkpoint reload if reconciliation lagged

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin quota reconcile --tenant <id>
```

Once the failure cause is resolved, restart the demoted master so it rejoins as a replica. If the §11.2 MAX-rule reconciliation did not catch up, the command above forces a checkpoint reload. If repeated failovers occur within an hour, see the Redis Sentinel documentation for split-brain mitigation and add a third Sentinel node.

## Verification

- `redis-cli -p 26379 SENTINEL replicas mymaster` lists the demoted node as a healthy replica.
- `lenny_redis_master_changed_total` no longer advances.
- A test session start through the gateway succeeds and `lenny_quota_redis_fallback_total` stops advancing, confirming quota enforcement reads Redis again rather than the fail-open path.

## Escalation

Page the on-call platform engineer when more than one failover occurs in a single Sentinel quorum window — that pattern points at network instability rather than a single-node fault.
