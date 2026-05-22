// SPDX-License-Identifier: MIT
//
// k6 scenario: session_pod_claim
//
// Cloud-load §12.7 pod-claim latency scenario. Drives POST
// /v1/sessions/start at the LENNY_RATE arrival rate; each iteration
// terminates the session so the pool recycles. The scenario targets
// the load-session-pool whose minWarm/maxWarm are sized by
// LENNY_LOAD_SCALE through agent-workload-load.yaml.tmpl.
//
// Environment (set by tests/tier7_load_cloud/scaffolds_test.go):
//   LENNY_BASE_URL   Gateway base URL.
//   LENNY_TENANT     Tenant ID. Default acme.
//   LENNY_ROLES      Roles. Default tenant-admin.
//   LENNY_USER       User ID. Default alice.
//   LENNY_RUNTIME    runtimeRef. The cloud-load harness sets it.
//   LENNY_RATE       Arrival rate (sessions/sec).
//   LENNY_DURATION   Scenario duration. Default 1m.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-session-runtime';

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  scenarios: {
    pod_claim: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '1m',
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_duration{name:claim_pod}': ['p(99)<2000'],
    'http_req_failed{name:claim_pod}': ['rate<0.05'],
  },
};

function authHeaders(extra) {
  return Object.assign({
    'Content-Type': 'application/json',
    'X-Lenny-Tenant-ID': TENANT,
    'X-Lenny-Roles': ROLES,
    'X-Lenny-User-ID': USER,
  }, extra || {});
}

export default function () {
  const idem = `${__VU}-${__ITER}-${Date.now()}`;
  const create = http.post(
    `${BASE}/v1/sessions/start`,
    JSON.stringify({ runtimeRef: RUNTIME, isolationProfile: 'standard' }),
    { headers: authHeaders({ 'Idempotency-Key': idem }), tags: { name: 'claim_pod' } },
  );
  if (!check(create, { 'session running': (r) => r.status === 201 }) || !create.body) {
    return;
  }
  const id = JSON.parse(create.body).id;
  if (!id) return;
  http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
    headers: authHeaders({}),
    tags: { name: 'release_pod' },
  });
}
