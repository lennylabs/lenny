// SPDX-License-Identifier: MIT
//
// k6 scenario: cworkspace_fanout_mcp
//
// Cloud-load §5.2 + §8.2 concurrent-workspace fan-out scenario.
// Each iteration creates a parent session on the concurrent-
// workspace pool and fans out LENNY_FANOUT children through the
// MCP lenny/delegate_task tool. Children land on the same pool;
// they compete for slots, exercising slot scheduling under burst
// arrival.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-cworkspace-runtime';
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
    'http_req_duration{name:mcp_delegate}': ['p(99)<3000'],
    'http_req_failed{name:mcp_delegate}': ['rate<0.05'],
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

function mcpCall(args, tagName) {
  const body = JSON.stringify({
    jsonrpc: '2.0',
    id: `${__VU}-${__ITER}-${tagName}-${Date.now()}`,
    method: 'tools/call',
    params: { name: 'lenny/delegate_task', arguments: args },
  });
  return http.post(`${BASE}/mcp`, body, { headers: authHeaders({}), tags: { name: tagName } });
}

export default function () {
  const parent = http.post(
    `${BASE}/v1/sessions/start`,
    JSON.stringify({ runtimeRef: RUNTIME, isolationProfile: 'standard' }),
    { headers: authHeaders({ 'Idempotency-Key': `root-${__VU}-${__ITER}-${Date.now()}` }), tags: { name: 'spawn_root' } },
  );
  if (!check(parent, { 'root created': (r) => r.status === 201 }) || !parent.body) return;
  const parentID = JSON.parse(parent.body).id;

  const childIDs = [];
  for (let i = 0; i < FANOUT; i++) {
    const res = mcpCall(
      { parentSessionId: parentID, runtimeRef: RUNTIME, taskInput: `child-${i}` },
      'mcp_delegate',
    );
    if (check(res, { 'mcp ok': (r) => r.status === 200 }) && res.body) {
      const m = res.body.match(/"sessionId"\s*:\s*"([^"]+)"/);
      if (m) childIDs.push(m[1]);
    }
  }

  for (let i = 0; i < childIDs.length; i++) {
    http.post(`${BASE}/v1/sessions/${childIDs[i]}/terminate`, '', { headers: authHeaders({}), tags: { name: 'release_child' } });
  }
  http.post(`${BASE}/v1/sessions/${parentID}/terminate`, '', { headers: authHeaders({}), tags: { name: 'release_root' } });
}
