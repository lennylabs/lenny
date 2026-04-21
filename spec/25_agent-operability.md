# 25. Agent Operability

Lenny is designed to be natively operable by AI agents. DevOps agents — running in separate orchestration platforms, in external clusters, or on an operator's workstation — can deploy, configure, monitor, troubleshoot, upgrade, and maintain a Lenny installation entirely through APIs, without direct Kubernetes, database, or storage access. All DevOps agents operate from outside the Lenny installation (Section 25.1).

---

## 25.1 Design Philosophy and Agent Model

### Component Model

The operability surface defined in this section is implemented by two components:

- **The gateway** — hosts the in-process operability endpoints (Section 25.3): health, capacity recommendations, version, config, and the event buffer. These all read in-process state that cannot be obtained externally.
- **`lenny-ops`** — hosts the rest of the operability surface (Section 25.4 and the feature sections that follow): event stream, diagnostics, runbooks, platform lifecycle, audit, drift detection, backup/restore, MCP management, bundled alerting rules.

**`lenny-ops` is mandatory in every Lenny installation regardless of tier.** There is no supported topology without it — the features it hosts have no alternative path. Section 25.2 details the split between the two components.

External dependencies and their requirement levels are summarized in Section 25.4 (Dependencies) and Section 25.15 (Failure Mode Analysis):

- **Postgres, Redis, K8s API, gateway admin API** — required for full functionality; `lenny-ops` degrades gracefully when each is transiently unavailable.
- **Prometheus** (or any Prometheus-HTTP-API-compatible time-series backend) — optional at Tier 1 (dev), **required at Tier 2/3** (production). Several `lenny-ops` features are functionally broken without persistent time-series storage; see Section 25.4, Prometheus Requirement.
- **MinIO** (or any S3-compatible object store) — required for backup storage; degraded only for backup verification.

### The Operational Loop

The existing admin API (Section 15.1) provides CRUD operations for all platform resources. CRUD is necessary but not sufficient for autonomous operations. A DevOps agent executing an operational loop requires:

1. **Observe** — structured platform health, not raw metric scraping
2. **Detect** — real-time notification of operational events, not polling
3. **Diagnose** — structured causality chains, not log parsing
4. **Decide** — actionable recommendations, not threshold interpretation
5. **Act** — API-encapsulated remediation, not kubectl/psql/redis-cli
6. **Verify** — confirmation that the action had the intended effect

Each subsection below fills a gap in this loop. Together, they ensure that every step an operator takes in every runbook (Section 17.7) can be performed by an API call, and every signal an operator reads from a dashboard is available as structured data.

### The Bootstrap Problem and External Agent Model

The agent that fixes Lenny cannot run inside Lenny. If the warm pool is exhausted, an agent inside Lenny cannot start a session to fix the warm pool. If Postgres is down, session state is gone — including the DevOps agent's session. If the gateway is crash-looping, no agent inside Lenny can reach the admin API.

This yields a hard constraint: **all DevOps agents must live outside Lenny.** An agent may be its own Deployment in the cluster (but in a separate namespace with independent failure domain), a workload in a separate cluster, a cloud function, or a process on an operator's workstation. What matters is that the agent's lifecycle is independent of Lenny's — it can observe and act on a Lenny installation that is fully or partially down.

Agents connect to `lenny-ops` (Section 25.4), which is a separate Deployment from the gateway and survives gateway failures. `lenny-ops` accepts only external traffic via an Ingress — no internal cluster traffic is permitted (Section 25.4, NetworkPolicy). This is a deliberate security boundary: agent pods inside Lenny run tenant-supplied code, and allowing them to reach the operational control plane would require reasoning about compromised workloads reaching admin APIs. By making `lenny-ops` external-only, the attack surface is reduced to authenticated external callers.

If `lenny-ops` is unreachable, the agent falls back to the gateway's health summary endpoint (`GET /v1/admin/health/summary`) as a heartbeat. If both are unreachable, the agent knows the platform has a total failure.

### Design Constraints

- **No Kubernetes access required.** Every runbook diagnostic and remediation step that currently uses `kubectl`, `psql`, `redis-cli`, or `mc` has an admin API equivalent. Direct cluster access is available for escape-hatch scenarios but is never required for standard operations.
- **External by design.** `lenny-ops` is only reachable from outside the cluster via Ingress. No internal cluster workload — including Lenny's own agent pods — can reach the operational control plane. This eliminates an entire class of lateral-movement attacks.
- **Structured over textual.** All operational responses use typed JSON schemas. Error codes, severity levels, and suggested actions are machine-parseable.
- **Idempotent and safe by default.** Diagnostic endpoints are read-only. Remediation endpoints are idempotent and accept an optional `Idempotency-Key` header (Section 25.4). Destructive actions require explicit `"confirm": true` in the body — without it, the endpoint returns a dry-run preview of what would happen.
- **Coordinated.** Remediation actions acquire remediation locks (Section 25.4) to coordinate exclusive access and prevent conflicting concurrent operations from multiple agents. Locks are enforced at the durable tiers (Postgres, Redis); agents that need to override an existing lock do so explicitly via the `Steal` endpoint, which is audited.
- **Audited.** All agent-initiated operations produce the same audit trail as human-initiated operations (Section 11.7). The audit event includes the caller's identity, which distinguishes agent service accounts from human operators. Agents may pass an `X-Lenny-Operation-ID` header to correlate multi-step remediations in the audit trail.

### Authentication for Agent Callers

Agent callers authenticate using the same OIDC-based mechanism as human operators and `lenny-ctl` (Section 15.1). Deployers create dedicated service accounts with the `platform-admin` or `tenant-admin` role. A `caller_type: "agent"` claim in the JWT token identifies agent callers in audit events and metrics. There is no separate authentication mechanism for agents — they are first-class API consumers alongside humans.

#### Scoped Tokens

The `platform-admin` role confers broad authority; granting it in full to every automation agent is often too much. Tokens therefore support the standard OAuth 2.0 **`scope`** JWT claim (RFC 9068, "JWT Profile for OAuth 2.0 Access Tokens") that narrows the caller's effective tool surface below the role ceiling. The role sets the maximum; scopes restrict further.

**Issuing a scoped token.** Operators issue narrowed tokens by calling the canonical [RFC 8693](https://www.rfc-editor.org/rfc/rfc8693) token-exchange endpoint `POST /v1/oauth/token` ([§15.1](15_external-api-surface.md#151-rest-api)) with the agent's existing token as `subject_token` and the narrower space-separated `scope` as the exchange parameter. The Token Service enforces that every value in the requested `scope` is present in `subject_token.scope` (scope narrowing is monotonic; broadening is rejected with `invalid_scope` per [§13](13_security-model.md#133-credential-flow)). This replaces any earlier ad-hoc "mint a narrowed token" mechanism — RFC 8693 is the only path.

**Claim format.** Per RFC 9068, the `scope` claim is a **space-separated string** of scope values (not an array):

```json
{
  "sub": "sa-prod-watchdog-01",
  "roles": ["platform-admin"],
  "caller_type": "agent",
  "scope": "tools:health:read tools:diagnostics:read tools:operations:read tools:escalation:create"
}
```

**Scope value syntax.** Each scope follows `tools:<domain>:<action>`, where:

- `<domain>` is the tool family: `pool`, `health`, `diagnostics`, `recommendations`, `runbooks`, `events`, `audit`, `drift`, `backup`, `restore`, `upgrade`, `locks`, `escalation`, `logs`, `me`, `operations`, `tenant`, `credential_pool`, `credential`, `runtime`, `quota`, `config`, `circuit_breaker`. The authoritative closed list lives at [Section 15.1](15_external-api-surface.md#151-rest-api) Scope taxonomy; this paragraph mirrors it.
- `<action>` is `read` (any `lenny_<domain>_list` / `lenny_<domain>_get` / `lenny_<domain>_summary` / similar read tool), `write` (any mutating tool), or a specific tool name (e.g., `scale`, `rotate`, `create`, `steal`).
- `*` is permitted in the `<action>` position only: `tools:pool:*` matches every pool tool (`read` and `write`). `tools:*` is the most permissive scope and is equivalent to omitting the claim.

Each MCP tool declares its scope identifier via the `x-lenny-scope` OpenAPI extension (Section 15.1), e.g., `lenny_pool_scale` declares `x-lenny-scope: "tools:pool:scale"`. The mapping from tool to scope is therefore build-time explicit, not inferred from the tool name.

**Matching.**

- A scope matches a tool if the scope's domain equals the tool's domain AND the scope's action equals the tool's action OR the scope's action is `*`.
- Multiple space-separated scopes are OR-combined; a tool is permitted if any scope matches.
- A request for a tool not permitted by any scope returns `403 SCOPE_FORBIDDEN` with a response body listing the caller's active scopes.
- Absent `scope` claim: no scope restriction — the token's role ceiling applies unmodified.

Scopes are enforced in three places:

1. **Admin API middleware** — every endpoint is mapped to a canonical scope via its `x-lenny-scope` OpenAPI extension. The middleware checks scopes before routing to the handler.
2. **MCP tool invocation** — `/mcp/management` `tools/call` checks the scope before dispatch.
3. **`/v1/admin/me/authorized-tools`** — pre-filters the tool list to what the caller's scopes permit.

**Example scoped-token policies** (deployer-configured at the OIDC identity provider):

- **Watchdog agent:** `"tools:health:* tools:diagnostics:* tools:recommendations:read tools:operations:read tools:me:* tools:escalation:create tools:runbooks:read tools:events:read"` — full observation plus ability to escalate; cannot mutate state.
- **Pool scaling bot:** `"tools:health:read tools:pool:* tools:me:* tools:operations:read"` — pool-domain only.
- **Upgrade orchestrator:** `"tools:upgrade:* tools:backup:* tools:restore:* tools:health:read tools:operations:read tools:me:*"`.
- **Fully-privileged platform-admin agent:** no `scope` claim (or `"tools:*"`).

Scopes do not replace tenancy — a `tenant-admin` caller is still constrained to its tenant regardless of scope. Scopes restrict *actions*; tenancy restricts *resources*. Both are enforced independently.

**Playground-allowed scope set.** The `/playground/*` mint paths ([§10.2 "Playground mint invariants"](10_gateway-internals.md#102-authentication)) narrow every minted session-capability JWT's `scope` to `intersection(subject_token.scope, playground_allowed_scope)` — never the union. For v1, `playground_allowed_scope` is pinned to:

```
{tools:sessions:*, tools:me:read, tools:runtimes:read, tools:pools:read, tools:operations:read, tools:events:read}
```

This covers chat session management (the playground's primary purpose) plus read-only runtime/pool/operations/events introspection (needed for the playground's runtime picker and status panels). It deliberately omits write scopes on `runtime`, `pool`, `credential_pool`, `quota`, `config`, `upgrade`, `backup`, `restore`, and `experiment` — the playground never needs to mutate platform configuration, so no capability to do so is minted even if the pasted subject token would otherwise permit it. Operators who need to exercise write-scope tools against the platform should use `lenny-ctl` or a dedicated client with a direct OIDC/service-account token, not the playground.

**Standards note.** The claim name (`scope`), format (space-separated string), and colon-separated domain:action syntax follow OAuth 2.0 conventions so that off-the-shelf OIDC libraries can parse, sign, and inspect tokens without custom claim mappers. Deployers using any OIDC provider (Dex, Keycloak, Okta, Azure AD, AWS Cognito, etc.) can configure scopes through the provider's standard "scopes" UI — no Lenny-specific claim plumbing required.

### Agent Identity and Correlation

Agents may include the following optional headers on any request to the admin API or `lenny-ops`:

- **`X-Lenny-Operation-ID`** — a caller-generated UUID that ties multiple API calls to a single remediation effort. All audit events produced during the request include this ID, enabling post-incident analysis of multi-step remediations ("what did the agent do in response to alert X?").
- **`X-Lenny-Agent-Name`** — a human-readable identifier for the agent instance (e.g., `"prod-watchdog-us-east-1"`). Recorded in audit events alongside the service account identity.

These headers are advisory — omitting them does not affect request processing. They are propagated to audit events and included in operational metrics labels (`agent_name`).

---

## 25.2 Architecture Overview

### The Split

```
┌─────────────────────────────────────────────────────────────────────┐
│                              Gateway                                │
│                                                                     │
│  Client traffic (Sections 4-14)                                     │
│  Admin API (Section 15.1)                                           │
│                                                                     │
│  Ops-facing endpoints on the admin API:                             │
│    GET  /v1/admin/health              Aggregate health              │
│    GET  /v1/admin/health/{component}  Component deep-dive           │
│    GET  /v1/admin/health/summary      Minimal status                │
│    GET  /v1/admin/recommendations     Capacity recommendations      │
│    GET  /v1/admin/platform/version    Compiled-in version info      │
│    GET  /v1/admin/platform/config     Effective running config      │
│    GET  /v1/admin/events/buffer       In-memory event ring buffer   │
│                                                                     │
│  /metrics (Prometheus scrape target)                                │
│                                                                     │
│  Internal: EventEmitter writes to Redis stream on state changes     │
│            + in-memory ring buffer (fallback for Redis outage)      │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                          lenny-ops Deployment                       │
│                                                                     │
│  Operational Event Stream      (25.5)   — reads Redis stream,       │
│                                           falls back to gw buffer   │
│  Diagnostic Endpoints          (25.6)   — queries Postgres/K8s,     │
│                                           Prometheus, gateway API   │
│  Runbook Index                 (25.7)   — read-only, bundled markdown│
│  Platform Upgrade Orchestration(25.8)   — K8s API, Postgres         │
│  Audit Log Query               (25.9)   — Postgres                  │
│  Configuration Drift Detection (25.10)  — Postgres, gateway API     │
│  Backup and Restore            (25.11)  — K8s Jobs, Postgres, MinIO │
│  MCP Management Server         (25.12)  — translates MCP → REST     │
│                                                                     │
│  Connects to: Postgres, Redis, K8s API, MinIO, Prometheus,          │
│               Gateway admin API                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Why This Boundary

The split criterion is simple: **does the feature read in-process state that cannot be obtained externally?**

| Feature | In-process state required | Where it lives |
|---|---|---|
| Health API | Yes — reads `prometheus.Registry` directly, works when Prometheus is down. Reads in-process circuit breaker cache. | Gateway |
| Capacity Recommendations | Yes — in-memory ring buffers for sliding-window aggregation. No persistent storage. | Gateway |
| Event emission (`Emit()`) | Yes — gateway subsystems call `Emit()` at the point of state change. Writes to Redis + in-memory buffer. | Gateway |
| Event buffer (`/events/buffer`) | Yes — in-memory ring buffer of recent events. Fallback source when Redis is unavailable. | Gateway |
| Version introspection | Yes — `ldflags`-compiled build metadata. | Gateway |
| Running config | Yes — effective merged config lives in the running process. | Gateway |
| Everything else | No — reads from Postgres, Redis, K8s API, Prometheus, or calls the gateway's admin API. | `lenny-ops` |

### API Conventions

All operability endpoints — on both the gateway and `lenny-ops` — follow the conventions in this subsection. Individual endpoint sections use these conventions by reference rather than redefining them.

#### Terminology

To avoid ambiguity across the rest of this section:

- **In-memory** — state held in a process's RAM (metric registries, ring buffers, caches). Lost on process restart.
- **In-process** — code path or evaluator running inside a binary without external calls. Used only as an adjective for evaluators, never for storage.
- **Gateway-local** — state or logic scoped to a single gateway replica (per-replica). May differ across replicas under non-uniform load.
- **Platform-scoped** — state or signal that applies to the whole installation, not a specific tenant or session.
- **Shared-state** — state readable identically from any replica (typically backed by Postgres, Redis, or K8s API).

#### Canonical Degradation Envelope

Any response whose data quality depends on the availability of an external dependency includes a top-level `degradation` object with a uniform schema:

```json
{
  "degradation": {
    "level": "healthy",                    // "healthy" | "degraded" | "failed"
    "primarySource": "prometheus",         // what the response would normally come from
    "actualSource": "replica-fanout",      // what actually served the response
    "fallbackPath": ["prometheus", "replica-fanout", "in-memory"],
    "confidence": 0.75,                    // 0.0-1.0 — how much the agent should trust it
    "unavailableFields": ["retryHistory"], // fields that could not be populated
    "thresholdSource": "compiled-in-defaults", // optional — for health/recommendations endpoints
                                              // "operator-customized" | "compiled-in-defaults"
    "warnings": ["Pod events unavailable: K8s API unreachable"],
    "since": "2026-04-16T10:22:03Z"        // when degradation began (best effort)
  }
}
```

The envelope is the canonical response-level signal for every endpoint whose data quality depends on external dependency availability. The `thresholdSource` field is used for health and recommendations responses where the active rule set may differ from operator-configured Prometheus rules (Section 25.13).

**Distinguishing response signals from resource attributes.** Some Lenny resources have intrinsic durability or coordination attributes that are NOT part of the degradation envelope because they describe the resource itself, not the response that returned it:

- `Lock.lockStore` (Section 25.4 Remediation Coordination) — which storage tier this specific lock lives in. Stays on the Lock struct.
- `Escalation.persistence` (Section 25.4 Escalation) — which storage tier this specific escalation lives in. Stays on the Escalation struct.

These resource-attribute fields complement the response-level `degradation` envelope — the envelope says "this response was served from a fallback source," while the resource attribute says "this specific record exists in tier X." Both pieces of information are useful and distinct.

**Omitted when healthy.** Endpoints serving from their primary source omit `degradation` entirely (or return `"level": "healthy"` with no other fields set). Agents should treat an absent envelope as equivalent to healthy.

#### Pagination

All list endpoints use the following canonical parameters and response fields:

| Parameter | Purpose | Default | Max |
|-----------|---------|---------|-----|
| `cursor` | Opaque continuation token from the previous response. | — | — |
| `limit` | Page size. | 100 | 1000 |
| `since`, `until` | RFC 3339 timestamps for time-windowed queries. | — | — |
| `sortOrder` | `asc` or `desc`. Default is endpoint-specific (oldest-first for event-like lists, newest-first for audit). | endpoint-specific | — |

Response envelope for paginated responses:

```json
{
  "items": [ ... ],
  "pagination": {
    "cursor": "opaque-string",
    "hasMore": true,
    "limit": 100,
    "cursorKind": "redis-stream-id",   // endpoint-specific values; common kinds: "redis-stream-id", "buffer-seq", "timestamp", "pk", "redis", "buffer", "mixed", "none"
    "headCursor": "opaque-string"      // present when the page is at the head of a live stream
  }
}
```

**Cursor opaqueness.** Agents MUST NOT parse cursors. A cursor produced by one `actualSource` may be invalid at a different source — agents treat cursor incompatibility as a gap (see below) and reset.

**Gap detection.** When the provided cursor cannot be honored (evicted from a ring buffer, invalidated by a source transition, or otherwise lost), the response includes `"gapDetected": true` and a suggested recovery cursor:

```json
{
  "pagination": {
    "gapDetected": true,
    "gapReason": "cursor evicted from in-memory buffer during Redis outage",
    "oldestAvailableCursor": "...",
    "suggestedAction": "resync"
  }
}
```

Agents receiving `gapDetected: true` should re-read platform state (e.g., `GET /v1/admin/health`) before assuming the new page is continuous with their prior position. Events prior to the gap are lost.

#### Dry-Run / Confirm Pattern

All mutating endpoints that make non-convergent changes follow a single pattern:

1. Request body MAY include `"confirm": true`.
2. Without `confirm: true`, the endpoint returns **200 OK** with a **dry-run preview** and `"dryRun": true` in the response body. No state is mutated.
3. With `confirm: true`, the endpoint executes the change and returns its normal response.

This applies to: `PUT /v1/admin/pools/{name}/warm-count` (when the change is >50% of current), `PUT /v1/admin/platform/config`, `POST /v1/admin/backups` (full backups in production), `POST /v1/admin/restore/execute`, `POST /v1/admin/drift/reconcile`. Naturally-convergent operations (e.g., full-state `PUT` of a pool config) do not require `confirm` and simply idempotently apply.

Preview responses include a `preview` object describing what would be done:

```json
{
  "dryRun": true,
  "preview": {
    "resourcesAffected": [...],
    "estimatedDowntime": "0s",
    "warnings": [...]
  }
}
```

Endpoints that require `confirm: true` and omit it return **200** with a preview, not an error. Endpoints that require additional acknowledgment fields (e.g., restore's `acknowledgeDataLoss: true`) return **400** with an actionable error if the acknowledgment is missing after `confirm: true` was supplied — this distinguishes "I need more info" from "I'm previewing."

#### Error Response Envelope

All errors use the following envelope:

```json
{
  "error": {
    "code": "REMEDIATION_LOCK_CONFLICT",
    "category": "TRANSIENT",
    "message": "Another agent holds a lock on scope 'pool:default-gvisor'.",
    "retryable": true,
    "suggestedRetryAfter": "30s",
    "details": { ... },
    "documentationUrl": "https://docs.lenny.dev/errors/REMEDIATION_LOCK_CONFLICT"
  }
}
```

**Categories and retry semantics:**

| Category | HTTP range | Meaning | Agent behavior |
|----------|-----------|---------|---------------|
| `TRANSIENT` | 5xx, 429, selective 409 | Temporary failure; safe to retry. | Retry with exponential backoff respecting `Retry-After`. |
| `PERMANENT` | 4xx (400, 404, 422) | Will not succeed as-is; agent must change input. | Do not retry. Agent must re-examine input. |
| `POLICY` | 400, 409 | Operation is rejected by a platform policy (e.g., missing `confirm`, conflict with another operation). Retry only after the caller takes an action. | Take the indicated action (add `confirm`, wait for a condition, resolve a conflict) and retry. |
| `AUTH` | 401, 403 | Authentication or authorization failure. | Refresh credentials or fail to higher layer. |

Every error code defined in this section includes a `Category` in its error-codes table. `retryable` is present on the response envelope and duplicates `category != "PERMANENT"` for convenience; agents can rely on either. `suggestedRetryAfter` is advisory but matches the `Retry-After` HTTP header when present.

#### Filter Parameter Naming

Filter query parameters use the following canonical names across all list endpoints:

| Parameter | Used for | Examples |
|-----------|----------|----------|
| `eventType` | Filter by event type (singular or CSV). | `?eventType=alert_fired,session_failed` |
| `severity` | Filter by severity (CSV). | `?severity=critical,warning` |
| `resourceType`, `resourceId` | Filter by resource. | `?resourceType=pool&resourceId=default-gvisor` |
| `tenantId` | Filter by tenant. | `?tenantId=t-12345` |
| `actorId` | Filter by caller identity. | `?actorId=prod-watchdog` |
| `status` | Filter by lifecycle state (domain-specific). | `?status=open,acknowledged` |
| `since`, `until` | Time window. | `?since=2026-04-16T00:00:00Z` |


#### Operation Correlation

Agents include the following optional headers on any mutating request to tie multi-step operations together:

- **`X-Lenny-Operation-ID`** — caller-generated UUID. Propagated to audit events, operational events, and structured logs. Enables "what did the agent do in response to alert X?" post-incident analysis.
- **`X-Lenny-Agent-Name`** — human-readable agent instance identifier.

These are advisory; omitting them has no effect on request processing.

#### Canonical Progress Envelope

Long-running operations (platform upgrades, restores, backups, backup verifications, drift reconciliations, webhook backlog drains) include a `progress` object in their status endpoint responses AND in the Operations Inventory (Section 25.4). The object has a uniform shape:

```json
{
  "progress": {
    "percent": 47,
    "completedSteps": 5,
    "totalSteps": 10,
    "currentStep": "migrating_shard_3",
    "currentStepDetail": "executing 043_add_session_metadata.sql on shard 3",

    "etaSeconds": 240,
    "etaConfidence": 0.7,
    "etaMethod": "historical_p50",

    "rateMetric": {
      "name": "shards_per_minute",
      "value": 0.5
    },

    "startedAt": "2026-04-16T10:10:00Z",
    "lastProgressAt": "2026-04-16T10:15:12Z",
    "stalledForSeconds": null
  }
}
```

**Field semantics:**

- **`percent`** — 0–100. Operations with discrete steps use `completedSteps / totalSteps * 100`. Size-based operations (e.g., backup dump) use `bytesWritten / bytesEstimated`. Rate-based operations (e.g., webhook backlog drain) use `1 - remaining / peak_backlog`. `null` when the operation has no meaningful percent basis.
- **`completedSteps` / `totalSteps`** — discrete step counts where applicable; `null` otherwise.
- **`currentStep`** — machine-readable step identifier (e.g., `"migrating_shard_3"`, `"OpsRoll"`, `"uploading_to_minio"`).
- **`currentStepDetail`** — human-readable detail suitable for an agent to pass through to a user ("executing 043_add_session_metadata.sql on shard 3").
- **`etaSeconds`** — server estimate of remaining time; `null` when the server has no basis (first-time operation, no historical samples).
- **`etaConfidence`** — `0.0`–`1.0`. Low for one-off operations; higher for operations with historical baselines in `ops_operation_baselines`.
- **`etaMethod`** — how the estimate was produced. Values: `"historical_p50"` (from baseline table), `"linear_extrapolation"` (from current rate), `"fixed_phase_durations"` (from compiled-in per-phase durations), `"rate_based"` (for size/rate operations), `"none"` (no estimate available).
- **`rateMetric`** — current throughput metric for transparency. Agents that distrust the server's ETA can compute their own from this value.
- **`lastProgressAt`** — when progress last advanced.
- **`stalledForSeconds`** — populated when `now() - lastProgressAt` exceeds the operation kind's expected cadence. `null` when advancing normally.

**Historical baselines.** A Postgres table `ops_operation_baselines (kind, p50_duration_ms, p90_duration_ms, sample_size, last_updated)` is updated on each operation completion. New operations of a kind with `sample_size >= 3` receive `etaMethod: "historical_p50"` and `etaConfidence >= 0.5`; below that threshold they return `etaMethod: "none"`.

**Stalled detection.** Each operation kind defines an expected max inter-step cadence (e.g., 2 min per migration, 5 min per restore shard). Operations where `stalledForSeconds > 0` trigger the `OperationStalled` bundled alert (Section 25.13), giving agents and operators an automatic signal without having to track progress themselves.

**Progress events.** The event stream emits `operation_progressed` events whenever a step transition occurs OR `percent` crosses a named threshold (10, 25, 50, 75, 90, 95, 99). Agents subscribed with `?eventType=operation_progressed` receive real-time updates without polling.

Each operation subsystem populates the envelope with the semantics relevant to its domain (per-subsystem specifics in Sections 25.5, 25.8, 25.10, 25.11).

---

## 25.3 Gateway-Side Ops Endpoints

These endpoints live on the existing admin API, served from the gateway's main port. They require `platform-admin` or `tenant-admin` role (same as the rest of the admin API). They share the admin API's listener, port, goroutine pool, and TLS configuration.

### Platform Health API

A unified health surface that synthesizes component status, metric thresholds, and alert states into structured, actionable responses. The health endpoints are lightweight (in-process metric reads + parallel 2s-timeout dependency probes) and run on the admin API's existing goroutine budget.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/health` | Aggregate health of all components |
| `GET` | `/v1/admin/health/{component}` | Component deep-dive with diagnostics and metric values |
| `GET` | `/v1/admin/health/summary` | Minimal status for synthetic health checks |

#### Go Interface

```go
// pkg/gateway/health/service.go

type HealthService interface {
    GetAggregateHealth(ctx context.Context) (*AggregateHealthResponse, error)
    GetComponentHealth(ctx context.Context, component string) (*ComponentHealthResponse, error)
    GetHealthSummary(ctx context.Context) (*HealthSummaryResponse, error)
}
```

#### Data Sources

The `HealthService` reads from three sources, none of which require Prometheus:

1. **In-process metric registry.** The gateway reads gauge/counter values directly from the same `prometheus.Registry` that Prometheus scrapes. No Prometheus query needed — this works even when Prometheus is down.
2. **Redis circuit breaker state.** Reads `cb:{name}` keys from `StoreRouter.PlatformRedis()` (Section 11.6). Falls back to the gateway's in-process cache if Redis is unreachable.
3. **Lightweight dependency probes.** TCP connect + single-query probes against Postgres (`SELECT 1`), Redis (`PING`), MinIO (`HeadBucket`), K8s API server (`/healthz`), cert-manager (certificate status), and registered connectors. Each probe has a hard timeout of 2 seconds. Probes run in parallel.

#### Health Derivation Rules

Component status is derived deterministically from the same threshold expressions used by the alerting rules (Section 16.5):

- `healthy` — no firing alerts for this component
- `degraded` — warning-severity alerts firing
- `unhealthy` — critical-severity alerts firing

The `HealthService` maintains an in-memory alert state tracker that evaluates the same threshold expressions as the bundled Prometheus alerting rules (Section 25.13), but reads from the in-process metric registry. Both sides share the rule definitions via the `pkg/alerting/rules` package — the gateway tracker and the rendered Helm manifests are generated from a single source. This means `/v1/admin/health` returns accurate results even when Prometheus itself is unreachable. When operators customize rules in their Prometheus configuration, the gateway's in-process tracker continues to use the compiled-in defaults (see Section 25.13, Gateway In-Process Tracker).

**Per-replica scope.** This endpoint reflects the calling replica's view. Shared-state signals (dependency probes, circuit breakers, pool counts from Postgres) are identical across replicas. Per-replica signals (request queue depth, active connections, error rates) reflect only the replica that handled the request. `lenny-ops` aggregates health across all replicas via Prometheus (primary) or headless Service fan-out (fallback) — see Section 25.4, Metrics Source.

#### `suggestedAction` / `suggestedActions` Contract

When a component is degraded or unhealthy, the response includes a machine-executable remediation hint. Two forms are used depending on whether one or multiple reasonable responses exist:

```go
type SuggestedAction struct {
    Action     string          `json:"action"`             // SCALE_WARM_POOL, ADD_CREDENTIALS, RESTART_COMPONENT, etc.
    Endpoint   string          `json:"endpoint"`           // admin API endpoint to call
    Body       json.RawMessage `json:"body"`               // request body
    Reasoning  string          `json:"reasoning"`          // human-readable explanation
    Runbook    string          `json:"runbook,omitempty"`  // operational runbook name (Section 25.7)
    Confidence float64         `json:"confidence,omitempty"` // 0.0–1.0; present on ranked alternatives
    Risk       string          `json:"risk,omitempty"`     // "none" | "low" | "medium" | "high"; present on ranked alternatives
}
```

**When one canonical response exists** (e.g., `SessionStoreUnavailable`, `MinioUnreachable`, `CertExpiryImminent`): the response has a single `suggestedAction` field holding one `SuggestedAction`. `Confidence` and `Risk` are omitted (the action is the singular correct response).

**When multiple reasonable responses exist** (typically capacity/throttling alerts like `WarmPoolExhausted`, `CredentialPoolExhausted`, `CircuitBreakerOpen`): the response has a `suggestedActions` array containing ordered `SuggestedAction` entries, each with populated `Confidence` and `Risk`. The array is ordered by descending confidence. Agents pick based on their policy — the highest-confidence option is usually right, but alternatives exist for context (e.g., scale the pool vs. investigate the upstream cause).

Example for `WarmPoolExhausted`:

```json
{
  "status": "unhealthy",
  "issue": "WARM_POOL_EXHAUSTED",
  "suggestedActions": [
    {
      "action": "SCALE_WARM_POOL",
      "endpoint": "PUT /v1/admin/pools/default-gvisor/warm-count",
      "body": { "minWarm": 15 },
      "reasoning": "Peak claim rate 4.2/min over 8 minutes. Scaling to 15 absorbs current demand with 30% headroom.",
      "runbook": "warm-pool-exhaustion",
      "confidence": 0.85,
      "risk": "low"
    },
    {
      "action": "INVESTIGATE_UPSTREAM",
      "endpoint": "GET /v1/admin/diagnostics/sessions",
      "reasoning": "Pool exhaustion may be a symptom of excessive retries from failing sessions. Investigate before scaling as a band-aid.",
      "runbook": "oom-root-cause",
      "confidence": 0.55,
      "risk": "none"
    }
  ]
}
```

Example for `SessionStoreUnavailable` (singular):

```json
{
  "status": "unhealthy",
  "issue": "SESSION_STORE_UNAVAILABLE",
  "suggestedAction": {
    "action": "RUN_POSTGRES_FAILOVER",
    "endpoint": null,
    "reasoning": "Database dependency unreachable. Follow the postgres-failover runbook; no automated API-driven remediation.",
    "runbook": "postgres-failover"
  }
}
```

**Alerts with ranked alternatives** (`suggestedActions`): `WARM_POOL_EXHAUSTED`, `WARM_POOL_LOW`, `CREDENTIAL_POOL_EXHAUSTED`, `CIRCUIT_BREAKER_OPEN`. All others use the singular `suggestedAction` field.

The `runbook` field names the relevant operational runbook for the detected issue. See Section 25.7 "Path B" for the mapping and discovery flow.

Suggestions are advisory — the agent is free to ignore them, pick a non-top alternative, or modify parameters. Agents that select an alternative should include their reasoning in the resulting audit event's `operationId`-correlated record (via a subsequent `POST /v1/admin/escalations` or a comment on the lock acquisition).

#### Caching

Component probe results are cached in-memory for 5 seconds to avoid probe storms from concurrent health checks. The cache is per-gateway-replica (not shared). Metric registry reads are instantaneous (same-process).

#### Degradation

If Postgres is unreachable: `postgres.status` reports `"unhealthy"` with `"details": {"reachable": false}`. If Redis is unreachable: `redis.status` reports `"unhealthy"`; circuit breaker state falls back to in-process cache. If MinIO is unreachable: `objectStore.status` reports `"unhealthy"`. **The health endpoint itself never returns 5xx** — it reports what it can observe.

#### Storage

None. Purely computed from runtime state.

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_health_check_duration_seconds` | Histogram | `component` | Probe latency per component |
| `lenny_health_status` | Gauge | `component` | 0=healthy, 1=degraded, 2=unhealthy |

#### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `UNKNOWN_HEALTH_COMPONENT` | `PERMANENT` | 404 | Requested component name is not recognized |

### Capacity Recommendations

A rules engine that synthesizes current metrics and usage patterns into actionable capacity recommendations.

#### Endpoint

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/recommendations` | Prioritized recommendations. Optional `?category=` filter. |

#### Go Interface

```go
// pkg/gateway/recommendations/service.go

type CapacityService interface {
    GetRecommendations(ctx context.Context, category *string) (*RecommendationsResponse, error)
}

type MetricReader interface {
    GaugeValue(name string, labels map[string]string) (float64, bool)
    CounterValue(name string, labels map[string]string) (float64, bool)
    HistogramQuantile(name string, labels map[string]string, quantile float64) (float64, bool)
    WindowedRate(name string, labels map[string]string, window time.Duration) (float64, bool)
}
```

#### Rules Engine

Rules are deterministic heuristics, not AI. Each rule reads metric values from the in-process registry, evaluates a condition, and generates a recommendation with a formula-derived value and confidence score.

| Category | Condition (example) | Recommendation logic |
|---|---|---|
| `warm_pool_sizing` | Pool exhausted 3+ times in 24h | `minWarm = ceil(peak_claim_rate * (startup_p99 + failover_seconds) * 1.3)` |
| `credential_pool_sizing` | Utilization > 70% over 7d with rate-limit events | "Add N credentials to bring utilization below 60%" |
| `gateway_scaling` | CPU > 70% or queue depth > HPA target for > 15m | "Increase HPA max replicas" |
| `resource_limits` | OOM events > 0 in 24h | "Increase memory limit for pool X" |
| `retention_tuning` | Storage utilization > 80% | "Reduce artifact retention TTL" |
| `quota_adjustment` | Quota rejection rate > 5% over 24h | "Increase tenant session quota" |

**Sliding window aggregation.** The rules engine maintains in-memory ring buffers per metric (no Postgres, no Redis). Window sizes are configurable per rule (default: 24h for pool sizing, 7d for credential sizing). After a gateway restart, windows are empty and recommendations include `"confidence": 0.0` and `"dataAvailable": false`.

**Memory budget at Tier 3.** Ring buffers store one sample per emission for the configured window. Sample size is ~64 bytes (timestamp + value + labels). At Tier 3 with ~50 distinct metrics tracked and 7-day max windows:

- 50 metrics × (7 days × 24 hours × 60 minutes × 1 sample/min average rate) × 64 bytes ≈ 32 MB per gateway replica.
- High-emission-rate metrics (request counters at 100/s) use sub-second downsampling within ring buffers — the indexed window stores 1 sample per second, with full-resolution samples kept only for the most recent 5 minutes. This keeps total memory bounded at ~50 MB per replica even under burst load.

Operators concerned about memory can reduce window sizes (sacrifice recommendation accuracy) via `gateway.recommendations.windowOverrides`. The default windows are intentionally generous because Prometheus is the primary aggregation source at Tier 2/3 — the ring buffers exist primarily as a fallback when Prometheus is unreachable.

A `lenny_recommendations_ring_buffer_bytes` gauge per replica reports current memory use. Alert if it exceeds 100 MB (indicates buffer is undersized in time/sample budget calculations or some emitting code is over-emitting).

**Per-replica scope.** Each replica's ring buffers accumulate from its own traffic (~1/N of total requests). The recommendation values are directionally correct but based on a partial sample. `lenny-ops` produces aggregate recommendations using Prometheus data (primary) or headless Service fan-out (fallback) — see Section 25.4, Metrics Source.

The recommendation rules are defined in a shared package (`pkg/recommendations/rules`) compiled into both the gateway and `lenny-ops` binaries. In the gateway, rules evaluate against the in-process `MetricReader`. In `lenny-ops`, the same rules evaluate against a Prometheus-backed `MetricReader` that queries aggregate metrics across all replicas.

Deployers disable specific rules via `platform.recommendations.disabledRules` Helm value (array of rule IDs).

#### Degradation

If metrics are stale (gateway recently restarted): recommendations include `"confidence": 0.0`. No recommendations are generated for categories with insufficient data.

#### Storage

None. Computed on-demand from in-memory metric state.

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_recommendations_generated_total` | Counter | `category`, `priority` | Recommendations generated |

#### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `UNKNOWN_RECOMMENDATION_CATEGORY` | `PERMANENT` | 400 | Unrecognized category filter |
| `RECOMMENDATIONS_UNAVAILABLE` | `TRANSIENT` | 503 | Returned only when `ops.recommendations.disableOnPrometheusOutage: true` and Prometheus is unreachable. |

### Version and Config Introspection

Two read-only endpoints that return process-local state.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/platform/version` | Compiled-in version info (`gateway.version`, `gitCommit`, `buildDate`, `goVersion`) |
| `GET` | `/v1/admin/platform/config` | Effective running configuration (secrets redacted) |

`GET /v1/admin/platform/version` returns only the gateway's own compiled-in metadata. Component versions that require K8s API or Postgres queries (controller versions, CRD versions, schema version) are served by `lenny-ops` (Section 25.8), which aggregates them with the gateway's version response to produce a full version report.

`GET /v1/admin/platform/config` returns the effective merged config. Secret values are redacted to `"***"`.

#### Storage

None.

### Event Emission

Gateway subsystems emit operational events by writing to a Redis stream and an in-memory ring buffer. This is not a new endpoint — it's an internal behavior.

Every emitted event is a [CloudEvents v1.0.2](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) JSON record; see [§12.6](12_storage-architecture.md#126-interface-design) EventBus for the envelope contract. The CloudEvents `id` attribute is the canonical `eventKey` described below — the two names refer to the same value. The CloudEvents `type` follows the form `dev.lenny.<short_name>` (see [§16.6](16_observability.md#166-operational-events-catalog) for the full type catalogue); the short names in the table below are the suffix of the `type`.

**Single-envelope model — no double-wrapping.** Audit-bearing events carry the OCSF record **directly** in the CloudEvents `data` field with `datacontenttype: application/ocsf+json`. The CloudEvents envelope is the transport; the OCSF record is the payload; there is no intermediate container between them. Consumers parse the CloudEvents record first, read `data` as the OCSF v1.1.0 record ([§11.7](11_policy-and-controls.md#117-audit-logging) Wire Format), and apply the Lenny → OCSF field mapping. Non-audit operational events use `datacontenttype: application/json` and carry an event-specific JSON payload in `data` whose schema is documented per `type` in the catalogue ([§16.6](16_observability.md#166-operational-events-catalog)).

```go
// pkg/gateway/events/emitter.go

// OperationalEvent is a CloudEvents v1.0.2 Event — see §12.6.
type OperationalEvent = cloudevents.Event

type EventEmitter interface {
    Emit(ctx context.Context, event OperationalEvent) error
}
```

`Emit()` writes to two destinations:

1. **Redis stream** `ops:events:stream` via `XADD` with `MAXLEN ~ 10000`.
2. **In-memory ring buffer** (500 events, ~250 KB). The buffer is always written, regardless of Redis availability.

The emitter is called by existing gateway subsystems at the point of state change:

- Alert state evaluator → `alert_fired`, `alert_resolved`
- Upgrade state machine → `upgrade_progressed`
- Pool state manager → `pool_state_changed`
- Circuit breaker handler → `circuit_breaker_opened`, `circuit_breaker_closed`
- Session manager → `session_failed`
- Credential pool manager → `credential_rotated`, `credential_pool_exhausted`
- Health service → `health_status_changed`

#### Event Types

| Event type | Trigger | Payload highlights |
|---|---|---|
| `alert_fired` | Any alerting rule (Section 16.5) fires | Alert name, severity, labels, runbook ref, suggested action |
| `alert_resolved` | A previously firing alert resolves | Alert name, duration |
| `upgrade_progressed` | Pool upgrade state machine advances (Section 10.5) | Pool, old/new phase, image digest |
| `pool_state_changed` | Pool enters/exits draining, warming, exhausted | Pool, old/new state |
| `circuit_breaker_opened` | Circuit breaker opened | Name, reason, opener identity |
| `circuit_breaker_closed` | Circuit breaker closed | Name, closer identity |
| `credential_rotated` | Credential lease rotated | Pool, credential ID, reason |
| `credential_pool_exhausted` | No available credentials | Pool |
| `session_failed` | Session entered `failed` state | Session ID, runtime, failure class |
| `backup_completed` | Backup job finished | Type, status, size, duration |
| `backup_failed` | Backup job failed | Type, error |
| `platform_upgrade_available` | New Lenny release detected | Current version, available version |
| `drift_detected` | Configuration drift detected (Section 25.10) | Resource type, name, drifted fields |
| `health_status_changed` | Aggregate health transitioned | Old status, new status, triggering component |
| `ops_health_status_changed` | `lenny-ops` self-health transitioned (Section 25.4) | Old status, new status, triggering check |
| `escalation_created` | Agent recorded a structured escalation (Section 25.4) | Severity, source, alert name, summary |
| `remediation_lock_acquired` | Agent acquired a remediation lock (Section 25.4) | Scope, operation, agent name |
| `remediation_lock_released` | Remediation lock released or expired | Scope, reason (explicit / expired) |

If Redis is unreachable, `Emit()` skips the Redis write, logs the event at WARN level, and increments `lenny_ops_events_emit_failed_total`. The in-memory ring buffer write always succeeds. Events in the buffer are queryable via the gateway event buffer endpoint (see below). Events are best-effort notifications — the underlying state is always queryable via health and admin API endpoints.

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_events_emitted_total` | Counter | `type` | Events emitted by type |
| `lenny_ops_events_emit_failed_total` | Counter | `type` | Events that failed to emit (Redis unreachable) |

### Gateway Event Buffer

An in-memory ring buffer of recent operational events, exposed as an admin API endpoint. This is the fallback event source when Redis is unavailable — the same pattern as the Prometheus → gateway `/metrics` scrape fallback for diagnostics.

#### Endpoint

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/events/buffer` | Recent events from in-memory buffer. Params: `?since={monotonic_id}`, `?eventType=`, `?severity=`, `?limit=` (default 100, max 500). |

#### Go Interface

```go
// pkg/gateway/events/buffer.go

type EventBuffer struct {
    mu     sync.RWMutex
    events [500]OperationalEvent  // fixed-size ring buffer
    head   uint64                 // monotonic counter, used as event ID
}

type EventBufferService interface {
    // Query returns events after the given cursor. Returns events in
    // chronological order with monotonic IDs for cursor-based polling.
    Query(ctx context.Context, since uint64, filter EventFilter, limit int) (*BufferedEventPage, error)
}

type BufferedEventPage struct {
    Events  []BufferedEvent `json:"events"`
    Cursor  uint64          `json:"cursor"`   // monotonic ID of last event returned
    HasMore bool            `json:"hasMore"`
}
```

#### Behavior

The buffer holds the last 500 events (~250 KB). Each event is assigned a monotonic uint64 ID (per gateway replica, not globally ordered) AND carries a stable `eventKey` — a ULID-like identifier composed of `{replicaID}:{emittedAt}:{nonce}`, where `nonce` is a **per-replica monotonically-increasing uint64 counter** that increments for every event emitted by that replica (regardless of the emitting subsystem). Combined with the unique `replicaID`, this guarantees `eventKey` is globally unique across all replicas and across emission paths (gateway in-process emission, `lenny-ops` emission). Two events with the same `eventKey` are by definition the same event — deduplication is correct. The `eventKey` is stable across restarts (the nonce counter survives via a periodic checkpoint to local disk; on restart, the counter resumes from `last_checkpointed + safe_skip_window` to avoid any chance of replaying an old nonce).

`lenny-ops` polls the buffer by passing its last-seen buffer sequence ID as `?since={uint64}`. When the buffer has wrapped and the requested ID has been evicted, the response includes the canonical `pagination.gapDetected: true` envelope (Section 25.2) along with `pagination.oldestAvailableCursor` — the oldest remaining buffer ID. Agents receiving a gap response should re-read platform state (health, diagnostics) before assuming continuity.

The buffer is per-gateway-replica (not shared). When `lenny-ops` uses the buffer fallback, it discovers all gateway pod IPs via the headless Service `lenny-gateway-pods` (see below) and polls each replica individually. **Deduplication across replicas uses `eventKey`**, not a content hash — this avoids the collision class where two distinct events happen to have identical `(type, timestamp, payload)` across replicas (e.g., two replicas both emitting `alert_fired` for the same alert in the same second, or two replicas both reporting a `credential_rotated` for a broadcast rotation). Content hashing is still used as a secondary check to detect truly duplicate events from a single replica (should not happen but defends against retry bugs).

**Headless Service for buffer polling.** The Helm chart renders a headless Service (`lenny-gateway-pods`, `clusterIP: None`) alongside the standard ClusterIP Service. `lenny-ops` uses DNS SRV lookup (`lenny-gateway-pods.{namespace}.svc`) to discover all gateway pod IPs. This Service is used exclusively for the event buffer fallback — all other `GatewayClient` calls use the standard ClusterIP Service (`lenny-gateway`).

After a gateway restart, the buffer is empty. The response includes `"bufferAge"` (duration since the oldest buffered event) so agents can assess data completeness.

#### Degradation

None — purely in-process. This endpoint is always available when the gateway is running.

#### Storage

None. In-process ring buffer only.

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_events_buffer_length` | Gauge | | Current events in buffer |
| `lenny_events_buffer_queries_total` | Counter | | Buffer endpoint queries |
| `lenny_events_buffer_gaps_total` | Counter | | Queries where requested cursor was evicted |

---

## 25.4 The `lenny-ops` Service

### Purpose

A **mandatory** standalone service that hosts all operability features not implemented in-process by the gateway (Section 25.3). It provides the diagnostic, remediation, and lifecycle management surface for DevOps agents and operators. Every Lenny installation includes a `lenny-ops` deployment regardless of tier — there is no supported topology without it. The features it hosts (audit, backup, upgrade orchestration, drift detection, event stream, MCP management) have no alternative path.

### Deployment

A separate Kubernetes Deployment: `lenny-ops`. Always deployed by the Helm chart. Single replica by default (leader-elected for singleton behaviors like webhook delivery and backup scheduling), scalable to multiple replicas for read-heavy workloads (audit queries, diagnostics).

#### Helm Values Hierarchy

Lenny's Helm chart uses these top-level keys. Every Helm value referenced in Section 25 lives under one of these keys — there are no other top-level keys defined by the operability surface.

| Top-level key | Purpose |
|---------------|---------|
| `platform` | Cross-component platform settings (registry, upgrade channel, tier). |
| `monitoring` | Prometheus integration, bundled alerting rules, observability. |
| `gateway` | Gateway Deployment config (replicas, resources, HPA, health tracker). |
| `ops` | `lenny-ops` Deployment config (replicas, resources, Ingress, rate limiting). |
| `controllers` | Warm pool and pool scaling controllers. |
| `backups` | Backup schedule, retention, encryption. |
| `security` | OIDC, webhook signing, TLS overrides. |

The full canonical values.yaml reference is maintained at `deploy/helm/lenny/values.yaml` in the repository. Tier-specific presets live at `deploy/helm/lenny/values-tier1.yaml`, `-tier2.yaml`, `-tier3.yaml` and override a defined subset of the base values (listed in the comment header of each preset file).

#### `lenny-ops` Helm Values

```yaml
platform:
  tier: "tier2"                          # "tier1" | "tier2" | "tier3" — drives tier-dependent defaults
  registry:
    url: "ghcr.io/lennylabs"             # all component images resolve relative to this
    overrides: {}                        # per-component: { gateway: "my-registry/lenny/gateway" }
    pullSecretName: ""                   # K8s Secret of type kubernetes.io/dockerconfigjson
    requireDigest: false                 # require digest-pinned refs (recommended for prod)
  upgradeChannel: "https://releases.lenny.dev/v1/latest"  # "" to disable

monitoring:
  namespace: "monitoring"                # where operator's Prometheus lives
  podLabel: "app.kubernetes.io/name=prometheus"  # label to find Prometheus pods
  bundleRules: true                      # render Lenny's bundled alerting rules
  format: "prometheusrule"               # "prometheusrule" | "configmap" | "both"
  acknowledgeNoPrometheus: false         # set true to deploy Tier 2/3 without Prometheus
  prometheusRule:
    namespace: ""                        # default: same as monitoring.namespace
    additionalLabels: {}                 # for prometheus-operator selector matching
  configMap:
    namespace: ""                        # default: lenny-system
    name: "lenny-alerting-rules"
  alertThresholds:                       # tier-preset files override these
    gatewayQueueDepthHigh:
      value: 10
      duration: "5m"
      severity: "warning"
    # ... see Section 25.13 for the full list
  alertOverrides: {}                     # per-rule overrides; see Section 25.13

gateway:
  replicas: 3
  # ... (see SPEC.md for the full gateway block)
  healthTracker:
    useCompiledRules: true               # false to disable the in-process alert tracker
                                         # (strict consistency with operator-customized
                                         # Prometheus rules, at the cost of losing health
                                         # status derivation when Prometheus is down)
  recommendations:
    windowOverrides: {}                  # per-rule window override, e.g. { warm_pool_sizing: "12h" }

ops:
  replicas: 1                            # leader-elected; scale >1 for read-heavy workloads
  image:
    repository: ""                       # defaults to {platform.registry.url}/lenny-ops
    tag: ""                              # defaults to .Chart.AppVersion
  resources:
    requests:
      cpu: 250m
      memory: 256Mi
    limits:
      cpu: "1"
      memory: 512Mi
  gateway:
    url: "https://lenny-gateway:8443"    # internal ClusterIP Service — HTTPS by default
                                         # (NET-070); chart renders http://lenny-gateway:8080 only
                                         # when ops.tls.internalEnabled=false AND
                                         # ops.acknowledgePlaintextAdminAPI=true (or dev mode).
    headlessService: "lenny-gateway-pods"  # for per-replica fan-out (Section 25.4)
    timeoutSeconds: 5                    # per-request timeout for ClusterIP calls
    fanOutTimeoutSeconds: 2              # per-replica timeout for headless fan-out
    fanOutCircuitBreaker:
      failureThreshold: 3                # consecutive failures before skipping a replica
      resetAfter: "60s"
  prometheus:
    url: ""                              # required at Tier 2/3; empty at Tier 1
    queryTimeoutSeconds: 15              # HTTP client timeout for PromQL queries
    unreachableThreshold: "10s"          # time above which Prometheus is treated as "down"
  ingress:
    host: "ops.lenny.example.com"        # required — external hostname for agent access
    tlsSecretName: ""                    # if empty, cert-manager issues via ClusterIssuer
    selfSigned: false                    # dev-only: chart generates self-signed cert when true
    className: "nginx"
    annotations: {}
    idleTimeoutSeconds: 900              # must exceed SSE client hold time
  sessionAffinity: "ClientIP"            # for SSE; overrideable per-Service
  rateLimiting:
    requestsPerSecond: 20                # per service account, across all endpoints
    burst: 50
  tls:
    internalEnabled: true                # TLS between lenny-ops and gateway — default in non-dev
                                         # profiles (NET-070). Flip to false only with
                                         # ops.acknowledgePlaintextAdminAPI=true (chart fails helm
                                         # install/upgrade otherwise outside dev mode).
    certSecretName: ""                   # ops.tls server-auth cert (cert-manager-issued by default)
    clientCertSecretName: ""             # optional: GatewayClient mTLS identity (server-auth only
                                         # when empty); see §10.3 for PKI details
  acknowledgePlaintextAdminAPI: false    # REQUIRED to be set to true if ops.tls.internalEnabled=false
                                         # outside dev mode (NET-070). Auto-implied when
                                         # global.devMode=true. Chart fails helm install/upgrade
                                         # with a message pointing at NET-070 if unset and
                                         # internalEnabled is false in a non-dev profile.
  leaderElection:
    backend: "k8s-lease"                 # "k8s-lease" (default) | "postgres"
    leaseName: "lenny-ops-leader"
    leaseDurationSeconds: 15
    renewDeadlineSeconds: 10
    retryPeriodSeconds: 2
  selfHealth:
    checkIntervalSeconds: 10             # 10 recommended at Tier 2/3 to catch fast failures
  webhooks:
    subscriptionCacheTTLSeconds: 60      # periodic refresh interval for the subscription cache
    generationBasedInvalidation: true    # invalidate cache entries immediately on CRUD
    allowHTTP: false                     # require HTTPS callbacks; HTTP is rejected
    blockedCIDRs:                        # application-layer SSRF check — defense in depth on top of
                                         # the `lenny-ops-egress` NetworkPolicy (§25.4) and the
                                         # gateway `allow-gateway-egress-llm-upstream` NetworkPolicy
                                         # (§13.2). Default list mirrors `egressCIDRs.excludePrivate`
                                         # so that NetworkPolicy and app-layer share one block list
                                         # at install time (NET-057); deployers SHOULD extend both
                                         # together if additional internal ranges must be blocked.
      - "10.0.0.0/8"
      - "172.16.0.0/12"
      - "192.168.0.0/16"
      - "169.254.0.0/16"
      - "fc00::/7"
      - "fe80::/10"
    domainAllowlist: []                  # if non-empty, callbacks must match a suffix in this list
    deliveryRetentionDays: 7             # ops_event_deliveries retention; tier presets override
    deliveryTrackingMode: "full"         # "full" | "metric-only" | "failures-only"
    failuresOnlyRetentionDays: 30        # when deliveryTrackingMode == "failures-only"
  idempotency:
    keyTTLSeconds: 86400                 # 24h — for standard mutations (pool scale, config apply, etc.)
    longRunningKeyTTLSeconds: 604800     # 7d — for multi-phase operations (upgrade, restore)
    bindToCaller: true                   # composite PK includes caller identity
  locks:
    postgresTier: "enabled"              # "enabled" | "disabled"
    redisTier: "enabled"                 # fallback when Postgres is down
    memoryTier: "single-replica-only"    # "single-replica-only" | "always" | "never"
                                         # ("never" fails lock acquisition with 503 in multi-replica
                                         # when both Postgres and Redis are down)
    minTTLSeconds: 10                    # reject lock acquire requests with TTL < 10s
    maxTTLSeconds: 1800                  # reject lock acquire requests with TTL > 30 min
    defaultTTLSeconds: 300               # used when caller omits ttlSeconds
  escalation:
    requireDurable: false                # set true to fail with 503 when no durable store available
    reconciliationWritesPerSecond: 20    # rate-limit on flush goroutine
  events:
    streamMaxLen: 10000                  # Tier 1 default; tier presets raise to 50k–100k
  recommendations:
    disableOnPrometheusOutage: false     # set true to return 503 instead of fan-out fallback
  drift:
    runningStateCacheTTLSeconds: 60      # cache running-state collection across drift queries
    snapshotStaleWarningDays: 7          # GET /v1/admin/drift sets snapshot_stale=true when
                                         # bootstrap_seed_snapshot.written_at is older than this many
                                         # days. Set 0 to disable the warning. See §25.10.
  audit:
    diagnosticsRatePerMinute: 60         # cap on diagnostic-audit emissions per service account
    scatterGatherCacheEnabled: true      # cache cross-shard audit query results
    scatterGatherMaxConcurrency: 0       # 0 = match number of shards
    retention:
      diagnosticsRetainDays: 30          # diagnostic audit events; shorter than the default audit retention

backups:
  schedule:
    full: "0 2 * * *"                    # daily full backup
    postgres: "0 */6 * * *"              # Postgres snapshots every 6 hours
    enabled: true
  retention:                             # base values are Tier 2 defaults; values-tierN.yaml overrides
    retainDays: 30                       # Tier 1: 7, Tier 2: 30, Tier 3: 90
    retainCount: 10                      # Tier 1: 5, Tier 2: 10, Tier 3: 30
    retainMinFull: 3                     # Tier 1: 2, Tier 2: 3, Tier 3: 7
    preRestoreRetainDays: 7              # pre-restore safety backups; cleaned aggressively
  verification:
    schedule: "0 3 * * 0"                # weekly integrity check
    testRestoreSchedule: "0 4 1 * *"     # monthly test restore to temporary namespace
  encryption:
    atRest: true                         # SSE on MinIO PutObject
    minioServerSide: "SSE-S3"            # "SSE-S3" | "SSE-KMS"
    kmsKeyId: ""                         # required when SSE-KMS in single-region mode;
                                         # overridden per-region when backups.regions is non-empty
    perTenantWrapKeys: false             # enable per-tenant crypto-shredding: each tenant's
                                         # dump is encrypted with a tenant-scoped wrap key that
                                         # DeleteByTenant destroys (alternative to reconciler for
                                         # tenant-level erasure only; see §12.8 Backups in erasure scope)
  regions: {}                            # per-region backup endpoints — REQUIRED when any tenant
                                         # has dataResidencyRegion set; mirrors storage.regions.
                                         # Example:
                                         #   eu-west-1:
                                         #     minioEndpoint: "https://minio.eu-west-1.internal:9000"
                                         #     kmsKeyId: "arn:aws:kms:eu-west-1:...:key/..."
                                         #     accessCredentialSecret: "lenny-backup-minio-eu-west-1"
                                         #   us-east-1:
                                         #     minioEndpoint: "https://minio.us-east-1.internal:9000"
                                         #     kmsKeyId: "arn:aws:kms:us-east-1:...:key/..."
                                         #     accessCredentialSecret: "lenny-backup-minio-us-east-1"
                                         # See §12.8 Backup pipeline residency.
  erasureReconciler:
    enabled: true                        # post-restore GDPR erasure reconciler; disable only if
                                         # retention.retainDays ≤ GDPR erasure SLA (72h T3 / 1h T4).
                                         # See §12.8 Backups in erasure scope.
  contentPolicy:
    includeSensitiveTables: false        # excludes secrets.* tables by default
    excludeTables: []                    # additional tables to exclude beyond defaults
    redactColumns: []                    # additional column redaction (column names matched verbatim)

# Additional platform.* keys (extending the platform block defined at the top of this values file)
platform:
  releaseChannel:
    publicKeyPath: ""                    # override Lenny's compiled-in Ed25519 public key when mirroring
  upgrade:
    opsRollTimeoutSeconds: 600           # 10 min — auto-rollback if OpsRoll doesn't finish
    gatewayRollTimeoutSeconds: 1200      # 20 min — accounts for warm-pool drain
    controllerRollTimeoutSeconds: 600    # 10 min
  recommendations:
    disabledRules: []                    # array of rule IDs to disable (across all replicas)

security:
  oidc:
    issuerUrl: ""                        # required — OIDC issuer for token validation
    tokenRefreshBeforeExpirySeconds: 60  # refresh lead time for GatewayClient's service-account token
    minTokenTTLSeconds: 300              # reject token TTLs below this
```

This block is the canonical reference. Individual subsections below reference specific fields by path (e.g., `ops.prometheus.url`); all such paths resolve to the block above.

### Kubernetes Resources

The Helm chart renders the resources below. All resources live in the `{Release.Namespace}` namespace (default `lenny-system`); the chart is parameterized for namespace overrides via standard Helm `--namespace` and `Release.Namespace` substitution.

#### Deployment: `lenny-ops`

- Configurable replicas (default 1), resource limits, pod security context (non-root, read-only root filesystem, `runAsNonRoot: true`, `seccompProfile: RuntimeDefault`, all capabilities dropped).
- Volume: `emptyDir` for `/tmp` (writable scratch).
- `topologySpreadConstraints` when `ops.replicas >= 2` to spread replicas across nodes.
- Annotations include `prometheus.io/scrape: "true"` and `prometheus.io/port: "9090"` so deployer Prometheus instances can scrape `lenny-ops` metrics natively.

#### Probes

```yaml
startupProbe:
  httpGet:
    path: /healthz
    port: 8090
  failureThreshold: 30
  periodSeconds: 2          # 60s total — covers Postgres connection establishment
readinessProbe:
  httpGet:
    path: /healthz?strict=true
    port: 8090
  failureThreshold: 3
  periodSeconds: 5
livenessProbe:
  httpGet:
    path: /healthz
    port: 8090
  failureThreshold: 5
  periodSeconds: 10
```

- **`/healthz`** (permissive): 200 if the process is alive and at least one of {Postgres, K8s API} is reachable. Used by liveness and startup.
- **`/healthz?strict=true`** (strict): 200 only when both Postgres connection AND K8s API are reachable. Used by readiness — traffic is not routed to replicas that can't serve the full API.

When the `strict=true` check returns 503, the response body identifies which dependencies are degraded so operators reading endpoint logs can diagnose the cause.

#### Services

| Service | Type | Port | Purpose |
|---------|------|------|---------|
| `lenny-ops` | ClusterIP | 8090 | Internal traffic; targeted by the Ingress. `sessionAffinity: ClientIP` with `sessionAffinityConfig.clientIP.timeoutSeconds: 10800` (3h) to keep SSE clients on the same replica through reconnects. |
| `lenny-gateway-pods` | ClusterIP, headless (`clusterIP: None`) | 8080 | Per-replica gateway pod discovery for event buffer fan-out. `publishNotReadyAddresses: false` — only ready replicas are returned via DNS SRV lookup. |

#### ServiceAccount and RBAC

`lenny-ops` uses the ServiceAccount `lenny-ops-sa` with the following bindings. Roles are namespace-scoped where possible; ClusterRoles are used only where the resource is cluster-scoped (CRDs, Nodes for diagnostics) or cross-namespace access is genuinely required.

**Role `lenny-ops-namespace`** (in `{Release.Namespace}`):

```yaml
rules:
  # Deployment patching for upgrades
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "patch", "update"]
  - apiGroups: ["apps"]
    resources: ["deployments/scale"]
    verbs: ["get", "patch"]
  # Pod and event diagnostics
  - apiGroups: [""]
    resources: ["pods", "pods/log", "events"]
    verbs: ["get", "list", "watch"]
  # Job creation (backup, restore, verify)
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["get", "list", "watch", "create", "delete"]
  # ConfigMap access for runtime config
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "list", "watch"]
  # Lease for leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]
```

**ClusterRole `lenny-ops-cluster`** (cluster-scoped):

```yaml
rules:
  # CRD reads for version introspection and drift detection
  - apiGroups: ["apiextensions.k8s.io"]
    resources: ["customresourcedefinitions"]
    verbs: ["get", "list", "watch"]
  # Lenny CRDs (RuntimeDefinition, WarmPool, etc.)
  - apiGroups: ["lenny.dev"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
  # Node events for diagnostics
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get", "list"]
```

**Preflight check.** `lenny-preflight` (Section 17.6) issues `kubectl auth can-i` against each verb/resource in the above bindings using the `lenny-ops-sa` token. Missing permissions are reported (non-blocking — the operator may have legitimate restrictions, but degraded features are flagged).

#### NetworkPolicy

Three policies are rendered:

**`lenny-ops-deny-all-ingress`** — default deny:
```yaml
podSelector: { matchLabels: { app: lenny-ops } }
policyTypes: [Ingress]
ingress: []
```

**`lenny-ops-allow-ingress-from-ingress-controller`** — explicit allow:
```yaml
podSelector: { matchLabels: { app: lenny-ops } }
policyTypes: [Ingress]
ingress:
  - from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: { ingress.controllerNamespace }  # configurable
        podSelector:
          matchLabels: { ingress.controllerLabel }
    ports:
      - protocol: TCP
        port: 8090
```

**`lenny-ops-egress`** — bound egress:
```yaml
podSelector: { matchLabels: { app: lenny-ops } }
policyTypes: [Egress]
egress:
  # Gateway (ClusterIP and headless) — cross-namespace: lenny-ops runs in its own
  # namespace, gateway runs in lenny-system. Both podSelector and namespaceSelector
  # MUST be present; omitting namespaceSelector would (incorrectly) restrict the rule
  # to same-namespace gateway pods that do not exist (NET-050). Selector uses the
  # canonical lenny.dev/component label, not the legacy `app:` key (NET-047).
  #
  # Port rendering (NET-070): exactly one of the two ports is rendered into this rule
  # at chart install/upgrade time, matching the transport the GatewayClient actually
  # negotiates. TLS port (default 8443, `gateway.internalTLSPort`) is rendered when
  # `ops.tls.internalEnabled: true` (default in non-dev profiles). The plaintext port
  # (default 8080, `gateway.internalPort`) is rendered only when
  # `ops.tls.internalEnabled: false` AND `ops.acknowledgePlaintextAdminAPI: true`
  # (or in dev mode). The `lenny-preflight` selector-consistency audit (§13.2
  # counterparty-rules note) verifies that the port rendered here matches the gateway
  # ingress allow-list port for `app: lenny-ops` peers (NET-051, NET-070).
  - to:
      - namespaceSelector:
          matchLabels: { kubernetes.io/metadata.name: lenny-system }
        podSelector:
          matchLabels: { lenny.dev/component: gateway }
    ports: [{ protocol: TCP, port: 8443 }]  # 8080 when plaintext acknowledged
  # Postgres (via PgBouncer — `lenny-ops` does not bypass the pooler; direct `app: postgres`
  # access is reserved for `lenny-backup-job`). Both `namespaceSelector` AND `podSelector`
  # MUST be present (NET-061): omitting the `podSelector` is interpreted by K8s as
  # "any pod in the namespace," which means every other workload in `{ storage.namespace }`
  # listening on TCP 5432 (stray debug containers, co-located subcharts, webhook pods that
  # happen to share the namespace when storage co-locates with `lenny-system`) becomes
  # reachable from a compromised operability pod, defeating the containment model. The
  # canonical `lenny.dev/component: pgbouncer` label matches the §13.2 PgBouncer row
  # (NET-047/NET-050); when `{ storage.namespace }` hosts a non-Lenny Postgres proxy
  # (cloud-managed or external), the chart replaces this rule with an `ipBlock` egress
  # entry resolved at render time, mirroring `lenny-backup-job`'s cloud-managed substitution.
  # Namespace selector uses `kubernetes.io/metadata.name` — the immutable label auto-populated
  # by the K8s API server on namespace creation. Matches §13.2 normative guidance (NET-054):
  # custom label keys like `name:` are mutable and not guaranteed, so an attacker with
  # namespace-update rights could apply the key to an attacker-controlled namespace to
  # gain ingress; a legitimate deployer whose storage namespace lacks the custom key would
  # silently match zero namespaces.
  - to:
      - namespaceSelector:
          matchLabels: { kubernetes.io/metadata.name: { storage.namespace } }
        podSelector:
          matchLabels: { lenny.dev/component: pgbouncer }
    ports: [{ protocol: TCP, port: 5432 }]
  # Redis (TLS — plaintext port 6379 disabled per §12.4 / §10.7). Both selectors are
  # required (NET-061); `app: redis` matches the self-managed Redis chart convention
  # (parallels the `app: postgres` / `app: minio` selectors used in `lenny-backup-job`).
  # Cloud-managed Redis (ElastiCache, Memorystore, Azure Cache) causes the chart to
  # substitute an `ipBlock` egress rule resolved at render time.
  - to:
      - namespaceSelector:
          matchLabels: { kubernetes.io/metadata.name: { storage.namespace } }
        podSelector:
          matchLabels: { app: redis }
    ports: [{ protocol: TCP, port: 6380 }]
  # MinIO / S3-compatible (TLS — port follows §13.2 normative MinIO listener per NET-053).
  # Both selectors required (NET-061); `lenny.dev/component: minio` matches the §13.2 MinIO
  # row (NET-047/NET-050). Cloud-managed object storage (S3, GCS, Azure Blob) causes the
  # chart to substitute an `ipBlock` egress rule resolved at render time.
  - to:
      - namespaceSelector:
          matchLabels: { kubernetes.io/metadata.name: { storage.namespace } }
        podSelector:
          matchLabels: { lenny.dev/component: minio }
    ports: [{ protocol: TCP, port: 9443 }]
  # Prometheus. Both selectors required (NET-061); `app: prometheus` matches the standard
  # kube-prometheus-stack / prometheus-community chart pod label (Prometheus is deployer-
  # supplied, not Lenny-rendered, so there is no `lenny.dev/component` key to use). When
  # Prometheus runs as a StatefulSet with custom labels, operators override via the
  # `{{ .Values.monitoring.prometheusPodLabel }}` Helm value (default `app: prometheus`).
  - to:
      - namespaceSelector:
          matchLabels: { kubernetes.io/metadata.name: { monitoring.namespace } }
        podSelector:
          matchLabels: { app: prometheus }
    ports: [{ protocol: TCP, port: 9090 }]
  # K8s API — scoped to the kube-apiserver Service ClusterIP range via ipBlock, matching
  # the §13.2 NET-040 idiom used by every other lenny-system component's kube-apiserver
  # egress rule. An empty `namespaceSelector: {}` (previous shape) matches every namespace
  # in the cluster and would permit TCP 443 egress to any pod listening on 443 — agent,
  # monitoring, tenant, third-party operator namespaces — which defeats the containment
  # model (NET-055). Using `ipBlock` with `{{ .Values.kubeApiServerCIDR }}` scopes the rule
  # to the `kubernetes.default` Service ClusterIP range and works uniformly on self-hosted
  # and managed Kubernetes (GKE/EKS/AKS) where the apiserver is reached via an in-cluster
  # ClusterIP rather than as a labelled pod. `namespaceSelector: {}` is a forbidden idiom
  # in the Lenny chart; the `lenny-preflight` Job rejects any Lenny-rendered NetworkPolicy
  # rule combining an empty `namespaceSelector` with a non-loopback port.
  - to: [{ ipBlock: { cidr: "{{ .Values.kubeApiServerCIDR }}" } }]
    ports: [{ protocol: TCP, port: 443 }]
  # DNS — both `namespaceSelector` (kube-system) AND `podSelector` (canonical
  # CoreDNS label `k8s-app: kube-dns`) are required per NET-067. kube-system
  # hosts many system pods (CoreDNS, metrics-server, kube-proxy, cloud-provider
  # controllers, CSI drivers); a namespace-only selector would permit UDP/53
  # and TCP/53 egress to every one of them (and to any future custom DNS/relay
  # pod an operator co-locates in kube-system). `lenny-preflight` (Section 17.6)
  # rejects any Lenny-rendered NetworkPolicy DNS rule whose peer omits a
  # destination `podSelector`.
  - to:
      - namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: kube-system } }
        podSelector: { matchLabels: { k8s-app: kube-dns } }
    ports: [{ protocol: UDP, port: 53 }, { protocol: TCP, port: 53 }]
  # Webhook delivery (egress to internet) — host filtering enforced at the application layer (SSRF checks).
  # Two parallel `ipBlock` peers are emitted — one per address family — because
  # Kubernetes NetworkPolicy requires every `except` entry to share the address
  # family of its enclosing `cidr`; mixing IPv4 and IPv6 is rejected by strict
  # CNIs and silently drops entries under lenient CNIs (NET-062). The chart
  # partitions `egressCIDRs.excludePrivate` and the cluster pod/service CIDRs
  # by address family at render time.
  # The `except` block carries three categories, rendered from shared Helm values:
  #   1. Cluster-internal CIDRs — `egressCIDRs.excludeClusterPodCIDR` and
  #      `excludeClusterServiceCIDR` (IPv4) plus the v6 variants when set —
  #      mirroring the gateway `allow-gateway-egress-llm-upstream` rule (§13.2)
  #      so that a webhook URL resolving to a cluster pod IP or Service ClusterIP
  #      cannot be used to dial gateway/controller/token-service pods directly
  #      (NET-065). On clusters using CGNAT-range pod CIDRs (`100.64.0.0/10`,
  #      the default on several managed Kubernetes providers) or custom
  #      non-RFC1918 pod CIDRs, `excludePrivate` alone is insufficient — the
  #      cluster CIDRs are outside RFC1918 and would otherwise be reachable.
  #      The cluster-CIDR discovery, preflight validation, and continuous drift
  #      detection are the same mechanisms documented for the `internet` egress
  #      profile under NET-022 in §13.2 (lenny-preflight reads node
  #      `spec.podCIDR` aggregation and the `kubernetes` Service ClusterIP range;
  #      WarmPoolController re-reads every 5 minutes and fires
  #      `NetworkPolicyCIDRDrift`). `lenny-preflight` fails the install if the
  #      discovered cluster pod/service CIDRs are not in the rendered `except`
  #      block of `lenny-ops-egress`.
  #   2. Private/link-local ranges — rendered from the shared
  #      `egressCIDRs.excludePrivate` Helm value (NET-057). Every entry MUST
  #      also appear in the same-family `except` block of the gateway
  #      `allow-gateway-egress-llm-upstream` rule (§13.2). Both surfaces face
  #      the same SSRF threat model (tenant-influenced URLs — webhook targets
  #      here; LLM base URLs, connector callbacks, and interceptor endpoints
  #      on the gateway side) and share one normative private-range block list.
  #      `lenny-preflight` fails the install if any `excludePrivate` entry is
  #      missing from either rule.
  #   3. IMDS addresses — `egressCIDRs.excludeIMDS` (NET-044), mirroring the
  #      gateway rule so that a webhook URL cannot dial cloud instance metadata.
  # The two rules (gateway LLM-upstream and ops-egress webhook) are set-equal
  # on categories (1)-(3): the two `lenny-system` surfaces that initiate
  # outbound HTTPS to tenant-influenced URLs share one SSRF boundary.
  - to: [{ ipBlock: { cidr: 0.0.0.0/0, except: [
        "{{ .Values.egressCIDRs.excludeClusterPodCIDR }}",
        "{{ .Values.egressCIDRs.excludeClusterServiceCIDR }}",
        10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16,
        169.254.169.254/32, 100.100.100.200/32
      ] } }]
    ports: [{ protocol: TCP, port: 443 }]
  - to: [{ ipBlock: { cidr: ::/0, except: [
        # v6 cluster CIDRs rendered only on dual-stack clusters (see §13.2).
        {{- with .Values.egressCIDRs.excludeClusterPodCIDRv6 }} "{{ . }}", {{- end }}
        {{- with .Values.egressCIDRs.excludeClusterServiceCIDRv6 }} "{{ . }}", {{- end }}
        fc00::/7, fe80::/10, fd00:ec2::254/128
      ] } }]
    ports: [{ protocol: TCP, port: 443 }]
```

The internet egress for webhooks excludes cluster pod/service CIDRs, RFC1918, IPv4 link-local, IPv6 ULA, IPv6 link-local, and IMDS addresses at the network layer in addition to the application-layer SSRF checks. The two parallel `ipBlock` peers (one per address family) are required by the Kubernetes NetworkPolicy schema, which constrains every `except` entry to the enclosing `cidr`'s address family; the chart partitions the shared `egressCIDRs.excludePrivate`, `egressCIDRs.excludeIMDS`, and cluster pod/service CIDR values at render time (NET-062). Operators with stricter requirements can replace this rule with a domain-based egress policy (requires CNI support like Cilium or service mesh egress gateway). The `except` block is the same list rendered into the gateway `allow-gateway-egress-llm-upstream` NetworkPolicy (§13.2) via the shared `egressCIDRs.excludePrivate`, `excludeClusterPodCIDR`/`excludeClusterServiceCIDR`, and `excludeIMDS` Helm values — see the §13.2 normative note under `allow-gateway-egress-llm-upstream` for the SSRF symmetry guarantee and the `lenny-preflight` check that enforces it (NET-057, NET-065). The cluster-CIDR exclusion closes an SSRF gap specific to CGNAT-range pod CIDRs (`100.64.0.0/10`, the default on several managed Kubernetes providers) and custom non-RFC1918 pod CIDRs, where `excludePrivate` alone would permit an operability-plane pod — through a tenant-influenced webhook target URL resolving to an in-cluster pod IP — to dial gateway, controller, or token-service pod IPs directly on their service ports (NET-065). Cluster-CIDR discovery and drift detection re-use the NET-022 mechanism documented in §13.2 (preflight reads the cluster's actual pod/service CIDRs; WarmPoolController re-reads every 5 minutes and fires `NetworkPolicyCIDRDrift` on change); the `lenny-preflight` Job extends the NET-022 `except`-block audit to cover the `lenny-ops-egress` webhook rule as well, failing the install if either cluster CIDR is absent from the rendered `except` list.

> **Normative operability-plane selector requirement (NET-061).** Any Lenny-rendered NetworkPolicy that originates from the operability plane (`lenny-ops` Deployment pods, `lenny-backup` Job pods, or any future operability-plane workload) and targets a storage, monitoring, or in-cluster platform-component destination MUST pair a `namespaceSelector` (keyed on the immutable `kubernetes.io/metadata.name` label per NET-054) with a `podSelector` on the destination pod's canonical label (`lenny.dev/component: <component>` for Lenny-rendered platform components per NET-047/NET-050, or the documented `app: <component>` key for storage/monitoring workloads rendered by upstream subcharts — `app: redis`, `app: prometheus` — matching the idiom established by `lenny-backup-job`). A `to:` clause that carries only a `namespaceSelector` permits egress to every pod in the destination namespace on the listed ports: in any deployment where storage or monitoring co-locates with `lenny-system` (including the default single-namespace install), this exposes gateway, token-service, controller, admission-webhook, and CoreDNS pods to the operability plane on Postgres/Redis/MinIO/Prometheus-shaped ports, defeating the containment boundary that §13.2's default-deny plus per-component allow-lists establish. This requirement is the operability-plane analogue of the `lenny-backup-job` two-selector rule (NET-056) and extends the NET-047/NET-050 selector-consistency audit. The `lenny-preflight` Job enforces it via the `ops-egress-selector-parity` check: it enumerates every `to:` clause in `lenny-ops-egress` and `lenny-backup-job` (and any future operability-plane NetworkPolicy registered in the chart's `operabilityEgressPolicies` list) and fails the install/upgrade if a storage/monitoring/platform-component destination clause omits the `podSelector`, if the `podSelector` uses a non-canonical label key for a Lenny-rendered platform component (Exception: the `app:` key is permitted for storage/monitoring destinations rendered by upstream subcharts — `app: redis`, `app: prometheus` — or where the operability plane's own identity is the selector target, per §13.2 line 201), or if the resolved selector matches zero pods for a component expected to be running given the rendered Helm values. The preflight deliberately fails the install rather than warning because a silently over-broad `lenny-ops-egress` rule is strictly more dangerous than a missing one — it grants access that will not be revoked until a future policy edit is deployed.

**Cross-namespace deployments.** When `lenny-system` and `lenny-agents` are separate namespaces (for tenant workload isolation), no NetworkPolicy change is required — `lenny-ops` only talks to gateway and storage, both of which live in `lenny-system`. The agent pods themselves never reach `lenny-ops`.

#### PodDisruptionBudget

```yaml
# Always rendered, regardless of replica count
apiVersion: policy/v1
kind: PodDisruptionBudget
spec:
  minAvailable: 1
  selector: { matchLabels: { app: lenny-ops } }
```

For single-replica deployments, this is effectively a no-op (no eviction is "available" if we only have one). It's rendered anyway so that scaling up requires no chart change. During node drains, the kubelet evicts the pod gracefully; the lease expires (15s by default) and the new pod elected as leader on a different node.

#### Leader Election

`lenny-ops` uses **K8s Lease API** (`coordination.k8s.io/v1`) for leader election. The lease is named `lenny-ops-leader` in `{Release.Namespace}`. Lease parameters (configurable):

- `leaseDurationSeconds: 15`
- `renewDeadlineSeconds: 10`
- `retryPeriodSeconds: 2`

Singleton behaviors gated by leader election:
- Backup scheduling (cron evaluator).
- Webhook delivery (background goroutine per subscription runs only on leader).
- Reconciliation goroutines (escalation flush, idempotency cleanup, lock outage epoch reconciliation, drift snapshot validation).
- `platform_upgrade_check` cron.
- `bundleRules` reconciler.

Non-leader replicas serve **read-heavy** API traffic (audit queries, diagnostics, runbooks) and proxy mutating endpoints to the leader if needed (or accept them locally if the operation is replica-independent — most are).

`kubectl get leases -n lenny-system lenny-ops-leader` shows the current leader's pod identity (`spec.holderIdentity`) for operator visibility.

#### Backup Job NetworkPolicy

A dedicated NetworkPolicy `lenny-backup-job` is rendered for backup Job pods (selected by label `app: lenny-backup`):

```yaml
podSelector: { matchLabels: { app: lenny-backup } }
policyTypes: [Ingress, Egress]
ingress: []  # Jobs accept no incoming traffic
egress:
  # Postgres and MinIO — cross-namespace by default (backup Jobs run in
  # `{{ .Release.Namespace }}`; storage runs in `{{ .Values.storage.namespace }}`).
  # Both `namespaceSelector` and `podSelector` MUST be present: a `to:` clause with
  # only a `podSelector` is interpreted by K8s NetworkPolicy as "same namespace as
  # source pod", which accidentally works when storage co-locates with the Job but
  # silently matches zero pods in any deployment where storage lives elsewhere
  # (NET-056). The `namespaceSelector` uses the immutable `kubernetes.io/metadata.name`
  # key auto-populated by the API server — mirrors `lenny-ops-egress` (NET-054) and
  # §13.2 normative guidance. For cloud-managed data stores (RDS, Cloud SQL, S3, GCS,
  # Azure Blob) or per-region backup endpoints (CMP-045), the chart replaces these
  # rules with `ipBlock: { cidr: "<endpoint-CIDR>" }` entries resolved at render time;
  # `lenny-preflight` validates every configured backup endpoint is covered by the
  # rendered policy.
  - to:
      - namespaceSelector:
          matchLabels: { kubernetes.io/metadata.name: { storage.namespace } }
        podSelector:
          matchLabels: { app: postgres }
    ports: [{ protocol: TCP, port: 5432 }]
  - to:
      - namespaceSelector:
          matchLabels: { kubernetes.io/metadata.name: { storage.namespace } }
        podSelector:
          matchLabels: { app: minio }
    ports: [{ protocol: TCP, port: 9443 }]  # TLS — matches §13.2 MinIO listener (NET-053)
  # K8s API (CRD reads) — ipBlock-scoped to the kube-apiserver Service ClusterIP range
  # per §13.2 NET-040; empty `namespaceSelector: {}` is forbidden (NET-055).
  - to: [{ ipBlock: { cidr: "{{ .Values.kubeApiServerCIDR }}" } }]
    ports: [{ protocol: TCP, port: 443 }]
  # DNS — both UDP/53 and TCP/53 MUST be allowed. Per RFC 7766, resolvers fall
  # back to TCP/53 when a UDP response is truncated (TC bit set); backup Jobs
  # resolve object-storage endpoints (MinIO, S3, GCS, Azure Blob) whose DNS
  # responses frequently exceed 512 bytes (multi-record A/AAAA sets, long CNAME
  # chains). Omitting TCP/53 produces non-deterministic backup failures that
  # correlate with DNS record size rather than backup logic (NET-066). Both
  # `namespaceSelector` (kube-system) AND `podSelector` (canonical CoreDNS
  # label `k8s-app: kube-dns`) are required per NET-067 — a namespace-only
  # selector would permit TCP/53 and UDP/53 egress to every pod in kube-system
  # (metrics-server, kube-proxy, CSI drivers, cloud-provider controllers).
  # This matches the `lenny-ops-egress` DNS rule above and the §13.2 agent-pod
  # DNS rule; `lenny-preflight` (Section 17.6) fails the install if any
  # Lenny-rendered NetworkPolicy DNS rule lists UDP/53 without the TCP/53
  # companion OR omits a destination `podSelector`.
  - to:
      - namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: kube-system } }
        podSelector: { matchLabels: { k8s-app: kube-dns } }
    ports: [{ protocol: UDP, port: 53 }, { protocol: TCP, port: 53 }]
```

Jobs in remote-storage configurations (cloud-managed Postgres, S3-backed object storage, per-region MinIO endpoints outside the cluster) replace the Postgres/MinIO rules above with `ipBlock` egress entries resolved to concrete CIDRs at chart-render time; `lenny-preflight` fails the install if any configured backup endpoint is not covered by the rendered NetworkPolicy.

#### CNI / Service Mesh Compatibility

- **Cilium / Calico:** the rendered NetworkPolicies use only standard `networking.k8s.io/v1` semantics and work on any CNI implementing it.
- **Istio / Linkerd:** `lenny-ops` does not require any service mesh feature. If a mesh is present and intercepts traffic, ensure: (1) the SSE long-lived connections aren't terminated by mesh idle timeouts (set `idleTimeout: 0` or large for the `lenny-ops` virtual service); (2) mTLS isn't broken by the headless Service resolution (Istio's PERMISSIVE mode works; STRICT requires the gateway pods to also be in the mesh); (3) the Ingress is configured to bypass the mesh or terminate at the mesh edge.

#### Air-Gapped Deployments

For clusters without internet access:

- Set `platform.upgradeChannel: ""` to disable automatic upgrade-check polling.
- Mirror Lenny images to a private registry; set `platform.registry.url` and `platform.registry.pullSecretName`.
- Set `platform.registry.requireDigest: true` to enforce digest-pinned image references (defends against tag mutation in the mirror).
- Webhook delivery to internal-only endpoints requires no special config (the SSRF checks reject only private CIDRs by default; configure `ops.webhooks.blockedCIDRs: []` to permit private IPs only if the deployment intentionally targets internal webhooks).
- The release-channel cache (`platform_upgrade_check_cache`) holds the last-known release info; operators can manually populate it via Helm post-install hook.

### Dependencies

| Dependency | Required | Used by |
|---|---|---|
| Postgres | No (degraded) | Audit queries, backup jobs, upgrade state are unavailable. Diagnostics fall back to K8s API. Drift detection works with caller-supplied desired state. Remediation locks and escalations fall back to Redis or in-memory. Event subscriptions served from cache. |
| Redis | No (degraded) | Event stream falls back to gateway in-memory buffer. Remediation locks fall back to in-memory. Escalation creation falls back to in-memory. |
| K8s API | Yes | Diagnostics (pod events, pod state fallback), upgrade orchestration, backup jobs, version introspection |
| Gateway admin API | Yes | Drift reconciliation, diagnostics (pool config, connectors), version aggregation, event buffer fallback |
| Prometheus (or compatible) | Required at Tier 2/3; optional at Tier 1 | Cross-replica health aggregation, capacity recommendations, diagnostics (pool bottleneck analysis), bundled alerting rules (Section 25.13). Has fallback paths for transient outages, but several features are functionally broken without persistent time-series storage — see Prometheus Requirement below. |
| MinIO | No (degraded) | Backup verification only |

### Prometheus Requirement

`lenny-ops` lists Prometheus as a tier-dependent requirement: optional at Tier 1, required at Tier 2/3. This subsection explains the reasoning, the BYO model, and the documented fallback behavior for operators who choose to deploy without it.

#### Why required at Tier 2/3

`lenny-ops` has fallback paths for short Prometheus outages (per-replica fan-out via headless Service for health and recommendations, gateway `/metrics` scrape for diagnostics — see Section 25.4, Metrics Source). These fallbacks let the platform survive a transient Prometheus failure. They do not substitute for a permanently absent Prometheus, because several features depend on persistent time-series storage:

- **Capacity recommendations.** Many rules use multi-day sliding windows ("pool exhausted 3+ times in 24h", "credential utilization > 70% over 7d"). These require historical data. Per-replica in-memory ring buffers reset on every gateway restart and capture only ~1/N of total traffic per replica. Without Prometheus, recommendations return `confidence: 0.0` for hours after any restart and are based on partial samples between restarts.
- **Cross-replica health aggregation.** The fan-out fallback works for "what is firing right now" but cannot evaluate alert rules with `for: "15m"` clauses correctly — those need historical time-series, not point-in-time samples.
- **Historical diagnostics.** Investigating a session or pool issue from two hours ago requires metric values from that time. The gateway's in-process registry only holds current values; without Prometheus, the data is gone.
- **Bundled alerting rules (Section 25.13).** The rules are useless without something to load them into. If no Prometheus is present, the rules are rendered into Helm manifests but never evaluated. Human operators lose alerting entirely.

For these reasons, deploying `lenny-ops` at Tier 2/3 without Prometheus is technically supported but **strongly discouraged**: agents will receive degraded responses, recommendations will be unreliable, and human operators will not receive alerts.

#### Why optional at Tier 1

In dev (Tier 1), the data volume is low enough that per-replica samples are representative, the operational time horizon is short enough that loss of historical data is irrelevant, and dev clusters typically have neither on-call rotations nor alert routing. `lenny-ops` operating in degraded mode is acceptable for development and exploratory use. Tier 1 deployers who want full functionality can enable the optional Prometheus container in the dev compose file.

#### BYO model

Lenny does not deploy Prometheus. Operators provide their own Prometheus-HTTP-API-compatible endpoint via the `ops.prometheus.url` Helm value. Any backend implementing the Prometheus query API works:

- Self-hosted Prometheus, vanilla or via prometheus-operator / kube-prometheus-stack.
- Long-term storage backends: Mimir, Cortex, Thanos.
- Alternative time-series databases with Prometheus compatibility: Victoria Metrics, M3DB.
- Managed services: Amazon Managed Prometheus, Grafana Cloud, Google Cloud Managed Service for Prometheus.

The same NetworkPolicy that allows the operator's Prometheus to scrape Lenny components (rendered by the chart per Section 16 of `SPEC.md`) also allows `lenny-ops` egress to the configured Prometheus URL.

#### Preflight validation

`lenny-preflight` (Section 17.6 of `SPEC.md`) checks for a reachable Prometheus endpoint at the configured URL. The check is **non-blocking** by design — operators may have legitimate reasons to deploy without Prometheus temporarily (initial setup, chart testing, staged rollouts) and a hard install gate would be obstructive. The check emits a tier-specific message:

| Tier | Message | Severity |
|------|---------|----------|
| 1 | "Prometheus not configured. `lenny-ops` will operate in degraded mode. This is acceptable for development." | INFO |
| 2/3 | "Prometheus not configured at `{url}`. Several `lenny-ops` features (capacity recommendations, historical diagnostics, alerting) require persistent time-series storage. Configuring a Prometheus-compatible endpoint is strongly recommended for production deployments." | WARN |

Tier 2/3 deployers who intentionally run without Prometheus must set `monitoring.acknowledgeNoPrometheus: true` in Helm values to suppress the warning. The Helm value name is intentionally explicit — operators have to think about what they're acknowledging.

#### Operational consequences summary

| Capability | With Prometheus | Without Prometheus (transient) | Without Prometheus (permanent) |
|---|---|---|---|
| Cross-replica health aggregation | Aggregate alert state from `GET /api/v1/alerts` | Fan-out via headless Service, worst-of merge | Same as transient — works, but `for: "15m"` alerts misfire because no history exists |
| Capacity recommendations | Aggregate metrics from PromQL, full window data | Fan-out, highest-confidence merge | `confidence: 0.0` after every restart; ring buffers only have ~1/N replica's worth of recent data |
| Pool bottleneck diagnostics | Range queries against historical data | Per-replica `/metrics` scrape, point-in-time only | Same as transient — works, but no trend analysis |
| Bundled alerting rules → human alerts | Loaded by Prometheus, fired via Alertmanager | Same (rules already loaded) | Not loaded; humans receive no alerts |
| Historical session/pool investigation | Range queries against historical data | Falls back to in-process metric values, current-only | No historical data available |

The "transient" column is acceptable for short outages (minutes to a few hours). The "permanent" column is the steady state of running without Prometheus — this is what production operators are signing up for if they skip it.

### Storage Routing

`lenny-ops` uses the platform's `StoreRouter` (Section 12.6) to access Postgres and Redis. The routing rules:

**Postgres:**

| Data | StoreRouter method | Rationale |
|------|-------------------|-----------|
| Ops-specific tables (`ops_remediation_locks`, `ops_lock_epoch`, `ops_lock_conflicts`, `ops_idempotency_keys`, `ops_escalations`, `ops_backups`, `ops_backup_schedule`, `ops_retention_policy`, `ops_restore_state`, `ops_event_subscriptions`, `ops_event_deliveries`, `platform_upgrade_state`, `platform_upgrade_check_cache`, `bootstrap_seed_snapshot`, `audit_log_deferred_writes`) | `PlatformPostgres()` | Platform-scoped, not per-tenant or per-session. Low volume. Must be reachable without a tenant or session ID. |
| Session diagnostics | `SessionShard(sessionID)` | Reads `sessions` and `agent_pod_state` from the shard that owns the session. |
| Audit queries | `AuditShard(tenantID)` | Per-tenant for filtered queries. Platform-admin cross-tenant queries use `AllAuditShards()` for scatter-gather. |
| Platform-tenant audit events referencing a non-platform `target_tenant_id` (`security.audit_write_rejected`, `admin.impersonation_*`, `gdpr.legal_hold_overridden_tenant`, `legal_hold.escrow_region_resolved`, `legal_hold.escrowed`, `legal_hold.escrow_released`, `compliance.profile_decommissioned`, `DataResidencyViolationAttempt` with `operation: "platform_audit_write"`) | `PlatformPostgres(region)` when the target tenant's `dataResidencyRegion` is set; falls back to `PlatformPostgres()` when unset | CMP-058, fail-closed. The *fact* of a platform-tenant event referencing a regulated target tenant is itself personal data describing that tenant; it must reside in the target's jurisdiction. Unresolvable region (target `dataResidencyRegion` set but `storage.regions.<region>.postgresEndpoint` missing or unreachable) raises `PLATFORM_AUDIT_REGION_UNRESOLVABLE` (HTTP 422, `PERMANENT`) — a mirror of `BACKUP_REGION_UNRESOLVABLE` and `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`. See [§11.7](11_policy-and-controls.md#117-audit-logging) "Platform-tenant audit event residency". |
| Backup (`pg_dump`) | All shards via `AllSessionShards()` + `PlatformPostgres()` (single-region) or `PlatformPostgres(region)` per region (multi-region; see Backup pipeline residency in [§12.8](12_storage-architecture.md#128-compliance-interfaces)) | Full backups dump every shard. |

`PlatformPostgres()` is a `StoreRouter` method (analogous to `PlatformRedis()`) that returns the connection pool for platform-global tables that are not owned by any tenant or session. In v1 (`SingleShardRouter`), it returns the same pool as all other methods. In multi-shard deployments, it routes to the dedicated platform database instance. Adding this method requires the change listed in "Edits Required Outside Section 25".

`PlatformPostgres(region)` (CMP-058) is the region-scoped variant introduced for platform-tenant audit events that reference a non-platform `target_tenant_id` with a set `dataResidencyRegion` — see [§11.7](11_policy-and-controls.md#117-audit-logging) "Platform-tenant audit event residency". It resolves the target's region against the same `storage.regions.<region>.postgresEndpoint` map used by `REGION_CONSTRAINT_UNRESOLVABLE` at runtime, `BACKUP_REGION_UNRESOLVABLE` at backup time, and `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` at tenant-delete time; an unresolvable target region raises `PLATFORM_AUDIT_REGION_UNRESOLVABLE` fail-closed. In v1 single-region deployments (no `storage.regions` map configured), `PlatformPostgres(region)` returns the same pool as `PlatformPostgres()` for every region argument — the fail-closed gate is a no-op because there is no residency constraint to violate.

**Redis:**

| Data | StoreRouter method | Rationale |
|------|-------------------|-----------|
| Event stream (`ops:events:stream`) | `PlatformRedis()` | Platform-scoped event stream, not tenant-routed. |
| Remediation locks (`ops:lock:{scope}`) | `PlatformRedis()` | Platform-scoped coordination keys. |
| Escalation buffer (`ops:escalations:{id}`) | `PlatformRedis()` | Platform-scoped operational records. |
| Webhook cursor tracking | `PlatformRedis()` | Platform-scoped delivery state. |

All ops Redis keys use the `ops:` prefix to avoid collisions with tenant-scoped keys on the same instance (relevant in v1 where `PlatformRedis()` and `RedisShard()` return the same client).

### Multi-Replica Lenny-Ops Scaling

`lenny-ops` defaults to a single replica with leader election. Most deployments — including all Tier 1/2 deployments and most Tier 3 deployments — should keep this default. This subsection documents when and how to scale beyond one replica, and the trade-offs involved.

#### When to Scale

Scale `lenny-ops` to multiple replicas when:

- **Read query load is high.** Audit queries, diagnostics, drift reports, and event-stream connections from many concurrent agents create CPU/memory pressure on a single replica. Indicator: sustained CPU > 70% on the `lenny-ops` pod, or `lenny_ops_rate_limited_total` consistently incrementing for read endpoints.
- **Many concurrent SSE connections.** Each SSE connection is a long-lived goroutine. With > ~500 concurrent connections, a single replica can become memory-pressured. Indicator: `lenny_ops_events_sse_active_connections` > 500.
- **High webhook subscription count.** Each subscription spawns a delivery goroutine on the leader. With > ~200 subscriptions and high event throughput, the leader's webhook delivery pipeline can become a bottleneck. Indicator: `lenny_ops_webhook_backlog` > 100 sustained.

Do **not** scale `lenny-ops` for write throughput. Most mutations are leader-only (only one replica owns webhook delivery, backup scheduling, upgrade orchestration). Adding replicas adds read capacity but doesn't help mutating-endpoint throughput.

#### Recommended Scale by Tier

| Tier | Default | Maximum recommended | Notes |
|------|---------|--------------------|-------|
| 1 (dev) | 1 | 1 | No reason to scale; degraded modes are acceptable. |
| 2 (staging) | 1 | 2 | Scale for read load testing; otherwise 1. |
| 3 (production) | 1 | 3 | Scale when read load justifies. Beyond 3, gains diminish (most work is leader-only). |

#### Trade-Offs

Scaling beyond 1 replica enables read throughput at the cost of:

- **In-memory lock tier becomes per-replica.** When both Postgres and Redis are unreachable, the in-memory lock fallback (Section 25.4 Remediation Coordination) provides no cross-replica coordination. Default behavior in multi-replica mode (`ops.locks.memoryTier: "single-replica-only"`) **rejects** in-memory locks during dual-storage outages, returning `503 REMEDIATION_LOCK_NO_COORDINATION`. This is safer than silently allowing split-brain but means agents have no lock coordination during dual outages.
- **In-memory escalation buffer becomes per-replica.** Tier-3 escalations are isolated to whichever replica accepted them. The reconciliation goroutine on the leader replica only flushes its own buffer. Operators concerned about this can set `ops.escalation.requireDurable: true` to fail-fast instead of accepting in-memory escalations in multi-replica deployments.
- **In-memory event buffer (lenny-ops's own) is per-replica.** During dual gateway-and-Redis outages, only the events that landed on each replica's local buffer are accessible to consumers connected to that replica. SSE clients with `sessionAffinity: ClientIP` (the default) stay on one replica through reconnects, mitigating this for individual clients.
- **PDB and rollout behavior change.** With multiple replicas, rolling updates can use `maxUnavailable > 0`, but PodDisruptionBudget (`minAvailable: 1`) ensures at least one replica is up.

#### Operator Decision Checklist

When considering multi-replica `lenny-ops`:

1. Is the read load actually high enough to justify? (Check the indicators above.)
2. Are Postgres and Redis HA-deployed? (Multi-replica `lenny-ops` only helps if storage is also HA.)
3. Is `ops.locks.memoryTier` set appropriately for the safety profile? (Default `"single-replica-only"` is recommended for multi-replica.)
4. Does the deployment topology have node anti-affinity? (Multi-replica on the same node provides no failure tolerance.)

For most deployments, the answer to question 1 is "no" and a single-replica deployment with HA storage gives the best operational profile. Scale up only when monitoring data drives the decision.

### Startup and Readiness

`lenny-ops` starts its HTTP listener immediately. Individual endpoint handlers report dependency status rather than crashing — same philosophy as the gateway health endpoint. Readiness probe: `GET /healthz` returns 200 when the Postgres connection is established or when the K8s API is reachable (either dependency suffices for readiness, since most endpoints can operate in degraded mode without Postgres).

### Authentication

Same OIDC middleware as the gateway admin API. `lenny-ops` validates JWT tokens using the same OIDC issuer and JWKS endpoint. Requires `platform-admin` or `tenant-admin` role on all endpoints. No anonymous access except `/healthz`.

Callers are also subject to the optional `scope` JWT claim (Section 25.1, Scoped Tokens; RFC 9068). When present, the claim narrows the caller's effective tool surface below the role ceiling — a `platform-admin` token with `"scope": "tools:pool:* tools:health:read"` can call only pool tools and health reads despite its role theoretically permitting more.

### Caller Identity and Capability Discovery

An agent arriving with a valid token has no built-in way to know what it can do — role, tenant scope, rate-limit budget, effective tool surface, platform capabilities — without making trial calls and observing 403s. `lenny-ops` exposes a single discovery endpoint that returns this context in one call.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/me` | Identity, authorization, rate-limit state, token lifecycle, platform context, feature flags, and tool-surface summary for the calling identity. |
| `GET` | `/v1/admin/me/authorized-tools` | The MCP tool inventory pre-filtered to tools the caller can actually invoke (RBAC + `scope` claim applied, conditional guards annotated). |
| `GET` | `/v1/admin/me/operations` | The caller's in-flight operations — alias for `GET /v1/admin/operations?actor=me&status=in_progress,paused,held,awaiting_flush` (Section 25.4, Operations Inventory). |

#### `GET /v1/admin/me` Response

```json
{
  "identity": {
    "sub": "sa-prod-watchdog-01",
    "displayName": "prod-watchdog-us-east-1",
    "callerType": "agent",
    "issuer": "https://auth.example.com",
    "authenticatedAt": "2026-04-16T10:22:03Z"
  },

  "authorization": {
    "roles": ["platform-admin"],
    "tenantScope": "*",
    "scope": "tools:*",
    "authorizedLockScopes": [
      "pool:*", "credential-pool:*", "session:*",
      "tenant:*:*", "platform:*", "upgrade:platform",
      "restore:platform", "config:global"
    ],
    "subjectToGuards": {
      "confirmRequiredFor": ["pool-scale-over-1.5x", "full-backup", "restore", "upgrade-start"],
      "acknowledgeDataLossRequiredFor": ["restore-with-recent-writes"]
    }
  },

  "rateLimits": {
    "requestsPerSecond": 20,
    "burst": 50,
    "currentTokensAvailable": 48,
    "windowResetAt": "2026-04-16T10:22:13Z"
  },

  "token": {
    "expiresAt": "2026-04-16T11:00:00Z",
    "refreshBeforeExpiry": "60s",
    "refreshEndpoint": "https://auth.example.com/token"
  },

  "platform": {
    "installationId": "inst-a1b2c3",
    "version": "1.5.0",
    "tier": "tier2",
    "namespace": "lenny-system",
    "opsServiceURL": "https://ops.lenny.example.com",
    "gatewayURL": "https://lenny-gateway:8443"
  },

  "capabilities": {
    "prometheusAvailable": true,
    "bundledRulesLoaded": true,
    "opsReplicas": 1,
    "mtlsInternal": false,
    "lockMemoryTier": "single-replica-only",
    "tenantFiltering": true,
    "mcpManagementServer": true,
    "openApiAvailable": true,
    "headlessServiceFallback": true
  },

  "links": {
    "authorizedTools": "/v1/admin/me/authorized-tools",
    "myOperations": "/v1/admin/me/operations",
    "myRecentAudit": "/v1/admin/audit-events?actorId=sa-prod-watchdog-01&limit=50",
    "platformHealth": "/v1/admin/health/summary",
    "openApi": "/v1/openapi.json"
  }
}
```

Field notes:

- **`authorization.scope`** — the RFC 9068 `scope` JWT claim echoed back as a space-separated string. `"tools:*"` means no additional restriction beyond role. A narrower value restricts the caller's effective tool surface (see Section 25.1 Scoped Tokens and Section 25.12).
- **`authorization.authorizedLockScopes`** — pre-computed from the scope-pattern rules in Section 25.4 Remediation Coordination. Tenant-admin callers see tenant-specific expansions (`tenant:t-12345:*`) rather than wildcards.
- **`authorization.subjectToGuards`** — surfaces the conditional-requirement rules from tool `x-lenny-guards` extensions (Section 25.12). Agents learn upfront which operations will require `confirm` / `acknowledgeDataLoss` without first encountering the relevant error.
- **`rateLimits.currentTokensAvailable`** — current token-bucket balance for this caller's rate-limit bucket. Agents can self-pace precisely. The value is instantaneous and refills at `requestsPerSecond`.
- **`platform.installationId`** — stable UUID for this Lenny installation. Useful for multi-cluster agents that track state per installation.
- **`capabilities`** — reflects the **actual** install state, not compiled feature flags. `prometheusAvailable: false` tells an agent not to rely on Prometheus-backed aggregation in this deployment. `lockMemoryTier` tells an agent the safety profile of Tier-3 lock acquisition.
- **`links`** — discovery hop-off. A fresh agent typically follows these in order: `authorizedTools` for its callable surface, `myOperations` for in-flight work, `platformHealth` for current platform state.

Responses are cheap to compute (all data is in-process or cached). Clients may cache `/me` for the lifetime of a logical task; re-fetch on token refresh or on any `401`/`403`.

#### Authorization

Any authenticated caller may call `/v1/admin/me` and its sub-endpoints — they return only the calling identity's context, never another identity's. `tenant-admin` callers receive tenant-scoped values (their tenant ID populating wildcards, not the full platform surface).

#### Degradation

If Postgres is unreachable, `/v1/admin/me` still returns — `identity`, `authorization`, `rateLimits`, `token`, `platform`, `capabilities` are all derivable from the authenticated request alone or from `lenny-ops` process state. Only `links.myRecentAudit` is degraded (audit is Postgres-backed); the canonical `degradation` envelope (Section 25.2) is populated accordingly.

If the MCP Management Server is unreachable, `/v1/admin/me/authorized-tools` returns `503 AUTHORIZED_TOOLS_UNAVAILABLE` with a suggestion to use the OpenAPI spec at `/v1/openapi.json` plus the caller's `authorization` block to derive the tool surface locally.

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_me_requests_total` | Counter | `caller_type` | Calls to `/v1/admin/me` and sub-endpoints. |

#### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `AUTHORIZED_TOOLS_UNAVAILABLE` | `TRANSIENT` | 503 | MCP Management Server unreachable; derive the tool surface from OpenAPI + `/me.authorization`. |

#### Audit Events

`identity.discovered` — emitted on first `/v1/admin/me` call per token (deduplicated by `(sub, token_iat)`). Records the caller's identity, roles, and scopes at discovery time.

### Operations Inventory

Long-running actions — platform upgrades, restores, backups, backup verifications, held locks, buffered escalations, in-flight idempotency keys, drift reconciliations, webhook delivery backlogs — are each owned by a different subsystem with its own status endpoint. An agent that needs "what is in flight?" must otherwise query every subsystem individually and merge the results. The Operations Inventory endpoint provides a unified, filterable view.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/operations` | Paginated list of operations across all subsystems. Filters: `?actor=`, `?status=`, `?kind=`, `?since=`, `?until=`, `?tenantId=`, `?operationId=`, `?limit=`, `?cursor=`. |
| `GET` | `/v1/admin/operations/{id}` | Single operation with full detail (same schema as a list entry). |

Read-only. Mutations still go to the owning subsystem (`POST /v1/admin/platform/upgrade/proceed`, `POST /v1/admin/restore/resume`, `DELETE /v1/admin/remediation-locks/{id}`, etc.). The Operations Inventory surfaces the `resources` links on each operation so an agent can reach the mutating endpoints without cross-referencing sections.

#### Operation Kinds

| `kind` | Source table / state | Typical duration |
|---|---|---|
| `platform_upgrade` | `platform_upgrade_state` singleton | Minutes–hours |
| `restore` | `ops_restore_state` | Minutes |
| `backup` | `ops_backups` where `status IN ('running', 'verifying')` | Minutes |
| `backup_verification` | `ops_backups` where `status='verifying'` | Minutes |
| `escalation_open` | `ops_escalations` where `status='open'` | Indefinite |
| `escalation_buffered` | escalations at `persistence='buffered-redis'` or `'buffered-memory'` awaiting flush | Seconds–minutes |
| `remediation_lock` | `ops_remediation_locks` non-expired rows | Minutes (TTL-bounded) |
| `idempotency_in_progress` | `ops_idempotency_keys` where `status='in_progress'` | Seconds–minutes |
| `drift_reconciliation` | in-flight `POST /v1/admin/drift/reconcile` | Seconds–minutes |
| `webhook_delivery_pending` | `ops_event_deliveries` where `status='pending'` | Seconds |

#### Response

```json
{
  "operations": [
    {
      "operationId": "upgrade-550e8400-e29b-41d4-a716-446655440000",
      "kind": "platform_upgrade",
      "status": "paused",
      "startedBy": "sa-deploy-01",
      "startedAt": "2026-04-16T09:12:00Z",
      "timeoutAt": "2026-04-16T10:12:00Z",
      "progress": {
        "percent": 65,
        "completedSteps": 5,
        "totalSteps": 7,
        "currentStep": "GatewayRoll",
        "currentStepDetail": "Waiting for operator to call /upgrade/proceed",
        "etaSeconds": null,
        "etaConfidence": 0.0,
        "etaMethod": "none",
        "lastProgressAt": "2026-04-16T09:58:12Z",
        "stalledForSeconds": null
      },
      "correlatedOperations": [
        { "operationId": "lock-restore-platform", "kind": "remediation_lock", "scope": "upgrade:platform" }
      ],
      "resources": {
        "status":   "GET /v1/admin/platform/upgrade/status",
        "proceed":  "POST /v1/admin/platform/upgrade/proceed",
        "pause":    "POST /v1/admin/platform/upgrade/pause",
        "rollback": "POST /v1/admin/platform/upgrade/rollback",
        "audit":    "GET /v1/admin/audit-events?operationId=upgrade-550e8400-e29b-41d4-a716-446655440000"
      },
      "cancellable": true,
      "metadata": { "targetVersion": "1.6.0", "previousVersion": "1.5.0" }
    },
    {
      "operationId": "lock-7c9e6679-7425-40de-944b-e07fc1f90ae7",
      "kind": "remediation_lock",
      "status": "held",
      "startedBy": "sa-prod-watchdog-01",
      "startedAt": "2026-04-16T10:15:00Z",
      "timeoutAt": "2026-04-16T10:20:00Z",
      "progress": null,
      "correlatedOperations": [],
      "resources": {
        "get":     "GET /v1/admin/remediation-locks/lock-7c9e6679-7425-40de-944b-e07fc1f90ae7",
        "extend":  "PATCH /v1/admin/remediation-locks/lock-7c9e6679-7425-40de-944b-e07fc1f90ae7",
        "release": "DELETE /v1/admin/remediation-locks/lock-7c9e6679-7425-40de-944b-e07fc1f90ae7",
        "steal":   "POST /v1/admin/remediation-locks/lock-7c9e6679-7425-40de-944b-e07fc1f90ae7/steal"
      },
      "cancellable": true,
      "metadata": { "scope": "pool:default-gvisor", "operation": "scale" }
    }
  ],
  "pagination": { "cursor": "...", "hasMore": false, "cursorKind": "pk" }
}
```

#### Canonical `operationId` Format

Operation IDs are a concatenation of the kind prefix and the subsystem's natural key:

```
operationId := <kind-prefix>-<natural-key>
```

Prefixes: `upgrade`, `restore`, `backup`, `esc`, `lock`, `idemp`, `drift-rec`, `delivery`. The natural key is the subsystem's existing ID (lock ID, restore ID, backup ID, escalation ID, or `X-Lenny-Operation-ID` for the singleton upgrade). Examples: `upgrade-550e8400-...`, `lock-abc`, `restore-xyz`, `esc-def`.

This form is stable across `lenny-ops` restarts (IDs come from Postgres or Redis, not process memory) and decodable (an agent reading `lock-abc` knows to call `/v1/admin/remediation-locks/abc`).

#### Filters

| Parameter | Meaning | Default |
|-----------|---------|---------|
| `actor` | `me` (calling identity), a specific `sub` (`platform-admin` only), or `*` (all callers; `platform-admin` only). Tenant-admin is auto-restricted to `me`. | `me` |
| `status` | CSV of operation statuses. Values: `in_progress`, `paused`, `held`, `awaiting_flush`, `failed`, `completed`, `all`. | `in_progress,paused,held,awaiting_flush` |
| `kind` | CSV of operation kinds (from the table above), or `all`. | `all` |
| `since`, `until` | RFC 3339 timestamps. Operation's `startedAt` falls in this window. | — |
| `operationId` | Lookup by ID. Returns a single-element list if found. | — |
| `tenantId` | Restrict to operations associated with a tenant. `platform-admin` only; `tenant-admin` is auto-restricted to its own tenant. | — |
| `limit` | Page size. Default 100, max 500. | 100 |
| `cursor` | Canonical pagination (Section 25.2). | — |

#### Status Values

- **`in_progress`** — actively executing (upgrade advancing, restore writing a shard, backup Job running, drift reconciliation in flight, idempotency key in progress, webhook delivery pending).
- **`paused`** — awaiting an explicit advance (e.g., `upgrade/proceed`). Upgrade-only today.
- **`held`** — remediation locks currently held.
- **`awaiting_flush`** — escalations buffered in Redis or memory awaiting reconciliation to Postgres.
- **`failed`** — terminal-failure operations (failed restore, failed upgrade). Included in the inventory because they require operator resolution (e.g., restore/resume, upgrade/rollback).
- **`completed`** — terminal-success operations. Usually excluded from the default filter; use `?status=completed&since=1h` for recent-history queries.

#### Event Subscription

`lenny-ops` emits `operation_progressed` events (Section 25.5 event types) on every operation state transition. Payload: `operationId`, `kind`, `prevStatus`, `newStatus`, `progress`. Agents subscribed to the event stream with `?eventType=operation_progressed` receive real-time updates without polling the inventory.

#### Implementation

`/v1/admin/operations` is a scatter-gather read — `lenny-ops` queries each owning subsystem's table (or, for in-memory state, its own process state) and assembles the response. No new storage; the inventory is a **view** over existing state.

#### Degradation

If a subsystem's backing store is unavailable (e.g., Postgres down for upgrade/restore state, Redis down for Tier-2 buffered escalations), the operations from that subsystem are omitted from the response and listed in the canonical `degradation.warnings`:

```json
"degradation": {
  "level": "degraded",
  "warnings": [
    "Operations of kind 'platform_upgrade', 'restore', 'backup', 'escalation_open' omitted: Postgres unreachable.",
    "Operations of kind 'escalation_buffered' omitted: Redis unreachable."
  ]
}
```

In-memory-only operations (Tier-3 escalations, in-memory locks on the calling replica) are always included because they don't depend on external stores.

#### Authorization

- `tenant-admin` sees only operations where (a) `started_by` is themselves, OR (b) the operation carries a `tenantId` field AND its value matches the caller's tenant. Platform-scoped operations (no `tenantId` field — `platform_upgrade`, platform-level `restore`/`backup`, drift reconciliation, etc.) are visible **only** when `started_by` matches the caller; a tenant-admin never sees platform-scoped operations started by other principals. This mirrors the event-subscription semantics for platform-scoped events (Section 25.5).
- `platform-admin` sees all operations.
- `actor=*` requires `platform-admin`.

Every operation row includes a `resources` block. The URLs in `resources` require the same authorization as their target endpoint — surfacing the URL does not grant access.

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_operations_inventory_requests_total` | Counter | `actor_kind` (`self`, `other`, `all`) | Calls to the inventory endpoint. |
| `lenny_ops_operations_inventory_kinds_returned` | Histogram | | Distribution of `kind` values returned per request (observability for how balanced the workload is). |

#### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `OPERATION_NOT_FOUND` | `PERMANENT` | 404 | `operationId` not found in any subsystem. |
| `OPERATIONS_INVENTORY_PARTIAL` | `TRANSIENT` | 207 | At least one subsystem's backing store was unreachable; response is partial per `degradation.warnings`. |

#### Audit Events

`operations.inventory_queried` — records query parameters and result count. Not per-operation-returned (to avoid audit log explosion under frequent polling).

### Calling the Gateway

`lenny-ops` calls the gateway's admin API as a regular authenticated HTTPS client. It uses a dedicated service account (`lenny-ops-sa`) with `platform-admin` role. The service account's JWT token includes `caller_type: "service"` for audit trail distinction.

All calls go through the gateway's standard RBAC, validation, and audit — no backdoor, no loopback shortcut.

```go
// pkg/ops/gateway/client.go

type GatewayClient struct {
    baseURL          string            // ClusterIP Service: https://lenny-gateway:8443 by default
                                       // (NET-070). Rendered as http://lenny-gateway:8080 only when
                                       // ops.tls.internalEnabled=false AND
                                       // ops.acknowledgePlaintextAdminAPI=true (or dev mode).
    httpClient       *http.Client      // configured with the cluster trust bundle plus any
                                       // deployer-supplied CA via ops.tls.caBundleConfigMap;
                                       // emits lenny_ops_admin_api_tls_handshake_total{result}
                                       // on every connection attempt (§16.1).
    tokenSrc         auth.TokenSource  // OIDC token refresh
    replicaDiscovery ReplicaDiscovery  // headless Service for per-replica queries
}

// Shared-state calls — routed through ClusterIP, any replica gives the same answer
func (c *GatewayClient) GetPoolConfig(ctx context.Context, pool string) (*PoolConfig, error)
func (c *GatewayClient) ScalePool(ctx context.Context, pool string, body ScaleRequest) error
func (c *GatewayClient) ListConnectors(ctx context.Context) ([]Connector, error)
// ... one method per admin API endpoint used by ops features

// Per-replica calls — fan out to all replicas via headless Service
func (c *GatewayClient) QueryAllEventBuffers(ctx context.Context, since uint64, filter EventFilter) ([]BufferedEvent, error)
func (c *GatewayClient) GetHealthFromAllReplicas(ctx context.Context) ([]*AggregateHealthResponse, error)
func (c *GatewayClient) GetRecommendationsFromAllReplicas(ctx context.Context, category *string) ([]*RecommendationsResponse, error)
```

`GatewayClient` uses two routing modes:

- **ClusterIP calls** (pool config, connectors, scaling, drift reconciliation, etc.) route through the ClusterIP Service (`lenny-gateway`). Any replica can serve these — they read from shared state (Postgres, Redis) and return identical results regardless of which replica handles the request.
- **Per-replica calls** (event buffers, health, recommendations) use `ReplicaDiscovery` to resolve all gateway pod IPs via the headless Service (`lenny-gateway-pods`) and query each replica individually. These endpoints read in-process state that varies across replicas — per-replica metrics, ring buffers, and event buffers.

Note: `lenny-ops` does not call `GetHealthFromAllReplicas` or `GetRecommendationsFromAllReplicas` directly in the normal case — it uses Prometheus as the primary aggregation source (see Metrics Source below). The per-replica fan-out methods are the fallback when Prometheus is unavailable.

```go
// pkg/ops/gateway/discovery.go

type ReplicaDiscovery interface {
    // Endpoints returns the current set of gateway pod IPs.
    // Implementation: DNS SRV lookup on the headless Service.
    Endpoints(ctx context.Context) ([]string, error)
}
```

### Metrics Source and Cross-Replica Aggregation

Prometheus scrapes every gateway replica's `/metrics` endpoint. It is the natural aggregation layer for any metric or alerting rule that varies across replicas. `lenny-ops` uses Prometheus as the primary source for three categories of cross-replica data, with per-replica fan-out as the fallback when Prometheus is unavailable.

```go
// pkg/ops/metrics/source.go

type MetricSource interface {
    Query(ctx context.Context, query string) (float64, error)
    QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error)
}

type PrometheusWithFallback struct {
    prometheus *PrometheusClient
    gateway    *GatewayClient  // per-replica fan-out for fallback
    cache      *FallbackCache  // short-TTL cache to amortize fan-out cost
}
```

#### "Prometheus Unreachable" Definition

Prometheus is considered unreachable when any of the following holds for longer than `ops.prometheus.unreachableThreshold` (default 10s):

- HTTP connection failure (DNS resolution failure, TCP refused, TLS error).
- HTTP 5xx response.
- Query timeout at `ops.prometheus.queryTimeoutSeconds` (default 15s).
- Prometheus's own readiness endpoint (`/-/ready`) returns non-200.

Slow-but-responsive Prometheus (queries taking 5-15 seconds) is **not** treated as unreachable — `lenny-ops` waits for the query to complete. A query exceeding the timeout triggers the unreachable state; `lenny-ops` logs and emits `prometheus_query_timeout` event with the query string. Consistently slow queries should be caught by operators via a `PrometheusQueryLatencyHigh` alert (bundled, Section 25.13).

The metric `lenny_prometheus_query_duration_seconds` (histogram, labels: `kind` ∈ `{alerts, range, instant}`) tracks query latency; alerting on its p95 >= 10s gives operators early warning of degradation.

#### Fallback Caching

Per-replica fan-out is expensive: N HTTP calls per query, hitting gateway replicas that are already strained when Prometheus is down. `lenny-ops` mitigates this with aggressive caching during fallback:

- **Fan-out results cached for 10s (health) or 30s (recommendations).** Repeated queries within the cache window return the cached aggregate without new fan-out calls. Cache TTLs are tuned shorter than typical poll intervals so agents observe reasonably fresh data.
- **Per-replica circuit breakers.** If a gateway replica fails 3 consecutive fan-out requests (default, configurable via `ops.gateway.fanOutCircuitBreaker.failureThreshold`), that replica is skipped for the next 60 seconds (`resetAfter`). This prevents one struggling replica from slowing every aggregation.
- **Timeout per replica:** `ops.gateway.fanOutTimeoutSeconds` (default 2). A replica not responding within this window is skipped for the current aggregation (and potentially tripped by the circuit breaker).
- **Partial aggregation.** If the fan-out loop returns only a subset of replicas (due to timeouts or circuit breakers), the aggregation proceeds with the available data. The response's `degradation` envelope (Section 25.2) includes `"warnings": ["Aggregation based on 4 of 5 replicas — 1 replica unavailable"]`.

These protections change the fan-out cost during outages from "amplifying N×M HTTP load" to "roughly M queries per agent at the cache-refresh cadence," which is tractable even at Tier 3.

#### Health Aggregation

The gateway health endpoint evaluates threshold expressions against its in-process metric registry — the same expressions used by the Prometheus alerting rules (Section 16.5). Each replica evaluates independently. For shared-state signals (dependency probes, circuit breaker state from Redis, warm pool counts from Postgres), all replicas converge. For per-replica signals (request queue depth, active connections, error rates), each replica reflects only its own load.

`lenny-ops` aggregates health as follows:

| Source | Method | What it provides |
|--------|--------|-----------------|
| Prometheus (primary) | `GET /api/v1/alerts` on Prometheus | All firing alerting rules across all gateway replicas. Prometheus evaluates the threshold expressions from whatever rules the operator has loaded — by default, Lenny's bundled rules (Section 25.13), which match the gateway's compiled-in expressions. Operator customizations to the bundled rules propagate automatically to this aggregated view. A queue-depth alert firing on one overloaded replica appears here even if other replicas are healthy. |
| Any single gateway replica | `GET /v1/admin/health` via ClusterIP | Shared-state signals: dependency probe results, circuit breaker state, suggested actions, runbook links. These are identical across replicas. |
| All gateway replicas (fallback) | `GetHealthFromAllReplicas()` via headless Service | Used when Prometheus is unreachable. `lenny-ops` queries each replica's health endpoint and merges: worst-of status per component, union of suggested actions. Cached for 10s. Circuit breakers skip unresponsive replicas. |

Responses use the canonical `degradation` envelope (Section 25.2). `actualSource` is one of `"prometheus"` or `"replica-fanout"`.

#### Recommendations Aggregation

The capacity recommendation rules (Section 25.3) are deterministic heuristics that read from the `MetricReader` interface — gauges, counters, histogram quantiles, and windowed rates. Each gateway replica maintains independent in-memory ring buffers for sliding-window aggregation. A single replica sees ~1/N of total traffic, so its peak claim rates, queue depths, and error counts are partial samples.

`lenny-ops` produces aggregate recommendations as follows:

| Source | Method | What it provides |
|--------|--------|-----------------|
| Prometheus (primary) | PromQL queries via `MetricSource` | The same metric values the recommendation rules read — but aggregated across all replicas. `sum(rate(lenny_warmpool_claims_total[5m]))` gives the true cluster-wide claim rate, not one replica's 1/N sample. `lenny-ops` evaluates the same recommendation rules against Prometheus data, producing recommendations based on the full traffic picture. |
| All gateway replicas (fallback) | `GetRecommendationsFromAllReplicas()` via headless Service | Used when Prometheus is unreachable. **Metrics are aggregated before rule evaluation**, not after: per-replica metric values are summed (rates, counts) or max'd (queue depths, utilization) first, then the same rule set evaluates against the merged metric values. This produces the same recommendations the primary path would have, just with less history. Cached for 30s. |

**Aggregate-before-evaluate.** Per-replica metric values are aggregated first (sum/max as appropriate); then the rule set evaluates once against the merged metrics. This produces recommendations comparable to the primary (Prometheus) path. Evaluating per-replica recommendations and merging them afterward would amplify noise — a confident-but-wrong recommendation from one replica could override correct signal from others.

The recommendation rules are compiled into both the gateway and `lenny-ops` binaries as a shared `pkg/recommendations/rules` package. This avoids duplicating rule definitions — the rules are defined once, evaluated against different `MetricReader` implementations (in-process registry in the gateway, Prometheus-backed or aggregated-fan-out-backed in `lenny-ops`). Responses use the canonical `degradation` envelope.

**Opt-out for recommendations during Prometheus outage.** Operators who would rather see no recommendations than imprecise ones can set `ops.recommendations.disableOnPrometheusOutage: true`. With this flag, recommendations endpoints return 503 `RECOMMENDATIONS_UNAVAILABLE` when Prometheus is unreachable rather than computing from fan-out. Default is `false`.

#### Pool Bottleneck Analysis (Diagnostics)

Pool diagnostics (Section 25.6) query Prometheus for metrics like `lenny_warmpool_pod_startup_duration_seconds`, `lenny_warmpool_replenishment_rate`, and `lenny_warmpool_warmup_failure_total`. When Prometheus is unreachable, diagnostics fall back to scraping individual gateway replicas' `/metrics` endpoints via the headless Service. This provides point-in-time values only (no range queries), which is sufficient for diagnostics but produces lower-confidence results. The response's `degradation` envelope has `actualSource: "gateway-scrape"`.

#### OIDC Token Lifecycle (GatewayClient)

`GatewayClient` uses a service-account OIDC token to call the gateway admin API. Token management:

- **Pre-emptive refresh** at `security.oidc.tokenRefreshBeforeExpirySeconds` (default 60s) before expiry. The refresh runs asynchronously in a background goroutine; in-flight requests use the current token until refresh succeeds.
- **Minimum TTL enforcement.** Tokens with TTL shorter than `security.oidc.minTokenTTLSeconds` (default 300s) are rejected at startup with a clear error — very short TTLs cause refresh storms.
- **Refresh failure handling.** If refresh fails, the current token is used until it expires. Continuous refresh failures within the pre-expiry window emit `ops_health_status_changed` with component `gateway-auth` degraded. Once the current token expires and refresh is still failing, subsequent calls fail with `AUTH` category errors (401 from the gateway).
- **Revocation detection.** An unexpected 401 from the gateway's admin API triggers an immediate token refresh attempt. If the new token also produces 401, the token is assumed revoked and `ops_health_status_changed` emits with `gateway-auth` unhealthy.
- **Metric:** `lenny_ops_gateway_auth_token_refresh_total{status="success|failure"}`. Alert on failure rate > 0.

The `GatewayClient` never logs token values. The refresh path uses the same OIDC configuration as `lenny-ops`'s own authentication — configured via Helm value `security.oidc.issuerUrl` and the ServiceAccount's projected token volume.

### Rate Limiting

`lenny-ops` enforces per-service-account rate limits on all endpoints. The rate limiter uses a token bucket algorithm keyed by the `sub` claim in the JWT token. Default: 20 requests/second with burst of 50, configurable via `ops.rateLimiting` Helm values.

Rate-limited responses return `429 Too Many Requests` with `Retry-After` header. The rate limiter is in-process (not shared across replicas) — with multiple replicas, the effective limit is `limit * replicas`.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_rate_limited_total` | Counter | `service_account` | Requests rejected by rate limiter |

### Idempotency

Mutating endpoints (`POST`, `PUT`) accept an optional `Idempotency-Key` header (caller-generated UUID). When present:

1. `lenny-ops` looks up `(key, caller_id)` in `ops_idempotency_keys` (Postgres).
2. If found and completed: returns the stored response without re-executing.
3. If found and in-progress: returns `409 OPERATION_IN_PROGRESS` with the elapsed time.
4. If not found: inserts the key, executes the operation, stores the response.

**Keys are bound to caller identity.** The primary key is `(key, caller_id)`, not `key` alone, where `caller_id` is the OIDC `sub` claim of the requesting service account. Two different callers using the same UUID receive independent idempotency behavior — one caller cannot replay another caller's operation by guessing their key.

**Two TTL classes:**

| Class | TTL | Used for |
|-------|-----|----------|
| Standard | 24h | Single-request mutations (pool scale, config apply, drift reconciliation, single backup trigger, lock operations). |
| Long-running | 7d (default) | Multi-phase operations where the agent may pause between steps — upgrades (`POST /v1/admin/platform/upgrade/start`, `proceed`, `pause`, `rollback`) and restore (`POST /v1/admin/restore/execute`). |

Long-running TTL is configurable via `ops.idempotency.longRunningKeyTTLSeconds` (default 604800). The endpoint picks the TTL based on a static classification — agents do not request a TTL.

**Endpoints that are naturally convergent** (PUT with full-state semantics) do not require idempotency keys — they are safe to retry regardless of the system's state between attempts. Idempotency keys are most valuable for non-convergent operations.

**Required for non-convergent operations at Tier 2/3.** The following endpoints **require** `Idempotency-Key` at Tier 2/3 and return `400 IDEMPOTENCY_KEY_REQUIRED` when omitted:
- `POST /v1/admin/platform/upgrade/start`
- `POST /v1/admin/restore/execute`
- `POST /v1/admin/backups` for `type: "full"`

At Tier 1 (dev), the key is optional on these endpoints to simplify interactive testing.

#### Behavior during Postgres outage

During a Postgres outage, `lenny-ops` cannot durably record idempotency keys. In this state:

- **Endpoints requiring idempotency keys return `503 IDEMPOTENCY_STORE_UNAVAILABLE`.** This is stricter than most ops endpoints (which degrade with fallback stores) because idempotency is the guarantee the agent relied on when submitting the mutation — silently proceeding without it would violate the contract.

  **Chicken-and-egg note for `restore/execute` during Postgres outage.** The restore endpoint requires an idempotency key, but is also the recovery mechanism for some Postgres-failure scenarios. When Postgres is the thing that's broken and the operator wants to restore from backup, the API path is unavailable — the operator must use the manual recovery procedure documented in Section 25.15 Total-Outage Recovery (Path D for break-glass `kubectl port-forward` or, if `lenny-ops` itself can't help, the manual restore Job creation in Path D step 4). This chicken-and-egg is inherent to a self-hosted recovery API that depends on its own state store; the manual path closes the gap.

  **Operation-status uncertainty after 503.** When an agent receives `503 IDEMPOTENCY_STORE_UNAVAILABLE` after submitting a required-key mutation, the operation's true state is **unknown**: the outage may have started before the row was inserted (operation didn't run) or after the row was inserted but before the response was returned (operation may be running). Agents should NOT assume either outcome. Recovery: when Postgres recovers, the agent retries with the same key. Three outcomes are possible:
  - **`200 OK` with the original response** — the operation completed before the outage; the response was cached. Safe.
  - **`409 OPERATION_IN_PROGRESS`** — the operation is still running. Safe to wait.
  - **A fresh execution** — the row was never inserted; the operation runs now. Safe.
  In all three cases, the agent ends with a known operation state, but it cannot distinguish them from the 503 response alone. For operations where this uncertainty is unacceptable (e.g., `upgrade/start`), the agent should consult a separate state endpoint (`GET /v1/admin/platform/upgrade/status`) post-recovery to confirm.
- **Endpoints that accept optional idempotency keys proceed without tracking**, but the response includes `degradation.warnings` noting that retry-safety is not guaranteed.

This avoids a multi-replica split-brain: without Postgres, distinct `lenny-ops` replicas cannot coordinate idempotency state, so required-key endpoints fail-closed rather than returning inconsistent results.

```sql
CREATE TABLE ops_idempotency_keys (
    key         TEXT NOT NULL,
    caller_id   TEXT NOT NULL,              -- OIDC sub claim
    endpoint    TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'in_progress',  -- 'in_progress', 'completed', 'failed'
    response    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (key, caller_id)
);

CREATE INDEX ops_idempotency_keys_expires_at ON ops_idempotency_keys (expires_at);
```

#### Retention

A scheduled `DELETE` runs daily off-peak (02:30 UTC by default) removing expired rows. Lazy cleanup on acquire handles burst cases between scheduled runs. Operators can reduce `ops.idempotency.keyTTLSeconds` from 24h to 1h if the deployment has high mutation volume and short agent retry windows.

#### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `OPERATION_IN_PROGRESS` | `POLICY` | 409 | A matching idempotency key is in-progress. Response includes `"elapsed"`. |
| `IDEMPOTENCY_KEY_REQUIRED` | `PERMANENT` | 400 | This endpoint requires `Idempotency-Key` at the current tier. |
| `IDEMPOTENCY_STORE_UNAVAILABLE` | `TRANSIENT` | 503 | Postgres is unreachable and this endpoint cannot proceed without idempotency tracking. |
| `IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER` | `AUTH` | 403 | The `(key, caller_id)` lookup is not yours; distinct from a conflict on your own key. Rare — useful for detecting accidental key reuse in shared agent logic. |

### Remediation Coordination

To prevent conflicting concurrent remediations from multiple agents, `lenny-ops` provides remediation locks. Locks use a tiered storage strategy (Postgres → Redis → in-memory) so that coordination remains available during storage outages — precisely when remediation is most needed. The rest of this subsection describes the consistency model explicitly because tiered stores are subtle.

#### Consistency Model

The lock service provides **monotonically weakening exclusivity with epoch-enforced tier transitions**:

- **Tier 1 (Postgres available):** strict exclusivity across all replicas and all callers. Equivalent to a distributed mutex.
- **Tier 2 (Postgres down, Redis available):** strict exclusivity across all replicas and all callers, within Redis's own consistency guarantees (Sentinel failover may lose very recent writes; Redis Cluster does not). Acquisitions made at Tier 2 are **marked with the Postgres outage epoch** (see below) so that when Postgres recovers, split-brain is detected and resolved deterministically.
- **Tier 3 (both down):** advisory within a single `lenny-ops` replica. In multi-replica deployments, cross-replica coordination is **not provided** — the service either rejects the acquisition (default) or records a coordinated-degradation event, depending on `ops.locks.memoryTier` configuration.

Locks **are** enforced when acquired through a tier that has consensus (Tiers 1 and 2). Agents may override an existing lock only by explicitly stealing it (see Stealing below) — "advisory" in this spec means overridable via a documented audited mechanism, not silently unenforced.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/admin/remediation-locks` | Acquire a lock. Body: `{"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": 300}`. |
| `GET` | `/v1/admin/remediation-locks` | List active locks. |
| `GET` | `/v1/admin/remediation-locks/{id}` | Get a single lock's current state (used to validate ownership before continuing remediation). |
| `PATCH` | `/v1/admin/remediation-locks/{id}` | Extend an existing lock's TTL. Body: `{"ttlSeconds": 300}`. Requires caller to be the current `acquiredBy`. |
| `DELETE` | `/v1/admin/remediation-locks/{id}` | Release a lock. Requires caller to be the current `acquiredBy`. |
| `POST` | `/v1/admin/remediation-locks/{id}/steal` | Explicitly take over an existing lock. Requires `confirm: true` and a `reason`. |

#### Authorization

Lock operations are authorized in two layers — **scope-based** (which roles can touch which scopes at all) and **identity-based** (which caller can mutate a specific existing lock). Both must pass.

**Scope-based** (applies to `Acquire`, `Release`, `Steal`, `List`):

| Scope pattern | `platform-admin` | `tenant-admin` |
|---------------|------------------|----------------|
| `pool:{name}` | ✓ | ✓ if pool belongs to caller's tenant |
| `credential-pool:{name}` | ✓ | ✓ if credential pool belongs to caller's tenant |
| `session:{id}` | ✓ | ✓ if session belongs to caller's tenant |
| `tenant:{tenantID}:*` | ✓ | ✓ only when `{tenantID}` equals caller's tenant |
| `platform:*`, `upgrade:platform`, `restore:platform`, `config:global` | ✓ | ✗ (returns `403 LOCK_SCOPE_FORBIDDEN`) |

`tenant-admin` attempts on platform-scoped locks return `403 LOCK_SCOPE_FORBIDDEN`. This prevents a tenant admin from blocking a platform upgrade.

**Identity-based** (applies to mutations of existing locks):

| Operation | Required identity match |
|-----------|-------------------------|
| `Acquire` | None (creates a new lock; caller becomes `acquiredBy`). |
| `Release` | Caller MUST equal `acquiredBy`. Returns `403 LOCK_NOT_OWNED` otherwise. To release someone else's lock, use `Steal` (audited). |
| `Steal` | Any caller passing scope-based authorization. The steal is recorded with the previous and new `acquiredBy`; both audit and operational events are emitted. |
| `List` / `Get` | Read-only; no identity match required (any caller passing scope-based authorization can see the lock). |

The identity-based rule on `Release` resolves the otherwise-ambiguous interaction with `restore/resume` and other downstream operations that require ownership: the only paths to change a lock's holder are `Acquire` (after expiry/release) and `Steal` (audited). A second platform-admin cannot silently release another's lock; they must steal.

#### Lock Struct

```go
// pkg/ops/coordination/locks.go

type RemediationLockService interface {
    Acquire(ctx context.Context, req LockRequest) (*Lock, error)
    List(ctx context.Context) ([]Lock, error)
    Get(ctx context.Context, lockID string) (*Lock, error)
    Extend(ctx context.Context, lockID string, ttlSeconds int) (*Lock, error)
    Release(ctx context.Context, lockID string) error
    Steal(ctx context.Context, lockID string, req StealRequest) (*Lock, error)
}

type Lock struct {
    ID            string    `json:"id"`
    Scope         string    `json:"scope"`
    Operation     string    `json:"operation"`
    AcquiredBy    string    `json:"acquiredBy"`
    OperationID   string    `json:"operationId,omitempty"`
    AcquiredAt    time.Time `json:"acquiredAt"`     // server-authoritative (see Clock Source)
    ExpiresAt     time.Time `json:"expiresAt"`      // server-authoritative
    LockStore     string    `json:"lockStore"`      // "postgres" | "redis" | "memory"
    Epoch         uint64    `json:"epoch"`          // monotonic — see Tier Transitions
    Revision      uint64    `json:"revision"`       // increments on each steal
    StolenFrom    string    `json:"stolenFrom,omitempty"`  // prior acquiredBy if this was a steal
}
```

#### Storage Tiers

The lock service attempts storage tiers in order, falling back on failure. Each tier's acquire path uses a compare-and-set primitive; there is no "check then write" window.

| Tier | Store | Acquire mechanism | Coordination scope | When used |
|------|-------|-------------------|--------------------|-----------|
| 1 | Postgres | `INSERT ... ON CONFLICT (scope) DO NOTHING RETURNING *` | All replicas (shared table) | Default — both stores available |
| 2 | Redis | `SET NX EX` with Lua script writing `{lockID, epoch, revision}` atomically | All replicas (shared keyspace) | Postgres unreachable |
| 3 | In-memory | `sync.Map` + per-replica TTL wheel | Depends on `ops.locks.memoryTier` (see below) | Both Postgres and Redis unreachable |

**Tier 1 — Postgres (default):**

```sql
CREATE TABLE ops_remediation_locks (
    id           TEXT PRIMARY KEY,
    scope        TEXT NOT NULL,
    operation    TEXT NOT NULL,
    acquired_by  TEXT NOT NULL,
    operation_id TEXT,
    acquired_at  TIMESTAMPTZ NOT NULL,        -- written from server clock via Postgres now()
    expires_at   TIMESTAMPTZ NOT NULL,
    epoch        BIGINT NOT NULL,
    revision     BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT unique_active_scope UNIQUE (scope)
);

-- All timestamps use Postgres `now() at time zone 'UTC'` as the authoritative clock.
```

Acquisitions at Tier 1 use `epoch = 0` (reserved for the Postgres tier). `Acquire` fails with `409 REMEDIATION_LOCK_CONFLICT` if a non-expired lock exists for the same scope. Expired rows are cleaned up lazily on acquire or by a periodic goroutine (every 60 seconds).

**Tier 2 — Redis:**

Uses a Lua script on key `ops:lock:{scope}` that atomically:
1. Reads the current outage epoch from `ops:lock-epoch:current` (see Tier Transitions).
2. Checks for an existing non-expired lock.
3. On no conflict, writes `{lockID, scope, operation, acquiredBy, operationID, acquiredAt, expiresAt, epoch, revision}` with `PEXPIREAT`.

`acquiredAt` and `expiresAt` are derived from `redis.call('TIME')` so locks across replicas use a consistent clock source. Release uses a similar CAS script that verifies the lock ID matches before deleting.

**Tier 3 — In-memory:**

A `sync.Map` keyed by scope, with a TTL wheel expiring entries every 10 seconds. Behavior depends on `ops.locks.memoryTier`:

- **`"single-replica-only"`** (default): lock acquisition succeeds only if `lenny-ops` is running with a single replica (detected via K8s Endpoints lookup at startup and re-checked every 30s). In multi-replica deployments, Tier 3 returns `503 REMEDIATION_LOCK_NO_COORDINATION` to force agents to either retry (waiting for Postgres/Redis) or explicitly accept uncoordinated operation.
- **`"always"`**: lock acquisition always proceeds, with a warning in `degradation.warnings` that coordination is replica-local. Suitable for single-replica production and dev; unsafe for multi-replica production.
- **`"never"`**: Tier 3 is disabled. When both Postgres and Redis are down, lock acquisition returns `503 REMEDIATION_LOCK_NO_COORDINATION`. Most conservative option.

The default (`"single-replica-only"`) matches the recommendation for the v1 deployment topology.

#### Tier Transitions and Split-Brain Prevention

The critical failure mode in a naive tiered design is split-brain: agent A holds a lock in Postgres; Postgres fails; agent B acquires the same lock in Redis; both run concurrently. Lenny prevents this with **outage epochs**:

**Outage epochs.** A monotonically-increasing integer stored **in both Postgres (`ops_lock_epoch.current`) and Redis (`ops:lock-epoch:current`)**. The dual-store epoch is the foundation of split-brain detection — storing it only in Redis would leave a gap during simultaneous Postgres+Redis outages. Maintenance:

- On Postgres → Tier 2 transition: the service increments the Redis-side epoch via `INCR ops:lock-epoch:current`, stamps the new value onto every Tier 2 acquisition. The Postgres-side epoch will be brought up to date during the next reconciliation pass (see below).
- On Tier 2 → Postgres recovery: the service reads `MAX(ops:lock-epoch:current_redis, ops_lock_epoch.current_postgres)`, writes the max back to Postgres in the same transaction that performs reconciliation, and resumes Tier 1 service.
- On Tier 3 (both stores down) operation: `lenny-ops` records the outage start time and an in-memory `pendingEpochIncrement` flag. When **either** Redis or Postgres recovers first, the recovering store's epoch counter is incremented by 1 and stamped with the outage start time. When the second store recovers, the standard `MAX(redis_epoch, postgres_epoch)` reconciliation brings them into sync.

Because both stores hold the epoch and reconciliation uses the maximum, the system is **safe under any single-store outage and any out-of-order recovery sequence**. The only failure mode is "neither store ever recovers," in which case Lenny is effectively offline.

When Postgres recovers, the service performs a **reconciliation pass** before serving any new Tier 1 acquisitions. The reconciliation runs as a **single Postgres transaction** that holds an advisory lock on `pg_advisory_xact_lock(0xLOCK_EPOCH_RECONCILE)` for its duration. New Tier 1 acquisitions block on this advisory lock — they wait for reconciliation to commit before proceeding. This eliminates the race where a Tier 1 acquisition could read a stale epoch from Postgres while reconciliation is still computing the new max.

Within the transaction:

1. Read `MAX(redis_epoch, postgres_epoch)` and write the max back to `ops_lock_epoch.current` in Postgres.
2. Read all active Redis locks with epoch ≥ the epoch of the last reconciliation.
3. For each, attempt `INSERT ... ON CONFLICT (scope) DO NOTHING` into Postgres.
4. If a Postgres row already exists for the same scope (the pre-outage lock that was never released because Postgres was unreachable), apply the **deterministic split-brain resolution rule** (below).
5. Commit. New Tier 1 acquisitions resume; they will read the just-updated epoch.

```sql
CREATE TABLE ops_lock_epoch (
    id        TEXT PRIMARY KEY DEFAULT 'singleton',
    current   BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ops_lock_conflicts (
    id                 BIGSERIAL PRIMARY KEY,
    scope              TEXT NOT NULL,
    pre_outage_lock    JSONB NOT NULL,
    post_outage_lock   JSONB NOT NULL,
    winner             TEXT NOT NULL,         -- "pre_outage" | "post_outage"
    loser_was_active   BOOLEAN NOT NULL,      -- true if the loser was non-expired at resolution time
    detected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Deterministic split-brain resolution rule.** When both a Postgres lock (epoch `E_pre`) and a Redis lock (epoch `E_post`, `E_post > E_pre`) exist on the same scope at reconciliation time:

| Postgres lock state | Redis lock state | Resolution |
|---------------------|------------------|------------|
| Expired by clock | Any | **Redis wins.** Postgres row deleted; Redis lock copied into Postgres with `epoch = E_post`. |
| Not expired | Expired by clock | **Postgres wins.** Redis lock removed; Postgres lock retained with `epoch = E_pre`. |
| Not expired | Not expired | **Pre-outage (Postgres) wins.** Postgres lock retains exclusivity. The Redis lock is removed; the Redis lock holder receives `409 REMEDIATION_LOCK_CONFLICT` with `splitBrain: true, winner: "pre_outage", winnerHolder: "<pre-outage acquiredBy>"` on its next heartbeat/list/release call. |

The "pre-outage wins when both active" rule is intentional: the pre-outage lock represented exclusive ownership in a state where consensus existed; the post-outage lock was acquired during a window where the lock holder couldn't see the pre-outage one. Preferring the pre-outage holder restores the original ownership rather than rewarding the agent that happened to retry during the outage.

The losing holder (always notified via the heartbeat path) can either back off (typical) or call `Steal` (Section 25.4 Stealing) to override — but stealing is now a deliberate, audited action, not a silent split-brain.

`ops_lock_conflicts` records every conflict for post-incident audit. Operators alerted on `lenny_ops_lock_split_brain_detected_total` should review the table to understand which agents collided and why.

**Grace period for same-epoch Redis acquisitions:** When Postgres recovers, Tier 1 is the service default again, but outstanding Tier 2 locks are honored until their TTL expires. New acquisitions for a scope currently held at Tier 2 return `409 REMEDIATION_LOCK_CONFLICT` (not silently retried at Tier 1).

**Tier 3 isolation:** Locks held only in memory at Tier 3 are not reconciled upward. On Postgres or Redis recovery, they exist only for the rest of their TTL on the originating replica. Agents holding Tier 3 locks should treat `lenny-ops` recovery as a signal that coordination is now available and acquire a new lock at the available higher tier before continuing the remediation.

#### Clock Source

All TTLs and `expiresAt` timestamps are authored by a single clock per tier:
- Tier 1: Postgres `now() at time zone 'UTC'`.
- Tier 2: Redis `TIME` command (via Lua script).
- Tier 3: the `lenny-ops` process clock.

Agents do not pass `expiresAt` in the request body; they pass `ttlSeconds`. The server computes `expiresAt = server_clock + ttlSeconds`. This removes client-side clock dependence and most clock-skew failure modes. Clock skew between nodes running Postgres and Redis is bounded by NTP (operator-managed); `lenny-ops` monitors drift and alerts when Postgres ↔ Redis skew exceeds 10s (see Self-Monitoring).

#### Stealing

An agent with higher-priority context (e.g., responding to a more severe alert) may explicitly steal an existing lock:

```
POST /v1/admin/remediation-locks/{id}/steal
{
  "confirm": true,
  "reason": "Superseded: warm-pool-exhaustion took priority over routine scaling",
  "ttlSeconds": 300
}
```

Steal increments `revision`, records the previous `acquiredBy` in `stolenFrom`, and emits a `remediation.lock_stolen` audit event. The steal is authorized by the same scope-pattern rules as acquisition. Without `confirm: true`, the endpoint returns a preview (Section 25.2, Dry-Run / Confirm Pattern).

Steal is the explicit alternative to silently ignoring a lock — which the API does not support. An agent that wants to act despite an existing lock must either steal it (audited, visible) or escalate.

#### Holding a Lock Across Long Remediations

An agent's lock can expire mid-remediation if the chosen TTL is shorter than the remediation actually takes. The lock service offers two mechanisms agents use to keep ownership for the full duration:

- **Heartbeat / extend.** `PATCH /v1/admin/remediation-locks/{id}` with body `{"ttlSeconds": 300}` extends the existing lock's `expiresAt` to `now() + ttlSeconds`. Authorization is identity-based: the caller MUST be the current `acquiredBy`, otherwise `403 LOCK_NOT_OWNED` (consistent with `Release`). Extension increments `revision`. Returns the updated lock with the new `expiresAt`. Agents performing long remediations should extend periodically (a common pattern: extend once per `ttlSeconds / 3` interval).
- **Validation before acting.** `GET /v1/admin/remediation-locks/{id}` returns the current state. Returns `404 REMEDIATION_LOCK_NOT_FOUND` if the lock has expired or been released; returns the lock with potentially-different `acquiredBy` if it has been stolen. Agents that cannot guarantee continuous extension (e.g., long-blocking operations that prevent the heartbeat goroutine from running) should re-validate ownership before each significant remediation step.

If an agent calls `Release` on a lock that no longer exists or no longer belongs to it, the call returns `404 REMEDIATION_LOCK_NOT_FOUND` or `403 LOCK_NOT_OWNED` respectively — never silent success. This forces the agent to discover lost ownership and emit an escalation if the remediation has already produced state changes that the new lock holder may conflict with.

The lock service does NOT auto-extend on activity — extension is the agent's responsibility. This is intentional: silently extending would obscure the contract between TTL and remediation duration. Agents that consistently need long TTLs should request them up front (`ttlSeconds` up to the documented maximum, see Helm values `ops.locks.maxTTLSeconds`).

#### Multi-Replica Behavior

When `lenny-ops` runs with multiple replicas:
- **Tiers 1 and 2** work identically across replicas (shared storage).
- **Tier 3** behavior is governed by `ops.locks.memoryTier` (see above). The default `"single-replica-only"` disables Tier 3 in multi-replica deployments and forces agents to retry when both Postgres and Redis are unavailable. This is a safer default than silently allowing split-brain-prone in-memory locks.

Deployers scaling `lenny-ops` beyond a single replica should review `ops.locks.memoryTier` and make an explicit trade-off. See also "Multi-Replica Lenny-Ops Scaling" at the end of this section.

#### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `REMEDIATION_LOCK_CONFLICT` | `POLICY` | 409 | Another agent holds a lock on this scope. Response includes `splitBrain: true` if a split-brain condition was detected. |
| `REMEDIATION_LOCK_NOT_FOUND` | `PERMANENT` | 404 | Lock ID not found or already expired. |
| `LOCK_SCOPE_FORBIDDEN` | `AUTH` | 403 | The caller's role does not authorize this lock scope. |
| `LOCK_NOT_OWNED` | `AUTH` | 403 | Caller is not the lock's `acquiredBy`; use `Steal` to take over an existing lock. |
| `REMEDIATION_LOCK_NO_COORDINATION` | `TRANSIENT` | 503 | Both Postgres and Redis are unreachable and Tier 3 is not permitted for this deployment. Retry after storage recovers. |

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_lock_store_active` | Gauge | `store` | 1 for the currently active lock store tier. |
| `lenny_ops_lock_outage_epoch` | Gauge | | Current outage epoch value. |
| `lenny_ops_lock_split_brain_detected_total` | Counter | `scope_pattern` | Split-brain events detected during reconciliation. |
| `lenny_ops_lock_steal_total` | Counter | `scope_pattern` | Explicit steals. |
| `lenny_ops_clock_skew_seconds` | Gauge | `pair` ("postgres-redis", "postgres-gateway") | Measured clock skew between dependency clocks. |

#### Audit Events

`remediation.lock_acquired`, `remediation.lock_extended`, `remediation.lock_released`, `remediation.lock_expired`, `remediation.lock_stolen`, `remediation.lock_split_brain_detected`. (`Get` and `List` are read operations and are not individually audited; `audit.query_executed` covers them in aggregate.)

### Escalation

When an agent determines that a problem exceeds its remediation capabilities (e.g., requires cluster admin access, or repeated remediation has failed), it can record a structured escalation. The create path uses a tiered storage strategy so that escalations can always be recorded — including during the storage outages that are most likely to trigger them.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/admin/escalations` | Create an escalation |
| `GET` | `/v1/admin/escalations` | List escalations. Params: `?status=`, `?since=`, `?severity=`, `?limit=`, `?cursor=` |
| `PUT` | `/v1/admin/escalations/{id}` | Update status (acknowledged, resolved) |

#### Behavior

Creating an escalation emits an `escalation_created` operational event (written to the Redis stream and/or in-memory event buffer alongside other events). The escalation includes the cause (alert name, diagnostic summary, failed remediation steps) and a severity level. Agents subscribed to the event stream — or webhook subscribers — receive the escalation and can route it to PagerDuty, Slack, or any external system. The platform provides the structured record; the routing is the deployer's responsibility.

```go
type Escalation struct {
    ID              string          `json:"id"`
    Severity        string          `json:"severity"`        // critical, warning, info
    Source          string          `json:"source"`          // agent name / service account
    OperationID     string          `json:"operationId,omitempty"`
    AlertName       string          `json:"alertName,omitempty"`
    RunbookName     string          `json:"runbookName,omitempty"`
    Summary         string          `json:"summary"`
    DiagnosticData  json.RawMessage `json:"diagnosticData,omitempty"`
    FailedActions   []FailedAction  `json:"failedActions,omitempty"`
    Status          string          `json:"status"`          // open, acknowledged, resolved
    Persistence     string          `json:"persistence"`     // "durable-postgres", "durable-redis", "buffered-memory"
    Emitted         bool            `json:"emitted"`         // true once the escalation_created event has been published
    CreatedAt       time.Time       `json:"createdAt"`       // authoring timestamp (preserved across tier flushes)
}
```

#### Storage Tiers (Create Path)

`POST /v1/admin/escalations` attempts storage tiers in order:

| Tier | Store | Behavior | HTTP status | `persistence` value |
|------|-------|----------|-------------|---------------------|
| 1 | Postgres | Insert into `ops_escalations` table. Full durability. | 201 Created | `"durable-postgres"` |
| 2 | Redis | Write to Redis hash `ops:escalations:{id}` with 24h TTL. | 201 Created | `"durable-redis"` |
| 3 | In-memory | Buffer in `lenny-ops` process memory (capped at 100 entries, oldest evicted). | 202 Accepted | `"buffered-memory"` |

When not at Tier 1, the response also includes a `X-Lenny-Persistence` response header matching the `persistence` field. Tier 3 responses additionally include a warning in the response body:

```json
{
  "id": "esc-abc",
  "persistence": "buffered-memory",
  "warning": "Escalation stored in memory only. It will be lost if lenny-ops restarts before Postgres or Redis recovers. The escalation_created event has been emitted to the event stream so webhook subscribers will still receive it."
}
```

**At Tier 2/3 deployments where `ops.escalation.requireDurable: true`** (default is `false`), escalation creation fails with `503 ESCALATION_NO_DURABLE_STORE` when both Postgres and Redis are unavailable, instead of accepting a memory-only record. This is the conservative option for deployers who would rather have an explicit failure than a silent durability gap during storage outages.

#### Emission Exactly-Once

The event stream receives exactly one `escalation_created` event per escalation, regardless of how many times the escalation is written or flushed across tiers.

- The emission path writes the event to the gateway's Redis stream + in-memory ring buffer, and then sets `emitted = true` on the escalation record.
- On reconciliation flush (below), the `emitted` flag is carried forward unchanged. The flush **never re-emits** — it only promotes the record to a higher-durability tier.
- If emission itself fails (e.g., Redis and gateway buffer both unavailable at the moment of escalation creation), the `emitted` flag stays `false`. A background retry attempts emission every 30 seconds until successful. Agents querying the escalation can tell whether emission has happened via the `emitted` field.


#### Reconciliation

A background goroutine checks every 30 seconds whether a higher-priority store has recovered. When it has, buffered escalations are flushed upward: in-memory → Redis → Postgres. Flush semantics:

1. **Timestamps preserved.** The original `CreatedAt` is preserved across all tier transitions. Agents querying the escalation after reconciliation see the real authoring time, not the flush time.
2. **Emission flag preserved.** `emitted` is not reset during flush. An escalation that was already emitted when it was created at Tier 2 remains `emitted=true` after reconciliation to Tier 1.
3. **Idempotency.** If the destination store already has a record with the same ID (a previous partial flush), the flush is a no-op.
4. **Rate-limited writes.** Flush is rate-limited to `ops.escalation.reconciliationWritesPerSecond` (default 20) to avoid spiking Postgres under recovery load.
5. **Audit.** Each successful flush emits a `remediation.escalation_persisted` audit event recording the source tier, destination tier, and flush duration. These events are rate-limited per unique tier transition to avoid audit-log noise during large flushes.

#### Storage Tiers (Query Path)

`GET /v1/admin/escalations` and `PUT /v1/admin/escalations/{id}` read/write from the highest available store:

- **Postgres available:** full query with pagination, filtering, and status updates.
- **Postgres down, Redis available:** queries scan Redis hash keys with `ops:escalations:*` prefix. Pagination uses the canonical envelope but `cursorKind: "pk"` is replaced with `cursorKind: "none"` (no pagination — `limit` only); `hasMore` reflects whether more records exist in the Redis scan. Status updates work.
- **Both down:** queries return only in-memory buffered escalations. The response includes the canonical `degradation` envelope with `level: "degraded"`, `actualSource: "in-memory"`, and a warning noting durable history is unavailable. Status updates work within the buffer until flush.

#### Event Emission

The `escalation_created` event is emitted through the gateway's event emitter (Redis stream + in-memory buffer), independently of which storage tier accepted the escalation record. This means a webhook subscriber (e.g., PagerDuty integration) receives the escalation notification even when Postgres is down — the event stream carries the event, and `lenny-ops` delivers it to cached webhook subscriptions (Section 25.5).

When **both** Redis and the gateway (and therefore both event destinations) are unreachable at escalation-creation time, `lenny-ops` sets `emitted = false` and retries the emission every 30 seconds until one of them recovers. Agents can poll the escalation to observe `emitted` transitioning to `true`.

#### Storage

```sql
CREATE TABLE ops_escalations (
    id              TEXT PRIMARY KEY,
    severity        TEXT NOT NULL,            -- 'critical', 'warning', 'info'
    source          TEXT NOT NULL,            -- service account / agent name
    operation_id    TEXT,
    alert_name      TEXT,
    runbook_name    TEXT,
    summary         TEXT NOT NULL,
    diagnostic_data JSONB,
    failed_actions  JSONB NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'open',  -- 'open', 'acknowledged', 'resolved'
    persistence     TEXT NOT NULL,            -- 'durable-postgres', 'durable-redis', 'buffered-memory'
    emitted         BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL,     -- preserved across tier flushes (NOT defaulted)
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX ops_escalations_status_severity ON ops_escalations (status, severity);
CREATE INDEX ops_escalations_created_at ON ops_escalations (created_at DESC);
```

The `created_at` column is **not** defaulted to `now()` — the application supplies the original creation timestamp so that flushed-from-buffer escalations preserve their authoring time (Section 25.4 Reconciliation).

#### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `ESCALATION_NOT_FOUND` | `PERMANENT` | 404 | Escalation ID not found in any tier. |
| `ESCALATION_NO_DURABLE_STORE` | `TRANSIENT` | 503 | `requireDurable: true` and both Postgres and Redis are unavailable. |

### Self-Monitoring

`lenny-ops` monitors its own operational health and emits structured events when it degrades. This closes the "who watches the watcher" gap — the external watchdog agent receives health signals about `lenny-ops` itself through the same event stream it uses for platform events.

#### Self-Health Checks

A background goroutine runs every `ops.selfHealth.checkIntervalSeconds` seconds (default 10). Shorter intervals catch fast failure-and-recovery cycles; longer intervals reduce overhead. Tier 1 deployments may raise this to 30s to reduce dev-cluster overhead. The check evaluates:

| Check | Condition for degraded | Condition for unhealthy |
|---|---|---|
| Postgres connection pool | Active connections > 80% of pool size | Connection errors > 0 in last 60s |
| Redis consumer lag | Stream lag > 1000 events | Stream lag > 5000 events or consumer disconnected |
| Webhook delivery backlog | Pending deliveries > 100 | Pending deliveries > 500 or all deliveries failing |
| K8s API connectivity | Latency > 2s | Unreachable |
| Memory pressure | RSS > 80% of limit | RSS > 95% of limit |

When the aggregate self-health status changes, `lenny-ops` emits an `ops_health_status_changed` event to the Redis stream (and the gateway in-memory buffer; or its own local buffer when both are unavailable). The watchdog receives this alongside platform events.

**Multi-replica scope.** Each `lenny-ops` replica runs its own self-health checks and emits its own `ops_health_status_changed` events. Events carry the replica identity (`source.replicaID` field) so subscribers can distinguish leader-replica self-health from non-leader-replica self-health. During dual gateway+Redis outages where events fall back to per-replica local buffers, `ops_health_status_changed` events are visible only to consumers connected to the emitting replica — `sessionAffinity: ClientIP` (Section 25.4 Services) keeps SSE clients on a single replica through reconnects, mitigating this for individual long-lived consumers, but no global view of self-health exists during dual outages.

**Event-driven supplements.** Beyond the periodic check, `lenny-ops` also emits health-affecting events synchronously on critical state changes that would otherwise be missed within a 10s window:

- Postgres connection error (any) → immediate evaluation of the `postgres_pool` check.
- Redis stream consumer disconnect → immediate evaluation of `redis_consumer_lag`.
- Webhook delivery 5xx burst (>10 in 30s for a single subscription) → immediate `webhook_backlog` evaluation.
- K8s API 5xx → immediate `k8s_api` evaluation.

These supplements ensure failures shorter than the 10s polling interval still trigger health changes. They are deduplicated against the periodic check so a single failure-and-recovery within 10s doesn't produce duplicate `ops_health_status_changed` events.

Additionally, `lenny-ops` exposes a detailed self-health endpoint:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/ops/health` | Structured self-health report (same format as gateway health API) |

This endpoint is served on the same Ingress as all other `lenny-ops` endpoints. The watchdog can poll it as a complement to the event stream.

#### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_self_health_status` | Gauge | `check` | 0=healthy, 1=degraded, 2=unhealthy per check |
| `lenny_ops_postgres_pool_active` | Gauge | | Active Postgres connections |
| `lenny_ops_redis_consumer_lag` | Gauge | | Redis stream consumer lag (events behind head) |
| `lenny_ops_webhook_backlog` | Gauge | | Pending webhook deliveries |

### Structured Logging

All `lenny-ops` and gateway components emit structured JSON logs to stdout (standard for Kubernetes log collection). Each log line includes:

```json
{
  "ts": "2026-04-15T10:22:03.456Z",
  "level": "info",
  "msg": "remediation lock acquired",
  "component": "ops.coordination",
  "operation_id": "550e8400-e29b-41d4-a716-446655440000",
  "agent_name": "prod-watchdog",
  "trace_id": "abc123"
}
```

Log collection and forwarding (to Loki, Elasticsearch, CloudWatch, etc.) is the deployer's responsibility — Lenny does not ship a log aggregator. However, `lenny-ops` provides a convenience endpoint for querying container logs via the K8s API, so agents do not need `kubectl logs` access:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/logs/pods/{namespace}/{name}` | Container logs. Params: `?container=`, `?since=`, `?tail=`, `?previous=` |

This proxies to the K8s API's pod log endpoint. The `SessionDiagnosis.RelatedLogs` field (Section 25.6) includes the pod namespace and name, so agents can follow the reference directly.

### TLS

**External (Ingress).** TLS is terminated at the Ingress controller. The Ingress resource references a TLS Secret (cert-manager-issued or deployer-provided). All agent traffic to `lenny-ops` is encrypted in transit.

**Internal (TLS-default, NET-070).** `ops.tls.internalEnabled` defaults to `true` in every non-dev profile, mirroring the TLS-default posture established for the OTLP exporter (OTLP-068, §16.1 `lenny_otlp_export_tls_handshake_total`). When enabled, `lenny-ops` listens on TLS (port 8090) using the cert from `ops.tls.certSecretName`, and `GatewayClient` calls the gateway admin-API over HTTPS on the gateway's internal TLS port (`{{ .Values.gateway.internalTLSPort }}`, default 8443). The gateway already owns a cert chain via cert-manager (§10.3) and reuses it for the internal admin listener; no additional CA material is required. The internal path is mTLS-capable when `ops.tls.clientCertSecretName` is set; the default deploys server-auth TLS with JWT-authenticated clients.

The rationale is bearer-token exposure: every `GatewayClient` request carries a `lenny-ops-sa` JWT with `platform-admin` scope in the `Authorization` header, plus admin-API payloads (pool configs, connector settings, audit-bearing event envelopes). These payloads match the confidentiality profile that the OTLP-068 fix deemed unacceptable at plaintext. A plaintext admin-API hop is treated as a confidentiality regression regardless of whether the `lenny-system` NetworkPolicy limits peers to `lenny-ops` and admission-webhook pods, because any compromise of a sidecar in either pod — or of the admission-webhook allow-list peer, which lacks identity binding to `lenny-ops` — can lift a replayable admin JWT in transit.

**Plaintext opt-out (dev and explicit acknowledgment).** Deployers who must keep the plaintext admin API — for example, during cluster bring-up before cert-manager is fully reconciled, or in clusters that terminate mTLS at a service mesh sidecar whose listener exposes only plaintext to the app — set `ops.tls.internalEnabled: false` **and** `ops.acknowledgePlaintextAdminAPI: true`. The chart's `required` guard fails `helm install`/`helm upgrade` with a message pointing at this finding (NET-070) when `internalEnabled: false` is set without the acknowledgment outside dev mode (`global.devMode: true` — §17.4 — auto-implies the acknowledgment so local dev retains plaintext for convenience). When the acknowledged plaintext path is in effect, the admin JWT transits in cleartext and any entity able to read it can escalate to `platform-admin` from any pod the NetworkPolicy permits to reach the gateway internal port (admission webhooks, the Ingress controller peer); operators MUST compensate with network-layer controls (service mesh mTLS, an encrypted CNI data plane, or a narrower NetworkPolicy than the chart default).

**Observability (parity with OTLP-068).** `lenny-ops` emits `lenny_ops_admin_api_tls_handshake_total{result}` on every `GatewayClient` request attempt (§16.1). A `plaintext` result fires the `OpsAdminAPIPlaintextDetected` critical alert (§16.5), symmetric with `OTLPPlaintextEgressDetected`. The `lenny-preflight` Job runs an `ops-admin-tls` handshake probe against the gateway's internal TLS port at install and upgrade time (§17.9), validating that the server certificate's SAN covers the `lenny-gateway` ClusterIP hostname and that the handshake completes; the probe is skipped when `internalEnabled: false` is paired with the explicit plaintext acknowledgment.

---

## 25.5 Operational Event Stream

A real-time feed of platform operational events. `lenny-ops` reads from the Redis stream that the gateway writes to (or the gateway's in-memory event buffer when Redis is unavailable) and exposes SSE, polling, and webhook delivery.

**Event envelope.** Every event emitted and delivered by this service — SSE, polling (`GET /v1/admin/events`), and webhook — is a [CloudEvents v1.0.2](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) JSON record ([§12.6](12_storage-architecture.md#126-interface-design) envelope contract). The `type` field is `dev.lenny.<short_name>` (e.g., `dev.lenny.alert_fired`, `dev.lenny.upgrade_progressed`, `dev.lenny.operation_progressed`) using the event-type identifiers catalogued in [§16.6](16_observability.md#166-operational-events-catalog); `source` identifies the emitting component (`//lenny.dev/gateway/{replicaId}` or `//lenny.dev/ops/{replicaId}`); Lenny extensions `lennytenantid`, `lennyoperationid`, and `lennyrootsessionid` carry tenant, operation, and delegation-tree correlation. The event's source-independent identity is the CloudEvents `id` attribute, which matches the `eventKey` described in [§25.3](#253-gateway-side-ops-endpoints) (Event Buffer); the two names refer to the same value (the CloudEvents attribute is authoritative on the wire, the `eventKey` name is used in internal storage prose).

**Audit-bearing events.** When an operational event carries an audit record (e.g., `dev.lenny.audit_session_terminated`), `datacontenttype` is `application/ocsf+json` and the CloudEvents `data` field is the [OCSF v1.1.0](https://schema.ocsf.io/1.1.0/) record defined in [§11.7](11_policy-and-controls.md#117-audit-logging) Wire Format. Single-envelope model: CloudEvents is the transport, OCSF is the payload. Nothing is double-wrapped.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/events/stream` | SSE stream of operational events |
| `GET` | `/v1/admin/events` | Polling endpoint with pagination |
| `POST` | `/v1/admin/event-subscriptions` | Register a webhook |
| `GET` | `/v1/admin/event-subscriptions` | List subscriptions |
| `GET` | `/v1/admin/event-subscriptions/{id}` | Subscription details and delivery stats |
| `PUT` | `/v1/admin/event-subscriptions/{id}` | Update subscription filters |
| `DELETE` | `/v1/admin/event-subscriptions/{id}` | Delete a subscription |
| `GET` | `/v1/admin/event-subscriptions/{id}/deliveries` | Recent delivery attempts |

### Go Interface

```go
// pkg/ops/events/service.go

type EventStreamService interface {
    StreamEvents(ctx context.Context, w http.ResponseWriter, filter EventFilter) error
    ListEvents(ctx context.Context, filter EventFilter, cursor string, limit int) (*EventPage, error)
    CreateSubscription(ctx context.Context, sub SubscriptionRequest) (*Subscription, error)
    ListSubscriptions(ctx context.Context) ([]Subscription, error)
    GetSubscription(ctx context.Context, id string) (*Subscription, error)
    UpdateSubscription(ctx context.Context, id string, update SubscriptionUpdate) (*Subscription, error)
    DeleteSubscription(ctx context.Context, id string) error
    ListDeliveries(ctx context.Context, subID string, cursor string, limit int) (*DeliveryPage, error)
}
```

Event types and payload schemas match those defined in Section 25.3 (Event Emission). `alert_fired` event payloads include an optional `runbook` field (string) naming the relevant operational runbook, sourced from the Prometheus alerting rule's `runbook` annotation. See Section 25.7 "Path B" for the discovery flow.

**Emission responsibility.** Both the gateway and `lenny-ops` emit events to the same stream. The gateway emits signals derived from in-process state (`alert_fired`, `alert_resolved`, `pool_state_changed`, `circuit_breaker_*`, `credential_*`, `session_failed`, `backup_completed`/`backup_failed` based on Job watch, `platform_upgrade_available`, `health_status_changed`). `lenny-ops` emits signals it originates itself (`ops_health_status_changed`, `escalation_created`, `remediation_lock_acquired`/`_released`, `drift_detected`, `platform_upgrade_*` lifecycle events, `operation_progressed`). Both write to the same Redis stream and the gateway's in-memory ring buffer (via an internal RPC call when `lenny-ops` emits). Consumers don't need to distinguish the source — the event `type` carries the semantic, and `source` in the event envelope (values: `"gateway"` or `"lenny-ops"`) is available for audit-trail tracing.

### Storage

**Redis capped stream.** Key: `ops:events:stream` (platform-scoped). Uses Redis Streams (`XADD` with `MAXLEN ~ {ops.events.streamMaxLen}`). The Redis stream ID serves as the monotonically increasing per-source `eventId`; the canonical cross-source identifier is the `eventKey` carried in the event payload (Section 25.3 Event Buffer).

**Sizing guidance and memory budget:**

| Tier | `streamMaxLen` default | Approx Redis memory | Catch-up window at peak |
|------|------------------------|---------------------|-------------------------|
| 1 (dev) | 10,000 | ~5 MB | ~hours at low rates |
| 2 (staging) | 50,000 | ~25 MB | ~hours at moderate rates |
| 3 (production) | 100,000 | ~50 MB | ~10-30 minutes at peak |

Each event averages ~500 bytes (alert metadata + suggested action + payload). At Tier 3 peak (50+ pools, >100 sessions/min), event rate can reach ~5 events/sec sustained, with bursts to ~50/sec during incidents. A 100k-event stream provides 30-minute catch-up at burst rate, sufficient for the watchdog to recover from network blips without losing events.

**Memory monitoring.** Operators should monitor `lenny_ops_events_stream_length` (gauge, current stream length) and alert if it stays at MAXLEN for more than a few minutes — that indicates events are being evicted faster than consumed, and subscribers may experience gaps. Recommended alert: `lenny_ops_events_stream_length / lenny_ops_events_stream_maxlen > 0.95 for 5m`.

**Eviction and gap behavior.** When events are evicted before a slow subscriber reads them, the subscriber's next request returns `pagination.gapDetected: true` (canonical envelope, Section 25.2). The agent re-reads platform state to recover. Gap rate (`lenny_ops_events_stream_gaps_total`) should be near zero in healthy operation; persistent gaps suggest the stream is undersized.

**Cost trade-off.** Doubling `streamMaxLen` doubles Redis memory but proportionally extends catch-up window. For deployments with very slow watchdogs (cron-scheduled rather than continuous) or unstable networks, larger MAXLEN values are reasonable.

**Postgres tables for webhook subscriptions:**

```sql
CREATE TABLE ops_event_subscriptions (
    id                    TEXT PRIMARY KEY,
    callback_url          TEXT NOT NULL,
    types                 TEXT[] NOT NULL,
    severity              TEXT[],
    secret_hash           TEXT NOT NULL,
    secret_fingerprint    TEXT NOT NULL,             -- first 8 chars of SHA-256 (for audit)
    description           TEXT,
    created_by            TEXT NOT NULL,             -- OIDC sub of creator
    created_by_tenant_id  TEXT,                      -- NULL when creator is platform-admin scope
    tenant_filter         TEXT NOT NULL DEFAULT '*', -- "*" = match all events; otherwise a tenant ID
    generation            BIGINT NOT NULL DEFAULT 0, -- bumped on each update; used for cache invalidation
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    active                BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE ops_event_deliveries (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES ops_event_subscriptions(id),
    event_id        TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    status          TEXT NOT NULL,          -- "delivered", "failed", "pending"
    attempts        INT NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL    -- computed from retention policy
);

CREATE INDEX ops_event_deliveries_expires_at ON ops_event_deliveries (expires_at);
CREATE INDEX ops_event_deliveries_subscription_status ON ops_event_deliveries (subscription_id, status);
```

#### Delivery Tracking Retention

Webhook delivery rows can grow large at Tier 3: 100 events/min × 1000 subscriptions × 3 retry attempts = ~26M rows per 24 hours. Without retention, the table grows unbounded.

The chart configures retention via `ops.webhooks.deliveryRetentionDays`:

| Tier | Default | Rationale |
|------|---------|-----------|
| 1 | 1 day | Dev — recent debugging only. |
| 2 | 7 days | Standard incident-investigation window. |
| 3 | 30 days | Compliance-aligned for audit; sized for ~750M rows at high throughput. |

A scheduled `DELETE` runs daily at 03:45 UTC (off-peak) using `expires_at` as the predicate, with `LIMIT 10000` per batch to avoid long locks. Operators with stricter retention requirements can override per tier.

For deployments that don't need delivery audit (only the success/failure metric matters), set `ops.webhooks.deliveryTrackingMode: "metric-only"` — `lenny-ops` increments counters but doesn't write rows. Retains only delivery failure rows (those are useful for investigation) under `ops.webhooks.failuresOnlyRetentionDays`.

### Cursor Model

Event cursors are **opaque strings** containing an internal representation that always round-trips correctly regardless of which source served the response. The cursor encodes both the source kind and the source-specific position:

```
cursor := base64(source_kind || ":" || source_position)
```

Where `source_kind` is one of `"redis"`, `"buffer"`, or `"mixed"` (spans a transition). Agents MUST NOT parse cursors — they opaquely round-trip them. The canonical `pagination.cursorKind` field reports the source kind for observability. When a caller sends a cursor from one source to another that cannot honor it, `lenny-ops` translates by scanning for the first event with a matching `eventKey`. If no match is found (the event has been evicted), the response returns `gapDetected: true` per the canonical pagination envelope (Section 25.2) along with `oldestAvailableCursor`.

The canonical ordering key is `eventKey` (a ULID-like `{replicaID}:{emittedAt}:{nonce}` — see Section 25.3 Event Buffer). Agents that need to deduplicate across sources use `eventKey`; agents that just iterate forward rely on cursor round-trip.

### SSE Delivery

The SSE handler holds an open HTTP response and reads from the Redis stream via `XREAD BLOCK 0` in a goroutine. Each SSE frame is a CloudEvents JSON record — the full envelope in the frame's `data:` line — per CloudEvents' [JSON Format](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/formats/json-format.md). The SSE `id:` line carries the CloudEvents `id` attribute so clients reconnecting with `Last-Event-ID` resume from the correct position via `XRANGE`. Clients filter via canonical query parameters (`?eventType=`, `?severity=`, `?resourceType=pool&resourceId=default-gvisor`, `?since=`) defined in Section 25.2.

Each SSE connection gets an independent read cursor against the raw Redis stream (not a consumer group). Multiple agents can subscribe concurrently — each sees all events that match their filters. There is no "competing consumer" behavior; every subscriber gets every matching event.

**Redis-unavailable fallback.** When Redis is unreachable, the SSE handler switches to polling the gateway's event buffer endpoint (`GET /v1/admin/events/buffer`, Section 25.3) every 2 seconds. Events continue to flow to the SSE client with the canonical `degradation` envelope embedded in a periodic `:degradation {...}\n` comment line. The SSE connection stays open — the client sees only a brief gap in events (no disconnection). When Redis recovers, the handler switches back to `XREAD` transparently and emits a `:degradation {"level":"healthy"}` comment.

**Cursor transition safety.** When the source switches (Redis ↔ buffer), the handler internally tracks the last `eventKey` delivered and uses it to locate the continuation point in the new source. If no event in the new source has a greater-or-equal `eventKey`, the handler emits a `:gap {...}\n` comment indicating a gap was detected; the client can choose to re-read platform state.

### Polling Delivery

`GET /v1/admin/events` returns a paginated list for agents that prefer polling over SSE. It uses the canonical pagination envelope (Section 25.2). Requests and responses include:

- **`?cursor=`** — opaque string from a prior response. Omit to start from the oldest event.
- **`?limit=`** — page size (default 100, max 1000).
- **`?eventType=`, `?severity=`, etc.** — canonical filters (Section 25.2).
- **Response:** `items`, `pagination.cursor`, `pagination.hasMore`, `pagination.cursorKind` (one of `"redis"`, `"buffer"`, `"mixed"`), `pagination.headCursor`.
- **Ordering:** chronological (oldest-first within a page) by default. `?sortOrder=desc` reverses.

**Source fallback is transparent.** When Redis is unreachable, the endpoint serves from the gateway event buffer. The returned cursor encodes the buffer source; sending that cursor back in a later request after Redis recovers works (the cursor encodes the source kind, and `lenny-ops` translates forward). If the cursor's source kind is no longer serviceable (e.g., buffer source but buffer has wrapped past that event), the response returns `pagination.gapDetected: true` and `pagination.oldestAvailableCursor`.

History is limited to the buffer window (~500 events) while Redis is down. The response includes the canonical `degradation` envelope noting the fallback.

### Webhook Delivery

A background goroutine per active subscription reads events from the Redis stream (or gateway event buffer when Redis is unavailable) and POSTs the CloudEvents JSON record to the callback URL. `Content-Type` is `application/cloudevents+json` (CloudEvents JSON Format). Each delivery includes these HTTP headers:

- `X-Lenny-Signature` — HMAC-SHA256 of the request body using the subscription's secret. Agents validate this to confirm the delivery originated from Lenny.
- `X-Lenny-Event-Type` — the CloudEvents `type` attribute (e.g., `dev.lenny.alert_fired`).
- `X-Lenny-Event-Id` — the CloudEvents `id` attribute (same value as `eventKey`, [§25.3](#253-gateway-side-ops-endpoints) Event Buffer).
- `X-Lenny-Delivery-Id` — a UUID unique to this delivery attempt (enables idempotency on the receiver).
- `X-Lenny-Delivery-Attempt` — attempt number (1-based).

Receivers MAY also read the CloudEvents attributes directly from the JSON body; the `X-Lenny-Event-*` headers are duplicates for consumers that prefer header-level filtering.

Retry: 3 attempts with exponential backoff (1s, 5s, 30s). After 3 failed attempts, the delivery is marked `failed` in `ops_event_deliveries` and an `event_delivery_failed` operational event is emitted (but not itself delivered to that subscription, to avoid loops).

#### Webhook Secret Lifecycle

Each subscription has a high-entropy secret used for HMAC signing. The lifecycle is:

1. **Creation.** `POST /v1/admin/event-subscriptions` generates a secret server-side (256 bits from a CSPRNG), stores its SHA-256 hash in `ops_event_subscriptions.secret_hash`, and returns the plaintext secret **once** in the response body alongside a clear single-use notice:
   ```json
   {
     "id": "sub-abc",
     "callbackUrl": "https://...",
     "secret": "whsec_...",
     "secretRotationWarning": "This is the only time the secret will be returned. Store it securely. To rotate, delete and recreate this subscription or POST to /v1/admin/event-subscriptions/{id}/rotate-secret."
   }
   ```
2. **Storage.** The plaintext secret is **never logged** (response bodies for this endpoint are explicitly redacted in logs) and **never returned** on read endpoints (`GET /v1/admin/event-subscriptions`, `GET /v1/admin/event-subscriptions/{id}` omit the secret field entirely).
3. **Rotation.** `POST /v1/admin/event-subscriptions/{id}/rotate-secret` generates a new secret, returns it once, and updates the stored hash. A 60-second overlap window honors both the old and new secret (HMAC with either validates) so deployments can update the receiver without dropping deliveries.
4. **Revocation.** `DELETE /v1/admin/event-subscriptions/{id}` deletes the row. In-flight deliveries using the old secret complete but no new deliveries occur.
5. **Audit.** Audit events: `ops_event.subscription_created`, `ops_event.subscription_secret_rotated`, `ops_event.subscription_updated`, `ops_event.subscription_deleted`. Secret values never appear in audit records.

The "subscription_created" audit event fingerprints the secret (first 8 chars of its SHA-256) so operators investigating a delivery can confirm which secret was in effect without learning the secret.

#### SSRF and DNS Rebinding Protections

Callback URL validation runs both at subscription creation AND at each delivery attempt:

- **Scheme:** HTTPS only (HTTP allowed only when `ops.webhooks.allowHTTP: true`, off by default).
- **Host:** must be a registered domain (not a raw IPv4 or IPv6 literal). `[::1]`, `[fe80::1]`, and other link-local/private addresses are rejected. This includes rejecting URLs whose path segment contains an IP literal (`https://example.com@127.0.0.1/`, `https://example.com#@127.0.0.1/`, etc.).
- **Resolved IP:** the HTTP client resolves the host per request and checks the resolved IP is **not** in any of: RFC 1918 (10/8, 172.16/12, 192.168/16), RFC 4193 (fc00::/7), RFC 3927 (169.254/16), loopback, multicast, reserved, or the Kubernetes service CIDR and pod CIDR ranges (configurable via `ops.webhooks.blockedCIDRs`). The check happens on **every** delivery — this closes the DNS rebinding gap where a URL that resolves to a legitimate IP at subscription time later resolves to `127.0.0.1`.
- **Metadata service:** cloud instance metadata services (`169.254.169.254`, `metadata.google.internal`, `fd00:ec2::254`) are explicitly blocked regardless of other rules.
- **Redirects:** the HTTP client does **not** follow redirects. A redirect from a whitelisted domain to another destination is reported as a delivery failure (receivers are expected to return 200 directly).
- **Optional allowlist:** `ops.webhooks.domainAllowlist: ["pagerduty.com", "slack.com"]` restricts callbacks to a fixed list of domains (matches by suffix). Recommended for strict environments.


#### Subscription Cache and Invalidation

`lenny-ops` caches the full subscription list in memory on startup and on every subscription CRUD operation. Cache behavior:

- **On CRUD,** the cache is updated synchronously in-process before the request returns. On multi-replica deployments, a `subscription_cache_invalidate` internal RPC is sent to all replicas over the `lenny-ops` headless Service. Invalidation is **version-stamped**: each subscription has a `generation` counter incremented on every update. Delivery goroutines check `generation` before each delivery; a deleted-but-not-yet-invalidated subscription will be skipped because its generation mismatches.
- **Periodic refresh** from Postgres every 60 seconds as a consistency safeguard. During Postgres outages, the cache ages without refresh, but per-delivery generation checks still skip deleted subscriptions that were propagated via RPC before the outage.
- **Cold-start:** if `lenny-ops` starts while Postgres is down, the cache is empty — no webhook delivery occurs. A warning is logged loudly and `ops_health_status_changed` emits with a `subscriptionsUnavailable: true` flag. When Postgres recovers, the cache is populated and delivery begins.
- **Cache refresh is non-blocking:** the refresh runs on a background goroutine and does not block delivery goroutines. During refresh, deliveries continue against the prior snapshot.

The combined effect is that a subscription deleted via the API stops receiving events within a few hundred milliseconds (synchronous cache update + RPC propagation across replicas), regardless of the periodic-refresh interval.

#### Tenant Isolation

Subscriptions and event delivery respect tenant boundaries:

- A `platform-admin` may create subscriptions that match any events. Their subscriptions have `tenantFilter: "*"` by default.
- A `tenant-admin` may only create subscriptions with `tenantFilter: "{their-tenant}"`. Attempts to use a different tenant or wildcard return `403 SUBSCRIPTION_TENANT_FORBIDDEN`.
- The subscription record has a `created_by_tenant_id` column (or `null` for platform-scope) and an explicit `tenantFilter` matching regex.
- Event delivery filters **each event** against the subscription's `tenantFilter` before dispatch. Events that carry no tenant label (platform-scoped events — `platform_upgrade_*`, `ops_health_status_changed`, etc.) are matched by all `tenantFilter: "*"` subscriptions but **not** by tenant-scoped subscriptions.
- SSE and polling endpoints apply the same filter: tenant-scoped callers only see events matching their tenant or carrying no tenant label **if** the caller has permission for platform-scoped events (typically `platform-admin` only).

### Degradation

**Redis unreachable:** SSE, polling, and webhook delivery fall back to the gateway's in-memory event buffer (Section 25.3). Responses include the canonical `degradation` envelope with `actualSource: "gateway-buffer"`. History is limited to the buffer window (~500 events). Events are not persisted — after a gateway restart during a Redis outage, the buffer is empty. This is acceptable because operational events are notifications, not the system of record. The underlying state is always queryable via health and diagnostic endpoints.

**Gateway unreachable (Redis available):** `lenny-ops` still reads directly from the Redis stream; no fallback is needed. Events from the gateway stop arriving because the gateway isn't emitting, but events `lenny-ops` itself emits (escalations, lock changes, drift detection) still reach subscribers.

**Postgres unreachable:** Subscription CRUD endpoints return `503`. Webhook delivery and event streaming continue using cached subscriptions (see Webhook Subscription Lifecycle below). Delivery tracking (`ops_event_deliveries` table) is skipped — deliveries are best-effort without audit trail until Postgres recovers.

**Redis unreachable AND gateway unreachable:** Events that originate at the **gateway** (alerts, pool state changes, session failures) cannot be observed by `lenny-ops` — they have nowhere to land. Gateway-originated events emitted during this window are lost. Events that originate in **`lenny-ops` itself** (escalations, lock changes, drift detection, ops self-health) are buffered in `lenny-ops`'s own in-memory ring buffer (500 events per replica, analogous to the gateway's) and continue to flow to SSE/webhook consumers connected to the same `lenny-ops` replica. Polling and SSE for gateway-originated events return `503 EVENT_STREAM_UNAVAILABLE`; SSE responses still serve `lenny-ops`-originated events with a `:degradation {"actualSource":"lenny-ops-local-buffer","unavailableFields":["gateway-events"]}` comment line.

**`lenny-ops` local buffer mechanics.** The `lenny-ops` ring buffer is per-replica. In multi-replica deployments, only events emitted by *that* replica are in *that* replica's buffer — non-leader replicas buffer the events they emit; the leader buffers what it emits. SSE clients with `sessionAffinity: ClientIP` (the default) stay on one replica through reconnects, so a client connected to the leader sees the leader's events and a client connected to a non-leader sees that replica's events. **For any global view of `lenny-ops`-originated events during dual outages, no mechanism exists** — this is an explicit limitation of the dual-down failure mode. Recovery: when either Redis or the gateway buffer becomes available, `lenny-ops` flushes its local buffer to the recovered destination on a best-effort basis, with `eventKey` deduplication preventing duplicate consumer-side delivery.

**Both Postgres and Redis unreachable:** Combines the two above — stream flows from the gateway buffer (if available) for gateway-originated events; from `lenny-ops`'s per-replica local buffer for `lenny-ops`-originated events; webhook delivery uses cached subscriptions (only on replicas whose cache was populated before the outage — see Subscription Cache cold-start behavior); subscription CRUD is unavailable; no delivery tracking.


### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_events_stream_length` | Gauge | | Current Redis stream length |
| `lenny_ops_events_stream_gaps_total` | Counter | | Subscriber requests where the requested stream ID was already evicted (`pagination.gapDetected: true` returned) |
| `lenny_ops_events_sse_active_connections` | Gauge | | Active SSE connections |
| `lenny_ops_events_webhook_delivery_total` | Counter | `subscription_id`, `status` | Webhook delivery outcomes |
| `lenny_ops_events_webhook_delivery_latency_seconds` | Histogram | `subscription_id` | Webhook delivery latency |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `INVALID_EVENT_FILTER` | `PERMANENT` | 400 | Unrecognized event type or severity in filter |
| `WEBHOOK_VALIDATION_FAILED` | `PERMANENT` | 422 | Callback URL failed SSRF validation |
| `EVENT_STREAM_UNAVAILABLE` | `TRANSIENT` | 503 | Both Redis stream and gateway event buffer unreachable (gateway down) |
| `EVENT_STREAM_DEGRADED` | `TRANSIENT` | 200 | Serving from gateway buffer (Redis unavailable). Returned as response metadata, not HTTP error. |
| `SUBSCRIPTION_NOT_FOUND` | `PERMANENT` | 404 | Subscription ID not found |
| `SUBSCRIPTION_STORE_UNAVAILABLE` | `TRANSIENT` | 503 | Subscription CRUD unavailable (Postgres unreachable) |

### Audit Events

`ops_event.subscription_created`, `ops_event.subscription_updated`, `ops_event.subscription_deleted`.

---

## 25.6 Diagnostic Endpoints

Structured diagnostic endpoints that encapsulate the diagnosis steps from each operational runbook (Section 17.7). These replace `kubectl`, `psql`, `redis-cli`, and `mc` commands with API calls.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/diagnostics/sessions/{id}` | Structured cause chain for a session |
| `GET` | `/v1/admin/diagnostics/pools/{name}` | Pool bottleneck analysis |
| `GET` | `/v1/admin/diagnostics/connectivity` | Dependency connectivity checks |
| `GET` | `/v1/admin/diagnostics/credential-pools/{name}` | Credential pool health diagnosis |

### Go Interface

```go
// pkg/ops/diagnostics/service.go

type DiagnosticService interface {
    DiagnoseSession(ctx context.Context, sessionID string) (*SessionDiagnosis, error)
    DiagnosePool(ctx context.Context, poolName string) (*PoolDiagnosis, error)
    CheckConnectivity(ctx context.Context) (*ConnectivityReport, error)
    DiagnoseCredentialPool(ctx context.Context, poolName string) (*CredentialPoolDiagnosis, error)
}
```

### Response Types

```go
type CauseChainEntry struct {
    Level     int             `json:"level"`
    Category  string          `json:"category"`  // POD_CRASH, RESOURCE_PRESSURE, OOM_KILLED,
                                                  // IMAGE_PULL_FAILURE, SETUP_COMMAND_FAILED,
                                                  // BUDGET_EXPIRED, CREDENTIAL_FAILURE, etc.
    Summary   string          `json:"summary"`
    Details   json.RawMessage `json:"details"`
    Timestamp time.Time       `json:"timestamp"`
}

type SessionDiagnosis struct {
    SessionID        string            `json:"sessionId"`
    State            string            `json:"state"`
    Runtime          string            `json:"runtime"`
    Pool             string            `json:"pool"`
    CauseChain       []CauseChainEntry `json:"causeChain"`
    RetryHistory     []RetryAttempt    `json:"retryHistory"`
    SuggestedActions []SuggestedAction `json:"suggestedActions"`
    RelatedLogs      *LogReference     `json:"relatedLogs,omitempty"`
    Degradation      *Degradation      `json:"degradation,omitempty"`  // canonical envelope (Section 25.2)
}

type PoolBottleneck struct {
    Category string          `json:"category"`  // DEMAND_EXCEEDS_SUPPLY, IMAGE_PULL,
                                                 // NODE_PRESSURE, QUOTA_EXHAUSTED,
                                                 // SETUP_FAILURE, CRD_SYNC_LAG
    Details  json.RawMessage `json:"details"`
    Summary  string          `json:"summary"`
}

type PoolDiagnosis struct {
    Pool             string            `json:"pool"`
    Status           string            `json:"status"`
    PodCounts        PodCountBreakdown `json:"podCounts"`
    Config           PoolConfigSummary `json:"config"`
    Bottleneck       *PoolBottleneck   `json:"bottleneck,omitempty"`
    SuggestedActions []SuggestedAction `json:"suggestedActions"`
    CRDSyncStatus    SyncStatus        `json:"crdSyncStatus"`
    Degradation      *Degradation      `json:"degradation,omitempty"`  // canonical envelope (Section 25.2)
}
```

When the diagnostic is served from a fallback source, the `Degradation` envelope's `actualSource` carries values like `"kubernetes"` (K8s API fallback for pod state) or `"gateway-scrape"` (per-replica `/metrics` fallback when Prometheus is unavailable). `unavailableFields` lists any fields the fallback couldn't populate (e.g., `"retryHistory"`, `"sessionMetadata"` when Postgres is down).

### Data Sources

**Session diagnosis (`DiagnoseSession`):**
1. Reads `sessions` table via `StoreRouter.SessionShard(session_id)` — gets state, terminal reason, retry count. **K8s fallback:** if Postgres is unreachable, attempts to locate the session's pod via K8s API label selector (`lenny.dev/session-id={id}`) and reads pod status directly.
2. Reads `agent_pod_state` table — gets pod exit code, OOM flag, container status. **K8s fallback:** reads pod `.status.containerStatuses[].state.terminated` for exit code and reason (including `OOMKilled`), and `.status.containerStatuses[].lastState` for previous container state.
3. Queries K8s API for `v1.EventList` on the pod — gets image pull errors, node pressure events, scheduling failures.
4. Reads retry log from session metadata in Postgres. **No fallback** — retry history is unavailable when Postgres is down.
5. Builds cause chain by cross-referencing: exit code 137 + OOM reason → `OOM_KILLED`; exit code 1 + setup phase → `SETUP_COMMAND_FAILED`; etc. The cause chain logic works with data from either Postgres or K8s API — the same fields (exit code, OOM flag, container state) are available from both sources.

**Pool diagnosis (`DiagnosePool`):**
1. Reads `agent_pod_state` table grouped by state → pod count breakdown. **K8s fallback:** lists pods via K8s API with label selector `lenny.dev/pool={name}` in the agent namespace. Pod `.status.phase` and pool-specific labels (`lenny.dev/pod-state`) provide the same state breakdown. The K8s query is slower than the indexed Postgres table (list + filter vs. `GROUP BY`) but returns the same data.
2. Reads metrics from Prometheus (`lenny_warmpool_pod_startup_duration_seconds`, `lenny_warmpool_replenishment_rate`, `lenny_warmpool_warmup_failure_total`) via `MetricSource`. Falls back to scraping all gateway replicas' `/metrics` endpoints via the headless Service if Prometheus is unreachable — point-in-time values only, summed/max'd across replicas. When using fallback, the response includes `"metricsSource": "gateway-scrape"`.
3. Reads pool config via `GatewayClient.GetPoolConfig()`.
4. Reads CRD sync status via `GatewayClient.GetPoolSyncStatus()`.
5. Classifies bottleneck: if `lenny_warmpool_warmup_failure_total{error_type="image_pull_error"} > 0` → `IMAGE_PULL`; if `lenny_warmpool_warmup_failure_total{error_type="node_pressure"} > 0` → `NODE_PRESSURE`; if `lenny_warmpool_warmup_failure_total{error_type="resource_quota_exceeded"} > 0` → `QUOTA_EXHAUSTED`; if replenishment rate < claim rate → `DEMAND_EXCEEDS_SUPPLY`.

**Connectivity (`CheckConnectivity`):**
`lenny-ops` runs parallel dependency probes (Postgres, Redis, MinIO, K8s API) with 2s timeouts. Additionally, it probes the gateway admin API itself (`GET /v1/admin/health/summary`) — if the gateway is unreachable, this appears in the connectivity report as a failed dependency. It also reads registered connectors via `GatewayClient.ListConnectors()` and probes each. This tests the gateway from the outside (real network path).

**Credential pool diagnosis (`DiagnoseCredentialPool`):**
1. Reads credential pool state via `GatewayClient.GetCredentialPool()`.
2. Reads credential metrics from Prometheus via `MetricSource` (`lenny_credential_pool_utilization`, `lenny_credential_provider_rate_limit_total`). Same fallback behavior as pool diagnosis.
3. Identifies hot keys (credentials with highest rate-limit event count in 24h window).
4. Computes utilization trend.

### Degradation

All degraded responses use the canonical `degradation` envelope (Section 25.2). Cases:

**Postgres unreachable:** Session and pool diagnostics fall back to the K8s API for pod state and return partial results (HTTP 207 `DIAGNOSTICS_PARTIAL`). The `degradation` envelope reports `actualSource: "kubernetes"`, `primarySource: "postgres"`, and `unavailableFields: ["retryHistory", "sessionMetadata"]`. The cause chain is built from K8s pod status — exit codes, OOM flags, and container states are available from the K8s API. Connectivity check still runs and reports Postgres as unreachable.

**K8s API unreachable:** Session diagnosis omits pod events; `degradation.unavailableFields` includes `"podEvents"` (HTTP 207 `DIAGNOSTICS_PARTIAL`). Pool diagnosis cannot fall back for pod counts.

**Both Postgres and K8s API unreachable:** Session and pool diagnostics return `503`. Connectivity check still runs and reports both as unreachable.

**Prometheus unreachable:** Pool and credential diagnostics fall back to scraping all gateway replicas' `/metrics` via headless Service; `degradation.actualSource: "gateway-scrape"`.

**Gateway unreachable:** Pool config and connector probes fail; diagnostics return partial results (207) with `degradation.unavailableFields: ["config", "connectors"]`.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_diagnostics_request_duration_seconds` | Histogram | `endpoint` | Per-diagnostic-endpoint latency |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `SESSION_NOT_FOUND` | `PERMANENT` | 404 | Session ID not found in any shard |
| `POOL_NOT_FOUND` | `PERMANENT` | 404 | Pool name not registered |
| `CREDENTIAL_POOL_NOT_FOUND` | `PERMANENT` | 404 | Credential pool not found |
| `DIAGNOSTICS_PARTIAL` | `TRANSIENT` | 207 | Some data sources unavailable; partial result |

### Audit Events

`diagnostics.session_diagnosed`, `diagnostics.pool_diagnosed`, `diagnostics.connectivity_checked`, `diagnostics.credential_pool_diagnosed`.

### Auto-remediation (`fix=true` mode)

The diagnostic suite can run in a read-only mode (default) or an auto-remediation mode that applies a narrow set of safe, idempotent fixes and reports what it did. Agents and operators invoke this via `lenny-ctl doctor --fix` (Section 24.2) or the REST endpoint:

```
POST /v1/admin/diagnostics/run?fix=true
Content-Type: application/json

{
  "findings": ["coreDnsStuckEndpoint", "bootstrapConfigDrift", "certManagerExpiring"]
}
```

Response is a long-running operation envelope (Section 25.2 Progress Envelope). The `operationId` correlates all log lines, audit events, and progress updates for the fix run.

**Fixable findings and what the fix does:**

| Finding code | Detection | Remediation | Idempotent? |
|--------------|-----------|-------------|-------------|
| `coreDnsStuckEndpoint` | CoreDNS Service `.subsets[].addresses` out of sync with Ready pods | Rolling restart of CoreDNS Deployment (`kubectl rollout restart -n kube-system deployment/coredns` equivalent via controller-runtime client); waits for Ready | Yes |
| `bootstrapConfigDrift` | ConfigMap `lenny-bootstrap` hash differs from Helm-rendered value (release annotation) | Re-applies the Helm-rendered ConfigMap; does NOT restart gateway (reload is watch-driven) | Yes |
| `certManagerExpiring` | Certificate within 7 days of expiry and cert-manager healthy | Annotates Certificate with `cert-manager.io/issue-temporary-certificate: "true"` and deletes the Secret to force re-issuance | Yes |
| `prometheusRuleMissing` | `monitoring.enabled=true` but no PrometheusRule/ServiceMonitor in the release namespace | Re-applies the Helm-rendered `monitoring.yaml` template | Yes |
| `warmPoolStuckReplenish` | Pool status `DEMAND_EXCEEDS_SUPPLY` with zero in-flight warm-up claims for > 5m | Bumps pool generation (triggers controller to re-drive) | Yes |

**Non-fixable findings** (returned as read-only recommendations): any finding not in this table — including anything that requires destructive action, credential rotation, or schema migration — is reported with `remediation: "manual"` and a pointer to the runbook.

**Guardrails:**
- Each fix has a per-operation timeout (default 120s) configurable via `admin.doctor.fixTimeoutSeconds`.
- Fixes never run when `global.maintenanceMode=true`.
- Fixes never run against components whose `lenny.dev/doctor-optout: "true"` annotation is set.
- The set of fixable findings is gated by `admin.doctor.allowedFixes` (Helm value, defaults to the full list above). Operators can narrow the list per environment.

**Audit events:**

| Event | When | Fields |
|-------|------|--------|
| `diagnostics.fix_started` | Fix run begins | `operationId`, `findings`, `principal` |
| `diagnostics.fix_applied` | Individual finding fixed | `operationId`, `finding`, `resource`, `result: "applied"` |
| `diagnostics.fix_skipped` | Finding not applied (guardrail) | `operationId`, `finding`, `reason` |
| `diagnostics.fix_failed` | Fix attempted and failed | `operationId`, `finding`, `error` |
| `diagnostics.fix_completed` | Run finishes | `operationId`, `appliedCount`, `skippedCount`, `failedCount` |

---

## 25.7 Operational Runbooks

### Rationale

AI agents are capable of reading structured prose, understanding diagnostic steps, and making the appropriate API calls. A machine-executable runbook format (YAML decision trees, step-by-step state machines) would reimplement — less flexibly — what an LLM with tool access already does well. Instead, the runbooks defined in Section 17.7 are written as structured markdown that serves both human operators and AI agents. The Diagnostics API (Section 25.6) and admin API give agents the tools to act on what the runbooks describe.

### Runbook Authoring Guidelines

Each runbook in `docs/runbooks/` follows the existing three-part structure (Trigger → Diagnosis → Remediation) with additional conventions that make them agent-usable:

**1. Structured front matter.** Each runbook starts with YAML front matter that agents and the runbook index can parse for discovery:

```markdown
---
triggers:
  - alert: WarmPoolLow
    severity: warning
  - alert: WarmPoolExhausted
    severity: critical
components:
  - warmPools
symptoms:
  - "session creation returns RUNTIME_UNAVAILABLE"
  - "idle pod count drops to zero"
  - "warm pool replenishment stalls"
tags:
  - scaling
  - pods
  - warm-pool
  - capacity
requires:
  - admin-api          # lenny-ctl / REST API access
  - cluster-access     # kubectl (optional, for fallback steps)
related:
  - docs/runbooks/gateway-replica-failure.md
---
```

Field descriptions:
- `triggers` — links runbooks to alerts. When an agent receives an `alert_fired` event, it matches the alert name to find the relevant runbook.
- `components` — maps to the component names used in the health API response (`warmPools`, `postgres`, `redis`, `gateway`, `objectStore`, `certManager`, `credentialPools`, `controllers`, `circuitBreakers`). When the health API reports a component as degraded/unhealthy, the agent finds runbooks by component.
- `symptoms` — human-readable descriptions of observable problems. Useful for agents responding to unstructured requests ("sessions are failing") — the agent can scan symptom strings for relevance.
- `tags` — free-form labels for broader topic matching and chained diagnosis (e.g., an agent following one runbook that mentions credential issues can search for `tag=credentials`).
- `requires` — access levels used in the runbook's steps. Agents skip steps they cannot execute.
- `related` — pointers to related runbooks for chained diagnosis.

**2. Multi-path steps with structured access markers.** Each diagnosis and remediation step provides commands at multiple access levels. To enable agent parsing without an LLM-only pass, each access level is preceded by an HTML comment that machine consumers parse. Below is the literal markdown source of an example step (using four-tilde fences for the outer block to avoid nested-fence confusion):

~~~~markdown
### Step 1: Check pool status

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose pool <pool-name>
```

<!-- access: api method=GET path=/v1/admin/diagnostics/pools/{name} -->
```
GET /v1/admin/diagnostics/pools/<pool-name>
```

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandboxes -n lenny-agents -l pool=<pool-name>
kubectl describe sandbox <pod-name> -n lenny-agents
```
~~~~

The `<!-- access: ... -->` comment lines are invisible to humans reading the rendered markdown (they're HTML comments) but are parsed by `lenny-ops`'s runbook indexer. The indexer extracts the structured form and exposes it via:

- `GET /v1/admin/runbooks/{name}` — full markdown content (unchanged).
- `GET /v1/admin/runbooks/{name}/steps` — structured representation:
  ```json
  {
    "steps": [
      {
        "id": "step-1",
        "title": "Check pool status",
        "paths": [
          {"access": "lenny-ctl", "command": "lenny-ctl diagnose pool <pool-name>"},
          {"access": "api", "method": "GET", "path": "/v1/admin/diagnostics/pools/{name}"},
          {"access": "kubectl", "requires": "cluster-access", "commands": ["kubectl get sandboxes ...", "kubectl describe sandbox ..."]}
        ]
      }
    ]
  }
  ```

Agents querying `/steps` can iterate steps and select the access path matching their `requires` capability without parsing markdown. Agents that prefer LLM-style consumption can still use the full markdown.

An external watchdog agent with only API access uses the `api` path. A cluster admin with `kubectl` can use any. A human operator picks whichever they prefer.

**3. Decision points as prose, not code.** Instead of a YAML decision tree, runbooks describe the decision logic in natural language that both humans and agents can follow:

```markdown
### Decision

- If the bottleneck is **quota exhaustion** (diagnosis shows `quotaExhausted: true`):
  escalate to cluster admin — ResourceQuota prevents pod creation.
- If the bottleneck is **image pull failure** (diagnosis shows `imagePullHealthy: false`):
  check registry credentials and image digest (Step 3a).
- If the bottleneck is **demand exceeds supply** (idle count is 0, no failures):
  scale the warm pool (Step 3b).
```

**4. Expected outcomes.** Each remediation step states what success looks like, so the agent can verify:

```markdown
### Step 3b: Scale the warm pool

**Via lenny-ctl:**
```bash
lenny-ctl admin pools set-warm-count --pool <pool-name> --min <current + 10>
```

**Via API:**
```
PUT /v1/admin/pools/<pool-name>/warm-count
{"minWarm": <current + 10>}
```

**Expected outcome:** Within 2 minutes, `lenny-ctl diagnose pool <pool-name>` shows
`idle > 0` and the `WarmPoolExhausted` alert resolves.
```

**5. Escalation criteria.** Runbooks explicitly state when to stop and escalate, rather than embedding this in a state machine:

```markdown
### Escalation

Escalate if:
- Pool does not recover within 5 minutes after scaling.
- The root cause is node resource pressure or quota exhaustion (requires cluster admin).
- The same pool has exhausted 3+ times in 24 hours (indicates structural undersizing).
```

### Runbook Discovery

There are three complementary discovery paths. Together they cover: automated response to alerts, health-driven diagnosis, and open-ended human/agent queries.

#### Path A: Index API with Rich Filtering

Runbooks are version-controlled in `docs/runbooks/` alongside the platform code. `lenny-ops` provides a read-only index endpoint:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/runbooks` | List all runbooks with full front matter |
| `GET` | `/v1/admin/runbooks/{name}` | Get full runbook content (rendered markdown) |

**Query parameters for filtering** (all optional, combinable):

| Parameter | Example | Matches against |
|---|---|---|
| `alert` | `?alert=WarmPoolExhausted` | `triggers[].alert` |
| `component` | `?component=warmPools` | `components[]` |
| `tag` | `?tag=scaling` | `tags[]` |
| `requires` | `?requires=admin-api` | `requires[]` (filter to runbooks the agent can execute) |
| `q` | `?q=image+pull+failure` | Full-text search across `symptoms[]`, `tags[]`, and runbook title |

When no filters are provided, the endpoint returns all runbooks — the full index. The set is small (currently 15 runbooks), so returning everything is cheap and allows agents to make their own relevance decisions.

**Go Interface:**

```go
// pkg/ops/runbooks/index.go

type RunbookIndex interface {
    List(ctx context.Context, filter RunbookFilter) ([]RunbookSummary, error)
    Get(ctx context.Context, name string) (*Runbook, error)
}

type RunbookFilter struct {
    Alert     string   // match triggers[].alert
    Component string   // match components[]
    Tag       string   // match tags[]
    Requires  string   // match requires[]
    Query     string   // full-text search across symptoms, tags, title
}

type RunbookSummary struct {
    Name       string         `json:"name"`
    Title      string         `json:"title"`
    Triggers   []AlertTrigger `json:"triggers"`
    Components []string       `json:"components"`
    Symptoms   []string       `json:"symptoms"`
    Tags       []string       `json:"tags"`
    Requires   []string       `json:"requires"`
    Related    []string       `json:"related"`
}

type Runbook struct {
    RunbookSummary
    Content string `json:"content"`  // full markdown
}
```

The index is built at startup by scanning the bundled `docs/runbooks/*.md` files and parsing their front matter. No Postgres storage — runbooks are read-only artifacts shipped with the binary. The `q` parameter does substring matching against symptoms and tags — not a search engine, just enough for agents to narrow by keyword.

#### Path B: Health API Links to Runbooks

When the health API (Section 25.3) returns a degraded or unhealthy component, the `suggestedAction` object includes a `runbook` field:

```json
{
  "status": "degraded",
  "components": {
    "warmPools": {
      "status": "unhealthy",
      "pools": [
        {
          "name": "default-gvisor",
          "status": "unhealthy",
          "idle": 0, "warming": 2, "claimed": 18, "minWarm": 5,
          "issue": "WARM_POOL_EXHAUSTED",
          "since": "2026-04-08T14:22:00Z",
          "suggestedAction": {
            "action": "SCALE_WARM_POOL",
            "endpoint": "PUT /v1/admin/pools/default-gvisor/warm-count",
            "body": { "minWarm": 15 },
            "reasoning": "Pool exhausted for 8 minutes. Peak claim rate: 4.2/min.",
            "runbook": "warm-pool-exhaustion"
          }
        }
      ]
    }
  }
}
```

The `runbook` field is the runbook name as used in `GET /v1/admin/runbooks/{name}`. This closes the loop for the most common automated path: agent calls health API → sees degraded component with suggested action → fetches the linked runbook for full context → executes the appropriate steps.

The mapping from issue/component to runbook is maintained in the gateway's health service as a simple lookup table:

```go
// pkg/gateway/health/runbook_links.go

var issueRunbooks = map[string]string{
    "WARM_POOL_EXHAUSTED":      "warm-pool-exhaustion",
    "WARM_POOL_LOW":            "warm-pool-exhaustion",
    "CREDENTIAL_POOL_EXHAUSTED":"credential-pool-exhaustion",
    "POSTGRES_UNREACHABLE":     "postgres-failover",
    "REDIS_UNREACHABLE":        "redis-failure",
    "MINIO_UNREACHABLE":        "minio-failure",
    "CERT_EXPIRY_IMMINENT":     "cert-manager-outage",
    "CIRCUIT_BREAKER_OPEN":     "gateway-replica-failure",
}
```

If no runbook matches the issue, the `runbook` field is omitted. The gateway does not need to validate that the runbook exists in `lenny-ops` — the mapping is maintained by convention and version-controlled alongside the runbooks.

Similarly, `alert_fired` events in the operational event stream (Section 25.5) include a `runbook` field when the alert's `runbook` annotation is set (standard Prometheus alerting rule annotation):

```json
{
  "specversion": "1.0",
  "id": "01HN7Y0QW6S7X9ZP8M2F5K4R3B",
  "source": "//lenny.dev/gateway/gw-7f4c2a1e",
  "type": "dev.lenny.alert_fired",
  "time": "2026-04-17T14:32:08Z",
  "datacontenttype": "application/json",
  "data": {
    "severity": "critical",
    "alertName": "WarmPoolExhausted",
    "labels": { "pool": "default-gvisor" },
    "runbook": "warm-pool-exhaustion"
  }
}
```

#### Path C: Fetch the Full List

The runbook index is small. An agent can call `GET /v1/admin/runbooks` with no filters and read all summaries (name, title, triggers, components, symptoms, tags) in a single response. For an LLM-based agent, scanning 15 summaries to pick the right one is trivial — no structured matching needed.

This is the fallback for unstructured requests. A human says "sessions are slow for tenant X." The agent fetches the full index, scans the symptoms and tags, and decides which runbooks are relevant. No query parameter can capture this kind of fuzzy matching as well as the agent itself.

#### Discovery Flow Summary

```
Agent receives alert_fired event
  → event.payload.runbook is set?
    → yes: GET /v1/admin/runbooks/{name} — done
    → no:  GET /v1/admin/runbooks?alert={alertName} — match by trigger

Agent reads health API, sees degraded component
  → suggestedAction.runbook is set?
    → yes: GET /v1/admin/runbooks/{name} — done
    → no:  GET /v1/admin/runbooks?component={component} — match by component

Agent receives unstructured request or follows chained diagnosis
  → GET /v1/admin/runbooks?q={keywords} — search by symptom/tag
  → or GET /v1/admin/runbooks — fetch all, pick by relevance
```

---

## 25.8 Platform Lifecycle Management

APIs for version introspection (aggregated), upgrade orchestration, and configuration management.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/platform/version/full` | Full version report (aggregates gateway + K8s + Postgres) |
| `GET` | `/v1/admin/platform/upgrade-check` | Check for available upgrades |
| `POST` | `/v1/admin/platform/upgrade/preflight` | Validate upgrade safety |
| `POST` | `/v1/admin/platform/upgrade/start` | Begin platform upgrade |
| `POST` | `/v1/admin/platform/upgrade/proceed` | Advance to next phase |
| `POST` | `/v1/admin/platform/upgrade/pause` | Pause upgrade |
| `POST` | `/v1/admin/platform/upgrade/rollback` | Rollback upgrade |
| `GET` | `/v1/admin/platform/upgrade/status` | Current upgrade state |
| `POST` | `/v1/admin/platform/upgrade/verify` | Post-upgrade health verification |
| `GET` | `/v1/admin/platform/config/diff` | Compare running config vs. desired state |
| `PUT` | `/v1/admin/platform/config` | Apply a runtime config change |
| `GET` | `/v1/admin/platform/registry` | Current registry configuration |
| `PUT` | `/v1/admin/platform/registry` | Update registry configuration |

### Image Registry Configuration

All Lenny components pull container images from a configurable registry. Deployers configure the registry once; the upgrade system, warm pool controller, and backup jobs all resolve image references through it.

#### Helm Values

```yaml
platform:
  registry:
    # Base registry URL. All Lenny component images are resolved relative to this.
    # Default: ghcr.io/lennylabs
    url: "ghcr.io/lennylabs"

    # Optional: override per component. Useful when some images are mirrored
    # to a different path than others.
    overrides:
      # gateway: "my-registry.internal/lenny/gateway"
      # ops: "my-registry.internal/lenny/ops"
      # controllers: "my-registry.internal/lenny/controllers"
      # backup: "my-registry.internal/lenny/backup"

    # Pull secret for private registries.
    # References an existing K8s Secret of type kubernetes.io/dockerconfigjson.
    pullSecretName: ""

    # If true, require digest-pinned references (no mutable tags).
    # Recommended for production. Default: false (allows tags for dev convenience).
    requireDigest: false
```

#### Image Resolution

All image references are resolved through a central `ImageResolver`:

```go
// pkg/common/registry/resolver.go

type ImageResolver struct {
    baseURL    string
    overrides  map[string]string  // component → full registry/path
    requireDigest bool
}

// Resolve returns the full image reference for a component at a given version.
// version can be a tag ("1.5.0") or a digest ("sha256:abc123...").
// If an override exists for the component, it is used as the base instead of baseURL.
func (r *ImageResolver) Resolve(component, version string) (string, error)

// Components:
//   "gateway"     → {baseURL}/lenny-gateway:{version}
//   "ops"         → {baseURL}/lenny-ops:{version}
//   "controllers" → {baseURL}/lenny-controllers:{version}
//   "backup"      → {baseURL}/lenny-backup:{version}
```

`ImageResolver` is shared by the upgrade system, the warm pool controller (for agent pod images — those use runtime-defined image references, not the platform registry), and backup job creation. Deployers who mirror Lenny images to an internal registry configure `platform.registry.url` once and all components resolve correctly.

#### Runtime API

`GET /v1/admin/platform/registry` returns the effective registry configuration (pull secret name is included, secret contents are not). `PUT /v1/admin/platform/registry` updates the registry URL and overrides at runtime (stored in Postgres, takes effect on next image resolution). This is a restart-free setting.

### Version Aggregation

`GET /v1/admin/platform/version/full` aggregates:
- Gateway binary metadata from `GatewayClient.GetVersion()` (calls `GET /v1/admin/platform/version` on the gateway).
- `lenny-ops` binary metadata (local — compiled-in via `ldflags`).
- Controller Deployment versions from K8s API.
- CRD versions from K8s API.
- Helm chart version from K8s API (`helm.sh/release.v1` Secret).
- Postgres schema version from `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`.

When any component's current version does not match the compiled-in required version, the response includes `"versionDrift": true` and each drifted component includes `"drift": true` and `"requiredAction"`.

### Upgrade Check

`GET /v1/admin/platform/upgrade-check` queries a configurable release channel endpoint (`platform.upgradeChannel` Helm value, default: `https://releases.lenny.dev/v1/latest`). Deployers can point this at an internal mirror or disable it (`platform.upgradeChannel: ""` disables). The check runs periodically (every 6 hours) and the result is cached in Postgres. A `platform_upgrade_available` operational event is emitted when a new version is detected.

#### Release Channel Response

```json
{
  "version": "1.5.0",
  "images": {
    "gateway": "lenny-gateway:1.5.0",
    "ops": "lenny-ops:1.5.0",
    "controllers": "lenny-controllers:1.5.0",
    "backup": "lenny-backup:1.5.0"
  },
  "digests": {
    "gateway": "sha256:a1b2c3...",
    "ops": "sha256:d4e5f6...",
    "controllers": "sha256:g7h8i9...",
    "backup": "sha256:j0k1l2..."
  },
  "minUpgradeFrom": "1.3.0",
  "schemaVersion": 42,
  "crdVersion": "v1beta2",
  "releaseNotes": "https://github.com/lennylabs/lenny/releases/tag/v1.5.0"
}
```

The `images` field contains image names relative to the release channel's own registry. The upgrade system resolves them through `ImageResolver` — prefixing with the deployer's configured `platform.registry.url` (or per-component override). This means the release channel does not dictate the registry — it provides version identifiers, the deployer's registry config determines where images are pulled from.

The `digests` field provides content-addressable references. When `platform.registry.requireDigest` is true, the upgrade system uses digests instead of tags. Deployers mirroring to a private registry must ensure the digests match (standard for `crane copy`, `skopeo copy`, etc.).

#### Release Channel Service Details

- **Endpoint contract.** The release channel serves a GET-only HTTP endpoint returning the JSON structure above. Request includes query params `?currentVersion=1.4.3` (for personalized `minUpgradeFrom` — the service may refuse to advertise a newer release if the current version is below a hard prerequisite) and `?channel=stable|beta` (selects release track).
- **Signing.** Responses are signed with an Ed25519 signature in a `X-Lenny-Release-Signature` response header. The Lenny release-channel public key is compiled into `lenny-ops`. Operators running a mirror that re-signs with their own key can override via `platform.releaseChannel.publicKeyPath`.
- **SLA.** The `releases.lenny.dev` service is operated by the Lenny project with a target of 99.9% monthly availability. Unreachable periods do not break running Lenny deployments — only the upgrade-check feature degrades.
- **Unreachable behavior.** When the channel is unreachable, `GET /v1/admin/platform/upgrade-check` returns the cached response from `platform_upgrade_check_cache` with `"cached": true`, `"cacheAge": "..."`, and a `degradation.warnings` entry noting the channel was unreachable at the last check attempt. If the cache is empty (first check after install with channel unreachable), the endpoint returns 503 `UPGRADE_CHANNEL_UNREACHABLE`.
- **Caching.** Responses are cached in Postgres (`platform_upgrade_check_cache.ttl_seconds`, default 21600 — 6 hours). The check cron runs hourly even when the cache has not expired (to detect channel recovery after outages).
- **Disabling.** Set `platform.upgradeChannel: ""` to disable all channel queries. Upgrade commands still work — operators pass `version` and `images` explicitly to `POST /v1/admin/platform/upgrade/start`.

#### Air-Gapped Support

For deployments without internet access, operators host their own release channel or bypass it entirely:

1. **Mirror the release channel.** Copy Lenny release artifacts to an internal HTTP endpoint serving the same JSON contract. Point `platform.upgradeChannel` at the mirror URL. Re-sign with an operator-held Ed25519 key if response signing is desired (configure `platform.releaseChannel.publicKeyPath`).
2. **Skip the channel.** Set `platform.upgradeChannel: ""`. Deployers invoke `POST /v1/admin/platform/upgrade/start` with explicit `version` and `images` derived from whichever process the operator uses to track Lenny releases.
3. **Mirror images.** Copy Lenny component images to an internal registry using `crane copy` or `skopeo copy`. Set `platform.registry.url` and `platform.registry.overrides` to point at the internal registry. Set `platform.registry.requireDigest: true` to defend against tag mutation in the mirror.
4. **Preserve digests.** The mirror must preserve image digests so `ImageResolver` can verify digest matches (standard behavior for `crane copy` and `skopeo copy`).
5. **CRD and schema assets.** CRDs and migration SQL are compiled into the `lenny-ops` binary, not fetched from the release channel, so no additional air-gap steps are needed for schema/CRD updates.

The end-to-end air-gap install procedure is documented in `docs/deployment/air-gap.md` (not part of this spec — operator-facing deployment guide).

### Cert-Manager Integration

`lenny-ops` and the gateway Ingress require TLS certificates for external access. The Helm chart supports three models:

| Model | Configuration | Lifecycle |
|-------|---------------|-----------|
| **cert-manager ClusterIssuer** (recommended) | Set `ops.ingress.annotations["cert-manager.io/cluster-issuer"]` to the ClusterIssuer name. Leave `ops.ingress.tlsSecretName` empty — cert-manager creates and manages the Secret. | cert-manager handles renewal. Requires cert-manager deployed separately (not bundled by Lenny). |
| **Deployer-provided Secret** | Create a TLS Secret externally; set `ops.ingress.tlsSecretName` to its name. | Operator manages renewal. |
| **Self-signed (dev only)** | Helm chart generates a self-signed cert when `ops.ingress.selfSigned: true`. | No renewal — regenerated on chart upgrade. Not for production. |

**cert-manager is not bundled.** Lenny assumes operators running cert-manager already have it deployed (it's a common CNCF project). The Helm chart renders `cert-manager.io/cluster-issuer` annotations on the Ingress when `ops.ingress.annotations` includes them. `lenny-preflight` warns (non-blocking) if `cert-manager.io/cluster-issuer` is configured but no ClusterIssuer by that name exists.

**Expected ClusterIssuer configuration:**

```yaml
# Example (operator-provided) — acme-production ClusterIssuer
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: acme-production
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    privateKeySecretRef: { name: acme-key }
    solvers: [ { http01: { ingress: { class: nginx } } } ]
```

`lenny-ops`'s health API probes cert-manager's certificate status (Section 25.3, Data Sources). The `certManager` component in the health response reports:
- `healthy` — all Lenny-managed certificates are renewed and valid for >30 days.
- `degraded` — at least one certificate expires within 30 days AND renewal has failed in the last attempt.
- `unhealthy` — at least one certificate expires within 7 days OR has already expired.

The `CertExpiryImminent` alert (Section 25.13) fires based on these signals. The runbook (Section 25.7) for `cert-manager-outage` handles the common remediation paths.

**Operator model.** Renewal is cert-manager's responsibility; Lenny only observes and reports. Runbook remediation steps reference cert-manager's own tooling (`kubectl describe certificate`, cert-manager's Prometheus metrics).

### Bootstrapping Lenny-Ops Monitoring

External agents and deployer Prometheus need to reach `lenny-ops` for its own health and metrics. The chart supports two patterns:

**Pattern 1: Prometheus scrapes `lenny-ops` directly** (recommended for deployer Prometheus in the same cluster). The chart renders a PodMonitor/ServiceMonitor (depending on `monitoring.format`) that selects the `lenny-ops` Pods on metrics port 9090. NetworkPolicy is rendered to allow ingress on port 9090 from the `monitoring.namespace`. No Ingress needed for metrics scraping — it happens internally.

**Pattern 2: External agents poll `/v1/admin/ops/health` via Ingress.** External DevOps agents (outside the cluster) use the same Ingress as all other `lenny-ops` API traffic. The Ingress is configured via `ops.ingress.host`. Agents discover this hostname from the gateway's `GET /v1/admin/platform/version` response (`opsServiceURL` field, Section 25.14 `lenny-ctl` auto-discovery).

**Bootstrap scenarios:**

- **Fresh install, Prometheus in the cluster:** chart renders PodMonitor; Prometheus discovers and scrapes `lenny-ops`. No manual wiring.
- **Fresh install, external Prometheus (managed service, Grafana Cloud, etc.):** operator configures the external Prometheus to scrape `lenny-ops` through a federation endpoint or remote-write pull. The chart does not auto-configure external Prometheus.
- **Gateway unavailable at agent bootstrap:** external agents can't call `GET /v1/admin/platform/version` to discover the ops hostname. Operators pre-configure agents with the ops hostname from their deployment config (same place the Helm `ops.ingress.host` lives). The gateway version endpoint is the preferred discovery path but not the only one.

`kubectl` users can always port-forward directly: `kubectl port-forward -n lenny-system svc/lenny-ops 8090`. This bypasses the Ingress and NetworkPolicy (port-forward establishes an API-server-to-pod tunnel) and is documented as the break-glass access method in Section 25.15, Total-Outage Recovery.

### Upgrade State Machine

```
Preflight → OpsRoll → CRDUpdate → SchemaMigration → GatewayRoll → ControllerRoll → Verification → Complete
                                                                                                     ↗
Any non-terminal state → Paused ──────────────────────────────────────────────────────────────────→ (resume)
Any pre-SchemaMigration state → RolledBack
```

`lenny-ops` upgrades **first**. This is critical: CRD manifests and database migrations are compiled into the `lenny-ops` binary. The old `lenny-ops` cannot apply new CRDs or run new migrations — it doesn't have them. By rolling `lenny-ops` first, the new binary takes over and orchestrates the rest of the upgrade with the correct assets.

**Progress.** `GET /v1/admin/platform/upgrade/status` and the Operations Inventory (Section 25.4) return the canonical `progress` envelope (Section 25.2). `totalSteps` is 7 (one per phase); `currentStep` is the phase name; `etaSeconds` uses `etaMethod: "fixed_phase_durations"` (per-phase hard-coded durations) combined with `historical_p50` when `ops_operation_baselines` has samples. SchemaMigration nests its own sub-progress (migration N of M across S shards) into `currentStepDetail`. The `operation_progressed` event fires on every phase transition.

#### Phase Details

**1. Preflight** (old `lenny-ops`):
- Validates current platform health is `healthy` (calls gateway `GET /v1/admin/health/summary`).
- Validates no other upgrade is in progress (checks `platform_upgrade_state` singleton).
- Validates current version meets `minUpgradeFrom` constraint.
- Resolves all target images through `ImageResolver` and validates they are pullable. For each component, issues a HEAD request to the registry manifest endpoint (or `crane manifest --platform linux/amd64` equivalent). This catches missing mirrors before any changes are made.
- Validates Postgres has enough free connections for migration.
- Writes upgrade state to Postgres: `current_phase: "OpsRoll"`, `target_version`, `target_images` (resolved full references).
- Returns the upgrade plan as a preview.

**2. OpsRoll** (old `lenny-ops` → new `lenny-ops`):
- Old `lenny-ops` patches its own Deployment's image tag via K8s API to the resolved `ops` image reference. The patch is a **strategic merge patch** using the digest form when `platform.registry.requireDigest: true` (digests are stable across registry mutations).
- Old `lenny-ops` pod terminates as K8s rolls the Deployment (RollingUpdate strategy, `maxUnavailable: 0`, `maxSurge: 1`).
- New `lenny-ops` pod starts. On startup, it checks `platform_upgrade_state`. If `current_phase` is `OpsRoll` and `target_version` matches its own compiled-in version, it advances to `CRDUpdate`.
- **OpsRoll timeout (10 minutes).** If the upgrade stays in `OpsRoll` for longer than `platform.upgrade.opsRollTimeoutSeconds` (default 600), the old pod (if still running) detects the timeout via a watchdog goroutine and automatically rolls back: it re-patches its Deployment to the previous image reference (stored in `platform_upgrade_state.metadata.previousImages.ops`) and sets `current_phase: "RolledBack"` with error `OPS_ROLL_TIMEOUT`. Without this timeout, an upgrade stuck on an image pull failure would hang until the `PlatformUpgradeStuck` alert fires (default 1h).
- **Image pull failure event.** When the old pod detects that the new pod is stuck in `ImagePullBackOff` or `CrashLoopBackOff` (observed via K8s pod watch with a 60s observation window), it emits `platform_upgrade_image_pull_failed` with the pod description (image reference, failure reason). This surfaces the concrete failure to the agent before the timeout triggers automatic rollback.
- **Observability during OpsRoll.** Upgrade state transitions to `target_phase: "CRDUpdate"` only when the new pod writes an `ops_healthy` heartbeat to Postgres (`platform_upgrade_state.metadata.opsRollHeartbeat`). If the heartbeat is not written within the timeout, the rollback logic above kicks in.

**3. CRDUpdate** (new `lenny-ops`):
- Applies CRD manifests compiled into the new binary via K8s server-side apply.
- Waits for CRDs to be established (`status.conditions[type=Established]`).

**4. SchemaMigration** (new `lenny-ops`):
- Triggers a Postgres backup (Section 25.11) and records the backup ID in `platform_upgrade_state.pre_upgrade_backup_id`. Blocks until backup completes.
- **Multi-shard migration semantics:** migrations are run **serially against each shard** returned by `StoreRouter.AllSessionShards()` plus `StoreRouter.PlatformPostgres()`. The order is: platform shard first, then session shards in deterministic (sorted) order. If any shard's migration fails, the upgrade is **paused** in `SchemaMigration` state with `failed_shard` recorded in `platform_upgrade_state.metadata`. The agent can either:
  - Fix the failed shard (manual intervention, e.g., resolve a locking conflict) and call `POST /v1/admin/platform/upgrade/proceed` — the migration resumes from the failed shard (successful shards are skipped because migrations are idempotent).
  - Roll back: `POST /v1/admin/platform/upgrade/rollback` — but because some shards already migrated, rollback requires restore from the pre-migration backup (`details.requiresRestore: true`, `details.partialFailure: true`).
- **Migrations must be idempotent.** The migration framework requires every migration to be re-runnable (enforced by tests). This is the safety net for partial-failure recovery.
- Validates schema version matches expected post-migration version on every shard after success.

**5. GatewayRoll** (new `lenny-ops`):
- Patches the gateway Deployment's image tag to the resolved `gateway` image reference via K8s strategic merge patch (same rules as OpsRoll).
- Ensures Deployment update strategy is `RollingUpdate` with `maxUnavailable: 0, maxSurge: 1` before patching (patches strategy if not set).
- Waits for rollout completion (`status.updatedReplicas == status.replicas && status.unavailableReplicas == 0`).
- Same image-pull failure observation as OpsRoll; timeout is `platform.upgrade.gatewayRollTimeoutSeconds` (default 1200 — 20 minutes, accounting for warm-pool drain and re-warm time).

**6. ControllerRoll** (new `lenny-ops`):
- Patches WarmPoolController and PoolScalingController Deployments similarly.
- Waits for rollout completion. Timeout: `platform.upgrade.controllerRollTimeoutSeconds` (default 600).

**7. Verification** (new `lenny-ops`):
- Calls `GET /v1/admin/health` on the gateway — must return `healthy`.
- Calls `GET /v1/admin/diagnostics/connectivity` on itself — all dependencies must pass.
- Validates all component versions match `target_version` via `GET /v1/admin/platform/version/full`.
- On success: sets `current_phase: "Complete"`, emits `platform_upgrade_completed` event.
- On failure: sets `current_phase: "Paused"` and emits `platform_upgrade_verification_failed` event. The operator or watchdog agent decides whether to retry verification or rollback.

#### Rollback

Rollback is available before `SchemaMigration` completes. The rollback path reverses the completed phases:
- `ControllerRoll`/`GatewayRoll` → patch Deployments back to previous image references (stored in `platform_upgrade_state.metadata.previousImages`).
- `CRDUpdate` → CRDs are generally backwards-compatible; no rollback needed. If a CRD version introduced breaking changes, the rollback endpoint returns `409 UPGRADE_ROLLBACK_MANUAL_CRD` with instructions.
- `OpsRoll` → patches `lenny-ops` Deployment back to previous image. Old `lenny-ops` resumes.

After `SchemaMigration` completes, the database schema may be incompatible with the old binaries. Rollback returns `409 UPGRADE_ROLLBACK_UNAVAILABLE` with `details.requiresRestore: true` and `details.backupId` pointing to the pre-migration backup.

**Drift snapshot cleanup on rollback.** When rollback completes (state transitions to `RolledBack`), `lenny-ops` deletes the `bootstrap_seed_snapshot_target` row for this upgrade (matched by `upgrade_id`), if one was written. The target snapshot is written by the new `lenny-ops` early in OpsRoll (Section 25.10), so:
- Rollback during **Preflight** is a no-op for the snapshot — none was written. The DELETE matches zero rows; this is expected.
- Rollback during **OpsRoll or later phases** deletes the target snapshot row.
After this point, `GET /v1/admin/drift?against=target` returns `404 DRIFT_NO_TARGET_SNAPSHOT` until a new upgrade starts. Without this cleanup, drift detection would keep comparing against an aborted upgrade's desired state, misleading operators about what the platform is supposed to look like.

#### Pausing and Resuming

The state machine pauses between phases and requires `POST /v1/admin/platform/upgrade/proceed` to advance. This allows the agent or operator to verify health at each step. `POST /v1/admin/platform/upgrade/pause` can be called at any time to stop the state machine after the current phase completes.

**Behavior across long pauses (hours to days).** The upgrade state lives in the `platform_upgrade_state` Postgres row, not in process memory — a `lenny-ops` restart or leader-election change during a paused upgrade is harmless. When a new leader takes over (after lease expiry following a pod restart), it reads the current phase and resumes from there only when an explicit `proceed` is received. Long pauses do NOT auto-resume — the state machine waits indefinitely.

**Idempotency keys for `proceed`/`pause`/`rollback`.** Each of these endpoints accepts (and at Tier 2/3 requires) an `Idempotency-Key` header per Section 25.4. Because these are multi-phase operations, they use the **long-running TTL** (`ops.idempotency.longRunningKeyTTLSeconds`, default 7d) rather than the standard 24h TTL. Agents pausing an upgrade for longer than 7 days should generate a fresh key for the eventual `proceed` call — the operation is idempotent regardless (replaying with a fresh key into the existing state machine yields the same outcome), but the per-key replay-protection window is 7 days.

**Watchdog interaction.** The `PlatformUpgradeStuck` alert fires when an upgrade has been in a non-terminal phase (including `Paused`) for >1 hour by default. Operators expecting longer pauses should silence this alert for the duration of the planned pause to avoid noise; the alert is a safety net for unintentionally-stuck upgrades, not a hard limit on pause duration.

### Config Diff and Config Apply

`GET /v1/admin/platform/config/diff` accepts a `{"desired": {...}}` body and returns a structured diff between the desired state and the running config (fetched from the gateway via `GatewayClient.GetConfig()`). Used for GitOps reconciliation.

`PUT /v1/admin/platform/config` validates the proposed config before applying:

1. **Schema validation.** The proposed config is validated against the known config schema. Unknown keys, type mismatches, and out-of-range values are rejected with `422 CONFIG_VALIDATION_FAILED` and a structured list of errors.
2. **Impact preview.** Without `"confirm": true`, the endpoint returns a dry-run response: the diff, which settings require a restart, and any warnings (e.g., "reducing warm pool minimum below current demand"). No changes are applied.
3. **Apply.** With `"confirm": true`, the config is proxied to the gateway's `PUT /v1/admin/platform/config` via `GatewayClient`. Returns `422 CONFIG_RESTART_REQUIRED` for settings that require a gateway restart (the change is applied but takes effect only after restart).

### Postgres Schema

```sql
CREATE TABLE platform_upgrade_state (
    id                    TEXT PRIMARY KEY DEFAULT 'singleton',
    target_version        TEXT NOT NULL,
    target_images         JSONB NOT NULL,         -- resolved image references per component
    current_phase         TEXT NOT NULL,           -- OpsRoll, CRDUpdate, SchemaMigration,
                                                   -- GatewayRoll, ControllerRoll, Verification,
                                                   -- Complete, Paused, RolledBack
    previous_phases       TEXT[] NOT NULL DEFAULT '{}',
    started_by            TEXT NOT NULL,
    started_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    paused_at             TIMESTAMPTZ,
    completed_at          TIMESTAMPTZ,
    pre_upgrade_backup_id TEXT,
    error                 TEXT,
    metadata              JSONB NOT NULL DEFAULT '{}'
    -- metadata includes: previousImages (map of component → old image ref),
    --                     phaseTimings, preflightResults
);

CREATE TABLE platform_upgrade_check_cache (
    id              TEXT PRIMARY KEY DEFAULT 'singleton',
    checked_at      TIMESTAMPTZ NOT NULL,
    current_version TEXT NOT NULL,
    latest_version  TEXT,
    response        JSONB NOT NULL,
    ttl_seconds     INT NOT NULL DEFAULT 21600
);
```

### Degradation

If the release channel is unreachable: `upgrade-check` returns cached data with `"cached": true, "cacheAge": "..."`. If Postgres is down: upgrade state machine operations fail; version introspection returns partial data (binary metadata always available; schema version unavailable). If K8s API is down: controller versions, CRD versions, and Deployment patches are unavailable. If gateway is down: version aggregation is partial; config diff/apply fail.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_platform_upgrade_phase` | Gauge | `target_version` | Current phase (encoded as integer) |
| `lenny_platform_upgrade_duration_seconds` | Gauge | `target_version` | Time since upgrade started |
| `lenny_platform_version_drift` | Gauge | | 1 if any component version drift, 0 otherwise |
| `lenny_platform_image_pull_check_duration_seconds` | Histogram | `component` | Preflight image pullability check latency |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `PlatformUpgradeAvailable` | `upgrade-check` detected new version | Info |
| `PlatformUpgradeStuck` | Upgrade in non-terminal phase for > 1h | Warning |
| `PlatformVersionDrift` | `lenny_platform_version_drift == 1` for > 5m | Warning |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `UPGRADE_ALREADY_IN_PROGRESS` | `POLICY` | 409 | An upgrade is already running |
| `UPGRADE_PREFLIGHT_FAILED` | `PERMANENT` | 422 | Preflight checks failed. `details.failures` lists each (e.g., image not pullable, health not green, version too old). |
| `UPGRADE_IMAGE_NOT_PULLABLE` | `PERMANENT` | 422 | One or more target images could not be resolved from the configured registry. `details.images` lists the failing references. |
| `UPGRADE_ROLLBACK_UNAVAILABLE` | `PERMANENT` | 409 | Schema migration completed; rollback requires DB restore |
| `UPGRADE_ROLLBACK_MANUAL_CRD` | `PERMANENT` | 409 | CRD rollback requires manual intervention |
| `UPGRADE_NOT_IN_PROGRESS` | `PERMANENT` | 409 | No upgrade to proceed/pause/rollback |
| `UPGRADE_CHANNEL_UNREACHABLE` | `TRANSIENT` | 503 | Release channel unreachable |
| `CONFIG_VALIDATION_FAILED` | `PERMANENT` | 422 | Config schema validation failed. `details.errors` lists each violation. |
| `CONFIG_RESTART_REQUIRED` | `PERMANENT` | 422 | Setting change requires gateway restart |

### Audit Events

`platform.version_checked`, `platform.upgrade_started`, `platform.upgrade_ops_rolled`, `platform.upgrade_crds_updated`, `platform.upgrade_schema_migrated`, `platform.upgrade_gateway_rolled`, `platform.upgrade_controllers_rolled`, `platform.upgrade_phase_advanced`, `platform.upgrade_paused`, `platform.upgrade_rolled_back`, `platform.upgrade_completed`, `platform.upgrade_verified`, `platform.config_changed`, `platform.registry_updated`.

---

## 25.9 Audit Log Query API

Structured query access to the audit trail (Section 11.7). Enables agents to investigate incidents without direct database access.

**Wire format.** All endpoints in this subsection return audit records as [OCSF v1.1.0](https://schema.ocsf.io/1.1.0/) JSON objects per the Wire Format in [§11.7](11_policy-and-controls.md#117-audit-logging). The `items[]` array in paginated responses is an array of OCSF records. Lenny's chain-integrity fields (`chainIntegrity`, `prev_hash`) are surfaced via the OCSF `unmapped.lenny_chain.*` extension on each record. A scope-restricted `?format=raw-canonical` query parameter returns the Lenny-internal canonical tuple (pre-OCSF) for chain auditors who need to verify the hash chain against the exact bytes Postgres hashed over; this requires the `audit:raw-canonical:read` scope. The response envelope carries `ocsfVersion` ("1.1.0") and `chainIntegrityReport` ({verified, broken, gap_suspected, rechained_post_outage, redacted_gdpr}) as top-level fields outside `items[]`.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/audit-events` | Paginated query. Params: `?since=`, `?until=`, `?eventType=`, `?actorId=`, `?resourceType=`, `?resourceId=`, `?tenantId=`, `?severity=`, `?limit=` (default 100, max 1000), `?cursor=`, `?ocsf_translation_state=` (one of `pending` \| `retry_pending` \| `succeeded` \| `dead_lettered`; see [§11.7](11_policy-and-controls.md#117-audit-logging) OCSF translation failure), `?eventbus_publish_state=` (one of `pending` \| `retry_pending` \| `published` \| `failed`; see [§12.2](12_storage-architecture.md#122-storage-roles) for the authoritative column enum and [§12.6](12_storage-architecture.md#126-interface-design) Publish failure after durable commit). Combining filters is AND. Default view returns events regardless of `ocsf_translation_state` (dead-lettered events still appear, with their OCSF payload replaced by the class 2004 receipt); pass `?ocsf_translation_state=succeeded` to exclude dead-lettered and in-flight rows. Operators reconciling after an EventBus outage typically query `?eventbus_publish_state=failed&since=<outage_start>`. |
| `GET` | `/v1/admin/audit-events/{id}` | Single event with full payload |
| `GET` | `/v1/admin/audit-events/summary` | Aggregate counts by type/actor/resource over a time window. Params: `?since=`, `?until=`, `?groupBy=eventType|actorId|resourceType` |
| `POST` | `/v1/admin/audit-events/{id}/retranslate` | Re-run OCSF translation on a single audit row after a translator version bump or a schema-gap fix. Body: `{"translatorVersion": "<semver>" }` (optional; defaults to the active translator). Only rows with `ocsf_translation_state IN ('retry_pending', 'dead_lettered')` are eligible; other rows return `409 ocsf_translation_not_retryable`. On success the row transitions back to `pending` and is picked up by the next translator sweep; the response returns the updated row state and the receiving translator version. Requires `audit:retranslate` scope. Audited as `audit.ocsf_retranslate_requested`. |
| `POST` | `/v1/admin/audit-events/{id}/republish` | Re-queue a single audit row for CloudEvents re-publication after the [§12.6](12_storage-architecture.md#126-interface-design) EventBus retranscribe worker has terminally abandoned it (state `failed`, `retry_count >= eventBus.maxRetryAttempts`). The endpoint resets `retry_count = 0` and `eventbus_publish_state = 'pending'` so the next retranscribe sweep picks the row up. Only rows with `eventbus_publish_state = 'failed'` are eligible; rows in `published` return `409 ALREADY_PUBLISHED`, rows in `pending`/`retry_pending` return `409 ALREADY_PUBLISHED` with `details.currentState` so an operator can distinguish in-flight from completed. A missing `id` returns `404 NOT_FOUND`. Requires `audit:republish` scope — a caller lacking the scope receives `403 FORBIDDEN` (scope taxonomy: `tools:audit:republish`, [§15.2](15_external-api-surface.md#152-mcp-api)). Audited as `eventbus.republish_requested` (payload: `event_id`, `prior_state`, `prior_retry_count`, `requester_sub`, `tenant_id`, `topic`). Parity with `audit.ocsf_retranslate_requested` above. |
| `POST` | `/v1/admin/audit-partitions/{partition}/drop` | Force-drop an audit partition whose retention TTL has expired but whose `AuditPartitionDropBlocked` alert is active because the SIEM forwarder has not advanced past the partition's last event. Requires the `?force=true` query parameter AND a request body `{"acknowledgeDataLoss": true, "partition": "<partition-name>"}` (the `partition` field must match the path and is an anti-footgun cross-check). Irreversibly drops the Postgres partition; events not yet delivered to the SIEM are permanently lost. Requires `audit:partition:drop` scope. Audited as `audit.partition_drop_forced` with the last observed SIEM high-water mark and the partition's (oldest, newest) event timestamps. |

### Implementation

Reads from the existing `audit_log` table (Section 11.7) via `StoreRouter.AuditShard()` for per-tenant queries, or `StoreRouter.AllAuditShards()` for platform-admin cross-tenant queries (scatter-gather across all audit shards, merged in memory).

**Chain integrity.** Each event in the response includes a `chainIntegrity` field:
- `verified` — hash matches its predecessor's hash; tamper-free.
- `broken` — hash does NOT match predecessor; tampering or data corruption (and no `RedactionReceipt` is present to authorize the discontinuity).
- `unchecked` — verification wasn't performed (e.g., cross-shard boundary where hashes are per-shard).
- `redacted_gdpr` — row was rewritten in place by the [§12.8](12_storage-architecture.md#128-compliance-interfaces) `DeleteByUser` OCSF dead-letter PII redaction step under GDPR Article 17. The discontinuity is authorized and accompanied by a signed `RedactionReceipt` ([§12.8](12_storage-architecture.md#128-compliance-interfaces) schema) pinning the rewrite to a specific erasure job, legal basis, and redactor identity. External verifiers accept `redacted_gdpr` as a valid discontinuity only after verifying the receipt's signature and matching its `(original_hash, new_hash)` pair to the observed chain rewrite boundary; otherwise they MUST classify the row as `broken` and fire the `AuditRedactionReceiptMissing` alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)).

**Chain gap detection.** `chainIntegrity` also detects **temporal gaps** caused by degraded-mode writes (not just tampering):

- `gap_suspected` — sequence number jumps in the event stream suggest a gap (e.g., event #1000 then #1150 with no events between). Returned when querying across a period during which Postgres was unavailable.
- The response's `auditMetadata` object lists suspected gap windows: `[{"start": "2026-04-16T10:22Z", "end": "2026-04-16T10:30Z", "reason": "postgres_unreachable"}]`. This is computed from `ops_postgres_outage_log` (a small table recording when `lenny-ops` detected Postgres unreachable) and cross-referenced with event sequence numbers.

**Degraded-mode write semantics.** When `lenny-ops` creates audit events during a Postgres outage (via buffered escalations, flushed locks, etc.), the events are written to the audit log during reconciliation with their **original timestamps** (not the flush timestamp). A dedicated `audit_log_deferred_writes` table tracks these for reconciliation:

```sql
CREATE TABLE audit_log_deferred_writes (
    id             BIGSERIAL PRIMARY KEY,
    event_payload  JSONB NOT NULL,     -- full audit event including original timestamp
    deferred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at     TIMESTAMPTZ,        -- when reconciled into audit_log
    replica_id     TEXT NOT NULL       -- which lenny-ops replica generated this
);
```

Deferred writes are applied to `audit_log` during reconciliation in original-timestamp order. The chain hash is re-computed for the affected range, which intentionally breaks `chainIntegrity: verified` for those events to `chainIntegrity: rechained_post_outage` so operators can distinguish tamper-broken chains from legitimately-rechained ones.

### Diagnostics Audit Rate Limiting

The diagnostics endpoints (`diagnostics.session_diagnosed`, `diagnostics.pool_diagnosed`, `diagnostics.credential_pool_diagnosed`, `diagnostics.connectivity_checked`) can emit hundreds of audit events per minute at Tier 3 if agents poll aggressively. Rate limiting:

- **Per-resource coalescing:** repeated diagnostic calls for the same `{resourceType, resourceId}` within a 60s window emit only one audit event with an incremented `invocationCount` field, instead of one per call.
- **Rate limit override:** `ops.audit.diagnosticsRatePerMinute` (default 60) caps the number of distinct diagnostic audit events per minute per service account. Excess is dropped silently with an `lenny_audit_rate_limited_total` counter increment (so operators can detect).
- **Audit retention for diagnostic events:** `ops.audit.retention.diagnosticsRetainDays` (default 30) is shorter than the default audit retention (normally 365d+). Diagnostics are useful for recent incident analysis but don't need long-term archival.

Cross-cutting diagnostic correlation via `X-Lenny-Operation-ID` still works — audit events tagged with the same operation ID are grouped in queries with `?operationId=`.

### Query Limits and Scatter-Gather

- **`AUDIT_QUERY_TOO_BROAD`** is returned when a query matches criteria that would scan more than 10M rows or when `since`/`until` spans more than 90 days without sufficient filters.
- **Time-window requirement:** queries without `since` or `until` default to the last 24 hours (not "all time"). Unbounded queries are disabled by policy.
- **Scatter-gather caching:** platform-admin cross-tenant queries that use `AllAuditShards()` cache their results in Redis for 5 minutes keyed by a hash of the query parameters. Repeated queries within the window (common for dashboards, exploratory investigation) return cached results. Set `?fresh=true` to bypass the cache. The cache is opt-out (operators can disable via `ops.audit.scatterGatherCacheEnabled: false`).
- **Scatter-gather fan-out control:** queries scan shards in parallel with a configurable concurrency limit (`ops.audit.scatterGatherMaxConcurrency`, default equal to number of shards). Per-shard timeouts ensure a single slow shard doesn't block the whole query.

### Degradation

If Postgres is down: all audit query endpoints return `503 AUDIT_STORE_UNAVAILABLE`. No cache — audit data must come from the authoritative store. Agents should poll `GET /v1/admin/diagnostics/connectivity` to detect Postgres recovery.

When only some audit shards are reachable (partial-shard outage), the endpoint returns 207 `AUDIT_PARTIAL_RESULTS` with the canonical `degradation` envelope listing the missing shards. The response includes only events from reachable shards.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_audit_query_duration_seconds` | Histogram | `endpoint`, `shards` | Audit query latency. |
| `lenny_audit_chain_verification_broken_total` | Counter | | Broken chain segments detected (tamper evidence). |
| `lenny_audit_chain_rechained_post_outage_total` | Counter | | Chain segments rechained after a Postgres outage (not tamper evidence). |
| `lenny_audit_rate_limited_total` | Counter | `event_type`, `service_account` | Audit events dropped by rate limiting. |
| `lenny_audit_scatter_gather_shards_queried` | Histogram | | Shard count per scatter-gather query. |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `AUDIT_EVENT_NOT_FOUND` | `PERMANENT` | 404 | Audit event ID not found. |
| `AUDIT_QUERY_TOO_BROAD` | `POLICY` | 400 | Query would scan too many rows; narrow time range or add filters. |
| `AUDIT_STORE_UNAVAILABLE` | `TRANSIENT` | 503 | Postgres unreachable; no cache. |
| `AUDIT_PARTIAL_RESULTS` | `TRANSIENT` | 207 | Some audit shards were unreachable; response contains reachable-shard data only. |

### Downstream SIEM Scope Boundary (GDPR Erasure)

Lenny's cryptographic control extends only to the data it persists — `audit_log`, `audit_redaction_receipts`, and every other Lenny-managed store. Copies that external processors have already ingested from Lenny's OCSF egress (the SIEM forwarder, the pgaudit sink consumer, every subscribed webhook, and any downstream data lake fed by those sinks) are outside Lenny's control:

- When the `DeleteByUser` erasure job redacts an OCSF dead-lettered row ([§12.8](12_storage-architecture.md#128-compliance-interfaces) Step 14), Lenny rewrites the row in its own Postgres shard and emits the paired `gdpr.erasure_deadletter_redacted` (in-tree receipt) and `gdpr.erasure_deadletter_downstream_notified` (OCSF class 5001 Entity Management, `activity_id: 4 Delete`) events. The downstream notification is Lenny's fulfillment of GDPR Art. 17(2) "reasonable steps to inform other processors holding the data".
- Lenny **cannot** reach into the SIEM to delete the previously-ingested class 2004 translation-failure receipt, its `unmapped.lenny.raw_canonical_b64` payload, or any derived indexes, backups, or SIEM-side dead-letters built from it. The SIEM operator is the processor responsible for acting on the `gdpr.erasure_deadletter_downstream_notified` event — typically by running a retention-policy or targeted-delete workflow against the matching `audit_event_id` / `original_hash` within their system.
- Deployers MUST document downstream SIEM erasure responsibility in their GDPR data-processing agreement with the SIEM vendor (or their internal SIEM team). The default posture is that `gdpr.erasure_deadletter_downstream_notified` is an **action-required** signal for the SIEM operator, not an informational one; ignoring it leaves pre-redaction canonical PII reachable in the SIEM past Lenny's erasure SLA.
- Offline or air-gapped copies (e-discovery exports, quarterly SIEM backups, legal-hold snapshots) are out of Lenny's scope by the same rationale. Deployers who require hard-time-bounded erasure across these media MUST configure the SIEM operator's own retention and deletion workflow to honor `gdpr.erasure_deadletter_downstream_notified` — Lenny cannot discover or act on offline copies.

The `gdpr.erasure_deadletter_downstream_notified` event itself is written to Lenny's audit trail under `audit.gdprRetentionDays` (7-year default) and is queryable via `GET /v1/admin/audit-events?eventType=gdpr.erasure_deadletter_downstream_notified` so compliance teams can produce an auditor-ready record of every downstream notification Lenny emitted.

### Audit Events

`audit.query_executed` (includes query parameters, result count, cache hit/miss, shards touched), `audit.chain_integrity_broken_detected` (emitted when a broken chain is encountered during query — indicates potential tampering).

---

## 25.10 Configuration Drift Detection

Detects discrepancies between the desired platform state and the actual running state.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/drift` | Drift report. Supports `?scope=` (one of `all`, `pools`, `runtimes`, `tenants`, etc.) and `?fresh=true` to bypass the running-state cache. |
| `POST` | `/v1/admin/drift/validate` | Validate the desired-state snapshot against an externally-supplied desired state (typically Helm values.yaml). Body: `{"desired": {...}}`. Returns differences as warnings without affecting any state. |
| `POST` | `/v1/admin/drift/snapshot/refresh` | Refresh `bootstrap_seed_snapshot` from current desired state. Body: `{"desired": {...}, "confirm": true}`. Replaces the snapshot. |
| `POST` | `/v1/admin/drift/reconcile` | Reconcile drifted resources. Body: `{"scope": "all"}` or `{"scope": "resources", "resources": [...]}`. Supports `confirm: true` per Section 25.2 (omit for dry-run). |

### Drift Detection Logic

`GET /v1/admin/drift` compares:
1. **Running state** — read via `GatewayClient` calls to `GET /v1/admin/runtimes`, `GET /v1/admin/pools`, etc.
2. **Desired state** — read from `bootstrap_seed_snapshot` table in Postgres. Alternatively, the caller can supply a `{"desired": {...}}` body for ad-hoc comparison.

A field-by-field JSON diff is computed for each resource. Differences are classified by severity: `high` (image changes, isolation profile, security settings), `medium` (scaling parameters, quota values), `low` (labels, descriptions, metadata).

#### Snapshot Validation

`POST /v1/admin/drift/validate` lets agents check whether the stored `bootstrap_seed_snapshot` matches an externally-supplied source-of-truth (typically the Helm values file in a GitOps repository). The endpoint:

1. Accepts the externally-supplied desired state in the request body.
2. Loads the stored snapshot from Postgres.
3. Returns a structured diff between the two with classification (added/removed/modified).
4. Reports `snapshotValidationResult: "match" | "diverged"`.

This addresses the "stale snapshot" failure mode: if the operator updated Helm values but the snapshot wasn't refreshed (e.g., because the upgrade orchestrator was bypassed for an emergency manual fix), all subsequent drift detection runs against an out-of-date desired state. Validation surfaces the issue and points operators toward `POST /v1/admin/drift/snapshot/refresh`.

**When the snapshot is updated.** The `bootstrap_seed_snapshot` is updated at two well-defined points to make drift behavior predictable across upgrades:

1. **Early in OpsRoll (after the new `lenny-ops` pod becomes Ready, before CRDUpdate).** The new `lenny-ops` binary — which understands the new schema and configuration shape — reads the rendered Helm values from the chart's ConfigMap and writes them into `bootstrap_seed_snapshot_target` (a separate row from the live snapshot). The OLD `lenny-ops` cannot compute this snapshot because it lacks the new version's type definitions; the new binary is required. This means the target snapshot is only available after OpsRoll succeeds. During Preflight (run by old `lenny-ops`), drift detection has no target snapshot — `GET /v1/admin/drift?against=target` returns `404 DRIFT_NO_TARGET_SNAPSHOT`. After OpsRoll completes, drift detection during paused phases (CRDUpdate, SchemaMigration, GatewayRoll, ControllerRoll, Verification) compares the current running state against **both** the live snapshot (showing pre-upgrade drift) and the target snapshot (showing what the upgrade will change).
2. **At the end of an upgrade (Verification phase completion).** The target snapshot is promoted to the live snapshot atomically. From this point onward, `GET /v1/admin/drift` compares against the new desired state.

`GET /v1/admin/drift` returns the comparison against the **live** snapshot by default. During an active upgrade, agents can pass `?against=target` to compare against the in-flight target snapshot, or `?against=both` to receive both diffs in a single response (useful for understanding "what's drifted that the upgrade won't fix").

Operators bypassing the upgrade orchestrator (manual `kubectl apply` of resources, direct API mutations) must call `snapshot/refresh` themselves to reflect their out-of-band changes in the live snapshot.

**Snapshot staleness warning.** Because the `bootstrap_seed_snapshot` live row is written only at OpsRoll and at Verification-phase completion (see "When the snapshot is updated" above), any admin-API mutation that occurs between upgrades (manual `POST /v1/admin/runtimes`, pool edits, tenant updates, hotfix reconciliations) causes the stored desired state to diverge from the current intended state. Subsequent drift reports would then compare running state against a snapshot that is no longer the operator's desired state, producing misleading "drift" for changes that were in fact intentional. To surface this condition, `GET /v1/admin/drift` returns two additional fields derived from `bootstrap_seed_snapshot.written_at` for the row being compared against (`live` by default, or the `target` row when `?against=target`):

| Field | Type | Description |
|-------|------|-------------|
| `snapshot_written_at` | RFC3339 timestamp | Value of `bootstrap_seed_snapshot.written_at` for the row compared against. Omitted when the caller supplied `{"desired": {...}}` in the body (no stored snapshot was read). |
| `snapshot_age_seconds` | integer | Seconds between `snapshot_written_at` and the drift report generation time. Omitted when `snapshot_written_at` is omitted. |
| `snapshot_stale` | boolean | `true` when `snapshot_age_seconds` exceeds `ops.drift.snapshotStaleWarningDays × 86400` (default threshold: **7 days**); otherwise `false`. Defaults to `false` when the caller supplied `{"desired": {...}}` in the body. |

When `snapshot_stale: true`, the response also includes a human-readable `snapshot_stale_warning` string recommending the operator call `POST /v1/admin/drift/snapshot/refresh` to reconcile the stored snapshot with the current desired state. The warning text is:

> `The bootstrap_seed_snapshot is <N> days old. If any admin-API changes (runtime, pool, tenant, credential-pool, or delegation-policy mutations) were made since then without a corresponding upgrade, the comparison below may report intentional changes as drift. Call POST /v1/admin/drift/snapshot/refresh with the current desired state to reconcile, then re-run drift detection.`

`snapshot_stale` is advisory — it does not alter the diff computation, does not change HTTP status, and does not suppress drift findings. It is a signal to the operator that the *desired-state side* of the comparison may itself be outdated, independent of any actual drift on the running-state side. Durable consumers that pre-date this field MUST treat its absence as `false` per the forward-compatibility contract in [§15.5](15_external-api-surface.md#155-api-versioning-and-stability).

The warning threshold is tunable per deployment via `ops.drift.snapshotStaleWarningDays` in `lenny-ops` config (default `7`; setting `0` disables the warning). The bound is advisory only; there is no hard ceiling and no automatic refresh — refresh remains an explicit operator action through `POST /v1/admin/drift/snapshot/refresh` so that a human or agent confirms the current desired state before overwriting the stored snapshot. See also the post-hotfix cleanup step in the [Operational Runbooks §17.7](17_deployment-topology.md#177-operational-runbooks) drift-snapshot runbook.

```sql
CREATE TABLE bootstrap_seed_snapshot (
    id            TEXT PRIMARY KEY DEFAULT 'live',  -- 'live' or 'target'
    desired_state JSONB NOT NULL,
    source        TEXT NOT NULL,         -- 'helm-values', 'caller-supplied', 'snapshot-refresh'
    upgrade_id    TEXT,                  -- non-null when id='target' and an upgrade is in flight
    written_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    written_by    TEXT NOT NULL
);
```

#### Running-State Caching

Running state collection is expensive at Tier 3 (50+ pools, many tenants — multiple sequential gateway API calls). `lenny-ops` caches the collected running state in Redis for `ops.drift.runningStateCacheTTLSeconds` (default 60). Clients can bypass with `?fresh=true`. Reconciliation calls always read fresh state to avoid acting on stale data.

#### Comparison Scope

The `?scope=` parameter limits which resource types are compared:

| Scope | Resources compared |
|-------|--------------------|
| `all` (default) | Pools, runtimes, tenants, credential pools, quotas, controller config |
| `pools` | Just pools |
| `runtimes` | Just runtime definitions |
| `tenants` | Just tenant configs |
| `credential-pools` | Just credential pools |

Narrow scopes complete in seconds even at Tier 3; `scope=all` may take 10-30s on large platforms. Recommended workflow: agents poll `scope=pools` (the most volatile) frequently and run `scope=all` opportunistically.

### Reconciliation

`POST /v1/admin/drift/reconcile` calls admin API `PUT` endpoints via `GatewayClient` to apply the desired state. Each call goes through full RBAC, validation, and audit on the gateway side. Following the canonical dry-run pattern (Section 25.2), omitting `confirm: true` returns a preview.

In-flight reconciliations appear in the Operations Inventory (Section 25.4) with `kind: "drift_reconciliation"` and the canonical `progress` envelope: `totalSteps` = resources-to-reconcile count, `currentStep` = resource currently being reconciled (`"{resourceType}:{resourceId}"`), `etaMethod: "linear_extrapolation"`. The `operation_progressed` event fires on every resource reconciliation.

### Degradation

**Postgres down, caller supplies `desired` body:** Drift detection runs normally — the running state is read from the gateway admin API (no Postgres dependency), and the desired state comes from the caller. The response includes `"desiredStateSource": "caller"`. This enables GitOps agents that carry their own desired state to continue drift checks during a Postgres outage.

**Postgres down, no `desired` body:** Drift detection returns `503 DRIFT_DESIRED_STATE_MISSING` — the `bootstrap_seed_snapshot` table is unavailable and no alternative desired state was supplied.

**Reconciliation during Postgres outage:** `POST /v1/admin/drift/reconcile` works if the caller supplies the desired state in the body (the reconciliation calls go through the gateway admin API, which may or may not depend on Postgres for the specific resources being reconciled). If individual admin API calls fail: drift report includes failed resources in an `errors` array (HTTP 207 `DRIFT_RECONCILE_PARTIAL`).

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_drift_detected_total` | Counter | `resource_type`, `severity` | Drift detections |
| `lenny_drift_reconciled_total` | Counter | `resource_type`, `outcome` | Reconciliation outcomes |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `DRIFT_RECONCILE_PARTIAL` | `TRANSIENT` | 207 | Some resources could not be reconciled |
| `DRIFT_DESIRED_STATE_MISSING` | `PERMANENT` | 404/503 | No snapshot exists and no desired state supplied by caller |
| `DRIFT_NO_TARGET_SNAPSHOT` | `PERMANENT` | 404 | `?against=target` requested but no upgrade is in flight (no target snapshot written). |

### Audit Events

`drift.report_generated`, `drift.reconciliation_started`, `drift.resource_reconciled`, `drift.reconciliation_completed`, `drift.snapshot_refreshed` (emitted by `POST /v1/admin/drift/snapshot/refresh`; carries `{"previous_written_at": ..., "previous_source": ..., "new_source": ..., "byteSize": ...}` in its details).

---

## 25.11 Backup and Restore API

APIs for managing platform backups, extending the disaster recovery procedures in Section 17.3. `lenny-ops` creates K8s Jobs for backup/restore operations and tracks their status in Postgres. The backup pipeline has two surfaces: a **Postgres/config archive pipeline** (described below through the Backup Execution section — covers Postgres shards, platform configuration, CRDs, and optional secrets, packaged into a `pg_dump`-style tar archive in MinIO) and a **continuous ArtifactStore replication pipeline** (MinIO workspace bucket replicated to an off-cluster destination — see ArtifactStore Backup below). Restore of a disaster-struck deployment requires both surfaces: Postgres rows reference ArtifactStore object keys, so restoring Postgres alone against a missing ArtifactStore produces unusable sessions.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/admin/backups` | Trigger on-demand backup. Body: `{"type": "full"}` or `{"type": "postgres"}` or `{"type": "config"}`. Supports `"confirm": true` (required for full backups in production). |
| `GET` | `/v1/admin/backups` | List backups. Params: `?type=`, `?status=`, `?since=`, `?until=`, `?limit=` (default 50), `?cursor=` |
| `GET` | `/v1/admin/backups/{id}` | Backup details including job status, size, duration, and storage location |
| `POST` | `/v1/admin/backups/{id}/verify` | Verify backup integrity (checksum + test restore to temp namespace) |
| `GET` | `/v1/admin/backup-jobs/{id}` | K8s Job status for a running backup/restore operation |
| `GET` | `/v1/admin/backups/schedule` | Current backup schedule |
| `PUT` | `/v1/admin/backups/schedule` | Update schedule. Body: `{"full": "0 2 * * *", "postgres": "0 */6 * * *", "enabled": true}` |
| `GET` | `/v1/admin/backups/policy` | Retention policy |
| `PUT` | `/v1/admin/backups/policy` | Update policy. Body: `{"retainDays": 30, "retainCount": 10, "retainMinFull": 3}` |
| `POST` | `/v1/admin/restore/preview` | Analyze restore impact without executing. Body: `{"backupId": "..."}`. Returns affected resources, version compatibility, and estimated downtime. |
| `POST` | `/v1/admin/restore/execute` | Execute restore. Body: `{"backupId": "...", "confirm": true}`. Requires `confirm: true` — without it, returns a dry-run preview identical to `/restore/preview`. |
| `GET` | `/v1/admin/restore/safety-check` | Compare a backup against current state to estimate data loss. Params: `?backupId=`. |
| `GET` | `/v1/admin/restore/{id}/status` | Per-shard status of an in-flight or completed restore (for monitoring and failure diagnosis). |
| `POST` | `/v1/admin/restore/resume` | Resume a partially-completed restore. Params: `?restoreId=`. Caller must hold the original `restore:platform` lock. |
| `POST` | `/v1/admin/restore/{id}/confirm-legal-hold-ledger` | Confirm that the current legal-hold ledger is authoritative after a `gdpr.backup_reconcile_blocked` stall (ledger restored in lockstep). Body: `{"justification": "<text>"}`. Requires `platform-admin`; operator identity and justification are recorded in the audit trail. Resumes the erasure reconciler on next retry. See [§12.8](12_storage-architecture.md#128-compliance-interfaces) "Post-restore reconciler". |
| `POST` | `/v1/admin/artifact-replication/{region}/resume` | Resume ArtifactStore replication for a region that was suspended by the runtime residency preflight ([§25.11](#2511-backup-and-restore-api) "Runtime residency preflight"). Body: `{"justification": "<text>"}`. Requires `platform-admin`; operator identity and justification are recorded in the audit trail. Preflight re-runs synchronously on invocation — if the jurisdiction-tag mismatch is still present, the resume is rejected with `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` and replication remains suspended. On success, emits `artifact_replication.resumed` audit event carrying `region`, `operator_sub`, `justification`, `resumed_at`, and `destination_jurisdiction_tag` (confirming the post-fix tag value). |
| `GET` | `/v1/admin/artifact-replication/{region}/status` | Return the current replication state for a region: `status` (`active` \| `suspended_residency_violation` \| `suspended_operator`), `last_preflight_at`, `last_preflight_result`, `destination_endpoint`, `destination_bucket`, `destination_jurisdiction_tag`, `replication_lag_seconds`, and `suspended_since` (when applicable). Used by agents to detect and triage residency-driven suspensions before `ArtifactReplicationResidencyViolation` alerts escalate. |

### Go Interface

```go
// pkg/ops/backup/service.go

type BackupService interface {
    CreateBackup(ctx context.Context, req BackupRequest) (*Backup, error)
    ListBackups(ctx context.Context, filter BackupFilter, cursor string, limit int) (*BackupPage, error)
    GetBackup(ctx context.Context, id string) (*Backup, error)
    VerifyBackup(ctx context.Context, id string) (*BackupVerification, error)
    GetJob(ctx context.Context, jobID string) (*BackupJob, error)
    GetSchedule(ctx context.Context) (*BackupSchedule, error)
    UpdateSchedule(ctx context.Context, schedule BackupSchedule) (*BackupSchedule, error)
    GetPolicy(ctx context.Context) (*RetentionPolicy, error)
    UpdatePolicy(ctx context.Context, policy RetentionPolicy) (*RetentionPolicy, error)
    PreviewRestore(ctx context.Context, backupID string) (*RestorePreview, error)
    SafetyCheckRestore(ctx context.Context, backupID string) (*RestoreSafetyCheck, error)
    ExecuteRestore(ctx context.Context, req RestoreRequest) (*RestoreResult, error)
    GetRestoreStatus(ctx context.Context, restoreID string) (*RestoreState, error)
    ResumeRestore(ctx context.Context, restoreID string) (*RestoreResult, error)
}
```

### Response Types

```go
type Backup struct {
    ID            string          `json:"id"`
    Type          string          `json:"type"`            // "full", "postgres", "config"
    Status        string          `json:"status"`          // "running", "completed", "failed", "verifying", "verified"
    StartedAt     time.Time       `json:"startedAt"`
    CompletedAt   *time.Time      `json:"completedAt,omitempty"`
    SizeBytes     int64           `json:"sizeBytes,omitempty"`
    Duration      string          `json:"duration,omitempty"`
    StoragePath   string          `json:"storagePath"`     // MinIO path
    Checksum      string          `json:"checksum"`        // SHA-256 of backup archive
    Components    []BackupComponent `json:"components"`    // what was backed up
    StartedBy     string          `json:"startedBy"`       // service account or "scheduler"
    OperationID   string          `json:"operationId,omitempty"`
    JobID         string          `json:"jobId"`
    Error         string          `json:"error,omitempty"`
}

type BackupComponent struct {
    Name     string `json:"name"`      // "postgres", "config", "crds", "secrets"
    Status   string `json:"status"`
    SizeBytes int64 `json:"sizeBytes"`
}

type RestorePreview struct {
    BackupID          string   `json:"backupId"`
    BackupVersion     string   `json:"backupVersion"`
    CurrentVersion    string   `json:"currentVersion"`
    Compatible        bool     `json:"compatible"`
    IncompatibleReason string  `json:"incompatibleReason,omitempty"`
    AffectedResources []string `json:"affectedResources"`
    EstimatedDowntime string   `json:"estimatedDowntime"`
    RequiresFullStop  bool     `json:"requiresFullStop"`
    Warnings          []string `json:"warnings"`
}
```

### Backup Execution

`lenny-ops` does not run backups in-process. Instead, it orchestrates a K8s Job in the `lenny-system` namespace using the `lenny-backup` image (resolved through `ImageResolver`).

#### Creation Sequence (Insert-Before-Job)

To avoid orphaned Jobs when the Postgres row insert fails, the creation sequence is:

1. **Insert `ops_backups` row** with `status: "pending"` and a generated `backup_id`. If this fails, return an error to the caller — no Job is created.
2. **Create the K8s Job** with `spec.template.metadata.annotations["lenny.dev/backup-id"] = backup_id` so a later reconciler can correlate Jobs with DB rows.
3. **Update the row** to `status: "running"` with `job_id` set once the Job is accepted by the API server.
4. The Job executes, writes to MinIO, and updates the row to `completed` or `failed` on exit (via a Postgres update from inside the Job pod).

A reconciler goroutine in `lenny-ops` runs every 60s and:
- Finds `ops_backups` rows with `status: "pending"` older than 2 minutes (Job creation never happened) and marks them `status: "failed"` with `error: "JOB_CREATE_FAILED"` (the row-level reason; agents observe this via `GET /v1/admin/backups/{id}`. Distinct from the HTTP-level `BACKUP_JOB_CREATION_FAILED` which is returned synchronously when `lenny-ops` cannot reach the K8s API to create the Job in the first place).
- Finds K8s Jobs in `lenny-system` with the `lenny.dev/backup-id` annotation where no matching row exists (stale Job from earlier versions) and deletes them.


#### Job Pod Specification

The Job pod has:

- **Image:** `{platform.registry.url}/lenny-backup:{version}` (resolved by `ImageResolver`).
- **`spec.restartPolicy: Never`** — backup failures should not loop in-pod; retry happens at the Job level.
- **`spec.backoffLimit: 3`** — 3 retries at the Job level before Job is marked failed.
- **`spec.ttlSecondsAfterFinished: 3600`** — 1 hour for post-mortem inspection via `kubectl describe job`, then auto-cleaned.
- **`spec.activeDeadlineSeconds: 7200`** — 2 hours hard deadline; Jobs exceeding this are killed.
- **Access to Postgres** via a dedicated `lenny-backup` Postgres role with `SELECT` on shard tables and no write access. Connection string from Secret `lenny-backup-postgres`.
- **Access to MinIO** via a dedicated `lenny-backup` access key with `s3:PutObject`, `s3:GetObject` (for verification), and `s3:DeleteObject` (for retention) on the backup bucket only. Credentials from Secret `lenny-backup-minio`.
- **Access to K8s API** via a dedicated ServiceAccount `lenny-backup-sa` with `get`/`list` on CRDs in the cluster and `get` on ConfigMaps in `lenny-system`. No Pod, Deployment, or Secret read access.
- **Pod security context:** non-root, read-only root filesystem, `/tmp` writable via emptyDir (for staging archives before upload).
- **NetworkPolicy:** egress permitted to Postgres Service, MinIO Service, and K8s API only. No other egress.

**Full backup** flow inside the Job:
1. Runs `pg_dump` against each Postgres shard (with `--format=custom` for efficient compression). By default, sensitive tables (configured via `backups.contentPolicy.excludeTables` and including `secrets.*`, `credential_pools.raw_secret` columns) are excluded via `--exclude-table-data=...`; these are expected to be restored from a separate secrets backup or re-seeded from SSM/Vault. **Per-region dispatch:** when `backups.regions` is non-empty (required in any deployment where a tenant has `dataResidencyRegion` set, see [§12.8](12_storage-architecture.md#128-compliance-interfaces) "Backup pipeline residency"), `lenny-ops` resolves each shard's region via the same `StorageRouter` resolution used at runtime and runs **one `pg_dump` per region** against that region's shards only — there is no global aggregated dump. Each per-region invocation uses that region's Postgres endpoint, its MinIO endpoint (`backups.regions.<region>.minioEndpoint`), its KMS key (`backups.regions.<region>.kmsKeyId`), and its access-credential Secret (`backups.regions.<region>.accessCredentialSecret`). A shard whose resolved region has no `backups.regions.<region>` entry, or whose region endpoint/KMS is unreachable, aborts the backup with `BACKUP_REGION_UNRESOLVABLE` and emits a `DataResidencyViolationAttempt` audit event (counter `lenny_data_residency_violation_total`).
2. Exports platform configuration (runtimes, pools, tenants, quotas) as JSON.
3. Exports CRD manifests from the K8s API.
4. Packages everything into a tar archive.
5. Encrypts the archive **client-side** with AES-256-GCM using a data key wrapped by the KMS key configured in `backups.encryption.kmsKeyId` (single-region) or `backups.regions.<region>.kmsKeyId` (per-region) if set. Without KMS (not recommended for production), client-side encryption is skipped and server-side SSE-S3 encryption is used on the MinIO upload. When `backups.encryption.perTenantWrapKeys: true`, each tenant's dump segment is encrypted under a tenant-scoped wrap key so that `DeleteByTenant` can crypto-shred the archive segment by destroying the wrap key (see [§12.8](12_storage-architecture.md#128-compliance-interfaces) "Backups in erasure scope").
6. Computes SHA-256 checksum of the encrypted archive.
7. Uploads to MinIO at `backups/{type}/{id}/{timestamp}.tar.gz.enc` with server-side encryption (SSE-S3 or SSE-KMS per `backups.encryption.minioServerSide`). In the per-region path, the object is written to the region's bucket (never a global/default bucket).
8. Updates the `ops_backups` row with size, checksum, encryption metadata, and `status: "completed"`. In the per-region path, `components` lists one entry per region covered so verification, retention, and restore all operate per-region.

**Postgres-only backup** runs step 1 (and encryption/upload steps 5–7) only. **Config-only backup** runs steps 2–3 (and encryption/upload) only.

#### Sensitive Content Policy

Deployers control what appears in backups via `backups.contentPolicy`:

| Helm value | Default | Effect |
|------------|---------|--------|
| `includeSensitiveTables` | `false` | When false, tables listed in `defaultExcludedTables` (see below) are excluded from `pg_dump`. |
| `excludeTables` | `[]` | Additional table names to exclude beyond the defaults. |
| `redactColumns` | `[]` | Column-level redaction: `pg_dump` output is piped through a sed filter that replaces matched columns with `'[REDACTED]'`. |
| `encryption.atRest` | `true` | Server-side encryption on MinIO (SSE-S3 or SSE-KMS). |
| `encryption.clientSide` | `false` | Client-side encryption in the Job before upload. Requires `encryption.kmsKeyId`. |
| `encryption.kmsKeyId` | `""` | AWS KMS key ARN or equivalent for client-side key wrapping. |
| `access.minioObjectACL` | `"private"` | ACL on uploaded objects. |

**`defaultExcludedTables`** (excluded unless `includeSensitiveTables: true`): `platform_secrets`, `tenant_secrets`, `credential_pool_raw_secrets`, and any table matching the pattern `%_secret%` or `%_token%`. Operators can override with `backups.contentPolicy.excludeTables` (to add) or explicitly set `includeSensitiveTables: true` (to include — not recommended unless the encryption story is airtight).

The backup archive's internal manifest records what was included vs excluded, so restore tooling can detect and handle the difference.

#### MinIO Bucket Policy

The Helm chart renders a suggested MinIO bucket policy that:
- Grants `s3:PutObject`, `s3:GetObject`, `s3:DeleteObject`, `s3:ListBucket` to the `lenny-backup` service account only.
- Grants `s3:GetObject`, `s3:ListBucket` to the `lenny-ops` service account (for listing and verification).
- Grants nothing to any other principal.
- Requires TLS (`aws:SecureTransport` condition).
- If SSE-KMS is configured, requires `s3:x-amz-server-side-encryption-aws-kms-key-id` to match `backups.encryption.kmsKeyId` on PutObject.

Operators deploying on cloud object stores (S3, GCS, Azure Blob) should apply equivalent policies at the cloud provider level.

#### ArtifactStore Backup (MinIO workspace bucket replication)

The backup Job described above covers Postgres, platform configuration, CRDs, and (optionally) secrets — but the archive it produces does not include the **ArtifactStore** bucket (workspace snapshots, checkpoints, uploaded files, session transcripts, and eviction-context objects — see [§12.5](12_storage-architecture.md#125-artifact-store) and the erasure-scope table in [§12.8](12_storage-architecture.md#128-compliance-interfaces)). Postgres rows reference ArtifactStore objects by key; if a disaster restores only Postgres, those keys point to objects that no longer exist, breaking workspace reconstruction, checkpoint resume, transcript retrieval, and compliance-relevant object access (legal hold, erasure receipt attachments). ArtifactStore is therefore a first-class element of the backup pipeline, implemented as **bucket replication to an off-cluster destination** rather than a `pg_dump`-style archive.

**Replication mechanism.** The ArtifactStore bucket is replicated continuously to a deployer-configured off-cluster destination (a second MinIO cluster, AWS S3, GCS, or Azure Blob — any S3-compatible endpoint). Replication is configured on the existing MinIO deployment via the Helm values block — no new operator, CRD, or service is introduced. For self-managed MinIO this uses MinIO's native bucket replication (`mc replicate add` equivalent, configured via the `minio.artifactBackup` Helm values); for cloud object stores the equivalent is provider-native cross-region or cross-account replication (S3 Replication Configuration, GCS Storage Transfer, Azure Blob object replication), configured on the provider at install time. Replication covers object PUT, object DELETE (so that erasure deletes propagate to the replication target), and — when the source bucket has versioning enabled — version history. Object-level server-side encryption (SSE-KMS per `storage.artifactStore.kmsKeyId`) is preserved at the destination.

**Required Helm values.**

```yaml
minio:
  artifactBackup:
    enabled: true                          # Tier 2/3 default: true; Tier 1 (dev): false
    target:
      endpoint: ""                         # off-cluster S3-compatible endpoint (e.g.,
                                           # "https://artifact-backup.lenny-dr:9000" or
                                           # "https://s3.us-east-2.amazonaws.com")
      bucket: ""                           # destination bucket name
      accessCredentialSecret: ""           # K8s Secret with {accessKey, secretKey}
      kmsKeyId: ""                         # KMS key on the destination side; must reside in
                                           # the same jurisdiction when dataResidencyRegion
                                           # is set (see §12.8 Backup pipeline residency)
    versioning: true                       # source-bucket versioning required so that delete
                                           # markers replicate without destroying prior versions
    replicationLagRpoSeconds: 900          # Tier 2 default; Tier 3: 900 (15 min). Alert fires
                                           # when lag exceeds this. Aligned with §25.11 RPO table.
    residencyCheckIntervalSeconds: 300     # Runtime residency-preflight tick in seconds (min 60,
                                           # max 3600). Also runs before every replication batch;
                                           # tick only applies to long idle gaps between batches.
                                           # Mirrors the BACKUP_REGION_UNRESOLVABLE cadence.
    residencyAuditSamplingWindowSeconds: 3600  # Positive-audit sampling window for
                                           # artifact.cross_region_replication_verified events
                                           # (first event per (region, destination) per window).
```

In the per-region data-residency topology, `minio.artifactBackup.target` is declared **per region** under `minio.regions.<region>.artifactBackup.target.*`, mirroring the per-region structure of `backups.regions` ([§12.8](12_storage-architecture.md#128-compliance-interfaces) "Backup pipeline residency"). A region that has any tenant with `dataResidencyRegion` set MUST have `minio.regions.<region>.artifactBackup.target.*` fully declared; `lenny-ops` validates at startup and rejects the configuration with `CONFIG_INVALID: minio.regions.<region>.artifactBackup.target incomplete` when missing. Cross-region artifact replication is prohibited — a region's ArtifactStore bucket may only replicate to a destination that itself resides in the same jurisdiction.

**Runtime residency preflight (fail-closed).** Startup-time validation of `minio.regions.<region>.artifactBackup.target.*` is necessary but not sufficient: a late Helm reconfiguration, DNS rebinding of the destination endpoint, region-tag drift in the target MinIO cluster's metadata, or an operator mis-edit during an incident can silently redirect replication across a jurisdiction boundary without any restart of `lenny-ops`. The ArtifactStore replication path therefore runs a **runtime residency preflight** that mirrors the `BACKUP_REGION_UNRESOLVABLE` fail-closed pattern used for Postgres backups ([§12.8](12_storage-architecture.md#128-compliance-interfaces) "Backup pipeline residency"). Cadence: the preflight runs **before every replication batch** submitted to the destination and, independently, on a **periodic tick every `minio.artifactBackup.residencyCheckIntervalSeconds`** (default 300s / 5 min) so that long idle gaps between batches cannot hide a silent redirection. On each run, the preflight (a) resolves the source region (`storage.regions.<region>`) and the configured destination (`minio.regions.<region>.artifactBackup.target`), (b) issues a jurisdiction-tag probe against the destination — an `s3:GetBucketTagging` (or provider equivalent) on the destination bucket — and reads the mandatory `lenny.dev/jurisdiction-region` tag that operators MUST set on any MinIO / S3 / GCS / Azure destination bucket participating in Lenny replication, (c) compares the returned jurisdiction tag to the source region's `dataResidencyRegion`, and (d) re-verifies that the destination endpoint resolves (via DNS lookup on the configured hostname) to an IP range declared in the same region under `backups.regions.<region>.allowedDestinationCidrs` (when that Helm value is set — optional, used as a second-layer DNS-rebinding guard). If any of (b), (c), or (d) fails — tag missing, tag value not equal to the source region, DNS rebinding outside the allowlisted CIDRs, or the probe itself failing — the preflight **halts replication for the affected source region**: the region's replication configuration on the source MinIO cluster is placed into a suspended state (`mc replicate disable` on self-managed MinIO, equivalent on provider-native replication), `lenny-ops` records the suspension in `ops_artifact_replication_state` with `status: "suspended_residency_violation"`, the tenant-facing error code `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` is returned on any admin API that queries replication health for that region, a `DataResidencyViolationAttempt` audit event is emitted (critical severity, same counter `lenny_data_residency_violation_total` that runtime `StorageRouter` writes and Postgres backups already increment, labeled `operation: "artifact_replication"`), and a dedicated counter `lenny_minio_replication_residency_violation_total` (labeled by `region`) is incremented. The `ArtifactReplicationResidencyViolation` critical alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)) fires on any non-zero increment. Replication remains suspended until an operator resolves the jurisdiction mismatch (correct the Helm values, restore the destination tag, or re-provision the destination bucket in the correct region) and invokes `POST /v1/admin/artifact-replication/{region}/resume` (requires `platform-admin`, audited with operator identity and justification). There is no silent retry and no automatic resume — a residency mismatch is treated as a hard compliance fault, not a transient failure to route around. Replication-lag alerts (`MinIOArtifactReplicationLagHigh`) will naturally begin firing while replication is suspended, but they are not the primary signal — `ArtifactReplicationResidencyViolation` is. The runtime preflight is a second-layer control that complements (does not replace) the startup-time `CONFIG_INVALID` check: startup rejects configurations that are malformed or incomplete at load; the runtime preflight rejects configurations that were valid at load time but have drifted or been tampered with since.

**Cross-region replication audit (residency at write time).** On every successful preflight (i.e., every batch-replication round whose residency check passed), `lenny-ops` emits an `artifact.cross_region_replication_verified` audit event (`INFO` severity, sampled — the first event per `(region, destination_endpoint)` per `minio.artifactBackup.residencyAuditSamplingWindowSeconds` window, default 3600s) carrying `source_region`, `source_data_residency_region`, `destination_endpoint`, `destination_bucket`, `destination_jurisdiction_tag` (the tag value read in step (b) above), `verified_at`, and `batch_object_count` (objects submitted to replication in the batch). This ensures chain-of-custody for cross-region replication records the jurisdiction tag **at the write-time of each batch**, not only at the startup config-load time — so a later audit of "was this replication round compliant?" has a signed attestation of the destination's advertised jurisdiction on that specific batch rather than a bare endpoint URL that could have been silently redirected. The event is written under the source tenant for tenant-scoped replication configurations and under the platform tenant for region-scoped configurations that aggregate multiple tenants. A mismatch turns this positive audit event into the `DataResidencyViolationAttempt` critical event described above and suspends replication as documented.

**RPO/RTO targets.** The ArtifactStore backup inherits the same tier-parameterized RPO/RTO envelope as the Postgres pipeline (RTO/RPO Targets by Tier table above) with the following tier-specific RPO values reflecting replication lag rather than backup frequency:

| Tier | Replication mode | RPO target (artifact loss window) | RTO target (restore-to-primary) |
|------|------------------|----------------------------------|--------------------------------|
| 1 (dev) | optional (off by default) | n/a | best-effort |
| 2 (staging) | continuous async | 15m | 30m (same as Postgres RTO) |
| 3 (prod) | continuous async | 15m | 15m (same as Postgres RTO) |

**Replication lag** is monitored as `lenny_minio_replication_lag_seconds` (gauge, labeled by `region` when per-region is in use). Two bundled alerts ([§16.5](16_observability.md#165-alerting-rules-and-slos)) are driven from this gauge: `MinIOArtifactReplicationLagHigh` (Warning) fires when lag exceeds `minio.artifactBackup.replicationLagRpoSeconds` (1× RPO); `MinIOArtifactReplicationLagCritical` (Critical) fires when lag exceeds `4 * minio.artifactBackup.replicationLagRpoSeconds` (4× RPO). Both signal that artifacts written in the most recent window are at risk of being lost in a full-site disaster, with the Critical severity indicating the lag window has breached the tier's published RPO envelope by a material factor. A second counter, `lenny_minio_replication_failed_total`, tracks object-level replication failures (permission, network, destination-full); `MinIOArtifactReplicationFailed` fires on any non-zero rate.

**Restore procedure.** On primary-site loss, the ArtifactStore is restored by promoting the replication target to primary:

1. Operator points the gateway's `storage.artifactStore.endpoint` (or `minio.regions.<region>.artifactStoreEndpoint`) at the replication target via a Helm-values change and applies the chart.
2. `lenny-ops` validates the target's bucket inventory against the restored `artifact_store` Postgres rows (sampled, not exhaustive) and reports the count of `artifact_store` rows whose MinIO object is absent at the target (`lenny_restore_artifact_missing_total`).
3. Rows whose objects are absent are cleaned up by the **existing GC path** ([§12.5](12_storage-architecture.md#125-artifact-store) GC job): the GC job's `WHERE deleted_at IS NULL` guard and MinIO delete-on-absent semantics already handle dangling pointers as idempotent no-ops, so a restored-from-replication ArtifactStore converges without a dedicated reconciler. Sessions whose workspace snapshot is missing transition to `failed` on next resume attempt with error `WORKSPACE_SNAPSHOT_MISSING`; the gateway surfaces this to clients via the existing session-state API ([§15.1](15_external-api-surface.md#151-rest-api)).
4. Once the operator confirms the replication target is the authoritative primary, they reverse the replication direction (target → new standby) to re-establish DR posture.

**Consistency rule — Postgres restore vs. ArtifactStore replication lag.** Because replication is asynchronous, a Postgres restore that pre-dates the most recent replicated artifact is trivially consistent (artifacts referenced by restored rows exist at the target). The reverse case — a Postgres restore point **newer** than the replication-target horizon — produces rows whose `artifact_store.id` points to objects not yet replicated. The existing **GC job** ([§12.5](12_storage-architecture.md#125-artifact-store)) observes these as orphan rows on next sweep and transitions them to `deleted` via the standard `WHERE deleted_at IS NULL` guard; there is no separate artifact reconciler and no new failure mode introduced. Operators who want to minimize the orphan count MUST choose a Postgres restore point whose `completed_at <= now() - lenny_minio_replication_lag_seconds` at restore time; the `POST /v1/admin/restore/preview` response includes `artifactReplicationLagSeconds` and `estimatedOrphanArtifactRows` drawn from the current replication-lag gauge so the operator can make an informed choice. The GDPR erasure reconciler ([§12.8](12_storage-architecture.md#128-compliance-interfaces) Backups in erasure scope) runs against the restored database state and enumerates `DeleteByUser` / `DeleteByTenant` receipts independently of ArtifactStore replication lag — any deletion the reconciler replays against MinIO is idempotent (delete-on-absent is a no-op) and propagates to the replication target via the standard replication path.

**ArtifactStore backups and tenant crypto-shredding.** When `backups.encryption.perTenantWrapKeys: true` is used for Postgres archives, the ArtifactStore's equivalent is **per-tenant SSE-KMS keys** ([§12.5](12_storage-architecture.md#125-artifact-store) T4 per-tenant KMS key lifecycle) — `DeleteByTenant` destroys the tenant's KMS key, rendering every tenant artifact in both the primary bucket **and the replication target** cryptographically unrecoverable. No additional configuration is required on the replication target beyond replicating the SSE-KMS header (native behavior for MinIO replication and provider-native S3/GCS replication). Crypto-shredding of the Postgres archive and crypto-shredding of the ArtifactStore therefore remain symmetric after a full-site disaster.

**Test restore.** The monthly test-restore Job (see Test Restore below) covers ArtifactStore as well: it exercises a sampled read from the replication target (HEAD on N randomly selected object keys drawn from the restored `artifact_store` rows, where N is configured via `backups.verification.artifactSampleSize`, default 100), asserts that ≥ 99% of samples exist at the target, and emits `lenny_restore_test_artifact_success_rate` (gauge) and `lenny_restore_test_artifact_missing_total` (counter). A sampled success rate < 99% sets `lenny_restore_test_success = 0` (the existing test-restore gate [§16.1](16_observability.md#161-metrics)) so the existing restore-test monitoring picks up ArtifactStore failures alongside Postgres failures; no new alert is introduced.

#### Backup Progress

`GET /v1/admin/backups/{id}` includes the canonical `progress` envelope (Section 25.2) while `status IN ('running', 'verifying')`. For `running` backups: `percent` = `bytesWritten / bytesEstimated` (size-based), `etaMethod: "linear_extrapolation"`, `rateMetric: {"name": "bytes_per_second", "value": ...}`. For `verifying` backups: `percent` = `bytesScanned / archiveSize`, `etaMethod: "linear_extrapolation"`. The `operation_progressed` event fires on percent thresholds (10/25/50/75/90/95/99).

### Scheduled Backups

A leader-elected goroutine in `lenny-ops` evaluates cron expressions from the `ops_backup_schedule` table and creates Jobs at the scheduled times. Default schedule: full backup daily at 02:00 UTC, Postgres backup every 6 hours. The schedule is stored in Postgres and modifiable at runtime via `PUT /v1/admin/backups/schedule`.

### Retention Enforcement

After each successful backup, `lenny-ops` evaluates the retention policy and deletes expired backups from both MinIO and Postgres. Retention criteria (all must be satisfied before deletion): age exceeds `retainDays`, backup count exceeds `retainCount`, and at least `retainMinFull` full backups remain.

#### Tier-Based Retention Defaults

The base values in the canonical Helm values block are the **Tier 2** defaults. Tier preset files (`values-tier1.yaml`, `values-tier3.yaml`) override them as follows:

| Tier | `retainDays` | `retainCount` | `retainMinFull` | Notes |
|------|-------------|---------------|-----------------|-------|
| 1 (dev) | 7 | 5 | 2 | Minimize storage cost; recovery from a week-old backup is acceptable for dev. |
| 2 (staging/base) | 30 | 10 | 3 | Default values from canonical Helm values block. |
| 3 (prod) | 90 | 30 | 7 | Long retention enables forensics and complies with common audit requirements (SOC 2, ISO 27001). |

**Pre-restore backups** (created automatically by `restore/execute`) follow `backups.retention.preRestoreRetainDays` (default 7) independently — they're cleaned aggressively to avoid unbounded growth from repeated failed restores.

**Operator override:** any of these values can be raised (operator's call) but cannot be lowered below `retainMinFull: 1` and `retainCount: 1` (the chart refuses to render zero-retention configs).

The retention enforcement Job runs after each successful backup AND on a daily cron at 03:30 UTC (independent of backup completion) to handle cases where backups have stopped.

### Backup Verification

`POST /v1/admin/backups/{id}/verify` creates a K8s Job that:
1. Downloads the backup archive from MinIO.
2. Validates the SHA-256 checksum.
3. For Postgres backups: runs `pg_restore --list` to verify the dump is readable (no actual data restoration).
4. Updates the backup status to `"verified"` or `"verification_failed"`.

### Restore Execution

`POST /v1/admin/restore/execute` is a destructive operation. Agents MUST supply `confirm: true` (dry-run is returned otherwise, per Section 25.2) AND `acknowledgeDataLoss: true` (after reviewing the preview). Without `acknowledgeDataLoss` on a confirm'd request, the endpoint returns `400 RESTORE_ACKNOWLEDGE_REQUIRED` listing the specific data that would be lost (rows written since the backup).

The restore process:

1. **Pre-validate.** Calls `/v1/admin/restore/safety-check` internally (see below) and aborts if `safe: false` unless `acknowledgeDataLoss: true` was supplied.
2. **Acquire lock.** Takes a remediation lock on scope `restore:platform` (Section 25.4). Fails with `409 REMEDIATION_LOCK_CONFLICT` if another restore is in progress.
3. **Pre-restore backup.** Creates a full backup tagged `type: "pre-restore"` to MinIO. This backup is retained for 7 days by default (configurable via `backups.retention.preRestoreRetainDays`) and is automatically deleted when the restore completes successfully (see Pre-Restore Backup Lifecycle below). Failed restores keep the pre-restore backup for the full retention window for post-mortem recovery.
4. **Create the restore K8s Job.** Runs `pg_restore` against each shard. The Job has the same security profile as backup Jobs but with write access on the target shards. Per-shard progress is recorded in `ops_restore_state` (see Failure and Recovery below).
5. **Emit events.** `restore_started` on kick-off; per-shard `restore_shard_completed` events as each shard finishes; `restore_completed` (all shards) or `restore_failed` (any shard fails) on exit.
6. **GDPR erasure reconciler.** Between `restore_completed` and the gateway restart, `lenny-ops` runs the post-restore erasure reconciler ([§12.8](12_storage-architecture.md#128-compliance-interfaces) "Backups in erasure scope"). The reconciler scans the restored `audit_log` for rows matching `event_type LIKE 'gdpr.%'` with the receipt's `completed_at > ops_backups.completed_at` (the `backupTakenAt` boundary); before replay it runs the **legal-hold ledger freshness gate** (§12.8 phase 2) — it consults the current legal-hold ledger (not the backup-time snapshot) and blocks replay with `gdpr.backup_reconcile_blocked` if the ledger's most recent write timestamp is `<= backupTakenAt` (i.e., the ledger itself is stale because it was restored in lockstep), since the reconciler cannot then prove that a post-backup hold does not veto an enumerated erasure or that a post-backup hold release does not expand which artifacts the replay must delete. When the ledger is fresh, the reconciler replays `DeleteByUser(user_id)` / `DeleteByTenant(tenant_id)` for each enumerated subject (suppressing replay for any subject under an active hold whose `legal_hold.set` post-dates the receipt) against the restored databases in dependency order, and emits a single `gdpr.backup_reconcile_completed` audit event with the reconciled and suppressed subjects. `gdpr.*` receipts survive restore under `audit.gdprRetentionDays` (7y default), which always exceeds the 90-day maximum `backups.retention.retainDays`. The reconciler executes as a dedicated K8s Job with the same security profile as the restore Job. **Ready-gating:** the gateway MUST NOT be restarted or marked Ready until the reconciler reports success. On reconciler failure (individual replay failure, Postgres unavailability mid-reconcile, enumeration error, or the legal-hold ledger freshness gate blocking with `gdpr.backup_reconcile_blocked`), the restore is aborted with `RESTORE_ERASURE_RECONCILE_FAILED`, `ops_restore_state.status` is set to `"failed"`, the `restore_failed` event is emitted with `failure_phase: "erasure_reconcile"` (carrying `block_reason: "legal_hold_ledger_stale"` when the ledger gate fired), the `restore:platform` remediation lock remains held, and step 7 is skipped — the gateway is not restarted because serving a partial reconcile would either resurrect erased personal data or destroy legally-held data. Ledger-stale blocks clear only when an operator confirms ledger currency via `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` (`platform-admin`, audited).
7. **Gateway restart.** On successful reconciler completion, `lenny-ops` patches the gateway Deployment's annotations to trigger a rolling restart (so the gateway picks up restored schema/config). Agents monitoring `platform_upgrade_*` events can observe this. **Not** triggered on failure — a partial-restore platform (including a restore that completed its shards but failed the reconciler) should not be served until the operator decides on recovery.
8. **Lock release on success.** After the gateway rolling restart in step 7 has completed (`status.updatedReplicas == status.replicas`), `lenny-ops` releases the `restore:platform` remediation lock automatically. From that point, subsequent `restore/execute` calls are unblocked. The pre-restore backup becomes eligible for deletion (per Pre-Restore Backup Lifecycle below). On **failure** (restore itself, or the erasure reconciler), the lock is NOT auto-released — see Restore Failure and Recovery.

#### Pre-Restore Backup Lifecycle

Pre-restore backups are managed by a single deletion path to avoid races between immediate-deletion and the daily retention cron:

- **On successful restore completion (step 8, after the GDPR erasure reconciler has succeeded and the gateway has rolled),** `lenny-ops` updates the pre-restore backup's `ops_backups` row in Postgres: `status: "expired", expires_at: now()`. It does NOT delete the MinIO object directly.
- **The retention enforcement Job** (which already deletes expired backups from both MinIO and Postgres atomically) handles the actual MinIO `DeleteObject` and the Postgres row removal in a coordinated sequence (see Section 25.11 Retention Enforcement). The retention Job runs after every backup AND on the daily 03:30 UTC cron, so an expired pre-restore backup is cleaned up within minutes typically.
- **The retention Job is leader-elected** (runs only on the leader `lenny-ops` replica) so concurrent runs across replicas are not possible.
- This single-deletion-path design ensures the pre-restore backup is never partially deleted (Postgres row gone but MinIO object lingering, or vice versa).

#### Restore Failure and Recovery

Restore is multi-shard and partial failure is possible: shards 1–2 may complete while shard 3 fails. The restore service records per-shard progress so failures are recoverable:

```sql
CREATE TABLE ops_restore_state (
    id                TEXT PRIMARY KEY,
    backup_id         TEXT NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL,
    completed_at      TIMESTAMPTZ,
    status            TEXT NOT NULL,      -- 'running', 'completed', 'failed', 'paused'
    shard_states      JSONB NOT NULL,     -- {shard_id: {status, started_at, completed_at, error}}
    started_by        TEXT NOT NULL,
    operation_id      TEXT,
    pre_restore_backup_id TEXT NOT NULL,  -- safety-net backup created in step 3
    failed_shard      TEXT,               -- first shard that failed (if status=failed)
    error             TEXT
);
```

**Failure semantics.** If any shard's `pg_restore` fails:

- The Job exits non-zero, sets `status: "failed"`, records `failed_shard` and `error`.
- `restore_failed` event is emitted with the per-shard breakdown.
- **The remediation lock on `restore:platform` is NOT auto-released.** It remains held to prevent a competing restore from starting against partially-restored state. The operator must explicitly release via `DELETE /v1/admin/remediation-locks/{id}` (audited) or steal it (audited via `Steal`) to attempt recovery.
- The pre-restore backup is retained for the full `backups.retention.preRestoreRetainDays` window.
- The platform is in a partial-restore state. Sessions targeting restored shards may succeed; sessions targeting unrestored shards may fail with stale or inconsistent data. Operators should NOT direct production traffic to the platform until recovery completes.

**Recovery options after failure:**

- **Resume.** `POST /v1/admin/restore/resume?restoreId={id}` re-creates the Job, restoring only `shard_states` entries with `status != "completed"`. Idempotent — successfully-restored shards are skipped. The operator must first fix the underlying issue (storage space, schema mismatch, etc.). Lock semantics: resume requires the caller to be the **current `acquiredBy`** of the `restore:platform` remediation lock. If the lock has been stolen by another operator (Section 25.4 Stealing), the new holder is now `acquiredBy` and may resume; the original caller must steal it back to regain control. If the lock has been released or expired, resume returns `409 RESTORE_LOCK_REQUIRED` with instructions to re-acquire (`POST /v1/admin/remediation-locks` with scope `restore:platform`) before retrying. This prevents two operators from concurrently resuming the same partial restore.
- **Restore to a different (older) backup.** First, release the held `restore:platform` lock from the failed restore (`DELETE /v1/admin/remediation-locks/{id}` if the operator is still the `acquiredBy`, or `Steal` if not — both are audited). Then initiate a fresh `restore/execute` with the earlier backup ID; the new call acquires its own lock. The `acknowledgeDataLoss: true` requirement still applies; the safety check considers the partial-restore state as "current."
- **Manual per-shard repair.** For deeply partial failures, operators access Postgres directly via the Total-Outage Recovery Path E (Section 25.15). Per-shard rollback is not provided as an API primitive; recovery in this case is an operator-driven Postgres workflow.

`GET /v1/admin/restore/{id}/status` returns the full `ops_restore_state` row including per-shard status — agents poll this for progress and to diagnose failures. The response includes the canonical `progress` envelope (Section 25.2): `totalSteps` = shard count + 1 (the post-restore gateway restart), `currentStep` = the shard currently being restored or `"gateway-restart"`, `etaSeconds` derived from `linear_extrapolation` over completed shards + `historical_p50` for the gateway restart phase. The `operation_progressed` event fires on every shard completion.

#### Safety Check

`GET /v1/admin/restore/safety-check?backupId={id}` compares the backup's snapshot against current state:

```json
{
  "backupId": "bkp-abc",
  "backupTakenAt": "2026-04-15T02:00:00Z",
  "currentTime": "2026-04-16T14:30:00Z",
  "safe": false,
  "dataLossEstimate": {
    "mutationsSinceBackup": 15234,
    "sessionsAffected": 124,
    "auditEventsLost": 8921,
    "tablesWithDivergence": ["sessions", "credential_pool_state", "audit_log"]
  },
  "compatibility": {
    "backupSchemaVersion": 42,
    "currentSchemaVersion": 43,
    "backupPlatformVersion": "1.5.0",
    "currentPlatformVersion": "1.5.1",
    "schemaMigrationsBetween": ["043_add_session_metadata.sql"],
    "compatible": true,
    "warnings": ["Schema forward-migration required; restore is at version 42, current is 43"]
  },
  "recommendedAction": "review and acknowledgeDataLoss, then execute"
}
```

`mutationsSinceBackup` is computed from Postgres's write transaction logs (via `pg_stat_*` views or `pg_wal` position comparison, depending on the Postgres deployment). This gives a reasonable estimate of how much data would be lost without requiring a full row-level diff.

`safe: true` is returned only when the backup is so recent (< 5 minutes old) or the platform has been idle (no recent writes) that no data loss occurs. In practice, most restores are `safe: false` and require explicit `acknowledgeDataLoss: true`.

### Restore Workflow (Day-2 Operations)

For human operators or agents performing a restore, the recommended workflow:

1. **Diagnose the incident.** Use health, diagnostics, and audit queries to understand why a restore is needed vs another remediation.
2. **Identify the target backup.** `GET /v1/admin/backups` to list candidates; `GET /v1/admin/backups/{id}` for details including `platformVersion` and `schemaVersion`.
3. **Verify the backup.** `POST /v1/admin/backups/{id}/verify` runs a verify Job (checksum + `pg_restore --list`) against the archive. Required before restore in production.
4. **Preview the restore.** `GET /v1/admin/restore/safety-check?backupId={id}` then `POST /v1/admin/restore/preview` (or `POST /v1/admin/restore/execute` without `confirm`). Review `affectedResources`, `estimatedDowntime`, `dataLossEstimate`, and `compatibility.warnings`.
5. **Notify stakeholders.** Restore causes downtime; coordinate with tenants. The preview's `estimatedDowntime` drives the communication.
6. **Execute.** `POST /v1/admin/restore/execute` with `confirm: true` and (if not `safe: true`) `acknowledgeDataLoss: true`.
7. **Monitor.** Subscribe to the event stream filtered on `restore_*` events for progress.
8. **Post-restore verification.** After `restore_completed` and the automatic gateway restart, run `GET /v1/admin/health`, `GET /v1/admin/diagnostics/connectivity`, and `GET /v1/admin/drift` to confirm platform state matches expectations.

#### RTO/RPO Targets by Tier

These are targets the operator should aim for; actual values depend on backup frequency, verification cadence, and platform size. The **ArtifactStore RPO** is the MinIO replication-lag horizon described in ArtifactStore Backup above — replication is continuous-async, so the RPO is expressed as a lag threshold rather than a backup interval.

| Tier | Recommended full backup frequency | Recommended verification | Postgres RPO target | ArtifactStore RPO target | RTO target |
|------|----------------------------------|--------------------------|-----------|-----------|-----------|
| 1 (dev) | daily | on-demand | 24h | n/a (replication optional) | best-effort |
| 2 (staging) | daily | weekly | 6h | 15m (replication lag) | 30m |
| 3 (prod) | daily + 6h Postgres snapshots | daily integrity, monthly test restore | 15m | 15m (replication lag) | 15m |

The verification recommendation is stronger at higher tiers because backups that are never verified can silently rot (corrupted archives, schema drift, credential changes invalidating access). ArtifactStore verification uses the sampled-HEAD test-restore path (see ArtifactStore Backup above) because a full object-by-object scan is impractical at production bucket scale; the 99%-success floor is the test-restore gate.

#### Test Restore

`POST /v1/admin/backups/{id}/verify?mode=test-restore` runs a Job that actually restores the backup to a temporary namespace (`lenny-restore-test-{job-id}`), runs smoke tests against the temporary Postgres, and reports the outcome. The temporary namespace is torn down automatically. This is the strongest form of verification — it proves the backup is actually restorable, not just readable. Monthly test restores are recommended for Tier 3.

### Storage

```sql
CREATE TABLE ops_backups (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,            -- 'full', 'postgres', 'config'
    status          TEXT NOT NULL,            -- 'running', 'completed', 'failed', 'verified', 'verification_failed'
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    size_bytes      BIGINT,
    duration_ms     BIGINT,
    storage_path    TEXT,
    checksum        TEXT,
    components      JSONB NOT NULL DEFAULT '[]',
    started_by      TEXT NOT NULL,
    operation_id    TEXT,
    job_id          TEXT NOT NULL,
    error           TEXT,
    platform_version TEXT NOT NULL,           -- version at time of backup (for restore compatibility)
    schema_version  INT NOT NULL,             -- Postgres schema version at time of backup
    expires_at      TIMESTAMPTZ              -- computed from retention policy
);

CREATE TABLE ops_backup_schedule (
    id          TEXT PRIMARY KEY DEFAULT 'singleton',
    full_cron   TEXT NOT NULL DEFAULT '0 2 * * *',
    pg_cron     TEXT NOT NULL DEFAULT '0 */6 * * *',
    enabled     BOOLEAN NOT NULL DEFAULT true,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ops_retention_policy (
    id               TEXT PRIMARY KEY DEFAULT 'singleton',
    retain_days      INT NOT NULL DEFAULT 30,
    retain_count     INT NOT NULL DEFAULT 10,
    retain_min_full  INT NOT NULL DEFAULT 3,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Degradation

If Postgres is down: backup creation, listing, and scheduling all fail (503). Backups already running as K8s Jobs continue independently — they write directly to MinIO and update Postgres on completion (will retry on reconnect). If MinIO is down: backup upload fails; the Job retries 3 times with backoff, then fails. Restore preview fails if the backup archive cannot be fetched. If K8s API is down: Job creation fails; `lenny-ops` returns `503 BACKUP_JOB_CREATION_FAILED`.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_backup_duration_seconds` | Histogram | `type` | Backup duration by type |
| `lenny_backup_size_bytes` | Gauge | `type`, `backup_id` | Backup size |
| `lenny_backup_total` | Counter | `type`, `status` | Backup outcomes |
| `lenny_backup_last_successful_timestamp` | Gauge | `type` | Unix timestamp of last successful backup |
| `lenny_restore_duration_seconds` | Histogram | | Restore duration |
| `lenny_restore_total` | Counter | `status` | Restore outcomes |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `BackupOverdue` | `lenny_backup_last_successful_timestamp{type="full"}` older than 48h | Warning |
| `BackupFailed` | `lenny_backup_total{status="failed"}` incremented | Warning |
| `BackupStorageHigh` | Total backup storage in MinIO > 80% of quota | Warning |
| `BackupReconcileBlocked` | `lenny_backup_reconcile_blocked_total{reason="legal_hold_ledger_stale"}` incremented. Post-restore GDPR erasure reconciler blocked because the legal-hold ledger was restored in lockstep with the rest of the data and cannot be trusted to reflect post-backup hold transitions. Operator must confirm ledger currency via `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` before the gateway is restarted. See [§12.8](12_storage-architecture.md#128-compliance-interfaces) post-restore reconciler. | Critical |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `BACKUP_NOT_FOUND` | `PERMANENT` | 404 | Backup ID not found |
| `BACKUP_JOB_CREATION_FAILED` | `TRANSIENT` | 503 | Could not create K8s Job |
| `BACKUP_VERIFICATION_FAILED` | `PERMANENT` | 422 | Checksum mismatch or corrupt archive |
| `RESTORE_INCOMPATIBLE` | `PERMANENT` | 422 | Backup version incompatible with current platform |
| `RESTORE_REQUIRES_CONFIRM` | `POLICY` | 400 | `confirm: true` not provided; dry-run preview returned |
| `RESTORE_ACKNOWLEDGE_REQUIRED` | `POLICY` | 400 | `confirm: true` supplied but `acknowledgeDataLoss: true` is missing; safety check returned `safe: false`. |
| `RESTORE_LOCK_REQUIRED` | `POLICY` | 409 | `restore/resume` called but the caller does not hold the `restore:platform` lock; re-acquire and retry. |
| `RESTORE_NOT_FOUND` | `PERMANENT` | 404 | Restore ID not found in `ops_restore_state`. |
| `RESTORE_ERASURE_RECONCILE_FAILED` | `PERMANENT` | 500 | Post-restore GDPR erasure reconciler ([§12.8](12_storage-architecture.md#128-compliance-interfaces) "Backups in erasure scope") failed. Covers: individual replay failure, Postgres unavailability mid-reconcile, enumeration error, and the legal-hold ledger freshness gate blocking replay (`gdpr.backup_reconcile_blocked`, reason `legal_hold_ledger_stale` — the legal-hold ledger was restored in lockstep with the rest of the data and its most recent write timestamp is `<= backupTakenAt`, so the reconciler cannot prove the hold-vs-erase ordering is correct). Restore is aborted without gateway restart; the `restore:platform` lock is retained. Operators must investigate the reconciler failure (typically via `GET /v1/admin/restore/{id}/status`) and resolve before retrying — for ledger-stale blocks, confirm ledger currency via `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` once out-of-band evidence establishes that no post-backup hold transitions are being silently overridden. |
| `BACKUP_STORAGE_UNREACHABLE` | `TRANSIENT` | 503 | MinIO unreachable |
| `BACKUP_REGION_UNRESOLVABLE` | `PERMANENT` | 422 | A shard's resolved `dataResidencyRegion` has no corresponding `backups.regions.<region>` entry, or the region's MinIO endpoint / KMS key is unreachable. Fail-closed mirror of `REGION_CONSTRAINT_UNRESOLVABLE`; emits `DataResidencyViolationAttempt` audit event ([§12.8](12_storage-architecture.md#128-compliance-interfaces) "Backup pipeline residency"). |
| `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` | `PERMANENT` | 422 | The ArtifactStore replication runtime residency preflight ([§25.11](#2511-backup-and-restore-api) "Runtime residency preflight") observed a jurisdiction-tag mismatch between the source region's `dataResidencyRegion` and the destination bucket's advertised `lenny.dev/jurisdiction-region` tag, a missing tag, a DNS rebinding outside `backups.regions.<region>.allowedDestinationCidrs` (when set), or a failed tag-probe against the destination. Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE` for the continuous-replication surface. Replication for the affected region is suspended and does not auto-resume; operator must fix the jurisdiction mismatch and invoke `POST /v1/admin/artifact-replication/{region}/resume` (`platform-admin`, audited). Emits `DataResidencyViolationAttempt` audit event (`operation: "artifact_replication"`) and increments `lenny_minio_replication_residency_violation_total`; raises the `ArtifactReplicationResidencyViolation` critical alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)). |
| `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` | `PERMANENT` | 422 | Phase 3.5 of the tenant force-delete lifecycle ([§12.8](12_storage-architecture.md#128-compliance-interfaces) tenant deletion lifecycle, **Legal hold interaction during deletion**) aborted because the tenant's resolved `dataResidencyRegion` has no corresponding `storage.regions.<region>.legalHoldEscrow` entry, the regional escrow bucket endpoint is unreachable, or the regional escrow KMS key (`platform:legal_hold_escrow:<region>`) cannot be resolved. Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE` / `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` applied to the legal-hold escrow surface, so that EU-pinned held evidence is never migrated to a non-EU escrow bucket under the platform `legal_hold_escrow_kek`. Emits `DataResidencyViolationAttempt` audit event (`operation: "legal_hold_escrow"`) and raises the `LegalHoldEscrowResidencyViolation` critical alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)). The tenant is left in Phase 3.5 `deleting` state; operator must add the missing `storage.regions.<region>.legalHoldEscrow` configuration (or restore endpoint/KMS reachability) and re-invoke `POST /v1/admin/tenants/{id}/force-delete` with `acknowledgeHoldOverride: true`. |
| `PLATFORM_AUDIT_REGION_UNRESOLVABLE` | `PERMANENT` | 422 | A platform-tenant audit event referencing a non-platform `target_tenant_id` (see [§11.7](11_policy-and-controls.md#117-audit-logging) "Platform-tenant audit event residency") could not be written because the target tenant's `dataResidencyRegion` has no corresponding `storage.regions.<region>.postgresEndpoint` entry, or that region's platform-Postgres is unreachable. Fail-closed mirror of `BACKUP_REGION_UNRESOLVABLE` for the platform-tenant audit-write surface (CMP-058). The originating operation halts (impersonation issuance, Phase 3.5 escrow ledger write, `compliance.profile_decommissioned`, etc.). Emits `DataResidencyViolationAttempt` audit event (`operation: "platform_audit_write"`) and raises the `PlatformAuditResidencyViolation` critical alert. Operator must configure the missing `storage.regions.<region>.postgresEndpoint` entry (chart render validation rejects the release if missing) and retry. See [Section 11.7](11_policy-and-controls.md#117-audit-logging) and Storage Routing above. |

### Audit Events

`backup.created`, `backup.completed`, `backup.failed`, `backup.verified`, `backup.deleted_by_retention`, `backup.schedule_updated`, `backup.policy_updated`, `restore.preview_generated`, `restore.started`, `restore.shard_completed`, `restore.resumed`, `restore.completed`, `restore.failed`, `gdpr.backup_reconcile_completed` (§12.8 Backups in erasure scope — written by the post-restore reconciler between `restore_completed` and the gateway restart), `gdpr.backup_reconcile_blocked` (§12.8 post-restore reconciler phase 2 — emitted when the legal-hold ledger freshness gate blocks replay because `ledgerLatestWriteAt <= backupTakenAt`), `gdpr.erasure_reconciled_suppressed_by_hold` (§12.8 post-restore reconciler phase 3 — emitted per subject when an active legal hold post-dates the enumerated receipt and the replay is suppressed), `legal_hold.ledger_confirmed_current_at` (§12.8 post-restore reconciler — written when an operator confirms ledger currency via `/restore/{id}/confirm-legal-hold-ledger`), `legal_hold.escrow_region_resolved` (§12.8 tenant deletion lifecycle Phase 3.5 — CMP-054; written exactly once per tenant force-delete with `acknowledgeHoldOverride: true` immediately before any held-resource migration, records the resolved escrow region, region-scoped `escrow_kek_id` (`platform:legal_hold_escrow:<region>`), bucket endpoint, and whether resolution used the tenant's `dataResidencyRegion` or the deployment single-region default; INFO severity), `DataResidencyViolationAttempt` (§12.8 Backup pipeline residency — emitted on `BACKUP_REGION_UNRESOLVABLE` with `operation: "backup"`, on `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` with `operation: "artifact_replication"`, on `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` with `operation: "legal_hold_escrow"`, and on `PLATFORM_AUDIT_REGION_UNRESOLVABLE` with `operation: "platform_audit_write"` (CMP-058; see [§11.7](11_policy-and-controls.md#117-audit-logging) "Platform-tenant audit event residency") — see §25.11 Runtime residency preflight and tenant deletion lifecycle Phase 3.5), `artifact.cross_region_replication_verified` (§25.11 Cross-region replication audit — per-batch positive residency attestation with `destination_jurisdiction_tag`, `destination_endpoint`, `source_data_residency_region`, and `batch_object_count`; sampled per `minio.artifactBackup.residencyAuditSamplingWindowSeconds`), `artifact_replication.resumed` (§25.11 Runtime residency preflight — written when an operator resumes suspended replication via `POST /v1/admin/artifact-replication/{region}/resume`; payload carries `region`, `operator_sub`, `justification`, `resumed_at`, `destination_jurisdiction_tag`).

---

## 25.12 MCP Management Server

Lenny exposes its full admin and operability surface as an MCP tool server, enabling any MCP-capable agent to manage Lenny natively — not just observe it. An agent that speaks MCP can discover every tool (operability + platform management), inspect schemas, and invoke them without REST API knowledge.

### Scope

Every admin-API endpoint with documented RBAC is exposed as an MCP tool — not only the operability endpoints. This includes:

- **Operability** (Sections 25.3–25.11): health, recommendations, diagnostics, runbooks, events, audit, drift, backup/restore, upgrade, remediation locks, escalations, caller identity, operations inventory.
- **Platform management** (documented in other sections of `SPEC.md`): tenant lifecycle (create/update/suspend/resume/delete), pool CRUD (create/update/delete in addition to scaling), credential pool management (add/rotate/retire credentials, provider config), runtime registration (register/update/retire), quota management, delegation policy.

The MCP inventory is the canonical agent interface for Lenny. An MCP-first agent can do anything a REST caller can. See "Admin API MCP Extension" in "Edits Required Outside Section 25" for the requirement that every admin-API endpoint carries the `x-lenny-*` extensions enabling auto-generation.

### Architecture

The `ManagementMCPAdapter` lives in `lenny-ops` at `/mcp/management` on port 8090. `/mcp/runtimes/{name}` (Section 15) proxies MCP tool calls to agent pods; `/mcp/management` serves Lenny's own management tools. They are separate MCP servers with separate capability negotiation and authentication scopes.

Tool schemas are auto-generated from the canonical OpenAPI spec (Section 15.1) at build time. Tool invocations are routed transparently to either:

- **`lenny-ops` handlers** — for ops-owned endpoints.
- **Gateway admin API via `GatewayClient`** — for gateway-owned endpoints (all platform management plus health and recommendations).

The routing is invisible to the MCP client — all tools appear in a single tool inventory.

```go
// pkg/ops/mcp/adapter.go

type ManagementMCPAdapter struct {
    BaseAdapter
    toolRegistry *ManagementToolRegistry
    opsRouter    http.Handler         // local ops service routes
    gateway      *GatewayClient       // proxied admin API calls
}
```

### Tool Inventory

The following tools are exposed via the MCP `tools/list` method. Tool names follow the pattern `lenny_{domain}_{action}`.

#### Observation Tools (read-only)

| Tool Name | Maps to | Description |
|---|---|---|
| `lenny_health_get` | `GET /v1/admin/health` | Get aggregate platform health |
| `lenny_health_component` | `GET /v1/admin/health/{component}` | Get component health deep-dive |
| `lenny_health_summary` | `GET /v1/admin/health/summary` | Minimal health status |
| `lenny_ops_health_get` | `GET /v1/admin/ops/health` | Get lenny-ops self-health |
| `lenny_recommendations_get` | `GET /v1/admin/recommendations` | Get capacity recommendations |
| `lenny_diagnostics_session` | `GET /v1/admin/diagnostics/sessions/{id}` | Diagnose a session |
| `lenny_diagnostics_pool` | `GET /v1/admin/diagnostics/pools/{name}` | Diagnose a pool |
| `lenny_diagnostics_connectivity` | `GET /v1/admin/diagnostics/connectivity` | Check dependency connectivity |
| `lenny_diagnostics_credential_pool` | `GET /v1/admin/diagnostics/credential-pools/{name}` | Diagnose a credential pool |
| `lenny_events_list` | `GET /v1/admin/events` | List operational events (polling) |
| `lenny_runbooks_list` | `GET /v1/admin/runbooks` | List runbooks with optional filters |
| `lenny_runbooks_get` | `GET /v1/admin/runbooks/{name}` | Get full runbook content |
| `lenny_audit_query` | `GET /v1/admin/audit-events` | Query audit log |
| `lenny_drift_report` | `GET /v1/admin/drift` | Get drift report |
| `lenny_version_full` | `GET /v1/admin/platform/version/full` | Full platform version report |
| `lenny_upgrade_check` | `GET /v1/admin/platform/upgrade-check` | Check for available upgrades |
| `lenny_upgrade_status` | `GET /v1/admin/platform/upgrade/status` | Current upgrade state |
| `lenny_backups_list` | `GET /v1/admin/backups` | List backups |
| `lenny_restore_safety_check` | `GET /v1/admin/restore/safety-check` | Compare a backup against current state to estimate data loss before restore |
| `lenny_restore_status` | `GET /v1/admin/restore/{id}/status` | Per-shard status of an in-flight or completed restore |
| `lenny_locks_list` | `GET /v1/admin/remediation-locks` | List active remediation locks |
| `lenny_logs_pod` | `GET /v1/admin/logs/pods/{ns}/{name}` | Get pod container logs |
| `lenny_me_get` | `GET /v1/admin/me` | Caller identity, authorization, rate-limits, platform capabilities |
| `lenny_me_authorized_tools` | `GET /v1/admin/me/authorized-tools` | Tool inventory pre-filtered to what caller can invoke |
| `lenny_me_operations` | `GET /v1/admin/me/operations` | Caller's in-flight operations |
| `lenny_operations_list` | `GET /v1/admin/operations` | Unified inventory of in-flight operations across all subsystems |
| `lenny_operation_get` | `GET /v1/admin/operations/{id}` | Get a single operation by canonical operation ID |
| `lenny_tenant_list` | `GET /v1/admin/tenants` | List tenants |
| `lenny_tenant_get` | `GET /v1/admin/tenants/{id}` | Get a tenant |
| `lenny_pool_list` | `GET /v1/admin/pools` | List warm pools |
| `lenny_pool_get` | `GET /v1/admin/pools/{name}` | Get a pool's configuration |
| `lenny_credential_pool_list` | `GET /v1/admin/credential-pools` | List credential pools |
| `lenny_credential_pool_get` | `GET /v1/admin/credential-pools/{name}` | Get a credential pool's configuration |
| `lenny_runtime_list` | `GET /v1/admin/runtimes` | List registered runtimes |
| `lenny_runtime_get` | `GET /v1/admin/runtimes/{name}` | Get a runtime's definition |
| `lenny_quota_get` | `GET /v1/admin/tenants/{id}` | Get a tenant's quota (quota fields are embedded in the tenant record; see [§15.1](15_external-api-surface.md#151-rest-api)) |
| `lenny_circuit_breaker_list` | `GET /v1/admin/circuit-breakers` | List operator-managed circuit breakers and their current state (`x-lenny-scope: "tools:circuit_breaker:read"`; see [§11.6](11_policy-and-controls.md#116-circuit-breakers) and [§15.1](15_external-api-surface.md#151-rest-api)) |
| `lenny_circuit_breaker_get` | `GET /v1/admin/circuit-breakers/{name}` | Get state for a single circuit breaker (`x-lenny-scope: "tools:circuit_breaker:read"`) |

#### Action Tools (mutating)

| Tool Name | Maps to | Description |
|---|---|---|
| `lenny_pool_scale` | `PUT /v1/admin/pools/{name}/warm-count` | Scale warm pool |
| `lenny_drift_reconcile` | `POST /v1/admin/drift/reconcile` | Reconcile drifted resources |
| `lenny_upgrade_preflight` | `POST /v1/admin/platform/upgrade/preflight` | Run upgrade preflight checks |
| `lenny_upgrade_start` | `POST /v1/admin/platform/upgrade/start` | Start platform upgrade |
| `lenny_upgrade_proceed` | `POST /v1/admin/platform/upgrade/proceed` | Advance upgrade to next phase |
| `lenny_upgrade_pause` | `POST /v1/admin/platform/upgrade/pause` | Pause upgrade |
| `lenny_upgrade_rollback` | `POST /v1/admin/platform/upgrade/rollback` | Rollback upgrade |
| `lenny_backup_create` | `POST /v1/admin/backups` | Trigger a backup |
| `lenny_backup_verify` | `POST /v1/admin/backups/{id}/verify` | Verify backup integrity |
| `lenny_restore_preview` | `POST /v1/admin/restore/preview` | Preview restore impact |
| `lenny_restore_execute` | `POST /v1/admin/restore/execute` | Execute restore (requires confirm) |
| `lenny_restore_resume` | `POST /v1/admin/restore/resume` | Resume a partially-completed restore |
| `lenny_restore_confirm_legal_hold_ledger` | `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` | Confirm legal-hold ledger currency after a `gdpr.backup_reconcile_blocked` stall |
| `lenny_drift_validate` | `POST /v1/admin/drift/validate` | Validate a caller-supplied desired state against the stored snapshot |
| `lenny_drift_snapshot_refresh` | `POST /v1/admin/drift/snapshot/refresh` | Replace the stored desired-state snapshot |
| `lenny_lock_acquire` | `POST /v1/admin/remediation-locks` | Acquire a remediation lock |
| `lenny_lock_get` | `GET /v1/admin/remediation-locks/{id}` | Get a single lock's current state (validate ownership) |
| `lenny_lock_extend` | `PATCH /v1/admin/remediation-locks/{id}` | Extend a held lock's TTL |
| `lenny_lock_release` | `DELETE /v1/admin/remediation-locks/{id}` | Release a remediation lock |
| `lenny_lock_steal` | `POST /v1/admin/remediation-locks/{id}/steal` | Take over an existing lock (audited) |
| `lenny_escalation_create` | `POST /v1/admin/escalations` | Create an escalation |
| `lenny_config_apply` | `PUT /v1/admin/platform/config` | Apply config change |
| `lenny_tenant_create` | `POST /v1/admin/tenants` | Provision a tenant |
| `lenny_tenant_update` | `PUT /v1/admin/tenants/{id}` | Update a tenant |
| `lenny_tenant_suspend` | `POST /v1/admin/tenants/{id}/suspend` | Suspend a tenant |
| `lenny_tenant_resume` | `POST /v1/admin/tenants/{id}/resume` | Resume a suspended tenant |
| `lenny_tenant_delete` | `DELETE /v1/admin/tenants/{id}` | Delete a tenant (destructive; requires `confirm`). Blocked by active legal holds — see `lenny_tenant_force_delete` for the override path. |
| `lenny_tenant_force_delete` | `POST /v1/admin/tenants/{id}/force-delete` | Force-delete a tenant despite active legal holds. Requires `acknowledgeHoldOverride: true` plus non-empty `justification`; without the override, or when omitted, tenant-delete is rejected with `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` ([§15.1](15_external-api-surface.md#151-rest-api)) if holds exist. Triggers the Phase 3.5 legal-hold segregation step ([§12.8](12_storage-architecture.md#128-compliance-interfaces) tenant deletion lifecycle) which resolves the tenant's `dataResidencyRegion` to a region-scoped `storage.regions.<region>.legalHoldEscrow` entry, re-encrypts held evidence under the region-scoped platform `legal_hold_escrow_kek` (`platform:legal_hold_escrow:<region>`) and migrates it to the region-scoped legal-hold escrow bucket before Phase 4 / 4a tenant KMS destruction. If the region has no complete `legalHoldEscrow` entry or the regional escrow KMS/endpoint is unreachable, Phase 3.5 aborts fail-closed with `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`. Audited as `gdpr.legal_hold_overridden_tenant` (override) and `legal_hold.escrow_region_resolved` (per-migration residency attestation); raises `LegalHoldOverrideUsedTenant` and, on residency resolution failure, `LegalHoldEscrowResidencyViolation` ([§16.5](16_observability.md#165-alerting-rules-and-slos)). |
| `lenny_tenant_compliance_profile_decommission` | `POST /v1/admin/tenants/{id}/compliance-profile/decommission` | Attested wind-down of a regulated `complianceProfile` (sole legitimate path — the `lenny_tenant_update` tool's generic `PUT` surface rejects downgrades with `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` per the [§11.7](11_policy-and-controls.md#117-audit-logging) "Compliance profile downgrade ratchet" ordered `none < soc2 < fedramp < hipaa`). Requires `acknowledgeDataRemediation: true`, non-empty `justification`, at least one `remediationAttestations` entry, and `targetProfile` strictly lower than `previousProfile`. `platform-admin` only (a `tenant-admin` cannot self-downgrade). Audited as `compliance.profile_decommissioned` (critical) recording previous/target profile, operator identity, justification, and attested remediation steps; raises the `CompliancePostureDecommissioned` warning alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)). The tool is the compliance-posture analog of `lenny_tenant_force_delete` — a dedicated attested endpoint that cannot be reached via the generic update tool. |
| `lenny_pool_create` | `POST /v1/admin/pools` | Create a warm pool |
| `lenny_pool_update` | `PUT /v1/admin/pools/{name}` | Update a pool's configuration |
| `lenny_pool_delete` | `DELETE /v1/admin/pools/{name}` | Delete a pool (destructive; requires `confirm`) |
| `lenny_credential_pool_create` | `POST /v1/admin/credential-pools` | Create a credential pool |
| `lenny_credential_pool_update` | `PUT /v1/admin/credential-pools/{name}` | Update a credential pool |
| `lenny_credential_pool_delete` | `DELETE /v1/admin/credential-pools/{name}` | Delete a credential pool (destructive; requires `confirm`) |
| `lenny_credential_add` | `POST /v1/admin/credential-pools/{name}/credentials` | Add a credential to a pool |
| `lenny_credential_retire` | `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | Retire (revoke) a pool credential. Pool credential rotation is performed by revoking the old credential and adding a replacement via `lenny_credential_add` — see [§4.9](04_system-components.md#49-credential-leasing-service) Credential rotation workflow. |
| `lenny_runtime_register` | `POST /v1/admin/runtimes` | Register a runtime |
| `lenny_runtime_update` | `PUT /v1/admin/runtimes/{name}` | Update a runtime |
| `lenny_runtime_retire` | `DELETE /v1/admin/runtimes/{name}` | Retire a runtime |
| `lenny_quota_update` | `PUT /v1/admin/tenants/{id}` | Update a tenant's quota (quota fields are part of the tenant record payload; see [§15.1](15_external-api-surface.md#151-rest-api)) |
| `lenny_circuit_breaker_open` | `POST /v1/admin/circuit-breakers/{name}/open` | Open (activate) an operator-managed circuit breaker (destructive admission-gate activation; `x-lenny-scope: "tools:circuit_breaker:write"`; see [§11.6](11_policy-and-controls.md#116-circuit-breakers)) |
| `lenny_circuit_breaker_close` | `POST /v1/admin/circuit-breakers/{name}/close` | Close (deactivate) an operator-managed circuit breaker (`x-lenny-scope: "tools:circuit_breaker:write"`) |

The tool list above is not exhaustive; every admin-API endpoint with documented RBAC becomes an MCP tool automatically via the build-time OpenAPI → MCP generation (Section 25.12 Scope, and "Admin API MCP Extension" in "Edits Required Outside Section 25"). The canonical inventory is always `/v1/admin/me/authorized-tools` (Section 25.4) for the caller's filtered view, or `/v1/openapi.json` for the full surface.

### Tool Schema Example

Each tool is defined with a JSON Schema `inputSchema`. Example for `lenny_pool_scale`:

```json
{
  "name": "lenny_pool_scale",
  "description": "Scale a warm pool's minimum warm count. Requires confirm:true for changes >50% of current value.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "pool": {
        "type": "string",
        "description": "Pool name (e.g., 'default-gvisor')"
      },
      "minWarm": {
        "type": "integer",
        "minimum": 0,
        "description": "New minimum warm pod count"
      },
      "confirm": {
        "type": "boolean",
        "description": "Required for large changes. Omit for dry-run preview."
      }
    },
    "required": ["pool", "minWarm"]
  }
}
```

### OpenAPI Schema Discovery

The canonical API contract is exposed as an OpenAPI 3.1 document served by `lenny-ops`:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/openapi.json` | Full OpenAPI 3.1 document (JSON) for the entire operability surface — both gateway admin-API endpoints that `lenny-ops` proxies/aggregates and `lenny-ops`'s own endpoints. |
| `GET` | `/v1/openapi.yaml` | Same content, YAML. |
| `GET` | `/v1/openapi/{endpoint-id}` | Schema fragment for a single endpoint (useful for tool generation). |

The OpenAPI document is generated at build time from the same Go type definitions used by the server, ensuring it can't drift from the implementation. The MCP tool schemas referenced below are derived from this OpenAPI document — a single build-time `openapi-to-mcp` step produces the tool inventory from the OpenAPI.

The document includes:
- All request/response schemas (including the canonical `degradation` envelope, `pagination`, and `error` envelopes from Section 25.2).
- Enum values for all filter parameters.
- `x-lenny-category` extension on error responses (`TRANSIENT` | `PERMANENT` | `POLICY` | `AUTH`).
- `x-lenny-retryable` extension (boolean).
- `x-lenny-mcp-tool` extension mapping each endpoint to its MCP tool name (or `null` for endpoints not exposed via MCP).

Agents and SDK generators use this document as the single source of truth.

### Security Model

The MCP Management Server's authorization is enforced at three independent layers — **all three must permit a call for it to succeed**. The MCP-layer capability filtering is a UX convenience, not a security boundary; RBAC and scopes are the real security layers.

1. **MCP-layer capability filtering (convenience, not security).** The tool list returned by `tools/list` is filtered based on the agent's declared capabilities (see Capability Negotiation below) AND the caller's `scope` claim. This is a UX convenience — an agent that doesn't "see" a tool is less likely to accidentally invoke it. It does **not** prevent the agent from invoking the tool by name; that's what layers 2–3 are for.
2. **Scope enforcement (the narrowing layer).** Every `tools/call` invocation checks the caller's `scope` JWT claim (RFC 9068, space-separated values) against the tool's declared `x-lenny-scope` identifier. A caller whose scope doesn't match receives `403 SCOPE_FORBIDDEN` from the MCP adapter before any REST call is issued. This enforces the per-tool scoping model defined in Section 25.1.
3. **REST-layer RBAC (the role layer).** Every MCP tool invocation that passes the scope check is translated into a REST call against `lenny-ops` or the gateway admin API. That REST call passes through the standard OIDC/JWT middleware and role-based authorization check. An agent with a `tenant-admin` role receives `403` from the underlying REST endpoint when calling a tool that requires `platform-admin`, regardless of what its scopes declare (scopes narrow; they don't elevate).

**Every MCP tool has explicit RBAC and scope metadata on its mapped endpoint.** The OpenAPI spec records `x-lenny-required-role` and the mapping `x-lenny-mcp-tool` on every admin-API endpoint. The CI suite includes two tests:

- Iterates every MCP tool, attempts to call it with a token carrying no role, asserts `403`.
- Iterates every MCP tool, attempts to call it with a scope-restricted token that excludes the tool, asserts `403 SCOPE_FORBIDDEN`.

These catch endpoints that accidentally skip authorization at implementation time.

**Unhealthy-endpoint behavior.** When an underlying endpoint is unreachable (e.g., gateway is down and the tool maps to a gateway endpoint), the MCP adapter returns `-32000` (generic server error) with `data.code: "ENDPOINT_UNAVAILABLE"` and the standard `retryable: true`. It does **not** remove the tool from `tools/list` during the outage — removing tools would cause confusing client behavior (tools "disappearing" from the inventory).

**Scope-forbidden behavior.** When a caller invokes a tool outside its `scope` claim, the adapter returns `-32001` with `data.code: "SCOPE_FORBIDDEN"`, `data.retryable: false`, `data.requiredScope: "tools:<domain>:<action>"`, and `data.activeScope: "<space-separated current scopes>"`. Callers should either use `/v1/admin/me/authorized-tools` upfront to avoid forbidden calls, or inspect the returned fields to understand the restriction.

### Dry-Run Result Mapping

For tools with `x-lenny-dry-run-support: "confirm-bool"` (the canonical dry-run pattern from Section 25.2), invoking the tool without `confirm: true` produces a REST response of `200 OK` with `dryRun: true` in the body. The MCP adapter maps this to:

```json
{
  "isError": false,
  "content": [
    {
      "type": "text",
      "text": "{\"dryRun\":true,\"preview\":{...}}"
    }
  ],
  "_meta": {
    "lenny.dryRun": true,
    "lenny.preview": { ... }
  }
}
```

The structured `_meta.lenny.dryRun: true` field is the canonical signal — MCP clients programmatically check this rather than parsing the text content. The text content remains JSON-formatted so clients without metadata support can still detect the dry-run via string parsing.

**Why `isError: false`.** A dry-run is a successful preview, not a failure. Reporting it as `isError: true` would cause MCP clients to surface it as an error to users, which is wrong — the agent successfully performed a preview. The `_meta.lenny.dryRun` flag distinguishes "this was a preview, not a mutation" from "this was a real mutation that succeeded."

**Agent guidance.** Agents that report tool outcomes to users (typical LLM agents) MUST check `_meta.lenny.dryRun`. A truthful report when this flag is `true` is "I previewed the change but did not apply it. To apply, retry with `confirm: true`." A truthful report when the flag is `false` (or absent) is the action's actual effect.

### Capability Negotiation

During MCP initialization, the agent declares its capabilities in the `initialize` request's `clientInfo.capabilities` field. The MCP Management Server uses these declarations to filter the tool list for display purposes. Filtering is ALWAYS intersected with the caller's `scope` claim (tools not permitted by scope are filtered out regardless of capability declaration).

| Capability declaration | Filter effect |
|---|---|
| `{"access": ["admin-api"]}` (or omitted) | All tools the caller's scopes permit. Default. |
| `{"access": ["admin-api"], "scope": "operability"}` | Only operability tools (health, diagnostics, runbooks, events, audit, drift, backup/restore, upgrade, locks, escalations, caller identity, operations inventory). |
| `{"access": ["admin-api"], "scope": "admin"}` | Only platform-management tools (tenant/pool/credential/runtime/quota lifecycle). |
| `{"access": ["admin-api"], "readOnly": true}` | Only observation-category tools regardless of domain. |
| `{"access": ["admin-api"], "nonDestructive": true}` | Excludes tools with `x-lenny-category: "destructive"`. |
| `{"access": ["admin-api"], "tenantScoped": true}` | Only tools whose underlying endpoints are accessible to `tenant-admin` callers. |

Multiple filters can be combined (e.g., `"scope": "operability"` + `"readOnly": true` yields read-only operability tools). If the agent declares no capabilities, all scope-permitted tools are returned.

Capability filtering is a convenience for agents that want a curated view. It is **not** a security mechanism — the scope and RBAC layers in Security Model above are what actually prevent unauthorized calls.

### Tool Schema Details

Each tool's `inputSchema` is a JSON Schema Draft 2020-12 document. Beyond standard JSON Schema, tools use the following `x-lenny-*` extensions:

- **`x-lenny-guards`** — describes conditional-requirement behavior. For tools where a parameter is required under certain conditions (e.g., `confirm: true` is required when `minWarm` exceeds 1.5× the current value):
  ```json
  "x-lenny-guards": [
    {
      "parameter": "confirm",
      "condition": "minWarm > currentWarmCount * 1.5",
      "whenRequired": true,
      "description": "Required for large changes; omit for dry-run preview."
    }
  ]
  ```
  Agents that honor `x-lenny-guards` can prompt users correctly for conditionally-required parameters.
- **`x-lenny-category`** — classifies the tool: `"observation"` (read-only), `"coordination"` (locks, escalations — mutating but low-risk), `"mutation"` (state changes), `"destructive"` (backup, restore, upgrade).
- **`x-lenny-required-role`** — `"platform-admin"` or `"tenant-admin"`.
- **`x-lenny-idempotency-key`** — `"required"`, `"recommended"`, or `"ignored"`.
- **`x-lenny-dry-run-support`** — `"confirm-bool"` (standard pattern) or `"none"`.

Example for `lenny_pool_scale` with extensions:

```json
{
  "name": "lenny_pool_scale",
  "description": "Scale a warm pool's minimum warm count.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "pool": { "type": "string", "description": "Pool name" },
      "minWarm": { "type": "integer", "minimum": 0, "description": "New minimum warm pod count" },
      "confirm": { "type": "boolean", "description": "Required for large changes. Omit for dry-run preview." },
      "operationId": { "type": "string", "format": "uuid", "description": "Optional UUID for multi-step correlation" }
    },
    "required": ["pool", "minWarm"]
  },
  "x-lenny-guards": [
    {
      "parameter": "confirm",
      "condition": "minWarm > currentWarmCount * 1.5",
      "whenRequired": true
    }
  ],
  "x-lenny-category": "mutation",
  "x-lenny-required-role": "platform-admin",
  "x-lenny-idempotency-key": "recommended",
  "x-lenny-dry-run-support": "confirm-bool"
}
```

### Headers and Correlation

The MCP adapter propagates the following from the MCP session to underlying REST calls:

- The authenticated identity from the MCP session → `Authorization` header on gateway calls.
- `X-Lenny-Operation-ID` from the tool input's optional `operationId` field (if present) OR from MCP tool call metadata (`_meta.operationId` in the `tools/call` request, per MCP convention for request metadata) → same HTTP header on REST calls. When both are provided, the tool input field wins.
- `X-Lenny-Agent-Name` from the MCP `clientInfo.name` → same HTTP header on REST calls.

**Where does the agent put the operation ID?** There are two equivalent ways:

1. **In tool input** — include `"operationId": "550e8400-..."` in the arguments to `tools/call`. Every tool's schema includes `operationId` as an optional property.
2. **In MCP request metadata** — set `_meta.operationId` in the `tools/call` JSON-RPC request object. This works without modifying tool input and is cleaner for agents that are instrumented to emit the operation ID automatically.

The adapter accepts either. Agents typically use option 2 for cross-cutting instrumentation and option 1 when the operation ID is domain-specific to the call.

### SSE Event Subscription via MCP

The MCP Management Server supports the `notifications/message` method for streaming operational events to the MCP client. This is the MCP-native equivalent of the SSE endpoint. The agent subscribes by sending a `notifications/subscribe` request with optional event type filters. Events are delivered as MCP notifications with the same payload schema as the REST event stream.

---

## 25.13 Bundled Alerting Rules

### Rationale

Section 16.5 of `SPEC.md` defines 40+ alerting rules with exact PromQL threshold expressions. Without bundling, deployers must translate each rule into their Prometheus configuration by hand — error-prone, tedious, and prone to drift from the authoritative spec. More importantly, `lenny-ops`'s cross-replica health aggregation (Section 25.4, Metrics Source) queries Prometheus's `GET /api/v1/alerts` endpoint to derive the platform-wide health view. If the rules aren't loaded into Prometheus, this endpoint returns nothing and health aggregation falls back to per-replica fan-out unnecessarily.

Bundling the rules solves both problems: out-of-the-box deployments get a working alerting setup, and `lenny-ops` health aggregation works reliably. Operator customizations (changing thresholds, adding routing, disabling specific alerts) propagate automatically to both human alerts (via Alertmanager) and DevOps agent health views (via `lenny-ops`) — there is one source of truth for "is the platform degraded?" and it lives in the operator's Prometheus configuration.

### Single Source of Truth

Alerting rules are defined in a shared Go package (`pkg/alerting/rules`) compiled into both:

- **The gateway binary** — the in-process alert state tracker (Section 25.3, Health API) evaluates these expressions against the in-process metric registry. This is the per-replica fallback used when Prometheus is unreachable.
- **The Helm chart's rendered manifests** — at chart build time, a code generator emits the rule definitions as YAML in the formats described below.

This avoids the two-source-of-truth problem: the rule definitions cannot drift between what the gateway evaluates and what Prometheus loads. A change to a threshold expression updates both the compiled-in evaluator and the rendered manifests in lockstep.

```go
// pkg/alerting/rules/rules.go

type Rule struct {
    Name        string
    Expression  string            // PromQL
    For         time.Duration     // alert "for" clause
    Severity    string            // "critical", "warning", "info"
    Labels      map[string]string
    Annotations map[string]string // includes "runbook" key (Section 25.7 Path B)
}

// Default rule set, defined as Go code so the gateway can also evaluate them.
func DefaultRules() []Rule { ... }

// EvaluateAgainstReader is used by the gateway's in-process alert tracker.
func (r Rule) EvaluateAgainstReader(reader MetricReader) (firing bool, value float64) { ... }
```

### Helm Rendering

The Helm chart renders rules in two formats, gated by Helm values:

```yaml
monitoring:
  bundleRules: true                         # render rules at all (default: true)
  format: "prometheusrule"                  # "prometheusrule" | "configmap" | "both"
  prometheusRule:
    namespace: ""                           # default: same as monitoring.namespace
    additionalLabels: {}                    # for prometheus-operator selector matching
  configMap:
    namespace: ""                           # default: lenny-system
    name: "lenny-alerting-rules"
```

**`PrometheusRule` CRD manifest.** For deployers using prometheus-operator / kube-prometheus-stack. The manifest is rendered with operator-discoverable labels (configurable via `monitoring.prometheusRule.additionalLabels`). The operator picks it up and reloads Prometheus automatically.

**ConfigMap with `rules.yaml`.** For deployers running vanilla Prometheus or other stacks. The ConfigMap can be mounted into the Prometheus pod's `rule_files` directory. The chart emits the same rules in standard Prometheus YAML format.

**Documented YAML reference.** The full rule set is also rendered into `docs/alerting/rules.yaml` in the repository, so deployers using non-Prometheus monitoring stacks (Datadog, Grafana Cloud, Victoria Metrics, etc.) can translate the expressions to their target system. This file is generated from the same Go source and committed to the repo on each release.

### Tier-Aware Defaults

Most thresholds are universal — they encode platform invariants that don't depend on scale. A small subset depends on the deployment tier (Section 17 of `SPEC.md`).

| Category | Examples | Defaults |
|---|---|---|
| **Universal** | `WarmPoolExhausted`, `SessionStoreUnavailable`, `DualStoreUnavailable`, `CertExpiryImminent`, `BackupOverdue`, `PlatformUpgradeStuck` | Fixed values (e.g., warm pool == 0 for >60s). Same at any scale. |
| **Tier-dependent** | `GatewayQueueDepthHigh`, `GatewayLatencyHigh`, `WarmPoolReplenishmentSlow`, `CredentialPoolLow` | Defaults set by tier preset; tighter thresholds at higher tiers (stricter SLAs). |
| **Workload-specific** | Per-tenant quota rejection rates, per-provider credential rate limits | No meaningful default; operator must tune. Alerts disabled by default and enabled via Helm values when the operator has data to set them. |

Tier-dependent thresholds are exposed as Helm values with tier-aware defaults set in the tier preset files (`values-tier1.yaml`, `values-tier2.yaml`, `values-tier3.yaml`):

```yaml
# Excerpt from values-tier2.yaml
monitoring:
  alertThresholds:
    gatewayQueueDepthHigh:
      value: 10
      duration: "5m"
      severity: "warning"
    gatewayLatencyHigh:
      p99Seconds: 2.0
      duration: "5m"
      severity: "warning"
    warmPoolReplenishmentSlow:
      ratioBelow: 0.5      # replenishment rate < 50% of claim rate
      duration: "10m"
      severity: "warning"

# Excerpt from values-tier3.yaml — tighter thresholds
monitoring:
  alertThresholds:
    gatewayQueueDepthHigh:
      value: 5
      duration: "1m"
      severity: "warning"
    gatewayLatencyHigh:
      p99Seconds: 1.0
      duration: "2m"
      severity: "warning"
    warmPoolReplenishmentSlow:
      ratioBelow: 0.7
      duration: "5m"
      severity: "critical"
```

Universal thresholds are also exposed as Helm values for genuinely unusual deployments, but their defaults are fixed across tiers.

### Operator Customization Model

Operators have three customization paths, listed by increasing scope:

1. **Adjust thresholds via Helm values** — change a number in `values.yaml`, redeploy. The bundled rules are re-rendered with the new threshold. Both human alerts (via Alertmanager) and `lenny-ops` aggregated health views reflect the change.

2. **Override individual rules** — for deployers who want to replace a specific rule expression entirely (not just tweak a number), `monitoring.alertOverrides` accepts a map of rule name → custom rule definition. The custom rule replaces the bundled one in the rendered manifest:
   ```yaml
   monitoring:
     alertOverrides:
       WarmPoolExhausted:
         expr: 'lenny_warmpool_pod_idle == 0 and on(pool) lenny_warmpool_claim_rate > 1'
         for: "30s"
         severity: "critical"
   ```

3. **Disable bundled rules and provide their own** — set `monitoring.bundleRules: false` and ship a separate `PrometheusRule` manifest. Both humans and agents will see whatever the operator's rules produce. The compiled-in gateway alert tracker still uses the bundled defaults as a fallback (when Prometheus is down), but the operator's view is whatever they configured.

In all three cases, **`lenny-ops` health aggregation reflects the operator's view** because it queries Prometheus's alerts API. If an operator has decided that queue depth >5 is critical instead of warning, agents see that severity. There is no second set of thresholds in `lenny-ops`.

### Gateway In-Process Tracker (Fallback Behavior)

The gateway's in-process alert state tracker (Section 25.3, Health Derivation Rules) evaluates the **compiled-in default expressions** — not the operator's customized rules. This is intentional and acceptable because:

- The in-process tracker is a fallback used only when Prometheus is unreachable. The primary aggregated view comes from Prometheus, which evaluates the operator's customized rules.
- Per-replica health is acknowledged as approximate (Section 25.3, Per-replica scope). Slight divergence from the operator's customized thresholds is consistent with the broader caveat that the per-replica view is not the platform-wide view.
- Synchronizing operator-customized thresholds into the gateway's in-process tracker would require either reading rule overrides from a ConfigMap at startup (added complexity, restart-on-change behavior) or pushing them via the admin API (added attack surface). The complexity isn't justified for a fallback path.

If an operator's customizations make this divergence problematic, they can disable the in-process tracker entirely (`gateway.healthTracker.useCompiledRules: false`), in which case the gateway health endpoint falls back to dependency probes and circuit breaker state only — losing the threshold-derived component status. This is rarely the right trade-off but is available for operators who want strict consistency with their custom rules.

### Alertmanager Routing

Alertmanager configuration is **not bundled** — alert routing is highly deployer-specific (PagerDuty vs. Slack vs. OpsGenie vs. email; on-call rotations; per-team routing; severity-based escalation policies). Bundling defaults would either be wrong for most deployers or be so generic as to be useless.

Instead, the spec provides a recommended severity-to-routing mapping in `docs/alerting/routing-recommendations.md`:

| Severity | Recommended routing | Rationale |
|---|---|---|
| `critical` | Page on-call (PagerDuty/OpsGenie) immediately; post to `#lenny-alerts-critical` Slack channel | Platform-affecting; needs human response within minutes |
| `warning` | Post to `#lenny-alerts` Slack channel; page only if unresolved >30 minutes | Indicates degradation but platform is still serving traffic |
| `info` | Log/notification only | Informational; no immediate action required |

Operators copy the recommendations into their Alertmanager `route` configuration as a starting point.

### Storage

None. Bundled rules are static manifests rendered at Helm chart install/upgrade time. Operator customizations live in their Helm values file (their source of truth for the deployment). No runtime mutation of bundled rules is supported.

### Evaluation Cost

The bundled rule set contains ~40 rules. Cost considerations:

- **Prometheus-side evaluation.** Prometheus evaluates each rule on every scrape interval (default 15s). With 40 rules at ~10ms p95 evaluation each, total evaluation time per scrape is ~400ms — well within Prometheus's per-target budget. Prometheus reports per-rule evaluation duration via `prometheus_rule_evaluation_duration_seconds`. Operators should alert on p95 > 1s (indicates a rule is doing too much work or the metrics it queries have exploded in cardinality).
- **Gateway in-process tracker.** The compiled-in alert tracker evaluates the same 40 rules but only on health-endpoint hits (cached for 5s). Cost is negligible.
- **`lenny-ops` aggregation.** When using Prometheus as the primary source (Section 25.4 Metrics Source), `lenny-ops` queries `/api/v1/alerts` once per health request (cached). When using the fallback path, `lenny-ops` evaluates rules in-process against scraped metrics, with the same cost as the gateway tracker.
- **Cardinality risk.** Some rules involve labels like `pool` or `tenant_id`. At Tier 3 with many tenants, alerts can multiply. The bundled rules use `topk()` and `histogram_quantile()` carefully to bound output cardinality; operators adding custom rules should follow the same pattern.

If evaluation cost becomes an issue (very large deployments), operators can prune rules via `monitoring.alertOverrides` (set `enabled: false` on individual rules). Pruning trades alerting coverage for evaluation cost.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_alerting_rules_bundled` | Gauge | `format` | 1 if rules are rendered in the given format (`prometheusrule`, `configmap`). |
| `lenny_alerting_rule_overrides` | Gauge | | Count of operator-overridden rules from `monitoring.alertOverrides`. |
| `lenny_alerting_rule_eval_duration_seconds` | Histogram | `rule` | In-process tracker evaluation latency per rule (when fallback active). |

These metrics let operators verify their bundling configuration is in effect and detect evaluation cost issues.

### Audit Events

None. Rule changes happen via Helm upgrade, which is captured in the Helm release history (and in the Lenny upgrade audit trail when the upgrade is initiated through `lenny-ops`).

### Failure Mode Implications

The bundled rules change two rows in the failure mode analysis (Section 25.15):

- **Prometheus down, rules bundled, operator unmodified:** Aggregated health falls back to per-replica fan-out. The fan-out responses come from the gateway's in-process tracker, which uses the same rule expressions as Prometheus would have. The aggregated view is consistent with what Prometheus would have returned, just slower and noisier (per-replica vs. global).
- **Prometheus down, rules bundled, operator customized:** Same fallback path, but the per-replica fan-out uses compiled-in defaults rather than the operator's customized thresholds. The aggregated view diverges from what the operator configured. `lenny-ops` includes `"thresholdSource": "compiled-in-defaults"` in the response so the agent knows the view may not match the operator's intent during this fallback.

---

## 25.14 `lenny-ctl` Extensions

The following command groups wrap the operability APIs. Same conventions as Section 24: `--output json`, `--quiet`, global flags.

### Routing

`lenny-ctl` commands target two different services:

| Commands | Target | How `lenny-ctl` knows |
|---|---|---|
| `health`, `recommendations` | Gateway admin API | Direct to gateway URL (`--server` flag) |
| `events`, `diagnose`, `runbooks`, `upgrade`, `audit`, `drift`, `backup`, `locks`, `escalations`, `logs`, `mcp-management` | `lenny-ops` | `--ops-server` flag, or auto-discovered via `GET /v1/admin/platform/version` (gateway response includes `opsServiceURL`) |

### Runbook Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl runbooks list` | `GET /v1/admin/runbooks` | List all runbooks with triggers |
| `lenny-ctl runbooks list --alert <name>` | `GET /v1/admin/runbooks?alert=<name>` | Find runbooks matching an alert |
| `lenny-ctl runbooks get <name>` | `GET /v1/admin/runbooks/<name>` | Print full runbook content |

### Remediation Lock Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl locks list` | `GET /v1/admin/remediation-locks` | List active locks |
| `lenny-ctl locks acquire --scope <scope> --op <op>` | `POST /v1/admin/remediation-locks` | Acquire a lock |
| `lenny-ctl locks release <id>` | `DELETE /v1/admin/remediation-locks/{id}` | Release a lock |

### Escalation Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl escalations list` | `GET /v1/admin/escalations` | List escalations |
| `lenny-ctl escalations create --severity <sev> --summary <text>` | `POST /v1/admin/escalations` | Create an escalation |
| `lenny-ctl escalations resolve <id>` | `PUT /v1/admin/escalations/{id}` | Mark as resolved |

### Log Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl logs pod <ns> <name>` | `GET /v1/admin/logs/pods/{ns}/{name}` | Get pod container logs |
| `lenny-ctl logs pod <ns> <name> --tail 100` | Same with `?tail=100` | Last N lines |
| `lenny-ctl logs pod <ns> <name> --previous` | Same with `?previous=true` | Previous container logs |

### Identity and Operations Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl me` | `GET /v1/admin/me` | Show caller identity, authorization, rate-limits, and platform capabilities |
| `lenny-ctl me tools` | `GET /v1/admin/me/authorized-tools` | List tools the caller can actually invoke |
| `lenny-ctl me operations` | `GET /v1/admin/me/operations` | Caller's in-flight operations |
| `lenny-ctl operations list [--actor=<sub>] [--kind=<csv>] [--status=<csv>]` | `GET /v1/admin/operations` | Unified list of in-flight operations |
| `lenny-ctl operations get <operationId>` | `GET /v1/admin/operations/{id}` | Full detail of a single operation |

### Diagnose Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl diagnose session <id>` | `GET /v1/admin/diagnostics/sessions/{id}` | Diagnose a session |
| `lenny-ctl diagnose pool <name>` | `GET /v1/admin/diagnostics/pools/{name}` | Diagnose a pool |
| `lenny-ctl diagnose connectivity` | `GET /v1/admin/diagnostics/connectivity` | Check dependency connectivity |
| `lenny-ctl diagnose credential-pool <name>` | `GET /v1/admin/diagnostics/credential-pools/{name}` | Diagnose a credential pool |

### Event Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl events tail` | `GET /v1/admin/events/stream` | Stream operational events (SSE) |
| `lenny-ctl events list --since <time>` | `GET /v1/admin/events?since=<time>` | List operational events (polling) |
| `lenny-ctl events subscriptions list` | `GET /v1/admin/event-subscriptions` | List webhook subscriptions |
| `lenny-ctl events subscriptions create --url <url> --types <csv>` | `POST /v1/admin/event-subscriptions` | Create a webhook subscription |
| `lenny-ctl events subscriptions delete <id>` | `DELETE /v1/admin/event-subscriptions/{id}` | Delete a webhook subscription |

### Audit Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl audit query --since <time> [...]` | `GET /v1/admin/audit-events` | Query audit log |
| `lenny-ctl audit get <id>` | `GET /v1/admin/audit-events/{id}` | Get a single audit event |
| `lenny-ctl audit summary --since <time>` | `GET /v1/admin/audit-events/summary` | Aggregate counts |
| `lenny-ctl audit retranslate <id> [--translator-version <semver>]` | `POST /v1/admin/audit-events/{id}/retranslate` | Retry OCSF translation on a single `retry_pending` or `dead_lettered` row (e.g., after a translator schema-gap fix) |
| `lenny-ctl audit republish <id>` | `POST /v1/admin/audit-events/{id}/republish` | Re-queue a single `eventbus_publish_state='failed'` row for the EventBus retranscribe worker (resets `retry_count` and state to `pending`); used after `EventBusPublishFinalFailure` |
| `lenny-ctl audit drop-partition <partition> --force --acknowledge-data-loss` | `POST /v1/admin/audit-partitions/{partition}/drop` | Force-drop an audit partition held by the SIEM delivery guard; permanently discards any events not yet forwarded to the SIEM |

### Drift Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl drift report [--scope <s>] [--against <live\|target\|both>]` | `GET /v1/admin/drift` | Drift report |
| `lenny-ctl drift validate --desired <file>` | `POST /v1/admin/drift/validate` | Validate desired state against snapshot |
| `lenny-ctl drift snapshot refresh --desired <file>` | `POST /v1/admin/drift/snapshot/refresh` | Replace stored snapshot |
| `lenny-ctl drift reconcile [--scope <s>] [--confirm]` | `POST /v1/admin/drift/reconcile` | Reconcile drifted resources |

### Backup and Restore Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl backup list` | `GET /v1/admin/backups` | List backups |
| `lenny-ctl backup get <id>` | `GET /v1/admin/backups/{id}` | Backup details |
| `lenny-ctl backup create --type <full\|postgres\|config> [--confirm]` | `POST /v1/admin/backups` | Trigger a backup |
| `lenny-ctl backup verify <id> [--mode test-restore]` | `POST /v1/admin/backups/{id}/verify` | Verify backup integrity |
| `lenny-ctl backup schedule get / set` | `GET/PUT /v1/admin/backups/schedule` | Backup schedule |
| `lenny-ctl backup policy get / set` | `GET/PUT /v1/admin/backups/policy` | Retention policy |
| `lenny-ctl restore safety-check --backup <id>` | `GET /v1/admin/restore/safety-check` | Estimate data loss before restore |
| `lenny-ctl restore preview --backup <id>` | `POST /v1/admin/restore/preview` | Preview restore |
| `lenny-ctl restore execute --backup <id> --confirm --acknowledge-data-loss` | `POST /v1/admin/restore/execute` | Execute restore |
| `lenny-ctl restore status <id>` | `GET /v1/admin/restore/{id}/status` | Per-shard restore status |
| `lenny-ctl restore resume <id>` | `POST /v1/admin/restore/resume` | Resume partially-completed restore |
| `lenny-ctl restore confirm-legal-hold-ledger <id> --justification <text>` | `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` | Confirm legal-hold ledger currency after a `BackupReconcileBlocked` alert; clears the reconciler block so the restore can resume |

### Upgrade Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl upgrade check` | `GET /v1/admin/platform/upgrade-check` | Check for new release |
| `lenny-ctl upgrade preflight --version <v>` | `POST /v1/admin/platform/upgrade/preflight` | Validate upgrade safety |
| `lenny-ctl upgrade start --version <v> [--confirm]` | `POST /v1/admin/platform/upgrade/start` | Begin upgrade |
| `lenny-ctl upgrade proceed` | `POST /v1/admin/platform/upgrade/proceed` | Advance to next phase |
| `lenny-ctl upgrade pause` | `POST /v1/admin/platform/upgrade/pause` | Pause upgrade |
| `lenny-ctl upgrade rollback [--confirm]` | `POST /v1/admin/platform/upgrade/rollback` | Rollback upgrade |
| `lenny-ctl upgrade status` | `GET /v1/admin/platform/upgrade/status` | Current upgrade state |
| `lenny-ctl upgrade verify` | `POST /v1/admin/platform/upgrade/verify` | Post-upgrade health verification |

### MCP-Management Commands

| Command | API Mapping | Description |
|---------|-------------|-------------|
| `lenny-ctl mcp-management tools list` | MCP `tools/list` against `/mcp/management` | List exposed MCP tools |
| `lenny-ctl mcp-management tools call <name> --args <json>` | MCP `tools/call` | Invoke a tool through MCP (for end-to-end testing) |

**Auto-discovery.** To avoid requiring deployers to configure two URLs, the gateway's `GET /v1/admin/platform/version` response includes an `opsServiceURL` field (configured via `ops.ingress.host` Helm value). `lenny-ctl` fetches this on first use and caches it for the session. If the ops URL is not configured, `lenny-ctl` falls back to `--ops-server` or errors with a clear message.

---

## 25.15 Failure Mode Analysis

| Failure | Impact |
|---|---|
| **Gateway crash-loop** | `lenny-ops` stays up. Watchdog calls diagnostics, fetches runbooks, queries audit trail. Remediation steps that call the gateway admin API will fail — the agent sees this and escalates or waits for gateway recovery. Gateway appears as unreachable in connectivity check. Event stream loses the gateway buffer fallback — if Redis is also down, events are unavailable. |
| **Gateway overloaded** | `lenny-ops` has an independent resource budget. Zero contention with client traffic. Health and recommendations on the gateway remain lightweight (in-process reads). |
| **Postgres down** | Audit queries, backup management, and upgrade state machine are unavailable. Diagnostics degrade to K8s API data (partial results, 207). Drift detection works when caller supplies desired state. Remediation locks fall back to Redis (or in-memory). Escalation creation works (Redis or in-memory fallback). Event stream and webhook delivery are unaffected. Health endpoint on gateway still works (in-process metrics). |
| **Redis down** | Event stream falls back to gateway in-memory event buffer — degraded but functional. Webhook delivery continues from buffer with cached subscriptions. Remediation locks fall back to in-memory (if Postgres also down). Health endpoint still works (in-process circuit breaker cache fallback). |
| **Postgres + Redis both down** | Core operational loop still functions in degraded mode: event stream via gateway buffer, diagnostics via K8s API (partial), remediation locks in-memory (single-replica only), escalation creation in-memory (202 Accepted), drift detection with caller-supplied desired state, webhook delivery from buffer with cached subscriptions **provided `lenny-ops` was running with a populated cache before the outage** (a `lenny-ops` cold start during the outage produces an empty subscription cache and no webhook deliveries until Postgres recovers). Unavailable: audit queries, backup management, upgrade state machine, subscription CRUD, retry history, idempotency-required endpoints (including `restore/execute` — see Total-Outage Recovery for the manual recovery path). |
| **`lenny-ops` degraded** | Self-monitoring detects internal degradation (Postgres pool, Redis lag, memory pressure) and emits `ops_health_status_changed` to the event stream (Redis or gateway buffer). Watchdog receives this event and can poll `GET /v1/admin/ops/health` for details. |
| **`lenny-ops` crash** | Gateway continues serving client traffic unaffected. Diagnostics, runbook index, audit, drift, backup, and upgrade APIs are unavailable. Watchdog detects ops service down via Ingress health check failure (no response from `/healthz`). In-memory escalations and remediation locks are lost (Redis/Postgres copies survive if those stores are up). |
| **`lenny-ops` + gateway both down** | Total platform outage. Watchdog detects both unreachable. |
| **Prometheus transiently down** | Gateway health and recommendations endpoints still work per-replica (in-process metrics). `lenny-ops` aggregation falls back to per-replica fan-out via headless Service: health uses worst-of merge, recommendations use highest-confidence merge. Pool diagnostics fall back to scraping individual replicas' `/metrics` — point-in-time values only. See Section 25.13 for fallback behavior of bundled vs. operator-customized alerting rules. |
| **Prometheus permanently absent** | Acceptable at Tier 1; **strongly discouraged at Tier 2/3** (preflight emits a WARN). Beyond the transient-down behavior above, the long-term degradations described in Section 25.4, Prometheus Requirement, apply: capacity recommendations return `confidence: 0.0` after every restart; alert rules with `for: "15m"` clauses misfire because no historical data exists; humans receive no Alertmanager pages because bundled rules are never loaded; agents cannot investigate sessions/pools from past time windows. |

The key property of this architecture: `lenny-ops` being up while the gateway is down is the high-value scenario — the ops surface remains available precisely when it's most needed. The reverse (ops down, gateway up) is low-impact — client traffic is fine, only ops tooling is unavailable. The degraded-mode design ensures that even during storage outages (Postgres and/or Redis down), the core operational loop — detect, diagnose, coordinate, escalate — continues to function, because these are exactly the scenarios where DevOps agents need the ops surface most.

### Total-Outage Recovery Runbook

When **both** `lenny-ops` and the gateway are unreachable — the watchdog reports total outage — the operability surface itself is offline. This subsection documents the manual escape hatches available to a human operator with cluster-admin access. AI agents cannot execute these steps because they require cluster access (`kubectl`); this is intentional, since agents that fix Lenny cannot run inside Lenny.

Each step below is the minimum action; details live in `docs/runbooks/total-outage.md`.

#### Triage (first 5 minutes)

1. **Confirm the outage scope.** From outside the cluster, attempt the gateway and `lenny-ops` Ingress. If both fail at the LB layer, suspect cluster-wide infrastructure (Ingress controller, DNS, cluster networking). If both fail at the application layer (200 from LB but app-level errors), suspect a shared dependency (Postgres, Redis, K8s API).
2. **Check the K8s API.** `kubectl cluster-info`. If this fails, the cluster itself is degraded — escalate to cluster admin. Lenny cannot recover before the cluster does.
3. **Check Lenny pod status.** `kubectl get pods -n lenny-system -o wide`. Look for `CrashLoopBackOff`, `ImagePullBackOff`, `Pending`, `Terminating` stuck states.
4. **Check storage dependencies.** `kubectl exec -it -n lenny-system deploy/postgres -- pg_isready` (or your managed-service equivalent). `kubectl exec -it -n lenny-system deploy/redis-master -- redis-cli ping`. If either fails, restoration of that dependency is the priority.

#### Recovery Paths

**Path A: Gateway crash-loop, `lenny-ops` is also down because it depends on gateway readiness for upgrade orchestration but otherwise wouldn't crash.**

1. `kubectl logs -n lenny-system deploy/lenny-gateway --previous` — read the previous container's exit logs.
2. Common cause: bad config from a recent change. `kubectl rollout undo deploy/lenny-gateway -n lenny-system` reverts to the prior ReplicaSet.
3. If that doesn't help, scale down to 0: `kubectl scale deploy/lenny-gateway -n lenny-system --replicas=0`. Then start `lenny-ops` access via port-forward (Path D below) and use the API to apply a known-good config before scaling back up.

**Path B: `lenny-ops` crash-loop, gateway is alive but watchdog can't reach it (no Ingress hostname known to the watchdog).**

1. `kubectl logs -n lenny-system deploy/lenny-ops --previous` — read the previous exit.
2. Common causes: bad migration during upgrade (`platform_upgrade_state` stuck in a transient phase), corrupted leader-election Lease (rare), Postgres connection failure at startup.
3. If migration-related, check `platform_upgrade_state.current_phase`. If `SchemaMigration` failed mid-way, the recovery is Path E (manual schema reconciliation).
4. If Lease-related, delete the Lease: `kubectl delete lease lenny-ops-leader -n lenny-system`. New pods will acquire a fresh lease.
5. Roll back to the previous image: `kubectl rollout undo deploy/lenny-ops -n lenny-system`.

**Path C: Postgres down (and therefore many things down).**

1. Triage Postgres directly (logs, disk space, connection limits). This is outside Lenny's surface.
2. While Postgres is recovering, `lenny-ops` will operate in its degraded mode (in-memory locks/escalations, K8s API diagnostics). The watchdog can still observe the platform via `/v1/admin/health/summary` on the gateway and the event stream.
3. Once Postgres recovers, `lenny-ops` reconciliation goroutines will restore consistency: buffered escalations flushed, idempotency keys re-tracked, lock outage epoch reconciled (Section 25.4).
4. After Postgres recovery, confirm reconciliation completed by checking `lenny_ops_lock_split_brain_detected_total` (should be 0 if no concurrent operations crossed the boundary).

**Path D: Total outage with gateway and `lenny-ops` both down — emergency port-forward.**

1. Port-forward `lenny-ops` directly: `kubectl port-forward -n lenny-system svc/lenny-ops 8090`. The Ingress is bypassed; NetworkPolicy is bypassed (port-forward establishes an API-server tunnel).
2. From a local terminal, hit `http://localhost:8090/v1/admin/health/summary`, `/v1/admin/diagnostics/connectivity`, and other endpoints to triage. The OIDC token can be obtained via `kubectl create token lenny-ops-sa -n lenny-system` (operator-time; the token is short-lived).
3. If `lenny-ops` itself isn't running (CrashLoopBackOff), port-forward to a Postgres pod and use SQL directly to inspect `platform_upgrade_state`, `ops_remediation_locks`, `audit_log` — but treat this as last resort.
4. If everything is broken, the canonical recovery is restore from backup. With both `lenny-ops` and gateway down, the restore Job must be created manually:
   ```bash
   kubectl create job --from=cronjob/lenny-backup-template lenny-restore-manual-1 -n lenny-system
   # Then kubectl exec into the pod to run pg_restore against backup ID NNN
   ```
   This is documented step-by-step in `docs/runbooks/manual-restore.md`.

**Path E: Stuck mid-upgrade (SchemaMigration failed on shard N of M).**

1. `kubectl exec -it -n lenny-system deploy/postgres -- psql -d lenny_platform -c "SELECT * FROM platform_upgrade_state"`.
2. Identify `metadata.failed_shard`. Diagnose the failure (locking conflict, disk space, etc.).
3. After fixing the root cause, port-forward `lenny-ops` and call `POST /v1/admin/platform/upgrade/proceed` — the migration resumes from the failed shard.
4. If un-resumable, roll back via restore from `platform_upgrade_state.pre_upgrade_backup_id`. This is destructive but deterministic.

#### Decision Tree

```
Watchdog reports both unreachable
  └→ Can `kubectl cluster-info` succeed?
       │
       ├── No → Escalate to cluster admin. Stop here.
       └── Yes
            └→ kubectl get pods -n lenny-system: which pods are bad?
                 │
                 ├── lenny-gateway CrashLooping → Path A
                 ├── lenny-ops CrashLooping → Path B
                 ├── Postgres pod NotReady → Path C
                 └── Both running but not reachable → Path D (port-forward triage)
```

#### Why human-only

These steps require cluster admin access (`kubectl exec`, `kubectl rollout undo`, `kubectl create job`). DevOps agents that operate via `lenny-ops` cannot perform them — that's the bootstrap problem (Section 25.1). The total-outage runbook is intentionally human-operator-facing and assumes the operator has cluster admin credentials and a working `kubectl`.

For organizations that want partial automation of total-outage recovery, the recommendation is to deploy a separate watchdog with cluster-admin credentials in a different cluster (or as a CronJob in the same cluster but a different namespace, with restricted RBAC) that can execute Path D operations. This watchdog is **not** part of Lenny — it's deployer-built.

---

## 25.16 Deployment Topology Summary

### Minimal (single-node / dev)

```
Pod: lenny-gateway                                   [mandatory]
  Container: gateway (ports 8080, 9090)
    - Client traffic, admin API, health, recommendations, version, config
    - /metrics for Prometheus

Pod: lenny-ops                                       [mandatory]
  Container: ops (port 8090)
  Ingress: localhost or port-forward for dev access
    - All ops features
    - Connects to gateway:8080, Postgres, Redis
    - NetworkPolicy still enforces external-only ingress (port-forward bypasses NetworkPolicy)

Prometheus                                           [optional at Tier 1]
  - Compose file provides one when --profile observability is enabled
  - lenny-ops degrades to per-replica fan-out when absent (acceptable for dev)
```

### Production

```
Deployment: lenny-gateway (HPA, 3+ replicas)         [mandatory]
  Service: lenny-gateway (ClusterIP → 8080)
  Service: lenny-gateway-pods (headless → 8080, for ops event buffer polling)
  - Client traffic, admin API, health, recommendations, event buffer

Deployment: lenny-ops (1 replica, leader-elected)    [mandatory]
  Service: lenny-ops (ClusterIP → 8090)
  Ingress: ops.lenny.example.com → lenny-ops:8090 (TLS)
  PodDisruptionBudget: minAvailable 1 (when replicas >= 2)
  NetworkPolicy: ingress from Ingress controller only; egress to gateway, Postgres, Redis, etc.
  - All ops features
  - External-only access via Ingress

StatefulSet: postgres (or managed RDS)               [mandatory]
StatefulSet: redis (Sentinel)                        [mandatory]

Prometheus (BYO — not deployed by Lenny)             [required at Tier 2/3]
  - Operator provides any Prometheus-HTTP-API-compatible backend:
    self-hosted Prometheus, Mimir, Cortex, Thanos, Victoria Metrics,
    Amazon Managed Prometheus, Grafana Cloud, etc.
  - Configured via ops.prometheus.url Helm value
  - Loads bundled alerting rules (Section 25.13) for human alerts
    and lenny-ops cross-replica health aggregation
  - See Section 25.4, Prometheus Requirement, for the BYO model and
    the consequences of running production without it
```

### External DevOps Agent

All DevOps agents live outside the Lenny installation. An agent may be a Deployment in a separate namespace or cluster, a cloud function, or a process on an operator's workstation. Its calls traverse the Ingress:

1. `lenny-ops` `/v1/admin/events/stream` — subscribe to operational events via SSE.
2. `lenny-ops` `/v1/admin/diagnostics/*` — diagnose issues.
3. `lenny-ops` `/v1/admin/runbooks/*` — fetch relevant runbook, execute steps.
4. `lenny-ops` `/v1/admin/remediation-locks` — acquire lock before remediation.
5. `lenny-ops` `/v1/admin/escalations` — escalate when remediation exceeds capabilities.
6. Gateway `/v1/admin/health/summary` — heartbeat check (if ops is also unreachable, confirms total platform failure vs. ops-only failure).

---

## 25.17 End-to-End Operational Example

This section walks through a concrete operational loop: from alert to verified remediation. The scenario is a warm pool exhaustion detected by an external watchdog agent.

### Scenario

The `default-gvisor` pool runs out of idle pods. Session creation starts failing. The watchdog agent detects, diagnoses, remediates, and verifies — entirely through the API, with no human intervention.

### Step 1: Observe — Event Arrives

The watchdog agent maintains a persistent SSE connection to `lenny-ops`:

```
GET /v1/admin/events/stream?eventType=alert_fired,health_status_changed HTTP/1.1
Host: ops.lenny.example.com
Authorization: Bearer <jwt>
Accept: text/event-stream
```

The gateway's health service detects that `default-gvisor` has 0 idle pods and fires the `WarmPoolExhausted` alert. The event emitter writes to the Redis stream and the in-memory event buffer. The SSE handler delivers:

```
id: 1681234567890-0
event: alert_fired
data: {"specversion":"1.0","id":"01HN7Y0QW6S7X9ZP8M2F5K4R3B","source":"//lenny.dev/gateway/gw-7f4c2a1e","type":"dev.lenny.alert_fired","time":"2026-04-17T14:32:08Z","datacontenttype":"application/json","data":{"severity":"critical","alertName":"WarmPoolExhausted","labels":{"pool":"default-gvisor"},"runbook":"warm-pool-exhaustion","suggestedAction":{"action":"SCALE_WARM_POOL","endpoint":"PUT /v1/admin/pools/default-gvisor/warm-count","body":{"minWarm":15},"reasoning":"Pool exhausted for 3 minutes. Peak claim rate: 4.2/min."}}}
```

### Step 2: Detect — Agent Evaluates

The agent parses the event. It sees `severity: "critical"` and `runbook: "warm-pool-exhaustion"`. It generates an `operationId` (`550e8400-...`) for this remediation effort.

### Step 3: Diagnose — Fetch Context

The agent calls the diagnostic endpoint and the runbook in parallel:

```
GET /v1/admin/diagnostics/pools/default-gvisor HTTP/1.1
X-Lenny-Operation-ID: 550e8400-...
X-Lenny-Agent-Name: prod-watchdog-us-east-1
```

Response:
```json
{
  "pool": "default-gvisor",
  "status": "unhealthy",
  "podCounts": {"idle": 0, "warming": 2, "claimed": 18, "terminating": 0},
  "config": {"minWarm": 5, "maxPods": 50, "image": "..."},
  "bottleneck": {
    "category": "DEMAND_EXCEEDS_SUPPLY",
    "summary": "Claim rate (4.2/min) exceeds replenishment rate (0.8/min). No pod failures.",
    "details": {"claimRate": 4.2, "replenishmentRate": 0.8, "idleDuration": "8m"}
  },
  "suggestedActions": [
    {"action": "SCALE_WARM_POOL", "endpoint": "PUT /v1/admin/pools/default-gvisor/warm-count", "body": {"minWarm": 15}, "reasoning": "...", "runbook": "warm-pool-exhaustion"}
  ],
  "crdSyncStatus": {"synced": true}
}
```

```
GET /v1/admin/runbooks/warm-pool-exhaustion HTTP/1.1
```

The agent reads the runbook and confirms the diagnosis aligns with the "demand exceeds supply" decision path: no pod failures, no quota issues, just undersized pool. The runbook says to scale.

### Step 4: Decide — Agent Chooses Action

The agent decides to follow the `suggestedAction`: scale `minWarm` from 5 to 15. The bottleneck is demand-driven (no infrastructure failure), so scaling is appropriate. The runbook confirms this is the right action for this bottleneck category.

### Step 5: Act — Acquire Lock and Remediate

First, the agent acquires a remediation lock:

```
POST /v1/admin/remediation-locks HTTP/1.1
X-Lenny-Operation-ID: 550e8400-...

{"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": 300}
```

Response: `201 Created` with `{"id": "lock-abc", "expiresAt": "...", "lockStore": "postgres"}`.

Then the agent executes the remediation:

```
PUT /v1/admin/pools/default-gvisor/warm-count HTTP/1.1
X-Lenny-Operation-ID: 550e8400-...
Idempotency-Key: 7c9e6679-...

{"minWarm": 15, "confirm": true}
```

Response: `200 OK`.

### Step 6: Verify — Confirm Recovery

The agent waits 2 minutes (as the runbook's "expected outcome" specifies), then re-checks:

```
GET /v1/admin/diagnostics/pools/default-gvisor HTTP/1.1
X-Lenny-Operation-ID: 550e8400-...
```

Response shows `"idle": 8, "warming": 5, "claimed": 14`. The pool is recovering. The agent also checks health:

```
GET /v1/admin/health/default-gvisor HTTP/1.1
```

The `WarmPoolExhausted` alert has resolved. The agent releases the lock:

```
DELETE /v1/admin/remediation-locks/lock-abc HTTP/1.1
```

The remediation is complete. The audit trail shows four calls tied to operation `550e8400-...`: lock acquire, pool scale, diagnostic re-check, lock release.

### Failure Path: Escalation

If after 5 minutes the pool had not recovered (e.g., bottleneck changed to `NODE_PRESSURE`), the agent would create an escalation:

```
POST /v1/admin/escalations HTTP/1.1
X-Lenny-Operation-ID: 550e8400-...

{
  "severity": "critical",
  "alertName": "WarmPoolExhausted",
  "runbookName": "warm-pool-exhaustion",
  "summary": "Scaled pool from 5 to 15, but recovery stalled. Bottleneck changed to NODE_PRESSURE. Requires cluster admin.",
  "failedActions": [
    {"action": "SCALE_WARM_POOL", "result": "Pool scaled but pods not scheduling due to node resource pressure."}
  ]
}
```

The `escalation_created` event is emitted to the event stream. A webhook subscriber routes it to PagerDuty.

### Multi-Cluster Note

This design operates per-installation. In a multi-cluster deployment, each Lenny installation has its own `lenny-ops` Ingress. A multi-cluster watchdog agent maintains SSE connections to each installation's `lenny-ops` and performs the same operational loop independently per cluster. Cross-cluster coordination (e.g., draining traffic from one cluster before upgrading) is outside the scope of this section — it is the responsibility of the deployment orchestrator or the agent's own logic.

---
