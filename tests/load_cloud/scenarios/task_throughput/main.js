// SPDX-License-Identifier: MIT
//
// k6 scenario: task_throughput
//
// Cloud-load §5.2 + §12.7 task-mode throughput scenario. Drives
// one-shot task execution against the task pool. The gateway claims
// a free task-mode pod, runs the task as a session, scrubs the
// workspace, and returns the pod to the pool (subject to
// §13.1 maxTasksPerPod). Each iteration is one task; the rate
// measures task ingestion + completion throughput.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-task-runtime';

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  scenarios: {
    task_throughput: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '1m',
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_duration{name:run_task}': ['p(99)<5000'],
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
  // Task-mode session: create + message + terminate is one task. The
  // gateway picks a task-pool pod for the create, runs the message
  // synchronously, and scrubs on terminate.
  const create = http.post(
    `${BASE}/v1/sessions/start`,
    JSON.stringify({ runtimeRef: RUNTIME, isolationProfile: 'standard' }),
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
