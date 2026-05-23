// SPDX-License-Identifier: MIT
//
// k6 scenario: gateway_capacity_calibration
//
// Phase 2 calibration harness for the §4.1 gateway capacity budget and
// the §4.1 subsystem extraction thresholds. spec/04_system-components.md
// §4.1 lines 86-94 require this harness to replace the provisional
// `maxSessionsPerReplica` and the four `gateway.extractionThresholds.*`
// defaults before any Tier 2 production deployment. spec/04 lines
// 136-144 cite the same methodology for the extraction thresholds.
//
// Methodology (spec §4.1 lines 86-94):
//   1. Drive a single gateway replica from 0 to maxSessionsPerReplica
//      × 1.5 concurrent sessions in 10% increments.
//   2. At each step, record the per-subsystem key metrics:
//        - lenny_stream_proxy_p99_attach_latency_seconds
//        - lenny_stream_proxy_queue_depth
//        - lenny_upload_handler_p99_latency_seconds
//        - lenny_upload_handler_active_uploads
//        - lenny_mcp_fabric_p99_orchestration_latency_seconds
//        - lenny_mcp_fabric_active_delegations
//        - lenny_gateway_llm_proxy_active_connections
//        - lenny_llm_proxy_p99_ttfb_seconds
//        - lenny_gateway_gc_pause_p99_ms
//      plus RSS.
//   3. Saturation point: first session count at which
//      `lenny_stream_proxy_p99_attach_latency_seconds` first exceeds
//      0.8 s OR `lenny_stream_proxy_queue_depth` first exceeds 500.
//   4. Budget setting: `maxSessionsPerReplica` = saturation × 0.8
//      (20% headroom).
//   5. HPA validation: confirm `lenny_gateway_request_queue_depth` HPA
//      target triggers at least one full scale cycle (2-3 min) before
//      the replica reaches saturation.
//   6. Threshold setting (lines 136-144): replace each provisional
//      `gateway.extractionThresholds.<subsystem>.<metric>` with the
//      saturation value × 0.8 from the corresponding metric in step 2.
//
// This file is the Phase 2 scaffold. It is NOT run by the PR-cadence
// smoke; the calibration is operator-driven on a single isolated
// gateway replica with the Tier 2 session target sized appropriately.
// The companion baselines.json carries placeholder zero values that
// the calibrator overwrites with the empirically measured per-step
// percentile output. spec: §4.1 line 94 — "Provisional values must not
// remain in place for any Tier 2 production deployment."
//
// Operator workflow (see README.md in this directory):
//   1. Deploy a single gateway replica (`gateway.replicas: 1`) sized
//      for the Tier 2 target.
//   2. Run this scenario through the operator's k6 harness against
//      that replica with LENNY_BASE_URL pointing at it.
//   3. Capture the per-step percentile output, identify the saturation
//      point, and apply step 3-6 of the methodology.
//   4. Apply the resulting values to the Helm values:
//        - `gateway.maxSessionsPerReplica`
//        - `gateway.extractionThresholds.streamProxy.*`
//        - `gateway.extractionThresholds.uploadHandler.*`
//        - `gateway.extractionThresholds.mcpFabric.*`
//        - `gateway.extractionThresholds.llmProxy.*`
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on create body. Default echo-runtime-sidecar.
//   LENNY_TIER2_MAX  Tier 2 target maxSessionsPerReplica (the ramp peak
//                    is 1.5 × this value per spec line 86 step 1).
//                    Default 200 (the provisional Tier 2 value from
//                    spec/04 line 73).

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'echo-runtime-sidecar';
const TIER2_MAX = parseInt(__ENV.LENNY_TIER2_MAX || '200', 10);

// spec: §4.1 line 86 step 1 — "0 to maxSessionsPerReplica × 1.5 in
// 10% increments". The 10% increments are encoded as stage durations
// against a constant ramp.
const RAMP_PEAK = Math.ceil(TIER2_MAX * 1.5);

export const options = {
  // Emit p99 and p99.9 in the summary export so the calibrator can
  // identify the saturation inflection at the percentile the SLO is
  // stated at (spec §4.1 line 88).
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // Stage-based ramp from 0 to RAMP_PEAK across the 10 deciles. Each
  // stage holds the VU level long enough for queue depth and latency
  // to stabilise (60s; in the operator-driven calibration run, this
  // is the post-ramp dwell time the spec line 87 step 2 reads
  // metrics from).
  stages: [
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.10) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.20) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.30) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.40) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.50) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.60) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.70) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.80) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 0.90) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 1.00) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 1.10) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 1.20) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 1.30) },
    { duration: '60s', target: Math.ceil(RAMP_PEAK * 1.40) },
    { duration: '60s', target: RAMP_PEAK },
  ],
  // Capture the spec's saturation conditions as k6 thresholds. The
  // calibration run intentionally drives the gateway past them so the
  // operator can identify the inflection; the thresholds are recorded
  // (abortOnFail=false) rather than enforced.
  thresholds: {
    // spec §4.1 line 88 step 2 — saturation at p99 attach latency > 0.8s
    // OR queue depth > 500. The k6 view here is the request latency at
    // the gateway's session-creation endpoint; the in-replica
    // `lenny_stream_proxy_p99_attach_latency_seconds` metric is the
    // authoritative saturation signal and must be scraped from
    // /metrics out-of-band.
    'http_req_duration{name:create_session}': [
      { threshold: 'p(99)<800', abortOnFail: false },
    ],
    'http_req_failed': [
      { threshold: 'rate<0.05', abortOnFail: false },
    ],
  },
};

function authHeaders(extra) {
  return Object.assign(
    {
      'Content-Type': 'application/json',
      'X-Lenny-Tenant-ID': TENANT,
      'X-Lenny-Roles': ROLES,
      'X-Lenny-User-ID': USER,
    },
    extra || {},
  );
}

export default function () {
  const idem = `${__VU}-${__ITER}-${Date.now()}`;
  const payload = JSON.stringify({
    runtimeRef: RUNTIME,
    userId: `vu-${__VU}@${TENANT}.com`,
  });
  const params = {
    headers: authHeaders({ 'Idempotency-Key': idem }),
    tags: { name: 'create_session' },
  };
  const res = http.post(`${BASE}/v1/sessions`, payload, params);
  check(res, {
    'status is 201': (r) => r.status === 201,
  });
}
