# Agent Operability — Technical Design Addendum

> **Status:** Draft
> **Applies to:** `technical-design.md` — proposed Section 25
> **Author:** Claude (with Joan)
> **Date:** 2026-04-08

Lenny is designed to be natively operable by AI agents. External DevOps agents — whether running inside Lenny itself or in separate orchestration platforms — can deploy, configure, monitor, troubleshoot, upgrade, and maintain a Lenny installation entirely through APIs, without direct Kubernetes, database, or storage access. This document specifies the APIs, protocols, and design principles that make this possible.

---

## 25.1 Design Philosophy

**Principle: The admin API is the complete operational surface — for agents as well as humans.**

The existing admin API (Section 15.1) provides CRUD operations for all platform resources. However, CRUD is necessary but not sufficient for autonomous operations. A DevOps agent executing an operational loop requires:

1. **Observe** — structured platform health, not raw metric scraping
2. **Detect** — real-time notification of operational events, not polling
3. **Diagnose** — structured causality chains, not log parsing
4. **Decide** — actionable recommendations, not threshold interpretation
5. **Act** — API-encapsulated remediation, not kubectl/psql/redis-cli
6. **Verify** — confirmation that the action had the intended effect

Each subsection below fills a gap in this loop. Together, they ensure that every step an operator takes in every runbook (Section 17.7) can be performed by an API call, and every signal an operator reads from a dashboard is available as structured data.

**Design constraints:**

- **No Kubernetes access required.** Every runbook diagnostic and remediation step that currently uses `kubectl`, `psql`, `redis-cli`, or `mc` must have an admin API equivalent. Direct cluster access is available for escape-hatch scenarios but is never required for standard operations.
- **Structured over textual.** All operational responses use typed JSON schemas, not human-readable prose or log lines. Error codes, severity levels, and suggested actions are machine-parseable.
- **Idempotent and safe by default.** Diagnostic endpoints are read-only. Remediation endpoints are idempotent and require explicit confirmation for destructive actions via a `confirm: true` body field (without it, the endpoint returns a dry-run preview of what would happen).
- **Audited.** All agent-initiated operations produce the same audit trail as human-initiated operations (Section 11.7). The audit event includes the caller's identity, which distinguishes agent service accounts from human operators.

**Authentication for agent callers.** Agent callers authenticate using the same OIDC-based mechanism as human operators and `lenny-ctl` (Section 15.1). Deployers create dedicated service accounts with the `platform-admin` or `tenant-admin` role for their DevOps agents. A `caller_type: "agent"` claim in the JWT token identifies agent callers in audit events. No separate authentication mechanism is introduced — agents are first-class API consumers.

---

## 25.2 Platform Health API

A unified health surface that synthesizes component status, metric thresholds, and alert states into structured, actionable responses.

### `GET /v1/admin/health`

Returns the aggregate health of the Lenny installation. Each component reports a status (`healthy`, `degraded`, `unhealthy`) derived from the alerting rules in Section 16.5, plus a machine-readable summary of what is wrong and what action to take.

```json
{
  "status": "degraded",
  "checkedAt": "2026-04-08T14:30:00Z",
  "components": {
    "gateway": {
      "status": "healthy",
      "replicas": { "ready": 3, "desired": 3 },
      "details": {}
    },
    "warmPools": {
      "status": "degraded",
      "pools": [
        {
          "name": "default-gvisor",
          "status": "unhealthy",
          "idle": 0,
          "warming": 2,
          "claimed": 18,
          "minWarm": 5,
          "issue": "WARM_POOL_EXHAUSTED",
          "since": "2026-04-08T14:22:00Z",
          "suggestedAction": {
            "action": "SCALE_WARM_POOL",
            "endpoint": "PUT /v1/admin/pools/default-gvisor/warm-count",
            "body": { "minWarm": 15 },
            "reasoning": "Pool exhausted for 8 minutes. Current claimed (18) exceeds minWarm (5) by 3.6x. Recommended minWarm = ceil(claimed * 1.3 / 2) * 2 = 14, rounded to 15."
          }
        },
        {
          "name": "default-runc",
          "status": "healthy",
          "idle": 8,
          "warming": 0,
          "claimed": 12,
          "minWarm": 5,
          "issue": null
        }
      ]
    },
    "postgres": {
      "status": "healthy",
      "replicationLagMs": 12,
      "connectionPoolUtilization": 0.35,
      "details": {}
    },
    "redis": {
      "status": "healthy",
      "memoryUtilization": 0.42,
      "sentinelQuorum": true,
      "details": {}
    },
    "objectStore": {
      "status": "healthy",
      "details": {}
    },
    "certManager": {
      "status": "healthy",
      "nextCertExpiry": "2026-04-08T18:30:00Z",
      "details": {}
    },
    "credentialPools": {
      "status": "healthy",
      "pools": [],
      "details": {}
    },
    "controllers": {
      "status": "healthy",
      "warmPoolController": { "leaderElected": true, "lastReconcile": "2026-04-08T14:29:55Z" },
      "poolScalingController": { "leaderElected": true, "lastReconcile": "2026-04-08T14:29:58Z" }
    },
    "circuitBreakers": {
      "status": "healthy",
      "openBreakers": []
    }
  },
  "activeAlerts": []
}
```

**Component health derivation.** Each component's status is derived deterministically from the same thresholds used by the alerting rules (Section 16.5):
- `healthy` — no firing alerts for this component
- `degraded` — warning-severity alerts firing
- `unhealthy` — critical-severity alerts firing

**`suggestedAction` contract.** When a component is degraded or unhealthy, the `suggestedAction` object contains: `action` (enum — `SCALE_WARM_POOL`, `ADD_CREDENTIALS`, `RESTART_COMPONENT`, `OPEN_CIRCUIT_BREAKER`, `TRIGGER_FAILOVER`, etc.), `endpoint` (the admin API endpoint to call), `body` (the request body), and `reasoning` (human-readable explanation of why this action is recommended). Agents can execute the suggestion by calling the endpoint directly. The `suggestedAction` is advisory — the agent is free to ignore it or modify the parameters.

### `GET /v1/admin/health/{component}`

Deep-dive into a single component. Returns the same schema as the component object in the aggregate response, plus additional `diagnostics` and `metrics` fields with the most recent metric values relevant to that component.

