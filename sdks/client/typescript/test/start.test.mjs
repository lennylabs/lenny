// SPDX-License-Identifier: MIT

// Unit tests for the single-attempt start of the TypeScript client SDK.
// A /start that fails after the gateway wrote starting leaves the session
// failed, so start sends its request once and retries neither an error
// response nor a transport failure (§15.6, §15.1). The request count each
// stub records is the discriminating assertion.
//
// spec: §15.6 (Client SDKs), §15.1 (/start), §7.2 (starting → failed)

import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';

import { ApiError, newClient } from '../dist/esm/index.js';

// startServer counts requests per path and answers each through respond.
function startServer(respond) {
  const counts = new Map();
  return new Promise((resolve) => {
    const server = createServer((req, res) => {
      counts.set(req.url, (counts.get(req.url) ?? 0) + 1);
      respond(req, res);
    });
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        url: `http://127.0.0.1:${port}`,
        counts,
        close: () =>
          new Promise((done) => {
            server.closeAllConnections?.();
            server.close(done);
          }),
      });
    });
  });
}

function answer503(code) {
  return (_req, res) => {
    res.statusCode = 503;
    res.setHeader('Content-Type', 'application/json');
    res.setHeader('Retry-After', '1');
    res.end(
      JSON.stringify({
        error: { code, category: 'TRANSIENT', message: 'stub failure', retryable: true },
      }),
    );
  };
}

const policy = { maxAttempts: 5, baseDelayMs: 1, jitter: false };

test('start sends one request on 503 STARTING_FAILED', async () => {
  const srv = await startServer(answer503('STARTING_FAILED'));
  try {
    const client = newClient(srv.url, { retryPolicy: policy });
    await assert.rejects(client.start('sess_1'), (err) => {
      assert.ok(err instanceof ApiError);
      assert.equal(err.code, 'STARTING_FAILED');
      return true;
    });
    assert.equal(srv.counts.get('/v1/sessions/sess_1/start'), 1);
  } finally {
    await srv.close();
  }
});

test('start sends one request on a dropped connection', async () => {
  const srv = await startServer((req) => {
    req.socket.destroy();
  });
  try {
    const client = newClient(srv.url, { retryPolicy: policy });
    await assert.rejects(client.start('sess_1'));
    assert.equal(srv.counts.get('/v1/sessions/sess_1/start'), 1);
  } finally {
    await srv.close();
  }
});

test('finalize keeps the retry policy', async () => {
  const srv = await startServer(answer503('SESSION_CREATION_FAILED'));
  try {
    const client = newClient(srv.url, { retryPolicy: { ...policy, maxAttempts: 3 } });
    await assert.rejects(client.finalize('sess_1', 'tok-1'));
    assert.equal(srv.counts.get('/v1/sessions/sess_1/finalize'), 3);
  } finally {
    await srv.close();
  }
});

// spec: §7.4 (Upload Safety, Upload authorization), §15.6 (Client SDKs).
// Every finalize request carries the §7.1 uploadToken in the
// X-Lenny-Upload-Token header, and an empty token is refused before any
// request is sent.
test('finalize sends the upload token and refuses an empty one', async () => {
  const tokens = [];
  const srv = await startServer((req, res) => {
    tokens.push(req.headers['x-lenny-upload-token'] ?? '');
    answer503('SESSION_CREATION_FAILED')(req, res);
  });
  try {
    const client = newClient(srv.url, { retryPolicy: { ...policy, maxAttempts: 1 } });
    await assert.rejects(client.finalize('sess_1', 'tok-7', { idempotencyKey: 'k1' }));
    assert.deepEqual(tokens, ['tok-7']);
    await assert.rejects(client.finalize('sess_1', ''), /requires an upload token/);
    assert.equal(srv.counts.get('/v1/sessions/sess_1/finalize'), 1);
  } finally {
    await srv.close();
  }
});
