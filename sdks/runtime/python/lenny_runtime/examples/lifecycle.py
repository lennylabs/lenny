# SPDX-License-Identifier: MIT

"""Full-level Lenny agent runtime built on the Python runtime-author SDK
(``lenny-runtime``).

The runtime is started with ``level="full"``, so the SDK opens the
§15.4.3 CH-RUNTIMEOPS and completes the ``lifecycle_capabilities`` /
``lifecycle_support`` handshake in addition to the Standard-level MCP
setup. The SDK answers the checkpoint, interrupt, credential-rotation,
and deadline events automatically; the lifecycle callbacks below show
where a real Full-level runtime quiesces output, reaches a safe
interrupt point, and rebinds rotated credentials.

The message handler is an echo. The Full-level behavior lives in the
CH-RUNTIMEOPS hooks rather than the per-turn path.

Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol error.
"""

from __future__ import annotations

import sys
import threading

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


# _NO_VARIANT is the variant the reply names for a session that is not
# enrolled in an experiment.
_NO_VARIANT = "none"


class LifecycleHandler:
    """Full-level handler.

    The message path is an echo naming the session and its experiment
    variant; the Full-level behavior lives in the lifecycle hooks passed
    to :func:`run`, each of which names the session its event concerns.
    Handler calls for different sessions run on different threads, so the
    variant map is lock-guarded.
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
        """Echo the inbound parts."""
        with self._lock:
            variant = self._variants.get(msg.session_id, _NO_VARIANT)
        out = []
        for part in msg.envelope.input:
            if part.type == "text" and part.inline:
                out.append(
                    text(
                        f"[lifecycle seq={msg.sequence} session={msg.session_id} "
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


def _on_checkpoint(session_id: str, checkpoint_id: str) -> None:
    """Quiesce the session's output before the SDK replies
    checkpoint_ready.

    This echo runtime holds no streaming state, so it is quiescent
    immediately.
    """


def _on_interrupt(session_id: str, interrupt_id: str) -> None:
    """Bring the session's work to a safe stop point before the SDK
    replies interrupt_acknowledged."""


def _on_credentials_rotated(session_id: str, creds: CredentialBundle | None) -> None:
    """Rebind the session's upstream client to its refreshed bundle.

    The SDK has already re-read the credential file the event named by
    the time this callback runs.
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
