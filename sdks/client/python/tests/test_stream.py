# SPDX-License-Identifier: MIT

"""Unit tests for the section 15.1 SSE streaming surface.

The tests stand up a local :mod:`http.server` that speaks the section
15.1 SSE frame protocol and drive :meth:`lenny.Client.stream_events`
against it. They cover the SSE frame parser, in-order delivery, the
``Last-Event-ID`` reconnect with backlog dedup, a caller-supplied
resume cursor, a non-retryable status ending the stream, a retryable
status reconnecting, and :meth:`EventStream.close` stopping the stream.

The tests use only the Python standard library (:mod:`unittest`,
:mod:`http.server`), matching the SDK's standard-library-only policy.
Run them with ``python3 -m unittest discover -s tests`` from
``sdks/client/python``.
"""

from __future__ import annotations

import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Callable, Optional

import lenny


def sse_frame(seq: int, event_type: str, data: str) -> bytes:
    """Format one section 15.1 SSE frame as UTF-8 bytes."""
    return f"id: {seq}\nevent: {event_type}\ndata: {data}\n\n".encode("utf-8")


class _Server:
    """A local HTTP server driven by a per-request handler callable.

    The handler receives the :class:`BaseHTTPRequestHandler` for one GET
    request and writes the SSE response. The server runs on a loopback
    port chosen by the OS.
    """

    def __init__(self, handler: Callable[[BaseHTTPRequestHandler], None]) -> None:
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
                handler(self)

            def log_message(self, *_args: object) -> None:
                # Silence the default stderr request log.
                pass

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

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
        return f"http://{host}:{port}"


def fast_client(base_url: str) -> lenny.Client:
    """Build a client whose reconnect backoff is short for a unit test."""
    return lenny.Client(
        base_url,
        retry_policy=lenny.RetryPolicy(
            max_attempts=5, base_delay=0.001, max_delay=0.005, jitter=False
        ),
    )


def collect(
    stream: lenny.EventStream, stop_after: Optional[int] = None
) -> list[lenny.StreamEvent]:
    """Drain an event stream into a list.

    When ``stop_after`` is set, the stream is closed once that many
    events have arrived so a reconnecting stream terminates.
    """
    out: list[lenny.StreamEvent] = []
    for event in stream:
        out.append(event)
        if stop_after is not None and len(out) >= stop_after:
            stream.close()
    return out


class SSEParserTest(unittest.TestCase):
    """The SSE frame parser decodes the section 15.1 frame format."""

    def test_decodes_frames(self) -> None:
        # spec: section 15.1 SSE frame format id/event/data.
        from lenny.stream import _SSEParser

        parser = _SSEParser()
        lines = [
            "id: 1",
            "event: message_delivered",
            'data: {"content":"hello"}',
            "",
        ]
        events = [parser.push_line(line) for line in lines]
        frame = events[-1]
        assert frame is not None
        self.assertEqual(frame.seq, 1)
        self.assertEqual(frame.type, "message_delivered")
        self.assertEqual(frame.data, '{"content":"hello"}')
        self.assertEqual(frame.json(), {"content": "hello"})

    def test_joins_multiline_data(self) -> None:
        # spec: section 15.1 SSE stream; repeated data lines join with a
        # newline.
        from lenny.stream import _SSEParser

        parser = _SSEParser()
        for line in ["id: 9", "event: response", "data: line one", "data: line two"]:
            self.assertIsNone(parser.push_line(line))
        frame = parser.push_line("")
        assert frame is not None
        self.assertEqual(frame.data, "line one\nline two")

    def test_skips_comments_and_blank_padding(self) -> None:
        # spec: section 15.1 SSE stream; comment lines and blank-line
        # padding do not arm a frame.
        from lenny.stream import _SSEParser

        parser = _SSEParser()
        # A comment line followed by a blank line must not emit a frame.
        self.assertIsNone(parser.push_line(": keepalive"))
        self.assertIsNone(parser.push_line(""))
        # A real frame still parses afterward.
        for line in ["id: 3", "event: response", "data: {}"]:
            parser.push_line(line)
        frame = parser.push_line("")
        assert frame is not None
        self.assertEqual(frame.seq, 3)


