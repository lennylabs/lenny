---
layout: default
title: Configuration
parent: "Operator Guide"
nav_order: 2
---

# Configuration

This page provides a deep dive into the `values.yaml` configuration surface, covering every major section: gateway tuning, runtime registration, pool configuration, credential pools, delegation policies, and per-tier operational defaults.

---

## values.yaml Structure

The Helm chart organizes configuration into the following top-level sections:

| Section | Purpose |
|---|---|
| `global` | Dev mode flag, image registry, trace sampling rate |
| `gateway` | Replica count, session capacity, HPA targets, subsystem concurrency |
| `postgres` | Connection string, pooler mode, write ceiling |
| `redis` | Connection string, TLS, maxmemory |
| `minio` | Endpoint, bucket, credentials, encryption |
| `pools` | Warm pool definitions (runtime, isolation, resources, warm count) |
| `runtimes` | Runtime registration (standalone and derived) |
| `credentialPools` | LLM provider credential pools |
| `bootstrap` | Day-1 seed configuration (tenant, runtimes, pools) |
| `preflight` | Preflight check toggles and timeout |
| `agentNamespaces` | Namespace list, ResourceQuota, LimitRange overrides |
| `admissionController` | Replica count, PDB settings for admission webhooks |
| `capacityPlanning` | Workload profile parameters for formula-driven sizing |
| `slo` | SLO targets and burn-rate multipliers |
| `observability` | OTLP endpoint, trace sampling, Prometheus config |
| `audit` | Retention preset, SIEM endpoint, batching config |
| `billing` | Retention days, dual-control threshold, flush intervals |

---

## Gateway Configuration

### Core Settings

```yaml
gateway:
  replicas: 2                    # Starting replica count (HPA adjusts dynamically)
  maxSessionsPerReplica: 50      # Capacity ceiling per replica (must be calibrated)
  
  grpcPort: 50051                # Pod-to-gateway gRPC control channel
  llmProxyPort: 8443             # LLM proxy port (proxy-mode pools only)
  
  periodicCheckpointIntervalSeconds: 600   # Checkpoint interval (default: 10 min)
  periodicCheckpointJitterFraction: 0.2    # Jitter to prevent checkpoint storms
```

### Per-Tier `maxSessionsPerReplica`

These values are **provisional first-principles estimates** and must be replaced with empirically calibrated measurements from the Phase 2 benchmark harness.

| Tier | Provisional Value | HPA Target Utilization | Notes |
|---|---|---|---|
| Tier 1 (Starter) | 50 | 80% | Derived from 100 max sessions / 2 replicas |
| Tier 2 (Growth) | 200 | 80% | Derived from 1,000 max sessions / 5 replicas |
| Tier 3 (Scale) | 400 | 80% | Requires LLM Proxy subsystem extraction |
| Tier 4 (Platform) | 400 | 80% | Same per-replica; scale-out via replica count |

**Calibration methodology:** Use the ramp test described in [Scaling](scaling.html) to determine the empirical saturation point and set `maxSessionsPerReplica` to that value minus 20% headroom.

### Subsystem Concurrency Limits

Each gateway subsystem has independently configured concurrency limits:

```yaml
gateway:
  subsystems:
    streamProxy:
      maxConcurrent: 500
    uploadHandler:
      maxConcurrent: 100
    mcpFabric:
      maxConcurrent: 200
    llmProxy:
      maxConcurrent: 1000
  
  extractionThresholds:
    streamProxy:
      queueDepth: 500
      p99AttachLatencyMs: 800
    uploadHandler:
      activeConcurrent: 200
    mcpFabric:
      activeDelegations: 1000
    llmProxy:
      activeConnections: 2000
```

The extraction thresholds determine when a subsystem should be extracted to its own service. These are also provisional and must be calibrated via the Phase 2 benchmark harness.

### HPA Configuration

```yaml
gateway:
  hpa:
    enabled: true
    minReplicas: 2
    maxReplicas: 10
    metrics:
      - type: Pods
        pods:
          metric:
            name: lenny_gateway_request_queue_depth
          target:
            type: AverageValue
            averageValue: 10       # Primary scale-out trigger
      - type: Pods
        pods:
          metric:
            name: lenny_gateway_active_streams
          target:
            type: AverageValue
            averageValue: 200      # Secondary metric
      - type: Resource
        resource:
          name: cpu
          target:
            type: Utilization
            averageUtilization: 70
    behavior:
      scaleDown:
        stabilizationWindowSeconds: 300
        policies:
          - type: Percent
            value: 10
            periodSeconds: 60
```

