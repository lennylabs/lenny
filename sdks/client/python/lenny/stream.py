# SPDX-License-Identifier: MIT

"""The section 15.1 Server-Sent Events session-event stream.

:class:`EventStream` consumes ``GET /v1/sessions/{id}/events``, the
section 15.1 SSE stream of session activity. It decodes each SSE frame
into a :class:`StreamEvent`, reconnects on a transport disconnect, and
resumes from the last delivered sequence with the ``Last-Event-ID``
header so the section 15.1 streaming-reconnect contract holds: the
backlog is replayed, no event is skipped, and no event is delivered
twice.

The transport uses only the Python standard library
(:mod:`urllib.request`). :class:`EventStream` is the synchronous
iterator; :class:`AsyncEventStream` is the ``async``/``await`` form,
which runs each blocking read on a worker thread so an ``async for``
caller does not block the event loop.
"""

from __future__ import annotations

import http.client
import json
import random
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Iterator, Optional

from .auth import Authenticator
from .errors import APIError
from .retry import DEFAULT_RETRY_POLICY, RetryPolicy

#: Identifies the SDK in the ``User-Agent`` request header.
_STREAM_USER_AGENT = "lenny-client-sdk-python"


@dataclass
class StreamEvent:
    """One decoded frame from the section 15.1 SSE event stream."""

    #: The monotonic per-session sequence the gateway assigns. It is the
    #: cursor a reconnect resumes from: the SDK sends the last delivered
    #: ``seq`` as the ``Last-Event-ID`` header. A frame with no ``id:``
    #: field has ``seq`` 0.
    seq: int = 0

    #: The SSE event type (the ``event:`` field), for example
    #: ``message_delivered``, ``response``, or ``state_changed``. Empty
    #: when the frame carried no ``event:`` field.
    type: str = ""

    #: The event payload, the JSON text carried in the ``data:`` field.
    data: str = ""

    def json(self) -> Any:
        """Decode :attr:`data` as JSON and return the parsed value."""
        return json.loads(self.data) if self.data else None


@dataclass
class StreamOptions:
    """Settings for an :meth:`~lenny.client.Client.stream_events` call.

    Attributes:
        last_event_id: The sequence the stream resumes after. Zero (the
            default) starts from the beginning of the retained backlog.
            A non-zero value sends the section 15.1 ``Last-Event-ID``
            header on the initial request so the gateway replays only
            events after that cursor.
    """

    last_event_id: int = 0


@dataclass
class _StreamConfig:
    """The transport surface the stream loop reads.

    It is the subset of the :class:`~lenny.client.Client` internals the
    stream needs, passed in so the stream module does not reach into the
    client's private state.
    """

    base_url: str
    timeout: float
    retry: RetryPolicy
    auth: Optional[Authenticator] = None
    tenant_id: str = ""
    rng: random.Random = field(default_factory=random.Random)


class _SSEParser:
    """Accumulates the lines of an SSE byte stream into frames.

    The parser holds the ``id``, ``event``, and ``data`` fields of one
    frame and emits a :class:`StreamEvent` on the blank line that
    terminates the frame.
    """

    def __init__(self) -> None:
        self._seq = 0
        self._type = ""
        self._data: list[str] = []
        self._started = False

    def push_line(self, line: str) -> Optional[StreamEvent]:
        """Feed one SSE line.

        Return the completed :class:`StreamEvent` when ``line`` is the
        blank line that terminates a frame, and ``None`` otherwise.
        """
        if line == "":
            # A blank line terminates the frame. Dispatch it only when a
            # recognized field was seen, so a blank line that follows
            # only comment lines or blank padding is skipped rather than
            # dispatched as an empty frame.
            if not self._started:
                return None
            event = StreamEvent(
                seq=self._seq,
                type=self._type,
                data="\n".join(self._data),
            )
            self._reset()
            return event

        field_name, value = _split_sse_line(line)
        if field_name == "id":
            self._started = True
            if value.isdigit():
                self._seq = int(value)
        elif field_name == "event":
            self._started = True
            self._type = value
        elif field_name == "data":
            self._started = True
            # The SSE format joins multiple data lines with a newline.
            self._data.append(value)
        # A comment line (an empty field name, the line started with a
        # colon) or an unknown field name contributes nothing.
        return None

    def _reset(self) -> None:
        self._seq = 0
        self._type = ""
        self._data = []
        self._started = False


def _split_sse_line(line: str) -> tuple[str, str]:
    """Split one SSE line into its field name and value.

    The SSE format separates them with the first colon and strips a
    single leading space from the value. A line with no colon is a field
    name with an empty value.
    """
    idx = line.find(":")
    if idx < 0:
        return line, ""
    value = line[idx + 1 :]
    if value.startswith(" "):
        value = value[1:]
    return line[:idx], value


