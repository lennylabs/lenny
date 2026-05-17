# Build progress

This file audits the Lenny implementation against the phased build sequence in
[`spec/18_build-sequence.md`](spec/18_build-sequence.md). It records which phases are
complete, which are partial, and what remains.

Audited 2026-05-16, branch `impl/v1-initial`. First audited at commit `48adf0a`; the
progress log records work since.

## Progress log

Newest first. Each entry is one increment toward the critical path below.

- `97aae93` — §15.1 OpenAPI: `GET /v1/admin/recommendations` documented.
- `4a68370` — §25.3 `GET /v1/admin/recommendations` endpoint. The capacity-recommendations
  endpoint is wired onto the admin Router (platform-admin gated, optional `?category=`
  filter); `cmd/lenny-gateway` serves it over a 7-day `WindowStore`. The store is not
  yet fed by the gateway's metric emission, so the endpoint reports no recommendations
  until that wiring lands — the §25.3 empty-window behaviour.
- `5bb025e` — §25.3 `CapacityService` rule-evaluation engine. The service runs the
  recommendation-rule catalog against a `MetricReader`: each catalog rule is paired with
  a Go evaluator that reads its metrics and applies its threshold, a rule whose metric
  is absent does not trigger, and `GetRecommendations` narrows the result by the
  optional category filter. The §25.3 per-replica ring-buffer feeding (the gateway
  emitting metrics into the `WindowStore`) and the `lenny-ops` Prometheus-backed
  `MetricReader` remain unbuilt.
- `737359d` — §25.3 `MetricReader` and the windowed metric store. New
  `pkg/gateway/recommendations` holds the §25.3 `MetricReader` interface the rules
  evaluate against and `WindowStore` — the in-memory sliding-window metric store that
  backs it on the gateway. `WindowStore` keeps a bounded per-series ring buffer:
  `GaugeValue` / `CounterValue` return the latest sample, `WindowedRate` the per-second
  increase across a trailing window, and `HistogramQuantile` a quantile over the
  retained observations; samples past the retention window are evicted. The sub-second
  downsampling optimisation, the rule-evaluation engine, and the
  `GET /v1/admin/recommendations` endpoint remain unbuilt.
- `66864b7` — §18 Phase 2.5 / §25.3 capacity-recommendation rule substrate
  (`pkg/recommendations/rules`). Ships the typed `Rule` (name, category, PromQL
  condition, sliding window), the `Category` closed enum, and `Validate` (the PromQL
  condition validator plus field checks), shared by the gateway and `lenny-ops`.
  `Catalog` ships a representative sample; the full catalog is populated alongside the
  §25.3 Capacity Recommendations service (the `GET /v1/admin/recommendations` endpoint
  and the per-replica ring-buffer aggregation remain unbuilt).
- `c634504` — §5.1 derived-runtime validation on the runtime update path. A PUT may not
  change `baseRuntime` (so a standalone runtime cannot be converted to derived in
  place), and a PUT against an already-derived runtime may not set the inherited or
  prohibited fields. A violation returns `400 INVALID_DERIVED_RUNTIME`.
- `6911dbd` — §5.1 resolve derived runtimes in the meta endpoints. `GET
  /v1/runtimes/{name}/meta/{key}` and the `/internal` counterpart resolve through
  `runtimestore.Resolve`, so a derived runtime serves the publishedMetadata entries it
  inherits from its base. With this, the §5.1 derived-runtime feature is built end to
  end: model, the `Merge` algorithm, `Resolve`, registration and update validation, and
  resolution at every runtime read site (discovery, models, list_runtimes, the meta
  endpoints, and the message-injection check).
- `cd37e20` — §5.1 resolve derived runtimes in runtime discovery. `GET /v1/runtimes`,
  `GET /v1/models`, and `lenny/list_runtimes` resolve each derived runtime to its
  effective merged definition before the §10.6 environment filter and the discovery
  entry, so a derived runtime is reported (and label-filtered) with the fields it
  inherits from its base. The §9.1 per-runtime meta endpoints still read the declared
  runtime; resolving them is a follow-up.
- `7257dc5` — §5.1 `runtimestore.Resolve` and the derived-runtime injection check.
  `Resolve` returns the effective runtime for a name — the runtime itself when
  standalone, or `Merge(base, derived)` for a derived runtime. The §15.1
  message-injection handler resolves the session's runtime through `Resolve` before the
  injection-support check, so a session on a derived runtime is checked against the
  `injection.supported` it inherits from its base. Other runtime-lookup sites (the §9.1
  discovery surface) still read the declared runtime; resolving them is a follow-up.
- `557c12a` — §5.1 derived-runtime registration validation. The admin runtime create
  handler rejects a derived runtime that declares an inherited or prohibited field
  (`image`, `type`, `executionMode`, `isolationProfile`, `integrationLevel`,
  `capabilities`) or whose `baseRuntime` does not reference an existing standalone
  runtime, with `400 INVALID_DERIVED_RUNTIME`. The §5.1 merge algorithm is single-level,
  so a base that is itself derived is rejected. The PUT-update derived-runtime
  validation is a follow-up.
- `d5ab12b` — §15.1 OpenAPI: `baseRuntime` in the admin `Runtime` schema.
- `38195a1` — §15.1 `baseRuntime` round-trip on the admin runtime API. `POST` / `GET`
  carry it on `RuntimePayload`; `PUT` carries it as an optional string pointer. The §5.1
  derived-runtime registration validation (base existence, the prohibited-field rules)
  is the next step.
- `e32e697` — §5.1 `base_runtime` column (migration 0023). The pgstore persists the
  §5.1 `baseRuntime` reference.
- `cde6fbe` — §5.1 `baseRuntime` field and the derived-runtime merge algorithm.
  `runtimestore.Runtime` gains the §5.1 `baseRuntime` reference (`Runtime.IsDerived`
  reports a derived runtime). `runtimestore.Merge` implements the §5.1 normative merge
  algorithm that resolves a derived runtime against its base into the effective
  runtime: inherited security fields are taken from the base, override fields take the
  derived value when set, `publishedMetadata` appends with a duplicate key won by
  derived, `labels` overlay, and `setupPolicy.timeoutSeconds` takes the maximum. The
  result shares no mutable state with either input. The derived-runtime registration
  validation and the session-creation resolution that call `Merge` are the next steps.
- `f27ee5a` — §15.1 OpenAPI: `taskPolicy` in the admin `Runtime` schema.
- `09069c9` — §15.1 `taskPolicy` round-trip on the admin runtime API. `POST` / `GET`
  carry it on `RuntimePayload`; `PUT` carries it as an optional pointer. Create and
  update validate the `microvmScrubMode` / `onCleanupFailure` enums and the
  non-negative numeric fields; the §5.1 cross-field rules stay with the pool controller.
- `6b3aa09` — §5.1 `taskPolicy` runtime field. `runtimestore.Runtime` gains the §5.1
  `taskPolicy` block — the §5.2 task-mode pod-reuse and workspace-cleanup policy
  (best-effort-scrub acknowledgment, cross-tenant reuse, microvm scrub mode, cleanup
  commands, pod retirement limits, the §6.6 per-task retry budget). `MicrovmScrubMode`
  and `CleanupFailureDisposition` are closed enums; the in-memory store deep-copies the
  command slice and retry pointer and the pgstore persists the block (migration 0022).
  With `taskPolicy` the §5.1 RuntimeDefinition advanced-field model is complete:
  `agentInterface`, `publishedMetadata`, `capabilityInferenceMode`,
  `toolCapabilityOverrides`, `setupPolicy`, `capabilities`, `minPlatformVersion`, and
  `taskPolicy` all round-trip through the model, pgstore, and the §15.1 admin API.
- `a7a940d` — §15.1 OpenAPI: `minPlatformVersion` in the admin `Runtime` schema.
- `86dae6a` — §15.1 `minPlatformVersion` round-trip and registration gate. `POST` / `GET`
  carry it on `RuntimePayload`; `PUT` carries it as an optional string pointer. Per §5.1
  create and update reject registration when `minPlatformVersion` is not a valid
  version or the running gateway's version is below it; the floor check is skipped for
  a dev build whose version does not parse.
- `a2a984a` — §5.1 `minPlatformVersion` runtime field. `runtimestore.Runtime` gains the
  §5.1 `minPlatformVersion` field; the pgstore persists it (migration 0021,
  `min_platform_version` text column).
- `1eb941f` — `pkg/gateway/semver` shared version comparator. The MAJOR.MINOR.PATCH
  comparator `agentcard.NeedsRegen` used privately is extracted to a shared package so
  the runtime `minPlatformVersion` registration check can reuse it; `agentcard` now
  consumes `semver.Compare`.
- `88393ef` — §15.1 OpenAPI: `capabilities` in the admin `Runtime` schema and the
  `403 INJECTION_REJECTED` response on `POST /v1/sessions/{id}/messages`.
- `98d9151` — §15.1 injection rejected against unsupported runtimes. `POST
  /v1/sessions/{id}/messages` rejects mid-session injection with `403
  INJECTION_REJECTED` when the session's runtime does not declare
  `capabilities.injection.supported: true` (§5.1 default false). The check degrades
  safely when the runtime registry is not wired.
- `fae2758` — §15.1 `capabilities` round-trip on the admin runtime API. `POST` / `GET`
  carry the block on `RuntimePayload`; `PUT` carries it as an optional pointer. Create
  and update validate the interaction and injection-mode enums and the §5.1 coherence
  rule that a `multi_turn` runtime must declare `injection.supported: true`.
- `d8a29ee` — §5.1 `capabilities` block on the runtime model. `runtimestore.Runtime`
  gains the §5.1 `capabilities` block — the interaction model (`one_shot` /
  `multi_turn`) and the mid-session injection support (`injection.supported`,
  `injection.modes`). `Runtime.InjectionSupported` is the §5.1 default-false injection
  gate. The in-memory store deep-copies the injection modes; the pgstore persists the
  block (migration 0020, `capabilities` jsonb column). With this the §5.1
  capabilities-driven injection rejection is enforced end to end.
- `df9d053` — §15.1 OpenAPI: `setupPolicy` in the admin `Runtime` schema.
- `27bec28` — §15.1 `setupPolicy` round-trip on the admin runtime API. `POST` / `GET`
  carry it on `RuntimePayload`; `PUT` carries it as an optional pointer. Create and
  update validate the non-negative timeout and the fail / warn `onTimeout` enum.
- `f79b05b` — §5.1 `setupPolicy` runtime field. `runtimestore.Runtime` gains the §5.1
  `setupPolicy` block (the pod setup-phase cap `timeoutSeconds` and the `onTimeout`
  fail / warn disposition); the in-memory store copies the struct and the pgstore
  persists it (migration 0019, `setup_policy` jsonb column). The adapter consuming the
  policy at the pod setup phase is the remaining infra-coupled wiring.
- `a9c60c6` — §15.1 OpenAPI: `toolCapabilityOverrides` in the admin `Runtime` schema.
- `2350844` — §15.1 `toolCapabilityOverrides` round-trip on the admin runtime API. `POST`
  / `GET` carry the map on `RuntimePayload`; `PUT` carries it as an optional map
  pointer. Create and update validate it through `capabilityinference.ValidateOverrides`.
- `b518911` — §5.1 `toolCapabilityOverrides` runtime field. `runtimestore.Runtime` gains
  the §5.1 `toolCapabilityOverrides` map (explicit per-tool §5.3 capability set); the
  in-memory store deep-copies the map of slices and the pgstore persists it (migration
  0018, `tool_capability_overrides` jsonb column).
- `f76aa30` — §5.1 `capabilityinference.Resolve` with toolCapabilityOverrides. The
  override-aware resolution: a tool with a `toolCapabilityOverrides` entry uses that
  explicit capability set and inference does not run, otherwise `Infer` applies the
  MCP-annotation table. `ValidateOverrides` and `Capability.IsValid` expose the closed
  §5.3 capability enum. With this the §5.1 capability-determination model and logic are
  complete; the live `tools/list` read at registration is the remaining infra-coupled
  wiring.
- `fc75e8e` — §15.1 OpenAPI: `capabilityInferenceMode` in the admin `Runtime` schema.
- `1c758af` — §15.1 `capabilityInferenceMode` round-trip on the admin runtime API. `POST`
  / `GET` carry it on `RuntimePayload`; `PUT` carries it as an optional string pointer.
  Create and update validate it against the strict / permissive enum.
- `3ab3020` — §5.1 `capabilityInferenceMode` runtime field. `runtimestore.Runtime` gains
  the §5.1 `capabilityInferenceMode` field; `ApplyDefaults` fills the strict default and
  the pgstore persists it (migration 0017, `capability_inference_mode` text column).
- `78c388f` — §5.1 capability inference from MCP ToolAnnotations
  (`pkg/gateway/capabilityinference`). The inference table maps a tool's MCP annotations
  onto its §5.3 capability set (read-only → `read`, not-read-only → `write`, destructive
  → `write` + `delete`, open-world → `network`); an annotation-free tool takes the
  `capabilityInferenceMode` default (strict → `admin` with the §5.1 registration WARN,
  permissive → `write`). The live `tools/list` read at connector / `type:mcp` runtime
  registration that feeds the inferrer with real tool annotations is the remaining
  wiring; it is infra-coupled.
- `9e44abd` — §15.1 OpenAPI: `POST /v1/admin/runtimes/regenerate-cards` documented with
  its `generatorVersionBefore` / `dryRun` body and the regenerated / skipped / errors
  response.
- `c2777da` — §5.1 `POST /v1/admin/runtimes/regenerate-cards` bulk card regeneration.
  Iterates every runtime carrying an `agentInterface` and regenerates the `agent-card`
  entry; a runtime without an `agentInterface` is skipped, leaving a hand-crafted card
  untouched. `generatorVersionBefore` filters to runtimes whose stored card is strictly
  older; `dryRun` reports the affected runtimes without writing. Platform-admin gated.
- `3b6d948` — §5.1 `agentcard.NeedsRegen` version-threshold check. The regenerate-cards
  threshold rule with a numeric `MAJOR.MINOR.PATCH` comparator (so `1.9.0` sorts below
  `1.10.0`); an empty threshold regenerates all, an empty or unparseable stored version
  sorts oldest.
- `bd64d9e` — §5.1 write-time A2A agent-card auto-generation. The admin runtime create
  and update handlers generate the §5.1 A2A agent card whenever the runtime carries an
  `agentInterface` and store it as the `agent-card` publishedMetadata entry, replacing
  any prior entry with that key. A runtime with no `agentInterface` is left alone, so a
  hand-crafted `agent-card` entry is preserved.
- `545e579` — §5.1 A2A agent card generator (`pkg/gateway/agentcard`). `Generate` maps a
  runtime's `agentInterface` (description, input/output modes, skills, examples) onto an
  A2A-shaped card and injects the two §5.1 envelope fields `generatedAt` (RFC 3339) and
  `generatorVersion`. `Entry` returns the card as a public `agent-card` publishedMetadata
  entry.
