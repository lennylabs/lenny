// SPDX-License-Identifier: MIT
//
// k6 scenario: checkpoint_duration
//
// §12.7 target: workspaces at 10 MB, 100 MB, 500 MB. SLO: ≤100MB
// checkpoint P95 < 2s with cooperative quiescence overhead included.
//
// The scenario uploads a workspace of the configured size, drives
// the session to running, then triggers a checkpoint via
// POST /v1/sessions/{id}/checkpoint and measures the duration.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.LENNY_BASE_URL || 'http://127.0.0.1:8080';
const TENANT = __ENV.LENNY_TENANT || 'acme';
const SIZE = parseInt(__ENV.LENNY_WORKSPACE_BYTES || '10485760', 10); // 10 MB default

export const options = {
  thresholds: {
    'http_req_duration{name:checkpoint}': ['p(95)<2000'],
    'http_req_failed': ['rate<0.01'],
  },
};

export default function () {
  // Create the session.
  const create = http.post(`${BASE}/v1/sessions`, JSON.stringify({ runtimeRef: 'streaming-echo' }), {
    headers: { 'Content-Type': 'application/json', 'X-Lenny-Tenant-ID': TENANT },
  });
  if (!check(create, { 'created': (r) => r.status === 201 })) {
    return;
  }
  const sessionID = JSON.parse(create.body).id;

  // Stub the workspace upload: in the real scenario the workspace
  // is materialised via WorkspacePlan; here we POST a JSON blob of
  // the target size so the bytes-on-the-wire match the SLO target.
  const blob = 'a'.repeat(SIZE);
  http.post(
    `${BASE}/v1/sessions/${sessionID}/workspace`,
    blob,
    { headers: { 'X-Lenny-Tenant-ID': TENANT, 'Content-Type': 'application/octet-stream' } },
  );

  // Trigger the checkpoint.
  const ckpt = http.post(
    `${BASE}/v1/sessions/${sessionID}/checkpoint`,
    null,
    {
      headers: { 'X-Lenny-Tenant-ID': TENANT },
      tags: { name: 'checkpoint' },
    },
  );
  check(ckpt, { 'checkpoint ok': (r) => r.status === 200 || r.status === 202 });
}
