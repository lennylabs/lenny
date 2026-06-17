# SPDX-License-Identifier: MIT

"""Unit tests for the section 15.2 MCP client surface.

The tests stand up a local :mod:`http.server` that answers the JSON-RPC
2.0 MCP methods :class:`lenny.MCPClient` exercises and drive
:meth:`lenny.Client.mcp` against it. They cover the ``initialize``
handshake, ``tools/list`` discovery, ``tools/call`` session driving
(``lenny/create_session``, ``lenny/send_message``), an unknown tool
surfacing as a JSON-RPC transport error, a tool failure surfacing as a
result with ``is_error`` set, and a non-2xx status surfacing as the
shared section 15.1 :class:`~lenny.APIError`.

The tests use only the Python standard library (:mod:`unittest`,
:mod:`http.server`), matching the SDK's standard-library-only policy.
Run them with ``python3 -m unittest discover -s tests`` from
``sdks/client/python``.
"""

from __future__ import annotations

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

import lenny


def _tool_result(text: str, is_error: bool) -> dict[str, Any]:
    """Build an MCP ``tools/call`` result carrying one text block."""
    return {"content": [{"type": "text", "text": text}], "isError": is_error}


def _mcp_response(request: dict[str, Any]) -> dict[str, Any]:
    """Answer one section 15.2 MCP JSON-RPC request.

    It is a faithful stand-in for the gateway ``/mcp`` endpoint; the
    tier-3 contract test drives the real gateway MCP server.
    """
    method = request.get("method")
    body: dict[str, Any] = {"jsonrpc": "2.0", "id": request.get("id")}
    if method == "initialize":
        body["result"] = {
            "protocolVersion": "2025-06-18",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "lenny-gateway", "version": "0.1.0"},
        }
        return body
    if method == "tools/list":
        body["result"] = {
            "tools": [
                {
                    "name": "lenny/create_session",
                    "description": "Create a new agent session against a runtime.",
                    "inputSchema": {"type": "object"},
                },
                {
                    "name": "lenny/send_message",
                    "description": "Deliver a message to a running session.",
                    "inputSchema": {"type": "object"},
                },
            ]
        }
        return body
    if method == "tools/call":
        params = request.get("params") or {}
        name = params.get("name")
        args = params.get("arguments") or {}
        if name == "lenny/create_session":
            if not args.get("runtimeRef"):
                body["result"] = _tool_result("runtimeRef is required", True)
                return body
            body["result"] = _tool_result(
                json.dumps({"sessionId": "sess_mcp_1", "state": "running"}),
                False,
            )
            return body
        if name == "lenny/send_message":
            # section 8.5 line 537 wire contract: the tool arguments are
            # ``to`` (target session id) and ``message`` (content).
            # F-8.5.16 renamed them from the legacy ``sessionId``/``content``.
            if not args.get("to"):
                body["result"] = _tool_result("to is required", True)
                return body
            body["result"] = _tool_result(f"echo: {args.get('message')}", False)
            return body
        body["error"] = {"code": -32601, "message": f"unknown tool {name}"}
        return body
    body["error"] = {"code": -32601, "message": f"unknown method {method}"}
    return body


class _Server:
    """A local HTTP server that answers section 15.2 MCP requests.

    When ``status`` is set the server returns that HTTP status with a
    section 15.1 error envelope instead of dispatching the request, so a
    test can exercise the non-2xx error path.
    """

    def __init__(self, status: int = 0) -> None:
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
                length = int(self.headers.get("Content-Length", "0"))
                raw = self.rfile.read(length)
                if outer._status:
                    payload = json.dumps(
                        {
                            "error": {
                                "code": "PERMISSION_DENIED",
                                "category": "POLICY",
                                "message": "no",
                                "retryable": False,
                            }
                        }
                    ).encode("utf-8")
                    self.send_response(outer._status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
                    return
                request = json.loads(raw)
                payload = json.dumps(_mcp_response(request)).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            def log_message(self, *_args: object) -> None:
                # Silence the default stderr request log.
                pass

        self._status = status
        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self._thread = threading.Thread(
            target=self._server.serve_forever, daemon=True
        )

    def __enter__(self) -> "_Server":
        self._thread.start()
        return self

    def __exit__(self, *_exc: object) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=5)

    @property
    def url(self) -> str:
        host, port = self._server.server_address[:2]
        host_str = host.decode() if isinstance(host, bytes) else str(host)
        return f"http://{host_str}:{port}"


