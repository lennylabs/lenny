# SPDX-License-Identifier: MIT

"""§15.4.1 adapter-local tool surface.

An adapter-local tool call emits a stdout ``tool_call`` frame and blocks
until the matching ``tool_result`` frame arrives on stdin, correlating
by id. Unlike the platform MCP tools (which require Standard level),
adapter-local tools (read_file, write_file, list_dir, delete_file) are
resolved inside the adapter process with no MCP server.
"""

from __future__ import annotations

import secrets
import threading
from typing import Any

from .transport import FrameWriter
from .types import MessagePart, ToolResult


class _Pending:
    """In-flight bookkeeping for one ``tool_call``: an event that fires
    when the correlated ``tool_result`` arrives, plus the result slot
    and a rejection flag."""

    def __init__(self) -> None:
        self.event = threading.Event()
        self.result: ToolResult | None = None
        self.error: Exception | None = None


class ToolCallRegistry:
    """Correlates outbound §15.4.1 ``tool_call`` frames with inbound
    ``tool_result`` frames.

    The §15.4.1 frame loop delivers a ``tool_result`` here; an
    :class:`AdapterToolset` call registers and waits for one.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._pending: dict[str, _Pending] = {}

    def register(self, call_id: str) -> _Pending:
        """Record a pending ``tool_call`` id and return its
        bookkeeping."""
        pending = _Pending()
        with self._lock:
            self._pending[call_id] = pending
        return pending

    def deliver(self, raw: dict[str, Any]) -> bool:
        """Route an inbound ``tool_result`` to the call that emitted the
        matching id. Reports whether a pending call was found."""
        call_id = str(raw.get("id", ""))
        with self._lock:
            pending = self._pending.pop(call_id, None)
        if pending is None:
            return False
        pending.result = ToolResult(
            content=[MessagePart.from_wire(p) for p in raw.get("content", [])],
            is_error=bool(raw.get("isError", False)),
        )
        pending.event.set()
        return True

    def cancel(self, call_id: str, err: Exception) -> None:
        """Drop a pending ``tool_call`` registration and fail its
        waiter."""
        with self._lock:
            pending = self._pending.pop(call_id, None)
        if pending is not None:
            pending.error = err
            pending.event.set()

    def reject_all(self, err: Exception) -> None:
        """Fail every pending call.

        The §15.4.1 frame loop calls it when the inbound stream closes
        so no caller waits forever.
        """
        with self._lock:
            pending_items = list(self._pending.items())
            self._pending.clear()
        for _, pending in pending_items:
            pending.error = err
            pending.event.set()


def _new_call_id() -> str:
    """Generate a unique §15.4.1 ``tool_call`` id with the recommended
    ``tc_`` prefix."""
    return "tc_" + secrets.token_hex(8)


def _tool_error(result: ToolResult, tool: str) -> None:
    """Raise when an error-flagged tool result is present.

    The adapter sets ``content[0].inline`` to the failure string (for
    example ``path_outside_workspace``).
    """
    if not result.is_error:
        return
    msg = "tool reported an error"
    if result.content and result.content[0].inline:
        msg = result.content[0].inline
    raise RuntimeError(f"{tool}: {msg}")


class AdapterToolset:
    """§15.4.1 adapter-local tool surface available at every integration
    level.

    It emits a stdout ``tool_call`` frame and blocks until the matching
    ``tool_result`` frame arrives on stdin.
    """

    def __init__(
        self,
        writer: FrameWriter,
        registry: ToolCallRegistry,
        timeout_s: float,
        slot_id: str | None,
    ) -> None:
        self._writer = writer
        self._registry = registry
        self._timeout_s = timeout_s if timeout_s > 0 else 30.0
        self._slot_id = slot_id

    def tool_call(self, name: str, arguments: dict[str, Any]) -> ToolResult:
        """Emit a §15.4.1 ``tool_call`` frame for the named
        adapter-local tool and block until the correlated
        ``tool_result`` arrives.

        The id is generated and unique within the process.
        """
        call_id = _new_call_id()
        pending = self._registry.register(call_id)
        frame: dict[str, Any] = {
            "type": "tool_call",
            "id": call_id,
            "name": name,
            "arguments": arguments,
        }
        if self._slot_id:
            frame["slotId"] = self._slot_id
        try:
            self._writer.write(frame)
        except Exception as err:
            self._registry.cancel(call_id, err)
            raise RuntimeError(f"write tool_call {name!r}: {err}") from err

        if not pending.event.wait(self._timeout_s):
            self._registry.cancel(
                call_id, TimeoutError(f"tool_call {name!r} timed out")
            )
            raise TimeoutError(
                f"tool_call {name!r} timed out after {self._timeout_s}s"
            )
        if pending.error is not None:
            raise pending.error
        assert pending.result is not None
        return pending.result

    def read_file(self, path: str) -> str:
        """Invoke the §15.4.1 ``read_file`` adapter-local tool.

        The path is confined to the pod workspace by the adapter; a path
        resolving outside /workspace returns an error result.
        """
        result = self.tool_call("read_file", {"path": path})
        _tool_error(result, "read_file")
        if not result.content:
            return ""
        return result.content[0].inline or ""

    def write_file(self, path: str, content: str) -> None:
        """Invoke the §15.4.1 ``write_file`` adapter-local tool, creating
        or overwriting a workspace file with UTF-8 content."""
        result = self.tool_call("write_file", {"path": path, "content": content})
        _tool_error(result, "write_file")

    def list_dir(self, path: str) -> list[MessagePart]:
        """Invoke the §15.4.1 ``list_dir`` adapter-local tool and return
        the directory entries the adapter reports."""
        result = self.tool_call("list_dir", {"path": path})
        _tool_error(result, "list_dir")
        return result.content

    def delete_file(self, path: str) -> None:
        """Invoke the §15.4.1 ``delete_file`` adapter-local tool."""
        result = self.tool_call("delete_file", {"path": path})
        _tool_error(result, "delete_file")