```json
{
  "component": "warmPools",
  "status": "degraded",
  "pools": [ "..." ],
  "diagnostics": {
    "podStartupP99Seconds": 4.2,
    "imagePullErrors": 0,
    "nodeResourcePressure": false,
    "quotaExhausted": false,
    "recentEvents": [
      {
        "timestamp": "2026-04-08T14:22:00Z",
        "type": "WARM_POOL_EXHAUSTED",
        "pool": "default-gvisor",
        "message": "Idle pod count reached 0"
      }
    ]
  },
  "metrics": {
    "lenny_warmpool_idle_pods": { "value": 0, "labels": { "pool": "default-gvisor" } },
    "lenny_warmpool_pod_startup_duration_seconds_p99": { "value": 4.2, "labels": { "pool": "default-gvisor" } }
  }
}
```

**Metrics subsection.** The `metrics` field exposes the most recent values of the Prometheus metrics relevant to the requested component, without requiring the caller to query Prometheus directly. The gateway reads these values from its in-process metric registry (the same registry scraped by Prometheus). This is a convenience for agents that do not have Prometheus access — it does not replace Prometheus for time-series queries or alerting.

### `GET /v1/admin/health/summary`

Minimal endpoint for monitoring integrations. Returns only the aggregate status and a list of unhealthy component names, suitable for a synthetic health check.

```json
{
  "status": "degraded",
  "unhealthy": ["warmPools"],
  "degraded": ["warmPools"],
  "checkedAt": "2026-04-08T14:30:00Z"
}
```

---

## 25.3 Operational Event Stream

A real-time feed of platform operational events. Agents subscribe to this stream instead of polling individual endpoints.

### `GET /v1/admin/events/stream` (SSE)

Server-Sent Events stream of operational events. The connection remains open and delivers events as they occur.

```
event: alert_fired
data: {"alertName":"WarmPoolExhausted","severity":"critical","pool":"default-gvisor","timestamp":"2026-04-08T14:22:00Z","runbookRef":"docs/runbooks/warm-pool-exhaustion.md","suggestedAction":{"action":"SCALE_WARM_POOL","endpoint":"PUT /v1/admin/pools/default-gvisor/warm-count","body":{"minWarm":15}}}

event: upgrade_progressed
data: {"pool":"default-runc","phase":"Expanding","previousPhase":"Paused","newImageDigest":"sha256:abc123","timestamp":"2026-04-08T14:25:00Z"}

event: circuit_breaker_opened
data: {"name":"runtime-x-degraded","reason":"Elevated error rate","openedBy":"agent-ops@svc","timestamp":"2026-04-08T14:26:00Z"}

event: session_failed
data: {"sessionId":"s-abc123","runtime":"my-agent","failureClass":"POD_CRASH","exitCode":137,"timestamp":"2026-04-08T14:27:00Z"}

event: credential_rotated
data: {"pool":"openai-pool","credentialId":"cred-456","reason":"rate_limit","timestamp":"2026-04-08T14:28:00Z"}
```

**Event types:**

| Event type | Trigger | Payload highlights |
|---|---|---|
| `alert_fired` | Any alerting rule (§16.5) fires | Alert name, severity, labels, runbook ref, suggested action |
| `alert_resolved` | A previously firing alert resolves | Alert name, duration |
| `upgrade_progressed` | Pool upgrade state machine advances (§10.5) | Pool, old/new phase, image digest |
| `pool_state_changed` | Pool enters/exits draining, warming, exhausted | Pool, old/new state |
| `circuit_breaker_opened` | Circuit breaker opened | Name, reason, opener |
| `circuit_breaker_closed` | Circuit breaker closed | Name, closer |
| `credential_rotated` | Credential lease rotated | Pool, credential ID, reason |
| `credential_pool_exhausted` | No available credentials | Pool |
| `session_failed` | Session entered `failed` state | Session ID, runtime, failure class |
| `backup_completed` | Backup job finished | Type, status, size, duration |
| `backup_failed` | Backup job failed | Type, error |
| `platform_upgrade_available` | New Lenny release detected | Current version, available version, changelog URL |
| `drift_detected` | Configuration drift detected (§25.8) | Resource type, resource name, drifted fields |
| `health_status_changed` | Aggregate health transitioned | Old status, new status, triggering component |

**Filtering.** Clients filter via query parameters: `?types=alert_fired,session_failed` (comma-separated event types), `?severity=critical,warning` (for alert events), `?pool=default-gvisor` (scoped to a pool), `?since=2026-04-08T14:00:00Z` (replay events after a timestamp for catch-up after reconnection).

**Delivery guarantees.** Each event carries a monotonically increasing `eventId` (string). Clients reconnect with `Last-Event-ID` header to resume from the last received event. The gateway buffers the most recent 10,000 events in Redis (key: `ops:events:stream`, capped stream). Events older than the buffer are available via the polling endpoint below.

### `GET /v1/admin/events`

Polling endpoint for agents that cannot use SSE. Returns paginated events with the same schema. Query parameters: `?types=`, `?severity=`, `?pool=`, `?since=`, `?until=`, `?limit=` (default 100, max 1000), `?cursor=` (opaque pagination cursor from the `nextCursor` response field).

### `POST /v1/admin/event-subscriptions`

Register a webhook for operational events. The gateway POSTs events to the registered URL as they occur.

```json
{
  "callbackUrl": "https://ops-agent.internal/lenny-events",
  "types": ["alert_fired", "alert_resolved", "upgrade_progressed"],
  "severity": ["critical", "warning"],
  "secret": "whsec_...",
  "description": "DevOps agent event sink"
}
```

Response: `{"subscriptionId": "sub-789", "status": "active"}`.

Events are delivered as HTTP POST with an HMAC-SHA256 signature in the `X-Lenny-Signature` header (same scheme as A2A outbound push, Section 21.1). Delivery failures are retried with exponential back-off (3 attempts, max 30s). The `callbackUrl` must pass the same SSRF validation as session callback URLs (Section 14).