class StreamDeliveryTest(unittest.TestCase):
    """End-to-end streaming against a local SSE server."""

    def test_delivers_events_in_order(self) -> None:
        # spec: section 15.1 GET /v1/sessions/{id}/events SSE stream.
        def handler(req: BaseHTTPRequestHandler) -> None:
            req.send_response(200)
            req.send_header("Content-Type", "text/event-stream")
            req.end_headers()
            for seq in range(1, 4):
                req.wfile.write(sse_frame(seq, "response", f'{{"n":{seq}}}'))

        with _Server(handler) as server:
            client = fast_client(server.url)
            got = collect(client.stream_events("sess_1"))
        self.assertEqual([e.seq for e in got], [1, 2, 3])
        self.assertEqual(got[0].type, "response")
        self.assertEqual(got[2].json(), {"n": 3})

    def test_discards_partial_trailing_frame(self) -> None:
        # spec: section 15.1 SSE stream; a frame without its terminating
        # blank line is incomplete and is not delivered.
        def handler(req: BaseHTTPRequestHandler) -> None:
            req.send_response(200)
            req.send_header("Content-Type", "text/event-stream")
            req.end_headers()
            req.wfile.write(sse_frame(1, "response", '{"a":1}'))
            # A second frame missing its terminating blank line.
            req.wfile.write(b'id: 2\nevent: response\ndata: {"b":2}\n')

        with _Server(handler) as server:
            client = fast_client(server.url)
            got = collect(client.stream_events("sess_partial"))
        self.assertEqual([e.seq for e in got], [1])

    def test_reconnects_with_last_event_id_and_deduplicates(self) -> None:
        # spec: section 15.1 streaming-reconnect-with-cursor; a reconnect
        # resumes via Last-Event-ID and replays the backlog without a
        # gap or a duplicate.
        #
        # The server holds the full event log [1..6]. The first
        # connection delivers events 1 through 3, then closes. On the
        # reconnect the client sends Last-Event-ID: 3; the server,
        # mimicking the gateway, replays event 3 again (an inclusive
        # backlog boundary the SDK must deduplicate) and delivers 4..6.
        total = 6
        state: dict[str, object] = {"conn": 0, "last_event_ids": []}
        lock = threading.Lock()

        def handler(req: BaseHTTPRequestHandler) -> None:
            with lock:
                state["conn"] = int(state["conn"]) + 1  # type: ignore[call-overload]
                conn = int(state["conn"])  # type: ignore[call-overload]
                ids = state["last_event_ids"]
                assert isinstance(ids, list)
                ids.append(req.headers.get("Last-Event-ID", ""))
            req.send_response(200)
            req.send_header("Content-Type", "text/event-stream")
            req.end_headers()
            if conn == 1:
                for seq in range(1, 4):
                    req.wfile.write(sse_frame(seq, "response", f'{{"n":{seq}}}'))
                return
            after = int(req.headers.get("Last-Event-ID", "0") or "0")
            start = after if after > 0 else 1
            for seq in range(start, total + 1):
                req.wfile.write(sse_frame(seq, "response", f'{{"n":{seq}}}'))

        with _Server(handler) as server:
            client = fast_client(server.url)
            got = collect(client.stream_events("sess_reconnect"), stop_after=total)

        # Every event arrived exactly once, in order, with no gap.
        self.assertEqual([e.seq for e in got], list(range(1, total + 1)))
        self.assertGreaterEqual(int(state["conn"]), 2, "expected a reconnect")  # type: ignore[call-overload]
        ids = state["last_event_ids"]
        assert isinstance(ids, list)
        self.assertEqual(ids[0], "", "first connection sends no Last-Event-ID")
        self.assertEqual(ids[1], "3", "reconnect carries the last delivered cursor")

    def test_resumes_from_caller_supplied_cursor(self) -> None:
        # spec: section 15.1 streaming-reconnect-with-cursor; a
        # caller-supplied last_event_id sets the initial Last-Event-ID
        # header.
        seen: dict[str, str] = {}

        def handler(req: BaseHTTPRequestHandler) -> None:
            seen.setdefault("first", req.headers.get("Last-Event-ID", ""))
            req.send_response(200)
            req.send_header("Content-Type", "text/event-stream")
            req.end_headers()
            req.wfile.write(sse_frame(11, "response", '{"n":11}'))

        with _Server(handler) as server:
            client = fast_client(server.url)
            stream = client.stream_events(
                "sess_resume", lenny.StreamOptions(last_event_id=10)
            )
            got = collect(stream, stop_after=1)
        self.assertEqual(got[0].seq, 11)
        self.assertEqual(seen["first"], "10")

    def test_non_retryable_status_raises_typed_error(self) -> None:
        # spec: section 15.1 GET /v1/sessions/{id}/events returns 404
        # RESOURCE_NOT_FOUND for an unknown session; a non-retryable
        # status ends the stream with the typed error.
        def handler(req: BaseHTTPRequestHandler) -> None:
            body = (
                b'{"error":{"code":"RESOURCE_NOT_FOUND","category":"PERMANENT",'
                b'"message":"session not found","retryable":false}}'
            )
            req.send_response(404)
            req.send_header("Content-Type", "application/json")
            req.send_header("Content-Length", str(len(body)))
            req.end_headers()
            req.wfile.write(body)

        with _Server(handler) as server:
            client = fast_client(server.url)
            with self.assertRaises(lenny.APIError) as caught:
                collect(client.stream_events("sess_missing"))
        self.assertEqual(caught.exception.code, "RESOURCE_NOT_FOUND")

    def test_retryable_status_reconnects(self) -> None:
        # spec: section 15.1 GET /v1/sessions/{id}/events; a retryable
        # status (503 EVENT_STREAM_UNAVAILABLE) is retried like a
        # transport disconnect rather than ending the stream.
        state = {"attempt": 0}
        lock = threading.Lock()

        def handler(req: BaseHTTPRequestHandler) -> None:
            with lock:
                state["attempt"] += 1
                attempt = state["attempt"]
            if attempt == 1:
                body = (
                    b'{"error":{"code":"EVENT_STREAM_UNAVAILABLE",'
                    b'"category":"TRANSIENT","message":"event bus unavailable",'
                    b'"retryable":true}}'
                )
                req.send_response(503)
                req.send_header("Content-Type", "application/json")
                req.send_header("Content-Length", str(len(body)))
                req.end_headers()
                req.wfile.write(body)
                return
            req.send_response(200)
            req.send_header("Content-Type", "text/event-stream")
            req.end_headers()
            req.wfile.write(sse_frame(1, "response", '{"n":1}'))

        with _Server(handler) as server:
            client = fast_client(server.url)
            got = collect(client.stream_events("sess_503"), stop_after=1)
        self.assertEqual([e.seq for e in got], [1])
        self.assertGreaterEqual(state["attempt"], 2, "a 503 must be retried")

    def test_close_stops_the_stream(self) -> None:
        # spec: section 15.1 SSE stream; closing the stream stops it
        # cleanly without raising.
        release = threading.Event()

        def handler(req: BaseHTTPRequestHandler) -> None:
            req.send_response(200)
            req.send_header("Content-Type", "text/event-stream")
            req.end_headers()
            req.wfile.write(sse_frame(1, "response", '{"n":1}'))
            # Hold the connection open until the test releases it so the
            # client must observe the close to stop iterating.
            release.wait(timeout=5)

        with _Server(handler) as server:
            client = fast_client(server.url)
            got: list[lenny.StreamEvent] = []
            with client.stream_events("sess_close") as stream:
                for event in stream:
                    got.append(event)
                    # Closing inside the loop ends the iteration without
                    # raising: a clean caller-requested stop.
                    stream.close()
            release.set()
        self.assertEqual([e.seq for e in got], [1])


class AsyncStreamTest(unittest.IsolatedAsyncioTestCase):
    """The async streaming surface mirrors the synchronous one."""

    async def test_async_iteration_delivers_events(self) -> None:
        # spec: section 15.1 SSE stream consumed through the async
        # AsyncClient.stream_events surface.
        def handler(req: BaseHTTPRequestHandler) -> None:
            req.send_response(200)
            req.send_header("Content-Type", "text/event-stream")
            req.end_headers()
            for seq in range(1, 4):
                req.wfile.write(sse_frame(seq, "response", f'{{"n":{seq}}}'))

        with _Server(handler) as server:
            client = lenny.AsyncClient(
                server.url,
                retry_policy=lenny.RetryPolicy(
                    max_attempts=5, base_delay=0.001, max_delay=0.005, jitter=False
                ),
            )
            stream = await client.stream_events("sess_async")
            got: list[lenny.StreamEvent] = []
            async with stream:
                async for event in stream:
                    got.append(event)
        self.assertEqual([e.seq for e in got], [1, 2, 3])


if __name__ == "__main__":
    unittest.main()
