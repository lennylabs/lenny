## 5. Runtime Registry and Pool Model

### 5.1 Runtime

`Runtime` and `SessionTemplate` are unified into a single **`Runtime`** concept. A Runtime is either **standalone** (has an `image`) or **derived** (has a `baseRuntime` reference). All runtimes are registered via the admin API as static configuration.

Every Runtime has a **`type`** field and an optional **`capabilities`** field:

```yaml
type: agent # agent (default) | mcp
capabilities:
  interaction: one_shot # one_shot | multi_turn
  injection:
    supported: true # default: false
    modes: [immediate, queued]
  preConnect: false # default: false; set true to enable SDK-warm mode (see Section 6.1)
sdkWarmBlockingPaths:  # glob patterns; demotion triggered if any uploaded file matches
  - CLAUDE.md          # default list — override to restrict or expand
  - .claude/*
```

**`capabilities.preConnect`** — when `true`, the warm pool controller pre-connects the agent SDK process during the warm phase. All pods in the pool are SDK-warm. The runtime's adapter must implement `DemoteSDK`. Default: `false`.

**`sdkWarmBlockingPaths`** — list of glob patterns matched against relative workspace paths (see matching contract in [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)). If any uploaded file (including `workspaceDefaults` files) matches a pattern, the gateway demotes the SDK-warm pod before use. Default: `["CLAUDE.md", ".claude/*"]`. The field is only meaningful when `capabilities.preConnect: true`; ignored otherwise. Patterns follow Go `path.Match` extended with `**` (see matching contract in [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).

**`type: agent`** — participates in Lenny's task lifecycle. Receives tasks via stdin `{type: "message"}`, has sessions, workspace, delegation, elicitation, multi-turn dialog. Callable via `lenny/delegate_task`.

**`type: mcp`** — hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit). No task lifecycle. Runtime binary is oblivious to Lenny. No `capabilities` field.

**`capabilities.interaction: multi_turn`** — the runtime supports the `lenny/request_input` → response cycle and multiple `{type: "message"}` deliveries over the lifetime of a task. Multi-turn requires `capabilities.injection.supported: true` — the gateway enforces this at runtime registration. A multi-turn runtime that doesn't accept injections is incoherent.

**`capabilities.interaction: one_shot`** — the runtime consumes the initial `{type: "message"}`, produces exactly one `{type: "response"}` carrying the final result, and the task ends. At Standard level and above, a `one_shot` runtime may use `lenny/request_input` once (for a single clarification); a second call returns a gateway error. Basic-level `one_shot` runtimes cannot request clarification (`lenny/request_input` requires the platform MCP server) and must produce their response based solely on the initial input. **Timeout behavior:** If a `one_shot` runtime's single `request_input` call times out (`maxRequestInputWaitSeconds` fires), the gateway delivers a `REQUEST_INPUT_TIMEOUT` tool-call error and the runtime transitions back to `running`. The runtime MUST then produce a best-effort response without the requested clarification or fail with a structured error (`{ "code": "INSUFFICIENT_INPUT" }`) explaining that the required clarification was not received. The gateway does not auto-fail the task — the runtime is responsible for deciding whether it can produce a useful response without the clarification.

**`capabilities.injection`** declares whether the runtime supports mid-session message delivery. Default: `supported: false`. Gateway rejects injection attempts against unsupported sessions at the API level before they reach the adapter.

**`integrationLevel`** — optional enum, one of `basic`, `standard`, `full`. Author-declared intent about which level of the Runtime Adapter Specification ([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels)) the runtime binary implements. Only meaningful on `type: agent` runtimes — must not be set on `type: mcp` (gateway rejects with `INVALID_RUNTIME: integrationLevel is only valid on type: agent runtimes`). Default when absent: `basic`. Ordering: `basic < standard < full`.

The field is consumed by three users:

