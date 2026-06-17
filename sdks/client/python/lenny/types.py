# SPDX-License-Identifier: MIT

"""Wire types for the Lenny client SDK.

These dataclasses mirror the section 15.1 REST envelopes. The client
decodes JSON responses into them and encodes :class:`CreateSessionRequest`
into the POST body.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional


# The section 7.1 session lifecycle states the REST surface exposes.
# The gateway returns one of these as the ``state`` field of every
# session envelope.
STATE_CREATED = "created"
STATE_READY = "ready"
STATE_RUNNING = "running"
STATE_SUSPENDED = "suspended"
STATE_COMPLETED = "completed"
STATE_CANCELLED = "cancelled"
STATE_FAILED = "failed"
STATE_EXPIRED = "expired"


@dataclass
class CreateSessionRequest:
    """Body of ``POST /v1/sessions``.

    ``runtime_ref`` is required; the remaining fields are optional.
    """

    #: Identifies the runtime the session targets. Required.
    runtime_ref: str

    #: The section 10.2 user the session is created for. Optional.
    user_id: str = ""

    #: The section 14 workspace plan submitted at creation. Optional; an
    #: absent plan starts the session with an empty workspace.
    workspace_plan: Optional[dict[str, Any]] = None

    #: The section 10.6 environment the session is created in. Optional.
    environment: str = ""

    #: Pins the session to a section 5.3 isolation profile. Optional;
    #: the gateway resolves a default when this is empty.
    isolation_profile: str = ""

    def to_wire(self) -> dict[str, Any]:
        """Return the JSON body the gateway expects.

        Optional fields are omitted when empty so the body matches the
        Go SDK's ``omitempty`` encoding.
        """
        body: dict[str, Any] = {"runtimeRef": self.runtime_ref}
        if self.user_id:
            body["userId"] = self.user_id
        if self.workspace_plan is not None:
            body["workspacePlan"] = self.workspace_plan
        if self.environment:
            body["environment"] = self.environment
        if self.isolation_profile:
            body["isolationProfile"] = self.isolation_profile
        return body


@dataclass
class Session:
    """The section 15.1 session envelope.

    Returned by GET, the transition endpoints, and DELETE.
    """

    id: str = ""
    tenant_id: str = ""
    user_id: str = ""
    runtime_ref: str = ""
    environment: str = ""
    state: str = ""
    created_at: str = ""
    updated_at: str = ""

    #: Populated when ``state`` is ``failed``.
    failure_class: str = ""

    #: Echoes the section 14 plan stored at creation. ``None`` when the
    #: session was created without a plan.
    workspace_plan: Optional[dict[str, Any]] = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "Session":
        """Decode a section 15.1 session envelope."""
        return cls(
            id=str(raw.get("id", "")),
            tenant_id=str(raw.get("tenantId", "")),
            user_id=str(raw.get("userId", "")),
            runtime_ref=str(raw.get("runtimeRef", "")),
            environment=str(raw.get("environment", "")),
            state=str(raw.get("state", "")),
            created_at=str(raw.get("createdAt", "")),
            updated_at=str(raw.get("updatedAt", "")),
            failure_class=str(raw.get("failureClass", "")),
            workspace_plan=raw.get("workspacePlan"),
        )


@dataclass
class IsolationLevel:
    """The section 7.1 ``sessionIsolationLevel`` object on the create response."""

    #: The section 5.2 execution mode of the assigned pool: ``session``
    #: or ``service``.
    execution_mode: str = ""
    isolation_profile: str = ""
    pod_reuse: bool = False
    scrub_policy: str = ""
    residual_state_warning: bool = False

    #: ``platform`` for session mode (the platform binds the session to a
    #: pod and preserves conversation context across messages) or
    #: ``none`` for service mode (the gateway routes each message to any
    #: ready replica and maintains no conversation context between
    #: messages). spec: section 7.1.
    conversation_continuity: str = ""

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "IsolationLevel":
        """Decode the section 7.1 ``sessionIsolationLevel`` object."""
        return cls(
            execution_mode=str(raw.get("executionMode", "")),
            isolation_profile=str(raw.get("isolationProfile", "")),
            pod_reuse=bool(raw.get("podReuse", False)),
            scrub_policy=str(raw.get("scrubPolicy", "")),
            residual_state_warning=bool(raw.get("residualStateWarning", False)),
            conversation_continuity=str(raw.get("conversationContinuity", "")),
        )


@dataclass
class CreateSessionResult:
    """The section 15.1 ``POST /v1/sessions`` response.

    It embeds the session envelope and adds the section 7.1
    ``uploadToken`` and isolation-level fields.
    """

    session: Session = field(default_factory=Session)

    #: The section 7.1 single-use upload token. Treat it as a secret.
    upload_token: str = ""

    #: Echoes the section 7.1 ``sessionIsolationLevel`` object.
    isolation_level: IsolationLevel = field(default_factory=IsolationLevel)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "CreateSessionResult":
        """Decode the section 15.1 create-session response."""
        return cls(
            session=Session.from_wire(raw),
            upload_token=str(raw.get("uploadToken", "")),
            isolation_level=IsolationLevel.from_wire(
                raw.get("sessionIsolationLevel") or {}
            ),
        )


@dataclass
class ListOptions:
    """Narrows ``GET /v1/sessions``.

    An empty field applies no filter for that dimension.
    """

    #: Filters by session state.
    state: str = ""

    #: Filters by ``runtimeRef``.
    runtime: str = ""

    #: Carries the pagination cursor from a prior page's ``next_cursor``.
    #: An empty cursor requests the first page.
    cursor: str = ""

    #: Caps the page size. Zero leaves the gateway default in place.
    limit: int = 0


@dataclass
class SessionPage:
    """One page of a ``GET /v1/sessions`` listing.

    The gateway returns the section 15.1 canonical cursor-paginated
    envelope ``{items, cursor, hasMore, total?}`` (spec section 15.1
    lines 1228-1253).
    """

    #: The page of session envelopes.
    sessions: list[Session] = field(default_factory=list)

    #: The cursor for the following page. Empty when ``has_more`` is
    #: false.
    next_cursor: str = ""

    #: Whether more pages follow this one.
    has_more: bool = False

    #: The total match count across all pages, or ``None`` when the
    #: gateway omits it (spec section 15.1 line 1252). Callers must not
    #: rely on its presence.
    total: Optional[int] = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "SessionPage":
        """Decode a ``GET /v1/sessions`` page from the section 15.1
        canonical ``{items, cursor, hasMore, total?}`` envelope."""
        sessions = [
            Session.from_wire(s) for s in raw.get("items", []) or []
        ]
        total = raw.get("total")
        return cls(
            sessions=sessions,
            next_cursor=str(raw.get("cursor", "") or ""),
            has_more=bool(raw.get("hasMore", False)),
            total=int(total) if total is not None else None,
        )


# spec: section 15.4 lines 1715-1723 -- delivery is a closed enum on the
# inbound message payload. Surface the values as constants so the SDK
# consumer does not stringly type them.
DELIVERY_QUEUED = "queued"
DELIVERY_IMMEDIATE = "immediate"


@dataclass
class MessagePayload:
    """One section 15.4 inbound MessageEnvelope.

    Mirrors the spec field names verbatim; only ``content`` is required
    by the gateway. spec: section 15.4 lines 1672-1721.
    """

    #: Optional client-supplied message id; the gateway stamps
    #: ``msg_<random>`` when absent.
    id: str = ""

    #: Message role (``user``, ``assistant``, ...). The runtime
    #: interprets it.
    role: str = ""

    #: The message body delivered to the runtime.
    content: str = ""

    #: When set, names a pending ``lenny/request_input`` request the
    #: gateway resolves directly (section 7.2 path 1).
    in_reply_to: str = ""

    #: Section 15.4 closed enum: ``queued`` (default) or ``immediate``.
    delivery: str = ""

    #: Section 5.2 concurrent-workspace slot identifier.
    slot_id: str = ""

    def to_wire(self) -> dict[str, Any]:
        """Return the JSON payload the gateway expects."""
        body: dict[str, Any] = {"content": self.content}
        if self.id:
            body["id"] = self.id
        if self.role:
            body["role"] = self.role
        if self.in_reply_to:
            body["inReplyTo"] = self.in_reply_to
        if self.delivery:
            body["delivery"] = self.delivery
        if self.slot_id:
            body["slotId"] = self.slot_id
        return body


@dataclass
class DeliveryReceipt:
    """The section 15.4 ``delivery_receipt`` envelope.

    spec: section 15.4 lines 1725-1737; section 7.2 line 345.
    """

    #: Gateway-stamped or sender-supplied message id.
    message_id: str = ""
    #: One of ``delivered`` | ``queued`` | ``dropped`` | ``expired`` |
    #: ``rate_limited`` | ``error``.
    status: str = ""
    #: RFC 3339 timestamp the gateway accepted the message. Empty when
    #: ``status`` is not ``delivered``.
    delivered_at: str = ""
    #: Optional disposition reason for a non-``delivered`` status
    #: (``target_terminated``, ``dlq_overflow``, ...).
    reason: str = ""

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "DeliveryReceipt":
        """Decode a section 15.4 delivery_receipt envelope."""
        return cls(
            message_id=str(raw.get("messageId", "")),
            status=str(raw.get("status", "")),
            delivered_at=str(raw.get("deliveredAt", "")),
            reason=str(raw.get("reason", "")),
        )


@dataclass
class OutputPart:
    """One section 8.5 OutputPart returned alongside a delivery receipt."""

    type: str = ""
    text: str = ""
    data: Optional[dict[str, Any]] = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "OutputPart":
        """Decode a section 8.5 OutputPart."""
        return cls(
            type=str(raw.get("type", "")),
            text=str(raw.get("text", "")),
            data=raw.get("data"),
        )


@dataclass
class SendMessagesResponse:
    """``POST /v1/sessions/{id}/messages`` response.

    spec: section 15.1 messages endpoint; section 15.4 delivery_receipt;
    section 7.2 line 345.
    """

    delivery_receipt: DeliveryReceipt = field(default_factory=DeliveryReceipt)
    output: list[OutputPart] = field(default_factory=list)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "SendMessagesResponse":
        """Decode the section 15.1 messages response."""
        return cls(
            delivery_receipt=DeliveryReceipt.from_wire(
                raw.get("deliveryReceipt") or {}
            ),
            output=[OutputPart.from_wire(p) for p in raw.get("output") or []],
        )


@dataclass
class TranscriptEntry:
    """One row of the section 15.1 transcript page."""

    seq: int = 0
    role: str = ""
    content: str = ""
    created_at: str = ""
    metadata: Optional[dict[str, Any]] = None

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "TranscriptEntry":
        """Decode a section 15.1 transcript entry."""
        return cls(
            seq=int(raw.get("seq", 0) or 0),
            role=str(raw.get("role", "")),
            content=str(raw.get("content", "")),
            created_at=str(raw.get("createdAt", "")),
            metadata=raw.get("metadata"),
        )


@dataclass
class TranscriptResponse:
    """The section 15.1 ``GET /v1/sessions/{id}/transcript`` envelope."""

    session_id: str = ""
    entries: list[TranscriptEntry] = field(default_factory=list)

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "TranscriptResponse":
        """Decode the section 15.1 transcript envelope."""
        return cls(
            session_id=str(raw.get("sessionId", "")),
            entries=[
                TranscriptEntry.from_wire(e) for e in raw.get("entries") or []
            ],
        )


@dataclass
class TranscriptOptions:
    """Narrows ``GET /v1/sessions/{id}/transcript``."""

    #: Returns only entries with ``seq > after_seq``.
    after_seq: int = 0
    #: Caps the page size. Zero leaves the gateway default in place.
    limit: int = 0


@dataclass
class InteractionResolution:
    """Response from a section 15.1 interaction-resolution endpoint.

    Returned by tool-use approve/deny and elicitation respond/dismiss.
    spec: section 7.2 table lines 124-127; section 15.1.
    """

    #: Interaction id (``tool_call_id`` or ``elicitation_id``).
    id: str = ""
    #: New interaction phase (``approved``, ``denied``, ``responded``,
    #: ``dismissed``).
    phase: str = ""
    #: RFC 3339 resolution timestamp.
    resolved_at: str = ""

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "InteractionResolution":
        """Decode the section 15.1 interaction-resolution envelope."""
        return cls(
            id=str(raw.get("id", "")),
            phase=str(raw.get("phase", "")),
            resolved_at=str(raw.get("resolvedAt", "")),
        )
