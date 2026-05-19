# SPDX-License-Identifier: MIT

"""§15.4.3 Full-level lifecycle channel.

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


@dataclass
class LifecycleEvent:
    """Decoded lifecycle-channel frame handed to a runtime callback.

    ``raw`` carries the full frame for fields the typed callbacks do not
    cover.
    """

    type: str
    raw: dict[str, Any]


@dataclass
class LifecycleHooks:
    """Optional runtime callbacks for lifecycle events.

    An unset hook means the SDK answers with the default behavior.
    """

    # on_checkpoint runs on a §15.4.3 checkpoint_request before the SDK
    # replies checkpoint_ready. The callback quiesces runtime output.
    on_checkpoint: Callable[[str], None] | None = None
    # on_interrupt runs on a §15.4.3 interrupt_request before the SDK
    # replies interrupt_acknowledged. The callback brings the runtime
    # to a safe stop point.
    on_interrupt: Callable[[str], None] | None = None
    # on_credentials_rotated runs after the SDK re-reads the §4.7
    # credential file on a credentials_rotated event.
    on_credentials_rotated: Callable[[CredentialBundle | None], None] | None = (
        None
    )
    # on_deadline runs on a §15.4.3 deadline_approaching or
    # deadline_signal event.
    on_deadline: Callable[[LifecycleEvent], None] | None = None


@dataclass
class LifecycleHost:
    """Subset of the SDK session the lifecycle channel reaches.

    The lifecycle channel uses the stdout frame writer for the §15.4.6
    terminate response, reloads credentials, invokes the terminate
    callback, stops the frame loop, and logs diagnostics.
    """

    write_stdout_frame: Callable[[dict[str, Any]], None]
    reload_credentials: Callable[[], CredentialBundle | None]
    invoke_terminate: Callable[[str, int], None]
    stop_frame_loop: Callable[[], None]
    log: Callable[[str], None] = field(default=lambda _msg: None)


class Lifecycle:
    """§15.4.3 Full-level lifecycle channel surface.

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
        """Open the §15.4.3 lifecycle channel.

        It dials the manifest-advertised socket, completes the
        ``lifecycle_capabilities`` / ``lifecycle_support`` handshake,
        and starts the event loop on a daemon thread.
        """
        if manifest.lifecycle_channel is None:
            raise RuntimeError(
                "adapter manifest has no lifecycle channel socket"
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
        """Process inbound lifecycle-channel frames until the connection
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
            kind = frame.get("type", "")
            if kind == "checkpoint_request":
                self._handle_checkpoint(frame)
            elif kind == "interrupt_request":
                self._handle_interrupt(frame)
            elif kind == "credentials_rotated":
                self._handle_credentials_rotated(frame)
            elif kind in ("deadline_approaching", "deadline_signal"):
                self._handle_deadline(LifecycleEvent(type=kind, raw=frame))
            elif kind == "terminate":
                self._handle_terminate(frame)
                return
            else:
                self._host.log(f"ignoring unknown lifecycle event {kind!r}")

    def _handle_checkpoint(self, frame: dict[str, Any]) -> None:
        """Answer a §15.4.3 checkpoint_request: run the runtime quiesce
        callback, then reply ``checkpoint_ready``."""
        checkpoint_id = str(frame.get("checkpointId", ""))
        if self._hooks.on_checkpoint is not None:
            try:
                self._hooks.on_checkpoint(checkpoint_id)
            except Exception as err:
                self._host.log(f"on_checkpoint callback error: {err}")
        self._writer.write(
            {"type": "checkpoint_ready", "checkpointId": checkpoint_id}
        )

    def _handle_interrupt(self, frame: dict[str, Any]) -> None:
        """Answer a §15.4.3 interrupt_request: run the runtime safe-stop
        callback, then reply ``interrupt_acknowledged``."""
        interrupt_id = str(frame.get("interruptId", ""))
        if self._hooks.on_interrupt is not None:
            try:
                self._hooks.on_interrupt(interrupt_id)
            except Exception as err:
                self._host.log(f"on_interrupt callback error: {err}")
        self._writer.write(
            {"type": "interrupt_acknowledged", "interruptId": interrupt_id}
        )

    def _handle_credentials_rotated(self, frame: dict[str, Any]) -> None:
        """Answer a §15.4.3 credentials_rotated event: re-read the §4.7
        credential file in place, run the runtime rotation callback,
        then reply ``credentials_acknowledged``."""
        lease_id = str(frame.get("leaseId", ""))
        provider = str(frame.get("provider", ""))
        creds = self._host.reload_credentials()
        if self._hooks.on_credentials_rotated is not None:
            self._hooks.on_credentials_rotated(creds)
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
        """Answer a §15.4.3 terminate event: emit a final §15.4.1
        response frame on stdout (carrying a DEADLINE_EXCEEDED error,
        per the §15.4.6 deadline-signal expectation), invoke
        on_terminate, and stop the frame loop so the runtime exits."""
        reason = str(frame.get("reason", "")) or "lifecycle_terminate"
        deadline_ms = int(frame.get("deadlineMs", 0))

        # §15.4.6 deadline-signal handling: the runtime writes a final
        # response on the stdout protocol channel before it exits.
        try:
            self._host.write_stdout_frame(
                {
                    "type": "response",
                    "output": [],
                    "error": {
                        "code": "DEADLINE_EXCEEDED",
                        "message": reason,
                    },
                }
            )
        except Exception as err:
            self._host.log(f"write terminate response: {err}")

        self._host.invoke_terminate(reason, deadline_ms)
        self._host.stop_frame_loop()

    def send(self, frame: dict[str, Any]) -> None:
        """Write an arbitrary frame on the lifecycle channel.

        It is the escape hatch for lifecycle messages the SDK does not
        model.
        """
        if self._closed:
            raise RuntimeError("lifecycle channel closed")
        self._writer.write(frame)

    def close(self) -> None:
        """Release the lifecycle-channel connection."""
        if self._closed:
            return
        self._closed = True
        self._stream.close()
