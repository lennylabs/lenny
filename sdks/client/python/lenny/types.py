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

    execution_mode: str = ""
    isolation_profile: str = ""
    pod_reuse: bool = False
    scrub_policy: str = ""
    residual_state_warning: bool = False

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "IsolationLevel":
        """Decode the section 7.1 ``sessionIsolationLevel`` object."""
        return cls(
            execution_mode=str(raw.get("executionMode", "")),
            isolation_profile=str(raw.get("isolationProfile", "")),
            pod_reuse=bool(raw.get("podReuse", False)),
            scrub_policy=str(raw.get("scrubPolicy", "")),
            residual_state_warning=bool(raw.get("residualStateWarning", False)),
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

    The gateway returns the session array; the pagination fields are
    populated from the section 25.2 pagination envelope when the gateway
    supplies it.
    """

    #: The page of session envelopes.
    sessions: list[Session] = field(default_factory=list)

    #: The cursor for the following page. Empty when ``has_more`` is
    #: false.
    next_cursor: str = ""

    #: Whether more pages follow this one.
    has_more: bool = False

    @classmethod
    def from_wire(cls, raw: dict[str, Any]) -> "SessionPage":
        """Decode a ``GET /v1/sessions`` page.

        The section 25.2 pagination envelope is the canonical source
        when the gateway supplies it; the top-level fields are the
        fallback.
        """
        sessions = [
            Session.from_wire(s) for s in raw.get("sessions", []) or []
        ]
        next_cursor = str(raw.get("nextCursor", ""))
        has_more = bool(raw.get("hasMore", False))
        pagination = raw.get("pagination")
        if isinstance(pagination, dict):
            next_cursor = str(pagination.get("cursor", ""))
            has_more = bool(pagination.get("hasMore", False))
        return cls(
            sessions=sessions,
            next_cursor=next_cursor,
            has_more=has_more,
        )
