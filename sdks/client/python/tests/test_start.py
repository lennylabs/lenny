# SPDX-License-Identifier: MIT

"""Unit tests for the single-attempt ``Client.start``.

A ``/start`` that fails after the gateway wrote ``starting`` leaves the
session failed, so ``start`` sends its request once and retries neither an
error response nor a transport failure (section 15.6, section 15.1). The
request count each stub records is the discriminating assertion. Run with
``python3 -m unittest discover -s tests`` from ``sdks/client/python``.
"""

from __future__ import annotations

import http.server
import json
import socket
import threading
import unittest

import lenny


class _StubHandler(http.server.BaseHTTPRequestHandler):
    """Counts POST requests per path and answers per the server's mode."""

    def do_POST(self) -> None:  # noqa: N802 - stdlib handler name
        server = self.server
        with server.lock:  # type: ignore[attr-defined]
            counts = server.counts  # type: ignore[attr-defined]
            counts[self.path] = counts.get(self.path, 0) + 1
            server.tokens[self.path] = self.headers.get(  # type: ignore[attr-defined]
                "X-Lenny-Upload-Token", ""
            )
        mode = server.mode  # type: ignore[attr-defined]
        if mode == "drop":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        body = json.dumps(
            {
                "error": {
                    "code": server.code,  # type: ignore[attr-defined]
                    "category": "TRANSIENT",
                    "message": "stub failure",
                    "retryable": True,
                }
            }
        ).encode("utf-8")
        self.send_response(503)
        self.send_header("Content-Type", "application/json")
        self.send_header("Retry-After", "1")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args: object) -> None:
        return


def _serve(mode: str, code: str = "STARTING_FAILED") -> http.server.HTTPServer:
    server = http.server.HTTPServer(("127.0.0.1", 0), _StubHandler)
    server.mode = mode  # type: ignore[attr-defined]
    server.code = code  # type: ignore[attr-defined]
    server.counts = {}  # type: ignore[attr-defined]
    server.tokens = {}  # type: ignore[attr-defined]
    server.lock = threading.Lock()  # type: ignore[attr-defined]
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def _client(server: http.server.HTTPServer, attempts: int) -> lenny.Client:
    host, port = server.server_address[:2]
    return lenny.Client(
        f"http://{host}:{port}",
        retry_policy=lenny.RetryPolicy(
            max_attempts=attempts, base_delay=0.001, jitter=False
        ),
        timeout=5.0,
    )


class StartSingleAttemptTest(unittest.TestCase):
    """spec: section 15.6 (Client SDKs), section 15.1 (/start)."""

    def test_start_sends_one_request_on_starting_failed(self) -> None:
        server = _serve("error")
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with self.assertRaises(lenny.APIError) as caught:
            _client(server, 5).start("sess_1")
        self.assertEqual(caught.exception.code, "STARTING_FAILED")
        self.assertEqual(server.counts.get("/v1/sessions/sess_1/start"), 1)

    def test_start_sends_one_request_on_dropped_connection(self) -> None:
        server = _serve("drop")
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with self.assertRaises(lenny.LennyError):
            _client(server, 5).start("sess_1")
        self.assertEqual(server.counts.get("/v1/sessions/sess_1/start"), 1)

    def test_finalize_keeps_the_retry_policy(self) -> None:
        server = _serve("error", code="SESSION_CREATION_FAILED")
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with self.assertRaises(lenny.APIError):
            _client(server, 3).finalize("sess_1", "tok-1")
        self.assertEqual(server.counts.get("/v1/sessions/sess_1/finalize"), 3)


class FinalizeUploadTokenTest(unittest.TestCase):
    """spec: section 7.4 (Upload Safety, Upload authorization), section 15.6.

    Every finalize request carries the section 7.1 uploadToken in the
    X-Lenny-Upload-Token header, and an empty token is refused before any
    request is sent.
    """

    def test_finalize_sends_upload_token(self) -> None:
        server = _serve("error", code="SESSION_CREATION_FAILED")
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with self.assertRaises(lenny.APIError):
            _client(server, 1).finalize("sess_1", "tok-7")
        self.assertEqual(
            server.tokens.get("/v1/sessions/sess_1/finalize"), "tok-7"
        )

    def test_finalize_keeps_idempotency_key_with_token(self) -> None:
        server = _serve("error", code="SESSION_CREATION_FAILED")
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with self.assertRaises(lenny.APIError):
            _client(server, 1).finalize(
                "sess_1", "tok-8", lenny.RequestOptions(idempotency_key="k1")
            )
        self.assertEqual(
            server.tokens.get("/v1/sessions/sess_1/finalize"), "tok-8"
        )

    def test_finalize_refuses_an_empty_token(self) -> None:
        server = _serve("error")
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with self.assertRaises(ValueError):
            _client(server, 1).finalize("sess_1", "")
        self.assertIsNone(server.counts.get("/v1/sessions/sess_1/finalize"))


if __name__ == "__main__":
    unittest.main()
