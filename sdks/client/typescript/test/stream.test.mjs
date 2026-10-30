// SPDX-License-Identifier: MIT

// Unit tests for the §15.1 Server-Sent Events streaming surface of the
// TypeScript client SDK. The tests import the built ESM bundle (the
// dual-build output the package publishes) and stand up a local
// node:http server that speaks the §15.1 SSE frame protocol.
//
// The tests cover the SSE frame parser, in-order delivery, the
// Last-Event-ID reconnect with backlog dedup, a caller-supplied
// resume cursor, a non-retryable status ending the stream, a
// retryable status reconnecting, and an AbortSignal stopping the
// stream cleanly.

import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';

import { ApiError, newClient } from '../dist/esm/index.js';

// fastStreamClient builds a Client whose reconnect backoff is short
// enough for a unit test. The retry policy governs the §15.1 stream
// reconnect spacing the same way it governs REST retries.
function fastStreamClient(baseUrl) {
  return newClient(baseUrl, {
    retryPolicy: { maxAttempts: 5, baseDelayMs: 1, maxDelayMs: 5, jitter: false },
  });
}

// sseFrame formats one §15.1 SSE frame: id, event, and data lines
// terminated by a blank line.
function sseFrame(seq, type, data) {
  return `id: ${seq}\nevent: ${type}\ndata: ${data}\n\n`;
}

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

// collect drains an async iterable into an array, calling onEach after
// every event so a test can stop the stream once it has enough.
async function collect(iterable, onEach) {
  const out = [];
  for await (const ev of iterable) {
    out.push(ev);
    if (onEach) {
      onEach(ev, out);
    }
  }
  return out;
}

test('delivers SSE events in order', async () => {
  const srv = await startServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    for (let seq = 1; seq <= 3; seq++) {
      res.write(sseFrame(seq, 'response', JSON.stringify({ n: seq })));
    }
    res.end();
  });
  try {
    const client = fastStreamClient(srv.url);
    const ctl = new AbortController();
    const got = await collect(client.streamEvents('sess_1', { signal: ctl.signal }));
    assert.deepEqual(
      got.map((e) => e.seq),
      [1, 2, 3],
    );
    assert.equal(got[0].type, 'response');
    assert.deepEqual(JSON.parse(got[2].data), { n: 3 });
  } finally {
    await srv.close();
  }
});

test('parses comments, blank padding, and multi-line data', async () => {
  const raw =
    ': keepalive comment\n\n' +
    'id: 9\nevent: response\ndata: line one\ndata: line two\n\n' +
    '\n\n' +
    ': another comment\n' +
    sseFrame(10, 'state_changed', JSON.stringify({ ok: true }));
  const srv = await startServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.end(raw);
  });
  try {
    const client = fastStreamClient(srv.url);
    const got = await collect(client.streamEvents('sess_parse', { signal: new AbortController().signal }));
    assert.equal(got.length, 2, 'comment lines and blank padding must not arm a frame');
    assert.equal(got[0].seq, 9);
    assert.equal(got[0].data, 'line one\nline two');
    assert.equal(got[1].seq, 10);
    assert.equal(got[1].type, 'state_changed');
  } finally {
    await srv.close();
  }
});

test('discards a partial trailing frame', async () => {
  // The second frame has no terminating blank line; it is incomplete
  // and must not be delivered.
  const raw = sseFrame(1, 'response', '{"a":1}') + 'id: 2\nevent: response\ndata: {"b":2}\n';
  const srv = await startServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.end(raw);
  });
  try {
    const client = fastStreamClient(srv.url);
    const got = await collect(client.streamEvents('sess_partial', { signal: new AbortController().signal }));
    assert.deepEqual(
      got.map((e) => e.seq),
      [1],
    );
  } finally {
    await srv.close();
  }
});