**Subscription management:**

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/v1/admin/event-subscriptions` | List all event subscriptions |
| `GET` | `/v1/admin/event-subscriptions/{id}` | Get subscription details and delivery stats |
| `PUT` | `/v1/admin/event-subscriptions/{id}` | Update subscription filters |
| `DELETE` | `/v1/admin/event-subscriptions/{id}` | Delete a subscription |
| `GET` | `/v1/admin/event-subscriptions/{id}/deliveries` | List recent delivery attempts with status |

---

## 25.4 Diagnostic Endpoints

Structured diagnostic endpoints that encapsulate the diagnosis steps from each operational runbook (Section 17.7). These replace the `kubectl`, `psql`, `redis-cli`, and `mc` commands in the runbooks with API calls that return structured results.

### `GET /v1/admin/diagnostics/sessions/{id}`

Returns a structured cause chain explaining why a session is in its current state. Replaces manual log parsing and pod inspection.

```json
{
  "sessionId": "s-abc123",
  "state": "failed",
  "runtime": "my-agent",
  "pool": "default-gvisor",
  "causeChain": [
    {
      "level": 0,
      "category": "POD_CRASH",
      "summary": "Agent container exited with code 137 (OOMKilled)",
      "details": {
        "exitCode": 137,
        "oomKilled": true,
        "memoryLimitBytes": 2147483648,
        "peakMemoryBytes": 2140000000,
        "containerName": "agent"
      },
      "timestamp": "2026-04-08T14:27:00Z"
    },
    {
      "level": 1,
      "category": "RESOURCE_PRESSURE",
      "summary": "Container memory usage reached 99.6% of limit (2Gi) before OOM",
      "details": {
        "recommendation": "Increase pool resource limit to 4Gi or optimize runtime memory usage"
      }
    }
  ],
  "retryHistory": [
    {
      "attempt": 1,
      "podName": "lenny-agent-abc-1",
      "result": "POD_CRASH",
      "timestamp": "2026-04-08T14:25:00Z"
    },
    {
      "attempt": 2,
      "podName": "lenny-agent-abc-2",
      "result": "POD_CRASH",
      "timestamp": "2026-04-08T14:27:00Z"
    }
  ],
  "suggestedActions": [
    {
      "action": "UPDATE_POOL_RESOURCES",
      "endpoint": "PUT /v1/admin/pools/default-gvisor",
      "reasoning": "OOMKilled twice — memory limit (2Gi) is insufficient for this runtime."
    }
  ],
  "relatedLogs": {
    "endpoint": "GET /v1/sessions/s-abc123/logs",
    "lastLines": 20,
    "snippet": ["...last 5 log lines..."]
  }
}
```

### `GET /v1/admin/diagnostics/pools/{name}`

Diagnoses a pool's current state. Encapsulates the diagnosis section of the warm-pool-exhaustion runbook.

```json
{
  "pool": "default-gvisor",
  "status": "exhausted",
  "podCounts": {
    "idle": 0,
    "warming": 2,
    "claimed": 18,
    "terminating": 0
  },
  "config": {
    "minWarm": 5,
    "maxWarm": 50,
    "executionMode": "session"
  },
  "bottleneck": {
    "category": "DEMAND_EXCEEDS_SUPPLY",
    "details": {
      "claimRatePerMinute": 4.2,
      "replenishmentRatePerMinute": 1.8,
      "podStartupP99Seconds": 12.5,
      "imagePullHealthy": true,
      "nodeResourcePressure": false,
      "quotaExhausted": false,
      "setupCommandFailures": 0
    },
    "summary": "Pod replenishment rate (1.8/min) cannot keep pace with claim rate (4.2/min). Pod startup is slow (p99=12.5s)."
  },
  "suggestedActions": [
    {
      "action": "SCALE_WARM_POOL",
      "endpoint": "PUT /v1/admin/pools/default-gvisor/warm-count",
      "body": { "minWarm": 15 },
      "reasoning": "Increase minWarm to absorb demand spikes."
    }
  ],
  "crdSyncStatus": {
    "inSync": true,
    "lagSeconds": 0
  }
}
```

### `GET /v1/admin/diagnostics/connectivity`

Tests connectivity to all platform dependencies and returns structured results. Replaces the `psql`, `redis-cli`, and `mc` commands used in multiple runbooks for initial diagnosis.

```json
{
  "checkedAt": "2026-04-08T14:30:00Z",
  "components": {
    "postgres": {
      "status": "reachable",
      "latencyMs": 2,
      "primaryHealthy": true,
      "replicaHealthy": true,
      "replicationLagMs": 12,
      "connectionPoolUtilization": 0.35,
      "rlsActive": true,
      "schemaVersion": "v1.42"
    },
    "redis": {
      "status": "reachable",
      "latencyMs": 1,
      "sentinelQuorum": true,
      "memoryUtilization": 0.42,
      "tlsActive": true
    },
    "objectStore": {
      "status": "reachable",
      "latencyMs": 5,
      "bucketAccessible": true,
      "encryptionEnabled": true
    },
    "certManager": {
      "status": "reachable",
      "certificatesHealthy": true,
      "nextExpiry": "2026-04-08T18:30:00Z"
    },
    "kubeApiServer": {
      "status": "reachable",
      "latencyMs": 3,
      "crdVersions": {
        "sandboxes.sandbox.sigs.k8s.io": "v1alpha1"
      }
    }
  },
  "connectors": [
    {
      "name": "github-connector",
      "status": "reachable",
      "latencyMs": 45,
      "tlsValid": true,
      "lastSuccessfulCall": "2026-04-08T14:29:00Z"
    }
  ]
}
```

### `GET /v1/admin/diagnostics/credential-pools/{name}`

Diagnoses credential pool health. Encapsulates the diagnosis section of the credential-pool-exhaustion runbook.

```json
{
  "pool": "openai-pool",
  "status": "degraded",
  "credentials": {
    "total": 5,
    "available": 1,
    "leased": 3,
    "coolingDown": 1,
    "revoked": 0
  },
  "hotKeys": [
    {
      "credentialId": "cred-123",
      "rateLimitEvents24h": 42,
      "cooldownRemaining": "45s"
    }
  ],
  "providerRateLimitTier": "tier-2",
  "suggestedActions": [
    {
      "action": "ADD_CREDENTIALS",
      "endpoint": "POST /v1/admin/credential-pools/openai-pool/credentials",
      "reasoning": "Pool is at 80% utilization with active rate limiting. Add 2-3 credentials to absorb load."
    }
  ]
}
```

---

## 25.5 Machine-Executable Runbooks

Each operational runbook (Section 17.7) is published in a machine-executable format alongside the human-readable Markdown version. The machine-executable format enables agents to follow runbook procedures autonomously.

### Runbook Schema

Machine-executable runbooks are stored in `docs/runbooks/` alongside their Markdown counterparts, with a `.runbook.yaml` suffix (e.g., `warm-pool-exhaustion.runbook.yaml`).

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

    - id: check_health
      description: Get overall platform health
      action:
        method: GET
        endpoint: "/v1/admin/health/warmPools"
      outputs:
        - name: pool_health
          jsonPath: "$.pools[?(@.name=='{{pool}}')]"

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

### Runbook Registry API

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/v1/admin/runbooks` | List all registered machine-executable runbooks |
| `GET` | `/v1/admin/runbooks/{name}` | Get a specific runbook definition |
| `POST` | `/v1/admin/runbooks/{name}/execute` | Execute a runbook. Body: `{"parameters": {...}, "mode": "auto"|"step"|"dry-run"}`. `auto` executes the full diagnosis-decision-remediation-verification loop. `step` pauses after each step and returns the next step for the caller to approve. `dry-run` runs diagnosis and decision only, returning the selected remediation without executing it. |
| `GET` | `/v1/admin/runbooks/{name}/executions` | List recent executions of a runbook |
| `GET` | `/v1/admin/runbook-executions/{id}` | Get execution details: steps completed, current step, outputs, decisions made |
| `POST` | `/v1/admin/runbook-executions/{id}/confirm` | Confirm a pending remediation step in `step` mode or for `confirmation: required` steps |

