# SPDX-License-Identifier: MIT

"""§15.7 entry point of the Python runtime-author SDK.

:func:`run` wires up the §15.4.1 stdin/stdout framing, optionally dials
the manifest-advertised Unix sockets (platform MCP server, connector MCP
servers, lifecycle channel) with the §15.4.3 manifest-nonce handshake,
parses the §4.7 credential file, and drives the §15.4.2 dispatch loop.
"""

from __future__ import annotations

import json
import os
import queue
import sys
import threading
from dataclasses import dataclass, field
from typing import Any, BinaryIO, Protocol

from .lifecycle import Lifecycle, LifecycleHooks, LifecycleHost
from .mcp import PlatformTools
from .tool import AdapterToolset, ToolCallRegistry
from .transport import (
    ByteSink,
    ByteSource,
    FrameWriter,
    LineReader,
    SocketStream,
    StdioSource,
    dial_unix_socket,
)
from .types import (
    AdapterManifest,
    CreateRequest,
    CredentialBundle,
    Message,
    MessageEnvelope,
    ProtocolError,
    Reply,
    ResponseError,
    TerminationReason,
)

# SOCKET_ENV_VAR is the §4.7 environment variable the adapter sets on
# the runtime container in the sidecar deployment model. Its value is
# the adapter's abstract Unix socket name.
SOCKET_ENV_VAR = "LENNY_ADAPTER_SOCKET"

# MANIFEST_ENV_VAR overrides the §4.7 adapter manifest path. The default
# path is /run/lenny/adapter-manifest.json.
MANIFEST_ENV_VAR = "LENNY_ADAPTER_MANIFEST"

# DEFAULT_MANIFEST_PATH is the §4.7 adapter manifest path.
DEFAULT_MANIFEST_PATH = "/run/lenny/adapter-manifest.json"

# DEFAULT_CREDENTIALS_PATH is the §4.7 runtime credential file path.
DEFAULT_CREDENTIALS_PATH = "/run/lenny/credentials.json"

# _LEVEL_RANK orders the §15.4.3 integration levels for comparison.
_LEVEL_RANK = {"basic": 0, "standard": 1, "full": 2}


@dataclass
class HandlerTools:
    """SDK surface passed to :meth:`Handler.on_message`.

    It carries the §15.4.1 adapter-local tool helpers (available at
    every level), the §8.5 platform MCP tool helpers (Standard level and
    above), and the current §4.7 credential bundle.
    """

    # adapter is the §15.4.1 adapter-local tool surface, present at
    # every integration level.
    adapter: AdapterToolset
    # platform is the §8.5 platform MCP tool surface, present only when
    # the runtime runs at Standard level or above and the manifest
    # advertised a platform MCP server. None otherwise.
    platform: PlatformTools | None = None
    # credentials is the current §4.7 credential bundle, or None when
    # the runtime's pool has no active lease.
    credentials: CredentialBundle | None = None


class Handler(Protocol):
    """Single interface a runtime author implements.

    The SDK invokes :meth:`on_create` once before the first message of a
    task, :meth:`on_message` for every inbound §15.4.1 message frame,
    and :meth:`on_terminate` once when the adapter closes stdin or sends
    a shutdown frame.
    """

    def on_create(self, req: CreateRequest) -> None:
        """Receive the task-scoped context snapshot before the first
        :class:`Message`. A raised exception aborts the runtime."""
        ...

    def on_message(self, msg: Message, tools: HandlerTools) -> Reply:
        """Handle one inbound message and return the turn's
        :class:`Reply`.

        A raised exception is reported to the adapter as a structured
        response error and the runtime continues.
        """
        ...

    def on_terminate(self, reason: TerminationReason) -> None:
        """Run once when the session ends.

        It SHOULD return before the shutdown deadline elapses.
        """
        ...


