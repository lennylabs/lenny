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

**`sources[]` catalogue.** Each entry in `workspacePlan.sources[]` has a `type` discriminator. V1 ships the following built-in types; the `type` field is an open string (see "Unknown `source.type` handling" in [§14.1](#141-workspaceplan-schema-versioning)) so new types can be added in minor releases.

| `type`          | Required fields                                                  | Optional fields                                                                                                                                                                                                                                                                                                                                                                  | Purpose                                                                                                                                                                                                                                                      |
|-----------------|------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `inlineFile`    | `path`, `content`                                                | `mode` (octal string, default `0644`; see `mode` field notes below for the `^0[0-7]{3,4}$` pattern and bit-mask constraints)                                                                                                                                                                                                                                                    | Write a small file whose contents are embedded in the request. Content is UTF-8 text; binary inline files should use `uploadFile`.                                                                                                                           |
| `uploadFile`    | `path`, `uploadRef`                                              | `mode` (octal string, default `0644`; see `mode` field notes below for the `^0[0-7]{3,4}$` pattern and bit-mask constraints)                                                                                                                                                                                                                                                    | Place a previously uploaded single file (via the upload API) at `path` under `/workspace/current/`.                                                                                                                                                          |
| `uploadArchive` | `pathPrefix`, `uploadRef`, `format` (`tar`, `tar.gz`, or `zip`)  | `stripComponents` (integer, default `0`)                                                                                                                                                                                                                                                                                                                                         | Extract a previously uploaded archive under `pathPrefix` relative to `/workspace/current/`.                                                                                                                                                                  |
| `mkdir`         | `path`                                                           | `mode` (octal string, default `0755`; see `mode` field notes below for the `^0[0-7]{3,4}$` pattern and bit-mask constraints)                                                                                                                                                                                                                                                    | Create an empty directory (and any missing parents).                                                                                                                                                                                                         |
| `gitClone`      | `url` (HTTPS Git URL — scheme MUST be `https`; see §14 `gitClone.url` notes), `ref` (branch, tag, or commit SHA) | `path` (workspace-relative clone destination, default `.` — the repo root), `depth` (positive integer for shallow clones, default full clone), `submodules` (boolean, default `false` — when `true`, initialize and update submodules recursively), `auth` (object; see below — omit for public repos)                                                                           | Clone a Git repository into the workspace at materialization time. The gateway performs the clone using its network path, not the pod's, so the runtime never sees raw credentials.                                                                         |

**`gitClone.url` restrictions.** V1 accepts **HTTPS Git URLs only** — the URL must parse as a valid RFC 3986 URI with scheme `https` (case-insensitive at parse, normalized to lowercase). SSH URL forms (both explicit `ssh://git@host/owner/repo.git` and SCP-style `git@host:owner/repo.git`) and the `git://` anonymous protocol are **not supported in v1** and are rejected at session creation with `400 WORKSPACE_PLAN_INVALID` by the published JSON Schema (the `pattern` constraint on `gitClone.url` is `^https://`). The gateway extracts the host from the parsed URL's authority component (`u.Host` in Go's `net/url`) for credential-pool matching per the `gitClone.auth` paragraph below. SSH URL support — including SSH-key Secret shapes, `known_hosts` provisioning, and ssh-agent models — is deferred post-V1 ([§21.9](21_planned-post-v1.md#21-planned--post-v1)); deployers who need SSH clone today should mirror the repository to an HTTPS endpoint reachable by the gateway, or use an HTTPS Git host with a credential-lease provider.

**`gitClone.auth` object.** When set, `auth.mode` must be `"credential-lease"` and `auth.leaseScope` must follow the host-agnostic pattern `vcs.<provider>.read` (read-only clones — the common case) or `vcs.<provider>.write` (when the session will push back to the remote). `<provider>` identifies the VCS credential provider registered in [§4.9](04_system-components.md#49-credential-leasing-service); v1 ships `github` as the only built-in provider (scopes `vcs.github.read` / `vcs.github.write`), and deployers may register additional providers (e.g., GitLab, Bitbucket, Gitea, self-hosted) via the custom `CredentialProvider` interface in [§4.9](04_system-components.md#49-credential-leasing-service). The gateway resolves `<provider>` by matching the `url` host (extracted from the parsed HTTPS URL's authority — see `gitClone.url` restrictions above) against the tenant's configured VCS credential pools — each VCS pool declares one or more `hostPatterns` (exact host or `*.suffix` wildcard), and the URL's host must match exactly one pool whose provider equals `<provider>` in the requested scope. URLs whose host matches no registered VCS pool are rejected at session creation with `422 GIT_CLONE_AUTH_UNSUPPORTED_HOST`; URLs whose host resolves to multiple pools are rejected with `422 GIT_CLONE_AUTH_HOST_AMBIGUOUS`. The gateway then performs the clone with a short-lived HTTPS bearer/basic token scoped to the repository, and the `git` client inside the pod uses an in-pod HTTPS credential helper that calls the gateway's token endpoint for any subsequent operations (pull, push) — this flow is HTTPS-specific by design; `git` over SSH does not consult credential helpers, which is one reason SSH is deferred post-V1. Omit `auth` for public repositories — no URL-to-pool binding is required when `auth` is absent.

**Field notes:**

- `workspacePlan.setupCommands[].timeoutSeconds`: Optional per-command timeout. When omitted, the command has no independent time limit and runs until the runtime's aggregate `setupPolicy.timeoutSeconds` cap ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)) terminates the entire setup phase. If `setupPolicy.timeoutSeconds` is also absent the command runs until the pod is killed by an external deadline. Clients that need per-command bounds SHOULD set this field explicitly.
- `uploadArchive.stripComponents`: Non-negative integer (default `0`) specifying how many leading path segments to drop from each archive entry before extraction. Semantics are format-independent and defined in terms of the entry's path string: the gateway splits the entry path on `/` into segments, discards the first `N = stripComponents` segments, and re-joins the remainder (joined with `/`) as the write-relative path under `pathPrefix`. For `tar` and `tar.gz` this matches GNU tar's `--strip-components=N`; for `zip`, the same `/`-split-and-drop algorithm is applied to the flat path string stored in each entry's filename field (zip paths are already `/`-separated per APPNOTE.TXT §4.4.17). Entries whose segment count (after stripping any leading/trailing empty segments) is less than `N` are **skipped** (not written, not treated as a fatal error) and the gateway emits a `workspace_plan_strip_components_skip` warning event per skipped entry (fields: `sourceIndex`, `entryPath`, `segmentCount`, `stripComponents`). Entries whose remaining-segment path is empty after stripping (i.e., exactly `N` segments and no residual filename) are also skipped with the same warning. Directory entries are subject to the same rule; the implicit parent directories for written files are created from the post-strip segments. This behavior is identical for all three values of `format` so that a given plan behaves the same regardless of archive packaging.
- `mode` (all variants — `inlineFile`, `uploadFile`, `mkdir`): Octal string representing Unix file permissions. The published JSON Schema constrains `mode` with `{"type": "string", "pattern": "^0[0-7]{3,4}$"}` — three or four octal digits with a required leading zero. The four-digit form encodes setuid (`04xxx`), setgid (`02xxx`), and sticky (`01xxx`) bits; the three-digit form is equivalent to a leading `0` on the high bits (no special bits set). Non-matching strings (`"644"` without leading zero, `"rw-r--r--"` symbolic form, `"0o644"` Go/Python prefix, `"0x1A4"` hex, or arbitrary non-numeric values) are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`, `details.field = "sources[<n>].mode"`, `details.reason = "invalid_mode_format"`. V1 additionally rejects `mode` values whose leading digit (in the four-digit form) has the setuid (`04xxx`) or setgid (`02xxx`) bit set on any variant, with `details.reason = "setuid_setgid_prohibited"`; the sticky bit (`01xxx`) is permitted on `mkdir` but rejected on `inlineFile` / `uploadFile` (sticky on a regular file has no defined semantics on modern Linux kernels) with `details.reason = "sticky_on_file_prohibited"`. After successful validation the gateway parses the string with `strconv.ParseUint(mode, 8, 32)` and applies the result via `os.Chmod` after the file or directory is written. Defaults when `mode` is omitted: `0644` for `inlineFile` and `uploadFile`, `0755` for `mkdir`.
- **`gitClone.ref` resolution (per-session immutability).** At session creation the gateway resolves each `gitClone.ref` to an immutable commit SHA by performing a `git ls-remote` against the target repository, using the same credential-lease as the clone itself (or unauthenticated for public repos per the `gitClone.auth` paragraph above). The resolved SHA is persisted alongside the stored `WorkspacePlan` as `sources[<n>].resolvedCommitSha` and is a read-only, gateway-written field — clients MUST NOT set it in the `CreateSessionRequest` (any client-supplied value is rejected at session creation with `400 WORKSPACE_PLAN_INVALID`, `details.field = "sources[<n>].resolvedCommitSha"`, `details.reason = "gateway_written_field"`).

    **Schema encoding of the request/response asymmetry.** `resolvedCommitSha` is declared on the `gitClone` variant in the published JSON Schema with `"readOnly": true` so response-side consumers (`GET /v1/sessions/{id}`) can validate its presence. However, `readOnly: true` is informational in JSON Schema 2020-12 — it does not by itself cause a validator to reject a value on the request path. Because the per-variant object schema sets `additionalProperties: false` (see "Per-variant field strictness" in [§14.1](#141-workspaceplan-schema-versioning)), declaring `resolvedCommitSha` inside that object means a strict validator would accept a client-supplied value. The gateway therefore performs a second request-time check that rejects `resolvedCommitSha` when present on any `sources[<n>]` entry in `CreateSessionRequest` with the field-specific error above. This dual-schema pattern (schema declares the field with `readOnly: true` for response validation; request-time check enforces non-set) is the canonical encoding for gateway-written fields in Lenny and is identical to the encoding used for `last_used_at` on `GET /v1/credentials`. Clients SHOULD omit `resolvedCommitSha` from request bodies; tooling that round-trips the response into a new request MUST strip it first. All subsequent materializations for the same session (retries within `retryPolicy.maxResumeWindowSeconds`, resumes after pod eviction, checkpoint restores) clone `resolvedCommitSha` rather than re-resolving `ref`, so a moving branch or floating tag does not change the workspace contents across the session's lifetime. When `ref` is already a 40-character lowercase hexadecimal string matching `^[0-9a-f]{40}$`, the gateway treats it as a commit SHA and skips the `ls-remote` step (`resolvedCommitSha` equals `ref`). `resolvedCommitSha` MAY be surfaced to clients in `GET /v1/sessions/{id}` for audit purposes (see [§15.1](15_external-api-surface.md#151-rest-api)). Sessions that reference the same `WorkspacePlan` template but are independent sessions (recursive delegation children, session-from-session retries initiated after the original session's `maxResumeWindowSeconds` has elapsed, or any `POST /v1/sessions` that reuses the plan body) re-resolve `ref` and MAY see a different `resolvedCommitSha` than the parent — the immutability guarantee is **per-session**, not **per-plan**. If `ls-remote` fails at session creation, the gateway rejects the request with one of two error codes depending on the failure mode so clients can distinguish retryable from non-retryable failures: transient network-level failures (DNS, connection reset, TLS handshake failure, remote-host timeout) return `503 GIT_CLONE_REF_RESOLVE_TRANSIENT` with `details.reason = network_error` (retryable); authentication failures and unknown refs return `422 GIT_CLONE_REF_UNRESOLVABLE` with `details.reason` one of `auth_failed` or `ref_not_found` (not retryable without source-definition changes). Both responses carry `details.url`, `details.ref`, and `details.sourceIndex`. See [§15.1](15_external-api-surface.md#151-rest-api).
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

**Envelope terminology.** The `POST /v1/sessions` (and `POST /v1/sessions/start`) request body is the **`CreateSessionRequest`** envelope. It embeds a nested **`WorkspacePlan`** sub-object under the `workspacePlan` field. The two are distinct:

- **`WorkspacePlan` (inner, `workspacePlan.*`).** The declarative specification of how the workspace is materialized. Fields: `$schema` (optional, for client-side validators), `schemaVersion`, `sources[]`, `setupCommands[]`. This is the object published as a JSON Schema.
- **`CreateSessionRequest` (outer, sibling fields).** The session-creation envelope that embeds the `WorkspacePlan` and also carries session-scope fields: `pool`, `isolationProfile`, `env`, `labels`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `runtimeOptions`, `delegationLease`. These fields are **not** part of `WorkspacePlan` — they configure the session that owns the plan, not the plan itself. See the canonical example at the top of this section for the exact placement of each field.

**Published JSON Schema.** The inner `WorkspacePlan` sub-object is published as a canonical JSON Schema (Draft 2020-12) at `schemas/workspaceplan-v1.json` in the repository, served at the stable URL `https://schemas.lenny.dev/workspaceplan/v1.json`. The schema covers **only** the inner plan fields: `$schema`, `schemaVersion`, `sources[]`, and `setupCommands[]`. It does **not** cover the `CreateSessionRequest` outer fields (`env`, `labels`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `runtimeOptions`, `delegationLease`) — those belong to a separate request-body contract documented in [Section 15.1](15_external-api-surface.md#151-rest-api). Clients MAY reference the inner schema via the optional `$schema` keyword **on their `workspacePlan` object** (matching the canonical example above) for local validation of the plan sub-object.

**Gateway validation at `POST /v1/sessions`.** The gateway performs validation in two layers:

1. **Inner-plan validation against the published `WorkspacePlan` JSON Schema.** Failures are reported as `400 WORKSPACE_PLAN_INVALID` with a JSON Schema validation report in `details`. The identical validation is also performed at `POST /v1/sessions/start`.
2. **Outer-envelope validation of `CreateSessionRequest` fields.** Each outer field is validated individually against its own contract and mapped to a field-specific error code — e.g. `ENV_VAR_BLOCKLISTED` for blocked entries in `env`, `RUNTIME_OPTIONS_INVALID` for a failed `runtimeOptions` check (validated against the target Runtime's `runtimeOptionsSchema`; see the `runtimeOptions` field notes above), and the callback/domain/isolation/credential errors documented in [Section 15.1](15_external-api-surface.md#151-rest-api) for the remaining fields. `WORKSPACE_PLAN_INVALID` is **reserved for inner-plan schema failures only** and is not emitted for outer-envelope violations.

**`schemaVersion` field.** The `workspacePlan` object carries a `schemaVersion` integer field (shown as `"schemaVersion": 1` in the example above). This field identifies the schema revision used when the plan was written and governs forward-compatibility obligations for all consumers of persisted workspace plans. `schemaVersion` is distinct from the JSON Schema `$schema` URL: the former is Lenny's wire-compat identifier; the latter references the publication that defines the wire shape for a given major schema version.

**Producer obligation.** The gateway MUST set `schemaVersion` to the highest version required by the fields it writes. When a new `schemaVersion` introduces new source types, new setup-command fields, or changes field semantics, producers MUST set `schemaVersion` to that version so consumers can detect the presence of fields they may not understand.

**Consumer obligations by consumer type.** `WorkspacePlan` is persisted in Postgres as part of the session record and retained for the session's lifetime. Two consumer categories apply:

- **Gateway reconciliation (live consumer):** The gateway reads back the stored `WorkspacePlan` when replaying workspace setup for resumed or retried sessions. If the gateway encounters a `schemaVersion` higher than it understands (e.g., a plan written by a newer gateway version is read by an older one during a rollback), it MUST NOT proceed with workspace materialization — instead it MUST reject the operation with error code `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (HTTP 422), including `details.knownVersion` and `details.encounteredVersion`. Silently processing a plan at a higher schema version could materialize an incorrect workspace. When replaying a plan that contains `gitClone` entries, the gateway clones `sources[<n>].resolvedCommitSha` (pinned at session creation — see the `gitClone.ref` resolution field note in [§14](#14-workspace-plan-schema)) rather than re-resolving `ref`, so the reconciliation loop produces the same repository contents the first materialization saw.

- **Audit / analytics consumers (durable consumers):** Audit pipelines, compliance exporters, and analytics queries that read session records containing embedded `WorkspacePlan` objects are **durable consumers** and MUST apply the forward-read rule from [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7: process all fields they understand and preserve unknown fields verbatim. They MUST NOT reject session records solely because the embedded `WorkspacePlan.schemaVersion` is unrecognized.

**Migration window SLA.** When a new `WorkspacePlan` `schemaVersion` is introduced, all gateway replicas (which are live consumers) MUST be upgraded to understand the new version within the platform's standard rolling-upgrade window (typically within one deployment cycle, ≤ 24 hours). Durable consumers MUST be upgraded within **90 days** per the general schema migration SLA in [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7.

**Backwards compatibility guarantee.** New `schemaVersion` values MUST NOT remove or rename existing `sources` types or `setupCommands` fields. Adding new optional fields or new `type` values to `sources` is a non-breaking addition (existing consumers ignore unknown `type` values in `sources` entries per the open-string extensibility contract below). Changing the semantics of an existing field or making a previously optional field required is a breaking change requiring a `schemaVersion` bump and a corresponding gateway upgrade gate.

**Unknown `source.type` handling.** The `type` field on each `sources` entry is an open string (not a closed enum). A consumer that encounters an unknown `source.type` MUST skip that source entry and emit a `workspace_plan_unknown_source_type` warning (fields: `schemaVersion`, `unknownType`) rather than rejecting the entire plan. This allows new source types to be added in minor releases without breaking existing readers.

**Per-variant field strictness.** Open-string extensibility applies **only** to the `type` discriminator, not to the per-variant field set. Within a known `source.type` variant (`inlineFile`, `uploadFile`, `uploadArchive`, `mkdir`, `gitClone`), the published JSON Schema sets `additionalProperties: false`; unknown fields on a known type are rejected at session creation with `400 WORKSPACE_PLAN_INVALID` and a JSON Schema validation report identifying the offending field. This aligns `sources[]` strictness with the `runtimeOptions` schemas above (each of which sets `additionalProperties: false`). Mixed shapes that combine one variant's required fields with another variant's fields (e.g., `{"type":"inlineFile","path":"x","content":"y","url":"..."}`) are rejected under the same rule. Clients extending the schema with vendor-specific fields MUST register a new `type` value (exercising the open-string discriminator) rather than attaching extra fields to a built-in type. The `sources[]` item schema encodes this with JSON Schema 2020-12 `allOf` + per-variant `if`/`then` branching on `type.const` (not `oneOf` — `oneOf` would require exactly-one-match, which is incompatible with the intent that an unknown-`type` entry matches no variant branch and is still accepted for the consumer to skip per "Unknown `source.type` handling" above). Each branch has the shape `{"if": {"properties": {"type": {"const": "<variantName>"}}, "required": ["type"]}, "then": {<variant object schema with additionalProperties: false>}}`; a top-level `{"type": "object", "required": ["type"], "properties": {"type": {"type": "string"}}}` ensures every entry has a string `type` without constraining its value. An entry whose `type` matches no known variant passes validation (none of the `if` clauses fire, so no `then` is enforced) and is skipped by the consumer per the open-extensibility rule. An entry whose `type` matches a known variant is strictly validated against that variant's field set — this is the JSON Schema 2020-12 construction that correctly expresses the "known types strict; unknown types pass-through" contract.

**Path collision rule (within `sources` array).** Sources in the `sources` array are applied in declaration order; if two or more entries resolve to the same workspace path, the **later entry wins** (last-writer-wins). This is intentional and consistent with the cross-tier materialization order described in [Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime) (base defaults → derived defaults → client uploads → file exports from parent delegation), where each tier overwrites any same-path file from the preceding tier. Clients that want strict conflict detection MUST ensure their `sources` entries target non-overlapping paths. A future `schemaVersion` may introduce a per-plan `onConflict` field to override this default; until then, last-writer-wins is the only supported mode. The gateway emits a `workspace_plan_path_collision` warning event (fields: `path`, `winningSourceIndex`, `losingSourceIndex`) whenever a path collision is detected during materialization, so operators and clients can audit and correct unintended overwrites.

