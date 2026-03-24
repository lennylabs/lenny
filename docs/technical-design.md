# Lenny Technical Design

**Status:** Draft
**Date:** 2026-03-23
**Source:** Synthesized from design conversation (chatgpt-conversation.md)

---

## 1. Executive Summary

Lenny is a **Kubernetes-native, runtime-agnostic agent session platform** that provides on-demand, pre-warmed, isolated cloud agent instances to clients. It is not tied to any single agent runtime — it defines a standard contract that any compliant pod binary can implement.

The platform solves a specific problem: teams need cloud-hosted agent sessions (e.g., Claude Code, custom agents) that start fast, run in isolation, support long-lived interactive workflows, and can delegate work recursively — all behind a unified gateway that owns lifecycle, policy, security, and MCP-facing behavior.

### Core Design Principles

1. **Gateway-centric**: All external interaction goes through the gateway. Pods are internal workers, never directly exposed.
2. **Pre-warm everything possible**: Pods are warm before requests arrive. Workspace setup is the only hot-path work.
3. **Pod-local workspace, gateway-owned state**: Pod disk is a cache. Session truth lives in durable stores.
4. **MCP for interaction, custom protocol for infrastructure**: Use MCP where its semantics matter (tasks, elicitation, auth, delegation). Use a custom protocol for lifecycle plumbing.
5. **Recursive delegation as a platform primitive**: Any pod can delegate to other pods through gateway-mediated tools. The gateway enforces scope, budget, and lineage.
6. **Least privilege by default**: No broad credentials in pods. No shared mounts. Gateway-mediated file delivery only.

---

## 2. Goals and Non-Goals

### Goals

- Run agent runtimes on demand in Kubernetes with low startup latency
- Support full SDK-like interactive sessions (streaming, interrupts, follow-up prompts, tool use)
- Support multiple runtime binaries via a standard runtime adapter contract
- Enable recursive orchestration: any pod can delegate to other pods
- Make gateway failure survivable and pod failure recoverable (bounded resume)
- Enforce least-privilege security: no standing credentials in pods, gateway-mediated file delivery
- Scale the gateway horizontally with externalized session state
- Support deployer-selectable isolation profiles (runc, gVisor, Kata)
- Provide rate limiting, token budgets, concurrency controls, and audit logging

### Non-Goals

- Shared RWX storage mounts across agent pods
- Git-based or object-store-based workspace population by pods
- Mid-session file uploads as a default (supported as opt-in capability per runtime)
- Arbitrary late-bound volume mounts on warm pods
- Live migration of in-flight agent processes
- KubeVirt/full VM workloads
- Direct pod-to-pod communication (all delegation goes through gateway)
- Making every internal edge speak MCP

---

## 3. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client / MCP Host                        │
└──────────────────────────────┬──────────────────────────────────┘
                               │ MCP (Streamable HTTP)
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Gateway Edge Replicas                        │
│  ┌──────────┐ ┌─────────────┐ ┌───────────┐ ┌───────────────┐  │
│  │  Auth /   │ │   Policy    │ │  Session   │ │  MCP Fabric   │  │
│  │  OIDC     │ │   Engine    │ │  Router    │ │  (tasks,      │  │
│  │           │ │             │ │            │ │  elicitation,  │  │
│  │           │ │             │ │            │ │  delegation)   │  │
│  └──────────┘ └─────────────┘ └───────────┘ └───────────────┘  │
└────────┬──────────┬──────────────┬──────────────┬───────────────┘
         │          │              │              │
    ┌────▼────┐ ┌───▼────┐ ┌──────▼─────┐ ┌─────▼──────┐
    │Session  │ │Token / │ │  Event /   │ │  Artifact  │
    │Manager  │ │Connec- │ │ Checkpoint │ │   Store    │
    │(Postgres│ │tor Svc │ │   Store    │ │            │
    │+ Redis) │ │        │ │            │ │            │
    └─────────┘ └────────┘ └────────────┘ └────────────┘

         Gateway ←──mTLS──→ Pods (custom control protocol)

┌─────────────────────────────────────────────────────────────────┐
│                    Warm Pool Controller                          │
│   Manages pool lifecycle, scaling, pod state machine            │
└────────┬───────────────┬────────────────┬───────────────────────┘
         │               │                │
    ┌────▼────┐    ┌─────▼─────┐    ┌─────▼─────┐
    │  Pod A  │    │   Pod B   │    │   Pod C   │
    │┌───────┐│    │┌─────────┐│    │┌─────────┐│
    ││Runtime││    ││ Runtime  ││    ││ Runtime  ││
    ││Adapter││    ││ Adapter  ││    ││ Adapter  ││
    │├───────┤│    │├─────────┤│    │├─────────┤│
    ││Agent  ││    ││  Agent   ││    ││  Agent   ││
    ││Binary ││    ││  Binary  ││    ││  Binary  ││
    │└───────┘│    │└─────────┘│    │└─────────┘│
    └─────────┘    └───────────┘    └───────────┘
```

---

## 4. System Components

### 4.1 Edge Gateway Replicas

**Role:** The only externally-facing component. All client interaction enters through the gateway.

**Responsibilities:**

- Authenticate clients (OIDC/OAuth 2.1)
- Expose MCP-facing interfaces (tasks, elicitation, tool surfaces)
- Route sessions to the correct runtime pod
- Proxy long-lived interactive streams (WebSocket / gRPC bidi / Streamable HTTP)
- Run the Policy Engine on every request
- Handle reconnect/reattach after disconnection
- Mediate all file uploads to pods
- Host virtual MCP child interfaces for delegation

**Deployment:**

- Stateless-ish replicas behind ingress/load balancer
- HPA on CPU, memory, active sessions, open streams
- Sticky routing is an optimization, not a correctness requirement
- PodDisruptionBudget to limit simultaneous disruptions

**Key invariant:** A client can land on any gateway replica. Session state is always in durable stores.

### 4.2 Session Manager

**Role:** Source of truth for all session and task metadata.

**Backed by:** Postgres (primary), Redis (hot routing cache, short-lived locks)

**Manages:**

- Session records (id, **tenant_id**, user_id, state, pool, pod assignment, cwd, generation)
- Task records and parent/child lineage (task DAG)
- Retry counters and policy enforcement
- Resume eligibility and window
- Pod-to-session binding
- Delegation lease tracking

**Multi-tenancy:** `tenant_id` is carried on all session, task, quota, and token store records. The platform provides **logical isolation** via tenant_id filtering on all queries and policy enforcement. Namespace-level or cluster-level isolation is a future goal. All quotas, rate limits, and usage reports can be scoped by tenant.

### 4.3 Connector / Token Service

**Role:** Manages two categories of credentials:

1. **MCP tool tokens** — OAuth tokens for external tools (GitHub, Jira, etc.) used via the MCP fabric
2. **LLM provider credentials** — API keys, cloud IAM roles, and service accounts that runtimes need to access their backing LLM (see Section 4.9 for the full credential leasing design)

**Deployment:** Runs as a **separate process** (Deployment) with its own ServiceAccount and KMS access. This is the only component with KMS decrypt permissions for downstream OAuth tokens. Gateway replicas call the Token Service over mTLS — they cannot directly decrypt stored tokens.

**Key rules:**

- Pods never hold downstream OAuth tokens — the Token Service does
- Refresh tokens stored encrypted at rest (envelope encryption via KMS)
- Access tokens short-lived, cached in Redis (encrypted, not plaintext)
- Scoped by user + connector + tenant + environment
- Kubernetes Secrets used only for bootstrap/internal credentials, not per-user OAuth tokens
- Gateway replicas request tokens via the Token Service API; they receive short-lived access tokens, never refresh tokens or KMS keys
- Each gateway replica has a distinct mTLS identity so compromise of one is attributable and revocable independently

### 4.4 Event / Checkpoint Store

**Role:** Enables session recovery and observability.

**Stores:**

- Event cursors / stream offsets
- Session logs and runtime stderr
- Workspace checkpoint references
- Claude session file snapshots
- Resume metadata

### 4.5 Artifact Store

**Role:** Durable storage for workspace files and exports.

**Stores:**

- Original uploaded workspace files (the canonical "initial workspace")
- Sealed workspace bundles
- Exported file subsets for delegation
- Runtime checkpoints
- Large logs and artifacts

**Implementation:** MinIO (S3-compatible). Local disk for development mode. **Never** Postgres for blob storage — the TOAST overhead and vacuum pressure degrade transactional workload performance. See Section 12.5 for retention policy.

### 4.6 Warm Pool Controller

**Role:** Keeps pre-warmed pods available and manages their lifecycle.

**Responsibilities:**

- Maintain warm pods per pool (between `minWarm` and `maxWarm`)
- Manage pod state transitions via CRD status subresource
- Handle pod scaling based on demand (with time-of-day and demand-predictive scaling)
- Surface readiness status to gateway
- Claim/release/drain lifecycle
- Garbage-collect orphaned pods via owner references
- Handle node drain gracefully (checkpoint active sessions before eviction)

**Implementation:** Built with **kubebuilder** (controller-runtime) as a standard Go operator. Defines three custom CRDs:

| CRD            | Purpose                                                                                                                                                                                 |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AgentPool`    | Declares a pool: runtime type, isolation profile, resource class, warm count range, scaling policy                                                                                      |
| `AgentPod`     | Represents a managed agent pod. Owner reference to `AgentPool`. Status subresource carries the authoritative state machine. Enables PDB protection, GC, and structured claim semantics. |
| `AgentSession` | Represents an active session binding. Links a claimed `AgentPod` to session metadata. Owner reference to `AgentPod`.                                                                    |

**Pod claim mechanism:** Gateway replicas submit claims by creating an `AgentSession` resource referencing an idle `AgentPod`. The controller reconciles using **optimistic concurrency** on the `AgentPod` status subresource (`resourceVersion`-based). Only one claim can succeed per pod. This avoids race conditions without requiring the controller to be on the hot path for every claim.

**Leader election:** The controller runs as a Deployment with 2+ replicas using Kubernetes Lease-based leader election (lease duration: 15s, renew deadline: 10s, retry period: 2s). During failover (~15s), existing sessions continue unaffected; only new pod creation and pool scaling pause.

**Scaling:** Pools support `minWarm`, `maxWarm`, and an optional `scalePolicy` with time-of-day schedules or demand-based rules. Low-traffic pools can scale to zero warm pods with documented cold-start latency as fallback.

### 4.7 Runtime Adapter

**Role:** Standardized bridge between the Lenny platform and any pod binary.

**Contract (internal gRPC/HTTP+mTLS API):**

