#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

"""MCP-conformance probe for the tier-3 Python client contract.

The section 15.2 MCP surface is a JSON-RPC endpoint rather than a
request/response REST op, so it does not fit the harness JSON-line
model. This probe is the MCP counterpart of the test-helper. The Go
test (``TestPythonClientMCP``) stands up the gateway's MCP server in
process and spawns this probe as a subprocess.

The probe drives the SDK MCP client against the gateway: it runs the
``initialize`` handshake, lists the tool catalog, creates a session,
sends a message, reads the reply, and confirms an unknown tool fails as
a JSON-RPC transport error. It prints one JSON line on stdout
summarizing the outcome and exits 0 on success. Any error is printed as
``{"error": "..."}`` and the probe exits 1.

The gateway origin is read from ``LENNY_GATEWAY_URL`` in the environment
the Go test sets.
"""

from __future__ import annotations

import json
import os
import sys

# The probe runs from an arbitrary working directory; make the
# committed ``lenny`` package importable without an install.
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import lenny  # noqa: E402 - path setup precedes the import


def fail(message: str) -> None:
    """Print an error line and exit non-zero."""
    sys.stdout.write(json.dumps({"error": message}) + "\n")
    sys.exit(1)


def main() -> None:
    gateway_url = os.environ.get("LENNY_GATEWAY_URL", "")
    if not gateway_url:
        fail("LENNY_GATEWAY_URL is not set")
        return
    mcp = lenny.Client(gateway_url, tenant_id="acme").mcp()

    # The initialize handshake negotiates the section 15.2 protocol
    # version.
    init = mcp.initialize()
    if not init.protocol_version:
        fail("initialize returned an empty negotiated protocol version")
        return

    # tools/list returns the platform tool catalog.
    tools = mcp.list_tools()
    tool_names = [t.name for t in tools]
    for want in ("lenny/create_session", "lenny/send_message"):
        if want not in tool_names:
            fail(f"tools/list omitted {want}; catalog={tool_names}")
            return

    # Drive a session over MCP: create, send a message, read the reply.
    created = mcp.create_session("claude-code", "alice@acme.com")
    if not created.session_id:
        fail("create_session returned an empty session id")
        return
    reply = mcp.send_message(created.session_id, "ping")
    if "ping" not in reply:
        fail(f"send_message reply {reply!r} does not echo the message")
        return

    # An unknown tool is a JSON-RPC transport error.
    try:
        mcp.call_tool("lenny/no_such_tool", {})
        fail("call_tool of an unknown tool returned no error")
        return
    except lenny.MCPError as exc:
        unknown_code = exc.code

    sys.stdout.write(
        json.dumps(
            {
                "protocolVersion": init.protocol_version,
                "serverInfo": init.server_info.name,
                "tools": tool_names,
                "sessionId": created.session_id,
                "reply": reply,
                "unknownToolCode": unknown_code,
            }
        )
        + "\n"
    )
    sys.exit(0)


if __name__ == "__main__":
    main()
