#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

"""Streaming-conformance probe for the tier-3 Python client contract.

The section 15.1 SSE stream is a long-lived connection rather than a
request/response op, so it does not fit the harness JSON-line model: a
``stream`` op would block the synchronous ``harness.Send`` while the Go
test publishes events and forces a disconnect. This probe is the
streaming counterpart of the test-helper. The Go test
(``TestPythonClientStreamingReconnect``) spawns it as a subprocess,
then concurrently publishes events to the gateway event bus and severs
the first connection.

The probe opens :meth:`lenny.Client.stream_events` against the gateway,
collects events until it has the expected count, prints one JSON line
``{"seqs": [...], "types": [...]}`` on stdout, and exits 0. Any error
is printed as ``{"error": "..."}`` and the probe exits 1.

Arguments are read from the environment the Go test sets:

* ``LENNY_GATEWAY_URL``  the in-process gateway origin
* ``LENNY_TENANT_ID``    the tenant the session was created on
* ``LENNY_SESSION_ID``   the session whose event stream to consume
* ``LENNY_EVENT_COUNT``  the number of events to collect before stopping
"""

from __future__ import annotations

import json
import os
import sys

# The probe runs from sdks/client/python so the committed ``lenny``
# package imports without an install; make that explicit for a probe
# spawned with an arbitrary working directory.
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import lenny  # noqa: E402 - path setup precedes the import


def fail(message: str) -> None:
    """Print an error line and exit non-zero."""
    sys.stdout.write(json.dumps({"error": message}) + "\n")
    sys.exit(1)


def main() -> None:
    gateway_url = os.environ.get("LENNY_GATEWAY_URL", "")
    tenant_id = os.environ.get("LENNY_TENANT_ID", "acme")
    session_id = os.environ.get("LENNY_SESSION_ID", "")
    try:
        event_count = int(os.environ.get("LENNY_EVENT_COUNT", "0"))
    except ValueError:
        event_count = 0

    if not gateway_url or not session_id or event_count <= 0:
        fail(
            "stream_probe: LENNY_GATEWAY_URL, LENNY_SESSION_ID, and a positive "
            "LENNY_EVENT_COUNT are required"
        )

    # A short reconnect backoff keeps the forced-disconnect reconnect
    # quick; the gateway holds the retained backlog so the reconnect
    # resumes from the Last-Event-ID cursor.
    client = lenny.Client(
        gateway_url,
        tenant_id=tenant_id,
        retry_policy=lenny.RetryPolicy(
            max_attempts=10, base_delay=0.005, max_delay=0.05, jitter=False
        ),
    )

    seqs: list[int] = []
    types: list[str] = []
    try:
        stream = client.stream_events(session_id)
        with stream:
            for event in stream:
                seqs.append(event.seq)
                types.append(event.type)
                if len(seqs) >= event_count:
                    # Every expected event arrived; stop the stream.
                    stream.close()
    except Exception as exc:  # noqa: BLE001 - the probe reports any failure
        fail(f"stream_probe: {exc}")

    sys.stdout.write(json.dumps({"seqs": seqs, "types": types}) + "\n")
    sys.exit(0)


if __name__ == "__main__":
    main()
