# Non-spec changes — Upload tokens and playground OIDC state fail across gateway replicas

## Design (implementation-facing)

The code implements SPEC-1 and SPEC-3 at these seams.

- **Token checks.** `uploadtoken.Verifier.Verify` holds parse, scope, digest, and expiry. `verifyUploadToken` adds the missing-header check before them and the consumption check after them, and both call sites use that one function, so it alone defines the SPEC-1 **Check order:**.
- **Consumption.** `session.UploadTokenConsumed` is the one predicate for SPEC-1 **Single-use invalidation:**. Finalize performs no consume step, because the write to `ready` is the consumption.
- **Row reads.** `handleFinalize`, `admitUpload`, and the `finalizeUpload` recheck read through `s.readPrimary`.
- **OAuth state.** `w.oauthStateStore()` builds one `connectoroauth.StateStore` that the connector flow and the playground share.

**Commit sequence.** S4 changes the verification and leaves the `uploadtoken` constructors' signatures in place, with their ring and tracker arguments ignored, so their callers stay untouched and the F-7.1.31 fix lands at a green commit. S5 then deletes the key material, changes the signatures, and sweeps the callers. Each code step edits the test fixtures its change breaks. `## Testing` names the deliverable each test lands with.

## Staged code changes

### CODE-1 · `pkg/uploadtoken` issues random tokens and verifies against the row

Targets: `pkg/uploadtoken/uploadtoken.go`, `pkg/uploadtoken/uploadtoken_test.go`, `pkg/uploadtoken/rotator_test.go`, `pkg/api/v1/session/session.go`.

`Issuer.IssueDetailed` draws 32 bytes with `crypto/rand.Read` and returns `<session_id>.<expiry_unix_seconds>.<hex>`. `crypto/rand.Read` is called without an error branch, because under Go 1.25 it never returns an error. `upload_token_issuance_failed` remains reachable only through the empty-id and dot rejections. `ParsedToken.Digest` stays the SHA-256 hex of the whole token.

`Verify` changes as follows:

```go
// Verify checks token against the session row's id, digest, and expiry. It
// returns ErrInvalid for a malformed token, ErrSessionMismatch for a foreign
// session, ErrInvalid for an empty rowDigest or a digest mismatch, and
// ErrExpired once rowExpiry has passed, checked in that order.
// spec: §7.1 (uploadToken format and security properties)
func (v *Verifier) Verify(token, sessionID, rowDigest string, rowExpiry time.Time) error
```

- Parse requires three non-empty dot-separated segments, an integer second segment, and a hexadecimal third segment.
- The digest comparison uses `subtle.ConstantTimeCompare`.
- The expiry compared is `rowExpiry`. The token's second segment is not read for enforcement.
- `ErrConsumed` stays in the package as the sentinel `verifyUploadToken` returns and `writeUploadTokenError` maps. `Verify` no longer returns it.

`NewIssuer` and `NewVerifier` keep their signatures in this step and ignore the ring and tracker. CODE-3 removes those parameters. Delete `sign`, `computeHMAC`, `parseAndVerifyHMAC`, and `(*KeyRing).keys`, which lose their last callers here, and delete `TestRotatorPreservesOverlapVerification_spec_7_1_13` and `TestRotatorSweepsExpiredOverlapKeys_spec_7_1_13`, because a row check has no key overlap to assert.

Rewrite the package comment: the format, the fact that a token authenticates by digest against the session row, and the checks `Verify` makes. Its invariants list does not claim that the package enforces single use.

In `pkg/api/v1/session/session.go`, add the predicate beside `IsTerminal`:

```go
// UploadTokenConsumed reports whether a session in state s has consumed its
// uploadToken. A terminal row and a finalizing row fall through to the §15.1
// precondition. A state added later reads as consumed.
// spec: §7.1 (uploadToken format and security properties)
func UploadTokenConsumed(s State) bool {
	return !IsTerminal(s) && s != StateCreated && s != StateFinalizing
}
```

### CODE-2 · The session server verifies against the primary row at both call sites

Targets: `pkg/gateway/sessionserver/upload.go`, `pkg/gateway/sessionserver/finalize_handler.go`, `pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/sessionserver/session_response.go`, `pkg/gateway/session/sessionstore/sessionstore.go`, `pkg/gateway/externalapi/errorclassify/errorclassify.go`, `cmd/lenny-gateway/flags.go`.

`verifyUploadToken` takes the row:

```go
func (s *Server) verifyUploadToken(r *http.Request, row sessionstore.Session) error {
	if s.uploadVerifier == nil {
		return nil
	}
	tok := r.Header.Get("X-Lenny-Upload-Token")
	if tok == "" {
		return uploadtoken.ErrInvalid
	}
	if err := s.uploadVerifier.Verify(tok, row.ID, row.UploadTokenDigest, row.UploadTokenExpiry); err != nil {
		return err
	}
	if session.UploadTokenConsumed(row.State) {
		return uploadtoken.ErrConsumed
	}
	return nil
}
```

