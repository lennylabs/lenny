// SPDX-License-Identifier: MIT

// Unit tests for the §15.2 MCP client surface of the TypeScript client
// SDK. The tests import the built ESM bundle (the dual-build output the
// package publishes) and stand up a local node:http server that
// answers the JSON-RPC 2.0 MCP methods the MCPClient exercises.
//
// The tests cover the initialize handshake, tools/list discovery,
// tools/call session driving (create_session, send_message), an
// unknown tool surfacing as a JSON-RPC transport error, a tool failure
// surfacing as a result with isError set, and a non-2xx status
// surfacing as the shared §15.1 ApiError.

import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import test from 'node:test';

import { ApiError, asMCPError, newClient } from '../dist/esm/index.js';

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

// readBody collects an incoming request body and parses it as JSON.
function readBody(req) {
  return new Promise((resolve, reject) => {
    let raw = '';
    req.on('data', (chunk) => {
      raw += chunk;
    });
    req.on('end', () => {
      try {
        resolve(JSON.parse(raw));
      } catch (err) {
        reject(err);
      }
    });
    req.on('error', reject);
  });
}

// toolResult builds an MCP tools/call result carrying one text block.
function toolResult(text, isError) {
  return { content: [{ type: 'text', text }], isError };
}

// mcpHandler answers the §15.2 MCP JSON-RPC methods. It is a faithful
// stand-in for the gateway /mcp endpoint; the tier-3 contract test
// drives the real gateway MCP server.
async function mcpHandler(req, res) {
  if (req.method !== 'POST' || req.url !== '/mcp') {
    res.writeHead(404).end();
    return;
  }
  const rpc = await readBody(req);
  const send = (result, error) => {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    const body = { jsonrpc: '2.0', id: rpc.id };
    if (error) {
      body.error = error;
    } else {
      body.result = result;
    }
    res.end(JSON.stringify(body));
  };
  switch (rpc.method) {
    case 'initialize':
      send({
        protocolVersion: '2025-06-18',
        capabilities: { tools: {} },
        serverInfo: { name: 'lenny-gateway', version: '0.1.0' },
      });
      return;
    case 'tools/list':
      send({
        tools: [
          {
            name: 'lenny/create_session',
            description: 'Create a new agent session against a runtime.',
            inputSchema: { type: 'object' },
          },
          {
            name: 'lenny/send_message',
            description: 'Deliver a message to a running session.',
            inputSchema: { type: 'object' },
          },
        ],
      });
      return;
    case 'tools/call': {
      const { name, arguments: args } = rpc.params;
      if (name === 'lenny/create_session') {
        if (!args.runtimeRef) {
          send(toolResult('runtimeRef is required', true));
          return;
        }
        send(toolResult(JSON.stringify({ sessionId: 'sess_mcp_1', state: 'running' }), false));
        return;
      }
      if (name === 'lenny/send_message') {
        // §8.5 line 537 wire contract: the tool arguments are `to`
        // (target session id) and `message` (content). F-8.5.16 renamed
        // them from the legacy `sessionId`/`content`.
        if (!args.to) {
          send(toolResult('to is required', true));
          return;
        }
        send(toolResult(`echo: ${args.message}`, false));
        return;
      }
      send(undefined, { code: -32601, message: `unknown tool ${name}` });
      return;
    }
    default:
      send(undefined, { code: -32601, message: `unknown method ${rpc.method}` });
  }
}

test('initialize performs the §15.2 MCP handshake', async () => {
  const srv = await startServer(mcpHandler);
  try {
    const mcp = newClient(srv.url, { tenantId: 'acme' }).mcp();
    const res = await mcp.initialize();
    assert.equal(res.protocolVersion, '2025-06-18');
    assert.equal(res.serverInfo.name, 'lenny-gateway');
  } finally {
    await srv.close();
  }
});

test('listTools returns the platform tool catalog', async () => {
  const srv = await startServer(mcpHandler);
  try {
    const mcp = newClient(srv.url, { tenantId: 'acme' }).mcp();
    const tools = await mcp.listTools();
    const names = tools.map((t) => t.name);
    assert.ok(names.includes('lenny/create_session'), `missing create_session in ${names}`);
    assert.ok(names.includes('lenny/send_message'), `missing send_message in ${names}`);
    for (const tool of tools) {
      assert.ok(tool.inputSchema, `tool ${tool.name} has no input schema`);
    }
  } finally {
    await srv.close();
  }
});

test('listTools runs the initialize handshake on first use', async () => {
  const srv = await startServer(mcpHandler);
  try {
    // No explicit initialize call; listTools must still succeed.
    const mcp = newClient(srv.url, { tenantId: 'acme' }).mcp();
    const tools = await mcp.listTools();
    assert.ok(tools.length > 0);
  } finally {
    await srv.close();
  }
});

test('callTool drives a session over MCP', async () => {
  const srv = await startServer(mcpHandler);
  try {
    const mcp = newClient(srv.url, { tenantId: 'acme' }).mcp();
    const created = await mcp.createSession('claude-code', 'alice@acme.com');
    assert.ok(created.sessionId, 'create returned no session id');
    assert.equal(created.state, 'running');
    const reply = await mcp.sendMessage(created.sessionId, 'hello');
    assert.equal(reply, 'echo: hello');
  } finally {
    await srv.close();
  }
});

test('an unknown tool surfaces as a JSON-RPC transport error', async () => {
  const srv = await startServer(mcpHandler);
  try {
    const mcp = newClient(srv.url, { tenantId: 'acme' }).mcp();
    await assert.rejects(
      () => mcp.callTool('lenny/no_such_tool', {}),
      (err) => {
        const mcpErr = asMCPError(err);
        assert.ok(mcpErr, 'error is not an MCPError');
        assert.equal(mcpErr.code, -32601);
        return true;
      },
    );
  } finally {
    await srv.close();
  }
});

test('a tool failure is a result with isError set, not a transport error', async () => {
  const srv = await startServer(mcpHandler);
  try {
    const mcp = newClient(srv.url, { tenantId: 'acme' }).mcp();
    // create_session with no runtimeRef makes the tool report a failure.
    const res = await mcp.callTool('lenny/create_session', {});
    assert.equal(res.isError, true);
    assert.ok(res.content.length > 0, 'tool failure result carried no content');
  } finally {
    await srv.close();
  }
});

test('a non-2xx MCP status surfaces as the shared §15.1 ApiError', async () => {
  const srv = await startServer((req, res) => {
    res.writeHead(403, { 'Content-Type': 'application/json' });
    res.end(
      JSON.stringify({
        error: {
          code: 'PERMISSION_DENIED',
          category: 'POLICY',
          message: 'no',
          retryable: false,
        },
      }),
    );
  });
  try {
    const mcp = newClient(srv.url, { tenantId: 'acme' }).mcp();
    await assert.rejects(
      () => mcp.initialize(),
      (err) => {
        assert.ok(err instanceof ApiError, 'error is not an ApiError');
        assert.equal(err.code, 'PERMISSION_DENIED');
        return true;
      },
    );
  } finally {
    await srv.close();
  }
});
