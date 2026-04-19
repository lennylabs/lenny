## 14. Workspace Plan Schema

The `WorkspacePlan` is the declarative specification for how a session's workspace should be prepared.

**Concurrent-workspace mode scope note.** In `concurrencyStyle: workspace` pools, the `WorkspacePlan` serves as a shared template: the same sources, setup commands, and options are materialized independently for every slot on the pod (each into its own `/workspace/slots/{slotId}/current/` directory). Per-slot workspace differentiation — different files or environment per slot — is intentionally out of scope. All slots on a given pod are assigned tasks from sessions that share the same workspace plan; the pool model relies on this uniformity to pre-warm pods with a single workspace template. Clients that require different workspace content per task should create separate sessions (each with its own `WorkspacePlan`) rather than using per-slot overrides.

```json
{
  "pool": "claude-worker-sandboxed-medium",
  "isolationProfile": "gvisor",
  "workspacePlan": {
    "$schema": "https://schemas.lenny.dev/workspaceplan/v1.json",
    "schemaVersion": 1,
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
    "preferredSource": "pool"
  },
  "callbackUrl": "https://ci.example.com/hooks/lenny-complete",
  "callbackSecret": "whsec_...",
  "delegationLease": {
    "maxDepth": 2,
    "maxChildrenTotal": 5,
    "delegationPolicyRef": "default-policy"
  }
}
```

**Field notes:**

