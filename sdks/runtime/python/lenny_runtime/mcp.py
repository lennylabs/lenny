# SPDX-License-Identifier: MIT

"""§15.4.3 intra-pod MCP client and the typed §8.5 platform tool helpers.

The MCP client speaks JSON-RPC 2.0 over a single Unix socket: an
``initialize`` request carrying the manifest nonce, then ``tools/list``
and ``tools/call``. A Standard-level or Full-level runtime reaches the
typed helpers through :class:`PlatformTools`.
"""

from __future__ import annotations

import json
import threading
from typing import Any

from .transport import LineReader, SocketStream, dial_unix_socket
from .types import (
    AdapterManifest,
    MessagePart,
    TaskHandle,
    TaskResult,
)

# MCP_PROTOCOL_VERSION is the §15.4.3 intra-pod MCP spec version the
# adapter's local MCP servers speak.
MCP_PROTOCOL_VERSION = "2025-03-26"

# NONCE_PARAM_KEY is the §15.4.3 canonical injection key for the
# intra-pod MCP nonce: the top-level field of the MCP initialize
# request's params object. The adapter validates and strips it before
# forwarding the request to its MCP server implementation.
NONCE_PARAM_KEY = "_lennyNonce"


def _stamp_parts(parts: list[MessagePart]) -> list[dict[str, Any]]:
    """Serialize parts to wire form, honoring the §15.4.1 producer
    obligation that every part carries a schema version."""
    return [p.to_wire() for p in parts]


class McpClient:
    """Minimal §15.4.3 intra-pod MCP client over a single Unix socket.

    The SDK issues calls sequentially; a lock serializes the
    connection.
    """

    def __init__(self, stream: SocketStream) -> None:
        self._stream = stream
        self._reader = LineReader(stream)
        self._lock = threading.Lock()
        self._next_id = 0

    @classmethod
    def connect(
        cls,
        socket_name: str,
        nonce: str,
        client_name: str,
        timeout_s: float,
    ) -> McpClient:
        """Dial the intra-pod MCP socket, complete the
        nonce-authenticated initialize handshake (§15.4.3), and discover
        the tool set via ``tools/list``.

        The nonce is presented as the top-level ``params._lennyNonce``
        field of the initialize request.
        """
        stream = dial_unix_socket(socket_name, timeout_s)
        client = cls(stream)
        try:
            client.call(
                "initialize",
                {
                    NONCE_PARAM_KEY: nonce,
                    "protocolVersion": MCP_PROTOCOL_VERSION,
                    "clientInfo": {"name": client_name, "version": "1.0.0"},
                },
            )
            client.call("tools/list", {})
        except Exception as err:
            stream.close()
            raise RuntimeError(f"connect {socket_name}: {err}") from err
        return client

    def call(self, method: str, params: Any) -> Any:
        """Send one JSON-RPC request and read the matching response."""
        with self._lock:
            self._next_id += 1
            request = {
                "jsonrpc": "2.0",
                "id": self._next_id,
                "method": method,
                "params": params,
            }
            line = (json.dumps(request, separators=(",", ":")) + "\n").encode(
                "utf-8"
            )
            self._stream.write(line)
            self._stream.flush()
            raw = self._reader.next()
            if raw is None:
                raise RuntimeError(f"{method}: MCP server closed the connection")
            resp = json.loads(raw)
            err = resp.get("error")
            if err is not None:
                raise RuntimeError(
                    f"{method}: rpc error {err.get('code')}: {err.get('message')}"
                )
            return resp.get("result")

    def call_tool(self, name: str, arguments: Any) -> Any:
        """Invoke one MCP tool via ``tools/call`` and return the raw
        result."""
        return self.call("tools/call", {"name": name, "arguments": arguments})

    def close(self) -> None:
        """Release the MCP connection."""
        self._stream.close()


def _decode_task_results(raw: Any) -> list[TaskResult]:
    """Decode a ``lenny/await_children`` result, accepting either a list
    (mode all/settled) or a single object (mode any)."""
    if isinstance(raw, list):
        return [TaskResult.from_wire(r) for r in raw]
    return [TaskResult.from_wire(raw)]


def _decode_parts(raw: Any) -> list[MessagePart]:
    """Decode a result that is either a bare MessagePart array or an
    object with a ``parts`` field."""
    if isinstance(raw, list):
        return [MessagePart.from_wire(p) for p in raw]
    if isinstance(raw, dict):
        return [MessagePart.from_wire(p) for p in raw.get("parts", [])]
    return []


