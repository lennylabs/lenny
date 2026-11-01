# Summary: Verify upload tokens against the session row and OAuth state against the shared state store

## Summary

**Problem statement.** Three gateway flows verify a client-held value against a secret that exists in one process, so each fails on a replica other than the issuer and after a restart, which contradicts the §10.1.1 statement that a client can reach any replica. A finalize on a peer answers `401 UPLOAD_TOKEN_INVALID`, a code the specification does not define. A token replay on a peer receives the wrong status. A playground OIDC login and a connector OAuth callback on a peer are refused although their flows are valid.

**What changes.**

- §7.1 and §7.4 make the session row the `uploadToken` authority, and `pkg/uploadtoken` plus both session server call sites verify the digest, expiry, and state from the primary row (SPEC-1, CODE-1, CODE-2).
- §15.1 defines `UPLOAD_TOKEN_INVALID`, `errorclassify` carries it, and §18 drops the rotation items (SPEC-2, CODE-2).
- The ring, tracker, rotator, and their gateway wiring are deleted (CODE-3).
- `StateSigner` is deleted, and `StateStore.Consume` is the connector callback's single authority (SPEC-3, CODE-4).
- The playground keeps its `state` and PKCE verifier in the shared `conn:oauth:state:{state}` store, and the cookie holds the opaque `state` (SPEC-3, CODE-5).
- Client docs, tests across tiers 1 to 11, and the finding records follow (DOCS-1, TEST-1, RECORDS-1).

**Decisions.**

1. No shared key exists: each per-process secret is deleted and state the replicas already share is the only authority, which adds no Secret, chart value, KMS alias, migration, column, Redis key prefix, wire field, or store interface.
2. The `uploadToken` stays a required second credential, because deleting it reverses the F-7.4.21 fix and removes a check §7.4 requires.
3. The format stays three segments and the third becomes 256 random bits, because the digest covers the whole token and so binds the embedded expiry.
4. `verifyUploadToken` defines the check order through SPEC-1 **Check order:**, and expiry precedes consumption as `Verifier.Verify` orders them today.
5. Consumption is derived from the row with no write (SPEC-1 **Single-use invalidation:**), and a valid token on a terminal row falls through to 409, because a terminal row does not record whether finalize succeeded and 410 must always mean finalization committed.
6. No code returns a row to `created`, and the rule runs ahead of the precondition, so a future capability that admits `running` cannot reopen replay for a token holder.
7. Both call sites and the `finalizeUpload` recheck read through `s.readPrimary`, because `pgstore.Get` reads the §12.3 read pool and a lagging `created` row would admit an upload after consumption. A read error at the recheck refuses the upload, because the per-process `uploadAborts` leaves the recheck as the only channel close on a peer.
8. `handleFinalize` reads the row before the token check, which exposes nothing new because `requireSessionAccess` already read the row and answers 404 first.
9. The `verifyUploadToken` nil-verifier opt-out stays, because it is a test seam outside the validated problem.
10. The playground reuses `connectoroauth.StateStore` and `conn:oauth:state:{state}` unchanged, following the recorded owner direction.
11. The cookie stays because it carries the browser binding against login CSRF.
12. One store instance serves both flows, and each callback refuses the other flow's entry.
13. A store fault at login answers `503 REDIS_UNAVAILABLE` and at callback redirects to `oidc_state_unavailable`, because §27.3.1 **Redis unavailability.** is fail-closed and §27.3.1 says a browser cannot consume a JSON envelope on the redirect path.
14. Login resolves the authorization URL, then writes the store, then sets the cookie, so a provider outage leaves no orphan key from an anonymous request.
15. `StateSigner` goes because §9.3 asks for no signature, the random bytes alone make the value unforgeable, and an unknown value performs one script call and no write. `CONNECTOR_OAUTH_STATE_INVALID` is retired.
16. All items stay in one proposal under the §10.1.1 rule. The problem statement records the split for the owner: no step of items 1 and 2 depends on items 3 and 4, so a split costs only a second pipeline run.
17. The `uploadToken` work lands first, row verification before key deletion, so the F-7.1.31 defect is fixed at a green commit ahead of the constructor sweep.
18. This proposal closes F-15.1.69, because it adds the catalog row and `errorclassify` entry that finding asks for.
19. The §11.1 upload caps stay counted in each replica's memory, and SPEC-1(f) states that scope, so a session can reach a cap on each replica and a restart clears that replica's counts. Shared counters would add Redis to the upload path for caps that are off by default, and the §11.2 storage quota already bounds upload bytes across replicas.