1. **`lenny runtime validate`** ([§15.4.6](15_external-api-surface.md#1546-conformance-test-suite)) reads the declared value from `runtime.yaml` to select the conformance test categories to execute (each higher level inherits every test category from the levels below it). The validator additionally probes the running runtime image for its **observed** level (does it open the lifecycle channel? does it present a valid MCP nonce handshake?) and compares observed against declared.
2. **Admission at registration** — the gateway records the declared value alongside the Runtime definition and emits a `runtime.integrationLevel.declared` audit field. At the point where the runtime actually registers a pool and the adapter completes its first `lifecycle_capabilities` / `lifecycle_support` exchange ([§4.7](04_system-components.md#47-runtime-adapter)), the gateway compares the declared level against the observed level from the handshake:
   - `observed < declared` — gateway rejects the first session assignment with `RUNTIME_LEVEL_UNDERPERFORMS` and logs a structured error. The runtime has claimed more than it delivers; continuing would silently degrade features callers expect (checkpoint, clean interrupt, credential rotation).
   - `observed > declared` — gateway accepts the registration and emits a `runtime.integrationLevel.underdeclared` warning so the author can raise the declared level in a future release.
   - `observed == declared` — gateway accepts without annotation.
3. **Tooling hints** — the declared level feeds the `Level` column in the reference runtime catalog ([§26.1](26_reference-runtime-catalog.md#261-catalog-overview)), README badges produced by `lenny runtime init` ([§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding)), and the `integrationLevel` field exposed in runtime listing APIs.

The `lifecycle_support` handshake from [§4.7](04_system-components.md#47-runtime-adapter) remains the **runtime source of truth** — a runtime with no lifecycle channel is observationally Basic (or Standard, if it still opens MCP) regardless of what `runtime.yaml` declares, and the gateway treats it accordingly for capability selection (credential rotation strategy, checkpoint strategy, interrupt delivery). The declared `integrationLevel` is an author-side commitment that tooling and admission use to catch drift between claim and behaviour; it never grants features the runtime does not actually implement.

**Capabilities are customizable per tenant**, with the platform defaults as described above.

**Labels are required from v1** — primary mechanism for environment `runtimeSelector` and `connectorSelector` matching (see [Section 10.6](10_gateway-internals.md#106-environment-resource-and-rbac-model)).

#### Standalone Runtime

```yaml
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
integrationLevel: full # basic | standard | full — optional; defaults to basic
capabilities:
  interaction: one_shot # one_shot | multi_turn
  injection:
    supported: true
    modes: [immediate, queued]
executionMode: session
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy
allowSelfRecursion: false  # when true AND platform+DelegationPolicy agree, this runtime's (runtime_name, pool_name) identity may repeat in its own delegation lineage (self-decomposition pattern); see [Section 8.2](08_recursive-delegation.md#82-delegation-mechanism) cycle detection
supportedProviders:
  - anthropic_direct
  - aws_bedrock
credentialCapabilities:
  hotRotation: true
  proxyDialect: [openai, anthropic]   # dialects the runtime's SDK speaks to Lenny's LLM proxy ([§4.9](04_system-components.md#49-credential-leasing-service)). Required when any pool bound to this Runtime uses deliveryMode: proxy. Set to empty ([]) for runtimes that only support direct mode.
limits:
  maxSessionAge: 7200
  maxUploadSize: 500MB
  maxRequestInputWaitSeconds: 600  # inter-agent lenny/request_input timeout; see §11.3
setupCommandPolicy:
  mode: allowlist
  shell: false
  allowlist:
    - npm ci
    - pip install
    - make
    - chmod
  maxCommands: 10
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail # fail | warn
runtimeOptionsSchema:
  type: object
  properties:
    model: { type: string }
    temperature: { type: number, minimum: 0, maximum: 2 }
  additionalProperties: false
defaultPoolConfig:
  warmCount: 5
  resourceClass: medium
  egressProfile: restricted
sharedAssets:  # files populated into /workspace/shared/ (read-only) during pod init; only meaningful when sessionPolicy.maxConcurrentSessions > 1
  - type: artifact
    ref: "lenny-blob://tenant_acme/shared/models.tar.gz"
    destPath: models/
  - type: inline
    path: config.json
    content: '{"version": 1}'
labels:
  team: platform
  approved: "true"
```

#### Derived Runtime

A derived runtime references a `baseRuntime` and customizes workspace, setup, agent interface, and policy — but cannot override security-critical fields from the base.

```yaml
name: research-pipeline
baseRuntime: langgraph-runtime
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
  inputModes:
    - type: "text/plain"
    - type: "application/json"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
delegationPolicyRef: research-policy
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public # internal | tenant | public
    value: "..."
sessionPolicy:
  recycle:
    enabled: true
    acknowledgeBestEffortScrub: true # required when enabled: true
    allowCrossTenantReuse: false # only valid when isolationProfile: microvm
    maxSessionsPerPod: 50 # required when enabled — pod retired after serving this many sessions
    maxPodUptimeSeconds: 86400 # optional — pod retired after this uptime (seconds)
    maxScrubFailures: 3
    onScrubFailure: warn
  cleanupCommands:
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
labels:
  team: research
  approved: "true"
minPlatformVersion: "1.4.0" # optional; gateway rejects registration if platform version < this
```

#### Inheritance Rules

**Never overridable on derived runtime:** `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, `allowedResourceClasses`, `integrationLevel`. (`allowStandardIsolation` is a pool configuration flag ([Section 5.3](#53-isolation-profiles)), not a Runtime definition field — it is not subject to inheritance.)

**Independently configurable on derived runtime:** Pool settings, `workspaceDefaults`, `setupCommands`, `setupPolicy.timeoutSeconds` (gateway takes maximum of base and derived), `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, `sessionPolicy`.

**Base runtime mutability:** `image` and `name` immutable via API. All other fields mutable with impact validation — changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

#### Normative Merge Algorithm

When the gateway resolves a derived runtime, it applies the following per-field merge rules. The result is the **effective runtime** used for pod scheduling and session validation.

| Field | Merge behavior | Notes |
|---|---|---|
| `name` | N/A — derived runtime has its own `name` | |
| `image` | **Inherited** — derived may not set | Derived runtimes never carry their own image |
| `type` | **Prohibited** — derived may not set | Must match base; gateway rejects registration if present |
| `executionMode` | **Prohibited** — derived may not set | |
| `isolationProfile` | **Prohibited** — derived may not set | |
| `allowedResourceClasses` | **Prohibited** — derived may not set | Must be a subset of base; pool config constrains further |
| `capabilities.interaction` | **Prohibited** — derived may not set | |
| `capabilities.injection` | **Prohibited** — derived may not set | |
| `integrationLevel` | **Inherited** — derived may not set | Level is a property of the runtime binary image, which is inherited; derived runtimes cannot re-declare a different level. Gateway rejects registration with `INVALID_DERIVED_RUNTIME: integrationLevel is prohibited on derived runtimes` if present. |
| `supportedProviders` | **Override** — derived value replaces base if set; otherwise base value applies | Derived may restrict but not expand beyond base |
| `credentialCapabilities` | **Override** — derived value replaces base if set | |
| `limits` | **Override** — derived value replaces base if set; otherwise base value applies | |
| `setupCommandPolicy` | **Override** — derived value replaces base if set | Derived may restrict allowlist; gateway enforces at pod startup |
| `setupPolicy.timeoutSeconds` | **Maximum** — gateway uses `max(base, derived)` | Neither can be zero if the other is set |
| `setupPolicy.onTimeout` | **Override** — derived value replaces base if set | |
| `workspaceDefaults` | **Append** — derived files appended to base; conflicting paths replaced by derived | Order: base defaults → derived defaults → client uploads |
| `workspaceDefaults.setupCommands` | **Append** — derived commands appended after base commands | Execution order: base setup commands → derived setup commands → client-provided setup commands ([Section 14](14_workspace-plan-schema.md)). Per-command `timeoutSeconds` is preserved from each source. |
| `runtimeOptionsSchema` | **Override** — derived value replaces base if set | Derived schema MAY only reference property names present in the base schema's `properties` map; introducing a property name absent from the base is forbidden. Gateway validates at derived-runtime registration and rejects with `INVALID_DERIVED_RUNTIME: runtimeOptionsSchema declares forbidden property '<name>'` if the constraint is violated. |
| `defaultPoolConfig` | **Override** — derived value replaces base if set | |
| `delegationPolicyRef` | **Override** — derived may set a more restrictive policy; gateway validates derived policy is a subset of base | |
| `allowSelfRecursion` | **Override (restrict-only)** — derived may set `false` when base is `true`; a derived value of `true` is rejected when the base is `false` | Security boundary. Gateway rejects registration with `INVALID_DERIVED_RUNTIME: allowSelfRecursion cannot widen base value` if derived `true` while base `false`. See [Section 8.2](08_recursive-delegation.md#82-delegation-mechanism). |
| `agentInterface` | **Override** — derived value replaces base if set | |
| `publishedMetadata` | **Append** — derived entries merged into base list; duplicate keys replaced by derived | |
| `sessionPolicy` | **Override** — derived value replaces base if set | |
| `capabilityInferenceMode` | **Override** — derived value replaces base if set; otherwise base value applies | Derived runtimes may relax to `permissive` for third-party tools; does not affect tools with explicit `toolCapabilityOverrides` |
| `labels` | **Merge** — derived labels merged into base labels; conflicting keys replaced by derived | |
| `sharedAssets` | **Append** — derived assets appended to base assets; conflicting `destPath` entries replaced by derived | Populates `/workspace/shared/` (read-only) during pod initialization ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout)). Only meaningful when `sessionPolicy.maxConcurrentSessions` exceeds 1. |

**Merge behavior definitions:**
- **Prohibited** — field must not be present in derived runtime definition. Gateway returns `INVALID_DERIVED_RUNTIME` at registration if present.
- **Inherited** — field is always taken from base; derived definition may not set it.
- **Override** — derived value wholly replaces base value when present; base value applies when absent.
- **Maximum** — numeric fields where gateway selects `max(base, derived)` to ensure the stricter (larger) bound applies.
- **Append** — collection fields where derived entries are added to base entries; conflicting keys/paths are won by the derived value.
- **Merge** — map fields where derived key-value pairs are overlaid onto base map.

#### Worked Examples

**Example A — `setupPolicy.timeoutSeconds` (Maximum rule)**

```
base:    setupPolicy.timeoutSeconds: 300
derived: setupPolicy.timeoutSeconds: 120
result:  setupPolicy.timeoutSeconds: 300   # max(300, 120)
```

The derived runtime wanted a shorter timeout but the base imposes a minimum floor. The gateway always enforces the larger value so that base-defined safety margins cannot be undermined by derived runtimes.

**Example B — `labels` (Merge rule) and `capabilities` (Prohibited)**

```
base:
  labels:       { team: platform, approved: "true" }
  capabilities: { interaction: multi_turn, injection: { supported: true, modes: [immediate, queued] } }

derived:
  labels:       { team: research, env: staging }   # override team, add env
  capabilities: { interaction: one_shot }           # INVALID — prohibited field

result (if capabilities absent):
  labels:       { team: research, approved: "true", env: staging }
  capabilities: { interaction: multi_turn, injection: { supported: true, modes: [immediate, queued] } }
```

If the derived definition includes any `capabilities` field, the gateway rejects registration with `INVALID_DERIVED_RUNTIME: capabilities.interaction is prohibited on derived runtimes`. For labels, derived keys win on collision (`team` becomes `research`), and base-only keys are preserved (`approved: "true"`).

#### Derived Runtime Instantiation

Registered via admin API as static configuration, not instantiated per-session. `workspaceDefaults` is the workspace plan the gateway materializes into every pod. Small files inline in `workspaceDefaults`, large files via MinIO reference. Session creation clients upload additional files on top of derived defaults. Workspace materialization order: base defaults → derived defaults → client uploads → file exports from parent delegation. Before materializing, the adapter MUST remove all files from `/workspace/current` to prevent residual state from prior tasks — this applies regardless of whether the pod has a `scrub_warning` annotation from a prior failed cleanup.

Derived runtimes have **fully independent pool settings**. Constraint: resource classes cannot exceed base runtime's configured classes. If no pool registered for a derived runtime, gateway falls back to base runtime's pool.

#### Setup Commands and Policy

Setup commands run after workspace materialization and before runtime starts. While executing, pod in INIT state, not READY. Pod failure during setup causes pod replacement before warm pool entry. Setup commands run once per pod, not per task. Per-task setup belongs in the runtime's initialization.

```yaml
setupPolicy:
  timeoutSeconds: 300 # optional — waits indefinitely if absent
  onTimeout: fail # fail | warn
```

Gateway takes the **maximum** of base and derived `timeoutSeconds` if both set.

#### `agentInterface` Field

`type: agent` runtimes gain an optional `agentInterface` field serving three purposes: discovery, A2A card auto-generation, and adapter manifest summaries.

`supportsWorkspaceFiles: true` signals that workspace files in TaskSpec will be honored, distinguishing internal runtimes from external agents. `type: mcp` runtimes do not have `agentInterface`.

#### `publishedMetadata` Field

Generic metadata publication mechanism on `Runtime`, replacing any named protocol-specific fields (e.g., no dedicated `agentCard` field).

**Visibility levels:**

- **`internal`** — served at `GET /internal/runtimes/{name}/meta/{key}`, requires valid Lenny session JWT. Only reachable from inside the cluster.
- **`tenant`** — same as internal but additionally filtered by `tenant_id` claim in the JWT. An agent in tenant A cannot discover tenant B's agents.
- **`public`** — served at `GET /v1/runtimes/{name}/meta/{key}`, no auth required. A2A cards meant for cross-organization discovery live here.

Not-found and not-authorized produce identical responses — no enumeration. Gateway treats content as **opaque pass-through** — stores and serves without parsing or validating. Validation is the runtime author's responsibility.

**Relationship to `agentInterface` auto-generation:** When an `agentInterface` field is present on a Runtime, the gateway generates an A2A agent card at **write time** (during registration or update) and stores the pre-formatted card as a `publishedMetadata` entry — the gateway never parses `publishedMetadata` on read. The opaque pass-through guarantee applies at serve time; generation is a one-time write-path operation that produces a normal metadata entry. Runtime authors who prefer full control may omit `agentInterface` and publish a hand-crafted card directly via `publishedMetadata`.

**Card versioning fields.** Every auto-generated card includes two envelope fields injected by the gateway generator (not drawn from `agentInterface`):

- **`generatedAt`** — RFC 3339 timestamp of the generation instant.
- **`generatorVersion`** — semantic version of the Lenny gateway that produced the card (e.g., `"1.4.0"`).

These fields let operators detect staleness after a Lenny upgrade: if the `generatorVersion` in a stored card is older than the running gateway, the card may not reflect format changes introduced by the new version. Hand-crafted cards published directly via `publishedMetadata` are not subject to this convention — the gateway only injects these fields into cards it generates.

**Bulk regeneration.** The admin API exposes a bulk-regeneration endpoint:

```
POST /v1/admin/runtimes/regenerate-cards
```

Request body (all fields optional):

| Field | Type | Default | Meaning |
|---|---|---|---|
| `generatorVersionBefore` | string | — | Regenerate only runtimes whose stored card has `generatorVersion` strictly less than this value. Omit to regenerate all. |
| `dryRun` | bool | `false` | Return affected runtime names without writing. |

Response: `{ "regenerated": ["runtime-a", "runtime-b"], "skipped": [...], "errors": [...] }`.

The endpoint iterates over all registered runtimes that have an `agentInterface` field, re-invokes the card generator, and atomically replaces the `publishedMetadata` entry for key `agent-card`. It does not affect hand-crafted entries (i.e., runtimes that have an `agent-card` entry but no `agentInterface` field are skipped and listed under `"skipped"`). The operation is idempotent — re-running with the same version input produces the same stored card.

**Rationale:** Does not encode a bet on A2A's longevity into the schema. Naturally accommodates agent cards, OpenAPI specs, cost manifests, or whatever the ecosystem invents.

#### Capability Inference from MCP `ToolAnnotations`

Gateway reads `tools/list` at connector or `type:mcp` runtime registration and infers capabilities from MCP `ToolAnnotations`. No manual re-annotation required.

| MCP annotation                                | Inferred capabilities                                  |
| --------------------------------------------- | ------------------------------------------------------ |
| `readOnlyHint: true`                          | `read`                                                 |
| `readOnlyHint: false, destructiveHint: false` | `write`                                                |
| `destructiveHint: true`                       | `write, delete`                                        |
| `openWorldHint: true`                         | `network`                                              |
| No annotations                                | `admin` (conservative default)                         |
| _(no MCP equivalent)_                         | `execute`, `admin` — set via `toolCapabilityOverrides` |

Tenant-overridable via `tenantRbacConfig.mcpAnnotationMapping` (see [Section 10.6](10_gateway-internals.md#106-environment-resource-and-rbac-model)).

**Warning on inferred `admin`.** When a tool's capability is inferred as `admin` due to absent annotations (the "No annotations" row), the gateway emits a `WARN`-level log at registration time: `"Tool '<name>' on connector/runtime '<id>' has no MCP ToolAnnotations; capability inferred as 'admin' (conservative default). Use toolCapabilityOverrides or add ToolAnnotations to suppress this warning."` This surfaces the inference explicitly so deployers are not silently surprised when a pool without `admin` capability rejects the tool. Tools inferred as `admin` that are assigned to pools lacking `admin` capability return `TOOL_CAPABILITY_DENIED` at call time; the warning at registration time provides an earlier signal to address the mismatch.

**`capabilityInferenceMode`** (field on `RuntimeDefinition`, `strict` | `permissive`, default: `strict`) controls the default capability for unannotated tools: in `strict` mode (default), unannotated tools infer as `admin` and emit the WARN log above; in `permissive` mode, unannotated tools infer as `write`, suppressing the WARN log. Use `permissive` mode for third-party runtimes with unannotated tools that do not require admin pool assignment. Note: `capabilityInferenceMode` does not affect tools with explicit `toolCapabilityOverrides` entries — those always use their explicit value.

#### First-Party Reference Runtimes

Lenny ships a catalog of maintained, first-party reference runtimes — `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, and `crewai`. Each is a complete `Runtime` definition plus container image, published under `github.com/lennylabs/runtime-<name>`. Deployers get working agents without writing their own. See [Section 26](26_reference-runtime-catalog.md) for the full catalog, per-runtime `runtimeOptions` schemas ([§14](14_workspace-plan-schema.md)), workspace conventions, and credential-lease scopes. Reference runtimes are registered by `lenny-ctl install` (Section 17.6) or auto-installed by `lenny up` (Section 17.4). They are platform-global records with no default tenant access grants; operators explicitly grant access per tenant.

#### Minimal Configuration

Most fields above have sensible defaults. The absolute minimum to register a runtime and start handling sessions:

```yaml
# Minimal Lenny configuration — everything else uses sensible defaults
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

This is the minimum configuration for a single-runtime deployment. All other fields (isolation profile, resource class, warm count, delegation, egress profile, etc.) use deployer-safe defaults. See the full Runtime schema above for customization options.

### 5.2 Pool Configuration and Execution Modes

This section covers: pool dimensions and configuration fields, the execution modes (`session` and `service`), the `sessionPolicy` block and its common configurations, tenant pinning and cross-tenant reuse rules, the Lenny scrub procedure for recycling pods, slot assignment atomicity, slot retry policy, execution mode scaling implications (mode-adjusted formulas), pool taxonomy, and bootstrap behavior.

Each pool is a warmable deployment target for one runtime + operational profile.

**Pool dimensions:**

- Runtime name
- Isolation profile (runc / gvisor / kata)
- Resource class (small / medium / large)
- Execution mode
- Upload and setup policy
- Egress/network profile
- Warm count
- Max session age
- Checkpoint cadence

#### Execution Modes

The `session` and `service` execution modes are both implemented in v1. Graph mode is removed as a separate concept; graph-aware runtimes are session-mode runtimes. In v1, graph-aware runtimes may emit OpenTelemetry spans using their own OTel SDK configured against the OTLP collector endpoint injected in the adapter manifest as `observability.otlpEndpoint`. Lenny does not define a dedicated span emission tool or RPC in v1; runtimes use standard OTLP libraries directly. A dedicated `lenny/emit_span` MCP tool is deferred to post-v1.

```yaml
executionMode: session | service
```

The mode names follow the unit of the client contract. In `session` mode the session is the managed unit: the gateway binds each session to a claimed pod and manages its workspace, lifecycle, and recovery. In `service` mode the runtime is a replicated service: the gateway routes each message to any ready tenant-pinned replica, creates no `SandboxClaim`, and materializes no workspace (see the `service` definition below).

**`session`** — the session is bound to a claimed pod for the session's lifetime. Default mode. Session mode is parameterized by the `sessionPolicy` block. In the default configuration (`maxConcurrentSessions: 1`, `recycle.enabled: false`) each pod is exclusive to one session and terminates when the session ends.

```yaml
sessionPolicy:                            # session mode only
  maxConcurrentSessions: 1                # simultaneous sessions per pod; > 1 requires acknowledgeProcessLevelIsolation
  acknowledgeProcessLevelIsolation: false # required when maxConcurrentSessions > 1 — see the concurrent-session acknowledgment below
  recycle:
    enabled: false                        # serve successive sessions on one pod with a scrub between them
    acknowledgeBestEffortScrub: false     # required when enabled: true — the workspace scrub is best-effort
    maxSessionsPerPod: 50                 # required when enabled — counts every session served; pod retired at the bound
    maxPodUptimeSeconds: 86400            # optional — pod retired after this uptime (seconds)
    maxScrubFailures: 3                   # pod retired after this many cumulative scrub failures (default: 3)
    onScrubFailure: warn                  # warn | fail — see the onScrubFailure behaviors below
    scrubProfile: standard                # standard | vm-restart | in-place — see the Kata/microvm scrub variant below
    acknowledgeMicrovmResidualState: false # required when scrubProfile: in-place
    allowCrossTenantReuse: false          # requires isolationProfile: microvm; never permitted when maxConcurrentSessions > 1
  cleanupCommands: []                     # deployer cleanup executed before the whole-pod scrub
  cleanupTimeoutSeconds: 60
  maxSessionRetries: 1                    # crash re-dispatch attempts (default: 1, giving 2 total attempts; 0 disables retries)
  maxSessionAgeSeconds: 7200              # wall-clock session age cap (default: 7200)
  maxClientIdleSeconds: 7200              # client-inactivity bound; defaults to the effective maxSessionAgeSeconds (see Section 6.2)
  slotRetries: 1                          # retries for failed slots when maxConcurrentSessions > 1 (default: 1)
  onPoolExhausted: reject                 # reject | queue — see below
  maxQueueWaitSeconds: 30                 # queue wait bound when onPoolExhausted: queue
```

Common configurations of the block:

| Configuration | `maxConcurrentSessions` | `recycle.enabled` | Behavior |
|:--|:--|:--|:--|
| One session per pod (default) | 1 | `false` | The pod is exclusive to a single session and terminates when the session ends. |
| Pod reuse | 1 | `true` | The pod serves sequential sessions of one tenant, with a whole-pod scrub between sessions. |
| Concurrent | N | `true` | The pod serves up to N simultaneous sessions in per-slot workspaces and recycles when occupancy reaches zero. |
| Bounded cohort | N | `false` | The pod serves up to N simultaneous sessions, then terminates after the cohort drains. |

Acknowledgments, tenant pinning, `residualStateWarning`, and the scaling factors are derived from `sessionPolicy` properties rather than keyed to mode names:

- `recycle.acknowledgeBestEffortScrub: true` is required when `recycle.enabled: true`; the workspace scrub is best-effort (see the deployer acknowledgment below).
- `acknowledgeProcessLevelIsolation: true` is required when `maxConcurrentSessions > 1`; concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack (see the concurrent-session acknowledgment below).
- `recycle.acknowledgeMicrovmResidualState: true` is required when `recycle.scrubProfile: in-place` (see the Kata/microvm scrub variant below).
- Tenant pinning is required when `maxConcurrentSessions > 1` or `recycle.enabled: true` (see tenant pinning below).
- `capabilities.preConnect: true` is admitted only when `maxConcurrentSessions` is 1, and service-mode pools reject it ([Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).

**`maxClientIdleSeconds`** terminates a session after continuous client inactivity. It is the single platform idle bound; the activity definition and the per-state pause table are specified in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine). The default is the pool's effective `maxSessionAgeSeconds`, so the bound binds before the age cap only when a deployer lowers it or when the session accrues idle time in a state where `maxSessionAge` is paused but the idle clock runs.

**`onPoolExhausted`** parameterizes the per-pool claim queue ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). `reject` keeps the bounded claim-path wait and the Postgres fallback before returning `WARM_POOL_EXHAUSTED`. `queue` holds the request in the same per-pool FIFO for up to `maxQueueWaitSeconds` after the claim-path timeout and the Postgres fallback are exhausted, re-entering acquisition as pods free; a queued request holds no pod, slot, or claim, and the client receives a `session_id` only on success. On timeout the gateway returns `WARM_POOL_EXHAUSTED` with a `Retry-After` header.

**Tenant pinning:** A session-mode pod whose pool sets `recycle.enabled: true` or `maxConcurrentSessions > 1` is pinned to a single tenant for its entire lifetime; service-mode pods are pinned by the tenant-affinity routing in the `service` definition below. The gateway MUST NOT assign a pinned pod to a different tenant than its first assignment. This is enforced at two independent layers:

1. **Application layer (gateway):** Each pinned pod records its `tenantId` on first use, and subsequent assignment requests verify `tenantId` match before routing. The gateway reads the pin from the `lenny.dev/tenant-id` Pod label it stamps at first assignment.
2. **Kubernetes layer (admission webhook):** Warm-pool pods are labeled `lenny.dev/tenant-id: {tenant_id}` at first assignment time by the gateway agent. A `ValidatingAdmissionWebhook` (`lenny-tenant-label-immutability`) rejects any request that mutates the `lenny.dev/tenant-id` label on an existing pod to a different non-empty value. The only permitted transitions are: unset → `{tenant_id}` (initial assignment) and `{tenant_id}` → `unassigned` (pod return to pool). Any other mutation is rejected with HTTP 403 and error `tenant_label_immutable`. This webhook provides defense-in-depth: even if the gateway's application-layer logic has a bug, no Kubernetes API call can silently re-label a pod to a different tenant. The webhook runs in **fail-closed** mode (`failurePolicy: Fail`) and is deployed with `replicas: 2` and a PodDisruptionBudget (`minAvailable: 1`), matching the HA requirements of the `lenny-label-immutability` webhook ([Section 13.2](13_security-model.md#132-network-isolation)). The `unset → {tenant_id}` transition is authorized only for the gateway ServiceAccount (`system:serviceaccount:lenny-system:lenny-gateway`); the `{tenant_id} → unassigned` transition is authorized only for the WarmPoolController ServiceAccount (`system:serviceaccount:lenny-system:lenny-controller`). The webhook is deployed as part of the Helm chart under `templates/admission-policies/` and its correctness is covered by the admission policy integration test suite (`tests/integration/admission_policy_test.go`).

The pin persists across the recycle-to-idle edge for the pod's lifetime on pools without microvm-gated `recycle.allowCrossTenantReuse`: a pinned idle pod is claimable only by its pinned tenant, the candidate scan filters on the pin label, and inventory accounting counts pinned-idle pods as idle inventory available to that tenant alone. The `{tenant_id} → unassigned` transition applies only where cross-tenant reuse is permitted.

Cross-tenant pod reuse is only permitted on the sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`), with `microvm` isolation and an explicit `allowCrossTenantReuse: true` field in `sessionPolicy.recycle`, where the VM boundary provides a VM-level isolation boundary that is significantly stronger than runc or gVisor but shares host virtio devices. Kata provides isolation appropriate for cross-tenant sequential reuse where tenants have been independently vetted, but it is not equivalent to dedicated hardware isolation. The pool controller rejects `recycle.allowCrossTenantReuse: true` on any pool whose `isolationProfile` is not `microvm` at validation time with a descriptive error. Cross-tenant slot sharing when `maxConcurrentSessions > 1` is categorically prohibited regardless of isolation profile (see the cross-tenant prohibition below).

**T4 cross-tenant reuse prohibition.** The pool controller additionally rejects `recycle.allowCrossTenantReuse: true` on any pool whose associated Runtime is configured with `workspaceTier: T4` ([Section 12.9](12_storage-architecture.md#129-data-classification)). T4 workloads require dedicated node pools for per-tenant key isolation ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout)); cross-tenant pod reuse, even with microvm isolation, places two tenants' data within the same shared microvm host and violates the dedicated-node boundary that T4 requires. The rejection error is: `"recycle.allowCrossTenantReuse: true is not permitted for T4-tier pools (workspaceTier: T4); T4 workloads require dedicated node pools (Section 6.4)"`. The gateway also enforces this at session assignment time: if a T4 session request would route to a sequential-reuse pod (`maxConcurrentSessions: 1`, `recycle.enabled: true`) already bound to a different tenant, the assignment is rejected and the pod is retired from the cross-tenant pool. This guards against misconfigured pools that bypass the pool controller validation.

**Scrub model.** The scrub is uniform across session-mode configurations: a per-slot cleanup runs on every session release, reported by the adapter via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)), and a whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is reused, reported via `ReportPodScrub`.

**Recycle lifecycle (`recycle.enabled: true`).** When the pod's occupancy reaches zero, the gateway patches the pod's `SandboxClaim` to `recycling` and the whole-pod boundary runs: the adapter removes `/run/lenny/credentials.json` (the credential purge precedes any deployer code; see scrub step 0 below), deployer-defined `cleanupCommands` execute (with access to session state but without the previous session's credential file), the Lenny whole-pod scrub runs, and the adapter reports the binary outcome via `ReportPodScrub`. On a successful scrub the pod is held for its tenant through the claim's `reserved` state and serves the next session; on a preConnect pool the SDK re-warm runs after the scrub reports success and before the claim enters `reserved`, so every pod that reaches `reserved` or `idle` is SDK-warm ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). A session that ends in failure or a crash always retires its pod regardless of recycle settings. `setupCommands` run once per pod at start; per-session setup belongs in the runtime's initialization.

**Recycling and integration levels.** Pod recycling requires no runtime cooperation: the per-slot cleanup and the whole-pod scrub are adapter-executed and gateway-coordinated, with no lifecycle-channel exchange between sessions. Recycling works at every integration level (Basic, Standard, and Full), and `recycle.maxSessionsPerPod` applies as configured at every level.

**Lenny scrub procedure.** The whole-pod scrub runs at the recycle boundary, when occupancy reaches zero on a recycling pod and the pod's claim is `recycling` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). The scrub has two phases: a **pre-cleanup credential purge** (step 0) executed by the adapter before `cleanupCommands`, and a **post-cleanup scrub sequence** (steps 1-6) executed after `cleanupCommands` finish. The adapter reports the scrub's binary outcome to the gateway via `ReportPodScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)); a missing report is bounded by a gateway-side timeout (`cleanupTimeoutSeconds` plus a grace period), after which the pod is retired.

**Step 0 (pre-cleanup, before `cleanupCommands`):** Remove `/run/lenny/credentials.json`. The credential file is a platform-managed security artifact rather than deployer session state, and MUST be purged before any deployer-defined code runs. This prevents `cleanupCommands` (which may be deployer-defined and potentially untrusted or buggy) from reading the previous session's credential file. If `cleanupCommands` require credential metadata for custom audit logging, the adapter exposes sanitized metadata (provider name, lease ID) in the cleanup environment variables `LENNY_PREV_CREDENTIAL_PROVIDER` and `LENNY_PREV_LEASE_ID` rather than leaving the full credential file accessible.

**Steps 1-6 (post-cleanup, after `cleanupCommands` finish):**

1. Kill all remaining user processes (`kill -9 -1` as the sandbox user).
1b. Purge all `shmget`-allocated IPC shared memory segments (`ipcrm --all=shm`). This handles segments whose creating process was not yet killed when step 1 executed (IPC segments persist until explicitly removed or the owning IPC namespace is destroyed). For gVisor pods this step is a no-op in practice because gVisor's per-pod sandbox kernel provides a fully isolated IPC namespace — segments cannot leak to other pods — but the step executes unconditionally for consistency.
2. Remove the workspace directory (`rm -rf $WORKSPACE_DIR`).
3. Purge environment variables injected for the previous session (tracked by the adapter; restored to the pod baseline set recorded at first boot).
4. Clear `/tmp`, `/dev/shm`, and any adapter-managed scratch directories.
5. Truncate adapter-local log buffers.
6. Verify scrub by stat-checking the workspace path, `/tmp`, `/dev/shm`, and `/run/lenny/credentials.json` — if any path is non-empty after scrub (or if `/run/lenny/credentials.json` still exists despite step 0), the scrub is marked failed.

The scrub is **best-effort** and is not a security boundary: it reduces cross-session data leakage within a single tenant but does not replace isolation. This is why recycling pods are tenant-pinned (see above). Specifically, the scrub **cannot** address the following residual state vectors: kernel TCP socket `TIME_WAIT` state and connection tracking entries, DNS resolver cache in long-lived `nscd` or `systemd-resolved` processes (killed in step 1 but cache may be observable via timing during the kill window), kernel buffer/page cache priming (files read by a previous session remain in page cache, observable via timing), `inotify` and `fanotify` watch registrations (cleared only when the owning process is killed), and named pipes or UNIX domain sockets outside managed paths. (`shmget`-allocated IPC shared memory segments are addressed by step 1b above.) Deployers should evaluate whether these residual vectors are acceptable for their workload's sensitivity level.

**Kata/microvm scrub variant.** When `isolationProfile: microvm` and `recycle.allowCrossTenantReuse: true`, the standard Lenny scrub (steps 1-6 above) is insufficient because the guest VM itself persists across sessions: the guest kernel's DNS resolver cache, TCP connection tracking state, kernel buffer/page cache, and in-memory filesystem metadata survive the scrub. For cross-tenant sequential reuse on microvm pods, the scrub procedure includes an additional step after step 6 (the `vm-restart` scrub profile):

7. **Guest VM restart:** The adapter requests a full guest VM restart via the Kata runtime's VM lifecycle API. The guest kernel is shut down and a fresh guest boots from the original VM image. This eliminates all guest-kernel-level residual state (DNS cache, TCP `TIME_WAIT`, page cache, inotify registrations, kernel module state). The restart adds latency (typically 3-8 seconds, consistent with Kata cold-start times documented in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). The scrub verification (step 6) is re-executed after the guest restarts to confirm a clean state.

The scrub procedure is selected by `recycle.scrubProfile`: `standard` (the default) runs steps 0-6, `vm-restart` adds the guest restart in step 7, and `in-place` runs only steps 0-6 inside the continuing VM guest. On a pool with `recycle.allowCrossTenantReuse: true`, `scrubProfile: standard` is insufficient and the pool controller rejects it at validation time; such pools set `vm-restart`, or `in-place` when the deployer requires cross-tenant reuse without the guest restart latency cost. In `in-place` mode the following residual state vectors are documented as persisting across tenant boundaries: guest kernel DNS resolver cache, guest kernel TCP `TIME_WAIT` state, guest kernel buffer/page cache priming, and guest kernel inotify/fanotify registrations. The `in-place` profile requires an additional acknowledgment: `acknowledgeMicrovmResidualState: true` in `sessionPolicy.recycle`. The pool controller rejects `scrubProfile: in-place` without this acknowledgment.

**`onScrubFailure` behaviors:**

- **`warn`** (default) — the pod is returned to the available pool with a `scrub_warning` annotation. The gateway logs the failure, increments `lenny_pod_scrub_failure_total` (aggregate counter) and `lenny_pod_scrub_failure_count` (per-pod gauge, labeled by `k8s_pod_name`, `pool`, `runtime_class`), and the pod serves the next session. The deployer accepts residual state risk. **preConnect interaction:** for pools where the runtime declares `capabilities.preConnect: true`, the pod routes through the recycle re-warm edge `claimed → sdk_connecting` before reaching `reserved` even on a `scrub_warning` outcome (see [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) and the "preConnect re-warm on scrub_warning" note). This preserves the Section 6.1 invariant that every pod reaching `reserved` or `idle` in a preConnect pool is SDK-warm; the `scrub_warning` annotation and cumulative failure count persist through the re-warm. When the pod's cumulative scrub failure count reaches `maxScrubFailures` (default: `3`), the pod is retired and terminated regardless of the `onScrubFailure` setting; the gateway provisions a replacement from the warm pool and logs the retirement reason as `scrub_failure_limit_reached`.
- **`fail`** — the pod is removed from the pool and terminated. The gateway provisions a replacement pod from the warm pool. The failed pod's metadata is retained in the audit log for inspection.

**Pod retirement policy (recycling pools).** A recycling pod is retired (transitioned to `draining` and then terminated) when any of the following conditions is met:

- **Session count limit:** The pod's served-session count reaches `recycle.maxSessionsPerPod`. After the current session ends and the whole-pod scrub finishes, the pod transitions to `draining` instead of returning to `idle`. A replacement pod is provisioned from the warm pool. `maxSessionsPerPod` is required with no default when `recycle.enabled: true`; the deployer must make an explicit choice based on the workload's sensitivity and the residual state vectors enumerated above.
- **Uptime limit:** The pod's wall-clock uptime since first boot exceeds `recycle.maxPodUptimeSeconds`. The gateway checks uptime before dispatching the next session; if the pod has exceeded the limit, it transitions to `draining` after its current sessions end. `maxPodUptimeSeconds` is optional; if omitted, only `maxSessionsPerPod` and `maxScrubFailures` govern retirement.
- **Scrub failure limit:** The pod's cumulative scrub failure count reaches `recycle.maxScrubFailures` (see `onScrubFailure: warn` above).

A session that ends in failure or a crash also retires the pod, regardless of recycle settings. When a pod is retired, the gateway increments `lenny_pod_retirement_total` (labeled by `reason`: `session_count_limit`, `uptime_limit`, `scrub_failure_limit`) and logs the retirement with the pod's lifetime session count and uptime.

**Deployer acknowledgment.** Because the workspace scrub is best-effort, deployers must set an explicit acknowledgment flag to enable recycling:

```yaml
sessionPolicy:
  recycle:
    enabled: true
    acknowledgeBestEffortScrub: true # required — recycling rejected without this
    allowCrossTenantReuse: false # requires isolationProfile: microvm; sequential reuse only
    scrubProfile: standard # standard | vm-restart | in-place
    # acknowledgeMicrovmResidualState: false # required when scrubProfile: in-place
    maxScrubFailures: 3 # pod retired after this many cumulative failures (default: 3)
    maxSessionsPerPod: 50 # required — no default, forces deployer choice
    maxPodUptimeSeconds: 86400 # optional — retirement after uptime threshold
  cleanupCommands: [...]
  cleanupTimeoutSeconds: 30
```

If `acknowledgeBestEffortScrub` is absent or `false` on a pool with `recycle.enabled: true`, the pool controller rejects the pool definition at validation time with a descriptive error referencing this section. Similarly, `maxSessionsPerPod` is required with no default when recycling is enabled; the pool controller rejects recycling pool definitions that omit it, forcing the deployer to make an explicit reuse-limit choice appropriate to their workload.

**Client visibility of weak-isolation reuse.** Because `acknowledgeBestEffortScrub` and `acknowledgeProcessLevelIsolation` are deployer-level configuration, clients creating sessions against a pool that recycles pods, that runs `maxConcurrentSessions > 1`, or that runs in service mode have no independent mechanism to determine their isolation posture unless the platform surfaces it explicitly. To address this, the session creation response (`POST /v1/sessions`) includes a `sessionIsolationLevel` object containing `executionMode`, `isolationProfile`, `podReuse`, `scrubPolicy`, `residualStateWarning`, and `conversationContinuity` — see [Section 7.1](07_session-lifecycle.md#71-normal-flow) for the full field definitions. `podReuse` and `residualStateWarning` are `true` when `recycle.enabled` is `true`, when `maxConcurrentSessions > 1`, or when `executionMode` is `service`. When `residualStateWarning: true`, the session runs on a pod that serves more than one session over its lifetime: on a recycling pod the scrub is best-effort and residual state vectors (DNS cache, TCP `TIME_WAIT`, page cache, etc.) may be observable from prior sessions; concurrent slots share process-level state; and service-mode pods serve successive requests with no scrub. Clients that require strict isolation should check `residualStateWarning` in the response and reject sessions where this field is `true` if their use case cannot tolerate residual state.

**Concurrent sessions (`maxConcurrentSessions > 1`).** Setting `sessionPolicy.maxConcurrentSessions` above 1 multiplexes simultaneous sessions onto a single pod, each in its own slot. Each slot gets its own workspace under `/workspace/slots/{slotId}/` (see [Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) for the full per-slot filesystem layout). The gateway tracks per-slot lifecycle. Message delivery uses `slotId` multiplexing over stdin: the adapter assigns a `slotId` per slot, creates the per-slot directory tree, and sets the slot's `cwd` to `/workspace/slots/{slotId}/current/`; the runtime implements a dispatch loop keyed on `slotId`; all binary protocol messages (inbound and outbound) carry `slotId` when `maxConcurrentSessions > 1`. Cross-slot isolation is process-level and filesystem-level, which is weaker than the one-session-per-pod configuration.

**Deployer acknowledgment (concurrent sessions).** Because concurrent sessions share a single pod's process namespace, `/tmp`, cgroup memory, and network stack across all simultaneous slots, deployers must set `sessionPolicy.acknowledgeProcessLevelIsolation: true` to configure `maxConcurrentSessions > 1`. If `acknowledgeProcessLevelIsolation` is absent or `false`, the pool controller rejects the pool definition at validation time with a descriptive error referencing this section and listing the specific isolation properties the deployer is accepting: shared process namespace, shared `/tmp`, shared cgroup memory, shared network stack, and **shared credential-file group-read access** (each slot's `/run/lenny/slots/{slotId}/credentials.json` is readable by every slot's agent process via the shared `lenny-cred-readers` supplementary group — see [§13.1](13_security-model.md)) between concurrent slots. The rejection message additionally enumerates network-level side-channels inherent to the shared network namespace: (a) cross-slot network traffic observation via raw sockets, (b) port binding conflicts between slots, (c) DNS resolver cache poisoning where one slot's DNS queries populate cached entries visible to other slots, and (d) network activity timing patterns observable across slots. To mitigate raw socket sniffing, the agent container's `securityContext` MUST drop `CAP_NET_RAW` (the `SandboxWarmPool` CRD validation webhook rejects pool definitions where `maxConcurrentSessions > 1` and the pod template grants `CAP_NET_RAW`). Deployers requiring network isolation or credential-lease isolation between concurrent sessions should set `maxConcurrentSessions: 1`.

**Cross-tenant prohibition (concurrent sessions).** Concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack *simultaneously*, so simultaneous process-level cotenancy has no isolation boundary. Cross-tenant slot sharing is therefore never permitted when `maxConcurrentSessions > 1`, regardless of `isolationProfile` or `recycle.scrubProfile`; the microvm gate above applies only to the sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`). The pool controller rejects any pool definition where `maxConcurrentSessions > 1` and `recycle.allowCrossTenantReuse: true` at validation time with error: `"recycle.allowCrossTenantReuse: true is not permitted when maxConcurrentSessions > 1; cross-tenant slot sharing has no isolation boundary"`. Tenant pinning for concurrent-session pods follows the derivation above (pinning is required when `maxConcurrentSessions > 1`) and is enforced by the same two layers.

**`service`** — the runtime is a replicated service with no Lenny-managed session state. There is no workspace materialization and no `SandboxClaim`. The gateway routes each message through the pool's Kubernetes Service with **tenant-affinity routing**: it maintains an in-memory mapping of `tenantId → set of pinned pod IPs` and uses Kubernetes `EndpointSlice` watches to discover pod IPs behind the Service. On first request for a tenant, the gateway selects an unpinned pod from the Service's endpoints, pins it to the tenant (applying the `lenny.dev/tenant-id` label), and records the mapping. Subsequent requests for the same tenant are routed directly to a pinned pod IP (bypassing the Service load balancer) via the gateway's HTTP client. If all pinned pods for a tenant are at slot capacity (readiness probe `false`), the gateway selects a new unpinned pod and pins it. This ensures tenant affinity is enforced despite the Service-based routing model. The pool-level `maxConcurrent` field is the service-mode per-pod request capacity (`sessionPolicy` and its `maxConcurrentSessions` apply only to session mode): the pod readiness probe reflects slot availability, and the PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

```yaml
executionMode: service
maxConcurrent: 8 # per-pod concurrent request capacity; readiness reports false at capacity
```

**Session contract (service mode).** Sessions remain the API surface (`POST /v1/sessions`, `lenny/create_session`, `send_message`); a service-mode session is a connection handle. Service mode provides no cross-message conversation continuity: every message is self-contained, and the platform may route successive messages of one session to different replicas. `capabilities.interaction: multi_turn` runtimes are permitted; clients of multi-turn runtimes re-inject any needed context into each message's `input`. The session creation response carries `sessionIsolationLevel.conversationContinuity: "none"` for service-mode pools and `"platform"` otherwise ([Section 7.1](07_session-lifecycle.md#71-normal-flow)), and runtime registration emits a warning event when a `multi_turn` runtime is bound to a service-mode pool. Service-mode pools report `podReuse: true`, `residualStateWarning: true`, and `scrubPolicy: "none"`: pods serve successive requests with no scrub and share process space, network stack, `/tmp`, and page cache across same-tenant concurrent requests.

**Tenant isolation (service mode).** Service-mode pods are tenant-pinned using the same two-layer mechanism as pinned session-mode pods: (1) the gateway records `tenantId` on first request routed to the pod and rejects subsequent requests with a mismatched `tenantId`, and (2) the `lenny-tenant-label-immutability` `ValidatingAdmissionWebhook` prevents mutation of the `lenny.dev/tenant-id` label. Although service-mode pods have no Lenny-managed workspace or session state, they share a network namespace and process space across all concurrent requests; cross-tenant routing would expose tenant-specific network traffic patterns and process metadata. Cross-tenant reuse is structurally unrepresentable in service mode: the only cross-tenant-reuse field, `sessionPolicy.recycle.allowCrossTenantReuse`, applies to session mode alone, so a service-mode pool has no configuration that could permit serving two tenants from one pod.

> **Service-mode limitations (v1).** `executionMode: service` provides only minimal platform guarantees compared to session mode. There is no workspace delivery, no per-slot lifecycle tracking, no slot-level retry policy, no checkpoint support, and no per-slot failure isolation; a pod failure affects all concurrent requests routed to it. The gateway's role is limited to load-balanced routing via a Kubernetes Service; it does not track individual request outcomes. Deployers are responsible for all retry, idempotency, and error-handling logic in their runtime.
>
> **Preferred alternative:** Truly stateless runtimes that do not need workspace materialization or session lifecycle management are better registered as **external connectors** (see [Section 9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)). Connectors integrate with Lenny's routing and observability without incurring pod warm-pool overhead or requiring a Lenny-managed runtime adapter. `executionMode: service` exists for runtimes that are already deployed as Lenny pods and have minimal statefulness, but where migrating to the connector model is not yet feasible. New deployments with no workspace requirements should use connectors instead.
>
> **Choosing between `service` mode and connectors:**
> - Use `service` mode if: you already have a Lenny-managed pod image and want simple horizontal scaling with Lenny's pool management and readiness-probe-driven routing.
> - Use connectors if: your runtime is independently deployed, has its own scaling, or you need richer failure semantics. Connectors are the recommended long-term target for stateless workloads.

**Slot failure and cleanup (`maxConcurrentSessions > 1`).** Slots fail independently — a single slot failure does not terminate the pod or affect other active slots. Per-slot behavior:

- **Failure isolation:** When a slot's session fails (runtime error, pod-level OOM kill, or unhandled exception), the adapter marks that `slotId` as `failed` and emits `lenny_slot_failure_total{error_type}`. Other slots continue unaffected. The gateway is notified via the lifecycle channel and applies the slot retry policy below.
- **Slot cleanup:** On slot completion or failure, the adapter removes the slot's workspace directory, kills any processes owned by the slot's process group, and releases the `slotId`. The adapter reports each slot cleanup outcome (`released` or `leaked`) to the gateway via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)). Cleanup timeout is `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds (minimum 5s enforced at runtime by the adapter). **CRD validation rule:** The `SandboxWarmPool` admission webhook rejects any pool configuration where `cleanupTimeoutSeconds / maxConcurrentSessions < 5`, i.e., where `cleanupTimeoutSeconds < maxConcurrentSessions × 5`. Rejection error: `422 INVALID_POOL_CONFIGURATION` with message `"cleanupTimeoutSeconds / maxConcurrentSessions would produce a per-slot cleanup timeout below the 5s minimum; set cleanupTimeoutSeconds ≥ maxConcurrentSessions × 5"`. This CRD validation is intentionally stricter than the runtime formula requires: the runtime formula `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` would clamp sub-5s results to 5s regardless, but the CRD validation rejects such configurations at admission time to ensure the deployer's configured `cleanupTimeoutSeconds` produces a meaningful per-slot budget above the minimum floor rather than silently relying on the runtime clamp. If cleanup fails, the slot is leaked — the pod continues but the slot is not reclaimed until pod termination. See **`leaked` slot semantics** ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) for the full specification of leaked slot behavior: leaked slots remain counted in the slot counter, count toward the unhealthy threshold, and are surfaced via the `lenny_adapter_leaked_slots` gauge.
- **Checkpoint granularity:** Checkpoints are per-slot. Each slot's checkpoint includes only that slot's workspace state and conversation history. Whole-pod checkpoints are not taken when `maxConcurrentSessions > 1` because slot lifecycles are independent. Per-slot checkpoints are subject to the same tiered cap as one-session-per-pod checkpoints ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling) preStop hook, stage 2): the cap is selected based on the slot's last measured workspace size (`last_checkpoint_workspace_bytes` for the `(session_id, slot_id)` pair in Postgres). The total preStop budget for a pod with `maxConcurrentSessions > 1` is the **sum** of per-slot caps across all active slots; the `SandboxWarmPool` CRD validation webhook enforces that `maxConcurrentSessions × max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds`. Deployers must set `terminationGracePeriodSeconds` accordingly when `maxConcurrentSessions > 1` — the Helm chart provides a helper formula in `values.yaml` comments. **Node drain timeout interaction:** At high `maxConcurrentSessions` values, the required `terminationGracePeriodSeconds` can exceed typical cluster automation drain timeouts (e.g., `maxConcurrentSessions: 8` with 512 MB workspaces yields 8 × 90 + 90 + 30 = 840s, or 14 minutes). If `terminationGracePeriodSeconds` exceeds the node drain timeout (commonly 600s), the kubelet will SIGKILL the pod before checkpoints complete, causing data loss for in-flight slots. Deployers MUST ensure that the cluster's node drain timeout (`--pod-eviction-timeout` on the kube-controller-manager, or the equivalent setting in managed Kubernetes node group configurations) is at least as large as the pool's `terminationGracePeriodSeconds`. The `SandboxWarmPool` CRD validation webhook emits a warning (not a rejection) when the computed `terminationGracePeriodSeconds` floor exceeds 600s: `"terminationGracePeriodSeconds floor (<value>s) exceeds 600s; verify that cluster node drain timeout is configured to accommodate this value"`. Additionally, the pool definition supports an optional `maxTerminationGracePeriodSeconds` field (default: unset) that, when set, causes the CRD validation webhook to **reject** (not warn) any pool configuration whose computed `terminationGracePeriodSeconds` floor exceeds the configured value. This provides a hard ceiling for deployments where the cluster's drain timeout is known and non-negotiable, reconciling the warning-only default with the fail-closed philosophy applied elsewhere. The `lenny-preflight` Job ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) also checks for this condition and emits a preflight warning. **Eviction checkpoint ordering:** Eviction checkpoints for concurrent-session pods are serialized across slots (not fully parallel) to avoid MinIO write amplification. The adapter processes one slot's checkpoint upload at a time, in slot-ID order. This prevents `maxConcurrentSessions` simultaneous uploads from saturating MinIO write bandwidth during a degraded-MinIO scenario, where all uploads would individually exhaust their retry budgets and cascade to `maxConcurrentSessions` simultaneous Postgres fallback writes. The per-slot tiered cap applies to each slot's upload; the total preStop budget (the sum of per-slot caps) accommodates serialized execution. The Postgres fallback retry budget (60s per slot) also runs serially. The CRD validation formula uses `max_tiered_checkpoint_cap` per slot; for pools where `max_tiered_checkpoint_cap ≥ 60s` (workspaces > 100 MB), this subsumes the Postgres fallback budget. For pools with smaller workspaces (`max_tiered_checkpoint_cap = 30s`), the Postgres fallback path (60s per slot) exceeds the per-slot budget assumed by the formula — deployers of small-workspace, high-concurrency pools should use `max(max_tiered_checkpoint_cap, 60)` when computing `terminationGracePeriodSeconds` manually if MinIO degradation is a concern.
- **Resource contention:** CPU and memory are shared across slots (no per-slot cgroup subdivision in v1). If a single slot monopolizes resources, the adapter's health probe degrades and the PoolScalingController reduces `mode_factor` for the pool. Deployers should set `maxConcurrentSessions` conservatively relative to the resource class. Future versions may introduce per-slot resource quotas via cgroup nesting.

**Slot assignment atomicity (`maxConcurrentSessions > 1`).** Slot availability checks and slot reservation must be atomic to prevent double-assignment. The gateway uses an atomic Redis `INCR` with a cap check: the slot counter for a given pod (`lenny:pod:{pod_id}:active_slots`) is incremented only if the resulting value does not exceed `maxConcurrentSessions`. Specifically: the gateway uses a Lua script that atomically checks `GET` and conditionally `INCR` — if `current_count >= maxConcurrentSessions`, the script returns `nil` (slot unavailable) without incrementing; if `current_count < maxConcurrentSessions`, the script increments and returns the new count. This prevents two concurrent session assignments from both reading "1 slot available" on a pod where `maxConcurrentSessions: 1` and both being assigned, which would transiently exceed the pod's slot limit. The counter is the intra-pod capacity gate; pod acquisition itself is guarded by the deterministic per-pod `SandboxClaim` CREATE ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). During a Redis outage the gateway gates capacity on the Postgres `SessionStore.GetActiveSlotsByPod` check under a per-pod advisory lock, failing closed only after a bounded outage window ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). If the atomic reservation fails (all slots taken), the gateway falls through to the next available pod in the pool. If all pods in the pool have reached their `maxConcurrentSessions` slot limit, the gateway attempts to claim an additional warm pod from the pool (the standard warm pool claim path). If no warm pods are available, the request is rejected with `WARM_POOL_EXHAUSTED`, subject to the pool's `onPoolExhausted` setting (a `queue` pool first holds the request in the [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) claim queue for up to `maxQueueWaitSeconds`) — the same error code used for pod exhaustion at `maxConcurrentSessions: 1`. The `details.reason` field distinguishes the cause: `"concurrent_slots_exhausted"` indicates that pods exist but all slots are full, whereas the default reason `"no_idle_pods"` indicates no pods are available at all. The metric `lenny_slot_assignment_conflict_total` (counter, labeled by `pool`) tracks atomic reservation failures due to slot contention, enabling operators to detect pool under-sizing.

**Post-recovery rehydration atomicity.** After a Redis restart, slot counters reset to zero but pods may still have active slots. The Lua script enforces a **blocking rehydration** guarantee via a per-pod `rehydrated` flag (`lenny:pod:{pod_id}:rehydrated`). On every slot allocation attempt, the Lua script first checks whether the `rehydrated` flag exists for the target pod. If the flag is absent (indicating the counter has not been rehydrated since the last Redis restart), the script does **not** proceed with the slot reservation. Instead, it returns a `REHYDRATE_REQUIRED` sentinel value. The calling gateway goroutine then acquires a per-pod rehydration mutex (in-process, with cross-replica coordination via a short-lived Redis `SET NX` lock on `lenny:pod:{pod_id}:rehydrating`), queries `SessionStore.GetActiveSlotsByPod(pod_id)` from Postgres, writes the accurate count to `lenny:pod:{pod_id}:active_slots`, sets the `rehydrated` flag (`SET lenny:pod:{pod_id}:rehydrated 1`), and retries the Lua script. Concurrent allocation attempts for the same pod that arrive while rehydration is in progress block on the `SET NX` lock (with a short spin-wait, bounded by `slotRehydrationTimeoutMs`, default 2000ms) and retry after the flag is set. This eliminates the race window where two simultaneous post-recovery requests could both observe `counter=0` and succeed before rehydration completes. **Scope of blocking:** Rehydration blocks only slot allocations targeting the specific pod being rehydrated; slot allocations for other pods (whether already rehydrated or awaiting their own rehydration) proceed independently. There is no global lock. **Postgres query burst mitigation:** After a Redis restart, the first slot allocation attempt for each concurrent-session pod triggers a `GetActiveSlotsByPod` query. At Tier 3 with hundreds of concurrent-session pods, this produces a burst of Postgres queries as traffic arrives. The queries are naturally staggered by incoming request arrival times (not all pods receive a slot allocation request in the same instant), and the per-pod `SET NX` lock ensures at most one query per pod. The `GetActiveSlotsByPod` query is indexed (`sessions(pod_assignment) WHERE state = 'active'`) and returns at most `maxConcurrentSessions` rows, so each query completes in < 5ms under normal Postgres load. The total rehydration burst at Tier 3 (e.g., 500 pods) is expected to complete within 2-5 seconds of traffic resumption. The `lenny_slot_rehydration_total` counter (labeled by `pod`, `pool`) is emitted on each rehydration event. The `rehydrated` flag has no TTL — it persists until the next Redis restart, at which point all flags are naturally cleared and rehydration is triggered again on first access.

**Slot retry policy (`maxConcurrentSessions > 1`).** When a slot fails, the gateway applies the following retry policy (analogous to the pre-attached failure retry policy in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)):

- **Max retries:** `sessionPolicy.slotRetries` (default: 1, giving 2 total attempts including the original; 0 disables retries). The retry is always assigned to a **new slot** on the same pod (if a slot is available) or on a different pod from the pool (if the original pod is fully saturated or unhealthy).
- **Fresh workspace guarantee:** A retried slot always receives a fresh workspace — workspace staging is materialized from scratch. The retried slot never inherits any state from the failed slot's workspace, even if the failed slot's cleanup has not yet completed.
- **Non-retryable failure categories:** The following failure reasons are returned to the client immediately without retry:
  - **OOM** (`reason: oom`) — the same input is likely to OOM again on an identically-sized slot.
  - **Workspace validation error** (`reason: workspace_validation`) — the workspace plan is structurally invalid and will fail on any slot.
  - **Policy rejection** (`reason: policy_rejection`) — the session was rejected by admission policy.
- **Whole-pod replacement trigger:** When `ceil(maxConcurrentSessions / 2)` or more slots on the same pod **fail or leak** within a rolling 5-minute window, the gateway marks the pod as unhealthy, drains remaining slots gracefully, and requests a replacement pod from the warm pool. The `lenny_slot_pod_replacement_total` counter is incremented. Both categories contribute to the threshold: slots that transition to `failed` (runtime error, OOM, unhandled exception, non-retryable failure) and slots that transition to `leaked` (cleanup timeout exceeded; see **`leaked` slot semantics** in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). Leaked slots count because they consume slot capacity without being reclaimable, so a pod accumulating leaks is as degraded as one accumulating outright failures. This prevents a degraded pod from consuming retries across many independent slot failures or leaks.
- **Client error on exhaustion:** When a slot fails and either no retry is attempted (non-retryable category) or the retry budget is exhausted, the gateway returns a structured error to the client with `error.category` set to the failure reason, `error.retryable: false` from the platform's perspective (the client may choose to resubmit as a new request), and `error.slotId` identifying the failed slot.

**Truly stateless runtimes** with no workspace and no expensive shared state should be registered as external connectors, not Lenny-managed pods.

`executionMode` is declared on the `Runtime` from v1 (and on the corresponding `SandboxTemplate`).

#### Execution Mode Scaling Implications

The default PoolScalingController formula ([Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration)) assumes the default session-mode configuration: one session per pod with no reuse. Pod recycling, concurrent sessions, and service mode change the relationship between pod count and effective capacity, so the formula includes a per-pool adjustment factor derived from `sessionPolicy` (session mode) or from `maxConcurrent` (service mode).

**Term definitions:**
- `mode_factor`: pod reuse multiplier — in session mode, the expected number of sessions a pod serves over its lifetime; in service mode, `maxConcurrent`.
- `burst_mode_factor`: burst-term equivalent of `mode_factor`, reflecting slot availability during burst periods.

**Mode adjustment factor (`mode_factor`):**

- **`session`**: `mode_factor = expected_sessions_per_pod_lifetime`. In the default configuration (`maxConcurrentSessions: 1`, `recycle.enabled: false`) each pod serves exactly one session and `mode_factor = 1.0`. On a recycling pool a pod serves multiple sessions before replacement: if a pod typically handles 10 sessions before retirement, the pool needs ~1/10th the pods to serve the same request volume. Measured via the `lenny_pod_session_reuse_count` histogram (p50). **Formula assumption:** the `mode_factor` estimate converges toward the configured `recycle.maxSessionsPerPod` for predictable workloads where pods are not retired early by `maxPodUptimeSeconds` or `maxScrubFailures`. For variable workloads where early retirement is common, use the observed `lenny_pod_session_reuse_count` p50 rather than `recycle.maxSessionsPerPod` as the estimate.
- **`service`**: `mode_factor = maxConcurrent` — each pod serves `maxConcurrent` simultaneous requests. A pod with `maxConcurrent: 8` provides 8x the effective capacity of a one-session-per-pod session-mode pod.

**Adjusted formula (non-experiment pools):**

```
target_minWarm = ceil(base_demand_p95 × safety_factor × (failover_seconds + pod_warmup_seconds) / mode_factor
                      + burst_p99_claims × pod_warmup_seconds / burst_mode_factor)
```

For A/B experiment variant pools, apply `variant_weight` as defined in the variant pool formula in [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration), combined with the `mode_factor` and `burst_mode_factor` divisors from this formula.

The steady-state term (first) is divided by `mode_factor` because pod recycling and slot multiplexing both reduce the number of pods needed to sustain a given throughput over time. The burst term (second) uses a separate `burst_mode_factor` because burst absorption depends on how many simultaneous requests a single pod can handle at the instant of arrival rather than on lifetime reuse:

- **`session`**: `burst_mode_factor = maxConcurrentSessions` — a pod absorbs as many simultaneous burst arrivals as it has free slots. With `maxConcurrentSessions: 1` each pod absorbs exactly one burst arrival regardless of how many sessions it serves over its lifetime.
- **`service`**: `burst_mode_factor = maxConcurrent` — each pod has `maxConcurrent` slots that can accept simultaneous arrivals.

The `(failover_seconds + pod_warmup_seconds)` factor in the first term converts the claim rate (claims/second) to a pod count, consistent with the base formula in [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration). `pod_warmup_seconds` is the pod creation-to-ready time for the pool (pod-warm baseline ≈ 10s, SDK-warm 30–90s; see [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration) terminology note).

**Caveats:**

- For recycling pools, `mode_factor` is derived from observed reuse metrics and converges toward `recycle.maxSessionsPerPod` over time. During cold start (no historical data), the controller falls back to `mode_factor = 1.0` (one-session-per-pod sizing) until sufficient samples are collected (default: 100 completed sessions). Once converged, `mode_factor` is bounded above by `recycle.maxSessionsPerPod` (pods cannot serve more sessions than the configured limit). **Integration level consideration:** Because each pool references exactly one runtime (and therefore one integration level), there is no level heterogeneity within a pool, and recycling requires no runtime cooperation ([Execution Modes](#execution-modes)), so no cross-level adjustment is needed. For recycling pools with `preConnect: true`, the between-session SDK re-warm window (up to `sdkConnectTimeoutSeconds`, default 60s) adds to the per-session cycle time (whole-pod scrub + SDK re-warm + potential demotion), reducing effective throughput per pod and lowering the observed `mode_factor` below the configured `recycle.maxSessionsPerPod`. The PoolScalingController should use the observed `lenny_pod_session_reuse_count` p50 (which naturally reflects this overhead) rather than `recycle.maxSessionsPerPod` for such pools.
- For session pools with `maxConcurrentSessions > 1`, the effective `mode_factor` may be lower than the slot-derived expectation if workspace materialization per slot is a bottleneck. The PoolScalingController uses `active_slots / (pod_count × maxConcurrentSessions)` saturation to detect this and adjusts `mode_factor` downward when slot saturation consistently exceeds 0.85.
- For service mode, routing goes through a Kubernetes Service and pod readiness reflects slot availability, so the scaling controller monitors slot saturation directly rather than using the warm pool claim model. **Demand signal source:** Because service mode bypasses the gateway claim model, the PoolScalingController derives `base_demand_p95` from the Prometheus metric `rate(lenny_service_requests_total[5m])` (requests per second arriving at the pool's Service) and `burst_p99_claims` from `max_over_time(lenny_service_concurrent_active[5m])` (peak concurrent active slots). These metrics are emitted by the gateway's tenant-affinity routing layer (see the `service` definition above). The scaling formula uses `mode_factor = maxConcurrent` and `burst_mode_factor = maxConcurrent`, and the saturation signal is `active_slots / (pod_count × maxConcurrent)`.

#### Pool Taxonomy

**Example pools:**

- `claude-worker-sandboxed-small`
- `claude-orchestrator-microvm-medium`

**Pool taxonomy strategy:** Not every runtime × isolation × resource combination needs a warm pool. Use a tiered approach:

- **Hot pools** (minWarm > 0): High-traffic combinations that need instant availability
- **Cold pools** (minWarm = 0, maxWarm > 0): Valid combinations that create pods on demand with documented cold-start latency
- **Disallowed combinations**: Invalid or insecure combinations rejected at pool definition time

This prevents the combinatorial explosion of 3 runtimes × 3 isolation × 3 resource = 27 pools each holding idle pods. In practice, Tier 1 deployments typically need 1-2 hot pools; Tier 2/3 deployments need 3-10 depending on runtime variety and isolation requirements ([Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)).

#### Bootstrap Behavior — Pool Warming at First Deployment

When a pool is first created (or after a full drain), there are zero idle pods. Requests during this window would silently cold-start or fail. Lenny defines explicit bootstrap behavior to surface this state to clients and operators:

**`PoolWarmingUp` condition.** The `SandboxTemplate` CRD carries a `PoolWarmingUp` condition (type: `PoolWarmingUp`, status: `True`/`False`) managed by the WarmPoolController. The condition is set to `True` when:

- The pool's `minWarm > 0`, **and**
- The current `idlePodCount` is 0, **and**
- At least one pod is in the `warming` state (i.e., the controller is actively provisioning pods but none is ready yet).

The condition is cleared to `False` once `idlePodCount >= 1`. The `reason` field carries `Provisioning` while pods are warming, or `Drained` if `idlePodCount == 0` and no pods are in `warming` state (pool is fully empty with no controller activity — an error condition).

**Client-facing `503 Pool Not Ready` response.** When a session creation request targets a pool in `PoolWarmingUp` state, the gateway returns:

```
HTTP 503 Service Unavailable
Retry-After: <estimated_warmup_seconds>
Content-Type: application/json

{
  "error": {
    "code": "RUNTIME_UNAVAILABLE",
    "category": "TRANSIENT",
    "message": "Pool '<pool-name>' is warming up — no idle pods are available yet. Retry after the indicated interval.",
    "retryable": true,
    "details": {
      "poolName": "<pool-name>",
      "poolCondition": "PoolWarmingUp",
      "estimatedReadyIn": <seconds>,
      "podsWarming": <count>
    }
  }
}
```

`Retry-After` is set to `max(30, estimatedWarmupSeconds)` where `estimatedWarmupSeconds` is derived from the `lenny_warmpool_pod_startup_duration_seconds` p50 for the pool's runtime class (falls back to 120s if no historical data is available). Clients that honor `Retry-After` will not hammer the gateway while the pool bootstraps.

**`WarmPoolBootstrapping` alert.** A `WarmPoolBootstrapping` alert fires when `PoolWarmingUp = True` for more than `warmupDeadlineSeconds` (default: 300s) for any pool with `minWarm > 0`. This surfaces bootstrap failures (image pull errors, node pressure, insufficient quota) before they cause sustained session unavailability. The alert links to the warm pool exhaustion runbook ([Section 17.7](17_deployment-topology.md#177-operational-runbooks)).

**Operator visibility.** `GET /v1/admin/pools/<name>` returns `"poolCondition": "PoolWarmingUp"` and `"idlePodCount": 0` during the bootstrap window, giving operators a clear signal without requiring log inspection.

**Topology spread constraints:** Agent pods use `topologySpreadConstraints` to distribute across availability zones and nodes. This follows a two-step propagation consistent with the CRD field ownership table in [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries): the PoolScalingController (which owns `SandboxTemplate.spec`) writes the defaults into `SandboxTemplate.spec.topologySpreadConstraints`; the WarmPoolController (which owns `Sandbox.spec`) then copies those constraints into `Sandbox.spec` when creating or updating agent pods. The defaults set by the PoolScalingController in `SandboxTemplate.spec`:

- `maxSkew: 1`, `topologyKey: topology.kubernetes.io/zone`, `whenUnsatisfiable: ScheduleAnyway` (soft spread across zones)
- `maxSkew: 1`, `topologyKey: kubernetes.io/hostname`, `whenUnsatisfiable: ScheduleAnyway` (soft spread across nodes)

Deployers can override these defaults per pool via the `SandboxTemplate` CRD's `topologySpreadConstraints` field. For pools where zone balance is critical (e.g., high-availability orchestrator pools), deployers should set `whenUnsatisfiable: DoNotSchedule` to enforce strict spread.

### 5.3 Isolation Profiles

Lenny uses standard Kubernetes `RuntimeClass` for isolation:

| Profile     | RuntimeClass | Use Case                                                                                             | Default? |
| ----------- | ------------ | ---------------------------------------------------------------------------------------------------- | -------- |
| `standard`  | `runc`       | Development/testing only — requires explicit deployer opt-in with security acknowledgment            | No       |
| `sandboxed` | `gvisor`     | **Default for all workloads**. Kernel-level isolation prevents container escape via kernel exploits. | **Yes**  |
| `microvm`   | `kata`       | Higher-risk, semi-trusted, or multi-tenant workloads                                                 | No       |

**Security note:** `runc` provides no protection against kernel exploits. Even trusted developers can introduce malicious dependencies. `gvisor` is the minimum recommended isolation for any workload processing untrusted input (which includes all LLM-generated code execution). Deployers must explicitly opt in to `runc` via a pool configuration flag (`allowStandardIsolation: true`).

Each `RuntimeClass` should define `Pod Overhead` so scheduling accounts for the isolation cost. Reference overhead values:

| Profile              | CPU Overhead | Memory Overhead | Notes                            |
| -------------------- | ------------ | --------------- | -------------------------------- |
| `standard` (runc)    | None         | None            | Native container runtime         |
| `sandboxed` (gVisor) | ~200m        | ~200Mi          | gVisor userspace kernel overhead |
| `microvm` (Kata)     | ~500m        | ~500Mi          | VM boot + guest kernel overhead  |

> These are reference values; actual overhead depends on workload and should be tuned per deployment.

A `RuntimeProvider` abstraction keeps the door open for future backends (e.g., KubeVirt).

**Image supply chain controls:**

- Images **must** be pinned by digest (not tag) in Runtime definitions
- Image signature verification via cosign/Sigstore, enforced by a ValidatingAdmissionWebhook (or OPA/Gatekeeper policy). The cosign admission webhook must be configured as **fail-closed** (`failurePolicy: Fail`). If the webhook is unavailable, pod admission is blocked. This prevents unsigned images from being admitted during webhook outages. Alert on webhook unavailability (`CosignWebhookUnavailable`).
- Only images from deployer-configured trusted registries are admitted
- Vulnerability scanning integrated into CI for all runtime images

**Image provenance verification (signing, attestation) is a prerequisite for any production or staging deployment.** While full hardening is Phase 14 in the build sequence, deployers must not run untrusted agent images without provenance controls. At minimum, images should be pulled from a private registry with digest-based references (not mutable tags) starting from Phase 3 (when the warm pool controller begins creating pods).

**RuntimeClass-aware admission policies:** Isolation profile enforcement relies on RuntimeClass-aware admission policy webhooks (OPA/Gatekeeper or Kyverno) deployed in the agent namespaces. These webhooks **must** be configured with `failurePolicy: Fail` (fail-closed) so that if the admission controller is unavailable, pod admission is denied rather than permitted without security constraints. See [Section 17.2](17_deployment-topology.md#172-namespace-layout) for the full admission webhook failure mode specification, minimum-availability SLO, and alerting requirements.

**RuntimeClass validation and dev fallback:**

1. **Controller startup validation.** The warm pool controller validates that the required `RuntimeClass` objects exist in the cluster at startup. If a pool references a `RuntimeClass` that doesn't exist (e.g., `gvisor` on a cluster without gVisor installed), the controller logs an error and sets the pool's status to `Degraded` with a clear message: "RuntimeClass 'gvisor' not found — install gVisor or change the pool's isolation profile."
2. **Helm pre-install hook.** The Helm chart includes a `lenny-preflight` validation Job (see [Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) that checks for required RuntimeClasses and all other infrastructure dependencies before installation proceeds.
3. **Dev mode fallback.** When `global.devMode: true` in the Helm chart (or `LENNY_DEV_MODE=true`), the default isolation profile falls back to `standard` (runc) so developers can run locally without installing gVisor. A warning is logged: "Dev mode: using runc isolation. Do not use in production."
4. **gVisor installation guidance.** For production clusters, install gVisor via the GKE Sandbox (GKE), or the gVisor containerd-shim (`runsc`) on self-managed clusters. See gVisor documentation for installation instructions.