**Execution model.** Runbook execution is a server-side state machine. The gateway processes each step, evaluates conditions, and advances to the next step. In `auto` mode, remediation steps that have `confirmation: required` still require the caller to confirm — the execution pauses and returns a `202 Accepted` with `status: awaiting_confirmation` and `confirmEndpoint: POST /v1/admin/runbook-executions/{id}/confirm`. This ensures agents explicitly approve destructive actions.

**Escalation.** When a runbook decision tree reaches an `escalation` node (a condition it cannot handle autonomously), the execution pauses with `status: escalated` and the escalation reason. The event stream (Section 25.3) emits a `runbook_escalated` event. The expectation is that a human or a higher-level agent handles the escalation.

**Custom runbooks.** Deployers can register custom runbooks via the admin API (`POST /v1/admin/runbooks`) or by adding `.runbook.yaml` files to the Helm chart's `files/runbooks/` directory. Custom runbooks follow the same schema and can reference any admin API endpoint.

---

## 25.6 Platform Self-Management API

APIs for managing Lenny's own lifecycle — version introspection, upgrade orchestration, and configuration management.

### `GET /v1/admin/platform/version`

Returns the current version of all Lenny components.

```json
{
  "gateway": {
    "version": "1.2.0",
    "gitCommit": "abc123",
    "buildDate": "2026-04-01T00:00:00Z",
    "goVersion": "go1.23.0"
  },
  "controllers": {
    "warmPoolController": { "version": "1.2.0" },
    "poolScalingController": { "version": "1.2.0" }
  },
  "crds": {
    "sandboxes.sandbox.sigs.k8s.io": { "installed": "v1alpha1", "required": "v1alpha1" },
    "sandboxtemplates.sandbox.sigs.k8s.io": { "installed": "v1alpha1", "required": "v1alpha1" }
  },
  "helmChart": {
    "version": "1.2.0",
    "appVersion": "1.2.0"
  },
  "schema": {
    "postgres": { "current": "v1.42", "required": "v1.42" },
    "redis": { "current": "v1.5", "required": "v1.5" }
  }
}
```

**Version drift detection.** When any component's `current` version does not match `required`, the response includes a top-level `"versionDrift": true` field and each drifted component includes `"drift": true` and `"requiredAction"` describing what needs to happen (e.g., `"Run schema migration v1.41 -> v1.42"`).

### `GET /v1/admin/platform/upgrade-check`

Checks for available Lenny upgrades. The gateway queries a configurable release channel endpoint (`platform.upgradeChannel`, default: `https://releases.lenny.dev/v1/latest`). Deployers can point this at an internal mirror or disable it entirely (`platform.upgradeChannel: ""` disables the check). The check is also performed periodically (every 6 hours by default) and the result is cached; an `alert_fired` event with `alertName: PlatformUpgradeAvailable` is emitted when a new version is detected.

```json
{
  "currentVersion": "1.2.0",
  "latestVersion": "1.3.0",
  "upgradeAvailable": true,
  "releaseNotes": "https://github.com/lenny-dev/lenny/releases/tag/v1.3.0",
  "breakingChanges": false,
  "crdChanges": true,
  "migrationRequired": true,
  "upgradeSteps": [
    "1. Apply CRD updates: lenny-ctl platform upgrade apply-crds --version 1.3.0",
    "2. Run schema migration: lenny-ctl platform upgrade migrate --version 1.3.0",
    "3. Roll gateway: lenny-ctl platform upgrade roll-gateway --version 1.3.0",
    "4. Roll controllers: lenny-ctl platform upgrade roll-controllers --version 1.3.0",
    "5. Verify: lenny-ctl platform upgrade verify"
  ],
  "estimatedDowntime": "0s (rolling upgrade)",
  "preflightEndpoint": "POST /v1/admin/platform/upgrade/preflight"
}
```

### Platform Upgrade State Machine