class PlatformTools:
    """§15.7 platform MCP tool surface a Standard-level or Full-level
    runtime uses.

    It wraps the §15.4.3 intra-pod MCP client to the adapter's platform
    MCP server and exposes typed helpers for the §8.5 / §4.7 platform
    tool set. A handler reaches it through the ``tools`` argument of
    :meth:`Handler.on_message`.
    """

    def __init__(
        self,
        platform: McpClient,
        connectors: dict[str, McpClient],
    ) -> None:
        self._platform = platform
        self._connectors = connectors

    @classmethod
    def dial(cls, manifest: AdapterManifest, timeout_s: float) -> PlatformTools:
        """Dial the §15.4.3 platform MCP server and every connector MCP
        server advertised in the manifest, completing the
        manifest-nonce handshake on each."""
        if manifest.platform_mcp_server is None:
            raise RuntimeError(
                "adapter manifest has no platform MCP server socket"
            )
        platform = McpClient.connect(
            manifest.platform_mcp_server.socket,
            manifest.mcp_nonce,
            "lenny-runtime-sdk-python",
            timeout_s,
        )
        connectors: dict[str, McpClient] = {}
        for conn in manifest.connector_servers:
            try:
                connectors[conn.id] = McpClient.connect(
                    conn.socket,
                    manifest.mcp_nonce,
                    "lenny-runtime-sdk-python",
                    timeout_s,
                )
            except Exception as err:
                platform.close()
                for client in connectors.values():
                    client.close()
                raise RuntimeError(
                    f"connect connector MCP server {conn.id!r}: {err}"
                ) from err
        return cls(platform, connectors)

    def close(self) -> None:
        """Release the platform and connector MCP connections."""
        self._platform.close()
        for client in self._connectors.values():
            client.close()

    def connector(self, connector_id: str) -> McpClient | None:
        """Return the MCP client for the named §4.7 connector MCP
        server, or None when no connector with that id was advertised."""
        return self._connectors.get(connector_id)

    def delegate_task(
        self,
        target: str,
        parts: list[MessagePart],
        budget: dict[str, Any] | None = None,
    ) -> TaskHandle:
        """Invoke the §8.2 ``lenny/delegate_task`` platform tool.

        It spawns a child sub-task whose input is ``parts`` and returns
        the child :class:`TaskHandle`. ``budget``, when set, is
        forwarded as the delegation budget metadata the §8.3 policy
        validates.
        """
        task: dict[str, Any] = {"input": _stamp_parts(parts)}
        if budget is not None:
            task["budget"] = budget
        raw = self._platform.call_tool(
            "lenny/delegate_task", {"target": target, "task": task}
        )
        task_id = str(raw.get("taskId", "")) if isinstance(raw, dict) else ""
        if not task_id:
            raise RuntimeError("lenny/delegate_task returned an empty taskId")
        return TaskHandle(task_id=task_id)

    def await_children(
        self, child_ids: list[str], mode: str = "all"
    ) -> list[TaskResult]:
        """Invoke the §8.5 ``lenny/await_children`` platform tool.

        It blocks until the named children settle per ``mode`` (all,
        any, settled) and returns their :class:`TaskResult` values.
        """
        raw = self._platform.call_tool(
            "lenny/await_children", {"child_ids": child_ids, "mode": mode}
        )
        return _decode_task_results(raw)

    def cancel_child(self, child_id: str) -> None:
        """Invoke the §8.5 ``lenny/cancel_child`` platform tool."""
        self._platform.call_tool("lenny/cancel_child", {"child_id": child_id})

    def discover_agents(self, query: dict[str, Any]) -> Any:
        """Invoke the §4.7 ``lenny/discover_agents`` platform tool and
        return the raw result for the runtime to decode."""
        return self._platform.call_tool("lenny/discover_agents", query)

    def output(self, parts: list[MessagePart]) -> None:
        """Invoke the §4.7 ``lenny/output`` platform tool, emitting
        output parts incrementally to the parent or client.

        The stdout response frame is still required to signal turn
        completion (§15.4.1).
        """
        self._platform.call_tool("lenny/output", {"output": _stamp_parts(parts)})

    def request_input(self, prompt: list[MessagePart]) -> list[MessagePart]:
        """Invoke the §4.7 ``lenny/request_input`` platform tool.

        It blocks until an answer arrives and returns the answer parts.
        """
        raw = self._platform.call_tool(
            "lenny/request_input", {"parts": _stamp_parts(prompt)}
        )
        return _decode_parts(raw)

    def request_elicitation(self, args: dict[str, Any]) -> Any:
        """Invoke the §4.7 ``lenny/request_elicitation`` platform tool
        and return the raw result."""
        return self._platform.call_tool("lenny/request_elicitation", args)

    def send_message(self, args: dict[str, Any]) -> Any:
        """Invoke the §4.7 ``lenny/send_message`` platform tool and
        return the raw delivery_receipt for the runtime to decode."""
        return self._platform.call_tool("lenny/send_message", args)

    def memory_write(self, args: dict[str, Any]) -> None:
        """Invoke the §4.7 ``lenny/memory_write`` platform tool."""
        self._platform.call_tool("lenny/memory_write", args)

    def memory_query(self, args: dict[str, Any]) -> Any:
        """Invoke the §4.7 ``lenny/memory_query`` platform tool and
        return the raw result."""
        return self._platform.call_tool("lenny/memory_query", args)

    def get_task_tree(self, args: dict[str, Any]) -> Any:
        """Invoke the §4.7 ``lenny/get_task_tree`` platform tool and
        return the raw result."""
        return self._platform.call_tool("lenny/get_task_tree", args)

    def set_tracing_context(self, ctx: dict[str, Any]) -> None:
        """Invoke the §4.7 ``lenny/set_tracing_context`` platform tool,
        registering tracing identifiers that propagate through
        delegation (§16.3)."""
        self._platform.call_tool("lenny/set_tracing_context", ctx)

    def call(self, name: str, arguments: dict[str, Any]) -> Any:
        """Invoke an arbitrary tool on the platform MCP server by name.

        It is the escape hatch for tools the typed helpers do not cover.
        """
        return self._platform.call_tool(name, arguments)