- `f2973f0` — §15.1 OpenAPI: `GET /internal/runtimes/{name}/meta/{key}` documented as a
  BearerAuth-gated path.
- `380fa06` — §5.1 `GET /internal/runtimes/{name}/meta/{key}` internal/tenant fetch. The
  endpoint requires an authenticated session principal: an internal-visibility entry is
  served to any authenticated caller, and a tenant-visibility entry only when the
  caller's tenant holds a §4 runtime tenant-access grant. A missing principal, a
  missing or soft-deleted runtime, a missing key, a public entry, and an unreachable
  tenant entry all return an identical 404; the tenant check fails closed when the
  tenant-access registry is not wired. `cmd/lenny-gateway` shares one
  `tenantaccessstore` between the admin tenant-access endpoints and this endpoint. This
  completes the §5.1 publishedMetadata meta-fetch surface.
- `d1ae44a` — §15.1 OpenAPI: `PublishedMetadataEntry` / `PublishedMetadataRef` component
  schemas, referenced from the admin `Runtime` body and the `GET /v1/runtimes`
  discovery item, plus the `GET /v1/runtimes/{name}/meta/{key}` path.
- `97f5ffc` — §15 `publishedMetadata` refs on runtime discovery. `GET /v1/runtimes` and
  `lenny/list_runtimes` carry per-runtime `publishedMetadata` refs (key, content type,
  visibility — not content). The unauthenticated discovery surface carries only the
  public refs; internal and tenant refs surface with the `/internal` discovery-auth
  path.
- `4239260` — §5.1 `GET /v1/runtimes/{name}/meta/{key}` public publishedMetadata fetch.
  Serves an entry only when its visibility class is public, returning the content
  opaquely under its declared content type. A missing runtime, a soft-deleted runtime,
  a missing key, and a non-public entry all return an identical 404 (§5.1 no
  enumeration). The `/internal/runtimes/{name}/meta/{key}` endpoint for internal and
  tenant visibility is the remaining half.
- `91539f8` — §15.1 `publishedMetadata` round-trip on the admin runtime API. `POST` /
  `GET` carry the list on `RuntimePayload`; `PUT` carries it as an optional slice
  pointer so an omitted key leaves it unchanged and an empty array clears it. Create
  and update validate the list (non-empty unique keys, valid visibility class).
- `a52ee89` — §5.1 `published_metadata` jsonb column (migration 0016). The §5.1
  `publishedMetadata` list persists to a nullable jsonb column; an empty list stores as
  SQL NULL. The pgstore contract test gains a round-trip subtest.
- `186fae9` — §5.1 `publishedMetadata` list on the runtime model. `runtimestore.Runtime`
  gains the §5.1 `publishedMetadata` field — the runtime's named, opaque metadata
  entries (agent cards, OpenAPI specs, cost manifests). `PublishedMetadataEntry` carries
  a key, content type, visibility class, and opaque content; `MetadataVisibility` is the
  closed `internal` / `tenant` / `public` enum. `ValidatePublishedMetadata` enforces
  non-empty unique keys and a valid visibility class. The A2A agent-card
  auto-generation and the bulk-regeneration endpoint remain unbuilt.
- `4af8de2` — §15.1 OpenAPI: `AgentInterface` component schema (with its mode / skill /
  example sub-schemas) referenced from the admin `Runtime` schema and the
  `GET /v1/runtimes` discovery item.
- `95f3271` — §9.1 `agentInterface` surfaced on runtime discovery. `GET /v1/runtimes`
  and `lenny/list_runtimes` carry the per-runtime §5.1 `agentInterface` descriptor.
  The §9.1 `mcpEndpoint` per-runtime field for `type:mcp` runtimes is the remaining
  discovery block.
- `6dc4b8b` — §15.1 `agentInterface` round-trip on the admin runtime API. `POST` / `GET`
  carry the descriptor on `RuntimePayload`; `PUT` carries it as a raw message so an
  omitted key leaves it unchanged, JSON null clears it, and an object replaces it. Per
  §5.1 a `type:mcp` runtime carries no `agentInterface`, so create and update reject
  the field with `400 VALIDATION_ERROR` when the type is `mcp`.
- `ff4a690` — §5.1 `agent_interface` jsonb column. The §5.1 `agentInterface` descriptor
  persists to a new nullable `agent_interface` jsonb column (migration 0015); a nil
  descriptor stores as SQL NULL. The pgstore contract test gains an `agentInterface`
  round-trip subtest.
- `3f92006` — §5.1 `agentInterface` descriptor on the runtime model. `runtimestore.Runtime`
  gains the optional §5.1 `agentInterface` field — the structured descriptor a
  `type:agent` runtime declares for discovery, A2A agent-card auto-generation, and
  adapter manifest summaries. The `AgentInterface` struct and its mode / skill /
  example sub-types mirror the §5.1 YAML shape, which §15 names the normative contract.
  The in-memory store deep-copies it through `AgentInterface.Clone`. The §5.1 A2A
  agent-card auto-generation and the `publishedMetadata` store remain unbuilt.
- `204ada6` — §9.1 `adapterCapabilities` on `lenny/list_runtimes`. The MCP discovery
  tool embeds the MCP adapter's capability block (`/mcp`, protocol `mcp`, session
  continuity / delegation / elicitation / interrupt all supported).
- `0c29621` — §15.1 OpenAPI: `adapterCapabilities` component schema referenced from the
  `GET /v1/runtimes` and `GET /v1/models` response bodies.
- `d74865b` — §9.1 `adapterCapabilities` discovery block. New `pkg/gateway/adapter`
  holds the §15 `AdapterCapabilities` type; `GET /v1/runtimes` and `GET /v1/models`
  embed a top-level `adapterCapabilities` block describing the REST adapter (`/v1`,
  protocol `rest`, session continuity / interrupt / elicitation supported, delegation
  absent — no REST delegate route). §9.1 requires this block on every discovery
  response so a consumer can inspect `supportsElicitation` before an
  elicitation-dependent workflow. The §9.1 per-runtime `agentInterface` / `mcpEndpoint`
  fields still need a fuller runtime record.
- `50af1f8` — §15.1 OpenAPI: `GET /v1/models` documented with its OpenAI-compatible
  model-list response schema.
- `4998c8c` — §9.1 `GET /v1/models` model discovery. The OpenAI Completions / Open
  Responses model-discovery surface lists the runtime registry as OpenAI-compatible
  model objects (`{"object":"list","data":[...]}`), each runtime surfaced as a model.
  Results reuse the §10.6 environment-access filter `GET /v1/runtimes` applies.
- `cef45cd` — §9.1 `lenny/list_runtimes` MCP discovery tool. The MCP-surface
  counterpart of `GET /v1/runtimes`; covers every runtime type (unlike
  `discover_agents`, which is type:agent-only) and is identity-filtered by §10.6
  environment access.
- `3bf40f1` — §15.1 OpenAPI: `GET /v1/runtimes` documented with its discovery-entry
  response schema.
- `df6c5d8` — §9.1 `GET /v1/runtimes` runtime discovery. The REST discovery endpoint
  lists the runtime registry, identity-filtered by §10.6 environment access via the
  `envaccess` resolver. `cmd/lenny-gateway` wires the runtime and environment
  registries into the sessionserver. The §9.1 `agentInterface` / `mcpEndpoint` /
  `adapterCapabilities` response fields need a fuller runtime record and are deferred.
- `25fdf00` — §15.1 OpenAPI: explicit-environment session endpoint and field sync. Adds
  `POST /v1/environments/{name}/sessions` and the `environment` / runtime `labels`
  fields to the `Session`, `CreateSessionRequest`, and `Runtime` schemas.
- `2097571` — §10.6 explicit-environment session endpoint. `POST
  /v1/environments/{name}/sessions` creates a session pinned to the path environment;
  `handleCreate` and the new handler share an extracted `createSession` core. The
  `/mcp/environments/{name}` explicit MCP path remains unbuilt.
- `8da4ca5` — §10.6 cross-environment membership verification (security fix). The
  cross-environment delegation check previously trusted the parent session's
  caller-supplied `Environment` field; `crossEnvReachable` now confirms the caller is a
  genuine member of that environment before honoring its cross-environment
  declarations, closing a path where a caller could borrow an environment's reach by
  tagging a session with it.
- `10282d0` — §15.1 OpenAPI: §4 runtime/pool tenant-access endpoints. With these, the
  embedded `openapi.json` covers every admin route the gateway registers.
- `bb1df75` — §15.1 OpenAPI document completion. Adds the remaining undocumented admin
  endpoints — the credential-pool and delegation-policy CRUD, the tenant
  elicitation-content-integrity GET/PUT, and the compliance-profile decommission. The
  embedded `openapi.json` now covers the admin API the gateway serves.
- `8b43a66` — §15.1 OpenAPI document sync. The embedded `openapi.json` gains path
  entries for the custom-role CRUD, the tenant `rbac-config` endpoint, the tenant
  `access-report`, and the environment `runtime-exposure` endpoint — all served by the
  gateway but previously absent from the document SDK and MCP-tool generators consume.
- `fa82e9c` — §10.6 `delegation.isolation_violation` audit event. `lenny/delegate_task`
  emits the event through a new optional `DelegationAuditor` dep when the delegation
  service reports a SEC-001 violation, recording the parent/child isolation profiles
  and the `cross_environment` flag. `cmd/lenny-gateway` adapts its audit sink.
- `06ad182` — §10.6 `ISOLATION_MONOTONICITY_VIOLATED` reason on `lenny/delegate_task`.
  A delegation refused for a §8.3 isolation-monotonicity violation now leads its error
  with the §10.6 reason token.
- `cb76325` — §8.3 / §10.6 `delegate_task` child-pool isolation. The tool resolves the
  child pool's §5.3 isolation profile (new optional `Pools` dep) and hands it to the
  delegation service, so the §8.3 monotonicity check evaluates the child pool rather
  than the inherited parent profile — a delegation to a weaker-isolation pool is now
  rejected. `cmd/lenny-gateway` wires the pool registry.
- `9b4b270` — §10.6 `target_not_in_scope` reason on `lenny/delegate_task`. A delegation
  refused because its target is outside the effective delegation scope now leads its
  error with the §10.6 reason token.
- `57e876e` — §10.6 session environment persisted in Postgres. Migration 0014 adds the
  `environment` column to `sessions`; the session pgstore writes and reads it. The
  field had been in-memory-only.
- `ae45aef` — §10.6 cross-environment delegation bilateral check.
  `envaccess.CrossEnvironmentReachable` implements the §10.6 bilateral algorithm — a
  target is reachable when a peer environment admits it and both environments declare
  reciprocal outbound/inbound rules. `lenny/delegate_task` consults it for the parent
  session's environment, additively widening the delegation scope beyond the caller's
  own environment. The §10.6 cross-environment isolation-monotonicity step is not yet
  enforced.
- `5003c74` — §10.6 environment on `lenny/create_session`. The platform MCP
  session-creation tool records the environment, so every session-creation surface
  now carries the §10.6 session environment context.
- `3f3ec85` — §10.6 session environment on the two-step start path. `POST
  /v1/sessions/start` now records the environment on the session row, matching
  `POST /v1/sessions`.
- `53a1dab` — §10.6 session environment context. `sessionstore.Session` gains an
  `Environment` field; `POST /v1/sessions` accepts it and `GET /v1/sessions/{id}`
  echoes it. This is the session-side anchor for §10.6 cross-environment delegation,
  whose bilateral checks resolve against the calling session's environment. The
  `lenny/create_session` tool and the session Postgres column do not yet carry the
  field.
- `62dfa18` — §10.6 environment scoping on `lenny/delegate_task`. The tool rejects a
  delegation whose `runtimeRef` is outside the caller's environment scope, closing the
  bypass where a hard-coded reference reached a runtime `discover_agents` would hide.
  `runtimeAuthorizedForCaller` reuses the `envaccess` resolver. Cross-environment
  delegation (the §10.6 bilateral checks) stays unbuilt — it needs session environment
  context, which the session row does not carry.
- `0ad15c1` — §10.6 transparent filtering activated in `cmd/lenny-gateway`. The gateway
  wires the environment and tenant stores into the mcptools deps, so
  `lenny/discover_agents` filters in the running gateway. A `--no-environment-policy`
  flag carries the platform-wide `noEnvironmentPolicy` — dev mode derives allow-all,
  and outside dev mode an unset or invalid value is a fatal startup error.
  `mcptools.Deps.DefaultNoEnvironmentPolicy` resolves the per-tenant value first and
  the platform default second, matching §10.6 precedence.
- `e78f57a` — §10.6 `lenny_noenvironmentpolicy_allowall_total` metric. `gatewaymetrics`
  registers the counter; `PUT /v1/admin/tenants/{id}/rbac-config` increments it
  (labelled by `tenant_id`) on an allow-all write through the admin Router's new
  optional `RBACConfigMetrics` dependency, which `cmd/lenny-gateway` wires.
- `b42d882` — §10.6 transparent filtering on `lenny/discover_agents`. The tool narrows
  its agent list to the runtimes the caller's environment membership authorizes,
  resolved through `pkg/gateway/envaccess` from the request principal's groups, the
  tenant's environments, and the tenant `noEnvironmentPolicy`. Filtering is conditional
  on the new optional `Environments` / `Tenants` mcptools deps; without them the list
  is unchanged. `cmd/lenny-gateway` does not yet wire those deps — production
  activation should land with the §10.6 platform-level `global.noEnvironmentPolicy`
  Helm value and its fatal-if-unset startup check, which are not built.
- `338ea03` — §10.6 tenant `rbac-config` endpoint. `GET` / `PUT
  /v1/admin/tenants/{id}/rbac-config` store `noEnvironmentPolicy` on the tenant;
  deny-all is the platform default and an omitted value is treated as deny-all.
  Setting allow-all attaches the §10.6 advisory `Warning` response header. This is the
  stored policy the §10.6 transparent-filtering resolver consumes. The other
  RBAC-config fields (`identityProvider`, `tokenPolicy`, `capabilities`,
  `mcpAnnotationMapping`), the allow-all metric counter, and the tenant Postgres
  column are not yet built.
- `82f4167` — §10.6 environment access resolver (`pkg/gateway/envaccess`). `Membership`
  reports a caller's environment role by matching member identities against the
  caller's OIDC groups or subject; `AuthorizedRuntimes` computes the transparent-filter
  view — the union of every member environment's `runtimeSelector` matches, with the
  `noEnvironmentPolicy` deny-all / allow-all fallback for callers in no environment.
  The wiring into the user-facing runtime-discovery path, and the tenant
  `noEnvironmentPolicy` stored field, are the remaining steps.
- `aaa93c9` — §10.6 OIDC groups claim. `jwt.Claims` and the middleware `Principal` gain
  a `Groups` field; the Bearer path copies the JWT `groups` claim and the dev-header
  path parses `X-Lenny-Groups` under `AllowDevRoles` (dropped in production, like
  `X-Lenny-Roles`). This is the authentication-layer prerequisite for §10.6 environment
  membership — transparent filtering and cross-environment delegation resolve a
  caller's groups against environment member lists, which is the consuming step.
