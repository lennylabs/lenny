# SPDX-License-Identifier: MIT

"""The section 15.2 gateway MCP client.

:class:`MCPClient` drives the section 15.2 gateway MCP API. It speaks
JSON-RPC 2.0 over HTTP POST to the gateway's ``/mcp`` endpoint: the same
connection carries the ``initialize`` handshake, ``tools/list`` tool
discovery, and ``tools/call`` invocation of the platform tools
(``lenny/create_session``, ``lenny/send_message``, and the others).

Construct an :class:`MCPClient` with :meth:`~lenny.client.Client.mcp` so
it inherits the REST client's base URL, authentication credential, and
development tenant header. The transport uses only the Python standard
library (:mod:`urllib.request`). An :class:`AsyncMCPClient` offers the
same surface behind ``async``/``await`` by running each blocking call on
a worker thread.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Callable, Optional, TypeVar

from .auth import Authenticator
from .errors import APIError, LennyError, TransportError

#: Identifies the SDK in the ``User-Agent`` request header.
_MCP_USER_AGENT = "lenny-client-sdk-python"

#: The MCP protocol revision this client requests in the section 15.2
#: ``initialize`` handshake. The gateway negotiates the highest version
#: it and the client both support; the negotiated value is reported on
#: :attr:`InitializeResult.protocol_version`.
_MCP_PROTOCOL_VERSION = "2025-06-18"

#: The client identity sent in the section 15.2 ``initialize``
#: ``clientInfo``.
_MCP_CLIENT_NAME = "lenny-client-sdk-python"

#: Return type of a worker-thread call dispatched by
#: :class:`AsyncMCPClient`.
_T = TypeVar("_T")


class MCPError(LennyError):
    """Typed form of a section 15.2 JSON-RPC error object.

    A ``tools/call`` that fails at the transport level (unknown tool,
    invalid params) raises this error; a tool that runs and reports a
    failure returns an :class:`MCPToolResult` with ``is_error`` set
    instead.
    """

    def __init__(
        self, code: int = 0, message: str = "", data: Optional[Any] = None
    ) -> None:
        #: The JSON-RPC error code. The MCP and JSON-RPC 2.0 reserved
        #: codes are negative (for example -32601 method not found,
        #: -32602 invalid params).
        self.code: int = code

        #: The human-readable error description.
        self.message: str = message

        #: Error-specific context, when the gateway supplies it.
        self.data: Optional[Any] = data

        super().__init__(f"lenny: MCP error {code}: {message}")


@dataclass
class MCPContent:
    """One content block in an MCP tool result."""

    #: The content block type. The gateway tools emit ``text``.
    type: str = ""

    #: The inline text when :attr:`type` is ``text``.
    text: str = ""


@dataclass
class MCPToolResult:
    """The section 15.2 ``tools/call`` result.

    A tool that reports a failure returns ``is_error`` true with the
    failure text in :attr:`content`; the JSON-RPC transport itself
    succeeded.
    """

    #: The list of result content blocks.
    content: list[MCPContent] = field(default_factory=list)

    #: Whether the tool reported a failure. The MCP spec surfaces a
    #: tool-level failure as a result with this flag set rather than as
    #: a transport error.
    is_error: bool = False

    def text(self) -> str:
        """Return the concatenation of every text content block."""
        return "".join(c.text for c in self.content if c.type == "text")

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "MCPToolResult":
        """Decode a ``tools/call`` result object."""
        blocks = raw.get("content") or []
        content = [
            MCPContent(
                type=str(b.get("type", "")),
                text=str(b.get("text", "")),
            )
            for b in blocks
            if isinstance(b, dict)
        ]
        return cls(content=content, is_error=bool(raw.get("isError", False)))


@dataclass
class MCPTool:
    """One entry in the section 15.2 MCP tool catalog."""

    #: The tool identifier, for example ``lenny/create_session``.
    name: str = ""

    #: The human-readable tool description.
    description: str = ""

    #: The JSON Schema for the tool's arguments object.
    input_schema: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "MCPTool":
        """Decode a ``tools/list`` catalog entry."""
        schema = raw.get("inputSchema")
        return cls(
            name=str(raw.get("name", "")),
            description=str(raw.get("description", "")),
            input_schema=schema if isinstance(schema, dict) else {},
        )


@dataclass
class MCPServerInfo:
    """Identifies an MCP server in the ``initialize`` response."""

    name: str = ""
    version: str = ""


@dataclass
class InitializeResult:
    """The section 15.2 ``initialize`` handshake response."""

    #: The MCP spec version the gateway negotiated. The connection is
    #: pinned to this version for its lifetime.
    protocol_version: str = ""

    #: The gateway's advertised MCP capability set.
    capabilities: dict[str, Any] = field(default_factory=dict)

    #: The identity of the gateway MCP server.
    server_info: MCPServerInfo = field(default_factory=MCPServerInfo)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "InitializeResult":
        """Decode an ``initialize`` result object."""
        info = raw.get("serverInfo")
        server = MCPServerInfo()
        if isinstance(info, dict):
            server = MCPServerInfo(
                name=str(info.get("name", "")),
                version=str(info.get("version", "")),
            )
        caps = raw.get("capabilities")
        return cls(
            protocol_version=str(raw.get("protocolVersion", "")),
            capabilities=caps if isinstance(caps, dict) else {},
            server_info=server,
        )


@dataclass
class MCPCreateSessionResult:
    """The decoded result of the ``lenny/create_session`` MCP tool."""

    #: The identifier of the created session.
    session_id: str = ""

    #: The session state the gateway reports for the new session.
    state: str = ""


@dataclass
class _MCPConfig:
    """The transport surface the MCP client reads.

    It is the subset of the :class:`~lenny.client.Client` internals the
    MCP client needs, passed in so the MCP module does not reach into the
    client's private state.
    """

    base_url: str
    timeout: float
    auth: Optional[Authenticator] = None
    tenant_id: str = ""


class MCPClient:
    """Synchronous section 15.2 gateway MCP client.

    A :class:`MCPClient` is safe for concurrent use by multiple threads.
    """

    def __init__(self, config: _MCPConfig) -> None:
        """Construct an MCP client from a transport snapshot.

        Application code does not call this directly; it uses
        :meth:`~lenny.client.Client.mcp`.
        """
        self._config = config
        self._endpoint = config.base_url + "/mcp"
        self._id_seq = 0
        self._initialized = False

    def initialize(self) -> InitializeResult:
        """Perform the section 15.2 MCP ``initialize`` handshake.

        It sends the client's supported protocol version and
        ``clientInfo``, and returns the gateway's negotiated protocol
        version, capability set, and ``serverInfo``.

        Calling :meth:`initialize` is optional before :meth:`list_tools`
        or :meth:`call_tool`: those methods perform the handshake on
        first use when it has not run yet. Call it explicitly to read
        the negotiated protocol version or the gateway ``serverInfo``.
        """
        raw = self._call(
            "initialize",
            {
                "protocolVersion": _MCP_PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": {
                    "name": _MCP_CLIENT_NAME,
                    "version": "0.1.0",
                },
            },
        )
        self._initialized = True
        return InitializeResult.from_wire(raw)

    def list_tools(self) -> list[MCPTool]:
        """Call the section 15.2 ``tools/list`` method.

        It returns the platform MCP tool catalog
        (``lenny/create_session``, ``lenny/send_message``, and the
        others). It runs the ``initialize`` handshake first when it has
        not run yet.
        """
        self._ensure_initialized()
        raw = self._call("tools/list", {})
        tools = raw.get("tools") or []
        return [MCPTool.from_wire(t) for t in tools if isinstance(t, dict)]

    def call_tool(
        self, name: str, arguments: Optional[dict[str, Any]] = None
    ) -> MCPToolResult:
        """Call the section 15.2 ``tools/call`` method.

        ``arguments`` is sent as the JSON-RPC ``arguments`` object; pass
        ``None`` or an empty mapping for a tool that takes no arguments.

        A transport-level failure (unknown tool, invalid params) raises
        an :class:`MCPError`. A tool that runs and reports a failure
        returns an :class:`MCPToolResult` with ``is_error`` set, matching
        the MCP contract that a tool failure is a result rather than a
        transport error. It runs the ``initialize`` handshake first when
        it has not run yet.

        Raises:
            ValueError: When ``name`` is empty.
        """
        self._ensure_initialized()
        if not name:
            raise ValueError("lenny: MCP tool name is required")
        raw = self._call(
            "tools/call", {"name": name, "arguments": arguments or {}}
        )
        return MCPToolResult.from_wire(raw)

    def create_session(
        self, runtime_ref: str, user_id: str = ""
    ) -> MCPCreateSessionResult:
        """Invoke the section 15.2 ``lenny/create_session`` MCP tool.

        It returns the created session identifier and state. It is the
        MCP counterpart of :meth:`~lenny.client.Client.create_session`.
        """
        args: dict[str, Any] = {"runtimeRef": runtime_ref}
        if user_id:
            args["userId"] = user_id
        result = self.call_tool("lenny/create_session", args)
        if result.is_error:
            raise MCPError(
                message=(
                    "lenny/create_session reported a failure: "
                    + result.text()
                )
            )
        decoded = json.loads(result.text())
        return MCPCreateSessionResult(
            session_id=str(decoded.get("sessionId", "")),
            state=str(decoded.get("state", "")),
        )

    def send_message(self, session_id: str, content: str) -> str:
        """Invoke the section 15.2 ``lenny/send_message`` MCP tool.

        It delivers ``content`` to the session and returns the agent's
        text reply. It is the MCP counterpart of a section 15.1
        send-message REST call.
        """
        result = self.call_tool(
            "lenny/send_message",
            {"sessionId": session_id, "content": content},
        )
        if result.is_error:
            raise MCPError(
                message=(
                    "lenny/send_message reported a failure: "
                    + result.text()
                )
            )
        return result.text()

    # -- internals --------------------------------------------------------

    def _ensure_initialized(self) -> None:
        """Run the ``initialize`` handshake once."""
        if not self._initialized:
            self.initialize()

    def _call(self, method: str, params: dict[str, Any]) -> dict[str, Any]:
        """Execute one JSON-RPC 2.0 method against the gateway endpoint.

        It returns the raw result object. A JSON-RPC error object in the
        response is raised as an :class:`MCPError`; a non-2xx HTTP status
        is raised as the typed REST :class:`APIError` so a single
        error-handling strategy covers both surfaces.
        """
        self._id_seq += 1
        body = json.dumps(
            {
                "jsonrpc": "2.0",
                "id": self._id_seq,
                "method": method,
                "params": params,
            }
        ).encode("utf-8")

        headers: dict[str, str] = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": _MCP_USER_AGENT,
        }
        if self._config.tenant_id:
            headers["X-Lenny-Tenant-ID"] = self._config.tenant_id
        if self._config.auth is not None:
            self._config.auth.apply(headers)

        request = urllib.request.Request(
            self._endpoint,
            data=body,
            headers=headers,
            method="POST",
        )
        try:
            with urllib.request.urlopen(
                request, timeout=self._config.timeout
            ) as resp:
                status, raw = resp.status, resp.read()
        except urllib.error.HTTPError as exc:
            # An HTTP error response is a complete round trip; the status
            # and body feed the section 15.1 envelope decoder below.
            status, raw = exc.code, exc.read()
        except urllib.error.URLError as exc:
            raise TransportError(
                f"lenny: MCP request failed: {exc.reason}"
            ) from exc
        except OSError as exc:
            raise TransportError(f"lenny: MCP request failed: {exc}") from exc

        # A JSON-RPC transport error still returns HTTP 200; the error is
        # in the body. A non-2xx status is a gateway-level failure (auth
        # rejection, an unmounted endpoint) and is surfaced as the typed
        # REST error.
        if not 200 <= status < 300:
            raise APIError.from_response(status, raw)

        try:
            envelope = json.loads(raw)
        except ValueError as exc:
            raise TransportError(
                f"lenny: decode MCP {method} response: {exc}"
            ) from exc
        if not isinstance(envelope, dict):
            raise TransportError(
                f"lenny: MCP {method} response is not a JSON object"
            )

        error = envelope.get("error")
        if isinstance(error, dict):
            raise MCPError(
                code=int(error.get("code", 0)),
                message=str(error.get("message", "unknown MCP error")),
                data=error.get("data"),
            )
        result = envelope.get("result")
        return result if isinstance(result, dict) else {}


class AsyncMCPClient:
    """Asynchronous section 15.2 gateway MCP client.

    :class:`AsyncMCPClient` exposes the same surface as
    :class:`MCPClient` behind ``async``/``await``. Each call runs the
    corresponding blocking :class:`MCPClient` method on a worker thread,
    so an ``await``-ing caller does not block the event loop.
    """

    def __init__(self, config: _MCPConfig) -> None:
        """Construct an async MCP client from a transport snapshot."""
        self._sync = MCPClient(config)

    async def initialize(self) -> InitializeResult:
        """Await the section 15.2 MCP ``initialize`` handshake."""
        return await self._run(self._sync.initialize)

    async def list_tools(self) -> list[MCPTool]:
        """Await the section 15.2 ``tools/list`` method."""
        return await self._run(self._sync.list_tools)

    async def call_tool(
        self, name: str, arguments: Optional[dict[str, Any]] = None
    ) -> MCPToolResult:
        """Await the section 15.2 ``tools/call`` method."""
        return await self._run(self._sync.call_tool, name, arguments)

    async def create_session(
        self, runtime_ref: str, user_id: str = ""
    ) -> MCPCreateSessionResult:
        """Await the ``lenny/create_session`` MCP tool."""
        return await self._run(self._sync.create_session, runtime_ref, user_id)

    async def send_message(self, session_id: str, content: str) -> str:
        """Await the ``lenny/send_message`` MCP tool."""
        return await self._run(self._sync.send_message, session_id, content)

    @staticmethod
    async def _run(func: Callable[..., _T], *args: Any) -> _T:
        """Run a blocking MCP client method on a worker thread."""
        import asyncio
        import functools

        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(None, functools.partial(func, *args))
