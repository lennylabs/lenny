// SPDX-License-Identifier: MIT
//
// k6 scenario: session_streaming_throughput
//
// Cloud-load §12.7 streaming-throughput scenario. Each iteration
// creates a session via /v1/sessions/start, injects a message via
// POST /v1/sessions/{id}/messages, then terminates the session.
// The LENNY_STREAMING env caps the concurrent in-flight session
// count; constant-arrival-rate matches the active load scale.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-session-runtime';
const STREAMING = parseInt(__ENV.LENNY_STREAMING || '25', 10);

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  scenarios: {
    streaming: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '1m',
      preAllocatedVUs: Math.max(STREAMING, 10),
      maxVUs: Math.max(STREAMING * 2, 50),
    },
  },
  thresholds: {
    'http_req_duration{name:stream_message}': ['p(95)<1000'],
    'http_req_failed{name:stream_message}': ['rate<0.05'],
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
  const create = http.post(
    `${BASE}/v1/sessions/start`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    { headers: authHeaders({ 'Idempotency-Key': `${__VU}-${__ITER}-${Date.now()}` }), tags: { name: 'create_session' } },
  );
  if (!check(create, { 'session created': (r) => r.status === 201 }) || !create.body) return;
  const id = JSON.parse(create.body).id;
  if (!id) return;

  const msg = http.post(
    `${BASE}/v1/sessions/${id}/messages`,
    JSON.stringify({ messages: [{ role: 'user', content: 'ping' }] }),
    { headers: authHeaders({}), tags: { name: 'stream_message' } },
  );
  check(msg, {
    'message delivered': (r) => r.status === 200,
    'response carries output': (r) => r.body && r.body.includes('"deliveryStatus"'),
  });

  http.post(`${BASE}/v1/sessions/${id}/terminate`, '', { headers: authHeaders({}), tags: { name: 'release_session' } });
}
