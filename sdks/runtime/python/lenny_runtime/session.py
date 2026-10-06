# SPDX-License-Identifier: MIT

"""Per-session state of one runtime process.

One runtime process serves every session the pod holds, one after another
on a recycling pool and side by side on a concurrent pool. The adapter
opens each session with ``session_start`` and ends it with
``session_end`` on CH-MSGSOCK, and the SDK keeps one
:class:`SessionRecord` per ``session_start`` it acts on. A record is keyed
by the session's ``sessionId`` in the routing table while the session is
live, and by the ``startId`` of the ``session_start`` that created it from
its ``session_end`` until its context is released. Each record has its own
worker thread and its own message queue, so one session's handler never
holds up another session or the frame loop.

spec: §4.7.10 (runtime process lifetime), §28.5.3 (CH-MSGSOCK, Inbound:
session_start, Inbound: session_end, Outbound: session_started, Session
errors), §15.7 (Handler).
"""

from __future__ import annotations

import json
import queue
import threading
from typing import Any

from .types import (
    CredentialBundle,
    ExperimentContext,
    LLMConfig,
    TerminationReason,
)

# CLOSE is the queue item that ends a session's worker loop. Items queued
# before it are dispatched unless the session's session_end was read.
CLOSE = object()


class CredentialFileError(Exception):
    """A credential file a frame named could not be read or parsed."""


class SessionStart:
    """Decoded ``session_start`` frame.

    spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table).
    """

    def __init__(self, raw: dict[str, Any]) -> None:
        self.session_id = str(raw.get("sessionId", "") or "")
        self.start_id = str(raw.get("startId", "") or "")
        self.credentials_path = str(raw.get("credentialsPath", "") or "")
        self.experiment_context = ExperimentContext.from_wire(
            raw.get("experimentContext")
        )
        tracing = raw.get("tracingContext")
        self.tracing_context: dict[str, str] | None = (
            {str(k): str(v) for k, v in tracing.items()}
            if isinstance(tracing, dict)
            else None
        )
        self.llm = LLMConfig.from_wire(raw.get("llm"))


class SessionRecord:
    """One session's context on this runtime process.

    The frame loop, the session's worker thread, and the CH-RUNTIMEOPS
    thread all reach a record. ``lock`` guards the creation and end
    bookkeeping; ``credentials`` is replaced whole under ``lock`` on a
    rotation; ``sequence`` is touched only by the worker thread; and
    ``ended`` is the stdout drop mark, which the frame writer sets and
    reads under its own lock.
    """

    def __init__(self, start: SessionStart, prev: SessionRecord | None) -> None:
        self.id = start.session_id
        self.start_id = start.start_id
        self.start = start
        self.queue: queue.Queue[Any] = queue.Queue()
        self.sequence = 0
        # prev is the record an earlier session_end removed for the same
        # sessionId while its context was still held. This record's
        # on_create runs only after prev's on_terminate returns, so a later
        # start never overlaps the release of an earlier one.
        self.prev = prev
        # released is set once the record's context is released.
        self.released = threading.Event()
        self.lock = threading.Lock()
        self.credentials: CredentialBundle | None = None
        # create_done is set once the creation finished, after the
        # record's own session_started was written.
        self.create_done = False
        # create_error is the credential-read or on_create failure, if
        # any. It is final once set, before session_started is written.
        self.create_error: Exception | None = None
        # end_read is set when the frame loop reads the session's
        # session_end.
        self.end_read = False
        # close_reason is the reason EOF or shutdown closed the session
        # with.
        self.close_reason: TerminationReason | None = None
        # pending_acks holds the startIds of duplicate session_start frames
        # read while the creation was still running.
        self.pending_acks: list[str] = []
        # ended is the stdout drop mark (see FrameWriter.write_for).
        self.ended = False

    def failed(self) -> bool:
        """Report whether the record's context creation failed."""
        with self.lock:
            return self.create_error is not None

    def ending(self) -> bool:
        """Report whether the frame loop read the session's
        ``session_end``."""
        with self.lock:
            return self.end_read

    def get_credentials(self) -> CredentialBundle | None:
        """Return the session's current credential bundle."""
        with self.lock:
            return self.credentials

    def set_credentials(self, creds: CredentialBundle | None) -> None:
        """Replace the session's credential bundle."""
        with self.lock:
            self.credentials = creds

    def termination_reason(self) -> TerminationReason:
        """Return the reason :meth:`Handler.on_terminate` receives."""
        with self.lock:
            if self.end_read:
                return TerminationReason(reason="session_end")
            if self.close_reason is not None:
                return self.close_reason
            return TerminationReason(reason="stdin_closed")


