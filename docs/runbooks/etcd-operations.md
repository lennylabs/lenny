---
layout: default
title: "etcd-operations"
parent: "Runbooks"
triggers:
  - alert: EtcdQuotaNearLimit
    severity: warning
  - alert: EtcdUnavailable
    severity: critical
components:
  - cluster
symptoms:
  - "kube-apiserver write latency climbing"
  - "etcd DB size approaching the configured quota"
  - "controller reconcile failures from etcd errors"
tags:
  - etcd
  - kubernetes
  - defrag
  - compaction
requires:
  - cluster-access
related:
  - controller-leader-election
  - warm-pool-exhaustion
---

# etcd-operations

Operational procedures for etcd maintenance. Lenny writes heavily to etcd via CRDs (Sandbox, SandboxClaim, Runtime, and related resources). Under sustained write pressure, defragmentation and compaction become ongoing operational concerns.

This runbook covers: (1) live-cluster defragmentation, (2) compaction setting changes, (3) quota-exhaustion recovery, and (4) escalation.

## Trigger

- `EtcdQuotaNearLimit` — etcd DB size approaching the configured `--quota-backend-bytes`.
- `EtcdUnavailable` — kube-apiserver cannot reach etcd.
- Controller logs: `etcdserver: mvcc: database space exceeded`.
- Kube-apiserver p99 write latency climbing.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Member health (self-managed only)

<!-- access: kubectl requires=cluster-access -->
```bash
ETCDCTL_API=3 etcdctl --endpoints=<member1,member2,member3> endpoint health
ETCDCTL_API=3 etcdctl --endpoints=<...> endpoint status --write-out=table
```

Identify the leader and confirm all members are reachable and in sync.

### Step 2 — DB size and quota

<!-- access: kubectl requires=cluster-access -->
```bash
ETCDCTL_API=3 etcdctl --endpoints=<...> endpoint status --write-out=json \
  | jq '.[] | {ep: .Endpoint, dbSize: .Status.dbSize, dbSizeInUse: .Status.dbSizeInUse}'
```

A large gap between `dbSize` and `dbSizeInUse` indicates space recoverable by defrag.

### Step 3 — Key metrics

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=etcd_disk_wal_fsync_duration_seconds&window=15m
GET /v1/admin/metrics?q=etcd_server_proposals_committed_total&window=15m
GET /v1/admin/metrics?q=etcd_debugging_mvcc_db_total_size_in_bytes&window=15m
```

Capture these values before escalating — cloud-provider support will ask for them.

### Step 4 — Alarm state

<!-- access: kubectl requires=cluster-access -->
```bash
ETCDCTL_API=3 etcdctl --endpoints=<...> alarm list
```

If `NOSPACE` is present, etcd is in read-only mode for writes — follow quota recovery below.

## Remediation

### Procedure 1 — Live-cluster defragmentation

Defragment one member at a time (followers first, then leader). Each defrag **pauses writes** on that member for seconds to minutes depending on DB size.

1. Identify leader (Step 1) and list followers.
2. Defrag each follower:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   ETCDCTL_API=3 etcdctl --endpoints=<follower-1> defrag
   ```
3. Verify member health returns before moving to the next:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   ETCDCTL_API=3 etcdctl --endpoints=<follower-1> endpoint health
   ```
4. Defrag the leader last. Expect a brief leadership transition during the defrag — tolerable on 3-node and 5-node clusters.

> **Tier 3 deployments with narrow off-peak windows:** script the follower rotation and run during the lowest-traffic 15-minute window you have.

### Procedure 2 — Compaction setting changes (self-managed)

1. Update the `--auto-compaction-mode` and `--auto-compaction-retention` flags on the etcd members (typically via static pod manifest or systemd unit).
2. Restart members rolling, leader last (same pattern as defrag).
3. Verify `etcd_debugging_mvcc_db_total_size_in_bytes` starts declining within one compaction interval.

For **managed Kubernetes** (EKS, GKE, AKS), compaction flags are provider-controlled — escalate (Section below).

### Procedure 3 — Quota-exhaustion recovery

If `alarm list` shows `NOSPACE`:

1. Defrag **all** members (Procedure 1).
2. Verify space reclaimed:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   ETCDCTL_API=3 etcdctl --endpoints=<...> endpoint status --write-out=table
   ```
3. Disarm the alarm:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   ETCDCTL_API=3 etcdctl --endpoints=<...> alarm disarm
   ```
4. Verify kube-apiserver write capability is restored — create a test ConfigMap:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl create configmap etcd-write-test --from-literal=ok=true
   kubectl delete configmap etcd-write-test
   ```

### Procedure 4 — Long-term growth

If `etcd_debugging_mvcc_db_total_size_in_bytes` grows faster than compaction can reclaim:

1. Audit Sandbox retention — Lenny should garbage-collect terminated Sandboxes promptly. Check `lenny_warmpool_terminated_age_seconds` distribution.
2. Reduce CRD verbosity — review custom controller status fields for unnecessary updates.
3. Increase `--quota-backend-bytes` only as a last resort; it is a hard cap, not a solution to sustained growth.

## Escalation

Escalate to:

- **Cloud provider support** for managed K8s (EKS, GKE, AKS) when etcd is unreachable or the provider controls compaction flags. Provide the metrics captured in Diagnosis Step 3.
- **Cluster admin / SRE** for self-managed etcd when defrag does not reclaim space, or when a member cannot rejoin after restart.
- **Platform engineering** if CRD volume itself appears unbounded — indicates a controller bug leaking resources.

Cross-reference: `EtcdQuotaNearLimit` / `EtcdUnavailable` alerts (see [metrics reference](../reference/metrics.html)).