**HPA metric roles:**

| Metric | Role | Used By |
|---|---|---|
| `lenny_gateway_request_queue_depth` | **Primary HPA scale-out trigger** | HPA / KEDA ScaledObject |
| `lenny_gateway_active_streams` | **Secondary HPA metric** | HPA / KEDA ScaledObject |
| `lenny_gateway_active_sessions / maxSessionsPerReplica` | **Capacity ceiling alert only** -- not an HPA trigger | Alert only |

---

## Runtime Registration

Runtimes are registered via the admin API or bootstrap seed and define the agent binary, capabilities, and operational constraints.

### Standalone Runtime

A standalone runtime specifies its own container image:

```yaml
runtimes:
  - name: claude-worker
    image: registry.example.com/claude-runtime:v1.2
    type: agent                          # agent | mcp
    capabilities:
      interaction: multi_turn            # one_shot | multi_turn
      injection:
        supported: true
        modes: [immediate, queued]
      preConnect: false                  # SDK-warm mode (default: false)
    executionMode: session               # session | task | concurrent
    isolationProfile: sandboxed          # standard | sandboxed | microvm
    allowedResourceClasses: [small, medium, large]
    supportedProviders:
      - anthropic_direct
      - aws_bedrock
    credentialCapabilities:
      hotRotation: true
    limits:
      maxSessionAge: 7200
      maxUploadSize: 500MB
      maxRequestInputWaitSeconds: 600
    setupCommandPolicy:
      mode: allowlist
      shell: false
      allowlist:
        - npm ci
        - pip install
      maxCommands: 10
    setupPolicy:
      timeoutSeconds: 300
      onTimeout: fail                    # fail | warn
    labels:
      team: platform
      approved: "true"
```

### Derived Runtime

A derived runtime inherits from a base and customizes workspace, setup, and policy:

```yaml
runtimes:
  - name: research-pipeline
    baseRuntime: claude-worker
    workspaceDefaults:
      files:
        - path: agent.py
          content: "..."
      setupCommands:
        - pip install -r requirements.txt
    setupPolicy:
      timeoutSeconds: 300
      onTimeout: fail
    agentInterface:
      description: "Analyzes codebases and produces refactoring plans"
      supportsWorkspaceFiles: true
    delegationPolicyRef: research-policy
    labels:
      team: research
```

### Inheritance Rules

Fields that **cannot** be overridden on a derived runtime:

- `type`, `executionMode`, `isolationProfile`
- `capabilities.interaction`, `capabilities.injection`
- `allowedResourceClasses`

Fields that **can** be independently configured:

- `workspaceDefaults` (appended to base)
- `setupCommands` (appended after base commands)
- `setupPolicy.timeoutSeconds` (gateway uses the maximum of base and derived)
- `agentInterface`, `delegationPolicyRef`, `labels`, `taskPolicy`

### Runtime Types

| Type | Purpose | Capabilities |
|---|---|---|
| `agent` | Participates in Lenny's task lifecycle (sessions, workspace, delegation) | Full interaction, injection, delegation |
| `mcp` | Hosts an MCP server; Lenny manages pod lifecycle only | No task lifecycle, no capabilities field |

### Minimal Configuration

The absolute minimum to register a runtime and start handling sessions:

```yaml
runtimes:
  - name: my-agent
    image: registry.example.com/my-agent:latest
    type: agent
    supportedProviders:
      - anthropic_direct
    labels:
      team: default
credentialPools:
  - name: default
    provider: anthropic_direct
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key
```

---

## Pool Configuration

Each pool is a warmable deployment target for one runtime combined with an operational profile.

### Pool Dimensions