- `24455a5` — §10.6 / §15.1 tenant cross-environment `access-report` endpoint.
  `GET /v1/admin/tenants/{id}/access-report` aggregates every environment's §10.6
  member list into an access matrix keyed by identity: `environments` are the columns,
  each entry is one identity's row of per-environment roles. Identities are reported
  as declared; OIDC-group expansion is a separate concern. Gated on
  `PermManageEnvironments`.
- `d01ca2e` — §10.6 / §15.1 environment `runtime-exposure` endpoint.
  `GET /v1/admin/environments/{name}/runtime-exposure` evaluates the environment's
  `runtimeSelector` against the runtime registry and its `connectorSelector` against
  the connector registry through `pkg/environment`, returning the runtimes and
  connectors in scope. The §10.6 Include / Exclude overrides, type filter, and
  matchExpressions all apply. Gated on `PermManageEnvironments`.
- `df68410` — §5.1 runtime label set. `runtimestore.Runtime` gains a `Labels` map; §5.1
  requires labels from v1 as the matching mechanism for environment `runtimeSelector`
  (§10.6), and the model carried none. The in-memory store deep-copies the map through
  a `cloneRuntime` helper, the Postgres store persists it as a jsonb column (migration
  0013), and the §15.1 admin runtime API round-trips it on create, read, and update.
  This is the prerequisite for the §10.6 environment `runtime-exposure` endpoint.
- `61f8056` — §10.2 view_usage gating fix. `GET /v1/usage` carried no authorization
  check, so the `user` role could read the usage report; `GET /v1/metering/events`
  gated on a role list that omitted `tenant-viewer`, which §10.2 names for the usage
  endpoints. Both now gate on the view_usage permission through the new
  `auth.RolesGrant`, admitting platform-admin, tenant-admin, tenant-viewer, and
  billing-viewer and rejecting `user`. `principalGrantsPermission` reuses `RolesGrant`.
- `cc8744b` — §10.2 custom-role enforcement for the custom-role admin endpoints. The
  `/v1/admin/tenants/{id}/roles` routes are gated on `PermManageRBACConfig`, the
  permission the matrix names "Configure tenant RBAC config". `authorizeTenantPath`
  became a pure path-tenant resolver, consistent with `authorizedTenantForUser`.
- `b8f0995` — §10.2 custom-role enforcement for environments and credential pools. New
  `Router.requirePermission` gate factory; the environment and credential-pool routes
  are gated on `PermManageEnvironments` and `PermManageCredentialPools`. The built-in
  admitted set is unchanged from `requireTenantResourceAdmin`; the gate additionally
  admits a tenant custom role holding the permission.
- `57fc1ec` — §10.2 custom-role enforcement for user management. `requireUserAdmin`
  resolves a principal's roles to the permission set (`Router.principalGrantsPermission`:
  built-in roles via `auth.RolePermissions`, custom roles via the `customrolestore`
  registry) and admits any principal granting `PermManageUsers`, so a tenant custom
  role with `manage_users` can manage users. `authorizedTenantForUser` is now a pure
  tenant resolver, confining a custom-role principal to its own tenant, with
  authorization left to the route gate.
- `4c54d71` — §10.2 built-in-role permission matrix. `auth.RolePermissions` returns the
  permission-matrix row for each of the five built-in roles, and `TenantAdminPermissions`
  now derives from `RolePermissions(RoleTenantAdmin)` rather than duplicating the list.
  The read-only matrix cells for the manage categories contribute no permission; the
  permission set models manage-level operations plus the explicit session-read
  categories. A non-built-in role name resolves to nil, since a tenant custom role
  draws its permissions from the custom-role registry. This is the resolution
  foundation for permission-based authorization gating.
- `b6f5647` — tree-wide `gofmt` pass. 27 files had drifted out of `gofmt` compliance
  on import ordering and struct-tag alignment; reformatted with no semantic change.
- `798aa5a` — §10.2 custom-role assignment to users. `userstore.Memory`'s role
  validation predated custom roles and rejected every non-built-in name in
  `User.Roles`, so a user could not hold a tenant custom role. The storage layer
  cannot resolve custom-role existence (the registry is keyed by tenant and reachable
  only from the admin layer), so it now validates role-name syntax through the new
  `auth.IsWellFormedRoleName`. An unknown but well-formed role in storage is
  fail-safe, granting no permissions. `Router.validateRoleNames` resolves each role
  against the built-in set and the tenant's custom-role registry, and
  `handleCreateUser` / `handleUpdateUser` reject an unregistered role with
  `400 VALIDATION_ERROR`. When the custom-role registry is not wired, only built-in
  roles are accepted. This completes the `e8375d8` follow-up: the role-deletion
  dependents guard now fires against the real store.
- `e8375d8` — §10.2 / §15.1 custom-role admin CRUD. `POST` / `GET` / list / `PUT` /
  `DELETE /v1/admin/tenants/{id}/roles` are wired onto the admin Router, gated on
  platform-admin or tenant-admin; the tenant is taken from the path and a tenant-admin
  is confined to their own. Create/update enforce the §10.2 subset rule; `DELETE` runs
  the `RESOURCE_HAS_DEPENDENTS` guard against assigned users. Follow-up:
  `userstore.Memory`'s role validation predates §10.2 custom roles and rejects
  custom-role names in `User.Roles`; relaxing it (a well-formed-role-name check, with
  custom-role existence verified at the role-assignment endpoint) lets the guard fire
  against the real store.
- `b91da25` — §10.2 custom-role registry (`pkg/gateway/customrolestore`). The
  tenant-scoped store keyed by `(tenant_id, name)`; `Validate` enforces that every
  permission is within the tenant-admin set and the name does not collide with a
  built-in role.
- `0c5754b` — §10.2 RBAC permission model. `pkg/auth.Permission` is the closed 19-entry
  operation set from the §10.2 permission matrix; `TenantAdminPermissions` returns the
  16 the tenant-admin column holds — the ceiling a custom role may not exceed.
- `5c46127` — §8.3 DelegationPolicy contentPolicy defaults. `ApplyDefaults` fills the
  §8.3 ceilings (`maxInputSize` 128 KiB, `maxExportedFileSize` 10 MiB) on a policy whose
  size fields were left zero; the admin create/update handlers call it.
- `fc80b34` — §4 tenant-scoped runtime/pool reads. `GET /v1/admin/runtimes` and
  `/v1/admin/pools` (list and by-name) now admit a tenant-admin and filter the result to
  the resources granted to their tenant through the `runtime_tenant_access` /
  `pool_tenant_access` join tables; a platform-admin is unfiltered, and a by-name read
  of an ungranted resource is `404`. `tenantaccessstore` gains `ListForTenant`; an
  unwired access store fails closed (a tenant-admin sees nothing).
- `adf6f77` — §8.3 / §16.6 scanExportedFiles transition events. `PUT
  /v1/admin/delegation-policies/{name}` emits `delegation_policy.export_scan_weakened`
  on a `true → false` transition (carrying `cooldown_seconds`) and
  `delegation_policy.export_scan_strengthened` on `false → true`; no event fires when
  the value is unchanged. The cooldown enforcement at `delegate_task` time
  (`INTERCEPTOR_WEAKENING_COOLDOWN`) is deferred to the delegation request path.
- `a0b3259` — §4 / §15.1 runtime/pool tenant-access endpoints. The six §15.1
  endpoints — `POST` / `GET /v1/admin/{runtimes,pools}/{name}/tenant-access` and
  `DELETE .../tenant-access/{tenantId}` — are wired onto the admin Router,
  platform-admin gated. The grant is idempotent (201 new, 200 existing); the list
  resolves grantee display names; a revoke of an absent grant is 404. The runtime/pool
  list-endpoint filtering that consults these join tables is a follow-up.
- `8cff3bd` — §4 runtime/pool tenant-access registry (`pkg/gateway/tenantaccessstore`).
  The join-table store behind `runtime_tenant_access` / `pool_tenant_access`: `Grant`
  (idempotent), `Revoke`, and `List`, scoped by `(kind, resource)`.
- `a422cdc` — §4.9 / §15.1 CredentialPool admin CRUD. `POST` / `GET` / list / `PUT` /
  `DELETE /v1/admin/credential-pools` are wired onto the admin Router, gated on
  platform-admin or tenant-admin and tenant-scoped: a tenant-admin operates on their own
  tenant, a platform-admin specifies `tenantId`. `assignmentStrategy` is validated
  against the §4.9 enum; PUT is a full replace of the mutable fields. The §4.9 Token
  Service RBAC live-probe on create and the revoke / re-enable endpoints are deferred —
  both need the gateway-to-Token-Service mTLS link and the lease store.
- `d8a2abf` — §4.9 CredentialPool registry (`pkg/gateway/credentialpoolstore`). The
  tenant-scoped store for the §4.9 CredentialPool resource — a named, per-provider
  credential set with an assignment strategy and lease limits, keyed by
  `(tenant_id, name)`. `Validate` enforces the §4.9 structural invariants.
- `279305b` — §8.3 DelegationPolicy deletion guard. The Runtime resource gains
  `delegationPolicyRef`, settable through `POST` / `PUT /v1/admin/runtimes`;
  `DELETE /v1/admin/delegation-policies/{name}` rejects the delete with
  `409 RESOURCE_HAS_DEPENDENTS` when an active runtime references the policy, listing
  the runtime dependents in `details.dependents`. The active-lease half of the guard is
  deferred — delegation leases are not enumerable from the admin Router.
- `aa69a1a` — §8.3 tag-matching policy evaluation (`Target.Matches`,
  `DelegationPolicy.Evaluate`): the rule set is an allow-list with deny-overrides
  precedence, per the §8.3 least-privilege discipline.
- `f483c9e` — §8.3 / §15.1 DelegationPolicy admin CRUD. `POST` / `GET` / list / `PUT` /
  `DELETE /v1/admin/delegation-policies` are wired onto the admin Router, platform-admin
  gated. The wire payload mirrors the §8.3 resource (tag-matched allow/deny rules, the
  contentPolicy, `allowSelfRecursion`); a policy whose `scanExportedFiles` is set without
  an `interceptorRef` is rejected `400 EXPORT_SCAN_REQUIRES_INTERCEPTOR`. The
  `RESOURCE_HAS_DEPENDENTS` deletion guard is deferred — nothing references a
  `DelegationPolicy` yet.
- `6c2561b` — §8.3 DelegationPolicy registry (`pkg/gateway/delegationpolicystore`). The
  platform-global store for the first-class `DelegationPolicy` resource: a named,
  tag-matched allow/deny rule set plus a `contentPolicy` and `allowSelfRecursion`.
  `Validate` enforces the §8.3 structural invariants, including the rule-1 dependency
  that `contentPolicy.scanExportedFiles` requires a `contentPolicy.interceptorRef`.
- `132b349` — §11.7 / §15.1 compliance-profile decommission endpoint.
  `POST /v1/admin/tenants/{id}/compliance-profile/decommission` is the attested,
  platform-admin-only path that lowers a regulated `complianceProfile`. It validates the
  `acknowledgeDataRemediation` attestation, the required justification and remediation
  attestations, the `previousProfile` concurrency guard (`409` on mismatch), and a
  strictly lower ladder target; on success it emits the critical
  `compliance.profile_decommissioned` audit event.
- `6dbc31f` — §11.7 compliance-profile downgrade ratchet. `PUT /v1/admin/tenants/{id}`
  rejects a `complianceProfile` transition that lowers the ratchet ordinal
  (`none < soc2 < fedramp < hipaa`) with `422 COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`,
  carrying `currentProfile` / `requestedProfile` in `details`. An off-ladder profile
  (`gdpr`) is not constrained by the ratchet.
- `5507482` — §12.8 `compliance.billing_erasure_exempt_regulated` at gateway startup.
  `EmitBillingErasureExemptRegulatedStartup` scans active tenants and emits the event for
  each `exempt` tenant under a regulated compliance profile; `lenny-gateway` runs the
  scan once at startup so the retention posture cannot silently persist across
  redeployments.
- `6fcf51f` — §12.8 `billingErasurePolicy` on the tenant admin API. `POST` / `PUT
/v1/admin/tenants` accept `billingErasurePolicy` (`pseudonymize` or `exempt`; an
  unrecognized value is `400`). An `exempt` tenant under a regulated `complianceProfile`
  (`hipaa`, `fedramp`, `soc2`) emits the `compliance.billing_erasure_exempt_regulated`
  audit event at create/update — the combination is permitted; the event records the
  retention trade-off.
- `33e7f1b` — §12.8 erasure receipt records the billing disposition. The
  `gdpr.erasure_completed` receipt now carries a `billingErasure` block: the disposition,
  the rewritten-event count, and the verification result.
- `f078c41` — §12.8 billing-erasure phases wired into the erasure Runner. With a
  `BillingEraser` attached, `Run` drives `pseudonymizing` → `verifying` after store
  deletion, recording the outcome on `Job.Billing`; a pseudonymization error or a failed
  verification fails the job closed. `lenny-gateway` attaches the `BillingEraser` for the
  in-memory billing store.
- `d2809a2` — §12.8 `BillingEraser`. `erasurejob.BillingEraser` resolves a tenant's
  `billingErasurePolicy` (`exempt` retains the original user id under GDPR Article
  17(3)(b)), generates and persists a 256-bit erasure salt, rewrites the user's billing
  events to their salted-hash pseudonym, destroys the salt, and runs the §12.8
  post-pseudonymization verification. `tenantstore.Tenant` gains `BillingErasurePolicy`
  and the transient `ErasureSalt`; `billingstore.Memory` gains `CountUser`.
- `4b51fb0` — §12.8 billing-event pseudonymization primitive. `billingstore.Pseudonymize`
  is the one-way `SHA-256(user_id || erasure_salt)` hash; `Memory.PseudonymizeUser`
  rewrites every billing event owned by a user, replacing the user id with its pseudonym
  while the append-only sequence numbers, the tenant id, and the cost dimensions survive
  for financial reconciliation. The method rejects an empty salt (an unsalted hash is
  trivially reversible) and is idempotent. The Postgres-backed counterpart is deferred —
  it needs an UPDATE under the `lenny_erasure` role.
- `6cc57d9` — §10.7 ExperimentRouter rejection event + counter. The fail-closed
  rejection now emits the `experiment.isolation_mismatch` operational event (§16.6) and
  increments `lenny_experiment_isolation_rejections_total` (§16.1, labeled by
  `tenant_id`, `experiment_id`, and `variant_id`) alongside the 422. `sessionserver`
  gains an `ExperimentRejectionReporter` interface (mirroring `DeriveAuditSink`, so the
  package stays decoupled from the audit and metrics subsystems); the gateway wires an
  adapter that fans the rejection out to the §11.7 audit chain and the metrics registry.