class EventStream:
    """Synchronous iterator over the section 15.1 SSE event stream.

    Iterate it with a ``for`` loop. The iterator reconnects on a
    transport disconnect, resuming from the ``Last-Event-ID`` cursor of
    the last delivered event, and drops any replayed event at or below
    that cursor so no event is skipped or delivered twice.

    The gateway holds the SSE connection open until the client
    disconnects; it does not close a live stream on its own. The SDK
    therefore treats any connection close, including a clean end of
    body, as an unexpected disconnect and reconnects, the same reconnect
    behavior the WHATWG EventSource model specifies. A reconnect that
    delivers an event resets the attempt counter, so a long-lived stream
    reconnects indefinitely; consecutive reconnects that deliver no
    event are bounded by the retry policy's ``max_attempts``.

    Iteration ends when :meth:`close` is called (a clean
    caller-requested stop), when the gateway returns a non-retryable
    HTTP status (the iterator raises the typed
    :class:`~lenny.errors.APIError`), or when consecutive zero-progress
    reconnects exhaust the retry budget. A retryable HTTP status (429,
    5xx) is retried like a transport disconnect.

    The stream is a context manager: a ``with`` block closes it on exit.
    """

    def __init__(
        self,
        config: _StreamConfig,
        session_id: str,
        options: Optional[StreamOptions] = None,
    ) -> None:
        self._config = config
        self._path = "/v1/sessions/" + urllib.parse.quote(session_id, safe="") + "/events"
        opt = options or StreamOptions()
        self._last_seq = opt.last_event_id if opt.last_event_id > 0 else 0
        self._closed = False
        self._response: Optional[http.client.HTTPResponse] = None

    def __iter__(self) -> Iterator[StreamEvent]:
        return self._run()

    def __enter__(self) -> "EventStream":
        return self

    def __exit__(self, *_exc: Any) -> None:
        self.close()

    def close(self) -> None:
        """Stop the stream.

        It marks the stream closed and tears down any open connection.
        An iterator blocked on a read returns once the underlying socket
        is closed. :meth:`close` is idempotent.
        """
        self._closed = True
        self._close_response()

    def _close_response(self) -> None:
        response = self._response
        self._response = None
        if response is not None:
            try:
                response.close()
            except OSError:
                # The connection is already torn down; nothing to free.
                pass

    def _run(self) -> Iterator[StreamEvent]:
        policy = self._config.retry
        # failures counts consecutive reconnects that delivered no
        # event. It bounds reconnection against a permanently broken
        # stream and spaces attempts with the retry policy's backoff. A
        # connection that delivers an event resets it to zero.
        failures = 0
        while not self._closed:
            if failures > 0:
                delay = policy.delay_for_attempt(failures, self._config.rng)
                if _sleep_or_closed(delay, self._is_closed):
                    return

            try:
                response = self._open()
            except APIError as exc:
                # A non-retryable HTTP status ends the stream with the
                # typed error; a retryable status reconnects.
                if not exc.retryable:
                    raise
                failures += 1
                if failures >= policy.max_attempts:
                    raise
                continue
            except OSError:
                # A transport-level failure before any response
                # (connection refused, DNS, a reset). Reconnect.
                failures += 1
                if failures >= policy.max_attempts:
                    raise
                continue

            progressed = False
            read_error: Optional[BaseException] = None
            try:
                for event in self._read_frames(response):
                    # Drop a replayed event at or below the last
                    # delivered sequence so a reconnect does not surface
                    # a duplicate.
                    if event.seq != 0 and event.seq <= self._last_seq:
                        continue
                    yield event
                    if event.seq > self._last_seq:
                        self._last_seq = event.seq
                        progressed = True
            except OSError as exc:
                # A read error mid-stream. Reconnect from the last
                # delivered cursor.
                read_error = exc
            finally:
                self._close_response()

            if self._closed:
                return
            # A transport disconnect, a clean end of body, or a read
            # error. Reconnect. A connection that delivered an event
            # restarts the attempt counter; otherwise the counter
            # advances toward the max_attempts ceiling.
            if progressed:
                failures = 0
                continue
            failures += 1
            if failures >= policy.max_attempts:
                # The stream made no progress across the full retry
                # budget. Surface the last read failure, or stop
                # cleanly when the connection kept ending at a clean
                # end of body.
                if read_error is not None:
                    raise read_error
                return

    def _is_closed(self) -> bool:
        return self._closed

    def _open(self) -> http.client.HTTPResponse:
        """Open one SSE connection.

        Return the streaming response on a 2xx. A non-2xx status raises
        the typed :class:`~lenny.errors.APIError`; a transport failure
        raises an :class:`OSError`.
        """
        headers: dict[str, str] = {
            "Accept": "text/event-stream",
            "User-Agent": _STREAM_USER_AGENT,
        }
        if self._last_seq > 0:
            # The section 15.1 reconnect cursor. The gateway replays the
            # retained backlog with ``seq`` greater than this value
            # before live delivery resumes.
            headers["Last-Event-ID"] = str(self._last_seq)
        if self._config.tenant_id:
            headers["X-Lenny-Tenant-ID"] = self._config.tenant_id
        if self._config.auth is not None:
            self._config.auth.apply(headers)

        request = urllib.request.Request(
            self._config.base_url + self._path,
            headers=headers,
            method="GET",
        )
        try:
            # urlopen is typed to return Any; for an HTTP(S) URL the
            # concrete return is an http.client.HTTPResponse, whose
            # incremental readline the SSE parser drives.
            opened = urllib.request.urlopen(request, timeout=self._config.timeout)
            response: http.client.HTTPResponse = opened
        except urllib.error.HTTPError as exc:
            # A non-2xx response is a complete round trip; decode the
            # section 15.1 envelope and surface the typed error.
            body = exc.read()
            exc.close()
            raise APIError.from_response(exc.code, body) from None
        except urllib.error.URLError as exc:
            raise OSError(f"lenny: stream connection failed: {exc.reason}") from exc

        self._response = response
        return response

    def _read_frames(
        self, response: http.client.HTTPResponse
    ) -> Iterator[StreamEvent]:
        """Decode the SSE byte stream of one connection into events.

        The generator returns when the body ends. A frame buffered
        without its terminating blank line is discarded.
        """
        parser = _SSEParser()
        while True:
            if self._closed:
                return
            raw = response.readline()
            if raw == b"":
                # The body ended. A clean stream ends on a frame
                # boundary; a partial frame in the parser is discarded.
                return
            # SSE lines are LF-terminated; tolerate a CRLF stream by
            # trimming a trailing CR.
            line = raw.rstrip(b"\n").rstrip(b"\r").decode("utf-8", "replace")
            event = parser.push_line(line)
            if event is not None:
                yield event