Like pool upgrades (Section 10.5), platform self-upgrades follow a tracked, pauseable state machine.

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/v1/admin/platform/upgrade/preflight` | Validate that an upgrade is safe: check CRD compatibility, schema migration path, resource availability. Returns a go/no-go assessment. |
| `POST` | `/v1/admin/platform/upgrade/start` | Begin platform upgrade. Body: `{"targetVersion": "1.3.0"}`. Requires passing preflight. |
| `POST` | `/v1/admin/platform/upgrade/proceed` | Advance to the next upgrade phase. |
| `POST` | `/v1/admin/platform/upgrade/pause` | Pause upgrade. |
| `POST` | `/v1/admin/platform/upgrade/rollback` | Rollback to the previous version. Only valid before the point-of-no-return (schema migration completion). |
| `GET` | `/v1/admin/platform/upgrade/status` | Current upgrade state, phase, progress percentage, and per-component status. |
| `POST` | `/v1/admin/platform/upgrade/verify` | Run post-upgrade health verification. |

**Upgrade phases:** `Preflight` -> `CRDUpdate` -> `SchemaMigration` -> `GatewayRoll` -> `ControllerRoll` -> `Verification` -> `Complete`. The state machine pauses between phases and requires `proceed` to advance. This allows an agent to verify health at each step before continuing.

**Implementation.** The upgrade API orchestrates Helm operations and schema migrations internally. The gateway delegates CRD updates and Deployment image patches to the Kubernetes API using its service account. The schema migration uses the same migration framework as `lenny-preflight` (Section 17.6). The gateway's service account requires RBAC to update its own Deployment and the controller Deployments — these permissions are included in the Helm chart and documented in Section 17.1.

**Rollback constraints.** Rollback is available before schema migration completes. After schema migration, the database schema may be incompatible with the old binary — rollback requires a database restore from the pre-upgrade backup (the upgrade state machine creates an automatic backup before `SchemaMigration`). The rollback endpoint communicates this constraint in its response when called after schema migration.

### Configuration Management

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/v1/admin/platform/config` | Returns the effective running configuration (Helm values + admin API overrides). Secrets are redacted. |
| `GET` | `/v1/admin/platform/config/diff` | Compares the running config against a supplied configuration. Body: `{"desired": {...}}`. Returns a structured diff. Used for GitOps reconciliation. |
| `PUT` | `/v1/admin/platform/config` | Apply a configuration change at runtime. Only supports a subset of settings that can be changed without restart (pool sizes, quotas, rate limits, circuit breakers). Returns `422 RESTART_REQUIRED` for settings that require a restart, with the specific settings listed. |

---

## 25.7 Audit Log Query API

Structured query access to the audit trail (Section 11.7). Enables agents to investigate incidents without direct database access.

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/v1/admin/audit-events` | Query audit events. Params: `?since=`, `?until=`, `?eventType=`, `?actorId=`, `?resourceType=`, `?resourceId=`, `?tenantId=`, `?severity=`, `?limit=` (default 100, max 1000), `?cursor=`. Returns paginated results. |
| `GET` | `/v1/admin/audit-events/{id}` | Get a single audit event with full payload. |
| `GET` | `/v1/admin/audit-events/summary` | Aggregate summary: event counts by type and severity over a time window. Params: `?since=`, `?until=`, `?groupBy=eventType|actorId|resourceType`. Useful for agents scanning for anomalies. |

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

**Chain integrity.** Each event in the response includes a `chainIntegrity` field (`verified`, `broken`, `unchecked`) indicating whether the hash chain (Section 11.7) is intact up to and including this event. This allows agents to detect audit tampering without running a full chain verification.

---

## 25.8 Configuration Drift Detection

Detects discrepancies between the desired platform state and the actual running state. Essential for GitOps workflows where an agent ensures the cluster matches a version-controlled configuration.

### `GET /v1/admin/drift`

Compares the running platform state against the most recently applied bootstrap seed (Section 17.6) or a deployer-supplied desired state. Returns a structured drift report.

```json
{
  "checkedAt": "2026-04-08T14:30:00Z",
  "driftDetected": true,
  "drifts": [
    {
      "resourceType": "runtime",
      "resourceName": "my-agent",
      "field": "image",
      "expected": "my-agent:v2.1@sha256:abc123",
      "actual": "my-agent:v2.0@sha256:def456",
      "severity": "medium",
      "source": "bootstrap-seed"
    },
    {
      "resourceType": "pool",
      "resourceName": "default-gvisor",
      "field": "minWarm",
      "expected": 10,
      "actual": 5,
      "severity": "low",
      "source": "admin-api-override",
      "note": "Override applied via PUT /v1/admin/pools at 2026-04-07T10:00:00Z"
    }
  ],
  "crdDrift": {
    "detected": false,
    "details": []
  },
  "schemaDrift": {
    "detected": false,
    "details": []
  }
}
```

**Drift sources.** Each drift entry includes a `source` field indicating what the expected value is compared against: `bootstrap-seed` (the last applied `POST /v1/admin/bootstrap` payload), `admin-api-override` (an explicit runtime change via the admin API), or `external` (a change made outside the admin API, e.g., direct CRD edit via kubectl).

### `POST /v1/admin/drift/reconcile`

Reconcile drifted resources back to their expected state. Body: `{"scope": "all"}` or `{"scope": "resources", "resources": [{"type": "runtime", "name": "my-agent"}, ...]}`. Supports `"mode": "dry-run"` to preview changes. Each reconciliation action is audited.

---

## 25.9 Capacity Recommendations

Synthesizes current metrics and usage patterns into actionable capacity recommendations. This closes the gap between "data exists in Prometheus" and "an agent knows what to do."

### `GET /v1/admin/recommendations`

Returns a prioritized list of capacity and configuration recommendations based on current platform state and recent usage patterns.

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
      "reasoning": "Pool has been exhausted 3 times in the past 24h (at 09:15, 11:42, 14:22). Peak claim rate is 4.2/min during business hours. Current minWarm (5) is insufficient to absorb burst demand.",
      "currentValue": { "minWarm": 5 },
      "recommendedValue": { "minWarm": 15 },
      "action": {
        "endpoint": "PUT /v1/admin/pools/default-gvisor/warm-count",
        "body": { "minWarm": 15 }
      },
      "confidence": 0.85,
      "basedOn": {
        "metric": "lenny_warmpool_idle_pods",
        "window": "24h",
        "exhaustionCount": 3
      }
    },
    {
      "id": "rec-002",
      "priority": "medium",
      "category": "credential_pool_sizing",
      "resource": "credential-pool/openai-pool",
      "title": "Add credentials to openai-pool",
      "reasoning": "Pool utilization has averaged 78% over the past 7 days with 12 rate-limit events. Adding 2 credentials would bring utilization below 60%.",
      "currentValue": { "credentialCount": 5 },
      "recommendedValue": { "credentialCount": 7 },
      "action": {
        "endpoint": "POST /v1/admin/credential-pools/openai-pool/credentials",
        "note": "Repeat for each new credential"
      },
      "confidence": 0.72,
      "basedOn": {
        "metric": "lenny_credential_pool_utilization",
        "window": "7d",
        "avgUtilization": 0.78
      }
    }
  ]
}
```