| RPC                  | Description                                                          |
| -------------------- | -------------------------------------------------------------------- |
| `PrepareWorkspace`   | Accept streamed files into staging area                              |
| `FinalizeWorkspace`  | Validate, materialize to `/workspace/current`                        |
| `RunSetup`           | Execute bounded setup commands                                       |
| `StartSession`       | Start the agent runtime with final `cwd` (pod-warm mode)             |
| `ConfigureWorkspace` | Point a pre-connected session at the finalized `cwd` (SDK-warm mode) |
| `Attach`             | Connect client stream to running session                             |
| `Interrupt`          | Interrupt current agent work                                         |
| `Checkpoint`         | Export recoverable session state                                     |
| `ExportPaths`        | Package files for delegation, rebased per export spec (Section 8.7)  |
| `AssignCredentials`  | Push a credential lease to the runtime before session start          |
| `RotateCredentials`  | Push replacement credentials mid-session (fallback/rotation)         |
| `Resume`             | Restore from checkpoint on a replacement pod                         |
| `Terminate`          | Graceful shutdown                                                    |

**Runtime → Gateway events (sent over the control channel):**

| Event                  | Description                                                              |
| ---------------------- | ------------------------------------------------------------------------ |
| `RATE_LIMITED`         | Current credential is rate-limited; request fallback                     |
| `AUTH_EXPIRED`         | Credential lease expired or was rejected by provider                     |
| `PROVIDER_UNAVAILABLE` | Provider endpoint is unreachable                                         |
| `LEASE_REJECTED`       | Runtime cannot use the assigned credential (incompatible provider, etc.) |

**Deployment model:**

- **Default: Sidecar container** communicating with the agent binary over a local Unix socket on a shared `emptyDir` volume. `shareProcessNamespace: false`. This minimizes what third-party binary authors need to implement — just a binary that reads/writes on a well-defined socket protocol.
- **Alternative: Embedded** — first-party binaries can embed the adapter directly and expose the same gRPC contract to the gateway.
- Same external contract either way.

**Health check:** gRPC Health Checking Protocol. The warm pool controller marks a pod as `idle` only after the health check passes.

### 4.8 Gateway Policy Engine

**Role:** Centralized policy evaluation on the request path.

**Physically embedded** in edge gateway replicas (not a separate service). Can be split out later if policy evaluation becomes a scaling or organizational bottleneck.

**Evaluators:**

| Module                      | Scope                                                |
| --------------------------- | ---------------------------------------------------- |
| `AuthEvaluator`             | AuthN/AuthZ, user invalidation                       |
| `QuotaEvaluator`            | Rate limits, token budgets, concurrency limits       |
| `DelegationPolicyEvaluator` | Depth, fan-out, allowed runtimes, budget inheritance |
| `RetryPolicyEvaluator`      | Retry eligibility, resume window                     |
| `AdmissionController`       | Queue/reject/prioritize, circuit breakers            |

**Backs onto:** SessionStore, QuotaStore, TokenStore, UserStateStore, RuntimeRegistry

### 4.9 Credential Leasing Service

**Role:** Supplies runtime pods with the credentials they need to access their backing LLM provider (Anthropic API, AWS Bedrock, Vertex AI, etc.). This is distinct from the MCP tool token flow in Section 4.3 — this is about the runtime's own LLM access, not downstream tool OAuth.

**Design principle:** Runtimes receive **short-lived credential leases**, never long-lived API keys or root credentials. The Token Service owns all durable credential material.

#### Credential Provider

A pluggable interface per LLM provider type. Each provider knows how to mint usable runtime credentials from its source material.

| Provider           | Source Material        | Runtime Receives                                             |
| ------------------ | ---------------------- | ------------------------------------------------------------ |
| `anthropic_direct` | API key                | Short-lived API key or scoped token                          |
| `aws_bedrock`      | IAM role / access keys | Short-lived STS session credentials + region/endpoint config |
| `vertex_ai`        | GCP service account    | Short-lived access token + project/region config             |
| `azure_openai`     | Azure AD / API key     | Short-lived token + endpoint config                          |
| Custom             | Provider-specific      | Provider-specific config bundle                              |

New providers are added by implementing the `CredentialProvider` interface — no gateway changes required.

#### Credential Pool

An admin-managed set of credentials for a given provider. Deployers register pools via configuration:

```yaml
credentialPools:
  - name: claude-direct-prod
    provider: anthropic_direct
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key-1 # K8s Secret reference
      - id: key-2
        secretRef: lenny-system/anthropic-key-2
    assignmentStrategy: least-loaded # least-loaded | round-robin | sticky-until-failure
    maxConcurrentSessions: 10 # per credential
    cooldownOnRateLimit: 60s

  - name: bedrock-us-east-prod
    provider: aws_bedrock
    credentials:
      - id: role-1
        roleArn: arn:aws:iam::123456789:role/lenny-bedrock
        region: us-east-1
    assignmentStrategy: sticky-until-failure
    maxConcurrentSessions: 50
```

#### Credential Lease

A session-scoped assignment from the gateway to a runtime pod:

```json
{
  "leaseId": "cl_abc123",
  "sessionId": "s_xyz789",
  "provider": "anthropic_direct",
  "poolId": "claude-direct-prod",
  "credentialId": "key-1",
  "materializedConfig": {
    "apiKey": "sk-ant-...<short-lived or scoped>",
    "baseUrl": "https://api.anthropic.com"
  },
  "expiresAt": "2026-03-23T15:30:00Z",
  "renewBefore": "2026-03-23T15:25:00Z",
  "fallbackAllowed": true,
  "source": "pool"
}
```

The runtime receives the `materializedConfig` — a provider-specific bundle with everything needed to authenticate. It never receives the pool's root secret or long-lived key.

#### Credential Policy

Attached to a pool or RuntimeType, controls how credentials are selected and managed:

```yaml
credentialPolicy:
  preferredSource: pool # pool | user | prefer-user-then-pool | prefer-pool-then-user
  allowedProviders:
    - anthropic_direct
    - aws_bedrock
  defaultPool: claude-direct-prod
  fallback:
    enabled: true
    order: [claude-direct-prod, bedrock-us-east-prod]
    cooldownOnRateLimit: 60s
    maxRotationsPerSession: 3
    requiresRuntimeRestart: false # per-provider; overridden by runtime capability
  userCredentialMode: elicitation # elicitation | pre-authorized | disabled
```

#### Three Credential Modes

| Mode                     | How It Works                                                                                                                  | Use Case                                              |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| **Pool (admin-managed)** | Gateway picks a credential from a registered pool using the assignment strategy. Runtime gets a lease.                        | Shared team/org API keys, service accounts            |
| **User-scoped**          | User provides their own credential via MCP elicitation or pre-authorized flow. Token Service stores it. Runtime gets a lease. | "Bring your own API key", user-specific Bedrock roles |
| **Hybrid**               | Policy determines precedence. E.g., prefer user credential, fall back to pool on failure.                                     | Flexible enterprise deployments                       |

#### Fallback Flow

```
1. Runtime reports RATE_LIMITED (or AUTH_EXPIRED, PROVIDER_UNAVAILABLE) to gateway
2. Gateway marks current lease as degraded, records cooldown timestamp
3. Gateway evaluates CredentialPolicy fallback chain:
   a. Same provider, different credential from pool?
   b. Different provider allowed by policy?
   c. User credential available as fallback?
4. Gateway issues replacement CredentialLease
5. Gateway pushes new credentials to runtime via RotateCredentials RPC
6. Runtime performs provider rebind (if hotRotation supported) or session restart/resume
```

**Credential health scoring:** For pooled credentials, the gateway tracks per-credential:

- Recent rate-limit events and cooldown expiry
- Auth failure count
- Concurrent session count
- Spend tracking (if provider reports it)

Assignment strategies use this health data to avoid assigning degraded credentials.

#### Security Boundaries

- Long-lived credentials (API keys, IAM role ARNs, service account keys) live **only** in the Token Service and Kubernetes Secrets
- Pods receive **materialized short-lived credentials** (scoped tokens, STS sessions) via the `AssignCredentials` RPC
- Leases are **revocable** — on user invalidation or credential compromise, the gateway revokes the lease and the runtime loses access
- Credential material is **never logged** in audit events, transcripts, or agent output — only lease IDs and provider/pool names are logged
- The `env` allowlist (Section 14) blocks keys like `ANTHROPIC_API_KEY`, `AWS_SECRET_ACCESS_KEY` — credentials flow through the leasing system, not environment variables

---

## 5. Runtime Registry and Pool Model

### 5.1 RuntimeType

Deployers register runtime types with the gateway:

```yaml
name: claude-worker
version: "1.0"
protocolVersion: "1"
image: registry.example.com/lenny/claude-worker@sha256:abc123... # pinned by digest
entrypoint: ["/runtime-adapter", "--binary", "/agent/claude-worker"]
runtimeClassProfile: sandboxed # runc | gvisor | kata
capabilities:
  delegation: false
  elicitation: true
  checkpoint: true
  preConnect: false # true = supports SDK-warm mode
  midSessionUpload: false # true = supports mid-session file uploads
supportedProviders: # LLM providers this runtime can use
  - anthropic_direct
  - aws_bedrock
credentialCapabilities:
  hotRotation: true # can swap credentials mid-session without restart
  requiresRestartOnProviderSwitch: true # needs session restart if provider type changes
mcpFeatures:
  tasks: true
  elicitation: true
delegationFeatures:
  maxDepth: 0
  maxChildren: 0
limits:
  maxSessionAge: 7200
  maxUploadSize: 500MB
  maxSetupTimeout: 300
setupCommandPolicy:
  mode: blocklist # blocklist | allowlist
  blocklist: # denied command prefixes (blocklist mode)
    - curl
    - wget
    - nc
    - ssh
    - scp
  # allowlist:                   # allowed command prefixes (allowlist mode)
  #   - npm ci
  #   - pip install
  #   - make
  #   - chmod
  maxCommands: 10 # max setup commands per session
defaultPoolConfig:
  warmCount: 5
  resourceClass: medium
  egressProfile: restricted
```

### 5.2 Pool Configuration

Each pool is a warmable deployment target for one runtime type + operational profile.

**Pool dimensions:**

- Runtime name and version
- Isolation profile (runc / gvisor / kata)
- Resource class (small / medium / large)
- Upload and setup policy
- Egress/network profile
- Warm count
- Max session age
- Checkpoint cadence

**Example pools:**

- `claude-worker-sandboxed-small`
- `claude-orchestrator-microvm-medium`

**Pool taxonomy strategy:** Not every runtime × isolation × resource combination needs a warm pool. Use a tiered approach:

- **Hot pools** (minWarm > 0): High-traffic combinations that need instant availability
- **Cold pools** (minWarm = 0, maxWarm > 0): Valid combinations that create pods on demand with documented cold-start latency
- **Disallowed combinations**: Invalid or insecure combinations rejected at pool definition time

