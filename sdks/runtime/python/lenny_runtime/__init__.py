# SPDX-License-Identifier: MIT

"""Lenny Python runtime-author SDK.

This package lets a developer write a Lenny agent runtime in Python by
implementing the :class:`Handler` protocol and calling :func:`run`; the
SDK drives the §15.4.1 adapter binary protocol, the §15.4.2 RPC
lifecycle state machine, the §8.5 platform MCP tool helpers, and the
Full-level lifecycle channel.

This SDK is the runtime-author counterpart of the client SDK at
``sdks/client/python``. The client SDK wraps the gateway REST API for
application developers; this SDK targets the agent process that runs
inside a Lenny-managed pod and speaks the Runtime Adapter Specification
(§15.4).

Integration levels
-------------------

The SDK covers the §15.4.3 integration levels:

* Basic: the stdin/stdout JSON Lines protocol. :func:`run` with the
  default :class:`RunOptions` exercises Basic level fully.
* Standard: the SDK additionally dials the manifest-advertised platform
  MCP server and connector MCP servers with the §15.4.3 manifest-nonce
  handshake, and exposes typed §8.5 platform tool helpers through the
  ``tools`` argument of :meth:`Handler.on_message`.
* Full: the SDK additionally opens the §15.4.3 lifecycle channel,
  completes the ``lifecycle_capabilities`` / ``lifecycle_support``
  handshake, and answers checkpoint, interrupt, credential rotation, and
  deadline events.

Minimal runtime
---------------

A Basic-level echo runtime is a :class:`Handler` whose
:meth:`~Handler.on_message` echoes the inbound parts::

    from lenny_runtime import run, Message, HandlerTools, Reply

    class Echo:
        def on_create(self, req):
            pass

        def on_message(self, msg: Message, tools: HandlerTools) -> Reply:
            return Reply(parts=msg.envelope.input, final=True)

        def on_terminate(self, reason):
            pass

    run(Echo())
"""

from .lifecycle import Lifecycle, LifecycleEvent, LifecycleHooks
from .mcp import McpClient, PlatformTools
from .runtime import Handler, HandlerTools, RunOptions, run
from .tool import AdapterToolset
from .types import (
    SCHEMA_VERSION,
    AdapterLocalTool,
    AdapterManifest,
    ConnectorServerRef,
    CreateRequest,
    CredentialBundle,
    MCPServerRef,
    Message,
    MessageEnvelope,
    MessageFrom,
    MessagePart,
    ProtocolError,
    Reply,
    ResponseError,
    SocketRef,
    TaskHandle,
    TaskOutput,
    TaskResult,
    TerminationReason,
    ToolResult,
    WorkspacePlan,
    text,
    text_reply,
)

__all__ = [
    "run",
    "RunOptions",
    "Handler",
    "HandlerTools",
    "Lifecycle",
    "LifecycleEvent",
    "LifecycleHooks",
    "McpClient",
    "PlatformTools",
    "AdapterToolset",
    "SCHEMA_VERSION",
    "AdapterLocalTool",
    "AdapterManifest",
    "ConnectorServerRef",
    "CreateRequest",
    "CredentialBundle",
    "MCPServerRef",
    "Message",
    "MessageEnvelope",
    "MessageFrom",
    "MessagePart",
    "ProtocolError",
    "Reply",
    "ResponseError",
    "SocketRef",
    "TaskHandle",
    "TaskOutput",
    "TaskResult",
    "TerminationReason",
    "ToolResult",
    "WorkspacePlan",
    "text",
    "text_reply",
]
