---
layout: default
title: CloudEvents Catalog
parent: "Reference"
nav_order: 6
---

# CloudEvents Catalog

Every event Lenny emits — over EventBus (Redis pub/sub), the agent-operability SSE stream, webhook subscriptions, and `callbackUrl` session webhooks — is a [CloudEvents v1.0.2](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) JSON record. A single envelope style covers all event transports; the `data` field carries the event-specific payload.

This page is the reference catalog of event `type` values and their `data` schemas.

---

## Envelope attributes

Every event carries these CloudEvents attributes:

| Attribute | Value |
|---|---|
| `specversion` | `"1.0"` |
| `id` | Globally unique `{tenantId}:{publisherId}:{nanoTimestamp}:{nonce}`. Use as idempotency key. |
| `source` | `//lenny.dev/gateway/{replicaId}` or `//lenny.dev/ops/{replicaId}` |
| `type` | `dev.lenny.<short_name>` (see catalog below) |
| `time` | RFC 3339 UTC timestamp |
| `datacontenttype` | `"application/json"` for normal events; `"application/ocsf+json"` for audit-bearing events |
| `subject` | Domain subject: `session/{sessionId}`, `tree/{rootSessionId}`, `pool/{poolName}` |
| `lennytenantid` | Tenant ID (extension) |
| `lennyoperationid` | Operation correlation ID (extension, optional) |
| `lennyrootsessionid` | Delegation-tree root (extension, optional) |

---

## Gateway-emitted events

### Alerts

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.alert_fired` | A Prometheus alerting rule fires | `alert`, `severity`, `labels`, `runbook`, `suggested_action` |
| `dev.lenny.alert_resolved` | A previously firing alert resolves | `alert`, `duration_seconds` |

### Pools and upgrades

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.pool_state_changed` | Pool enters draining / warming / exhausted | `pool`, `prev_state`, `new_state` |
| `dev.lenny.upgrade_progressed` | Pool upgrade state machine advances | `pool`, `prev_phase`, `new_phase`, `image_digest` |

### Circuit breakers

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.circuit_breaker_opened` | Circuit opened | `name`, `reason`, `opener` |
| `dev.lenny.circuit_breaker_closed` | Circuit closed | `name`, `closer` |

### Credentials

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.credential_rotated` | Credential lease rotated | `pool`, `credential_id`, `error_type` |
| `dev.lenny.credential_pool_exhausted` | Credential pool has no available credentials | `pool` |

### Sessions

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.session_completed` | Session terminal state = `completed` | `session_id`, `usage`, `artifacts` |
| `dev.lenny.session_failed` | Session terminal state = `failed` | `session_id`, `error.code`, `error.message`, `usage` |
| `dev.lenny.session_terminated` | Admin/system termination (external state = `completed`) | `session_id`, `reason`, `terminated_by` |
| `dev.lenny.session_cancelled` | User/runtime cancellation | `session_id`, `reason` |
| `dev.lenny.session_expired` | `maxSessionAge` or `maxIdleTimeSeconds` hit | `session_id`, `expiry_reason` |
| `dev.lenny.session_awaiting_action` | Session entered `awaiting_client_action` | `session_id`, `action_required`, `resume_url` |

### Delegation

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.delegation_completed` | Child session reaches terminal state | `parent_session_id`, `child_session_id`, `status`, `usage` |

### Backups and platform

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.backup_completed` | Backup Job finished | `kind`, `status`, `size_bytes`, `duration_seconds` |
| `dev.lenny.backup_failed` | Backup Job failed | `kind`, `error` |
| `dev.lenny.platform_upgrade_available` | New Lenny release detected | `current_version`, `available_version` |
| `dev.lenny.health_status_changed` | Aggregate health transitioned | `prev_status`, `new_status`, `triggering_component` |

---

## `lenny-ops`-emitted events

| `type` | `data` highlights |
|---|---|
| `dev.lenny.ops_health_status_changed` | `prev_status`, `new_status`, `triggering_check` |
| `dev.lenny.escalation_created` | `severity`, `source`, `alert_name`, `summary` |
| `dev.lenny.remediation_lock_acquired` | `scope`, `operation`, `agent_name` |
| `dev.lenny.remediation_lock_released` | `scope`, `release_reason` |
| `dev.lenny.remediation_lock_expired` | `scope` |
| `dev.lenny.remediation_lock_stolen` | `scope`, `stealer`, `previous_holder` |
| `dev.lenny.remediation_lock_split_brain_detected` | `scope`, `resolved_via_outage_epoch` |
| `dev.lenny.drift_detected` | `resource_type`, `resource_name`, `drifted_fields` |
| `dev.lenny.platform_upgrade_completed` | `version` |
| `dev.lenny.platform_upgrade_verification_failed` | `phase`, `error` |
| `dev.lenny.platform_upgrade_image_pull_failed` | `image`, `error` |
| `dev.lenny.restore_started` | `restore_id`, `source_backup_id` |
| `dev.lenny.restore_shard_completed` | `restore_id`, `shard` |
| `dev.lenny.restore_completed` | `restore_id` |
| `dev.lenny.restore_failed` | `restore_id`, `error` |
| `dev.lenny.event_delivery_failed` | `subscription_id`, `event_id`, `error` |
| `dev.lenny.prometheus_query_timeout` | `query`, `duration_ms` |
| `dev.lenny.lock_split_brain_detected` | `scope`, `resolution` |
| `dev.lenny.operation_progressed` | `operation_id`, `kind`, `prev_status`, `new_status`, `progress` |

---

## Sample event

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

---

## Audit-bearing events

When a CloudEvents envelope carries an audit record, `datacontenttype` is `application/ocsf+json` and `data` is an OCSF v1.1.0 record. See the [OCSF audit wire format guide](../operator-guide/audit-ocsf.md) for the full field mapping.

The single-envelope model applies: nothing is double-wrapped. CloudEvents is the transport; OCSF is the payload.

---

## Consumer guidance

### Idempotency

Deduplicate by CloudEvents `id`. Within a release, collisions are astronomically improbable (ULID-like `{replicaID}:{nanoTimestamp}:{nonce}` composition); across releases, deduplication still holds because `id` embeds the originating replica ID.

### Event type prefix

All Lenny-emitted events use the `dev.lenny.` reverse-DNS prefix. Consumers can filter to this prefix to receive only Lenny-originated events when sharing a transport with other producers.

### Transport-agnostic parsing

The same parser works for EventBus, SSE frames, webhook bodies, and `callbackUrl` deliveries. Parse the JSON as a CloudEvents envelope, extract `type`, and dispatch on the `data` schema for that type.

---

## Related

- [CloudEvents v1.0.2 specification](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md)
- [Webhooks guide](../client-guide/webhooks.md) — CloudEvents deliveries over HTTP.
- [Agent operability event stream](../operator-guide/observability.md) — SSE consumption.
- [OCSF audit wire format](../operator-guide/audit-ocsf.md) — audit `data` payload.