@dataclass
class RunOptions:
    """Configuration for :func:`run`.

    The default :class:`RunOptions` covers the Basic level.
    """

    # level is the §15.4.3 integration level. "basic" runs the
    # stdin/stdout protocol; "standard" dials the platform and connector
    # MCP servers; "full" additionally opens the lifecycle channel.
    level: str = "basic"
    # lifecycle holds the Full-level lifecycle-event callbacks. Setting
    # it implies level "full".
    lifecycle: LifecycleHooks | None = None
    # manifest_path overrides the §4.7 adapter manifest path. None means
    # the LENNY_ADAPTER_MANIFEST environment variable when set,
    # otherwise /run/lenny/adapter-manifest.json.
    manifest_path: str | None = None
    # credentials_path overrides the §4.7 runtime credential file path.
    # None means /run/lenny/credentials.json.
    credentials_path: str | None = None
    # socket_transport enables the §4.7 abstract-Unix-socket transport
    # fallback. When True (the default) and LENNY_ADAPTER_SOCKET is set,
    # run dials that socket instead of using stdin/stdout.
    socket_transport: bool = True
    # dial_timeout_s bounds each Unix-socket dial.
    dial_timeout_s: float = 5.0
    # input_stream and output_stream override the §15.4.1 byte
    # transport with explicit streams. They are intended for in-process
    # testing; production runtimes use the default stdin/stdout or
    # socket transport.
    input_stream: BinaryIO | None = None
    output_stream: BinaryIO | None = None
    # logger is the diagnostic sink for SDK-internal messages (unknown
    # frame types, handler errors). None means a stderr writer.
    logger: Any = field(default=None)


def run(handler: Handler, options: RunOptions | None = None) -> None:
    """Wire up the §15.4.1 stdin/stdout framing, dial the higher-level
    channels for the configured integration level, parse the §4.7
    credential file, and drive the §15.4.2 dispatch loop.

    It returns when the adapter closes the inbound stream or sends a
    shutdown frame.

    :func:`run` with the default :class:`RunOptions` covers the Basic
    level. Set ``level`` to ``"standard"`` or ``"full"`` to opt into the
    higher integration levels.
    """
    if handler is None:
        raise ValueError("runtime: run requires a handler")
    opts = options if options is not None else RunOptions()
    _Session(handler, opts).run()


def _logf(opts: RunOptions, msg: str) -> None:
    """Write a diagnostic line through the configured logger."""
    if opts.logger is not None:
        opts.logger(msg)
    else:
        sys.stderr.write(msg + "\n")
        sys.stderr.flush()


