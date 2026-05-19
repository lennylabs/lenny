---
layout: default
title: "Go"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 3
---

# Go Client SDK

The Go client SDK wraps the gateway REST session API. It covers the session
lifecycle, decodes the typed error envelope, retries retryable errors with
exponential backoff, supports per-request idempotency keys, and verifies
webhook signatures. This page covers installation, constructing a client,
running a session, and verifying a webhook.

The SDK depends only on the Go standard library.

---

## Install

The SDK lives in the Lenny repository module. Add it to a project with
`go get`:

```bash
go get github.com/lennylabs/lenny/sdks/client/go/lenny
```

The package import path is `github.com/lennylabs/lenny/sdks/client/go/lenny`.
The webhook signature verifier is in the sub-package
`github.com/lennylabs/lenny/sdks/client/go/webhook`.

---

## Construct a client

`lenny.New` returns a client bound to a gateway base URL. The base URL is the
gateway origin; the SDK appends the `/v1` path prefix. `New` returns an error
when the URL is empty or is not absolute.

```go fragment
import "github.com/lennylabs/lenny/sdks/client/go/lenny"

client, err := lenny.New(
    "https://gateway.acme.com",
    lenny.WithAuth(lenny.BearerToken(token)),
)
if err != nil {
    return err
}
```

A `Client` is safe for concurrent use by multiple goroutines.

### Authentication

`WithAuth` sets the credential the client attaches to every request. The SDK
ships three authenticators.

| Constructor | Header sent | Use it for |
|---|---|---|
| `lenny.BearerToken(token)` | `Authorization: Bearer <token>` | An OIDC ID token, a Lenny-issued access token, or a service-account token. |
| `lenny.APIKey(key)` | `X-Lenny-API-Key: <key>` | A static API key. |
| `lenny.RefreshingToken(source)` | `Authorization: Bearer <token>` | A token that the SDK fetches fresh before each request. |

`RefreshingToken` takes a `TokenSource`. The SDK calls `Token()` before each
request, so an implementation refreshes the token when it is close to expiry
and returns the current value:

```go fragment
type cachedSource struct{ /* token and expiry fields */ }

func (s *cachedSource) Token() (string, error) {
    // Refresh when the cached token is close to expiry, then return it.
    return s.current, nil
}

client, err := lenny.New(
    "https://gateway.acme.com",
    lenny.WithAuth(lenny.RefreshingToken(&cachedSource{})),
)
```

### Other client options

| Option | Effect |
|---|---|
| `lenny.WithHTTPClient(hc)` | Sets the underlying `*http.Client`. The default has a 30-second timeout. |
| `lenny.WithRetryPolicy(p)` | Overrides `lenny.DefaultRetryPolicy`. Unset fields are filled from the default. |
| `lenny.WithTenant(id)` | Sets the development `X-Lenny-Tenant-ID` header. The minimal gateway honors this header when no OIDC principal is present. Production deployments derive the tenant from the authenticated principal and ignore the header. |

---

## Run a session

The client methods cover the session lifecycle. Each one calls a single REST
endpoint.

| Method | Endpoint | Transition |
|---|---|---|
| `CreateSession` | `POST /v1/sessions` | Creates a session in the `created` state. |
| `GetSession` | `GET /v1/sessions/{id}` | Reads the current session envelope. |
| `ListSessions` | `GET /v1/sessions` | Returns one page of sessions. |
| `IterateSessions` | `GET /v1/sessions` | Walks every page of a listing. |
| `Finalize` | `POST /v1/sessions/{id}/finalize` | `created` to `ready`. |
| `Start` | `POST /v1/sessions/{id}/start` | `ready` to `running`. |
| `Interrupt` | `POST /v1/sessions/{id}/interrupt` | `running` to `suspended`. |
| `Resume` | `POST /v1/sessions/{id}/resume` | A session awaiting client action back to `running`. |
| `Terminate` | `POST /v1/sessions/{id}/terminate` | A non-terminal session to `completed`. |
| `DeleteSession` | `DELETE /v1/sessions/{id}` | A non-terminal session to `cancelled`. |

The following program creates a session, finalizes it, starts it, and
terminates it:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/lennylabs/lenny/sdks/client/go/lenny"
)