- `workspacePlan.setupCommands[].timeoutSeconds`: Optional per-command timeout. When omitted, the command has no independent time limit and runs until the runtime's aggregate `setupPolicy.timeoutSeconds` cap ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)) terminates the entire setup phase. If `setupPolicy.timeoutSeconds` is also absent the command runs until the pod is killed by an external deadline. Clients that need per-command bounds SHOULD set this field explicitly.
- `env`: Key-value environment variables injected into the agent session. Validated against a deployer-configured blocklist of denied environment variable names/patterns. The blocklist supports both exact names and glob patterns using `*` (full-string wildcard — matches any sequence of zero or more characters including `_`; case-sensitive). Examples: `AWS_SECRET_ACCESS_KEY` (exact), `ANTHROPIC_API_KEY` (exact), `*_SECRET_*` (glob — matches any key containing `_SECRET_`), `*_KEY` (glob — matches any key ending in `_KEY`), `*_PASSWORD` (glob — matches any key ending in `_PASSWORD`). The gateway applies blocklist matching at session creation time; any env var whose name matches any entry (exact or glob) is rejected with `400 ENV_VAR_BLOCKLISTED` identifying the offending key name and the matching pattern. Everything else is allowed. The platform ships a default blocklist that operators can extend but not reduce in multi-tenant mode.
- `labels`: User-defined metadata for querying and organizing sessions. Not used for internal routing. Labels are indexed in the session store and filterable in all query APIs: `GET /v1/sessions` (list), `GET /v1/usage` (usage reports), `GET /v1/metering/events` (billing events). This enables cost attribution by project, team, ticket, or any custom dimension.
- `timeouts`: Per-session overrides, capped by deployer policy. Cannot exceed the Runtime's `limits.maxSessionAge`.
- `callbackUrl`: Optional webhook. Gateway POSTs a [CloudEvents v1.0.2](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) envelope with session-terminal event data when the session reaches a terminal state (see "Webhook Delivery Model" below for the envelope and event types). Because this field accepts a URL from the client, it is a potential SSRF vector. The following mitigations apply:
  1. **URL validation.** The value must be an HTTPS URL (no HTTP, no non-HTTP schemes). It must parse as a valid URL with a public DNS hostname. IP literals, `localhost`, loopback addresses, and link-local addresses are rejected at submission time. Additionally, well-known cloud metadata hostnames (`metadata.google.internal`, `metadata.google.internal.`, `instance-data`) are rejected regardless of their resolved IP, as a defense-in-depth measure against non-standard metadata endpoint configurations.
  2. **DNS pinning.** The gateway resolves the hostname at registration time and pins the resolved IP. If the resolved IP falls within a private or reserved range (RFC 1918, RFC 6598, loopback, link-local, etc.) the callback is rejected. The callback `http.Client` uses a custom `DialContext` that connects directly to the pinned IP at the TCP level, with the original hostname set only in the `Host` header and TLS SNI. This prevents DNS rebinding attacks where the hostname re-resolves to an internal IP between validation and request time, and ensures the pinned IP — not a re-resolved address — is always the actual connection target.
  3. **Isolated callback worker.** Callback HTTP requests are made from a dedicated goroutine pool with its own `http.Client` configured with: connect timeout of 5 s, response-read timeout of 10 s, `CheckRedirect` returning an error (no redirect following), and egress through a separate network path where possible. At minimum, the gateway's `NetworkPolicy` `except` clauses on the external HTTPS egress rule ([Section 13.2](13_security-model.md#132-network-isolation)) block callback traffic from reaching cluster-internal CIDRs (pod network, service network, node metadata endpoints).
  4. **Optional domain allowlist.** Deployers can set `callbackUrlAllowedDomains` in the platform configuration. When the list is non-empty, only callback URLs whose hostname matches an entry (exact or `*.suffix` wildcard) are accepted. When the list is empty, the public-DNS validation in (1) applies.

  **Webhook Delivery Model.** The callback URL receives [CloudEvents v1.0.2](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) JSON-mode events — the same envelope used across Lenny's EventBus ([§12.6](12_storage-architecture.md#126-interface-design)), SSE stream ([§25.4](25_agent-operability.md#254-the-lenny-ops-service)), and §25 event-subscription webhooks. Receivers read the session-event-specific body from the `data` field.

  **Payload schema:**

  ```json
  {
    "specversion": "1.0",
    "id": "t_acme:gw-7f4c2:1718203320000000000:9f3a",
    "source": "//lenny.dev/gateway/gw-7f4c2",
    "type": "dev.lenny.session_completed",
    "time": "2026-04-17T10:30:00Z",
    "datacontenttype": "application/json",
    "subject": "session/sess_abc123",
    "lennytenantid": "t_acme",
    "data": {
      "session_id": "sess_abc123",
      "status": "completed",
      "usage": { "inputTokens": 15000, "outputTokens": 8000 },
      "artifacts": ["workspace.tar.gz"]
    }
  }
  ```

  `id` serves as the idempotency key — receivers MUST deduplicate by CloudEvents `id`. `time` replaces the previous `timestamp` field. `type` is `dev.lenny.<short_name>` where `<short_name>` matches the event-type catalog in [§16.6](16_observability.md#166-operational-events-catalog).

  **Authentication:** Webhooks are signed with HMAC-SHA256. The `X-Lenny-Signature` header format is `t=<unix_seconds>,v1=<hex_signature>`. The signing input is `"<unix_seconds>.<raw_body_bytes>"`. A replay window of 5 minutes is enforced: receivers MUST reject events where `abs(current_time - t) > 300s`. The `t` value is the **delivery timestamp** (when the gateway generated this specific HMAC for transmission), NOT the event's `time` attribute inside the CloudEvents envelope. Retried deliveries re-sign with a fresh `t` each attempt; the CloudEvents `time` attribute remains fixed (it is the event's original emission timestamp). Receivers MUST use `t` for replay-window validation, never the CloudEvents `time` — otherwise a retried delivery of a stale event would be rejected even though the current delivery attempt is fresh. The signing secret is provided by the client at session creation (`callbackSecret` field). **`callbackSecret` storage:** The secret is stored in the `sessions` table as KMS-envelope-encrypted ciphertext using the same KMS backend as credential pool secrets ([Section 4.9](04_system-components.md#49-credential-leasing-service)). The `lenny_app` database role can `SELECT` the ciphertext column, but only the gateway process with KMS `Decrypt` permission can recover the plaintext. The plaintext is never returned by any API endpoint — `callbackSecret` is a write-only field. When the session reaches a terminal state and all webhook delivery attempts are exhausted or have succeeded, the gateway sets the column to `NULL`. GDPR erasure ([Section 12.9](12_storage-architecture.md#129-data-classification)) pseudonymizes or deletes the column as part of session data purge. The `callbackSecret` is classified as T3 data ([Section 12.9](12_storage-architecture.md#129-data-classification)).

  **Per-event `data` schemas** (the CloudEvents `data` field):
  - `dev.lenny.session_completed`: `{ "session_id", "status": "completed", "usage": { "inputTokens": N, "outputTokens": N }, "artifacts": ["<name>"] }`
  - `dev.lenny.session_failed`: `{ "session_id", "status": "failed", "error": { "code": "<error_code>", "message": "<string>" }, "usage": { "inputTokens": N, "outputTokens": N } }`
  - `dev.lenny.session_terminated` (admin or system termination; the session's external state is `completed` — this event type distinguishes operator-initiated completion from agent-initiated): `{ "session_id", "reason": "<string>", "terminatedBy": "<admin|system>" }`
  - `dev.lenny.session_cancelled` (user/runtime cancelled; the session's external state is `cancelled`): `{ "session_id", "reason": "<string>" }`
  - `dev.lenny.session_expired` (maxSessionAge or maxIdleTimeSeconds): `{ "session_id", "expiryReason": "max_session_age|max_idle_time" }`
  - `dev.lenny.session_awaiting_action`: `{ "session_id", "actionRequired": "<string>", "resumeUrl": "<string>" }`
  - `dev.lenny.delegation_completed`: `{ "parent_session_id", "childSessionId": "<id>", "status": "completed|failed|cancelled|expired", "usage": { "inputTokens": N, "outputTokens": N } }`

  **Retry behavior:** Failed deliveries (non-2xx response or timeout) are retried with exponential backoff: 10 s, 30 s, 60 s, 300 s, 900 s (5 attempts total). After exhaustion, the event is marked as undelivered and queryable via `GET /v1/sessions/{id}/webhook-events`.

  **Idempotency:** Deduplicate by CloudEvents `id`. Within a Lenny release, `id` collisions are astronomically improbable (ULID-like time + nonce composition); across releases, deduplication still holds because `id` embeds the originating gateway replica ID and nanosecond timestamp.

- `credentialPolicy`: Per-session override hints for credential assignment. `preferredSource` can be `pool`, `user`, `prefer-user-then-pool`, or `prefer-pool-then-user`. If omitted, the tenant-level credential policy is used. Per-session overrides can only restrict, not expand, the tenant policy. See [Section 4.9](04_system-components.md#49-credential-leasing-service).
- `runtimeOptions`: Runtime-specific options passed through to the agent binary. The field is a **per-runtime discriminated union** — the active schema is determined by the target Runtime's registered `runtimeOptionsSchema`. If the target Runtime defines a `runtimeOptionsSchema` (a JSON Schema document registered at runtime registration time), the gateway validates `runtimeOptions` against it at session creation time and rejects invalid options with a descriptive error (`400 RUNTIME_OPTIONS_INVALID`, including a JSON Schema validation report). If no schema is registered, options are passed through as-is (backward compatible) but a `RuntimeOptionsUnschematized` warning event is emitted. Maximum size: 64 KB.

  **Built-in runtime `runtimeOptions` schemas.** The platform ships documented schemas for all built-in runtimes. Third-party runtimes MUST register a schema to be listed as schema-validated in the runtime catalogue.

  **`claude-code` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "model":            { "type": "string", "description": "Claude model ID (e.g. claude-opus-4-5)" },
      "settingSources":   { "type": "array", "items": { "type": "string", "enum": ["project", "user", "global"] }, "description": "Ordered list of settings sources" },
      "streamingMode":    { "type": "boolean", "default": true, "description": "Enable SSE streaming output" },
      "maxTokens":        { "type": "integer", "minimum": 1, "maximum": 200000, "description": "Override max output tokens" },
      "temperature":      { "type": "number", "minimum": 0, "maximum": 1, "description": "Sampling temperature. Note: the Anthropic Messages API accepts `temperature` in [0, 1]; other first-party runtimes below (openai-assistants, gemini-cli, codex, chat) use [0, 2] per their respective provider APIs." },
      "thinkingBudget":   { "type": "integer", "minimum": 0, "description": "Extended thinking token budget; 0 disables thinking" }
    },
    "additionalProperties": false
  }
  ```

  **`langgraph` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "graphModule":      { "type": "string", "description": "Python dotted path to the LangGraph graph object" },
      "checkpointBackend":{ "type": "string", "enum": ["memory", "postgres", "redis"], "default": "postgres" },
      "recursionLimit":   { "type": "integer", "minimum": 1, "maximum": 500, "default": 25 },
      "configSchema":     { "type": "object", "description": "Runtime-specific config forwarded as LangGraph RunnableConfig.configurable" }
    },
    "required": ["graphModule"],
    "additionalProperties": false
  }
  ```

  **`openai-assistants` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "assistantId":      { "type": "string", "description": "OpenAI Assistants API assistant ID (asst_...)" },
      "model":            { "type": "string", "description": "Optional model override; defaults to the assistant's configured model" },
      "temperature":      { "type": "number", "minimum": 0, "maximum": 2 },
      "parallelToolCalls":{ "type": "boolean", "default": true },
      "responseFormat":   { "type": "string", "enum": ["text", "json_object", "json_schema"], "default": "text" }
    },
    "required": ["assistantId"],
    "additionalProperties": false
  }
  ```

  **`gemini-cli` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "model":            { "type": "string", "description": "Gemini model ID (e.g. gemini-2.5-pro)" },
      "streamingMode":    { "type": "boolean", "default": true },
      "maxTokens":        { "type": "integer", "minimum": 1, "maximum": 1000000, "description": "Override max output tokens" },
      "temperature":      { "type": "number", "minimum": 0, "maximum": 2 },
      "thinkingBudget":   { "type": "integer", "minimum": 0, "description": "Extended thinking token budget; 0 disables thinking" }
    },
    "additionalProperties": false
  }
  ```

  **`codex` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "model":             { "type": "string", "description": "OpenAI model ID used by Codex (e.g. gpt-5-codex)" },
      "streamingMode":     { "type": "boolean", "default": true },
      "maxTokens":         { "type": "integer", "minimum": 1, "maximum": 200000 },
      "temperature":       { "type": "number", "minimum": 0, "maximum": 2 },
      "reasoningEffort":   { "type": "string", "enum": ["low", "medium", "high"], "description": "Codex reasoning effort hint" }
    },
    "additionalProperties": false
  }
  ```

  **`cursor-cli` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "model":            { "type": "string", "description": "Cursor agent model selection (e.g. cursor-auto, cursor-fast)" },
      "streamingMode":    { "type": "boolean", "default": true },
      "rulesFile":        { "type": "string", "description": "Relative path under /workspace/current to a rules file (e.g. .cursor/rules)" }
    },
    "additionalProperties": false
  }
  ```

  **`chat` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "model":            { "type": "string", "description": "Provider model ID; provider is inferred from the pool's credential identity" },
      "temperature":      { "type": "number", "minimum": 0, "maximum": 2 },
      "maxTokens":        { "type": "integer", "minimum": 1, "maximum": 1000000 },
      "systemPrompt":     { "type": "string", "description": "Prepended as a system-role message" }
    },
    "additionalProperties": false
  }
  ```

  **`mastra` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "agentModule":      { "type": "string", "description": "TypeScript module path to the exported Mastra Agent" },
      "configSchema":     { "type": "object", "description": "Runtime-specific config forwarded to the Mastra Agent at construction" }
    },
    "required": ["agentModule"],
    "additionalProperties": false
  }
  ```

  **`crewai` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "crewModule":       { "type": "string", "description": "Python dotted path to the exported Crew object" },
      "process":          { "type": "string", "enum": ["sequential", "hierarchical"], "default": "sequential" },
      "verbose":          { "type": "boolean", "default": false },
      "configSchema":     { "type": "object", "description": "Runtime-specific config forwarded to the Crew at kickoff" }
    },
    "required": ["crewModule"],
    "additionalProperties": false
  }
  ```

  All built-in runtime schemas above are published at `https://schemas.lenny.dev/runtime-options/<runtime-name>/v1.json`. Reference runtimes in [§26](26_reference-runtime-catalog.md) declare their `runtimeOptionsSchema` field as a `$ref` to these URLs rather than inlining the schema body, so the canonical schema lives in one place.

  Custom runtimes declare their schema in the `runtimeOptionsSchema` field of the `RuntimeDefinition` ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)). Derived runtimes ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime), Derived Runtime) inherit the base runtime's schema; they MAY narrow it by registering a stricter schema but MAY NOT declare properties that the base schema forbids. **Validation:** at derived-runtime registration the gateway computes `derived.properties.keys() − base.properties.keys()`; if the difference is non-empty the registration is rejected with `INVALID_DERIVED_RUNTIME: runtimeOptionsSchema declares forbidden property '<name>'` for each offending property name. Constraints on existing properties (e.g. tightened `minimum`/`maximum`, added `enum`, changed `default`) are permitted.

