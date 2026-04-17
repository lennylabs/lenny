## 14. Workspace Plan Schema

The `WorkspacePlan` is the declarative specification for how a session's workspace should be prepared.

**Concurrent-workspace mode scope note.** In `concurrencyStyle: workspace` pools, the `WorkspacePlan` serves as a shared template: the same sources, setup commands, and options are materialized independently for every slot on the pod (each into its own `/workspace/slots/{slotId}/current/` directory). Per-slot workspace differentiation — different files or environment per slot — is intentionally out of scope. All slots on a given pod are assigned tasks from sessions that share the same workspace plan; the pool model relies on this uniformity to pre-warm pods with a single workspace template. Clients that require different workspace content per task should create separate sessions (each with its own `WorkspacePlan`) rather than using per-slot overrides.

```json
{
  "pool": "claude-worker-sandboxed-medium",
  "isolationProfile": "gvisor",
  "workspacePlan": {
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
- `callbackUrl`: Optional webhook. Gateway POSTs a `SessionComplete` payload when the session reaches a terminal state. Because this field accepts a URL from the client, it is a potential SSRF vector. The following mitigations apply:
  1. **URL validation.** The value must be an HTTPS URL (no HTTP, no non-HTTP schemes). It must parse as a valid URL with a public DNS hostname. IP literals, `localhost`, loopback addresses, and link-local addresses are rejected at submission time. Additionally, well-known cloud metadata hostnames (`metadata.google.internal`, `metadata.google.internal.`, `instance-data`) are rejected regardless of their resolved IP, as a defense-in-depth measure against non-standard metadata endpoint configurations.
  2. **DNS pinning.** The gateway resolves the hostname at registration time and pins the resolved IP. If the resolved IP falls within a private or reserved range (RFC 1918, RFC 6598, loopback, link-local, etc.) the callback is rejected. The callback `http.Client` uses a custom `DialContext` that connects directly to the pinned IP at the TCP level, with the original hostname set only in the `Host` header and TLS SNI. This prevents DNS rebinding attacks where the hostname re-resolves to an internal IP between validation and request time, and ensures the pinned IP — not a re-resolved address — is always the actual connection target.
  3. **Isolated callback worker.** Callback HTTP requests are made from a dedicated goroutine pool with its own `http.Client` configured with: connect timeout of 5 s, response-read timeout of 10 s, `CheckRedirect` returning an error (no redirect following), and egress through a separate network path where possible. At minimum, the gateway's `NetworkPolicy` `except` clauses on the external HTTPS egress rule ([Section 13.2](13_security-model.md#132-network-isolation)) block callback traffic from reaching cluster-internal CIDRs (pod network, service network, node metadata endpoints).
  4. **Optional domain allowlist.** Deployers can set `callbackUrlAllowedDomains` in the platform configuration. When the list is non-empty, only callback URLs whose hostname matches an entry (exact or `*.suffix` wildcard) are accepted. When the list is empty, the public-DNS validation in (1) applies.

  **Webhook Delivery Model.** The callback URL receives structured webhook events with the following contract:

  **Payload schema:**

  ```json
  {
    "event": "session.completed",
    "session_id": "sess_abc123",
    "status": "completed",
    "timestamp": "2025-01-15T10:30:00Z",
    "idempotency_key": "evt_xyz789",
    "data": {
      "usage": { "inputTokens": 15000, "outputTokens": 8000 },
      "artifacts": ["workspace.tar.gz"]
    }
  }
  ```

  **Authentication:** Webhooks are signed with HMAC-SHA256. The `X-Lenny-Signature` header format is `t=<unix_seconds>,v1=<hex_signature>`. The signing input is `"<unix_seconds>.<raw_body_bytes>"`. A replay window of 5 minutes is enforced: receivers MUST reject events where `abs(current_time - t) > 300s`. The signing secret is provided by the client at session creation (`callbackSecret` field). **`callbackSecret` storage:** The secret is stored in the `sessions` table as KMS-envelope-encrypted ciphertext using the same KMS backend as credential pool secrets ([Section 4.9](04_system-components.md#49-credential-leasing-service)). The `lenny_app` database role can `SELECT` the ciphertext column, but only the gateway process with KMS `Decrypt` permission can recover the plaintext. The plaintext is never returned by any API endpoint — `callbackSecret` is a write-only field. When the session reaches a terminal state and all webhook delivery attempts are exhausted or have succeeded, the gateway sets the column to `NULL`. GDPR erasure ([Section 12.9](12_storage-architecture.md#129-data-classification)) pseudonymizes or deletes the column as part of session data purge. The `callbackSecret` is classified as T3 data ([Section 12.9](12_storage-architecture.md#129-data-classification)).

  **Per-event `data` schemas:**
  - `session.completed`: `{ "usage": { "inputTokens": N, "outputTokens": N }, "artifacts": ["<name>"] }`
  - `session.failed`: `{ "error": { "code": "<error_code>", "message": "<string>" }, "usage": { "inputTokens": N, "outputTokens": N } }`
  - `session.terminated` (admin or system termination; the session's external state is `completed` — this webhook type distinguishes operator-initiated completion from agent-initiated): `{ "reason": "<string>", "terminatedBy": "<admin|system>" }`
  - `session.cancelled` (user/runtime cancelled; the session's external state is `cancelled`): `{ "reason": "<string>" }`
  - `session.expired` (maxSessionAge or maxIdleTimeSeconds): `{ "expiryReason": "max_session_age|max_idle_time" }`
  - `session.awaiting_action`: `{ "actionRequired": "<string>", "resumeUrl": "<string>" }`
  - `delegation.completed`: `{ "childSessionId": "<id>", "status": "completed|failed|cancelled|expired", "usage": { "inputTokens": N, "outputTokens": N } }`

  **Event types:** `session.completed`, `session.failed`, `session.terminated`, `session.cancelled`, `session.expired`, `session.awaiting_action` (fired when a session enters `awaiting_client_action` state, enabling CI systems to react without polling), `delegation.completed` (for child task completion notifications).

  **Retry behavior:** Failed deliveries (non-2xx response or timeout) are retried with exponential backoff: 10 s, 30 s, 60 s, 300 s, 900 s (5 attempts total). After exhaustion, the event is marked as undelivered and queryable via `GET /v1/sessions/{id}/webhook-events`.

  **Idempotency:** Each event has a unique `idempotency_key`. Receivers should deduplicate by this key.

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
      "temperature":      { "type": "number", "minimum": 0, "maximum": 1, "description": "Sampling temperature" },
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

  **`openai-agents` runtime:**

  ```json
  {
    "type": "object",
    "properties": {
      "model":            { "type": "string", "description": "OpenAI model ID" },
      "temperature":      { "type": "number", "minimum": 0, "maximum": 2 },
      "parallelToolCalls":{ "type": "boolean", "default": true },
      "responseFormat":   { "type": "string", "enum": ["text", "json_object", "json_schema"], "default": "text" }
    },
    "additionalProperties": false
  }
  ```

  Custom runtimes declare their schema in the `runtimeOptionsSchema` field of the `RuntimeDefinition` ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)). Derived runtimes ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime), Derived Runtime) inherit the base runtime's schema; they MAY narrow it by registering a stricter schema but MAY NOT declare properties that the base schema forbids. **Validation:** at derived-runtime registration the gateway computes `derived.properties.keys() − base.properties.keys()`; if the difference is non-empty the registration is rejected with `INVALID_DERIVED_RUNTIME: runtimeOptionsSchema declares forbidden property '<name>'` for each offending property name. Constraints on existing properties (e.g. tightened `minimum`/`maximum`, added `enum`, changed `default`) are permitted.

---

### 14.1 WorkspacePlan Schema Versioning

**`schemaVersion` field.** The `workspacePlan` object carries a `schemaVersion` integer field (shown as `"schemaVersion": 1` in the example above). This field identifies the schema revision used when the plan was written and governs forward-compatibility obligations for all consumers of persisted workspace plans.

**Producer obligation.** The gateway MUST set `schemaVersion` to the highest version required by the fields it writes. When a new `schemaVersion` introduces new source types, new setup-command fields, or changes field semantics, producers MUST set `schemaVersion` to that version so consumers can detect the presence of fields they may not understand.

**Consumer obligations by consumer type.** `WorkspacePlan` is persisted in Postgres as part of the session record and retained for the session's lifetime. Two consumer categories apply:

- **Gateway reconciliation (live consumer):** The gateway reads back the stored `WorkspacePlan` when replaying workspace setup for resumed or retried sessions. If the gateway encounters a `schemaVersion` higher than it understands (e.g., a plan written by a newer gateway version is read by an older one during a rollback), it MUST NOT proceed with workspace materialization — instead it MUST reject the operation with error code `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (HTTP 422), including `details.knownVersion` and `details.encounteredVersion`. Silently processing a plan at a higher schema version could materialize an incorrect workspace.