**Watch out for.**

- A store wrapper that embeds `sessionstore.Store` loses `GetPrimary`, so `ReadPrimary` silently falls back to `Update`, and `Update` counters and hooks then misreport. Testing item 6 lists the wrappers.
- `AllStates()` omits `resuming`, which rows can hold. A state table over it leaves that state unpinned.
- A test that wires a verifier and seeds a row directly must set the digest and expiry, or it answers 401.
- `w.connectorStateStore = connectorStateStore` in `credentialsurface.go` would overwrite the memory store the helper set. CODE-5 deletes it.
- `handleAuthError` writes 200 for every code, so the 503 that SPEC-3 states needs the CODE-5 edit.
- `oidc_state_expired` fires only on the memory store. A Redis test that expects it will mislead.
- Login is now an anonymous Redis write, and F-27.3.17 records that exposure.

## Goals

- A token minted on one replica verifies for upload and finalize on every replica and after a restart.
- A consumed token answers `410 UPLOAD_TOKEN_CONSUMED` on every replica.
- A playground OIDC login and a connector OAuth callback complete on any replica and after a restart.
- §15.1 defines `UPLOAD_TOKEN_INVALID`, and REST and MCP classify it the same way.
- A two-replica test in tiers 4 and 5 pins each cross-replica path that a process boundary reaches.

## Non-goals