---

### 14.1 WorkspacePlan Schema Versioning

**Published JSON Schema.** The full `WorkspacePlan` object is published as a canonical JSON Schema (Draft 2020-12) at `schemas/workspaceplan-v1.json` in the repository, served at the stable URL `https://schemas.lenny.dev/workspaceplan/v1.json`. The schema covers every field documented in this section — `sources[]`, `setupCommands[]`, `env`, `labels`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `runtimeOptions`, and `delegationLease`. Clients MAY reference the schema via the optional `$schema` keyword on their `workspacePlan` object for local validation; the gateway performs identical validation at `POST /v1/sessions` and `POST /v1/sessions/start` and rejects malformed plans with `400 WORKSPACE_PLAN_INVALID` carrying a JSON Schema validation report. The `runtimeOptions` subobject is validated against its Runtime-specific schema ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)) in a second pass; the top-level WorkspacePlan schema treats `runtimeOptions` as an opaque object.

**`schemaVersion` field.** The `workspacePlan` object carries a `schemaVersion` integer field (shown as `"schemaVersion": 1` in the example above). This field identifies the schema revision used when the plan was written and governs forward-compatibility obligations for all consumers of persisted workspace plans. `schemaVersion` is distinct from the JSON Schema `$schema` URL: the former is Lenny's wire-compat identifier; the latter references the publication that defines the wire shape for a given major schema version.

