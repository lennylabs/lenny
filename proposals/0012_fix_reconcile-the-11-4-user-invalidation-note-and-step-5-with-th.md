# Proposal: Reconcile the §11.4 user-invalidation Note and step 5 with the synchronous-local + async-cross-replica implementation

- **Status:** Approved (2026-06-19). Verified and converged after 3 adversarial review rounds (2 findings fixed); signed off by the human approver.
- **Date:** 2026-06-19.
- **Scope:** Reconciles two §11.4 "User Invalidation" wording defects with the implemented full-revoke contract. The trailing Note at `spec/11_policy-and-controls.md:263` advertises fire-and-forget propagation ("Invalidation is asynchronous — the API call returns immediately and propagation completes within seconds"), but `handleInvalidateUser` (`pkg/gateway/admin/users.go:486-601`) runs every full-revoke effect synchronously before returning and reports per-effect counts in the response body; the only asynchronous portion is cross-replica fan-out over Redis pub/sub. Step 5 at `spec/11_policy-and-controls.md:259` reads "Cached auth tokens for the user are invalidated in Redis," but the invalidation is not performed in Redis: the revocation is recorded authoritatively in the Postgres issued-token index, the per-replica cache is in-memory, and Redis carries only the cross-replica propagation message. The fix rewords the Note and step 5 to match the implemented behavior and corrects the identical "cached-auth invalidation in Redis" wording at `spec/18_build-sequence.md:386`. The change touches no v1 behavior, schema, code, or proto.

This document stages the proposed spec changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

§11.4 documents three invalidation levels and a "Full revoke propagation mechanism" numbered list, followed by a Note. Two pieces of that text misstate the implemented behavior.

### 1.1 The Note advertises fire-and-forget propagation the implementation does not perform

The trailing Note at `spec/11_policy-and-controls.md:263` reads:

```
> **Note:** Invalidation is asynchronous — the API call returns immediately and propagation completes within seconds. The `GET /v1/sessions` endpoint reflects the updated state once propagation completes.
```

