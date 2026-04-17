---
layout: default
title: Configuration Reference
parent: Reference
nav_order: 4
---

# Configuration Reference
{: .no_toc }

Complete reference for all Helm `values.yaml` configuration fields, organized by component. Each field includes its type, default value, description, and validation rules.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## Gateway configuration

### Core gateway settings

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `gateway.maxSessionsPerReplica` | int | 50 (T1), 200 (T2), 400 (T3/T4) | Maximum concurrent sessions per gateway replica. Used for `GatewaySessionBudgetNearExhaustion` alert (capacity ceiling, not HPA trigger). Values are provisional -- must be calibrated by Phase 2 benchmarks. Tier 3 value (400) assumes LLM Proxy subsystem has been extracted. | Must be > 0. |
| `gateway.maxCreatedStateTimeoutSeconds` | int | 300 | Maximum time a session can remain in `created` state before automatic cleanup. Also governs upload token TTL. | Must be > 0. |

### Subsystem concurrency limits

Each gateway subsystem has independently configurable concurrency and queue-depth settings.

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `gateway.subsystems.streamProxy.maxConcurrent` | int | Per-tier (see Deployment Topology) | Maximum concurrent goroutines for the Stream Proxy subsystem. | Must be > 0. |
| `gateway.subsystems.uploadHandler.maxConcurrent` | int | Per-tier | Maximum concurrent goroutines for the Upload Handler subsystem. | Must be > 0. |
| `gateway.subsystems.mcpFabric.maxConcurrent` | int | Per-tier | Maximum concurrent goroutines for the MCP Fabric subsystem. | Must be > 0. |
| `gateway.subsystems.llmProxy.maxConcurrent` | int | Per-tier | Maximum concurrent goroutines for the LLM Proxy subsystem. | Must be > 0. |

### Extraction thresholds

Configurable thresholds for subsystem extraction decisions. All values are provisional and must be calibrated by Phase 2 benchmarks.

| Field | Type | Default | Description |
|:------|:-----|:--------|:------------|
| `gateway.extractionThresholds.streamProxy.queueDepth` | int | 500 | Queue depth threshold for Stream Proxy extraction consideration. |
| `gateway.extractionThresholds.streamProxy.p99AttachLatencyMs` | int | 800 | P99 attach latency threshold (ms). |
| `gateway.extractionThresholds.uploadHandler.activeConcurrent` | int | 200 | Active upload threshold. |
| `gateway.extractionThresholds.mcpFabric.activeDelegations` | int | 1000 | Active delegation threshold. |
| `gateway.extractionThresholds.mcpFabric.p99OrchestrationLatencyMs` | int | 2000 | P99 orchestration latency threshold (ms). |
| `gateway.extractionThresholds.llmProxy.activeConnections` | int | 2000 | Active upstream LLM connection threshold. |

### LLM Proxy (LiteLLM sidecar)

