---
layout: default
title: "Runtime Configuration"
parent: "Runtime Author Guide"
nav_order: 1
---

# Runtime Configuration

This page describes the configuration an author sets on a `Runtime`: the fields of the runtime manifest, what each one controls, its default, and how it behaves when a derived runtime inherits or overrides it. It is the field reference for the manifest you register; the protocol your binary speaks is covered in the [Adapter Contract](../reference/adapter-contract.md), and the exhaustive deployment-level schema is in the [Configuration Reference](../reference/configuration.md).

## What the manifest is

A `Runtime` is the registration record for a runtime image. It is static configuration: an operator registers it through the admin API (see [Publishing](publishing.md#runtime-registration)), and the gateway reads it when it schedules pods and validates sessions. A runtime is not instantiated per session. The manifest sets the runtime's identity, its isolation and execution model, the capabilities its binary implements, the providers and credentials it can use, and the policies and defaults that govern its pods.

A runtime is one of two kinds:

- **Standalone:** the manifest carries an `image`. This is the common case and the path the rest of this page leads with.
- **Derived:** the manifest carries a `baseRuntime` reference instead of an `image`, and customizes a base runtime's workspace, setup, interface, and policy without re-declaring its image or its security-critical fields. See [Derived runtimes](#derived-runtimes) below.

A second axis is the `type` field, which is either `agent` or `mcp`. Most fields apply to both, but the `capabilities` group and `agentInterface` apply only to `type: agent`. The differences are called out per field below and summarized under [Type-specific fields](#type-specific-fields).

## A minimal manifest

The shortest manifest that registers a runtime and lets it handle sessions sets an identity, an image, a type, at least one provider, and labels. Everything else takes a default. The following is an example standalone `type: agent` runtime:

```yaml
name: my-agent
image: registry.example.com/my-agent@sha256:...
type: agent
supportedProviders:
  - anthropic_direct
labels:
  team: default
```

With this manifest the runtime runs one session per pod (`executionMode: session`), under the default isolation profile (`sandboxed`, gVisor), at the default integration level (`basic`). The sections below describe the fields you set when you need to depart from those defaults.

## Field reference

The tables group fields by purpose. For each field, the **Set on** column states whether the value is part of the runtime definition (`author`) or part of a warm pool's configuration (`pool default`, set per pool and overridable per pool). The **Derived** column states how the field behaves on a derived runtime, drawn from the runtime merge rules:

- **Inherited:** always taken from the base; a derived runtime cannot set it.
- **Prohibited:** a derived runtime must not set it; the gateway rejects registration if it is present.
- **Override:** a derived value replaces the base value when present, and the base value applies when absent.
- **Append** or **Merge:** collection and map fields where derived entries are added to or overlaid on the base.

### Identity and image

| Field | Controls | Values / default | Set on | Derived |
| :--- | :--- | :--- | :--- | :--- |
| `name` | The runtime's unique identifier, used by `runtimeSelector`, delegation, and the admin API. | String, required. | author | Each derived runtime has its own `name`. |
| `image` | The container image the pod runs. Must be pinned by digest rather than a mutable tag. | Image reference, required on standalone runtimes. | author | Inherited. A derived runtime never carries its own image. Immutable on the base after registration. |
| `baseRuntime` | The base runtime a derived runtime extends, in place of an `image`. | Runtime name; present only on derived runtimes. | author | N/A. |
| `type` | Whether the runtime participates in the session lifecycle (`agent`) or hosts an MCP server (`mcp`). | `agent` (default) or `mcp`. | author | Prohibited. Must match the base. |
| `labels` | Key-value labels matched by environment `runtimeSelector` and `connectorSelector`. Required from v1. | Map of string to string, required. | author | Merge. Derived keys win on collision; base-only keys are preserved. |
| `minPlatformVersion` | The minimum platform version that may register the runtime. | Semantic version string, optional. The gateway rejects registration on an older platform. | author | Override. |

### Execution and isolation

| Field | Controls | Values / default | Set on | Derived |
| :--- | :--- | :--- | :--- | :--- |
| `executionMode` | How sessions map to pods. See [Execution mode](#execution-mode) below for the full model. | `session` (default) or `service`. | author | Prohibited. Must match the base. |
| `isolationProfile` | The Kubernetes `RuntimeClass` that isolates the pod. | `sandboxed` (gVisor, default), `microvm` (Kata), or `standard` (runc; requires an explicit operator opt-in with a security acknowledgment). | author | Prohibited. Must match the base. |
| `allowedResourceClasses` | The CPU and memory classes a pool may select for the runtime. | List, for example `[small, medium, large]`. A pool's `resourceClass` must fall within this set. | author | Prohibited. A derived runtime's classes must be a subset of the base. |
| `integrationLevel` | The author's declared level of the adapter contract the binary implements. Tooling and admission compare it against the observed level. | `basic` (default), `standard`, or `full`. Valid only on `type: agent`. | author | Inherited. The level is a property of the image. |

The `isolationProfile` values map to isolation backends. `standard` (runc) provides no protection against kernel exploits and is intended for development and testing only. `sandboxed` (gVisor) is the default and the minimum recommended isolation for any workload that processes untrusted input, which includes any LLM-generated code execution. `microvm` (Kata) suits higher-risk, semi-trusted, or multi-tenant workloads and is the only profile that may permit cross-tenant pod reuse.

### Capabilities (`type: agent` only)

The `capabilities` group declares what interaction patterns and platform features the agent binary supports. It is valid only on `type: agent` runtimes; a `type: mcp` runtime has no `capabilities` field. The whole group is **Prohibited** on a derived runtime, because it is a property of the image.

| Field | Controls | Values / default |
| :--- | :--- | :--- |
| `capabilities.interaction` | Whether the runtime handles a single request-response (`one_shot`) or an ongoing dialog with mid-session message delivery and clarification requests (`multi_turn`). | `one_shot` (default) or `multi_turn`. `multi_turn` requires `capabilities.injection.supported: true`. |
| `capabilities.injection.supported` | Whether the runtime accepts mid-session message delivery (injection). The gateway rejects injection attempts against runtimes that do not support it. | `true` or `false` (default). |
| `capabilities.injection.modes` | The injection delivery modes the runtime accepts. | List, for example `[immediate, queued]`. |
| `capabilities.preConnect` | Whether the warm pool pre-connects the agent SDK process during the warm phase (SDK-warm mode). When `true`, the adapter must implement `DemoteSDK`. | `true` or `false` (default). |
| `capabilities.midSessionUpload` | Whether clients may upload files into an active session. The gateway rejects mid-session uploads against runtimes that do not declare it. | `true` or `false` (default). |
| `sdkWarmBlockingPaths` | Glob patterns that, when matched by an uploaded file, demote an SDK-warm pod before use. Meaningful only when `capabilities.preConnect: true`. | List of glob patterns. Default: `["CLAUDE.md", ".claude/*"]`. |

### Providers and credentials

| Field | Controls | Values / default | Set on | Derived |
| :--- | :--- | :--- | :--- | :--- |
| `supportedProviders` | The model providers the runtime can use. | List, for example `[anthropic_direct, aws_bedrock]`. Required to register a usable runtime. | author | Override. A derived runtime may restrict the set but not expand beyond the base. |
| `credentialCapabilities` | What the runtime's credential handling supports: in-place rotation and the proxy dialects its SDK speaks to Lenny's LLM proxy. | `hotRotation: true` or `false`; `proxyDialect` is a list of dialects (for example `[openai, anthropic]`), required when a pool uses `deliveryMode: proxy`, and set to `[]` for direct-only runtimes. | author | Override. |

### Policy and limits

| Field | Controls | Values / default | Set on | Derived |
| :--- | :--- | :--- | :--- | :--- |
| `limits` | Per-session bounds: `maxSessionAge`, `maxUploadSize`, `maxRequestInputWaitSeconds` (the inter-agent clarification timeout), and `maxIdleTimeSeconds` (the idle timeout that expires an inactive session; default 600s). | Object; each member optional. | author | Override. The whole object is replaced when the derived runtime sets it. |
| `setupCommandPolicy` | The allowlist and execution constraints for setup commands. | Object with `mode`, `shell`, `allowlist`, and `maxCommands`. | author | Override. A derived runtime may restrict the allowlist. |
| `setupPolicy` | The timeout and timeout disposition for setup commands. | `timeoutSeconds` (waits indefinitely if absent) and `onTimeout` (`fail` or `warn`). | author | `timeoutSeconds` takes the maximum of base and derived; `onTimeout` overrides. |
| `runtimeOptionsSchema` | A JSON Schema the gateway validates each session's `runtimeOptions` against. | JSON Schema object. | author | Override. A derived schema may only reference property names present in the base schema. |
| `capabilityInferenceMode` | The default tool capability for unannotated MCP tools. | `strict` (default; unannotated tools infer as `admin` and emit a warning) or `permissive` (unannotated tools infer as `write`). | author | Override. A derived runtime may relax to `permissive`. |
| `sessionPolicy` | The pod-occupancy configuration for `session` mode: concurrency, recycling, retirement, scrub, and idle bounds. See [Execution mode](#execution-mode) below. | Object; applies only to `executionMode: session`. | author | Override. The whole object is replaced when the derived runtime sets it. |

Setup commands run once per pod after workspace materialization and before the runtime starts, while the pod is in its init state. Per-session setup belongs in the runtime's own initialization rather than in setup commands. `sessionPolicy` carries the pod-reuse policy for session mode, described under [Execution mode](#execution-mode).

### Workspace and pool defaults

| Field | Controls | Values / default | Set on | Derived |
| :--- | :--- | :--- | :--- | :--- |
| `workspaceDefaults` | Files and setup commands the gateway materializes into every pod before client uploads. | Object with `files` and `setupCommands`. | author | Append. Derived files are appended to the base; a conflicting path is replaced by the derived file. Derived setup commands run after base setup commands. |
| `sharedAssets` | Read-only files populated into `/workspace/shared/` during pod initialization. Meaningful only when a pool runs more than one session per pod. | List of `artifact` or `inline` entries with a destination path. | author | Append. A conflicting destination path is replaced by the derived entry. |
| `defaultPoolConfig` | The pool settings a pool inherits when it does not set its own: warm count, resource class, and egress profile. | Object, for example `warmCount: 5`, `resourceClass: medium`, `egressProfile: restricted`. | author (the per-pool values are pool defaults) | Override. |

Warm-pool fields such as `warmCount`, `resourceClass`, and `egressProfile` are set per pool when an operator attaches a pool to the runtime, and `defaultPoolConfig` supplies their fallback values. See [Pool Configuration](publishing.md#pool-configuration) for how a pool is attached and tuned. Derived runtimes have fully independent pool settings; a pool's resource classes cannot exceed the base runtime's `allowedResourceClasses`.

### Delegation and discovery

| Field | Controls | Values / default | Set on | Derived |
| :--- | :--- | :--- | :--- | :--- |
| `delegationPolicyRef` | The delegation policy that bounds the runtime's child tasks. | Policy name, optional. | author | Override (restrict only). A derived policy must be a subset of the base. |
| `allowSelfRecursion` | Whether the runtime's `(runtime, pool)` identity may repeat in its own delegation lineage. A security boundary. | `true` or `false` (default). | author | Override (restrict only). A derived runtime may set `false` when the base is `true`, but a derived `true` is rejected when the base is `false`. |
| `agentInterface` | The discovery and A2A-card metadata for the runtime: description, input and output modes, skills, and examples. Valid only on `type: agent`. | Object, optional. | author | Override. |
| `publishedMetadata` | Opaque metadata entries the gateway serves without parsing, for example an A2A agent card. Each entry has a key, content type, and a visibility of `internal`, `tenant`, or `public`. | List, optional. | author | Append. A duplicate key is replaced by the derived entry. |

The gateway treats `publishedMetadata` content as opaque pass-through, so validating its contents is the author's responsibility. When `agentInterface` is present, the gateway generates an A2A agent card at registration time and stores it as a `publishedMetadata` entry; an author who wants full control over the card omits `agentInterface` and publishes the card directly.

## Execution mode

The `executionMode` field is either `session` or `service`. The mode name follows the unit of the client contract.

- **`session`** (default): the session is the managed unit. The gateway binds each session to a claimed pod and manages its workspace, lifecycle, and recovery. In the default configuration the pod is exclusive to one session and terminates when the session ends.
- **`service`:** the runtime is a replicated service. The gateway routes each message to any ready, tenant-pinned replica, creates no pod claim, and materializes no workspace. Service mode provides no cross-message conversation continuity; every message is self-contained, and the platform may route successive messages of one session to different replicas. Stateless workloads with no workspace requirement are better registered as external connectors than as service-mode runtimes.

Session mode is parameterized by the `sessionPolicy` block, which `service` mode ignores. The block's two governing fields are `maxConcurrentSessions` (the number of simultaneous sessions a pod may hold) and `recycle.enabled` (whether a pod serves successive sessions with a scrub between them). Their combinations cover the common configurations:

| Configuration | `maxConcurrentSessions` | `recycle.enabled` | Behavior |
| :--- | :--- | :--- | :--- |
| One session per pod (default) | 1 | `false` | The pod is exclusive to a single session and terminates when the session ends. |
| Pod reuse | 1 | `true` | The pod serves sequential sessions of one tenant, with a whole-pod scrub between sessions. |
| Concurrent | N | `true` | The pod serves up to N simultaneous sessions in per-slot workspaces and recycles when occupancy reaches zero. |
| Bounded cohort | N | `false` | The pod serves up to N simultaneous sessions, then terminates after the cohort drains. |

Departing from one session per pod requires explicit acknowledgments, because the alternatives weaken isolation. `recycle.enabled: true` requires `recycle.acknowledgeBestEffortScrub: true`, because the between-session workspace scrub is best-effort and is not a security boundary. `maxConcurrentSessions > 1` requires `sessionPolicy.acknowledgeProcessLevelIsolation: true`, because concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack. A pool that recycles pods or runs more than one session per pod is pinned to a single tenant for its lifetime. Cross-tenant pod reuse is permitted only on the sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`) under `microvm` isolation with `recycle.allowCrossTenantReuse: true`.

When recycling is enabled, set `recycle.maxSessionsPerPod` to the number of sessions a pod serves before it is retired; the field is required and has no default. The block also carries `maxClientIdleSeconds` (the client-inactivity bound, defaulting to the pool's effective maximum session age), `maxSessionRetries` (crash re-dispatch attempts, default 1), and `onPoolExhausted` (`reject`, the default, or `queue` to hold a request in a bounded FIFO until a pod frees).

This subsection summarizes the model so an author can choose a mode and set the policy. The full per-pod claim mechanism, the scrub procedure, and the retirement rules are platform behavior; consult the [Configuration Reference](../reference/configuration.md) and the [Pod Lifecycle](lifecycle.md) page for the exhaustive treatment.

## Type-specific fields

Most fields apply to both runtime types. The following apply only to one:

- **`capabilities` and `sdkWarmBlockingPaths`:** `type: agent` only. A `type: mcp` runtime has no capabilities block, because it has no session lifecycle.
- **`agentInterface`:** `type: agent` only. It signals workspace-file support and supplies discovery metadata.
- **`integrationLevel`:** meaningful only on `type: agent`. The gateway rejects it on a `type: mcp` runtime.

A `type: mcp` runtime hosts an MCP server behind Lenny's pod isolation, credential management, pool scaling, egress control, and audit, and its binary needs to know nothing about Lenny. It still sets identity, image, isolation, providers, credentials, limits, and pool defaults. See [`type: mcp` runtimes](integration-levels.md#type-mcp-runtimes) for how MCP runtimes differ from agents.

## Derived runtimes

A derived runtime references a `baseRuntime` instead of an `image` and customizes the base without re-declaring its security-critical fields. The **Derived** column in each table above states the rule for each field. The fields a derived runtime can independently configure are the pool settings, `workspaceDefaults`, setup commands, `setupPolicy.timeoutSeconds`, `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, and `sessionPolicy`. The fields it can never set are `type`, `executionMode`, `isolationProfile`, `allowedResourceClasses`, `capabilities.interaction`, `capabilities.injection`, and `integrationLevel`; these are inherited or prohibited because they are properties of the base image or its security posture. The gateway rejects registration of a derived runtime that sets a prohibited field, or that widens a restrict-only field beyond the base.

On the base runtime, `image` and `name` are immutable after registration. All other base fields are mutable, with impact validation: a change that would invalidate an existing derived runtime is rejected with the list of affected runtimes.

## Where to read next

- [Publishing](publishing.md) -- registering the manifest through the admin API and attaching a warm pool.
- [Integration Levels](integration-levels.md) -- what the binary must implement at each declared `integrationLevel`.
- [Pod Lifecycle](lifecycle.md) -- how `executionMode` and `sessionPolicy` play out across a pod's life.
- [Configuration Reference](../reference/configuration.md) -- the exhaustive deployment-level schema.