class AsyncEventStream:
    """Asynchronous iterator over the section 15.1 SSE event stream.

    It is the ``async``/``await`` counterpart of :class:`EventStream`.
    Each blocking connection and frame read runs on a worker thread, so
    an ``async for`` caller does not block the event loop. The reconnect
    and dedup behavior is identical to :class:`EventStream`.
    """

    def __init__(
        self,
        config: _StreamConfig,
        session_id: str,
        options: Optional[StreamOptions] = None,
    ) -> None:
        self._stream = EventStream(config, session_id, options)

    def __aiter__(self) -> AsyncIterator[StreamEvent]:
        return self._run()

    async def __aenter__(self) -> "AsyncEventStream":
        return self

    async def __aexit__(self, *_exc: Any) -> None:
        await self.aclose()

    async def aclose(self) -> None:
        """Stop the stream.

        It closes the underlying synchronous stream so a worker-thread
        read returns. :meth:`aclose` is idempotent.
        """
        self._stream.close()

    async def _run(self) -> AsyncIterator[StreamEvent]:
        import asyncio

        loop = asyncio.get_running_loop()
        # The synchronous iterator is advanced one event at a time on a
        # worker thread. _NO_MORE is the sentinel the worker returns
        # when the synchronous iterator is exhausted.
        iterator = iter(self._stream)

        def next_event() -> Any:
            try:
                return next(iterator)
            except StopIteration:
                return _NO_MORE

        try:
            while True:
                event = await loop.run_in_executor(None, next_event)
                if event is _NO_MORE:
                    return
                yield event
        finally:
            self._stream.close()


#: Sentinel marking the synchronous iterator as exhausted, returned by
#: the worker-thread step in :class:`AsyncEventStream`.
_NO_MORE = object()


def _sleep_or_closed(delay: float, is_closed: Any) -> bool:
    """Wait ``delay`` seconds, or return early when the stream closes.

    Return ``True`` when the stream was closed during the wait so the
    caller stops, and ``False`` when the full delay elapsed.
    """
    import time

    if delay <= 0:
        return bool(is_closed())
    deadline = time.monotonic() + delay
    while time.monotonic() < deadline:
        if is_closed():
            return True
        time.sleep(min(0.02, deadline - time.monotonic()))
    return bool(is_closed())
