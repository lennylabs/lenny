---
layout: default
title: "network-policy-drift"
parent: "Runbooks"
triggers:
  - alert: NetworkPolicyCIDRDrift
    severity: critical
components:
  - cluster
symptoms:
  - "installed NetworkPolicy CIDRs no longer match cluster CIDRs"
  - "pods unable to reach services despite policy existing"
  - "admin webhook flags policy deviation"
tags:
  - network-policy
  - cidr
  - cluster
  - cni
requires:
  - cluster-access
related:
  - dns-outage
  - admission-webhook-outage
---

# network-policy-drift

The NetworkPolicies installed in the cluster reference CIDR ranges (pod, service, node) that no longer match the current cluster configuration. Typical causes: cluster re-provisioning with different CIDRs, CNI migration, or manual edits.

## Trigger

- `NetworkPolicyCIDRDrift` alert.
- Pod-to-service connectivity errors that disappear when policies are deleted.

## Diagnosis

### Step 1 — Current cluster CIDRs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl cluster-info dump | grep -iE "cluster-cidr|service-cluster-ip-range"
```

Or from kube-system flags:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n kube-system -l component=kube-controller-manager \
  -o yaml | grep -E "cluster-cidr|service-cluster-ip-range"
```

### Step 2 — CIDRs referenced in Lenny NetworkPolicies

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get networkpolicy -n lenny-system -o yaml \
  | grep -E "cidr:"
kubectl get networkpolicy -n lenny-agents -o yaml \
  | grep -E "cidr:"
```

Compare.

### Step 3 — Which policies drifted?

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get networkpolicy -A \
  -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}: {.spec}{"\n"}{end}' \
  | grep -E "lenny|<old-cidr>"
```

## Remediation

### Step 1 — Update values

Update Helm values with the current cluster CIDRs:

```yaml
networkPolicies:
  clusterCIDRs:
    pod: "10.244.0.0/16"
    service: "10.96.0.0/12"
    node: "10.0.0.0/8"
```

### Step 2 — Apply

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml
```

This re-renders all Lenny NetworkPolicies with the corrected CIDRs.

### Step 3 — Verify connectivity

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl run -it --rm connectivity-test --image=busybox:stable --restart=Never \
  -n lenny-system -- \
  sh -c "nc -zv lenny-gateway 443 && nc -zv postgres 5432"
```

Expected: both succeed.

### Step 4 — Verify

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get networkpolicy -n lenny-system -o yaml | grep -A2 ipBlock
```

- Installed CIDRs match current cluster CIDRs.
- `NetworkPolicyCIDRDrift` clears within its evaluation window.
- Pod-to-service connectivity succeeds end-to-end.

## Escalation

Escalate to:

- **Cluster admin** when cluster CIDRs are controlled by provider (EKS/GKE/AKS) and cannot be changed — Lenny values must be updated to match, not the other way around.
- **CNI / networking specialists** when policies look correct but connectivity still fails — may indicate a CNI-level enforcement issue.
- **Security on-call** if the drift correlates with an unintended permissive policy (e.g., `0.0.0.0/0` egress) — this is a compliance issue.