```yaml
pools:
  - name: claude-worker-sandboxed-small
    runtime: claude-worker
    isolationProfile: sandboxed          # standard | sandboxed | microvm
    resourceClass: small                 # small | medium | large
    executionMode: session               # session | task | concurrent
    minWarm: 5                           # Minimum warm pods
    maxWarm: 20                          # Maximum warm pods
    egressProfile: restricted            # restricted | internet
    checkpointPolicy:
      enabled: true
      intervalSeconds: 600
    workspaceSizeLimitBytes: 536870912   # 512 MiB
    terminationGracePeriodSeconds: 240
```

### Execution Modes

**`session`** -- One session per pod. Pod is exclusive to the session for its lifetime. Default mode.

**`task`** -- Pod reuses across sequential tasks with workspace scrub between tasks:

```yaml
pools:
  - name: task-pool
    runtime: claude-worker
    executionMode: task
    taskPolicy:
      acknowledgeBestEffortScrub: true   # Required acknowledgment
      allowCrossTenantReuse: false
      cleanupCommands:
        - rm -rf /tmp/sandbox-*
      cleanupTimeoutSeconds: 30
      onCleanupFailure: warn             # warn | fail
      maxScrubFailures: 3
      maxTasksPerPod: 50                 # Required -- no default
      maxPodUptimeSeconds: 86400
```

**`concurrent`** -- Multiple tasks on a single pod simultaneously:

```yaml
pools:
  - name: concurrent-pool
    runtime: claude-worker
    executionMode: concurrent
    concurrencyStyle: workspace          # stateless | workspace
    concurrentWorkspacePolicy:
      acknowledgeProcessLevelIsolation: true  # Required acknowledgment
      maxConcurrent: 8
      cleanupTimeoutSeconds: 60
```

### Pool Taxonomy Strategy

Not every runtime x isolation x resource combination needs a warm pool. Use a tiered approach:

| Pool Type | `minWarm` | Use Case |
|---|---|---|
| Hot pools | > 0 | High-traffic combinations needing instant availability |
| Cold pools | 0 (maxWarm > 0) | Valid combinations created on demand with cold-start latency |
| Disallowed | N/A | Invalid or insecure combinations rejected at validation |

### Resource Classes

| Class | CPU Request | Memory Request | CPU Limit | Memory Limit |
|---|---|---|---|---|
| small | 250m | 256Mi | 1 | 1Gi |
| medium | 500m | 512Mi | 2 | 2Gi |
| large | 1 | 1Gi | 4 | 4Gi |

Resource class values are configurable via `.Values.resourceClasses`.

---

## Credential Pools

Credential pools manage LLM provider API keys and distribute them across sessions.

```yaml
credentialPools:
  - name: anthropic-production
    provider: anthropic_direct
    deliveryMode: proxy                  # proxy | direct
    proxyDialect: anthropic              # openai | anthropic (required when deliveryMode: proxy)
    maxLeases: 50                        # Max concurrent leases per credential
    leaseTTLSeconds: 3600
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key-1
      - id: key-2
        secretRef: lenny-system/anthropic-key-2
    fallbackChain:
      - provider: aws_bedrock
        poolRef: bedrock-fallback
```

**`proxyDialect`** is required when `deliveryMode: proxy`. It must be one of the dialects declared in the bound Runtime's `credentialCapabilities.proxyDialect` list. Admission rejects mismatches with `422 INVALID_POOL_PROXY_DIALECT`. The LiteLLM sidecar (see [LiteLLM sidecar](litellm-sidecar.md)) handles upstream provider translation -- the agent pod speaks the declared dialect regardless of the actual upstream provider.

### Delivery Modes

| Mode | Security | How It Works |
|---|---|---|
| `proxy` | **Recommended** -- key never enters pod | Pod sends requests to gateway LLM Proxy; gateway injects real API key |
| `direct` | Key delivered to pod in `/run/lenny/credentials.json` | Pod contacts LLM provider directly; suitable for single-tenant/dev only |

### Credential Health and Rotation

The credential pool system tracks per-credential health scores and handles automatic rotation:

- **Rate-limited credentials** are placed in cooldown
- **Hot rotation** is supported when the runtime declares `credentialCapabilities.hotRotation: true`
- **Emergency revocation** terminates all active leases immediately

---

## Delegation Policies

Delegation policies control which runtimes can delegate to other runtimes:

