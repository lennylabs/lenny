# SPDX-License-Identifier: MIT

"""Basic-level Lenny agent runtime built on the Python runtime-author
SDK (``lenny-runtime``).

It echoes every inbound message back, prefixing text parts with a
per-session sequence number.

The handler implements the three §15.7 :class:`Handler` methods. The SDK
drives the §15.4.1 stdin/stdout protocol around it: it reads the inbound
message frames, invokes :meth:`on_message`, serializes the returned
:class:`Reply` into a response frame, answers heartbeats, and exits on
shutdown. A runtime author writing a real agent replaces the body of
:meth:`on_message` with a model call and keeps the rest unchanged.

Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol error.
"""

from __future__ import annotations

import sys

from lenny_runtime import (
    CreateRequest,
    HandlerTools,
    Message,
    ProtocolError,
    Reply,
    TerminationReason,
    run,
    text,
)

_EXIT_OK = 0
_EXIT_RUNTIME_ERROR = 1
_EXIT_PROTOCOL_ERROR = 2


class EchoHandler:
    """Basic-level handler.

    It holds a per-process sequence counter so multi-message sessions
    can be correlated.
    """

    def __init__(self) -> None:
        self._seq = 0

    def on_create(self, req: CreateRequest) -> None:
        """No task-scoped setup for an echo runtime."""

    def on_message(self, msg: Message, tools: HandlerTools) -> Reply:
        """Echo the inbound parts.

        Text parts are prefixed with the per-session sequence number;
        non-text parts are returned unchanged.
        """
        self._seq += 1
        out = []
        for part in msg.envelope.input:
            if part.type == "text" and part.inline:
                out.append(text(f"[echo seq={self._seq}] {part.inline}"))
            else:
                out.append(part)
        return Reply(parts=out, final=True)

    def on_terminate(self, reason: TerminationReason) -> None:
        """No teardown for an echo runtime."""


def main() -> None:
    """Entry point: run the echo handler at the Basic level."""
    try:
        run(EchoHandler())
    except ProtocolError as err:
        sys.stderr.write(f"{err}\n")
        sys.exit(_EXIT_PROTOCOL_ERROR)
    except Exception as err:  # noqa: BLE001 — top-level exit boundary
        sys.stderr.write(f"{err}\n")
        sys.exit(_EXIT_RUNTIME_ERROR)
    sys.exit(_EXIT_OK)


if __name__ == "__main__":
    main()