func main() {
	client, err := lenny.New(
		os.Getenv("LENNY_GATEWAY_URL"),
		lenny.WithAuth(lenny.BearerToken(os.Getenv("LENNY_TOKEN"))),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	created, err := client.CreateSession(ctx, lenny.CreateSessionRequest{
		RuntimeRef: "chat",
		UserID:     "alice@acme.com",
	}, lenny.WithIdempotencyKey("alice-session-2026-05-19-001"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created %s in state %s\n", created.ID, created.State)

	if _, err := client.Finalize(ctx, created.ID); err != nil {
		log.Fatal(err)
	}
	started, err := client.Start(ctx, created.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("session %s is %s\n", started.ID, started.State)

	final, err := client.Terminate(ctx, created.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("session %s ended in state %s\n", final.ID, final.State)
}
```

`CreateSession` returns a `*CreateSessionResult`, which embeds the session
envelope and adds the single-use `UploadToken` and the
`IsolationLevel`. Treat the upload token as a secret. The transition methods
each return the updated `*Session`.

### Idempotency keys

`WithIdempotencyKey` attaches an `Idempotency-Key` header. When the same key
is presented again with the same body within the key's TTL, the gateway
replays the cached response. Pass an idempotency key on `CreateSession` so a
retried create collapses to one session rather than producing a duplicate.

### Listing sessions

`ListSessions` returns one `SessionPage`. `IterateSessions` walks every page,
invoking a callback once per session. Iteration stops when the callback
returns `false`, when a page returns an error, or when the listing is
exhausted:

```go fragment
err := client.IterateSessions(ctx, lenny.ListOptions{
    State:   lenny.StateRunning,
    Runtime: "chat",
}, func(s lenny.Session) bool {
    fmt.Println(s.ID, s.State)
    return true
})
```

---

## Handle errors

The gateway returns a typed error envelope on every non-2xx response. The SDK
decodes it into an `*APIError` and returns it from the request method.
`APIError` implements the `error` interface, so `errors.As` recovers the
structured fields.

```go fragment
session, err := client.GetSession(ctx, sessionID)
if err != nil {
    var apiErr *lenny.APIError
    if errors.As(err, &apiErr) {
        fmt.Printf("code=%s category=%s retryable=%t\n",
            apiErr.Code, apiErr.Category, apiErr.Retryable)
    }
    return err
}
```

The `Category` field carries one of four values: `TRANSIENT`, `PERMANENT`,
`POLICY`, or `UPSTREAM`. `lenny.IsRetryable(err)` reports whether the error is
a retryable `APIError`. `lenny.AsAPIError(err)` narrows an error to an
`*APIError`.

### Retries

The client retries a retryable error on its own, following the retry policy.
`DefaultRetryPolicy` retries up to three times with exponential backoff,
jitter, a 200-millisecond base delay, and a 5-second cap. A transport-level
failure such as a connection refusal or a DNS error is retried the same way.
Override the policy with `WithRetryPolicy`:

```go fragment
client, err := lenny.New(
    "https://gateway.acme.com",
    lenny.WithAuth(lenny.BearerToken(token)),
    lenny.WithRetryPolicy(lenny.RetryPolicy{
        MaxAttempts: 5,
        BaseDelay:   500 * time.Millisecond,
        MaxDelay:    10 * time.Second,
        Jitter:      true,
    }),
)
```

Set `MaxAttempts` to 1 to disable retries.

---

## Verify a webhook

A webhook delivery carries an `X-Lenny-Signature` header of the form
`t=<unix_seconds>,v1=<hex_signature>`, where the signature is an HMAC-SHA256
over `<unix_seconds>.<raw_body>`. The `webhook` sub-package verifies the
signature with the `callbackSecret` supplied at session creation.

Construct a `Verifier` with `webhook.NewWithSecret` for a single secret, or
`webhook.New` for a secret set during a rotation overlap. `VerifyRequest`
reads the signature header from an `*http.Request` and checks it against the
raw body. Read the body before calling, because the verifier hashes the exact
bytes the gateway signed:

```go
package main

import (
	"io"
	"log"
	"net/http"

	"github.com/lennylabs/lenny/sdks/client/go/webhook"
)

func main() {
	verifier, err := webhook.NewWithSecret([]byte("the-callback-secret"))
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/webhooks/lenny", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if err := verifier.VerifyRequest(r, body); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		// The signature is valid. Process the delivery.
		w.WriteHeader(http.StatusNoContent)
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}
```

`Verify` returns `webhook.ErrMissingSignature` when the header is absent,
`webhook.ErrMalformedSignature` when the header does not parse,
`webhook.ErrReplayWindow` when the delivery timestamp is more than five
minutes from the receiver's clock, and `webhook.ErrSignatureMismatch` when the
HMAC matches no configured secret. Match a specific failure with `errors.Is`.

---

## See also

- [Wire Format](../wire-format.html) for the canonical JSON envelopes and headers.
- [Session Lifecycle](../session-lifecycle.html) for the session state machine.
- [Error Handling](../error-handling.html) for the error codes and retry strategy.
- [Webhooks](../webhooks.html) for the webhook event catalog and delivery model.
