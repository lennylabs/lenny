// SPDX-License-Identifier: MIT
//
// k6 scenario: session_throughput
//
// TESTING.md 12.7 target: ramp to 500 concurrent on Kind / 5000 on
// cloud, sustained 10 minutes. SLO: session creation P99 < 500ms; pod
// startup P95 < 2s (runc) and < 5s (gVisor).
//
// The script POSTs /v1/sessions repeatedly with a fresh idempotency
// key. Each request carries the dev-mode auth headers the e2e gateway
// honours (X-Lenny-Tenant-ID / X-Lenny-Roles / X-Lenny-User-ID).
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count; the production run uses
// the 12.7 ramp.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on the create body. Default echo-runtime-sidecar.
//
// Usage:
//   k6 run --vus 500 --duration 10m \
//          --env LENNY_BASE_URL=http://gateway:8080 \
//          tests/tier7_load/scenarios/session_throughput/main.js

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'echo-runtime-sidecar';

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  thresholds: {
    'http_req_duration{name:create_session}': ['p(99)<500'],
    'http_req_failed': ['rate<0.01'],
  },
};

function authHeaders(extra) {
  return Object.assign(
    {
      'Content-Type': 'application/json',
      'X-Lenny-Tenant-ID': TENANT,
      'X-Lenny-Roles': ROLES,
      'X-Lenny-User-ID': USER,
    },
    extra || {},
  );
}

export default function () {
  const idem = `${__VU}-${__ITER}-${Date.now()}`;
  const payload = JSON.stringify({
    runtimeRef: RUNTIME,
    userId: `vu-${__VU}@${TENANT}.com`,
  });
  const params = {
    headers: authHeaders({ 'Idempotency-Key': idem }),
    tags: { name: 'create_session' },
  };
  const res = http.post(`${BASE}/v1/sessions`, payload, params);
  check(res, {
    'status is 201': (r) => r.status === 201,
    'has id': (r) => r.body && r.body.includes('"id"'),
  });
}
