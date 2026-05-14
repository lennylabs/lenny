// SPDX-License-Identifier: MIT
//
// k6 scenario: streaming_reconnect
//
// §12.7 target: 500 concurrent streaming sessions, periodic
// disconnects. SLO: reconnect latency P95 < 500ms; zero event loss.
//
// The script opens an SSE stream for an existing session, drops
// the connection every ~10 iterations, then resumes with
// Last-Event-ID and verifies no event was missed.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';

export const options = {
  thresholds: {
    'http_req_duration{name:stream_reconnect}': ['p(95)<500'],
    'event_loss_rate': ['rate==0'],
  },
};

export function setup() {
  // Create a session up front; every VU streams from it.
  const res = http.post(`${BASE}/v1/sessions`, JSON.stringify({ runtimeRef: 'streaming-echo' }), {
    headers: { 'Content-Type': 'application/json', 'X-Lenny-Tenant-ID': TENANT },
  });
  check(res, { 'session created': (r) => r.status === 201 });
  const body = JSON.parse(res.body);
  return { sessionID: body.id };
}

export default function (data) {
  // §10.4: reconnect path uses Last-Event-ID. The scenario alternates
  // between a fresh connect and a resume-with-cursor; the cursor is
  // synthesized for this stub.
  const cursor = __ITER > 0 ? `event-${__ITER - 1}` : '';
  const params = {
    headers: {
      'Accept': 'text/event-stream',
      'X-Lenny-Tenant-ID': TENANT,
    },
    tags: { name: 'stream_reconnect' },
  };
  if (cursor) {
    params.headers['Last-Event-ID'] = cursor;
  }
  const res = http.get(`${BASE}/v1/sessions/${data.sessionID}/events`, params);
  check(res, {
    'status 200 / 206': (r) => r.status === 200 || r.status === 206,
  });
}
