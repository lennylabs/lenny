# SPDX-License-Identifier: MIT

"""The section 15.1 REST gateway client.

:class:`Client` wraps the session lifecycle endpoints: create, get,
list, delete, finalize, start, interrupt, terminate, and resume. It
decodes the section 15.1 error envelope into :class:`~lenny.errors.APIError`,
supports per-request idempotency keys, and retries retryable errors
with exponential backoff and jitter.

The client uses only the Python standard library for transport
(:mod:`urllib.request`). An :class:`AsyncClient` offers the same surface
behind ``async``/``await`` by running each blocking call on a worker
thread.
"""

from __future__ import annotations

import json
import random
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, AsyncIterator, Callable, Iterator, Optional, TypeVar

from .auth import Authenticator
from .errors import APIError, TransportError
from .mcp import AsyncMCPClient, MCPClient, _MCPConfig
from .retry import DEFAULT_RETRY_POLICY, RetryPolicy
from .stream import (
    AsyncEventStream,
    EventStream,
    StreamOptions,
    _StreamConfig,
)
from .types import (
    CreateSessionRequest,
    CreateSessionResult,
    InteractionResolution,
    ListOptions,
    MessagePayload,
    SendMessagesResponse,
    Session,
    SessionPage,
    TranscriptOptions,
    TranscriptResponse,
)

#: Identifies the SDK in the ``User-Agent`` request header.
USER_AGENT = "lenny-client-sdk-python"

#: Default request timeout in seconds.
DEFAULT_TIMEOUT = 30.0

#: Return type of a worker-thread call dispatched by :class:`AsyncClient`.
_T = TypeVar("_T")


class RequestOptions:
    """Per-call settings threaded through a single request.

    Construct one and pass it as the ``options`` keyword argument of a
    request method.
    """

    def __init__(self, idempotency_key: str = "") -> None:
        #: When set, attaches an ``Idempotency-Key`` header to the
        #: request. The section 11.5 gateway middleware replays the
        #: cached response when the same key is presented again with the
        #: same body within the key's TTL.
        self.idempotency_key: str = idempotency_key


