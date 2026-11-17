---
code: OPSAPI
type: index
summary: >-
  The ops API served by the gateway and lenny-ops: terminology, envelopes, pagination, rate limits, and the operation groups.
status: placeholder
---

# Ops API

The ops API served by the gateway and lenny-ops: terminology, envelopes, pagination, rate limits, and the operation groups.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Audit and drift](audit-and-drift.md) | `OPSAUD` | Audit query and configuration drift operations. |
| [Backup and upgrades](backup-and-upgrades.md) | `OPSLIFE` | Backup, restore, version, upgrade, and configuration apply operations. |
| [Caller identity](caller-identity.md) | `OPSME` | The caller identity and capability discovery operations. |
| [Events and webhooks](events-and-webhooks.md) | `OPSHOOK` | Operational event stream, polling, buffer, and webhook subscription operations. |
| [Health and diagnostics](health-and-diagnostics.md) | `OPSHEALTH` | Health, recommendation, diagnostic, and runbook operations. |
| [MCP management](mcp-management.md) | `OPSMCP` | The management MCP server of lenny-ops: tools, schemas, capability negotiation, and headers. |
| [Remediation and operations](remediation-and-operations.md) | `OPSREM` | Remediation lock, escalation, and operations inventory operations. |

<!-- /generated:index -->

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§15.1 REST API › Session Replay Semantics](../../15_external-api-surface.md#session-replay-semantics), in part
- [§25.2 Architecture Overview › API Conventions](../../25_agent-operability.md#api-conventions)
- [§25.2 Architecture Overview › API Conventions › Terminology](../../25_agent-operability.md#terminology)
- [§25.4 The `lenny-ops` Service › Rate Limiting](../../25_agent-operability.md#rate-limiting)
- [§25.4 The `lenny-ops` Service › Idempotency › Error Codes](../../25_agent-operability.md#idempotency)
