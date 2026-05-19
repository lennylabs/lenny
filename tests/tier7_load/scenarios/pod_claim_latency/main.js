// SPDX-License-Identifier: MIT
//
// k6 scenario: pod_claim_latency
//
// TESTING.md 12.7 target: 100 concurrent pod claims. SLO: P99 < 100ms
// cache-warm; SandboxClaim CAS under 50ms.
//
// POST /v1/sessions/start is the create-and-start path: the gateway
// creates the session and claims a warm pod for it in one request, so
// its latency is the end-to-end pod-claim cost the SLO bounds. The
// script drives that endpoint under a constant VU pool.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count; the production run uses
// 100 concurrent claims.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on the create body. Default claude-code.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'claude-code';

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  thresholds: {
    'http_req_duration{name:claim_pod}': ['p(99)<100'],
    'http_req_failed': ['rate<0.01'],
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
    tags: { name: 'claim_pod' },
  };
  const res = http.post(`${BASE}/v1/sessions/start`, payload, params);
  check(res, {
    'status is 201': (r) => r.status === 201,
    'session running': (r) => r.body && r.body.includes('"running"'),
  });
}