test('reconnects with Last-Event-ID and deduplicates the backlog', async () => {
  // The server holds the full event log [1..6]. The first connection
  // delivers events 1 through 3, then drops the connection. On the
  // reconnect the client sends Last-Event-ID: 3; the server, mimicking
  // the gateway, replays event 3 again (an inclusive backlog boundary
  // the SDK must deduplicate) and then delivers 4 through 6.
  const total = 6;
  const lastEventIds = [];
  let connCount = 0;
  const srv = await startServer((req, res) => {
    connCount++;
    const conn = connCount;
    lastEventIds.push(req.headers['last-event-id'] ?? '');
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    if (conn === 1) {
      for (let seq = 1; seq <= 3; seq++) {
        res.write(sseFrame(seq, 'response', JSON.stringify({ n: seq })));
      }
      res.end();
      return;
    }
    const after = Number.parseInt(req.headers['last-event-id'] ?? '0', 10) || 0;
    const start = after === 0 ? 1 : after;
    for (let seq = start; seq <= total; seq++) {
      res.write(sseFrame(seq, 'response', JSON.stringify({ n: seq })));
    }
    res.end();
  });
  try {
    const client = fastStreamClient(srv.url);
    const ctl = new AbortController();
    const got = await collect(client.streamEvents('sess_reconnect', { signal: ctl.signal }), (_ev, out) => {
      if (out.length === total) {
        ctl.abort();
      }
    });

    // Every event arrived exactly once, in order, with no gap.
    assert.equal(got.length, total);
    got.forEach((ev, i) => assert.equal(ev.seq, i + 1, `position ${i}`));
    assert.ok(connCount >= 2, `expected at least one reconnect, saw ${connCount}`);
    assert.equal(lastEventIds[0], '', 'first connection must send no Last-Event-ID');
    assert.equal(lastEventIds[1], '3', 'reconnect must carry the last delivered cursor');
  } finally {
    await srv.close();
  }
});

test('resumes from a caller-supplied lastEventId', async () => {
  let firstSeen;
  const srv = await startServer((req, res) => {
    if (firstSeen === undefined) {
      firstSeen = req.headers['last-event-id'] ?? '';
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.end(sseFrame(11, 'response', JSON.stringify({ n: 11 })));
  });
  try {
    const client = fastStreamClient(srv.url);
    const ctl = new AbortController();
    let first;
    for await (const ev of client.streamEvents('sess_resume', { lastEventId: 10, signal: ctl.signal })) {
      first = ev;
      ctl.abort();
    }
    assert.equal(first.seq, 11);
    assert.equal(firstSeen, '10', 'a caller-supplied lastEventId sets the initial Last-Event-ID header');
  } finally {
    await srv.close();
  }
});

test('a non-retryable status ends the stream with the typed error', async () => {
  const srv = await startServer((req, res) => {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(
      JSON.stringify({
        error: {
          code: 'RESOURCE_NOT_FOUND',
          category: 'PERMANENT',
          message: 'session not found',
          retryable: false,
        },
      }),
    );
  });
  try {
    const client = fastStreamClient(srv.url);
    await assert.rejects(
      collect(client.streamEvents('sess_missing', { signal: new AbortController().signal })),
      (err) => {
        assert.ok(err instanceof ApiError, 'a 404 stream must throw an ApiError');
        assert.equal(err.code, 'RESOURCE_NOT_FOUND');
        return true;
      },
    );
  } finally {
    await srv.close();
  }
});

test('a retryable status reconnects rather than ending the stream', async () => {
  let attempt = 0;
  const srv = await startServer((req, res) => {
    attempt++;
    if (attempt === 1) {
      res.writeHead(503, { 'Content-Type': 'application/json' });
      res.end(
        JSON.stringify({
          error: {
            code: 'EVENT_STREAM_UNAVAILABLE',
            category: 'TRANSIENT',
            message: 'event bus unavailable',
            retryable: true,
          },
        }),
      );
      return;
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.end(sseFrame(1, 'response', JSON.stringify({ n: 1 })));
  });
  try {
    const client = fastStreamClient(srv.url);
    const ctl = new AbortController();
    const got = await collect(client.streamEvents('sess_503', { signal: ctl.signal }), (_ev, out) => {
      if (out.length === 1) {
        ctl.abort();
      }
    });
    assert.deepEqual(
      got.map((e) => e.seq),
      [1],
    );
    assert.ok(attempt >= 2, `a 503 must be retried, saw ${attempt} attempt(s)`);
  } finally {
    await srv.close();
  }
});

test('an aborted signal stops the stream cleanly with no error', async () => {
  let released;
  const hold = new Promise((resolve) => {
    released = resolve;
  });
  const srv = await startServer(async (req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(sseFrame(1, 'response', JSON.stringify({ n: 1 })));
    // Hold the connection open until the test aborts so the SDK must
    // observe the abort to stop.
    await hold;
    res.end();
  });
  try {
    const client = fastStreamClient(srv.url);
    const ctl = new AbortController();
    const got = [];
    // The iteration must complete without throwing once the signal is
    // aborted: an abort is a clean caller-requested stop.
    for await (const ev of client.streamEvents('sess_cancel', { signal: ctl.signal })) {
      got.push(ev);
      ctl.abort();
    }
    assert.deepEqual(
      got.map((e) => e.seq),
      [1],
    );
  } finally {
    released();
    await srv.close();
  }
});