- `86bf8cc` — §10.7 ExperimentRouter isolation-monotonicity fail-closed. Before routing
  a session to a variant pool the router verifies the pool's §5.3 isolation profile is
  at least as restrictive as the session's; a weaker variant pool fails the session
  closed with `422 VARIANT_ISOLATION_UNAVAILABLE` (details carry experimentId,
  variantId, sessionMinIsolation, variantPoolIsolation), and the session row is never
  persisted. `sessionserver` gains a `poolstore.Store` dependency wired from the
  gateway; `POST /v1/sessions` and `POST /v1/sessions/start` share the check. The check
  is a no-op when the pool store is not wired or the variant pool is unresolvable.
- `c97dd37`, `b27eb3c` — §10.7 experiment tenant-floor advisory + the §5.3 tenant
  `minIsolationProfile`. The tenant model gains the isolation floor (migration 0012,
  admin tenant payload); `POST` / `PUT /v1/admin/experiments` emit
  `experiment.variant_weaker_than_tenant_floor` for each variant pool below the
  tenant's floor — an advisory event, never a rejection.
- `cd05f74` — §10.7 variant-pool isolation-monotonicity check. `POST` / `PUT
/v1/admin/experiments` reject a variant whose pool isolation profile is weaker than
  the experiment's base runtime with `422 CONFIGURATION_CONFLICT`, since a
  control-bucketed session falls through to the base runtime. The check resolves the
  variant pool and base runtime through the wired stores and is best-effort when either
  is unresolvable.
- `de87bae` — §4 PreDelegation interceptor wiring. `lenny/delegate_task` gained an
  optional `taskInput`; when set, the §4 PreDelegation interceptor chain runs over it
  before the delegation (a REJECT blocks it before any child is created, a MODIFY
  rewrites the input), and the resulting task input is delivered to the child as its
  first message. The chain payload is the task input only, keeping delegation metadata
  structurally immutable.
- `286c63f` — §10.7 results `breakdown_by` response shape. With `breakdown_by` set to
  `delegation_depth`, `inherited`, or `submitted_after_conclusion`, each variant's flat
  scorers block is replaced by a `breakdowns` array of per-bucket sub-aggregates,
  ascending by bucket value, with the variant sample count summed across buckets. This
  completes the §10.7 experiment results API.
- `767a30e` — §10.7 results per-dimension breakdown + filters. Each scorer aggregate on
  `GET /v1/admin/experiments/{name}/results` now carries the per-dimension `scores`
  breakdown (count/mean/p50/p95 per dimension), and the `delegation_depth`, `inherited`,
  and `exclude_post_conclusion` query filters narrow the result set. The `breakdown_by`
  alternate response shape remains deferred.
- `68d87e7`, `8c066ff` — §9.4 MemoryStore. `pkg/gateway/memorystore` is the tenant-scoped
  agent-memory store: Write / Query / Delete / List reject an empty tenant or user and
  scope strictly by (tenant, user), Write evicts a user's oldest memories past the
  capacity limit, and DeleteByUser / DeleteByTenant are the §12.8 erasure primitives.
  The `lenny/memory_write` and `lenny/memory_query` MCP tools write under the calling
  session's scope and recall across every session the user has run.
- `467616c`, `8f53d19`, `220b789` — §10.7 ExperimentRouter. `experiment.Route` is the
  first-match multi-experiment rule; `sessionstore.Session` gained the
  `experimentContext` (migration 0011), and `handleCreate` / `handleCreateAndStart` run
  the router at session creation — an enrolled session records its context and is
  routed onto the variant's runtime and pool. `POST /v1/sessions/{id}/eval` copies the
  context onto each EvalResult so `/results` aggregates real per-variant breakdowns.
- `26e1a3b` — §10.7 experiment results aggregation. `GET
/v1/admin/experiments/{name}/results` reports, for each variant plus the implicit
  control group, the distinct-session sample count and the per-scorer count, mean,
  p50, and p95. `evalstore.ListByExperiment` feeds the aggregation. The per-dimension
  `scores` breakdown and the `breakdown_by` / `delegation_depth` filters are deferred.
- `ba67d93`, `bf78359` — §10.7 built-in eval endpoint. `evalstore` is the per-session
  EvalResult registry; `POST /v1/sessions/{id}/eval` validates the submission, checks
  the session is eval-eligible (running/completed/failed), enforces the per-session
  storage bound (`429 EVAL_QUOTA_EXCEEDED`), and stores the score. `evalstore` also
  exposes the §12.8 `DeleteBySession` erasure adapter, wired into the erasure
  orchestrator. The `/results` aggregation endpoint and experiment attribution are
  deferred to the `ExperimentRouter` work.
- `550db8e`, `466b3a6` — §10.6 / §15.1 environment admin API. `environmentstore` is the
  per-tenant Environment registry; `/v1/admin/environments` serves POST/GET/list/PUT/
  DELETE. The wire payload mirrors the §10.6 resource — RBAC members, tag-based runtime
  and connector selectors, mcp capability filters, and bilateral cross-environment
  declarations — and validates against the `pkg/environment` Role and Selector
  primitives. The cross-environment delegation resolver and transparent-filtering
  middleware are deferred.
- `1567473`, `f37a856` — §10.7 / §15.1 experiment admin API. `experimentstore` is the
  per-tenant ExperimentDefinition registry; `/v1/admin/experiments` serves
  POST/GET/list/PUT/PATCH/DELETE. POST/PUT validate the §10.7 definition (variant
  weights, the reserved `control` id, enum fields); PUT leaves status untouched; PATCH
  is the canonical status-transition endpoint enforcing the §10.7 lifecycle (active and
  paused interconvert and may conclude; concluded is immutable) and emits
  `experiment.status_changed`. The `/results` endpoint and the cross-resource
  variant-pool isolation check are deferred.
- `f0b2787`, `ed68362` — §11.4 `full_revoke` SessionStore-side fan-out. `full_revoke`
  transitions every non-terminal session owned by the user to `cancelled` and dismisses
  the user's pending elicitations (`interactionstore.DismissByUser` — elicitations only,
  per the spec's step-7 wording). soft/hard disable leave running sessions and
  elicitations alone. The invalidate response and audit event report the counts. The
  pod-RPC `Terminate` fan-out and Redis cached-auth invalidation remain infra-bound.
- `112a3e9` — §7.1 artifact-retention GC (`pkg/gateway/retentiongc`). A periodic sweep
  deletes the transcript and blobs of every terminal session past its retention
  deadline and clears the session's snapshot reference so the row is collected once. A
  §12.8 legal hold exempts the session; the sweep re-reads each row immediately before
  the irreversible deletion so a hold placed mid-sweep still protects it. Wired into
  the gateway alongside the watchdog and orphan-cleanup workers.
- `6e79d19` — §12.8 `--acknowledge-hold-override` erasure path. A `platform-admin` may
  bypass the legal-hold preflight with a non-empty justification; a missing
  justification is `400` and a `tenant-admin` caller is `403`. The override emits
  `gdpr.legal_hold_overridden` and is recorded in the `gdpr.erasure_completed` receipt.
- `3cb9f4d`, `240dae1`, `a092e10` — §12.8 legal-hold control. The `legal_hold` flag on
  sessions (migration 0010), `POST /v1/admin/legal-hold` to set or clear it
  (platform-admin only, emitting `legal_hold.set` / `legal_hold.cleared`), and the
  step-0 erasure preflight: an erasure of a user who owns a held session is rejected
  with `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` before the job initiates, emitting
  `gdpr.erasure_blocked_by_hold`.
- `8a01b97` — §12.8 `gdpr.erasure_completed` receipt. A completed erasure job writes a
  `gdpr.erasure_completed` audit event carrying the job id, per-store deleted counts,
  and total — the authoritative proof that the erasure was carried out. A failed job
  emits no receipt; its outcome is the job record queried via the status endpoint.
- `8d971b8`, `e5204f9` — §12.8 / GDPR Article 18 processing-restriction flag. Initiating
  an erasure job marks the target user `processing_restricted`; the session-creation
  gate rejects a restricted user with `403 ERASURE_IN_PROGRESS`, surfacing the erasure
  job id. The erase handler sets the flag on initiation and clears it on completion,
  leaving it set when the job fails. Migration 0009 adds the `processing_restricted`
  and `erasure_job_id` columns so the Postgres userstore round-trips the restriction.
- `10bd520` — Gateway wires the §12.8 erasure orchestrator. `cmd/lenny-gateway` builds
  the `DeleteByUser` orchestrator from its wired stores — a session lister over the
  SessionStore, the transcript and blob stores as session-scoped erasers, the
  interaction and session stores as user-scoped erasers — and exposes it behind the
  admin erasure endpoints. The §12.8 erase path is now reachable in the running binary.
- `343d6d3` — §12.8 user-erasure admin endpoints. `POST /v1/admin/users/{user_id}/erase`
  initiates a background `DeleteByUser` job and returns its id; `GET
/v1/admin/erasure-jobs/{job_id}` reports the phase, an advisory completion
  percentage, elapsed time, per-store deleted counts, and any failure. Both are gated
  on platform-admin or tenant-admin; a job in another tenant reads as not-found.
- `0bc2f53` — §12.8 erasure-job registry and runner (`pkg/gateway/erasurejob`). A `Job`
  records the §12.8 phase field so a controller restart resumes from the persisted safe
  point; the `Runner` drives a job `initiated → store_deleting → completed/failed`
  while the erasure orchestrator deletes the user's data, preserving the fail-fast
  partial result on a store error.
- `348a9c6` — Blob store §12.8 `DeleteBySession` adapter. Both blob backends gained the
  per-session GDPR-erasure adapter: `MemoryStore` scans its blob map; the MinIO store
  lists and removes objects under the session prefix. A shared `sessionPrefix` helper
  keeps the MinIO key layout in sync with `objectKey`.
- `751efcf` — Adapter `Resume` RPC (§4.7, §7.1). Added to the adapter proto and
  implemented: the adapter claims the session, loads a checkpoint archive through a
  `CheckpointSource`, restores the workspace via `workspace.Extract`, and starts the
  runtime — the replacement-pod counterpart of `StartSession`. `workspace.Extract` now
  returns the uncompressed bytes restored.
- `3586fe6` — `workspace.Extract` (§4.4, §7.1, §14). Restores a workspace from a
  gzip-tar — the inverse of `Archive` — with per-entry containment, symlink-target
  validation that closes the tar-traversal vector, and a decompression-bomb cap. The
  adapter restore path and §14 `uploadArchive` materialization build on it.
- `5f3f268` — Gateway wires the checkpointer as the §7.1 session sealer.
  `cmd/lenny-gateway` passes the `Checkpointer` as the `sessionserver` `Sealer`, so a
  completing session's workspace is sealed on the session-completion path.
- `46aade9` — Seal-and-export on session completion (§7.1). `recordSessionCompleted`
  invokes the optional `Sealer` when a session reaches a terminal state, before
  `executor.Close` releases the pod, so the final snapshot is taken while the pod
  adapter is still reachable.
- `3abab51` — `checkpointer.Seal` (§7.1). The seal-and-export snapshot — a checkpoint
  recorded with `WorkspaceSnapshot` source `sealed` rather than `checkpoint`.
- `bbebbe1` — Gateway runs the §4.4 periodic-checkpoint loop. `cmd/lenny-gateway` starts
  `Checkpointer.Run` as a background runnable when `--agent-namespace` is set, on the
  `--checkpoint-interval` cadence (default 5m).
- `30d05a2` — Periodic-checkpoint loop (§4.4, §7.1). `Checkpointer.Run` ticks on the
  configured cadence and `Sweep` takes a §4.4 checkpoint of every running session this
  gateway replica coordinates, keeping each `WorkspaceSnapshot` fresh. `podsession.BindResult`
  gained `TenantID` and `podsession.Registry` gained `Snapshot` to enumerate the bindings.
- `eb1200e` — `checkpointer.Checkpointer` (§4.4, §7.1). Drives one §4.4 checkpoint of a
  running session: resolves the bound pod adapter via the `podsession` registry, calls
  the adapter `Checkpoint` RPC, and records the result as the session's §7.1
  `WorkspaceSnapshot` with source `checkpoint`.
- `c793815` — `adapterclient.Checkpoint` (§4.4, §15.4). The gateway-side adapter client
  gains a `Checkpoint` method that drives a pod adapter's §4.4 Checkpoint RPC and
  returns the stored checkpoint's identifier and size.
- `b7f8c9c` — Adapter `Checkpoint` RPC (§4.4, §4.7). Archives the session workspace with
  `workspace.Archive` and streams the gzip-tar to a `CheckpointSink`, returning the
  checkpoint identifier and compressed size — the best-effort path that archives the
  workspace live without quiescing the runtime. The `Server` gains an optional
  `Checkpoints` sink.
- `3a9898f` — `workspace.Archive` (§4.4, §7.1). Snapshots a workspace directory into a
  gzip-tar — the inverse of `Materialize` — for a §4.4 checkpoint or the §7.1
  seal-and-export. Symlinks are recorded as symlink entries without being followed, so
  the archive cannot embed content outside the workspace root.
- `12afa28` — Gateway selects the MinIO-backed artifact store (§4.5, §12.5).
  `cmd/lenny-gateway` gains `--minio-endpoint` and related flags; when set, the §4.5
  blob store is `miniostore.Store` and `GET /internal/drain-readiness` runs a real
  MinIO bucket probe instead of the always-ready stub.
- `8a15fa4` — `miniostore` MinIO-backed blob store (§4.5, §12.5). The production
  `blobstore.Store`: Put (write-once), Get, Stat with §4.5 TTL expiry, backed by the
  minio-go SDK. It also satisfies the §12.5 drain-readiness `Prober` via a bucket-exists
  probe. Pure logic is unit-tested; the S3 round-trip is a component-tier contract test
  against a MinIO container behind a new `containers.StartMinIO` helper.
- `5c7f1c1` — Gateway serves `GET /internal/drain-readiness` (§12.5). `cmd/lenny-gateway`
  mounts the `drainreadiness.Handler` so the `lenny-drain-readiness` webhook can run its
  pre-drain artifact-store health check. The §12.5 webhook is now complete end to end.
- `150f168` — `lenny-drain-readiness` Helm manifest (§12.5). The fail-closed
  `ValidatingWebhookConfiguration` on the `pods/eviction` subresource, rendered when
  `features.drainReadiness` is enabled; the shared webhook Deployment template passes
  `--gateway-drain-readiness-url`.
- `889e4ad` — `cmd/lenny-webhook` serves the `/drain-readiness` route (§12.5). The
  `lenny-drain-readiness` admission route on the `pods/eviction` subresource, built from
  the cluster client and an `HTTPDrainProbe` pointed at the gateway endpoint via the new
  `--gateway-drain-readiness-url` flag.
- `54725dd` — `webhook.DrainReadiness` handler (§12.5). The `lenny-drain-readiness`
  Decider for the `pods/eviction` subresource: it resolves the evicted pod's node,
  reads the `lenny.dev/drain-force` override, queries the gateway drain-readiness
  endpoint via `HTTPDrainProbe`, and applies `drain_readiness.Decide`. The
  `cmd/lenny-webhook` route, the Helm manifest, and the gateway endpoint wiring follow.
