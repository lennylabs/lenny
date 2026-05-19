---
layout: default
title: "Python"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 1
---

# Python Client SDK

The Python client SDK wraps the gateway REST session API. It covers the
session lifecycle, decodes the typed error envelope, retries retryable errors
with exponential backoff, supports per-request idempotency keys, and verifies
webhook signatures. This page covers installation, constructing a client,
running a session, and verifying a webhook.

The SDK uses only the Python standard library at runtime. The transport is
`urllib.request` and the webhook verifier is `hmac` plus `hashlib`, so the
package installs and runs without a third-party dependency. It requires
Python 3.10 or later.

---

## Install

```bash
pip install lenny-client
```

The package name is `lenny-client`; the import name is `lenny`. The top-level
module exports the clients, the authenticators, the typed error, the retry
policy, and the wire types. The webhook signature verifier is in the
`lenny.webhook` module.

```python fragment
from lenny import Client, BearerToken, APIError
from lenny.webhook import Verifier
```

---

## Construct a client

`Client` is constructed with a gateway base URL. The base URL is the gateway
origin; the SDK appends the `/v1` path prefix. The constructor raises
`ValueError` when the URL is empty or is not absolute.

```python fragment
from lenny import Client, BearerToken

client = Client("https://gateway.acme.com", auth=BearerToken(token))
```

A `Client` is safe for concurrent use by multiple threads. `AsyncClient`
exposes the same surface behind `async` and `await`; it accepts the same
constructor arguments and runs each call on a worker thread.

### Authentication

The `auth` argument sets the credential the client attaches to every request.
The SDK ships three authenticators.

| Class | Header sent | Use it for |
|---|---|---|
| `BearerToken(token)` | `Authorization: Bearer <token>` | An OIDC ID token, a Lenny-issued access token, or a service-account token. |
| `APIKey(key)` | `X-Lenny-API-Key: <key>` | A static API key. |
| `RefreshingToken(source)` | `Authorization: Bearer <token>` | A token that the SDK fetches fresh before each request. |

`RefreshingToken` takes a callable. The SDK invokes it before each request, so
the callable refreshes the token when it is close to expiry and returns the
current value:

```python fragment
from lenny import Client, RefreshingToken

def current_token() -> str:
    # Refresh when the cached token is close to expiry, then return it.
    return token

client = Client("https://gateway.acme.com", auth=RefreshingToken(current_token))
```

### Other constructor arguments

| Argument | Effect |
|---|---|
| `retry_policy` | Overrides `DEFAULT_RETRY_POLICY`. |
| `timeout` | The per-request timeout in seconds. The default is 30.0. |
| `tenant_id` | Sets the development `X-Lenny-Tenant-ID` header. The minimal gateway honors this header when no OIDC principal is present. Production deployments derive the tenant from the authenticated principal and ignore the header. |

---

## Run a session

The client methods cover the session lifecycle. Each one calls a single REST
endpoint.

| Method | Endpoint | Transition |
|---|---|---|
| `create_session` | `POST /v1/sessions` | Creates a session in the `created` state. |
| `get_session` | `GET /v1/sessions/{id}` | Reads the current session envelope. |
| `list_sessions` | `GET /v1/sessions` | Returns one page of sessions. |
| `iterate_sessions` | `GET /v1/sessions` | Yields every session across every page. |
| `finalize` | `POST /v1/sessions/{id}/finalize` | `created` to `ready`. |
| `start` | `POST /v1/sessions/{id}/start` | `ready` to `running`. |
| `interrupt` | `POST /v1/sessions/{id}/interrupt` | `running` to `suspended`. |
| `resume` | `POST /v1/sessions/{id}/resume` | A session awaiting client action back to `running`. |
| `terminate` | `POST /v1/sessions/{id}/terminate` | A non-terminal session to `completed`. |
| `delete_session` | `DELETE /v1/sessions/{id}` | A non-terminal session to `cancelled`. |

The following program creates a session, finalizes it, starts it, and
terminates it:

```python fragment
import os

from lenny import BearerToken, Client, CreateSessionRequest, RequestOptions


def main() -> None:
    client = Client(
        os.environ["LENNY_GATEWAY_URL"],
        auth=BearerToken(os.environ["LENNY_TOKEN"]),
    )

    created = client.create_session(
        CreateSessionRequest(runtime_ref="chat", user_id="alice@acme.com"),
        RequestOptions(idempotency_key="alice-session-2026-05-19-001"),
    )
    print(f"created {created.session.id} in state {created.session.state}")

    client.finalize(created.session.id)
    started = client.start(created.session.id)
    print(f"session {started.id} is {started.state}")

    final = client.terminate(created.session.id)
    print(f"session {final.id} ended in state {final.state}")


if __name__ == "__main__":
    main()
```

