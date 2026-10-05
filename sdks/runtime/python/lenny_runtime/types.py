# SPDX-License-Identifier: MIT

"""Wire and convenience types the runtime-author SDK surfaces.

The wire-level dataclasses (:class:`MessagePart`, :class:`MessageEnvelope`)
mirror the §28.5.3 adapter binary protocol. The convenience dataclasses
(:class:`CreateRequest`, :class:`Message`, :class:`Reply`,
:class:`CredentialBundle`, :class:`AdapterManifest`,
:class:`WorkspacePlan`) are §15.7 wrappers the SDK materializes from each
session's ``session_start`` frame, the credential file that frame names,
the pod-scoped manifest, and the stdin framing before invoking
:class:`Handler` methods. They introduce no new wire types.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

# SCHEMA_VERSION is the current MessagePart and MessageEnvelope schema
# revision (§28.5.3). Producers stamp it on every emitted MessagePart.
SCHEMA_VERSION = 1


@dataclass
class MessagePart:
    """§28.5.3 internal content model.

    A part either carries bytes inline or references blob storage;
    ``inline`` and ``ref`` are mutually exclusive. Basic-level runtimes
    need only set ``type`` and ``inline``; the SDK stamps
    ``schema_version`` when it is unset.
    """

    type: str
    # schema_version identifies the MessagePart schema revision. Defaults
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
    parts: list[MessagePart] | None = None
    # status is one of streaming, complete, or failed.
    status: str | None = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> MessagePart:
        """Build a MessagePart from a §28.5.3 wire object."""
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
        """Serialize the part to its §28.5.3 wire form.

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


def text(s: str) -> MessagePart:
    """Build a minimal text MessagePart with ``schema_version`` set."""
    return MessagePart(type="text", inline=s)


@dataclass
class MessageFrom:
    """§15.4 ``from`` object.

    ``kind`` is one of client, agent, system, or external. The adapter
    injects both fields; runtimes never supply them.
    """

    kind: str
    id: str


@dataclass
class MessageEnvelope:
    """§15.4 unified inbound message format.

    The adapter populates ``from_``, and the gateway populates
    ``schema_version`` and ``id`` when omitted. Basic-level handlers
    typically read only ``input``.

    ``annotations`` carries the §15.5 degradation-annotation
    catalog (``schema_version_ahead``, ``durable_schema_version_ahead``,
    ``mcp_protocol_version_retired``). Producers stamp them when forward-
    read or retirement defects occur; the field is open metadata so new
    annotations can land without a schema-version bump. F-15.5.5.
    """

    type: str
    id: str
    schema_version: int = SCHEMA_VERSION
    from_: MessageFrom | None = None
    in_reply_to: str | None = None
    thread_id: str | None = None
    delivery: str | None = None
    delegation_depth: int = 0
    session_id: str | None = None
    input: list[MessagePart] = field(default_factory=list)
    annotations: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> MessageEnvelope:
        """Build a MessageEnvelope from a §15.4 message frame."""
        sender = raw.get("from")
        annotations_raw = raw.get("annotations")
        annotations: dict[str, Any] = (
            dict(annotations_raw) if isinstance(annotations_raw, dict) else {}
        )
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
            session_id=raw.get("sessionId"),
            input=[MessagePart.from_wire(p) for p in raw.get("input", [])],
            annotations=annotations,
        )


@dataclass
class ResponseError:
    """Optional §28.5.3 ``response.error`` object and §8.8
    ``TaskResult.error`` object. Both carry a code and a message."""

    code: str
    message: str = ""

    def to_wire(self) -> dict[str, Any]:
        """Serialize the error to its §28.5.3 wire form."""
        return {"code": self.code, "message": self.message}


@dataclass
class ProviderCredential:
    """One entry of a :class:`CredentialBundle`'s ``providers`` list.

    ``materialized_config`` is left as the decoded JSON object because
    its fields depend on the provider and the delivery mode: a proxy
    entry carries ``proxyUrl`` and ``leaseToken``, and a direct entry
    carries the provider's own credential fields.

    spec: §4.7.11 (item 4, runtime credential file contract), §4.9
    (materializedConfig schema by provider).
    """

    # lease_id identifies the §4.9 credential lease.
    lease_id: str = ""
    # provider is the credential provider identifier.
    provider: str = ""
    # expires_at is the ISO 8601 lease expiry timestamp.
    expires_at: str | None = None
    # delivery_mode is direct or proxy.
    delivery_mode: str = ""
    # materialized_config is the entry's materializedConfig object.
    materialized_config: dict[str, Any] | None = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> ProviderCredential:
        """Build a ProviderCredential from one providers entry."""
        config = raw.get("materializedConfig")
        return cls(
            lease_id=str(raw.get("leaseId", "")),
            provider=str(raw.get("provider", "")),
            expires_at=raw.get("expiresAt"),
            delivery_mode=str(raw.get("deliveryMode", "")),
            materialized_config=dict(config) if isinstance(config, dict) else None,
        )


