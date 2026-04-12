# Agent Operability — Technical Design (Section 25)

**Status:** Draft
**Applies to:** `SPEC.md` — proposed Section 25
**Date:** 2026-04-12

Lenny is designed to be natively operable by AI agents. DevOps agents — whether running inside Lenny, in separate orchestration platforms, or on an operator's workstation — can deploy, configure, monitor, troubleshoot, upgrade, and maintain a Lenny installation entirely through APIs, without direct Kubernetes, database, or storage access.

---

## 25.1 Design Philosophy and Agent Model

### The Operational Loop

The existing admin API (Section 15.1) provides CRUD operations for all platform resources. CRUD is necessary but not sufficient for autonomous operations. A DevOps agent executing an operational loop requires:

1. **Observe** — structured platform health, not raw metric scraping
2. **Detect** — real-time notification of operational events, not polling
3. **Diagnose** — structured causality chains, not log parsing
4. **Decide** — actionable recommendations, not threshold interpretation
5. **Act** — API-encapsulated remediation, not kubectl/psql/redis-cli
6. **Verify** — confirmation that the action had the intended effect

Each subsection below fills a gap in this loop. Together, they ensure that every step an operator takes in every runbook (Section 17.7) can be performed by an API call, and every signal an operator reads from a dashboard is available as structured data.

### The Bootstrap Problem and Two-Tier Agent Model

The agent that fixes Lenny cannot run inside Lenny. If the warm pool is exhausted, an agent inside Lenny cannot start a session to fix the warm pool. If Postgres is down, session state is gone — including the DevOps agent's session. If the gateway is crash-looping, no agent inside Lenny can reach the admin API.

This yields a two-tier model:

**Tier 1 — Watchdog Agent.** Handles the critical path: detecting and remediating failures that affect Lenny's own availability. This agent **must** live outside Lenny — its own Deployment in the cluster, a separate cluster, a cloud function, or a developer's workstation. It is the one that monitors `GET /v1/admin/health/summary` on a heartbeat, receives operational events via webhooks (Section 25.3), executes runbooks for platform-level failures, drives platform upgrades, and triggers backups. If the heartbeat itself fails, the watchdog knows Lenny is unreachable.

**Tier 2 — Ops Agents.** Handle operational tasks that assume Lenny is healthy: deploying runtimes, managing tenant quotas, running capacity reviews, reconciling configuration drift, rotating credentials. These agents *may* run inside Lenny as regular sessions (using the MCP Management Server as a connector) or *may* run externally — the deployer chooses based on their operational topology. Either way, they connect to the same API surface.

Both tiers connect through the same admin REST API and MCP Management Server. The design does not distinguish between them at the protocol level — the difference is purely in deployment topology and failure-domain isolation.

### Design Constraints

- **No Kubernetes access required.** Every runbook diagnostic and remediation step that currently uses `kubectl`, `psql`, `redis-cli`, or `mc` has an admin API equivalent. Direct cluster access is available for escape-hatch scenarios but is never required for standard operations.
- **Structured over textual.** All operational responses use typed JSON schemas. Error codes, severity levels, and suggested actions are machine-parseable.
- **Idempotent and safe by default.** Diagnostic endpoints are read-only. Remediation endpoints are idempotent. Destructive actions require explicit `"confirm": true` in the body — without it, the endpoint returns a dry-run preview of what would happen.
- **Audited.** All agent-initiated operations produce the same audit trail as human-initiated operations (Section 11.7). The audit event includes the caller's identity, which distinguishes agent service accounts from human operators.

### Authentication for Agent Callers

Agent callers authenticate using the same OIDC-based mechanism as human operators and `lenny-ctl` (Section 15.1). Deployers create dedicated service accounts with the `platform-admin` or `tenant-admin` role. A `caller_type: "agent"` claim in the JWT token identifies agent callers in audit events and metrics. No separate authentication mechanism is introduced — agents are first-class API consumers.

---

## 25.2 Management Listener

A dedicated HTTP listener on a separate port, isolated from client traffic. This is the foundation for all operability features — it ensures the operational surface remains reachable even when the main gateway port is overwhelmed by client sessions or when some subsystems are degraded.

### Architecture

A second `net/http.Server` bound to `:9091` (configurable via `management.listenAddr` Helm value), started alongside the main gateway listener in the gateway's `main()` startup sequence. The management listener shares the gateway process but has its own `http.ServeMux`, goroutine budget, and TLS configuration.

```go
// pkg/gateway/management/listener.go

type ManagementListener struct {
    server        *http.Server
    mux           *http.ServeMux
    pool          *semaphore.Weighted    // dedicated goroutine pool
    authEvaluator auth.Evaluator         // shared with main gateway
    healthSvc     HealthService
    eventSvc      EventService
    diagSvc       DiagnosticService
    runbookSvc    RunbookService
    auditQuerySvc AuditQueryService
    driftSvc      DriftService
    capacitySvc   CapacityService
    backupSvc     BackupService
    platformSvc   PlatformService
    mcpMgmtAdapter *ManagementMCPAdapter
    metrics       *prometheus.Registry   // shared in-process registry
}

func (l *ManagementListener) Start(ctx context.Context) error
func (l *ManagementListener) Shutdown(ctx context.Context) error
```

### Goroutine Pool

A dedicated `semaphore.Weighted` with `management.maxConcurrent` (default: 50, configurable via Helm). This is intentionally separate from the four client-traffic subsystem pools (Section 4.1). When the management pool is saturated, new management requests receive `503 MANAGEMENT_POOL_EXHAUSTED` with `Retry-After: 1`. The pool is sized small because management endpoints are low-traffic, high-value — a saturated management pool indicates a misconfigured monitoring agent, not legitimate load.

### Authentication

Same OIDC middleware as the main admin API. The management listener reuses the gateway's `AuthEvaluator` and requires `platform-admin` or `tenant-admin` role on all endpoints. No anonymous access except `/mgmt/healthz` (K8s probe target for the management listener's own readiness).

### TLS

Same TLS certificate as the main listener by default. Deployers can configure a dedicated certificate via `management.tls.certSecretName` for environments where the management network has a separate CA.

### Startup and Degradation

The management listener starts **before** dependency probes complete. If Postgres, Redis, or MinIO are unreachable at startup, the listener still accepts connections — individual endpoint handlers report dependency status rather than crashing. This is critical: the watchdog agent must be able to call `/v1/admin/health` to discover that Postgres is down. The management listener has `ReadHeaderTimeout: 5s` and `WriteTimeout: 30s` to prevent slow-client resource exhaustion.

### Kubernetes Integration

The Helm chart adds:
- A second `containerPort` (9091) to the gateway Deployment
- A second `Service` (`lenny-management`, ClusterIP) targeting port 9091
- A `readinessProbe` on `/mgmt/healthz`

The management Service is **not** exposed via the main Ingress. It is accessible only within the cluster (for internal agents) or via a dedicated internal Ingress/LoadBalancer configured by the deployer (for external watchdog agents). The Helm chart includes an optional `management.ingress.enabled` flag for deployers who want external access.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_management_request_duration_seconds` | Histogram | `endpoint`, `method` | Per-endpoint latency |
| `lenny_management_request_total` | Counter | `endpoint`, `method`, `status_code` | Request count |
| `lenny_management_pool_in_use` | Gauge | | Current goroutine pool utilization |
| `lenny_management_pool_rejected_total` | Counter | | Requests rejected due to pool saturation |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `ManagementListenerUnavailable` | `lenny_management_request_total` rate is 0 for > 60s while the gateway is running | Critical |
| `ManagementPoolSaturated` | `lenny_management_pool_in_use / management.maxConcurrent > 0.9` for > 60s | Warning |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `MANAGEMENT_POOL_EXHAUSTED` | `TRANSIENT` | 503 | Management listener goroutine pool is saturated. `Retry-After` header included. |

### Audit Events

`management.listener_started` (at startup, includes listen address), `management.listener_stopped` (at shutdown). Individual features emit their own audit events per request.

---

## 25.3 Platform Health API

A unified health surface that synthesizes component status, metric thresholds, and alert states into structured, actionable responses. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/health` | Aggregate health of all components |
| `GET` | `/v1/admin/health/{component}` | Deep-dive into one component with diagnostics and metric values |
| `GET` | `/v1/admin/health/summary` | Minimal status for synthetic health checks |

### Go Interface

```go
// pkg/gateway/management/health.go

type HealthService interface {
    GetAggregateHealth(ctx context.Context) (*AggregateHealthResponse, error)
    GetComponentHealth(ctx context.Context, component string) (*ComponentHealthResponse, error)
    GetHealthSummary(ctx context.Context) (*HealthSummaryResponse, error)
}

type ComponentStatus string
const (
    StatusHealthy   ComponentStatus = "healthy"
    StatusDegraded  ComponentStatus = "degraded"
    StatusUnhealthy ComponentStatus = "unhealthy"
)

type SuggestedAction struct {
    Action    string          `json:"action"`     // SCALE_WARM_POOL, ADD_CREDENTIALS, OPEN_CIRCUIT_BREAKER, etc.
    Endpoint  string          `json:"endpoint"`   // admin API endpoint to call
    Body      json.RawMessage `json:"body"`       // request body
    Reasoning string          `json:"reasoning"`  // human-readable explanation of why
}

type ComponentHealth struct {
    Status          ComponentStatus  `json:"status"`
    Details         json.RawMessage  `json:"details"`
    SuggestedAction *SuggestedAction `json:"suggestedAction,omitempty"`
}
```