This prevents the combinatorial explosion of 3 runtimes × 3 isolation × 3 resource = 27 pools each holding idle pods. In practice, most deployments need 3-5 hot pools.

### 5.3 Isolation Profiles

Lenny uses standard Kubernetes `RuntimeClass` for isolation:

| Profile     | RuntimeClass | Use Case                                                                                             | Default? |
| ----------- | ------------ | ---------------------------------------------------------------------------------------------------- | -------- |
| `standard`  | `runc`       | Development/testing only — requires explicit deployer opt-in with security acknowledgment            | No       |
| `sandboxed` | `gvisor`     | **Default for all workloads**. Kernel-level isolation prevents container escape via kernel exploits. | **Yes**  |
| `microvm`   | `kata`       | Higher-risk, semi-trusted, or multi-tenant workloads                                                 | No       |

**Security note:** `runc` provides no protection against kernel exploits. Even trusted developers can introduce malicious dependencies. `gvisor` is the minimum recommended isolation for any workload processing untrusted input (which includes all LLM-generated code execution). Deployers must explicitly opt in to `runc` via a pool configuration flag (`allowStandardIsolation: true`).

Each `RuntimeClass` should define `Pod Overhead` so scheduling accounts for the isolation cost. A `RuntimeProvider` abstraction keeps the door open for future backends (e.g., KubeVirt).

**Image supply chain controls:**

- Images **must** be pinned by digest (not tag) in RuntimeType definitions
- Image signature verification via cosign/Sigstore, enforced by a ValidatingAdmissionWebhook (or OPA/Gatekeeper policy)
- Only images from deployer-configured trusted registries are admitted
- Vulnerability scanning integrated into CI for all runtime images

---

## 6. Warm Pod Model

### 6.1 What a Pre-Warmed Pod Looks Like

A warm pod is either **pod-warm** (default) or **SDK-warm** (optional, per runtime capability):

- Pod scheduled and running
- Selected `RuntimeClass` active (runc/gVisor/Kata)
- Runtime adapter listening and health-checked
- Agent binary dependencies installed and loaded
- `/workspace/current` exists but is empty
- `/workspace/staging` exists for upload staging
- `/sessions` directory present for session files
- Projected service account token mounted (audience: `gateway-internal`)
- No LLM provider credentials assigned (credential lease is assigned at claim time, not warm time)
- No user session bound
- No client files present
- Marked "idle and claimable" via readiness gate

**Pod-warm (default):** The agent process is NOT started. Because workspace contents (including `CLAUDE.md`, `.claude/*`) are unknown until request time, the session must start after workspace finalization. This is the safest and most general mode.

**SDK-warm (optional):** The agent process IS pre-connected and waiting for its first prompt. See below for constraints.

**Security invariant: pods are one-session-only.** After a session completes or fails, the pod is terminated and replaced — never recycled for a different session. This prevents cross-session data leakage through residual workspace files, session transcripts, cached DNS, or runtime memory. If economics later require pod reuse, a mandatory verified scrub protocol must be defined first.

**Optional: SDK-warm mode.** Runtimes that declare `capabilities.preConnect: true` can pre-connect their agent process during the warm phase (before workspace finalization) without sending a prompt. The warm pool controller starts the SDK process after the pod reaches `idle` state, leaving it waiting for its first prompt. This eliminates SDK cold-start latency from the hot path. **Constraint:** SDK-warm mode is only safe when the request does not inject top-level project config files (e.g., `CLAUDE.md`) that must be present at session start. The gateway selects between SDK-warm and pod-warm pods based on the workspace plan contents — if the plan includes top-level config files, a pod-warm (non-pre-connected) pod is used instead.

### 6.2 Pod State Machine

```
Pod-warm path:
  warming ──→ idle ──→ claimed ──→ receiving_uploads ──→ finalizing_workspace
                                                                │
                                                                ▼
                           attached ←── starting_session ←── running_setup

SDK-warm path (preConnect: true):
  warming ──→ sdk_connecting ──→ idle ──→ claimed ──→ receiving_uploads
                                                           │
                                                           ▼
                           attached ←── finalizing_workspace ──→ running_setup
                            │
                    ┌───────┼───────────────┐
                    ▼       ▼               ▼
               completed   failed    resume_pending
                                         │
                                    ┌────┤
                                    ▼    ▼
                               resuming  awaiting_client_action
                                  │              │
                                  ▼              ▼
                               attached    retry_exhausted / expired
                                                 │
                                                 ▼
                                              draining
```

**State storage:** The authoritative state machine lives in the `AgentPod` CRD `.status.phase` and `.status.conditions` fields, backed by Postgres via the controller. **Pod labels are used only for coarse operational states** needed by selectors and monitoring:

| Label               | Values                       | Purpose                                                       |
| ------------------- | ---------------------------- | ------------------------------------------------------------- |
| `lenny.dev/state`   | `idle`, `active`, `draining` | Coarse state for kubectl, monitoring, NetworkPolicy selectors |
| `lenny.dev/pool`    | pool name                    | Pool membership                                               |
| `lenny.dev/runtime` | runtime name                 | Runtime type                                                  |

This avoids the 8-10 label mutations per session that would stress the API server at scale. Detailed state transitions (e.g., `receiving_uploads` → `finalizing_workspace`) are tracked in the CRD status subresource only.

### 6.3 Startup Latency Analysis

**Saved by pre-warming (removed from hot path):**

- Pod scheduling and container creation
- Image pull
- Runtime sandbox initialization
- Volume mounting
- Runtime adapter/binary boot
- Health/readiness checks

**Still on hot path (pod-warm):**

- Pod claim and routing (~ms)
- File upload and workspace materialization (depends on payload size)
- Setup commands (depends on commands)
- Agent session start (depends on runtime)
- First prompt / first token

**Still on hot path (SDK-warm):**

- Pod claim and routing (~ms)
- File upload and workspace materialization (depends on payload size)
- Setup commands (depends on commands)
- First prompt / first token (session start is already done)

**Estimated latency savings:**

- runc with cached image: ~1–3s
- gVisor: ~2–5s
- Kata: ~3–8s
- Cold image pulls: +5–30s avoided

### 6.4 Pod Filesystem Layout

```
/workspace/
  current/      # Agent's actual cwd — populated during workspace finalization
  staging/      # Upload staging area — files land here first
/sessions/      # Session files (e.g., Claude .jsonl)        [tmpfs]
/artifacts/     # Logs, outputs, checkpoints
/tmp/           # tmpfs writable area                        [tmpfs]
```

**Data-at-rest protection:**

- `/sessions/` and `/tmp/` use `emptyDir.medium: Memory` (tmpfs) — data is guaranteed gone when the pod terminates. tmpfs usage counts against pod memory limits and must be accounted for in resource requests.
- `/workspace/` and `/artifacts/` use disk-backed emptyDir. Node-level disk encryption (LUKS/dm-crypt or cloud-provider encrypted volumes) is **required** for production deployments.
- `/dev/shm` is limited to 64MB. `procfs` and `sysfs` are masked/read-only. `shareProcessNamespace: false` when using sidecar containers.
- Combined with the one-session-only invariant, sensitive data never persists on disk after pod termination.

---

## 7. Session Lifecycle

### 7.1 Normal Flow

```
1. Client → Gateway:     CreateSession(runtime, pool, retryPolicy, metadata)
2. Gateway:              Authenticate, authorize, evaluate policy
3. Gateway:              Select pool, claim idle warm pod
4. Gateway → Store:      Persist session metadata (session_id, pod, state)
5. Gateway:              Evaluate CredentialPolicy → assign CredentialLease from pool or user source
6. Gateway → Pod:        AssignCredentials(lease) — push materialized provider config to runtime
7. Gateway → Client:     Return session_id + upload token

8. Client → Gateway:     UploadWorkspaceContent(files, archives)
9. Gateway → Pod:        Stream files over mTLS into /workspace/staging

10. Client → Gateway:    FinalizeWorkspace()
11. Gateway → Pod:       Validate staging, materialize to /workspace/current
12. Pod:                 Run setup commands (bounded, logged)

13. Gateway → Pod:       StartSession(cwd=/workspace/current, options)
                         (SDK-warm pods: skip this step — session already connected,
                          send ConfigureWorkspace to point it at finalized cwd)
14. Pod:                 Start agent binary/runtime session (or resume pre-connected one)

15. Client → Gateway:    AttachSession(session_id)
16. Gateway ↔ Pod:       Bidirectional stream proxy
17. Client ↔ Gateway:    Full interactive session (prompts, responses, tool use,
                         interrupts, elicitation, credential rotation on RATE_LIMITED)

18. Session completes or client disconnects
19. Gateway → Pod:       Seal workspace — export final workspace snapshot to Artifact Store
20. Gateway → Pod:       Terminate
21. Gateway → Store:     Mark session completed, persist final state, record artifact refs
22. Gateway:             Release credential lease back to pool
23. Warm Pool:           Release pod to draining → eventual cleanup
```

**Artifact retention:** Session artifacts (workspace snapshots, logs, transcripts) are retained for a configurable TTL (default: 7 days, deployer-configurable). A background GC job deletes expired artifacts. Clients can extend retention on specific sessions via `extend_artifact_retention(session_id, ttl)`.

**Seal-and-export invariant:** The workspace is always exported to durable storage before the pod is released. If export fails, the pod is held in `draining` state with a retry. This ensures session output is never lost due to pod cleanup.

```

```

### 7.2 Interactive Session Model

Once a session is attached, the client interacts via an **MCP Task** with bidirectional streaming over Streamable HTTP (SSE for server→client, POST for client→server).

**Message types (client → gateway → pod):**

| Message                                            | Description                          |
| -------------------------------------------------- | ------------------------------------ |
| `send_prompt(text, attachments?)`                  | Send a follow-up prompt to the agent |
| `interrupt()`                                      | Interrupt current agent work         |
| `approve_tool_use(tool_call_id)`                   | Approve a pending tool call          |
| `deny_tool_use(tool_call_id, reason?)`             | Deny a pending tool call             |
| `respond_to_elicitation(elicitation_id, response)` | Answer an elicitation request        |

**Message types (pod → gateway → client):**

| Message                                        | Description                                       |
| ---------------------------------------------- | ------------------------------------------------- |
| `agent_text(text, final?)`                     | Streaming text output from the agent              |
| `tool_use_requested(tool_call_id, tool, args)` | Agent wants to call a tool (if approval required) |
| `tool_result(tool_call_id, result)`            | Result of a tool call                             |
| `elicitation_request(elicitation_id, schema)`  | Agent/tool needs user input                       |
| `status_change(state)`                         | Session state transition                          |
| `error(code, message, transient?)`             | Error with classification                         |
| `session_complete(result)`                     | Session finished, result available                |

