# SPDX-License-Identifier: MIT

"""Typed errors for the Lenny client SDK.

The gateway returns the section 15.1 error envelope
``{code, category, message, retryable, docs_url, details}`` on every
non-2xx REST response. :class:`APIError` is the typed form of that
envelope. The client raises it from every request method when the
gateway returns a 4xx or 5xx status.
"""

from __future__ import annotations

from typing import Any, Optional


# The section 15.1 error categories. Every gateway error envelope
# carries one of these four values in its ``category`` field.

#: Marks an error whose cause is expected to clear without a request
#: change (pod crash, timeout, dependency degradation).
CATEGORY_TRANSIENT = "TRANSIENT"

#: Marks an error that will not clear on retry without a request change
#: (validation failure, bad state).
CATEGORY_PERMANENT = "PERMANENT"

#: Marks an error produced by a policy decision (quota, rate limit,
#: authorization).
CATEGORY_POLICY = "POLICY"

#: Marks an error returned by an external dependency the gateway called.
CATEGORY_UPSTREAM = "UPSTREAM"


class LennyError(Exception):
    """Base class for every error raised by the Lenny client SDK."""


class APIError(LennyError):
    """Typed form of the section 15.1 error envelope.

    The gateway returns this body on every non-2xx REST response. The
    client decodes the envelope into this exception and raises it from
    the request method that produced the failed call.
    """

    def __init__(
        self,
        code: str = "",
        category: str = "",
        message: str = "",
        retryable: bool = False,
        docs_url: str = "",
        details: Optional[dict[str, Any]] = None,
        http_status: int = 0,
    ) -> None:
        #: Machine-readable section 15.1 error code (for example
        #: ``VALIDATION_ERROR``, ``RESOURCE_NOT_FOUND``,
        #: ``RATE_LIMITED``).
        self.code: str = code

        #: Section 15.1 error category.
        self.category: str = category

        #: Human-readable description.
        self.message: str = message

        #: Whether the client should retry the request. The client
        #: retries an error only when this field is true.
        self.retryable: bool = retryable

        #: Documentation URL for this error code when the gateway
        #: supplies one. The section 15.1 envelope spells the field
        #: ``docs_url``.
        self.docs_url: str = docs_url

        #: Error-specific context. The structure varies by code;
        #: ``INVALID_STATE_TRANSITION``, for example, carries
        #: ``currentState`` and ``allowedStates``.
        self.details: dict[str, Any] = details or {}

        #: HTTP status code the gateway returned alongside the envelope.
        #: It is populated by the client and is not part of the wire
        #: envelope.
        self.http_status: int = http_status

        super().__init__(self._render())

    def _render(self) -> str:
        if not self.code:
            return (
                "lenny: gateway returned HTTP "
                f"{self.http_status} with no error envelope"
            )
        return f"lenny: {self.code} ({self.category}): {self.message}"

    @classmethod
    def from_response(cls, http_status: int, body: bytes) -> "APIError":
        """Parse a non-2xx response body into an :class:`APIError`.

        When the body does not carry a section 15.1 envelope, the error
        code is left empty and ``retryable`` is inferred from the HTTP
        status.
        """
        import json

        try:
            envelope = json.loads(body) if body else {}
        except (ValueError, TypeError):
            envelope = {}

        error = envelope.get("error") if isinstance(envelope, dict) else None
        if isinstance(error, dict) and error.get("code"):
            return cls(
                code=str(error.get("code", "")),
                category=str(error.get("category", "")),
                message=str(error.get("message", "")),
                retryable=bool(error.get("retryable", False)),
                docs_url=str(error.get("docs_url", "")),
                details=error.get("details") or {},
                http_status=http_status,
            )
        return cls(
            message=body.decode("utf-8", "replace").strip(),
            retryable=retryable_status(http_status),
            http_status=http_status,
        )


class TransportError(LennyError):
    """A request failed before the gateway returned an HTTP response.

    A connection refusal, a DNS failure, or a socket timeout produces
    this error. The client's retry loop treats a transport failure as a
    transient error and retries it under the retry policy.
    """


def is_retryable(err: BaseException) -> bool:
    """Report whether ``err`` is a retryable :class:`APIError`.

    A non-:class:`APIError` (a transport failure, a cancellation) is not
    retryable through this predicate; the client's retry loop treats
    transport failures separately.
    """
    return isinstance(err, APIError) and err.retryable


def retryable_status(status: int) -> bool:
    """Report whether an HTTP status warrants a retry.

    This is the fallback used when the response carried no parseable
    error envelope. The section 15.1 retryable categories map onto 429,
    500, 502, 503, and 504.
    """
    return status in (429, 500, 502, 503, 504)
