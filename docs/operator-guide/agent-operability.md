---
layout: default
title: "Agent Operability"
parent: "Operator Guide"
nav_order: 12
description: The lenny-ops management plane, the scope vocabulary agent callers use to narrow their authority, the MCP tool catalog served at /mcp/management, and the safety envelope every mutating call passes through.
---

# Agent Operability

Lenny is designed to be operable by AI agents as well as humans. Human on-call and AI on-call use the same endpoints; neither has to parse `kubectl` output. This page is the on-call entry point for both.

---

## The operational loop

Every operator runs the same six-step loop, whether human or agent:

1. **Observe** -- platform health via structured endpoints, not raw metric scraping.
2. **Detect** -- real-time notification of operational events.
3. **Diagnose** -- structured causality chains, not log parsing.
4. **Decide** -- actionable recommendations.
5. **Act** -- API-encapsulated remediation, not `kubectl` or `psql`.
6. **Verify** -- confirmation that the action had the intended effect.

Every step in every runbook can be performed by an API call, and every signal on a dashboard is available as structured data.

---

## Entry points

| Concern | Start here |
|---|---|
| Pager received, incident triage | [Troubleshooting](troubleshooting) |
| Metrics, alerts, dashboards | [Observability](observability) |
| `lenny-ctl doctor`, `admin diagnostics` | [`lenny-ctl` reference](lenny-ctl) |
| Recover from outage | [Disaster recovery](disaster-recovery) |
| Credential revocation, circuit breakers, orphan reconciliation | [Emergency procedures](troubleshooting#emergency-procedures) |
| Alert catalog | [Metrics reference](../reference/metrics) |
| Operational event types | [CloudEvents catalog](../reference/cloudevents-catalog) |
| State machines | [State-machine reference](../reference/state-machines) |
| Error codes | [Error catalog](../reference/error-catalog) |

---

## `lenny-ops`: the management plane

`lenny-ops` is a dedicated management plane that ships with every Lenny installation. It runs as a separate Deployment from the gateway and survives gateway failures. Agents reach it through an Ingress that accepts only external traffic -- no internal cluster workload (including Lenny's own agent pods) can reach the operational control plane, which eliminates an entire class of lateral-movement attacks.

### The surface `lenny-ops` exposes

| Surface | What it gives you | Canonical endpoint |
|---|---|---|
| Diagnostic endpoints | Structured platform health, scoped by subsystem | `/v1/admin/diagnostics/*` |
| Operational event stream | CloudEvents-compatible feed of platform events | `/v1/admin/events/stream` (SSE) |
| [Runbook catalog]({{ site.baseurl }}/runbooks/) | Machine-parseable runbooks bound to alerts | `/v1/admin/runbooks` |
| Audit log query API | OCSF-formatted, hash-chain verified | `/v1/admin/audit-events` |
| Drift detection | Deviation from declared configuration | `/v1/admin/drift` |
| Backup and restore API | Platform-lifecycle operations without direct cluster access | `/v1/admin/backups`, `/v1/admin/restore/*` |
| Platform upgrade state machine | Drive multi-phase upgrades end-to-end | `/v1/admin/platform/upgrade/*` |
| MCP management server | Exposes every admin tool via MCP | `/mcp/management` |
| Remediation locks | Coordinate concurrent agents | `/v1/admin/remediation-locks` |
| Escalations | Raise a signal to a human | `/v1/admin/escalations` |
| Caller introspection | What can *I* do right now? | `/v1/admin/me`, `/v1/admin/me/authorized-tools` |

All agent-initiated operations produce the same audit trail as human-initiated ones. The caller's `caller_type: "agent"` JWT claim distinguishes agent from human actors in audit events and metrics.

---

## Authentication for agent callers

Agent callers authenticate via the same OIDC-based mechanism as human operators and `lenny-ctl`. Deployers create dedicated service accounts with the `platform-admin` or `tenant-admin` role, then **narrow privileges further via OAuth 2.0 scopes** minted through the canonical token-exchange endpoint (`POST /v1/oauth/token`, RFC 8693).

The role sets the ceiling; scopes restrict the surface *below* that ceiling. Scopes never elevate.

---

## The scope vocabulary

Every scope is of the form `tools:<domain>:<action>`.

### Domains

Each domain corresponds to a tool family:

| Domain | What it covers |
|---|---|
| `health` | Platform aggregate health, component deep-dives, summary |
| `diagnostics` | Session / pool / connectivity / credential-pool diagnosis |
| `recommendations` | Capacity recommendations |
| `runbooks` | Runbook listing and retrieval |
| `events` | Operational event stream, event subscriptions |
| `audit` | Audit log query, summary, hash-chain verify |
| `drift` | Drift report, validate, snapshot refresh, reconcile |
| `backup` | Backup listing, creation, verification, schedule, policy |
| `restore` | Restore preview, safety-check, execute, status, resume |
| `upgrade` | Platform upgrade state machine |
| `locks` | Remediation-lock acquire / release / steal |
| `escalation` | Raise and resolve escalations |
| `logs` | Pod log retrieval through the ops service |
| `me` | Caller identity and authorized-tools introspection |
| `operations` | Operations inventory (in-flight operations across subsystems) |
| `pool` | Pool CRUD, scaling, drain, upgrade |
| `tenant` | Tenant lifecycle |
| `credential_pool` | Credential pool CRUD |
| `credential` | Individual credential add / rotate / retire / revoke |
| `runtime` | Runtime registration |
| `quota` | Per-tenant quota management |
| `config` | Effective running-config reads and applied-config writes |

### Actions

| Action | Meaning |
|---|---|
| `read` | Any listing, get, summary, or other non-mutating call in the domain |
| `write` | Any mutating call in the domain |
| `<tool>` | A specific action name (e.g., `scale`, `rotate`, `steal`, `create`) |
| `*` | Every action in the domain |

`tools:*` is equivalent to no `scope` claim (the role ceiling applies unrestricted).

### Matching rules

- A scope matches a tool if the scope's domain equals the tool's domain AND the scope's action equals the tool's action, OR the scope's action is `*`.
- Multiple space-separated scopes are OR-combined; a tool is permitted if **any** scope matches.
- Absent `scope` claim: no scope restriction — the role ceiling applies unmodified.
- A request for a tool not permitted by any scope returns `403 SCOPE_FORBIDDEN`, listing the caller's active scopes and the required scope.

### Canonical policies

Reference scope policies for common agent roles:

| Policy | Scope string |
|---|---|
| **Watchdog** (observe + escalate, cannot mutate) | `tools:health:* tools:diagnostics:* tools:recommendations:read tools:operations:read tools:me:* tools:escalation:create tools:runbooks:read tools:events:read` |
| **Pool scaling bot** (pool domain only) | `tools:health:read tools:pool:* tools:me:* tools:operations:read` |
| **Upgrade orchestrator** | `tools:upgrade:* tools:backup:* tools:restore:* tools:health:read tools:operations:read tools:me:*` |
| **Fully-privileged admin agent** | (no `scope` claim, or `tools:*`) |

### Where enforcement happens

Scopes are checked in three independent places:

1. **Admin API middleware** — every endpoint is mapped to its canonical scope via the `x-lenny-scope` OpenAPI extension. The middleware checks the scope before routing to the handler.
2. **MCP tool invocation** — `/mcp/management` `tools/call` checks the scope before dispatch. On mismatch, the adapter returns MCP error `-32001` with `data.code: "SCOPE_FORBIDDEN"`.
3. **`/v1/admin/me/authorized-tools`** — pre-filters the tool list to what the caller's scopes permit. Agents should call this on startup to avoid attempting forbidden tools.

Scopes don't replace tenancy — a `tenant-admin` caller is still constrained to its tenant regardless of scope. Scopes restrict *actions*; tenancy restricts *resources*.

### Issuing a scoped token

Operators mint scope-narrowed tokens by calling `POST /v1/oauth/token` with an RFC 8693 token-exchange grant. The Token Service enforces that every value in the requested scope is present in the `subject_token.scope` (narrowing is monotonic; broadening is rejected with `invalid_scope`).

```bash
curl -X POST https://lenny.example.com/v1/oauth/token \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange \
  -d subject_token="${ADMIN_TOKEN}" \
  -d subject_token_type=urn:ietf:params:oauth:token-type:access_token \
  -d scope="tools:health:* tools:diagnostics:* tools:escalation:create"
```

This replaces any earlier ad-hoc "mint a narrowed token" mechanism.

---

## MCP management catalog

Lenny exposes its full admin and operability surface as an MCP tool server at `/mcp/management` on `lenny-ops`. Any MCP-capable agent can discover every tool, inspect its schema, and invoke it without REST-specific knowledge.

Tool schemas are auto-generated at build time from the canonical OpenAPI document (served at `/v1/openapi.yaml` on `lenny-ops`), ensuring the MCP inventory never drifts from the REST contract.

### Observation tools (read-only)

| Tool | Maps to | Purpose |
|---|---|---|
| `lenny_health_get` | `GET /v1/admin/health` | Aggregate platform health |
| `lenny_health_component` | `GET /v1/admin/health/{component}` | Component deep-dive |
| `lenny_health_summary` | `GET /v1/admin/health/summary` | Minimal health status |
| `lenny_ops_health_get` | `GET /v1/admin/ops/health` | `lenny-ops` self-health |
| `lenny_recommendations_get` | `GET /v1/admin/recommendations` | Capacity recommendations |
| `lenny_diagnostics_session` | `GET /v1/admin/diagnostics/sessions/{id}` | Diagnose a session |
| `lenny_diagnostics_pool` | `GET /v1/admin/diagnostics/pools/{name}` | Diagnose a pool |
| `lenny_diagnostics_connectivity` | `GET /v1/admin/diagnostics/connectivity` | Dependency connectivity |
| `lenny_diagnostics_credential_pool` | `GET /v1/admin/diagnostics/credential-pools/{name}` | Diagnose a credential pool |
| `lenny_events_list` | `GET /v1/admin/events` | Poll operational events |
| `lenny_runbooks_list` | `GET /v1/admin/runbooks` | Structured runbook index |
| `lenny_runbooks_get` | `GET /v1/admin/runbooks/{name}` | Full runbook content |
| `lenny_audit_query` | `GET /v1/admin/audit-events` | Query audit log |
| `lenny_drift_report` | `GET /v1/admin/drift` | Drift report |
| `lenny_version_full` | `GET /v1/admin/platform/version/full` | Full platform version |
| `lenny_upgrade_check` | `GET /v1/admin/platform/upgrade-check` | Check for available upgrades |
| `lenny_upgrade_status` | `GET /v1/admin/platform/upgrade/status` | Current upgrade state |
| `lenny_backups_list` | `GET /v1/admin/backups` | List backups |
| `lenny_restore_safety_check` | `GET /v1/admin/restore/safety-check` | Estimated data loss pre-restore |
| `lenny_restore_status` | `GET /v1/admin/restore/{id}/status` | Per-shard restore status |
| `lenny_locks_list` | `GET /v1/admin/remediation-locks` | Active remediation locks |
| `lenny_logs_pod` | `GET /v1/admin/logs/pods/{ns}/{name}` | Pod container logs |
| `lenny_me_get` | `GET /v1/admin/me` | Caller identity, scopes, rate limits |
| `lenny_me_authorized_tools` | `GET /v1/admin/me/authorized-tools` | Pre-filtered tool inventory |
| `lenny_me_operations` | `GET /v1/admin/me/operations` | Caller's in-flight operations |
| `lenny_operations_list` | `GET /v1/admin/operations` | Unified operations inventory |
| `lenny_operation_get` | `GET /v1/admin/operations/{id}` | Single operation detail |
| `lenny_tenant_list` | `GET /v1/admin/tenants` | List tenants |
| `lenny_tenant_get` | `GET /v1/admin/tenants/{id}` | Tenant detail |
| `lenny_pool_list` | `GET /v1/admin/pools` | List warm pools |
| `lenny_pool_get` | `GET /v1/admin/pools/{name}` | Pool detail |
| `lenny_credential_pool_list` | `GET /v1/admin/credential-pools` | List credential pools |
| `lenny_credential_pool_get` | `GET /v1/admin/credential-pools/{name}` | Credential pool detail |
| `lenny_runtime_list` | `GET /v1/admin/runtimes` | List runtimes |
| `lenny_runtime_get` | `GET /v1/admin/runtimes/{name}` | Runtime detail |
| `lenny_quota_get` | `GET /v1/admin/tenants/{id}` | Tenant quota (quota fields are embedded in the tenant record) |

### Action tools (mutating)

Every action tool respects the [safety envelope](#the-safety-envelope) below.

| Tool | Maps to | Effect |
|---|---|---|
| `lenny_pool_scale` | `PUT /v1/admin/pools/{name}/warm-count` | Scale warm pool |
| `lenny_pool_create` | `POST /v1/admin/pools` | Create a pool |
| `lenny_pool_update` | `PUT /v1/admin/pools/{name}` | Update a pool |
| `lenny_pool_delete` | `DELETE /v1/admin/pools/{name}` | Delete a pool (destructive; requires `confirm`) |
| `lenny_drift_reconcile` | `POST /v1/admin/drift/reconcile` | Reconcile drifted resources |
| `lenny_drift_validate` | `POST /v1/admin/drift/validate` | Validate desired state |
| `lenny_drift_snapshot_refresh` | `POST /v1/admin/drift/snapshot/refresh` | Replace stored snapshot |
| `lenny_upgrade_preflight` | `POST /v1/admin/platform/upgrade/preflight` | Validate upgrade safety |
| `lenny_upgrade_start` | `POST /v1/admin/platform/upgrade/start` | Begin upgrade |
| `lenny_upgrade_proceed` | `POST /v1/admin/platform/upgrade/proceed` | Advance upgrade phase |
| `lenny_upgrade_pause` | `POST /v1/admin/platform/upgrade/pause` | Pause upgrade |
| `lenny_upgrade_rollback` | `POST /v1/admin/platform/upgrade/rollback` | Roll back upgrade |
| `lenny_backup_create` | `POST /v1/admin/backups` | Trigger a backup |
| `lenny_backup_verify` | `POST /v1/admin/backups/{id}/verify` | Verify backup integrity |
| `lenny_restore_preview` | `POST /v1/admin/restore/preview` | Preview restore impact |
| `lenny_restore_execute` | `POST /v1/admin/restore/execute` | Execute restore (requires `confirm` and `acknowledgeDataLoss`) |
| `lenny_restore_resume` | `POST /v1/admin/restore/resume` | Resume partial restore |
| `lenny_lock_acquire` | `POST /v1/admin/remediation-locks` | Acquire a lock |
| `lenny_lock_extend` | `PATCH /v1/admin/remediation-locks/{id}` | Extend lock TTL |
| `lenny_lock_release` | `DELETE /v1/admin/remediation-locks/{id}` | Release a lock |
| `lenny_lock_steal` | `POST /v1/admin/remediation-locks/{id}/steal` | Steal a lock (audited) |
| `lenny_escalation_create` | `POST /v1/admin/escalations` | Raise an escalation |
| `lenny_config_apply` | `PUT /v1/admin/platform/config` | Apply a config change |
| `lenny_tenant_create` | `POST /v1/admin/tenants` | Provision a tenant |
| `lenny_tenant_update` | `PUT /v1/admin/tenants/{id}` | Update a tenant |
| `lenny_tenant_suspend` | `POST /v1/admin/tenants/{id}/suspend` | Suspend a tenant |
| `lenny_tenant_resume` | `POST /v1/admin/tenants/{id}/resume` | Resume a tenant |
| `lenny_tenant_delete` | `DELETE /v1/admin/tenants/{id}` | Delete a tenant (destructive; requires `confirm`) |
| `lenny_credential_pool_create` | `POST /v1/admin/credential-pools` | Create a credential pool |
| `lenny_credential_pool_update` | `PUT /v1/admin/credential-pools/{name}` | Update a credential pool |
| `lenny_credential_pool_delete` | `DELETE /v1/admin/credential-pools/{name}` | Delete a credential pool (destructive) |
| `lenny_credential_add` | `POST /v1/admin/credential-pools/{name}/credentials` | Add a credential |
| `lenny_credential_retire` | `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | Retire (revoke) a pool credential. Pool credential rotation is performed via revoke + add. |
| `lenny_runtime_register` | `POST /v1/admin/runtimes` | Register a runtime |
| `lenny_runtime_update` | `PUT /v1/admin/runtimes/{name}` | Update a runtime |
| `lenny_runtime_retire` | `DELETE /v1/admin/runtimes/{name}` | Retire a runtime |
| `lenny_quota_update` | `PUT /v1/admin/tenants/{id}` | Update a tenant's quota (quota fields are part of the tenant record payload) |

The table above is representative, not exhaustive. Every admin-API endpoint with documented RBAC becomes an MCP tool automatically via the build-time OpenAPI → MCP generation. The authoritative caller-specific list is always `/v1/admin/me/authorized-tools`.

### Tool schema example

Each tool defines a JSON Schema `inputSchema`:

```json
{
  "name": "lenny_pool_scale",
  "description": "Scale a warm pool's minimum warm count. Requires confirm:true for changes >50% of current value.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "pool":    { "type": "string",  "description": "Pool name" },
      "minWarm": { "type": "integer", "minimum": 0, "description": "New minimum warm pod count" },
      "confirm": { "type": "boolean", "description": "Required for large changes. Omit for dry-run preview." }
    },
    "required": ["pool", "minWarm"]
  }
}
```

---

## The safety envelope

Every mutating endpoint — via REST, `lenny-ctl`, or MCP — passes through the same envelope. Agents that respect the envelope are safe to let loose; agents that bypass it are unsupported.

### Idempotency

Agents may send `Idempotency-Key: <uuid>` on any mutating call. Retries that use the same key within the configured TTL (default: 24h, 7 days for long-running operations) return the same result without re-executing.

- Use a **fresh** key for each logical operation.
- Re-use the **same** key for retries of a transient failure.
- Destructive operations enforce idempotency keys — the server rejects retries without the header to avoid accidental double-execution.

### Dry-run first

Destructive or high-impact endpoints follow the `confirm-bool` pattern: a request body **without** `"confirm": true` returns `200 OK` with `"dryRun": true` and a structured preview. Only a request **with** `"confirm": true` executes.

Over MCP, this surfaces as a textual preview in the tool response plus `dryRun: true` in the structured result.

Destructive endpoints that carry irreversible data loss (restore, partition drop, force-delete) require an additional explicit `"acknowledgeDataLoss": true`.

### Operation correlation

Agents may send `X-Lenny-Operation-ID: <uuid>` to tie multiple API calls into one logical remediation. Every audit event the server emits during the operation carries that ID, enabling post-incident "what did the agent do?" reconstruction.

An optional `X-Lenny-Agent-Name: <string>` identifies the agent instance (e.g., `prod-watchdog-us-east-1`) for audit and metric labels.

### Remediation locks

Mutating operations acquire a scoped lock at the durable tier before proceeding. Concurrent conflicting operations are rejected until the lock releases or expires.

- **Acquire** the lock via `POST /v1/admin/remediation-locks` with `{scope, operation, ttlSeconds}`.
- **Extend** via `PATCH /v1/admin/remediation-locks/{id}` (limited to the lock's max TTL).
- **Release** via `DELETE /v1/admin/remediation-locks/{id}`.
- **Steal** via `POST /v1/admin/remediation-locks/{id}/steal` — audited, requires justification. Split-brain (stolen while original holder was still live) is detected by `lenny_ops_lock_split_brain_detected_total`.

Most agents don't need to touch locks directly — mutating endpoints take and release them automatically around the call.

### Audit trail

Every agent action is logged in the OCSF-formatted audit stream with:
- Caller identity (`sub`, `roles`, `caller_type: "agent"`, `scope`)
- `X-Lenny-Agent-Name`
- `X-Lenny-Operation-ID`
- The canonical operation that fired
- The before/after state for applicable operations

Agents cannot suppress audit. An operation that succeeds but fails to audit is treated as failed and rolled back.

---

## The operational event stream

`lenny-ops` serves a CloudEvents v1.0.2 stream at `GET /v1/admin/events/stream` (SSE). It carries every operational event — alerts firing/resolving, pool state transitions, credential events, backup completion, drift detection, restore progress, lock split-brain, escalations, and more.

The full catalog of event `type` values and `data` payload shapes lives in [CloudEvents Catalog](../reference/cloudevents-catalog). Key design points:

- **Single envelope.** Every event uses the CloudEvents v1.0.2 envelope; nothing is double-wrapped. Audit-bearing events set `datacontenttype: "application/ocsf+json"` and carry an OCSF record in `data`.
- **Deduplication.** CloudEvents `id` values are globally unique. Consumers deduplicate by `id` to tolerate reconnect-with-cursor replays.
- **Filter prefix.** All Lenny events use `dev.lenny.` reverse-DNS prefix; consumers sharing a transport can filter to this prefix.
- **Degraded-mode survival.** When Redis is down, the stream falls back to the gateway's in-memory ring buffer. Events are still delivered; depth is bounded.

Webhook subscriptions (`POST /v1/admin/event-subscriptions`) receive the same CloudEvents envelopes with HMAC-SHA256 signatures — see the [webhooks guide](../client-guide/webhooks).

---

## Failure modes in brief

The operability surface is designed to stay up precisely when it's most needed.

| Failure | Impact |
|---|---|
| Gateway down, `lenny-ops` up | Ops surface remains fully available. Tools whose REST backend is the gateway return `ENDPOINT_UNAVAILABLE` (retryable). Diagnostics, runbook index, audit, drift, backup/restore, lock coordination, escalation — all work. |
| `lenny-ops` down, gateway up | Client traffic unaffected. Ops tooling unavailable. Watchdog detects via Ingress health check. |
| Postgres down | Audit queries, backup management, upgrade state machine unavailable. Diagnostics degrade to K8s-API data. Drift detection works when caller supplies desired state. Locks and escalations fall back to Redis / in-memory. |
| Redis down | Event stream falls back to gateway ring buffer. Lock coordination falls back to Postgres. |
| Postgres + Redis both down | Core loop still functions in degraded mode: diagnostics via K8s API, locks in-memory (single-replica), escalations in-memory, event stream from gateway buffer. Unavailable: audit, backup/upgrade state machines. |
| `lenny-ops` + gateway both down | Total outage. Manual escape hatches: `kubectl port-forward -n lenny-system svc/lenny-ops 8090` (bypasses Ingress and NetworkPolicy). Full procedure in the [total-outage runbook]({{ site.baseurl }}/runbooks/total-outage.html). |

---

## Related pages

- [Troubleshooting](troubleshooting)
- [Observability](observability)
- [`lenny-ctl` reference](lenny-ctl)
- [Disaster recovery](disaster-recovery)
- [Admin API](../api/admin)
- [MCP API](../api/mcp)
- [CloudEvents Catalog](../reference/cloudevents-catalog)
- [Glossary](../reference/glossary)
