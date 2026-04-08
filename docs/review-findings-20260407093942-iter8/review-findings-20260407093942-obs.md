# Review Findings — Iteration 8, Perspective 12: Observability & Operational Monitoring

**Spec:** `docs/technical-design.md` (8,649 lines)
**Date:** 2026-04-07
**Prior findings:** OBS-001 through OBS-036 (iterations 1–7). OBS-031 skipped (metrics table sync, excluded by scope).
**Category prefix:** OBS, starting at OBS-037.

---

## Summary

Five genuine design flaws found. No table-entry gaps raised. All findings are logic errors, specification contradictions, or architectural problems that would cause incorrect runtime behavior or mislead implementors.

| # | ID | Severity | Description | Sections |
|---|-----|----------|-------------|---------|
| 1 | OBS-037 | High | Availability SLO burn-rate formula is mathematically inverted | §16.5 |
| 2 | OBS-038 | High | Head-based sampling incompatible with stated 100%-error-sampling requirement | §16.3 |
| 3 | OBS-039 | Medium | HPA calibration step 4 contradicts canonical HPA metric role table | §4.1, §16.5 |
| 4 | OBS-040 | Medium | Fleet-wide GC pause metric requires cross-replica aggregation the spec never defines | §16.1 |
| 5 | OBS-041 | Medium | `CheckpointDurationHigh` threshold (10s) misaligned with checkpoint SLO (2s), practically unreachable, and checkpoint SLO has no burn-rate alert | §4.4, §16.5 |

---

## Findings

### OBS-037 Availability SLO Burn-Rate Formula Is Mathematically Inverted [High]

**Section:** §16.5

**Problem:** The burn-rate calculation paragraph states:

> "For availability SLOs, burn rate is `(1 - error_rate) / (1 - slo_target)` over the window."

This formula is inverted. Substituting correct values demonstrates the error:

- At 0% error rate (no errors at all): `(1 - 0) / (1 - 0.999) = 1000×` — indicates catastrophic budget consumption when there is none.
- At 100% error rate (complete outage): `(1 - 1) / (1 - 0.999) = 0` — indicates zero burn rate during a total outage.

The correct standard burn-rate formula (Google SRE Workbook, Chapter 5) is:

```
burn_rate = error_rate / (1 - slo_target)
```

At 0% errors: `0 / 0.001 = 0` (correct — no budget consumed).
At 100% errors: `1.0 / 0.001 = 1000×` (correct — full budget consumed at maximum rate).

The latency SLO formula (`rate(slow_requests[window]) / rate(total_requests[window]) / error_budget_fraction`) is correct and unaffected.

**Impact:** Any implementation that uses the specified formula will compute burn rates that are the inverse of reality, causing alerts to fire at 0% error rate and suppress during outages. This is a critical correctness error for any alerting implementation built from this spec.

**Recommendation:** Replace `(1 - error_rate) / (1 - slo_target)` with `error_rate / (1 - slo_target)`.

---

### OBS-038 Head-Based Sampling Incompatible with 100%-Error-Sampling Requirement [High]

**Section:** §16.3

**Problem:** The distributed tracing section states:

> "Head-based sampling at a default rate of 10% for normal operations... 100% sampling is applied for errors (any span with error status), slow requests (session creation exceeding P99 latency), and delegation trees."

These two requirements are mutually exclusive. Head-based sampling makes the sampling decision at the beginning of a trace, before any spans have been emitted — therefore before it can be known whether the trace will contain an error span or whether the request will be slow. It is architecturally impossible to retroactively apply 100% sampling to traces that were already 90%-dropped at the head.

Capturing 100% of error traces and slow traces requires **tail-based sampling**: the sampling decision is deferred until the complete trace is assembled (or enough spans have been collected to evaluate the condition). The spec never mentions tail-based sampling, never specifies a tail-based sampling component in the OTel Collector pipeline, and never defines how the OTel Collector is configured to implement this behavior.

The "delegation trees: all spans in a tree are sampled if the root is sampled" clause is consistent with head-based sampling (the decision is propagated from root to children), but the error and slow-request clauses require tail-based sampling.

**Impact:** Implementors following the "head-based sampling" specification will lose all error and slow-request traces at the 10% drop rate. The stated guarantee of 100% error trace capture will be silently violated.

**Recommendation:** Either:
1. Specify a tail-based sampling processor in the OTel Collector configuration (e.g., the OpenTelemetry Collector `tail_sampling` processor with `status_code: ERROR` and latency-based policies). Acknowledge that this requires buffering complete traces in the collector before sampling decisions are made.
2. Or replace the "100% sampling for errors" requirement with a head-based alternative: use a 100% sampling rate by default (with an option to reduce for high-volume deployments) and rely on the metrics layer for error rate signals rather than traces.

---

### OBS-039 HPA Calibration Step 4 Contradicts Canonical HPA Metric Role Table [Medium]

**Section:** §4.1

**Problem:** The canonical HPA metric role table in §4.1 explicitly states that `lenny_gateway_active_sessions / gateway.maxSessionsPerReplica` **"Must NOT be used as the sole HPA trigger"** and designates it as **"Alert only"**. The rationale given is that it measures Postgres-backed session count and lags real load.

