// SPDX-License-Identifier: MIT
//
// k6 scenario: streaming_throughput
//
// TESTING.md 12.7 target: 500 concurrent streaming sessions. SLO:
// streaming reconnect latency P95 < 500ms; zero event loss.
//
// Phase 6.5 incremental load test (streaming). The scenario drives the
// §15.1 message-streaming round-trip under a rate-bounded executor.
// Each iteration creates a session, injects a message via POST
// /v1/sessions/{id}/messages, and reads the synchronous response. The
// POST returns once the in-process executor has emitted its output
// parts, so the per-request duration is the gateway's
// inject-and-stream-back cost — the throughput surface the §6.5 phase
// gate baselines.
//
// The full SSE consumption path (GET /v1/sessions/{id}/events) is the
// production reconnect target; that surface is blocked on the
// gatewaymetrics middleware Flusher issue and is exercised in the
// streaming_reconnect scenario when the wrapper is fixed.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run holds a modest steady arrival rate; the production sweep
// raises LENNY_RATE.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on the session. Default claude-code.
//   LENNY_RATE       Arrivals per second. Default 5.
//   LENNY_DURATION   Run duration. Default 30s.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'claude-code';

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
    streaming: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '30s',
      preAllocatedVUs: 8,
      maxVUs: 20,
    },
  },
  thresholds: {
    'http_req_duration{name:stream_message}': ['p(95)<500'],
    'http_req_failed': ['rate<0.05'],
  },
};

function authHeaders(extra) {
  return Object.assign(
    {
      'X-Lenny-Tenant-ID': TENANT,
      'X-Lenny-Roles': ROLES,
      'X-Lenny-User-ID': USER,
    },
    extra || {},
  );
}

export default function () {
  // Create the session the streaming-throughput iteration drives.
  const create = http.post(
    `${BASE}/v1/sessions`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    {
      headers: authHeaders({
        'Content-Type': 'application/json',
        'Idempotency-Key': `${__VU}-${__ITER}-${Date.now()}`,
      }),
    },
  );
  if (!check(create, { 'session created': (r) => r.status === 201 })) {
    return;
  }
  const sessionID = JSON.parse(create.body).id;

  // Inject a message and consume the synchronous response. The
  // executor publishes the corresponding `message_delivered` and
  // `response` events on the §15.1 event stream alongside the
  // response body; the POST round-trip's duration is the
  // inject-and-stream-back cost the §6.5 gate baselines.
  const msg = http.post(
    `${BASE}/v1/sessions/${sessionID}/messages`,
    JSON.stringify({ messages: [{ role: 'user', content: 'ping' }] }),
    {
      headers: authHeaders({ 'Content-Type': 'application/json' }),
      tags: { name: 'stream_message' },
    },
  );
  check(msg, {
    'message delivered': (r) => r.status === 200,
    'response carries output': (r) => r.body && r.body.includes('"deliveryStatus"'),
  });
}
