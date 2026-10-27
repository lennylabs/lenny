# Problem: Session-capability and delegation child tokens are never minted, signed, or recorded

## Statement

Two gaps leave Lenny's session-scoped and delegation tokens unverifiable (BUILD-GAPS F-10.2.25 and F-8.2.31). First, §10.2 "Signing topology" states that the gateway mints a `session_capability` token on the session-creation path in process through its `JWTSigner`, and that it records the token in `issued_tokens` with its `token.exchanged` row before returning it. The §10.2 JWT-claims list says the token is returned to MCP clients for WebSocket upgrades and that pods present it back to the gateway. `POST /v1/sessions` mints no token, because `pkg/gateway/sessionserver` holds no signer. The §10.2 KMS signing failure mode therefore cannot occur on session creation. The mint itself is stated behavior that the code lacks rather than a gap in the specification.

Second, §8.2 states that the gateway mints a delegation child's token through an internal RFC 8693 exchange at `POST /v1/oauth/token`, and §8.2 and §13.3 require the step (1a) re-read of `issued_tokens.revoked_at` for the actor token inside the advisory-locked transaction that issues the child. `childtoken.Minter.MintChildToken` validates in process and returns an unsigned struct with a random `jti`, and it writes no `issued_tokens` or `token.exchanged` row. The returned token has no consumer: nothing delivers it to the child pod, and its only read is the `delegation.spawned` audit detail, whose `child_token_jti` points at no recorded token. On the production path a pod's `lenny/delegate_task` arrives through the GatewayControl platform-tool bridge, which builds a principal from the session row with no JTI and no scopes. `MintChildToken` skips its revocation-cache check when the parent JTI is empty, so in-pod delegation runs no actor-freshness check at all, `DELEGATION_PARENT_REVOKED` never fires on that path, and the in-process validation compares the parent's own claims with themselves and enforces no scope narrowing. The cache check runs only for edge `/mcp` callers that hold a JWT. A §13.3 recursive revocation cannot reach any child, because no child has an `issued_tokens` row.

The specification contradicts itself on the premise underneath both gaps, so the gaps need a proposal. The §10.2 boundary table gives Gateway↔Pod as "mTLS + projected service account token", and the §13.3 connector-credential flow says the pod receives session context and a projected SA token and never receives client tokens. The §10.2 JWT-claims list says pods present `session_capability` tokens back and that the gateway validates the signature on every pod→gateway request. The code follows the boundary table. The proposal first decides whether pods hold Lenny-issued JWTs. If they do, it settles these points: (1) the `actor_token` for a root parent, which is the session-creation token; (2) the `subject_token` at delegation time, where §8.2 names the authenticated user's JWT, the §13.3 RFC 8693 parameter table names "the tenant's root-session token", and the gateway holds only `sess.UserID` when it serves a pod-origin `delegate_task`; (3) which process signs the child token and writes its rows, given that §8.2 calls the exchange an in-process Token Service call, §4.3 runs the Token Service as its own Deployment, and §13.3 routes internal child minting through the Token Service's single write-before-issue exchange path, and how the gateway authenticates as that internal caller; (4) how the session token reaches the MCP client in the `POST /v1/sessions` response and the pod, and how the child token reaches the child pod; and (5) the credential the pod-origin `delegate_task` path presents. If pods do not hold Lenny JWTs, the proposal reconciles §10.2 with the boundary table and §13.3, either confines `session_capability` to client-facing paths or drops the session-creation mint, and makes parent revocation reach children through a durable record such as the session tree. Either way, the proposal tests revocation through the parent, the actor-freshness check on the pod path, the KMS-unavailable path, and the fail-closed paths across tiers, and it builds any session-creation mint to carry the obligations proposal 0102 adds to it.

A gateway-to-pod renewal path, and moving pod-held tokens from the 24-hour per-dialect cap to the `auth.capabilityTokenTtlSeconds` default, are a separate problem. §10.2 already keeps the session-creation token on its per-dialect cap because no refresh path exists, and the child `exp` formula `min(parent.exp, now + leaseTTL)` holds either way. Renewal belongs in a follow-up proposal unless the owner requires it as a condition of the mint.

