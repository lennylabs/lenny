---
layout: default
title: "elicitation-backlog"
parent: "Runbooks"
triggers:
  - alert: ElicitationBacklogHigh
    severity: warning
components:
  - gateway
symptoms:
  - "pending elicitations elevated above configured threshold"
  - "MCP clients slow to respond to elicitation prompts"
  - "session stalls waiting for human input"
tags:
  - elicitation
  - mcp
  - human-in-the-loop
requires:
  - admin-api
related:
  - gateway-capacity
---

# elicitation-backlog

The number of pending elicitation prompts (MCP `elicitation/create` requests awaiting client response) has exceeded `elicitation.alertThreshold`. Sessions are blocked waiting for human input; the backlog indicates the human side (client UI or operator) isn't keeping up.

## Trigger

- `ElicitationBacklogHigh` — pending elicitations elevated above `elicitation.alertThreshold`.
- Clients report stalled sessions with status `waiting_for_elicitation`.

Threshold and evaluation window are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Distribution by tenant and client

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_elicitation_pending&groupBy=tenant_id&window=15m
GET /v1/admin/metrics?q=lenny_elicitation_pending&groupBy=client_id&window=15m
```

If one tenant or client dominates, it's a client-side issue (UI bug, human bottleneck). If distributed, it's a broader traffic pattern.

### Step 2 — Elicitation age

<!-- access: api method=GET path=/v1/admin/elicitations -->
```
GET /v1/admin/elicitations?state=pending&ageSeconds=gt:<elicitation.ageThreshold>
```

Elicitations older than the configured age threshold are candidates for auto-timeout.

### Step 3 — Client responsiveness

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.50, rate(lenny_elicitation_response_latency_seconds_bucket[5m]))&groupBy=client_id&window=30m
```

Median response latency by client tells you which clients are slow.

## Remediation

### Step 1 — Client-side backlog

If a specific client's users are not responding:

1. Contact the client operator — this is usually a UI or notification bug on their side.
2. If the client has a default auto-timeout, confirm it's configured sensibly:
   <!-- access: lenny-ctl -->
   ```bash
   lenny-ctl admin elicitations defaults get
   ```

### Step 2 — Age-out stale elicitations

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin elicitations cancel --filter 'ageSeconds>$(lenny-ctl admin config get elicitation.timeoutThreshold --seconds)' --reason client_timeout
```

Marks aged-out elicitations as timed out so dependent sessions can fail fast or retry.

### Step 3 — Surge protection

If the backlog is a legitimate traffic spike:

- Raise `elicitation.alertThreshold` temporarily via Helm while capacity planning catches up.
- Check gateway capacity — elicitation pressure sometimes correlates with overall gateway load ([gateway-capacity](gateway-capacity.html)).

### Step 4 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose elicitations
```

- Pending count returns to baseline.
- Median elicitation age back within baseline.
- Alert clears.

## Escalation

Escalate to:

- **Client product owner** for specific client IDs with sustained backlog — this is usually their UI, not the platform.
- **Tenant operator** for tenants whose users routinely don't respond to elicitations; may indicate a workflow redesign is needed.
