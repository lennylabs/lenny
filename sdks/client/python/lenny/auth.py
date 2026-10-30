# SPDX-License-Identifier: MIT

"""Authentication credentials for the Lenny client SDK.

An :class:`Authenticator` attaches a credential to an outbound request.
The client invokes :meth:`Authenticator.apply` on every request before
sending it. The SDK ships a static bearer token, a static API key, and
a refreshing token sourced from a callable.
"""

from __future__ import annotations

from typing import Callable, Protocol, runtime_checkable


@runtime_checkable
class Authenticator(Protocol):
    """Attaches an authentication credential to an outbound request.

    The client invokes :meth:`apply` on every request before sending
    it. Implementations are expected to be safe for concurrent use.
    """

    def apply(self, headers: dict[str, str]) -> None:
        """Mutate ``headers`` to carry the credential.

        Raising an exception aborts the request.
        """
        ...


class BearerToken:
    """Sends a static token in the ``Authorization`` header.

    Use it for an OIDC ID token, a Lenny-issued access token, or a
    service-account token, per the section 13 client authentication
    model.
    """

    def __init__(self, token: str) -> None:
        self._token = token

    def apply(self, headers: dict[str, str]) -> None:
        headers["Authorization"] = "Bearer " + self._token


class APIKey:
    """Sends a static API key in the ``X-Lenny-API-Key`` header."""

    def __init__(self, key: str) -> None:
        self._key = key

    def apply(self, headers: dict[str, str]) -> None:
        headers["X-Lenny-API-Key"] = self._key


class RefreshingToken:
    """Sends a bearer token sourced fresh from a callable per request.

    It is the section 15.6 token-refresh-before-expiry hook. The
    ``source`` callable refreshes the token when it is close to expiry
    and returns the current value; the SDK attaches whatever the source
    returns. Raising from the source aborts the request.
    """

    def __init__(self, source: Callable[[], str]) -> None:
        self._source = source

    def apply(self, headers: dict[str, str]) -> None:
        headers["Authorization"] = "Bearer " + self._source()
