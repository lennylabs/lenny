---
layout: default
title: "TypeScript"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 2
---

# TypeScript Client SDK

The TypeScript client SDK wraps the gateway REST session API. It covers the
session lifecycle, parses the typed error envelope, retries retryable errors
with exponential backoff, supports per-request idempotency keys, and verifies
webhook signatures. This page covers installation, constructing a client,
running a session, and verifying a webhook.

The SDK is published as an ECMAScript module with a CommonJS build alongside
it. The REST client uses the global `fetch`. The webhook verifier uses the
Node.js `crypto` module, so webhook verification requires Node.js 18 or later.

---

## Install

```bash
npm install @lennylabs/client-sdk
```

The package exports the REST client, the authenticators, the typed error, the
wire types, and the webhook verifier from a single entry point:

```ts fragment
import { newClient, bearerToken, ApiError, newVerifier } from '@lennylabs/client-sdk';
```

---

## Construct a client

`newClient` returns a client bound to a gateway base URL. The base URL is the
gateway origin; the SDK appends the `/v1` path prefix. The constructor throws
when the URL is empty or is not absolute.

```ts fragment
import { newClient, bearerToken } from '@lennylabs/client-sdk';

const client = newClient('https://gateway.acme.com', {
  auth: bearerToken(token),
});
```

### Authentication

The `auth` option sets the credential the client attaches to every request.
The SDK ships three authenticators.

| Function | Header sent | Use it for |
|---|---|---|
| `bearerToken(token)` | `Authorization: Bearer <token>` | An OIDC ID token, a Lenny-issued access token, or a service-account token. |
| `apiKey(key)` | `X-Lenny-API-Key: <key>` | A static API key. |
| `refreshingToken(source)` | `Authorization: Bearer <token>` | A token that the SDK fetches fresh before each request. |

`refreshingToken` takes a `TokenSource`. The SDK calls `token()` before each
request, so an implementation refreshes the token when it is close to expiry
and returns the current value. The `token()` method may return a promise:

```ts fragment
import { newClient, refreshingToken } from '@lennylabs/client-sdk';

const client = newClient('https://gateway.acme.com', {
  auth: refreshingToken({
    async token(): Promise<string> {
      // Refresh when the cached token is close to expiry, then return it.
      return currentToken;
    },
  }),
});
```

### Other client options

| Option | Effect |
|---|---|
| `retryPolicy` | Overrides `defaultRetryPolicy`. Unset fields are filled from the default. |
| `timeoutMs` | The per-request timeout in milliseconds. The default is 30000. |
| `fetch` | The `fetch` implementation. The default is the global `fetch`. |
| `tenantId` | Sets the development `X-Lenny-Tenant-ID` header. The minimal gateway honors this header when no OIDC principal is present. Production deployments derive the tenant from the authenticated principal and ignore the header. |

---

## Run a session

The client methods cover the session lifecycle. Each one calls a single REST
endpoint and returns a promise.

| Method | Endpoint | Transition |
|---|---|---|
| `createSession` | `POST /v1/sessions` | Creates a session in the `created` state. |
| `getSession` | `GET /v1/sessions/{id}` | Reads the current session envelope. |
| `listSessions` | `GET /v1/sessions` | Returns one page of sessions. |
| `iterateSessions` | `GET /v1/sessions` | An async generator over every page. |
| `finalize` | `POST /v1/sessions/{id}/finalize` | `created` to `ready`. |
| `start` | `POST /v1/sessions/{id}/start` | `ready` to `running`. |
| `interrupt` | `POST /v1/sessions/{id}/interrupt` | `running` to `suspended`. |
| `resume` | `POST /v1/sessions/{id}/resume` | A session awaiting client action back to `running`. |
| `terminate` | `POST /v1/sessions/{id}/terminate` | A non-terminal session to `completed`. |
| `deleteSession` | `DELETE /v1/sessions/{id}` | A non-terminal session to `cancelled`. |

The following program creates a session, finalizes it, starts it, and
terminates it:

```ts fragment
import { newClient, bearerToken } from '@lennylabs/client-sdk';

async function main(): Promise<void> {
  const client = newClient(process.env.LENNY_GATEWAY_URL ?? '', {
    auth: bearerToken(process.env.LENNY_TOKEN ?? ''),
  });

  const created = await client.createSession(
    { runtimeRef: 'chat', userId: 'alice@acme.com' },
    { idempotencyKey: 'alice-session-2026-05-19-001' },
  );
  console.log(`created ${created.id} in state ${created.state}`);

  await client.finalize(created.id);
  const started = await client.start(created.id);
  console.log(`session ${started.id} is ${started.state}`);

  const final = await client.terminate(created.id);
  console.log(`session ${final.id} ended in state ${final.state}`);
}

void main();
```

