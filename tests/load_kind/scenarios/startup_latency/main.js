// SPDX-License-Identifier: MIT
//
// k6 scenario: startup_latency
//
// Spec cross-reference: spec/06_warm-pod-model.md §6.3. The pod-warm
// startup-latency SLO is P95 claim-to-ready (2s runc / 5s gVisor) against
// an already-warm pod. §6.3 also requires the Phase 2 benchmark harness to
// measure pod-warm vs SDK-warm latency per runtime class to validate the
// SDK-warm complexity tradeoff. This scenario drives the session-start path
// so the harness measures real session start rather than a host-process
// heartbeat round-trip (the prior main.go measured the latter).
//
// POST /v1/sessions/start is the create-and-start path: the gateway creates
// the session, claims a warm pod, and completes the agent-session-start RPC
// before it returns 201. The client-side latency is therefore the
// claim-to-ready envelope (pod claim + credential assignment + agent session
// start, plus workspace materialization and any setup commands). The arms
// drive empty-workspace, no-setup runtimes (echo / preconnect-echo) so the
// client envelope stays close to the §6.3 SLO envelope.
//
// Two measurements come out of a run:
//   - This client-side wall-clock, recorded as the per-arm baseline.
//   - The server histogram lenny_session_startup_duration_seconds{runtime_class},
//     which the Go scaffold scrapes from the gateway /metrics endpoint. That
//     metric is PodClaim + CredentialAssignment + AgentSessionStart (it
//     excludes workspace materialization and setup), so it is the gate-
//     canonical 2s/5s SLO number the runc/gVisor arms assert against.
//
// The warming model (pod-warm vs SDK-warm) and runtime class are selected by
// the pool that LENNY_RUNTIME points at; LENNY_WARMING_MODEL and
// LENNY_RUNTIME_CLASS are recorded as k6 tags so the summary carries the arm
// identity. The Tier-7 Go wrapper picks duration and VU count.
//
// Environment:
//   LENNY_BASE_URL        Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT          Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES           Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER            User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME         runtimeRef on the create body. Default echo-runtime-sidecar.
//   LENNY_WARMING_MODEL   Recorded tag: pod_warm | sdk_warm. Default pod_warm.
//   LENNY_RUNTIME_CLASS   Recorded tag: runc | gvisor | kata. Default runc.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'echo-runtime-sidecar';
const WARMING_MODEL = __ENV.LENNY_WARMING_MODEL || 'pod_warm';
const RUNTIME_CLASS = __ENV.LENNY_RUNTIME_CLASS || 'runc';

export const options = {
  // §6.3 SLO is P95-keyed; p99 is omitted from the baseline doc-comment
  // discipline (it needs ~1000+ samples to defend at one-sample
  // resolution). The summary still emits p99 for the regression corpus,
  // matching the sibling scenarios.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
  thresholds: {
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
  const payload = JSON.stringify({ runtimeRef: RUNTIME });
  const params = {
    headers: authHeaders({ 'Idempotency-Key': idem }),
    tags: { name: 'session_start', warming_model: WARMING_MODEL, runtime_class: RUNTIME_CLASS },
  };
  const res = http.post(`${BASE}/v1/sessions/start`, payload, params);
  check(res, {
    'status is 201': (r) => r.status === 201,
    'session running': (r) => r.body && r.body.includes('"running"'),
  });
  // Terminate so the §4.6 SandboxClaim releases its pod back to the warm
  // pool. Without this each iteration leaks a claimed pod and the
  // smoke-sized pool exhausts within a handful of iterations.
  if (res.status === 201 && res.body) {
    const id = JSON.parse(res.body).id;
    if (id) {
      http.post(`${BASE}/v1/sessions/${id}/terminate`, '', {
        headers: authHeaders({ 'Content-Type': 'application/json' }),
        tags: { name: 'release_pod' },
      });
    }
  }
}
