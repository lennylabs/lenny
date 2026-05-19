# SPDX-License-Identifier: MIT

"""Official Python client SDK for the Lenny gateway.

The package wraps the section 15.1 REST session API and the section
15.6 SDK contract surface so application developers do not re-implement
the wire protocol from the spec.

A :class:`Client` is constructed with a gateway base URL and an
authentication credential. Its methods cover the session lifecycle
(create, get, list, delete, finalize, start, interrupt, terminate,
resume), decode the section 15.1 error envelope into the typed
:class:`APIError`, support per-request idempotency keys, and retry
retryable errors with exponential backoff and jitter.

:meth:`Client.stream_events` consumes the section 15.1
``GET /v1/sessions/{id}/events`` Server-Sent Events stream as an
iterator, reconnecting with the ``Last-Event-ID`` cursor on a
disconnect.

:meth:`Client.mcp` returns an :class:`~lenny.mcp.MCPClient` that drives
the section 15.2 gateway MCP API over JSON-RPC 2.0: the ``initialize``
handshake, ``tools/list`` tool discovery, and ``tools/call`` invocation
of the platform tools.

:class:`AsyncClient` exposes the same surface behind ``async``/``await``.
The :mod:`lenny.webhook` module holds the section 14 webhook signature
verifier.

The transport uses only the Python standard library, so the package
runs without a third-party dependency install.
"""

from __future__ import annotations

from .auth import APIKey, Authenticator, BearerToken, RefreshingToken
from .client import (
    DEFAULT_TIMEOUT,
    USER_AGENT,
    AsyncClient,
    Client,
    RequestOptions,
)
from .errors import (
    CATEGORY_PERMANENT,
    CATEGORY_POLICY,
    CATEGORY_TRANSIENT,
    CATEGORY_UPSTREAM,
    APIError,
    LennyError,
    TransportError,
    is_retryable,
)
from .mcp import (
    AsyncMCPClient,
    InitializeResult,
    MCPClient,
    MCPContent,
    MCPCreateSessionResult,
    MCPError,
    MCPServerInfo,
    MCPTool,
    MCPToolResult,
)
from .retry import DEFAULT_RETRY_POLICY, RetryPolicy
from .stream import (
    AsyncEventStream,
    EventStream,
    StreamEvent,
    StreamOptions,
)
from .types import (
    STATE_CANCELLED,
    STATE_COMPLETED,
    STATE_CREATED,
    STATE_EXPIRED,
    STATE_FAILED,
    STATE_READY,
    STATE_RUNNING,
    STATE_SUSPENDED,
    CreateSessionRequest,
    CreateSessionResult,
    IsolationLevel,
    ListOptions,
    Session,
    SessionPage,
)

__version__ = "0.1.0"

__all__ = [
    "__version__",
    # client
    "Client",
    "AsyncClient",
    "RequestOptions",
    "USER_AGENT",
    "DEFAULT_TIMEOUT",
    # auth
    "Authenticator",
    "BearerToken",
    "APIKey",
    "RefreshingToken",
    # errors
    "LennyError",
    "APIError",
    "TransportError",
    "is_retryable",
    "CATEGORY_TRANSIENT",
    "CATEGORY_PERMANENT",
    "CATEGORY_POLICY",
    "CATEGORY_UPSTREAM",
    # retry
    "RetryPolicy",
    "DEFAULT_RETRY_POLICY",
    # streaming
    "EventStream",
    "AsyncEventStream",
    "StreamEvent",
    "StreamOptions",
    # mcp
    "MCPClient",
    "AsyncMCPClient",
    "MCPError",
    "MCPTool",
    "MCPToolResult",
    "MCPContent",
    "MCPServerInfo",
    "InitializeResult",
    "MCPCreateSessionResult",
    # types
    "CreateSessionRequest",
    "CreateSessionResult",
    "Session",
    "SessionPage",
    "ListOptions",
    "IsolationLevel",
    "STATE_CREATED",
    "STATE_READY",
    "STATE_RUNNING",
    "STATE_SUSPENDED",
    "STATE_COMPLETED",
    "STATE_CANCELLED",
    "STATE_FAILED",
    "STATE_EXPIRED",
]