def _mcp_client(base_url: str) -> lenny.MCPClient:
    """Build an MCP client against the test server."""
    return lenny.Client(base_url, tenant_id="acme").mcp()


class MCPInitializeTest(unittest.TestCase):
    """The MCP client performs the section 15.2 initialize handshake."""

    def test_initialize_negotiates_version(self) -> None:
        # spec: section 15.2 MCP initialize handshake + version negotiation.
        with _Server() as server:
            mcp = _mcp_client(server.url)
            result = mcp.initialize()
            self.assertNotEqual(result.protocol_version, "")
            self.assertEqual(result.server_info.name, "lenny-gateway")


class MCPListToolsTest(unittest.TestCase):
    """The MCP client lists the section 15.2 platform tool catalog."""

    def test_list_tools_returns_catalog(self) -> None:
        # spec: section 15.2 tools/list returns the platform tool catalog.
        with _Server() as server:
            mcp = _mcp_client(server.url)
            tools = mcp.list_tools()
            names = {t.name for t in tools}
            self.assertIn("lenny/create_session", names)
            self.assertIn("lenny/send_message", names)
            for tool in tools:
                self.assertTrue(tool.input_schema, tool.name)

    def test_list_tools_runs_handshake_first(self) -> None:
        # spec: section 15.2 list_tools runs the initialize handshake on
        # first use.
        with _Server() as server:
            mcp = _mcp_client(server.url)
            # No explicit initialize call; list_tools must still succeed.
            self.assertTrue(mcp.list_tools())


class MCPCallToolTest(unittest.TestCase):
    """The MCP client drives a session through section 15.2 tools/call."""

    def test_call_tool_drives_session(self) -> None:
        # spec: section 15.2 tools/call drives a session over MCP.
        with _Server() as server:
            mcp = _mcp_client(server.url)
            created = mcp.create_session("claude-code", "alice@acme.com")
            self.assertNotEqual(created.session_id, "")
            self.assertEqual(created.state, "running")
            reply = mcp.send_message(created.session_id, "hello")
            self.assertEqual(reply, "echo: hello")

    def test_unknown_tool_is_transport_error(self) -> None:
        # spec: section 15.2 an unknown tool is a JSON-RPC transport
        # error.
        with _Server() as server:
            mcp = _mcp_client(server.url)
            with self.assertRaises(lenny.MCPError) as caught:
                mcp.call_tool("lenny/no_such_tool", {})
            self.assertEqual(caught.exception.code, -32601)

    def test_tool_failure_is_result_not_error(self) -> None:
        # spec: section 15.2 a tool failure is a result with is_error
        # set, not a JSON-RPC transport error.
        with _Server() as server:
            mcp = _mcp_client(server.url)
            # create_session with no runtimeRef makes the tool report a
            # failure; that is a result, not a transport error.
            result = mcp.call_tool("lenny/create_session", {})
            self.assertTrue(result.is_error)
            self.assertNotEqual(result.text(), "")


class MCPErrorTaxonomyTest(unittest.TestCase):
    """A non-2xx MCP status uses the shared section 15.1 error taxonomy."""

    def test_non_2xx_status_is_api_error(self) -> None:
        # spec: section 15.2.1 a non-2xx status uses the shared section
        # 15.1 error taxonomy so one error-handling strategy covers both
        # surfaces.
        with _Server(status=403) as server:
            mcp = _mcp_client(server.url)
            with self.assertRaises(lenny.APIError) as caught:
                mcp.initialize()
            self.assertEqual(caught.exception.code, "PERMISSION_DENIED")


if __name__ == "__main__":
    unittest.main()
