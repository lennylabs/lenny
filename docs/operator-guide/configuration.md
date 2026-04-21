---
layout: default
title: Configuration
parent: "Operator Guide"
nav_order: 2
---

# Configuration

This page covers the `values.yaml` configuration surface: gateway tuning, runtime registration, pool configuration, credential pools, delegation policies, and operational defaults that vary by deployment size.

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

### `maxSessionsPerReplica` by Deployment Size

These values are **provisional first-principles estimates** and must be replaced with empirically calibrated measurements from the first-working-slice benchmark harness.

| Size | Provisional Value | HPA Target Utilization | Notes |
|---|---|---|---|
| Starter | 50 | 80% | Derived from 100 max sessions / 2 replicas |
| Growth | 200 | 80% | Derived from 1,000 max sessions / 5 replicas |
| Scale | 400 | 80% | Requires the gateway's LLM routing subsystem to have been extracted |
| Platform | 400 | 80% | Same per-replica; scale-out via replica count |

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

The extraction thresholds determine when a subsystem should be extracted to its own service. These are also provisional and must be calibrated via the first-working-slice benchmark harness.

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

**`proxyDialect`** is required when `deliveryMode: proxy`. It must be one of the dialects declared in the bound Runtime's `credentialCapabilities.proxyDialect` list. Admission rejects mismatches with `422 INVALID_POOL_PROXY_DIALECT`. The gateway handles upstream provider translation inside its own process — the agent pod speaks the declared dialect regardless of which upstream provider the gateway ultimately calls. For providers outside the gateway's built-in set, or for deployers who want to route through a shared team gateway, see [external LLM proxy](external-llm-proxy.md).

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

**These must be updated before relying on any formula-derived sizing output.** The PoolScalingController logs a warning if defaults are in use for Growth or Scale-size deployments.

---

## Infrastructure Configuration

### Postgres

```yaml
postgres:
  connectionString: "postgres://lenny:password@pgbouncer:5432/lenny"
  connectionPooler: pgbouncer           # pgbouncer | external
  writeCeilingIops: 600                 # Write ceiling for alerts; set according to your deployment size
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

### T4 KMS probe

```yaml
storage:
  t4KmsProbeInterval: 300s              # Default: 300s. Minimum: 60s.
```

A leader-elected gateway goroutine continuously probes the T4 envelope-encryption KMS key at `storage.t4KmsProbeInterval` and publishes `lenny_t4_kms_probe_last_success_timestamp` / `lenny_t4_kms_probe_result_total{outcome}`. The `T4KmsKeyUnusable` Critical alert fires when `time() - last_success > 2 * t4KmsProbeInterval`, and T4 writes fail closed until the KMS key recovers. Values below the 60s minimum are rejected at startup.

### Quota fail-open controls

```yaml
quotaUserFailOpenFraction: 0.25         # Default: 0.25. Range: (0, 1).
```

When Redis is unreachable and quota fail-open is active, each user may consume at most `quotaUserFailOpenFraction` of the tenant's per-second ceiling before being locally throttled. The gateway logs a startup warning and raises the `QuotaFailOpenUserFractionInoperative` alert when `quotaUserFailOpenFraction >= 0.5`, because a value that high lets a single runaway user exhaust the tenant ceiling during the outage window.

### Admission-plane feature flags

```yaml
features:
  llmProxy: false                         # Gates lenny-direct-mode-isolation admission webhook (Phase 5.8)
  drainReadiness: false                   # Gates lenny-drain-readiness admission webhook (Phase 8)
  compliance: false                       # Gates lenny-data-residency-validator and lenny-t4-node-isolation webhooks (Phase 13)
