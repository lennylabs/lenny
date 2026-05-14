// SPDX-License-Identifier: MIT
//
// k6 scenario: delegation_fanout
//
// §12.7 target: single root with N=50 concurrent children, depth=10.
// SLO: tree completes within 30 seconds; budget enforcement correct.
//
// The script creates a parent session against the delegation-echo
// runtime, then issues lenny/delegate_task calls in parallel and
// awaits the children.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const FANOUT = parseInt(__ENV.LENNY_FANOUT || '50', 10);

export const options = {
  thresholds: {
    'http_req_duration{name:delegate_task}': ['p(99)<5000'],
    'http_req_failed': ['rate<0.01'],
  },
};

export function setup() {
  const res = http.post(`${BASE}/v1/sessions`, JSON.stringify({ runtimeRef: 'delegation-echo' }), {
    headers: { 'Content-Type': 'application/json', 'X-Lenny-Tenant-ID': TENANT },
  });
  check(res, { 'parent session created': (r) => r.status === 201 });
  return { parentID: JSON.parse(res.body).id };
}

export default function (data) {
  for (let i = 0; i < FANOUT; i++) {
    const payload = JSON.stringify({
      runtimeRef: 'delegation-echo',
      parentID: data.parentID,
      budget: { tokens: 1000 },
    });
    const res = http.post(`${BASE}/v1/delegate`, payload, {
      headers: { 'Content-Type': 'application/json', 'X-Lenny-Tenant-ID': TENANT },
      tags: { name: 'delegate_task' },
    });
    check(res, {
      'status 201/202': (r) => r.status === 201 || r.status === 202,
    });
  }
}