- Deleting the `uploadToken` outright (`contrarian`). It is the smallest long-term surface and it dissolves F-11.5.27, but it removes a required credential, reverses F-7.4.21, and spans about 120 files across three SDKs, MCP schemas, and docs. The row digest resolves the validated problem at a fraction of that breadth. Contrarian's own floor names the row digest as the fallback.
- Removing only the playground signature and keeping the verifier in the cookie (`contrarian`). It leaves the PKCE verifier in the browser, gives no single use, and departs from the recorded owner direction.
- A shared HMAC key from a chart Secret, a KMS alias, the §10.2 `platform:jwt-signing` key, epoch-derived keys, or a key table in Postgres, for any of the three flows. Each adds a key source and rotation coordination to protect a value the row or store already authenticates. The §10.2 key is remote ES256 and would add a KMS dependency to session create.
- Reissuing the `uploadToken` as a JWT. It adds `KMS_SIGNING_UNAVAILABLE` to every create and still needs a consumption record.
- A Redis or Postgres `ConsumedTracker`. The row state already answers consumption, and a second store can disagree with the row and adds a failure mode.
- Recording consumption by clearing `upload_token_expiry` at the ready write (`spec-first`). It keeps 410 on terminal rows but overloads a column to separate two non-retryable refusals inside a 300 s window.
- A new `upload_token_consumed_at` column. It needs a migration and three pgstore column lists for the same narrow distinction. It is the fallback if a later round requires 410 on a terminal row that passed `ready`.
- Clearing or overwriting the row digest on finalize. A consumed token then becomes indistinguishable from a forged one, and the audit Detail loses the digest.
- Treating every state outside `created` and `finalizing` as consumed, terminal included (`reuse`). It tells a client whose finalize failed that the token was consumed by a successful finalize, and it departs from the §15.1 terminal 409.
- Returning 410 past expiry for a consumed token (`spec-first`). It changes the existing order, in which expiry precedes consumption, with no defect behind it.
- Dropping the embedded expiry from the token format (`spec-first`). It adds wire-format and fixture churn, and the digest already binds the segment. Dropping the embedded session id would lose 403 MISMATCH.
- Keeping the HMAC beside the digest check. The HMAC needs the shared key this design removes and adds nothing once the row is compared.
- Reading the row through `store.Get` for verification (`minimal`, `implementor`). A lagging row admits an upload with a consumed token.
- Removing the `verifyUploadToken` nil-verifier opt-out (`spec-first`). It is a test-wiring seam outside the validated problem and costs 13 test files.
- A new §10.1.1 **Client-held values.** paragraph (`spec-first`). The correctness rule already covers this. A second statement must carve out JWTs and in-memory backends and adds review surface.
- A dedicated key prefix through a `NewRedisStateStore` parameter (`reuse`, `implementor`). The shared key already reaches a refusal in both cross-flow directions, and a separate prefix adds a §12.4 row, two exception-list edits, a tier-11 registry entry, a constructor parameter, and a second memory store with a generalized sweep. It is the first alternative if a reviewer rejects the shared `conn:oauth:` name.
- New `PutLoginState` and `ConsumeLoginState` methods on `playground.SessionStore` with a `pg:oidc:state:{state}` or `pg:oidc-state:{state}` key (`spec-first`, `failure-modes`). It is a parallel single-use mechanism beside the store that already has Redis and memory backings.
- Removing the playground state cookie and relying on Redis alone. It loses the browser binding and opens login CSRF.
- A separate tombstone rule for the playground. The reused store has one, and the playground reports replay and unknown with the same code.
- Keeping a structural format check on the connector `state` and `CONNECTOR_OAUTH_STATE_INVALID` (`spec-first`, `failure-modes`, `implementor`). An attacker sends a well-formed random value at no cost, so the check stops nothing, and an unknown value performs one script call and no write.
- Keeping the connector signature under a shared key. §9.3 asks for no signature.
- Reporting a playground store fault as `oidc_state_invalid` or `LENNY_PLAYGROUND_OIDC_UNAVAILABLE` (`minimal`, `failure-modes`). It names an outage as a bad state or a provider fault.
- Client documentation that a 410 on a retried finalize confirms the commit (`failure-modes`). The status is accurate under this design, and the validated problem does not need new guidance.
- Session affinity on the gateway Service. §10.1.1 makes sticky routing an optimization, and affinity leaves the restart path broken.
- Rendering `--connector-oauth-callback-url` in the chart. This is a separate deployability question.
- Per-replica `uploadAborts` and the dead-letter receipt signer. These are out of scope per the problem statement.
- Multi-replica behavior with no `--redis-url`. The memory stores stay single-process, which is the existing posture and is logged at startup.
- Rate limiting on the unauthenticated callback and login paths. A forged connector `state` now reaches Redis at the cost of one script call and no write, and login adds one TTL-bounded write per anonymous request after the provider resolves. The design adds no limiter, and F-27.3.17 records the residual exposure.
- Handling in-flight tokens across this change. No deployments exist, so no compatibility code is owed.

## Open decisions for human to make

None.

## Defects in the shipped tree that this proposal does not stage