```yaml
delegationPolicies:
  - name: orchestrator-policy
    rules:
      - source: claude-orchestrator
        targets:
          - claude-worker
          - research-pipeline
        maxDepth: 3
        maxFanOut: 10
        maxTreeSize: 50
        maxTokenBudget: 100000
    contentPolicy:
      interceptorRef: content-filter
    isolationMonotonicity: enforce       # enforce | warn
```

### Isolation Monotonicity

The delegation policy can enforce that child sessions run at the same or higher isolation level:

| Parent Isolation | Allowed Child Isolation |
|---|---|
| microvm | microvm only |
| sandboxed | sandboxed, microvm |
| standard | standard, sandboxed, microvm |

---

## Capacity Planning Parameters

Workload profile assumptions are exposed as Helm values under `capacityPlanning.*` so operators with atypical workloads can substitute measurements:

```yaml
capacityPlanning:
  avgSessionDurationSeconds: 333
  delegationParticipationRate: 0.05
  avgDelegationsPerDelegatingSession: 10
  avgChildSessionSeconds: 60
  avgWorkspaceSizeMB: 100
  sessionIdleFraction: 0.30
```

**These must be updated before relying on any formula-derived sizing output.** The PoolScalingController logs a warning if defaults are in use for Tier 2 or Tier 3 deployments.

---

## Infrastructure Configuration

### Postgres

```yaml
postgres:
  connectionString: "postgres://lenny:password@pgbouncer:5432/lenny"
  connectionPooler: pgbouncer           # pgbouncer | external
  writeCeilingIops: 600                 # Per-tier write ceiling for alerts
  readReplicaConnectionString: ""       # Optional read replica endpoint
```

When using cloud-managed poolers (RDS Proxy, Cloud SQL Auth Proxy), set `connectionPooler: external` to trigger the `lenny_tenant_guard` trigger migration.

### Redis

```yaml
redis:
  connectionString: "rediss://:password@redis:6380"
  # TLS + AUTH required in all environments
```

### MinIO / Object Storage

```yaml
minio:
  endpoint: "https://minio.example.com"
  bucket: "lenny"
  accessKey: "minioadmin"
  secretKey: "minioadmin"
  encryption: true                      # Server-side encryption required
```

---

## Agent Namespace Configuration

```yaml
agentNamespaces:
  - name: lenny-agents
    resourceQuota:
      pods: 200
      requests.cpu: "400"
      requests.memory: "800Gi"
    limitRange:
      defaultRequest:
        cpu: "250m"
        memory: "256Mi"
      default:
        cpu: "2"
        memory: "2Gi"
  - name: lenny-agents-kata
    resourceQuota:
      pods: 50
      requests.cpu: "100"
      requests.memory: "200Gi"
```

---

## Per-Tier Configuration Profiles

The following table summarizes key configuration differences across deployment tiers:

| Setting | Tier 1 | Tier 2 | Tier 3 |
|---|---|---|---|
| `gateway.replicas` (min / max) | 2 / 4 | 3 / 10 | 5 / 30 |
| `gateway.maxSessionsPerReplica` | 50 | 200 | 400 |
| `gateway.hpa.queueDepthTarget` | 15 | 10 | 5 |
| `postgres.writeCeilingIops` | 200 | 600 | 1600 |
| `billing.flushIntervalMs` | 500 | 500 | 500 |
| `billing.redisStreamMaxLen` | 50000 | 50000 | 72000 |
| `quotaSyncIntervalSeconds` | 30 | 30 | 10-30 |
| `agentNamespaces[].resourceQuota.pods` | 50 | 200 | 1000 |

---

## Connector Registration

Connectors are registered via `lenny-ctl admin connectors create` or the admin API before they can be used -- unregistered external MCP servers cannot be called from inside a pod.

Each connector requires:

```yaml
connectors:
  - id: github
    displayName: GitHub
    mcpServerUrl: https://mcp.github.com
    transport: streamable_http
    auth:
      type: oauth2
      authorizationEndpoint: https://github.com/login/oauth/authorize
      tokenEndpoint: https://github.com/login/oauth/access_token
      clientId: "..."
      clientSecretRef: lenny-system/github-client-secret
      scopes: [repo, read:org]
    visibility: tenant                   # tenant | global
    labels:
      team: platform
```