- **Audit / analytics consumers (durable consumers):** Audit pipelines, compliance exporters, and analytics queries that read session records containing embedded `WorkspacePlan` objects are **durable consumers** and MUST apply the forward-read rule from [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7: process all fields they understand and preserve unknown fields verbatim. They MUST NOT reject session records solely because the embedded `WorkspacePlan.schemaVersion` is unrecognized.

**Migration window SLA.** When a new `WorkspacePlan` `schemaVersion` is introduced, all gateway replicas (which are live consumers) MUST be upgraded to understand the new version within the platform's standard rolling-upgrade window (typically within one deployment cycle, ≤ 24 hours). Durable consumers MUST be upgraded within **90 days** per the general schema migration SLA in [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7.

**Backwards compatibility guarantee.** New `schemaVersion` values MUST NOT remove or rename existing `sources` types or `setupCommands` fields. Adding new optional fields or new `type` values to `sources` is a non-breaking addition (existing consumers ignore unknown `type` values in `sources` entries per the open-string extensibility contract below). Changing the semantics of an existing field or making a previously optional field required is a breaking change requiring a `schemaVersion` bump and a corresponding gateway upgrade gate.

**Unknown `source.type` handling.** The `type` field on each `sources` entry is an open string (not a closed enum). A consumer that encounters an unknown `source.type` MUST skip that source entry and emit a `workspace_plan_unknown_source_type` warning (fields: `schemaVersion`, `unknownType`) rather than rejecting the entire plan. This allows new source types to be added in minor releases without breaking existing readers.

**Path collision rule (within `sources` array).** Sources in the `sources` array are applied in declaration order; if two or more entries resolve to the same workspace path, the **later entry wins** (last-writer-wins). This is intentional and consistent with the cross-tier materialization order described in [Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime) (base defaults → derived defaults → client uploads → file exports from parent delegation), where each tier overwrites any same-path file from the preceding tier. Clients that want strict conflict detection MUST ensure their `sources` entries target non-overlapping paths. A future `schemaVersion` may introduce a per-plan `onConflict` field to override this default; until then, last-writer-wins is the only supported mode. The gateway emits a `workspace_plan_path_collision` warning event (fields: `path`, `winningSourceIndex`, `losingSourceIndex`) whenever a path collision is detected during materialization, so operators and clients can audit and correct unintended overwrites.