**Recommendation categories:**

| Category | Description | Signals used |
|---|---|---|
| `warm_pool_sizing` | minWarm/maxWarm adjustment | Exhaustion events, claim rate, replenishment rate |
| `credential_pool_sizing` | Add/remove credentials | Utilization, rate limit events, cooling-down frequency |
| `gateway_scaling` | Gateway replica count | CPU utilization, queue depth, rejection rate |
| `resource_limits` | Pod resource request/limit tuning | OOM events, CPU throttling, memory high-watermark |
| `retention_tuning` | Artifact/log retention adjustment | Storage utilization, growth rate |
| `quota_adjustment` | Tenant quota increase/decrease | Quota utilization, rejection rate |

**Recommendation engine.** The recommendations are generated by a rules engine embedded in the gateway. Each rule defines a condition (expressed as a metric threshold over a time window) and a recommendation template. The rules are not AI-powered — they are deterministic heuristics documented in the gateway codebase. Deployers can disable specific rules via `platform.recommendations.disabledRules` in Helm values.

---

## 25.10 Backup and Restore API

APIs for managing platform backups, extending the disaster recovery procedures in Section 17.3 with programmatic access.

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/v1/admin/backups` | Trigger an on-demand backup. Body: `{"components": ["postgres", "objectStore"] | "all", "label": "pre-upgrade-1.3.0"}`. Returns a backup job ID. |
| `GET` | `/v1/admin/backups` | List available backups. Params: `?component=`, `?since=`, `?until=`, `?label=`, `?limit=`. |
| `GET` | `/v1/admin/backups/{id}` | Get backup details: status, size, duration, component checksums. |
| `POST` | `/v1/admin/backups/{id}/verify` | Verify a backup is restorable. Runs the same verification as the `lenny-restore-test` CronJob (Section 17.3): creates a temporary Postgres instance, verifies schema integrity, runs smoke queries, and reports results. Returns a job ID — verification is async. |
| `GET` | `/v1/admin/backup-jobs/{id}` | Get backup/verify job status and result. |
| `GET` | `/v1/admin/backups/schedule` | Get the current backup schedule (CronJob configuration). |
| `PUT` | `/v1/admin/backups/schedule` | Update the backup schedule. |
| `GET` | `/v1/admin/backups/policy` | Get backup retention policy. |
| `PUT` | `/v1/admin/backups/policy` | Update backup retention policy. |

**Restore operations.** Restore is intentionally not exposed as a one-click API due to its destructive nature (it replaces the current database). Instead, restore follows the confirmation pattern:

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/v1/admin/restore/preview` | Analyze what a restore would do: current data age vs. backup age, estimated data loss, affected sessions. Returns a preview without executing. |
| `POST` | `/v1/admin/restore/execute` | Execute a restore. Requires `confirm: true` in the body. Without it, returns the same preview as above. Body: `{"backupId": "...", "confirm": true}`. |

---

## 25.11 MCP Management Server

Lenny exposes its operational surface as an MCP tool server, enabling any MCP-capable agent to manage Lenny natively through the same protocol used for agent-to-tool interaction.

**Endpoint:** `/mcp/management` — a dedicated MCP Streamable HTTP endpoint served by the gateway. Authenticates using the same OIDC tokens as the admin REST API. Requires `platform-admin` or `tenant-admin` role.

**Capability negotiation.** The management MCP server advertises its tools via standard MCP `tools/list`. Tool schemas are auto-generated from the admin API's OpenAPI spec (Section 15.1), ensuring the MCP tool surface stays in sync with the REST API without manual maintenance.

**Tool inventory (representative, not exhaustive):**

