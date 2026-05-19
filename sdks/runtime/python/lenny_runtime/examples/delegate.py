# SPDX-License-Identifier: MIT

"""Standard-level Lenny agent runtime built on the Python runtime-author
SDK (``lenny-runtime``).

It shows the §8.5 delegation flow expressed through the SDK's typed
platform MCP tool helpers.

The runtime is started with ``level="standard"``, so the SDK reads the
adapter manifest, dials the platform MCP server and every connector MCP
server with the §15.4.3 manifest-nonce handshake, and exposes the
platform tool surface through the ``tools`` argument of
:meth:`on_message`. On each inbound message the handler delegates a
sub-task, awaits the child, confirms the result via ``request_input``,
emits the child output via ``lenny/output``, and returns the child parts
as the turn response.

When no adapter manifest is present the SDK cannot reach Standard level;
``tools.platform`` is ``None`` and the handler degrades to a plain echo
so the runtime still runs in Basic-only test paths.

Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol error.
"""

from __future__ import annotations

import sys

from lenny_runtime import (
    CreateRequest,
    HandlerTools,
    Message,
    OutputPart,
    ProtocolError,
    Reply,
    ResponseError,
    TerminationReason,
    run,
    text,
)

_EXIT_OK = 0
_EXIT_RUNTIME_ERROR = 1
_EXIT_PROTOCOL_ERROR = 2


def _echo_parts(parts: list[OutputPart], seq: int) -> list[OutputPart]:
    """Prefix text parts with the per-session sequence number."""
    out: list[OutputPart] = []
    for part in parts:
        if part.type == "text" and part.inline:
            out.append(text(f"[delegate seq={seq}] {part.inline}"))
        else:
            out.append(part)
    return out


def _delegation_error(err: object) -> Reply:
    """Build a final :class:`Reply` carrying a structured error so the
    adapter records the failure without losing context (§15.4.1)."""
    return Reply(
        error=ResponseError(code="DELEGATION_FAILED", message=str(err)),
        final=True,
    )


class DelegateHandler:
    """Standard-level handler."""

    def __init__(self) -> None:
        self._seq = 0

    def on_create(self, req: CreateRequest) -> None:
        """No task-scoped setup.

        The SDK has already dialed the platform MCP server and connector
        MCP servers by the time on_create runs.
        """

    def on_message(self, msg: Message, tools: HandlerTools) -> Reply:
        """Run the §8.5 delegation flow through the SDK platform tool
        helpers. Without a platform MCP server it echoes the input."""
        self._seq += 1
        platform = tools.platform
        if platform is None:
            # Basic-level fallback: no platform MCP server in the
            # manifest.
            return Reply(
                parts=_echo_parts(msg.envelope.input, self._seq), final=True
            )

        try:
            # 1. lenny/delegate_task — spawn a child whose input is this
            #    message's input parts.
            handle = platform.delegate_task(
                "delegate-child", _echo_parts(msg.envelope.input, self._seq)
            )

            # 2. lenny/await_children — wait for the child to settle.
            results = platform.await_children([handle.task_id], "all")
            if not results or results[0].state != "completed":
                return _delegation_error("child did not complete")
            child_parts = results[0].output.parts

            # 3. lenny/request_input — confirm the echoed result.
            platform.request_input(
                [text(f"confirm echo of child {handle.task_id}?")]
            )

            # 4. lenny/output — emit the child output to the parent or
            #    client. The response below still signals turn
            #    completion (§15.4.1).
            platform.output(child_parts)
            return Reply(parts=child_parts, final=True)
        except Exception as err:  # noqa: BLE001 — surfaced as a Reply error
            return _delegation_error(err)

    def on_terminate(self, reason: TerminationReason) -> None:
        """No teardown."""


def main() -> None:
    """Entry point: run the delegate handler at the Standard level."""
    from lenny_runtime import RunOptions

    try:
        run(DelegateHandler(), RunOptions(level="standard"))
    except ProtocolError as err:
        sys.stderr.write(f"{err}\n")
        sys.exit(_EXIT_PROTOCOL_ERROR)
    except Exception as err:  # noqa: BLE001 — top-level exit boundary
        sys.stderr.write(f"{err}\n")
        sys.exit(_EXIT_RUNTIME_ERROR)
    sys.exit(_EXIT_OK)


if __name__ == "__main__":
    main()
