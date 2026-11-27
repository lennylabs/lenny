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
  
  grpcPort: 50051                # Pod-to-gateway gRPC LNK-GWCONTROL
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
    executionMode: session               # session | service
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
    allowSelfRecursion: false            # Layer 2 of the §8.2 self-recursion gate (default: false)
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
- `agentInterface`, `delegationPolicyRef`, `labels`, `sessionPolicy`
- `allowSelfRecursion` (restrict-only override: a derived runtime may set `false` when the base is `true`; widening `false → true` is rejected with `INVALID_DERIVED_RUNTIME`)

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
        secretRef: anthropic-key
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
    executionMode: session               # session | service
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

Two execution modes are available: `session` and `service`. Session mode is parameterized by the `sessionPolicy` block; the acknowledgments, tenant pinning, and scaling factors derive from `sessionPolicy` properties rather than from mode names.

**`session`** -- A managed session is bound to a claimed pod for the session's lifetime. Default mode. In the default `sessionPolicy` (`maxConcurrentSessions: 1`, `recycle.enabled: false`) each pod is exclusive to one session and terminates when the session ends.

The full `sessionPolicy` block:

```yaml
pools:
  - name: claude-worker-pool
    runtime: claude-worker
    executionMode: session
    sessionPolicy:
      maxConcurrentSessions: 1           # simultaneous sessions per pod; > 1 requires acknowledgeProcessLevelIsolation
      acknowledgeProcessLevelIsolation: false  # required when maxConcurrentSessions > 1, and when recycling
                                               # with maxSessionsPerPod > 1, a scrubProfile other than
                                               # vm-restart, and runtimeProcess: keep, because a later
                                               # session can run in the process that served an earlier
                                               # one; admission refuses the pool without it
      recycle:
        enabled: false                   # true requires acknowledgeBestEffortScrub
        acknowledgeBestEffortScrub: false
        maxSessionsPerPod: 50            # required when recycle.enabled; counts every session served
        maxPodUptimeSeconds: 86400       # optional
        maxScrubFailures: 3
        onScrubFailure: warn             # warn | fail
        scrubProfile: standard           # standard | vm-restart | in-place
        # runtimeProcess: keep           # keep | restart; required, with no default, when recycle.enabled is true, maxSessionsPerPod > 1, and scrubProfile is not vm-restart; rejected on every other pool
        acknowledgeMicrovmResidualState: false  # required when scrubProfile: in-place
        allowCrossTenantReuse: false     # requires isolationProfile: microvm; never permitted when maxConcurrentSessions > 1
      cleanupCommands:
        - rm -rf /tmp/sandbox-*
      cleanupTimeoutSeconds: 60
      maxSessionAgeSeconds: 7200         # active-time session age cap; paused in suspended and the recovery states
      maxClientIdleSeconds: 900          # inactivity bound before idle suspension or expiry (default 900)
      slotRetries: 1
      onPoolExhausted: reject            # reject | queue
      maxQueueWaitSeconds: 30            # queue wait bound when onPoolExhausted: queue
```

The configurations the `sessionPolicy` settings express:

