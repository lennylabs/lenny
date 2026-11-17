// SPDX-License-Identifier: MIT
//
// k6 scenario: session_delegation_fanout (REST)
//
// Cloud-load §12.7 REST fan-out scenario. Each iteration creates a
// parent session, fans out LENNY_FANOUT child sessions via POST
// /v1/sessions/start, then terminates every claimed session so the
// pool recycles. Targets the load-session-pool.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-session-runtime';
const FANOUT = parseInt(__ENV.LENNY_FANOUT || '10', 10);

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  scenarios: {
    fanout: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '1m',
      preAllocatedVUs: 10,
      maxVUs: 100,
    },
  },
  thresholds: {
    'http_req_duration{name:spawn_child}': ['p(99)<3000'],
    'http_req_failed{name:spawn_child}': ['rate<0.05'],
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
  const parent = http.post(
    `${BASE}/v1/sessions`,
    JSON.stringify({ runtimeRef: RUNTIME, isolationProfile: 'standard' }),
    { headers: authHeaders({ 'Idempotency-Key': `root-${__VU}-${__ITER}-${Date.now()}` }), tags: { name: 'spawn_root' } },
  );
  if (!check(parent, { 'root created': (r) => r.status === 201 }) || !parent.body) return;
  const parentID = JSON.parse(parent.body).id;

  const childIDs = [];
  for (let i = 0; i < FANOUT; i++) {
    const res = http.post(
      `${BASE}/v1/sessions/start`,
      JSON.stringify({ runtimeRef: RUNTIME, parentID: parentID, isolationProfile: 'standard' }),
      { headers: authHeaders({ 'Idempotency-Key': `child-${__VU}-${__ITER}-${i}-${Date.now()}` }), tags: { name: 'spawn_child' } },
    );
    if (check(res, { 'child accepted': (r) => r.status === 201 }) && res.body) {
      const cid = JSON.parse(res.body).id;
      if (cid) childIDs.push(cid);
    }
  }

  for (let i = 0; i < childIDs.length; i++) {
    http.post(`${BASE}/v1/sessions/${childIDs[i]}/terminate`, '', { headers: authHeaders({}), tags: { name: 'release_child' } });
  }
  http.post(`${BASE}/v1/sessions/${parentID}/terminate`, '', { headers: authHeaders({}), tags: { name: 'release_root' } });
}
