# SPDX-License-Identifier: MIT

"""§15.7 entry point of the Python runtime-author SDK.

:func:`run` wires up the §28.5.3 stdin/stdout framing, optionally dials
the manifest-advertised Unix sockets (platform MCP server, connector MCP
servers, CH-RUNTIMEOPS) once per process with the §15.4.3 manifest-nonce
handshake, and drives the frame loop. One process serves every session
the pod holds: each ``session_start`` opens a session with its own
context and worker thread, and each ``session_end`` releases it
(§4.7.10).
"""

from __future__ import annotations

import json
import os
import sys
import threading
from dataclasses import dataclass, field
from typing import Any, BinaryIO, Protocol

from .lifecycle import Lifecycle, LifecycleHooks, LifecycleHost
from .mcp import PlatformTools
from .session import (
    CLOSE,
    CredentialFileError,
    SessionRecord,
    SessionStart,
    SessionTable,
    load_credential_bundle,
    session_error_response,
)
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

# _LEVEL_RANK orders the §15.4.3 integration levels for comparison.
_LEVEL_RANK = {"basic": 0, "standard": 1, "full": 2}


@dataclass
class HandlerTools:
    """SDK surface passed to :meth:`Handler.on_message`.

    It carries the §28.5.3 adapter-local tool helpers (available at
    every level), the §8.5 platform MCP tool helpers (Standard level and
    above), and the current §4.7 credential bundle.
    """

    # adapter is the §28.5.3 adapter-local tool surface, present at
    # every integration level.
    adapter: AdapterToolset
    # platform is the §8.5 platform MCP tool surface, present only when
    # the runtime runs at Standard level or above and the manifest
    # advertised a platform MCP server. None otherwise.
    platform: PlatformTools | None = None
    # credentials is the session's current credential bundle, or None
    # when the session's session_start named no credential file.
    credentials: CredentialBundle | None = None


class Handler(Protocol):
    """Single interface a runtime author implements.

    One runtime process serves any number of sessions, one after another
    and side by side. The SDK invokes :meth:`on_create` when a
    ``session_start`` opens a session, and writes the session's
    ``session_started`` once :meth:`on_create` returns;
    :meth:`on_message` for each of the session's messages; and
    :meth:`on_terminate` once when that session ends, on its
    ``session_end`` or at the end of the connection.

    Each session's calls run on that session's own worker thread, so calls
    for different sessions run concurrently on different threads and an
    implementation keeps per-session state keyed by session and guards
    state it shares across sessions with a lock. Calls for one session
    never overlap. A session whose :meth:`on_create` raises is answered as
    the CH-MSGSOCK Session errors rule states, and the process keeps
    serving its other sessions.

    spec: §15.7 (API surface, Handler), §4.7.10 (runtime process
    lifetime), §28.5.3 (CH-MSGSOCK, Inbound: session_start, Session
    errors).
    """

    def on_create(self, req: CreateRequest) -> None:
        """Receive the session's context snapshot before the session's
        first :class:`Message`.

        A raised exception fails the session's creation: the SDK writes
        ``session_started`` with ``error`` and answers the session's
        messages with a RUNTIME_ERROR response.
        """
        ...

    def on_message(self, msg: Message, tools: HandlerTools) -> Reply:
        """Handle one inbound message and return the turn's
        :class:`Reply`.

        A raised exception is reported to the adapter as a structured
        response error and the session continues with its next message.
        """
        ...

    def on_terminate(self, session_id: str, reason: TerminationReason) -> None:
        """Run once when the session ``session_id`` ends, after the
        session's last handler call returned.

        It runs for every session the SDK opened, including one whose
        creation failed. It SHOULD return before the shutdown deadline
        elapses.
        """
        ...


