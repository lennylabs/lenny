---
layout: default
title: "llm-translation-degraded"
parent: "Runbooks"
triggers:
  - alert: LLMTranslationLatencyHigh
    severity: warning
  - alert: LLMTranslationSchemaDrift
    severity: warning
components:
  - gateway
symptoms:
  - "p95 translation latency elevated above the configured threshold"
  - "lenny_gateway_llm_translation_errors_total{error_type=\"schema_mismatch\"} non-zero"
  - "downstream LLM protocol changed"
tags:
  - llm
  - translation
  - protocol
  - openai
  - responses-api
requires:
  - admin-api
related:
  - gateway-capacity
  - llm-egress-anomaly
---

# llm-translation-degraded

The gateway's LLM translator (bridging MCP / Open Responses / OpenAI Chat Completions into the upstream provider's wire format) is either slow or rejecting payloads due to schema drift from a provider-side change.

## Trigger

- `LLMTranslationLatencyHigh` — p95 translation duration elevated above the configured threshold over its evaluation window.
- `LLMTranslationSchemaDrift` — schema-mismatch errors sustained non-zero.

Exact thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Error classes

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_gateway_llm_translation_errors_total[5m])&groupBy=error_type,provider&window=30m
```

- `schema_mismatch` → provider changed their API shape.
- `rate_limit` → upstream 429s leaking through translation.
- `unsupported_feature` → a feature was requested the translator doesn't support for this provider.

### Step 2 — Latency distribution

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_gateway_llm_translation_duration_seconds_bucket[5m]))&groupBy=provider,direction&window=30m
```

Compare by `direction` (request vs response) — response-direction drift often points at a provider streaming-format change.

### Step 3 — Sample payload

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-gateway --since=5m \
  | grep -E "llm_translation_error|schema_mismatch" | head -5
```

Log entries include the offending field path.

### Step 4 — Provider changelog

Check the provider's API changelog for recent changes that match the error signatures.

## Remediation

### Step 1 — Schema drift: add a mapping

If the provider added or renamed a field:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin translator mappings show <provider>
lenny-ctl admin translator mappings update <provider> -f mapping.yaml
```

The mapping is hot-reloaded. Confirm:

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_llm_translation_errors_total{error_type="schema_mismatch"}&window=5m
```

Rate returns to zero within a minute.

### Step 2 — Latency: scale translation workers

If latency is elevated without errors:

- Raise `gateway.llmTranslator.workers` in Helm values and `helm upgrade`.
- Extract the translator subsystem if GC pressure is visible — see [gateway-subsystem-extraction](gateway-subsystem-extraction.html).

### Step 3 — Unsupported feature

If a caller is requesting a feature Lenny doesn't translate:

1. Document the requirement.
2. Decide with product whether to implement or reject at the API layer.
3. Until then, update the error message to be actionable for the client.

### Step 4 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose llm-translation
```

- Translation error rate back within provider-specific baseline.
- p95 translation duration back within the alert threshold.

## Escalation

Escalate to:

- **Platform engineering** for schema-drift incidents that require code changes (new mapping code, not a config update).
- **Provider support** if the provider API behavior contradicts their documentation.
- **Capacity owner** if translator subsystem extraction is needed but not yet planned for this tier.
