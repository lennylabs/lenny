# Schema & Content Model Review Findings — SCH

**Perspective:** Content Model, Data Formats & Schema Design
**Date:** 2026-04-04
**Spec:** docs/technical-design.md
**Category code:** SCH

---

### SCH-001 `OutputPart.type` is an open string with no canonical registry, making protocol translation non-deterministic [High]

**Section:** 15.4.1 (Internal `OutputPart` Format), 15.4.1 (Translation Fidelity Matrix)

The spec states `type` is "an open string — not a closed enum" and lists examples (`"text"`, `"code"`, `"reasoning_trace"`, `"citation"`, `"screenshot"`, `"diff"`). The translation matrix notes that custom types are "collapsed to `text` with original type in `annotations.originalType`" for MCP, and similarly degraded for OpenAI and A2A. However, there is no canonical registry, no versioning for when a new type becomes "known," and no defined behaviour for what counts as a "custom" vs "platform-defined" type. Two adapter implementations could disagree about whether `"reasoning_trace"` is a known type (and therefore gets a specific MCP mapping) or a custom type (and therefore collapses to `text`). This ambiguity makes the translation layer non-deterministic across adapter versions and makes it impossible for runtime authors to know which types get fidelity-preserving treatment.

**Recommendation:** Publish a versioned **canonical type registry** (a structured list in the runtime adapter spec, not an enum in code) of platform-defined types and their guaranteed translation behaviour per adapter. Unknown types (anything not in the current registry version) fall back to `text` with `annotations.originalType`. Types can be added to the registry in minor releases; removing or changing translation behaviour is a breaking change. This retains the open-string extensibility while making translation deterministic.

---

### SCH-002 `MessageEnvelope.from` field is underspecified — no schema for the `from` object, only a prose description [High]

**Section:** 15.4.1 (`MessageEnvelope` — Unified Message Format)

The spec shows `"from": { "kind": "client | agent | system | external", "id": "..." }` but does not define what `id` contains for each `kind`. For `kind: "agent"`, does `id` hold the session ID, the runtime name, or the pod SPIFFE URI? For `kind: "external"`, is it the connector ID or an external agent URL? For `kind: "system"`, is it always a fixed string like `"gateway"`? The spec states these fields are "injected by the adapter from execution context," but adapter authors have no schema to implement against. Any implementation inconsistency here breaks multi-turn reply routing, because `inReplyTo` resolution depends on the receiver being able to identify the `from` source correctly.

**Recommendation:** Formally specify the `from` object schema: `kind` as a closed enum, and for each `kind`, the exact semantics and format of `id` (e.g., for `agent`: `sess_{id}`, for `external`: the registered connector ID, for `system`: the literal string `"lenny-gateway"`). Document this in the runtime adapter spec published in Section 15.4.

---

### SCH-003 `WorkspacePlan` has no `schemaVersion` field, making durable plans non-evolvable [High]

**Section:** 14 (Workspace Plan Schema)

Section 15.5 item 7 establishes that all Postgres-persisted record types carry a `schemaVersion` integer field, listing `TaskRecord`, billing events, audit events, checkpoint metadata, and session records. The `WorkspacePlan` — the declarative workspace specification submitted at session creation — is conspicuously absent from this list. `WorkspacePlan` objects submitted by clients are likely persisted to support session derivation (`POST /v1/sessions/{id}/derive`), resume flows (Section 7.3), and retry (Section 7.2 replays workspace materialization from scratch). If the plan's source types or field semantics change between releases, older persisted plans will be misread by newer gateway code without a version discriminator to select the correct deserialization path.

**Recommendation:** Add `schemaVersion: 1` to the `WorkspacePlan` schema and add `WorkspacePlan` to the list of durable record types in Section 15.5 item 7. Apply the same forward-compatibility rules: unknown fields ignored, unknown `schemaVersion` rejected with a structured error.

---

### SCH-004 `OutputPart.schemaVersion` forward-compatibility contract is one-sided — the "drop unknown fields" rule creates silent data loss for producers [High]

**Section:** 15.4.1 (Internal `OutputPart` Format)