class _Session:
    """Per-process SDK state for one :func:`run` call."""

    def __init__(self, handler: Handler, opts: RunOptions) -> None:
        self._handler = handler
        self._opts = opts
        self._level = opts.level
        if opts.lifecycle is not None and self._level != "full":
            self._level = "full"
        self._manifest: AdapterManifest | None = None
        self._credentials: CredentialBundle | None = None
        self._tools: PlatformTools | None = None
        self._lifecycle: Lifecycle | None = None
        self._registry = ToolCallRegistry()
        self._writer: FrameWriter | None = None
        self._sequence = 0
        self._terminated = False
        self._terminated_lock = threading.Lock()
        self._exit_reason: TerminationReason | None = None
        self._loop_stopped = False
        self._socket_stream: SocketStream | None = None

    def run(self) -> None:
        """Drive one runtime lifecycle: resolve the transport, load the
        manifest and credentials, dial the higher-level channels for the
        configured level, then run the §15.4.1 frame loop."""
        in_source, out_sink = self._open_transport()
        self._writer = FrameWriter(out_sink)

        # §4.7 manifest and credential file. Both are optional: a
        # Basic-level runtime is exercised without a manifest, and a
        # runtime whose pool has no active lease has no credential file.
        self._load_manifest()
        self._load_credentials()

        self._start_channels()

        # on_create runs once before the first message with the
        # task-scoped snapshot the SDK assembled from the manifest and
        # credential file.
        try:
            self._handler.on_create(self._build_create_request())
        except Exception as err:
            self._close_channels()
            self._close_socket()
            raise RuntimeError(f"runtime: on_create: {err}") from err

        # The dispatch worker processes message frames one at a time, in
        # the order the loop reads them: this is the §15.4.1
        # coordinator-local FIFO contract. The loop keeps reading while
        # a handler is in flight, so heartbeats and tool_result frames
        # are still serviced.
        messages: queue.Queue[MessageEnvelope | None] = queue.Queue(maxsize=64)
        worker = threading.Thread(
            target=self._dispatch_worker, args=(messages,), daemon=True
        )
        worker.start()

        loop_err: Exception | None = None
        try:
            self._loop(in_source, messages)
        except Exception as err:
            loop_err = err

        # Close the queue and wait for the worker to drain every queued
        # message and write its response frame before on_terminate.
        messages.put(None)
        worker.join()

        # on_terminate runs once on the way out. The reason is the
        # shutdown frame's reason when the loop exited on a shutdown;
        # otherwise the adapter closed the transport without one. A
        # lifecycle terminate, if it fired, already invoked
        # on_terminate and this call is a no-op.
        reason = self._exit_reason or TerminationReason(reason="stdin_closed")
        self._invoke_terminate(reason.reason, reason.deadline_ms)

        self._registry.reject_all(
            RuntimeError("runtime: inbound stream closed")
        )
        self._close_channels()
        self._close_socket()
        if loop_err is not None:
            raise loop_err

    def _loop(
        self,
        in_source: ByteSource,
        messages: queue.Queue[MessageEnvelope | None],
    ) -> None:
        """§15.4.1 frame loop.

        It reads newline-delimited JSON and routes each frame by type:
        message frames go to the dispatch worker (preserving FIFO),
        heartbeat frames are answered inline, tool_result frames are
        correlated, and a shutdown frame ends the loop. Unknown frame
        types are ignored for forward compatibility (§15.4.1).
        """
        reader = LineReader(in_source)
        assert self._writer is not None
        while True:
            if self._loop_stopped:
                return
            try:
                line = reader.next()
            except (OSError, ValueError) as err:
                raise ProtocolError(f"input read error: {err}") from err
            if line is None:
                return
            if not line:
                continue
            try:
                frame = json.loads(line)
            except json.JSONDecodeError as err:
                raise ProtocolError(
                    f"malformed JSON Lines on input: {err}"
                ) from err
            kind = frame.get("type", "") if isinstance(frame, dict) else ""
            if kind == "message":
                try:
                    env = MessageEnvelope.from_wire(frame)
                except (KeyError, TypeError, ValueError) as err:
                    raise ProtocolError(
                        f"malformed message envelope: {err}"
                    ) from err
                messages.put(env)
            elif kind == "heartbeat":
                self._writer.write({"type": "heartbeat_ack"})
            elif kind == "tool_result":
                self._handle_tool_result(frame)
            elif kind == "shutdown":
                self._handle_shutdown(frame)
                return
            else:
                _logf(
                    self._opts,
                    f"runtime: ignoring unknown frame type {kind!r}",
                )

    def _dispatch_worker(
        self, messages: queue.Queue[MessageEnvelope | None]
    ) -> None:
        """Process queued §15.4.1 message frames one at a time, in
        arrival order."""
        while True:
            env = messages.get()
            if env is None:
                return
            self._handle_message(env)

    def _handle_message(self, env: MessageEnvelope) -> None:
        """Invoke :meth:`Handler.on_message` for one decoded §15.4.1
        message and write the resulting response frame.

        A handler exception is reported as a structured response error
        so the adapter records the failure without losing context
        (§15.4.1 error reporting via response).
        """
        assert self._writer is not None
        self._sequence += 1
        msg = Message(
            envelope=env,
            session_id=self._manifest.session_id if self._manifest else "",
            task_id=self._manifest.task_id if self._manifest else "",
            sequence=self._sequence,
        )
        tools = HandlerTools(
            adapter=AdapterToolset(
                self._writer,
                self._registry,
                self._opts.dial_timeout_s,
                env.slot_id,
            ),
            platform=self._tools,
            credentials=self._credentials,
        )

        try:
            reply = self._handler.on_message(msg, tools)
        except Exception as err:
            _logf(self._opts, f"runtime: on_message error: {err}")
            frame: dict[str, Any] = {
                "type": "response",
                "output": [],
                "error": {"code": "RUNTIME_ERROR", "message": str(err)},
            }
            if env.slot_id:
                frame["slotId"] = env.slot_id
            self._safe_write(frame)
            return

        # A turn marked streaming and not final defers the response
        # frame. The §15.4.1 contract still requires a final response
        # frame, so the SDK emits it once the runtime returns a final
        # Reply.
        if reply.streaming and not reply.final:
            return
        frame = {
            "type": "response",
            "output": [p.to_wire() for p in reply.parts],
        }
        if reply.error is not None:
            frame["error"] = reply.error.to_wire()
        if env.slot_id:
            frame["slotId"] = env.slot_id
        self._safe_write(frame)

    def _handle_tool_result(self, frame: dict[str, Any]) -> None:
        """Route an inbound §15.4.1 tool_result frame to the pending
        tool_call that emitted the matching id."""
        if not self._registry.deliver(frame):
            _logf(
                self._opts,
                f"runtime: tool_result {frame.get('id')!r} has no "
                "pending tool_call",
            )

    def _handle_shutdown(self, frame: dict[str, Any]) -> None:
        """Decode the §15.4.1 shutdown frame and record the termination
        reason for :meth:`run` to apply after draining in-flight
        handlers."""
        self._exit_reason = TerminationReason(
            reason=str(frame.get("reason", "shutdown")),
            deadline_ms=int(frame.get("deadline_ms", 0)),
        )

    def _safe_write(self, frame: dict[str, Any]) -> None:
        """Write an outbound frame, logging a write error instead of
        propagating it: a write failure usually means the adapter has
        closed the transport."""
        assert self._writer is not None
        try:
            self._writer.write(frame)
        except OSError as err:
            _logf(self._opts, f"runtime: write frame: {err}")

    def _start_channels(self) -> None:
        """Dial the §15.4.3 platform MCP server, connector MCP servers,
        and lifecycle channel for the configured integration level.

        When a higher-level channel is configured but the manifest does
        not advertise it, the SDK logs the gap and degrades to the level
        the manifest supports, so a Standard- or Full-level binary still
        runs in a Basic-only environment.
        """
        rank = _LEVEL_RANK.get(self._level, 0)
        if rank >= _LEVEL_RANK["standard"]:
            if self._manifest is None or self._manifest.platform_mcp_server is None:
                _logf(
                    self._opts,
                    "runtime: no platform MCP server in the manifest; "
                    "degrading to Basic level",
                )
            else:
                self._tools = PlatformTools.dial(
                    self._manifest, self._opts.dial_timeout_s
                )
        if rank >= _LEVEL_RANK["full"]:
            if self._manifest is None or self._manifest.lifecycle_channel is None:
                _logf(
                    self._opts,
                    "runtime: no lifecycle channel in the manifest; "
                    "lifecycle features disabled",
                )
            else:
                assert self._writer is not None
                self._lifecycle = Lifecycle.dial(
                    self._manifest,
                    self._opts.dial_timeout_s,
                    self._opts.lifecycle or LifecycleHooks(),
                    LifecycleHost(
                        write_stdout_frame=self._safe_write,
                        reload_credentials=self._reload_credentials,
                        invoke_terminate=self._invoke_terminate,
                        stop_frame_loop=self._stop_frame_loop,
                        log=lambda msg: _logf(self._opts, f"runtime: {msg}"),
                    ),
                )

    def _stop_frame_loop(self) -> None:
        """Signal the §15.4.1 frame loop to exit. The lifecycle channel
        calls it on a terminate event."""
        self._loop_stopped = True

    def _close_channels(self) -> None:
        """Release the higher-level channels."""
        if self._tools is not None:
            self._tools.close()
        if self._lifecycle is not None:
            self._lifecycle.close()

    def _close_socket(self) -> None:
        """Release the Unix-socket transport when one was dialed."""
        if self._socket_stream is not None:
            self._socket_stream.close()

    def _invoke_terminate(self, reason: str, deadline_ms: int) -> None:
        """Call :meth:`Handler.on_terminate` at most once.

        The terminated guard makes a lifecycle-channel terminate and the
        stdin shutdown path idempotent.
        """
        with self._terminated_lock:
            if self._terminated:
                return
            self._terminated = True
        try:
            self._handler.on_terminate(
                TerminationReason(reason=reason, deadline_ms=deadline_ms)
            )
        except Exception as err:
            _logf(self._opts, f"runtime: on_terminate error: {err}")

    def _build_create_request(self) -> CreateRequest:
        """Assemble the §15.7 on_create snapshot from the manifest and
        credential file."""
        return CreateRequest(
            session_id=self._manifest.session_id if self._manifest else "",
            task_id=self._manifest.task_id if self._manifest else "",
            runtime_options=self._manifest.runtime_options
            if self._manifest
            else {},
            credentials=self._credentials,
            manifest_snapshot=self._manifest,
        )

    def _load_manifest(self) -> None:
        """Parse the §4.7 adapter manifest.

        A missing file leaves the manifest unset; a malformed file is
        logged and ignored. A manifest version newer than the SDK
        understands is rejected (§4.7 forward-compatibility rule).
        """
        path = (
            self._opts.manifest_path
            or os.environ.get(MANIFEST_ENV_VAR)
            or DEFAULT_MANIFEST_PATH
        )
        try:
            with open(path, encoding="utf-8") as fh:
                raw = json.load(fh)
        except OSError as err:
            _logf(self._opts, f"runtime: no adapter manifest at {path} ({err})")
            return
        except json.JSONDecodeError as err:
            _logf(
                self._opts,
                f"runtime: malformed adapter manifest {path}: {err}",
            )
            return
        manifest = AdapterManifest.from_wire(raw)
        if manifest.version > 1:
            _logf(
                self._opts,
                f"runtime: adapter manifest {path} version "
                f"{manifest.version} is newer than supported (1)",
            )
            return
        self._manifest = manifest

    def _load_credentials(self) -> None:
        """Parse the §4.7 runtime credential file.

        A missing file is normal when the runtime's pool has no active
        lease.
        """
        path = self._opts.credentials_path or DEFAULT_CREDENTIALS_PATH
        try:
            with open(path, encoding="utf-8") as fh:
                raw = json.load(fh)
        except OSError:
            return
        except json.JSONDecodeError as err:
            _logf(
                self._opts,
                f"runtime: malformed credential file {path}: {err}",
            )
            return
        self._credentials = CredentialBundle.from_wire(raw)

    def _reload_credentials(self) -> CredentialBundle | None:
        """Re-read the §4.7 credential file in place.

        The lifecycle channel calls it on a credentials_rotated event so
        a Full-level runtime continues without restart.
        """
        self._load_credentials()
        return self._credentials

    def _open_transport(self) -> tuple[ByteSource, ByteSink]:
        """Resolve the §15.4.1 transport.

        When explicit streams were supplied it adapts them; when socket
        transport is enabled and LENNY_ADAPTER_SOCKET names a socket it
        dials that socket; otherwise it returns the stdin/stdout binary
        buffers. The read side is wrapped in a :class:`StdioSource` so
        the line reader gets first-available-chunk reads.
        """
        if (
            self._opts.input_stream is not None
            or self._opts.output_stream is not None
        ):
            return (
                StdioSource(self._opts.input_stream or sys.stdin.buffer),
                self._opts.output_stream or sys.stdout.buffer,
            )
        if self._opts.socket_transport:
            name = os.environ.get(SOCKET_ENV_VAR, "").strip()
            if name:
                stream = dial_unix_socket(name, self._opts.dial_timeout_s)
                self._socket_stream = stream
                return stream, stream
        return StdioSource(sys.stdin.buffer), sys.stdout.buffer
