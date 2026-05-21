// SPDX-License-Identifier: MIT
//
// k6 scenario: experiment_load
//
// TESTING.md 13.34 target: Phase 16.5 baseline for the §10.7
// ExperimentRouter hot path. SLO: bucketing adds < 5ms to the session
// create-and-claim path; variant-pool sizing keeps the per-variant
// claim P99 within the §12.7 pod_claim_latency budget.
//
// Phase 16.5 incremental load re-baseline (experiments). The scenario
// drives POST /v1/sessions/start at a steady arrival rate. When the
// gateway has an active experiment configured for the caller's tenant
// whose `baseRuntime` matches the request's runtimeRef, the §10.7
// ExperimentRouter buckets the session into a variant pool and may
// rewrite the runtime / pool on the row before the create-and-claim
// commit. The per-request duration is the bucketed claim cost the
// §16.5 phase gate baselines; the per-variant breakdown is observable
// from the gateway's `lenny_experiment_assignments_total` counter.
//
// The Tier-7 Go wrapper picks rate and duration. The PR-cadence smoke
// run holds a modest steady arrival rate; the production sweep uses
// the §12.7 pod_claim_latency 100-concurrent profile with an active
// experiment seeded.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on the create body. Default echo-runtime-sidecar.
//                    Must equal the active experiment's baseRuntime so
//                    the ExperimentRouter buckets the session.
//   LENNY_RATE       Arrivals per second. Default 5.
//   LENNY_DURATION   Run duration. Default 30s.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'echo-runtime-sidecar';

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at and the
  // §13.34 phase gate compares.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // A rate-bounded executor. An unbounded VU loop spins into hundreds
  // of thousands of requests when a request fails fast, saturating the
  // kubectl port-forward the Tier-7 wrapper reaches the gateway
  // through.
  scenarios: {
    bucketed: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '30s',
      preAllocatedVUs: 8,
      maxVUs: 20,
    },
  },
  thresholds: {
    'http_req_duration{name:claim_pod_bucketed}': ['p(99)<200'],
    'http_req_failed': ['rate<0.05'],
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
  const payload = JSON.stringify({ runtimeRef: RUNTIME });
  const params = {
    headers: authHeaders({ 'Idempotency-Key': idem }),
    tags: { name: 'claim_pod_bucketed' },
  };
  const res = http.post(`${BASE}/v1/sessions/start`, payload, params);
  check(res, {
    'status is 201': (r) => r.status === 201,
    'session running': (r) => r.body && r.body.includes('"running"'),
  });
}
