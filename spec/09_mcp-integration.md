## 9. MCP Integration

### 9.1 Where MCP Is Used

| Boundary                         | Protocol                                            | Why                                                                                                                     |
| -------------------------------- | --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Client ↔ Gateway                 | MCP (Streamable HTTP) via `ExternalAdapterRegistry` | Tasks, elicitation, auth discovery, tool surface. Also OpenAI Completions, Open Responses, and other external adapters. |
| Adapter ↔ Runtime (intra-pod)    | MCP (local Unix socket servers)                     | Platform tools, per-connector tool servers. See [Section 4.7](04_system-components.md#47-runtime-adapter).                                                            |
| Parent pod ↔ child (via gateway) | MCP (virtual interface)                             | Delegation, tasks, elicitation forwarding                                                                               |
| Gateway ↔ external MCP tools     | MCP                                                 | Tool invocation, OAuth flows                                                                                            |
| Gateway ↔ `type:mcp` runtimes    | MCP (dedicated endpoints at `/mcp/runtimes/{name}`) | Direct MCP server access. Implicit session for audit/billing.                                                           |
| Gateway ↔ pod runtime control    | Custom gRPC/HTTP+mTLS                               | Lifecycle, uploads, checkpoints, lease extension — not MCP-like                                                         |

#### Platform MCP Server Tools

The platform MCP server (available to `type: agent` runtimes via the adapter manifest) exposes:

| Tool                        | Purpose                                                       | See     |
| --------------------------- | ------------------------------------------------------------- | ------- |
| `lenny/delegate_task`       | Spawn a child session (target is opaque)                      | [§8.2](08_recursive-delegation.md#82-delegation-mechanism)    |
| `lenny/await_children`      | Wait for children (streaming, unblocks on `input_required`)   | [§8.5](08_recursive-delegation.md#85-delegation-tools)    |
| `lenny/cancel_child`        | Cancel a child and its descendants                            |         |
| `lenny/discover_agents`     | List available delegation targets (policy-scoped)             | [§8.5](08_recursive-delegation.md#85-delegation-tools)    |
| `lenny/output`              | Emit output parts to the parent/client                        | [§15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol) |
| `lenny/request_elicitation` | Request human input via the elicitation chain                 | [§9.2](#92-elicitation-chain)    |
| `lenny/memory_write`        | Write to the memory store (see [Section 9.4](#94-memory-store))                   | [§9.4](#94-memory-store)    |
| `lenny/memory_query`        | Query the memory store                                        | [§9.4](#94-memory-store)    |
| `lenny/request_input`       | Block until answer arrives (replaces stdout `input_required`) | [§7.2](07_session-lifecycle.md#72-interactive-session-model)    |
| `lenny/send_message`        | Send a message to any task by taskId                          | [§7.2](07_session-lifecycle.md#72-interactive-session-model)    |
| `lenny/get_task_tree`       | Return task hierarchy with states                             | [§8.9](08_recursive-delegation.md#89-task-tree)    |
| `lenny/set_tracing_context` | Propagate the OTel tracing context into the current operation for parent-linked spans across delegation and tool calls | [§16](16_observability.md#16-observability)    |

#### Runtime Discovery

Every external interface exposes runtime discovery via `HandleDiscovery`. All results are identity-filtered and policy-scoped. Not-found and not-authorized produce identical responses — no enumeration. Every discovery response includes an `adapterCapabilities` block (see [Section 15](15_external-api-surface.md)) reflecting the capabilities of the adapter serving the request — consumers must inspect `adapterCapabilities.supportsElicitation` before initiating elicitation-dependent workflows.

- **MCP:** `list_runtimes` tool — response includes a top-level `adapterCapabilities` object
- **REST:** `GET /v1/runtimes` with full `agentInterface`, `mcpEndpoint`, and `adapterCapabilities` fields
- **OpenAI Completions:** `GET /v1/models`
- **Open Responses:** `GET /v1/models`

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

| Field              | Description                                                             |
| ------------------ | ----------------------------------------------------------------------- |
| `origin_pod`       | Which pod initiated the elicitation                                     |
| `delegation_depth` | How deep in the task tree                                               |
| `origin_runtime`   | Runtime type of the originating pod                                     |
| `purpose`          | Stated purpose (e.g., "oauth_login", "user_confirmation")               |
| `connector_id`     | Registered connector ID (for OAuth flows)                               |
| `expected_domain`  | Expected OAuth endpoint domain (for URL-mode elicitations)              |
| `initiator_type`   | `connector` (gateway-registered connector) or `agent` (agent-initiated) |

Client UIs **must** display provenance prominently so users can distinguish platform OAuth flows from agent-initiated prompts.

**URL-mode elicitation security controls:**

1. **Agent-initiated URL-mode blocked by default.** URL-mode elicitations (those containing a URL for the user to visit, e.g., OAuth flows) can only be initiated by gateway-registered connectors. Agent binaries cannot emit URL-mode elicitations unless explicitly allowlisted per-pool. This prevents a compromised agent from phishing users via crafted URLs. The per-pool allowlist for agent-initiated URL-mode is a structured object with a required non-empty `domainAllowlist` array: `{"urlModeElicitation": {"enabled": true, "domainAllowlist": ["accounts.example.com"]}}`. Pool registration that sets `urlModeElicitation.enabled: true` with an empty or absent `domainAllowlist` is rejected with `400 URL_MODE_ELICITATION_DOMAIN_REQUIRED`. The gateway validates each emitted URL against this list and drops the elicitation with `DOMAIN_NOT_ALLOWLISTED` if the URL's effective host does not match any entry (exact match or `*.suffix` wildcard).
2. **URL domain validation is a hard enforcement boundary.** The gateway rejects any URL-mode elicitation whose URL domain does not match the registered connector's `expected_domain`. This is not a metadata annotation — the elicitation is dropped and an error is returned to the originator. Wildcards and subdomain matching follow the connector's registered domain policy.
3. **Initiator type in provenance.** The provenance metadata includes an `initiator_type` field: `connector` (gateway-initiated via a registered connector) or `agent` (agent-initiated, only if allowlisted). Client UIs should render these with distinct trust indicators — connector-initiated elicitations carry higher trust than agent-initiated ones.

**Depth-based restrictions:** Deployers can configure per-pool or global rules limiting which elicitation types are allowed at each delegation depth (e.g., children below depth 2 cannot trigger OAuth flows).

**Deep elicitation suppression:** At delegation depth >= 3, agent-initiated elicitations are **auto-suppressed by default** unless the elicitation type appears in the pool's allow list. Suppressed elicitations return a `SUPPRESSED` response to the originating pod, which should handle it equivalently to "user declined." Deployers configure this via `elicitationDepthPolicy` per pool:

- `allow_all` — no suppression at any depth
- `suppress_at_depth: N` — suppress agent-initiated elicitations at depth N+
- `block_all` — no elicitations from delegated sessions

OAuth flows initiated by gateway-registered connectors are exempt from suppression at any depth, provided the connector is authorized by the session's effective `DelegationPolicy` (these are gateway-initiated, not agent-initiated).

**Elicitation Timeout Semantics:**

1. **Timer pause:** When a session is waiting for an elicitation response, the session's `maxIdleTime` timer is paused. The session is in a "waiting_for_human" state, not idle.
2. **Elicitation timeout:** A separate `maxElicitationWait` timeout (default: 600s, configurable per pool) limits how long a session waits for a human response. If exceeded, the elicitation is dismissed and the pod receives a timeout error that the agent can handle.
3. **Per-hop forwarding timeout:** Each hop in the elicitation chain has a forwarding timeout (30s). If a hop doesn't forward the elicitation within 30s, the gateway treats it as a failure and returns a timeout to the originating pod.
4. **Dismiss elicitation:** Clients can explicitly dismiss a pending elicitation via a `dismiss_elicitation` action (sends a cancellation response down the chain).
5. **Elicitation budget:** Deployers can configure `maxElicitationsPerSession` (default: 50) to prevent agents from spamming the user with elicitation requests.

**`respond_to_elicitation` authorization:** When a client calls `respond_to_elicitation(elicitation_id, response)`, the gateway validates the `(session_id, user_id, elicitation_id)` triple before routing the response down the chain. The `elicitation_id` must have been issued to the exact session making the call and must belong to the authenticated user. If the triple does not match — because the ID is unknown, belongs to a different session, or belongs to a different user — the gateway returns a `404 ELICITATION_NOT_FOUND` error. This applies uniformly to dismiss-elicitation actions as well. Returning 404 rather than 403 avoids leaking the existence of elicitations belonging to other sessions.

**Interaction with `input_required` deadlock detection:** Elicitation chains and `input_required` chains are independent blocking mechanisms, but both participate in the gateway's subtree deadlock detection ([Section 8.9](08_recursive-delegation.md#89-task-tree)). A task blocked on an elicitation waiting for a human response is **not** considered deadlocked — it is waiting on an external actor. However, a task blocked on `lenny/request_input` is waiting on its parent, which is an internal actor. If the parent is itself blocked (on its own `request_input` or on `await_children` with all children in `input_required`), the gateway's deadlock detector treats this as a circular wait.

### 9.3 Connector Definition and OAuth/OIDC

#### `ConnectorDefinition` as First-Class Resource

Connector configuration is a first-class admin API resource, supporting both tool connectors and external agents:

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
    visibility: tenant
    labels:
      team: platform
```

Includes `labels` map for environment selector matching. Tool capability metadata derived from MCP `ToolAnnotations` at registration time (see [Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime) — Capability Inference).

> **Transport extensibility.** V1 connectors use MCP (Streamable HTTP) as the transport protocol (`mcpServerUrl` + `transport: streamable_http`). Post-v1, the `ConnectorDefinition` schema will add support for A2A (`transport: a2a`, `a2aAgentUrl`) and Agent Protocol (`transport: agent_protocol`, `agentProtocolUrl`) transports, enabling delegation to external agents over their native protocols. The `allowedExternalEndpoints` field on delegation leases ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) is reserved for this purpose.

All connectors must be registered before they can be used — unregistered external endpoints cannot be called from inside a pod (security: gateway must know about every external endpoint for OAuth flow, audit logging, and protocol mediation).

Each connector in a session's effective delegation policy gets its own independent MCP server in the adapter manifest (see [Section 4.7](04_system-components.md#47-runtime-adapter)). No aggregated connector proxy.

#### OAuth/OIDC Flow

When a nested agent calls an external MCP tool requiring user auth:

1. Pod calls the connector's local MCP server in the adapter manifest
2. Gateway (acting as MCP client to external tool) receives auth challenge
3. Gateway emits URL-mode elicitation through the chain (hop by hop up to client)
4. User completes OAuth flow
5. Gateway connector receives and stores resulting tokens (encrypted, never in pods)
6. Future calls from pods **authorized for that connector** use gateway-held connector state

**OAuth security requirements:**

- **`state` parameter (anti-CSRF).** For every authorization request the gateway generates a cryptographically random `state` value (≥128 bits, base64url-encoded), stores it bound to the initiating session ID and connector ID (Redis, TTL = 10 min), and validates the returned `state` exactly on the redirect callback before exchanging the code for tokens. Mismatched or missing `state` values cause the flow to be aborted and the pending record deleted.
- **PKCE (S256) for public clients.** When the connector's `auth` block omits `clientSecretRef` (i.e. a public client), the gateway MUST generate a `code_verifier` (≥43 random characters, unreserved alphabet) and send the corresponding `code_challenge` (SHA-256 hash, base64url-encoded, `code_challenge_method=S256`) in the authorization request. The `code_verifier` is stored alongside the `state` entry and submitted at token exchange.
- **Confidential clients.** Connectors with a `clientSecretRef` are treated as confidential clients. PKCE is still recommended and SHOULD be used when the authorization server supports it — include `code_challenge` / `code_verifier` unless the server is known to reject it.

**Key invariants:**

- Tokens never transit through pods. The gateway owns all downstream credential state.
- **Connector access is scoped per delegation level.** The `DelegationPolicy` (see [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) controls which connectors each session is authorized to use. The gateway validates the `connector_id` in every external tool call against the calling pod's effective delegation policy before proxying. A child cannot use connectors not permitted by its policy, even if tokens exist for them at the root level.

### 9.4 Memory Store

`MemoryStore` is a role-based storage interface alongside `SessionStore`, `ArtifactStore`, etc.

```go
type MemoryStore interface {
    Write(ctx, scope MemoryScope, memories []Memory) error
    Query(ctx, scope MemoryScope, query string, limit int) ([]Memory, error)
    Delete(ctx, scope MemoryScope, ids []string) error
    List(ctx, scope MemoryScope, filter MemoryFilter) ([]Memory, error)
}

type MemoryScope struct {
    TenantID  string
    UserID    string
    AgentType string   // optional: scope to a runtime type
    SessionID string   // optional: scope to a specific session
}
```

**Tenant isolation contract.** All `MemoryStore` implementations MUST guarantee that `Write`, `Query`, `Delete`, and `List` operations are strictly scoped to the `TenantID` in the supplied `MemoryScope`. Cross-tenant reads and writes MUST be impossible regardless of application-layer correctness. The interface boundary MUST validate that `MemoryScope.TenantID` is non-empty before dispatching to the underlying implementation — calls with an empty `TenantID` are rejected with an error, never silently defaulted.

**Default implementation:** Postgres + pgvector. The `memories` table carries a `tenant_id` column with the same RLS policy as all other tenant-scoped tables (see [Section 4.2](04_system-components.md#42-session-manager)): rows are filtered by `current_setting('app.current_tenant', false)`, and every query runs inside a transaction preceded by `SET LOCAL app.current_tenant`. The `connect_query` sentinel and cloud-managed pooler trigger ([Section 12.3](12_storage-architecture.md#123-postgres-ha-requirements)) cover the `memories` table identically to other tenant-scoped tables.

**Custom implementations:** The store is fully replaceable. Deployers who want Mem0, Zep, or another vector database implement the interface backed by their choice. Custom implementations MUST enforce tenant isolation equivalent to the default — a `ValidateMemoryStoreIsolation(t *testing.T, store MemoryStore)` contract validation helper is provided so deployers can verify their implementation. Technology choice explicitly deferred — the memory layer market is not settled as of Q1 2026. **Instrumentation contract:** All `MemoryStore` implementations (default and custom) MUST emit `lenny_memory_store_operation_duration_seconds` (histogram), `lenny_memory_store_errors_total` (counter), and `lenny_memory_store_record_count` (gauge) as defined in [Section 16.1](16_observability.md#161-metrics). The contract validation helper (`ValidateMemoryStoreIsolation`) also verifies that the implementation registers these metrics — a custom backend that omits instrumentation will fail the contract test.

**Retention and capacity limits.** Memories are user-scoped and persist across sessions by design — there is no automatic TTL-based expiry. Retention is unbounded unless the deployer configures limits. To prevent unbounded accumulation, the `MemoryStore` enforces a configurable per-user capacity limit: `memory.maxMemoriesPerUser` (default: 10,000). When a `Write` would push a user's memory count above this limit, the oldest memories (by `created_at`) are evicted to make room. Deployers may also configure `memory.retentionDays` (default: unset / no TTL) to apply time-based expiry; when set, the GC sweep ([Section 12.5](12_storage-architecture.md#125-artifact-store)) deletes memory rows whose `created_at` exceeds the configured TTL. The `lenny_memory_store_record_count` gauge (per tenant, per user) enables operators to monitor growth; alert `MemoryStoreGrowthHigh` ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)) fires when any user's memory count exceeds 80% of `memory.maxMemoriesPerUser`.

**Integration test:** `TestMemoryStoreTenantIsolation` verifies that a `Write` with `TenantID=A` followed by a `Query` with `TenantID=B` returns zero results, and that calls with an empty `TenantID` are rejected. This test runs against the default Postgres implementation at startup and is available for custom implementations.

Accessed via `lenny/memory_write` and `lenny/memory_query` tools on platform MCP server. Runtimes that don't need memory ignore these tools entirely.