The spec states: "consumers MUST ignore unknown fields and MUST NOT reject an `OutputPart` solely because its `schemaVersion` is higher than the consumer understands. When a consumer encounters a `schemaVersion` it does not recognize, it processes the fields it does understand and silently discards the rest." This is standard Postel's Law for forward compatibility. However, the spec provides no guidance for the **producer** side: when a new schema version adds a field that carries semantic weight (e.g., a `citations` field in v2), a v1 consumer that silently drops it may produce a coherent but materially incomplete response to the end user — with no indication that data was lost. For billing events retained 13 months and spanning multiple schema revisions, this silent loss is particularly dangerous.

**Recommendation:** Add a producer-side obligation: when emitting an `OutputPart` with semantically required fields introduced in version N, the producer MUST set `schemaVersion: N`. Consumers that encounter a version they do not recognize SHOULD surface a degradation signal (e.g., a `schema_version_too_new` annotation on the parent message, or a gateway warning event) rather than silently truncating. For billing and audit records, the rejection-at-read rule in Section 15.5 item 7 is stronger and correct — extend that rule explicitly to `OutputPart` when stored as part of a `TaskRecord`.

---

### SCH-005 Derived `Runtime` inheritance rules have ambiguous precedence for `setupPolicy.timeoutSeconds` when base and derived both set it [Medium]

**Section:** 5.1 (Inheritance Rules)

The spec states: "gateway takes maximum of base and derived `timeoutSeconds`." This means a derived runtime cannot impose a *stricter* (shorter) timeout than its base. This is the reverse of the "most-restrictive wins" principle that governs other inheritance rules (delegation policy, messaging scope). A derived runtime that represents a simpler, faster pipeline might legitimately want a timeout of 60 s on a base runtime configured at 300 s; under current rules it cannot. Worse, the rationale for the maximum-wins rule is not documented, leaving implementers uncertain whether it is intentional or an oversight.

More broadly, the inheritance table (Section 5.1) does not list all fields — `credentialCapabilities`, `runtimeOptionsSchema`, `supportedProviders`, and `egressProfile` are absent from both the "never overridable" and "independently configurable" categories. Their inheritance behaviour is unspecified.

**Recommendation:** (1) Document the rationale for `max(timeoutSeconds)` and, if intentional, note the tradeoff explicitly. If derived runtimes should be able to shorten timeouts, change to `min(base, derived)`. (2) Extend the inheritance table to cover every field in the Runtime schema, explicitly assigning each to one of: "not overridable," "can only restrict," "independently configurable," or "inherited unchanged." Leave no field unclassified.

---

### SCH-006 `TaskRecord.messages` array role field is a string enum without a defined closed set [Medium]

**Section:** 8.9 (TaskRecord and TaskResult Schema)

The `TaskRecord` schema shows:
```json
"messages": [
  { "role": "caller", "parts": ["OutputPart[]"] },
  { "role": "agent",  "parts": ["OutputPart[]"], "state": "completed" }
]
```
The `role` field uses string values `"caller"` and `"agent"` but the spec does not define this as a closed enum. When A2A support is added (Section 21.1), A2A maps to roles like `"user"` and `"agent"`. If `lenny/request_input` replies are injected as messages, do they carry `role: "agent"` (the question) and `role: "caller"` (the answer)? What role does a `lenny/send_message` from a sibling task carry? The `state` field on the agent message is also undefined as an enum.

**Recommendation:** Define `role` as a closed enum with values: `caller`, `agent`, `system`, `external` (aligning with `MessageEnvelope.from.kind`). Define `state` on a message as the `OutputPart.status` values (`streaming | complete | failed`), or remove it if it duplicates `TaskRecord.state`. Document how multi-turn injection messages and sibling messages are tagged.

---

### SCH-007 `CredentialLease.materializedConfig` is an untyped JSON object — no schema per provider type [Medium]

**Section:** 4.9 (Credential Lease)

The `CredentialLease` example shows `"materializedConfig": { "apiKey": "...", "baseUrl": "..." }` and notes it is "a provider-specific bundle with everything needed to authenticate." Each `CredentialProvider` implementation emits a different shape: `anthropic_direct` emits `{apiKey, baseUrl}`, `aws_bedrock` emits `{accessKeyId, secretAccessKey, sessionToken, region, endpoint}`, `vertex_ai` emits `{accessToken, projectId, region}`, etc. The `materializedConfig` object has no versioned schema per provider type. If a provider adds a field (e.g., `bedrock` adds `modelId` for ARN-scoped access), existing runtime adapters that parse this config will silently ignore it or misinterpret it. There is no way for a runtime to discover which fields are present without trial-and-error.

