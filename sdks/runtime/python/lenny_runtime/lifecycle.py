# SPDX-License-Identifier: MIT

"""§15.4.3 Full-level CH-RUNTIMEOPS.

The SDK answers the protocol-level handshake and the checkpoint,
interrupt, credential-rotation, and deadline events automatically; a
runtime that needs to react registers callbacks through
:class:`LifecycleHooks`.
"""

from __future__ import annotations

import json
import threading
from dataclasses import dataclass, field
from typing import Any, Callable

from .transport import FrameWriter, LineReader, SocketStream, dial_unix_socket
from .types import AdapterManifest, CredentialBundle

# LIFECYCLE_CAPABILITIES is the §15.4.3 / §15.4.6 set of Full-level
# lifecycle events the SDK handles on the runtime's behalf. It is the
# payload of the lifecycle_support handshake reply.
LIFECYCLE_CAPABILITIES = [
    "checkpoint",
    "interrupt",
    "credential_rotation",
    "deadline_signal",
]


# SESSION_SCOPED_EVENTS are the adapter-to-runtime CH-RUNTIMEOPS frames
# that name a session. The SDK hands each to the session it names and
# drops, without a reply, one naming a session the runtime does not hold
# or whose context it failed to create. The adapter writes them only after
# it reads the session's session_started, so the SDK keeps no queue of
# early events.
#
# spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
SESSION_SCOPED_EVENTS = frozenset(
    {
        "checkpoint_request",
        "checkpoint_complete",
        "interrupt_request",
        "credentials_rotated",
        "deadline_approaching",
        "deadline_signal",
        "files_updated",
    }
)


@dataclass
class LifecycleEvent:
    """Decoded CH-RUNTIMEOPS frame handed to a runtime callback.

    ``session_id`` names the session a session-scoped event concerns.
    ``raw`` carries the full frame for fields the typed callbacks do not
    cover.
    """

    type: str
    raw: dict[str, Any]
    session_id: str = ""


@dataclass
class LifecycleHooks:
    """Optional runtime callbacks for lifecycle events.

    Each callback receives the session the event names. An unset hook
    means the SDK answers with the default behavior. Callbacks run on the
    SDK's CH-RUNTIMEOPS thread, which is not the thread a session's
    handler calls run on.
    """

    # on_checkpoint runs on a §15.4.3 checkpoint_request before the SDK
    # replies checkpoint_ready, with the session id and checkpoint id.
    # The callback quiesces the named session's output.
    on_checkpoint: Callable[[str, str], None] | None = None
    # on_interrupt runs on a §15.4.3 interrupt_request before the SDK
    # replies interrupt_acknowledged, with the session id and interrupt
    # id. The callback brings the named session's work to a safe stop
    # point.
    on_interrupt: Callable[[str, str], None] | None = None
    # on_credentials_rotated runs after the SDK re-reads the credential
    # file a credentials_rotated event names for the named session, with
    # the session id and the session's refreshed bundle.
    on_credentials_rotated: (
        Callable[[str, CredentialBundle | None], None] | None
    ) = None
    # on_deadline runs on a §15.4.3 deadline_approaching or
    # deadline_signal event for a session the runtime holds.
    on_deadline: Callable[[LifecycleEvent], None] | None = None


@dataclass
class LifecycleHost:
    """Subset of the SDK process the CH-RUNTIMEOPS reaches.

    ``held_session`` returns the live session a session-scoped event
    names, or None when the runtime does not hold it or failed to create
    its context. ``reload_credentials`` re-reads the file an event names
    into that session's bundle and returns the bundle the session holds
    afterwards. ``end_process`` handles the terminate event: it records
    the termination reason every live session's on_terminate receives
    and stops the frame loop.
    """

    write_stdout_frame: Callable[[dict[str, Any]], None]
    held_session: Callable[[str], Any]
    reload_credentials: Callable[[Any, str], CredentialBundle | None]
    end_process: Callable[[str, int], None]
    log: Callable[[str], None] = field(default=lambda _msg: None)