- **The connector callback reports a store fault as a bad state.** `RedisStateStore.Consume` returns the raw script error, and the handler's default branch answers `400 CONNECTOR_OAUTH_STATE_UNKNOWN`. §9.3 is silent on a store fault, so the fix needs spec text, and this design neither causes nor worsens it.
- **`REDIS_UNAVAILABLE` has no §15.1 catalog row.** §27.3.1 states the code and logout already emits it. SPEC-3 adds one more emitter, and a catalog row for the code is separate from this repair.
- **The §12.4 Failure behavior per use case table is incomplete.** It has no row for playground revocation or OAuth state. The omission predates this change and no behavior depends on it.
- **The playground error page renders 200 for tenant-claim rejections.** §27.3.1 records 401 and 403 on the final response, and `handleAuthError` writes 200. CODE-5 changes only the code SPEC-3 adds, because the tenant-claim rows are a separate correction.
- **`uploadLimiter.closeSession` runs only in the finalize handler.** The replica that ran finalize drops its `sessionBytes` entry. A peer that served an upload for the session, and a replica whose session ended without finalize, keeps its entry until the process restarts. The entry refuses nothing, and it exists only when `maxBytesPerSession` is set. Cross-replica admission makes the peer case reachable.
- **`uploadAborts` is per process, so finalize does not sever an upload on a peer.** `pkg/gateway/sessionserver/finalize_handler.go:220` closes the registry at `pkg/gateway/sessionserver/upload_abort.go:36` on the replica that ran finalize alone. A peer refuses the token today, and cross-replica admission makes a stream on a peer reachable, so that stream runs to the post-Put recheck at `pkg/gateway/sessionserver/upload.go:498`. The recheck already covers a same-replica stream past its last Read, and this defect widens that window on a peer from the tail of the Put to the whole stream. The cost on a peer is the bytes of that stream, which the CODE-2 recheck refuses and soft-deletes. This proposal does not stage a fix, because §7.4 names no abort mechanism, no staged text promises an abort on a peer, and a cross-replica signal is new machinery.
- **The dead-letter redaction receipt signer holds a per-process key that no party can verify.** `NewKMSReceiptSigner` at `pkg/gateway/storage/deadletterredaction/signer.go:39` generates a random HMAC-SHA256 key at boot and discards the sealed output at `pkg/gateway/storage/deadletterredaction/signer.go:51`, and `cmd/lenny-gateway/adminrouter.go:655` calls it with the fixed key id `boot`, so replicas sign under different keys that share one id and each key ends with its process. The §12.8 `signature` field states that external verifiers fetch a public key from the admin API, and a symmetric key discarded at exit leaves them no key to verify a stored receipt with. A search of `pkg/` and `cmd/` finds no admin route that serves such a key. This proposal does not stage a fix, because no replica verifies a receipt signature: `receiptedRedaction` at `pkg/audit/chain.go:478` tests only the stored hash and a non-empty signature read from Postgres, so its result is the same on every replica and the problem statement's condition for bringing the signer into scope is unmet. The fix is a §12.8 conformance change, and no staged text relies on the signer.
- **Finalize's token check sits behind the §11.5 idempotency middleware, and a replay is not bound to the caller (F-11.5.26, F-11.5.27).** `cmd/lenny-gateway/httpsurface.go:735` lists `/v1/sessions/{id}/finalize` in the middleware's `AllowedPaths`, and `pkg/gateway/middleware/idempotency/idempotency.go:357` claims the key before the inner handler runs at `pkg/gateway/middleware/idempotency/idempotency.go:424`, while `verifyUploadToken` runs inside that handler at `pkg/gateway/sessionserver/finalize_handler.go:107`. A same-key retry therefore receives the stored response without the checks, whatever token it carries, and `pkg/gateway/middleware/idempotency/idempotency.go:472` stores every response below 500, so a token refusal is replayed to a corrected retry. The record matches on tenant, key, and body hash alone, so a replay also ignores the caller. On a first keyed finalize the claim inserts a pending `idempotency_keys` row at `pkg/gateway/middleware/idempotency/pgstore/pgstore.go:141` ahead of the checks. That row is middleware bookkeeping for the request, and no session state is written. This proposal does not stage a fix, because neither finding has to close for SPEC-1 to SPEC-3 to hold, BUILD-GAPS.md files both as open, and the fix needs §11.5 text in the paragraph the proposal that closes F-11.5.26 owns.
- **The specification does not settle whether REST `POST /v1/sessions/{id}/upload` in `running` admits a bearer.** `spec/15_external-api-surface.md:726` and `spec/29_communication-scenarios.md:636` list that route as valid in `running`, `spec/07_session-lifecycle.md:502` gives a mid-session upload the caller's bearer, and `spec/07_session-lifecycle.md:500` names only `upload_to_session` as the mid-session call. The mid-session route in code is `/v1/sessions/{id}/upload-to-session` at `pkg/gateway/sessionserver/route_table.go:75`. On the REST route, `admitUpload` runs the token check at `pkg/gateway/sessionserver/upload.go:308` ahead of the precondition check at `pkg/gateway/sessionserver/upload.go:316`, and that check passes no capability, so the `running` arm at `pkg/api/v1/session/session.go:298` is unreachable and a production gateway refuses a bearer-only request with `401` in every session state. This proposal does not stage a fix, because SPEC-1 **Authentication:** scopes its rules to a request governed by the `uploadToken`, CODE-2 keeps the check order, and the answer needs §7.4, §15.1, and §29 reconciled, which no repaired flow depends on.
- **The chart cannot enable the connector OAuth flow.** `cmd/lenny-gateway/credentialsurface.go:181` wires the flow only when `--connector-oauth-callback-url` is set, and `cmd/lenny-gateway/flags.go:1252` also reads that flag from `LENNY_CONNECTOR_OAUTH_CALLBACK_URL`. `charts/lenny/` renders neither the flag nor the variable, and the `args` at `charts/lenny/templates/gateway-deployment.yaml:457` and the `env` at `charts/lenny/templates/gateway-deployment.yaml:778` expose no values key that adds one, so a chart deployer patches the rendered Deployment to reach the flow. This proposal does not stage a fix, because the flow is opt-in, CODE-4 holds once a deployer sets the flag, and no staged test needs the chart to enable the flow: `tests/tier4_integration/connector_oauth_test.go:90` passes the flag directly.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0097 | Implemented | Resolves the `uploadToken` ring finding its summary left out, with no shared key. | Nothing. |
| 0104 | Implemented | Closes F-7.1.31, which it declined as a separate plan step. | Nothing. |
| 0111 | Draft | Both edit §7.1, and 0111 adds **Bind bounds.** in a different paragraph of it. Both can touch `handleFinalize`. | The second to land rebases its anchors. |

