# SPDX-License-Identifier: MIT

"""Section 14 Lenny webhook signature verification.

A webhook delivery carries an ``X-Lenny-Signature`` header of the form
``t=<unix_seconds>,v1=<hex_signature>``, where the signature is an
HMAC-SHA256 over ``"<unix_seconds>.<raw_body>"``. A receiver verifies
the HMAC with the ``callbackSecret`` it supplied at session creation and
rejects a delivery timestamp outside a five-minute replay window.

The :class:`Verifier` reproduces the scheme the gateway signs with so an
SDK consumer verifies deliveries with the same algorithm.
"""

from __future__ import annotations

import hashlib
import hmac
import time
from typing import Callable, Optional, Sequence, Union

from .errors import LennyError

#: The section 14 HTTP header that carries the webhook signature.
SIGNATURE_HEADER = "X-Lenny-Signature"

#: The section 14 maximum delivery-timestamp skew in seconds. A delivery
#: whose timestamp is more than this far from the receiver's clock is
#: rejected.
REPLAY_WINDOW_SECONDS = 5 * 60


class WebhookError(LennyError):
    """Base class for every webhook signature verification failure."""


class MalformedSignatureError(WebhookError):
    """The ``X-Lenny-Signature`` header does not parse as ``t=<unix>,v1=<hex>``."""


class ReplayWindowError(WebhookError):
    """The delivery timestamp is outside the five-minute replay window."""


class SignatureMismatchError(WebhookError):
    """The signature matches none of the configured secrets."""


class MissingSignatureError(WebhookError):
    """The delivery carries no ``X-Lenny-Signature`` header."""


def _coerce_bytes(value: Union[str, bytes]) -> bytes:
    """Return ``value`` as bytes, encoding a str as UTF-8."""
    return value.encode("utf-8") if isinstance(value, str) else value


class Verifier:
    """Checks webhook delivery signatures against one or more secrets.

    Holding more than one secret lets a receiver verify deliveries
    during a secret-rotation overlap window. Construct with at least one
    secret.
    """

    def __init__(
        self,
        secrets: Sequence[Union[str, bytes]],
        clock: Optional[Callable[[], float]] = None,
    ) -> None:
        """Construct a verifier.

        Args:
            secrets: One or more signing secrets. Pass the
                ``callbackSecret`` supplied at session creation; pass
                both the old and new secret during a rotation overlap.
            clock: Overrides the time source used for the replay-window
                check. Tests inject a fixed clock; production leaves
                this unset.

        Raises:
            ValueError: When ``secrets`` is empty.
        """
        secret_list = [_coerce_bytes(s) for s in secrets]
        if not secret_list:
            raise ValueError("webhook: at least one signing secret is required")
        self._secrets: list[bytes] = secret_list
        self._clock: Callable[[], float] = clock or time.time

    @classmethod
    def with_secret(
        cls,
        secret: Union[str, bytes],
        clock: Optional[Callable[[], float]] = None,
    ) -> "Verifier":
        """Construct a verifier from a single secret."""
        return cls([secret], clock=clock)

    def verify(
        self, body: Union[str, bytes], signature_header: str
    ) -> None:
        """Check that ``signature_header`` is a valid signature over ``body``.

        ``signature_header`` is the raw value of the
        ``X-Lenny-Signature`` header. The method returns ``None`` when
        the signature is valid.

        Raises:
            MissingSignatureError: When ``signature_header`` is empty.
            MalformedSignatureError: When the header does not parse.
            ReplayWindowError: When the timestamp is stale.
            SignatureMismatchError: When the HMAC matches no configured
                secret.
        """
        if not signature_header:
            raise MissingSignatureError(
                "webhook: request is missing the X-Lenny-Signature header"
            )
        body_bytes = _coerce_bytes(body)
        unix, v1 = _parse_header(signature_header)
        try:
            timestamp = int(unix)
        except ValueError as exc:
            raise MalformedSignatureError(
                "webhook: X-Lenny-Signature header is malformed"
            ) from exc

        skew = abs(int(self._clock()) - timestamp)
        if skew > REPLAY_WINDOW_SECONDS:
            raise ReplayWindowError(
                "webhook: signature timestamp is outside the replay window"
            )

        for secret in self._secrets:
            expected = _compute_hmac(secret, unix, body_bytes)
            if hmac.compare_digest(expected, v1):
                return
        raise SignatureMismatchError("webhook: signature does not match")


def sign(
    secret: Union[str, bytes],
    body: Union[str, bytes],
    timestamp: Optional[int] = None,
) -> str:
    """Produce the section 14 ``X-Lenny-Signature`` header value.

    It signs ``body`` with ``secret`` at delivery time ``timestamp``
    (Unix seconds). When ``timestamp`` is omitted the current time is
    used. This is the inverse of :meth:`Verifier.verify` and is useful
    in tests.
    """
    if timestamp is None:
        timestamp = int(time.time())
    unix = str(int(timestamp))
    digest = _compute_hmac(_coerce_bytes(secret), unix, _coerce_bytes(body))
    return f"t={unix},v1={digest}"


def _compute_hmac(secret: bytes, unix: str, body: bytes) -> str:
    """Return the hex-encoded HMAC-SHA256 over ``"<unix>.<body>"``."""
    mac = hmac.new(secret, digestmod=hashlib.sha256)
    mac.update(unix.encode("utf-8"))
    mac.update(b".")
    mac.update(body)
    return mac.hexdigest()


def _parse_header(header: str) -> tuple[str, str]:
    """Extract the ``t`` and ``v1`` components of an ``X-Lenny-Signature`` header."""
    unix = ""
    v1 = ""
    for part in header.split(","):
        key, sep, value = part.partition("=")
        if not sep:
            raise MalformedSignatureError(
                "webhook: X-Lenny-Signature header is malformed"
            )
        key = key.strip()
        if key == "t":
            unix = value
        elif key == "v1":
            v1 = value
    if not unix or not v1:
        raise MalformedSignatureError(
            "webhook: X-Lenny-Signature header is malformed"
        )
    return unix, v1
