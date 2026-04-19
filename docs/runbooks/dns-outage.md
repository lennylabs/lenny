---
layout: default
title: "dns-outage"
parent: "Runbooks"
triggers:
  - alert: DedicatedDNSUnavailable
    severity: critical
components:
  - cluster
symptoms:
  - "gateway cannot resolve service names"
  - "CoreDNS replica pods all unready"
  - "dial tcp: lookup ... no such host"
tags:
  - dns
  - coredns
  - cluster
  - networking
requires:
  - cluster-access
related:
  - gateway-replica-failure
  - network-policy-drift
---

# dns-outage

The dedicated CoreDNS deployment (Lenny runs a dedicated CoreDNS to isolate platform traffic from tenant traffic) has no healthy replicas. Gateway and controller DNS lookups fail.

## Trigger

- `DedicatedDNSUnavailable` — all dedicated CoreDNS replicas unready past the configured sustain window.
- Gateway logs: `dial tcp: lookup <svc> on <dns-ip>: no such host` or `i/o timeout`.
- Controller reconcile errors referencing DNS.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — CoreDNS pod state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n lenny-system -l app=lenny-coredns -o wide
kubectl describe pod <coredns-pod> -n lenny-system
```

Look for `CrashLoopBackOff`, `Pending`, OOM, or probe failures.

### Step 2 — Service endpoints

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get endpoints lenny-coredns -n lenny-system
kubectl get svc lenny-coredns -n lenny-system
```

Endpoints must list one or more Ready pod IPs. No endpoints → no DNS.

### Step 3 — Config sanity

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get configmap lenny-coredns -n lenny-system -o yaml | head -40
```

Recent ConfigMap corruption (bad Corefile) causes CoreDNS to crash-loop.

### Step 4 — Upstream reachability

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec <coredns-pod> -n lenny-system -- \
  dig @<upstream-resolver> example.com +short
```

If the CoreDNS replica cannot reach its upstream, DNS resolution fails even when CoreDNS itself is up.

## Remediation

### Step 1 — Pod-level restart

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-coredns -n lenny-system
kubectl rollout status deployment lenny-coredns -n lenny-system --timeout=2m
```

### Step 2 — Config rollback

If a recent ConfigMap change is suspect:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout history configmap lenny-coredns -n lenny-system  # if versioned via GitOps
```

Revert to the prior Corefile via Helm or Git.

### Step 3 — Scale up

Temporary relief while investigating:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl scale deployment lenny-coredns -n lenny-system --replicas=3
```

### Step 4 — Upstream outage

If upstream DNS is unreachable from the cluster, this is a cluster-level networking issue — escalate to cluster admin.

### Step 5 — Verify

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl run -it --rm dns-test --image=busybox:stable --restart=Never -- \
  nslookup lenny-gateway.lenny-system.svc.cluster.local
```

Answer must include the gateway Service ClusterIP.

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose dns
```

- All CoreDNS replicas Ready.
- Test lookups succeed from a cluster-local pod.
- Alert clears.

## Escalation

Escalate to:

- **Cluster admin** for cluster-networking issues (CNI, ipvs/iptables, kube-proxy).
- **Network operations** if upstream DNS reachability is the problem (firewall change, ISP-side).
- **Platform engineering** for persistent CoreDNS crashes that don't match a config change — could be a bug in a CoreDNS plugin.
