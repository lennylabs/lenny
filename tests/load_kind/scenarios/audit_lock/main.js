// SPDX-License-Identifier: MIT
//
// k6 scenario: audit_lock
//
// TESTING.md 12.7 target: 1000 audit-event writes/sec at single-tenant
// burst. SLO: pg_advisory_xact_lock P95 < 50ms.
//
// Every admin write emits a 11.7 admin audit event. PUT
// /v1/admin/tenants/{id} updates one tenant and emits an
// admin.tenant.updated event; repeating it against a single tenant is
// a single-tenant audit-write burst that contends on the per-tenant
// advisory lock. The scenario drives that endpoint at the VU count the
// Go wrapper picks and baselines the admin-write latency.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count; the production run drives
// the 1000-writes/sec single-tenant burst with the advisory-lock SLO
// asserted.
//
// Environment:
//   LENNY_BASE_URL    Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_ADMIN_TENANT  Tenant for X-Lenny-Tenant-ID. Default platform.
//   LENNY_ADMIN_ROLES   Roles for X-Lenny-Roles. Default platform-admin.
//   LENNY_ADMIN_USER    User ID for X-Lenny-User-ID. Default alice.
//   LENNY_TARGET_TENANT The tenant whose row is updated. Default acme.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const ADMIN_TENANT = __ENV.LENNY_ADMIN_TENANT || 'platform';
const ADMIN_ROLES = __ENV.LENNY_ADMIN_ROLES || 'platform-admin';
const ADMIN_USER = __ENV.LENNY_ADMIN_USER || 'alice';
const TARGET = __ENV.LENNY_TARGET_TENANT || 'acme';

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  thresholds: {
    'http_req_duration{name:audit_write}': ['p(95)<2000'],
    'http_req_failed': ['rate<0.01'],
  },
};

function authHeaders(extra) {
  return Object.assign(
    {
      'Content-Type': 'application/json',
      'X-Lenny-Tenant-ID': ADMIN_TENANT,
      'X-Lenny-Roles': ADMIN_ROLES,
      'X-Lenny-User-ID': ADMIN_USER,
    },
    extra || {},
  );
}

export default function () {
  // Each PUT emits one admin.tenant.updated audit event for the same
  // tenant, contending on that tenant's advisory lock.
  const res = http.put(
    `${BASE}/v1/admin/tenants/${TARGET}`,
    JSON.stringify({ displayName: `audit-lock-burst-vu${__VU}` }),
    { headers: authHeaders(), tags: { name: 'audit_write' } },
  );
  check(res, { 'audit write accepted': (r) => r.status === 200 });
}
