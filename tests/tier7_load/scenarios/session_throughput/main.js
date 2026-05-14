// SPDX-License-Identifier: MIT
//
// k6 scenario: session_throughput
//
// §12.7 target: ramp to 500 concurrent on Kind / 5000 on cloud,
// sustained 10 minutes. SLO: session creation P99 < 500ms; pod
// startup P95 < 2s (runc) and < 5s (gVisor).
//
// The script POSTs /v1/sessions repeatedly with a fresh
// idempotency key. The base URL comes from LENNY_BASE_URL (default
// http://127.0.0.1:8080). Tier-7 wrapper picks duration / VUs.
//
// Usage:
//   k6 run --vus 500 --duration 10m \
//          --env LENNY_BASE_URL=http://gateway:8080 \
//          tests/tier7_load/scenarios/session_throughput/main.js

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';

export const options = {
  thresholds: {
    'http_req_duration{name:create_session}': ['p(99)<500'],
    'http_req_failed': ['rate<0.01'],
  },
};

export default function () {
  const idem = `${__VU}-${__ITER}-${Date.now()}`;
  const payload = JSON.stringify({
    runtimeRef: 'claude-code',
    userId: `vu-${__VU}@acme.com`,
  });
  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Idempotency-Key': idem,
      'X-Lenny-Tenant-ID': TENANT,
    },
    tags: { name: 'create_session' },
  };
  const res = http.post(`${BASE}/v1/sessions`, payload, params);
  check(res, {
    'status is 201': (r) => r.status === 201,
    'has id': (r) => r.body && r.body.includes('"id"'),
  });
}