| Configuration | `maxConcurrentSessions` | `recycle.enabled` | Behavior |
|---|---|---|---|
| One session per pod | 1 | `false` | Each pod is exclusive to one session and terminates when the session ends (default). |
| Pod reuse | 1 | `true` | The pod is recycled across sequential sessions of the same tenant with a whole-pod scrub at the occupancy-zero boundary. |
| Concurrent | N | `true` | The pod serves up to N simultaneous sessions and recycles when occupancy reaches zero, unless the last release ends the occupancy without reuse ([Recycle lifecycle](../reference/execution-modes#recycle-lifecycle)). |
| Bounded cohort | N | `false` | The pod serves N concurrent sessions, then terminates after the cohort drains. |

**`service`** -- The gateway routes each message to any ready tenant-pinned replica, creating no claim and materializing no workspace. The pool keeps a per-pod [slot](../reference/glossary#slot) bound through `maxConcurrent`. Service mode provides no cross-message conversation continuity, and `sessionPolicy` does not apply:

```yaml
pools:
  - name: service-pool
    runtime: tool-server
    executionMode: service
    maxConcurrent: 8                     # per-pod slot bound; readiness-driven routing
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
        secretRef: anthropic-key-1
      - id: key-2
        secretRef: anthropic-key-2
    fallbackChain:
      - provider: aws_bedrock
        poolRef: bedrock-fallback
```

**`proxyDialect`** is required when `deliveryMode: proxy`. It must be one of the dialects declared in the bound Runtime's `credentialCapabilities.proxyDialect` list. Admission rejects mismatches with `422 INVALID_POOL_PROXY_DIALECT`. The gateway handles upstream provider translation inside its own process — the agent pod speaks the declared dialect regardless of which upstream provider the gateway ultimately calls. For providers outside the gateway's built-in set, or for deployers who want to route through a shared team gateway, see [external LLM proxy](external-llm-proxy.md).

### Credential Secrets

Each credential's value lives in its own Kubernetes Secret in the credentials namespace. The Helm value `credentials.namespace` names that namespace (default `lenny-credentials`), and the chart creates it. [Credentials Namespace](namespace-and-isolation.md#credentials-namespace) describes its access rules.

A credential enters a pool by value, by reference, or by role. Adding by value is the standard path for a `tenant-admin`, because it needs no Kubernetes access.

| Method | Caller | Request | Rotation |
|---|---|---|---|
| By value | `tenant-admin` for its own tenant, or `platform-admin` | `POST /v1/admin/credential-pools/{name}/credentials` carrying `secretData`. The gateway writes the Secret. | `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` carrying `secretData` overwrites the Secret. |
| By reference | `platform-admin`, or the bootstrap seed in Helm values | Create the Secret in the credentials namespace, then submit its name as `secretRef`. | Update the Secret. No pool update is required. |
| By role | `tenant-admin` for its own tenant, or `platform-admin` | `roleArn` on the same endpoint. | The entry stores the role ARN and no credential value. |

The following request adds a credential by value:

```
POST /v1/admin/credential-pools/anthropic-production/credentials
{"id": "key-3", "secretData": {"apiKey": "sk-ant-..."}}
```

The following commands create a Secret and add it by reference:

```bash
kubectl create secret generic anthropic-key-3 \
  -n lenny-credentials \
  --from-literal=apiKey="sk-ant-..."

lenny-ctl admin credential-pools add-credential \
  --pool anthropic-production --provider anthropic_direct \
  --id key-3 --secret-ref anthropic-key-3
```

The keys of a Secret, and of `secretData`, are the provider's key fields. A key the provider does not read leaves that provider's read failing closed.

| Provider | Secret keys |
|---|---|
| `anthropic_direct` | `apiKey` |
| `aws_bedrock` (role) | `roleArn` |
| `aws_bedrock` (access keys) | `accessKeyId` and `secretAccessKey` |
| `vertex_ai` | `serviceAccountJson` |
| `azure_openai` (API key) | `apiKey` |
| `azure_openai` (Azure AD) | `clientId`, `clientSecret`, and `tenantId` |
| `github` | `token` |

A `secretRef` is either `<name>` or `<credentials.namespace>/<name>`, and the gateway stores it in the second form. Names that begin with `lenny-cred-` are reserved for Secrets the gateway writes. The gateway probes each newly introduced `secretRef` through the Token Service before it commits the write, so a missing Secret is refused at registration. The bootstrap seed runs no probe: a Secret may be created after the bootstrap Job runs, and a read of a Secret that does not yet exist fails closed. The [Admin API](../api/admin.md#credential-write-rules) lists the refusals.

The Token Service reads a Secret on every use and caches no value, so the next read after a rotation returns the new value. Active leases keep the previously materialized credential until they expire. To force them onto the new value, revoke the credential as described in the [credential-revocation runbook](../runbooks/credential-revocation.md).

### Delivery Modes

| Mode | Security | How It Works |
|---|---|---|
| `proxy` | **Recommended** -- key never enters pod | Pod sends requests to gateway LLM Proxy; gateway injects real API key |
| `direct` | Key delivered to the session's credential file at `/run/lenny/slots/{sessionId}/credentials.json` on the pod | Pod contacts LLM provider directly; suitable for single-tenant/dev only |

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
    allowSelfRecursion: false            # Layer 3 of the §8.2 three-layer self-recursion gate
```

### Self-recursion gate (platform layer)

A delegation hop where the target's `(runtime_name, pool_name)` already appears in the caller's lineage is rejected by default. To opt in, **all three** layers must be `true` and the outer gate must be `enforce`:

```yaml
gateway:
  cycleDetection:
    mode: enforce                # enforce | warn | permissive
    # justification: "..."       # required when mode is warn|permissive
  allowSelfRecursion: yes        # yes | no — Layer 1 (master platform gate)
  delegation:
    defaultMaxDepth: 10          # Always-enforced fallback for lease maxDepth
```

- **`mode: enforce`** (default) — the three-layer AND gate (Helm + Runtime + DelegationPolicy) decides admission. Rejections return `DELEGATION_CYCLE_DETECTED` with `details.blockedBy` naming the first `false` layer.
- **`mode: warn`** — every self-recursive hop is admitted; each emits a `delegation.cycle_warning` audit event and increments `lenny_delegation_would_have_blocked_total{mode="warn"}`. Use this to size the impact of an `enforce` rollout before flipping. Requires `gateway.cycleDetection.justification`.
- **`mode: permissive`** — no check runs. Development clusters only; raises the standing `CycleDetectionModeUnsafe` warning alert.
- **`gateway.allowSelfRecursion: no`** — master kill-switch that rejects every self-recursive hop regardless of runtime or policy opt-in.
- **`gateway.delegation.defaultMaxDepth`** — bounds chain length even under `mode: permissive`. Cycle-detection mode controls runtime-identity gating, not depth.

Helm transitions on these values emit `gateway.cycle_detection_mode_changed`, `gateway.allow_self_recursion_changed`, and `gateway.default_max_depth_changed` audit events; security-relaxing transitions require a justification.

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

### Stale slot reaper

```yaml
controller:
  claimOrphanTimeoutSeconds: 900   # Default: 900. Also the stale slot reaper's age.
gateway:
  slotReaper:
    intervalSeconds: 60            # Default: 60. Wait between passes.
    ratePerSecond: 50              # Default: 50. Must be positive.
```

On a pool with `maxConcurrentSessions` above 1, the gateway counts each pod's occupied slots in a Redis slot set. A session's release frees its slot. When the release does not free the slot, such as when it fails during a Redis outage, the slot stays counted and the pod admits one session fewer. The gateway leader runs a stale slot reaper that releases such a slot once the session that held it ended at least `claimOrphanTimeoutSeconds` ago. The reaper asks the pod's runtime adapter to stop that session and frees the slot only when the adapter confirms a clean stop. A slot whose adapter does not answer, or reports that the session's runtime may still be running, stays counted, and the reaper retries it at a later pass. The reaper never frees a leaked slot, which is a slot whose cleanup the adapter did not report as clean. The reaper also keeps a slot that is the pod's last remaining slot, and it does not visit a pod that no session holds, where a session that has ended holds its pod until the gateway releases it. Such a slot stays counted until the pod's last active session ends, after which the controller drains the pod once the orphaned-claim timeout (`claimOrphanTimeoutSeconds`) elapses. A pass starts `intervalSeconds` after the previous pass ends, and `ratePerSecond` bounds the number of slots the reaper releases per second.

On a pod that still runs other sessions, a slot whose release failed is freed `claimOrphanTimeoutSeconds` after its session ended, plus up to one pass interval, plus the time the reaper takes to reach it at `ratePerSecond`. At the defaults, the roughly 12,000 slots that a 60-second Redis outage can leave on a Scale-size deployment drain in about 4 minutes, so the reaper frees each such slot within about 20 minutes of its session's end while the pod's other sessions keep running. On a pod whose slot set the gateway rebuilt after a Redis outage, the reaper reaches the slot at the first pass after the pod's next slot reservation or release.

The gateway also records each slot reservation in Postgres before the session's record names the pod, so that the Postgres check it uses during a Redis outage counts a session that is still being created. The session's release deletes that record. When a gateway replica stops while creating a session, the record stays and the pod admits one session fewer. Each reaper pass deletes such a record once it is older than `claimOrphanTimeoutSeconds` and its session either has no record of its own or ended more than `claimOrphanTimeoutSeconds` ago.

`claimOrphanTimeoutSeconds` also sets the controller's orphaned-claim timeout. The chart renders the one value into the controller's `--claim-orphan-timeout` flag and the gateway's `--slot-reaper-terminal-age` flag, so the two stay equal; a deployment outside the chart sets both flags to the same value. The value must be at least one second longer than the longest of the session finalize timeout (the gateway's `--max-finalizing-state-timeout-seconds`, default 600 seconds), the session resume timeout (the gateway's `--max-resuming-seconds`, default 300 seconds), and the sum of `gateway.workspaceSealMaxDurationSeconds` (300 seconds when set to 0) and the gateway's release allowance (`--stream-failure-report-timeout`, default 30 seconds). This margin ensures that no session launch, restore, workspace seal, or post-seal release still uses a slot when the reaper stops it. The chart refuses to render with a shorter value or with a `gateway.slotReaper.ratePerSecond` that is not positive, and the gateway refuses to start with a `--slot-reaper-terminal-age` shorter than the values it has in effect allow.

Change these values across a rolling gateway update in a fixed order, because a gateway replica that still runs the old configuration can hold the leader role while a replica with the new configuration runs a launch, restore, or seal. To raise one of these timeouts, raise `claimOrphanTimeoutSeconds` first in its own rollout, and raise the timeout in a later rollout. To lower `claimOrphanTimeoutSeconds`, lower the timeout first in its own rollout, and lower `claimOrphanTimeoutSeconds` in a later rollout.

### Erasure and tenant-deletion wait

| Gateway flag | Environment variable | Default | Effect |
|:--|:--|:--|:--|
| `--terminal-pipeline-wait-margin` | `LENNY_TERMINAL_PIPELINE_WAIT_MARGIN` | `30s` | Time that user erasure and tenant deletion add to the terminal pipeline budget while they wait for the terminal steps of the sessions they drained. The gateway does not start with a negative value, or with a value so large that its sum with the budget exceeds the largest duration the gateway represents (about 292 years). `0s` waits for the budget alone. |

When a session ends, the gateway that ends it runs the session's terminal steps: the workspace seal, the pod or slot release, the session-log close, the final quota checkpoint, and the final billing event. It records on the session's record when they finish. A [user erasure](security.md#gdpr-erasure) job deletes no data, and a [tenant deletion](multi-tenancy.md) does not leave its session-drain phase, while a session it drained has not recorded the end of its terminal steps, for at most the terminal pipeline budget plus the margin after the session ended. The terminal pipeline budget is the largest `maxWorkspaceSealDurationSeconds` of any pool plus the release allowance (`--stream-failure-report-timeout`, default 30 seconds).

The gateway that runs a session's terminal steps stops them when the budget has elapsed on its own clock, and the gateway leader runs the wait on its clock. The margin covers the difference between the clocks of the two replicas and a write of the terminal steps that commits just after the budget ends. Set the margin to at least the largest clock offset between gateway replicas that the deployment allows, plus the commit latency of a Postgres write. The margin lengthens only this wait: the pod and slot accounting, the stale slot reaper, and artifact retention use the budget alone. A session whose terminal steps record no end, such as one cut by a gateway restart, delays an erasure or a tenant deletion by at most the budget plus the margin.

The wait assumes that every gateway replica runs with the same budget. A rolling update that raises `maxWorkspaceSealDurationSeconds` or the release allowance leaves replicas with the old, shorter budget, and while one of them holds the leader role it ends the wait before the terminal steps that a replica with the new budget runs can end. Keeping the wait over the longest budget in effect during such a rollout is the deployer's responsibility: raise `--terminal-pipeline-wait-margin` by at least the increase of the budget in its own rollout first, raise the budget in a later rollout, and lower the margin again once every replica runs the new budget.

The chart renders neither `--terminal-pipeline-wait-margin` nor `--stream-failure-report-timeout`, and it has no value for extra gateway arguments or environment variables. A chart install changes either setting only by patching the rendered gateway Deployment, for example with a Helm post-renderer that adds `LENNY_TERMINAL_PIPELINE_WAIT_MARGIN` to the gateway container's environment.

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

### Upload-token signing keys

Every gateway replica signs and verifies upload tokens with the same keys, which the gateway stores encrypted in the Postgres table `platform_signing_keys` and rotates there. The rotation runs on three gateway settings. The chart does not set them, so the defaults apply unless the gateway container's arguments or environment set them.

| Flag | Environment variable | Default | Meaning |
|:--|:--|:--|:--|
| `--signing-key-refresh-interval` | `LENNY_SIGNING_KEY_REFRESH_INTERVAL` | `30s` | How often a replica reloads the keys, writes the next key when the active key is due for rotation, and deletes keys past their verification time. |
| `--signing-key-publish-lead` | `LENNY_SIGNING_KEY_PUBLISH_LEAD` | `2m` | How far ahead of its activation a replica writes the next key, so every replica has loaded it before any replica signs with it. It must be longer than the refresh interval, and the gateway does not start when it is not. |
| `--upload-token-key-rotation-interval` | `LENNY_UPLOAD_TOKEN_KEY_ROTATION_INTERVAL` | `24h` | How long a key signs before the next key replaces it. |

After the next key becomes active, a replaced key keeps verifying for the overlap window (five minutes, which has no setting) plus the longer of the overlap window and the upload-token lifetime, `--max-created-state-timeout-seconds` (`LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS`, default 300 seconds). At the defaults that is ten minutes, and a longer token lifetime lengthens it, so every token a replica signs with the replaced key verifies on every replica until it expires.

A replica whose reload has failed for longer than five minutes stops issuing upload tokens, so session creation on it answers `503 SESSION_CREATION_FAILED` until a reload succeeds, while it keeps accepting the tokens it can verify. On a cloud KMS provider, map the alias `platform:upload-token-signing` in `kms.aliases` to a symmetric encryption key; the gateway does not start when it cannot wrap or unwrap a key under that alias. [Security](security.md) describes the keys and the KMS grants.

### Retired redaction receipt keys

```yaml
audit:
  retiredReceiptKeyIds: []              # Default: empty.
```

`audit.retiredReceiptKeyIds` lists the KMS key identifiers of retired redaction receipt signing keys: keys that signed receipts and that neither `platform:audit-signing` nor `platform:audit-verify` in `kms.aliases` names any longer. The chart renders the list as the gateway's `LENNY_AUDIT_RETIRED_RECEIPT_KEY_IDS` environment variable, which sets `--audit-retired-receipt-key-ids` (a comma-separated list), and omits it when the list is empty. The chain verifier and the periodic receipt scan trust the keys the two aliases name and the listed keys, and a receipt signed by any other key reads `broken`.

Each entry is the `signature_kms_key_id` that receipts carry, in the form of a receipt alias's reference: an AWS key ID or key ARN, a GCP `cryptoKeyVersions/<n>` resource name, or an Azure `<name>@<version>` reference. The gateway reads each listed key's public key from the KMS by its identifier at startup, and it does not start when an identifier does not name one key version or when the KMS cannot return the key. When a rotation moves `platform:audit-verify` off a key, add that key's identifier to the list in the same rollout. Keep each listed key in the KMS, and in the list, for `audit.gdprRetentionDays`. [Security](security.md#redaction-receipt-signing-key) describes the rotation.

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

### Elicitation content integrity floor

```yaml
security:
  elicitationContentIntegrity:
    floor: off                            # Default: off. One of: off | detect-only | enforce.
```

The gateway binds elicitation display text to its originating pod by recording the canonical `{message, schema}` pair at `lenny/request_elicitation` time and comparing its SHA-256 digest against every forward-hop `elicitation/create` re-emission (SPEC §9.2 gateway-origin-binding invariant). Whether a divergence is rejected, recorded-and-forwarded, or ignored is governed per tenant by the `elicitationContentIntegrityEnforcement` mode (three values: `enforce`, `detect-only`, `off`; tenants default to `enforce`), clamped from below by this platform-level floor. The gateway computes every tenant's effective mode as `max(platform_floor, tenant_stored_mode)` under ordering `off < detect-only < enforce`. The floor is the platform operator's lever to ensure that no tenant operates below a minimum integrity posture, regardless of per-tenant configuration.

The floor value is rendered into the `lenny-deployment-phase-stamp` ConfigMap (same ConfigMap that holds the feature-flag keys) under the key `security.elicitationContentIntegrity.floor`. Unlike the feature-flag keys on that ConfigMap — whose write semantics are strictly append-only — the floor key is rewritten on every render; raising or lowering the floor is an ordinary, operator-audited posture change rather than a phase downgrade. Each floor transition emits a `platform.elicitation_content_integrity_floor_changed` audit event (SPEC §16.7) recording the previous and new floor, the operator's OIDC `sub`, and a count of tenants whose effective mode was raised by the change. A floor change that raises the effective mode for one or more tenants also emits one `tenant.elicitation_content_integrity_floor_clamp` audit event per affected tenant, so the full fanout is traceable.

The `ElicitationContentIntegrityWeakened` warning alert fires continuously whenever any active tenant's effective mode is weaker than `enforce` (see `docs/reference/metrics.md`). This is the steady-state signal that the deployment is operating below the maximum-integrity posture — operators who raised the floor to `enforce` AND ensured every tenant's stored mode is also `enforce` will see this alert resolve. The companion `ElicitationContentIntegrityPermissiveTamper` alert is orthogonal: it fires only on an actual `detect-only` divergence, whereas `ElicitationContentIntegrityWeakened` fires on the standing configuration alone.

**Typical values.** For non-regulated deployments, `floor: off` (default) leaves per-tenant control authoritative. For regulated or security-sensitive deployments, `floor: detect-only` as a pre-enforcement canary (with `ElicitationContentIntegrityPermissiveTamper` as the actionable signal) or `floor: enforce` as the production posture are both reasonable. Setting `floor: enforce` has the effect that every tenant's effective mode is `enforce` regardless of their stored value — use this to lock in a platform-wide integrity guarantee.

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

Experiments are managed via the admin API or `lenny-ctl admin experiments`. They configure variant pools and deterministic routing for runtime version rollouts. Lenny provides the infrastructure primitives (variant pools, routing, and variant delivery in each session's [`session_start` frame](../reference/adapter-contract.md#inbound-messages-adapter-writes-to-your-stdin)) and a basic built-in assigner; most teams plug in an external experimentation platform (LaunchDarkly, Statsig, Unleash) via OpenFeature for assignment decisions.

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

### Event Fields

Each billing event carries the tenant, the event type, and a timestamp. Most events also carry the session, and most of those carry the ID of the user who started the session. A `session.completed` event carries no user ID. A consumer that needs the user of a `session.completed` event joins it on `session_id` with the session's `session.created` event. After a user erasure pseudonymizes the user's billing events, the `session.created` event carries the pseudonym, so the join yields the pseudonym. The gateway writes `session.completed` when the session's terminal steps finish, which can be after an erasure of its user has pseudonymized the user's events, and an event with no user ID leaves no raw user ID behind in that case. The gateway writes no `session.completed` event for a session that ends in one of these ways: a failure while the session is `finalizing` or `starting`, the failure of a delegated session during delegation tree recovery, a cancel by the `lenny/cancel_session` or `lenny/cancel_child` tool, a cancel of a delegation tree's sessions by a failure cascade or after the tree's budget records expire, the end of a held Open Responses conversation, a cancel when an administrator fully revokes the session's user, and the end of a single-shot request through the OpenAI Chat Completions or Open Responses surface.

### Retention

| Profile | Retention |
|---|---|
| Default | 395 days (~13 months) |
| SOC2 / FedRAMP | 365 days (floor) |
| HIPAA | 2,190 days (6 years) |

### Billing Corrections

Billing corrections require **dual-control approval** (four-eyes principle). Corrections whose absolute adjustment value exceeds `billing.dualControlThreshold` (default: `0`, meaning all corrections) must be approved by a second `platform-admin` before they are committed to the immutable billing stream.