```json
{
  "tools": [
    {
      "name": "lenny/platform_health",
      "description": "Get the aggregate health status of the Lenny platform, including all components, active alerts, and suggested remediation actions.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "component": {
            "type": "string",
            "description": "Optional. Specific component to inspect (gateway, warmPools, postgres, redis, objectStore, certManager, credentialPools, controllers, circuitBreakers). Omit for aggregate health."
          }
        }
      },
      "annotations": {
        "title": "Platform Health Check",
        "readOnlyHint": true,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/diagnose_session",
      "description": "Get a structured cause chain explaining why a session is in its current state, including retry history and suggested remediation actions.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "sessionId": { "type": "string", "description": "The session ID to diagnose." }
        },
        "required": ["sessionId"]
      },
      "annotations": {
        "title": "Diagnose Session",
        "readOnlyHint": true,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/diagnose_pool",
      "description": "Diagnose a warm pool's current state, including pod counts, bottleneck analysis, and remediation suggestions.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "pool": { "type": "string", "description": "Pool name to diagnose." }
        },
        "required": ["pool"]
      },
      "annotations": {
        "title": "Diagnose Pool",
        "readOnlyHint": true,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/execute_runbook",
      "description": "Execute a machine-readable operational runbook. Supports auto, step-by-step, and dry-run modes.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "runbook": { "type": "string", "description": "Runbook name (e.g., 'warm-pool-exhaustion')." },
          "parameters": { "type": "object", "description": "Runbook parameters (e.g., pool name)." },
          "mode": { "type": "string", "enum": ["auto", "step", "dry-run"], "description": "Execution mode. Default: dry-run." }
        },
        "required": ["runbook"]
      },
      "annotations": {
        "title": "Execute Runbook",
        "readOnlyHint": false,
        "openWorldHint": false,
        "destructiveHint": true
      }
    },
    {
      "name": "lenny/scale_pool",
      "description": "Adjust the minWarm count for a warm pool. Use after diagnosing pool exhaustion.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "pool": { "type": "string" },
          "minWarm": { "type": "integer", "minimum": 0 }
        },
        "required": ["pool", "minWarm"]
      },
      "annotations": {
        "title": "Scale Warm Pool",
        "readOnlyHint": false,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/deploy_runtime",
      "description": "Register a new runtime definition or update an existing one.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "name": { "type": "string" },
          "type": { "type": "string", "enum": ["agent", "mcp"] },
          "image": { "type": "string" },
          "config": { "type": "object", "description": "Full runtime configuration." }
        },
        "required": ["name", "type", "image"]
      },
      "annotations": {
        "title": "Deploy Runtime",
        "readOnlyHint": false,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/platform_upgrade_check",
      "description": "Check if a Lenny platform upgrade is available and get upgrade steps.",
      "inputSchema": { "type": "object", "properties": {} },
      "annotations": {
        "title": "Check Platform Upgrade",
        "readOnlyHint": true,
        "openWorldHint": true
      }
    },
    {
      "name": "lenny/start_platform_upgrade",
      "description": "Begin a platform upgrade to the specified version. The upgrade follows a phased state machine that pauses between phases for verification.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "targetVersion": { "type": "string", "description": "Target Lenny version." }
        },
        "required": ["targetVersion"]
      },
      "annotations": {
        "title": "Start Platform Upgrade",
        "readOnlyHint": false,
        "openWorldHint": false,
        "destructiveHint": true
      }
    },
    {
      "name": "lenny/query_audit_events",
      "description": "Search the audit log for events matching the specified filters.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "since": { "type": "string", "format": "date-time" },
          "until": { "type": "string", "format": "date-time" },
          "eventType": { "type": "string" },
          "actorId": { "type": "string" },
          "resourceType": { "type": "string" },
          "limit": { "type": "integer", "default": 50, "maximum": 200 }
        }
      },
      "annotations": {
        "title": "Query Audit Events",
        "readOnlyHint": true,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/get_recommendations",
      "description": "Get capacity and configuration recommendations based on current platform metrics and usage patterns.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "category": {
            "type": "string",
            "enum": ["warm_pool_sizing", "credential_pool_sizing", "gateway_scaling", "resource_limits", "retention_tuning", "quota_adjustment"],
            "description": "Optional. Filter by recommendation category."
          }
        }
      },
      "annotations": {
        "title": "Get Recommendations",
        "readOnlyHint": true,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/connectivity_check",
      "description": "Test connectivity to all platform dependencies (Postgres, Redis, object store, cert-manager, connectors) and return structured results.",
      "inputSchema": { "type": "object", "properties": {} },
      "annotations": {
        "title": "Connectivity Check",
        "readOnlyHint": true,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/trigger_backup",
      "description": "Trigger an on-demand backup of platform data stores.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "components": {
            "type": "array",
            "items": { "type": "string", "enum": ["postgres", "objectStore"] },
            "description": "Components to back up. Default: all."
          },
          "label": { "type": "string", "description": "Human-readable label for the backup." }
        }
      },
      "annotations": {
        "title": "Trigger Backup",
        "readOnlyHint": false,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/drift_report",
      "description": "Detect configuration drift between the running platform state and the desired state (bootstrap seed or supplied config).",
      "inputSchema": { "type": "object", "properties": {} },
      "annotations": {
        "title": "Configuration Drift Report",
        "readOnlyHint": true,
        "openWorldHint": false
      }
    },
    {
      "name": "lenny/manage_circuit_breaker",
      "description": "Open or close a circuit breaker to control traffic to a degraded runtime, pool, or connector.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "name": { "type": "string", "description": "Circuit breaker name." },
          "state": { "type": "string", "enum": ["open", "closed"] },
          "reason": { "type": "string", "description": "Required when opening." }
        },
        "required": ["name", "state"]
      },
      "annotations": {
        "title": "Manage Circuit Breaker",
        "readOnlyHint": false,
        "openWorldHint": false
      }
    }
  ]
}
```

**Auto-generation pipeline.** The MCP tool definitions are generated from the OpenAPI spec at build time. Each admin API endpoint maps to one MCP tool. The mapping rules:
- Tool name: `lenny/{operation_id}` from the OpenAPI spec
- Input schema: request body JSON Schema (for POST/PUT) or query parameters (for GET)
- `readOnlyHint`: `true` for GET endpoints
- `destructiveHint`: `true` for DELETE endpoints and endpoints annotated with `x-lenny-destructive: true` in the OpenAPI spec

The generated tool definitions are served directly by the gateway — no separate MCP server process is required.

**Why a dedicated MCP endpoint.** The `/mcp/runtimes/{name}` endpoints (Section 15) serve `type: mcp` runtimes — they proxy MCP tool calls to agent pods. The `/mcp/management` endpoint is structurally different: it serves Lenny's own operational tools directly from the gateway. The two are separate MCP servers with separate capability negotiation and separate authentication scopes. A DevOps agent connects to `/mcp/management` to operate Lenny; a client agent connects to `/mcp/runtimes/{name}` to use Lenny-hosted tools.