The implementation is a hybrid of synchronous-local effects and asynchronous cross-replica propagation. `handleInvalidateUser` (`pkg/gateway/admin/users.go:486-601`) does not return immediately on the `full_revoke` path. It runs `terminateUserSessions` (`users.go:535`, a synchronous SessionStore write transitioning the user's non-terminal sessions to `cancelled` at `users.go:442-471`), `runFullRevokeFanOut` (`users.go:545`, the local pod `Terminate` RPCs plus credential-lease revocation plus durable token revoke with a revocation-cache push plus playground-session revoke at `users_invalidate.go:145-197`), and `interactions.DismissByUser` (`users.go:547`) synchronously, then encodes the response at `users.go:600`. No goroutine or background queue is on this path. The response body (`users.go:579-600`) carries actual per-effect counts plus partial-failure detail, and the matching `admin.user.invalidated` audit detail (`users.go:556-577`) carries the same fields. The Note describes none of this.

The only asynchronous portion is cross-replica propagation. `podTerminateFanOut.TerminateUserSessions` (`cmd/lenny-gateway/user_revocation.go:78-90`) terminates the handling replica's local pods synchronously via `terminateLocal` (each `Terminate` RPC bounded at `userTerminateRPCTimeout = 20s`, `user_revocation.go:50,123-124`), then `prop.Publish` fans the request out to peer replicas over Redis pub/sub. The token-revocation cache (`pkg/gateway/revocation/propagator/propagator.go`) and the credential deny-list (`pkg/gateway/credrenewal/propagator/propagator.go`) use the same local-sync plus cross-replica-async pattern. The returned counts therefore reflect only the handling replica's local effects (`user_revocation.go:75-77,85-89`).

Clause by clause: "the API call returns immediately" is false (the call blocks for the bounded duration of the synchronous local effects); "propagation completes within seconds" is accurate only for the cross-replica asynchronous portion; the `GET /v1/sessions` clause holds in the default single-primary read configuration but is subject to read-replica lag when a separate read replica is configured (see §1.3 below). This recurs as finding F-11.4.5 (DEFERRED at `BUILD-GAPS.md:17328`), whose resolution states the spec should describe the synchronous-with-counts contract and that the edit is blocked by Rule B from touching `spec/`.

### 1.2 Step 5 names Redis as the store of invalidated tokens

§11.4 step 5 at `spec/11_policy-and-controls.md:259` reads:

```
5. Cached auth tokens for the user are invalidated in Redis.
```

The §11.4 invalidation path does not invalidate tokens in Redis. The Token Service caches materialized access tokens in Redis (`spec/04_system-components.md:201`), but that cache is a validation-performance optimization the full-revoke path neither reads nor evicts; a revoked token is rejected at validation through the revocation state regardless of whether its cached form in Redis lingers. The durable revocation record is the Postgres issued-token index (`pkg/gateway/issuedtokenstore/issuedtokenstore.go:318`, `RevokeBySubject`, a Postgres `UPDATE` inside a transaction). The per-replica cache is in-memory (`pkg/gateway/revocation/revocation.go:3,38-50`, a `map[string]struct{}` under `sync.RWMutex`). Redis is used only as the cross-replica propagation channel (`pkg/gateway/revocation/propagator/propagator.go:37-39`, `Channel = "token:revocations"`). §13.3 already states this layering: Postgres is the sole authoritative store for token revocation, and the in-memory revocation cache and the cross-replica propagation are latency optimizations only (`spec/13_security-model.md:597-605`). Step 5 contradicts that section by naming Redis as the store. This recurs as finding F-11.4.9 (verify-CLOSED at `BUILD-GAPS.md:17374`), whose resolution states the reconciling spec note "is a docs change tracked outside the code surface" and was never landed. The identical wrong wording appears a second time at `spec/18_build-sequence.md:386` ("cached-auth invalidation in Redis").

### 1.3 The GET /v1/sessions clause is conditional on read routing

The SessionStore cancellation write (`pgstore.Update` at `pkg/gateway/sessionstore/pgstore/pgstore.go:371,428`) is a synchronous transaction against the primary write pool, committed before `handleInvalidateUser` returns, so cancelled sessions are durable on return and do not depend on the cross-replica pub/sub propagation. `GET /v1/sessions` (`handleList`, registered with the `read()` wrapper at `sessionserver.go:1840`) reads via `pgstore.List`, which routes to `s.read` (`pgstore.go:577-578`). `s.read` defaults to the primary (`pgstore.go:60-61`), in which case cancelled sessions are visible on return. When `LENNY_PG_READ_DSN` configures a separate read replica (`cmd/lenny-gateway/main.go:1411-1416,1480`; §12.3 read replicas at `spec/12_storage-architecture.md:146`), the read routes to the replica and primary-to-replica replication lag can briefly delay visibility. The rewrite must state the synchronous central-store write and frame replica visibility as subject to the same read-replica lag the spec already documents, rather than claiming unconditional read-after-write visibility.

Both edits land in the same §11.4 block (lines 243-264; §11.5 begins at 265), so one spec-only proposal lands both. The §18 step-5 correction extends the same fix to its second occurrence.

## 2. Decisions

- **Fix the spec rather than the code.** The synchronous-local plus async-cross-replica contract is the more useful surface: callers receive actual per-effect counts and partial-failure fields, and there is no observability surface that would let a caller reconcile a fire-and-forget Note (the only invalidation route is the single synchronous `POST /v1/admin/users/{user_id}/invalidate`; there is no propagation-status endpoint and no propagation-complete event for the §11.4 flow). Changing the code to match fire-and-forget would discard the reported counts and the central-store-synchronous session cancellation. The spec wording is the defect.
- **Scope is spec-only.** Stage the §11.4 Note (line 263), the §11.4 step-5 (line 259), and the §18 step-5 (`spec/18_build-sequence.md:386`) edits. No `pkg/`, `cmd/`, schema, proto, chart, or docs change. The implementation already exhibits the behavior the rewritten text describes.
- **Bundle F-11.4.5 and F-11.4.9 into one proposal.** Both §11.4 edits land in the same numbered list and trailing Note (lines 243-264). F-11.4.5 is the primary (DEFERRED spec-wording fix at `BUILD-GAPS.md:17328`); F-11.4.9 is the adjacent step-5 wording (verify-CLOSED at `BUILD-GAPS.md:17374` with its spec rewrite never landed). One spec-only proposal closes the open finding and lands the never-applied rewrite together.
- **Qualify the GET /v1/sessions clause rather than asserting it unconditionally.** The cancellation write is committed to the primary write pool before the call returns, so `GET /v1/sessions` reflects it on return in the default configuration; when read traffic is routed to a Postgres read replica, the cancellation becomes visible once it replicates, subject to the read-replica lag §12.3 already documents.
- **Do not enumerate the response-body fields in the Note.** The per-effect count names and the partial-failure field names are response-schema detail, and field-name lists go stale. §15.1 documents the `POST /v1/admin/users/{user_id}/invalidate` endpoint as a single action row with no response-body specification (`spec/15_external-api-surface.md:834`), and none of the field names appear anywhere in `spec/`, so the Note cannot defer the names to a §15.1 response that does not exist. The Note states that the API performs the local effects synchronously and returns reported counts plus partial-failure detail for the effects it enumerates, without naming individual fields and without pointing to a §15.1 response body. Adding the response-body specification to §15.1 is a separate enhancement outside this wording-fix proposal's scope.
- **Do not name the audit event in the Note.** The handler emits `admin.user.invalidated` (`users.go:577`; the OCSF mapping keys on it at `pkg/audit/ocsf/mapping.go:184`), but the spec spells the same event `user.invalidated` everywhere it names it (`spec/15_external-api-surface.md:834`; `spec/27_web-playground.md:94,152,204`). Introducing `admin.user.invalidated` into §11.4 prose would create a third, conflicting spelling. The Note omits the audit-event name; the code/spec name divergence is a separate pre-existing defect outside this proposal's scope and is recorded as a non-goal.
- **Reuse the spec's existing propagation vocabulary rather than inventing a parallel one.** §13.3 is the canonical description of token revocation: Postgres is the sole authoritative store, the in-memory revocation cache and the cross-replica propagation are latency optimizations (`spec/13_security-model.md:597-605`). The step-5 rewrite names Postgres as authoritative, glosses the cache-plus-propagation layering briefly, and cross-references §13.3 rather than restating the whole mechanism. The Note reuses the same Redis-pub/sub cross-replica framing the surrounding spec already uses.

## 3. Proposed changes

### 3.1 Spec change: `spec/11_policy-and-controls.md` §11.4 trailing Note (line 263)

Anchor on the Note that immediately follows the "Full revoke propagation mechanism" numbered list in §11.4, currently at line 263. The current text is:

```
> **Note:** Invalidation is asynchronous — the API call returns immediately and propagation completes within seconds. The `GET /v1/sessions` endpoint reflects the updated state once propagation completes.
```

Replace it with the following Note. It states the two-tier contract (synchronous local effects on the handling replica, asynchronous cross-replica propagation), reports that the response carries per-effect counts and partial-failure detail without naming the response-body fields, omits the audit-event name, and qualifies the `GET /v1/sessions` clause by read routing:

```
> **Note:** Full-revoke propagation is two-tiered. On the handling gateway replica, the API performs the local effects synchronously before it returns: it cancels the user's non-terminal sessions in the SessionStore, sends the [§4.7](04_system-components.md#47-runtime-adapter) `Terminate` RPC to the pods that replica coordinates, revokes the user's credential leases, revokes the user's issued tokens and pushes them into the in-memory revocation cache, dismisses pending elicitations, and revokes the user's playground sessions. The response body reports the per-effect counts and any partial-failure detail for the effects enumerated above. Because each local pod `Terminate` RPC is bounded, the call blocks for a bounded duration rather than returning immediately. Cross-replica propagation is asynchronous: the gateway publishes the step-2 `Terminate` request, the token revocations, and the credential deny-list entries on Redis pub/sub channels, and peer replicas apply them on their subscribers within seconds. The reported counts therefore reflect only the handling replica's local effects; peer-replica pods, revocation-cache entries, and deny-list entries follow asynchronously. The cancelled session state is committed to the central SessionStore before the call returns, so `GET /v1/sessions` reflects it on return; when read traffic is routed to a Postgres read replica (see [§12.3](12_storage-architecture.md#123-postgres-ha-requirements)), the cancellation becomes visible once it replicates, subject to the usual read-replica lag.
```

Notes for the applier:

- Confirm the §4.7 and §12.3 anchor slugs against the current headings before applying. The §4.7 slug is `#47-runtime-adapter` (heading `### 4.7 Runtime Adapter` at `spec/04_system-components.md:636`, where the `Terminate` RPC is defined); the §12.3 slug is `#123-postgres-ha-requirements` (heading `### 12.3 Postgres HA Requirements`).
- Do not introduce the audit-event name `admin.user.invalidated` or `user.invalidated` into the Note. The code/spec name divergence is out of scope (see Non-goals).
- Do not alter the §11.5 heading at line 265 or the numbered list above the Note except via §3.2.

### 3.2 Spec change: `spec/11_policy-and-controls.md` §11.4 step 5 (line 259)

Anchor on item 5 of the "Full revoke propagation mechanism" numbered list in §11.4, currently at line 259. The current text is:

```
5. Cached auth tokens for the user are invalidated in Redis.
```

Replace it with the following. It names Postgres as the authoritative store and cross-references §13.3 rather than restating the full mechanism, matching §13.3's existing vocabulary:

```
5. The user's issued tokens are revoked in the durable Postgres issued-token index (the authoritative revocation store; see [§13.3](13_security-model.md#133-credential-flow)), each revoked token id is pushed into the gateway's in-memory revocation cache, and the revocations are fanned out to peer replicas so every replica's cache rejects the tokens within seconds.
```

Notes for the applier:

- Confirm the §13.3 anchor slug `#133-credential-flow` against the heading `### 13.3 Credential Flow` in `spec/13_security-model.md` before applying.
- §13.3 frames the cross-replica propagation as the `token.revoked` CloudEvents event on the Redis EventBus (`spec/13_security-model.md:597`), while the code uses the dedicated `token:revocations` pub/sub channel (`pkg/gateway/revocation/propagator/propagator.go:39,88`). The step-5 wording above stays vocabulary-neutral ("fanned out to peer replicas") to avoid encoding a third description. If §13.3's `token.revoked`-EventBus wording and the code's pub/sub channel genuinely diverge, raise that as its own finding rather than reconciling it here.

### 3.3 Spec change: `spec/18_build-sequence.md` user-invalidation deliverable (line 386)

Anchor on the user-invalidation Phase 7 deliverable bullet, currently at line 386, which references §11.4 and lists the full-revoke effects. The relevant clause reads:

```
... session-termination RPC fan-out; cached-auth invalidation in Redis; lease revocation; and elicitation dismissal.
```

Correct the `cached-auth invalidation in Redis` clause to match the §11.4 step-5 rewrite. Replace that single clause so the bullet reads:

```
... session-termination RPC fan-out; issued-token revocation in the durable Postgres issued-token index with in-memory revocation-cache push and cross-replica fan-out; lease revocation; and elicitation dismissal.
```

Notes for the applier:

- Change only the `cached-auth invalidation in Redis` clause. Leave the rest of the bullet, including the trailing Phase 12a / Phase 7 parenthetical, unchanged.

## 4. Non-goals

- **No code change.** `handleInvalidateUser` (`pkg/gateway/admin/users.go:486-601`), the fan-out (`users_invalidate.go:145-197`), the cross-replica propagators (`cmd/lenny-gateway/user_revocation.go`; `pkg/gateway/revocation/propagator`; `pkg/gateway/credrenewal/propagator`), the issued-token index (`pkg/gateway/issuedtokenstore`), and the in-memory revocation cache (`pkg/gateway/revocation/revocation.go`) already implement the behavior the rewritten text describes. The edits document existing behavior.
- **No new propagation-status endpoint or propagation-complete event.** The proposal does not add an async-completion observability surface; it documents the existing synchronous-with-counts contract. The existing aggregate propagation-latency signals (`lenny_token_revocation_propagation_seconds`, the `TokenRevocationPropagationLag` alert, `lenny_playground_session_revocation_propagation_seconds`, the `token.revoked` `propagation_mode` field) are unchanged and out of scope.
- **No change to the `GET /v1/sessions` read-routing behavior or the `LENNY_PG_READ_DSN` read-replica option.** The rewrite describes the existing read-after-write-on-primary and read-replica-lag behavior; it does not alter `pgstore` read routing (`pkg/gateway/sessionstore/pgstore/pgstore.go`) or the read-pool wiring (`cmd/lenny-gateway/main.go`).
- **No audit-event rename and no new audit-catalog entry.** The handler emits `admin.user.invalidated` (`users.go:577`) while the spec names the event `user.invalidated` (`spec/15_external-api-surface.md:834`; `spec/27_web-playground.md`). This divergence is a separate pre-existing defect; this proposal neither reconciles the name nor adds `admin.user.invalidated` to the §16.7 audit catalog. The Note omits the audit-event name to avoid introducing a third spelling.
- **No reconciliation of the §13.3 `token.revoked`-EventBus wording with the code's `token:revocations` pub/sub channel.** The step-5 rewrite stays vocabulary-neutral; the EventBus-versus-channel divergence is left for a separate finding.
- **No schema, CRD, proto, or chart change.** The `InvalidateUserRequest`/response surface and the admin route are unchanged.
- **No tier-2-or-higher behavioral test.** The change is wording-only with no behavior, wire-contract, or schema change. The existing tier-1 tests that pin the synchronous full-revoke fan-out and the per-effect counts (the admin handler and propagator unit tests) continue to hold; a tier-11 doc/spec-consistency check at most confirms that the spec and the code-comment citations agree.

## 5. Testing

- **Tier 0 (static):** confirm the edited spec renders and the added intra-spec anchors (`#47-runtime-adapter`, `#123-postgres-ha-requirements`, `#133-credential-flow`) resolve to live headings. The spec lint and link-check stage flags a broken anchor.
- **Tier 1 (unit), already covered:** the existing admin-handler and propagator unit tests assert that `handleInvalidateUser` runs the full-revoke effects synchronously, returns the per-effect counts, and that the revocation propagator fans out over Redis pub/sub. These continue to pin the contract the rewritten Note and step 5 now describe. No new unit test is required, because behavior is unchanged.
- **Tier 11 (docs):** confirm the edited §11.4 Note, §11.4 step 5, and §18 deliverable agree with each other and with §13.3 on the durable-Postgres plus in-memory-cache plus Redis-pub/sub layering. The check confirms convergence rather than requiring a further edit.
- **No tier-2-or-higher behavioral test is added.** The change is wording-only with no behavior, schema, or wire-contract change, so no envtest, contract, integration, e2e, chaos, or security tier is reached.

## 6. Findings closed on application

- **F-11.4.5** (Medium, DEFERRED at `BUILD-GAPS.md:17328`): the §11.4 Note advertises fire-and-forget propagation ("the API call returns immediately") that the synchronous-with-counts implementation does not perform. §3.1 rewrites the Note to state the synchronous-local plus async-cross-replica contract, removing the false "returns immediately" claim and qualifying the `GET /v1/sessions` clause by read routing. The finding's preferred resolution (describe synchronous propagation with reported counts) is the staged Note.
- **F-11.4.9** (Info, verify-CLOSED at `BUILD-GAPS.md:17374`): the §11.4 step-5 wording "invalidated in Redis" misrepresents the architecture. §3.2 rewrites step 5 to name the durable Postgres issued-token index as the authoritative store with the in-memory-cache push and cross-replica fan-out, cross-referencing §13.3; §3.3 corrects the identical clause at `spec/18_build-sequence.md:386`. This lands the reconciling spec edit the finding's resolution recorded as a never-applied docs change.

## 7. Resolved in adversarial review

Adversarial review rounds populate this section.

### Pass 1 (2026-06-19, automated)

- **Staged §11.4 Note hard-coded a broken §4.7 anchor and mislabeled the section.** The §3.1 staged Note linked the `Terminate` RPC as `[§4.7](04_system-components.md#47-pod-runtime)`, but no §4.7 "Pod Runtime" heading or `#47-pod-runtime` anchor exists in `spec/`. The §4.7 heading is `### 4.7 Runtime Adapter` (`spec/04_system-components.md:636`), the `Terminate` RPC is defined under it (`spec/04_system-components.md:662`), and every other §4.7 cross-reference, including in §11 itself (`spec/11_policy-and-controls.md:41,42`), uses `#47-runtime-adapter`. Corrected the staged Note link (§3.1) to `#47-runtime-adapter`, replaced the applier-note §4.7 hedge with the confirmed `#47-runtime-adapter` slug and heading name, and corrected the §5 tier-0 anchor list from `#47-pod-runtime` to `#47-runtime-adapter`.
- **Staged §11.4 Note deferred per-effect counts to a §15.1 response that §15.1 does not document.** The §3.1 Note pointed the reader to a `POST /v1/admin/users/{user_id}/invalidate` response in `[§15.1](15_external-api-surface.md#151-rest-api)`, and Decision §2 deferred the field names "to the §15 reference." §15.1 documents the endpoint as a single action row with no response body (`spec/15_external-api-surface.md:834`), and none of the per-effect count field names appear anywhere in `spec/`. Reworded the §3.1 Note to report the counts and partial-failure detail "for the effects enumerated above" without linking a nonexistent §15.1 response body, updated Decision §2 to record that §15.1 specifies no response body and that adding one is out of scope, removed the obsolete `#151-rest-api` anchor from the §5 tier-0 anchor list, and dropped the §5 tier-11 §15.1-vs-Note convergence check that the removed link no longer requires.

## 8. Open decisions for review

- **Audit-event mention in the Note — RESOLVED in favor of omission.** The §11.4 Note omits the audit-event name because the spec spells the event `user.invalidated` (§15.1, §27) while the code emits `admin.user.invalidated`, and naming either spelling in §11.4 would either repeat an existing divergence or introduce a third one. A reviewer who wants the audit event named in §11.4 should first land a companion edit that reconciles the code/spec name divergence, then add the spec's spelling; that reconciliation is out of this proposal's scope.

## 9. Files touched on application

- `spec/11_policy-and-controls.md`: §11.4 trailing Note (line 263) rewritten to state the synchronous-local plus async-cross-replica contract; §11.4 step 5 (line 259) rewritten to name the durable Postgres issued-token index with the in-memory-cache push and cross-replica fan-out.
- `spec/18_build-sequence.md`: the user-invalidation Phase 7 deliverable (line 386) "cached-auth invalidation in Redis" clause corrected to the Postgres-plus-cache-plus-fan-out wording.
- No code, schema, proto, chart, or docs file is touched. The existing tier-1 admin-handler and propagator tests and the tier-11 doc-consistency check verify the change.
