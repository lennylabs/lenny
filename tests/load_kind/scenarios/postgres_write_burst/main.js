// SPDX-License-Identifier: MIT
//
// k6 scenario: postgres_write_burst
//
// TESTING.md 12.7 target: quota-flush burst pattern at Tier 3. SLO:
// sustained IOPS within postgres.writeCeilingIops; burst within 3x
// ceiling.
//
// Every POST /v1/sessions commits a row to the Postgres sessions
// table, so a burst of session creation is a Postgres write burst
// driven through the gateway. The scenario drives that write path at
// the VU count the Go wrapper picks and baselines the write latency.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count; the production run uses the
// Tier-3 quota-flush burst pattern with the IOPS ceiling asserted.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on every session. Default echo-runtime-sidecar.

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
    'http_req_duration{name:pg_write}': ['p(95)<2000'],
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
  // One session row committed to Postgres per iteration.
  const res = http.post(
    `${BASE}/v1/sessions`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    { headers: authHeaders({ 'Idempotency-Key': `${__VU}-${__ITER}-${Date.now()}` }), tags: { name: 'pg_write' } },
  );
  check(res, { 'row committed': (r) => r.status === 201 });
}