**Usage example.** A DevOps agent (e.g., a Claude Code instance with MCP server configuration pointing at Lenny's management endpoint) can:

1. Call `lenny/platform_health` — sees that `warmPools` is degraded
2. Call `lenny/diagnose_pool` with `pool: "default-gvisor"` — gets structured bottleneck analysis
3. Call `lenny/execute_runbook` with `runbook: "warm-pool-exhaustion"`, `mode: "dry-run"` — previews the remediation
4. Call `lenny/execute_runbook` with `mode: "auto"` — executes the remediation
5. Call `lenny/platform_health` again — verifies the pool has recovered

All without kubectl, Prometheus, or any tool other than the MCP protocol.

---

## 25.12 `lenny-ctl` Extensions

The following `lenny-ctl` command groups wrap the agent operability APIs defined above. All commands follow the same conventions as Section 24: `--output json`, `--quiet`, same global flags.

### 25.12.1 Health

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl health` | Show aggregate platform health | `GET /v1/admin/health` | `platform-admin` |
| `lenny-ctl health <component>` | Show component-specific health | `GET /v1/admin/health/{component}` | `platform-admin` |
| `lenny-ctl health summary` | Minimal health summary | `GET /v1/admin/health/summary` | `platform-admin` |

### 25.12.2 Events

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl events stream [--types <t1,t2>] [--severity <s>]` | Stream operational events (SSE) | `GET /v1/admin/events/stream` | `platform-admin` |
| `lenny-ctl events list [--since <t>] [--types <t1,t2>]` | List recent events | `GET /v1/admin/events` | `platform-admin` |
| `lenny-ctl events subscribe --url <callback> --types <t1,t2>` | Register a webhook subscription | `POST /v1/admin/event-subscriptions` | `platform-admin` |
| `lenny-ctl events subscriptions list` | List webhook subscriptions | `GET /v1/admin/event-subscriptions` | `platform-admin` |
| `lenny-ctl events subscriptions delete <id>` | Delete a subscription | `DELETE /v1/admin/event-subscriptions/{id}` | `platform-admin` |

### 25.12.3 Diagnostics

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl diagnose session <id>` | Diagnose a session | `GET /v1/admin/diagnostics/sessions/{id}` | `platform-admin` |
| `lenny-ctl diagnose pool <name>` | Diagnose a pool | `GET /v1/admin/diagnostics/pools/{name}` | `platform-admin` |
| `lenny-ctl diagnose credential-pool <name>` | Diagnose a credential pool | `GET /v1/admin/diagnostics/credential-pools/{name}` | `platform-admin` |
| `lenny-ctl diagnose connectivity` | Test all dependency connectivity | `GET /v1/admin/diagnostics/connectivity` | `platform-admin` |

### 25.12.4 Runbooks

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl runbook list` | List available runbooks | `GET /v1/admin/runbooks` | `platform-admin` |
| `lenny-ctl runbook show <name>` | Show a runbook definition | `GET /v1/admin/runbooks/{name}` | `platform-admin` |
| `lenny-ctl runbook execute <name> [--param key=val] [--mode auto\|step\|dry-run]` | Execute a runbook | `POST /v1/admin/runbooks/{name}/execute` | `platform-admin` |
| `lenny-ctl runbook executions <name>` | List recent executions | `GET /v1/admin/runbooks/{name}/executions` | `platform-admin` |
| `lenny-ctl runbook status <execution-id>` | Get execution status | `GET /v1/admin/runbook-executions/{id}` | `platform-admin` |
| `lenny-ctl runbook confirm <execution-id>` | Confirm a pending remediation step | `POST /v1/admin/runbook-executions/{id}/confirm` | `platform-admin` |

### 25.12.5 Platform Lifecycle

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl platform version` | Show all component versions | `GET /v1/admin/platform/version` | `platform-admin` |
| `lenny-ctl platform upgrade-check` | Check for available upgrades | `GET /v1/admin/platform/upgrade-check` | `platform-admin` |
| `lenny-ctl platform upgrade preflight --version <v>` | Run upgrade preflight | `POST /v1/admin/platform/upgrade/preflight` | `platform-admin` |
| `lenny-ctl platform upgrade start --version <v>` | Begin platform upgrade | `POST /v1/admin/platform/upgrade/start` | `platform-admin` |
| `lenny-ctl platform upgrade proceed` | Advance to next phase | `POST /v1/admin/platform/upgrade/proceed` | `platform-admin` |
| `lenny-ctl platform upgrade pause` | Pause upgrade | `POST /v1/admin/platform/upgrade/pause` | `platform-admin` |
| `lenny-ctl platform upgrade rollback` | Rollback upgrade | `POST /v1/admin/platform/upgrade/rollback` | `platform-admin` |
| `lenny-ctl platform upgrade status` | Show upgrade progress | `GET /v1/admin/platform/upgrade/status` | `platform-admin` |
| `lenny-ctl platform upgrade verify` | Post-upgrade verification | `POST /v1/admin/platform/upgrade/verify` | `platform-admin` |
| `lenny-ctl platform config` | Show effective running config | `GET /v1/admin/platform/config` | `platform-admin` |
| `lenny-ctl platform config diff --desired <file>` | Compare running vs. desired config | `GET /v1/admin/platform/config/diff` | `platform-admin` |

### 25.12.6 Backup and Restore

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl backup create [--components postgres,objectStore] [--label <label>]` | Trigger a backup | `POST /v1/admin/backups` | `platform-admin` |
| `lenny-ctl backup list` | List available backups | `GET /v1/admin/backups` | `platform-admin` |
| `lenny-ctl backup show <id>` | Show backup details | `GET /v1/admin/backups/{id}` | `platform-admin` |
| `lenny-ctl backup verify <id>` | Verify a backup | `POST /v1/admin/backups/{id}/verify` | `platform-admin` |
| `lenny-ctl backup schedule` | Show backup schedule | `GET /v1/admin/backups/schedule` | `platform-admin` |
| `lenny-ctl restore preview --backup <id>` | Preview restore impact | `POST /v1/admin/restore/preview` | `platform-admin` |
| `lenny-ctl restore execute --backup <id> --confirm` | Execute a restore | `POST /v1/admin/restore/execute` | `platform-admin` |

### 25.12.7 Drift and Recommendations

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl drift report` | Show configuration drift | `GET /v1/admin/drift` | `platform-admin` |
| `lenny-ctl drift reconcile [--scope all\|<type>/<name>] [--dry-run]` | Reconcile drifted resources | `POST /v1/admin/drift/reconcile` | `platform-admin` |
| `lenny-ctl recommendations` | Show capacity recommendations | `GET /v1/admin/recommendations` | `platform-admin` |
| `lenny-ctl recommendations --category <cat>` | Filter by category | `GET /v1/admin/recommendations?category=<cat>` | `platform-admin` |

### 25.12.8 Audit

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl audit query [--since <t>] [--until <t>] [--event-type <type>] [--actor <id>]` | Query audit events | `GET /v1/admin/audit-events` | `platform-admin` |
| `lenny-ctl audit summary [--since <t>] [--group-by eventType\|actorId]` | Audit event summary | `GET /v1/admin/audit-events/summary` | `platform-admin` |

---

## Cross-References to Existing Sections

When this addendum is integrated into `technical-design.md`, the following cross-references should be added:

1. **Section 2 (Goals)** — add goal: "Enable autonomous operation by AI DevOps agents through a complete operational API surface"
2. **Section 15.1 (REST API)** — add the new admin API endpoints from Sections 25.2-25.10 to the admin API table
3. **Section 16.5 (Alerting)** — reference the event stream (25.3) as the programmatic alert delivery mechanism
4. **Section 17.7 (Runbooks)** — reference the machine-executable runbook format (25.5) and note that each `.md` runbook has a corresponding `.runbook.yaml`
5. **Section 21 (Planned/Post-V1)** — add "25.11 MCP Management Server" as a v1 delivery if agent operability is a v1 goal, or post-v1 otherwise
6. **Section 23.1 (Why Lenny?)** — add differentiator: "First agent platform natively operable by AI agents via MCP"
7. **Section 24 (lenny-ctl)** — reference Section 25.12 for the additional command groups
