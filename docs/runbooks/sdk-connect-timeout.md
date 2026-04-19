---
layout: default
title: "sdk-connect-timeout"
parent: "Runbooks"
triggers:
  - alert: SDKConnectTimeout
    severity: warning
components:
  - warmPools
symptoms:
  - "SDK connect timeout rate sustained above its configured threshold"
  - "clients fail to establish MCP session on fresh pods"
  - "cold-start latency elevated"
tags:
  - sdk
  - mcp
  - warm-pool
  - connect
requires:
  - admin-api
  - cluster-access
related:
  - warm-pool-exhaustion
  - dns-outage
---

# sdk-connect-timeout

Clients are timing out while establishing the initial MCP connection to a freshly-claimed warm pod. Symptom is rate-based, not per-session: sustained timeout rate above the configured threshold indicates a systemic issue.

## Trigger

- `SDKConnectTimeout` alert.
- SDK clients report connection timeout errors during session creation.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Timeout rate breakdown

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_sdk_connect_timeout_total[5m])&groupBy=pool,runtime_class&window=30m
```

Is the timeout scoped to one pool / runtime class, or cluster-wide?

### Step 2 — Pod startup latency

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_warmpool_pod_startup_duration_seconds_bucket[5m]))&groupBy=pool&window=30m
```

If p95 startup is elevated, clients are giving up before the pod is ready. Check the pool scaling and image.

### Step 3 — Gateway-to-pod reachability

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-gateway --since=5m \
  | grep -E "pod-connect|dial tcp|timeout" | tail
```

`dial tcp: i/o timeout` from the gateway to pod IPs indicates CNI or DNS issues (see [dns-outage](dns-outage.html)).

### Step 4 — Client SDK version

SDKs have version-specific connect-timeout defaults. An old SDK with a too-short default fails first.

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_sdk_connect_timeout_total&groupBy=sdk_version&window=30m
```

## Remediation

### Step 1 — Warm pool pressure

If pool-local: follow [warm-pool-exhaustion](warm-pool-exhaustion.html). Slow pod startup at claim time is the most common cause of connect timeouts.

### Step 2 — Pre-warm more pods

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools set-warm-count --pool <name> --min <N+10>
```

Absorbs bursts and reduces the probability of cold-start claims.

### Step 3 — DNS / CNI issues

If the timeout is at the TCP-connect layer, not SDK-level, see [dns-outage](dns-outage.html) and check CNI plugin health with your cluster admin.

### Step 4 — Client-side

For old SDKs with aggressive timeouts: advise clients to upgrade. Lenny's gateway enforces a `connectGracePeriod` on pod claim, giving the SDK up to that window.

### Step 5 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_sdk_connect_timeout_total[5m])&window=30m
```

- Timeout rate returns to baseline.
- p95 pod startup duration within tier SLO.
- Alert clears.

## Escalation

Escalate to:

- **Cluster admin** for sustained CNI or DNS issues at the node/networking layer.
- **SDK maintainers** for version-specific timeout defaults that can't absorb healthy pod startup time.
- **Capacity owner** if pool bootstrap latency is inherent to the runtime image (large image, slow setup) — may require pre-bake or image optimization.
