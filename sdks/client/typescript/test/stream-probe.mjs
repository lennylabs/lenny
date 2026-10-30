#!/usr/bin/env node
// SPDX-License-Identifier: MIT

// Streaming-conformance probe for the tier-3 TypeScript client SDK
// contract test. The §15.1 SSE stream is a long-lived connection
// rather than a request/response op, so it does not fit the harness
// JSON-line model: a `stream` op would block the synchronous
// harness.Send while the Go test publishes events and forces a
// disconnect. This probe is the streaming counterpart of the
// test-helper. The Go test (TestTypeScriptClientStreamingReconnect)
// spawns it as a subprocess, then concurrently publishes events to the
// gateway event bus and severs the first connection.
//
// The probe opens client.streamEvents against the gateway, collects
// events until it has the expected count, prints one JSON line
// {"seqs":[...],"types":[...]} on stdout, and exits 0. Any error is
// printed as {"error":"..."} and the probe exits 1.
//
// Arguments are read from the environment the Go test sets:
//   LENNY_GATEWAY_URL  the in-process gateway origin
//   LENNY_TENANT_ID    the tenant the session was created on
//   LENNY_SESSION_ID   the session whose event stream to consume
//   LENNY_EVENT_COUNT  the number of events to collect before stopping

import { newClient } from '../dist/esm/index.js';

function fail(message) {
  process.stdout.write(JSON.stringify({ error: message }) + '\n');
  process.exit(1);
}

const gatewayUrl = process.env.LENNY_GATEWAY_URL;
const tenantId = process.env.LENNY_TENANT_ID ?? 'acme';
const sessionId = process.env.LENNY_SESSION_ID;
const eventCount = Number.parseInt(process.env.LENNY_EVENT_COUNT ?? '0', 10);

if (!gatewayUrl || !sessionId || !Number.isInteger(eventCount) || eventCount <= 0) {
  fail('stream-probe: LENNY_GATEWAY_URL, LENNY_SESSION_ID, and a positive LENNY_EVENT_COUNT are required');
}

// A short reconnect backoff keeps the forced-disconnect reconnect
// quick; the gateway holds the retained backlog so the reconnect
// resumes from the Last-Event-ID cursor.
const client = newClient(gatewayUrl, {
  tenantId,
  retryPolicy: { maxAttempts: 10, baseDelayMs: 5, maxDelayMs: 50, jitter: false },
});

const controller = new AbortController();
const seqs = [];
const types = [];

try {
  for await (const event of client.streamEvents(sessionId, { signal: controller.signal })) {
    seqs.push(event.seq);
    types.push(event.type);
    if (seqs.length >= eventCount) {
      // Every expected event arrived; stop the stream cleanly.
      controller.abort();
    }
  }
} catch (err) {
  fail('stream-probe: ' + (err && err.message ? err.message : String(err)));
}

process.stdout.write(JSON.stringify({ seqs, types }) + '\n');
process.exit(0);