class Client:
    """Synchronous section 15.1 REST gateway client.

    A :class:`Client` is safe for concurrent use by multiple threads.
    """

    def __init__(
        self,
        base_url: str,
        auth: Optional[Authenticator] = None,
        tenant_id: str = "",
        retry_policy: Optional[RetryPolicy] = None,
        timeout: float = DEFAULT_TIMEOUT,
        clock: Optional[Callable[[], float]] = None,
    ) -> None:
        """Construct a client bound to ``base_url``.

        Args:
            base_url: The gateway origin (for example
                ``https://gateway.acme.com``); the ``/v1`` path prefix
                is appended by the SDK.
            auth: The credential attached to every request. See
                :mod:`lenny.auth`.
            tenant_id: The development ``X-Lenny-Tenant-ID`` header. The
                minimal gateway honors this header to scope requests to
                a tenant when no OIDC principal is present. Production
                deployments derive the tenant from the authenticated
                principal and ignore the header.
            retry_policy: Overrides :data:`~lenny.retry.DEFAULT_RETRY_POLICY`.
            timeout: Per-request timeout in seconds.
            clock: Overrides the time source. Tests inject a
                deterministic clock; production leaves this unset.

        Raises:
            ValueError: When ``base_url`` is empty or is not an absolute
                URL.
        """
        if not base_url:
            raise ValueError("lenny: base URL is required")
        parsed = urllib.parse.urlparse(base_url)
        if not parsed.scheme or not parsed.netloc:
            raise ValueError(f"lenny: base URL {base_url!r} must be absolute")
        self._base_url = base_url.rstrip("/")
        self._auth = auth
        self._tenant_id = tenant_id
        self._retry = (retry_policy or DEFAULT_RETRY_POLICY).normalized()
        self._timeout = timeout
        self._clock = clock or time.monotonic
        self._rng = random.Random()

    # -- session lifecycle ------------------------------------------------

    def create_session(
        self,
        req: CreateSessionRequest,
        options: Optional[RequestOptions] = None,
    ) -> CreateSessionResult:
        """Call ``POST /v1/sessions``.

        The request is retried on a retryable error per the client's
        retry policy. Pass a :class:`RequestOptions` with an
        ``idempotency_key`` so a retry is collapsed by the gateway's
        idempotency middleware rather than producing a duplicate
        session.
        """
        raw = self._do("POST", "/v1/sessions", req.to_wire(), options)
        return CreateSessionResult.from_wire(raw or {})

    def get_session(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Call ``GET /v1/sessions/{id}``."""
        path = "/v1/sessions/" + urllib.parse.quote(session_id, safe="")
        raw = self._do("GET", path, None, options)
        return Session.from_wire(raw or {})

    def list_sessions(
        self,
        opt: Optional[ListOptions] = None,
        options: Optional[RequestOptions] = None,
    ) -> SessionPage:
        """Call ``GET /v1/sessions``, applying the filters in ``opt``.

        It returns one page; iterate with the returned ``next_cursor``
        or use :meth:`iterate_sessions` for an all-pages helper.
        """
        opt = opt or ListOptions()
        query: dict[str, str] = {}
        if opt.state:
            query["state"] = opt.state
        if opt.runtime:
            query["runtime"] = opt.runtime
        if opt.cursor:
            query["cursor"] = opt.cursor
        if opt.limit > 0:
            query["limit"] = str(opt.limit)
        path = "/v1/sessions"
        if query:
            path += "?" + urllib.parse.urlencode(query)
        raw = self._do("GET", path, None, options)
        return SessionPage.from_wire(raw or {})

    def iterate_sessions(
        self,
        opt: Optional[ListOptions] = None,
        options: Optional[RequestOptions] = None,
    ) -> Iterator[Session]:
        """Yield every session across every page of a listing.

        It is the cursor-iteration helper for the section 15.6
        pagination-cursors contract. Iteration stops when the listing is
        exhausted.
        """
        opt = opt or ListOptions()
        cursor = opt.cursor
        while True:
            page_opt = ListOptions(
                state=opt.state,
                runtime=opt.runtime,
                cursor=cursor,
                limit=opt.limit,
            )
            page = self.list_sessions(page_opt, options)
            for session in page.sessions:
                yield session
            if not page.has_more or not page.next_cursor:
                return
            cursor = page.next_cursor

    def stream_events(
        self,
        session_id: str,
        options: Optional[StreamOptions] = None,
    ) -> EventStream:
        """Open the section 15.1 ``GET /v1/sessions/{id}/events`` stream.

        It returns an :class:`~lenny.stream.EventStream`, a synchronous
        iterator of decoded :class:`~lenny.stream.StreamEvent` values.
        Iterate it with a ``for`` loop, or use it as a context manager
        so the connection is closed on block exit.

        The iterator reconnects on a transport disconnect, resuming from
        the ``Last-Event-ID`` cursor of the last delivered event, and
        drops any replayed event at or below that cursor so the section
        15.1 reconnect contract holds with no gap and no duplicate.
        Reconnect attempts are spaced by the client's retry policy
        backoff. A non-retryable HTTP status ends the stream by raising
        the typed :class:`~lenny.errors.APIError`; a retryable status
        (429, 5xx) is retried like a disconnect.
        """
        return EventStream(self._stream_config(), session_id, options)

    def _stream_config(self) -> _StreamConfig:
        """Build the transport snapshot the event stream reads."""
        return _StreamConfig(
            base_url=self._base_url,
            timeout=self._timeout,
            retry=self._retry,
            auth=self._auth,
            tenant_id=self._tenant_id,
            rng=self._rng,
        )

    def mcp(self) -> MCPClient:
        """Return an :class:`~lenny.mcp.MCPClient` for the section 15.2 API.

        The MCP client drives the section 15.2 gateway MCP API over
        JSON-RPC 2.0: the ``initialize`` handshake, ``tools/list`` tool
        discovery, and ``tools/call`` invocation of the platform tools.
        It targets the gateway's ``/mcp`` endpoint and reuses this
        client's base URL, authentication credential, and development
        tenant header.
        """
        return MCPClient(self._mcp_config())

    def _mcp_config(self) -> _MCPConfig:
        """Build the transport snapshot the MCP client reads."""
        return _MCPConfig(
            base_url=self._base_url,
            timeout=self._timeout,
            auth=self._auth,
            tenant_id=self._tenant_id,
        )

    def delete_session(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Call ``DELETE /v1/sessions/{id}``.

        The section 15.1 contract moves a non-terminal session to the
        cancelled state and returns the updated envelope.
        """
        return self._transition("DELETE", session_id, "", options)

    def finalize(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Call ``POST /v1/sessions/{id}/finalize``.

        It transitions a created session to ready.
        """
        return self._transition("POST", session_id, "finalize", options)

    def start(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Call ``POST /v1/sessions/{id}/start``.

        It transitions a ready session to running.
        """
        return self._transition("POST", session_id, "start", options)

    def interrupt(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Call ``POST /v1/sessions/{id}/interrupt``.

        It transitions a running session to suspended.
        """
        return self._transition("POST", session_id, "interrupt", options)

    def terminate(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Call ``POST /v1/sessions/{id}/terminate``.

        It transitions a non-terminal session to completed.
        """
        return self._transition("POST", session_id, "terminate", options)

    def resume(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Call ``POST /v1/sessions/{id}/resume``.

        It transitions a session awaiting client action back to running.
        """
        return self._transition("POST", session_id, "resume", options)

    # -- messages / transcript / interactions ---------------------------

    def send_messages(
        self,
        session_id: str,
        messages: list[MessagePayload],
        options: Optional[RequestOptions] = None,
    ) -> SendMessagesResponse:
        """Call ``POST /v1/sessions/{id}/messages``.

        Returns the section 15.4 delivery receipt plus the executor's
        synchronous output. Each payload may carry ``inReplyTo``,
        ``delivery``, and ``slotId``; see :class:`MessagePayload`.

        spec: section 15.1; section 15.4 lines 1725-1737;
        section 7.2 line 345.
        """
        if not messages:
            raise ValueError("lenny: send_messages requires at least one message")
        body = {"messages": [m.to_wire() for m in messages]}
        path = "/v1/sessions/" + urllib.parse.quote(session_id, safe="") + "/messages"
        raw = self._do("POST", path, body, options)
        return SendMessagesResponse.from_wire(raw or {})

    def get_transcript(
        self,
        session_id: str,
        opt: Optional[TranscriptOptions] = None,
        options: Optional[RequestOptions] = None,
    ) -> TranscriptResponse:
        """Call ``GET /v1/sessions/{id}/transcript``.

        spec: section 15.1.
        """
        opt = opt or TranscriptOptions()
        query: dict[str, str] = {}
        if opt.after_seq > 0:
            query["afterSeq"] = str(opt.after_seq)
        if opt.limit > 0:
            query["limit"] = str(opt.limit)
        path = "/v1/sessions/" + urllib.parse.quote(session_id, safe="") + "/transcript"
        if query:
            path += "?" + urllib.parse.urlencode(query)
        raw = self._do("GET", path, None, options)
        return TranscriptResponse.from_wire(raw or {})

    def approve_tool_use(
        self,
        session_id: str,
        tool_call_id: str,
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Call ``POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve``.

        spec: section 7.2 table line 124; section 15.1.
        """
        path = (
            "/v1/sessions/"
            + urllib.parse.quote(session_id, safe="")
            + "/tool-use/"
            + urllib.parse.quote(tool_call_id, safe="")
            + "/approve"
        )
        raw = self._do("POST", path, {}, options)
        return InteractionResolution.from_wire(raw or {})

    def deny_tool_use(
        self,
        session_id: str,
        tool_call_id: str,
        reason: str = "",
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Call ``POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny``.

        spec: section 7.2 table line 125; section 15.1.
        """
        body: dict[str, Any] = {}
        if reason:
            body["reason"] = reason
        path = (
            "/v1/sessions/"
            + urllib.parse.quote(session_id, safe="")
            + "/tool-use/"
            + urllib.parse.quote(tool_call_id, safe="")
            + "/deny"
        )
        raw = self._do("POST", path, body, options)
        return InteractionResolution.from_wire(raw or {})

    def respond_elicitation(
        self,
        session_id: str,
        elicitation_id: str,
        response: Any,
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Call ``POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond``.

        spec: section 7.2 table line 126; section 9.2; section 15.1.
        """
        body = {"response": response}
        path = (
            "/v1/sessions/"
            + urllib.parse.quote(session_id, safe="")
            + "/elicitations/"
            + urllib.parse.quote(elicitation_id, safe="")
            + "/respond"
        )
        raw = self._do("POST", path, body, options)
        return InteractionResolution.from_wire(raw or {})

    def dismiss_elicitation(
        self,
        session_id: str,
        elicitation_id: str,
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Call ``POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss``.

        spec: section 7.2 table line 127; section 9.2; section 15.1.
        """
        path = (
            "/v1/sessions/"
            + urllib.parse.quote(session_id, safe="")
            + "/elicitations/"
            + urllib.parse.quote(elicitation_id, safe="")
            + "/dismiss"
        )
        raw = self._do("POST", path, {}, options)
        return InteractionResolution.from_wire(raw or {})

    # -- internals --------------------------------------------------------

    def _transition(
        self,
        method: str,
        session_id: str,
        action: str,
        options: Optional[RequestOptions],
    ) -> Session:
        """Issue a no-body state-mutating call.

        ``action`` is the trailing path segment (``finalize``,
        ``start``, ...); an empty action targets the bare
        ``/v1/sessions/{id}`` path for DELETE.
        """
        path = "/v1/sessions/" + urllib.parse.quote(session_id, safe="")
        if action:
            path += "/" + action
        raw = self._do(method, path, None, options)
        return Session.from_wire(raw or {})

    def _do(
        self,
        method: str,
        path: str,
        body: Optional[dict[str, Any]],
        options: Optional[RequestOptions],
    ) -> Optional[dict[str, Any]]:
        """Execute one REST call with retry.

        It serializes ``body`` to JSON when non-empty, sends the
        request, retries retryable failures per the client's retry
        policy, and decodes a 2xx response body. A non-2xx response is
        raised as an :class:`APIError`.
        """
        body_bytes: Optional[bytes] = None
        if body is not None:
            body_bytes = json.dumps(body).encode("utf-8")

        policy = self._retry
        last_error: Optional[BaseException] = None
        for attempt in range(1, policy.max_attempts + 1):
            if attempt > 1:
                delay = policy.delay_for_attempt(attempt - 1, self._rng)
                if delay > 0:
                    time.sleep(delay)

            try:
                status, resp_body = self._round_trip(
                    method, path, body_bytes, options
                )
            except TransportError as exc:
                # A transport-level failure (connection refused, DNS)
                # is retried like a TRANSIENT error.
                last_error = exc
                continue

            if 200 <= status < 300:
                if not resp_body:
                    return None
                try:
                    decoded = json.loads(resp_body)
                except ValueError as exc:
                    raise TransportError(
                        f"lenny: decode {method} {path} response: {exc}"
                    ) from exc
                return decoded if isinstance(decoded, dict) else None

            api_error = APIError.from_response(status, resp_body)
            last_error = api_error
            if api_error.retryable and attempt < policy.max_attempts:
                continue
            raise api_error

        assert last_error is not None
        raise last_error

    def _round_trip(
        self,
        method: str,
        path: str,
        body_bytes: Optional[bytes],
        options: Optional[RequestOptions],
    ) -> tuple[int, bytes]:
        """Perform a single HTTP request.

        It returns the status code and the response body and does not
        interpret the body. A transport failure is raised as a
        :class:`TransportError`.
        """
        headers: dict[str, str] = {
            "Accept": "application/json",
            "User-Agent": USER_AGENT,
        }
        if body_bytes is not None:
            headers["Content-Type"] = "application/json"
        if options and options.idempotency_key:
            headers["Idempotency-Key"] = options.idempotency_key
        if self._tenant_id:
            headers["X-Lenny-Tenant-ID"] = self._tenant_id
        if self._auth is not None:
            self._auth.apply(headers)

        request = urllib.request.Request(
            self._base_url + path,
            data=body_bytes,
            headers=headers,
            method=method,
        )
        try:
            with urllib.request.urlopen(
                request, timeout=self._timeout
            ) as resp:
                return resp.status, resp.read()
        except urllib.error.HTTPError as exc:
            # An HTTP error response is a complete round trip; the
            # status and body feed the section 15.1 envelope decoder.
            return exc.code, exc.read()
        except urllib.error.URLError as exc:
            raise TransportError(f"lenny: request failed: {exc.reason}") from exc
        except OSError as exc:
            raise TransportError(f"lenny: request failed: {exc}") from exc


class AsyncClient:
    """Asynchronous section 15.1 REST gateway client.

    :class:`AsyncClient` exposes the same surface as :class:`Client`
    behind ``async``/``await``. Each call runs the corresponding
    blocking :class:`Client` method on a worker thread, so an
    ``await``-ing caller does not block the event loop.
    """

    def __init__(self, base_url: str, **kwargs: Any) -> None:
        """Construct an async client.

        It accepts the same arguments as :class:`Client`.
        """
        self._sync = Client(base_url, **kwargs)

    async def create_session(
        self,
        req: CreateSessionRequest,
        options: Optional[RequestOptions] = None,
    ) -> CreateSessionResult:
        """Await ``POST /v1/sessions``."""
        return await self._run(self._sync.create_session, req, options)

    async def get_session(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Await ``GET /v1/sessions/{id}``."""
        return await self._run(self._sync.get_session, session_id, options)

    async def list_sessions(
        self,
        opt: Optional[ListOptions] = None,
        options: Optional[RequestOptions] = None,
    ) -> SessionPage:
        """Await ``GET /v1/sessions``."""
        return await self._run(self._sync.list_sessions, opt, options)

    async def iterate_sessions(
        self,
        opt: Optional[ListOptions] = None,
        options: Optional[RequestOptions] = None,
    ) -> "AsyncIterator[Session]":
        """Yield every session across every page of a listing.

        It is the async counterpart of :meth:`Client.iterate_sessions`.
        Each page fetch runs on a worker thread, so an ``async for``
        caller does not block the event loop.
        """
        opt = opt or ListOptions()
        cursor = opt.cursor
        while True:
            page_opt = ListOptions(
                state=opt.state,
                runtime=opt.runtime,
                cursor=cursor,
                limit=opt.limit,
            )
            page = await self.list_sessions(page_opt, options)
            for session in page.sessions:
                yield session
            if not page.has_more or not page.next_cursor:
                return
            cursor = page.next_cursor

    async def stream_events(
        self,
        session_id: str,
        options: Optional[StreamOptions] = None,
    ) -> AsyncEventStream:
        """Open the section 15.1 ``GET /v1/sessions/{id}/events`` stream.

        It returns an :class:`~lenny.stream.AsyncEventStream`, the
        ``async`` counterpart of the iterator
        :meth:`Client.stream_events` returns. Iterate it with
        ``async for``, or use it as an async context manager so the
        connection is closed on block exit. Each blocking connection
        and frame read runs on a worker thread, so an iterating caller
        does not block the event loop.

        The method is a coroutine so its call site mirrors the other
        :class:`AsyncClient` methods; ``await`` it to obtain the
        stream.
        """
        return AsyncEventStream(self._sync._stream_config(), session_id, options)

    async def mcp(self) -> AsyncMCPClient:
        """Return an :class:`~lenny.mcp.AsyncMCPClient` for the section 15.2 API.

        It is the ``async`` counterpart of :meth:`Client.mcp`. Each MCP
        call runs the corresponding blocking method on a worker thread,
        so an ``await``-ing caller does not block the event loop.

        The method is a coroutine so its call site mirrors the other
        :class:`AsyncClient` methods; ``await`` it to obtain the MCP
        client.
        """
        return AsyncMCPClient(self._sync._mcp_config())

    async def delete_session(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Await ``DELETE /v1/sessions/{id}``."""
        return await self._run(self._sync.delete_session, session_id, options)

    async def finalize(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Await ``POST /v1/sessions/{id}/finalize``."""
        return await self._run(self._sync.finalize, session_id, options)

    async def start(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Await ``POST /v1/sessions/{id}/start``."""
        return await self._run(self._sync.start, session_id, options)

    async def interrupt(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Await ``POST /v1/sessions/{id}/interrupt``."""
        return await self._run(self._sync.interrupt, session_id, options)

    async def terminate(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Await ``POST /v1/sessions/{id}/terminate``."""
        return await self._run(self._sync.terminate, session_id, options)

    async def resume(
        self, session_id: str, options: Optional[RequestOptions] = None
    ) -> Session:
        """Await ``POST /v1/sessions/{id}/resume``."""
        return await self._run(self._sync.resume, session_id, options)

    async def send_messages(
        self,
        session_id: str,
        messages: list[MessagePayload],
        options: Optional[RequestOptions] = None,
    ) -> SendMessagesResponse:
        """Await ``POST /v1/sessions/{id}/messages``."""
        return await self._run(self._sync.send_messages, session_id, messages, options)

    async def get_transcript(
        self,
        session_id: str,
        opt: Optional[TranscriptOptions] = None,
        options: Optional[RequestOptions] = None,
    ) -> TranscriptResponse:
        """Await ``GET /v1/sessions/{id}/transcript``."""
        return await self._run(self._sync.get_transcript, session_id, opt, options)

    async def approve_tool_use(
        self,
        session_id: str,
        tool_call_id: str,
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Await ``POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve``."""
        return await self._run(
            self._sync.approve_tool_use, session_id, tool_call_id, options
        )

    async def deny_tool_use(
        self,
        session_id: str,
        tool_call_id: str,
        reason: str = "",
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Await ``POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny``."""
        return await self._run(
            self._sync.deny_tool_use, session_id, tool_call_id, reason, options
        )

    async def respond_elicitation(
        self,
        session_id: str,
        elicitation_id: str,
        response: Any,
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Await ``POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond``."""
        return await self._run(
            self._sync.respond_elicitation,
            session_id,
            elicitation_id,
            response,
            options,
        )

    async def dismiss_elicitation(
        self,
        session_id: str,
        elicitation_id: str,
        options: Optional[RequestOptions] = None,
    ) -> InteractionResolution:
        """Await ``POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss``."""
        return await self._run(
            self._sync.dismiss_elicitation, session_id, elicitation_id, options
        )

    @staticmethod
    async def _run(func: Callable[..., _T], *args: Any) -> _T:
        """Run a blocking client method on a worker thread."""
        import asyncio
        import functools

        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(None, functools.partial(func, *args))
