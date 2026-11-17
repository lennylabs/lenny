// SPDX-License-Identifier: MIT
//
// k6 scenario: cstateless_dispatch
//
// Cloud-load §5.2 + §12.7 concurrent-stateless dispatch scenario.
// Drives sustained session creation against the concurrent-stateless
// pool. Each session is routed through the pool's Service to a
// free slot on a warm pod; the per-iteration cost is dominated by
// request routing rather than workspace materialization.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-cstateless-runtime';

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  scenarios: {
    dispatch: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '1m',
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_duration{name:dispatch}': ['p(99)<1000'],
    'http_req_failed{name:dispatch}': ['rate<0.05'],
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
    { headers: authHeaders({ 'Idempotency-Key': idem }), tags: { name: 'dispatch' } },
  );
  if (!check(create, { 'dispatched': (r) => r.status === 201 }) || !create.body) return;
  const id = JSON.parse(create.body).id;
  if (!id) return;
  http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
    headers: authHeaders({}),
    tags: { name: 'release_dispatch' },
  });
}