**Reconnect semantics:** The gateway persists an event cursor per session. On reconnect, the client provides its last-seen cursor and the gateway replays missed events from the EventStore. Events older than the checkpoint window may not be replayable; in that case the client receives a `checkpoint_boundary` marker and the current session state.

### 7.3 Retry and Resume

**Retry policy** is set per session by the client, bounded by deployer caps:

```json
{
  "retryPolicy": {
    "mode": "auto_then_client",
    "maxRetries": 2,
    "retryableFailures": ["pod_evicted", "node_lost", "runtime_crash"],
    "nonRetryableFailures": [
      "workspace_validation_failed",
      "setup_command_failed"
    ],
    "maxSessionAgeSeconds": 7200,
    "maxResumeWindowSeconds": 900
  }
}
```

**Session generations:** Each recovery creates a new generation of the same logical session. The client always sees one session_id.

**Resume flow after pod failure:**

1. Gateway detects session failure
2. Classify failure (retryable vs. non-retryable)
3. If retryable and `retryCount < maxRetries`:
   a. Allocate new warm pod
   b. Recreate same absolute `cwd` path
   c. Replay latest workspace checkpoint
   d. Restore session file to expected path
   e. Resume session (native SDK resume or fresh session with carried state)
4. If retries exhausted → state becomes `awaiting_client_action`

**Client actions after retry exhaustion:**

- Resume anyway (explicit override)
- Start fresh session from latest checkpoint
- Download artifacts / logs / transcript
- Terminate session
- Fork into a new session

### 7.3 Upload Safety

All uploads are gateway-mediated. **Pre-start uploads** are the default. **Mid-session uploads** are supported as an opt-in capability.

**Mid-session uploads:** If the runtime declares `capabilities.midSessionUpload: true` and the deployer policy allows it, clients can call `upload_to_session(session_id, files)` during an active session. Mid-session uploads go through the same gateway validation pipeline (path traversal protection, size limits, hash verification). Files are written directly to `/workspace/current` (no staging step). The runtime adapter receives a `FilesUpdated` notification so the agent can be informed.

**Enforcement rules:**

- All paths relative to workspace root
- Reject `..`, absolute paths, path traversal
- Reject symlinks, hard links, device files, FIFOs, sockets
- Per-file and total session size limits
- Hash verification (optional but recommended)
- Write to staging first, promote only after validation
- Archive extraction is especially strict (zip-slip protection)
- Upload channel closes after workspace finalization

### 7.4 Setup Commands

Run after workspace finalization, before session start.

**Constraints:**

- Time-bounded (configurable timeout per command and total)
- Resource-bounded
- Fully logged (stdout/stderr captured)
- Network **blocked by default** during setup (static NetworkPolicy; no dynamic toggling which would require NET_ADMIN)
- Max commands per session enforced (`setupCommandPolicy.maxCommands`)

**Command policy:** The gateway validates every setup command against the RuntimeType's `setupCommandPolicy` before forwarding to the pod:

| Mode                  | Behavior                                                                                                                                                                                 |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `blocklist` (default) | Commands matching any blocked prefix are rejected. Everything else is allowed. Suitable for most deployments where the sandbox already limits blast radius.                              |
| `allowlist`           | Only commands matching an allowed prefix are permitted. Everything else is rejected. Use for high-security or multi-tenant deployments where setup commands come from untrusted sources. |

Matching is by **command prefix** — e.g., a blocklist entry `curl` blocks `curl`, `curl -s http://...`, etc. The gateway rejects invalid commands before they reach the pod, and the rejection reason is included in the session's setup output.

---

## 8. Recursive Delegation

### 8.1 Design Philosophy

Recursive delegation is a **platform primitive**, not a hardcoded orchestration pattern. The gateway provides the foundational operations; the pod binary decides whether and how to use them.

Every pod runs the same orchestration-capable runtime. Whether it acts as a pure worker, a delegating orchestrator, or both is determined by the agent binary.

### 8.2 Delegation Mechanism

When a parent pod wants to delegate:

1. Parent calls `delegate_task` (a gateway-backed tool injected into its session)
2. Request includes: child runtime name, task spec, file export spec, delegation lease
3. Gateway validates against parent's lease (depth, fan-out, budget)
4. Gateway asks parent runtime to export files matching the export spec (see Section 8.7)
5. Gateway stores exported files durably (rebased to child workspace root)
6. Gateway allocates child pod from specified pool
7. Gateway streams rebased files into child before it starts
8. Child starts with its own local workspace containing the exported files
9. Gateway creates a **virtual MCP child interface** and injects it into parent
10. Parent interacts with child through this virtual interface

**What the parent sees:** A gateway-hosted virtual MCP server with:

- Task status/result
- Elicitation forwarding
- Cancellation
- (Later: richer MCP features as needed)

**What the parent never sees:** Pod addresses, internal endpoints, raw credentials.

### 8.3 Delegation Lease

Every delegating session carries a **delegation lease** that defines its authority:

```json
{
  "maxDepth": 3,
  "maxChildrenTotal": 10,
  "maxParallelChildren": 3,
  "maxTreeSize": 50,
  "maxTokenBudget": 500000,
  "allowedRuntimes": ["claude-worker", "review-agent"],
  "allowedPools": ["*-sandboxed-*"],
  "allowedConnectors": ["github", "jira"],
  "minIsolationProfile": "sandboxed",
  "perChildRetryBudget": 1,
  "perChildMaxAge": 3600,
  "fileExportLimits": { "maxFiles": 100, "maxTotalSize": "100MB" },
  "approvalMode": "policy",
  "cascadeOnFailure": "cancel_all",
  "credentialPropagation": "independent"
}
```

Child leases are always **strictly narrower** than parent leases (depth decremented, budgets reduced).

**Isolation monotonicity:** Children must use an isolation profile **at least as restrictive** as their parent. The enforcement order is: `standard` (runc) < `sandboxed` (gVisor) < `microvm` (Kata). A `sandboxed` parent cannot delegate to a `standard` child. The `minIsolationProfile` field in the lease enforces this, and the gateway validates it before approving any delegation.

**Tree-wide limits:** `maxTreeSize` caps the total number of pods across the entire task tree (all depths), preventing exponential fan-out. `maxTokenBudget` caps total LLM token consumption across the tree.

**Credential propagation:** Controls how child sessions get LLM provider credentials:

