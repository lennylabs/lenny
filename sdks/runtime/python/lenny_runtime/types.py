# SPDX-License-Identifier: MIT

"""Wire and convenience types the runtime-author SDK surfaces.

The wire-level dataclasses (:class:`OutputPart`, :class:`MessageEnvelope`)
mirror the §15.4.1 adapter binary protocol. The convenience dataclasses
(:class:`CreateRequest`, :class:`Message`, :class:`Reply`,
:class:`CredentialBundle`, :class:`AdapterManifest`,
:class:`WorkspacePlan`) are §15.7 wrappers the SDK materializes from the
manifest, the credential file, and the stdin framing before invoking
:class:`Handler` methods. They introduce no new wire types.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

# SCHEMA_VERSION is the current OutputPart and MessageEnvelope schema
# revision (§15.4.1). Producers stamp it on every emitted OutputPart.
SCHEMA_VERSION = 1


@dataclass
class OutputPart:
    """§15.4.1 internal content model.

    A part either carries bytes inline or references blob storage;
    ``inline`` and ``ref`` are mutually exclusive. Basic-level runtimes
    need only set ``type`` and ``inline``; the SDK stamps
    ``schema_version`` when it is unset.
    """

    type: str
    # schema_version identifies the OutputPart schema revision. Defaults
    # to 1. The SDK sets it to 1 on any emitted part that leaves it
    # unset.
    schema_version: int = SCHEMA_VERSION
    # id is a stable part identifier. The adapter generates one when a
    # runtime omits it.
    id: str | None = None
    # mime_type handles the encoding of the part content. Defaults to
    # text/plain for text parts.
    mime_type: str | None = None
    # inline carries the part content directly (base64 for binary).
    inline: str | None = None
    # ref references external blob storage via a lenny-blob:// URI.
    ref: str | None = None
    # annotations is an open metadata map (role, language, final, etc.).
    annotations: dict[str, Any] | None = None
    # parts holds nested parts for compound outputs (execution_result).
    parts: list[OutputPart] | None = None
    # status is one of streaming, complete, or failed.
    status: str | None = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> OutputPart:
        """Build an OutputPart from a §15.4.1 wire object."""
        nested = raw.get("parts")
        return cls(
            type=str(raw.get("type", "")),
            schema_version=int(raw.get("schemaVersion", SCHEMA_VERSION)),
            id=raw.get("id"),
            mime_type=raw.get("mimeType"),
            inline=raw.get("inline"),
            ref=raw.get("ref"),
            annotations=raw.get("annotations"),
            parts=[cls.from_wire(p) for p in nested] if nested else None,
            status=raw.get("status"),
        )

    def to_wire(self) -> dict[str, Any]:
        """Serialize the part to its §15.4.1 wire form.

        Fields left unset are omitted so the frame stays minimal. The
        SDK stamps ``schema_version`` before this is called.
        """
        out: dict[str, Any] = {
            "schemaVersion": self.schema_version,
            "type": self.type,
        }
        if self.id is not None:
            out["id"] = self.id
        if self.mime_type is not None:
            out["mimeType"] = self.mime_type
        if self.inline is not None:
            out["inline"] = self.inline
        if self.ref is not None:
            out["ref"] = self.ref
        if self.annotations is not None:
            out["annotations"] = self.annotations
        if self.parts is not None:
            out["parts"] = [p.to_wire() for p in self.parts]
        if self.status is not None:
            out["status"] = self.status
        return out


def text(s: str) -> OutputPart:
    """Build a minimal text OutputPart with ``schema_version`` set."""
    return OutputPart(type="text", inline=s)


@dataclass
class MessageFrom:
    """§15.4.1 ``from`` object.

    ``kind`` is one of client, agent, system, or external. The adapter
    injects both fields; runtimes never supply them.
    """

    kind: str
    id: str


@dataclass
class MessageEnvelope:
    """§15.4.1 unified inbound message format.

    The adapter populates ``from_``, and the gateway populates
    ``schema_version`` and ``id`` when omitted. Basic-level handlers
    typically read only ``input``.
    """

    type: str
    id: str
    schema_version: int = SCHEMA_VERSION
    from_: MessageFrom | None = None
    in_reply_to: str | None = None
    thread_id: str | None = None
    delivery: str | None = None
    delegation_depth: int = 0
    slot_id: str | None = None
    input: list[OutputPart] = field(default_factory=list)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> MessageEnvelope:
        """Build a MessageEnvelope from a §15.4.1 message frame."""
        sender = raw.get("from")
        return cls(
            type=str(raw.get("type", "")),
            id=str(raw.get("id", "")),
            schema_version=int(raw.get("schemaVersion", SCHEMA_VERSION)),
            from_=MessageFrom(
                kind=str(sender.get("kind", "")),
                id=str(sender.get("id", "")),
            )
            if isinstance(sender, dict)
            else None,
            in_reply_to=raw.get("inReplyTo"),
            thread_id=raw.get("threadId"),
            delivery=raw.get("delivery"),
            delegation_depth=int(raw.get("delegationDepth", 0)),
            slot_id=raw.get("slotId"),
            input=[OutputPart.from_wire(p) for p in raw.get("input", [])],
        )


@dataclass
class ResponseError:
    """Optional §15.4.1 ``response.error`` object and §8.8
    ``TaskResult.error`` object. Both carry a code and a message."""

    code: str
    message: str = ""

    def to_wire(self) -> dict[str, Any]:
        """Serialize the error to its §15.4.1 wire form."""
        return {"code": self.code, "message": self.message}


@dataclass
class CredentialBundle:
    """Parsed §4.7 runtime credential file at /run/lenny/credentials.json.

    The SDK refreshes it in place on a ``credentials_rotated`` lifecycle
    message. Fields are the union of proxy and direct delivery modes; an
    unset field is absent in the file.
    """

    # mode is proxy or direct (§4.7 manifest llm fields).
    mode: str | None = None
    # provider names the upstream LLM provider for this lease.
    provider: str | None = None
    # lease_id identifies the §4.9 credential lease.
    lease_id: str | None = None
    # api_key is the upstream key under direct delivery.
    api_key: str | None = None
    # api_key_env names the environment variable carrying the key under
    # proxy delivery.
    api_key_env: str | None = None
    # base_url is the upstream or proxy endpoint base URL.
    base_url: str | None = None
    # expires_at is the RFC 3339 lease expiry timestamp.
    expires_at: str | None = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> CredentialBundle:
        """Build a CredentialBundle from the §4.7 credential file."""
        return cls(
            mode=raw.get("mode"),
            provider=raw.get("provider"),
            lease_id=raw.get("leaseId"),
            api_key=raw.get("apiKey"),
            api_key_env=raw.get("apiKeyEnv"),
            base_url=raw.get("baseUrl"),
            expires_at=raw.get("expiresAt"),
        )


@dataclass
class MCPServerRef:
    """Names a platform MCP server socket in the manifest."""

    socket: str


@dataclass
class ConnectorServerRef:
    """Names one connector MCP server in the manifest."""

    id: str
    socket: str


@dataclass
class SocketRef:
    """Names a single Unix socket in the manifest."""

    socket: str


@dataclass
class AdapterLocalTool:
    """One §15.4.1 adapter-local tool entry: a name, a human-readable
    description, and a JSON Schema for its arguments."""

    name: str
    description: str = ""
    input_schema: dict[str, Any] | None = None


@dataclass
class AdapterManifest:
    """Parsed §4.7 adapter manifest at /run/lenny/adapter-manifest.json.

    Unknown fields are ignored (§4.7 forward compatibility).
    """

    # version is the manifest schema version. Every increment is
    # breaking; the SDK rejects a version newer than it understands.
    version: int = 0
    # session_id is the session this runtime instance is bound to.
    session_id: str = ""
    # task_id is the current task identifier.
    task_id: str = ""
    # mcp_nonce is the §15.4.3 intra-pod MCP nonce (256-bit hex). The
    # SDK injects it as params._lennyNonce on every MCP initialize.
    mcp_nonce: str = ""
    # platform_mcp_server names the platform MCP server socket.
    platform_mcp_server: MCPServerRef | None = None
    # connector_servers names the per-connector MCP server sockets.
    connector_servers: list[ConnectorServerRef] = field(default_factory=list)
    # lifecycle_channel names the Full-level lifecycle channel socket.
    lifecycle_channel: SocketRef | None = None
    # adapter_local_tools enumerates the §15.4.1 adapter-local tools the
    # runtime may invoke via stdout tool_call frames.
    adapter_local_tools: list[AdapterLocalTool] = field(default_factory=list)
    # runtime_options is the effective caller options map.
    runtime_options: dict[str, Any] = field(default_factory=dict)
    # tracing_context carries §16.3 tracing identifiers.
    tracing_context: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> AdapterManifest:
        """Build an AdapterManifest from the §4.7 manifest file."""
        platform = raw.get("platformMcpServer")
        lifecycle = raw.get("lifecycleChannel")
        return cls(
            version=int(raw.get("version", 0)),
            session_id=str(raw.get("sessionId", "")),
            task_id=str(raw.get("taskId", "")),
            mcp_nonce=str(raw.get("mcpNonce", "")),
            platform_mcp_server=MCPServerRef(socket=str(platform["socket"]))
            if isinstance(platform, dict) and platform.get("socket")
            else None,
            connector_servers=[
                ConnectorServerRef(id=str(c.get("id", "")), socket=str(c["socket"]))
                for c in raw.get("connectorServers", [])
                if isinstance(c, dict) and c.get("socket")
            ],
            lifecycle_channel=SocketRef(socket=str(lifecycle["socket"]))
            if isinstance(lifecycle, dict) and lifecycle.get("socket")
            else None,
            adapter_local_tools=[
                AdapterLocalTool(
                    name=str(t.get("name", "")),
                    description=str(t.get("description", "")),
                    input_schema=t.get("inputSchema"),
                )
                for t in raw.get("adapterLocalTools", [])
                if isinstance(t, dict)
            ],
            runtime_options=dict(raw.get("runtimeOptions", {})),
            tracing_context=dict(raw.get("tracingContext", {})),
        )


@dataclass
class WorkspacePlan:
    """Reference to the §14 materialized workspace plan.

    The SDK parses it from the manifest when present; runtimes consult
    it for source metadata rather than to drive materialization.
    """

    schema_version: int = SCHEMA_VERSION
    sources: list[Any] = field(default_factory=list)
    setup_commands: list[str] = field(default_factory=list)


@dataclass
class TerminationReason:
    """Reason passed to :meth:`Handler.on_terminate`.

    The SDK populates it from the §15.4.1 shutdown frame or the
    lifecycle-channel terminate event.
    """

    # reason is the adapter-supplied reason string (drain, deadline,
    # etc.) or stdin_closed when the adapter closed stdin without a
    # shutdown frame.
    reason: str
    # deadline_ms is the shutdown deadline in milliseconds when the
    # adapter supplied one; zero otherwise.
    deadline_ms: int = 0


@dataclass
class CreateRequest:
    """§15.7 snapshot of task-scoped context handed to
    :meth:`Handler.on_create` before the first :class:`Message`.

    Handler implementations MUST treat it as read-only.
    """

    # session_id is the session this runtime instance is bound to.
    session_id: str = ""
    # task_id is the current task identifier.
    task_id: str = ""
    # runtime_options is the effective caller options map.
    runtime_options: dict[str, Any] = field(default_factory=dict)
    # workspace_plan references the §14 materialized workspace plan.
    workspace_plan: WorkspacePlan | None = None
    # credentials is the current §4.7 credential bundle. The SDK
    # refreshes it in place on rotation rather than re-invoking
    # on_create. None when the runtime has no active lease.
    credentials: CredentialBundle | None = None
    # manifest_snapshot is the parsed adapter manifest.
    manifest_snapshot: AdapterManifest | None = None


@dataclass
class Message:
    """§15.7 per-turn envelope handed to :meth:`Handler.on_message` for
    every §15.4.1 message frame.

    Fields other than ``envelope`` are SDK-derived conveniences.
    """

    # envelope is the canonical §15.4.1 MessageEnvelope. All message
    # semantics live on this field.
    envelope: MessageEnvelope
    # session_id is the session the message was delivered to.
    session_id: str = ""
    # task_id is the active task the message belongs to.
    task_id: str = ""
    # sequence is a monotonic, SDK-assigned per-task counter ordering
    # messages as the SDK observed them on stdin. It is local to this
    # process and suitable for logging only.
    sequence: int = 0


@dataclass
class Reply:
    """Value :meth:`Handler.on_message` returns.

    The SDK serializes it into the stdout §15.4.1 response frame:
    ``parts`` becomes ``output``.
    """

    # parts is the OutputPart array the runtime emits for this turn.
    # Empty is valid when output was already emitted via the
    # lenny/output platform MCP tool.
    parts: list[OutputPart] = field(default_factory=list)
    # error reports a structured failure for this turn. When set, the
    # adapter maps the task to failed and populates TaskResult.error.
    error: ResponseError | None = None
    # streaming indicates more parts may still arrive out-of-band before
    # the turn is final.
    streaming: bool = False
    # final marks this Reply as the terminal response for the turn.
    # final MUST be True for Basic-level runtimes. The default Reply is
    # final.
    final: bool = True


def text_reply(s: str) -> Reply:
    """Build a final :class:`Reply` carrying a single text part."""
    return Reply(parts=[text(s)], final=True)


@dataclass
class ToolResult:
    """Decoded result of an adapter-local tool call.

    A failed call sets ``is_error``; ``content`` carries the result
    parts (for a failed call, ``content[0].inline`` is the error
    string).
    """

    content: list[OutputPart] = field(default_factory=list)
    is_error: bool = False


@dataclass
class TaskHandle:
    """§8.2 ``lenny/delegate_task`` return value."""

    task_id: str


@dataclass
class TaskOutput:
    """Output object of a §8.8 TaskResult."""

    parts: list[OutputPart] = field(default_factory=list)


@dataclass
class TaskResult:
    """§8.8 TaskResult returned by ``lenny/await_children``.

    Restricted to the fields the SDK decodes.
    """

    task_id: str
    state: str
    output: TaskOutput = field(default_factory=TaskOutput)
    schema_version: int = SCHEMA_VERSION
    error: ResponseError | None = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> TaskResult:
        """Build a TaskResult from a §8.8 wire object."""
        out = raw.get("output", {})
        err = raw.get("error")
        return cls(
            task_id=str(raw.get("taskId", "")),
            state=str(raw.get("state", "")),
            output=TaskOutput(
                parts=[
                    OutputPart.from_wire(p)
                    for p in (out.get("parts", []) if isinstance(out, dict) else [])
                ],
            ),
            schema_version=int(raw.get("schemaVersion", SCHEMA_VERSION)),
            error=ResponseError(
                code=str(err.get("code", "")),
                message=str(err.get("message", "")),
            )
            if isinstance(err, dict)
            else None,
        )


class ProtocolError(Exception):
    """Non-recoverable inbound-format violation.

    :func:`lenny_runtime.run` raises it; an entrypoint maps it to the
    §15.4 protocol-error exit code (2).
    """

    def __init__(self, message: str) -> None:
        super().__init__(f"protocol error: {message}")
