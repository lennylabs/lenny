# Spec changes — Upload tokens and playground OIDC state fail across gateway replicas

## Design (as the spec must state it)

Every client-held value a gateway replica verifies is checked against state all replicas share. No flow depends on a secret held by one process, as the §10.1.1 correctness rule requires, and that rule needs no edit.

- **`uploadToken`.** The session row is the authority. A token is a random secret, the row holds its digest and expiry, and the row state answers consumption. SPEC-1 **Check order:** is the home of the verification order and SPEC-1 **Single-use invalidation:** is the home of the consumption rule.
- **Build sequence.** With no key, nothing rotates, so SPEC-2 removes the rotation timer and the rotation admin surface from §18.
- **Playground OIDC login.** The `state` and PKCE `code_verifier` live in the existing `conn:oauth:state:{state}` store, single use with a 10-minute TTL. The cookie holds only the opaque `state`, and it stays because it binds the login to the browser. SPEC-3 is the home.
- **Connector OAuth.** §9.3 already requires only a random `state` stored in Redis and matched exactly, so §9.3 needs no edit. SPEC-3 corrects the §12.4 wording that calls the value an HMAC nonce and the §18.28 deliverable that calls it signed.

## Edge cases and accepted failure modes

- A replay past the row expiry answers `401 UPLOAD_TOKEN_EXPIRED` in every state (SPEC-1 **Check order:**).
- A replay on a session that passed `ready` and then reached a terminal state inside the TTL answers `409 INVALID_STATE_TRANSITION` rather than `410` (SPEC-1 **Single-use invalidation:**). This is accepted.
- The token `POST /v1/sessions/start` returns is consumed when issued (SPEC-1 **Single-use invalidation:**).
- A delegated child's row and a `derive_failure` row have no digest, so any token presented for either fails SPEC-1 **Authentication:**. Neither row has a token today, and both refuse the same way.
- A mid-session `upload_to_session` call is outside the checks (SPEC-1 **Authentication:**).
- The §11.1 upload caps hold per replica once a token verifies on every replica (SPEC-1(f)). This is accepted.
- The §11.5 replay of a stored finalize response precedes these checks, so "on every replica" in SPEC-1 does not describe a request that §11.5 answers. F-11.5.27 owns that ordering.
- A connector flow's `state` presented at the playground callback with a matching cookie is consumed and then refused, so the connector flow restarts. A caller can only do this to a `state` it already holds. This is accepted.

## Staged edits

### SPEC-1 · spec/07_session-lifecycle.md § 7.1 Normal Flow and § 7.4 Upload Safety, and spec/11_policy-and-controls.md § 11.1 Admission and Fairness

(a) In §7.1, replace the paragraph that begins "**`uploadToken` format and security properties.**" and ends "Properties:" with:

```markdown
**`uploadToken` format and security properties.** The `uploadToken` returned at step 8 is a short-lived, session-scoped secret. It is structured as `<session_id>.<expiry_unix_seconds>.<secret_hex>`, where `secret_hex` is 256 random bits in hexadecimal. The write that creates a session row for which a token is issued records the SHA-256 digest of the whole token and its expiry. The session row never holds the token. No signing key exists: every gateway replica verifies a token against the session row, which it reads from the primary ([§12.3](12_storage-architecture.md#123-postgres-ha-requirements) **Read replicas**) for each verification. Properties:
```

(b) In the same list, replace the **TTL:** bullet with:

```markdown
- **TTL:** The token expires at `session_creation_time + maxCreatedStateTimeoutSeconds` (default 300 s) — the same deadline that governs the `created` state. The expiry recorded on the session row is the enforced expiry. The digest covers the embedded expiry, so a token with an edited expiry fails the digest check. The gateway rejects any upload or finalize request bearing an expired token with `401 UPLOAD_TOKEN_EXPIRED`.
```

(c) Leave the **Session scope binding:** bullet unchanged. Replace the **Single-use invalidation:** bullet with:

