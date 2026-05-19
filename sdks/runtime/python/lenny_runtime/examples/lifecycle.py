# SPDX-License-Identifier: MIT

"""Full-level Lenny agent runtime built on the Python runtime-author SDK
(``lenny-runtime``).

The runtime is started with ``level="full"``, so the SDK opens the
§15.4.3 lifecycle channel and completes the ``lifecycle_capabilities`` /
``lifecycle_support`` handshake in addition to the Standard-level MCP
setup. The SDK answers the checkpoint, interrupt, credential-rotation,
and deadline events automatically; the lifecycle callbacks below show
where a real Full-level runtime quiesces output, reaches a safe
interrupt point, and rebinds rotated credentials.

The message handler is a plain echo. The Full-level behavior lives in
the lifecycle channel rather than the per-turn path.

Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol error.
"""

from __future__ import annotations

import sys

from lenny_runtime import (
    CreateRequest,
    CredentialBundle,
    HandlerTools,
    LifecycleHooks,
    Message,
    ProtocolError,
    Reply,
    RunOptions,
    TerminationReason,
    run,
    text,
)

_EXIT_OK = 0
_EXIT_RUNTIME_ERROR = 1
_EXIT_PROTOCOL_ERROR = 2


class LifecycleHandler:
    """Full-level handler.

    The message path is a plain echo; the Full-level behavior lives in
    the lifecycle hooks passed to :func:`run`.
    """

    def __init__(self) -> None:
        self._seq = 0

    def on_create(self, req: CreateRequest) -> None:
        """No task-scoped setup."""

    def on_message(self, msg: Message, tools: HandlerTools) -> Reply:
        """Echo the inbound parts."""
        self._seq += 1
        out = []
        for part in msg.envelope.input:
            if part.type == "text" and part.inline:
                out.append(text(f"[lifecycle seq={self._seq}] {part.inline}"))
            else:
                out.append(part)
        return Reply(parts=out, final=True)

    def on_terminate(self, reason: TerminationReason) -> None:
        """No teardown."""


def _on_checkpoint(checkpoint_id: str) -> None:
    """Quiesce output before the SDK replies checkpoint_ready.

    This echo runtime holds no streaming state, so it is quiescent
    immediately.
    """


def _on_interrupt(interrupt_id: str) -> None:
    """Bring the runtime to a safe stop point before the SDK replies
    interrupt_acknowledged."""


def _on_credentials_rotated(creds: CredentialBundle | None) -> None:
    """Rebind the upstream client to the refreshed bundle.

    The SDK has already re-read the credential file by the time this
    callback runs.
    """


def main() -> None:
    """Entry point: run the lifecycle handler at the Full level."""
    options = RunOptions(
        level="full",
        lifecycle=LifecycleHooks(
            on_checkpoint=_on_checkpoint,
            on_interrupt=_on_interrupt,
            on_credentials_rotated=_on_credentials_rotated,
        ),
    )
    try:
        run(LifecycleHandler(), options)
    except ProtocolError as err:
        sys.stderr.write(f"{err}\n")
        sys.exit(_EXIT_PROTOCOL_ERROR)
    except Exception as err:  # noqa: BLE001 — top-level exit boundary
        sys.stderr.write(f"{err}\n")
        sys.exit(_EXIT_RUNTIME_ERROR)
    sys.exit(_EXIT_OK)


if __name__ == "__main__":
    main()