@dataclass
class RunOptions:
    """Configuration for :func:`run`.

    The default :class:`RunOptions` covers the Basic level.
    """

    # level is the §15.4.3 integration level. "basic" runs the
    # stdin/stdout protocol; "standard" dials the platform and connector
    # MCP servers; "full" additionally opens the CH-RUNTIMEOPS.
    level: str = "basic"
    # lifecycle holds the Full-level lifecycle-event callbacks. Setting
    # it implies level "full".
    lifecycle: LifecycleHooks | None = None
    # manifest_path overrides the §4.7 adapter manifest path. None means
    # the LENNY_ADAPTER_MANIFEST environment variable when set,
    # otherwise /run/lenny/adapter-manifest.json.
    manifest_path: str | None = None
    # socket_transport enables the §4.7 abstract-Unix-socket transport
    # fallback. When True (the default) and LENNY_ADAPTER_SOCKET is set,
    # run dials that socket instead of using stdin/stdout.
    socket_transport: bool = True
    # dial_timeout_s bounds each Unix-socket dial.
    dial_timeout_s: float = 5.0
    # input_stream and output_stream override the §28.5.3 byte
    # transport with explicit streams. They are intended for in-process
    # testing; production runtimes use the default stdin/stdout or
    # socket transport.
    input_stream: BinaryIO | None = None
    output_stream: BinaryIO | None = None
    # logger is the diagnostic sink for SDK-internal messages (unknown
    # frame types, handler errors). None means a stderr writer.
    logger: Any = field(default=None)


def run(handler: Handler, options: RunOptions | None = None) -> None:
    """Wire up the §28.5.3 stdin/stdout framing, dial the higher-level
    channels for the configured integration level once per process, and
    drive the frame loop.

    Each ``session_start`` opens a session with its own context and worker
    thread, and each session loads its credentials from the path its
    ``session_start`` names. :func:`run` returns when the adapter closes
    the inbound stream or sends a shutdown frame, after it ends every
    session the process holds.

    :func:`run` with the default :class:`RunOptions` covers the Basic
    level. Set ``level`` to ``"standard"`` or ``"full"`` to opt into the
    higher integration levels.

    spec: §15.7 (API surface, Run), §4.7.10 (runtime process lifetime).
    """
    if handler is None:
        raise ValueError("runtime: run requires a handler")
    opts = options if options is not None else RunOptions()
    _Process(handler, opts).run()


def _logf(opts: RunOptions, msg: str) -> None:
    """Write a diagnostic line through the configured logger."""
    if opts.logger is not None:
        opts.logger(msg)
    else:
        sys.stderr.write(msg + "\n")
        sys.stderr.flush()


