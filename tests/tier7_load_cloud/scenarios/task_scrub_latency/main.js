// SPDX-License-Identifier: MIT
//
// k6 scenario: task_scrub_latency
//
// Cloud-load §5.2 + §12.7 between-task scrub latency scenario.
// Hammers tasks against the task pool with a low concurrency,
// then observes the per-task wall-clock floor. The §5.2
// cleanupCommands + workspace scrub is the dominant cost; a
// regression in pkg/sandbox or the cleanup path surfaces as
// elevated p99 on the run_task tag.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-task-runtime';

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // Low concurrency so the scenario observes the per-task scrub
  // floor rather than the saturation-throughput ceiling.
  scenarios: {
    scrub_latency: {
      executor: 'constant-vus',
      vus: 2,
      duration: __ENV.LENNY_DURATION || '1m',
    },
  },
  thresholds: {
    'http_req_duration{name:run_task}': ['p(99)<10000'],
    'http_req_failed{name:run_task}': ['rate<0.05'],
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
    { headers: authHeaders({ 'Idempotency-Key': `${__VU}-${__ITER}-${Date.now()}` }), tags: { name: 'run_task' } },
  );
  if (!check(create, { 'task started': (r) => r.status === 201 }) || !create.body) return;
  const id = JSON.parse(create.body).id;
  if (!id) return;

  http.post(
    `${BASE}/v1/sessions/${id}/messages`,
    JSON.stringify({ messages: [{ role: 'user', content: 'task-input' }] }),
    { headers: authHeaders({}), tags: { name: 'task_work' } },
  );

  http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
    headers: authHeaders({}),
    tags: { name: 'task_complete' },
  });
}
