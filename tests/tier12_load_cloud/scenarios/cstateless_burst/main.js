// SPDX-License-Identifier: MIT
//
// k6 scenario: cstateless_burst
//
// Cloud-load §5.2 + §10.1 concurrent-stateless burst scenario. A
// short, intense burst of session creates targeting the
// concurrent-stateless pool, followed by a steady-state floor. The
// scenario measures how quickly the §10.1 HPA settles the pool to
// absorb the burst and the Service-fronted latency profile during
// the spike.

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
    burst: {
      executor: 'ramping-arrival-rate',
      startRate: 1,
      timeUnit: '1s',
      stages: [
        { target: parseInt(__ENV.LENNY_RATE || '5', 10) * 4, duration: '10s' },
        { target: parseInt(__ENV.LENNY_RATE || '5', 10), duration: __ENV.LENNY_DURATION || '50s' },
      ],
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_duration{name:burst_dispatch}': ['p(99)<3000'],
    'http_req_failed{name:burst_dispatch}': ['rate<0.10'],
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
    { headers: authHeaders({ 'Idempotency-Key': idem }), tags: { name: 'burst_dispatch' } },
  );
  if (!check(create, { 'dispatched': (r) => r.status === 201 }) || !create.body) return;
  const id = JSON.parse(create.body).id;
  if (!id) return;
  http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
    headers: authHeaders({}),
    tags: { name: 'release_burst' },
  });
}