`create_session` returns a `CreateSessionResult`. Its `session` field is the
session envelope; the result also carries the single-use `upload_token` and
the `isolation_level`. Treat the upload token as a secret. The transition
methods each return the updated `Session`.

### Idempotency keys

A `RequestOptions` with an `idempotency_key` attaches an `Idempotency-Key`
header. When the same key is presented again with the same body within the
key's TTL, the gateway replays the cached response. Pass an idempotency key on
`create_session` so a retried create collapses to one session rather than
producing a duplicate.

### Listing sessions

`list_sessions` returns one `SessionPage`. `iterate_sessions` yields every
session across every page:

```python fragment
from lenny import ListOptions, STATE_RUNNING

for session in client.iterate_sessions(
    ListOptions(state=STATE_RUNNING, runtime="chat")
):
    print(session.id, session.state)
```

---

## Handle errors

The gateway returns a typed error envelope on every non-2xx response. The SDK
decodes it into an `APIError` and raises it from the request method.
`APIError` extends `LennyError`, the base class for every SDK error.

```python fragment
from lenny import APIError

try:
    client.get_session(session_id)
except APIError as err:
    print(f"code={err.code} category={err.category} retryable={err.retryable}")
    raise
```

The `category` field carries one of four values: `TRANSIENT`, `PERMANENT`,
`POLICY`, or `UPSTREAM`. The module exports these as the constants
`CATEGORY_TRANSIENT`, `CATEGORY_PERMANENT`, `CATEGORY_POLICY`, and
`CATEGORY_UPSTREAM`. `is_retryable(err)` reports whether an error is a
retryable `APIError`.

A request that fails before the gateway returns an HTTP response, such as a
connection refusal or a DNS failure, raises a `TransportError`.

### Retries

The client retries a retryable error on its own, following the retry policy.
`DEFAULT_RETRY_POLICY` retries up to three times with exponential backoff,
jitter, a 0.2-second base delay, and a 5-second cap. A transport failure is
retried the same way. Override the policy with the `retry_policy` argument:

```python fragment
from lenny import Client, BearerToken, RetryPolicy

client = Client(
    "https://gateway.acme.com",
    auth=BearerToken(token),
    retry_policy=RetryPolicy(
        max_attempts=5,
        base_delay=0.5,
        max_delay=10.0,
        jitter=True,
    ),
)
```

Set `max_attempts` to 1 to disable retries.

---

## Verify a webhook

A webhook delivery carries an `X-Lenny-Signature` header of the form
`t=<unix_seconds>,v1=<hex_signature>`, where the signature is an HMAC-SHA256
over `<unix_seconds>.<raw_body>`. The `Verifier` checks the signature with the
`callbackSecret` supplied at session creation.

Construct a `Verifier` with `Verifier.with_secret` for a single secret, or the
`Verifier` constructor with a list for a secret set during a rotation overlap.
`verify` checks a raw body against the signature header value. Pass the exact
bytes the gateway signed:

```python fragment
from http.server import BaseHTTPRequestHandler, HTTPServer

from lenny.webhook import SIGNATURE_HEADER, Verifier, WebhookError

verifier = Verifier.with_secret("the-callback-secret")


class Handler(BaseHTTPRequestHandler):
    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        signature = self.headers.get(SIGNATURE_HEADER, "")
        try:
            verifier.verify(body, signature)
        except WebhookError:
            self.send_response(401)
            self.end_headers()
            return
        # The signature is valid. Process the delivery.
        self.send_response(204)
        self.end_headers()


HTTPServer(("", 8080), Handler).serve_forever()
```

`verify` returns `None` when the signature is valid. It raises a subclass of
`WebhookError` on failure: `MissingSignatureError` when the header is absent,
`MalformedSignatureError` when the header does not parse, `ReplayWindowError`
when the delivery timestamp is more than five minutes from the receiver's
clock, and `SignatureMismatchError` when the HMAC matches no configured
secret. Catch `WebhookError` to reject every failed verification, or catch a
specific subclass to branch on the cause.

---

## See also

- [Wire Format](../wire-format.html) for the canonical JSON envelopes and headers.
- [Session Lifecycle](../session-lifecycle.html) for the session state machine.
- [Error Handling](../error-handling.html) for the error codes and retry strategy.
- [Webhooks](../webhooks.html) for the webhook event catalog and delivery model.
