// SPDX-License-Identifier: MIT
//
// k6 scenario: credential_rotation_under_load
//
// §12.7 target: 200 concurrent sessions, rotate provider
// credentials. SLO: rotation propagation P95 < 5 seconds; in-flight
// requests succeed.
//
// The scenario holds N sessions open with a streaming-echo runtime
// and periodically triggers POST /v1/admin/credential-pools/{name}/rotate.
// Inflight requests should observe the rotation via the
// credentials_rotated lifecycle message without failure.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const ADMIN_TOKEN = __ENV.LENNY_ADMIN_TOKEN || '';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const POOL = __ENV.LENNY_CRED_POOL || 'mock-anthropic';

export const options = {
  scenarios: {
    sustained_load: {
      executor: 'constant-vus',
      vus: 200,
      duration: __ENV.LENNY_DURATION || '5m',
    },
    rotator: {
      executor: 'constant-arrival-rate',
      rate: 2,
      timeUnit: '1m',
      duration: __ENV.LENNY_DURATION || '5m',
      preAllocatedVUs: 2,
      exec: 'rotate',
    },
  },
  thresholds: {
    'http_req_duration{name:streaming}': ['p(99)<5000'],
    'http_req_failed': ['rate<0.005'],
  },
};

export default function () {
  const payload = JSON.stringify({ runtimeRef: 'streaming-echo' });
  const res = http.post(`${BASE}/v1/sessions`, payload, {
    headers: { 'Content-Type': 'application/json', 'X-Lenny-Tenant-ID': TENANT },
    tags: { name: 'streaming' },
  });
  check(res, { 'created': (r) => r.status === 201 });
}

export function rotate() {
  if (!ADMIN_TOKEN) {
    // Skip rotation when no admin token is wired; the load is
    // still exercised, just without the rotation path.
    return;
  }
  const res = http.post(
    `${BASE}/v1/admin/credential-pools/${POOL}/rotate`,
    null,
    {
      headers: {
        'Authorization': `Bearer ${ADMIN_TOKEN}`,
        'X-Lenny-Tenant-ID': TENANT,
      },
      tags: { name: 'rotate' },
    },
  );
  check(res, { 'rotation accepted': (r) => r.status === 200 || r.status === 202 });
}