```

Each flag gates the rendering of one or more `ValidatingWebhookConfiguration` resources that enforce admission-time policy. Flipping a flag from `false` to `true` is routine (it progresses the deployment into a later phase); flipping a flag from `true` to `false` is prohibited by default because it would silently remove a fail-closed admission surface the cluster was already enforcing.

**Downgrade enforcement.** The chart renders a namespace-scoped, append-only `lenny-deployment-phase-stamp` ConfigMap that records every flag that has been set to `true`, along with an RFC3339 `enabledAt` timestamp. On every `helm install` and `helm upgrade`, a render-time guard reads the phase-stamp via the Helm `lookup` primitive and fails closed with `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` if any flag recorded as enabled in the phase-stamp is being rendered as `false` without an explicit acknowledgement. **GitOps note:** Helm's `lookup` function returns an empty map under `helm template` (including `helm template --dry-run`) because those commands do not connect to a live cluster, so the render-time guard is effective only during `helm install` / `helm upgrade`. In a GitOps deployment topology (ArgoCD or Flux Helm Controller that renders manifests via `helm template` and applies them Server-Side), the effective pre-Sync fail-closed gate is the `lenny-preflight` Job's `Phase-stamp consistency` check (`PREFLIGHT_PHASE_STAMP_MISMATCH`, SPEC §17.9 row "Phase-stamp consistency"). GitOps operators SHOULD run `lenny-ctl preflight --config <values.yaml>` as a pre-Sync hook (or as a PresyncHook in ArgoCD / `helm.toolkit.fluxcd.io/preflight` workflow) so a downgrade attempt aborts before Sync touches the cluster. Regardless of code path, the `lenny-preflight` Job runs the same check against the live cluster at install time with `PREFLIGHT_PHASE_STAMP_MISMATCH` and the `AdmissionPlaneFeatureFlagDowngrade` Warning alert fires at runtime if the phase-stamp and the installed webhook set diverge after install. Operators with cluster-admin can reset the phase-stamp via `kubectl delete configmap lenny-deployment-phase-stamp -n <release-namespace>` and re-install the chart to establish a new baseline — this is the sole supported reset path.

**kube-state-metrics configuration for `AdmissionPlaneFeatureFlagDowngrade`.** The runtime alert evaluates its left-hand vector against `kube_configmap_labels{configmap="lenny-deployment-phase-stamp"}` rather than `kube_configmap_info`, because `kube_configmap_info` exposes no per-flag label encoding. The chart labels the phase-stamp ConfigMap with `lenny.dev/flag-<flag-slug>-enabled: "true"` for every currently-enabled feature flag (slugs: `llm-proxy`, `drain-readiness`, `compliance`). For these labels to surface as Prometheus metric labels, operators MUST configure `kube-state-metrics` with an opt-in allowlist that includes the `lenny.dev/flag-*` namespace. The standard Helm values for the `kube-state-metrics` chart expose this as `--metric-labels-allowlist=configmaps=[lenny.dev/flag-*]` (or the chart-equivalent `metricLabelsAllowlist: [configmaps=[lenny.dev/flag-*]]`). Without this allowlist, the `kube_configmap_labels{configmap="lenny-deployment-phase-stamp"}` series has no per-flag labels and the `AdmissionPlaneFeatureFlagDowngrade` expression evaluates to empty — the alert becomes inoperative (it does not fire falsely, but it also does not catch real drift). Preflight row 514 `Phase-stamp consistency` does not validate the `kube-state-metrics` configuration (it lives outside the Lenny chart), so the allowlist must be configured in the operator's `kube-state-metrics` deployment before Lenny is installed; an easy way to verify is `kubectl get --raw='/api/v1/namespaces/<release-ns>/services/kube-state-metrics:http-metrics/proxy/metrics' | grep 'kube_configmap_labels{.*configmap="lenny-deployment-phase-stamp"'` and confirm that `label_lenny_dev_flag_*_enabled="true"` labels are present for every enabled flag after a `helm install`.

**Acknowledged downgrade override.**

```yaml
acceptFeatureFlagDowngrade:
  llmProxy: false                         # Set to true to acknowledge disabling features.llmProxy after it was enabled
  drainReadiness: false                   # Set to true to acknowledge disabling features.drainReadiness after it was enabled
  compliance: false                       # Set to true to acknowledge disabling features.compliance after it was enabled
```

Setting `acceptFeatureFlagDowngrade.<flag>: true` is the sole authorised path to `helm upgrade` a flag from `true` to `false`. The override emits a `deployment.feature_flag_downgrade_acknowledged` audit event (catalogued in SPEC §16.7 with payload fields `flag_name`, `expected_webhook_name`, `acknowledged_by_sub`, `acknowledged_by_tenant_id`, `justification`, `acknowledged_at`) and retains the phase-stamp entry (so the `AdmissionPlaneFeatureFlagDowngrade` alert continues to fire until the flag is re-enabled or the ConfigMap is reset). The override MUST be set per-flag; a blanket acknowledgement is not supported, and the chart render fails with `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE_JUSTIFICATION_REQUIRED` if `--set acceptFeatureFlagDowngrade.<flag>.justification=<text>` is absent or empty. Typical invocation:

```
helm upgrade lenny ./chart \
  --set features.compliance=false \
  --set acceptFeatureFlagDowngrade.compliance=true \
  --set acceptFeatureFlagDowngrade.compliance.justification="compliance-profile decommission per change request CR-1234"
