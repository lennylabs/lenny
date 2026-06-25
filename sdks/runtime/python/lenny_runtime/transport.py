# SPDX-License-Identifier: MIT

"""§15.4.1 byte-transport plumbing.

This module holds the line reader over a binary stream, the frame
writer that flushes after every write, and a Unix-socket dial helper
with a startup-race retry. The framing is identical under both §4.7
deployment models; only the byte transport differs.
"""

from __future__ import annotations

import json
import socket
import threading
import time
from typing import Any, BinaryIO, Protocol

# MAX_FRAME_BYTES caps an inbound JSON Lines frame at the §15.4.1
# MessagePart hard limit. A larger frame is a protocol error.
MAX_FRAME_BYTES = 50 * 1024 * 1024


class ByteSource(Protocol):
    """Read side of a §15.4.1 byte transport.

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
    """Write side of a §15.4.1 byte transport."""

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
    """Adapts a connected socket to the §15.4.1 byte-transport surface.

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
    """Serializes §15.4.1 outbound frames.

    Every write is a single JSON object followed by a newline and an
    explicit flush, honoring the §15.4.1 stdout-flushing requirement. A
    lock serializes writes so two threads cannot interleave a frame.
    """

    def __init__(self, stream: ByteSink) -> None:
        self._stream = stream
        self._lock = threading.Lock()

    def write(self, frame: Any) -> None:
        """Serialize one frame and flush it."""
        line = (json.dumps(frame, separators=(",", ":")) + "\n").encode("utf-8")
        with self._lock:
            self._stream.write(line)
            self._stream.flush()


class LineReader:
    """Reads newline-delimited frames from a :class:`ByteSource`.

    It buffers partial reads and yields one frame per newline, dropping
    the trailing newline. The §15.4.1 frame loop consumes it.
    """

    def __init__(self, source: ByteSource) -> None:
        self._source = source
        self._buf = bytearray()
        self._eof = False

    def next(self) -> str | None:
        """Return the next frame, or None at end of stream.

        Raises :class:`ValueError` when a frame exceeds the §15.4.1
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