Rewrite its doc comment to state that it verifies against the session row and returns `ErrConsumed` from the row state. Keep the nil-verifier opt-out.

- `admitUpload`: replace `s.store.Get` with `s.readPrimary`. The not-found and error branches stay, so a primary read error answers 500 and refuses the request. Pass `row` to `verifyUploadToken`.
- `finalizeUpload`: read the §7.4 recheck through `s.readPrimary`, and on a read error release the reservation residual, soft-delete the blob as the `UPLOAD_CHANNEL_CLOSED` branch does, and answer as `admitUpload` maps the read error, so no read outcome falls through to success.
- `handleFinalize`: move the row read ahead of the token check, read through `s.readPrimary`, and keep its 404 and 500 mapping. Pass `row` to `verifyUploadToken`. The order becomes row read, token check, `session.Validate`, and then everything else. Plan binding, every write, and every pod RPC stay behind the check.
- `handleFinalize`: delete the `ConsumeDigest` block and the `uploadTokenDigest` and `uploadTokenExpiry` locals, and remove the `log` import. The audit `Detail` keeps `updated.UploadTokenDigest`.
- `handleFinalize` doc comment: state that the `finalizing` to `ready` write consumes the token, and that the upload-channel close, status change, plan warnings, and audit row run after that write. Delete the sentence saying the §7.4 check runs before the row is read. Rewrite the inline comment above the token check.
- `sessionserver.go`: replace the default-issuer seed block with `s.uploadIssuer = uploadtoken.NewIssuer(nil, s.clock)` and remove the `crypto/rand` import. In the `UploadTokenIssuer` and `UploadTokenVerifier` option comments, replace the random-key, process-restart, and same-KeyRing text with a statement that the row authenticates a token.
- `session_response.go` and `sessionstore.go`: rewrite the `UploadToken`, `UploadTokenDigest`, and `UploadTokenExpiry` comments, which name HMAC or the tracker. The digest and expiry are the §7.1 verification authority.
- `errorclassify.go`: add `"UPLOAD_TOKEN_INVALID": {CategoryPermanent, false}, // spec: §15.1 (Error code catalog)`.
- `flags.go`: in the `upload-max-concurrent-per-session` help, replace "§11.1 per-session concurrent-upload admission cap" with "§11.1 per-session (per-replica) concurrent-upload admission cap". In the `upload-max-bytes-per-session` help, replace "§11.1 per-session cumulative upload-size cap (bytes). The sum of all uploads in a session" with "§11.1 per-session (per-replica) cumulative upload-size cap (bytes). The sum of a session's uploads on the replica". In the `sessionserver.go` `MaxUploadBytesPerSession` option comment, replace "the sum of all uploads in a session" with "the sum of a session's uploads on the replica".

### CODE-3 · The key material and the gateway wiring are removed

Targets: `pkg/uploadtoken/uploadtoken.go`, `pkg/uploadtoken/rotator.go` (delete), `pkg/uploadtoken/rotator_test.go` (delete), `pkg/uploadtoken/uploadtoken_test.go`, `pkg/gateway/sessionserver/sessionserver.go`, `cmd/lenny-gateway/token_signing.go`, `cmd/lenny-gateway/workers.go`, `cmd/lenny-gateway/wiring_fields.go`, `cmd/lenny-gateway/llmproxy.go` (buildStores doc comment), and every caller the sweep below finds.

In `pkg/uploadtoken/uploadtoken.go`, delete `SigningKey`, `KeyRing`, `ConsumedTracker`, `MemoryTracker`, `Consume`, `ConsumeDigest`, `ParsedToken.KeyID`, `DefaultRotationInterval`, and `DefaultOverlapWindow`. The constructors become `NewIssuer(clock func() time.Time) *Issuer` and `NewVerifier(clock func() time.Time) *Verifier`.

buildTokenSigningStores keeps `uploadtoken.NewIssuer(nil)` and `uploadtoken.NewVerifier(nil)` and loses the seed, ring, tracker, and rotator, including the rotation comment block at the top of the step. Rewrite its doc comment so it no longer names a key ring or rotator. Remove the `crypto/rand` import, whose only use is the seed. Remove `go w.uploadRotator.Run`, the tracker sweep goroutine, their comments, and the `pkg/uploadtoken` import from workers.go. Remove `ring`, `uploadTracker`, and `uploadRotator` from wiring_fields.go, which keeps the `uploadtoken` import for the issuer and verifier fields. `w.ring` has no reader today. In the llmproxy.go buildStores doc comment, replace `uploadToken key ring` with `uploadToken issuer and verifier`.

Sweep the callers from a list computed in this step rather than a stored count:

```bash
grep -rlE 'uploadtoken\.(New|SigningKey|KeyRing|MemoryTracker)' pkg cmd sdks tests --include='*.go'
```