**Producer obligation.** The gateway MUST set `schemaVersion` to the highest version required by the fields it writes. When a new `schemaVersion` introduces new source types, new setup-command fields, or changes field semantics, producers MUST set `schemaVersion` to that version so consumers can detect the presence of fields they may not understand.

**Consumer obligations by consumer type.** `WorkspacePlan` is persisted in Postgres as part of the session record and retained for the session's lifetime. Two consumer categories apply:

- **Gateway reconciliation (live consumer):** The gateway reads back the stored `WorkspacePlan` when replaying workspace setup for resumed or retried sessions. If the gateway encounters a `schemaVersion` higher than it understands (e.g., a plan written by a newer gateway version is read by an older one during a rollback), it MUST NOT proceed with workspace materialization — instead it MUST reject the operation with error code `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (HTTP 422), including `details.knownVersion` and `details.encounteredVersion`. Silently processing a plan at a higher schema version could materialize an incorrect workspace.

- **Audit / analytics consumers (durable consumers):** Audit pipelines, compliance exporters, and analytics queries that read session records containing embedded `WorkspacePlan` objects are **durable consumers** and MUST apply the forward-read rule from [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7: process all fields they understand and preserve unknown fields verbatim. They MUST NOT reject session records solely because the embedded `WorkspacePlan.schemaVersion` is unrecognized.

**Migration window SLA.** When a new `WorkspacePlan` `schemaVersion` is introduced, all gateway replicas (which are live consumers) MUST be upgraded to understand the new version within the platform's standard rolling-upgrade window (typically within one deployment cycle, ≤ 24 hours). Durable consumers MUST be upgraded within **90 days** per the general schema migration SLA in [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7.

**Backwards compatibility guarantee.** New `schemaVersion` values MUST NOT remove or rename existing `sources` types or `setupCommands` fields. Adding new optional fields or new `type` values to `sources` is a non-breaking addition (existing consumers ignore unknown `type` values in `sources` entries per the open-string extensibility contract below). Changing the semantics of an existing field or making a previously optional field required is a breaking change requiring a `schemaVersion` bump and a corresponding gateway upgrade gate.

**Unknown `source.type` handling.** The `type` field on each `sources` entry is an open string (not a closed enum). A consumer that encounters an unknown `source.type` MUST skip that source entry and emit a `workspace_plan_unknown_source_type` warning (fields: `schemaVersion`, `unknownType`) rather than rejecting the entire plan. This allows new source types to be added in minor releases without breaking existing readers.

**Path collision rule (within `sources` array).** Sources in the `sources` array are applied in declaration order; if two or more entries resolve to the same workspace path, the **later entry wins** (last-writer-wins). This is intentional and consistent with the cross-tier materialization order described in [Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime) (base defaults → derived defaults → client uploads → file exports from parent delegation), where each tier overwrites any same-path file from the preceding tier. Clients that want strict conflict detection MUST ensure their `sources` entries target non-overlapping paths. A future `schemaVersion` may introduce a per-plan `onConflict` field to override this default; until then, last-writer-wins is the only supported mode. The gateway emits a `workspace_plan_path_collision` warning event (fields: `path`, `winningSourceIndex`, `losingSourceIndex`) whenever a path collision is detected during materialization, so operators and clients can audit and correct unintended overwrites.

