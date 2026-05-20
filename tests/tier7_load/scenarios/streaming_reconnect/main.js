// SPDX-License-Identifier: MIT
//
// k6 scenario: streaming_reconnect
//
// TESTING.md 12.7 target: 500 concurrent streaming sessions, periodic
// disconnects. SLO: streaming reconnect latency P95 < 500ms; zero
// event loss.
//
// setup() creates one session every VU streams from. Each iteration
// opens a GET /v1/sessions/{id}/events SSE connection. k6's HTTP
// client reads the response and closes it when the per-request
// timeout elapses, which simulates the disconnect. Odd iterations
// reconnect with a Last-Event-ID cursor (the 10.4 resume path); even
// iterations open a fresh stream. The request duration is the
// connect/reconnect latency the SLO bounds.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count; the production run uses
// 500 concurrent streaming sessions.
//
// Environment:
//   LENNY_BASE_URL   Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT     Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES      Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER       User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME    runtimeRef on the session. Default claude-code.

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'claude-code';

// k6 records http_req_failed=true for every request whose timeout
// fires, even when the server sent the 200 status header on time. A
// long-lived SSE stream always times out by design, so the built-in
// failure rate is unusable here. The streamOpenRate / streamConnectMs
// custom metrics carry the smoke signal instead: streamOpenRate=1
// when the response status is 200 or 206, streamConnectMs records
// http_req_waiting (time to first byte) as the connect latency the
// §12.7 reconnect SLO bounds.
const streamOpenRate = new Rate('stream_opened');
const streamConnectMs = new Trend('stream_connect_ms', true);

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  thresholds: {
    // Time to first byte stays under the §12.7 P95 reconnect budget.
    'stream_connect_ms': ['p(95)<500'],
    // At least 95 % of reconnects return a 200 / 206 status.
    'stream_opened': ['rate>0.95'],
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

export function setup() {
  // Create one session up front; every VU streams from it.
  const res = http.post(
    `${BASE}/v1/sessions`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    { headers: authHeaders({ 'Content-Type': 'application/json' }) },
  );
  check(res, { 'session created': (r) => r.status === 201 });
  return { sessionID: JSON.parse(res.body).id };
}

export default function (data) {
  // Alternate between a fresh connect and a resume-with-cursor. The
  // 10.4 reconnect path carries Last-Event-ID; the cursor is the last
  // sequence number the VU observed (synthesised here from __ITER).
  const params = {
    headers: authHeaders({ Accept: 'text/event-stream' }),
    tags: { name: 'stream_reconnect' },
    // A short read window: the connection is opened, the reconnect
    // latency is measured, then k6 closes it (the periodic disconnect).
    timeout: '2s',
  };
  if (__ITER % 2 === 1) {
    params.headers['Last-Event-ID'] = `${__ITER - 1}`;
  }
  const res = http.get(`${BASE}/v1/sessions/${data.sessionID}/events`, params);
  const opened = res.status === 200 || res.status === 206;
  streamOpenRate.add(opened);
  if (opened) {
    streamConnectMs.add(res.timings.waiting);
  }
  check(res, {
    'stream opened': () => opened,
  });
}
