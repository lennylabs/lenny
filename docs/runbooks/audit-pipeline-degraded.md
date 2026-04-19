---
layout: default
title: "audit-pipeline-degraded"
parent: "Runbooks"
triggers:
  - alert: OCSFTranslationBacklog
    severity: warning
  - alert: AuditLockContention
    severity: warning
  - alert: EventBusPublishDropped
    severity: warning
components:
  - audit
symptoms:
  - "ocsf_translation_state=retry_pending rows accumulating"
  - "audit lock acquisition p99 elevated against baseline"
  - "event bus publish drops rising"
tags:
  - audit
  - ocsf
  - event-bus
  - compliance
requires:
  - admin-api
  - cluster-access
related:
  - redis-failure
  - postgres-failover
  - billing-stream-backlog
---

# audit-pipeline-degraded

One or more audit-pipeline subsystems are degraded: OCSF translation backlog, audit-lock contention, or event-bus publish drops. Covered here because the three failure modes share diagnosis and escalation paths.

## Trigger

- `OCSFTranslationBacklog`: `lenny_audit_ocsf_translation_failed_total` rising AND `audit_log` rows with `ocsf_translation_state='retry_pending'` exceed `audit.ocsf.alertThreshold`, OR any row transitions to `dead_lettered`.
- `AuditLockContention`: audit-lock acquire p99 elevated above its configured threshold AND `lenny_audit_concurrency_timeout_total` rising.
- `EventBusPublishDropped`: `rate(lenny_event_bus_publish_dropped_total[5m])` above `eventBus.dropAlertThreshold`.

Exact thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Per-subsystem error messages

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -n lenny-system deploy/lenny-gateway --since=15m \
  | grep -E "ocsf_translation|AUDIT_CONCURRENCY|eventbus_publish"
```

### Step 2 — OCSF translation state

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?ocsf_translation_state=retry_pending&since=1h
GET /v1/admin/audit-events?ocsf_translation_state=dead_lettered&since=24h
```

- Persistent `retry_pending` → translator or downstream SIEM pressure.
- `dead_lettered` rows → translator-level rejections; already recorded as class 2004 audit receipts.

### Step 3 — Audit-lock contention hot tenants

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT tenant_id, count(*)
   FROM pg_stat_activity
   WHERE query LIKE '%pg_advisory_xact_lock%'
   GROUP BY tenant_id ORDER BY count DESC LIMIT 10;"
```

A handful of tenants dominating advisory-lock waits is the typical shape.

### Step 4 — EventBus drop backlog

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?eventbus_publish_state=failed&since=<alert_fire_time>
```

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_event_bus_replay_buffer_utilization&window=15m
```

Replay-buffer utilization approaching 100 % indicates drops will accelerate — Redis health is usually the underlying cause (see [redis-failure](redis-failure.html)).

## Remediation

### Step 1 — OCSF translation backlog

1. Verify SIEM endpoint reachability from the gateway:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl exec deploy/lenny-gateway -- curl -sv <siem-endpoint> | head -20
   ```
2. If transient: no action — retries resume automatically; watch `retry_pending` drain.
3. If persistent: raise `audit.ocsf.alertThreshold` temporarily via Helm values and scale the translator worker pool:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl scale deployment lenny-ocsf-translator -n lenny-system --replicas=<N+1>
   ```
4. Dead-lettered receipts remain queryable under class `2004 unmapped.lenny_dead_letter`; investigate their structure to decide whether a translator schema update is needed.

### Step 2 — Audit-lock contention

1. Reduce new-session pressure on the hot tenant:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   # Helm upgrade lowering oauth.rateLimit.tenantPerSecond for the tenant
   ```
   This throttles `/v1/oauth/token` for that tenant, indirectly slowing new-session creation.
2. Force-terminate long-running sessions owned by the hot tenant to drain in-flight audit writes:
   <!-- access: lenny-ctl -->
   ```bash
   lenny-ctl admin sessions force-terminate <session-id>
   ```
3. If systemic: increase the Postgres connection pool (see [pgbouncer-saturation](pgbouncer-saturation.html)) or shard the hot tenant's audit writes onto a dedicated partition.

### Step 3 — EventBus drops

1. Reconcile subscribers by replaying failed publishes:
   <!-- access: api method=POST path=/v1/admin/audit-events/replay -->
   ```
   POST /v1/admin/audit-events/replay
   {"filter": {"eventbus_publish_state": "failed"}, "since": "<alert_fire_time>"}
   ```
2. Restore Redis health before clearing markers (Redis pub/sub is usually the bottleneck): see [redis-failure](redis-failure.html).
3. Only after Redis is healthy and the replay succeeded, clear the `eventbus_publish_state=failed` markers.

### Step 4 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose audit
```

- `ocsf_translation_state=retry_pending` row count drops toward 0.
- `lenny_audit_lock_acquire_seconds` p99 returns within its alert threshold.
- `lenny_event_bus_publish_dropped_total` rate flat.

## Escalation

Escalate to:

- **Security on-call** if `dead_lettered` receipts include sensitive event classes (e.g., auth failures); downstream SIEM correlation may be incomplete.
- **Finance ops** if the incident coincides with a billing window — billing cross-checks use `audit_log` as ground truth.
- **Compliance officer** if the backlog exceeded the regulatory reporting window (varies by jurisdiction; typically 24 h).

Cross-reference: Spec §11.7 (audit logging), §12.6 (interface design), §16.5 (alerting rules).