| Field | Description |
|---|---|
| `mcpServerUrl` | The external MCP server endpoint |
| `transport` | Transport protocol (`streamable_http`, `stdio`) |
| `auth` | OAuth2 configuration with `clientId`, `clientSecretRef`, and `scopes` |
| `visibility` | `tenant` (visible to owning tenant) or `global` (visible to all tenants) |

**OAuth security:** Connector OAuth `state` parameters use 128-bit cryptographic entropy (base64url-encoded), stored in Redis with a 10-minute TTL bound to the initiating session and connector ID. PKCE (S256) is enforced for public clients.

---

## Experiment Configuration

Experiments are managed via the admin API or `lenny-ctl admin experiments`. They enable A/B testing of runtime versions, configurations, or delegation policies.

### Experiment Lifecycle

```
active → paused → concluded
```

- **`active`:** Experiment is running; sessions are assigned to variants based on weights
- **`paused`:** Variant pools scale to zero warm pods; new sessions use the control group
- **`concluded`:** Variant pools are torn down; experiment enters terminal state

### Pool Sizing

Variant pools are automatically sized by the PoolScalingController based on `variant_weight`. When experiments create variant pools on a base pool, the base pool's `minWarm` is recomputed to reflect the traffic fraction diverted to variants.

### External Targeting

External experiment assignment integrates with any flag service via the [OpenFeature Go SDK](https://openfeature.dev/):

- **OFREP (Remote Evaluation Protocol)** -- recommended. Vendor-neutral REST evaluation API. Flagd, GO Feature Flag, ConfigCat, and LaunchDarkly (via Relay Proxy) all expose OFREP.
- **LaunchDarkly, Statsig, Unleash** -- via built-in OpenFeature SDK providers linked into the gateway.

Configure per-tenant via `experimentTargeting`:

```yaml
experimentTargeting:
  provider: ofrep                 # ofrep | launchdarkly | statsig | unleash
  timeoutMs: 200
  ofrep:
    endpoint: https://flags.internal/ofrep
    headers:
      Authorization: "Bearer ${OFREP_TOKEN}"
```

See [OpenFeature integration](openfeature-integration.md) for provider-specific configuration (LaunchDarkly, Statsig, Unleash), failure handling, circuit-breaker behavior, and troubleshooting. Percentage-mode bucketing is built in and requires no external configuration.

---

## Memory Store Configuration

The `MemoryStore` manages user-scoped persistent memories across sessions.

```yaml
memory:
  maxMemoriesPerUser: 10000            # Default: 10,000
  retentionDays:                       # Optional; unset = no TTL-based expiry
```

- **`memory.maxMemoriesPerUser`** (default: 10,000): When a write would push a user's memory count above this limit, the oldest memories are evicted.
- **`memory.retentionDays`** (optional): When set, the GC sweep deletes memory rows whose `created_at` exceeds the configured TTL.
- The `MemoryStoreGrowthHigh` alert fires when any user's memory count exceeds 80% of `memory.maxMemoriesPerUser`.

---

## Metering and Billing

Lenny provides a structured billing event stream for integration with external billing systems.

### Event Stream Properties

- **Append-only semantics:** Billing events are immutable once written
- **Sequence number monotonicity:** Per-tenant monotonic sequence numbers enable gap detection by downstream consumers
- **Redis stream backed:** Events are first written to a per-tenant Redis stream (`t:{tenant_id}:billing:stream`) with configurable max length (`billing.redisStreamMaxLen`), then flushed to Postgres

### Configuration

```yaml
billing:
  retentionDays: 395                   # Default: 395 days (~13 months)
  redisStreamMaxLen: 50000             # Per-tier: 50,000 (T1/T2), 72,000 (T3)
  dualControlThreshold: 0             # Default: 0 (all corrections require dual-control)
  flushIntervalMs: 500
```

### Retention

| Profile | Retention |
|---|---|
| Default | 395 days (~13 months) |
| SOC2 / FedRAMP | 365 days (floor) |
| HIPAA | 2,190 days (6 years) |

### Billing Corrections

Billing corrections require **dual-control approval** (four-eyes principle). Corrections whose absolute adjustment value exceeds `billing.dualControlThreshold` (default: `0`, meaning all corrections) must be approved by a second `platform-admin` before they are committed to the immutable billing stream.