### CODE-4 · The connector OAuth state signer is removed

Targets: `pkg/connectoroauth/state.go`, `pkg/connectoroauth/state_test.go`, `pkg/connectoroauth/pkce.go`, `pkg/connectoroauth/flow.go`, `pkg/connectoroauth/flow_test.go`, `pkg/connectoroauth/redis_state_store.go`, `pkg/gateway/externalapi/admin/connector_oauth.go`, `pkg/gateway/externalapi/admin/connector_oauth_test.go`, `pkg/gateway/externalapi/admin/routes.go`, `cmd/lenny-gateway/credentialsurface.go`, `tests/tier4_integration/connector_oauth_test.go`.

- `state.go`: delete `StateSigner`, `NewStateSigner`, `Rotate`, `Mint`, `Verify`, `SigningKey`, `computeStateHMAC`, `ErrStateMalformed`, and `ErrStateSignatureInvalid`. Add:

  ```go
  // NewState returns a random §9.3 `state` value: stateNonceBytes of crypto/rand
  // entropy in unpadded base64url.
  func NewState() string
  ```

- `connector_oauth.go`: delete the `ConnectorOAuth.StateSigner` field. The authorize handler calls `connectoroauth.NewState()` and drops the mint error branch. The callback handler deletes the `Verify` block, so `StateStore.Consume` is the single authority. `CONNECTOR_OAUTH_STATE_INVALID` is retired. A forged state answers `400 CONNECTOR_OAUTH_STATE_UNKNOWN` through the existing default branch, which stays as it is (see the summary's **Defects in the shipped tree that this proposal does not stage**).
- `credentialsurface.go`: remove the `stateSeed` and `stateSigner` block, the `StateSigner: stateSigner` field in the `admin.ConnectorOAuth` literal, and the `crypto/rand` import.
- `routes.go`: the registration comment changes from "the signed `state`" to the single-use `state` consumed from the store.
- Remove every signature reference: `connector_oauth.go` type doc, response-field comments, authorize doc, and callback doc; `state.go` `stateNonceBytes`, `ErrStateUnknown`, and `FlowContext` comments; `flow.go`; `pkce.go`; `redis_state_store.go`; and the `connector_oauth_test.go` fixture comment. The `flow_test.go` literal `signed-state-value` may be renamed in the same edit.
- `tests/tier4_integration/connector_oauth_test.go`: fix the header comments and the forged-state comment, which says the callback is refused because signature verification fails. The comment now says it is refused because no stored flow exists, and the test asserts `CONNECTOR_OAUTH_STATE_UNKNOWN` in addition to the 400.
- `connector_oauth_test.go`: remove the fixture signer. Merge `TestConnectorOAuthCallbackForgedStateRejected` and `TestConnectorOAuthCallbackUnknownStateRejected` into one test that sends a random unstored `state`, expects `CONNECTOR_OAUTH_STATE_UNKNOWN`, no provider exchange, no stored credential, and no store write.
- `state_test.go`: delete the signer and rotation tests, and add a `NewState` test asserting at least 128 bits of entropy, base64url encoding, and distinct values.

### CODE-5 · Playground OIDC state moves to the shared `StateStore`

Targets: `pkg/gateway/mcpfabric/playground/auth.go`, `pkg/gateway/mcpfabric/playground/crypto.go`, `pkg/gateway/mcpfabric/playground/playground.go`, `cmd/lenny-gateway/credentialsurface.go`, `cmd/lenny-gateway/httpsurface.go`, `cmd/lenny-gateway/wiring_fields.go`.

**Handler.**

- `playground.go`: add `OAuthState connectoroauth.StateStore` to `Options`, documented as required in oidc mode and ignored otherwise, and store it as `h.oauthState`. `pkg/connectoroauth` imports no other Lenny package, so this adds no cycle.
- `auth.go`: delete `stateCookieValue`. Rewrite the `oidcStateCookie` comment to say the cookie carries the opaque `state`. Rewrite the `handleCallback` doc comment to match the sequence below.
- `crypto.go`: delete `stateSecret`, `mustRandom`, `sealState`, `openState`, and the imports they leave unused.

`handleLogin` runs in this order:

1. Extend the guard to `h.oidc == nil || h.oauthState == nil`, answering the existing 503 `LENNY_PLAYGROUND_OIDC_UNAVAILABLE`, so an unwired store cannot panic.
2. Generate `state`, the verifier, and the challenge.
3. Resolve `h.oidc.AuthorizationURL`. A failure answers the existing 503.
4. Call `h.oauthState.Put(state, connectoroauth.FlowContext{CodeVerifier: verifier, CreatedAt: h.now()}, stateCookieTTL)`. A failure answers JSON `503 REDIS_UNAVAILABLE`.
5. Set the cookie with `Value: state`, keeping its attributes. Redirect.

The order of steps 3 and 4 is required: a provider outage must write no key to Redis on this anonymous route. The cookie is set after the write so a failed login sets no cookie.

`handleCallback` runs in this order:

1. The mode check, `clearCookie`, and the `code` and `state` check stay.
2. A missing cookie redirects `oidc_state_missing`.
3. A cookie value that differs from `state` redirects `oidc_state_mismatch`.
4. The nil check on `h.oidc`, `h.sessions`, and `h.oauthState` moves here, ahead of `Consume`, so an unconfigured handler burns no state. It keeps its JSON 503.
5. Call `h.oauthState.Consume(state, h.now())`. The cookie comparison stays ahead of this call, so a request without the cookie consumes nothing.

| `Consume` result | Redirect code |
|:--|:--|
| `ErrStateExpired` | `oidc_state_expired` |
| `ErrStateUnknown`, `ErrStateConsumed` | `oidc_state_invalid` |
| any other error | `oidc_state_unavailable` |
| success with a non-empty `ConnectorID` | `oidc_state_invalid` |

Every row above goes through `h.redirectAuthError`. A store fault never writes JSON from this handler, because a browser receives the callback.

6. `Exchange` uses the stored `fc.CodeVerifier`. The rest of the handler is unchanged.

`handleAuthError` writes status 200 for every code today. SPEC-3 requires 503 on the final response for `oidc_state_unavailable`, so it writes 503 for that code and 200 otherwise.

**Wiring.** Add a memoised helper and a field `oauthState connectoroauth.StateStore` on the wiring fields:

```go
// oauthStateStore returns the one §9.3 and §27.3.1 OAuth state store. Under
// Redis it survives a restart and resolves on any replica. The memory store is
// the single-process fallback, and the watchdog sweeps it.
func (w *gatewayWiring) oauthStateStore() connectoroauth.StateStore {
	if w.oauthState != nil {
		return w.oauthState
	}
	if w.redisClient != nil {
		w.oauthState = connectoroauth.NewRedisStateStore(w.concernRedis.For(storerouter.RedisConcernCachePubSub))
		return w.oauthState
	}
	mem := connectoroauth.NewMemoryStateStore()
	w.connectorStateStore = mem
	w.oauthState = mem
	return mem
}
```

`w.oauthState` is an interface and receives a non-nil value on each path, so it never holds a typed-nil `*MemoryStateStore`.

- `credentialsurface.go`: replace the store selection with `w.oauthStateStore()`. Delete the local `connectorStateStore` and the assignment `w.connectorStateStore = connectorStateStore`, which would otherwise overwrite the field with nil. Keep the startup log line for the Redis path in the helper.
- `httpsurface.go`: call `w.oauthStateStore()` only when `pgCfg.AuthMode` is `playground.AuthModeOIDC`, and pass the result as `Options.OAuthState`.

## Staged schema, chart, and migration changes

In the `uploadLimits` comment of `charts/lenny/values.yaml`, replace "Cap on in-flight uploads" with "Per-replica cap on in-flight uploads" under `maxConcurrentPerSession`, and replace "Cap on the cumulative size" with "Per-replica cap on the cumulative size" under `maxBytesPerSession`. This edit lands with CODE-2. No schema or migration change is staged.

## Staged docs changes

### DOCS-1 · Client documentation

Targets: `docs/client-guide/session-lifecycle.md` (a. Create Session, State Transition Preconditions), `docs/client-guide/wire-format.md` (Upload token format line), `docs/reference/glossary.md` (Upload Token entry), `docs/reference/error-catalog.md`, `docs/client-guide/error-handling.md`, `docs/api/mcp.md` (`upload_files` and `finalize_workspace` error tables), `docs/api/rest.md` (finalize error table).

- `session-lifecycle.md`: replace the first sentence of the `uploadToken` paragraph with "The `uploadToken` is a short-lived, session-scoped secret." Below the sentence beginning "Calling an endpoint in an invalid state", add "Upload and finalize requests check the `uploadToken` first, so a token error takes precedence over this response."
- `wire-format.md`: change the format to `<session_id>.<expiry_unix_seconds>.<secret_hex>`. Add no opacity statement.
- `glossary.md`: replace the first sentence with "A short-lived, session-scoped secret issued at session creation." and use the same format.
- `error-catalog.md`, in the PERMANENT table, above `UPLOAD_TOKEN_EXPIRED`:

  ```markdown
  | `UPLOAD_TOKEN_INVALID` | 401 | The upload token is missing or malformed, does not match the digest recorded on the session, or was presented for a session with no token. | Send the `uploadToken` returned when the session was created. |
  ```

- `error-handling.md`, above `UPLOAD_TOKEN_EXPIRED`, the same row with its columns:

  ```markdown
  | `UPLOAD_TOKEN_INVALID` | `PERMANENT` | 401 | No | The upload token is missing or malformed, does not match the digest recorded on the session, or was presented for a session with no token. | Send the `uploadToken` returned when the session was created. |
  ```

- `mcp.md`, `upload_files` and `finalize_workspace` tables: add `UPLOAD_TOKEN_INVALID` ("The upload token is malformed or does not match the session"), `UPLOAD_TOKEN_EXPIRED`, `UPLOAD_TOKEN_MISMATCH`, and `UPLOAD_TOKEN_CONSUMED`, skipping any already listed. Do not say "missing", because the tool answers `VALIDATION_ERROR` for that case.
- `mcp.md` and `rest.md` 409 rows: reword `INVALID_STATE_TRANSITION` to say the session is finalizing or has ended, and state that a valid token on a session past finalization returns `UPLOAD_TOKEN_CONSUMED`. This covers the `upload_files` row, the `finalize_workspace` row, and the rest.md finalize row.
- `rest.md` finalize table: add the four token codes.

Leave the `UPLOAD_TOKEN_CONSUMED` catalog rows unchanged. They stay true, and the row text `use the caller's bearer token` is pinned by the tier-11 test `delegation_mints_no_token_docs_test.go`.

### RECORDS-1 · Findings and plan records

Targets: `BUILD-GAPS.md` F-7.1.31, F-27.3.16, and F-15.1.69; `BUILD-GAPS.md` new findings F-9.3.18 and F-27.3.17 at the next free ids (verify at landing); `BUILD-GAPS.md` new open findings for each entry of the summary's **Defects in the shipped tree that this proposal does not stage**; `gateway-runtime-comms-remediation.md` §10.2 "Shared gateway key-source proposal" entry; the §10.2 F-27.3.16 and F-15.1.69 checkbox items; the §10 Redis audit table row for `pkg/connectoroauth`.

1. Close F-7.1.31, F-27.3.16, and F-15.1.69 with appended Resolution text. Leave the existing Spec, Evidence, Gap, Suggested resolution, and dated Note paragraphs unedited. F-7.1.31's resolution states that the 2026-10-31 "kind stack runs one replica" sentence was wrong: the chart default is 2, `e2e-values.yaml` does not override it, and `ensureGatewayReplicas` scales up. It also records the S9 red run. F-27.3.16's resolution states that about (N-1)/N logins failed rather than one in N.
2. File the connector signer finding as F-9.3.18 under §9.3 and §10.1.1 (Medium, opt-in reach, because the chart renders no callback flag or environment variable), citing the per-process seed in `credentialsurface.go` and the `Verify` call ahead of `Consume`. Record it as closed by 0114. Do not edit closed F-9.3.5.
3. File the summary's `Consume` fault mapping entry as an open finding, stating that §9.3 is silent on a store fault and the fix needs spec text, and noting that T-9.3.6's suggested `CONNECTOR_OAUTH_STATE_*` assertion would change with a 503 mapping. Queue it as an open item in plan §10.2, and change the `pkg/connectoroauth` failure-path cell from met to unmet, naming that finding.
4. File the other entries of that summary section as open findings, each queued in plan §10.2.
5. File F-27.3.17 as an open finding on the anonymous playground login Redis write. `/playground/auth/login` is unauthenticated, each request that reaches the store writes one key retained for the 10-minute TTL, the rate limits are off by default and the per-user and per-tenant scopes need a principal, and the shared Redis instance runs `noeviction`, so a flood can bring it to `maxmemory`, where writes for leases and budgets fail. The exposure is opt-in (`oidc` mode only) and TTL-bounded. Queue it in plan §10.2.
6. Tick the plan's key-source entry, keeping its name, and append that 0114 resolved it with the session row and the shared state store and created no shared key. Tick the F-27.3.16 and F-15.1.69 items, and add a closed line for F-9.3.18. Do not edit the dated 2026-11-01 owner-decision and pending-work records.

## Testing

Each test carries `// spec:` annotations for the sections named, and every tier-2-and-higher test carries a `// diagnosis:` comment. Each item names the deliverable it lands with. Items that name TEST-1 land in that step.

**State list.** Every table over session states runs over `append(session.AllStates(), session.StateResuming)`, because `AllStates()` omits `resuming` and the resume driver writes it to rows.

1. **Row verification** (CODE-1; tier 1; §7.1). `Verify` table: empty, two-segment, non-integer expiry, and non-hex secret tokens give `ErrInvalid`; an empty row digest gives `ErrInvalid`; a client-edited expiry segment gives `ErrInvalid`; a syntactically valid token with a guessed secret naming a foreign session gives `ErrSessionMismatch`, which pins the move from 401 to 403; the boundary instant at `rowExpiry` is admitted and one second later gives `ErrExpired`. Two issued tokens for one session differ.
2. **Consumption predicate** (CODE-1; tier 1; §7.1). `UploadTokenConsumed` table over the state list: false for `created`, `finalizing`, and every terminal state, and true otherwise, with `resuming` true.
3. **Handler state table** (CODE-2; tier 1; §7.1, §15.1). Finalize and upload over the state list with a valid token, asserting the SPEC-1 rule's status and code for each row. The assertion that discriminates is 410 on `ready` and 409 on a terminal row. Rewrite `TestUploadRejectsWrongState` to seed a digest and assert 410 `UPLOAD_TOKEN_CONSUMED` on `ready`, with added cases in `finalizing` and a terminal state asserting 409 `INVALID_STATE_TRANSITION`. Audit every verifier-wired test that asserts 409 on a state past `created`.
4. **Check order** (CODE-2; tier 1; §7.1, §15.1). One case per adjacent pair in SPEC-1 **Check order:**: a foreign-session token with a bad digest gives 403, a token with a bad digest past expiry gives 401 `UPLOAD_TOKEN_INVALID`, and an expired token on `ready` gives 401 `UPLOAD_TOKEN_EXPIRED`. Each case asserts zero writes. A missing row answers 404. A row whose digest is empty answers 401, and a `derive_failure` row is one such case for upload and for finalize. The `create-and-start` token answers 410 on finalize and on upload. In `TestDeriveFailurePersistsAuditRow_spec_7_1_derive_rule_2` (`derive_failure_audit_test.go`), drop `finalize` from the action-endpoint loop, which wires no verifier and so asserts a status §15.1 no longer states.
5. **Primary read** (CODE-2; tier 1; §7.1, §7.4, §12.3). A store whose `Get` returns `created` and whose `GetPrimary` returns `ready` makes upload and finalize answer 410, which fails if either site reads `Get`. A `GetPrimary` error at either site answers 500 and writes nothing. At the post-Put recheck, with `Get` and the admission `GetPrimary` returning `created`, a second `GetPrimary` returning `ready` answers 410 `UPLOAD_CHANNEL_CLOSED`, one returning `ErrNotFound` answers 404, and one returning an error answers 500, and each case soft-deletes the blob and releases the reservation.
6. **Store wrappers** (CODE-2; tiers 1, 4, 7a). `ReadPrimary` falls back to `Update` on a store without `GetPrimary`, which corrupts `Update` counters and hooks. Add a forwarding `GetPrimary` to each embedding store that drives finalize or upload: `finalize_upload_token_test.go`, `finalize_exit_guard_internal_test.go`, `finalize_admission_internal_test.go`, `finalize_race_test.go`, `terminal_context_internal_test.go`, `start_pod_test.go`, `credential_lookup_failclosed_internal_test.go`, `tests/tier4_integration/finalize_admission_race_test.go`, and `tests/tier7a_load_local/finalize_admission_race_test.go`. In the tier-4 hook store, `GetPrimary` fires `onNextGet`. Port `barrierGetStore` in `finalize_admission_internal_test.go` to synchronize on `GetPrimary`. In `tracing_spans_test.go`, rework `vanishingStore`, which separates the access read from the handler read by whether the context is bounded, because `readPrimary`'s context is always bounded. In `finalize_upload_token_test.go`, change both zero-writes assertions to count primary reads and zero writes.
7. **Removed consume path** (CODE-2; tier 1). Delete `TestFinalizeConsumeFailureKeepsReady_spec_7_1`, the consume test in `finalize_race_test.go`, and `raceTracker`. The race fixture sends the token from the create response, or seeds the row's digest from `IssueDetailed`.
8. **Error classification** (CODE-2; tier 1; §15.1). Add an `UPLOAD_TOKEN_INVALID` case to `pkg/gateway/externalapi/errorclassify/errorclassify_test.go`.
9. **Cross-replica, in process** (CODE-2; tier 1; §7.1, §10.1.1, §11.1). New `pkg/gateway/sessionserver/upload_token_cross_replica_test.go`: two `Server` values over one store, each with its own issuer and verifier and sharing only the row. Mint on A; upload and finalize on B; replay on A gives 410. With `MaxUploadBytesPerSession` set on both servers, a session's uploads up to the cap on A are admitted, and uploads up to the cap on B are then admitted.
10. **Randomized interleavings** (CODE-2; tier 1 with `-race`; §7.1, §15.1). In the same file, random interleavings of uploads, finalizes, and terminates across both servers, with a seed logged on failure. Invariants: at most one finalize answers 200. No finalize or upload whose admitting primary read occurs after the ready commit succeeds, and the store hook records that ordering. An upload admitted earlier may answer 2xx only if its post-Put recheck preceded the commit, and otherwise it answers 410 `UPLOAD_CHANNEL_CLOSED`. A session terminated first produces no successful finalize.
11. **Connector state** (CODE-4; tiers 1, 4; §9.3). The test edits CODE-4 lists. In `connector_oauth_test.go`, authorize on one router, call back on a second router over the fixture's stores, and replay on the first for `CONNECTOR_OAUTH_STATE_REPLAYED`. In `tests/tier4_integration/connector_oauth_test.go`, two gateways on one `--postgres-dsn` and one `--redis-url`, following `keySetReplicaArgs`, authorize on A and call back on B, because the connector registry and the state store are per process without those flags. A gateway started after A stops completes a flow A began.
12. **Playground, in process** (CODE-5; tier 1; §27.3.1). Extend `pkg/gateway/mcpfabric/playground/cross_replica_test.go`: login on handler A and callback on handler B sharing a store, and a callback on a fresh handler after a simulated restart, each over `MemoryStateStore` and over `RedisStateStore` on miniredis. Non-happy cases: replay gives `oidc_state_invalid`; a cookie differing from `state` gives `oidc_state_mismatch` and leaves the entry unconsumed; an unwired store gives 503 at login with no panic, and burns no state at callback; a provider failure at login writes no key; a `Put` failure gives 503 `REDIS_UNAVAILABLE` and sets no cookie; a `Consume` fault gives a 302 to `oidc_state_unavailable`, and following it renders 503; a store entry with a `ConnectorID` gives `oidc_state_invalid`; a memory entry past its TTL gives `oidc_state_expired`, and a Redis entry past its TTL gives `oidc_state_invalid`. In `pkg/gateway/externalapi/admin/connector_oauth_test.go` a playground entry at the connector callback gives 400 `CONNECTOR_OAUTH_INVALID_CALLBACK`.
13. **Playground fixtures and store wiring** (CODE-5; tier 1; §9.3, §27.3.1). Wire a `MemoryStateStore` in `playground_test.go`, `oidc_discovery_test.go`, `cookie_opaque_test.go`, `token_capabilityttl_test.go`, and `token_currentexp_test.go`. In `sessionrecord_test.go`, the helper reads `state` from the cookie value directly. New `cmd/lenny-gateway/oauth_state_store_test.go` runs `buildCredentialSurface` with the connector flow enabled. With no Redis client, `w.connectorOAuth.StateStore`, `w.connectorStateStore`, and a later `oauthStateStore` call are one instance, which fails if the assignment CODE-5 deletes survives. With a Redis client on miniredis, `w.connectorOAuth.StateStore` and a later call are one instance and `w.connectorStateStore` is nil. With the flow disabled, `w.connectorStateStore` is nil until the first `oauthStateStore` call.

### TEST-1 · Cross-replica and contract tests

Targets: `tests/tier3_contract/rest_mcp_consistency/finalize_upload_token_parity_test.go`, `tests/tier4_integration/playground_oidc_login_test.go`, `tests/tier4_integration/upload_token_two_replica_test.go` (new), `tests/tier5_e2e_kind/upload_token_two_replica_kind_test.go` (new), `tests/tier7a_load_local/finalize_admission_race_test.go`, `tests/tier8_chaos/playground_oidc_redis_outage_test.go` (new), `tests/tier9_security/finalize_upload_token_test.go`, `tests/tier9_security/oauth_state_binding_test.go` (new), `tests/testinfra/sessiondriver/sessiondriver.go`, a new tier-11 docs test under `tests/tier11_docs/`, `tests/spec-map.json`.

14. **Catalog parity** (tier 3; §15.1). In `finalize_upload_token_parity_test.go`, drop the `UPLOAD_TOKEN_INVALID` exemption and its comment, so the case asserts full category and retryable parity for every code.
15. **Two gateway processes** (tier 4; §7.1, §10.1.1). `upload_token_two_replica_test.go` starts both gateway processes with the same `--postgres-dsn`, following `keySetReplicaArgs`, because the default memory store is per process. It mints on A, uploads and finalizes on B, and replays on A for 410. It simulates a restart by stopping one process and starting another on the same DSN. A finalize with no upload is one case.
16. **Playground across processes** (tier 4; §27.3.1). In `playground_oidc_login_test.go`, two gateways share Redis: login on A, callback on B. Both hops carry one `Host` value through a new `playgroundHop` host parameter, because `callbackURL` derives `redirect_uri` from the request host and the OIDC stub refuses a redemption whose `redirect_uri` differs from the authorization request.
17. **Kind** (tier 5; §7.1, §10.1.1). `upload_token_two_replica_kind_test.go` uses `twoReplicaDrivers` to mint on one pod, finalize on the other, and replay. Extend `tests/testinfra/sessiondriver/sessiondriver.go` with an upload method carrying the token and a finalize result exposing status and code, so the test asserts 410 without parsing an error string.
18. **Security** (tier 9; §7.1, §7.4). In `finalize_upload_token_test.go`, seed tokens from the create response, and assert that a forged token, a foreign-session token, and a replayed token leave the row unchanged and run no pod work.
19. **Docs** (tier 11). A docs check pins the new `UPLOAD_TOKEN_INVALID` catalog rows, the absence of `hmac_hex` from the three token pages, and the absence of "HMAC-SHA256" from `session-lifecycle.md` and `glossary.md`.
20. **Spec map.** Update `tests/spec-map.json` for the added and deleted tests.
21. **Consumption race** (tier 7a; §7.1, §15.1). In `tests/tier7a_load_local/finalize_admission_race_test.go`, add a case with a `lenny-test stress` budget that races finalize calls carrying one valid token across two `Server` values over one `transitionStore`, each `Server` with its own verifier, and that asserts exactly one 200, 409 `INVALID_STATE_TRANSITION` or 410 `UPLOAD_TOKEN_CONSUMED` for every other call, and one `created` to `finalizing` transition. Update the file header, which states that the cases wire no verifier.
22. **Playground Redis outage** (tier 8; §27.3.1). `playground_oidc_redis_outage_test.go` points `--redis-url` at a `tcpfault` proxy, because `containers.Redis.Stop` terminates the container and cannot bring it back. With the proxy cut, login and callback give the SPEC-3(b) and (d) store-fault outcomes. After `Restore`, with no gateway restart, a new login and its callback complete, and so does the callback of another login begun before the cut.
23. **OAuth state binding** (tier 9; §9.3, §27.3.1). `oauth_state_binding_test.go` drives the real gateway routes on one Redis, following `TestLivePlaygroundOIDCModeSignsInThroughTheIdP_spec_27_3_1` and `TestOAuthConnector`. A playground callback with no cookie, and one with the cookie of another login, each establish no session. The matching callback then succeeds with the same code, which is the assertion that discriminates, because the stub redeems a code once and the entry is single use. A replayed `state` establishes no session. An entry from the other flow is refused at each callback with no provider exchange. A forged connector `state` reaches no provider token endpoint while a flow for that connector is pending.

**Red evidence.** Items 15 and 17 and the tier-5 eager-claim tests use HTTP surfaces only, so they run against an older tree. Before S9 lands, run them in a worktree of the S3 commit, the last commit before row verification. Record the results, including skips, in RECORDS-1 as the answer to the problem statement's open question on the eager-claim tests, then run them green on the S9 tree. Tier-1 tests cannot show red, because the `Verify` signature changes.

## Edge cases and accepted failure modes

- `ReadPrimary` adds one primary read to upload admission and one to the post-Put recheck. A primary read error at either refuses the request, and at the recheck the client re-sends the blob.
- A row deleted between `requireSessionAccess` and the handler's read answers 404 ahead of the token check.
- A forged connector `state` now reaches Redis at the cost of one script call and no write.
- Multi-replica behavior with no `--redis-url` is unchanged in kind: the memory store is single process, and the gateway logs that posture at startup.

## Files touched on application (non-spec)

- `pkg/uploadtoken/uploadtoken.go`, `pkg/uploadtoken/uploadtoken_test.go`, `pkg/uploadtoken/rotator.go`, `pkg/uploadtoken/rotator_test.go`
- `pkg/api/v1/session/session.go`, `pkg/api/v1/session/session_test.go`
- `pkg/gateway/sessionserver/upload.go`, `finalize_handler.go`, `sessionserver.go`, `session_response.go`, and the test files Testing items 3 through 10 name
- `pkg/gateway/session/sessionstore/sessionstore.go`
- `pkg/gateway/externalapi/errorclassify/errorclassify.go`, `errorclassify_test.go`
- `pkg/connectoroauth/state.go`, `state_test.go`, `pkce.go`, `flow.go`, `flow_test.go`, `redis_state_store.go`
- `pkg/gateway/externalapi/admin/connector_oauth.go`, `connector_oauth_test.go`, `routes.go`
- `pkg/gateway/mcpfabric/playground/auth.go`, `crypto.go`, `playground.go`, and the test files Testing items 12 and 13 name
- `cmd/lenny-gateway/token_signing.go`, `workers.go`, `wiring_fields.go`, `llmproxy.go`, `credentialsurface.go`, `httpsurface.go`, `flags.go`, `oauth_state_store_test.go`
- `charts/lenny/values.yaml`
- The `uploadtoken` constructor callers the CODE-3 sweep lists
- `tests/tier3_contract/rest_mcp_consistency/finalize_upload_token_parity_test.go`
- `tests/tier4_integration/connector_oauth_test.go`, `finalize_admission_race_test.go`, `playground_oidc_login_test.go`, `upload_token_two_replica_test.go`
- `tests/tier5_e2e_kind/upload_token_two_replica_kind_test.go`
- `tests/tier7a_load_local/finalize_admission_race_test.go`
- `tests/tier8_chaos/playground_oidc_redis_outage_test.go`
- `tests/tier9_security/finalize_upload_token_test.go`, `oauth_state_binding_test.go`
- `tests/tier11_docs/` (new docs check)
- `tests/testinfra/sessiondriver/sessiondriver.go`, `tests/spec-map.json`
- `docs/client-guide/session-lifecycle.md`, `wire-format.md`, `error-handling.md`, `docs/reference/glossary.md`, `error-catalog.md`, `docs/api/mcp.md`, `rest.md`
- `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md`