**Recommendation:** Define a per-provider `materializedConfig` schema (either as a JSON Schema document per provider or as a proto `oneof` in the `AssignCredentials` RPC). Include a `configSchemaVersion` field inside `materializedConfig` so runtimes can detect config changes. Publish these schemas as part of the runtime adapter specification (Section 15.4).

---

### SCH-008 `WorkspacePlan.sources` source types have no explicit validation schema — edge cases unspecified [Medium]

**Section:** 14 (Workspace Plan Schema)

The `sources` array in `WorkspacePlan` supports types `inlineFile`, `uploadFile`, `uploadArchive`, and `mkdir`. However:

1. For `inlineFile`, there is no defined maximum `content` size. The spec caps `runtimeOptions` at 64 KB but imposes no analogous limit on inline file content. A client could inline a 10 MB file in JSON, bypassing the upload flow and staging/validation pipeline.
2. For `uploadArchive`, the `format` field accepts `"tar.gz"` — but Section 7.4 says supported formats are `tar.gz`, `tar.bz2`, and `zip`. The `WorkspacePlan` example only shows `tar.gz`; the other formats are not represented. A client using `format: "zip"` in a `WorkspacePlan` source could hit a runtime error or undefined behaviour.
3. There is no `symlinkPolicy` or `allowSymlinks` flag at the `WorkspacePlan` level — the allowSymlinks option is documented on the Runtime (Section 7.4) but not surfaced in the plan schema, making it unclear whether a plan-level source can override it.
4. There is no defined ordering guarantee when multiple sources write to the same path (Section 5.1 mentions "Workspace materialization order: base defaults → derived defaults → client uploads → file exports," but within a single plan's `sources` array the ordering is not documented).

**Recommendation:** (1) Add a max `content` size for `inlineFile` sources (recommend 256 KB, beyond which clients must use `uploadFile`). (2) Document all supported `format` values in the `WorkspacePlan` schema, not just the example. (3) Add a `sources` ordering guarantee: later entries win on path conflicts. (4) Surface `allowSymlinks` explicitly as an optional field on `uploadArchive` sources, inheriting the Runtime default.

---

### SCH-009 `protocolHints` annotation key is not versioned and conflicts with the open `annotations` map contract [Medium]

**Section:** 15.4.1 (Translation Fidelity Matrix, `protocolHints` annotation field)

The spec introduces `annotations.protocolHints` as a special structured object within the open `annotations` map. The translation matrix already marks `annotations` as "Lossy" for OpenAI and "Lossy — mapped to A2A `metadata` map; nested objects flattened to JSON strings" for A2A. This means `protocolHints` intended for the gateway adapter will survive in the `annotations` map when the `OutputPart` is round-tripped through A2A (as a flattened JSON string), then re-ingested by the gateway. The gateway must then correctly re-parse `protocolHints` from a flattened string, which is fragile. There is also no versioning or namespace for `protocolHints` — a future gateway version that renames a hint key (`preferResourceBlock` → `useResourceBlock`) breaks existing runtimes silently.

**Recommendation:** (1) Move `protocolHints` out of `annotations` and into a dedicated top-level field on `OutputPart` (e.g., `"gatewayDirectives": { ... }`). The field is consumed by the adapter and excluded from all serialized wire formats, eliminating the round-trip contamination problem. (2) Version the field name itself: `"gatewayDirectives": { "schemaVersion": 1, "mcp": {...}, "openai": {...} }`.

---

### SCH-010 `DelegationLease` preset merging semantics are underspecified for conflicting scalar fields [Medium]

**Section:** 8.3 (Delegation Presets)

The spec allows partial override of a named preset:
```json
"delegationLease": { "preset": "standard", "maxDepth": 2 }
```
The merge rule — inline fields override preset fields — is implied but not stated. More importantly, the spec does not address: (a) what happens if a client supplies both a `preset` and a fully inlined `DelegationLease` object (does the preset win, the inline win, or is it an error?); (b) whether inline fields can increase a value above the preset (e.g., can `{ "preset": "simple", "maxChildrenTotal": 50 }` override the preset's `maxChildrenTotal: 3`?); (c) what the validation order is — are inline overrides validated against the preset ceiling, the tenant ceiling, or both? The delegation lease is a security boundary (it enforces scope, depth, and budget). An ambiguous merge rule is a potential bypass vector.

**Recommendation:** Formally specify the merge algorithm: (1) Start with the named preset's values as the base. (2) Apply inline fields as overrides. (3) Validate the merged result against tenant ceilings. (4) Inline overrides can decrease any field but cannot increase it above the preset's value (treating presets as upper bounds, not starting points). Callers who need more than a preset must use a larger preset or the fully inline form. Document whether a fully inline `DelegationLease` without a `preset` key is valid (it should be, for backward compatibility).

---

### SCH-011 `TaskRecord.messages` vs `TaskResult.output.parts` — two representations of agent output with no canonical relationship defined [Medium]

**Section:** 8.9 (TaskRecord and TaskResult Schema)

`TaskRecord` stores conversation history as `messages[].parts: OutputPart[]`. `TaskResult` (returned by `lenny/await_children`) stores final output as `output.parts: OutputPart[]`. The spec does not define the relationship between them: is `TaskResult.output.parts` a denormalised copy of the final `role: "agent"` message from `TaskRecord.messages`? Or is it a separate aggregation? If a multi-turn task has multiple agent messages (because `lenny/request_input` creates a back-and-forth), which messages contribute to `TaskResult.output.parts`? This ambiguity means a parent agent receiving a `TaskResult` cannot reliably know whether it has the complete output or only a final summary.

**Recommendation:** Define `TaskResult.output.parts` as the concatenation of all `OutputPart[]` arrays from messages with `role: "agent"` in the final settled state of `TaskRecord.messages`, in order. If the intent is only the final response (last agent message), state that explicitly. Add a `messageCount` field to `TaskResult` so the receiver can know how many turns the child took.

---

### SCH-012 `expires` task state maps to both `failed` and `canceled` in the MCP/A2A translation table without a canonical rule [Medium]

**Section:** 8.9 (Protocol mapping table)

The mapping table shows:
- `expired` → MCP: `"failed" + error code`
- `expired` → A2A: `"failed" + error metadata`

But in Section 7.2, the `session_complete` event carries `status: "completed"`, `status: "failed"`, and implicitly `status: "cancelled"` through the session state machine. The A2A spec (Section 21.1) notes "A2A `canceled` maps to Lenny's `cancelled`." This creates inconsistency: a session cancelled by the client maps to A2A `canceled`, but a session expired (which the user might reasonably consider a form of automatic cancellation) maps to A2A `failed`. The error code distinguishes the two, but many A2A client implementations will only inspect the top-level state and treat `expired` as a failure requiring retry, when it may instead require a completely new session. Additionally, there is no canonical error code enum defined for the `expired` → `failed` mapping — the spec only says "+ error code" without specifying what the code is.

**Recommendation:** (1) Define the canonical error code for `expired`: `DEADLINE_EXCEEDED` (matching gRPC convention) for budget/deadline expiry, and `SESSION_TIMEOUT` for `maxSessionAge` expiry. Include both codes in the error catalog (Section 15.1). (2) Consider mapping `expired` to A2A `canceled` (not `failed`) when the expiry reason is a configurable deadline rather than an error condition, to give A2A clients a meaningful distinction. Document the mapping decision and its rationale in the A2A adapter design.

---

### SCH-013 `RuntimeDefinition` capability inference from MCP `ToolAnnotations` has no fallback for annotation drift after registration [Low]

**Section:** 5.1 (Capability Inference from MCP `ToolAnnotations`)

The spec states: "Gateway reads `tools/list` at connector or `type:mcp` runtime registration and infers capabilities from MCP `ToolAnnotations`." Capabilities are inferred once, at registration time. If the upstream MCP server subsequently changes its `ToolAnnotations` (e.g., a tool previously marked `readOnlyHint: true` now becomes `destructiveHint: true`), Lenny's capability model retains the stale inferred value indefinitely. A connector that gains write/delete capability without re-registration would be authorized by the cached capability set for operations it should no longer perform — or, conversely, a connector that loses a capability (e.g., network access removed) remains blocked from operations it now safely supports. The spec mentions `toolCapabilityOverrides` for `execute` and `admin` capabilities that have no MCP equivalent, but does not describe how overrides interact with re-registration.

**Recommendation:** (1) Add a `capabilityRefreshPolicy` to `ConnectorDefinition`: `{ "mode": "registration-only | periodic | on-demand", "ttl": 3600 }`. The default can remain `registration-only` for v1. (2) Expose a `POST /v1/admin/connectors/{id}/refresh-capabilities` admin endpoint that re-fetches `tools/list` and updates the inferred capabilities. (3) Document what happens to existing sessions using the connector when capabilities are updated: they continue with the capabilities they were granted at session creation (capability grants are per-lease, not live).

---

### SCH-014 `agentInterface.outputModes` type field conflicts with `OutputPart.type` open-string semantics [Low]

**Section:** 5.1 (Derived Runtime, `agentInterface` field)

The `agentInterface` field includes:
```yaml
outputModes:
  - type: "text/plain"
    role: "primary"
```
Here `type` is a MIME type string (`text/plain`). In `OutputPart`, `type` is a semantic descriptor (`"text"`, `"code"`, `"reasoning_trace"`). These are different namespaces using the same field name in structures that appear structurally similar and are both used for content description. A runtime author reading the spec could reasonably confuse the two. The `agentInterface.outputModes` declaration is intended for discovery and A2A card auto-generation, not for run-time dispatch. There is no defined mapping between `outputModes[].type` (MIME) and the `OutputPart.type` (semantic) that a runtime actually emits.

**Recommendation:** Rename `agentInterface.outputModes[].type` to `mimeType` to eliminate the confusion. Separately, define the relationship: `outputModes` is a declared capability hint used for discovery; the actual `OutputPart.type` values emitted at runtime are not validated against this declaration. If validation is desired (e.g., a connector should only emit declared MIME types), add an opt-in `enforceOutputModes: true` flag on the runtime definition.

---

### SCH-015 `DeliveryReceipt` schema has no `schemaVersion` and its `status` enum is incompletely documented [Low]

**Section:** 7.2 (Delivery receipts)

The `deliveryReceipt` object returned by `lenny/send_message` and `lenny/send_to_child` is:
```json
{
  "messageId": "msg_abc123",
  "status": "delivered | queued | error",
  "targetState": "running",
  "queueTTL": null
}
```
The `status` field has three values but the spec only defines them in prose. If a new status is added (e.g., `"rejected"` distinct from `"error"`) in a future version, runtimes that have hardcoded a `switch` over the three known values will mishandle it. The `targetState` field contains a session state string but the set of valid values is not cross-referenced to the session state machine. `queueTTL` is described as seconds in the prose but the JSON shows `null` with no type annotation. `schemaVersion` is absent, making it impossible to evolve this schema without breaking existing runtimes.

**Recommendation:** (1) Add `schemaVersion: 1` to the `deliveryReceipt` schema. (2) Define `status` as a closed string enum with the forward-compatibility note that unrecognised values should be treated as `error`. (3) Explicitly type `queueTTL` as `integer | null` (seconds). (4) Cross-reference `targetState` to the canonical session state machine in Section 6.2.

---

### SCH-016 `artifact://` URI scheme is used throughout the spec but never formally defined [Low]

**Section:** 8.9 (TaskResult), 12.5 (Workspace lineage)

The spec uses `artifact://session_xyz/workspace.tar.gz` in `TaskResult.output.artifactRefs` and references `artifact://` in the context of A2A translation ("A2A artifacts map to Lenny artifact refs (`artifact://`)"). This URI scheme appears to be an internal Lenny reference format, but its structure is never formally specified: is it `artifact://{session_id}/{path}`, `artifact://{tenant_id}/{session_id}/{path}`, or something else? How is the scheme resolved — does it map to a MinIO path, a gateway endpoint, or an opaque handle? Section 21.1 notes A2A artifacts map to this scheme with "scheme may be rewritten," implying the scheme is not always preserved through A2A translation.

**Recommendation:** Define the `artifact://` URI format formally in a dedicated subsection (or as an appendix to Section 8.9): `artifact://{session_id}/{artifact_name}`, with a resolution rule: the gateway resolves this to `GET /v1/sessions/{session_id}/artifacts/{artifact_name}`, accessible to callers with session read permission. Note that the scheme is internal and must be rewritten to an HTTPS URL before being exposed to external A2A clients.