```

After the upgrade, the phase-stamp still records `features.compliance.enabled=true`, the `lenny-data-residency-validator` and `lenny-t4-node-isolation` webhooks (the two that `features.compliance` gates per SPEC §17.2 Feature-gated chart inventory) are no longer rendered, and the `AdmissionPlaneFeatureFlagDowngrade` alert fires Warning. Operators MUST either re-enable `features.compliance=true` or reset the phase-stamp ConfigMap (as described above) to clear the alert. Note the flag-to-webhook mapping: `features.llmProxy` gates `lenny-direct-mode-isolation`; `features.drainReadiness` gates `lenny-drain-readiness`; `features.compliance` gates `lenny-data-residency-validator` and `lenny-t4-node-isolation` (two webhooks, both removed when the flag flips to `false`).

### Data Residency (multi-region only)

When any tenant has `dataResidencyRegion` set, the platform requires per-region storage, backup, and legal-hold escrow pipelines. Single-region deployments ignore this section and keep the default scalar settings above.

```yaml
storage:
  regions:                              # Required when any tenant has dataResidencyRegion set
    eu-west-1:
      postgresEndpoint: "postgres://lenny:password@pg.eu-west-1.example.com:5432/lenny"
      minioEndpoint:    "https://minio.eu-west-1.example.com"
      redisEndpoint:    "rediss://:password@redis.eu-west-1.example.com:6380"
      kmsEndpoint:      "arn:aws:kms:eu-west-1:<acct>:key/..."
      legalHoldEscrow:
        endpoint:     "https://escrow.minio.eu-west-1.example.com"
        bucket:       "lenny-legal-hold-escrow-eu-west-1"
        kmsKeyId:     "arn:aws:kms:eu-west-1:<acct>:key/..."
        escrowKekId:  "platform:legal_hold_escrow:eu-west-1"
    us-east-1:
      postgresEndpoint: "postgres://lenny:password@pg.us-east-1.example.com:5432/lenny"
      minioEndpoint:    "https://minio.us-east-1.example.com"
      redisEndpoint:    "rediss://:password@redis.us-east-1.example.com:6380"
      kmsEndpoint:      "arn:aws:kms:us-east-1:<acct>:key/..."
      legalHoldEscrow:
        endpoint:     "https://escrow.minio.us-east-1.example.com"
        bucket:       "lenny-legal-hold-escrow-us-east-1"
        kmsKeyId:     "arn:aws:kms:us-east-1:<acct>:key/..."
        escrowKekId:  "platform:legal_hold_escrow:us-east-1"

  # Single-region fallback used when storage.regions is empty. A single-region
  # deployment that never sets dataResidencyRegion on any tenant uses these
  # scalar values; the fail-closed residency gate is a no-op in that case
  # because no residency constraint can be violated.
  legalHoldEscrowDefault:
    endpoint:     "https://escrow.minio.example.com"
    bucket:       "lenny-legal-hold-escrow"
    kmsKeyId:     "arn:aws:kms:us-east-1:<acct>:key/..."
    escrowKekId:  "platform:legal_hold_escrow:default"

backups:
  regions:                              # Required when any tenant has dataResidencyRegion set
    eu-west-1:
      minioEndpoint:          "https://minio.eu-west-1.example.com"
      kmsKeyId:               "arn:aws:kms:eu-west-1:<acct>:key/..."
      accessCredentialSecret: "lenny-backups-eu-west-1"
    us-east-1:
      minioEndpoint:          "https://minio.us-east-1.example.com"
      kmsKeyId:               "arn:aws:kms:us-east-1:<acct>:key/..."
      accessCredentialSecret: "lenny-backups-us-east-1"