However, the capacity budget calibration methodology in the same section (step 4) directly contradicts this:

> "4. **HPA validation.** Confirm that a horizontal scale-out **triggered by** `lenny_gateway_active_sessions / gateway.maxSessionsPerReplica` **reaching 80%** fires at least one full HPA scale cycle..."

Step 4 describes this metric as an HPA trigger and uses it as the basis for validating scale-out behavior — which is precisely what the canonical table prohibits.

**Impact:** An implementor following step 4 would configure `lenny_gateway_active_sessions / gateway.maxSessionsPerReplica` as an HPA trigger, contradicting the canonical table's explicit prohibition. The two descriptions are irreconcilable.

**Recommendation:** Correct step 4 to describe HPA scale-out validation using the designated primary HPA trigger (`lenny_gateway_request_queue_depth`) and secondary trigger (`lenny_gateway_active_streams`), not the session capacity ratio. The session capacity ratio can be monitored during the benchmark but should not be the validation criterion for HPA firing.

---

### OBS-040 Fleet-Wide GC Pause Metric Requires Undefined Cross-Replica Aggregation [Medium]

**Section:** §16.1

**Problem:** The metrics table defines `lenny_gateway_gc_pause_fleet_p99_ms` as a **Gauge** metric and describes it as:

> "computed as `max(lenny_gateway_gc_pause_p99_ms)` over all replica instances"

A Gauge metric in the §16.1 table implies the platform instruments and emits this value. However, a single gateway replica process cannot compute `max()` across all other replicas without:

1. Inter-replica gossip or coordination (not described anywhere in the spec), OR
2. A Prometheus recording rule that aggregates the per-replica `lenny_gateway_gc_pause_p99_ms` metrics externally.

The notation `max(metric) over all replica instances` is standard PromQL syntax for option 2 — a recording rule evaluated by Prometheus, not a value emitted by the gateway process. But the §16.1 metrics table is titled "Metrics" (implying platform-emitted instrumentation), and the `Tier3GCPressureHigh` alert (§16.5) references this as a metric the system surfaces rather than a recording rule to be authored.

**Impact:** Implementors treating §16.1 as a list of metrics to instrument will attempt to emit `lenny_gateway_gc_pause_fleet_p99_ms` from each gateway process, which is impossible without a shared state mechanism. Implementors treating it as a recording rule may correctly implement it but will have no canonical expression or recording rule configuration in the spec.

**Recommendation:** Either:
1. Reclassify `lenny_gateway_gc_pause_fleet_p99_ms` as a **Prometheus recording rule** and provide the canonical PromQL expression: `max(lenny_gateway_gc_pause_p99_ms)`. Remove it from the emitted Gauges table and add it to an observability configuration specification (Helm chart recording rules).
2. Or, if the intent is for the gateway to compute this via cross-replica coordination, specify the mechanism (e.g., a gossip protocol, a coordinator sidecar, or a Redis-based shared state).

---

### OBS-041 `CheckpointDurationHigh` Alert Misaligned with SLO, Practically Unreachable, and Checkpoint SLO Lacks Burn-Rate Alert [Medium]

**Section:** §4.4, §16.5

**Problem:** Three related issues:

**1. Alert threshold is 5× the SLO target.** The checkpoint duration SLO (§16.5 SLO table) is P95 < **2 seconds** for workspaces ≤100MB. The `CheckpointDurationHigh` warning alert fires when P95 of `lenny_checkpoint_duration_seconds` exceeds **10 seconds**. The alert fires at 5× the SLO boundary. The alert description says it fires "indicating workspace sizes are approaching or exceeding the checkpoint SLO boundary" — but at 10s the SLO boundary (2s) was breached 5× ago. The description is factually incorrect.

**2. Alert is practically unreachable under normal configuration.** The workspace hard size limit (`workspaceSizeLimitBytes`) defaults to 512MB (§4.4). The quiescence time formula given in §4.4 is:

> `expected_quiescence_seconds ≈ workspace_bytes / (100 × 1024 × 1024)`

At the hard limit of 512MB: `512 × 1024 × 1024 / (100 × 1024 × 1024) ≈ 5.1 seconds`. The maximum possible checkpoint duration within the configured hard limit is ~5.1s — below the 10s alert threshold. The `CheckpointDurationHigh` alert therefore cannot fire under normal configuration. Any checkpoint approaching 10s would require a workspace exceeding the hard size limit, which should already have triggered pre-checkpoint abort (`lenny_checkpoint_size_exceeded_total`) before the slow checkpoint could occur.

**3. Checkpoint SLO has no burn-rate alert.** §16.5 states: "Multi-window burn-rate alerting is required for **all** availability and latency SLOs." The SLO table defines seven SLOs; the burn-rate alert table defines only six burn-rate alerts. The checkpoint duration SLO (P95 < 2s) has no corresponding burn-rate alert. This is inconsistent with the stated requirement.

**Recommendation:** 
- Lower the `CheckpointDurationHigh` threshold to align with the SLO: fire at P95 > 2s (or at a configurable percentage above the SLO target, e.g., 2.5s for a 25% headroom). Update the description to remove the inaccurate "approaching or exceeding the checkpoint SLO boundary" text.
- Add a `CheckpointDurationBurnRate` entry to the burn-rate alert table.
