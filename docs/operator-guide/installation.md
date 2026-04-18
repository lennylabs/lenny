---
layout: default
title: Installation
parent: "Operator Guide"
nav_order: 1
---

# Installation

This page covers end-to-end installation of Lenny on a Kubernetes cluster, from infrastructure prerequisites through post-install verification.

Lenny supports three installation paths. Pick one based on the scenario:

| Path | When to use | Starting point |
|------|-------------|----------------|
| **Tier 0: `lenny up`** | Evaluation, developer laptops, demos, smoke testing. Single binary, embedded k3s, same code path as production. Never production. | [`lenny up` quickstart](#tier-0-lenny-up) |
| **`lenny-ctl install` wizard** | New production installs. Interactive detection + question flow; generates a composed `values.yaml` and runs `helm install`, bootstrap, and a smoke test. | [Installer wizard](#interactive-installer-lenny-ctl-install) |
| **Direct Helm** | IaC / GitOps. Hand-write `values.yaml` or compose an answer-file base with tier overrides. | [Helm Chart Installation](#helm-chart-installation) |

All three paths exercise the same chart, the same preflight, and the same bootstrap. The wizard is a convenience layer over the third.

---

## Tier 0: `lenny up`

For evaluation and developer laptops, the single `lenny` binary runs the full platform in-process:

```bash
lenny up
```

On first run it downloads k3s to `~/.lenny/k3s/`, starts embedded Postgres, Redis, a KMS shim, an OIDC provider, the gateway, `lenny-ops`, the controllers, and the entire reference runtime catalog. The warm pool controller pre-warms pods; `lenny session new --runtime=chat --attach "hello"` is ready in under 60 seconds.

Tier 0 is gated by a non-suppressible banner stating that credentials, the KMS master key, and identities are insecure. The embedded OIDC provider refuses any audience claim not matching `dev.local`, and the gateway rejects externally-issued tokens. Any attempt to bind the gateway outside localhost fails closed with `EMBEDDED_MODE_LOCAL_ONLY`.

Use Tier 0 to understand the platform, to demo it to stakeholders, or to smoke-test a new runtime before pushing it to a real cluster. Do not use Tier 0 in production.

Teardown:

```bash
lenny down          # preserves ~/.lenny/
lenny down --purge  # deletes everything for a fresh start
```

---

## Interactive Installer (`lenny-ctl install`)

For production installs, the interactive wizard orchestrates detection, question rendering, values composition, preflight, `helm install`, bootstrap, and a smoke test against the `chat` reference runtime:

```bash
lenny-ctl install
```

The wizard runs in five phases:

1. **Detection** -- probes the target cluster for capabilities (CNI, RuntimeClass availability, admission controllers, cert-manager, Prometheus Operator, StorageClass options, cloud provider). Results seed default answers and rule out questions that do not apply.
2. **Questions** -- a small, targeted set (typically ~10 prompts) covering environment (production / staging / dev), cluster type (EKS / GKE / AKS / on-prem / k3s), backend services (managed RDS / self-hosted Postgres / cloud pooler), capacity tier, isolation profile, auth mode, and the handful of tenant/runtime/pool bootstrap entries.
3. **Preview** -- renders the composed `values.yaml` and diffs it against any existing install. Nothing is applied yet.
4. **Preflight + install** -- runs `lenny-ctl preflight`, then `helm install` (or `helm upgrade`), then `lenny-ctl bootstrap --from-values`.
5. **Smoke test** -- creates an MCP session against the `chat` reference runtime and verifies the full round trip.

### Answer files

Every interactive session can be captured to an answer file:

```bash
lenny-ctl install --save-answers ./answers.yaml
```

The saved file is plain YAML -- the wizard's Go structs serialized. Replay it non-interactively in CI/IaC:

```bash
lenny-ctl install --non-interactive --answers ./answers.yaml
```

The Lenny chart also ships **answer-file bases** for common scenarios (e.g., `answers/eks-small-team.yaml`, `answers/gke-tier2.yaml`, `answers/laptop.yaml`). Operators who prefer hand-written values can skip the wizard entirely and run `helm install -f answers/<base>.yaml -f values-tierN.yaml -f overrides.yaml`. Both paths are first-class.

### Upgrades

Upgrades replay the answer file against the existing install:

```bash
lenny-ctl upgrade --answers ./answers.yaml
```

This runs preflight, diffs the composed values against the live release, and invokes `helm upgrade` on approval.

### Airgap mode

`lenny-ctl install --offline` skips cluster-reachability probes in the detection phase. Preflight still runs against the target cluster -- only detection is affected.

---

## Cluster Prerequisites

### Required Components

| Component | Version | Purpose |
|---|---|---|
| Kubernetes | 1.28+ | Platform runtime; must support RuntimeClass, NetworkPolicy, Server-Side Apply |
| Helm | 3.12+ | Primary installation and upgrade mechanism |
| cert-manager | 1.12+ | Automated mTLS certificate provisioning and renewal for gateway-to-pod communication |
| Container runtime | containerd 1.7+ | Base container runtime; must support RuntimeClass selection |
| CNI plugin | Calico, Cilium, or cloud-native CNI + Calico policy-only mode | Must support NetworkPolicy enforcement including egress rules. On managed K8s (EKS, AKS, GKE), the recommended approach is the cloud provider's native CNI augmented with Calico in policy-only mode |
| PostgreSQL | 14+ | Session state, audit logs, billing events, credential pools, token storage |
| Redis | 7.0+ | Coordination leases, quota counters, pub/sub, routing cache; TLS + AUTH required |
| Object storage | MinIO / S3 / GCS / Azure Blob | Workspace snapshots, checkpoints, uploaded artifacts |

### Optional Components

| Component | Purpose | When Needed |
|---|---|---|
| gVisor (runsc) | Sandboxed isolation profile -- kernel-level isolation via userspace syscall interception | **Recommended for all production workloads**. Default isolation profile. |
| Kata Containers | MicroVM isolation profile -- full VM boundary per pod | High-risk workloads, semi-trusted code, multi-tenant with cross-tenant task reuse |
| OPA Gatekeeper or Kyverno | RuntimeClass-aware admission policies | **Required for production** -- enforces Pod Security Standards per RuntimeClass |
| External Secrets Operator | Synchronize credentials from AWS Secrets Manager, HashiCorp Vault, GCP Secret Manager | Tier 3 deployments with hundreds of credentials per pool |
| KEDA | ScaledObject-based HPA with direct Prometheus query (bypasses Prometheus Adapter cache) | Alternative to Prometheus Adapter for more responsive autoscaling |
| Prometheus + Grafana | Metrics collection and dashboarding | Required for observability (see [Observability](observability.html)) |
| External LLM routing proxy | Route proxy-mode traffic through a shared LLM gateway (LiteLLM, Portkey, cloud-managed) for broader provider catalog, custom routing intelligence, or shared spend reporting | Optional. Not needed when the native Go translator built into the gateway covers your provider set (`anthropic_direct`, `aws_bedrock`, `vertex_ai`, `azure_openai`). See [external LLM proxy](external-llm-proxy.md). |
| krew (for operators) | kubectl plugin manager; needed to install `kubectl-lenny` | Optional convenience for operators who prefer `kubectl lenny` over the standalone `lenny-ctl` binary. See [krew installation](krew-install.md). |

### Infrastructure Sizing Quick Reference

| Tier | Concurrent Sessions | Gateway Replicas | Postgres | Redis | MinIO |
|---|---|---|---|---|---|
| Tier 1 (Starter) | 100 | 2-3 | 2 vCPU / 4 GB | 2 GB | Single node |
| Tier 2 (Growth) | 1,000 | 5-10 | 4 vCPU / 16 GB | 8 GB | 4-node erasure coded |
| Tier 3 (Scale) | 10,000 | 25-50 | 8 vCPU / 32 GB | 16 GB | 8-node erasure coded |

See [Scaling](scaling.html) for detailed per-tier sizing guidance.

---

## Helm Chart Installation

### Step 1: Install CRDs

Helm does not update CRDs on `helm upgrade`. CRDs must be applied as a separate step before every install or upgrade.

```bash
# Download CRDs from the release
kubectl apply -f https://github.com/lenny-dev/lenny/releases/latest/download/crds.yaml

# Verify installation
kubectl get crd sandboxtemplates.lenny.dev sandboxwarmpools.lenny.dev \
  sandboxes.lenny.dev sandboxclaims.lenny.dev
```

For GitOps workflows (ArgoCD/Flux), configure CRDs as a separate sync wave:

```yaml
# ArgoCD: sync-wave -5 ensures CRDs apply before the main chart
metadata:
  annotations:
    argocd.argoproj.io/sync-wave: "-5"
```

### Step 2: Create Namespaces

The Helm chart creates these namespaces automatically, but you may want to pre-create them with appropriate labels:

```bash
kubectl create namespace lenny-system
kubectl create namespace lenny-agents
kubectl create namespace lenny-agents-kata  # Only if using Kata isolation

# Apply PSS labels (warn + audit only; enforcement is via admission policies)
kubectl label namespace lenny-agents \
  pod-security.kubernetes.io/warn=restricted \
  pod-security.kubernetes.io/audit=restricted

kubectl label namespace lenny-agents-kata \
  pod-security.kubernetes.io/warn=restricted \
  pod-security.kubernetes.io/audit=restricted
```

### Step 3: Create Infrastructure Secrets

Create Kubernetes Secrets for credential material before installing the chart:

```bash
# Postgres connection (if not using Helm values directly)
kubectl create secret generic lenny-postgres \
  -n lenny-system \
  --from-literal=dsn="postgres://lenny:password@pgbouncer:5432/lenny"

# Redis connection
kubectl create secret generic lenny-redis \
  -n lenny-system \
  --from-literal=dsn="rediss://:password@redis:6380"

# LLM provider credentials (one Secret per credential)
kubectl create secret generic lenny-anthropic-key-1 \
  -n lenny-system \
  --from-literal=apiKey="sk-ant-..."
```

### Step 4: Create values.yaml

Minimal annotated example for a functional deployment:

```yaml
global:
  devMode: false

# Infrastructure
postgres:
  connectionString: "postgres://lenny:password@pgbouncer:5432/lenny"
  # connectionPooler: pgbouncer  # or 'external' for cloud-managed proxies
redis:
  connectionString: "rediss://:password@redis:6380"
minio:
  endpoint: "https://minio.example.com"
  bucket: "lenny"
  accessKey: "minioadmin"
  secretKey: "minioadmin"

# Gateway
gateway:
  replicas: 2
  maxSessionsPerReplica: 50  # Tier 1 provisional; calibrate via ramp test

# Bootstrap seed -- creates Day-1 resources
bootstrap:
  enabled: true
  tenant:
    name: "default"
    displayName: "Default Tenant"
  runtimes:
    - name: echo
      type: agent
      image: "ghcr.io/lenny-dev/echo-runtime:latest"
      supportedProviders: []
      labels:
        team: default
  pools:
    - name: echo-pool
      runtime: echo
      isolationProfile: sandboxed
      resourceClass: small
      warmCount: 2
  credentialPools: []
```

### Step 5: Install the Helm Chart

```bash
helm repo add lenny https://charts.lenny.dev
helm repo update

helm install lenny lenny/lenny \
  --namespace lenny-system \
  --values values.yaml \
  --wait --timeout 10m
```

The install process executes in order:

1. **Preflight Job** (`helm.sh/hook: pre-install`, weight -10) -- validates all infrastructure prerequisites
2. **Schema migrations** -- applies Postgres schema migrations
3. **Component deployments** -- gateway, token service, controllers
4. **Bootstrap Job** (`helm.sh/hook: post-install`, weight 10) -- seeds Day-1 configuration
5. **CRD validation hook** (`helm.sh/hook: post-install`, weight 15) -- verifies CRD versions match the chart

---

## Preflight Checks

The preflight Job runs automatically during `helm install` and `helm upgrade`. It validates all infrastructure dependencies before any Lenny component is deployed.

### What It Validates

| Check | What It Does |
|---|---|
| Postgres connectivity | Connects and executes `SELECT 1` |
| Postgres version | Verifies server version >= 14 |
| PgBouncer pool mode | Confirms `pool_mode = transaction` (required for RLS) |
| PgBouncer connect_query | Verifies tenant sentinel is configured |
| Cloud-managed pooler defense | Checks `lenny_tenant_guard` trigger exists when using external pooler |
| Redis connectivity | Connects, verifies AUTH + TLS |
| MinIO connectivity | Verifies bucket access |
| MinIO encryption | Confirms server-side encryption is enabled |
| RuntimeClasses | Verifies referenced RuntimeClass objects exist |
| CRDs | Confirms all four agent-sandbox CRDs are installed at expected versions |
| cert-manager | Verifies CRDs installed and ClusterIssuer is Ready |
| CNI NetworkPolicy | Creates and deletes a test NetworkPolicy to verify support |
| ResourceQuota | Verifies quota exists and pod limit >= sum of minWarm |
| LimitRange | Verifies default resource requests prevent BestEffort pods |
| Kubernetes version | Confirms server version >= 1.28 |
| kube-apiserver CIDR | Validates the configured CIDR contains the actual apiserver IP |
| Internet egress CIDRs | Validates exclusion CIDRs match actual cluster CIDRs |
| etcd encryption | Non-blocking warning if encryption cannot be verified |

### Running Preflight Manually

Use `lenny-ctl` to run preflight checks outside of Helm, for example in a CI pipeline:

```bash
lenny-ctl preflight --config values.yaml

# With explicit infrastructure credentials (recommended for CI)
lenny-ctl preflight --config values.yaml \
  --postgres-dsn "$LENNY_PG_DSN" \
  --redis-dsn "$LENNY_REDIS_DSN" \
  --minio-endpoint "$LENNY_MINIO_ENDPOINT"
```

**Credential precedence** (highest wins):
1. CLI flags (`--postgres-dsn`, `--redis-dsn`, `--minio-endpoint`)
2. Environment variables (`LENNY_POSTGRES_DSN`, `LENNY_REDIS_DSN`, `LENNY_MINIO_ENDPOINT`)
3. Values file fields (`postgres.connectionString`, etc.)

### Skipping Preflight

For air-gapped or constrained environments:

```yaml
preflight:
  enabled: false  # Skips preflight; logs warning
  # timeoutSeconds: 120  # Default timeout for preflight Job
```

---

## Bootstrap

The bootstrap mechanism seeds Day-1 configuration into an empty Postgres database after installation.

### How It Works

1. The Helm chart renders bootstrap values into a ConfigMap
2. The `lenny-bootstrap` Job runs `lenny-ctl bootstrap --from-values` against the admin API
3. All resources are created using upsert semantics (create if absent, skip if present)

### Upsert Semantics

| Condition | Default Behavior | With `--force-update` |
|---|---|---|
| Resource does not exist | **Create** | Create |
| Resource exists, identical | **No-op** | No-op |
| Resource exists, differs | **Skip** (logged as WARN) | **Update** (PUT with overwrite) |
| Security-critical field change | **Error** (blocked) | **Error** (blocked) |

### Dry-Run Mode

Validate the seed file without making changes:

```bash
lenny-ctl bootstrap --dry-run --from-values bootstrap-values.yaml
```

### Initial Admin Credential

The bootstrap Job creates an initial `platform-admin` user and writes the API token to a Kubernetes Secret:

```bash
# Retrieve the admin token
kubectl get secret lenny-admin-token -n lenny-system \
  -o jsonpath='{.data.token}' | base64 -d
```

To rotate the initial admin token (internally calls `POST /v1/oauth/token` with an RFC 8693 token-exchange grant and patches the `lenny-admin-token` Kubernetes Secret):

```bash
lenny-ctl admin users rotate-token --user lenny-admin
```

### Minimum Day-1 Seed

| Resource | Required? | Purpose |
|---|---|---|
| Default tenant | Yes | At least one tenant must exist for any API call |
| At least one Runtime | Yes | Sessions require a runtime definition |
| At least one Pool | Yes | Sessions require warm pods |
| Credential pool | No | Only for real LLM providers; echo runtime needs none |
| Delegation policy | No | Default-deny is safe |
| Environment | No | Optional organizational construct |

---

## Post-Install Verification Checklist

After installation completes, verify the deployment:

```bash
# 1. Check all pods are running
kubectl get pods -n lenny-system
kubectl get pods -n lenny-agents

# 2. Verify gateway health
kubectl exec -n lenny-system deploy/lenny-gateway -- wget -qO- http://localhost:8080/healthz

# 3. Verify warm pool is populating
kubectl get sandboxwarmpools -A
kubectl get sandboxes -A

# 4. Check CRD versions match
kubectl get crd sandboxtemplates.lenny.dev \
  -o jsonpath='{.metadata.annotations.lenny\.dev/schema-version}'

# 5. Verify admission webhooks are operational
kubectl get validatingwebhookconfigurations | grep lenny

# 6. Check NetworkPolicies are applied
kubectl get networkpolicies -n lenny-agents
kubectl get networkpolicies -n lenny-system

# 7. Run smoke test (if echo runtime is seeded)
lenny-ctl preflight --config values.yaml

# 8. Verify Prometheus can scrape metrics
curl -s http://prometheus:9090/api/v1/query?query=lenny_gateway_active_sessions

# 9. Verify ResourceQuota and LimitRange
kubectl describe resourcequota -n lenny-agents
kubectl describe limitrange -n lenny-agents
```

### Expected Healthy State

- All gateway replicas are Running and Ready
- `lenny-ops` has 1-2 replicas Running with one holding the leader Lease
- Token Service has 2+ replicas Running
- Warm Pool Controller has 2+ replicas (one active leader)
- PoolScalingController has 2+ replicas (one active leader)
- Warm pool pods are reaching `idle` state within expected startup time
- No `PoolConfigDrift`, `WarmPoolBootstrapping`, or `LenniOpsSelfHealthDegraded` alerts firing
- `lenny_warmpool_idle_pods` gauge shows expected warm pod count per pool

### Structured diagnostics via `lenny-ctl doctor`

For a one-shot health assessment, run:

```bash
lenny-ctl doctor
```

This probes every subsystem via the `lenny-ops` diagnostic endpoints and renders a structured report of healthy / degraded / failing components. It does not make changes.

For common classes of misconfiguration (missing ResourceQuota, PSS labels out of date, ServiceMonitor missing required `app.kubernetes.io/instance` label, preflight trigger absent on external Postgres pooler, etc.), run with `--fix`:

```bash
lenny-ctl doctor --fix
```

The command executes idempotent remediations, each guarded by a per-finding guardrail. Every fix is recorded in the audit log with the operator identity and the before/after state. Use `--dry-run` to preview fixes without applying them.

---

## Cloud-Managed Services Integration

For production deployments, cloud-managed services are recommended over self-managed infrastructure. See the answer-file backends guidance in the spec (Section 17.9) for full details; `lenny-ctl install` detects and suggests an appropriate answer-file base when it recognizes the cloud provider.

### AWS

| Service | Lenny Component | Configuration |
|---|---|---|
| Amazon RDS (PostgreSQL) | SessionStore, TokenStore, EventStore | Multi-AZ, `db.r6g.xlarge` (Tier 2), envelope encryption via AWS KMS |
| Amazon ElastiCache (Redis) | LeaseStore, QuotaStore, routing cache | Cluster mode, TLS in-transit, AUTH token |
| Amazon S3 | ArtifactStore | Versioning enabled, lifecycle rules for noncurrent version expiry |
| AWS KMS | Envelope encryption | For etcd Secret encryption and Token Service DEK wrapping |
| Amazon RDS Proxy | Connection pooling | Use instead of self-managed PgBouncer; set `postgres.connectionPooler: external` |

### GCP

| Service | Lenny Component | Configuration |
|---|---|---|
| Cloud SQL (PostgreSQL) | SessionStore, TokenStore, EventStore | HA, `db-custom-4-16384` (Tier 2), CMEK via Cloud KMS |
| Memorystore (Redis) | LeaseStore, QuotaStore, routing cache | Standard tier (HA), TLS, AUTH |
| Google Cloud Storage | ArtifactStore | Versioning enabled, lifecycle rules |
| Cloud KMS | Envelope encryption | For Token Service DEK wrapping |
| Cloud SQL Auth Proxy | Connection pooling | Use instead of PgBouncer; set `postgres.connectionPooler: external` |

### Azure

| Service | Lenny Component | Configuration |
|---|---|---|
| Azure Database for PostgreSQL | SessionStore, TokenStore, EventStore | Flexible server with HA, `Standard_D4s_v3` (Tier 2) |
| Azure Cache for Redis | LeaseStore, QuotaStore, routing cache | Premium tier (HA), TLS, access keys |
| Azure Blob Storage | ArtifactStore | Versioning enabled, lifecycle management rules |
| Azure Key Vault | Envelope encryption | For etcd Secret encryption and Token Service DEK wrapping |

### Cloud-Managed Pooler Considerations

When using a cloud-managed connection proxy (RDS Proxy, Cloud SQL Auth Proxy, Azure PgBouncer), the `__unset__` sentinel defense cannot be configured via `connect_query`. You **must** set:

```yaml
postgres:
  connectionPooler: external
```

This triggers the Lenny schema migration to create the `lenny_tenant_guard` per-transaction validation trigger as an alternative RLS defense. The gateway will refuse to start if `connectionPooler: external` is set but the trigger is absent.
