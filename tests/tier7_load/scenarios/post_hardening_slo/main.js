// SPDX-License-Identifier: MIT
//
// k6 scenario: post_hardening_slo
//
// TESTING.md 13.31 target: re-run the §13.5 pre-hardening full-system
// load baseline with the §14 security hardening enabled — cosign
// image-signature verification at admission, the §13.1 pod-security
// admission webhook, NetworkPolicy refinement, and gateway mTLS. The
// §13.31 phase gate compares the post-hardening percentiles against
// the §13.29 baseline JSON; any regression beyond the documented
// budget is the §14 hardening overhead and is recorded in
// tests/tier7_load/baselines/.
//
// The workload is the §12.7 pod_claim_latency scenario (100 concurrent
// claims): POST /v1/sessions/start exercises the create-and-claim
// path whose latency is the pod-claim cost the SLO bounds. The
// hardening overhead surfaces as a per-percentile delta against the
// pre-hardening pod_claim_latency baseline.
//
// The Tier-7 Go wrapper picks rate and duration. The PR-cadence smoke
// run holds a modest steady arrival rate; the production run is the
// 100-concurrent sweep against the §13.29 baseline.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on the create body. Default claude-code.
//   LENNY_RATE       Arrivals per second. Default 5.
//   LENNY_DURATION   Run duration. Default 30s.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'claude-code';

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at and the
  // §13.31 phase gate compares.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // A rate-bounded executor. An unbounded VU loop spins into hundreds
  // of thousands of requests when a request fails fast, saturating the
  // kubectl port-forward the Tier-7 wrapper reaches the gateway
  // through.
  scenarios: {
    claim: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '30s',
      preAllocatedVUs: 8,
      maxVUs: 20,
    },
  },
  thresholds: {
    'http_req_duration{name:claim_pod_hardened}': ['p(99)<100'],
    'http_req_failed': ['rate<0.05'],
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
  const payload = JSON.stringify({ runtimeRef: RUNTIME });
  const params = {
    headers: authHeaders({ 'Idempotency-Key': idem }),
    tags: { name: 'claim_pod_hardened' },
  };
  const res = http.post(`${BASE}/v1/sessions/start`, payload, params);
  check(res, {
    'status is 201': (r) => r.status === 201,
    'session running': (r) => r.body && r.body.includes('"running"'),
  });
}