- `5974b11` — `drainreadiness` gateway endpoint (§12.5). The `GET /internal/drain-readiness`
  endpoint runs a MinIO liveness probe within a 2s timeout and returns the §12.5
  ready/not-ready JSON bodies; the probe is a `Prober` interface so the MinIO HeadBucket
  check wires in at the gateway.
- `a6c627c` — `drain_readiness` decision logic (§12.5, §13.2). The pure decision for the
  `lenny-drain-readiness` webhook: the node drain-force override admits an eviction as a
  forced drain, a healthy §12.5 MinIO probe admits it, and an unhealthy or unreachable
  probe rejects it fail-closed. The webhook handler, gateway endpoint, and Helm manifest
  follow.
- `e4d9a8b` — `credassign.Service` (§4.9). The credential-assignment service: at session
  start it selects a pool credential per the assignment strategy, mints the
  `CredentialLease`, records it in `credleasestore`, and caches the real upstream
  credential in `credcache` so the LLM proxy can inject it; `Release` frees the slot.
- `c5db326` — `credential.SelectCredential` (§4.9). The pool credential assignment
  strategies — least-loaded, round-robin, sticky-until-failure — each picking a healthy
  credential, skipping unhealthy ones, and returning `ErrPoolExhausted` when none is
  assignable.
- `beb2e77` — `credential.MintLease` (§4.9). The CredentialLease minter: synthetic-TTL
  resolution with the per-provider ceilings, `expiresAt`/`renewBefore` computation, and
  opaque lease-ID and unguessable bearer-token generation.
- `80039dc` — Gateway serves the §4.9 LLM reverse proxy. `cmd/lenny-gateway` gained
  `--llm-proxy-addr` and `--anthropic-version`; `newLLMProxyServer` builds the
  `llmproxy.Handler` over the credential-lease store, credential cache, deny list,
  translator, and breaker-gated forwarder, and the gateway serves it at
  `POST /llm-proxy/v1/messages` on a listener separate from the REST API.
- `293eb04` — `denylist.DenyList` (§4.9). The per-replica credential deny list the LLM
  proxy checks on every upstream request; a hit rejects with `CREDENTIAL_REVOKED`
  before any upstream call. Satisfies the proxy's `DenyList` interface.
- `1676fd6` — `credcache.Cache` (§4.9). The Token Service in-memory upstream-credential
  cache: real provider keys keyed by the source-aware credential identity, satisfying
  the proxy's `CredentialResolver`. The §4.9 source-aware key is renamed
  `credential.DenyListKey` → `credential.CredentialKey`, since the deny list and the
  cache both key on it.
- `3764c4a` — LLM proxy SSE streaming wired into the handler (§4.9). `Forwarder.ForwardStream`
  gates a streaming upstream call through the breaker and returns the live 2xx response;
  the `Handler` dispatches on the request `stream` field, relays the upstream SSE stream
  to the agent pod via `RelayStream`, and records the authoritative token usage through
  the optional `UsageRecorder` on both the streaming and non-streaming paths.
- `730d4da` — `llmproxy.RelayStream` (§4.9). The SSE relay copies an upstream Anthropic
  Messages event stream to the agent pod line by line with per-line flush, without
  buffering, and extracts the authoritative token usage from the `message_start` and
  `message_delta` events. A mid-stream failure is tagged `streaming_interrupted`.
- `d210138` — `llmproxy.Handler` (§4.9). The LLM reverse proxy HTTP handler for the
  Anthropic Messages dialect: it resolves the agent pod's bearer lease token through
  `credleasestore`, runs the per-request lease checks, translates and credential-injects
  the request, forwards through the breaker-gated `Forwarder`, and translates the
  response. The `CredentialResolver` and `DenyList` are interfaces so the Token Service
  credential cache and the §4.9 deny list plug in later. The non-streaming proxy path
  is whole; the SSE relay remains.
- `e65f804` — `credleasestore.Store` (§4.9). The in-memory, per-replica store of issued
  credential leases, indexed by lease ID and by the opaque proxy lease token so the LLM
  reverse proxy resolves an agent pod's request to its lease on the upstream hot path.
- `e15d457` — `credential.Lease` data model (§4.9). The CredentialLease as a Go type:
  the pool/user tagged union, the proxy-mode materializedConfig, structural validation,
  the source-aware deny-list key, and `ValidateProxyRequest` (expiry, revocation,
  SPIFFE-binding) — the per-request checks the LLM proxy runs against a resolved lease.
- `15634ed` — `lenny-direct-mode-isolation` Helm manifest (§4.9, §13.2). The fail-closed
  `ValidatingWebhookConfiguration` scoped to `sandboxtemplates` in agent namespaces,
  rendered only when `features.llmProxy` is enabled. `values.yaml` gained `tenancy.mode`
  and `global.devMode`, passed to every admission-webhook Deployment as `--tenancy-mode`
  and `--dev-mode`. The webhook is deployable end to end.
- `b4c27a8` — `lenny-direct-mode-isolation` webhook handler and route (§4.9, §13.2). The
  `DirectModeIsolation` Decider decodes a `SandboxTemplate` and applies
  `direct_mode_isolation.Decide`; `cmd/lenny-webhook` gained the `/direct-mode-isolation`
  route and the `--tenancy-mode` / `--dev-mode` flags. `SandboxTemplateSpec` gained a
  `spiffeBinding` field so the webhook can enforce the `proxy` + `spiffeBinding: disabled`
  rejection on the resource it admits.
- `dd0ccf8` — `direct_mode_isolation` decision logic (§4.9, §13.2). The pure decision
  for the `lenny-direct-mode-isolation` webhook: in multi-tenant mode it rejects
  `deliveryMode: direct` with `isolationProfile: standard` and `deliveryMode: proxy`
  with `spiffeBinding: disabled` on `SandboxTemplate` and `CredentialPool` resources.
  Enforcement is inactive outside multi-tenant mode. The webhook HTTP handler and Helm
  manifest follow.
- `ee61d52` — `llmproxy.Forwarder` (§4.9). The upstream forwarder gated by the circuit
  breaker: an open breaker rejects the call with `ErrCircuitOpen` before dialing, a
  transport failure returns a `TranslationError` tagged `timeout` or `upstream_5xx`, and
  a completed HTTP exchange returns the `UpstreamResponse` at any status. A 5xx or
  transport failure feeds the breaker; a 4xx or 2xx records a success.
- `6221f0f` — `llmproxy.CircuitBreaker` (§4.9). The LLM Proxy circuit breaker around an
  upstream provider: consecutive failures trip it open, an open breaker rejects every
  request so the proxy returns `PROVIDER_UNAVAILABLE` without hanging, and after the
  cooldown it admits one half-open probe whose outcome closes or reopens it. State maps
  to the §16.1 `lenny_gateway_subsystem_circuit_state` gauge values.
- `9799022` — `llmproxy.AnthropicDirectTranslator` (§4.9). First leaf of the LLM reverse
  proxy: converts an agent pod's Anthropic Messages proxy-dialect request into the
  upstream `anthropic_direct` request (body passthrough, injected `x-api-key`,
  `anthropic-version` header handling) and passes the response back with the
  authoritative token usage extracted. Translator failures carry the §4.9 error
  taxonomy. The proxy HTTP handler, lease-token validation, the SSE relay, the circuit
  breaker, and the `lenny-direct-mode-isolation` webhook remain.
- `a2585eb` — `GET /v1/sessions/{id}` returns the stored `workspacePlan` (§15.1).
  `toResponse` echoes the §14 plan persisted on the session row; the `SessionResponse`
  envelope gained a `workspacePlan` field that `omitempty` drops for planless sessions.
- `f11558c` — Two-step §15.1 session start places sessions on warm pods. The granular
  `create → finalize → start` lifecycle now claims a §5 warm pod at the start
  transition, matching `POST /v1/sessions/start`. The §14 WorkspacePlan is persisted on
  the session row (new `sessions.workspace_plan` jsonb column, migration `0008`) so the
  dedicated `handleStart` re-parses it and materializes the workspace onto the claimed
  pod. `handleStart` claims the pod before transitioning the row, leaving the session
  `ready` and retryable on a claim failure.
- `b2935cd` — Gateway↔pod session wiring complete (§15.1, §4.7). `POST /v1/sessions/start`
  claims a §5 warm pod through `podsession.Binder` and records the binding in
  `podsession.Registry` for the message and teardown paths; a claim failure marks the
  session row failed and returns a retryable 503. `cmd/lenny-gateway` gained
  `--agent-namespace`: when set it builds a controller-runtime client, the `Binder`, the
  `Registry`, and the `PodExecutor`, so the REST session surface runs a session on a warm
  pod end to end. `adapter.TLSClientOption` builds the gateway's §4.7 mTLS dial option.
  Critical-path items 4 and 5 are done.
- `90048bf` — `podsession.WorkspacePlanToProto`. Converts a parsed §14 WorkspacePlan
  into the `adapterv1.WorkspacePlan` the gateway sends in `StartSession` — the
  conversion the session-start path needs to feed `Binder.Bind` a workspace plan.
- `77c00ef` — Gateway closes the executor on session completion.
  `recordSessionCompleted` now calls `executor.Close`, fixing a latent leak (a
  SubprocessExecutor child outlived its session) and giving the pod-backed
  `PodExecutor` its teardown hook — a prerequisite for the gateway↔pod wiring.
- `bd7e508` — `allow-gateway-egress` NetworkPolicy (§13.2). Re-admits the gateway's
  in-cluster egress (agent adapters, Token Service, PgBouncer, Redis, MinIO,
  kube-apiserver, CoreDNS) under the `lenny-system` default-deny. The external-HTTPS
  egress with its NET-062 dual-family IMDS exclusions is deferred to the LLM proxy.
- `0b5cc49` — `allow-minio` NetworkPolicy (§13.2). Re-admits MinIO ingress from the
  gateway and its CoreDNS egress under the `lenny-system` default-deny;
  `minio.tlsPort` value added.
- `9d13f34` — `allow-pgbouncer` NetworkPolicy (§13.2). Re-admits PgBouncer ingress
  from the gateway/Token Service/controller and its Postgres and CoreDNS egress under
  the `lenny-system` default-deny; `postgres.cidr` value added.
- `24785e2` — `allow-pod-egress-llm-proxy` NetworkPolicy (§13.2). The supplemental
  agent-namespace egress that admits only proxy-mode pods (by the
  `lenny.dev/delivery-mode: proxy` label) to the gateway LLM reverse-proxy port.
- `f129dce` — `podsession.ResolvePool` (§5). Resolves a runtime and §5.3 isolation
  profile to the matching `SandboxWarmPool` by inspecting each pool's template — the
  pool-resolution the gateway session-start handler needs to choose which pool to
  claim from. The last gateway↔pod component dependency.
- `258c320` — Gateway and Token Service metrics-scrape NetworkPolicies (§13.2). Admit
  Prometheus scrape from the monitoring namespace to those components' metrics ports
  under the `lenny-system` default-deny.
- `4aa16c7` — `allow-dedicated-coredns` NetworkPolicy (§13.2). Re-admits the dedicated
  CoreDNS ingress (agent-namespace DNS, monitoring scrape) and kube-system CoreDNS
  egress under the `lenny-system` default-deny.
- `18eba1a` — `allow-controller-metrics-scrape` NetworkPolicy (§13.2). Admits
  Prometheus scrape from the monitoring namespace to the controller's metrics port
  under the `lenny-system` default-deny; `controller.metricsPort` and
  `monitoring.namespace` values added.
- `867e439` — `allow-token-service` NetworkPolicy (§13.2). Re-admits the Token
  Service's gateway ingress and its PgBouncer/Redis/KMS/CoreDNS egress under the
  `lenny-system` default-deny; `tokenService.grpcPort`, `redis.tlsPort`, and
  `kms.endpointCIDR` values added.
- `5de9772` — `allow-controller-egress` NetworkPolicy (§13.2). Re-admits the
  controller's kube-apiserver, PgBouncer, and CoreDNS egress under the `lenny-system`
  default-deny, so the deployed controller can reach the API server; `kubeApiServerCIDR`
  value added.
- `c11f002` — `executor.PodExecutor` — the pod-backed `Executor`. `Send` drives a
  session's bound pod over the §4.7 `Attach` content stream and collects the agent's
  response; `Close` releases the pod. It implements the `Executor` interface, so the
  gateway message path becomes an executor swap. The gateway↔pod session subsystem is
  now built end to end as components; the gateway-binary wiring is the remaining step.
- `6c2f6e1` — `podsession.Registry` — the per-session pod-binding registry. Holds the
  live `BindResult` per coordinated session for the session-start, message, and
  teardown paths. The last component before the gateway session-path wiring; all of
  claim, start, content stream, teardown, and the binding registry now exist.
- `7b34036` — `podsession.Binder.Release` (§6.2). The teardown counterpart to `Bind`:
  shuts the pod's runtime down through the adapter, closes the connection, and
  transitions the Sandbox `claimed → draining` so the reconciler reclaims the pod.
  The gateway↔pod session lifecycle — claim, start, content stream, teardown — is now
  built as components; the gateway-binary session-path wiring remains.
- `3390203` — Gateway-side `adapterclient.Attach` (§4.7). `Client.Attach` opens and
  binds the content stream; `AttachStream.Send` / `Recv` / `CloseSend` wrap the bidi
  stream. The §4.7 content path now has its proto RPC, adapter handler, and gateway
  client; wiring it into the gateway session path remains.
