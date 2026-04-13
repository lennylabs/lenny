---
layout: default
title: "Deploy to Kubernetes"
parent: Tutorials
nav_order: 3
---

# Deploy to Kubernetes

**Persona:** Operator | **Difficulty:** Intermediate

This tutorial walks you through deploying Lenny to a Kubernetes cluster. You will install prerequisites, configure a Helm values file, run preflight checks, bootstrap the initial configuration, and create your first session on a real cluster.

## Prerequisites

- **Kubernetes cluster** version 1.28+ (EKS, GKE, AKS, or any conformant cluster)
- **Helm 3.12+** installed locally
- **kubectl** configured to access your cluster
- **cert-manager** installed in the cluster (for mTLS certificates)
- **A CNI plugin** that supports NetworkPolicy (Calico, Cilium, or cloud-native CNI with Calico in policy-only mode)
- **Postgres 14+** accessible from the cluster (managed or self-hosted)
- **Redis** with TLS and AUTH enabled (managed or self-hosted with Sentinel)
- **MinIO** or S3-compatible object storage

---

## Step 1: Add the Helm Repository

```bash
helm repo add lenny https://charts.lenny.dev
helm repo update
```

Inspect the chart's default values to understand what you can configure:

```bash
helm show values lenny/lenny > default-values.yaml
```

This produces a commented YAML file with every configurable option. The key sections are:

```yaml
# Infrastructure
postgres:
  connectionString: ""
redis:
  connectionString: ""
minio:
  endpoint: ""
  bucket: "lenny"

# Gateway
gateway:
  replicas: 2
  maxSessionsPerReplica: 50

# Pools
pools: []

# Bootstrap
bootstrap:
  enabled: true
  tenant: {}
  runtimes: []
  pools: []
```

---

## Step 2: Create the Namespace Layout

Lenny uses three namespaces to enforce isolation boundaries:

```bash
# System namespace -- gateway, controllers, token service, stores
kubectl create namespace lenny-system

# Agent namespace -- runc and gVisor pods
kubectl create namespace lenny-agents

# Kata namespace (optional) -- microVM pods on dedicated nodes
kubectl create namespace lenny-agents-kata
```

Apply Pod Security Standards labels (warn + audit, not enforce -- enforcement is handled by RuntimeClass-aware admission policies included in the Helm chart):

```bash
kubectl label namespace lenny-agents \
  pod-security.kubernetes.io/warn=restricted \
  pod-security.kubernetes.io/audit=restricted

kubectl label namespace lenny-agents-kata \
  pod-security.kubernetes.io/warn=restricted \
  pod-security.kubernetes.io/audit=restricted
```

---

## Step 3: Configure values.yaml

Create a `values.yaml` for your deployment. This example configures a single pool with runc isolation, single-tenant mode, and embedded stores suitable for testing.

```yaml
# values.yaml -- Lenny deployment configuration

global:
  devMode: false  # NEVER true in production

# --- Infrastructure Dependencies ---

postgres:
  # Use your actual PgBouncer or direct connection string.
  # PgBouncer MUST use pool_mode=transaction for RLS enforcement.
  connectionString: "postgres://lenny:changeme@pgbouncer.lenny-system:5432/lenny?sslmode=require"

redis:
  # rediss:// prefix = TLS enabled. Required for production.
  connectionString: "rediss://:changeme@redis.lenny-system:6380"

minio:
  endpoint: "https://minio.lenny-system:9000"
  bucket: "lenny"
  accessKey: "lennyaccess"
  secretKey: "lennysecret"  # In production, use a Kubernetes Secret reference

# --- Gateway Configuration ---

gateway:
  replicas: 2
  maxSessionsPerReplica: 50  # Provisional; calibrate with benchmarks
  resources:
    requests:
      cpu: "500m"
      memory: "512Mi"
    limits:
      cpu: "2"
      memory: "2Gi"

# --- Agent Namespaces ---

agentNamespaces:
  - name: lenny-agents
    resourceQuota:
      pods: 50
      requests.cpu: "100"
      requests.memory: "200Gi"
    limitRange:
      defaultRequest:
        cpu: "250m"
        memory: "256Mi"
      default:
        cpu: "2"
        memory: "2Gi"

# --- Bootstrap Configuration ---

bootstrap:
  enabled: true

  # Create a default tenant
  tenant:
    name: "default"
    displayName: "Default Tenant"

  # Register the echo runtime for smoke testing
  runtimes:
    - name: echo
      type: agent
      image: ghcr.io/lenny-dev/echo-runtime:latest
      description: "Echo runtime for testing"
      tenantAccess: ["default"]

  # Create a pool with 2 warm pods
  pools:
    - name: default-pool
      runtime: echo
      namespace: lenny-agents
      isolationProfile: runc
      executionMode: session
      warmCount:
        min: 2
        max: 5
      resources:
        requests:
          cpu: "250m"
          memory: "256Mi"
        limits:
          cpu: "1"
          memory: "1Gi"
      tenantAccess: ["default"]

  # RBAC: allow all users in the default tenant (dev/single-tenant)
  rbacConfig:
    noEnvironmentPolicy: allow-all
```