### Data Sources

The `HealthService` reads from three sources, none of which require Prometheus:

1. **In-process metric registry.** The gateway reads gauge/counter values directly from the same `prometheus.Registry` that Prometheus scrapes. No Prometheus query needed — this works even when Prometheus is down.

2. **Redis circuit breaker state.** Reads `cb:{name}` keys from `StoreRouter.PlatformRedis()` (Section 11.6). Falls back to the gateway's in-process cache if Redis is unreachable (same behavior as Section 12.4).

3. **Lightweight dependency probes.** TCP connect + single-query probes against Postgres (`SELECT 1`), Redis (`PING`), MinIO (`HeadBucket`), K8s API server (`/healthz`), cert-manager (certificate status), and registered connectors. Each probe has a hard timeout of 2 seconds. Probes run in parallel.

### Health Derivation Rules

Component status is derived deterministically from the same threshold expressions used by the alerting rules (Section 16.5):

- `healthy` — no firing alerts for this component
- `degraded` — warning-severity alerts firing
- `unhealthy` — critical-severity alerts firing

The `HealthService` maintains an in-memory alert state tracker that evaluates the same threshold expressions as the Prometheus alerting rules but reads from the in-process metric registry. This means `/v1/admin/health` returns accurate results even when Prometheus itself is unreachable.

### `suggestedAction` Contract

When a component is degraded or unhealthy, the `suggestedAction` object provides a machine-executable remediation hint. The `action` field is an enum (`SCALE_WARM_POOL`, `ADD_CREDENTIALS`, `RESTART_COMPONENT`, `OPEN_CIRCUIT_BREAKER`, `TRIGGER_FAILOVER`, etc.). The `endpoint` and `body` fields contain the exact admin API call to execute the suggestion. The `reasoning` field explains why this action is recommended. Agents can execute the suggestion by calling the endpoint directly. The `suggestedAction` is advisory — the agent is free to ignore it or modify the parameters.

### Response Schemas

**Aggregate health (`GET /v1/admin/health`):**

```json
{
  "status": "degraded",
  "checkedAt": "2026-04-08T14:30:00Z",
  "components": {
    "gateway": {
      "status": "healthy",
      "replicas": { "ready": 3, "desired": 3 }
    },
    "warmPools": {
      "status": "degraded",
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
            "reasoning": "Pool exhausted for 8 minutes. Claimed (18) exceeds minWarm (5) by 3.6x."
          }
        }
      ]
    },
    "postgres": {
      "status": "healthy",
      "replicationLagMs": 12,
      "connectionPoolUtilization": 0.35
    },
    "redis": {
      "status": "healthy",
      "memoryUtilization": 0.42,
      "sentinelQuorum": true
    },
    "objectStore": { "status": "healthy" },
    "certManager": {
      "status": "healthy",
      "nextCertExpiry": "2026-04-08T18:30:00Z"
    },
    "credentialPools": { "status": "healthy", "pools": [] },
    "controllers": {
      "status": "healthy",
      "warmPoolController": { "leaderElected": true, "lastReconcile": "2026-04-08T14:29:55Z" },
      "poolScalingController": { "leaderElected": true, "lastReconcile": "2026-04-08T14:29:58Z" }
    },
    "circuitBreakers": { "status": "healthy", "openBreakers": [] }
  },
  "activeAlerts": []
}
```

**Component deep-dive (`GET /v1/admin/health/{component}`):** Same schema as the component object above, plus `diagnostics` (recent events, probe details) and `metrics` (raw metric values from the in-process registry for the requested component).

**Minimal summary (`GET /v1/admin/health/summary`):**

```json
{
  "status": "degraded",
  "unhealthy": ["warmPools"],
  "degraded": ["warmPools"],
  "checkedAt": "2026-04-08T14:30:00Z"
}
```

### Caching

Component probe results are cached in-memory for 5 seconds to avoid probe storms from concurrent health checks. The cache is per-gateway-replica (not shared). Metric registry reads are instantaneous (same-process).

### Storage

No new Postgres tables or Redis keys. The health endpoint is purely computed from runtime state.

### Degradation

If Postgres is unreachable: `postgres.status` reports `"unhealthy"` with `"details": {"reachable": false}`. The health endpoint itself returns 200 — it does not crash. If Redis is unreachable: `redis.status` reports `"unhealthy"`; circuit breaker state falls back to in-process cache. If MinIO is unreachable: `objectStore.status` reports `"unhealthy"`. **The health endpoint itself never returns 5xx** — it reports what it can observe.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_health_check_duration_seconds` | Histogram | `component` | Probe latency per component |
| `lenny_health_status` | Gauge | `component` | 0=healthy, 1=degraded, 2=unhealthy |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `PlatformHealthDegraded` | `lenny_health_status{component="aggregate"} >= 1` for > 5m | Warning |
| `PlatformHealthUnhealthy` | `lenny_health_status{component="aggregate"} == 2` for > 2m | Critical |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `UNKNOWN_HEALTH_COMPONENT` | `PERMANENT` | 404 | Requested component name is not recognized |

### Audit Events

None. Health checks are read-only and high-frequency. Health status transitions are emitted as operational events (Section 25.4).

---

## 25.4 Operational Event Stream

A real-time feed of platform operational events. Agents subscribe instead of polling individual endpoints. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/events/stream` | SSE stream of operational events |
| `GET` | `/v1/admin/events` | Polling endpoint with pagination |
| `POST` | `/v1/admin/event-subscriptions` | Register a webhook for operational events |
| `GET` | `/v1/admin/event-subscriptions` | List subscriptions |
| `GET` | `/v1/admin/event-subscriptions/{id}` | Get subscription details and delivery stats |
| `PUT` | `/v1/admin/event-subscriptions/{id}` | Update subscription filters |
| `DELETE` | `/v1/admin/event-subscriptions/{id}` | Delete a subscription |
| `GET` | `/v1/admin/event-subscriptions/{id}/deliveries` | List recent delivery attempts |

### Go Interface

```go
// pkg/gateway/management/events.go

type EventService interface {
    Emit(ctx context.Context, event OperationalEvent) error
    StreamEvents(ctx context.Context, w http.ResponseWriter, filter EventFilter) error
    ListEvents(ctx context.Context, filter EventFilter, cursor string, limit int) (*EventPage, error)
    CreateSubscription(ctx context.Context, sub SubscriptionRequest) (*Subscription, error)
    ListSubscriptions(ctx context.Context) ([]Subscription, error)
    GetSubscription(ctx context.Context, id string) (*Subscription, error)
    UpdateSubscription(ctx context.Context, id string, update SubscriptionUpdate) (*Subscription, error)
    DeleteSubscription(ctx context.Context, id string) error
    ListDeliveries(ctx context.Context, subID string, cursor string, limit int) (*DeliveryPage, error)
}

type OperationalEvent struct {
    EventID   string          `json:"eventId"`   // Redis stream ID
    Type      string          `json:"type"`      // event type enum
    Severity  string          `json:"severity"`  // "critical", "warning", "info"
    Timestamp time.Time       `json:"timestamp"`
    Payload   json.RawMessage `json:"payload"`
}

type EventFilter struct {
    Types    []string   // event type filter
    Severity []string   // severity filter
    Pool     string     // pool scope
    Since    *time.Time // replay from timestamp
}
```

### Event Types

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
| `drift_detected` | Configuration drift detected (Section 25.9) | Resource type, name, drifted fields |
| `health_status_changed` | Aggregate health transitioned | Old status, new status, triggering component |
| `runbook_escalated` | Runbook execution hit an escalation node | Runbook name, execution ID, escalation reason |
| `runbook_completed` | Runbook execution finished | Runbook name, execution ID, outcome |

### Storage

**Redis capped stream.** Key: `ops:events:stream` (platform-scoped, not tenant-prefixed). Uses Redis Streams (`XADD` with `MAXLEN ~ 10000`). The Redis stream ID serves as the monotonically increasing `eventId`. The `~` (approximate trimming) avoids O(N) trim cost on every add.

**Postgres tables for webhook subscriptions:**

```sql
CREATE TABLE ops_event_subscriptions (
    id              TEXT PRIMARY KEY,       -- "sub-" + UUIDv4
    callback_url    TEXT NOT NULL,
    types           TEXT[] NOT NULL,        -- event type filter
    severity        TEXT[],                 -- optional severity filter
    secret_hash     TEXT NOT NULL,          -- bcrypt hash of HMAC secret
    description     TEXT,
    created_by      TEXT NOT NULL,          -- actor identity
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    active          BOOLEAN NOT NULL DEFAULT true
);

CREATE INDEX idx_ops_event_subs_active ON ops_event_subscriptions (active) WHERE active = true;
```

