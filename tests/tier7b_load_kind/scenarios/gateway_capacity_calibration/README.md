# gateway_capacity_calibration

Phase 2 calibration harness for the §4.1 gateway capacity budget and the
§4.1 subsystem extraction thresholds.

## What this is

`spec/04_system-components.md` §4.1 lines 86-94 specify the calibration
methodology that produces the empirical `gateway.maxSessionsPerReplica`
value. Lines 136-144 specify the same methodology for the four subsystem
extraction thresholds. Both sets of values ship with provisional
first-principles defaults that must be replaced with measured values
before any Tier 2 production deployment.

This directory is the scaffold for that calibration. It carries the k6
ramp scenario and a placeholder baseline. The PR-cadence smoke does not
run this scenario; calibration is an operator-driven activity against a
single isolated gateway replica sized for the Tier 2 target.

## When to run

Before promoting a deployment to Tier 2. The §4.1 line 94 / line 144
language is "Provisional values must not remain in place for any Tier 2
production deployment."

## Pre-run checks

The scenario requires:

- A single gateway replica deployed with `gateway.replicas: 1` (the
  per-replica budget is the unit of measurement).
- The replica sized for the Tier 2 target on memory and CPU. The Tier 2
  provisional budget of 200 sessions per replica from spec/04 line 73
  is the starting peak.
- A pre-warmed pod pool of at least `RAMP_PEAK` agents available, where
  `RAMP_PEAK = LENNY_TIER2_MAX × 1.5` (default 300 when
  `LENNY_TIER2_MAX=200`).
- Prometheus scraping the gateway's `/metrics` endpoint so the
  in-replica metric series are available for the post-run analysis.
- The k6 binary on the operator's PATH.

## Methodology

Per `spec/04_system-components.md` §4.1 lines 86-94:

1. **Ramp test.** Drive the replica from 0 to `RAMP_PEAK` in 10%
   increments. At each step record:
   - `lenny_stream_proxy_p99_attach_latency_seconds`
   - `lenny_stream_proxy_queue_depth`
   - `lenny_gateway_gc_pause_p99_ms`
   - RSS (`process_resident_memory_bytes`)
2. **Saturation point.** Identify the session count at which
   `lenny_stream_proxy_p99_attach_latency_seconds` first exceeds 0.8 s
   OR `lenny_stream_proxy_queue_depth` first exceeds 500.
3. **Budget setting.** `maxSessionsPerReplica = saturation × 0.8`.
4. **HPA validation.** Confirm that the HPA target on
   `lenny_gateway_request_queue_depth` triggers at least one full
   scale cycle (2-3 minutes) before the replica reaches saturation.
5. **Document and replace.** Replace the provisional values with the
   calibrated ones in the Helm values:
   - `gateway.maxSessionsPerReplica`
   - `gateway.extractionThresholds.streamProxy.queueDepth`
   - `gateway.extractionThresholds.streamProxy.p99AttachLatencySeconds`
   - `gateway.extractionThresholds.uploadHandler.activeConcurrent`
   - `gateway.extractionThresholds.uploadHandler.p99LatencySeconds`
   - `gateway.extractionThresholds.mcpFabric.activeDelegations`
   - `gateway.extractionThresholds.mcpFabric.p99OrchestrationLatencySeconds`
   - `gateway.extractionThresholds.llmProxy.activeConnections`
   - `gateway.extractionThresholds.llmProxy.p99TtfbSeconds`

Per `spec/04_system-components.md` §4.1 lines 136-144 the threshold
methodology is the same shape: ramp through 25 / 50 / 75 / 100 percent
of the Tier 2 target, identify the first statistically significant
inflection per subsystem, set the threshold at the saturation value
minus 20% headroom.

## Files

| File             | Role                                                                 |
|:-----------------|:---------------------------------------------------------------------|
| `main.js`        | k6 ramp scenario. The default `LENNY_TIER2_MAX` is 200.              |
| `README.md`      | This document.                                                       |

The companion baseline lives at
`tests/tier7b_load_kind/baselines/gateway_capacity_calibration.json`
and carries placeholder zero values. Operators write the post-calibration
suggested values into this file as part of step 5 above.

## What the scenario records

The scenario emits k6's standard `http_req_duration` and
`http_req_failed` metrics tagged by stage. The authoritative saturation
signals are the in-replica `lenny_stream_proxy_*` Prometheus series;
operators capture them from the gateway's `/metrics` endpoint during
the run. The k6 thresholds in `main.js` are advisory (no abort) — the
calibration intentionally pushes the replica past saturation.

## Why this is not in the smoke

The smoke runs sustain ~20 VUs for 25 seconds against the e2e Kind
gateway. The calibration harness sustains 300 concurrent sessions for
15 minutes against a single replica sized for that load. The Kind
gateway's modest memory limit and the smoke-sized warm pool
(`minWarm=1/maxWarm=2`) cannot absorb the ramp, so the scenario is not
PR-cadence and is excluded from `lenny-test --tier load_kind`. The
tier-0 static check `TestGatewayCapacityCalibrationScaffoldExists`
asserts the scenario file is present so the calibration harness cannot
silently disappear before Tier 2 promotion.

## Cross-references

- `spec/04_system-components.md` §4.1 lines 86-94 — capacity-budget
  methodology.
- `spec/04_system-components.md` §4.1 lines 136-144 — extraction
  threshold methodology.
- `spec/04_system-components.md` §4.1 line 117 — extraction threshold
  Helm values.
- `charts/lenny/values.yaml` — Helm defaults that the calibrated values
  replace.