```markdown
- **Single-use invalidation:** A session's token is consumed once the session has reached `ready`. A session that `POST /v1/sessions/start` creates has no upload window, so the token that call returns is consumed when it is issued. In `ready` or any later non-terminal state, a session's unexpired token returns `410 UPLOAD_TOKEN_CONSUMED`, on every replica. In `finalizing` or a terminal state, it reaches the [§15.1](15_external-api-surface.md#151-rest-api) precondition and returns `409 INVALID_STATE_TRANSITION`, so `UPLOAD_TOKEN_CONSUMED` always means finalization committed.
```

(d) Replace the **Cryptographic protection:** bullet with these two bullets:

```markdown
- **Authentication:** An upload or finalize request governed by the `uploadToken` that carries no token or a malformed token, or a token whose digest differs from the digest on the session row, or a token for a session row with no digest, is rejected with `401 UPLOAD_TOKEN_INVALID`. A mid-session upload through `upload_to_session` uses the caller's bearer ([§7.4](#74-upload-safety)) and is outside these checks.
- **Check order:** The gateway reads the session row, then rejects a missing or malformed token, then checks session scope, digest, expiry, and consumption in that order. These checks run ahead of the [§15.1](15_external-api-surface.md#151-rest-api) precondition, every write, and every pod operation. An expired token therefore returns `401 UPLOAD_TOKEN_EXPIRED` in every session state.
```

(e) In §7.4, in the paragraph that begins "**Upload authorization:**", replace the sentence "The gateway validates the token's HMAC signature, checks that it has not expired, confirms the embedded `session_id` matches the target session, and rejects any token that has already been consumed by a prior `FinalizeWorkspace` call." with:

```markdown
The gateway verifies the token against the session row in the order [Section 7.1](#71-normal-flow) **Check order:** states.
```

(f) In the §11.1 table, replace the Granularity cell of the `Concurrent uploads` row with `Per-session, global (each per replica)`, and the Granularity cell of the `Upload size` row with `Per-file, per-session (per replica)`. Each row stays one physical line and keeps the column padding.

### SPEC-2 · spec/15_external-api-surface.md § 15.1 and spec/18_build-sequence.md § 18.11 and § 18.31

(a) In §15.1, insert this row directly above the `UPLOAD_TOKEN_EXPIRED` row. The row stays one physical line. Leave the `UPLOAD_TOKEN_EXPIRED`, `UPLOAD_TOKEN_MISMATCH`, and `UPLOAD_TOKEN_CONSUMED` rows unchanged.

```markdown
| `UPLOAD_TOKEN_INVALID`        | `PERMANENT` | 401         | The upload token failed authentication. See [Section 7.1](07_session-lifecycle.md#71-normal-flow) **Authentication:**. |
```

(b) In §18.11, replace the bullet that begins "`uploadToken` HMAC issuance, validation, single-use invalidation," with:

```markdown
- `uploadToken` issuance, verification against the session row, and single-use invalidation per [§7.1](07_session-lifecycle.md#71-normal-flow).
```

(c) In §18.31, delete the bullet that begins "`uploadToken` HMAC operator-initiated rotation admin surface:".

(d) In §15.1, append this sentence to the paragraph that begins "**State-mutating endpoint preconditions.**":

```markdown
An upload or finalize request the `uploadToken` governs runs the [§7.1](07_session-lifecycle.md#71-normal-flow) **Check order:** first.
```

(e) In the §15.1 table under "**Derive-failure audit rows", delete `/upload`, `/upload-archive`, and `/finalize` from the first cell of the row that begins `POST /v1/sessions/{id}/interrupt`, and insert this row directly below it. The row stays one physical line.

```markdown
| `POST /v1/sessions/{id}/upload`, `/upload-archive`, `/finalize` | Refused at the token checks of [§7.1](07_session-lifecycle.md#71-normal-flow) **Check order:**, because no token was issued for the row. |
```

### SPEC-3 · spec/27_web-playground.md § 27.3.1, spec/15_external-api-surface.md § 15.1 playground auth table, spec/12_storage-architecture.md § 12.4, and spec/18_build-sequence.md § 18.28