@dataclass
class CredentialBundle:
    """Parsed runtime credential file that a session's ``session_start``
    names in ``credentialsPath``, the session's own
    ``/run/lenny/slots/{sessionId}/credentials.json``.

    The file lists one entry per credential provider the session holds a
    lease for. The SDK reloads the session's bundle on a
    ``credentials_rotated`` lifecycle event naming that session.

    spec: §4.7.11 (item 4, runtime credential file contract), §28.5.3
    (CH-MSGSOCK, Inbound: session_start).
    """

    # providers holds one entry per leased credential provider.
    providers: list[ProviderCredential] = field(default_factory=list)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> CredentialBundle:
        """Build a CredentialBundle from the credential file."""
        entries = raw.get("providers")
        return cls(
            providers=[
                ProviderCredential.from_wire(e)
                for e in (entries if isinstance(entries, list) else [])
                if isinstance(e, dict)
            ],
        )


@dataclass
class ExperimentContext:
    """Experiment enrollment a session's ``session_start`` carries in
    ``experimentContext``.

    ``inherited`` is True when the enrollment was propagated from a
    parent session through delegation.

    spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table),
    §10.7.
    """

    experiment_id: str = ""
    variant_id: str = ""
    inherited: bool = False

    @classmethod
    def from_wire(cls, raw: Any) -> ExperimentContext | None:
        """Build an ExperimentContext, or None for an absent or null
        member."""
        if not isinstance(raw, dict):
            return None
        return cls(
            experiment_id=str(raw.get("experimentId", "")),
            variant_id=str(raw.get("variantId", "")),
            inherited=bool(raw.get("inherited", False)),
        )