- `a21b2bf` — Adapter `Attach` handler (§4.7). The bidirectional content stream: a
  receive loop forwards client envelopes to the runtime's stdin, a send loop streams
  the runtime's output envelopes back. `RuntimeProcess` gained an `Output` method
  (`SubprocessExecutor` drains the child's stdout); tested over an in-memory gRPC
  stream with `-race`. The gateway-side `adapterclient.Attach` remains.
- `3128fa7` — `Attach` bidirectional content-stream RPC added to
  `schemas/lenny-adapter.proto` (§4.7, §15.4). Reconciles the Phase-1 skeleton proto
  with §4.7's RPC table: `Attach` plus the `AttachClientMessage` / `AttachServerMessage`
  frames. Purely additive; the regenerated bindings expose it. The adapter-server
  `Attach` handler and the gateway-side `adapterclient.Attach` remain.
- `e9ad1ce` — `allow-admission-webhooks` NetworkPolicy (§13.2). Re-admits
  kube-apiserver ingress and kube-system CoreDNS egress for the admission webhook
  pods under the `lenny-system` default-deny; `webhookIngressCIDR` value added.
- `8b72d0b` — `lenny-system` `default-deny-all` NetworkPolicy (§13.2). The fail-closed
  control-plane network baseline for the release namespace; the §13.2 component
  allow-lists remain.
- `8312655` — `make images` now builds `lenny-preflight`. The preflight Job's image
  was missing from the target; verified the image builds via the parameterized
  Dockerfile.
- `0b5faea` — Host-sharing check wired into `preflight.Run`. `Run` now gathers the
  Lenny-managed Deployments, DaemonSets, and Jobs and runs `CheckHostSharing`, so the
  deployed `lenny-preflight` Job enforces three checks; the preflight Role gained
  workload list access.
- `2a52365` — `pkg/preflight` host-sharing flag check (§13.1). `CheckHostSharing` fails
  fail-closed when any Lenny-managed pod template enables `shareProcessNamespace`,
  `hostPID`, `hostNetwork`, or `hostIPC`. A pure function; gathering Deployments,
  DaemonSets, and Jobs into `Run` and the matching RBAC remain.
- `9e48d8e` — `lenny-preflight` Helm Job and RBAC (§17.9). The pre-install/pre-upgrade
  hook Job at weight -10 with its read-only ServiceAccount, ClusterRole, and Role at
  -15. `lenny-preflight` is now deployable end to end (decision logic, `Run`, binary,
  Job); the remaining §17.9 checks beyond the two admission-plane checks are
  follow-ups.
- `f98a375` — `lenny-preflight` binary (§17.9). Builds an in-cluster client, runs
  `preflight.Run`, and exits non-zero on any check failure so the Helm pre-install /
  pre-upgrade Job aborts fail-closed. The Helm Job and its RBAC remain.
- `de4bd43` — `pkg/preflight.Run` cluster-gathering layer. Gathers the lenny-\*
  ValidatingWebhookConfigurations and the phase-stamp ConfigMap and runs the two
  §17.9 checks against them; takes a `client.Reader` so it is fake-client testable.
  The `lenny-preflight` binary and its Helm Job remain.
- `6469141` — `pkg/preflight` admission-webhook inventory check (§17.9).
  `ExpectedValidatingWebhooks` computes the §17.2 feature-gated expected set;
  `CheckAdmissionWebhooks` fails fail-closed on a missing webhook, a non-Fail
  failurePolicy, or an empty caBundle. The second `lenny-preflight` check.
- `04b1be0` — `pkg/preflight` phase-stamp consistency check (§17.9). The pure
  `CheckPhaseStamp` decision function for `PREFLIGHT_PHASE_STAMP_MISMATCH` — an
  unacknowledged admission-plane feature-flag downgrade fails fail-closed. The start
  of the `lenny-preflight` system; the binary and the remaining §17.9 checks remain.
- `b9a3c97` — Platform `Dockerfile`. A parameterized multi-stage build (Go to
  `distroless/static:nonroot`) that produces the adapter, controller, gateway,
  webhook, and token-service images via the `BINARY` build-arg; `make images` builds
  all five. Validated by building the adapter image. Critical-path item 1's container
  image.
- `c683a1a` — Adapter `DemoteSDK` RPC. The §4.7 SDK-warm demotion RPC; a pod-warm
  adapter is not preConnect-capable, so `DemoteSDK` returns `Unimplemented` with a
  precise message, the behavior §4.7 specifies for non-preConnect pods. Critical-path
  item 1 work.
- `2d4d7eb` — `crd-conversion` identity webhook (§17.2). The conversion webhook
  handler — every `lenny.dev` CRD is single-version, so conversion is the identity —
  plus the `/crd-conversion` route on `cmd/lenny-webhook`. The CRD `spec.conversion`
  wiring and Helm manifest remain.
- `5443af7` — `lenny-deployment-phase-stamp` ConfigMap (§17.2). Layer 1 of the
  four-layer feature-flag downgrade enforcement: an append-only ConfigMap recording
  when each admission-plane feature flag was first enabled, preserving `enabledAt`
  across re-renders via Helm `lookup`. The render-time validation, the preflight
  consistency check, and the runtime alert (layers 2-4) remain.
- `82db804` — §13.2 allow-companion NetworkPolicies. `allow-gateway-ingress` admits the
  gateway's gRPC connection to each managed pod's adapter; `allow-pod-egress-base`
  admits pod egress to the gateway control channel and cluster DNS. With
  `default-deny-all` the core §13.2 agent-namespace network set is rendered.
  `values.yaml` gained `adapter.grpcPort` and `gateway.grpcPort`.
- `e08e4db` — Adapter gRPC port corrected to 50051 (§13.2). The adapter binary and
  pod-spec builder bound the adapter to 8443, which §13.2 reserves for the LLM proxy
  port; §13.2 fixes the adapter gRPC port at 50051. A spec-conformance fix that also
  unblocks the §13.2 `allow-gateway-ingress` NetworkPolicy.
- `0eb6258` — `default-deny-all` NetworkPolicy (§13.2). Renders the fail-closed
  deny-all ingress/egress baseline into every agent namespace. The §13.2
  allow-companion policies (gateway ingress, pod egress plus DNS) remain.
- `ce6dae8` — `ephemeral-container-cred-guard` Helm manifest. The §13.1 webhook is now
  fully deployable: decision package, HTTP handler, `cmd/lenny-webhook` route, and the
  `ValidatingWebhookConfiguration` scoped to the `pods/ephemeralcontainers`
  subresource in agent namespaces.
- `658b134` — `ephemeral-container-cred-guard` webhook HTTP handler and the
  `/ephemeral-container-cred-guard` route on `cmd/lenny-webhook`. The §13.1 webhook
  now serves; `podspec` exports `AdapterUID`, `AgentUID`, `CredReadersGID`, and
  `CredVolumeName` so the webhook protects exactly the identities the pod-spec
  builder assigns. The Helm `ValidatingWebhookConfiguration` manifest remains.
- `2ec3791` — `ephemeral-container-cred-guard` decision logic
  (`pkg/admission/ephemeral_container_cred_guard`). The pure §13.1 four-condition
  guard that rejects an ephemeral debug container able to read
  `/run/lenny/credentials.json`. The webhook HTTP handler and Helm manifest remain.
  Phase 3.5 work.
- `3440833` — `pkg/gateway/podsession.Binder`. `Bind` joins the gateway↔pod path: it
  claims an idle Sandbox, resolves the bound pod's adapter address from
  `status.podIP`, performs the §15.5 version handshake, and starts the session on the
  pod's adapter. The claim-and-start half of critical-path items 4 and 5; the
  remaining work is constructing the Binder in the gateway binary and calling it from
  the session-creation handler.
- `55bbd9d` — `Sandbox.status.podIP`. The Sandbox reconciler records the backing pod's
  cluster IP as it observes the pod. The gateway needs this address to reach a claimed
  pod's §4.7 adapter; it unblocks the gateway-side integration of critical-path
  items 4 and 5.
- `d67c6d4` — Adapter `Interrupt` lifecycle RPC. A clean interrupt sends SIGTERM, a
  hard interrupt sends SIGKILL. `RuntimeProcess` gained an `Interrupt` method;
  `SubprocessExecutor` signals the child without taking the stdin/stdout lock so an
  interrupt reaches a busy runtime. `adapterclient` gained a matching method.
  Critical-path item 1 work.
- `37072e4` — Adapter credential RPCs (`AssignCredentials`, `RotateCredentials`,
  `RevokeCredentials`). Each materializes the §4.7 credential file from the session's
  per-provider lease set. The new `pkg/adapter/credfile` package writes
  `credentials.json` through an atomic temp-file rename at mode `0440`, relying on the
  pod fsGroup for group ownership so no `chown` runs (§13.1). `lenny-adapter` gained
  `--credentials-dir`. Critical-path item 1 work.
- `c7f6fe8` — Gateway-side adapter client (`pkg/gateway/adapterclient`). Wraps the
  generated `adapterv1` gRPC client with connection lifecycle management and a
  session-oriented surface: `NegotiateVersion` for the §15.5 handshake, and
  `StartSession` / `SendMessage` / `Shutdown` for the session path. The connective
  piece between the gateway and a claimed pod's adapter; needed by critical-path
  items 4 and 5.
- `6ee9fd9` — Gateway pod-claim `Claimer` (`pkg/gateway/podclaim`). Binds a session to
  an idle Sandbox: flips it to `claimed` under an optimistic-locking status update and
  creates the binding `SandboxClaim`; a conflict skips to the next idle pod. The
  claim logic of critical-path item 4; the gateway-binary integration remains.
- `c7810e9`, `ca0c364` — Sandbox-to-Pod reconciler. The controller-runtime Reconciler
  materializes each Sandbox into a backing Pod via `podspec.Build`, advances the §6.2
  warm-path phase from the `lifecycle.Decide` plan, and runs the draining teardown. It
  is registered in `cmd/lenny-controller` with the adapter image supplied by
  `--adapter-image`; the controller ClusterRole gained Pod create/delete and Runtime
  read. Critical-path item 3.
- `dd5d3fc` — Agent pod-spec builder. `podspec.Build` translates a Sandbox,
  SandboxTemplate, and Runtime into the backing `corev1.Pod`: the §4.7 two-container
  sidecar pod with the §13.1 security posture, the §6.1 volumes, and the §5.3
  RuntimeClass. Critical-path item 2.
- `0f8e61c` — Adapter `SendMessage` and `Shutdown` RPCs. `SendMessage` forwards the
  gateway's pre-encoded message envelope to the runtime's stdin; `Shutdown` closes the
  runtime and releases the session. `SubprocessExecutor` gained a `WriteEnvelope`
  raw-delivery path.
- `45ad73d` — Adapter `StartSession` RPC. Rejects a non-idle pod with Unavailable,
  materializes the workspace, runs the setup commands, and starts the runtime process;
  releases the pod on any post-claim failure. `SubprocessExecutor` gained an eager
  `Start` so the runtime is live at session start per §6.1.
- `da1a77b` — Adapter setup-command runner. `workspace.RunSetup` executes a
  WorkspacePlan's setup commands in order, each in the workspace directory under a
  wall-clock timeout, stopping at the first failure.
- `5900141` — Adapter workspace materializer. `pkg/adapter/workspace.Materialize`
  writes a WorkspacePlan's inlineFile and mkdir sources into the workspace root, with
  adapter-side path-containment and mode checks. uploadFile, uploadArchive, and
  gitClone return `ErrSourceUnsupported`.
- `d255c7f` — Runtime adapter gRPC server scaffold. `pkg/adapter.Server` implements the
  generated `adapterv1.AdapterServer` contract with `NegotiateVersion` and the
  TLS/mTLS transport wiring; `cmd/lenny-adapter` is the sidecar binary. The workspace,
  session, credential, and lifecycle RPCs still return `Unimplemented`.

## Summary

The implementation has broad coverage of the per-phase logic packages and the gateway
request surface, plus the Kubernetes control plane and the gateway↔pod session path.

The platform serves the REST and admin API against in-memory, Postgres, and Redis
stores, and runs a runtime as a local subprocess through `make run`. With
`--agent-namespace` set, `cmd/lenny-gateway` claims a §5 warm pod for a session started
through either `POST /v1/sessions/start` or the two-step `create → finalize → start`
lifecycle, materializes its workspace, and runs the session on the pod's §4.7 adapter —
the runtime adapter server, the pod-spec builder, and the Sandbox-to-Pod reconciler are
built. Credential-proxied sessions cannot reach a provider because the LLM Proxy is not
built.

## How the build diverged from the build sequence

The build did not follow the §18 phase order. Two tracks were built ahead of the
Kubernetes layer: the gateway request-handling surface (session lifecycle, admin API,
stores, translators) and the per-phase pure-logic Go packages that §18 lists as
deliverables (`pkg/checkpoint`, `pkg/elicitation`, `pkg/environment`, `pkg/experiment`,
`pkg/podsecurity`, `pkg/credential`, and others). The Kubernetes control plane (the
CRDs, the WarmPoolController, the PoolScalingController) was built most recently.

The consequence is that most later phases are partially complete: the logic substrate
for a phase exists as a tested Go package, while the controller, binary, or gateway
integration that consumes it does not. The phase table below uses "Substrate only" for
this state.

## Phase status

| Phase | Title                                                        | Status         | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| :---- | :----------------------------------------------------------- | :------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0     | Bootstrap the infrastructure repo                            | Partial        | Repo layout, CI, `LICENSE`, and the ADR template are present. ADR-007 and ADR-008 are not committed under `docs/adr/`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 1     | Core types and wire contracts                                | Mostly done    | The `lenny.dev/v1` CRD types, the task records, the session state machines, and the `schemas/` wire contracts exist. The adapter proto Go stubs are generated.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 1.5   | Database migration framework                                 | Mostly done    | Migrations 0001 through 0007 cover the §12 tables including `agent_pod_state`; the RLS guard, the immutability triggers, the role separation, and the schema linters are present. There is no dedicated `cmd/lenny-migrate`; `golang-migrate` is used.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 2     | Adapter protocol, make run, ImageResolver                    | Partial        | The adapter binary protocol, the `echo` runtime, `lenny-compliance`, and `make run` are present. The runtime-author SDKs and the `lenny-ctl runtime init` scaffolder are not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| 2.5   | Observability foundation                                     | Partial        | `pkg/observability` and `pkg/alerting/rules` exist. `pkg/recommendations/rules` and the Helm `ServiceMonitor`/`PrometheusRule` templates are not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| 2.8   | streaming-echo runtime                                       | Done           | The `streaming-echo` runtime and the full-level compliance battery pass.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| 3     | Pool scaling, delegation policy, runtime upgrade, mTLS       | Partial        | The PoolScalingController and the `RuntimeUpgrade` state substrate exist. The §8.3 `DelegationPolicy` first-class resource is built: `delegationpolicystore` is the platform-global registry, `/v1/admin/delegation-policies` serves the §15.1 CRUD with the `EXPORT_SCAN_REQUIRES_INTERCEPTOR` admission check and the §8.3 tag-matching `Evaluate`, and the §8.3 deletion guard rejects a delete with `409 RESOURCE_HAS_DEPENDENTS` when an active runtime's `delegationPolicyRef` names the policy. The Kubernetes `DelegationPolicy` CRD and controller sync, the delegation-time wiring of the tag-matching evaluator, the active-lease half of the deletion guard, the `agent_pod_state` mirror write path, CIDR-drift detection, and the SDK-warm circuit-breaker logic are not built. `pkg/mtls` exists; the cert-manager PKI wiring is partial.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| 3.5   | Admission policies, lenny-ops first deploy                   | Partial        | The `pkg/admission` decision packages and three baseline webhooks (label-immutability, sandboxclaim-guard, ephemeral-container-cred-guard) are built and deployable — decision logic, `cmd/lenny-webhook` handler, and Helm manifest. The core §13.2 agent-namespace NetworkPolicies (`default-deny-all`, `allow-gateway-ingress`, `allow-pod-egress-base`) and the §17.2 `lenny-deployment-phase-stamp` ConfigMap are rendered. The `crd-conversion` webhook handler is served by `cmd/lenny-webhook`. `lenny-preflight` is deployable end to end — the `pkg/preflight` checks and `Run` layer, the `cmd/lenny-preflight` binary, and its pre-install Helm Job and RBAC — running three checks (admission-webhook inventory, phase-stamp consistency, §13.1 host-sharing). `pool-config-validator`, the `crd-conversion` CRD-wiring and manifest, `lenny-ops`, the remaining §17.9 preflight checks, and `lenny-bootstrap` are not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 4     | Session manager, REST                                        | Mostly done    | The session store, the REST session surface, derive, blob dereference, the upload pipeline, `uploadToken`, and `cmd/lenny-gateway` are built. Both `POST /v1/sessions/start` and the two-step `create → finalize → start` lifecycle claim a §5 warm pod and run the session on the pod's §4.7 adapter when the gateway runs with `--agent-namespace`. The Postgres fallback claim path depends on the unbuilt `agent_pod_state` mirror writer.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 4.5   | Admin API, authentication, bootstrap                         | Mostly done    | The admin API, `pkg/auth`, JWT validation, the connector resource, and `lenny-ctl bootstrap` are built.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 5     | ExternalAdapterRegistry, MCP/Completions/Open Responses      | Partial        | The MCP adapter, the OpenAI Chat translator, the Open Responses translator, and the OpenAPI document are built. The `gitClone` materializer and the `type: mcp` gateway endpoints need confirmation.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 5.4   | etcd encryption at rest                                      | Not started    | No `EncryptionConfiguration` manifest in the chart.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| 5.5   | Basic credential leasing, Token Service                      | Mostly done    | `pkg/credential`, the Token Service binary, `POST /v1/oauth/token`, the `issued_tokens` table, and the `/v1/credentials` endpoints are built.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| 5.6   | Targeted security design review (credential)                 | Not started    | No review document under `tests/tier9_security/reviews/`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| 5.75  | Minimum viable policy enforcement                            | Mostly done    | `pkg/quota` and the auth and quota interceptors are built.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 5.8   | LLM Proxy, direct-mode-isolation webhook                     | Mostly done    | The §4.9 LLM proxy subsystem is built (`pkg/gateway/llmproxy`: the `anthropic_direct` translator, circuit breaker, breaker-gated forwarder, SSE relay, and `Handler`, over the `pkg/credential` lease model, the `credleasestore` lease store, the `credcache` credential cache, and the `denylist` deny list) and `cmd/lenny-gateway` serves it on the §13.2 LLM-proxy port. The `lenny-direct-mode-isolation` webhook is deployable end to end. The §4.9 credential-assignment path (`AssignCredentials`) that mints leases and populates the credential cache is not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| 6     | Interactive sessions, SDKs                                   | Partial        | The interactive-session endpoints, message injection, and replay are built. The Go, TypeScript, and Python client SDKs are not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 6.5   | Incremental load test (streaming)                            | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 7     | Policy engine (quotas, budgets, audit hooks)                 | Mostly done    | `pkg/circuitbreaker`, `pkg/idempotency`, quota enforcement, billing events, the usage endpoints, and the Redis breaker cache are built. The §11.4 three-tier user invalidation is built: soft/hard disable gate new work; full_revoke additionally transitions the user's non-terminal sessions to `cancelled` and dismisses their pending elicitations. The §11.4 full_revoke pod-RPC `Terminate` fan-out, Redis cached-auth invalidation, and credential-lease revocation remain infrastructure-bound. The external interceptor registration framework needs confirmation.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| 8     | Checkpoint/resume, drain-readiness webhook                   | Done           | The `lenny-drain-readiness` webhook, the §4.4 periodic-checkpoint path, and the §7.1 seal-and-export are complete. `workspace.Extract` restores a workspace from a gzip-tar, and the adapter `Resume` RPC rebuilds a replacement pod's workspace from a checkpoint. The gateway-side resume is complete: `adapterclient.Resume` drives the pod adapter's `Resume` RPC, `Binder.Resume` claims a fresh pod and restores onto it, and `handleResume` serves `POST /v1/sessions/{id}/resume` — valid only from `awaiting_client_action` per §15.1, restoring from the §7.1 `WorkspaceSnapshot` or rebuilding from the stored §14 `WorkspacePlan`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 9     | Delegation, delegation-echo                                  | Partial        | `pkg/delegation/cycle`, `pkg/delegation/lease`, `pkg/delegation/tracing`, and the gateway delegation service are built. The §8.5 platform MCP tool surface is complete: `lenny/create_session`, `lenny/send_message` (with `inReplyTo` input resolution), `lenny/get_task_tree`, `lenny/delegate_task`, `lenny/cancel_child`, `lenny/await_children`, `lenny/discover_agents`, `lenny/set_tracing_context`, `lenny/output`, and `lenny/request_input` (`pkg/gateway/inputwait` registry, §11.3 timeout). The delegation service propagates a parent's §8.3 tracingContext onto each child. The §4 `RequestInterceptor` chain framework (`pkg/gateway/interceptor`) is built; the `PreMessageDelivery` phase is wired into `lenny/send_message` and the `PreDelegation` phase into `lenny/delegate_task` (an optional `taskInput` runs the chain before the delegation — a REJECT blocks it, a MODIFY rewrites the input the child receives). The §8.10 `session_tree_archive` store (`pkg/gateway/treearchive`) is built with the archive-and-replay loop wired: child sessions are archived on every terminal transition (`lenny/cancel_child`, the sessionserver terminate / `DELETE` / start-failure paths, and the watchdog forced-failure and expiry sweeps), and `lenny/await_children` replays an archived child when its live session row is gone. The §8.10 `cascadeOnFailure` policy is applied on every terminal transition (`cancel_all` default cancels descendants, `detach` / `await_completion` leave them running, per-node policy honored), a resumed parent with active children receives the §7.1 `children_reattached` event, the §8.10 orphan-cleanup job (`pkg/gateway/orphancleanup`) sweeps every 60s to terminate orphans past the `cascadeTimeoutSeconds` window, and a `detach` cascade falls back to `cancel_all` when the tenant is over the `maxOrphanTasksPerTenant` cap, and the §8.10 bottom-up reattach traversal (`pkg/delegation/recovery`) groups failed tree nodes by depth and recovers them leaves-first under the `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds` deadlines. Not built: the `delegation-echo` runtime, the `PreExportMaterialization` interceptor wiring (needs the §8.7 file-export model), external gRPC interceptors, and the `ExtendLease` lease-extension control plane. |
| 9.5   | Incremental load test (delegation)                           | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 10    | MCP fabric, elicitation chain                                | Partial        | `pkg/elicitation` is built (`EnforcementMode` with `ResolveEffective`, `DepthPolicy` with `ShouldSuppress`, `InitiatorType`, `Content.Digest` / `VerifyContent`, `Provenance.Validate`). The `lenny/request_elicitation` platform MCP tool records a pending §9.2 elicitation in the shared interaction store and blocks until a human resolves it via the §15.1 respond/dismiss endpoints or the §9.1 `maxElicitationWait` timeout fires; the §9.1 `maxElicitationsPerSession` budget (default 50) drops an over-budget request. The tenant elicitation-content-integrity admin endpoints (`GET`/`PUT /v1/admin/tenants/{id}/elicitation-content-integrity`) are built, with justification-required weakening and the `tenant.elicitation_content_integrity_mode_changed` audit event. The `lenny_elicitation_dropped_total{reason}` metric counter is built and recorded on budget and depth-suppression drops. §9.2 depth-based elicitation suppression is wired into `lenny/request_elicitation` (the `suppress_at_depth` / `block_all` depth policies drop an agent elicitation raised too deep). Not built: the gateway virtual MCP server and the hop-by-hop elicitation chain (agent-interception forwarding up the task tree with content-integrity binding).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 11    | Advanced credentials, multi-provider translators, revocation | Partial        | The revocation cache exists. All §4.9 multi-provider LLM-proxy translators are built (`pkg/gateway/llmproxy`): `anthropic_direct`, `azure_openai`, `vertex_ai`, and `aws_bedrock`. The §4.9 `CredentialRenewalWorker` proactive-renewal loop (`pkg/gateway/credrenewal`, including emergency-revocation lease termination) and the `credentialPolicy` fallback chain (`pkg/gateway/credfallback`) are built. Not built: hot credential rotation (`credentials_rotated` / `credentials_acknowledged` adapter handshake) and cross-replica revocation propagation through Redis pub/sub.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 11.5  | Incremental load test (credential lifecycle)                 | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 12a   | Token Service hardening (KMS envelope, OAuth)                | Substrate only | `pkg/tokenexchange` exists. KMS envelope encryption and the full OAuth connector flow are not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 12b   | type: mcp runtime support                                    | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 12c   | Concurrent execution modes                                   | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 13    | Full observability, audit, lenny-backup, compliance          | Partial        | `pkg/audit` and the audit hash chain exist (the §11.7 `ChainIntegrity` / `ComplianceProfile` / `RetentionPreset` / `OCSFTranslationState` enums are built). The §12.8 GDPR erasure path is wired end to end. The dependency-ordered, fail-fast `DeleteByUser` orchestrator (`pkg/gateway/erasure`) covers user-scoped and session-scoped stores, with per-store erasure adapters on the SessionStore (in-memory and Postgres), the interaction store (`DeleteByUser`), and the transcript and blob stores (`DeleteBySession`, both the in-memory and MinIO blob backends). The erasure-job registry and runner (`pkg/gateway/erasurejob`) track the §12.8 phase field and drive a job `initiated → store_deleting → completed/failed`; `POST /v1/admin/users/{user_id}/erase` initiates a background job and `GET /v1/admin/erasure-jobs/{job_id}` reports its status, both wired into the running gateway. Initiating an erasure job marks the target user §12.8 / GDPR Article 18 processing-restricted (`processing_restricted` on the user record, persisted by migration 0009), so the session-creation gate rejects new sessions with `ERASURE_IN_PROGRESS` until the job completes. A completed job writes the §12.8 `gdpr.erasure_completed` receipt to the audit trail. §12.8 billing-event pseudonymization is built for the in-memory path: billing events are append-only, so the erasure Runner drives the `pseudonymizing` / `verifying` phases — `erasurejob.BillingEraser` resolves the tenant's `billingErasurePolicy` (`exempt` retains the original user id under GDPR Article 17(3)(b)), generates a per-tenant 256-bit salt, rewrites the user's billing events to their `SHA-256(user_id                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |     | erasure_salt)`pseudonym, destroys the salt, and verifies the result; the receipt records the disposition.`billingErasurePolicy`is settable through the tenant admin API, and an`exempt`tenant under a regulated compliance profile emits`compliance.billing_erasure_exempt_regulated`at create, update, and gateway startup. The §12.8 legal-hold control is built:`POST /v1/admin/legal-hold`sets and clears the`legal_hold`flag on a session (persisted by migration 0010), the §12.8 step-0 erasure preflight rejects an erasure with`409 ERASURE_BLOCKED_BY_LEGAL_HOLD`when the target user owns a held session, and a`platform-admin`may bypass the preflight via`acknowledgeHoldOverride`with a recorded justification (emitting`gdpr.legal_hold_overridden`). The §7.1 artifact-retention GC (`pkg/gateway/retentiongc`) is built and wired: a periodic sweep deletes the transcript and blobs of every terminal session past its retention deadline and exempts any session under a legal hold. Not built: artifact-level legal holds and the legal-hold ledger (audit-range and workspace-snapshot holds), the Postgres-backed billing pseudonymization and KMS envelope encryption of the `erasure_salt`, the database-level processing-restriction trigger, the `lenny-ops`runtime,`lenny-backup`, the compliance webhooks, the tenant-deletion controller, and the full observability catalog. |
| 13.5  | Pre-hardening full-system load baseline                      | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 14    | Comprehensive security hardening                             | Substrate only | `pkg/podsecurity` exists. The release pipeline, cosign verification, the final NetworkPolicy posture, and the pen-test driver are not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 14.5  | Post-hardening SLO re-validation                             | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 15    | Environment resource, RBAC                                   | Partial        | `pkg/environment` (the §10.6 Role enum and the tag-based Selector evaluator) and the environment admin API are built: `environmentstore` is the per-tenant Environment registry, and `/v1/admin/environments` serves POST/GET/list/PUT/DELETE with §10.6 resource validation (members, RBAC roles, runtime/connector selectors, mcp capability filters, bilateral cross-environment declarations). The `/runtime-exposure` sub-endpoint (selector evaluation over the runtime and connector registries) and the tenant cross-environment `access-report` (`GET /v1/admin/tenants/{id}/access-report`) are built. The cross-environment delegation resolver, OIDC-group resolution, the transparent-filtering middleware, the environment `/usage` rollup, and the environment `/access-report` (which needs OIDC-group expansion) are not built.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| 16    | Experiments, PoolScalingController integration               | Partial        | `pkg/experiment` (the §10.7 enums, HMAC bucketing, status-transition rules) and the experiment admin API are built: `experimentstore` is the per-tenant ExperimentDefinition registry, and `/v1/admin/experiments` serves POST/GET/list/PUT/PATCH/DELETE with §10.7 definition validation and the PATCH status-transition lifecycle. The §10.7 built-in eval surface is built: `evalstore` is the per-session EvalResult registry (with the `maxEvalsPerSession` storage bound and the §12.8 erasure adapter), `POST /v1/sessions/{id}/eval` ingests scores, and `GET /v1/admin/experiments/{name}/results` aggregates eval scores by variant (per-scorer count, mean, p50, p95, the per-dimension `scores` breakdown, the `delegation_depth` / `inherited` / `exclude_post_conclusion` query filters, and the `breakdown_by` per-bucket response shape) — the §10.7 results API is complete. The §10.7 `ExperimentRouter` is wired into session creation: `handleCreate` / `handleCreateAndStart` run the first-match `experiment.Route` over the tenant's active experiments matching the requested base runtime, record the `experimentContext` on the session, and route it onto the variant's runtime and pool — failing the session closed with `422 VARIANT_ISOLATION_UNAVAILABLE` when the variant pool's §5.3 isolation profile is weaker than the session's; the eval endpoint copies that context onto each EvalResult. The §10.7 admission-time isolation checks are enforced at `POST` / `PUT /v1/admin/experiments`: a variant pool weaker than the base runtime is rejected with `422 CONFIGURATION_CONFLICT`, and a variant pool weaker than the tenant's `minIsolationProfile` floor emits the advisory `experiment.variant_weaker_than_tenant_floor` event without rejecting. The §10.7 OpenFeature external-targeting integration and the PoolScalingController variant-pool sizing path are not built.                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 16.5  | Experiment load test SLO re-validation                       | Not started    |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| 17a   | Documentation, governance, community launch                  | Not started    | The first-party reference runtimes from §26, the installer wizard, the tier preset values files, and the web playground are not built.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 17b   | Memory, semantic caching, eval hooks                         | Partial        | The §9.4 MemoryStore role is built: `pkg/gateway/memorystore` is the tenant-scoped agent-memory store (Write / Query / Delete / List with strict tenant+user scoping, the per-user capacity-eviction limit, and the §12.8 `DeleteByUser` / `DeleteByTenant` erasure primitives), and the `lenny/memory_write` / `lenny/memory_query` platform MCP tools are wired. The §10.7 built-in eval endpoints (`POST /v1/sessions/{id}/eval`, `GET /v1/admin/experiments/{name}/results`) are built. The semantic-caching layer, the Postgres + pgvector memory backend, and the runtime-native eval-platform hooks are not.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |

## Implemented surface

The following are built and tested at the unit tier.

- **Gateway request surface.** Session lifecycle and REST endpoints, the admin API,
  derive, blob dereference, the upload pipeline, `uploadToken`, OIDC and OAuth JWT
  validation, the OpenAPI document, the MCP adapter, the OpenAI Chat and Open Responses
  translators, rate limiting, idempotency, circuit breakers, quotas, billing events,
  user invalidation, the §12.8 GDPR user-erasure endpoints, and the watchdog timers.
- **Storage.** Postgres-backed stores for sessions, transcripts, tenants, runtimes,
  users, connectors, billing events, issued tokens, and the audit hash chain; Redis
  layers for circuit breakers, session-coordination leases, quota counters, and storage
  quotas; the MinIO-backed §4.5 blob store (`pkg/blobstore/miniostore`); migrations 0001
  through 0008 with RLS, immutability triggers, and role separation.
- **Kubernetes control plane.** The five `lenny.dev/v1` CRDs with generated manifests;
  the WarmPoolController (the pure planner, the reconciler, the `PoolWarmingUp`
  condition, an envtest integration test) with the `lenny-controller` binary; the
  PoolScalingController (config sync, demand-driven minWarm, the periodic runnable); the
  Helm chart with the controller Deployment and RBAC, the agent namespaces, and the
  admission webhooks; the `lenny-webhook` binary.
- **Pure-logic substrate.** Tested Go packages exist for the warm-pod state machine,
  the isolation profiles, the sandbox-claim state, the scaling formula, the warm-pool
  and lifecycle planners, checkpoint enums, delegation cycle and lease arithmetic,
  elicitation, environment RBAC, experiments, idempotency, circuit breakers, quotas,
  the token-exchange invariants, the runtime-upgrade state machine, podsecurity
  validation, mTLS SPIFFE parsing, and the admission-decision packages.
- **Reference runtimes and tooling.** The `echo` and `streaming-echo` runtimes,
  `lenny-compliance`, `lenny-ctl`, the Token Service binary, the adapter gRPC
  contract with generated bindings, and the `tests/testinfra` harnesses for Kind,
  envtest, and Helm.

## Principal gaps

The gateway↔pod session path runs end to end through both `POST /v1/sessions/start` and
the two-step §15.1 `create → finalize → start` lifecycle. The remaining gaps, in
dependency order, are below.

- **gitClone ref resolution.** §15.1 pins `gitClone.resolvedCommitSha` at session
  creation. The gateway stores the submitted plan verbatim without resolving git refs.
- **Adapter workspace-staging RPCs.** §15.4 mandates separate `PrepareWorkspace`,
  `FinalizeWorkspace`, and `RunSetup` RPCs. The adapter bundles materialization, setup,
  and runtime start into `StartSession`, so the §15.1 finalize step short-circuits
  rather than materializing the workspace.
- **Remaining adapter RPCs.** The adapter serves `NegotiateVersion`, `StartSession`,
  `SendMessage`, `Interrupt`, `Shutdown`, `Attach`, the credential RPCs, and `DemoteSDK`.
  `Checkpoint`, `ReportUsage`, `ExtendLease`, and the `LifecycleChannel` stream are not
  built, and the adapter proto is still a Phase-1 skeleton behind §4.7 on the
  workspace-staging RPCs.
- **LLM Proxy.** Proxy-mode sessions cannot reach a provider; the LLM proxy subsystem
  and the `anthropic_direct` translator are not built.
- **Operational services.** `lenny-ops` and `lenny-backup` do not exist; `lenny-preflight`
  is built and deployable.
- **Custom-role permission enforcement.** The user-management, environment,
  credential-pool, and custom-role admin endpoints are permission-gated through
  `Router.requirePermission`, which resolves a principal's roles to the §10.2
  permission set with `principalGrantsPermission` (built-in roles via
  `auth.RolePermissions`, custom roles via the `customrolestore` registry).
  `requireTenantResourceAdmin` still gates the experiment routes — §10.2 defines no
  permission for experiments, so they stay built-in-only — and the runtime and pool
  read routes. The session-API gates still match `HasRole`, so a session-scoped custom
  role is not yet enforced on the session endpoints.
- **Built-in-role gating conformance.** Two `requireAdmin` (platform-admin-only)
  groupings may diverge from the §10.2 matrix. The matrix grants `tenant-admin`
  "Manage delegation policies" for its own tenant, but `/v1/admin/delegation-policies`
  is platform-admin-only; the `delegationpolicystore` is a platform-global registry,
  so whether create is platform-scoped while update is tenant-scoped needs a §8.3 /
  §15.1 decision. Likewise §10.2 line 298 lets a `tenant-admin` update a granted
  runtime or pool, but `PUT /v1/admin/runtimes/{name}` and `PUT /v1/admin/pools/{name}`
  are platform-admin-only.

Phases 13 through 17b are largely unbuilt beyond their logic substrate. The audit
pipeline, the compliance webhooks, the GDPR erasure pipeline, the backup and restore
surface, concurrent execution modes, the environment and experiment integrations, the
client SDKs, the first-party reference runtimes, and the web playground all remain.

## Critical path to an end-to-end Kubernetes session

The shortest route to running one real session on a warm pod. The first item is in
progress; see the progress log.

1. Build the §4.7 runtime adapter server. The gRPC scaffold, `cmd/lenny-adapter`,
   `StartSession`, `SendMessage`, `Shutdown`, `NegotiateVersion`, the credential RPCs
   (`AssignCredentials`, `RotateCredentials`, `RevokeCredentials`), `Interrupt`, and
   `DemoteSDK` are done, and the parameterized `Dockerfile` builds the adapter image;
   the remaining RPCs are `Checkpoint`, `ReportUsage`, `ExtendLease`, and
   `LifecycleChannel`.
2. Build the pod-spec builder. Done — `pkg/controller/sandbox/podspec`.
3. Build the Sandbox-to-Pod reconciler. Done — `pkg/controller/sandbox`, registered
   in `cmd/lenny-controller`.
4. Build the gateway pod-claim path against `SandboxClaim`. Done — `pkg/gateway/podclaim.Claimer`,
   driven by `podsession.Binder` and wired into `cmd/lenny-gateway` behind `--agent-namespace`.
5. Wire workspace materialization and session start from the gateway to the adapter.
   Done — `pkg/gateway/adapterclient` plus `pkg/gateway/podsession`, with the
   `cmd/lenny-gateway` `--agent-namespace` wiring. `POST /v1/sessions/start` claims a
   pod, runs the §15.5 handshake and StartSession, and records the binding.
6. Build the LLM Proxy so a credential-proxied session can reach a provider.

**The `Attach` content path.** The per-message agent-response round-trip uses the
`Attach` bidirectional-streaming RPC (§4.7 RPC table, §15.4). It is built end to end:
the proto RPC (`3128fa7`), the adapter-server handler (`a21b2bf`), the gateway-side
`adapterclient.Attach` (`3390203`), and `executor.PodExecutor` (`c11f002`), which the
gateway selects as its `Executor` when `--agent-namespace` is set.

**Adapter §4.7 RPC surface.** The adapter `Server` implements `StartSession`,
`SendMessage`, `Attach`, `AssignCredentials`, `RotateCredentials`, `RevokeCredentials`,
`Interrupt`, `Checkpoint`, `Resume`, `ReportUsage`, `Shutdown`, `DemoteSDK`, and
`NegotiateVersion`. The remaining proto RPCs `ExtendLease` and `LifecycleChannel` are
still behind the Phase-1 skeleton (the embedded `UnimplementedAdapterServer` answers
`Unimplemented`).

## Next step

Phase 9 (§18.23) is substantially built — the §8.5 platform MCP tool surface, the §4
`RequestInterceptor` chain framework, the §4.7 `ReportUsage` adapter RPC, and the full
§8.10 delegation-tree recovery (archive-and-replay, `cascadeOnFailure` with the orphan
cap, `children_reattached`, orphan-cleanup job, bottom-up reattach traversal). Three
Phase 9 items remain blocked on infra or a spec ambiguity and are deferred:

- `delegation-echo` is a Standard-level runtime — §15.4.3 requires it to connect to the
  adapter's local platform MCP server over an abstract Unix socket with the manifest
  `mcpNonce` handshake. That intra-pod MCP-server infrastructure is not built.
- `ExtendLease`'s direction needs settling: the proto places it in the `Adapter`
  service (gateway → pod) but the §8.6 prose describes the adapter requesting budget
  from the gateway.
- The `PreExportMaterialization` interceptor phase reuses the built chain framework but
  needs the §8.7 file-export model for its per-file payload. (`PreDelegation` is now
  wired into `lenny/delegate_task`.)

Phase 10 (§18.25, MCP fabric / elicitation chain) is substantially built:
`pkg/elicitation`, the `lenny/request_elicitation` tool, the §9.1
`maxElicitationsPerSession` budget, the tenant elicitation-content-integrity admin
endpoints, the `lenny_elicitation_dropped_total` counter, and §9.2 depth-based
suppression. The remaining Phase 10 items — the gateway virtual MCP server (§9.1) and
the hop-by-hop elicitation chain (agent-interception forwarding up the task tree with
content-integrity binding) — are deferred: both are substantial orchestration with
uncertain v1 substrate.

Phase 11 (§18.26, advanced credentials / multi-provider translators) is substantially
built: all four §4.9 LLM-proxy translators, the `CredentialRenewalWorker`
proactive-renewal loop (`pkg/gateway/credrenewal`, including emergency-revocation
lease termination — a revoked credential's leases are dropped on the next sweep), and
the `credentialPolicy` fallback chain (`pkg/gateway/credfallback`). The two remaining
Phase 11 items are infra-coupled and deferred: hot rotation needs the adapter
lifecycle channel for the `credentials_rotated` / `credentials_acknowledged`
handshake, and cross-replica revocation propagation needs Redis pub/sub.

Phases 12–13: the pure substrate is already in place — `pkg/tokenexchange` (RFC 8693
`Validate`), the `pkg/audit` §11.7 enums, and the audit hash chain are all built. The
§12.8 GDPR erasure path is wired end to end: the `pkg/gateway/erasure` orchestrator
covers user-scoped stores (session, interaction) and session-scoped stores (transcript,
blob), the `pkg/gateway/erasurejob` registry and runner track the §12.8 phase field,
`POST /v1/admin/users/{user_id}/erase` plus `GET /v1/admin/erasure-jobs/{job_id}` are
served by the running gateway, and an erasure job marks the user §12.8 / GDPR Article
18 processing-restricted so new session creation is rejected with `ERASURE_IN_PROGRESS`
until it completes. The bulk of the remaining Phase 12–16 surface is infrastructure
integration that requires external systems to realize authentically — KMS envelope
encryption, the OAuth 2.1 connector flow, Redis pub/sub propagation, Kubernetes
concurrent-execution slots, the `lenny-backup` pipeline, SIEM streaming, and the
security-hardening release pipeline.

The §12.8 legal-hold control is built end to end: the `legal_hold` flag on sessions,
`POST /v1/admin/legal-hold`, the step-0 erasure preflight, the `acknowledgeHoldOverride`
bypass, and the §7.1 artifact-retention GC (`pkg/gateway/retentiongc`) that collects
expired-TTL session artifacts and exempts held sessions. The §11.4 `full_revoke`
SessionStore-side steps are built: non-terminal sessions are transitioned to
`cancelled` and the user's pending elicitations are dismissed. The §11.4 pod-RPC
`Terminate` fan-out, Redis cached-auth invalidation, and credential-lease revocation
remain infrastructure-bound.

The Phase 15 environment admin API and the Phase 16 experiment surface are built end to
end: the experiment admin CRUD, the §10.7 built-in eval endpoints, the complete results
API (dimensions, filters, `breakdown_by`), the `ExperimentRouter` that assigns a variant
at session creation, and the eval-attribution flow. The §9.4 MemoryStore role and its
`lenny/memory_write` / `lenny/memory_query` MCP tools are built, and the §4
`PreDelegation` interceptor phase is wired into `lenny/delegate_task`. The remaining
work is predominantly infrastructure-coupled: the PoolScalingController variant-pool
sizing path, the OpenFeature external-targeting integration, the Postgres / Redis /
pgvector production backends, the Kubernetes control-plane completions, and the
security-hardening release pipeline; plus the Phase 17a documentation, reference
runtimes, and installer work. The §10.7 experiment surface is complete: the
admission-time isolation checks at experiment creation (the hard variant-pool
monotonicity rejection and the tenant-floor advisory) and the routing-time
`ExperimentRouter` isolation-monotonicity fail-closed check, where a session routed to
a variant pool weaker than its own §5.3 profile is rejected with
`422 VARIANT_ISOLATION_UNAVAILABLE`, the `experiment.isolation_mismatch` operational
event is emitted, and `lenny_experiment_isolation_rejections_total` is incremented. The
§5.3 tenant `minIsolationProfile` is a stored tenant field; its only spec-defined
consumer is the §10.7 admission-time advisory check, which is built. The spec defines
no session-creation-time tenant-floor enforcement (§7.1 populates `sessionIsolationLevel`
from the assigned pool's configuration), so the field has no further pure-Go consumer.
The `PreExportMaterialization` interceptor remains blocked on the §8.7 file-export model.

§12.8 tenant-controlled billing erasure is complete for the in-memory path. The erasure
Runner drives the `pseudonymizing` → `verifying` phases after store deletion: the
`erasurejob.BillingEraser` resolves the tenant's `billingErasurePolicy`, generates a
per-tenant 256-bit salt, rewrites the user's billing events to their
`SHA-256(user_id || erasure_salt)` pseudonym, destroys the salt, and runs the §12.8
post-pseudonymization verification; the `gdpr.erasure_completed` receipt records the
disposition. `billingErasurePolicy` is settable through the tenant admin API, and an
`exempt` tenant under a regulated compliance profile (`hipaa`, `fedramp`, `soc2`) emits
the `compliance.billing_erasure_exempt_regulated` audit event at create, update, and
gateway startup. The only deferred pieces are infrastructure: the Postgres-backed
`PseudonymizeUser` needs an UPDATE under the `lenny_erasure` role (the
role-switched-connection infrastructure the audit-erasure path also needs), and KMS
envelope encryption of the salt is part of Phase 12a.

The §11.7 compliance-profile lifecycle is built: `PUT /v1/admin/tenants/{id}` enforces
the one-way downgrade ratchet (`none < soc2 < fedramp < hipaa`), rejecting a lowering
transition with `422 COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`, and
`POST /v1/admin/tenants/{id}/compliance-profile/decommission` is the attested,
platform-admin-only wind-down path that emits `compliance.profile_decommissioned`. The
parallel `workspaceTier` stricter-only ratchet is referenced by §15.1 and §11.7 but no
error code is specified for a tenant-update downgrade rejection (§12.9 only defines the
environment-override stricter-only rule and the write-time `CLASSIFICATION_CONTROL_VIOLATION`);
that rejection is deferred pending the spec detail rather than inventing a code.

## Test status

The unit test tier is green. The component, contract, integration, end-to-end, load,
chaos, and security tiers exist as directory structures with scaffolds; most are skipped
without the corresponding infrastructure. The WarmPoolController has an envtest
integration test that runs against a real Kubernetes API server.