```

**Fail-closed validation.** `lenny-preflight` and `lenny-ops` startup both reject the release if any region referenced by a tenant's `dataResidencyRegion` is missing a complete `storage.regions.<region>.{postgresEndpoint, minioEndpoint, redisEndpoint, kmsEndpoint}`, a complete `storage.regions.<region>.legalHoldEscrow.{endpoint, bucket, kmsKeyId, escrowKekId}`, or a complete `backups.regions.<region>.{minioEndpoint, kmsKeyId, accessCredentialSecret}` entry. This is the install-time counterpart to the runtime fail-closed errors `REGION_CONSTRAINT_UNRESOLVABLE` (runtime writes), `BACKUP_REGION_UNRESOLVABLE` (backup pipeline), `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` (continuous replication), `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` (Phase 3.5 of tenant force-delete), and `PLATFORM_AUDIT_REGION_UNRESOLVABLE` (platform-tenant audit events referencing a regulated target tenant, CMP-058). The `postgresEndpoint` field is the same value used by runtime-tenant writes, backup-pipeline residency routing, and platform-tenant audit residency routing — configuring it once per region satisfies all three surfaces.

**Legal-hold escrow residency.** The legal-hold escrow bucket, KMS key, and escrow KEK are all region-scoped: when a tenant with `dataResidencyRegion: eu-west-1` is force-deleted with active holds, Phase 3.5 resolves the region, re-encrypts held evidence under `platform:legal_hold_escrow:eu-west-1`, and writes the escrow objects to the EU-region bucket — never to a non-EU escrow bucket. A deployment that has not configured any `storage.regions.*` map uses `storage.legalHoldEscrowDefault` as the implicit single-region target, under the escrow KEK `platform:legal_hold_escrow:default`.

**Platform-tenant audit residency (CMP-058).** Audit events written under the platform tenant that reference a non-platform `target_tenant_id` — `security.audit_write_rejected`, `admin.impersonation_started`/`_ended`, `gdpr.legal_hold_overridden_tenant`, `legal_hold.escrow_region_resolved`, `legal_hold.escrowed`, `legal_hold.escrow_released`, `compliance.profile_decommissioned` — are routed to the target tenant's regional platform-Postgres (the same `storage.regions.<region>.postgresEndpoint` used by runtime writes). The *fact* that a regulated tenant was the subject of an impersonation, an escrow migration, or a compliance decommission is itself personal data describing that tenant and must remain in its jurisdiction. In multi-region deployments, no additional configuration is required beyond the per-region `postgresEndpoint` already declared for runtime routing — the gateway reuses it. When a target tenant's `dataResidencyRegion` resolves to no `storage.regions.<region>.postgresEndpoint` or the region's platform-Postgres is unreachable at write time, the originating operation halts with `PLATFORM_AUDIT_REGION_UNRESOLVABLE` (HTTP 422, `PERMANENT`) and the `PlatformAuditResidencyViolation` critical alert fires — impersonation issuance, Phase 3.5 escrow ledger writes, and compliance decommission all refuse to proceed until the target region's platform-Postgres is restored.

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

## Configuration Profiles by Deployment Size

The following table summarizes key configuration differences across deployment sizes:

| Setting | Starter | Growth | Scale |
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

Experiments are managed via the admin API or `lenny-ctl admin experiments`. They configure variant pools and deterministic routing for runtime version rollouts. Lenny provides the infrastructure primitives (variant pools, routing, manifest delivery) and a basic built-in assigner; most teams plug in an external experimentation platform (LaunchDarkly, Statsig, Unleash) via OpenFeature for assignment decisions.

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
- The `MemoryStoreGrowthHigh` alert fires when `rate(lenny_memory_store_user_count_over_threshold_total[5m]) > 0` is sustained for more than 5 minutes on any `tenant_id` — i.e., any user in the tenant has crossed 80% of `memory.maxMemoriesPerUser`. Per-user attribution is surfaced via structured logs emitted alongside each counter increment; the metric itself carries no `user_id` label (forbidden as a high-cardinality metric label; see `spec/16_observability.md` §16.1.1).
- The `MemoryStoreErasureDurationHigh` alert fires when the P99 of `lenny_memory_store_operation_duration_seconds{operation="delete_by_user"}` exceeds 60 seconds for more than 10 minutes, or when `operation="delete_by_tenant"` exceeds 300 seconds on the same window. The alert is the backend-level leading indicator of GDPR erasure-job delay and fires ahead of the job-aggregate `ErasureJobOverdue` tier deadlines (72 h for T3, 1 h for T4). Custom `MemoryStore` backends (Mem0, Zep, etc.) MUST emit both erasure-scope label values to make the alert function — see `spec/09_mcp-integration.md` §9.4 Instrumentation contract and `spec/12_storage-architecture.md` §12.8 custom-backend deployment guidance.

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
  redisStreamMaxLen: 50000             # By deployment size: 50,000 (Starter/Growth), 72,000 (Scale)
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