```sql
CREATE TABLE ops_event_deliveries (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES ops_event_subscriptions(id),
    event_id        TEXT NOT NULL,          -- Redis stream ID
    event_type      TEXT NOT NULL,
    status          TEXT NOT NULL,          -- "delivered", "failed", "pending"
    attempts        INT NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ops_deliveries_sub_status ON ops_event_deliveries (subscription_id, status);
CREATE INDEX idx_ops_deliveries_created ON ops_event_deliveries (created_at);
```

No `tenant_id` columns — event subscriptions are platform-scoped and require `platform-admin`.

### Redis Key Patterns

| Key | Purpose |
|-----|---------|
| `ops:events:stream` | Capped Redis Stream (MAXLEN ~10000) |
| `ops:events:sub:{sub_id}:cursor` | Last-delivered event ID per webhook subscription |

### Event Generation

Existing subsystems publish events by calling `EventService.Emit()`:
- **Alert state changes:** The in-process alert evaluator (Section 25.3 health derivation) calls `Emit` on firing/resolving transitions.
- **Upgrade state machine:** `RuntimeUpgrade` controller calls `Emit` on phase transitions.
- **Circuit breaker changes:** Circuit breaker handler calls `Emit` after Redis write.
- **Session failures:** Session manager calls `Emit` on terminal failure transitions.
- **Credential events:** Credential pool manager calls `Emit` on rotation/exhaustion.
- **Health transitions:** `HealthService` calls `Emit` when aggregate status changes.

### SSE Delivery

The SSE handler holds an open HTTP response and reads from the Redis stream via `XREAD BLOCK 0` in a goroutine. Client reconnection with `Last-Event-ID` header resumes via `XRANGE ops:events:stream <last_id>+`. The SSE goroutine is counted against the management goroutine pool.

**Filtering.** Clients filter via query parameters: `?types=alert_fired,session_failed` (comma-separated), `?severity=critical,warning`, `?pool=default-gvisor`, `?since=2026-04-08T14:00:00Z` (replay after timestamp for catch-up).

### Webhook Delivery

A background goroutine per active subscription reads events from the Redis stream and POSTs to the callback URL. Each delivery includes:
- `X-Lenny-Signature` header: HMAC-SHA256 of the payload using the subscription's secret
- `X-Lenny-Event-Type` header: event type
- `X-Lenny-Event-ID` header: event ID for deduplication

Retry: 3 attempts with exponential backoff (1s, 5s, 30s). The `callback_url` must pass the same SSRF validation as session callback URLs (Section 14): HTTPS-only, private IP rejection, DNS pinning, optional domain allowlist.

### Degradation

If Redis is unreachable: SSE stream and polling endpoint return `503 EVENT_STREAM_UNAVAILABLE` with `Retry-After: 5`. Webhook delivery pauses and resumes on recovery. Events generated during Redis outage are lost — Redis Streams are not durable across restart. This is acceptable because operational events are notifications, not the system of record. The underlying state (alerts, circuit breakers, upgrade status) is always queryable via the health and diagnostic endpoints.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_ops_events_emitted_total` | Counter | `type` | Events emitted by type |
| `lenny_ops_events_stream_length` | Gauge | | Current Redis stream length |
| `lenny_ops_events_sse_active_connections` | Gauge | | Active SSE connections |
| `lenny_ops_events_webhook_delivery_total` | Counter | `subscription_id`, `status` | Webhook delivery outcomes |
| `lenny_ops_events_webhook_delivery_latency_seconds` | Histogram | `subscription_id` | Webhook delivery latency |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `OpsEventStreamUnavailable` | Redis stream `ops:events:stream` unreachable for > 60s | Warning |
| `WebhookDeliveryBacklog` | Failed webhook delivery rate > 1/s for > 5m | Warning |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `INVALID_EVENT_FILTER` | `PERMANENT` | 400 | Unrecognized event type or severity in filter |
| `WEBHOOK_VALIDATION_FAILED` | `PERMANENT` | 422 | Callback URL failed SSRF validation |
| `EVENT_STREAM_UNAVAILABLE` | `TRANSIENT` | 503 | Redis stream unreachable |
| `SUBSCRIPTION_NOT_FOUND` | `PERMANENT` | 404 | Subscription ID not found |

### Audit Events

`ops_event.subscription_created`, `ops_event.subscription_updated`, `ops_event.subscription_deleted`. Subscription mutations are audited; individual event emission and delivery are not (too high volume — tracked via metrics instead).

---

## 25.5 Diagnostic Endpoints

Structured diagnostic endpoints that encapsulate the diagnosis steps from each operational runbook (Section 17.7). These replace `kubectl`, `psql`, `redis-cli`, and `mc` commands with API calls that return structured results. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/diagnostics/sessions/{id}` | Structured cause chain for a session |
| `GET` | `/v1/admin/diagnostics/pools/{name}` | Pool bottleneck analysis |
| `GET` | `/v1/admin/diagnostics/connectivity` | Dependency connectivity checks |
| `GET` | `/v1/admin/diagnostics/credential-pools/{name}` | Credential pool health diagnosis |

### Go Interface

```go
// pkg/gateway/management/diagnostics.go

type DiagnosticService interface {
    DiagnoseSession(ctx context.Context, sessionID string) (*SessionDiagnosis, error)
    DiagnosePool(ctx context.Context, poolName string) (*PoolDiagnosis, error)
    CheckConnectivity(ctx context.Context) (*ConnectivityReport, error)
    DiagnoseCredentialPool(ctx context.Context, poolName string) (*CredentialPoolDiagnosis, error)
}

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
}
```

### Data Sources per Endpoint

**Session diagnosis (`DiagnoseSession`):**
1. Reads `sessions` table via `StoreRouter.SessionShard(session_id)` — gets state, terminal reason, retry count
2. Reads `agent_pod_state` table — gets pod exit code, OOM flag, container status
3. Queries K8s API for `v1.EventList` on the pod — gets image pull errors, node pressure events, scheduling failures
4. Reads retry log from session metadata in Postgres
5. Builds cause chain by cross-referencing: exit code 137 + OOM flag → `OOM_KILLED`; exit code 1 + setup phase → `SETUP_COMMAND_FAILED`; etc.

**Pool diagnosis (`DiagnosePool`):**
1. Reads `agent_pod_state` table grouped by state → pod count breakdown
2. Reads in-process metrics: `lenny_warmpool_pod_startup_duration_seconds` (histogram p99), `lenny_warmpool_replenishment_rate` (gauge), `lenny_warmpool_warmup_failure_total` (counter by reason)
3. Reads pool config from `GET /v1/admin/pools/{name}` (internal loopback)
4. Reads CRD sync status from `GET /v1/admin/pools/{name}/sync-status` (internal loopback)
5. Classifies bottleneck: if `warmup_failure_total{reason="image_pull_error"} > 0` → `IMAGE_PULL`; if `warmup_failure_total{reason="node_pressure"} > 0` → `NODE_PRESSURE`; if `warmup_failure_total{reason="resource_quota_exceeded"} > 0` → `QUOTA_EXHAUSTED`; if replenishment rate < claim rate → `DEMAND_EXCEEDS_SUPPLY`

**Connectivity (`CheckConnectivity`):**
Parallel probes to all dependencies with 2s timeout each. Same probe logic as the health API (Section 25.3) but returns richer detail: latency, TLS status, schema version, replication lag, connection pool utilization, etc. Also probes all registered connectors (from `GET /v1/admin/connectors` internal call).

**Credential pool diagnosis (`DiagnoseCredentialPool`):**
1. Reads credential pool state from `GET /v1/admin/credential-pools/{name}` (internal loopback)
2. Reads `lenny_credential_pool_utilization` and `lenny_credential_provider_rate_limit_total` from metric registry
3. Identifies hot keys (credentials with highest rate-limit event count in 24h window)
4. Computes utilization trend

### Degradation

If Postgres is unreachable: session and pool diagnostics return `503`. Connectivity check still runs and reports Postgres as unreachable. If K8s API is unreachable: session diagnosis omits pod events and includes `"podEventsUnavailable": true` — a partial result with HTTP 207 `DIAGNOSTICS_PARTIAL`.

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

`diagnostics.session_diagnosed`, `diagnostics.pool_diagnosed`, `diagnostics.connectivity_checked`, `diagnostics.credential_pool_diagnosed`. Each includes the caller identity and the resource inspected.

---

## 25.6 Machine-Executable Runbooks

