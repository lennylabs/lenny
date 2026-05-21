// SPDX-License-Identifier: MIT
//
// k6 scenario: delegation_fanout_mcp
//
// TESTING.md 12.7 target: single root with N=50 concurrent children,
// depth=10. SLO: tree completes within 30 seconds; budget enforcement
// correct.
//
// Phase 9.5 incremental load test (delegation). The scenario drives the
// §8.2 `lenny/delegate_task` MCP tool — the production delegation
// surface — under a rate-bounded executor. The companion REST scenario
// (delegation_fanout) drives POST /v1/sessions/start directly; this one
// drives JSON-RPC POST /mcp tools/call so the MCP delegation hot path
// is exercised at the §9.5 phase gate.
//
// Each iteration creates a parent session, then fans out FANOUT child
// `lenny/delegate_task` calls. The per-call duration is the MCP
// delegation-spawn cost the §8.2 SLO bounds.
//
// The Tier-7 Go wrapper picks rate and duration. The PR-cadence smoke
// run holds a modest steady arrival rate with a small fan-out so the
// e2e gateway is not flooded; the production run uses N=50, depth=10.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on the parent and the child task.
//                    Default echo-runtime-sidecar.
//   LENNY_FANOUT     Children spawned per root. Default 5.
//   LENNY_RATE       Arrivals per second. Default 5.
//   LENNY_DURATION   Run duration. Default 30s.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'echo-runtime-sidecar';
const FANOUT = parseInt(__ENV.LENNY_FANOUT || '5', 10);

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // A rate-bounded executor. An unbounded VU loop spins into hundreds
  // of thousands of requests when a request fails fast, saturating the
  // kubectl port-forward the Tier-7 wrapper reaches the gateway
  // through. The smoke run holds a modest steady arrival rate; the
  // production sweep raises LENNY_RATE.
  scenarios: {
    delegation: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '30s',
      preAllocatedVUs: 8,
      maxVUs: 20,
    },
  },
  thresholds: {
    'http_req_duration{name:mcp_delegate}': ['p(99)<5000'],
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

// mcpCall issues one tools/call JSON-RPC request through POST /mcp.
function mcpCall(name, args, tagName) {
  const body = JSON.stringify({
    jsonrpc: '2.0',
    id: `${__VU}-${__ITER}-${tagName}-${Date.now()}`,
    method: 'tools/call',
    params: { name: name, arguments: args },
  });
  return http.post(`${BASE}/mcp`, body, {
    headers: authHeaders(),
    tags: { name: tagName },
  });
}

export default function () {
  // The parent session. The delegate_task tool requires a running
  // parent session id so the child carries the §8.2 lineage edge.
  const parent = http.post(
    `${BASE}/v1/sessions/start`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    {
      headers: authHeaders({
        'Idempotency-Key': `root-${__VU}-${__ITER}-${Date.now()}`,
      }),
      tags: { name: 'spawn_root' },
    },
  );
  if (!check(parent, { 'root session created': (r) => r.status === 201 })) {
    return;
  }
  const parentID = JSON.parse(parent.body).id;

  // Fan out FANOUT children through the MCP delegate_task tool. Each
  // call's duration is the §8.2 MCP delegation-spawn cost.
  const childIDs = [];
  for (let i = 0; i < FANOUT; i++) {
    const res = mcpCall(
      'lenny/delegate_task',
      {
        parentSessionId: parentID,
        runtimeRef: RUNTIME,
        taskInput: `child-${i}`,
      },
      'mcp_delegate',
    );
    if (
      check(res, {
        'mcp delegate returned 200': (r) => r.status === 200,
        'mcp delegate has no error': (r) => r.body && !r.body.includes('"error"'),
      }) && res.body
    ) {
      // delegate_task's content text carries the child session id.
      const m = res.body.match(/"sessionId"\s*:\s*"([^"]+)"/);
      if (m) childIDs.push(m[1]);
    }
  }

  // Release each claimed child + the parent so the §4.6 SandboxClaims
  // free their pods back to the warm pool. Without this each iteration
  // leaks 1 + FANOUT pod claims and the pool exhausts within a handful
  // of iterations on a smoke-sized Kind cluster — production callers
  // terminate sessions when done, so the smoke does the same.
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