| Mode          | Behavior                                                                                                      |
| ------------- | ------------------------------------------------------------------------------------------------------------- |
| `inherit`     | Child uses the same credential pool/source as parent (gateway assigns from same pool)                         |
| `independent` | Child gets its own credential lease based on its own RuntimeType's default policy                             |
| `deny`        | Child receives no LLM credentials (for runtimes that don't need LLM access, e.g., pure file-processing tools) |

### 8.4 Approval Modes

| Mode       | Behavior                                                                  |
| ---------- | ------------------------------------------------------------------------- |
| `policy`   | Gateway auto-approves if request matches lease constraints                |
| `approval` | Gateway pauses parent, surfaces delegation request to client for approval |
| `deny`     | Delegation not permitted                                                  |

### 8.5 Delegation Tools

Injected into every delegation-capable pod:

| Tool                               | Purpose                                                              |
| ---------------------------------- | -------------------------------------------------------------------- |
| `delegate_task(spec)`              | Spawn a child session                                                |
| `await_child(child_id)`            | Wait for child completion, returns `TaskResult`                      |
| `await_children(child_ids, mode)`  | Wait for multiple children (`all`, `any`, or `settled`)              |
| `list_children()`                  | List active children with current status                             |
| `cancel_child(child_id)`           | Cancel a child (cascades to its descendants per policy)              |
| `export_workspace(paths)`          | Internal helper for file export (see Section 8.7 for rebasing rules) |
| `request_lease_extension(request)` | Request more budget mid-session (see Section 8.6)                    |

### 8.6 Lease Extension

A parent agent can request more budget mid-session via the `request_lease_extension` tool. This allows long-running orchestration tasks to adapt when the initial lease proves insufficient.

**Request:**

```json
{
  "extensions": {
    "additionalChildren": 5,
    "additionalTokenBudget": 200000,
    "additionalMaxAge": 1800
  },
  "reason": "Initial estimate insufficient; 3 more modules need review"
}
```

**Extendable fields:** `maxChildrenTotal`, `maxTokenBudget`, `maxTreeSize`, `perChildMaxAge`, `fileExportLimits`. Not extendable: `maxDepth`, `minIsolationProfile`, `allowedRuntimes`, `allowedConnectors` (these are security boundaries, not resource budgets).

**Hard ceilings — extensions can never exceed:**

1. **Deployer caps** on the RuntimeType or pool (e.g., if the deployer sets `maxChildrenTotal: 20`, no extension can push beyond 20)
2. **The parent's own lease limits** — a child requesting an extension cannot exceed what the parent was granted

**Approval modes:**

| Mode                    | Behavior                                                                                                                                                             |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `elicitation` (default) | Gateway surfaces the request to the client via MCP elicitation: "Agent requests 5 more children and 200K more tokens. Approve?" Client can approve, deny, or modify. |
| `auto`                  | Gateway auto-approves if the new totals remain within deployer caps. No client interaction. Opt-in via `delegationLease.extensionApproval: "auto"`.                  |

**Scope:**

- Extensions apply to the requesting session only
- Existing children are **unaffected** — their leases remain as originally granted
- Only new children spawned after the extension benefit from the expanded parent budget

**Audit:** Every extension request is logged with: requesting session, requested amounts, approval mode, outcome (approved/denied/modified), approver (gateway-auto or client), resulting new limits.

### 8.8 File Export Model

When a parent delegates to a child, it specifies which files to export and how they should appear in the child's workspace.

**Export spec (part of `delegate_task`):**

```json
{
  "fileExport": {
    "source": "./exports/export1/*",
    "destPrefix": "input/"
  }
}
```

**Rebasing rule:** The source glob's base path is stripped, and matched files are placed at the child's workspace root (or under `destPrefix` if specified). The child always sees a clean root-relative structure.

**Examples:**

| Parent workspace               | Source glob           | destPrefix     | Child sees              |
| ------------------------------ | --------------------- | -------------- | ----------------------- |
| `./exports/export1/foo.ts`     | `./exports/export1/*` | _(none)_       | `./foo.ts`              |
| `./exports/export1/lib/bar.ts` | `./exports/export1/*` | _(none)_       | `./lib/bar.ts`          |
| `./exports/export1/foo.ts`     | `./exports/export1/*` | `input/`       | `./input/foo.ts`        |
| `./src/auth.ts`                | `./src/*`             | `project/src/` | `./project/src/auth.ts` |
| `./results.json`               | `./results.json`      | _(none)_       | `./results.json`        |

This means the parent controls what slice of its workspace becomes the child's world. The child has no visibility into the parent's broader directory structure.

**Multiple exports:** A `delegate_task` can include multiple export entries. They are applied in order; if paths overlap, later entries overwrite earlier ones.

```json
{
  "fileExport": [
    { "source": "./src/*", "destPrefix": "src/" },
    { "source": "./config/child-config.json", "destPrefix": "" }
  ]
}
```

**Validation:**

- Source globs are resolved inside the parent's `/workspace/current` only — no traversal outside the workspace
- `destPrefix` must be a relative path, no `..`, no absolute paths
- Total exported size is checked against `fileExportLimits` in the delegation lease
- File count is checked against `fileExportLimits.maxFiles`

### 8.9 TaskResult Schema

Returned by `await_child` and `await_children`:

```json
{
  "taskId": "child_abc123",
  "status": "completed",
  "output": {
    "text": "Refactored the auth module and added tests.",
    "structured": { "filesChanged": 5, "testsAdded": 12 },
    "artifactRefs": ["artifact://session_xyz/workspace.tar.gz"]
  },
  "usage": {
    "inputTokens": 15000,
    "outputTokens": 8000,
    "wallClockSeconds": 120
  },
  "error": null
}
```

On failure:

```json
{
  "taskId": "child_abc123",
  "status": "failed",
  "output": null,
  "usage": {
    "inputTokens": 5000,
    "outputTokens": 1000,
    "wallClockSeconds": 30
  },
  "error": {
    "code": "RUNTIME_CRASH",
    "category": "TRANSIENT",
    "message": "Agent process exited with code 137",
    "retriesExhausted": true
  }
}
```

**`await_children` modes:**

- `all` — wait until all children complete or fail. Returns list of `TaskResult`.
- `any` — return as soon as any child completes. Returns the first `TaskResult`.
- `settled` — wait until all children reach a terminal state (completed, failed, or cancelled). Returns list of `TaskResult`.

### 8.10 Task Tree

The gateway maintains a complete task DAG:

```
root_task (client → pod A)
├── child_task_1 (pod A → pod B)
│   └── grandchild_task_1 (pod B → pod C)
└── child_task_2 (pod A → pod D)
```

Each node tracks: session_id, generation, pod, state, lease, budget consumed, failure history.

### 8.11 Delegation Tree Recovery

The gateway tracks the full task tree **independently of pods** in the TaskStore. This enables recovery when any node in the tree fails.

**Parent pod failure with active children:**

1. Gateway detects parent failure
2. Children continue running (they are independent pods with their own sessions)
3. If parent resumes on a new pod:
   a. Gateway re-injects virtual MCP child interfaces for all still-active children
   b. Parent session receives a `children_reattached` event listing current child states
   c. Parent can continue awaiting, canceling, or interacting with children
4. If parent exhausts retries (terminal failure):
   a. Gateway applies the parent's `cascadeOnFailure` policy (see below)

**Cascading behavior (configurable per delegation lease):**

| Policy             | Behavior                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------- |
| `cancel_all`       | Cancel all descendants immediately                                                                              |
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results                               |
| `detach`           | Children become orphaned; results are stored but no parent collects them. Client can query via `get_task_tree`. |

Default: `cancel_all`.

**Child failure notification:**

When a child fails, the gateway injects a `child_failed` event into the parent's session stream with:

- `child_task_id`
- failure classification (transient/permanent)
- error details
- whether retries were exhausted

The parent agent can then decide to: re-spawn a replacement, continue with partial results, or propagate the failure upward.

**Orphan cleanup:** A background job detects task tree nodes whose root session has been terminated and whose `cascadeTimeoutSeconds` has expired. Orphaned children are terminated and their artifacts follow standard retention policy.

---

## 9. MCP Integration

### 9.1 Where MCP Is Used

| Boundary                         | Protocol                | Why                                              |
| -------------------------------- | ----------------------- | ------------------------------------------------ |
| Client ↔ Gateway                 | MCP (Streamable HTTP)   | Tasks, elicitation, auth discovery, tool surface |
| Parent pod ↔ child (via gateway) | MCP (virtual interface) | Delegation, tasks, elicitation forwarding        |
| Gateway ↔ external MCP tools     | MCP                     | Tool invocation, OAuth flows                     |
| Gateway ↔ pod runtime control    | Custom gRPC/HTTP+mTLS   | Lifecycle, uploads, checkpoints — not MCP-like   |

### 9.2 Elicitation Chain

MCP requires hop-by-hop elicitation — servers elicit from their direct client, never skipping levels:

```
External Tool → (elicitation) → Gateway connector
Gateway connector → (elicitation) → Child pod (via virtual MCP)
Child pod → (elicitation) → Parent pod (via virtual MCP)
Parent pod → (elicitation) → Gateway edge
Gateway edge → (elicitation) → Client / Human
```

Response flows back down the same chain. The gateway mediates every hop but **does not erase the hop structure**.

**Elicitation provenance:** The gateway tags every elicitation with metadata before forwarding it up the chain:

| Field              | Description                                                |
| ------------------ | ---------------------------------------------------------- |
| `origin_pod`       | Which pod initiated the elicitation                        |
| `delegation_depth` | How deep in the task tree                                  |
| `origin_runtime`   | Runtime type of the originating pod                        |
| `purpose`          | Stated purpose (e.g., "oauth_login", "user_confirmation")  |
| `connector_id`     | Registered connector ID (for OAuth flows)                  |
| `expected_domain`  | Expected OAuth endpoint domain (for URL-mode elicitations) |

Client UIs **must** display provenance prominently so users can distinguish platform OAuth flows from agent-initiated prompts. URL-mode elicitation URLs are validated against the registered connector's expected OAuth endpoint domain.

**Depth-based restrictions:** Deployers can configure per-pool or global rules limiting which elicitation types are allowed at each delegation depth (e.g., children below depth 2 cannot trigger OAuth flows).

### 9.3 OAuth/OIDC for External Tools

When a nested agent calls an external MCP tool requiring user auth:

1. Pod calls gateway-backed virtual MCP interface for the tool
2. Gateway (acting as MCP client to external tool) receives auth challenge
3. Gateway emits URL-mode elicitation through the chain (hop by hop up to client)
4. User completes OAuth flow
5. Gateway connector receives and stores resulting tokens (encrypted, never in pods)
6. Future calls from pods **authorized for that connector** use gateway-held connector state

**Key invariants:**

- Tokens never transit through pods. The gateway owns all downstream credential state.
- **Connector access is scoped per delegation level.** The delegation lease includes `allowedConnectors` — a list of connector IDs the child is authorized to use. The gateway enforces this before proxying any external tool call. A child cannot use connectors not in its lease, even if tokens exist for them at the root level.

---

## 10. Gateway Internals

### 10.1 Horizontal Scaling

Gateway replicas are stateless proxies over externalized state:

```
                     ┌──────────────────┐
                     │  Load Balancer   │
                     └────┬────┬────┬───┘
                          │    │    │
                     ┌────▼┐ ┌─▼──┐ ┌▼────┐
                     │ GW  │ │ GW │ │ GW  │
                     │ #1  │ │ #2 │ │ #3  │
                     └──┬──┘ └─┬──┘ └──┬──┘
                        │      │       │
                     ┌──▼──────▼───────▼──┐
                     │   Postgres + Redis  │
                     └────────────────────┘
```

**Correctness rule:** Sticky routing is an optimization. A client can reconnect to any replica and resume.

**Per-session coordination:** Redis-based distributed lease ensures only one replica actively coordinates a given session at a time. If that replica dies, another picks up. Falls back to Postgres advisory locks if Redis is unavailable.

**HPA scale-down protection:** Use `behavior.scaleDown.stabilizationWindowSeconds: 300` and a preStop hook that drains active streams before shutdown. Scale-down only removes replicas with zero active streams (custom metric: `lenny_gateway_active_streams`).

### 10.2 Authentication

| Boundary          | Mechanism                                                          |
| ----------------- | ------------------------------------------------------------------ |
| Client → Gateway  | OIDC/OAuth 2.1 (MCP-standard protected resource server)            |
| Automated clients | Service-to-service auth (client credentials grant)                 |
| Gateway ↔ Pod     | mTLS + projected service account token (audience-bound, short TTL) |
| Pod → Gateway     | Projected service account token (audience: `gateway-internal`)     |

**Session capability context:** After authentication, the gateway mints a **signed JWT** (HMAC-SHA256, signed with a gateway-internal key) containing:

- `session_id`, `user_id`, `tenant_id`
- `delegation_depth`, `allowed_operations`
- `expiry` (short-lived, refreshed by gateway on each interaction)

Pods cannot forge or extend this token. The gateway validates the signature on every pod→gateway request.

### 10.3 mTLS PKI

**Certificate authority:** cert-manager with a cluster-internal CA (self-signed issuer or Vault-backed for production). This is the default; a service mesh (Istio/Linkerd) is an optional alternative.

**Certificate lifecycle:**

| Component        | Certificate TTL | SAN Format                                           | Rotation                                                |
| ---------------- | --------------- | ---------------------------------------------------- | ------------------------------------------------------- |
| Gateway replicas | 24h             | DNS: `lenny-gateway.lenny-system.svc`                | cert-manager auto-renewal at 2/3 lifetime               |
| Agent pods       | 4h              | SPIFFE URI: `spiffe://lenny/agent/{pool}/{pod-name}` | cert-manager auto-renewal; pod restart if renewal fails |
| Controller       | 24h             | DNS: `lenny-controller.lenny-system.svc`             | cert-manager auto-renewal                               |

**Pod identity:** Agent pods use SPIFFE-compatible URIs as SANs. The gateway validates the SPIFFE URI against the expected pool/pod on each connection. Each gateway replica gets a distinct certificate so compromise of one replica can be detected and revoked independently.

**Projected SA token:** Configured with `expirationSeconds: 900` (15 minutes). Kubelet auto-refreshes the token before expiry. The gateway validates the `aud: gateway-internal` audience claim on every pod→gateway request. The ServiceAccount bound to agent pods has **zero RBAC bindings** — no Kubernetes API access.

**For long-running sessions (up to 7200s):** SA token refresh is handled transparently by kubelet. Certificate TTL (4h) is longer than max session age (2h), so no mid-session certificate rotation is needed under normal conditions.

### 10.4 Gateway Reliability

**Design principle:** Gateway pod failure causes a broken stream and reconnect, never session loss.

**Mechanisms:**

- All session truth externalized (Postgres)
- Distributed session lease for active coordination
- App-level reconnect: client reconnects with session_id, gateway looks up state, reattaches
- Multiple replicas across zones/nodes
- PodDisruptionBudget limits voluntary disruptions
- Readiness probes remove unhealthy replicas from traffic
- Rolling updates preserve capacity

### 10.5 Upgrade and Rollback Strategy

**Gateway:** Rolling Deployment updates. Schema migrations use expand-contract pattern (add new columns/tables first, deploy code that writes both old and new, then migrate reads, then drop old). Mixed-version replicas must coexist during rollout.

**Warm Pool Controller:** Rolling update with leader election. During leader failover (~15s), existing sessions are unaffected; only new pod creation and scaling pause.

**Runtime adapters and agent binaries:** Versioned pool rotation:

1. Deploy new `AgentPool` CRD with updated image (e.g., `claude-worker-v2-sandboxed-medium`)
2. New warm pods start with new version
3. Old pool's `minWarm` set to 0 — existing pods drain naturally as sessions complete
4. Once old pool is fully drained, remove old `AgentPool` CRD

This avoids in-place image changes and ensures no session is disrupted by an upgrade.

**Token/Connector Service:** Rolling Deployment update. Stateless — reads from Postgres/Redis, so no special migration needed for the service itself. KMS key rotation: re-encrypt stored tokens in a background migration job; old and new envelope keys coexist during rotation.

**Rollback:** All components support rollback by deploying the previous version. Schema migrations are always backward-compatible (expand-contract). Pool rotation is reversed by creating a new pool with the old image.

---

## 11. Policy and Controls

### 11.1 Admission and Fairness

| Control                              | Granularity                             |
| ------------------------------------ | --------------------------------------- |
| Rate limits (requests/min)           | Global, per-user, per-runtime, per-pool |
| Concurrency limits (active sessions) | Global, per-user, per-team, per-runtime |
| Active delegated children            | Per-session, per-user                   |
| Concurrent uploads                   | Per-session, global                     |
| Upload size                          | Per-file, per-session                   |

### 11.2 Budgets and Quotas

| Budget                      | Scope                                                                   |
| --------------------------- | ----------------------------------------------------------------------- |
| Token limits (LLM tokens)   | Per-request, per-session, per-user/window, global/window, per-task-tree |
| Runtime limits (wall clock) | Per-session, per-child                                                  |
| Retry budget                | Per-session (client-set, deployer-capped)                               |
| Delegation budget           | Per-session (depth, fan-out, total children)                            |

**Budget inheritance:** Children inherit strictly narrower budgets. A parent cannot bypass top-level limits by spawning many children.

### 11.3 Timeouts and Cancellation

| Timeout               | Default | Configurable       |
| --------------------- | ------- | ------------------ |
| Request timeout       | 30s     | Yes                |
| Upload timeout        | 300s    | Yes                |
| Setup command timeout | 300s    | Yes                |
| Max session age       | 7200s   | Yes (deployer cap) |
| Max idle time         | 600s    | Yes                |
| Max resume window     | 900s    | Yes                |

Cancellation is first-class: clients and parent agents can cancel sessions/tasks cleanly.

### 11.4 User Invalidation

Three levels:

| Level        | Effect                                                             |
| ------------ | ------------------------------------------------------------------ |
| Soft disable | Deny new sessions                                                  |
| Hard disable | Also block new delegated tasks                                     |
| Full revoke  | Terminate active sessions, invalidate cached auth, deny reconnects |

Propagates through the task tree.

### 11.5 Idempotency

Critical operations support idempotency keys:

- CreateSession
- FinalizeWorkspace
- StartSession
- SpawnChild
- Approve/DenyDelegation
- Resume

Prevents duplicate sessions or children during gateway failover or client retries.

### 11.6 Circuit Breakers

Operators can declare degraded states:

- Runtime X degraded / offline
- Pool Y full
- External connector Z down
- Uploads temporarily disabled
- Delegation depth > N disabled during incident

### 11.7 Audit Logging

Every session/task/delegation produces durable records:

- Who requested it (user_id, tenant_id)
- What runtime, pool, isolation profile
- Parent/child lineage
- Which policies were applied and their results
- Token usage
- Retries, cancellations, failures
- External tool access and auth prompts
- Denial reasons

**Integrity:** Audit tables use **append-only semantics** — the gateway's database role has INSERT but no UPDATE or DELETE grants on audit tables. For high-assurance deployments, audit events should additionally be streamed to an external immutable log (e.g., SIEM, cloud audit service, or append-only object storage).

---

## 12. Storage Architecture

### 12.1 Design Principle

Abstract by **storage role**, not by raw database API. Each store exposes domain operations, not generic CRUD.

### 12.2 Storage Roles

| Role                  | Backend                                   | Purpose                                                                       |
| --------------------- | ----------------------------------------- | ----------------------------------------------------------------------------- |
| `SessionStore`        | Postgres                                  | Sessions, tasks, lineage, retry state                                         |
| `TaskStore`           | Postgres                                  | Task metadata, delegation tree                                                |
| `LeaseStore`          | Redis (fallback: Postgres advisory locks) | Distributed session coordination                                              |
| `TokenStore`          | Postgres (encrypted)                      | Downstream OAuth tokens, refresh tokens                                       |
| `QuotaStore`          | Redis + Postgres                          | Rate limit counters, budget tracking                                          |
| `ArtifactStore`       | MinIO (dev: local disk)                   | Uploaded files, checkpoints, workspace snapshots                              |
| `EventStore`          | Postgres                                  | Audit events, session logs, stream cursors                                    |
| `CredentialPoolStore` | Postgres (encrypted)                      | Credential pool definitions, lease assignments, health scores, cooldown state |

### 12.3 Postgres HA Requirements

**Minimum topology:** Primary + synchronous streaming replica, with automatic failover (e.g., Patroni, CloudNativePG operator, or managed service HA).

**Connection pooling:** PgBouncer (or pgcat) is **required** in front of Postgres. Each gateway replica maintains a connection pool; without pooling, HPA-scaled replicas exhaust Postgres connection limits.

**Read replicas:** Route read-heavy queries (session status, task tree, audit reads, usage reports) to replicas. Write traffic goes to the primary only.

**RPO/RTO targets:**

- RPO: 0 (synchronous replication — no committed transaction lost)
- RTO: < 30s (automatic failover)

**Backups:** Daily base backups + continuous WAL archival. Restore tested quarterly.

### 12.4 Redis HA and Failure Modes

**Minimum topology:** Redis Sentinel (3 sentinels, 1 primary + 1 replica). Redis Cluster if sharding is needed at scale.

**Security:** Redis AUTH (ACLs) and TLS are **required**. Cached access tokens are encrypted before storage in Redis (not stored as plaintext).

**Failure behavior per use case:**

| Use Case                   | On Redis Unavailability                                               |
| -------------------------- | --------------------------------------------------------------------- |
| Rate limit counters        | **Fail open** — allow requests, log degraded state                    |
| Distributed session leases | **Fall back** to Postgres advisory locks (higher latency)             |
| Routing cache              | **Fall back** to Postgres lookup                                      |
| Cached access tokens       | **Re-fetch** from TokenStore (Postgres)                               |
| Quota counters             | **Fail open** for short window, reconcile from Postgres when restored |

### 12.5 Artifact Store

**Backend:** MinIO (S3-compatible). For local development, use local disk with the same interface.

**Do not use Postgres for blob storage.** Workspace checkpoints (up to 500MB) cause TOAST overhead, vacuum pressure, and degrade transactional workload performance.

**Checkpoint retention policy:**

- Keep only the latest 2 checkpoints per active session
- Delete all checkpoints when session terminates and resume window expires
- Background GC job runs every 15 minutes to clean expired artifacts
- Session artifact retention: configurable TTL (default 7 days), extendable per session

### 12.6 Interface Design

Good:

```
SessionStore.claim_session(session_id, gateway_id)
SessionStore.mark_session_attached(session_id, pod_id)
TokenStore.save_refresh_token(user_id, connector_id, encrypted_token)
QuotaStore.increment_token_usage(user_id, window, tokens_used)
```

Bad:

```
Database.query("UPDATE sessions SET ...")
GenericStore.put(key, value)
```

### 12.7 Extensibility

- Define small role-based interfaces
- Start with Postgres + Redis
- Keep migrations/schema explicit and backend-specific
- Add new backends only under real pressure
- Token store module is separate even if initially backed by Postgres (allows future Vault/KMS migration)

---

## 13. Security Model

### 13.1 Pod Security

| Control         | Setting                                                                                           |
| --------------- | ------------------------------------------------------------------------------------------------- |
| User            | Non-root (specific UID/GID)                                                                       |
| Capabilities    | All dropped                                                                                       |
| Root filesystem | Read-only                                                                                         |
| Writable paths  | tmpfs (`/tmp`), workspace, sessions, artifacts                                                    |
| Egress          | Default-deny NetworkPolicy; allow only gateway + required internal services                       |
| Credentials     | No standing credentials; projected SA token + short-lived credential lease only (see Section 4.9) |
| File delivery   | Gateway-mediated only                                                                             |

### 13.2 Network Isolation

**Minimum CNI requirement:** Calico or Cilium (must support NetworkPolicy enforcement including egress rules).

**Default-deny policy (applied to `lenny-agents` namespace):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: lenny-agents
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
```

**Allow gateway-to-pod (applied to all agent pods):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-gateway-ingress
  namespace: lenny-agents
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              lenny.dev/component: system
          podSelector:
            matchLabels:
              lenny.dev/component: gateway
  policyTypes: [Ingress]
```

**Allow pod-to-gateway and DNS (applied to all agent pods):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-pod-egress-base
  namespace: lenny-agents
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
  egress:
    - to: # Gateway
        - namespaceSelector:
            matchLabels:
              lenny.dev/component: system
          podSelector:
            matchLabels:
              lenny.dev/component: gateway
    - to: # DNS (kube-system)
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
  policyTypes: [Egress]
```

**Per-pool egress relaxation:** Pools that need internet access (e.g., for LLM API calls) get additional NetworkPolicy resources allowing egress to specific CIDR ranges or services. These are created by the warm pool controller based on the pool's `egressProfile`.

**DNS exfiltration mitigation:** For high-security profiles, route DNS through a cluster-internal rate-limited DNS proxy that logs and throttles queries.

### 13.3 Credential Flow

**MCP tool credentials (OAuth):**

```
Client authenticates → Gateway validates → Gateway mints session context
                                         → Gateway holds all downstream OAuth tokens
                                         → Pod receives: session context + projected SA token
                                         → Pod never receives: client tokens, downstream OAuth tokens
```

**LLM provider credentials (credential leasing):**

```
Gateway evaluates CredentialPolicy → Token Service selects from pool or user source
                                   → Token Service materializes short-lived credentials
                                   → Gateway pushes CredentialLease to pod via AssignCredentials
                                   → Pod receives: materialized short-lived provider config
                                   → Pod never receives: pool root API keys, IAM role ARNs, long-lived secrets
                                   → On RATE_LIMITED: gateway rotates → pushes new lease via RotateCredentials
                                   → On session end: lease released back to pool
```

**Key distinction:** MCP tool tokens are used by the gateway on behalf of pods (pods never see them). LLM provider credentials are used directly by the runtime process inside the pod — but only as short-lived, scoped, revocable leases.

### 13.4 Upload Security

- Gateway validates and authorizes all uploads
- Pod trusts only the gateway (not arbitrary clients)
- Path traversal protection (reject `..`, absolute paths, symlinks escaping workspace)
- Size limits enforced at gateway and pod
- Staging → validation → promotion pattern
- Archive extraction with zip-slip protection

---

## 14. Workspace Plan Schema

The `WorkspacePlan` is the declarative specification for how a session's workspace should be prepared:

```json
{
  "pool": "claude-worker-sandboxed-medium",
  "isolationProfile": "gvisor",
  "workspacePlan": {
    "sources": [
      {
        "type": "inlineFile",
        "path": "CLAUDE.md",
        "content": "# Project Instructions\n..."
      },
      {
        "type": "inlineFile",
        "path": ".claude/settings.json",
        "content": "{...}"
      },
      {
        "type": "uploadFile",
        "path": "src/main.ts",
        "uploadRef": "upload_abc123"
      },
      {
        "type": "uploadArchive",
        "pathPrefix": ".",
        "uploadRef": "upload_def456",
        "format": "tar.gz"
      },
      {
        "type": "mkdir",
        "path": "output/"
      }
    ],
    "setupCommands": [
      {
        "cmd": "npm ci",
        "timeoutSeconds": 300
      }
    ]
  },
  "env": {
    "NODE_ENV": "production",
    "LOG_LEVEL": "info"
  },
  "labels": {
    "team": "platform",
    "project": "auth-refactor",
    "ticket": "JIRA-1234"
  },
  "runtimeOptions": {
    "settingSources": ["project"],
    "streamingMode": true
  },
  "timeouts": {
    "maxSessionAgeSeconds": 3600,
    "maxIdleSeconds": 300
  },
  "retryPolicy": {
    "mode": "auto_then_client",
    "maxRetries": 2,
    "maxResumeWindowSeconds": 900
  },
  "credentialPolicy": {
    "preferredSource": "pool",
    "provider": "anthropic_direct",
    "pool": "claude-direct-prod",
    "allowPoolFallback": true,
    "allowProviderSwitch": false
  },
  "callbackUrl": "https://ci.example.com/hooks/lenny-complete",
  "delegationLease": {
    "maxDepth": 2,
    "maxChildrenTotal": 5,
    "allowedRuntimes": ["claude-worker"]
  }
}
```

**Field notes:**

- `env`: Key-value environment variables injected into the agent session. Validated against a deployer-configured allowlist (blocks sensitive names like `AWS_SECRET_ACCESS_KEY`).
- `labels`: User-defined metadata for querying and organizing sessions. Not used for internal routing.
- `timeouts`: Per-session overrides, capped by deployer policy. Cannot exceed the RuntimeType's `limits.maxSessionAge`.
- `callbackUrl`: Optional webhook. Gateway POSTs a `SessionComplete` payload when the session reaches a terminal state.
- `credentialPolicy`: Controls how LLM provider credentials are assigned. `preferredSource` can be `pool`, `user`, `prefer-user-then-pool`, or `prefer-pool-then-user`. If omitted, the RuntimeType's default policy is used. See Section 4.9.
- `runtimeOptions`: Passed through to the agent binary. Schema is runtime-specific.

---

## 15. External API Surface

Lenny exposes **two client-facing APIs**: an MCP interface for interactive streaming sessions and delegation, and a REST/HTTP API for lifecycle management, admin operations, and CI/CD integration.

### 15.1 REST API

The REST API covers all non-interactive operations. It is the primary integration point for CI/CD pipelines, admin dashboards, CLIs, and clients in any language.

**Session lifecycle:**

| Method   | Endpoint                      | Description                                                      |
| -------- | ----------------------------- | ---------------------------------------------------------------- |
| `POST`   | `/v1/sessions`                | Create a new session                                             |
| `POST`   | `/v1/sessions/start`          | Create, upload inline files, and start in one call (convenience) |
| `GET`    | `/v1/sessions/{id}`           | Get session status and metadata                                  |
| `GET`    | `/v1/sessions`                | List sessions (filterable by status, runtime, tenant, labels)    |
| `POST`   | `/v1/sessions/{id}/upload`    | Upload workspace files (pre-start or mid-session if enabled)     |
| `POST`   | `/v1/sessions/{id}/finalize`  | Finalize workspace and run setup                                 |
| `POST`   | `/v1/sessions/{id}/start`     | Start the agent runtime                                          |
| `POST`   | `/v1/sessions/{id}/interrupt` | Interrupt current agent work                                     |
| `POST`   | `/v1/sessions/{id}/terminate` | End a session                                                    |
| `POST`   | `/v1/sessions/{id}/resume`    | Explicitly resume after retry exhaustion                         |
| `DELETE` | `/v1/sessions/{id}`           | Terminate and clean up                                           |

**Artifacts and introspection:**

| Method | Endpoint                             | Description                                      |
| ------ | ------------------------------------ | ------------------------------------------------ |
| `GET`  | `/v1/sessions/{id}/artifacts`        | List session artifacts                           |
| `GET`  | `/v1/sessions/{id}/artifacts/{path}` | Download a specific artifact/file                |
| `GET`  | `/v1/sessions/{id}/workspace`        | Download workspace snapshot (tar.gz)             |
| `GET`  | `/v1/sessions/{id}/transcript`       | Get session transcript (paginated)               |
| `GET`  | `/v1/sessions/{id}/logs`             | Get session logs (paginated, streamable via SSE) |
| `GET`  | `/v1/sessions/{id}/setup-output`     | Get setup command stdout/stderr                  |
| `GET`  | `/v1/sessions/{id}/tree`             | Get delegation task tree                         |
| `GET`  | `/v1/sessions/{id}/usage`            | Get token and resource usage                     |

**Async job support:**

| Method | Endpoint                 | Description                                                  |
| ------ | ------------------------ | ------------------------------------------------------------ |
| `POST` | `/v1/sessions/start`     | Accepts optional `callbackUrl` for completion notification   |
| `POST` | `/v1/sessions/{id}/send` | Send a prompt (non-interactive, returns when agent responds) |

**Admin:**

| Method | Endpoint       | Description                                       |
| ------ | -------------- | ------------------------------------------------- |
| `GET`  | `/v1/runtimes` | List registered runtime types                     |
| `GET`  | `/v1/pools`    | List pools and warm pod counts                    |
| `GET`  | `/v1/usage`    | Usage report (filterable by tenant, user, window) |

### 15.2 MCP API

The MCP interface is for **interactive streaming sessions** and **recursive delegation**. It exposes the gateway as an MCP server over Streamable HTTP.

**MCP tools:**

| Tool                       | Description                                          |
| -------------------------- | ---------------------------------------------------- |
| `create_session`           | Create a new agent session                           |
| `create_and_start_session` | Create, upload inline files, and start in one call   |
| `upload_files`             | Upload workspace files                               |
| `finalize_workspace`       | Seal workspace, run setup                            |
| `start_session`            | Start the agent runtime                              |
| `attach_session`           | Attach to a running session (returns streaming task) |
| `send_prompt`              | Send a follow-up prompt into an attached session     |
| `interrupt_session`        | Interrupt current agent work                         |
| `get_session_status`       | Query session state                                  |
| `get_task_tree`            | Get delegation tree for a session                    |
| `get_session_logs`         | Get session logs (paginated)                         |
| `get_token_usage`          | Get token usage for a session                        |
| `list_artifacts`           | List artifacts for a session                         |
| `download_artifact`        | Download a specific artifact                         |
| `terminate_session`        | End a session                                        |
| `resume_session`           | Explicitly resume after retry exhaustion             |
| `list_sessions`            | List active/recent sessions (filterable)             |

**MCP features used:**

- Tasks (for long-running session lifecycle and delegation)
- Elicitation (for user prompts, auth flows)
- Streamable HTTP transport

### 15.3 Internal Control API (Custom Protocol)

Gateway ↔ Pod communication over gRPC + mTLS. See Section 4.7 (Runtime Adapter) for the full RPC surface. Protobuf service definitions will be published as a separate runtime adapter specification (see Section 15.4).

### 15.4 Runtime Adapter Specification

The runtime adapter contract will be published as a **standalone specification** with:

- Protobuf `.proto` service and message definitions
- Error code enum with categories (transient, permanent, policy)
- Streaming message type definitions for `Attach` bidirectional stream
- Version negotiation protocol (adapter advertises capabilities at startup; gateway selects compatible protocol version)
- Health check contract (gRPC Health Checking Protocol)
- Reference implementation in Go

This is the primary document for community runtime adapter authors.

---

## 16. Observability

### 16.1 Metrics

| Metric                                                                           | Type            |
| -------------------------------------------------------------------------------- | --------------- |
| Active sessions (by runtime, pool, state, tenant)                                | Gauge           |
| Warm pods available (by pool)                                                    | Gauge           |
| Stale warm pods (idle beyond threshold, by pool)                                 | Gauge           |
| Session creation latency (phases)                                                | Histogram       |
| Time-to-claim (session request to pod claimed)                                   | Histogram       |
| Pod state transition durations (per state)                                       | Histogram       |
| Upload bytes/second and queue depth                                              | Counter + Gauge |
| Token usage (by user, runtime, tenant)                                           | Counter         |
| Retry count (by failure classification)                                          | Counter         |
| Resume success/failure rate                                                      | Counter         |
| Delegation depth distribution                                                    | Histogram       |
| Delegation tree size distribution                                                | Histogram       |
| Gateway replica count                                                            | Gauge           |
| Gateway active streams (per replica)                                             | Gauge           |
| Policy denials (by reason, tenant)                                               | Counter         |
| Checkpoint size and duration                                                     | Histogram       |
| Postgres connection pool utilization (per replica)                               | Gauge           |
| Redis memory usage and eviction rate                                             | Gauge + Counter |
| mTLS handshake latency (gateway-to-pod)                                          | Histogram       |
| Credential lease assignments (by provider, pool, source)                         | Counter         |
| Credential rotations (by reason: rate_limit, auth_expired, provider_unavailable) | Counter         |
| Credential pool utilization (active leases / total credentials, by pool)         | Gauge           |
| Credential pool health (credentials in cooldown, by pool)                        | Gauge           |
| Credential lease duration                                                        | Histogram       |

### 16.2 Key Latency Breakpoints

Instrument four timestamps per session:

1. Pod claimed
2. Workspace prep done
3. Session ready
4. First event/token emitted

This lets operators identify whether bottlenecks are in pod allocation, file upload, session start, or the agent runtime itself.

### 16.3 Distributed Tracing

**Mandatory:** OpenTelemetry trace ID propagation through the entire delegation tree.

**Trace context flows through:**

- Client → Gateway (HTTP headers)
- Gateway → Pod (gRPC metadata)
- Pod → Gateway (delegation tool calls carry parent trace context)
- Gateway → Child Pod (inherited trace context)
- Gateway → External MCP tools (HTTP headers)

**Span boundaries (instrumented):**

| Span                         | Component                             |
| ---------------------------- | ------------------------------------- |
| `session.create`             | Gateway                               |
| `session.claim_pod`          | Controller                            |
| `session.upload`             | Gateway + Pod                         |
| `session.finalize_workspace` | Pod                                   |
| `session.run_setup`          | Pod                                   |
| `session.start`              | Pod                                   |
| `session.prompt`             | Gateway + Pod (per prompt)            |
| `session.tool_call`          | Pod (per tool invocation)             |
| `delegation.spawn_child`     | Gateway                               |
| `delegation.await_child`     | Gateway + Parent Pod                  |
| `delegation.export_files`    | Gateway + Parent Pod                  |
| `mcp.external_tool_call`     | Gateway connector                     |
| `mcp.elicitation`            | Full chain (each hop is a child span) |
| `session.checkpoint`         | Gateway + Pod                         |
| `session.seal_and_export`    | Gateway + Pod                         |

**Error codes:** Structured error taxonomy with categories:

- `TRANSIENT` — retryable (pod crash, network timeout)
- `PERMANENT` — not retryable (invalid workspace, policy denial)
- `POLICY` — denied by policy engine (quota exceeded, unauthorized runtime)
- `UPSTREAM` — external dependency failure (MCP tool error, auth failure)

### 16.4 Logging

- Structured JSON logs from gateway, token service, pool controller, and runtime adapter
- Correlation fields in every log line: `session_id`, `tenant_id`, `trace_id`, `span_id`
- Setup command stdout/stderr captured and stored in EventStore
- Audit events for all policy decisions
- Error events include structured error codes (TRANSIENT/PERMANENT/POLICY/UPSTREAM)

---

## 17. Deployment Topology

### 17.1 Kubernetes Resources

| Component               | K8s Resource                              | Notes                                                |
| ----------------------- | ----------------------------------------- | ---------------------------------------------------- |
| Gateway                 | Deployment + Service + Ingress            | HPA, PDB, multi-zone, topology spread                |
| Token/Connector Service | Deployment + Service                      | Separate SA with KMS access                          |
| Warm Pool Controller    | Deployment (2+ replicas, leader election) | Manages `AgentPool`, `AgentPod`, `AgentSession` CRDs |
| Agent Pods              | Pods owned by `AgentPod` CRD              | RuntimeClass per pool, PDB via CRD                   |
| Postgres                | StatefulSet or managed service            | HA: primary + sync replica, PgBouncer required       |
| Redis                   | StatefulSet or managed service            | HA: Sentinel (3 nodes), TLS + AUTH required          |
| MinIO                   | StatefulSet or managed service            | Artifact/checkpoint storage                          |

### 17.2 Namespace Layout

```
lenny-system/         # Gateway, token service, controller, stores
lenny-agents/         # Agent pods (gVisor/runc isolation boundary)
lenny-agents-kata/    # Kata pods (separate node pool with dedicated hardware)
```

**Pod Security Standards:** The `lenny-agents` and `lenny-agents-kata` namespaces enforce **Restricted** Pod Security Standards at the namespace level. This provides an independent validation layer — even a compromised warm pool controller cannot create pods that violate the security baseline (e.g., privileged containers, host mounts, escalated capabilities). Enforced via namespace labels (`pod-security.kubernetes.io/enforce: restricted`) and optionally backed by OPA/Gatekeeper or Kyverno policies.

**Node isolation:** Kata (`microvm`) pods should run on dedicated node pools with taints/tolerations to ensure they do not share nodes with `standard` (runc) pods. A kernel compromise via an runc escape on a shared node would put co-located Kata pods at risk.

### 17.3 Disaster Recovery

**RPO/RTO targets:**

| Component                        | RPO                               | RTO                           |
| -------------------------------- | --------------------------------- | ----------------------------- |
| Postgres (session state, tokens) | 0 (sync replication)              | < 30s (auto failover)         |
| Redis (cache, leases)            | Ephemeral — rebuild from Postgres | < 15s (Sentinel failover)     |
| MinIO (artifacts, checkpoints)   | Last backup (daily)               | < 5 min (restore from backup) |

**Cross-zone requirements:**

- Postgres: primary and sync replica in different availability zones
- Redis: Sentinel nodes spread across zones
- Gateway: replicas spread via topology spread constraints
- Agent pods: spread via pool-level topology constraints

**Backup schedule:**

- Postgres: continuous WAL archival + daily base backups to object storage
- MinIO: daily bucket replication or backup
- Restore tested quarterly

**Zone failure blast radius:** Loss of one zone causes:

- Gateway: surviving replicas absorb traffic (PDB ensures minimum availability)
- Postgres: automatic failover to sync replica in another zone
- Agent pods: sessions on lost pods enter retry flow; warm pods in surviving zones serve new requests
- No data loss for committed transactions

### 17.4 Local Development Mode (`lenny-dev`)

For development, testing, and runtime adapter authoring, Lenny provides a **local development mode** that runs without Kubernetes:

```
docker compose up   # Starts: gateway, controller-sim, single agent pod, Postgres, Redis, MinIO
```

**Components in dev mode:**

- Gateway: single replica, no HPA, no mTLS (plain HTTP)
- Controller simulator: manages a single "pod" (Docker container) instead of CRDs
- Stores: real Postgres + Redis (lightweight containers), or optional SQLite + in-memory mode for zero-deps testing
- MinIO: single container for artifact storage
- Agent pod: single Docker container with runtime adapter + agent binary

**Use cases:**

- Runtime adapter authors testing their adapter against the gateway contract
- Agent binary authors testing their binary with the platform
- Lenny core developers iterating on gateway/controller logic
- CI integration tests

### 17.5 Cloud Portability

The design avoids baking in cloud-specific assumptions:

- Storage backends are pluggable
- Network policies are standard Kubernetes
- RuntimeClass works with any conformant runtime
- No cloud-specific CRDs required

---

## 18. Build Sequence

| Phase | Components                                                                                        | Milestone                                              |
| ----- | ------------------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| 1     | Core types, interfaces, storage abstractions, CRD definitions (AgentPool, AgentPod, AgentSession) | Foundation                                             |
| 2     | Runtime adapter contract (.proto files) + reference Go implementation                             | Can start an agent session manually                    |
| 3     | Warm pool controller (kubebuilder operator, basic pool management)                                | Pods stay warm and get claimed via CRD                 |
| 4     | Session manager + session lifecycle + REST API                                                    | Full create → upload → attach → complete flow          |
| 5     | Gateway edge (auth, routing, upload proxy, MCP server)                                            | Clients can create and use sessions via REST and MCP   |
| 6     | Interactive session model (streaming, prompts, reconnect with event replay)                       | Full interactive sessions work                         |
| 7     | Policy engine (rate limits, auth, budgets, tenant_id)                                             | Production-grade admission                             |
| 8     | Checkpoint/resume + artifact seal-and-export                                                      | Sessions survive pod failure; artifacts retrievable    |
| 9     | Delegation primitives (TaskResult, await_children, tree recovery)                                 | Parent → child task flow with partial failure handling |
| 10    | MCP fabric (virtual child interfaces, elicitation chain with provenance)                          | Recursive delegation with MCP semantics                |
| 11    | Credential leasing (CredentialProvider interface, pool management, lease assignment, rotation)    | Runtimes can access LLM providers                      |
| 12    | Token/Connector service (separate deployment, KMS, OAuth flows, credential pools)                 | External MCP tool auth + LLM credential management     |
| 13    | Audit logging, OpenTelemetry tracing, observability                                               | Operational readiness                                  |
| 14    | Hardening (gVisor/Kata, NetworkPolicy manifests, image signing, egress lockdown)                  | Security profiles                                      |
| 15    | Local development mode (docker-compose)                                                           | Community onboarding                                   |

---

## 19. Resolved Decisions

These were open questions from the initial design, now resolved:

| #   | Question                         | Decision                                                                                                                                                                                                                                   |
| --- | -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | Checkpointing strategy           | Full snapshots with size cap. Keep latest 2 per session. Incrementals deferred.                                                                                                                                                            |
| 2   | Agent binary packaging           | Sidecar container with local Unix socket. `shareProcessNamespace: false`. Lower barrier for third-party authors.                                                                                                                           |
| 3   | Multi-tenancy                    | `tenant_id` in all data models. Logical isolation via filtering. Namespace-level isolation deferred.                                                                                                                                       |
| 4   | Controller framework             | kubebuilder (controller-runtime). Standard Go operator pattern.                                                                                                                                                                            |
| 5   | Service mesh dependency          | cert-manager + manual mTLS. No Istio/Linkerd requirement (fewer deps for community adoption).                                                                                                                                              |
| 6   | Default isolation                | gVisor (`sandboxed`) is the default. `runc` requires explicit deployer opt-in.                                                                                                                                                             |
| 7   | Blob storage                     | MinIO. Never Postgres for blobs.                                                                                                                                                                                                           |
| 8   | Delegation file export structure | Source glob base path is stripped; files are rebased to child workspace root. Optional `destPrefix` prepends a path. Parent controls the slice; child sees clean root-relative structure. See Section 8.7.                                 |
| 9   | Inter-child data passing         | No first-class `pipe_artifacts` operation. Parents use the existing export→re-upload flow via `delegate_task` file exports. Simpler; avoids a new gateway primitive.                                                                       |
| 10  | Setup command policy             | Support both blocklist (default) and allowlist modes, deployer's choice per RuntimeType. Blocklist suits most sandboxed deployments; allowlist for high-security/multi-tenant. See Section 7.4.                                            |
| 11  | Billing/showback                 | Track per-session, per-token, and per-minute usage. Expose via REST API (`GET /v1/usage`). Filterable by tenant, user, runtime, and time window.                                                                                           |
| 12  | Session forking                  | Not supported. The `fork_session` concept is dropped. Clients can achieve similar results by creating a new session with the previous session's workspace snapshot as input.                                                               |
| 13  | Lease extension                  | Supported. Parents can request more budget mid-session via `request_lease_extension`. Default approval via client elicitation; auto-approval opt-in. Extensions can never exceed deployer caps or the parent's own lease. See Section 8.6. |

## 20. Open Questions

All open questions have been resolved. See Section 19 for decisions.