class Lifecycle:
    """§15.4.3 Full-level CH-RUNTIMEOPS surface.

    The channel is constructed only when the runtime runs at Full level
    and the manifest advertised a lifecycle socket.
    """

    def __init__(
        self,
        stream: SocketStream,
        hooks: LifecycleHooks,
        host: LifecycleHost,
    ) -> None:
        self._stream = stream
        self._writer = FrameWriter(stream)
        self._reader = LineReader(stream)
        self._hooks = hooks
        self._host = host
        self._closed = False
        self._thread: threading.Thread | None = None

    @classmethod
    def dial(
        cls,
        manifest: AdapterManifest,
        timeout_s: float,
        hooks: LifecycleHooks,
        host: LifecycleHost,
    ) -> Lifecycle:
        """Open the §15.4.3 CH-RUNTIMEOPS.

        It dials the manifest-advertised socket, completes the
        ``lifecycle_capabilities`` / ``lifecycle_support`` handshake,
        and starts the event loop on a daemon thread.
        """
        if manifest.lifecycle_channel is None:
            raise RuntimeError(
                "adapter manifest has no CH-RUNTIMEOPS socket"
            )
        stream = dial_unix_socket(manifest.lifecycle_channel.socket, timeout_s)
        lc = cls(stream, hooks, host)

        # §15.4.3 handshake: the adapter sends lifecycle_capabilities;
        # the runtime replies with lifecycle_support naming the events
        # it implements. Anything else on the first frame is a
        # handshake failure.
        first = lc._reader.next()
        if first is None:
            stream.close()
            raise RuntimeError(
                "lifecycle handshake: connection closed before frame"
            )
        try:
            caps = json.loads(first)
        except json.JSONDecodeError as err:
            stream.close()
            raise RuntimeError(
                f"lifecycle handshake: frame not JSON: {err}"
            ) from err
        if caps.get("type") != "lifecycle_capabilities":
            stream.close()
            raise RuntimeError(
                "lifecycle handshake: expected lifecycle_capabilities, "
                f"got {first}"
            )
        lc._writer.write(
            {
                "type": "lifecycle_support",
                "capabilities": LIFECYCLE_CAPABILITIES,
            }
        )

        lc._thread = threading.Thread(target=lc._loop, daemon=True)
        lc._thread.start()
        return lc

    def _loop(self) -> None:
        """Process inbound CH-RUNTIMEOPS frames until the connection
        closes or the adapter sends ``terminate``."""
        while True:
            try:
                line = self._reader.next()
            except (OSError, ValueError) as err:
                if not self._closed:
                    self._host.log(f"lifecycle read error: {err}")
                return
            if line is None:
                return
            try:
                frame = json.loads(line)
            except json.JSONDecodeError as err:
                self._host.log(f"malformed lifecycle frame: {err}")
                continue
            kind = frame.get("type", "") if isinstance(frame, dict) else ""
            if kind == "terminate":
                self._handle_terminate(frame)
                return
            if kind not in SESSION_SCOPED_EVENTS:
                self._host.log(f"ignoring unknown lifecycle event {kind!r}")
                continue
            self._route(kind, frame)

    def _route(self, kind: str, frame: dict[str, Any]) -> None:
        """Hand a session-scoped event to the session it names, or drop
        it without a reply when the runtime does not hold that session.

        spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
        """
        session_id = str(frame.get("sessionId", ""))
        session = self._host.held_session(session_id)
        if session is None:
            self._host.log(
                f"dropping lifecycle event {kind!r} for session "
                f"{session_id!r}, which this runtime does not hold"
            )
            return
        if kind == "checkpoint_request":
            self._handle_checkpoint(session_id, frame)
        elif kind == "interrupt_request":
            self._handle_interrupt(session_id, frame)
        elif kind == "credentials_rotated":
            self._handle_credentials_rotated(session, session_id, frame)
        elif kind in ("deadline_approaching", "deadline_signal"):
            self._handle_deadline(
                LifecycleEvent(type=kind, raw=frame, session_id=session_id)
            )
        # checkpoint_complete and files_updated need no reply.

    def _handle_checkpoint(self, session_id: str, frame: dict[str, Any]) -> None:
        """Answer a §15.4.3 checkpoint_request: run the runtime quiesce
        callback for the named session, then reply
        ``checkpoint_ready``."""
        checkpoint_id = str(frame.get("checkpointId", ""))
        if self._hooks.on_checkpoint is not None:
            try:
                self._hooks.on_checkpoint(session_id, checkpoint_id)
            except Exception as err:
                self._host.log(f"on_checkpoint callback error: {err}")
        self._writer.write(
            {"type": "checkpoint_ready", "checkpointId": checkpoint_id}
        )

    def _handle_interrupt(self, session_id: str, frame: dict[str, Any]) -> None:
        """Answer a §15.4.3 interrupt_request: run the runtime safe-stop
        callback for the named session, then reply
        ``interrupt_acknowledged``."""
        interrupt_id = str(frame.get("interruptId", ""))
        if self._hooks.on_interrupt is not None:
            try:
                self._hooks.on_interrupt(session_id, interrupt_id)
            except Exception as err:
                self._host.log(f"on_interrupt callback error: {err}")
        self._writer.write(
            {"type": "interrupt_acknowledged", "interruptId": interrupt_id}
        )

    def _handle_credentials_rotated(
        self, session: Any, session_id: str, frame: dict[str, Any]
    ) -> None:
        """Answer a §15.4.3 credentials_rotated event: re-read the
        credential file the event names into the named session's bundle,
        run the runtime rotation callback, then reply
        ``credentials_acknowledged``.

        The session comes from the frame's ``sessionId`` rather than from
        parsing ``credentialsPath``, whose root is operator-configurable.

        spec: §28.5.3 (CH-RUNTIMEOPS, credentials_rotated), §4.7.11
        (item 4).
        """
        lease_id = str(frame.get("leaseId", ""))
        provider = str(frame.get("provider", ""))
        path = str(frame.get("credentialsPath", "") or "")
        creds = self._host.reload_credentials(session, path)
        if self._hooks.on_credentials_rotated is not None:
            try:
                self._hooks.on_credentials_rotated(session_id, creds)
            except Exception as err:
                self._host.log(f"on_credentials_rotated callback error: {err}")
        self._writer.write(
            {
                "type": "credentials_acknowledged",
                "leaseId": lease_id,
                "provider": provider,
            }
        )

    def _handle_deadline(self, event: LifecycleEvent) -> None:
        """Run the runtime deadline callback for a §15.4.3
        deadline_approaching or deadline_signal event."""
        if self._hooks.on_deadline is not None:
            self._hooks.on_deadline(event)
        else:
            self._host.log(f"lifecycle {event.type}")

    def _handle_terminate(self, frame: dict[str, Any]) -> None:
        """Answer a CH-RUNTIMEOPS terminate event: emit a final §28.5.3
        response frame on stdout carrying a DEADLINE_EXCEEDED error,
        record the termination reason every live session's on_terminate
        receives, and stop the frame loop so the runtime exits."""
        reason = str(frame.get("reason", "")) or "lifecycle_terminate"
        deadline_ms = int(frame.get("deadlineMs", 0))
        try:
            self._host.write_stdout_frame(
                {
                    "type": "response",
                    "output": [],
                    "error": {
                        "code": "DEADLINE_EXCEEDED",
                        "message": reason,
                    },
                    "sessionId": str(frame.get("sessionId", "")),
                }
            )
        except Exception as err:
            self._host.log(f"write terminate response: {err}")
        self._host.end_process(reason, deadline_ms)

    def send(self, frame: dict[str, Any]) -> None:
        """Write an arbitrary frame on the CH-RUNTIMEOPS.

        It is the escape hatch for lifecycle messages the SDK does not
        model. It applies no per-session filter, because a runtime may
        still report a late ``llm_request_completed`` for a request that
        started before the session's ``session_end``.

        spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2).
        """
        if self._closed:
            raise RuntimeError("CH-RUNTIMEOPS closed")
        self._writer.write(frame)

    def close(self) -> None:
        """Release the CH-RUNTIMEOPS connection."""
        if self._closed:
            return
        self._closed = True
        self._stream.close()