LiteLLM runs as a sidecar container co-located with each gateway replica. The gateway's LLM Proxy subsystem terminates runtime-facing OpenAI/Anthropic requests and forwards to the sidecar on loopback.

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `gateway.llmProxy.litellm.enabled` | bool | `true` | Deploy the LiteLLM sidecar. When disabled, only direct-mode (runtime-managed credentials) is supported. | -- |
| `gateway.llmProxy.litellm.image` | string | `lenny/litellm-hardened:<lenny-version>` | Container image for the LiteLLM sidecar. Lenny ships a hardened wrapper image pinned to the main Lenny release. | Valid image reference. |
| `gateway.llmProxy.litellm.port` | int | 4000 | Loopback port inside the gateway pod on which the sidecar listens. | 1024-65535. |
| `gateway.llmProxy.litellm.configMap` | string | `lenny-litellm-config` | Name of the ConfigMap containing `config.yaml` for LiteLLM (model list, provider credentials, routing rules). | Must reference an existing ConfigMap. |
| `gateway.llmProxy.litellm.resources.cpu` | string | `"100m"` (request), `"500m"` (limit) | CPU request/limit for the sidecar container. | Valid Kubernetes resource quantity. |
| `gateway.llmProxy.litellm.resources.memory` | string | `"128Mi"` (request), `"512Mi"` (limit) | Memory request/limit for the sidecar container. | Valid Kubernetes resource quantity. |
| `gateway.llmProxy.proxyDialects` | string[] | `["openai", "anthropic"]` | Proxy dialects enabled for runtime-facing endpoints. Must cover every `Runtime.credentialCapabilities.proxyDialect` in use. | Subset of `openai`, `anthropic`. |
| `gateway.llmProxy.upstreamTLSVerify` | bool | `true` | Verify upstream provider TLS certificates (applies to LiteLLM's connection to OpenAI/Anthropic/Bedrock/etc). | -- |

---

## Pool configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `pool.minWarm` | int | Per-pool | Minimum number of idle warm pods to maintain. The PoolScalingController reconciles toward this target. | Must be >= 0. |
| `pool.runtimeClass` | string | `standard` | Kubernetes RuntimeClass for pods in this pool. One of `standard` (runc), `gvisor`, `kata-microvm`. | Must be a valid RuntimeClass name. |
| `pool.resources.cpu` | string | `"250m"` (request), `"2"` (limit) | CPU request and limit for agent containers. | Valid Kubernetes resource quantity. |
| `pool.resources.memory` | string | `"256Mi"` (request), `"2Gi"` (limit) | Memory request and limit for agent containers. | Valid Kubernetes resource quantity. |
| `pool.executionMode` | string | `session` | Pod execution mode: `session` (one session per pod), `task` (sequential reuse), `concurrent` (parallel slots). | One of `session`, `task`, `concurrent`. |

### Checkpointing configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `pool.checkpointing.enabled` | bool | `true` | Enable periodic workspace checkpointing. | -- |
| `pool.checkpointing.interval` | int | 600 | Alias for `periodicCheckpointIntervalSeconds`. | Must be > 0 when enabled. |
| `pool.checkpointing.workspaceSizeLimitBytes` | int | 536870912 (512 Mi) | Maximum workspace size for checkpointing. Workspaces exceeding this limit skip checkpoint and log a warning. The pre-checkpoint size probe increments `lenny_checkpoint_size_exceeded_total`. | Must be > 0. |
| `pool.periodicCheckpointIntervalSeconds` | int | 600 | Interval between periodic checkpoint attempts for active sessions. | Must be > 0 when checkpointing enabled. |

### Elicitation policy

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `pool.elicitationDepthPolicy` | string | `suppress_at_depth: 3` | Controls which elicitation types are allowed at each delegation depth. Options: `allow_all`, `suppress_at_depth: N` (suppress at depth N+), `block_all`. | Valid policy string. |
| `pool.urlModeElicitation.enabled` | bool | `false` | Enable URL-mode elicitation for agent-initiated OAuth flows. | -- |
| `pool.urlModeElicitation.domainAllowlist` | string[] | `[]` | Allowed domains for URL-mode elicitation. Required when `enabled: true`. Returns `URL_MODE_ELICITATION_DOMAIN_REQUIRED` if empty when enabled. | Non-empty when `enabled: true`. |

### Scaling policy

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `pool.scalingPolicy.podWarmupSecondsBaseline` | int | 30 | Expected pod warmup duration. Used to compute `WarmPoolReplenishmentSlow` alert threshold (2x this value). | Must be > 0. |
| `pool.scalingPolicy.sdkConnectTimeoutSeconds` | int | 60 | Maximum time for SDK warm connection establishment before marking pod as failed. | Must be > 0. |
| `pool.maxSuspendedPodHoldSeconds` | int | 300 | Maximum time a suspended session holds its pod before the gateway releases it. | Must be > 0. |
| `pool.maxWorkspaceSealDurationSeconds` | int | 300 | Maximum retry window for workspace seal-and-export. | Must be > 0. |

---

## Runtime configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `runtime.name` | string | Required | Unique name for the runtime. | Non-empty, alphanumeric with hyphens. |
| `runtime.image` | string | Required | Container image reference (digest recommended for production). | Valid image reference. |
| `runtime.executionMode` | string | `agent` | Execution mode: `agent` (full session lifecycle) or `mcp` (MCP server). | One of `agent`, `mcp`. |
| `runtime.capabilities` | object | `{}` | Runtime capability declarations. | See capabilities table below. |
| `runtime.agentInterface` | string | `grpc` | Adapter interface: `grpc` (Standard/Full tier) or `stdio` (Minimum tier). | One of `grpc`, `stdio`. |
| `runtime.publishedMetadata` | object | `{}` | Metadata published in the runtime registry for client discovery. | -- |
| `runtime.sdkWarmBlockingPaths` | string[] | `["CLAUDE.md", ".claude/*"]` | Glob patterns for files that trigger SDK-warm demotion. Empty list disables demotion. Uses Go `path.Match` with `**` support. | Valid glob patterns. |
| `runtime.delegationPolicyRef` | string | `null` | Reference to a named `DelegationPolicy` resource. | Must reference an existing policy if set. |

### Runtime capabilities

| Field | Type | Default | Description |
|:------|:-----|:--------|:------------|
| `capabilities.preConnect` | bool | `false` | Enable SDK-warm mode. Requires adapter to implement `DemoteSDK` RPC. |
| `capabilities.midSessionUpload` | bool | `false` | Allow file uploads during active sessions. |
| `capabilities.checkpoint` | bool | `false` | Support for workspace checkpointing. |
| `capabilities.injection` | object | `{ supported: true }` | Whether the runtime accepts injected messages. |

---

## Session configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `session.maxSessionAgeSeconds` | int | 7200 | Maximum wall-clock age of a session before automatic expiry. Timer paused during `suspended` and `resume_pending`. | Must be > 0. |
| `session.maxIdleTimeSeconds` | int | 600 | Maximum time without qualifying activity before automatic expiry. Qualifying events: agent output, tool call, tool result, elicitation request, message send. | Must be > 0. |
| `session.maxElicitationsPerSession` | int | 50 | Maximum elicitation requests per session. Prevents agents from spamming the user. | Must be > 0. |
| `session.maxElicitationWait` | int | 600 | Maximum time (seconds) a session waits for a human response to an elicitation. If exceeded, the elicitation is dismissed and the pod receives a timeout error. | Must be > 0. |
| `session.maxResumeWindowSeconds` | int | 900 | Maximum time to wait for pod allocation during `resume_pending`. | Must be > 0. |
| `session.maxAwaitingClientActionSeconds` | int | 900 | Maximum time in `awaiting_client_action` before automatic expiry. Timer starts fresh on entry. | Must be > 0. |
| `session.maxRequestInputWaitSeconds` | int | 600 | Maximum time a `lenny/request_input` call blocks before timeout. | Must be > 0. |
| `session.retryPolicy.maxRetries` | int | 2 | Maximum automatic retry attempts for retryable failures. | Must be >= 0. |

---

## Scaling configuration (HPA)

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `scaling.hpa.minReplicas` | int | Per-tier | Minimum gateway replicas. | Must be > 0. |
| `scaling.hpa.maxReplicas` | int | Per-tier | Maximum gateway replicas. | Must be >= minReplicas. |
| `scaling.hpa.targetUtilization` | int | 80 | CPU target utilization percentage for HPA. | 1-100. |
| `scaling.hpa.requestQueueDepth.target` | int | 10 | Target `averageValue` for `lenny_gateway_request_queue_depth` HPA metric. | Must be > 0. |

### Per-tier HPA defaults

| Parameter | Tier 1 | Tier 2 | Tier 3 | Tier 4 |
|:----------|:-------|:-------|:-------|:-------|
| `maxSessionsPerReplica` | 50 | 200 | 400 | 400 |
| HPA target utilization | 80% | 80% | 80% | 80% |

---

## Messaging configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `messaging.defaultScope` | string | `direct` | Default messaging scope for sessions without overrides. | One of `direct`, `siblings`. |
| `messaging.maxScope` | string | `siblings` | Absolute ceiling -- no tenant or runtime can widen beyond this. | One of `direct`, `siblings`. |
| `messaging.durableInbox` | bool | `false` | Use Redis-list-backed inbox instead of in-memory. Provides durability across coordinator crashes. | -- |
| `messaging.maxInboxSize` | int | 500 | Maximum messages per session inbox. Overflow drops oldest message. | Must be > 0. |
| `messaging.maxDLQSize` | int | 500 | Maximum messages in dead-letter queue per session. | Must be > 0. |

---

## EventBus / CloudEvents transport

Inter-subsystem events use a CloudEvents v1.0.2 envelope over Redis pub/sub (`RedisEventBus` is the v1 implementation). `type` values follow `dev.lenny.<short_name>`.

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `eventBus.source` | string | `https://lenny.example.com` | CloudEvents `source` attribute. Should be the deployment's canonical URL. | Valid URI-reference. |
| `eventBus.typePrefix` | string | `dev.lenny` | Prefix for CloudEvents `type` values. Concrete events append a short name (`dev.lenny.session.created`, `dev.lenny.audit.record`, etc). | Non-empty, reverse-DNS style. |
| `eventBus.datacontenttype` | string | `application/json` | CloudEvents `datacontenttype` for payloads on this bus. | Valid MIME type. |
| `eventBus.redis.channelPrefix` | string | `lenny:events` | Redis pub/sub channel prefix. Per-topic channels are `<prefix>:<topic>`. | Non-empty. |
| `eventBus.publishQueueDepth` | int | 2048 | In-memory publish buffer. Overflow drops events and increments `lenny_event_bus_publish_dropped_total`. | Must be > 0. |

---

## Storage configuration

### Postgres

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `postgres.connectionString` | string | Required | PostgreSQL connection string. Supports DSN format. | Valid DSN. |
| `postgres.deploymentProfile` | string | `self-managed` | Database deployment profile: `self-managed` (with PgBouncer) or `cloud-managed` (provider proxy). | One of `self-managed`, `cloud-managed`. |

### Redis

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `redis.connectionString` | string | Required | Redis connection string. | Valid Redis connection string. |

### MinIO / Object storage

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `minio.endpoint` | string | Required | MinIO endpoint URL. Supports S3, GCS, or Azure Blob with appropriate configuration. | Valid URL. |
| `minio.accessKey` | string | Required | MinIO access key. | Non-empty. |
| `minio.secretKey` | string | Required | MinIO secret key. | Non-empty. |

---

## Security configuration

### TLS

| Field | Type | Default | Description |
|:------|:-----|:--------|:------------|
| `tls.enabled` | bool | `true` | Enable TLS for all external and internal communication. |
| `tls.certManager.enabled` | bool | `true` | Use cert-manager for automatic certificate lifecycle. |
| `tls.certManager.issuerRef` | string | -- | Reference to cert-manager Issuer or ClusterIssuer. |

### OIDC / OAuth

| Field | Type | Default | Description |
|:------|:-----|:--------|:------------|
| `oidc.issuerUrl` | string | Required | OIDC issuer URL for client authentication. |
| `oidc.clientId` | string | Required | OIDC client ID. |
| `oidc.audience` | string | -- | Expected audience claim in JWT tokens. |

### Token exchange (`/v1/oauth/token`)

`/v1/oauth/token` is the canonical endpoint for token issuance, rotation, and delegation token minting via RFC 8693. Grant type URN: `urn:ietf:params:oauth:grant-type:token-exchange`.

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `tokenExchange.defaultTokenTTLSeconds` | int | 3600 | Default lifetime for newly minted access tokens. Narrowed child tokens inherit a cap from the parent's remaining lifetime. | Must be > 0. |
| `tokenExchange.maxTokenTTLSeconds` | int | 28800 | Absolute maximum lifetime for any token issued by `/v1/oauth/token`. Parent lifetime caps child lifetime. | Must be >= `defaultTokenTTLSeconds`. |
| `tokenExchange.rateLimitPerUser` | int | 60 | Maximum `/v1/oauth/token` requests per user per minute. Rotation + delegation minting share this budget. | Must be > 0. |
| `tokenExchange.requireActorToken` | bool | `true` | Require `actor_token` on delegation child-token minting. Enforces the parent→child chain of custody. | -- |
| `tokenExchange.supportedSubjectTokenTypes` | string[] | `["urn:ietf:params:oauth:token-type:access_token", "urn:ietf:params:oauth:token-type:jwt"]` | Accepted `subject_token_type` values. | Subset of IANA-registered token types. |

---

### KMS

| Field | Type | Default | Description |
|:------|:-----|:--------|:------------|
| `kms.provider` | string | -- | KMS provider: `aws-kms`, `gcp-kms`, `azure-keyvault`, `vault`. |
| `kms.keyId` | string | -- | KMS key identifier for envelope encryption. |

---

## Observability configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `global.traceSamplingRate` | float | 0.10 | Default probabilistic trace sampling rate (0.0-1.0). 100% for errors, slow requests, and delegation trees. | 0.0-1.0. |
| `slo.validated` | bool | `false` | Set to `true` by Phase 14.5 benchmark automation after SLO validation. Suppresses the provisional-values startup warning. | -- |
| `slo.burnRate.fastMultiplier` | int | 14 | Fast-window burn rate multiplier for SLO alerts. | Must be > 0. |
| `slo.burnRate.slowMultiplier` | int | 3 | Slow-window burn rate multiplier for SLO alerts. | Must be > 0. |

---

## Audit configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `audit.retentionPreset` | string | `soc2` | Audit retention preset. Options: `soc2` (365d), `fedramp-high` (1095d), `hipaa` (2190d), `nis2-dora` (1825d), `custom`. | Valid preset name. |
| `audit.retentionDays` | int | 365 | Custom retention days. Used when `retentionPreset: custom`. | Must be > 0. |
| `audit.gdprRetentionDays` | int | 2555 | Retention for GDPR erasure receipt rows. Hard minimum floor. | Must be >= 2555. |
| `audit.siem.endpoint` | string | -- | External SIEM endpoint for audit event forwarding. Required for regulated `complianceProfile` tenants. | Valid URL when set. |
| `audit.siem.maxDeliveryLagSeconds` | int | 30 | Maximum acceptable SIEM delivery lag before alerting. | Must be > 0. |
| `audit.pgaudit.enabled` | bool | `false` | Enable pgaudit for regulated tenants. Required when `complianceProfile` is `hipaa` or `fedramp`. | -- |
| `audit.pgaudit.sinkEndpoint` | string | -- | pgaudit log forwarding endpoint. Required when `pgaudit.enabled: true`. | Valid URL when enabled. |
| `audit.grantCheckInterval` | duration | `5m` | Interval for periodic audit table grant checks. | Valid duration. |
| `audit.hardFailOnDrift` | bool | `false` | Initiate graceful gateway shutdown on audit grant drift. | -- |

### OCSF wire format

Lenny's audit events leave the Postgres hot tier as OCSF v1.1.0 records. Wrapping in a CloudEvents envelope is applied only when the audit stream crosses the EventBus. The hash chain (`prev_hash`) is computed over the canonical pre-OCSF tuple; see `spec/11_policy-and-controls.md` §11.7.

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `audit.ocsf.version` | string | `1.1.0` | OCSF schema version emitted by the audit translator. | Must match a supported OCSF release. |
| `audit.ocsf.productName` | string | `lenny` | Value for `metadata.product.name` in every OCSF record. | Non-empty. |
| `audit.ocsf.productVendorName` | string | Required | Value for `metadata.product.vendor_name` (typically the deploying organization). | Non-empty. |
| `audit.ocsf.includeRawRequest` | bool | `false` | Include the original gateway request body in `enrichments[]`. Raises PII/size concerns; enable only for regulated tenants with explicit sign-off. | -- |

---

## Capacity planning configuration

Workload profile assumptions used by scaling formulas. Operators must update these values before relying on formula-derived sizing.

| Field | Type | Default | Unit | Description |
|:------|:-----|:--------|:-----|:------------|
| `capacityPlanning.avgSessionDurationSeconds` | int | 333 | seconds | Average session duration. Drives warm pool claim rate (Little's Law). |
| `capacityPlanning.delegationParticipationRate` | float | 0.05 | fraction | Fraction of sessions performing at least one delegation. |
| `capacityPlanning.avgDelegationsPerDelegatingSession` | int | 10 | integer | Average child delegations per delegating session. |
| `capacityPlanning.avgChildSessionSeconds` | int | 60 | seconds | Average child delegation session duration. |
| `capacityPlanning.avgWorkspaceSizeMB` | int | 100 | MB | Average workspace size for checkpoint bandwidth estimates. |
| `capacityPlanning.sessionIdleFraction` | float | 0.30 | fraction | Fraction of active sessions that are idle (no active LLM call). |

---

## Billing configuration

| Field | Type | Default | Description |
|:------|:-----|:--------|:------------|
| `billing.flushMaxPending` | int | 500 | Maximum in-memory billing events before back-pressure. |
| `billing.redisStreamMaxLen` | int | 50000 (T1/T2), 72000 (T3) | Maximum Redis stream length per tenant. |
| `billing.streamTTLSeconds` | int | 3600 | TTL for billing events in Redis stream. |
| `billing.approvalBacklogAlertMinutes` | int | 60 | Minutes before billing correction backlog alert fires. |

---

## Memory Store configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `memory.maxMemoriesPerUser` | int | 10000 | Maximum number of memories stored per user. When the limit is reached, oldest memories are evicted on new writes. | Must be > 0. |
| `memory.retentionDays` | int | -- | Auto-delete memories older than this many days. When unset, memories are retained indefinitely (subject to `maxMemoriesPerUser`). | Must be > 0 when set. |

---

## Delegation configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `delegation.maxDepth` | int | 10 | Maximum delegation tree depth. Delegations that would exceed this depth are rejected with `BUDGET_EXHAUSTED`. | Must be > 0. |
| `delegation.maxChildrenTotal` | int | 100 | Maximum total children across the entire delegation tree. | Must be > 0. |
| `delegation.maxParallelChildren` | int | 10 | Maximum concurrent children per parent session. | Must be > 0. |
| `delegation.maxTreeMemoryBytes` | int | 104857600 | Memory limit (bytes) for delegation tree metadata tracked in Redis. Prevents runaway trees from consuming excessive coordinator memory. Default: 100 MiB. | Must be > 0. |

---

## Evaluation configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `eval.maxEvalsPerSession` | int | 50 | Maximum `EvalResult` submissions per session. Exceeding this limit returns `EVAL_QUOTA_EXCEEDED`. | Must be > 0. |

---

## Experiment targeting (OpenFeature / OFREP)

Lenny routes external experiment lookups through the OpenFeature Go SDK and the OFREP HTTP provider. Percentage-mode bucketing is built-in and does not require an OpenFeature provider.

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `experimentTargeting.provider` | string | `percentage` | Provider mode. Built-in: `percentage`. External: `ofrep`, `launchdarkly`, `statsig`, `unleash`, `flagd`, `goff`. | One of the listed values. |
| `experimentTargeting.ofrep.endpoint` | string | -- | OFREP provider endpoint. Required when `provider: ofrep`. | Valid URL when required. |
| `experimentTargeting.ofrep.headers` | map[string]string | `{}` | Additional HTTP headers sent on every OFREP request (e.g., auth tokens). | -- |
| `experimentTargeting.ofrep.timeoutMs` | int | 250 | Per-evaluation request timeout. OFREP is on the session-creation hot path; keep this tight. | Must be > 0. |
| `experimentTargeting.ofrep.cacheTTLSeconds` | int | 30 | Local cache TTL for provider responses, per `(flagKey, evaluationContextHash)`. | Must be >= 0. |

---

## Lenny-ops service configuration

`lenny-ops` is the operability control plane that exposes AI-agent-driven diagnostic and remediation endpoints. Deployed as a separate Deployment alongside the gateway.

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `lennyOps.enabled` | bool | `true` | Deploy the `lenny-ops` service. When disabled, operability endpoints under `/v1/ops/*` return `503`. | -- |
| `lennyOps.replicas` | int | 2 | Replica count. `lenny-ops` is stateless; scale for availability, not throughput. | Must be > 0. |
| `lennyOps.image` | string | `lenny/lenny-ops:<lenny-version>` | Container image. Pinned to the main Lenny release. | Valid image reference. |
| `lennyOps.remediationLocks.defaultTTLSeconds` | int | 900 | Default TTL for remediation locks (scaling changes, cordon, pod quarantine). Prevents locks from surviving a crashed operator agent. | Must be > 0. |
| `lennyOps.remediationLocks.maxTTLSeconds` | int | 3600 | Absolute ceiling for remediation-lock TTL. | Must be >= `defaultTTLSeconds`. |
| `lennyOps.scopes.requireExplicit` | bool | `true` | Require scoped tokens (minted via `/v1/oauth/token`) rather than tenant-level OIDC tokens for write operations. | -- |

---

## Capacity tiers

| Parameter | Tier 1 (Starter) | Tier 2 (Growth) | Tier 3 (Scale) | Tier 4 (Platform) |
|:----------|:-----------------|:----------------|:---------------|:------------------|
| Max concurrent sessions | 100 | 1,000 | 10,000 | 100,000 |
| Session creation rate (sustained) | 5/s | 30/s | 200/s | 2,000/s |
| Gateway RPS (all endpoints) | 500 | 5,000 | 50,000 | 500,000 |
| Delegation fan-out (concurrent) | 10 | 100 | 500 | 5,000 |
| Active tenants | 5 | 50 | 500 | 5,000 |
| LLM proxy concurrent streams | 50 | 500 | 5,000 | 50,000 |
| `maxSessionsPerReplica` | 50 | 200 | 400 | 400 |

Tiers 1-3 are achievable with horizontal scaling of v1 components. Tier 4 requires swapping scaling extension interfaces (PostgresPodRegistry, multi-shard StoreRouter, durable EventBus).
