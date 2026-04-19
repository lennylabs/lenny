---
layout: default
title: "slo-ttft"
parent: "Runbooks"
triggers:
  - alert: TTFTBurnRate
    severity: critical
components:
  - gateway
  - warmPools
symptoms:
  - "time-to-first-token p95 exceeds the configured SLO target"
  - "LLM streaming slow to emit first token"
  - "clients perceive sluggish response start"
tags:
  - slo
  - ttft
  - llm
  - latency
requires:
  - admin-api
related:
  - llm-translation-degraded
  - llm-egress-anomaly
  - gateway-capacity
---

# slo-ttft

Time-to-first-token (TTFT) SLO is burning. TTFT = time from the session's first LLM call to the first token the client receives over the stream. The SLO target, burn-rate thresholds, and evaluation windows are deployer-configurable — your cluster's defaults are in [Metrics Reference](../reference/metrics.html#alert-rules).

## Trigger

- `TTFTBurnRate` — TTFT p95 burning at the fast or slow rate configured for this SLO.

## Diagnosis

### Step 1 — Phase breakdown

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_ttft_phase_duration_seconds_bucket[5m]))&groupBy=phase&window=1h
```

Phases: `upstream_request`, `upstream_first_byte`, `translate`, `stream_write`.

### Step 2 — Upstream latency

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_gateway_llm_upstream_ttft_seconds_bucket[5m]))&groupBy=provider,model&window=1h
```

TTFT is dominated by upstream provider time. Check if a specific provider or model is degraded.

### Step 3 — Translation cost

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_gateway_llm_translation_duration_seconds_bucket[5m]))&groupBy=direction,provider&window=1h
```

If `direction=request` translation p95 is elevated compared to baseline, the gateway adds meaningful latency before the upstream call. See [llm-translation-degraded](llm-translation-degraded.html) for the configured threshold and remediation.

### Step 4 — Queueing

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_llm_proxy_queue_depth&window=1h
```

Non-zero sustained queue depth means the LLM proxy is rate-limited or under-scaled for throughput.

## Remediation

### Step 1 — Provider slow

If `upstream_first_byte` dominates and the upstream provider is slow:

1. Check provider status.
2. Confirm you're not hitting credential rate limits — see [credential-pool-exhaustion](credential-pool-exhaustion.html).
3. Consider routing a share of traffic to an alternate provider if available.

### Step 2 — Translation slow

See [llm-translation-degraded](llm-translation-degraded.html).

### Step 3 — Queueing

If queue depth is elevated:

- Scale the LLM proxy (if extracted) — see [gateway-subsystem-extraction](gateway-subsystem-extraction.html).
- Confirm credentials are not the bottleneck — add more credentials to the pool.

### Step 4 — Streaming path

If `stream_write` phase is slow, check gateway CPU/memory pressure and client-side flow control. A slow client can back-pressure the gateway's stream write path.

### Step 5 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose slo ttft
```

- TTFT p95 back within the configured SLO target.
- Burn-rate alerts clear within the fast window.

## Escalation

Escalate to:

- **Provider support** for sustained upstream slowness.
- **Platform engineering** for translation or stream-write path issues.
- **SLO owner** for repeated burn-rate incidents despite remediation — may indicate the target is set too tight for the provider mix.
