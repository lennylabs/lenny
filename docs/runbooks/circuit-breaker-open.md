---
layout: default
title: "circuit-breaker-open"
parent: "Runbooks"
triggers:
  - alert: GatewaySubsystemCircuitOpen
    severity: warning
  - alert: CircuitBreakerActive
    severity: warning
components:
  - gateway
symptoms:
  - "gateway subsystem circuit in open state"
  - "downstream dependency failing consistently"
  - "upstream returns fail-fast responses"
tags:
  - circuit-breaker
  - resilience
  - gateway
requires:
  - admin-api
  - cluster-access
related:
  - token-service-outage
  - postgres-failover
  - redis-failure
---

# circuit-breaker-open

A gateway circuit breaker has opened. The gateway has tripped the breaker to prevent retry storms against an unhealthy downstream dependency; requests that would hit that dependency now fail fast.

## Trigger

- `GatewaySubsystemCircuitOpen` — any subsystem circuit sustained in the open state past the configured sustain window.
- `CircuitBreakerActive` — any global breaker sustained in the open state past the configured sustain window.
- Client reports: subset of endpoints returning 503 instantly.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Which breaker?

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_circuit_state&groupBy=subsystem&window=15m
```

Values: 0 = closed, 1 = half-open, 2 = open. Identify which subsystem tripped: `tokenService`, `postgres`, `redis`, `objectStore`, `llmUpstream`.

### Step 2 — Follow the breaker to the underlying runbook

| Subsystem | Underlying runbook |
|:----------|:-------------------|
| `tokenService` | [token-service-outage](token-service-outage.html) |
| `postgres` | [postgres-failover](postgres-failover.html) |
| `redis` | [redis-failure](redis-failure.html) |
| `objectStore` | [minio-failure](minio-failure.html) |
| `llmUpstream` | [llm-egress-anomaly](llm-egress-anomaly.html) |

The breaker is a symptom — the cause is downstream.

### Step 3 — Half-open probe results

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_circuit_probe_success_total&groupBy=subsystem&window=15m
GET /v1/admin/metrics?q=lenny_gateway_circuit_probe_failure_total&groupBy=subsystem&window=15m
```

The gateway periodically sends a probe while open. Sustained probe failures confirm the underlying dependency is still unhealthy.

## Remediation

### Step 1 — Fix the underlying cause

Follow the subsystem-specific runbook from Step 2 above.

### Step 2 — Do NOT force-close

The breaker is automatic: it half-opens on a schedule and closes after consecutive successful probes. Forcing it closed while the downstream is unhealthy will trigger the retry storm the breaker is designed to prevent.

### Step 3 — Verify auto-recovery

After the underlying subsystem is healthy:

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_circuit_state&groupBy=subsystem&window=5m
```

Expect sequence: `2` (open) → `1` (half-open) → `0` (closed) within a short window after the subsystem recovers (governed by the breaker's configured probe cadence).

### Step 4 — Verify blast-radius

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose gateway-circuits
```

- All circuits report `0` (closed).
- No lingering `5xx` elevation on the subsystem.

## Escalation

Escalate if:

- The underlying subsystem appears healthy but the breaker stays open well past its configured sustain window — investigate probe configuration with platform engineering.
- The breaker flaps (open → closed → open) — underlying dependency is intermittent; escalate per its runbook.
- Multiple breakers open simultaneously — may indicate network partition or DNS issue; see [dns-outage](dns-outage.html).