(a) In §27.3.1 step 1, replace the sentence of the `GET /playground/auth/login` bullet that begins "The gateway generates a per-login `state`" with:

```markdown
The gateway generates a per-login `state` and PKCE `code_verifier`, writes them to the shared store under `conn:oauth:state:{state}` ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) with a 10-minute TTL, sets a short-lived, HttpOnly `lenny_playground_oidc_state` cookie that holds only the opaque `state` (TTL 10 min, `Path=/playground/auth/`), and redirects the browser to the configured OIDC provider's authorization endpoint. The cookie binds the login to the browser. A store fault follows the **Failure modes summary** row below, and the gateway sets no cookie.
```

(b) In the `GET /playground/auth/callback?code=…&state=…` bullet, replace "The gateway verifies `state` against the state cookie, performs the PKCE-protected token exchange with the provider, validates" with:

```markdown
The gateway compares `state` with the state cookie and then atomically consumes the `conn:oauth:state:{state}` entry, so each `state` is single use. A missing or mismatched cookie, an unknown, expired, or replayed `state`, or an entry a connector flow wrote ends the flow on the playground error page. A `state` store fault does the same with `error=oidc_state_unavailable`, the HTTP status on the final response to the browser is 503, and the gateway does not fall back to the cookie. The user restarts the login. After a successful consume the gateway performs the PKCE-protected token exchange with the provider using the stored `code_verifier`, validates
```

(c) In the paragraph that begins "The redirect query-param namespace", replace "the state cookie is cleared as part of the error redirect to prevent dangling PKCE state" with:

```markdown
the state cookie is cleared as part of the error redirect
```

In the same paragraph, replace "distinct from the **Failure modes summary** table below (which covers post-session-record failures at `POST /v1/playground/token`)" with:

```markdown
distinct from the **Failure modes summary** table below, which covers failures at `/playground/auth/login`, at `POST /v1/playground/token`, and on the requests a minted bearer authorizes
```

(d) In **7. Failure modes summary.**, insert this row directly below the row "Redis unavailable during per-request revocation check":

```markdown
| Redis unavailable at `GET /playground/auth/login` | 503 | `REDIS_UNAVAILABLE` | Retry the login |
```

(e) In §15.1, in the playground auth endpoint table, replace the description cell of the `/playground/auth/login` row. The row stays one physical line.

```markdown
Start the OIDC authorization-code flow. Stores the PKCE verifier and CSRF `state` in the shared store, sets a `lenny_playground_oidc_state` cookie (TTL 10 min) holding the opaque `state`, then 302-redirects to the configured OIDC provider. No request body. See [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange).
```

(f) In the §12.4 key table, in the `conn:oauth:state:{state}` row, replace the role cell "Connector-OAuth authorization-request state" with "OAuth authorization-request state". The row stays one physical line and keeps the literal key. In the same row:

- Replace "context for the connector OAuth flow, held" with "context for the connector OAuth flow and the web-playground OIDC login, held".
- Replace "The `state` value is a cryptographically unique HMAC nonce, so the key" with "The `state` value is random with at least 128 bits of entropy, so the key".
- Replace "See [§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc) (`state` parameter)." with "See [§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc) (`state` parameter) and [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange)."

(g) In the §12.4 paragraph that begins "DLQ keys and billing stream keys follow the same tenant prefix convention.", replace "the `conn:oauth:state:{state}` connector-OAuth key is platform-scoped (its `state` value is a cryptographically unique HMAC nonce whose knowledge is itself the credential)" with:

```markdown
the `conn:oauth:state:{state}` OAuth key is platform-scoped (its `state` value is random with at least 128 bits of entropy, and knowledge of it is itself the credential)
```

(h) In §18.28, in the bullet that begins "Full OAuth 2.1 connector flow", replace "signed `state`" with "random `state`".

## Spec files touched

- `spec/07_session-lifecycle.md`
- `spec/11_policy-and-controls.md`
- `spec/12_storage-architecture.md`
- `spec/15_external-api-surface.md`
- `spec/18_build-sequence.md`
- `spec/27_web-playground.md`