## Evidence

- verified: spec/10_gateway-internals.md §10.2 "Signing topology" (the gateway mints `session_capability` on the session-creation path and records it in `issued_tokens` with `token.exchanged` before returning it).
- verified: spec/10_gateway-internals.md §10.2 JWT claims, `typ` `session_capability` ("Minted by the session-creation path and returned to MCP clients for WebSocket upgrades. Pods present these back to the gateway.").
- verified: spec/10_gateway-internals.md §10.2 boundary table (Gateway ↔ Pod: "mTLS + projected service account token"; Pod → Gateway: "Projected service account token").
- verified: spec/10_gateway-internals.md §10.2 expiry ("The `session_capability` token minted on the session-creation path keeps its per-dialect cap"; "Tokens whose holder has no refresh path do not take the default").
- verified: spec/10_gateway-internals.md §10.2 "KMS signing failure mode" (covers every mint path on the gateway and the Token Service) and the paragraph after it ("The gateway validates the signature on every pod→gateway request").
- verified: spec/13_security-model.md §13.3 connector-credential flow ("Pod receives: session context + projected SA token"; "Pod never receives: client tokens").
- verified: spec/13_security-model.md §13.3 RFC 8693 parameter mapping (`subject_token` for child minting is "the tenant's root-session token"; `actor_token` is "the parent session's token").
- verified: spec/13_security-model.md §13.3 per-dialect cap (`lenny-gateway` 24 hours), step (1a) inside the advisory-locked transaction, and recursive child revocation on a revocation request.
- verified: spec/08_recursive-delegation.md §8.2 "Child-token minting (internal RFC 8693 exchange)" (`subject_token` set to the authenticated user's JWT; "the exchange is an in-process Token Service call") and "Actor-token freshness under concurrent parent rotation/revocation".
- verified: spec/04_system-components.md §4.3 Token Service (separate Deployment called by gateway replicas over mTLS).
- verified: spec/28_communication-channels.md does not mention `session_capability`, capability tokens, or child tokens.
- verified: pkg/gateway/sessionserver has no non-test reference to a signer or `session_capability`; the session response carries only an `uploadToken` (pkg/gateway/sessionserver/session_response.go).
- verified: pkg/gateway/mcpfabric/delegation/childtoken/childtoken.go `MintChildToken` (freshness check guarded by `p.ParentJTI != ""`; synthetic subject and actor tokens built from the same parent fields; unsigned `delegation.ChildToken` with `JTI: m.idFn()`; no row write).
- verified: pkg/gateway/gatewaycontrol/platformtools/platformtools.go `CallPlatformTool` (principal with `Subject`, `TenantID`, `SessionID`, and `CallerType: "agent"`; no JTI and no scopes).
- verified: pkg/gateway/mcpfabric/mcptools/mcptools_register.go `delegate_task` handler (`ParentToken.JTI` and `Scope` copied from the principal).
- verified: pkg/gateway/mcpfabric/delegation/service.go (`buildChildSession` mints, `Delegate` returns `Result.ChildToken`, `emitSpawnedAudit` is the only read; the doc comment on `Result.ChildToken` claims a hand-off to the child pod that no code performs).
- verified: cmd/lenny-gateway/token_signing.go (the gateway `jwtSigner` serves impersonation bearers, the admin credential, and playground bearers); pkg/gateway/mcpfabric/playground/token.go is the only non-test `session_capability` mint.
- verified: BUILD-GAPS F-10.2.25 and F-8.2.31 are open, Medium, and both carry the 2026-10-26 reroute into one proposal (lead only).
- drifted: the phrase "§13.3 recursive revocation keyed on issued_tokens" does not appear in the specification. §13.3 says a revocation request triggers recursive child revocation and points to §8.3; the "keys on" wording comes from a code comment. The substance holds.

## Who observes it

- An auditor who follows `child_token_jti` from a `delegation.spawned` audit event finds no `issued_tokens` row and no `token.exchanged` row for it.
- An operator or tenant admin who issues a §13.3 recursive revocation to cut off a compromised delegation tree sees no token-level effect below the parent, because the Token Service's `cascade_from_parent` walk over `issued_tokens` finds no children. Whether another session-tree mechanism stops the child sessions was not established.
- A parent pod whose user credential was revoked or rotated can still spawn children through `lenny/delegate_task`, because the pod path runs no actor-freshness check.
- An operator testing KMS outage behavior never sees `KMS_SIGNING_UNAVAILABLE` on `POST /v1/sessions`, and the §17.8.2 capacity sizing assumes one mint per created session that does not happen.
- No pod or MCP client flow breaks today. Pods authenticate to the gateway by SPIFFE mTLS, MCP clients reach sessions with their own bearer, and no gateway endpoint requires a `session_capability` token.

## What breaks if nothing changes

- Parent revocation has no durable record to act on for delegation children: `DELEGATION_PARENT_REVOKED` never fires on the pod path, and recursive revocation revokes nothing below the parent.
- The child token enforces no scope narrowing, because the in-process validation compares the parent with itself. Any narrowing comes only from the lease and session-row stamping.
- The specification keeps stating a session-creation mint, a write-before-issue record, a KMS failure mode on session creation, and a pod-presented token that the code does not implement, and it keeps contradicting its own boundary table.
- TEST-GAPS T-8.2.7 and T-10.2.2 stay open, because the behaviors they cover do not exist to test.

## Findings this unblocks

- BUILD-GAPS F-10.2.25 (the session-creation mint half).
- BUILD-GAPS F-8.2.31.
- TEST-GAPS T-8.2.7 and T-10.2.2.
- The step (1a) impersonation refusal and rejection-audit discipline that proposal 0102 applies to the session-creation mint, which 0102 says holds in code once that mint lands.

## Prior art considered

- Proposal 0097 (Implemented) scoped this work out on purpose. It listed the session-creation mint, child-token signing, and the gateway-to-pod renewal path as follow-ups, and its decision D13 kept the session-creation token on its per-dialect cap because no renewal path exists. Its Edit 4 landed the step (1a) actor-freshness text in §8.2 and §13.3, so the proposal makes code match that text rather than define the check. The 0097 deviations record confirms the revocation-cache read is a gateway-side pre-check and the mint does not call the Token Service.
- Proposal 0102 (Approved, spec steps unticked) applies step (1a) and its impersonation refusal to the session-creation mint (D20), adds the rejection-audit discipline (D22), and edits the same §10.2 "Signing topology" paragraph (SPEC-2). This proposal has to coordinate its edits to that paragraph with 0102 and build the mint to carry those obligations.
- No other proposal stages this change. BUILD-GAPS records the owner of both findings as "not yet written".
- An authenticated gateway-to-Token-Service channel exists: gRPC over mTLS on port 50052, used by `credassign.Client`. The `TokenService` gRPC service exposes `AssignCredentials`, `RotateCredentials`, `RevokeCredentials`, and `ProbeSecretAccess`, and has no exchange RPC. The specification states no internal-caller authentication for the exchange.
- The §4 Proactive Lease Renewal loop (a `renewBefore` timestamp and a gateway push over existing RPCs) and `AssignCredentials` tmpfs delivery are patterns a delivery or renewal design could reuse. Neither delivers session or child JWTs today.
- TEST-GAPS T-8.2.7 (child-token minting and the actor-freshness race, tested only in process against a stub) and T-10.2.2 (the untested `POST /v1/sessions` half of the KMS failure mode) track parts of the required tests.

## Validated premises

### Premise lens (verdict: revise)

- Holds: §10.2 states the session-creation mint, the `issued_tokens` and `token.exchanged` record, the client use, and the pod use. Verified in §10.2 "Signing topology" and JWT claims.
- Holds: no Go code mints a session-creation `session_capability` token; the playground path is the only mint. Verified by grep of `pkg/gateway/sessionserver` and the playground token file.
- Holds: the session-creation token keeps the per-dialect cap (24 hours for `lenny-gateway`), and 0097 deferred renewal. Verified.
- Holds: `MintChildToken` returns an unsigned token with a random `jti`, writes no row, and its doc comment says the step (1a) re-read does not run on this path. Verified.
- Refinement, accepted: on the pod path the principal carries no JTI and no scopes, so no freshness check runs and the requested scope is empty. Verified in `platformtools.go`, `mcptools_register.go`, and `childtoken.go`. This corrects the original statement's claim that the minter checks freshness against the gateway revocation cache.
- Refinement, accepted: open point (2) is a contradiction rather than a silence, because §8.2 names the user's JWT and the §13.3 parameter table names "the tenant's root-session token". Verified.
- Holds: the gateway holds only `sess.UserID` on the pod path, and admission rejects an empty user with `ErrParentNoUser`.
- Holds, with context: §13.3 routes internal child minting through the Token Service's single exchange path, which partly settles open point (3) and leaves internal-caller authentication open. Verified.
- Holds: the KMS failure mode cannot occur on session creation, and recursive revocation cannot reach a child with no row.

### Evidence lens (verdict: stands)

- Every cited specification passage and code claim verified, as listed under Evidence.
- Drifted, minor: "§13.3 recursive revocation keyed on issued_tokens" is not specification wording. The substance holds.
- §28 never mentions `session_capability` or child tokens, so delivery and renewal are unstated. Verified.

### Prior-art lens (verdict: stands)

- No proposal stages this change; 0097 scoped it out and named this follow-up. Verified in the 0097 summary.
- The session-creation mint is stated in §10.2, so that half is missing code rather than a specification gap, and open point (1) is a dependency on the missing mint. Accepted.
- The atomic freshness check is already stated through 0097 Edit 4. Accepted.
- Proposal 0102 touches the same mint and paragraph. Verified its status as Approved. Recorded under Prior art considered.
- Candidate internal channel and the absence of an exchange RPC recorded under Prior art considered.

### Scope lens (verdict: revise)

- The two findings are one problem: the root parent's `actor_token` is the session-creation token, and the code fakes it with a synthetic actor of type `session_capability`. Verified in `childtoken.go`. Accepted.
- Pod delivery for both tokens belongs in the same proposal. Accepted as open point (4).
- The client-facing delivery in the `POST /v1/sessions` response is a distinct consumer and the proposal states it separately. Accepted.
- Renewal is a separate problem and is cut to a follow-up, because §10.2 and 0097 D13 already keep the token on its per-dialect cap when no refresh path exists. Accepted, with the owner able to require renewal as a condition of the mint.

### Impact lens (verdict: revise)

- The minted child token has no consumer beyond the spawn audit detail, and the `Result.ChildToken` doc comment's claim of a hand-off to the child pod is not implemented. Verified in `service.go`.
- No freshness check runs on the pod path, so `DELEGATION_PARENT_REVOKED` never fires there. Verified.
- The in-process validation compares the parent with itself and enforces no scope narrowing. Verified in `childtoken.go`.
- Recursive revocation cannot reach children; whether a session-tree mechanism stops them was not established.
- The `session_capability` half breaks no client or pod flow today; its case rests on the KMS failure mode, capacity sizing, and being the root `actor_token`. Accepted.

### Alternatives lens (verdict: revise)

- The specification contradicts itself on whether pods hold Lenny JWTs: the §10.2 boundary table and §13.3 credential flow say mTLS and a projected SA token, while the §10.2 JWT-claims list says pods present `session_capability` back. Verified. Accepted as the first decision the proposal makes; if pods hold no Lenny JWTs, open points (1), (2), and (4) for the pod path fall away.
- The WebSocket-upgrade use is named only in the §10.2 JWT-claims list, so a specification-only option of confining or dropping the session-creation mint is open. Accepted as an alternative.
- A smaller fix could key parent revocation on the session tree rather than on per-child `issued_tokens` rows. Accepted as an alternative to weigh.
- The conclusion that a proposal is needed holds either way, because the specification has to change.
