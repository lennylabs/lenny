---
layout: default
title: "agent-to-llm-partition"
parent: "Runbooks"
triggers:
  - alert: AgentLLMProviderPartition
    severity: warning
components:
  - platform
symptoms:
  - "agent pod cannot reach the LLM provider via the §4.9 proxy"
tags:
  - chaos
related: []
---

# agent-to-llm-partition

agent pod cannot reach the LLM provider via the §4.9 proxy.

## Trigger

The `AgentLLMProviderPartition` alert fires when the §16.5 condition documented in the alert rule holds for its `for:` window. See [Metrics Reference §Alert rules](../reference/metrics.html#alert-rules) for the exact PromQL.

## Diagnosis

### Step 1 — Read the firing alert and its labels

<!-- access: api method=GET path=/v1/admin/events -->
```
GET /v1/admin/events?type=dev.lenny.alert_fired
```

<!-- access: lenny-ctl -->
```bash
lenny-ctl events list --type alert_fired
```

Identify the firing alert and read its labels for the affected component and tenant.

### Step 2 — Correlate the gateway and component logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -n lenny-system deployment/lenny-gateway --since=15m
```

Correlate the gateway and component logs for the same time window, and check the §16.5 dashboards for upstream and downstream signals.

### Step 3 — Confirm the component health row

<!-- access: api method=GET path=/v1/admin/health -->
```
GET /v1/admin/health
```

Confirm which §16.5 health component the alert maps to and whether it reports `degraded` or `unhealthy`.

## Remediation

Apply the documented remediation for the named alert. The chaos test mapped to this runbook in `tests/tier8_chaos/runbook-map.yaml` records the failure mode and the recovery path the platform exercises. If the alert persists after the documented remediation, escalate per the Escalation section.

## Verification

The named alert returns to the firing-clear state and the affected component's health-API row reports `healthy` again.

## Escalation

Page the on-call platform engineer when the alert remains firing after one cycle of the documented remediation.
