// SPDX-License-Identifier: MIT
//
// k6 scenario: credential_lifecycle
//
// TESTING.md 12.7 target: 200 concurrent sessions, rotate provider
// credentials. SLO: rotation propagation P95 < 5 seconds; in-flight
// requests succeed.
//
// Phase 11.5 incremental load test (credential lifecycle). The
// scenario drives the §15.1 /v1/credentials end-user surface — the
// user-owned credential lifecycle — under a rate-bounded executor.
// Each iteration plays one full lifecycle: register → list → rotate
// → revoke → delete. The per-call duration is the credential
// write-path cost the §11.5 phase gate baselines, and the iteration's
// wall time is the §4.9 lifecycle throughput.
//
// The companion §11.3 credential_rotation_under_load scenario holds a
// sustained session-creation load with a periodic credential-pool
// read; this scenario exercises the write-side lifecycle so the §4.9
// register / rotate / revoke / delete path is baselined alongside the
// read path.
//
// The Tier-7 Go wrapper picks rate and duration. The PR-cadence smoke
// run holds a modest steady arrival rate; the production run is the
// 200-concurrent sweep.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_PROVIDER   Provider on the §4.9 enum. Default anthropic_direct.
//   LENNY_RATE       Arrivals per second. Default 5.
//   LENNY_DURATION   Run duration. Default 30s.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const PROVIDER = __ENV.LENNY_PROVIDER || 'anthropic_direct';

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // A rate-bounded executor. An unbounded VU loop spins into hundreds
  // of thousands of requests when a request fails fast, saturating the
  // kubectl port-forward the Tier-7 wrapper reaches the gateway
  // through.
  scenarios: {
    lifecycle: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '30s',
      preAllocatedVUs: 8,
      maxVUs: 20,
    },
  },
  thresholds: {
    'http_req_duration{name:cred_register}': ['p(95)<2000'],
    'http_req_duration{name:cred_rotate}':   ['p(95)<2000'],
    'http_req_duration{name:cred_revoke}':   ['p(95)<2000'],
    'http_req_failed': ['rate<0.05'],
  },
};

function authHeaders(user, extra) {
  return Object.assign(
    {
      'Content-Type': 'application/json',
      'X-Lenny-Tenant-ID': TENANT,
      'X-Lenny-Roles': ROLES,
      'X-Lenny-User-ID': user,
    },
    extra || {},
  );
}

// secretFor returns a per-iteration synthetic credential secret. The
// payload only needs to be a non-empty string; the credentialstore
// hashes it and never echoes it on the wire.
function secretFor(label) {
  return `secret-${__VU}-${__ITER}-${label}-${Date.now()}`;
}

// iterUser returns a per-iteration synthetic user id. The
// credentialstore deduplicates by (tenant, user, provider): two
// concurrent iterations that share a user would race on the same
// credential row, producing spurious 404s on delete. The per-iteration
// id gives each iteration its own credential namespace so the
// lifecycle reads cleanly.
function iterUser() {
  return `${USER}-vu${__VU}-i${__ITER}`;
}

export default function () {
  // Register a fresh credential. The §15.1 response carries the ref
  // the rest of the iteration drives.
  const user = iterUser();
  const reg = http.post(
    `${BASE}/v1/credentials`,
    JSON.stringify({ provider: PROVIDER, secret: secretFor('initial') }),
    { headers: authHeaders(user), tags: { name: 'cred_register' } },
  );
  if (!check(reg, { 'credential registered': (r) => r.status === 201 })) {
    return;
  }
  const ref = JSON.parse(reg.body).ref;

  // Touch the list endpoint so the read path is exercised alongside the
  // write lifecycle.
  const list = http.get(`${BASE}/v1/credentials`, {
    headers: authHeaders(user),
    tags: { name: 'cred_list' },
  });
  check(list, { 'credential list returned a verdict': (r) => r.status > 0 && r.status < 500 });

  // Rotate the credential (PUT /v1/credentials/{ref}). The handler
  // re-hashes the new secret and updates the rotatedAt timestamp.
  const rot = http.put(
    `${BASE}/v1/credentials/${ref}`,
    JSON.stringify({ secret: secretFor('rotated') }),
    { headers: authHeaders(user), tags: { name: 'cred_rotate' } },
  );
  check(rot, { 'credential rotated': (r) => r.status === 200 });

  // Revoke the credential (POST /v1/credentials/{ref}/revoke). The
  // handler flips status to `revoked` and stamps revokedAt.
  const rev = http.post(
    `${BASE}/v1/credentials/${ref}/revoke`,
    null,
    { headers: authHeaders(user), tags: { name: 'cred_revoke' } },
  );
  check(rev, { 'credential revoked': (r) => r.status === 200 });

  // Delete the revoked row to close the §4.9 lifecycle.
  const del = http.del(`${BASE}/v1/credentials/${ref}`, null, {
    headers: authHeaders(user),
    tags: { name: 'cred_delete' },
  });
  check(del, { 'credential deleted': (r) => r.status === 200 || r.status === 204 });
}