class _Process:
    """SDK state of one :func:`run` call.

    The manifest, the MCP connections, and the CH-RUNTIMEOPS are
    process-scoped and shared by every session; each session's own
    context lives in its :class:`SessionRecord`.
    """

    def __init__(self, handler: Handler, opts: RunOptions) -> None:
        self._handler = handler
        self._opts = opts
        self._level = opts.level
        if opts.lifecycle is not None and self._level != "full":
            self._level = "full"
        self._manifest: AdapterManifest | None = None
        self._tools: PlatformTools | None = None
        self._lifecycle: Lifecycle | None = None
        self._registry = ToolCallRegistry()
        self._writer: FrameWriter | None = None
        self._sessions = SessionTable()
        # _workers holds every session worker thread, including one whose
        # record a session_end removed while its release is running.
        self._workers: list[threading.Thread] = []
        self._workers_lock = threading.Lock()
        self._exit_reason: TerminationReason | None = None
        self._socket_stream: SocketStream | None = None

    def run(self) -> None:
        """Drive one runtime process: resolve the transport, load the
        manifest, dial the higher-level channels for the configured level,
        run the §28.5.3 frame loop, and end every session once the loop
        ends."""
        in_source, out_sink = self._open_transport()
        self._writer = FrameWriter(out_sink)

        # §4.7 manifest. It is optional: a Basic-level runtime is
        # exercised without one.
        self._load_manifest()
        self._start_channels()

        loop_err: Exception | None = None
        try:
            self._loop(in_source)
        except Exception as err:
            loop_err = err

        # Every live session dispatches the messages it queued and then
        # runs on_terminate with the shutdown frame's reason, or
        # stdin_closed when the adapter closed the transport without one.
        # run returns after every session worker returned, including one a
        # session_end removed whose release is still running.
        self._close_sessions(
            self._exit_reason or TerminationReason(reason="stdin_closed")
        )
        self._join_workers()

        self._registry.reject_all(
            RuntimeError("runtime: inbound stream closed")
        )
        self._close_channels()
        self._close_socket()
        if loop_err is not None:
            raise loop_err

    def _loop(self, in_source: ByteSource) -> None:
        """§28.5.3 frame loop.

        It reads newline-delimited JSON and routes each frame by type
        without blocking on any session's work: ``session_start``,
        ``message``, ``tool_result``, and ``session_end`` go to the
        addressed session, a heartbeat is answered inline, and a shutdown
        frame ends the loop. Unknown frame types are ignored for forward
        compatibility.

        spec: §28.5.3 (CH-MSGSOCK).
        """
        reader = LineReader(in_source)
        while True:
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
            if self._route_frame(frame):
                return

    def _route_frame(self, frame: Any) -> bool:
        """Handle one inbound frame. Returns True for a shutdown frame,
        which ends the loop."""
        assert self._writer is not None
        kind = frame.get("type", "") if isinstance(frame, dict) else ""
        if kind == "session_start":
            self._handle_session_start(frame)
        elif kind == "session_end":
            self._handle_session_end(frame)
        elif kind == "message":
            try:
                env = MessageEnvelope.from_wire(frame)
            except (KeyError, TypeError, ValueError) as err:
                raise ProtocolError(f"malformed message envelope: {err}") from err
            self._route_message(env)
        elif kind == "heartbeat":
            self._writer.write({"type": "heartbeat_ack"})
        elif kind == "tool_result":
            self._handle_tool_result(frame)
        elif kind == "shutdown":
            self._handle_shutdown(frame)
            return True
        else:
            _logf(self._opts, f"runtime: ignoring unknown frame type {kind!r}")
        return False

    # --- sessions -------------------------------------------------------

    def _handle_session_start(self, frame: dict[str, Any]) -> None:
        """Open the session a ``session_start`` names.

        A frame for a session the runtime already holds creates nothing
        and is answered with ``session_started`` again, carrying the held
        session's creation error when there is one.

        spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start rules 2 and 3).
        """
        start = SessionStart(frame)
        if not start.session_id or not start.start_id:
            raise ProtocolError("session_start carries no sessionId or no startId")
        rec, held = self._sessions.open(start)
        if held:
            self._ack_duplicate_start(rec, start.start_id)
            return
        worker = threading.Thread(
            target=self._serve,
            args=(rec,),
            name=f"lenny-session-{rec.id}",
            daemon=True,
        )
        with self._workers_lock:
            self._workers.append(worker)
        worker.start()

    def _ack_duplicate_start(self, rec: SessionRecord, start_id: str) -> None:
        """Answer a duplicate ``session_start`` for a held session. While
        the held session's creation is still running the answer waits for
        it, so ``session_started`` never precedes the context it
        reports."""
        with rec.lock:
            if not rec.create_done:
                rec.pending_acks.append(start_id)
                return
            err = rec.create_error
        self._write_session_started(rec, start_id, err)

    def _handle_session_end(self, frame: dict[str, Any]) -> None:
        """End the start of the session's live record.

        The loop removes the record from the routing table, marks it ended
        under the stdout writer's lock so no later response or tool_call
        for it is written, fails its pending tool_call waiters, and closes
        its queue. The session's worker skips the messages still queued,
        waits for the in-flight handler, and runs on_terminate once the
        creation has finished. A ``session_end`` for a session the runtime
        does not hold is ignored.

        spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rules 2 and 3).
        """
        assert self._writer is not None
        session_id = str(frame.get("sessionId", ""))
        rec = self._sessions.remove(session_id)
        if rec is None:
            _logf(
                self._opts,
                f"runtime: ignoring session_end for session {session_id!r}, "
                "which this runtime does not hold",
            )
            return
        self._writer.end_owner(rec)
        self._registry.cancel_owner(rec)
        with rec.lock:
            rec.end_read = True
        rec.queue.put(CLOSE)

    def _route_message(self, env: MessageEnvelope) -> None:
        """Hand a message frame to its session's queue.

        A message for a session whose ``session_start`` the runtime never
        read is answered at once with a RUNTIME_ERROR response; a message
        for a session whose creation failed is answered the same way by
        the session's worker, in order. A message for a session the
        runtime read a ``session_end`` for, and that no later
        ``session_start`` reopened, is logged and dropped: after the
        ``session_end`` the runtime writes no response addressed to the
        session.

        spec: §28.5.3 (CH-MSGSOCK, Session errors; Inbound: session_end
        rule 2).
        """
        session_id = env.session_id or ""
        rec = self._sessions.lookup(session_id)
        if rec is not None:
            rec.queue.put(env)
            return
        if self._sessions.ended_since(session_id):
            _logf(
                self._opts,
                f"runtime: dropping message {env.id!r} for session "
                f"{session_id!r}, which already ended",
            )
            return
        _logf(
            self._opts,
            f"runtime: message {env.id!r} for session {session_id!r}, "
            "which this runtime does not hold",
        )
        self._safe_write(
            session_error_response(
                session_id, f"no session {session_id} is open on this runtime"
            )
        )

    def _serve(self, rec: SessionRecord) -> None:
        """A session's worker thread: create the session's context,
        dispatch the session's messages in order, and release the
        context."""
        if rec.prev is not None:
            rec.prev.released.wait()
        self._create(rec)
        while True:
            item = rec.queue.get()
            if item is CLOSE:
                break
            if rec.ending():
                continue
            self._dispatch(rec, item)
        self._release(rec)

    def _create(self, rec: SessionRecord) -> None:
        """Load the session's credential bundle, invoke on_create, and
        write the session's ``session_started``, with ``error`` when
        either failed. Then answer any duplicate ``session_start`` read
        meanwhile. A failure is the session's own: the process keeps
        serving its other sessions.

        spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started rules 1 and
        2, Session errors), §15.7 (Handler).
        """
        err = self._create_context(rec)
        if err is not None:
            _logf(self._opts, f"runtime: session {rec.id}: {err}")
        with rec.lock:
            rec.create_error = err
        self._write_session_started(rec, rec.start_id, err)
        with rec.lock:
            rec.create_done = True
            acks = rec.pending_acks
            rec.pending_acks = []
        for start_id in acks:
            self._write_session_started(rec, start_id, err)

    def _create_context(self, rec: SessionRecord) -> Exception | None:
        """Read the credential file the ``session_start`` names and
        invoke on_create with the session's :class:`CreateRequest`.
        Returns the failure, or None."""
        try:
            creds = load_credential_bundle(rec.start.credentials_path)
        except CredentialFileError as err:
            return err
        rec.set_credentials(creds)
        manifest = self._manifest
        req = CreateRequest(
            session_id=rec.id,
            task_id=rec.id,
            runtime_options=dict(manifest.runtime_options) if manifest else {},
            credentials=creds,
            experiment_context=rec.start.experiment_context,
            tracing_context=rec.start.tracing_context,
            llm=rec.start.llm,
            manifest_snapshot=manifest,
        )
        try:
            self._handler.on_create(req)
        except Exception as err:  # noqa: BLE001 — the session's own failure
            return RuntimeError(f"on_create: {err}")
        return None

    def _dispatch(self, rec: SessionRecord, env: MessageEnvelope) -> None:
        """Handle one queued message for the session."""
        if rec.failed():
            self._write_for(
                rec,
                session_error_response(
                    rec.id, f"the context of session {rec.id} could not be created"
                ),
            )
            return
        self._handle_message(rec, env)

    def _release(self, rec: SessionRecord) -> None:
        """Run on_terminate for the session and drop its record.

        It runs on the session's worker after the in-flight handler
        returned, so on_terminate never overlaps one of the session's own
        handler calls. It runs for every record the SDK opened, whether
        its creation succeeded or failed, so every session ends the same
        way.

        spec: §15.7 (Runtime Author SDKs), §28.5.3 (CH-MSGSOCK, Inbound:
        session_end).
        """
        try:
            self._handler.on_terminate(rec.id, rec.termination_reason())
        except Exception as err:  # noqa: BLE001 — logged, the process continues
            _logf(self._opts, f"runtime: session {rec.id}: on_terminate error: {err}")
        rec.released.set()
        self._sessions.forget(rec)

    def _close_sessions(self, reason: TerminationReason) -> None:
        """Close every live session's queue with ``reason``, on EOF or
        shutdown. Each session dispatches the messages it already queued
        and then runs on_terminate with ``reason``."""
        for rec in self._sessions.live_records():
            with rec.lock:
                if rec.close_reason is None:
                    rec.close_reason = reason
            rec.queue.put(CLOSE)

    def _join_workers(self) -> None:
        """Wait for every session worker, including one started while an
        earlier worker was being joined."""
        joined = 0
        while True:
            with self._workers_lock:
                pending = self._workers[joined:]
            if not pending:
                return
            for worker in pending:
                worker.join()
            joined += len(pending)

    def _held_session(self, session_id: str) -> SessionRecord | None:
        """Return the live record a session-scoped CH-RUNTIMEOPS event
        names, or None when the runtime does not hold the session or
        failed to create its context. The caller drops the event without
        a reply.

        spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
        """
        rec = self._sessions.lookup(session_id)
        if rec is None or rec.failed():
            return None
        return rec

    def _write_session_started(
        self, rec: SessionRecord, start_id: str, err: Exception | None
    ) -> None:
        """Write the ``session_started`` frame answering the
        ``session_start`` whose startId is ``start_id``. It is written
        even after the session's ``session_end``, because the frame
        answers a ``session_start`` read before it."""
        frame: dict[str, Any] = {
            "type": "session_started",
            "sessionId": rec.id,
            "startId": start_id,
        }
        if err is not None:
            frame["error"] = {"code": "RUNTIME_ERROR", "message": str(err)}
        self._safe_write(frame)

    def _write_for(self, rec: SessionRecord, frame: dict[str, Any]) -> None:
        """Write a frame addressed to ``rec``, dropping it when the
        session already ended."""
        assert self._writer is not None
        try:
            if not self._writer.write_for(rec, frame):
                _logf(
                    self._opts,
                    f"runtime: session {rec.id} ended; dropping its "
                    f"{frame.get('type')} frame",
                )
        except OSError as err:
            _logf(self._opts, f"runtime: write frame: {err}")

    def _handle_message(self, rec: SessionRecord, env: MessageEnvelope) -> None:
        """Invoke :meth:`Handler.on_message` for one of the session's
        messages and write the resulting response frame.

        It runs on the session's worker, one message at a time. A handler
        exception is reported as a structured response error so the
        adapter records the failure without losing context (§28.5.3 error
        reporting via response). A response for a session that already
        ended is dropped.
        """
        assert self._writer is not None
        rec.sequence += 1
        msg = Message(
            envelope=env,
            session_id=rec.id,
            task_id=rec.id,
            sequence=rec.sequence,
        )
        tools = HandlerTools(
            adapter=AdapterToolset(
                self._writer, self._registry, self._opts.dial_timeout_s, rec
            ),
            platform=self._tools,
            credentials=rec.get_credentials(),
        )
        try:
            reply = self._handler.on_message(msg, tools)
        except Exception as err:  # noqa: BLE001 — surfaced as a response error
            _logf(self._opts, f"runtime: session {rec.id}: on_message error: {err}")
            self._write_for(rec, session_error_response(rec.id, str(err)))
            return

        # A turn marked streaming and not final defers the response
        # frame. The §28.5.3 contract still requires a final response
        # frame, so the SDK emits it once the runtime returns a final
        # Reply.
        if reply.streaming and not reply.final:
            return
        frame: dict[str, Any] = {
            "type": "response",
            "output": [p.to_wire() for p in reply.parts],
        }
        if reply.error is not None:
            frame["error"] = reply.error.to_wire()
        # §28.5.3: every session-scoped frame carries the session it
        # addresses.
        frame["sessionId"] = rec.id
        self._write_for(rec, frame)

    def _handle_tool_result(self, frame: dict[str, Any]) -> None:
        """Route an inbound §28.5.3 ``tool_result`` frame to the pending
        ``tool_call`` of the session its ``sessionId`` addresses. A result
        whose id matches no pending call of that session is dropped and
        logged.

        spec: §28.5.3 (CH-MSGSOCK, Inbound: tool_result).
        """
        if not self._registry.deliver(frame):
            _logf(
                self._opts,
                f"runtime: tool_result {frame.get('id')!r} for session "
                f"{frame.get('sessionId')!r} has no pending tool_call in "
                "that session",
            )

    def _handle_shutdown(self, frame: dict[str, Any]) -> None:
        """Record the termination reason of a §28.5.3 shutdown frame.

        Shutdown is process-scoped: :meth:`run` hands the reason to every
        live session after the loop ends, and each session drains its
        queued messages before its on_terminate.
        """
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

    # --- process-scoped channels ---------------------------------------

    def _start_channels(self) -> None:
        """Dial the §15.4.3 platform MCP server, connector MCP servers,
        and CH-RUNTIMEOPS for the configured integration level, once per
        process.

        When a higher-level channel is configured but the manifest does
        not advertise it, the SDK logs the gap and degrades to the level
        the manifest supports, so a Standard- or Full-level binary still
        runs in a Basic-only environment.

        spec: §15.7 (Run dials the sockets once per process).
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
                    "runtime: the manifest advertises no CH-RUNTIMEOPS "
                    "socket; lifecycle features disabled",
                )
            else:
                self._lifecycle = Lifecycle.dial(
                    self._manifest,
                    self._opts.dial_timeout_s,
                    self._opts.lifecycle or LifecycleHooks(),
                    LifecycleHost(
                        held_session=self._held_session,
                        reload_credentials=self._reload_credentials,
                        log=lambda msg: _logf(self._opts, f"runtime: {msg}"),
                    ),
                )

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

    def _reload_credentials(
        self, rec: SessionRecord, path: str
    ) -> CredentialBundle | None:
        """Re-read the credential file a ``credentials_rotated`` event
        names for the session and return the bundle the session holds
        afterwards.

        The event names the file the adapter just rewrote, so a failed
        read is reported and the session keeps the bundle it holds. An
        event carrying no path breaks the frame's contract; the session
        keeps its bundle rather than reading a file the event did not
        name.

        spec: §28.5.3 (CH-RUNTIMEOPS, credentials_rotated), §4.7.11
        (item 4).
        """
        if not path:
            _logf(
                self._opts,
                f"runtime: credential rotation for session {rec.id}: event "
                "carries no credentialsPath; keeping the bundle already held",
            )
            return rec.get_credentials()
        try:
            creds = load_credential_bundle(path)
        except CredentialFileError as err:
            _logf(
                self._opts,
                f"runtime: credential rotation for session {rec.id}: {err}",
            )
            return rec.get_credentials()
        rec.set_credentials(creds)
        return creds

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

    def _open_transport(self) -> tuple[ByteSource, ByteSink]:
        """Resolve the §28.5.3 transport.

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
