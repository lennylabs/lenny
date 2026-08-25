#!/usr/bin/env node
// SPDX-License-Identifier: MIT

// Message-body probe for the tier-3 TypeScript client SDK contract
// test. The harness JSON-line helper exposes no message op, so this
// probe drives client.sendMessages once against the recording server
// the Go test stands up (TestTypeScriptClientMessageBodyOmitsSlotAddress),
// which inspects the encoded request body. A client addresses a session
// rather than a slot, so the encoded payload carries no slot key.
//
// Arguments are read from the environment the Go test sets:
//   LENNY_GATEWAY_URL  the recording server origin
//   LENNY_TENANT_ID    the tenant header value
//
// The probe prints one JSON line on stdout: {} on success, or
// {"error":"..."} with exit 1 on failure.

import { newClient } from '../dist/esm/index.js';

const client = newClient(process.env.LENNY_GATEWAY_URL, {
  tenantId: process.env.LENNY_TENANT_ID,
});

try {
  await client.sendMessages('sess_1', {
    messages: [{ role: 'user', content: 'hello', delivery: 'queued' }],
  });
  process.stdout.write(JSON.stringify({}) + '\n');
} catch (err) {
  process.stdout.write(
    JSON.stringify({ error: String((err && err.message) || err) }) + '\n',
  );
  process.exit(1);
}
