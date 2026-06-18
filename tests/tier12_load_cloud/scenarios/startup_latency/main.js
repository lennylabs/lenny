// SPDX-License-Identifier: MIT
//
// k6 scenario: startup_latency (cloud)
//
// Cloud-load §6.3 startup-latency scenario. Drives POST /v1/sessions/start
// at the LENNY_RATE arrival rate against an SDK-warm pool (a runtime with
// capabilities.preConnect), terminating each session so the pool recycles.
// The cloud tier runs this for the SDK-warm arm of runc, gVisor, and Kata in
// one provisioned cluster, so the per-runtime-class SDK-warm claim-to-ready
// numbers are directly comparable (the §6.3 line 352 complexity-tradeoff
// validation). The runtime class is selected by the isolationProfile on the
// create body, which routes the claim to the per-class SDK-warm pool.
//
// The gate-canonical P95 is read by the Go scaffold from the server
// histogram lenny_session_startup_duration_seconds{runtime_class}; this
// client-side latency is the regression baseline.
//
// Environment (set by tests/tier12_load_cloud/session_startup_latency_test.go):
//   LENNY_BASE_URL          Gateway base URL.
//   LENNY_TENANT            Tenant ID. Default acme.
//   LENNY_ROLES             Roles. Default tenant-admin.
//   LENNY_USER              User ID. Default alice.
//   LENNY_RUNTIME           runtimeRef. The SDK-warm runtime for the arm.
//   LENNY_ISOLATION_PROFILE standard | sandboxed | microvm. Routes the class.
//   LENNY_WARMING_MODEL     Recorded tag. sdk_warm.
//   LENNY_RUNTIME_CLASS     Recorded tag. runc | gvisor | kata.
//   LENNY_RATE              Arrival rate (sessions/sec).
//   LENNY_DURATION          Scenario duration. Default 1m.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'load-preconnect-runtime';
const ISOLATION_PROFILE = __ENV.LENNY_ISOLATION_PROFILE || 'standard';
const WARMING_MODEL = __ENV.LENNY_WARMING_MODEL || 'sdk_warm';
const RUNTIME_CLASS = __ENV.LENNY_RUNTIME_CLASS || 'runc';

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  scenarios: {
    sdk_warm_start: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '1m',
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_failed{name:session_start}': ['rate<0.05'],
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

export default function () {
  const idem = `${__VU}-${__ITER}-${Date.now()}`;
  const create = http.post(
    `${BASE}/v1/sessions/start`,
    JSON.stringify({ runtimeRef: RUNTIME, isolationProfile: ISOLATION_PROFILE }),
    {
      headers: authHeaders({ 'Idempotency-Key': idem }),
      tags: { name: 'session_start', warming_model: WARMING_MODEL, runtime_class: RUNTIME_CLASS },
    },
  );
  if (!check(create, { 'session running': (r) => r.status === 201 }) || !create.body) {
    return;
  }
  const id = JSON.parse(create.body).id;
  if (!id) return;
  http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
    headers: authHeaders({}),
    tags: { name: 'release_pod' },
  });
}