class SessionTable:
    """Routing table from ``sessionId`` to the live record, plus the
    bookkeeping for records a ``session_end`` removed whose context is not
    yet released. One lock guards every index."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        # _live maps a sessionId to the record of the latest session_start
        # that created one for it.
        self._live: dict[str, SessionRecord] = {}
        # _tail maps a sessionId to its most recent unreleased record,
        # live or removed, which a new record for the same sessionId waits
        # on.
        self._tail: dict[str, SessionRecord] = {}
        # _removed maps a startId to a record a session_end removed from
        # _live until the record's context is released.
        self._removed: dict[str, SessionRecord] = {}
        # _ended holds each sessionId whose latest start a session_end
        # ended and that no later session_start reopened. It outlives the
        # release, so a message the adapter had in flight when it wrote the
        # session_end is dropped rather than answered with a RUNTIME_ERROR
        # response a later start of the same session could read as its
        # own. The number of entries is bounded by the pool's per-pod
        # session limit.
        self._ended: set[str] = set()

    def lookup(self, session_id: str) -> SessionRecord | None:
        """Return the live record for ``session_id``, or None."""
        with self._lock:
            return self._live.get(session_id)

    def ended_since(self, session_id: str) -> bool:
        """Report whether a ``session_end`` ended ``session_id``'s latest
        start and no ``session_start`` has reopened the session since."""
        with self._lock:
            return session_id in self._ended

    def open(self, start: SessionStart) -> tuple[SessionRecord, bool]:
        """Install a record for ``start`` unless the session is already
        held. Returns the record and whether it was already held, which
        makes the frame a duplicate under the session_start rule 3."""
        with self._lock:
            cur = self._live.get(start.session_id)
            if cur is not None:
                return cur, True
            rec = SessionRecord(start, self._tail.get(start.session_id))
            self._live[rec.id] = rec
            self._tail[rec.id] = rec
            self._ended.discard(rec.id)
            return rec, False

    def remove(self, session_id: str) -> SessionRecord | None:
        """Take the live record for ``session_id`` out of the routing
        table and track it under its startId until its release. Returns
        None when the session is not held."""
        with self._lock:
            rec = self._live.pop(session_id, None)
            if rec is None:
                return None
            self._removed[rec.start_id] = rec
            self._ended.add(session_id)
            return rec

    def forget(self, rec: SessionRecord) -> None:
        """Drop a released record from every index that still names
        it."""
        with self._lock:
            if self._live.get(rec.id) is rec:
                del self._live[rec.id]
            if self._tail.get(rec.id) is rec:
                del self._tail[rec.id]
            if self._removed.get(rec.start_id) is rec:
                del self._removed[rec.start_id]

    def live_records(self) -> list[SessionRecord]:
        """Return a snapshot of the live records."""
        with self._lock:
            return list(self._live.values())


def load_credential_bundle(path: str) -> CredentialBundle | None:
    """Read and parse the credential file at ``path``.

    An empty path loads no bundle: the adapter names the file only when
    it provisioned one. A named file that cannot be read or parsed raises
    :class:`CredentialFileError`, which fails the session's creation.

    spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start credentialsPath),
    §4.7.11 (item 4).
    """
    if not path:
        return None
    try:
        with open(path, encoding="utf-8") as fh:
            raw = json.load(fh)
    except OSError as err:
        raise CredentialFileError(f"read credential file {path}: {err}") from err
    except json.JSONDecodeError as err:
        raise CredentialFileError(
            f"malformed credential file {path}: {err}"
        ) from err
    if not isinstance(raw, dict):
        raise CredentialFileError(
            f"malformed credential file {path}: not a JSON object"
        )
    return CredentialBundle.from_wire(raw)


def session_error_response(session_id: str, message: str) -> dict[str, Any]:
    """Build the RUNTIME_ERROR response the SDK writes for a message it
    cannot dispatch to a session.

    spec: §28.5.3 (CH-MSGSOCK, Session errors).
    """
    return {
        "type": "response",
        "output": [],
        "error": {"code": "RUNTIME_ERROR", "message": message},
        "sessionId": session_id,
    }

