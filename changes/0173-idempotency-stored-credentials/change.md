# Credentials in stored idempotency responses

Status: `In progress`, revision 1, 2026-11-25.

## Problem

The gateway stores the response of every idempotent request so that a retry with the same key replays it ([§11.5](../../spec/11_policy-and-controls.md#115-idempotency)). Some of those responses carry the `uploadToken` of [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow), a bearer credential that authorizes uploads into a session and its `FinalizeWorkspace`. The idempotency table therefore holds bearer credentials for 24 hours, in Postgres under `--postgres-dsn`, with no application-layer encryption. [§12.9](../../spec/12_storage-architecture.md#129-data-classification) does not classify idempotency records, so no control in the classification table applies to them.

Evidence from the tree:

- `cmd/lenny-gateway/httpsurface.go` wraps the gateway handler with `idempotencymw.Wrap` over one `idempotencymw.Store`: the Postgres store of `pkg/idempotency/idempotencymw/pgstore` when `--postgres-dsn` is set, and `idempotencymw.MemoryStore` otherwise. Its `AllowedPaths` admit `POST /v1/sessions`, `POST /v1/sessions/start`, `POST /v1/environments/{name}/sessions`, `POST /v1/sessions/{id}/finalize`, `POST /v1/sessions/{id}/start`, `POST /v1/sessions/{id}/resume`, `POST /v1/sessions/{id}/derive`, the `POST /v1/sessions/{id}/tool-use/` subtree, and `POST /mcp`.
- The same function hands that store to the MCP server through `mcp.IdempotencyConfig`, for the `idempotencyKey` argument of `lenny/create_session` and `lenny/delegate_task` (`pkg/gateway/platformtools/mcp/idempotency.go`, `dispatchIdempotent`).
- `idempotencymw.maybePersist` stores the captured body as written, and `dispatchIdempotent` stores the JSON of the `ToolResult` as returned.

Which stored responses carry a credential:

| Operation | Path or tool | Credential in the response |
| :-- | :-- | :-- |
| CreateSession | `POST /v1/sessions`, `POST /v1/environments/{name}/sessions` | `uploadToken` (`writeCreateSessionResponse` in `pkg/gateway/core/session/sessionserver/create.go`) |
| Create-and-start | `POST /v1/sessions/start` | `uploadToken` (the same writer, from `handleCreateAndStart`) |
| Derive | `POST /v1/sessions/{id}/derive` | `uploadToken` (`DeriveResponse` in `derive.go`) |
| CreateSession over MCP | `lenny/create_session` with `idempotencyKey`, or a `tools/call` of it in `POST /mcp` with the `Idempotency-Key` header | `uploadToken` inside the tool result's text content (`mcptools_register.go`) |
| FinalizeWorkspace, StartSession, Resume | `POST /v1/sessions/{id}/finalize`, `/start`, `/resume` | None. The body is the session envelope of `toResponse`, which holds no token. A `Resume` body can hold the task record, which is Confidential data. |
| Approve and deny | the `POST /v1/sessions/{id}/tool-use/` subtree | None. |
| SpawnChild | `lenny/delegate_task` with `idempotencyKey` | None. The result is the task handle: child session id, state, runtime, and depth. |

The replay endpoint `POST /v1/sessions/{id}/replay` also returns an `uploadToken`, and the middleware does not store it, because its path is not in `AllowedPaths`. No other tool result or REST response on the stored paths carries a token, a key, or a secret field.

The owner settled the remedy as decision 1: a response that carries a credential is not stored with it.

## Scope

The change covers every response the gateway's idempotency store holds, on the REST `Idempotency-Key` path and on the MCP `idempotencyKey` path, and the classification of idempotency records in §12.9.

It leaves out:

- The `lenny-ops` idempotency records of [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service), which use their own table (`ops_idempotency_keys`) and their own store (`pkg/ops/opsidem`). Decision 1 names the critical operations of §11.5.
- Values a client sends that a response echoes back, such as session `env` and `metadata`. They are the client's data, and the §12.9 classification of the record applies to them.
- The verification of an upload token against its session row, which the M3 readiness review proposed as a separate change.

## Terms

| Term | Meaning |
| :-- | :-- |
| Idempotency record | A row of the `idempotency_keys` table, or an entry of the in-memory store: the tenant, the key, the request hash, and the stored response ([§11.5](../../spec/11_policy-and-controls.md#115-idempotency)). |
| Upload token | The `uploadToken` of [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow), `<session_id>.<expiry_unix_seconds>.<hmac_hex>`, an HMAC-SHA256 under a key of the gateway's signing-key ring. |
| Signing-key ring | The active upload-token signing key and the previous keys still inside the rotation overlap window (`uploadtoken.KeyRing`). |
| Redaction reference | What a stored response holds in place of an upload token: `<session_id>.<expiry_unix_seconds>.redacted.<mark>`, where the mark is an HMAC of the token under the key that signed it. The token cannot be recovered from the reference, and the reference does not verify as a token. |

## High-level design

Before the gateway stores an idempotent response, it finds every upload token in the body and replaces it with a redaction reference. When it replays the stored response, it rebuilds each token: it signs the reference's session id and expiry again with each key of the signing-key ring, and keeps the candidate whose mark matches the reference. HMAC signing is deterministic, so the rebuilt token is byte-for-byte the token of the original response, and the replayed body equals the original. Only a key that signed the original token can reproduce its mark, so a reference that a client wrote into its own request data cannot produce a token.

When the key that signed the original has left the ring, no key reproduces the mark, and the replay returns the reference in the token's place. The original token would no longer verify in that case either, so the replay keeps the behavior of the original response.

The decision allows two remedies per operation: store the response without the credential and mint a fresh one on replay, or call `idempotencymw.DoNotStore`. Every operation that returns a credential creates a session, and `DoNotStore` would make a retry create a second session, which §11.5 exists to prevent. Every such operation therefore takes the first remedy.

## Requirements

None by identifier. The change edits §7.1 (Normal Flow), §11.5 (Idempotency), and §12.9 (Data Classification), whose pages (SESS, APICONV, and the security and compliance pages) are placeholders with no requirement identifiers yet.

## Detailed design

### SC-1 Redaction of upload tokens

`uploadtoken.Redactor`, built over the gateway's `KeyRing`, has two operations on a byte string:

- **Strip** replaces every substring of the form `<session_id>.<digits>.<64 lowercase hex>` with a redaction reference. When the substring verifies under a key of the ring, the mark is an HMAC-SHA256 of the token under that key, with a domain-separation prefix distinct from the token's own signing input. A token-shaped substring that verifies under no key is replaced with a reference whose mark no key reproduces, so a token from another source is not stored either.
- **Remint** replaces every redaction reference whose mark a key of the ring reproduces with the token that key signs for the reference's session id and expiry. It leaves any other reference unchanged.

Both operate on raw bytes, so they reach a token wherever the body carries it: a top-level JSON field of a REST response, the text content of an MCP tool result, and that text inside a JSON-RPC response. The token's characters need no JSON escaping, so the form is the same at every nesting level.

### SC-2 The stores

`idempotencymw.WithCredentials` wraps an `idempotencymw.Store` with a redactor of the interface type `idempotencymw.CredentialRedactor`, which has `Strip` and `Remint`. The wrapped store's `Put` stores the stripped response body, and its `Get` and `Claim` return a stored record's body reminted, so the replay branch of the REST middleware and the replay path of the MCP hook's `dispatchIdempotent` both receive the original body. `cmd/lenny-gateway/httpsurface.go` wraps the one store that the middleware and the MCP hook share with an `uploadtoken.Redactor` over the gateway's ring. The redaction sits at the store boundary rather than in the middleware and the MCP hook, so the MCP package receives no upload-token value, which the gateway wiring lint refuses for the platform tools seam.

### SC-3 Classification

§12.9 lists idempotency records as T3, Confidential, in Postgres, because a stored response holds session metadata, user identifiers, and, for `Resume`, task results, which the table classifies as Confidential. §11.5 states that a stored response holds no credential, and §7.1 states that the gateway persists no upload token.

## Worked example

`alice` sends `POST /v1/sessions` with `Idempotency-Key: k1`. The gateway creates session `0f7c…`, mints `0f7c….1764068400.9ab3…` under key `boot`, and answers 201 with that token. Before storing the response, the middleware replaces the token with `0f7c….1764068400.redacted.51d0…`. The connection drops before `alice` reads the answer, and she retries with `k1` and the same body. The middleware finds the record, signs `0f7c….1764068400` with `boot` again, finds that its mark matches, and replays the body with `0f7c….1764068400.9ab3…`, the token of the first answer. A database reader who copies the stored row gets the reference, which `POST /v1/sessions/0f7c…/upload` refuses with `401 UPLOAD_TOKEN_INVALID`.

## Limits, and alternatives rejected

- A replay rebuilds a token only on a gateway replica whose ring holds the key that signed it. Upload tokens verify on the same condition, so the replay keeps the behavior of the original token.
- The reference keeps the token's session id and expiry, which the response's `id` field and the session row already hold.
- Options (b), encrypting the stored body under the tenant key, and (c), classifying the records as Secret and relying on volume encryption, were not taken (decision 1).
- Minting a new token under the active key on every replay was rejected. After a key rotation, the new token's digest would differ from the digest on the session row, which `FinalizeWorkspace` marks consumed, so the replayed token would escape single-use invalidation, and the replayed body would differ from the original.

## Specification and non-specification changes

- `spec/07_session-lifecycle.md`: §7.1, **`uploadToken` format and security properties**, a persistence property.
- `spec/11_policy-and-controls.md`: §11.5, **Stored credentials.**
- `spec/12_storage-architecture.md`: §12.9, the default data-type mappings gain idempotency records.
- `pkg/uploadtoken`: the redactor.
- `pkg/idempotency/idempotencymw`: `CredentialRedactor` and `WithCredentials`.
- `cmd/lenny-gateway`: the wiring.
- `tests/registers/spec-section-citations.yaml`: the section-citation counts of the packages whose annotations change.

## Documentation

- `docs/api/rest.md` (client developer): the idempotency section states that a replayed response carries the same `uploadToken` and that the gateway does not store the token.
- `docs/operator-guide/security.md` (operator): the data classification section states that idempotency records are Confidential and hold no upload token.

## Tests

- Unit, `pkg/uploadtoken`: a stripped body holds no token and remints to the original bytes; a token inside JSON-encoded text is handled; a token signed by an overlap key remints under that key; a reference whose key left the ring stays a reference; a reference forged from a token's digest is not reminted; a token-shaped value that verifies under no key is stripped and not reminted; strip and remint run concurrently with key rotation. Cites §7.1 (Normal Flow) and §11.5 (Idempotency).
- Unit, `pkg/idempotency/idempotencymw`: the middleware over the wrapped store stores the stripped body and replays the reminted body; with no redactor the store is unchanged; a pending claim, an absent record, `Release`, and the inner store's errors pass through. Cites §11.5 (Idempotency).
- Unit, `pkg/gateway/platformtools/mcp`: `dispatchIdempotent` over the wrapped store stores the stripped tool result and replays the reminted one. Cites §11.5 (Idempotency).
- Component, `tests/component/stores`: through the middleware and the Postgres store, the `response_body` column of a create response holds no upload token, and the replay returns the original body. Cites §11.5 (Idempotency) and §12.9 (Data Classification).
- Contract, `tests/contract/rest_idempotency`: a replayed `POST /v1/sessions` returns the original `uploadToken`, and the in-memory record holds none. Cites §11.5 (Idempotency).
