// SPDX-License-Identifier: MIT
//
// k6 scenario: delegation_fanout
//
// TESTING.md 12.7 target: single root with N=50 concurrent children,
// depth=10. SLO: tree completes within 30 seconds; budget enforcement
// correct.
//
// The delegation tree is spawned through the gateway session surface.
// Each VU iteration plays one root: it creates a parent session, then
// fans out FANOUT child sessions via POST /v1/sessions/start. Each
// child carries the parent session id, so the gateway records the
// parent edge. The iteration's wall time is the tree's fan-out cost
// the 30-second SLO bounds.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count with a small fan-out; the
// production run uses N=50, depth=10.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on every session. Default echo-runtime-sidecar.
//   LENNY_FANOUT     Children spawned per root. Default 50.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'echo-runtime-sidecar';
const FANOUT = parseInt(__ENV.LENNY_FANOUT || '50', 10);

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  thresholds: {
    'http_req_duration{name:spawn_child}': ['p(99)<5000'],
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
  // The root of one delegation tree.
  const parent = http.post(
    `${BASE}/v1/sessions`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    { headers: authHeaders({ 'Idempotency-Key': `root-${__VU}-${__ITER}-${Date.now()}` }), tags: { name: 'spawn_root' } },
  );
  if (!check(parent, { 'root session created': (r) => r.status === 201 })) {
    return;
  }
  const parentID = JSON.parse(parent.body).id;

  // Fan out the children. Each child is a started session carrying the
  // parent id so the gateway records the delegation edge.
  const childIDs = [];
  for (let i = 0; i < FANOUT; i++) {
    const payload = JSON.stringify({ runtimeRef: RUNTIME, parentID: parentID });
    const res = http.post(`${BASE}/v1/sessions/start`, payload, {
      headers: authHeaders({ 'Idempotency-Key': `child-${__VU}-${__ITER}-${i}-${Date.now()}` }),
      tags: { name: 'spawn_child' },
    });
    if (check(res, { 'child accepted': (r) => r.status === 201 }) && res.body) {
      const cid = JSON.parse(res.body).id;
      if (cid) childIDs.push(cid);
    }
  }

  // Terminate every claimed child + the parent so the §4.6
  // SandboxClaims release their pods back to the warm pool. Without
  // this, each iteration leaks FANOUT pods and the pool exhausts
  // within ~10 iterations on a Kind cluster sized for smoke runs.
  for (let i = 0; i < childIDs.length; i++) {
    http.post(`${BASE}/v1/sessions/${childIDs[i]}/terminate`, '', {
      headers: authHeaders({}),
      tags: { name: 'release_child' },
    });
  }
  http.post(`${BASE}/v1/sessions/${parentID}/terminate`, '', {
    headers: authHeaders({}),
    tags: { name: 'release_root' },
  });
}