### Key Configuration Decisions

| Decision | This Example | Production Recommendation |
|----------|-------------|--------------------------|
| Isolation profile | `runc` | `gvisor` for multi-tenant |
| Warm pod count | 2 min, 5 max | Size to expected concurrency |
| Gateway replicas | 2 | 3+ with PDB |
| noEnvironmentPolicy | `allow-all` | `deny-all` with environments |
| Stores | External | Managed cloud services |

---

## Step 4: Install cert-manager (if needed)

Lenny requires cert-manager for mTLS certificates between the gateway and agent pods.

```bash
# Check if cert-manager is already installed
kubectl get pods -n cert-manager

# If not installed:
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.0/cert-manager.yaml

# Wait for it to be ready
kubectl wait --for=condition=Available deployment/cert-manager -n cert-manager --timeout=120s
kubectl wait --for=condition=Available deployment/cert-manager-webhook -n cert-manager --timeout=120s
```

Verify the ClusterIssuer is ready (Lenny's preflight check validates this):

```bash
kubectl get clusterissuer
```

If you do not have a ClusterIssuer, create a self-signed one for testing:

```yaml
# cluster-issuer.yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: lenny-selfsigned
spec:
  selfSigned: {}
```

```bash
kubectl apply -f cluster-issuer.yaml
```

---

## Step 5: Install Lenny

### Apply CRDs First

Helm does not update CRDs on `helm upgrade`. You must always apply CRDs as a separate step:

```bash
# Download and apply CRDs
kubectl apply -f https://github.com/lenny-dev/lenny/releases/latest/download/crds.yaml
```

Verify CRDs are installed:

```bash
kubectl get crd | grep lenny
```

Expected output:

```
sandboxclaims.lenny.dev       2026-04-09T10:00:00Z
sandboxes.lenny.dev           2026-04-09T10:00:00Z
sandboxtemplates.lenny.dev    2026-04-09T10:00:00Z
sandboxwarmpools.lenny.dev    2026-04-09T10:00:00Z
```

### Run Helm Install

```bash
helm install lenny lenny/lenny \
  -n lenny-system \
  -f values.yaml \
  --wait \
  --timeout 10m
```

The `--wait` flag ensures Helm waits for all resources to be ready before returning. The installation will:

1. Run the **preflight Job** (validates all infrastructure prerequisites)
2. Deploy the gateway, token service, and controllers
3. Run database migrations
4. Run the **bootstrap Job** (seeds tenants, runtimes, pools)
5. Create warm pods in the agent namespace

---

## Step 6: Run Preflight Checks

The preflight Job runs automatically during `helm install`. If it fails, the install aborts with a clear error message. You can also run preflight manually:

```bash
lenny-ctl preflight --config values.yaml
```

Or check the preflight Job logs:

```bash
kubectl logs job/lenny-preflight -n lenny-system
```

### Expected Preflight Output

```
[PASS] Postgres connectivity: connected to postgres:5432
[PASS] Postgres version: 16.1
[PASS] PgBouncer pool_mode: transaction
[PASS] PgBouncer connect_query: contains tenant sentinel
[PASS] Redis connectivity: connected
[PASS] Redis AUTH/TLS: verified
[PASS] MinIO connectivity: bucket 'lenny' accessible
[PASS] MinIO encryption: SSE enabled
[PASS] RuntimeClasses: 'runc' exists
[PASS] Agent-sandbox CRDs: all 4 CRDs at expected versions
[PASS] cert-manager: ClusterIssuer 'lenny-selfsigned' is Ready
[PASS] CNI NetworkPolicy: verified
[PASS] Namespace ResourceQuota: lenny-agents allows 50 pods (need >= 5)
[PASS] Namespace LimitRange: lenny-agents has default requests
[PASS] Kubernetes version: 1.29.2

All preflight checks passed.
```

### Common Preflight Failures

| Check | Failure | Fix |
|-------|---------|-----|
| PgBouncer pool_mode | `pool_mode is 'session'` | Change PgBouncer to `pool_mode = transaction` |
| Redis AUTH/TLS | `TLS handshake failed` | Ensure Redis URL uses `rediss://` and TLS is enabled |
| CNI NetworkPolicy | `does not support NetworkPolicy` | Install Calico or Cilium, or enable Calico in policy-only mode alongside your cloud CNI |
| ResourceQuota | `allows 20 pods but configured pools require 25` | Increase `agentNamespaces[].resourceQuota.pods` |

---

## Step 7: Bootstrap Initial Configuration

The bootstrap Job runs automatically after Helm install. Verify it completed:

```bash
kubectl get job lenny-bootstrap -n lenny-system
```

Expected output:

```
NAME              COMPLETIONS   DURATION   AGE
lenny-bootstrap   1/1           8s         2m
```

You can also run bootstrap manually with `lenny-ctl`:

```bash
lenny-ctl bootstrap --from-values values.yaml
```

### Retrieve the Admin Token

The bootstrap Job creates an initial admin token stored in a Kubernetes Secret:

```bash
kubectl get secret lenny-admin-token -n lenny-system \
  -o jsonpath='{.data.token}' | base64 -d
```

Save this token -- you will need it for admin API calls. Example:

```
lenny_admin_01J5K9ABCDEF_a3f1c7e2d9b8...
```

---

## Step 8: Verify the Deployment

### Check Pods

```bash
kubectl get pods -n lenny-system
```

Expected output:

```
NAME                                    READY   STATUS    RESTARTS   AGE
lenny-gateway-7d8f9b6c4d-abc12         1/1     Running   0          3m
lenny-gateway-7d8f9b6c4d-def34         1/1     Running   0          3m
lenny-token-svc-5c7b8a9d3f-ghi56       1/1     Running   0          3m
lenny-token-svc-5c7b8a9d3f-jkl78       1/1     Running   0          3m
lenny-warm-pool-ctrl-6f8c7d5b2e-mno90   1/1     Running   0          3m
lenny-pool-scaling-ctrl-4a9b8c7d1f-pqr  1/1     Running   0          3m
```

```bash
kubectl get pods -n lenny-agents
```

Expected output (2 warm pods from the default pool):

```
NAME                                  READY   STATUS    RESTARTS   AGE
sandbox-default-pool-warm-abc12       2/2     Running   0          2m
sandbox-default-pool-warm-def34       2/2     Running   0          2m
```

Each agent pod has 2 containers: the adapter sidecar and the runtime binary.

### Check Services

```bash
kubectl get svc -n lenny-system
```

Expected output:

```
NAME                TYPE        CLUSTER-IP     EXTERNAL-IP   PORT(S)
lenny-gateway       ClusterIP   10.96.10.100   <none>        8080/TCP
lenny-token-svc     ClusterIP   10.96.10.101   <none>        8081/TCP
```

### Check CRDs

```bash
kubectl get sandboxwarmpools -n lenny-agents
```

Expected output:

```
NAME           RUNTIME   MIN-WARM   MAX-WARM   READY   AGE
default-pool   echo      2          5          2       3m
```

---

## Step 9: Create Your First Session via REST API

Now test the full session lifecycle against your cluster:

```bash
GATEWAY_URL="http://$(kubectl get svc lenny-gateway -n lenny-system -o jsonpath='{.spec.clusterIP}'):8080"
TOKEN="$(kubectl get secret lenny-admin-token -n lenny-system -o jsonpath='{.data.token}' | base64 -d)"

# Create a session
curl -s -X POST "${GATEWAY_URL}/v1/sessions/start" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "echo",
    "input": [{"type": "text", "inline": "Hello from Kubernetes!"}]
  }' | jq .
```

Expected response:

```json
{
  "session_id": "sess_01J5K9CLUSTER01",
  "state": "running",
  "sessionIsolationLevel": {
    "executionMode": "session",
    "isolationProfile": "runc",
    "podReuse": false
  }
}
```

If you are accessing the gateway from outside the cluster (e.g., via an Ingress), use that URL instead.

---

## Step 10: Monitor with lenny-ctl

The `lenny-ctl` CLI provides operational commands for monitoring and managing your deployment.

### List Pools

```bash
lenny-ctl admin pools list \
  --gateway "${GATEWAY_URL}" \
  --token "${TOKEN}"
```

Expected output:

```
NAME           RUNTIME   WARM   ACTIVE   MAX    ISOLATION   STATUS
default-pool   echo      2      0        5      runc        healthy
```

### Check Pool Warm Status

```bash
lenny-ctl admin pools describe default-pool \
  --gateway "${GATEWAY_URL}" \
  --token "${TOKEN}"
```

Expected output:

```
Pool: default-pool
  Runtime:          echo
  Namespace:        lenny-agents
  Isolation:        runc
  Execution Mode:   session
  Warm Pods:        2 / 2 (min) / 5 (max)
  Active Sessions:  0
  CRD Sync:         in-sync (lag: 0s)
  Health:           healthy
```

### List Sessions

```bash
lenny-ctl sessions list \
  --gateway "${GATEWAY_URL}" \
  --token "${TOKEN}"
```

---

## Production Considerations

> **Before going to production**, review the following checklist:

| Area | Requirement | Reference |
|------|-------------|-----------|
| **TLS** | Enable TLS on the Ingress and between gateway and pods (mTLS) | Helm `tls.*` values |
| **Authentication** | Configure OIDC provider for client authentication | Helm `auth.oidc.*` values |
| **Isolation** | Use `gvisor` or `kata` isolation profile for multi-tenant | Pool `isolationProfile` |
| **PgBouncer** | Verify `pool_mode=transaction` and `connect_query` sentinel | Section 12.3 |
| **Redis** | TLS + AUTH required; Sentinel for HA | Section 12.4 |
| **MinIO** | Server-side encryption enabled; erasure coding for durability | Section 12.5 |
| **Monitoring** | Deploy Prometheus + Grafana; configure alerts from Section 16 | Helm `monitoring.*` values |
| **Backups** | Continuous WAL archival for Postgres; daily MinIO replication | Section 17.3 |
| **ResourceQuotas** | Size quotas to accommodate warm pool + active sessions | Section 17.2 |
| **noEnvironmentPolicy** | Set to `deny-all` and configure environments for access control | Section 4.2 |
| **Credential pools** | Configure LLM provider credentials with encryption | Section 4.9 |
| **etcd encryption** | Enable EncryptionConfiguration for Secrets at rest | Section 13 |

---

## Common Installation Issues

### Pods stuck in Pending

```bash
kubectl describe pod sandbox-default-pool-warm-abc12 -n lenny-agents
```

**Cause:** Insufficient cluster resources or ResourceQuota too low.
**Fix:** Increase node count or adjust `agentNamespaces[].resourceQuota`.

### Gateway CrashLoopBackOff

```bash
kubectl logs deployment/lenny-gateway -n lenny-system
```

**Cause:** Usually a database connection failure or stale CRDs.
**Fix:** Check Postgres connectivity and apply CRDs (`kubectl apply -f charts/lenny/crds/`).

### Warm pods never reach Ready

```bash
kubectl describe pod sandbox-default-pool-warm-abc12 -n lenny-agents
```

**Cause:** The adapter cannot connect to the gateway (mTLS certificate issue or NetworkPolicy blocking).
**Fix:** Verify cert-manager is issuing certificates and NetworkPolicy allows lenny-agents to reach lenny-system.

### Bootstrap Job fails

```bash
kubectl logs job/lenny-bootstrap -n lenny-system
```

**Cause:** Gateway not yet ready when bootstrap runs.
**Fix:** The bootstrap command has a `--wait-timeout` (default 120s). Increase if your gateway takes longer to start.

---

## Upgrading

Every upgrade requires CRDs to be applied first:

```bash
# 1. Apply CRDs
kubectl apply -f https://github.com/lenny-dev/lenny/releases/download/vX.Y.Z/crds.yaml

# 2. Run Helm upgrade
helm upgrade lenny lenny/lenny \
  -n lenny-system \
  -f values.yaml \
  --wait \
  --timeout 10m
```

The post-upgrade CRD validation hook will catch stale CRDs if you forget step 1.

---

## Next Steps

- [Multi-Tenant Setup](multi-tenant-setup) -- configure tenant isolation, quotas, and OIDC
- [Build a Runtime Adapter](build-a-runtime) -- create and deploy custom runtimes
