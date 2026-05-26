// SPDX-License-Identifier: MIT
//
// k6 scenario: pod_claim_latency
//
// TESTING.md 12.7 target: 100 concurrent pod claims. SLO: P99 < 100ms
// cache-warm; SandboxClaim CAS under 50ms.
//
// Spec cross-reference: spec/06_warm-pod-model.md line 360 budgets
// "Pod claim and routing" at P95 ≤ 100ms (indicative planning until
// the §6.3 Tier-2 promotion gate clears, see line 368). The current
// baseline (P95 ≈ 113ms) records an overshoot relative to that budget;
// the regression-comparison logic asserts against the stored baseline,
// not the §6.3 indicative number. spec-reviews: F-6.3.17.
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
//   LENNY_RUNTIME    runtimeRef on the create body. Default echo-runtime-sidecar.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'echo-runtime-sidecar';

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
  // Terminate so the §4.6 SandboxClaim releases its pod back to the
  // warm pool. Without this each iteration leaks a claimed pod and
  // the pool exhausts within ~25 iterations on a Kind cluster sized
  // for smoke runs.
  if (res.status === 201 && res.body) {
    const id = JSON.parse(res.body).id;
    if (id) {
      http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
        headers: authHeaders({ 'Content-Type': 'application/json' }),
        tags: { name: 'release_pod' },
      });
    }
  }
}