Each operational runbook (Section 17.7) is published in a machine-executable format alongside the human-readable Markdown version. The machine-executable format enables agents to follow runbook procedures autonomously. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/runbooks` | List all registered runbooks |
| `GET` | `/v1/admin/runbooks/{name}` | Get a runbook definition |
| `POST` | `/v1/admin/runbooks` | Register a custom runbook |
| `PUT` | `/v1/admin/runbooks/{name}` | Update a runbook (requires `If-Match`) |
| `DELETE` | `/v1/admin/runbooks/{name}` | Delete a custom runbook (Helm-sourced runbooks cannot be deleted) |
| `POST` | `/v1/admin/runbooks/{name}/execute` | Execute a runbook |
| `GET` | `/v1/admin/runbooks/{name}/executions` | List recent executions |
| `GET` | `/v1/admin/runbook-executions/{id}` | Get execution status and step outputs |
| `POST` | `/v1/admin/runbook-executions/{id}/confirm` | Confirm a pending remediation step |

### Runbook Schema

Machine-executable runbooks are stored as YAML files in `files/runbooks/*.runbook.yaml` in the Helm chart, and loaded at gateway startup into the runbook registry. Custom runbooks are registered via the admin API.

```yaml
apiVersion: lenny.dev/v1
kind: OperationalRunbook
metadata:
  name: warm-pool-exhaustion
  humanReadable: warm-pool-exhaustion.md
  version: "1.0"

triggers:
  - alertName: WarmPoolExhausted
    severity: critical
  - alertName: WarmPoolLow
    severity: warning

parameters:
  - name: pool
    source: alert.labels.pool
    type: string
    required: true

diagnosis:
  steps:
    - id: check_pool_status
      description: Get pool diagnostic information
      action:
        method: GET
        endpoint: "/v1/admin/diagnostics/pools/{{pool}}"
      outputs:
        - name: bottleneck_category
          jsonPath: "$.bottleneck.category"
        - name: idle_count
          jsonPath: "$.podCounts.idle"
        - name: image_pull_healthy
          jsonPath: "$.bottleneck.details.imagePullHealthy"
        - name: node_pressure
          jsonPath: "$.bottleneck.details.nodeResourcePressure"
        - name: quota_exhausted
          jsonPath: "$.bottleneck.details.quotaExhausted"
        - name: suggested_min_warm
          jsonPath: "$.suggestedActions[0].body.minWarm"

decision:
  tree:
    - condition: "{{quota_exhausted}} == true"
      goto: remediate_quota
    - condition: "{{image_pull_healthy}} == false"
      goto: remediate_image_pull
    - condition: "{{node_pressure}} == true"
      goto: remediate_node_pressure
    - condition: "{{bottleneck_category}} == 'DEMAND_EXCEEDS_SUPPLY'"
      goto: remediate_scale

remediation:
  procedures:
    - id: remediate_scale
      description: Emergency scale the warm pool
      confirmation: required
      steps:
        - id: scale_pool
          description: Increase minWarm
          action:
            method: PUT
            endpoint: "/v1/admin/pools/{{pool}}/warm-count"
            body:
              minWarm: "{{suggested_min_warm}}"
          successCondition:
            httpStatus: 200

    - id: remediate_image_pull
      description: Image pull failures detected
      escalation:
        reason: "Image pull failure requires registry credential or image availability investigation."
        severity: critical

    - id: remediate_node_pressure
      description: Node resource pressure detected
      escalation:
        reason: "Node resource pressure — may require cluster scaling or node cordon."
        severity: critical

    - id: remediate_quota
      description: Namespace ResourceQuota exhausted
      escalation:
        reason: "ResourceQuota prevents pod creation — requires cluster admin intervention."
        severity: critical

verification:
  steps:
    - id: verify_recovery
      description: Verify pool is recovering
      wait: 120s
      action:
        method: GET
        endpoint: "/v1/admin/diagnostics/pools/{{pool}}"
      successCondition:
        jsonPath: "$.podCounts.idle"
        operator: ">="
        value: 1
      failureAction:
        escalation:
          reason: "Pool did not recover within 2 minutes after remediation."
          severity: critical
```

### Go Interface

```go
// pkg/gateway/management/runbook.go

type RunbookService interface {
    ListRunbooks(ctx context.Context) ([]RunbookSummary, error)
    GetRunbook(ctx context.Context, name string) (*RunbookDefinition, error)
    RegisterRunbook(ctx context.Context, def RunbookDefinition) error
    UpdateRunbook(ctx context.Context, name string, def RunbookDefinition, etag string) error
    DeleteRunbook(ctx context.Context, name string) error
    ExecuteRunbook(ctx context.Context, name string, req RunbookExecuteRequest) (*RunbookExecution, error)
    GetExecution(ctx context.Context, execID string) (*RunbookExecution, error)
    ListExecutions(ctx context.Context, runbookName string) ([]RunbookExecutionSummary, error)
    ConfirmStep(ctx context.Context, execID string) (*RunbookExecution, error)
}

type RunbookExecuteRequest struct {
    Parameters map[string]string `json:"parameters"`
    Mode       string            `json:"mode"`  // "auto", "step", "dry-run"
}
```

### Postgres Schema

```sql
CREATE TABLE runbook_definitions (
    name            TEXT PRIMARY KEY,
    version         TEXT NOT NULL,
    source          TEXT NOT NULL,          -- "helm" or "api"
    spec            JSONB NOT NULL,         -- full YAML parsed to JSON
    triggers        JSONB,                  -- alert triggers for auto-matching
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    etag            TEXT NOT NULL DEFAULT gen_random_uuid()::TEXT
);

CREATE TABLE runbook_executions (
    id              TEXT PRIMARY KEY,       -- "exec-" + UUIDv4
    runbook_name    TEXT NOT NULL REFERENCES runbook_definitions(name),
    mode            TEXT NOT NULL,          -- "auto", "step", "dry-run"
    parameters      JSONB NOT NULL,
    state           TEXT NOT NULL,          -- "running", "awaiting_confirmation",
                                            -- "escalated", "completed", "failed"
    current_step_id TEXT,
    step_outputs    JSONB NOT NULL DEFAULT '{}',
    decision_path   TEXT[],                 -- ordered list of step IDs executed
    started_by      TEXT NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    error           TEXT
);

CREATE INDEX idx_runbook_exec_name ON runbook_executions (runbook_name, started_at DESC);
CREATE INDEX idx_runbook_exec_state ON runbook_executions (state)
    WHERE state IN ('running', 'awaiting_confirmation');
```

### Execution Engine

The execution engine is a server-side state machine running in a goroutine (counted against the management pool). Each step:

1. Evaluates template expressions (`{{param}}`) against the parameters map + accumulated step outputs
2. Makes an **internal loopback HTTP call** to the admin API endpoint specified in the step's `action`. The loopback client routes through the main gateway's admin API handler, preserving RBAC, validation, and audit. The runbook caller's identity is propagated as the actor for all internal calls.
3. Extracts outputs from the response body via JSONPath
4. Evaluates the decision tree conditions (simple equality/comparison operators)
5. Advances to the next step or pauses for confirmation

**Execution modes:**
- `auto` — executes the full diagnosis→decision→remediation→verification loop. Steps with `confirmation: required` still pause and return `202 Accepted` with `status: awaiting_confirmation`.
- `step` — pauses after every step. The caller approves each step via `POST /v1/admin/runbook-executions/{id}/confirm`.
- `dry-run` — runs diagnosis and decision only. Returns the selected remediation procedure without executing it.

**Escalation.** When the decision tree reaches an `escalation` node, the execution pauses with `state: "escalated"`. The event stream (Section 25.4) emits a `runbook_escalated` event. A human or higher-level agent handles the escalation.

**Concurrency guard.** Only one execution per (runbook_name, parameters) combination can be in a non-terminal state at a time. Enforced via a partial unique index on `runbook_executions`.

### Degradation

If Postgres is down: execution cannot start or resume (state is in Postgres). The runbook registry falls back to the Helm-loaded in-memory cache for read-only operations (`ListRunbooks`, `GetRunbook`).

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_runbook_execution_total` | Counter | `runbook`, `mode`, `outcome` | Execution count |
| `lenny_runbook_execution_duration_seconds` | Histogram | `runbook` | Total execution duration |
| `lenny_runbook_step_duration_seconds` | Histogram | `runbook`, `step` | Per-step duration |
| `lenny_runbook_escalation_total` | Counter | `runbook` | Escalation count |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `RunbookExecutionStuck` | Execution in `awaiting_confirmation` for > 30m | Warning |
| `RunbookExecutionFailed` | `lenny_runbook_execution_total{outcome="failed"}` rate > 0 for > 5m | Warning |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `RUNBOOK_NOT_FOUND` | `PERMANENT` | 404 | Runbook name not registered |
| `RUNBOOK_EXECUTION_NOT_FOUND` | `PERMANENT` | 404 | Execution ID not found |
| `RUNBOOK_ALREADY_RUNNING` | `POLICY` | 409 | A running execution exists for this runbook+parameters |
| `RUNBOOK_CONFIRMATION_REQUIRED` | `PERMANENT` | 202 | Step requires confirmation |
| `RUNBOOK_STEP_FAILED` | `TRANSIENT` | 502 | A step's internal API call failed |
| `RUNBOOK_HELM_SOURCE_IMMUTABLE` | `PERMANENT` | 409 | Cannot delete/modify a Helm-sourced runbook via API |

### Audit Events

`runbook.registered`, `runbook.updated`, `runbook.deleted`, `runbook.execution_started`, `runbook.step_executed` (includes step ID, action, result), `runbook.step_confirmed`, `runbook.escalated`, `runbook.execution_completed`, `runbook.execution_failed`.

---

## 25.7 Platform Self-Management API

APIs for managing Lenny's own lifecycle — version introspection, upgrade orchestration, and configuration management. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/platform/version` | Current version of all components |
| `GET` | `/v1/admin/platform/upgrade-check` | Check for available upgrades |
| `POST` | `/v1/admin/platform/upgrade/preflight` | Validate upgrade safety |
| `POST` | `/v1/admin/platform/upgrade/start` | Begin platform upgrade |
| `POST` | `/v1/admin/platform/upgrade/proceed` | Advance to next phase |
| `POST` | `/v1/admin/platform/upgrade/pause` | Pause upgrade |
| `POST` | `/v1/admin/platform/upgrade/rollback` | Rollback upgrade |
| `GET` | `/v1/admin/platform/upgrade/status` | Current upgrade state |
| `POST` | `/v1/admin/platform/upgrade/verify` | Post-upgrade health verification |
| `GET` | `/v1/admin/platform/config` | Effective running configuration (secrets redacted) |
| `GET` | `/v1/admin/platform/config/diff` | Compare running config vs. supplied desired state |
| `PUT` | `/v1/admin/platform/config` | Apply a runtime config change (subset of restart-free settings) |

### Version Introspection Sources

| Field | Source |
|-------|--------|
| `gateway.version`, `gitCommit`, `buildDate`, `goVersion` | Compiled-in via `ldflags` |
| `controllers.warmPoolController.version` | K8s API: controller Deployment labels |
| `controllers.poolScalingController.version` | K8s API: controller Deployment labels |
| `crds.*.installed` | K8s API: CRD `.spec.versions` |
| `crds.*.required` | Compiled-in constant |
| `helmChart.version` | Helm release metadata via K8s API (`helm.sh/release.v1` Secret) |
| `schema.postgres.current` | `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1` |
| `schema.postgres.required` | Compiled-in constant |

When any component's `current` version does not match `required`, the response includes a top-level `"versionDrift": true` and each drifted component includes `"drift": true` and `"requiredAction"`.

### Upgrade Check

`GET /v1/admin/platform/upgrade-check` queries a configurable release channel endpoint (`platform.upgradeChannel` Helm value, default: `https://releases.lenny.dev/v1/latest`). Deployers can point this at an internal mirror or disable it (`platform.upgradeChannel: ""` disables). The check runs periodically (every 6 hours) and the result is cached in Postgres. A `platform_upgrade_available` operational event is emitted when a new version is detected.

### Upgrade State Machine

Mirrors the pool upgrade pattern (Section 10.5):

```
Preflight → CRDUpdate → SchemaMigration → GatewayRoll → ControllerRoll → Verification → Complete
                                                                                          ↗
Any non-terminal state → Paused ──────────────────────────────────────────────────────────→ (resume)
Any pre-migration state → RolledBack
```

The state machine pauses between phases and requires `proceed` to advance. This allows the agent to verify health at each step before continuing.

**Automatic pre-migration backup.** Before entering `SchemaMigration`, the state machine automatically triggers a Postgres backup (Section 25.11) and records the backup ID in `platform_upgrade_state.pre_upgrade_backup_id`. The upgrade blocks until the backup completes successfully.

**Rollback constraints.** Rollback is available before `SchemaMigration` completes. After schema migration, the database schema may be incompatible with the old binary — rollback requires a database restore from the pre-upgrade backup. The rollback endpoint communicates this constraint: when called after schema migration, it returns `409 UPGRADE_ROLLBACK_UNAVAILABLE` with `details.requiresRestore: true` and `details.backupId` pointing to the pre-migration backup.

### Postgres Schema

```sql
CREATE TABLE platform_upgrade_state (
    id                    TEXT PRIMARY KEY DEFAULT 'singleton',
    target_version        TEXT NOT NULL,
    current_phase         TEXT NOT NULL,       -- Preflight, CRDUpdate, SchemaMigration,
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

### Implementation

The upgrade API orchestrates Helm operations and schema migrations internally:
- **CRDUpdate:** The gateway applies CRD manifests via the K8s API using server-side apply.
- **SchemaMigration:** Runs the same migration framework as `lenny-preflight` (Section 17.6).
- **GatewayRoll:** Patches the gateway Deployment's image tag via the K8s API, then waits for rollout completion.
- **ControllerRoll:** Patches controller Deployments similarly.
- **Verification:** Runs `GET /v1/admin/health` and `GET /v1/admin/diagnostics/connectivity`; passes if both return `healthy`.

The gateway's service account requires RBAC to update its own Deployment, the controller Deployments, and CRDs. These permissions are included in the Helm chart's RBAC manifests.

### Configuration Management

`GET /v1/admin/platform/config` returns the effective running configuration (Helm values merged with admin API overrides). Secret values (Postgres DSN, Redis password, OIDC client secret, etc.) are redacted to `"***"`.

`GET /v1/admin/platform/config/diff` accepts a `{"desired": {...}}` body and returns a structured diff. Used for GitOps reconciliation — an agent compares the repo's values file against the running state.

`PUT /v1/admin/platform/config` applies runtime changes for a subset of settings that do not require restart: pool sizes, quotas, rate limits, circuit breaker state, recommendation rule enablement. Returns `422 CONFIG_RESTART_REQUIRED` (with `details.settings` listing the offending keys) for settings that require a gateway restart.

### Degradation

If the release channel is unreachable: `upgrade-check` returns cached data with `"cached": true, "cacheAge": "..."`. If Postgres is down: upgrade state machine operations fail; version introspection returns partial data (binary metadata always available; schema version unavailable). If K8s API is down: controller versions and CRD versions are unavailable.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_platform_upgrade_phase` | Gauge | `target_version` | Current phase (encoded as integer) |
| `lenny_platform_upgrade_duration_seconds` | Gauge | `target_version` | Time since upgrade started |
| `lenny_platform_version_drift` | Gauge | | 1 if any component version drift, 0 otherwise |

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
| `UPGRADE_PREFLIGHT_FAILED` | `PERMANENT` | 422 | Preflight checks failed. `details.failures` lists each. |
| `UPGRADE_ROLLBACK_UNAVAILABLE` | `PERMANENT` | 409 | Schema migration completed; rollback requires DB restore |
| `UPGRADE_NOT_IN_PROGRESS` | `PERMANENT` | 409 | No upgrade to proceed/pause/rollback |
| `UPGRADE_CHANNEL_UNREACHABLE` | `TRANSIENT` | 503 | Release channel unreachable |
| `CONFIG_RESTART_REQUIRED` | `PERMANENT` | 422 | Setting change requires gateway restart |

### Audit Events

`platform.version_checked`, `platform.upgrade_started`, `platform.upgrade_phase_advanced`, `platform.upgrade_paused`, `platform.upgrade_rolled_back`, `platform.upgrade_completed`, `platform.upgrade_verified`, `platform.config_changed`.

---

## 25.8 Audit Log Query API

Structured query access to the audit trail (Section 11.7). Enables agents to investigate incidents without direct database access. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/audit-events` | Paginated query. Params: `?since=`, `?until=`, `?eventType=`, `?actorId=`, `?resourceType=`, `?resourceId=`, `?tenantId=`, `?severity=`, `?limit=` (default 100, max 1000), `?cursor=` |
| `GET` | `/v1/admin/audit-events/{id}` | Single event with full payload |
| `GET` | `/v1/admin/audit-events/summary` | Aggregate counts by type/actor/resource over a time window. Params: `?since=`, `?until=`, `?groupBy=eventType|actorId|resourceType` |

### Implementation

Reads from the existing `audit_log` table (Section 11.7) via `StoreRouter.AuditShard()`. The following indexes support the query API's filter combinations:

```sql
-- Required indexes (should exist from Phase 13; verified by preflight)
CREATE INDEX IF NOT EXISTS idx_audit_log_tenant_time ON audit_log (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_event_type ON audit_log (event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor ON audit_log (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_resource ON audit_log (resource_type, resource_id, created_at DESC);
```

### Hash Chain Verification

Each event in the response includes a `chainIntegrity` field (`verified`, `broken`, `unchecked`). The query handler reads each event and its predecessor (`prev_hash`) and verifies:

```
expected = SHA-256(predecessor.id || predecessor.prev_hash || predecessor.tenant_id
                   || predecessor.event_type || predecessor.payload || predecessor.created_at)
chainIntegrity = (event.prev_hash == expected) ? "verified" : "broken"
```

To avoid N+1 queries, the query fetches `limit + 1` rows and verifies the chain across the returned window. Events at the page boundary where the predecessor is not in the current page are marked `"unchecked"`.

### Response Schema

```json
{
  "events": [
    {
      "id": "evt-123",
      "timestamp": "2026-04-08T14:22:00Z",
      "eventType": "circuit_breaker.state_changed",
      "severity": "warning",
      "actor": { "id": "agent-ops@svc", "type": "agent", "tenantId": "t1" },
      "resource": { "type": "circuit_breaker", "id": "runtime-x-degraded" },
      "payload": { "old_state": "closed", "new_state": "open", "reason": "Elevated error rate" },
      "chainIntegrity": "verified"
    }
  ],
  "nextCursor": "c-abc",
  "totalEstimate": 4200
}
```

### Degradation

If Postgres is down: all audit query endpoints return `503`. No cache — audit data must come from the authoritative store.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_audit_query_duration_seconds` | Histogram | `endpoint` | Audit query latency |
| `lenny_audit_chain_verification_broken_total` | Counter | | Broken chain segments detected |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `AuditChainIntegrityBroken` | `lenny_audit_chain_verification_broken_total` rate > 0 | Critical |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `AUDIT_EVENT_NOT_FOUND` | `PERMANENT` | 404 | Audit event ID not found |
| `AUDIT_QUERY_TOO_BROAD` | `POLICY` | 400 | Query would scan too many rows; narrow time range or add filters |

### Audit Events

`audit.query_executed` (T2 classification, eligible for batching — includes query parameters and result count).

---

## 25.9 Configuration Drift Detection

Detects discrepancies between the desired platform state and the actual running state. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/drift` | Drift report |
| `POST` | `/v1/admin/drift/reconcile` | Reconcile drifted resources. Body: `{"scope": "all"}` or `{"scope": "resources", "resources": [...]}`. Supports `"mode": "dry-run"`. |

### Postgres Schema

```sql
CREATE TABLE bootstrap_seed_snapshot (
    id              SERIAL PRIMARY KEY,
    resource_type   TEXT NOT NULL,          -- "runtime", "pool", "tenant", "connector",
                                            -- "delegation_policy", "environment", "credential_pool"
    resource_name   TEXT NOT NULL,
    desired_state   JSONB NOT NULL,         -- last-applied state
    source          TEXT NOT NULL,          -- "bootstrap" or "admin-api"
    applied_by      TEXT NOT NULL,
    applied_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (resource_type, resource_name)
);

CREATE INDEX idx_bootstrap_seed_type ON bootstrap_seed_snapshot (resource_type);
```

### Snapshot Population

The `bootstrap_seed_snapshot` table is populated automatically by:
1. **`POST /v1/admin/bootstrap`** — each resource in the seed writes its desired state.
2. **Admin API mutations** — each `POST` (create) and `PUT` (update) on an admin resource writes the new state as the desired snapshot. This ensures the snapshot always reflects the most recent intentional configuration, regardless of whether it came from a bootstrap seed or an admin API call.

### Drift Detection Logic

`GET /v1/admin/drift` compares:
1. **Running state** — read via internal loopback calls to `GET /v1/admin/runtimes`, `GET /v1/admin/pools`, etc.
2. **Desired state** — read from `bootstrap_seed_snapshot`. Alternatively, the caller can supply a `{"desired": {...}}` body for ad-hoc comparison.

A field-by-field JSON diff is computed for each resource. Differences are classified by severity:
- `high` — image changes, isolation profile changes, security settings
- `medium` — scaling parameters (minWarm, maxWarm), quota values
- `low` — labels, descriptions, metadata

CRD drift is detected via `GET /v1/admin/pools/{name}/sync-status` (existing endpoint). Schema drift is detected by comparing `schema_migrations` against the compiled-in required version.

Each drift entry includes a `source` field: `bootstrap-seed`, `admin-api-override`, or `external` (change made outside the admin API, e.g., direct CRD edit via kubectl).

### Reconciliation

`POST /v1/admin/drift/reconcile` calls admin API `PUT` endpoints via internal loopback to apply the desired state. Each call goes through full RBAC, validation, and audit. `mode: "dry-run"` previews changes without applying.

### Degradation

If Postgres is down: drift detection fails (cannot read desired state or running state). If individual admin API internal calls fail: drift report includes failed resources in an `errors` array, and returns `207 DRIFT_RECONCILE_PARTIAL`.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_drift_detected_total` | Counter | `resource_type`, `severity` | Drift detections |
| `lenny_drift_reconciled_total` | Counter | `resource_type`, `outcome` | Reconciliation outcomes |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `ConfigDriftDetected` | `lenny_drift_detected_total{severity="high"}` rate > 0 for > 10m | Warning |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `DRIFT_RECONCILE_PARTIAL` | `TRANSIENT` | 207 | Some resources could not be reconciled |
| `DRIFT_DESIRED_STATE_MISSING` | `PERMANENT` | 404 | No snapshot exists for the requested resource |

### Audit Events

`drift.report_generated`, `drift.reconciliation_started`, `drift.resource_reconciled`, `drift.reconciliation_completed`.

---

## 25.10 Capacity Recommendations

A rules engine that synthesizes current metrics and usage patterns into actionable capacity recommendations. Served on the management listener.

### Endpoint

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/admin/recommendations` | Prioritized recommendations. Optional `?category=` filter. |

### Go Interface

```go
// pkg/gateway/management/capacity.go

type CapacityService interface {
    GetRecommendations(ctx context.Context, category *string) (*RecommendationsResponse, error)
}

type RecommendationRule struct {
    ID        string
    Category  string                                 // warm_pool_sizing, credential_pool_sizing, etc.
    Condition func(metrics MetricReader) bool
    Generate  func(metrics MetricReader) *Recommendation
    Enabled   bool
}

type MetricReader interface {
    GaugeValue(name string, labels map[string]string) (float64, bool)
    CounterValue(name string, labels map[string]string) (float64, bool)
    HistogramQuantile(name string, labels map[string]string, quantile float64) (float64, bool)
    WindowedRate(name string, labels map[string]string, window time.Duration) (float64, bool)
}
```

### Rules Engine

Rules are deterministic heuristics, not AI. Each rule reads metric values from the in-process registry, evaluates a condition, and generates a recommendation with a formula-derived value and confidence score.

**Recommendation categories:**

| Category | Condition (example) | Recommendation logic |
|---|---|---|
| `warm_pool_sizing` | Pool exhausted 3+ times in 24h | `minWarm = ceil(peak_claim_rate * (startup_p99 + failover_seconds) * 1.3)` |
| `credential_pool_sizing` | Utilization > 70% over 7d with rate-limit events | "Add N credentials to bring utilization below 60%" |
| `gateway_scaling` | CPU > 70% or queue depth > HPA target for > 15m | "Increase HPA max replicas" |
| `resource_limits` | OOM events > 0 in 24h | "Increase memory limit for pool X" |
| `retention_tuning` | Storage utilization > 80% | "Reduce artifact retention TTL" |
| `quota_adjustment` | Quota rejection rate > 5% over 24h | "Increase tenant session quota" |

**Sliding window aggregation.** The rules engine maintains in-memory ring buffers per metric (no Postgres, no Redis). Window sizes are configurable per rule (default: 24h for pool sizing, 7d for credential sizing). After a gateway restart, windows are empty and recommendations include `"confidence": 0.0` and `"dataAvailable": false`.

Deployers disable specific rules via `platform.recommendations.disabledRules` Helm value (array of rule IDs).

### Response Schema

```json
{
  "generatedAt": "2026-04-08T14:30:00Z",
  "recommendations": [
    {
      "id": "rec-001",
      "priority": "high",
      "category": "warm_pool_sizing",
      "resource": "pool/default-gvisor",
      "title": "Increase warm pool minWarm for default-gvisor",
      "reasoning": "Pool exhausted 3 times in 24h. Peak claim rate 4.2/min. Current minWarm (5) insufficient.",
      "currentValue": { "minWarm": 5 },
      "recommendedValue": { "minWarm": 15 },
      "action": {
        "endpoint": "PUT /v1/admin/pools/default-gvisor/warm-count",
        "body": { "minWarm": 15 }
      },
      "confidence": 0.85,
      "basedOn": { "metric": "lenny_warmpool_idle_pods", "window": "24h", "exhaustionCount": 3 }
    }
  ]
}
```

### Storage

None. Recommendations are computed on-demand from in-memory metric state.

### Degradation

If metrics are stale (gateway recently restarted): recommendations include `"confidence": 0.0`. No recommendations are generated for categories with insufficient data.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_recommendations_generated_total` | Counter | `category`, `priority` | Recommendations generated |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `UNKNOWN_RECOMMENDATION_CATEGORY` | `PERMANENT` | 400 | Unrecognized category filter |

### Audit Events

`recommendations.generated` (includes recommendation count).

---

## 25.11 Backup and Restore API

APIs for managing platform backups, extending the disaster recovery procedures in Section 17.3. Served on the management listener.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/admin/backups` | Trigger on-demand backup. Body: `{"components": ["postgres","objectStore"]|"all", "label": "..."}` |
| `GET` | `/v1/admin/backups` | List backups. Params: `?component=`, `?since=`, `?until=`, `?label=`, `?limit=` |
| `GET` | `/v1/admin/backups/{id}` | Backup details: status, size, duration, checksums |
| `POST` | `/v1/admin/backups/{id}/verify` | Verify a backup is restorable (async job) |
| `GET` | `/v1/admin/backup-jobs/{id}` | Get backup/verify job status |
| `GET` | `/v1/admin/backups/schedule` | Current backup schedule |
| `PUT` | `/v1/admin/backups/schedule` | Update backup schedule |
| `GET` | `/v1/admin/backups/policy` | Backup retention policy |
| `PUT` | `/v1/admin/backups/policy` | Update retention policy |
| `POST` | `/v1/admin/restore/preview` | Analyze restore impact without executing |
| `POST` | `/v1/admin/restore/execute` | Execute restore. Requires `"confirm": true`. |

### Postgres Schema

```sql
CREATE TABLE backup_jobs (
    id              TEXT PRIMARY KEY,       -- "bak-" + UUIDv4
    type            TEXT NOT NULL,          -- "backup", "verify", "restore_preview"
    components      TEXT[] NOT NULL,
    label           TEXT,
    status          TEXT NOT NULL,          -- "pending", "running", "completed", "failed"
    started_by      TEXT NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    size_bytes      BIGINT,
    duration_ms     BIGINT,
    location        TEXT,                   -- MinIO path
    checksums       JSONB,                  -- per-component checksums
    error           TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_backup_jobs_status ON backup_jobs (status, started_at DESC);
CREATE INDEX idx_backup_jobs_label ON backup_jobs (label) WHERE label IS NOT NULL;

CREATE TABLE backup_schedule (
    id              TEXT PRIMARY KEY DEFAULT 'singleton',
    cron_expression TEXT NOT NULL DEFAULT '0 2 * * *',  -- daily at 2 AM
    components      TEXT[] NOT NULL DEFAULT '{"postgres","objectStore"}',
    retention_days  INT NOT NULL DEFAULT 30,
    updated_by      TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Job Orchestration

The gateway creates a Kubernetes Job for each backup/verify operation. The Job runs a `lenny-backup` container image (built from the same repo) that:
1. **Postgres backup:** `pg_dump --format=custom`, uploads to MinIO at `backups/postgres/{job_id}/`
2. **Object store backup:** `mc mirror` to a backup bucket/prefix
3. Updates the `backup_jobs` row with status, size, duration, checksums

The gateway polls the K8s Job status and updates the `backup_jobs` row. Verification uses the same `lenny-restore-test` pattern from Section 17.3: creates a temporary Postgres instance, restores, verifies schema integrity and row counts, tears down.

### Restore

Restore is intentionally two-step:
1. `POST /v1/admin/restore/preview` — returns current data age vs. backup age, estimated data loss, affected session count.
2. `POST /v1/admin/restore/execute` — requires `"confirm": true`. Without it, returns the same preview. The restore Job transitions the gateway to maintenance mode (refuses new sessions via a circuit breaker), restores Postgres, verifies schema, and exits maintenance mode.

### Degradation

If K8s API is unreachable: cannot create backup Jobs; returns `503`. If MinIO is unreachable: Postgres-only backups still work; MinIO backup component fails. If Postgres is down: cannot record backup jobs; the Job runs independently but status tracking is unavailable until Postgres recovers.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_backup_duration_seconds` | Histogram | `type`, `component` | Backup job duration |
| `lenny_backup_size_bytes` | Gauge | `component` | Size of latest backup |
| `lenny_backup_last_success_timestamp` | Gauge | `component` | Unix timestamp of last success |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `BackupOverdue` | `time() - lenny_backup_last_success_timestamp > 2 * schedule_interval` | Critical |
| `BackupFailed` | Backup job status is `failed` | Warning |

### Error Codes

| Code | Category | HTTP | Description |
|------|----------|------|-------------|
| `BACKUP_JOB_NOT_FOUND` | `PERMANENT` | 404 | Job ID not found |
| `BACKUP_IN_PROGRESS` | `POLICY` | 409 | A backup is already running |
| `RESTORE_CONFIRMATION_REQUIRED` | `PERMANENT` | 400 | `confirm: true` missing |
| `RESTORE_BACKUP_NOT_FOUND` | `PERMANENT` | 404 | Backup ID not found or expired |
| `RESTORE_BACKUP_INVALID` | `PERMANENT` | 422 | Backup verification failed |

### Audit Events

`backup.triggered`, `backup.completed`, `backup.failed`, `backup.verification_started`, `backup.verification_completed`, `restore.preview_generated`, `restore.started`, `restore.completed`, `restore.failed`, `backup.schedule_updated`, `backup.policy_updated`.

---

## 25.12 MCP Management Server

Lenny exposes its operational surface as an MCP tool server, enabling any MCP-capable agent to manage Lenny natively. Served on the management listener.

### Architecture

A new `ExternalProtocolAdapter` implementation: `ManagementMCPAdapter`, registered at `/mcp/management` on the management listener (not the main gateway port).

```go
// pkg/gateway/management/mcp_adapter.go

type ManagementMCPAdapter struct {
    BaseAdapter                          // no-op lifecycle hooks
    toolRegistry   *ManagementToolRegistry
    healthSvc      HealthService
    diagSvc        DiagnosticService
    runbookSvc     RunbookService
    auditQuerySvc  AuditQueryService
    driftSvc       DriftService
    capacitySvc    CapacityService
    backupSvc      BackupService
    platformSvc    PlatformService
}

func (a *ManagementMCPAdapter) Capabilities() AdapterCapabilities {
    return AdapterCapabilities{
        PathPrefix:                "/mcp/management",
        Protocol:                  "mcp-management",
        SupportsSessionContinuity: false,
        SupportsDelegation:        false,
        SupportsElicitation:       false,
        SupportsInterrupt:         false,
    }
}
```

### Tool Schema Generation

Tool schemas are auto-generated from the OpenAPI spec at build time. A code generator reads `openapi.yaml` (Section 15.1) and produces a `ManagementToolRegistry`:

- Tool name: `lenny/{operation_id}` from the OpenAPI spec
- Input schema: request body JSON Schema (POST/PUT) or query parameter schema (GET)
- `readOnlyHint`: `true` for GET endpoints
- `destructiveHint`: `true` for DELETE endpoints and those annotated `x-lenny-destructive: true`

Tool invocations translate MCP `tools/call` messages into internal loopback HTTP calls to the corresponding admin API handler. The caller's MCP auth token is propagated for RBAC and audit.

### Representative Tool Inventory

| Tool name | Maps to | Hints |
|---|---|---|
| `lenny/platform_health` | `GET /v1/admin/health` | readOnly |
| `lenny/diagnose_session` | `GET /v1/admin/diagnostics/sessions/{id}` | readOnly |
| `lenny/diagnose_pool` | `GET /v1/admin/diagnostics/pools/{name}` | readOnly |
| `lenny/connectivity_check` | `GET /v1/admin/diagnostics/connectivity` | readOnly |
| `lenny/execute_runbook` | `POST /v1/admin/runbooks/{name}/execute` | destructive |
| `lenny/scale_pool` | `PUT /v1/admin/pools/{name}/warm-count` | |
| `lenny/deploy_runtime` | `POST /v1/admin/runtimes` | |
| `lenny/platform_upgrade_check` | `GET /v1/admin/platform/upgrade-check` | readOnly |
| `lenny/start_platform_upgrade` | `POST /v1/admin/platform/upgrade/start` | destructive |
| `lenny/query_audit_events` | `GET /v1/admin/audit-events` | readOnly |
| `lenny/get_recommendations` | `GET /v1/admin/recommendations` | readOnly |
| `lenny/trigger_backup` | `POST /v1/admin/backups` | |
| `lenny/drift_report` | `GET /v1/admin/drift` | readOnly |
| `lenny/manage_circuit_breaker` | `POST /v1/admin/circuit-breakers/{name}/open` | |

The full tool inventory is the complete admin API surface — every admin endpoint maps to an MCP tool.

### Why a Dedicated MCP Endpoint

`/mcp/runtimes/{name}` (Section 15) proxies MCP tool calls to agent pods. `/mcp/management` serves Lenny's own operational tools directly from the gateway. They are separate MCP servers with separate capability negotiation and authentication scopes. A DevOps agent connects to `/mcp/management` to operate Lenny; a client agent connects to `/mcp/runtimes/{name}` to use Lenny-hosted tools.

### Authentication

Same OIDC tokens as the admin REST API. The MCP `initialize` handshake validates the auth token before advertising tools. Requires `platform-admin` or `tenant-admin` role.

### Degradation

If individual backend services are down, the corresponding MCP tools return structured errors with the appropriate error codes. The MCP server itself remains available as long as the management listener is running.

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_mcp_management_tool_call_total` | Counter | `tool`, `status` | Tool invocations |
| `lenny_mcp_management_tool_call_duration_seconds` | Histogram | `tool` | Tool latency |
| `lenny_mcp_management_active_connections` | Gauge | | Active MCP management connections |

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| `MCPManagementToolFailureRate` | Error rate > 10% for any tool over 5m | Warning |

### Audit Events

All MCP tool invocations produce the same audit events as the underlying REST API calls. Additionally: `mcp_management.session_established`, `mcp_management.session_closed`.

---

## 25.13 `lenny-ctl` Extensions

The following command groups wrap the operability APIs. Same conventions as Section 24: `--output json`, `--quiet`, global flags.

### Health

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl health` | `GET /v1/admin/health` | `platform-admin` |
| `lenny-ctl health <component>` | `GET /v1/admin/health/{component}` | `platform-admin` |
| `lenny-ctl health summary` | `GET /v1/admin/health/summary` | `platform-admin` |

### Events

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl events stream [--types <t1,t2>] [--severity <s>]` | `GET /v1/admin/events/stream` | `platform-admin` |
| `lenny-ctl events list [--since <t>] [--types <t1,t2>]` | `GET /v1/admin/events` | `platform-admin` |
| `lenny-ctl events subscribe --url <callback> --types <t1,t2>` | `POST /v1/admin/event-subscriptions` | `platform-admin` |
| `lenny-ctl events subscriptions list` | `GET /v1/admin/event-subscriptions` | `platform-admin` |
| `lenny-ctl events subscriptions delete <id>` | `DELETE /v1/admin/event-subscriptions/{id}` | `platform-admin` |

### Diagnostics

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl diagnose session <id>` | `GET /v1/admin/diagnostics/sessions/{id}` | `platform-admin` |
| `lenny-ctl diagnose pool <name>` | `GET /v1/admin/diagnostics/pools/{name}` | `platform-admin` |
| `lenny-ctl diagnose credential-pool <name>` | `GET /v1/admin/diagnostics/credential-pools/{name}` | `platform-admin` |
| `lenny-ctl diagnose connectivity` | `GET /v1/admin/diagnostics/connectivity` | `platform-admin` |

### Runbooks

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl runbook list` | `GET /v1/admin/runbooks` | `platform-admin` |
| `lenny-ctl runbook show <name>` | `GET /v1/admin/runbooks/{name}` | `platform-admin` |
| `lenny-ctl runbook execute <name> [--param key=val] [--mode auto\|step\|dry-run]` | `POST /v1/admin/runbooks/{name}/execute` | `platform-admin` |
| `lenny-ctl runbook executions <name>` | `GET /v1/admin/runbooks/{name}/executions` | `platform-admin` |
| `lenny-ctl runbook status <execution-id>` | `GET /v1/admin/runbook-executions/{id}` | `platform-admin` |
| `lenny-ctl runbook confirm <execution-id>` | `POST /v1/admin/runbook-executions/{id}/confirm` | `platform-admin` |

### Platform Lifecycle

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl platform version` | `GET /v1/admin/platform/version` | `platform-admin` |
| `lenny-ctl platform upgrade-check` | `GET /v1/admin/platform/upgrade-check` | `platform-admin` |
| `lenny-ctl platform upgrade preflight --version <v>` | `POST /v1/admin/platform/upgrade/preflight` | `platform-admin` |
| `lenny-ctl platform upgrade start --version <v>` | `POST /v1/admin/platform/upgrade/start` | `platform-admin` |
| `lenny-ctl platform upgrade proceed` | `POST /v1/admin/platform/upgrade/proceed` | `platform-admin` |
| `lenny-ctl platform upgrade pause` | `POST /v1/admin/platform/upgrade/pause` | `platform-admin` |
| `lenny-ctl platform upgrade rollback` | `POST /v1/admin/platform/upgrade/rollback` | `platform-admin` |
| `lenny-ctl platform upgrade status` | `GET /v1/admin/platform/upgrade/status` | `platform-admin` |
| `lenny-ctl platform upgrade verify` | `POST /v1/admin/platform/upgrade/verify` | `platform-admin` |
| `lenny-ctl platform config` | `GET /v1/admin/platform/config` | `platform-admin` |
| `lenny-ctl platform config diff --desired <file>` | `GET /v1/admin/platform/config/diff` | `platform-admin` |

### Backup and Restore

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl backup create [--components postgres,objectStore] [--label <label>]` | `POST /v1/admin/backups` | `platform-admin` |
| `lenny-ctl backup list` | `GET /v1/admin/backups` | `platform-admin` |
| `lenny-ctl backup show <id>` | `GET /v1/admin/backups/{id}` | `platform-admin` |
| `lenny-ctl backup verify <id>` | `POST /v1/admin/backups/{id}/verify` | `platform-admin` |
| `lenny-ctl backup schedule` | `GET /v1/admin/backups/schedule` | `platform-admin` |
| `lenny-ctl restore preview --backup <id>` | `POST /v1/admin/restore/preview` | `platform-admin` |
| `lenny-ctl restore execute --backup <id> --confirm` | `POST /v1/admin/restore/execute` | `platform-admin` |

### Drift and Recommendations

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl drift report` | `GET /v1/admin/drift` | `platform-admin` |
| `lenny-ctl drift reconcile [--scope all\|<type>/<name>] [--dry-run]` | `POST /v1/admin/drift/reconcile` | `platform-admin` |
| `lenny-ctl recommendations [--category <cat>]` | `GET /v1/admin/recommendations` | `platform-admin` |

### Audit

| Command | API Mapping | Min Role |
|---------|-------------|----------|
| `lenny-ctl audit query [--since <t>] [--until <t>] [--event-type <type>] [--actor <id>]` | `GET /v1/admin/audit-events` | `platform-admin` |
| `lenny-ctl audit summary [--since <t>] [--group-by eventType\|actorId]` | `GET /v1/admin/audit-events/summary` | `platform-admin` |

---

## 25.14 Build Phase and Migration

### Phase Assignment

All operability features target **Phase 13.1**, inserted between Phase 13 (full observability stack, durable audit trail) and Phase 13.5 (pre-hardening load baseline). Dependencies:
- **Phase 2.5** provides the metrics registry and structured logging
- **Phase 4.5** provides the admin API foundation and OIDC authentication
- **Phase 13** provides the audit tables, alerting rules, and SLO definitions

The Phase 13.5 load baseline should include the management listener overhead to ensure it does not impact client-traffic performance.

### Implementation Order within Phase 13.1

| Order | Feature | Rationale |
|-------|---------|-----------|
| 1 | Management Listener (25.2) | Foundation — all other features are served on it |
| 2 | Platform Health API (25.3) | No schema changes; pure computation from existing metrics |
| 3 | Operational Event Stream (25.4) | Redis stream + subscription tables; enables event-driven patterns for subsequent features |
| 4 | Diagnostic Endpoints (25.5) | Reads from existing tables only |
| 5 | Audit Log Query API (25.8) | Reads from existing audit tables; adds indexes |
| 6 | Configuration Drift (25.9) | Adds `bootstrap_seed_snapshot` table |
| 7 | Capacity Recommendations (25.10) | Pure in-memory computation |
| 8 | Machine-Executable Runbooks (25.6) | Adds `runbook_definitions` and `runbook_executions` tables |
| 9 | Platform Self-Management (25.7) | Adds `platform_upgrade_state` table; requires K8s RBAC for self-upgrade |
| 10 | Backup and Restore (25.11) | Adds `backup_jobs` table; K8s Job orchestration |
| 11 | MCP Management Server (25.12) | Depends on all other features being available as tools |

### Migration File

All new tables are added in a single migration: `migrations/XXXX_add_operability_tables.sql`:

```sql
-- ops_event_subscriptions, ops_event_deliveries (Section 25.4)
-- runbook_definitions, runbook_executions (Section 25.6)
-- platform_upgrade_state, platform_upgrade_check_cache (Section 25.7)
-- bootstrap_seed_snapshot (Section 25.9)
-- backup_jobs, backup_schedule (Section 25.11)
```

All tables are platform-scoped (no `tenant_id`, no RLS). They are accessed only by `platform-admin` callers through the management listener. The `lenny_app` database role has full CRUD on these tables (they are operational state, not audit data — audit tables remain append-only per Section 11.7).

---

## 25.15 Cross-References to SPEC.md

When this section is integrated into `SPEC.md`, the following cross-references should be added:

1. **Section 2 (Goals)** — add: "Enable autonomous operation by AI DevOps agents through a complete operational API surface"
2. **Section 4.1 (Gateway)** — reference the management listener as a separate HTTP server sharing the gateway process but isolated from client-traffic subsystems
3. **Section 15.1 (REST API)** — add the new admin API endpoints from Sections 25.3–25.11 to the admin API table
4. **Section 16.5 (Alerting)** — reference the operational event stream (25.4) as the programmatic alert delivery mechanism
5. **Section 17.7 (Runbooks)** — reference the machine-executable format (25.6) and note that each `.md` runbook has a corresponding `.runbook.yaml`
6. **Section 18 (Build Sequence)** — add Phase 13.1 with the implementation order from Section 25.14
7. **Section 23.1 (Why Lenny?)** — add differentiator: "First agent platform natively operable by AI agents via MCP"
8. **Section 24 (lenny-ctl)** — reference Section 25.13 for the additional command groups