`createSession` resolves to a `CreateSessionResult`, which extends the session
envelope with the single-use `uploadToken` and the `sessionIsolationLevel`.
Treat the upload token as a secret. The transition methods each resolve to the
updated `Session`.

### Idempotency keys

The `idempotencyKey` request option attaches an `Idempotency-Key` header. When
the same key is presented again with the same body within the key's TTL, the
gateway replays the cached response. Pass an idempotency key on
`createSession` so a retried create collapses to one session rather than
producing a duplicate.

### Listing sessions

`listSessions` resolves to one `SessionPage`. `iterateSessions` is an async
generator that walks every page, yielding one session at a time:

```ts fragment
for await (const session of client.iterateSessions({
  state: 'running',
  runtime: 'chat',
})) {
  console.log(session.id, session.state);
}
```

### Cancellation

Each request method accepts an `AbortSignal` in the `signal` request option.
The SDK aborts the request when the signal fires or when the per-request
timeout elapses, whichever is first.

---

## Handle errors

The gateway returns a typed error envelope on every non-2xx response. The SDK
parses it into an `ApiError` and throws it from the request method. `ApiError`
extends the built-in `Error`, so a caller can either `instanceof`-check it or
read the structured fields.

```ts fragment
import { ApiError } from '@lennylabs/client-sdk';

try {
  await client.getSession(sessionId);
} catch (err) {
  if (err instanceof ApiError) {
    console.error(`code=${err.code} category=${err.category} retryable=${err.retryable}`);
  }
  throw err;
}
```

The `category` field carries one of four values: `TRANSIENT`, `PERMANENT`,
`POLICY`, or `UPSTREAM`. `isRetryable(err)` reports whether the error is a
retryable `ApiError`. `asApiError(err)` narrows an error to an `ApiError` or
returns `undefined`.

### Retries

The client retries a retryable error on its own, following the retry policy.
`defaultRetryPolicy` retries up to three times with exponential backoff,
jitter, a 200-millisecond base delay, and a 5-second cap. A transport-level
failure such as a connection refusal or a DNS error is retried the same way.
An abort is surfaced rather than retried. Override the policy with the
`retryPolicy` option:

```ts fragment
const client = newClient('https://gateway.acme.com', {
  auth: bearerToken(token),
  retryPolicy: {
    maxAttempts: 5,
    baseDelayMs: 500,
    maxDelayMs: 10_000,
    jitter: true,
  },
});
```

Set `maxAttempts` to 1 to disable retries.

---

## Verify a webhook

A webhook delivery carries an `X-Lenny-Signature` header of the form
`t=<unix_seconds>,v1=<hex_signature>`, where the signature is an HMAC-SHA256
over `<unix_seconds>.<raw_body>`. The `Verifier` checks the signature with the
`callbackSecret` supplied at session creation.

Construct a `Verifier` with `verifierWithSecret` for a single secret, or
`newVerifier` for a secret set during a rotation overlap. `verify` checks a
raw body against the signature header value. Pass the exact bytes the gateway
signed:

```ts fragment
import { verifierWithSecret, WebhookError, signatureHeader } from '@lennylabs/client-sdk';
import { createServer } from 'node:http';

const verifier = verifierWithSecret('the-callback-secret');

createServer((req, res) => {
  const chunks: Buffer[] = [];
  req.on('data', (chunk) => chunks.push(chunk));
  req.on('end', () => {
    const body = Buffer.concat(chunks);
    const header = req.headers[signatureHeader.toLowerCase()];
    try {
      verifier.verify(body, typeof header === 'string' ? header : '');
    } catch (err) {
      if (err instanceof WebhookError) {
        res.writeHead(401).end('invalid signature');
        return;
      }
      throw err;
    }
    // The signature is valid. Process the delivery.
    res.writeHead(204).end();
  });
}).listen(8080);
```

`verify` throws a `WebhookError` when verification fails. The `reason` field
carries `missing_signature` when the header is absent, `malformed_signature`
when the header does not parse, `replay_window` when the delivery timestamp is
more than five minutes from the receiver's clock, and `signature_mismatch`
when the HMAC matches no configured secret. The HMAC comparison is
constant-time.

---

## See also

- [Wire Format](../wire-format.html) for the canonical JSON envelopes and headers.
- [Session Lifecycle](../session-lifecycle.html) for the session state machine.
- [Error Handling](../error-handling.html) for the error codes and retry strategy.
- [Webhooks](../webhooks.html) for the webhook event catalog and delivery model.
