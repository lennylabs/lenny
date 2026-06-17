// SPDX-License-Identifier: MIT

// Unit tests for the §7.1 sessionIsolationLevel decode of the
// TypeScript client SDK. The tests import the built ESM bundle (the
// dual-build output the package publishes) and stand up a local
// node:http server that returns a POST /v1/sessions response carrying
// the sessionIsolationLevel object.
//
// The tests cover the conversationContinuity field across the session
// and service execution modes: createSession surfaces it verbatim so a
// consumer can branch on platform vs none continuity.

import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';

import { newClient } from '../dist/esm/index.js';

// startServer starts an HTTP server bound to a loopback port and
// returns its base URL plus a close function.
function startServer(handler) {
  return new Promise((resolve) => {
    const server = createServer(handler);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        url: `http://127.0.0.1:${port}`,
        close: () => new Promise((done) => server.close(done)),
      });
    });
  });
}

// createSessionReturning answers POST /v1/sessions with the supplied
// sessionIsolationLevel envelope and returns the decoded result.
async function createSessionReturning(isolationLevel) {
  const srv = await startServer((req, res) => {
    res.setHeader('Content-Type', 'application/json');
    res.end(
      JSON.stringify({
        id: 'sess_1',
        state: 'created',
        uploadToken: 'tok',
        sessionIsolationLevel: isolationLevel,
      }),
    );
  });
  try {
    const client = newClient(srv.url);
    return await client.createSession({ runtimeRef: 'rt' });
  } finally {
    await srv.close();
  }
}

// spec: §7.1 sessionIsolationLevel — session mode reports platform
// conversation continuity alongside the session executionMode value.
test('createSession decodes platform continuity for session mode', async () => {
  const result = await createSessionReturning({
    executionMode: 'session',
    isolationProfile: 'gvisor',
    podReuse: false,
    residualStateWarning: false,
    conversationContinuity: 'platform',
  });
  assert.equal(result.sessionIsolationLevel.executionMode, 'session');
  assert.equal(result.sessionIsolationLevel.conversationContinuity, 'platform');
});

// spec: §7.1 sessionIsolationLevel — service mode reports no
// conversation continuity alongside the service executionMode value.
test('createSession decodes none continuity for service mode', async () => {
  const result = await createSessionReturning({
    executionMode: 'service',
    isolationProfile: 'runc',
    podReuse: true,
    scrubPolicy: 'none',
    residualStateWarning: true,
    conversationContinuity: 'none',
  });
  assert.equal(result.sessionIsolationLevel.executionMode, 'service');
  assert.equal(result.sessionIsolationLevel.conversationContinuity, 'none');
});
