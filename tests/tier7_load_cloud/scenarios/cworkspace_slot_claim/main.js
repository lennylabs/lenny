// SPDX-License-Identifier: MIT
//
// k6 scenario: cworkspace_slot_claim
//
// Cloud-load §5.2 + §12.7 concurrent-workspace slot-claim scenario.
// Drives sustained session creation against a concurrent-workspace
// pool. The gateway routes each session to a free slot on an
// existing warm pod via §4.6 SlotClaimer rather than claiming a
// new pod, so the scenario measures slot-claim throughput at high
// per-pod density.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-cworkspace-runtime';

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  scenarios: {
    slot_claim: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '1m',
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_duration{name:claim_slot}': ['p(99)<1500'],
    'http_req_failed{name:claim_slot}': ['rate<0.05'],
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
    JSON.stringify({ runtimeRef: RUNTIME }),
    { headers: authHeaders({ 'Idempotency-Key': idem }), tags: { name: 'claim_slot' } },
  );
  if (!check(create, { 'slot claimed': (r) => r.status === 201 }) || !create.body) return;
  const id = JSON.parse(create.body).id;
  if (!id) return;
  http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
    headers: authHeaders({}),
    tags: { name: 'release_slot' },
  });
}
