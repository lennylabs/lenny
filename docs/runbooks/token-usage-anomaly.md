---
layout: default
title: "token-usage-anomaly"
parent: "Runbooks"
triggers:
  - alert: TokenUsageAnomaly
    severity: warning
components:
  - gateway
symptoms:
  - "lenny_gateway_token_usage_anomaly_total > 0"
  - "sustained non-zero anomaly rate for a direct-mode tenant"
  - "a direct-mode session reporting repeated zero-token ReportUsage deltas"
tags:
  - direct-mode
  - usage
  - integrity
  - budgets
requires:
  - admin-api
  - cluster-access
related:
  - delegation-budget-exhaustion
  - delegation-budget-recovery
---

# token-usage-anomaly

A direct-mode session is under-reporting token usage. In direct mode the agent pod egresses to the LLM provider directly and never traverses the gateway LLM proxy, so the runtime adapter is the only in-pod observer of provider token counts. The gateway pulls those counts over the `ReportUsage` RPC and cannot re-derive them. When the pulled deltas are implausibly low the gateway increments `lenny_gateway_token_usage_anomaly_total`, labeled by `tenant_id` and `reason`. A sustained non-zero rate is the accepted residual-risk signal that a runtime is under-reporting direct-mode usage.

Direct-mode over-run is not terminated in-path: the granted delegation budget is a soft cap reconciled at settlement against the parent's delegation budget. This alert is an integrity signal for review, and it does not by itself terminate or throttle a session.

## Trigger

- `TokenUsageAnomaly` — `sum by (tenant_id) (rate(lenny_gateway_token_usage_anomaly_total[5m])) > 0` sustained for more than 5 minutes.
- `reason="zero_delta"`: a direct-mode session returned more than the operator-tunable count of consecutive zero-token `ReportUsage` deltas.
- `reason="implausibly_small"`: a direct-mode session's token-per-call ratio fell below the operator-tunable implausibly-small threshold.

The counter is labeled only by `tenant_id` and `reason`. Per-session attribution is emitted in structured logs alongside each increment; `session_id` is intentionally excluded from the metric because it is a high-cardinality label.

## Diagnosis

### Step 1 — Identify the affected tenant and reason

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=sum by (tenant_id, reason) (rate(lenny_gateway_token_usage_anomaly_total[5m]))&window=30m
```

Note the `tenant_id` and which `reason` dominates. A `zero_delta` pattern points at a runtime that stopped populating `inputTokens`/`outputTokens` on the `llm_request_completed` frame. An `implausibly_small` pattern points at a runtime that reports counts far below the observed call frequency.

### Step 2 — Find the affected sessions

The metric excludes `session_id`, so locate the affected sessions from the structured logs the gateway emits alongside each increment.

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -n lenny-system deploy/lenny-gateway | grep token_usage_anomaly | grep <tenant-id>
```

Each log line carries `session_id`, `tenant_id`, `reason`, and the observed delta. Collect the affected session IDs.

### Step 3 — Inspect a session's delivery mode and runtime image

<!-- access: api method=GET path=/v1/admin/sessions -->
```
GET /v1/admin/sessions/<session-id>
```

Confirm the session is `deliveryMode: direct` (the alert only fires for direct-mode sessions) and record the runtime image reference. Proxy-mode sessions extract authoritative counts at the LLM proxy and do not produce this anomaly.

### Step 4 — Confirm the runtime is the source

A single misbehaving runtime image usually drives the anomaly across many sessions of one tenant. Group the affected sessions by runtime image; a shared image is the likely cause.

## Remediation

### Step 1 — Review the affected runtime image

The operator response for this alert is to review the affected runtime image. A runtime that cannot extract provider token counts omits `inputTokens`/`outputTokens` from the `llm_request_completed` frame, which the gateway observes as a zero-token delta. Confirm the image extracts token counts from provider responses and populates those fields.

- If the image is a known-good build that recently regressed, roll the tenant's pool back to the previous image.
- If the image is a third-party or tenant-supplied runtime, notify the tenant operator that direct-mode usage is not being reported and cannot be reconciled against the delegation budget.

### Step 2 — Confirm the reconciliation impact

Direct-mode over-run is bounded at settlement by delegation `budget_return.lua` reconciliation against the parent's delegation budget. Verify the affected sessions still settle correctly:

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_delegation_budget_utilization_ratio&window=30m
```

Under-reported direct-mode usage can leave a delegation tree's recorded consumption below its actual provider spend. Cross-reference [delegation-budget-exhaustion](delegation-budget-exhaustion.html) if a tree is over-consuming without the budget reflecting it.

### Step 3 — Tune the thresholds if the alert is noisy

The zero-token window count, the implausibly-small ratio threshold, and the direct-mode `ReportUsage` poll interval are operator-tunable gateway config values. A legitimately quiet direct-mode session (long idle gaps between LLM calls) can trip `zero_delta` at a too-strict window. Raise the window count or the poll interval if the affected sessions are legitimately idle rather than under-reporting.

### Step 4 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=sum by (tenant_id) (rate(lenny_gateway_token_usage_anomaly_total[5m]))&window=30m
```

- The anomaly rate for the affected tenant returns to 0.
- New direct-mode sessions on the corrected runtime image report non-zero token deltas.

## Escalation

Escalate to:

- **Tenant operator** when the affected runtime image is tenant-supplied and the tenant controls the build.
- **Platform on-call** when a first-party runtime image regressed and needs a rollback across multiple tenants.
- **Billing or FinOps** when under-reported usage has already left a delegation tree's recorded consumption materially below actual provider spend — cross-reference [delegation-budget-recovery](delegation-budget-recovery.html).