@dataclass
class LLMConfig:
    """LLM provider configuration a session's ``session_start`` carries
    in ``llm``.

    ``delivery_mode`` is direct or proxy; ``dialect`` names the provider
    dialect a proxy-mode runtime speaks to the LLM Proxy; ``api_key_env``
    names the variable a runtime's LLM client reads its key from, which
    the runtime sets for this session only and never in its own process
    environment; ``headers`` lists the headers a proxy-mode runtime sends.

    spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table), §4.9.
    """

    delivery_mode: str = ""
    dialect: str | None = None
    api_key_env: str | None = None
    headers: dict[str, str] | None = None

    @classmethod
    def from_wire(cls, raw: Any) -> LLMConfig | None:
        """Build an LLMConfig, or None for an absent or null member."""
        if not isinstance(raw, dict):
            return None
        headers = raw.get("headers")
        return cls(
            delivery_mode=str(raw.get("deliveryMode", "")),
            dialect=raw.get("dialect"),
            api_key_env=raw.get("apiKeyEnv"),
            headers={str(k): str(v) for k, v in headers.items()}
            if isinstance(headers, dict)
            else None,
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
    """One §28.5.3 adapter-local tool entry: a name, a human-readable
    description, and a JSON Schema for its arguments."""

    name: str
    description: str = ""
    input_schema: dict[str, Any] | None = None


@dataclass
class AdapterManifest:
    """Parsed §4.7 adapter manifest at /run/lenny/adapter-manifest.json.

    It carries only pod-scoped fields: one runtime process serves every
    session the pod holds, so each session's own context arrives in that
    session's ``session_start`` frame rather than here. Unknown fields are
    ignored (§4.7 forward compatibility).

    spec: §4.7 (adapter manifest field reference), §4.7.10 (runtime
    process lifetime).
    """

    # version is the manifest schema version. Every increment is
    # breaking; the SDK rejects a version newer than it understands.
    version: int = 0
    # mcp_nonce is the §15.4.3 intra-pod MCP nonce (256-bit hex). The
    # SDK injects it as params._lennyNonce on every MCP initialize.
    mcp_nonce: str = ""
    # platform_mcp_server names the platform MCP server socket.
    platform_mcp_server: MCPServerRef | None = None
    # connector_servers names the per-connector MCP server sockets.
    connector_servers: list[ConnectorServerRef] = field(default_factory=list)
    # lifecycle_channel names the Full-level CH-RUNTIMEOPS socket.
    lifecycle_channel: SocketRef | None = None
    # adapter_local_tools enumerates the §28.5.3 adapter-local tools the
    # runtime may invoke via stdout tool_call frames.
    adapter_local_tools: list[AdapterLocalTool] = field(default_factory=list)
    # runtime_options is the effective caller options map.
    runtime_options: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> AdapterManifest:
        """Build an AdapterManifest from the §4.7 manifest file."""
        platform = raw.get("platformMcpServer")
        lifecycle = raw.get("runtimeOps")
        return cls(
            version=int(raw.get("version", 0)),
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

    The SDK populates it from the session's ``session_end``, from the
    §28.5.3 shutdown frame, or from the end of the connection.
    """

    # reason is session_end when the session's session_end ended it, the
    # shutdown frame's reason (drain, deadline, etc.) when a shutdown
    # ended the process, or stdin_closed when the adapter closed the
    # connection without a shutdown frame.
    reason: str
    # deadline_ms is the shutdown deadline in milliseconds when the
    # adapter supplied one; zero otherwise.
    deadline_ms: int = 0


@dataclass
class CreateRequest:
    """§15.7 snapshot of session context handed to
    :meth:`Handler.on_create` when the session's ``session_start``
    arrives and before the session's first :class:`Message`.

    The SDK assembles it from the ``session_start`` frame, the credential
    file that frame names, and the pod-scoped adapter manifest. Handler
    implementations MUST treat it as read-only.

    spec: §15.7 (SDK Handler types), §28.5.3 (CH-MSGSOCK, Inbound:
    session_start).
    """

    # session_id is the session this request opens, the session_start's
    # sessionId.
    session_id: str = ""
    # task_id is the session's external-protocol task identifier. Each
    # session has exactly one execution, so it equals session_id; the SDK
    # derives it from the session_start's sessionId.
    # spec: §15.7 (TaskID derived from sessionId), §7.2 (one execution per session)
    task_id: str = ""
    # runtime_options is the effective caller options map.
    runtime_options: dict[str, Any] = field(default_factory=dict)
    # workspace_plan references the §14 materialized workspace plan.
    workspace_plan: WorkspacePlan | None = None
    # credentials is the session's credential bundle, read from the file
    # the session_start's credentialsPath names. None when the frame
    # names no credentialsPath. The SDK reloads the session's bundle on a
    # credentials_rotated event rather than re-invoking on_create.
    credentials: CredentialBundle | None = None
    # experiment_context is the session_start's experimentContext; None
    # when the session is not enrolled in an experiment.
    experiment_context: ExperimentContext | None = None
    # tracing_context is the session_start's tracingContext; None for a
    # top-level session.
    tracing_context: dict[str, str] | None = None
    # llm is the session_start's llm; None when the session has no active
    # LLM credential lease.
    llm: LLMConfig | None = None
    # manifest_snapshot is the parsed pod-scoped adapter manifest.
    manifest_snapshot: AdapterManifest | None = None


@dataclass
class Message:
    """§15.7 per-turn envelope handed to :meth:`Handler.on_message` for
    every §28.5.3 message frame.

    Fields other than ``envelope`` are SDK-derived conveniences.
    """

    # envelope is the canonical §15.4 MessageEnvelope. All message
    # semantics live on this field.
    envelope: MessageEnvelope
    # session_id is the session the message was delivered to, the
    # frame's sessionId.
    session_id: str = ""
    # task_id is the external-protocol task identifier of the session the
    # message belongs to. It equals session_id and always equals the
    # session's CreateRequest.task_id.
    # spec: §15.7 (TaskID derived from sessionId), §7.2 (one execution per session)
    task_id: str = ""
    # sequence is a monotonic, SDK-assigned per-session counter ordering
    # the session's messages as the SDK observed them on stdin. It is
    # local to this process and suitable for logging only.
    sequence: int = 0


@dataclass
class Reply:
    """Value :meth:`Handler.on_message` returns.

    The SDK serializes it into the stdout §28.5.3 response frame:
    ``parts`` becomes ``output``.
    """

    # parts is the MessagePart array the runtime emits for this turn.
    # Empty is valid when output was already emitted via the
    # lenny/output platform MCP tool.
    parts: list[MessagePart] = field(default_factory=list)
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

    content: list[MessagePart] = field(default_factory=list)
    is_error: bool = False


@dataclass
class TaskHandle:
    """§8.2 ``lenny/delegate_task`` return value."""

    task_id: str


@dataclass
class TaskOutput:
    """Output object of a §8.8 TaskResult."""

    parts: list[MessagePart] = field(default_factory=list)


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
                    MessagePart.from_wire(p)
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
