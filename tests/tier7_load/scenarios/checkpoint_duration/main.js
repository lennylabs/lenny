// SPDX-License-Identifier: MIT
//
// k6 scenario: checkpoint_duration
//
// TESTING.md 12.7 target: workspaces at 10 MB, 100 MB, 500 MB. SLO:
// 100MB-or-smaller checkpoint P95 < 2s with cooperative quiescence
// overhead included.
//
// A checkpoint persists the session workspace to the blob store. The
// gateway's POST /v1/sessions/{id}/upload endpoint is the workspace-
// bytes path: it streams a workspace blob into the blob store under
// the session prefix. The scenario creates a session, then uploads a
// workspace blob of the configured size and measures the upload
// duration, which is the workspace-persistence cost the checkpoint SLO
// bounds.
//
// The Tier-7 Go wrapper picks duration and VU count. The PR-cadence
// smoke run is ~60 seconds at low VU count with a small workspace; the
// production run sweeps 10/100/500 MB.
//
// Environment:
//   LENNY_BASE_URL          Gateway base URL. Default http://127.0.0.1:8080.
//   LENNY_TENANT            Tenant ID for X-Lenny-Tenant-ID. Default acme.
//   LENNY_ROLES             Roles for X-Lenny-Roles. Default tenant-admin.
//   LENNY_USER              User ID for X-Lenny-User-ID. Default alice.
//   LENNY_RUNTIME           runtimeRef on the session. Default claude-code.
//   LENNY_WORKSPACE_BYTES   Workspace blob size. Default 1 MB.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const ROLES = __ENV.LENNY_ROLES || 'tenant-admin';
const USER = __ENV.LENNY_USER || 'alice';
const RUNTIME = __ENV.LENNY_RUNTIME || 'claude-code';
const SIZE = parseInt(__ENV.LENNY_WORKSPACE_BYTES || '1048576', 10); // 1 MB default

export const options = {
  // Emit p99 and p99.9 in the summary export so the Tier-7 baseline
  // diff has the percentiles the §12.7 SLOs are stated at.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // A rate-bounded executor. An unbounded VU loop spins into hundreds
  // of thousands of requests when a request fails fast, saturating the
  // kubectl port-forward the Tier-7 wrapper reaches the gateway
  // through. The smoke run holds a modest, steady arrival rate; the
  // production sweep raises LENNY_RATE.
  scenarios: {
    checkpoint: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.LENNY_RATE || '5', 10),
      timeUnit: '1s',
      duration: __ENV.LENNY_DURATION || '30s',
      preAllocatedVUs: 8,
      maxVUs: 20,
    },
  },
  thresholds: {
    'http_req_duration{name:checkpoint}': ['p(95)<2000'],
    'http_req_failed': ['rate<0.01'],
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

// The workspace blob. Built once per VU at init so the per-iteration
// cost is the upload, not the allocation.
const WORKSPACE = 'a'.repeat(SIZE);

export default function () {
  // Create the session and capture its single-use uploadToken.
  const create = http.post(
    `${BASE}/v1/sessions`,
    JSON.stringify({ runtimeRef: RUNTIME }),
    { headers: authHeaders({ 'Content-Type': 'application/json', 'Idempotency-Key': `${__VU}-${__ITER}-${Date.now()}` }) },
  );
  if (!check(create, { 'session created': (r) => r.status === 201 })) {
    // Surface the first 256 bytes of the response body on the
    // failing create so the §15.1 error envelope (code / message)
    // is visible in the k6 stderr; otherwise the scenario reports
    // only the HTTP status. Use console.error so the message
    // reaches stderr without inflating the summary JSON.
    console.error(`create failed: status=${create.status} body=${(create.body || '').slice(0, 256)}`);
    return;
  }
  const body = JSON.parse(create.body);
  const sessionID = body.id;
  const uploadToken = body.uploadToken;

  // Persist the workspace blob. The upload streams the bytes into the
  // blob store; its duration is the checkpoint-persistence cost.
  const ckpt = http.post(
    `${BASE}/v1/sessions/${sessionID}/upload`,
    WORKSPACE,
    {
      headers: authHeaders({
        'Content-Type': 'application/octet-stream',
        'X-Lenny-Upload-Token': uploadToken,
      }),
      tags: { name: 'checkpoint' },
    },
  );
  if (!check(ckpt, { 'workspace persisted': (r) => r.status === 201 })) {
    // Capture the first 256 bytes of the upload response so the
    // failing §15.1 error envelope is diagnosable from the k6
    // stderr stream. The session ID lets a reader correlate the
    // failure with the gateway-side request log.
    console.error(`upload failed: session=${sessionID} status=${ckpt.status} body=${(ckpt.body || '').slice(0, 256)}`);
  }
}
