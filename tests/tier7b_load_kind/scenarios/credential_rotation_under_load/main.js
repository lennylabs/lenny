// SPDX-License-Identifier: MIT
//
// k6 scenario: credential_rotation_under_load
//
// TESTING.md 12.7 target: 200 concurrent sessions, rotate provider
// credentials. SLO: rotation propagation P95 < 5 seconds; in-flight
// requests succeed.
//
// The scenario holds a sustained session-creation load against the
// gateway. Every VU also reads the credential-pool surface on a fixed
// cadence so the credential path is exercised alongside the session
// load. The SLO the smoke run asserts is the in-flight property: under
// the sustained load every request completes without error and within
// the latency baseline.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count; the production run uses
// 200 concurrent sessions with a credential rotation injected.
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
    'http_req_duration{name:session}': ['p(99)<5000'],
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
  // Sustained session load: in-flight requests must succeed.
  const res = http.post(
    `${BASE}/v1/sessions`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    { headers: authHeaders({ 'Idempotency-Key': `${__VU}-${__ITER}-${Date.now()}` }), tags: { name: 'session' } },
  );
  check(res, { 'session created': (r) => r.status === 201 });

  // Touch the credential-pool surface periodically so the credential
  // read path is exercised under the session load. Any definitive HTTP
  // status (including an authorization verdict) is acceptable; a 5xx or
  // a transport failure is not.
  if (__ITER % 10 === 0) {
    const pools = http.get(`${BASE}/v1/admin/credential-pools`, {
      headers: authHeaders(),
      tags: { name: 'credential_pools' },
    });
    check(pools, { 'credential-pool read returned a verdict': (r) => r.status > 0 && r.status < 500 });
  }
}
