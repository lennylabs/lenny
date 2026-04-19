---
layout: default
title: "redis-failure"
parent: "Runbooks"
triggers:
  - alert: RedisUnavailable
    severity: critical
  - alert: RedisMemoryHigh
    severity: warning
  - alert: RateLimitDegraded
    severity: warning
components:
  - redis
symptoms:
  - "quota enforcement switched to fail-open"
  - "delegation budgets not updating"
  - "rate limiter logs redis_unavailable"
tags:
  - redis
  - caching
  - quota
  - rate-limiting
requires:
  - admin-api
  - cluster-access
related:
  - delegation-budget-recovery
  - dual-store-unavailable
---

# redis-failure

Redis is unreachable or evicting. Quota and rate-limit enforcement enter fail-open mode within a bounded window, and delegation budgets may stale.

## Trigger

- `RedisUnavailable` alert.
- `RateLimitDegraded` alert (fail-open active).
- `RedisMemoryHigh` alert: Redis memory approaching `maxmemory`.
- `lenny_quota_redis_fallback_total` counter incrementing.
- `/v1/admin/health` returns `redis: degraded` or `unhealthy`.

## Diagnosis

### Step 1 — Reachability

<!-- access: kubectl requires=cluster-access -->
```bash
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls PING
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls INFO replication
```

Expected: `PONG` and a `role:master` / `role:slave` line. Timeouts or auth errors indicate reachability or credential issues.

### Step 2 — Cluster state

<!-- access: kubectl requires=cluster-access -->
```bash
# Redis Cluster:
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls CLUSTER INFO

# Sentinel:
redis-cli -p 26379 SENTINEL masters
```

`cluster_state:ok` and full slot coverage required. Sentinel `num-other-sentinels` must match your quorum size.

### Step 3 — Memory pressure

<!-- access: kubectl requires=cluster-access -->
```bash
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls INFO memory
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls INFO keyspace
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls INFO stats | grep evicted
```

`used_memory_rss_human` near `maxmemory`, rising `evicted_keys`, or a sudden keyspace shift point at memory exhaustion.

### Step 4 — Fail-open window

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-gateway --since=5m | grep -E "redis_unavailable|fallback"
```

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_quota_redis_fallback_total&window=15m
```

A rising `lenny_quota_redis_fallback_total` means quota enforcement is running on cached replica counts, not live values. The window is bounded by the deployer-configured `lenny_quota_redis_fallback_window`.

## Remediation

### Step 1 — Transient reachability problem

If Redis is reachable again within the fallback window, no action is needed -- the gateway reconnects and exits fail-open automatically.

Verify:

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose connectivity
```

### Step 2 — Redis is recoverable

1. **Sentinel / Cluster:** promote a healthy replica or replace the failed node following your HA procedure.
2. **Standalone:** restart the Redis process or pod. Existing connections drop; the gateway reconnects.

### Step 3 — Restore quota accuracy

After Redis is back:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin quota reconcile --all-tenants
```

<!-- access: api method=POST path=/v1/admin/quota/reconcile -->
```
POST /v1/admin/quota/reconcile
{"scope": "all-tenants"}
```

This rebuilds Redis counters from authoritative Postgres state, closing any gap from the fail-open window.

### Step 4 — Memory pressure

If diagnosis showed memory exhaustion:

1. Increase `maxmemory` on the Redis instance (requires cluster admin for self-managed; provider console for managed).
2. If rapid key growth from a specific keyspace, identify the owner:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   redis-cli --bigkeys
   ```
3. Long-term: shard hot tenants or split Redis databases by feature (quota / rate-limit / cache) via the `redis.database` Helm values.

### Step 5 — Assess billing impact

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_quota_redis_fallback_total&window=1h
```

If the counter climbed materially during the fail-open window, any tenant may have exceeded quota without enforcement. Review the [billing-stream-backlog](billing-stream-backlog.html) runbook for the correction-approval flow and file corrections for overcharges or refunds as needed.

## Escalation

Escalate if:

- Redis cannot be restored within 5 minutes of the first fail-open event (the bounded fallback window tolerates short outages, not structural ones).
- Both Redis and Postgres are unavailable -- follow [dual-store-unavailable](dual-store-unavailable.html) instead; that is a critical-severity event.
- `lenny_quota_redis_fallback_total` rises after Redis appears healthy, pointing at an intermittent connectivity problem.
- Memory pressure recurred within 24 hours despite the previous fix -- revisit sizing.
