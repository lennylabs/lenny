---
layout: default
title: Installation
parent: "Operator Guide"
nav_order: 1
---

# Installation

This page walks you through installing Lenny on a Kubernetes cluster, from the prerequisites through verifying the install worked.

There are three ways to install. Pick based on what you're doing:

| Path | Use it for | Starting point |
|------|------------|----------------|
| **`lenny up`** | Evaluation, developer laptops, demos, smoke-testing a new runtime before you touch a real cluster. Runs the entire platform as a single binary. Never for production. | [`lenny up` quickstart](#lenny-up-for-local-evaluation) |
| **`lenny-ctl install` wizard** | New production installs. Detects your cluster, asks a small set of questions, then runs Helm and bootstrap for you. | [Installer wizard](#interactive-installer-lenny-ctl-install) |
| **Direct Helm** | GitOps or infrastructure-as-code setups. Hand-write `values.yaml` (or start from an answer-file base) and run Helm yourself. | [Helm chart installation](#helm-chart-installation) |

All three paths use the same chart, the same preflight checks, and the same bootstrap. The wizard is a friendlier layer over Helm.

---

## `lenny up` for local evaluation
{: #lenny-up-for-local-evaluation }

For evaluation and developer laptops, the `lenny` binary runs the entire platform in-process:

```bash
lenny up
```

On first run it downloads k3s to `~/.lenny/k3s/` and starts the whole stack -- embedded Kubernetes, Postgres, Redis, a development key-management shim, an identity provider, the gateway, the management plane, the controllers, and the reference runtime catalog. The pool controller warms up pods, and `lenny session new --runtime chat --message "hello"` is ready in under a minute.

`lenny up` prints a banner you can't suppress: credentials, master key, and identities are insecure, and it's not for production. The built-in identity provider rejects any audience claim other than `dev.local`, and trying to expose the gateway beyond localhost fails with `EMBEDDED_MODE_LOCAL_ONLY`.

Use it to explore the platform, demo it, or smoke-test a new runtime before you ship it to a real cluster.

Teardown:

```bash
lenny down          # stop everything, keep ~/.lenny/
lenny down --purge  # stop everything and wipe ~/.lenny/
```

---

## Interactive installer (`lenny-ctl install`)

For production installs, the interactive wizard handles detection, questions, composing `values.yaml`, running preflight, invoking `helm install`, bootstrap, and a final smoke test against the `chat` runtime:

```bash
lenny-ctl install
```

It runs in five phases:

1. **Detection** -- probes the cluster: CNI, `RuntimeClass` availability, admission controllers, cert-manager, Prometheus Operator, storage classes, cloud provider. The results seed default answers and skip questions that don't apply.
2. **Questions** -- around 10 prompts: environment (production / staging / dev), cluster type (EKS / GKE / AKS / on-prem / k3s), database and cache options (managed or self-hosted), deployment size, isolation profile, auth setup, and a few tenant/runtime/pool seed entries.
3. **Preview** -- shows the composed `values.yaml` and diffs it against any existing install. Nothing is applied yet.
4. **Preflight + install** -- runs `lenny-ctl preflight`, then `helm install` (or `helm upgrade`), then `lenny-ctl bootstrap --from-values`.
5. **Smoke test** -- creates a session against the `chat` reference runtime and confirms the full round trip works.

### Answer files

Any interactive session can be saved to an answer file:

```bash
lenny-ctl install --save-answers ./answers.yaml
```

The saved file is plain YAML. Replay it non-interactively in CI:

```bash
lenny-ctl install --non-interactive --answers ./answers.yaml
```

The chart also ships answer-file bases for common scenarios -- `answers/eks-small-team.yaml`, `answers/gke-growth.yaml`, `answers/laptop.yaml`, and others. If you'd rather hand-write values, skip the wizard entirely and run `helm install -f answers/<base>.yaml -f values-<size>.yaml -f overrides.yaml`. Both paths are supported equally.

### Upgrades

Upgrades replay the answer file against the existing release:

```bash
lenny-ctl upgrade --answers ./answers.yaml
```

That runs preflight, diffs the composed values against the live release, and invokes `helm upgrade` once you approve.

### Airgapped clusters

`lenny-ctl install --offline` skips the cluster-reachability probes during detection. Preflight still runs against the target cluster; only detection is affected.

---

## Cluster Prerequisites

### Required components

| Component | Version | What Lenny uses it for |
|---|---|---|
| Kubernetes | 1.28+ | The platform itself. Lenny relies on `RuntimeClass`, `NetworkPolicy`, and Server-Side Apply being available. |
| Helm | 3.12+ | Installing and upgrading the chart. |
| cert-manager | 1.12+ | Issues and rotates the internal certificates the gateway and its controllers use to talk to each other. |
| Container runtime | containerd 1.7+ | Starts the session pods. Must be able to select different runtimes per pod (`RuntimeClass`). |
| CNI plugin | Calico, Cilium, or a cloud-native CNI with Calico in policy-only mode | Enforces the default-deny network policies that keep pods isolated. On EKS, AKS, and GKE, the usual pattern is the cloud provider's own CNI plus Calico in policy-only mode. |
| PostgreSQL | 14+ | Stores session state, audit logs, billing events, credential pools, and tokens. |
| Redis | 7.0+ | Short-lived coordination state -- leases, quota counters, routing cache, and pub/sub between gateway replicas. Requires TLS and a password. |
| Object storage | MinIO, S3, GCS, or Azure Blob | Holds workspace snapshots, checkpoints, and anything uploaded from a session. |

### Optional components

| Component | Why you'd add it | When it's needed |
|---|---|---|
| gVisor (`runsc`) | Runs each pod inside a user-space kernel that intercepts system calls, so a compromised agent can't reach the host kernel. This is the default isolation profile. | Recommended for all production workloads. |
| Kata Containers | Runs each pod in a full lightweight VM, for the strongest isolation Lenny supports. | High-risk workloads, partially trusted code, or multi-tenant clusters where pods may be reused across tenants. |
| OPA Gatekeeper or Kyverno | Admission policies that keep untrusted pods on the right sandboxing profile and prevent accidental privilege escalation. | Required for production. |
| External Secrets Operator | Syncs LLM keys and connector secrets from an external vault -- AWS Secrets Manager, HashiCorp Vault, GCP Secret Manager. | Large deployments with hundreds of credentials per pool, or when your secrets already live in an external vault. |
| KEDA | Scales deployments off a direct Prometheus query, bypassing the Prometheus Adapter cache for faster reactions to load. | Optional alternative to the Prometheus Adapter. |
| Prometheus + Grafana | Metrics collection and dashboards. | Required for observability (see [Observability](observability.html)). |
| External LLM routing proxy | Sends the gateway's outbound LLM traffic through a shared gateway like LiteLLM or Portkey -- useful for a broader provider catalog, custom routing rules, or shared spend reporting. | Optional. The gateway already talks directly to Anthropic, AWS Bedrock, Google Vertex AI, and Azure OpenAI; you only need this if you want a different provider or centralised cost tracking. See [external LLM proxy](external-llm-proxy.md). |
| krew | `kubectl` plugin manager, used to install `kubectl-lenny`. | Optional convenience for operators who'd rather run `kubectl lenny ...` than the standalone `lenny-ctl` binary. See [krew installation](krew-install.md). |

### Infrastructure sizing quick reference

| Deployment size | Concurrent sessions | Gateway replicas | Postgres | Redis | MinIO |
|---|---|---|---|---|---|
| Starter | 100 | 2-3 | 2 vCPU / 4 GB | 2 GB | Single node |
| Growth | 1,000 | 5-10 | 4 vCPU / 16 GB | 8 GB | 4-node erasure coded |
| Scale | 10,000 | 25-50 | 8 vCPU / 32 GB | 16 GB | 8-node erasure coded |

See [Scaling](scaling.html) for detailed sizing guidance.

---

## Helm Chart Installation

### Step 1: Install CRDs

Helm does not update CRDs on `helm upgrade`. CRDs must be applied as a separate step before every install or upgrade.

```bash
# Download CRDs from the release
kubectl apply -f https://github.com/lennylabs/lenny/releases/latest/download/crds.yaml

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
  maxSessionsPerReplica: 50  # Starter-size default; calibrate via a ramp test

# Bootstrap seed -- creates Day-1 resources
bootstrap:
  enabled: true
  tenant:
    name: "default"
    displayName: "Default Tenant"
  runtimes:
    - name: echo
      type: agent
      image: "ghcr.io/lennylabs/echo-runtime:latest"
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
| Admission webhook inventory | Verifies that the expected set of `ValidatingWebhookConfiguration` resources and CRD conversion webhooks are installed. The expected set is composed at render time from the three feature flags `features.llmProxy`, `features.drainReadiness`, `features.compliance` (see [Feature-gated chart inventory](#feature-gated-webhooks)); each present webhook must have `failurePolicy: Fail` and a non-empty `caBundle`. |
| Drain-readiness webhook | When `features.drainReadiness: true`: verifies `lenny-drain-readiness` webhook exists and has a non-empty `caBundle`. |
| T4 node isolation webhook | When `features.compliance: true`: verifies `lenny-t4-node-isolation` webhook exists and has a non-empty `caBundle`. |
| Playground apiKey mode warning | Non-blocking `WARNING` when `playground.enabled: true`, `playground.authMode: apiKey`, `global.devMode: false`, and `playground.acknowledgeApiKeyMode: false` — operator must explicitly acknowledge the bearer-paste phishing surface tradeoff. |

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

### Feature-gated webhooks

Lenny's validating/conversion admission webhooks are grouped into a baseline set that is always rendered, plus three optional webhooks gated by Helm `features.*` flags. The preflight Job computes its expected webhook set from the same flags, so a slice install (e.g., Phase 3.5 with all flags `false`) passes cleanly without requiring webhooks that have not yet been rolled out.

| Helm value | Default | Gated webhooks |
|:-----------|:--------|:---------------|
| `features.llmProxy` | `false` | `lenny-direct-mode-isolation` |
| `features.drainReadiness` | `false` | `lenny-drain-readiness` |
| `features.compliance` | `false` | `lenny-data-residency-validator`, `lenny-t4-node-isolation` |

The baseline set (always expected) is `lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, and the CRD conversion webhook. Flipping a feature flag from `true` to `false` after the corresponding phase has been reached is an unsupported downgrade — the per-webhook unavailability alerts and runtime enforcement paths depend on the webhook's continued presence.

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

### What a healthy deployment looks like

- All gateway replicas are Running and Ready.
- The management plane (`lenny-ops`) has one or two replicas Running, one of them holding the leader lease.
- The token service has two or more replicas Running.
- The controllers responsible for warming pools and scaling them have at least two replicas each, one leader per controller.
- The warm pool pods reach the `idle` state within their expected startup time.
- No drift, warm-pool-bootstrapping, or management-plane self-health alerts are firing.
- `lenny_warmpool_idle_pods` reports the warm pod count you configured, per pool.

### Getting a one-shot health report with `lenny-ctl doctor`

For a full health check in one command:

```bash
lenny-ctl doctor
```

`doctor` probes every subsystem through the management plane's diagnostic endpoints and prints a structured report of what's healthy, what's degraded, and what's broken. It doesn't change anything.

A lot of what `doctor` finds is fixable automatically -- a missing resource quota, stale pod-security labels on a namespace, a service monitor missing a required label, a missing tenant-guard trigger on an external Postgres pooler. To have `doctor` apply those fixes, run:

```bash
lenny-ctl doctor --fix
```

Each fix is idempotent, guarded so it can't make things worse, and recorded in the audit log with your identity and the before/after state. `--dry-run` previews the fixes without applying them.

---

## Cloud-Managed Services Integration

For production deployments, cloud-managed services are recommended over self-managed infrastructure. See the answer-file backends guidance in the spec (Section 17.9) for full details; `lenny-ctl install` detects and suggests an appropriate answer-file base when it recognizes the cloud provider.

### AWS

| Service | What Lenny uses it for | Recommended configuration |
|---|---|---|
| Amazon RDS (PostgreSQL) | Session state, tokens, audit log | Multi-AZ, `db.r6g.xlarge` at Growth size, envelope encryption via AWS KMS |
| Amazon ElastiCache (Redis) | Leases, quota counters, routing cache | Cluster mode, TLS in-transit, AUTH token |
| Amazon S3 | Workspace artifacts and checkpoints | Versioning on, lifecycle rules to expire non-current versions |
| AWS KMS | Envelope encryption of cluster secrets and data-encryption keys | Used for etcd secret encryption and for wrapping the gateway's data-encryption keys |
| Amazon RDS Proxy | Connection pooling | Use instead of self-managed PgBouncer; set `postgres.connectionPooler: external` |

### GCP

| Service | What Lenny uses it for | Recommended configuration |
|---|---|---|
| Cloud SQL (PostgreSQL) | Session state, tokens, audit log | HA, `db-custom-4-16384` at Growth size, CMEK via Cloud KMS |
| Memorystore (Redis) | Leases, quota counters, routing cache | Standard tier (HA), TLS, AUTH |
| Google Cloud Storage | Workspace artifacts and checkpoints | Versioning on, lifecycle rules |
| Cloud KMS | Envelope encryption of data-encryption keys | Used for wrapping the gateway's data-encryption keys |
| Cloud SQL Auth Proxy | Connection pooling | Use instead of PgBouncer; set `postgres.connectionPooler: external` |

### Azure

| Service | What Lenny uses it for | Recommended configuration |
|---|---|---|
| Azure Database for PostgreSQL | Session state, tokens, audit log | Flexible server with HA, `Standard_D4s_v3` at Growth size |
| Azure Cache for Redis | Leases, quota counters, routing cache | Premium tier (HA), TLS, access keys |
| Azure Blob Storage | Workspace artifacts and checkpoints | Versioning on, lifecycle management rules |
| Azure Key Vault | Envelope encryption of cluster secrets and data-encryption keys | Used for etcd secret encryption and for wrapping the gateway's data-encryption keys |

### When using a cloud-managed database proxy

Cloud-managed connection proxies (RDS Proxy, Cloud SQL Auth Proxy, Azure PgBouncer) don't let Lenny inject its own `connect_query` to guard against stale tenant context on reused connections. If you're using one of these, set:

```yaml
postgres:
  connectionPooler: external
```

That tells the schema migration to install a per-transaction trigger (`lenny_tenant_guard`) inside Postgres, which enforces the same tenant-scoping invariant from the database side. The gateway refuses to start if `connectionPooler: external` is set but the trigger is missing, so you'll notice the misconfiguration immediately.
