// SPDX-License-Identifier: MIT

// MCP-conformance probe for the tier-3 TypeScript client contract.
//
// The §15.2 MCP surface is a JSON-RPC endpoint rather than a
// request/response REST op, so it does not fit the harness JSON-line
// model. This probe is the MCP counterpart of the test-helper. The Go
// test (TestTypeScriptClientMCP) stands up the gateway's MCP server in
// process and spawns this probe as a subprocess.
//
// The probe drives the SDK MCP client against the gateway: it runs the
// initialize handshake, lists the tool catalog, creates a session,
// sends a message, reads the reply, and confirms an unknown tool fails
// as a JSON-RPC transport error. It prints one JSON line on stdout
// summarizing the outcome and exits 0 on success. Any error is printed
// as {"error": "..."} and the probe exits 1.
//
// The gateway origin is read from LENNY_GATEWAY_URL in the environment
// the Go test sets.

import { asMCPError, newClient } from '../dist/esm/index.js';

function fail(message) {
  process.stdout.write(JSON.stringify({ error: message }) + '\n');
  process.exit(1);
}

async function main() {
  const gatewayUrl = process.env.LENNY_GATEWAY_URL;
  if (!gatewayUrl) {
    fail('LENNY_GATEWAY_URL is not set');
    return;
  }
  const mcp = newClient(gatewayUrl, { tenantId: 'acme' }).mcp();

  // The initialize handshake negotiates the §15.2 protocol version.
  const init = await mcp.initialize();
  if (!init.protocolVersion) {
    fail('initialize returned an empty negotiated protocol version');
    return;
  }

  // tools/list returns the platform tool catalog.
  const tools = await mcp.listTools();
  const toolNames = tools.map((t) => t.name);
  for (const want of ['lenny/create_session', 'lenny/send_message']) {
    if (!toolNames.includes(want)) {
      fail(`tools/list omitted ${want}; catalog=${JSON.stringify(toolNames)}`);
      return;
    }
  }

  // Drive a session over MCP: create, send a message, read the reply.
  const created = await mcp.createSession('claude-code', 'alice@acme.com');
  if (!created.sessionId) {
    fail('create_session returned an empty session id');
    return;
  }
  const reply = await mcp.sendMessage(created.sessionId, 'ping');
  if (!reply.includes('ping')) {
    fail(`send_message reply ${JSON.stringify(reply)} does not echo the message`);
    return;
  }

  // An unknown tool is a JSON-RPC transport error.
  let unknownCode = 0;
  try {
    await mcp.callTool('lenny/no_such_tool', {});
    fail('callTool of an unknown tool returned no error');
    return;
  } catch (err) {
    const mcpErr = asMCPError(err);
    if (!mcpErr) {
      fail(`unknown-tool error is not an MCPError: ${err}`);
      return;
    }
    unknownCode = mcpErr.code;
  }

  process.stdout.write(
    JSON.stringify({
      protocolVersion: init.protocolVersion,
      serverInfo: init.serverInfo.name,
      tools: toolNames,
      sessionId: created.sessionId,
      reply,
      unknownToolCode: unknownCode,
    }) + '\n',
  );
  process.exit(0);
}

main().catch((err) => {
  fail(err && err.stack ? err.stack : String(err));
});
