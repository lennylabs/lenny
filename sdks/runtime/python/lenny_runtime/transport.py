# SPDX-License-Identifier: MIT

"""§28.5.3 byte-transport plumbing.

This module holds the line reader over a binary stream, the frame
writer that flushes after every write, and a Unix-socket dial helper
with a startup-race retry. The framing is identical under both §4.7
deployment models; only the byte transport differs.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import socket
import threading
import time
from typing import Any, BinaryIO, Protocol

# MAX_FRAME_BYTES caps an inbound JSON Lines frame at the §28.5.3
# MessagePart hard limit. A larger frame is a protocol error.
MAX_FRAME_BYTES = 50 * 1024 * 1024


class ByteSource(Protocol):
    """Read side of a §28.5.3 byte transport.

    ``readsome`` returns whatever bytes are available, blocking only
    until at least one byte arrives, and an empty result at end of
    stream. This is the semantics the line reader needs: a fixed-size
    ``read`` on a socket file object blocks for the full count, which
    would stall the frame loop while a peer keeps the connection open.
    """

    def readsome(self, size: int) -> bytes:
        """Return up to ``size`` bytes, blocking until at least one is
        available; an empty result signals end of stream."""
        ...


class ByteSink(Protocol):
    """Write side of a §28.5.3 byte transport."""

    def write(self, data: bytes) -> int:
        """Write ``data`` and return the count written."""
        ...

    def flush(self) -> None:
        """Flush any buffered bytes to the underlying transport."""
        ...


class StdioSource:
    """Wraps a stdin-style :class:`~typing.BinaryIO` as a
    :class:`ByteSource`.

    A buffered reader exposes ``read1``, which returns the first
    available chunk; that is the non-blocking-for-full-count read the
    line reader requires.
    """

    def __init__(self, stream: BinaryIO) -> None:
        self._stream = stream

    def readsome(self, size: int) -> bytes:
        read1 = getattr(self._stream, "read1", None)
        if callable(read1):
            chunk = read1(size)
        else:
            # A raw or unbuffered stream returns available bytes from
            # read.
            chunk = self._stream.read(size)
        return bytes(chunk)


class SocketStream:
    """Adapts a connected socket to the §28.5.3 byte-transport surface.

    The Unix-socket transport for the §4.7 sidecar deployment model and
    the §15.4.3 intra-pod channels are byte streams; this wrapper lets
    the same readline and frame-write code run over either. Reads use
    ``socket.recv`` so a partial frame is delivered as soon as it
    arrives.
    """

    def __init__(self, sock: socket.socket) -> None:
        self._sock = sock

    def readsome(self, size: int) -> bytes:
        return self._sock.recv(size)

    def write(self, data: bytes) -> int:
        self._sock.sendall(data)
        return len(data)

    def flush(self) -> None:
        # sendall has already pushed the bytes to the kernel; there is
        # no userspace buffer to drain.
        pass

    def close(self) -> None:
        self._sock.close()


class FrameWriter:
    """Serializes §28.5.3 outbound frames.

    Every write is a single JSON object followed by a newline and an
    explicit flush, honoring the §28.5.3 stdout-flushing requirement. A
    lock serializes writes so two threads cannot interleave a frame.
    """

    def __init__(self, stream: ByteSink) -> None:
        self._stream = stream
        self._lock = threading.Lock()

    def write(self, frame: Any) -> None:
        """Serialize one frame and flush it."""
        line = _encode(frame)
        with self._lock:
            self._stream.write(line)
            self._stream.flush()

    def write_for(self, owner: Any, frame: Any) -> bool:
        """Write a frame addressed to ``owner``, or drop it when the
        owner's ``ended`` mark is set.

        The check and the write happen under one lock, so no frame for
        a session follows the frame loop's read of its ``session_end``.
        It reports whether the frame was written.

        spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2).
        """
        line = _encode(frame)
        with self._lock:
            if getattr(owner, "ended", False):
                return False
            self._stream.write(line)
            self._stream.flush()
            return True

    def end_owner(self, owner: Any) -> None:
        """Set ``owner``'s ``ended`` mark under the writer's lock, so a
        :meth:`write_for` racing the mark either completes before it or
        drops its frame."""
        with self._lock:
            owner.ended = True


def _encode(frame: Any) -> bytes:
    """Serialize one frame as a JSON line."""
    return (json.dumps(frame, separators=(",", ":")) + "\n").encode("utf-8")


class LineReader:
    """Reads newline-delimited frames from a :class:`ByteSource`.

    It buffers partial reads and yields one frame per newline, dropping
    the trailing newline. The §28.5.3 frame loop consumes it.
    """

    def __init__(self, source: ByteSource) -> None:
        self._source = source
        self._buf = bytearray()
        self._eof = False

    def next(self) -> str | None:
        """Return the next frame, or None at end of stream.

        Raises :class:`ValueError` when a frame exceeds the §28.5.3
        size limit.
        """
        while True:
            idx = self._buf.find(b"\n")
            if idx >= 0:
                line = bytes(self._buf[:idx])
                del self._buf[: idx + 1]
                return line.decode("utf-8")
            if self._eof:
                if self._buf:
                    # A trailing line without a newline is a complete
                    # final frame.
                    line = bytes(self._buf)
                    self._buf.clear()
                    return line.decode("utf-8")
                return None
            chunk = self._source.readsome(65536)
            if not chunk:
                self._eof = True
                continue
            self._buf.extend(chunk)
            if len(self._buf) > MAX_FRAME_BYTES:
                raise ValueError(
                    f"inbound frame exceeds the {MAX_FRAME_BYTES}-byte limit"
                )


def dial_unix_socket(name: str, timeout_s: float) -> SocketStream:
    """Dial a Unix socket.

    A name beginning with ``@`` is a Linux abstract address; the ``@``
    is translated to a leading NUL. A filesystem path is dialed as-is so
    the helper also works off Linux. The dial is retried within
    ``timeout_s`` to absorb a startup race with the listener.
    """
    address = ("\x00" + name[1:]) if name.startswith("@") else name
    deadline = time.monotonic() + timeout_s
    last_err: Exception = OSError(f"dial {name}: not attempted")
    while True:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            sock.connect(address)
            return SocketStream(sock)
        except OSError as err:
            sock.close()
            last_err = err
            if time.monotonic() >= deadline:
                raise last_err
            time.sleep(0.1)


# HANDSHAKE_POLL_S is the pause between two reads of a manifest that is not
# yet published, and before each redial after the adapter closes a
# connection before its first protocol frame. The runtime keeps polling with
# no overall deadline, so the interval stays short.
HANDSHAKE_POLL_S = 0.1


def challenge_of(line: bytes | str) -> str | None:
    """Return the challenge a handshake line carries.

    It returns None for any line that is not a JSON object with a string
    ``_lennyChallenge`` member, which includes every protocol frame.
    """
    try:
        obj = json.loads(line)
    except (ValueError, UnicodeDecodeError):
        return None
    if not isinstance(obj, dict):
        return None
    value = obj.get("_lennyChallenge")
    return value if isinstance(value, str) else None


def challenge_response_line(nonce: str, challenge: str) -> bytes:
    """Return the line that answers a nonce-only challenge.

    The answer is HMAC-SHA256 keyed by the manifest nonce over the
    challenge, lowercase hex. The CH-MSGSOCK and CH-RUNTIMEOPS listeners
    and the intra-pod MCP servers issue the same challenge, so every client
    answers it with this function.

    spec: §4.7.11 (Nonce-only fallback).
    """
    mac = hmac.new(
        nonce.encode("utf-8"), challenge.encode("utf-8"), hashlib.sha256
    ).hexdigest()
    return _encode({"_lennyChallengeResponse": mac})


def read_manifest_nonce(path: str) -> str:
    """Return the ``mcpNonce`` of the manifest at ``path``, or the empty
    string when the file is absent, unreadable, or carries none."""
    try:
        with open(path, encoding="utf-8") as fh:
            raw = json.load(fh)
    except (OSError, ValueError):
        return ""
    nonce = raw.get("mcpNonce") if isinstance(raw, dict) else None
    return nonce if isinstance(nonce, str) else ""


class AuthenticatedStream:
    """A runtime-side CH-MSGSOCK or CH-RUNTIMEOPS connection that performs
    the runtime half of the runtime connection handshake.

    Its first write on each underlying socket is the nonce line read from
    the manifest just before the dial. Until the adapter's first protocol
    frame arrives, :meth:`readsome` answers each ``_lennyChallenge`` with
    the HMAC of the nonce that socket presented, and when the adapter
    closes the socket, which it does for a nonce the manifest has since
    replaced, it reads the manifest again, redials, and writes the new
    nonce line, with no overall deadline. The first protocol frame and
    everything after it pass through unchanged. The adapter writes the
    first protocol frame on both channels, so a runtime writes nothing
    before it has read it. :meth:`close` ends the connection and any
    redial.

    spec: §4.7.11 (Runtime connection handshake), §4.7.6 (mcpNonce row).
    """

    def __init__(self, name: str, manifest_path: str, timeout_s: float) -> None:
        self._name = name
        self._manifest_path = manifest_path
        self._timeout_s = timeout_s
        self._lock = threading.Lock()
        self._closed = False
        self._established = False
        self._pending = b""
        self._stream: SocketStream | None = None
        self._nonce = ""
        self._dial()

    def _dial(self) -> None:
        """Read the published nonce, dial, and write the nonce line."""
        while True:
            nonce = read_manifest_nonce(self._manifest_path)
            while not nonce:
                if self._closed:
                    raise OSError("runtime connection closed")
                time.sleep(HANDSHAKE_POLL_S)
                nonce = read_manifest_nonce(self._manifest_path)
            stream = dial_unix_socket(self._name, self._timeout_s)
            try:
                stream.write(_encode({"_lennyNonce": nonce}))
            except OSError:
                stream.close()
                time.sleep(HANDSHAKE_POLL_S)
                continue
            with self._lock:
                if self._closed:
                    stream.close()
                    raise OSError("runtime connection closed")
                self._stream, self._nonce = stream, nonce
            return

    def _current(self) -> SocketStream:
        with self._lock:
            assert self._stream is not None
            return self._stream

    def _await_first_frame(self) -> bool:
        """Read handshake lines until the first protocol frame, answering
        each challenge and redialing whenever the adapter closes the
        connection first. It returns False once :meth:`close` has run."""
        buf = bytearray()
        while True:
            idx = buf.find(b"\n")
            if idx >= 0:
                line = bytes(buf[: idx + 1])
                challenge = challenge_of(line)
                if challenge is None:
                    self._established = True
                    self._pending = bytes(buf)
                    return True
                try:
                    self._current().write(
                        challenge_response_line(self._nonce, challenge)
                    )
                except OSError:
                    # The next read reports the close and redials.
                    pass
                del buf[: idx + 1]
                continue
            stream = self._current()
            try:
                chunk = stream.readsome(65536)
            except OSError:
                chunk = b""
            if chunk:
                buf.extend(chunk)
                continue
            if self._closed:
                return False
            stream.close()
            time.sleep(HANDSHAKE_POLL_S)
            try:
                self._dial()
            except OSError:
                if self._closed:
                    return False
                raise
            buf = bytearray()

    def readsome(self, size: int) -> bytes:
        if not self._established and not self._await_first_frame():
            return b""
        if self._pending:
            chunk, self._pending = self._pending[:size], self._pending[size:]
            return chunk
        return self._current().readsome(size)

    def write(self, data: bytes) -> int:
        return self._current().write(data)

    def flush(self) -> None:
        pass

    def close(self) -> None:
        with self._lock:
            self._closed = True
            stream = self._stream
        if stream is not None:
            stream.close()


def dial_authenticated(
    name: str, manifest_path: str, timeout_s: float
) -> AuthenticatedStream:
    """Dial the adapter's CH-MSGSOCK or CH-RUNTIMEOPS socket and return an
    :class:`AuthenticatedStream` over it.

    The caller resolves ``manifest_path`` once and passes it.
    ``timeout_s`` bounds each socket connect.

    spec: §4.7.11 (Runtime connection handshake).
    """
    return AuthenticatedStream(name, manifest_path, timeout_s)