## Deliverable index

- SPEC-1 — spec/07_session-lifecycle.md, spec/11_policy-and-controls.md — The session row is the `uploadToken` authority in §7.1 and §7.4, and §11.1 states the upload cap scope.
- SPEC-2 — spec/15_external-api-surface.md, spec/18_build-sequence.md — The §15.1 `uploadToken` edits and the §18 rotation items.
- SPEC-3 — spec/27_web-playground.md, spec/15_external-api-surface.md, spec/12_storage-architecture.md, spec/18_build-sequence.md — OAuth state held in the shared store, and the §12.4 and §18.28 connector `state` wording.
- CODE-1 — pkg/uploadtoken/uploadtoken.go, pkg/api/v1/session/session.go — Random issuance, row verification, and the consumption predicate.
- CODE-2 — pkg/gateway/sessionserver/, pkg/gateway/session/sessionstore/sessionstore.go, pkg/gateway/externalapi/errorclassify/errorclassify.go, cmd/lenny-gateway/flags.go, charts/lenny/values.yaml — Both call sites verify against the primary row, `errorclassify` carries the new code, and the cap descriptions state their scope.
- CODE-3 — pkg/uploadtoken/, pkg/gateway/sessionserver/sessionserver.go, cmd/lenny-gateway/ — The key material, the gateway wiring, and the constructor sweep.
- CODE-4 — pkg/connectoroauth/, pkg/gateway/externalapi/admin/, cmd/lenny-gateway/credentialsurface.go, tests/tier4_integration/connector_oauth_test.go — The connector state signer is removed.
- CODE-5 — pkg/gateway/mcpfabric/playground/, cmd/lenny-gateway/ — Playground state in the shared store.
- DOCS-1 — docs/client-guide/, docs/reference/, docs/api/ — Client documentation.
- RECORDS-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — Findings and plan records.
- TEST-1 — tests/ — Cross-replica and contract tests.
