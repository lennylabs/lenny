# SPDX-License-Identifier: MIT

"""Basic-level Lenny agent runtime built on the Python runtime-author
SDK (``lenny-runtime``).

It echoes every inbound message back, prefixing text parts with the
session's message sequence number, the session's identifier, and the
session's experiment variant.

The handler implements the three §15.7 :class:`Handler` methods. The SDK
drives the §28.5.3 stdin/stdout protocol around it: it opens a session on
each ``session_start``, invokes :meth:`on_message` for the session's
messages, serializes the returned :class:`Reply` into a response frame,
answers heartbeats, and ends the session on ``session_end``. One process
serves every session the pod holds, so the handler keeps the context each
session's :meth:`on_create` delivered keyed by session. A runtime author
writing a real agent replaces the body of :meth:`on_message` with a model
call and keeps the rest unchanged.

Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol error.
"""

from __future__ import annotations

import sys
import threading

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


# _NO_VARIANT is the variant the reply names for a session that is not
# enrolled in an experiment.
_NO_VARIANT = "none"


class EchoHandler:
    """Basic-level handler.

    It records each live session's experiment variant, from that
    session's :meth:`on_create`, so a reply names the context of the
    session it answers. Handler calls for different sessions run on
    different threads, so the map is lock-guarded.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._variants: dict[str, str] = {}

    def on_create(self, req: CreateRequest) -> None:
        """Record the session's experiment variant."""
        variant = _NO_VARIANT
        if req.experiment_context is not None and req.experiment_context.variant_id:
            variant = req.experiment_context.variant_id
        with self._lock:
            self._variants[req.session_id] = variant

    def on_message(self, msg: Message, tools: HandlerTools) -> Reply:
        """Echo the inbound parts.

        Text parts are prefixed with the session's message sequence
        number, identifier, and experiment variant; non-text parts are
        returned unchanged.
        """
        with self._lock:
            variant = self._variants.get(msg.session_id, _NO_VARIANT)
        out = []
        for part in msg.envelope.input:
            if part.type == "text" and part.inline:
                out.append(
                    text(
                        f"[echo seq={msg.sequence} session={msg.session_id} "
                        f"variant={variant}] {part.inline}"
                    )
                )
            else:
                out.append(part)
        return Reply(parts=out, final=True)

    def on_terminate(self, session_id: str, reason: TerminationReason) -> None:
        """Forget the ended session's variant."""
        with self._lock:
            self._variants.pop(session_id, None)


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
