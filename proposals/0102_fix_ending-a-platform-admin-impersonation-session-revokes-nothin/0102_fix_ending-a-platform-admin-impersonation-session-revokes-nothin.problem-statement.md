# Problem: Ending an impersonation session does not revoke its bearer

## Statement

Ending a platform-admin impersonation session revokes nothing. The impersonation bearer stays valid on every gateway replica and on lenny-ops until its `exp`, which is at most one hour. `Service.Issue` in `pkg/gateway/credentials/impersonation/service.go` writes the `admin.impersonation_started` audit row, signs a `user_bearer` for the target user in the target tenant with `JWTID` set to the ticket ID, and records the ticket with `store.Put`. It writes no `issued_tokens` row. The claims are `Issuer`, `Subject`, `Audience`, `Expiry`, `NotBefore`, `IssuedAt`, `JWTID`, `TenantID`, `CallerType: "human"`, `Roles`, and `Typ: user_bearer`, so nothing marks the token as an impersonation bearer. `Service.End` and the shared `end` path write `admin.impersonation_ended` and call `store.MarkEnded`, and nothing else. The gateway wires that store as `impersonation.NewMemStore()` in `cmd/lenny-gateway/adminrouter.go`, a mutex-guarded map local to the replica that no verifier reads. The gateway auth middleware refuses a Lenny-issued token only when its `jti` is in the revocation cache, and the cache is filled from revoked `issued_tokens` rows and explicit revoke calls. The lenny-ops revocation set in §25.4 is built from the same rows and from `token.revoked` events. A bearer with no `issued_tokens` row therefore cannot become revoked by any route.

The same missing record has further consequences. A §11.4 full revoke of the target user misses the bearer, because step 5 revokes rows in the issued-token index and the bearer has none. The operator single-token revoke, `POST /v1/admin/issued-tokens/{jti}/revoke`, returns `404 RESOURCE_NOT_FOUND` for the bearer before it reaches the local revocation cache, so an operator has no workaround short of rotating the signing key. The session record and the one-minute expiry sweep are both local to the replica. `DELETE /v1/admin/impersonation/{id}` on a replica that did not issue the session returns 404, and `GET /v1/admin/impersonation` lists only that replica's sessions. When the issuing replica restarts before `exp`, the record is lost and `admin.impersonation_ended` is never written, which leaves an unmatched start in the audit chain that §16.7 requires to be paired.

The problem has two parts with different spec status. The full-revoke miss is a code defect under the current specification: §11.4 step 5 requires a full revoke to revoke the user's issued tokens in the durable issued-token index, §13.3 "Authoritative durability for revocation" makes `issued_tokens` the authoritative revocation store, and the bearer's `sub` is the target user. The rest is a spec gap. No section states that ending a session revokes its bearer, whether revoking the impersonating admin also revokes the bearers that admin holds, that the session record is durable and visible to every replica, or what the audit trail records when a replica is lost. The specification does not define the impersonation endpoints at all: no file under `spec/` or `schemas/` names `/v1/admin/impersonation`, and the one-hour cap exists only in code as `MaxDuration: time.Hour`. §15 mentions impersonation only in the `PLATFORM_AUDIT_REGION_UNRESOLVABLE` error row and a domain list.

The proposal states in the specification that the impersonation bearer is recorded where every verifier's revocation check reaches it, that ending the session revokes it through the existing `revoked_at` write and `token.revoked` propagation, and that a §11.4 full revoke of the target user revokes it. It specifies the POST, GET, and DELETE `/v1/admin/impersonation` endpoints in §15 with their error codes and the duration cap, and it states the durability and cross-replica behavior of list and end and what the audit trail records. It decides explicitly whether a full revoke of the impersonating admin reaches the bearer; that path differs from the target-user path because the admin acts from the platform tenant while the bearer's row carries the target tenant and subject, so `RevokeBySubject` in the target tenant does not reach it. It then implements and tests the result, including a bearer ended on one replica being refused on every replica and on lenny-ops, a §11.4 full revoke, expiry, an end on a non-issuing replica, a restart of the issuing replica, and the fail-closed paths.

Design points the proposal resolves rather than reopens:

- Recording the bearer in `issued_tokens` with `jti` equal to the ticket ID lets gateway replicas and lenny-ops (§25.4 rules 1 and 2) refuse a revoked bearer with no §25.4 change. A playground-style separate revocation key would need a new lenny-ops rule like rule 7 and is the larger intervention.
- The row carries `tenant_id` and `sub` of the target, so it is single-tenant. Proposal 0097 warns against writing a `token.exchanged` audit row for the cross-tenant mint, because §13.3 "Tenant-scope enforcement" states that `token.exchanged` always carries a single `tenant_id`. The recording step must therefore not reuse the `token.exchanged` audit discipline unchanged.
- `issued_tokens` lookups take a tenant ID. Ending by ticket ID on a non-issuing replica needs either a durable session record that yields the target tenant or a tenant-independent lookup by `jti`. The proposal weighs ending by `jti` through `issued_tokens` alone against a separate durable impersonation-session table. Cross-replica listing and the ended-audit payload (reason, ticket reference, and admin subject) need fields the `issued_tokens` columns lack, although `act_sub` can carry the admin.
- With a shared store, the per-replica expiry sweep could write `admin.impersonation_ended` twice for one session unless the claim is coordinated or idempotent.
- A durable session record carries `target_tenant_id` and `target_user_id`, which §11.7 CMP-058 treats as regulated data routed to `PlatformPostgres(region)`, so the table's location is a residency decision.
- Writing an `issued_tokens` row also lets a revoked impersonation bearer be refused as a `/v1/oauth/token` `subject_token` under §13.3 write-before-issue step (1a), which passes a token with no row today.

## Evidence

- (verified) `pkg/gateway/credentials/impersonation/service.go`: `Issue` appends the started audit row, signs claims with `JWTID: t.ID`, `TenantID: t.TargetTenantID`, and `Typ: auth.TokenUserBearer`, then calls `store.Put`, with no `issued_tokens` write. `End` and `end` append `admin.impersonation_ended` and call `store.MarkEnded` only. `defaultMaxDuration = time.Hour`.
- (verified) `pkg/gateway/credentials/impersonation/store.go`: `MemStore` is a mutex-guarded map with `Put`, `Get`, `MarkEnded`, `ListActive`, and `DueForExpiry`. Its doc comment states that a replica-local in-memory store is sufficient for v1 and that the bearer is self-expiring.
- (verified) `cmd/lenny-gateway/adminrouter.go`: wires `impersonation.NewMemStore()` with `MaxDuration: time.Hour`.
- (verified) `cmd/lenny-gateway/workers.go`: the impersonation expiry sweep is a one-minute ticker per replica over that replica's service.
- (verified) `pkg/gateway/middleware/auth/auth.go` and `pkg/gateway/credentials/revocation/revocation.go`: a token is refused when `Revocations.IsRevoked(jti)` is true, and the cache is filled from `ListRevoked` rows and explicit `Revoke` calls. The revocation package contains no reference to impersonation.
- (verified) `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go`: `Record`, `Get(tenantID, jti)`, `Revoke(tenantID, jti, ...)`, `RevokeBySubject(tenantID, subject, ...)`, and `ListRevoked`, all tenant-scoped.
- (verified) `pkg/gateway/externalapi/admin/issued_tokens.go`: the manual revoke returns `404 RESOURCE_NOT_FOUND` on `issuedtokenstore.ErrNotFound` before it updates the local revocation cache.
- (verified) `pkg/gateway/externalapi/admin/impersonation.go` and `routes.go`: the POST, GET, and DELETE `/v1/admin/impersonation` handlers, a platform-admin role check, and `ErrNotFound` mapped to 404.
- (verified) `charts/lenny/values.yaml`: the gateway runs with `minReplicas: 2` by default.
- (verified) spec/13_security-model.md §13.3: "Tenant-scope enforcement" names impersonation as a distinct code path not routed through `/v1/oauth/token`; "Write-before-issue ordering" step (1a) passes a token with no `issued_tokens` row; "Authoritative durability for revocation" makes `issued_tokens` the authoritative revocation store.
- (verified) spec/16_observability.md §16.7: `admin.impersonation_started` payload fields and "A matching `admin.impersonation_ended` event is emitted when the impersonation session terminates". Nothing about revocation or storage.
- (verified) spec/11_policy-and-controls.md §11.4 step 5 revokes the user's issued tokens in the durable issued-token index; §11.7 "Platform-tenant audit event residency (CMP-058, fail-closed)" lists both impersonation events.
- (verified) spec/10_gateway-internals.md §10.2 "Signing topology" lists platform-admin impersonation bearers among the gateway's in-process mints and records `issued_tokens` only for session-creation `session_capability` tokens.
- (verified) spec/12_storage-architecture.md §12.2: `issued_tokens` columns are `jti`, `tenant_id`, `sub`, `token_hash`, `scope`, `audience`, `issued_at`, `exp`, `revoked_at`, `revoked_reason`, `act_sub`, and `parent_jti`.
- (verified, corrected) spec/25_agent-operability.md §25.4 "Revocation set" rules 1 to 7: a Lenny-issued token is refused only when its `jti` came from revoked `issued_tokens` rows or `token.revoked` events, or when it carries the playground origin. The section never mentions impersonation. That lenny-ops honors an impersonation bearer until `exp` follows from rules 1 to 5 and is stated in proposal 0097's summary (D25), not in §25.4.
- (verified, corrected) spec/15_external-api-surface.md defines no impersonation endpoint. Its only impersonation mentions are the `PLATFORM_AUDIT_REGION_UNRESOLVABLE` error row and a domain list.
- (verified, lead only) BUILD-GAPS.md F-13.3.20, rated High and OPEN.

## Who observes it

A platform admin or incident responder who ends an impersonation session early, because the bearer leaked, the support task finished, or misuse is suspected. The responder receives a success response and an `admin.impersonation_ended` audit row, while the bearer keeps the target user's full roles in the target tenant for up to one hour. A SIEM consumer reads a termination that did not take effect, and the data subject's Article 15 export (§12.8) of the impersonation event pair records the same false end. An operator running a §11.4 full revoke of the target user, or a manual single-token revoke of the bearer's `jti`, sees the bearer survive or receives a 404. In the default topology of at least two gateway replicas, an admin whose DELETE or GET reaches the wrong replica sees 404 or a partial list.

## What breaks if nothing changes

- An explicit end of an impersonation session has no effect on access. The bearer verifies on every gateway replica and on lenny-ops until `exp`.
- A §11.4 full revoke of the target user leaves the bearer valid, which breaks the guarantee that a full revoke ends the user's issued tokens within seconds.
- The single-token revoke endpoint refuses the bearer, so the only control is the one-hour cap or a signing-key rotation.
- The audit trail records terminations that did not revoke anything.
- End and list on a non-issuing replica fail or return partial results.
- A restart of the issuing replica loses the session, so `admin.impersonation_ended` is never written and the §16.7 start/end pair is broken.

The exposure is bounded: the cap is one hour, only a platform admin can issue a bearer, and harm needs the bearer to be misused within the window. The cap is still the only control, and expiry alone does not satisfy §11.4.

## Findings this unblocks

- BUILD-GAPS F-13.3.20 (High).
- The remediation plan's queued "Impersonation revocation proposal", first after 0097 (`gateway-runtime-comms-remediation.md`).

## Prior art considered

- Proposal 0097 (Implemented) records this gap and leaves it out of scope. Its summary lists "making an impersonation bearer revocable (D25)" under out-of-scope items and records the defect in its unstaged-defects list. It rejected a new impersonation claim with a lenny-ops refusal because every gateway replica would still accept the token.
- Proposal 0097's review log warns against extending the `token.exchanged` recording rule to impersonation bearers, because a cross-tenant `token.exchanged` row falsifies §13.3's single-`tenant_id` statement.
- No other proposal stages this change. 0019, 0028, and 0098 mention impersonation for unrelated reasons; 0099, 0100, and 0101 do not mention it.
- The admin-token provisioner (`pkg/gateway/externalapi/admintoken/admintoken.go`) already records its gateway mint in the issued-token store and revokes it durably, which is a pattern to follow.
- The playground bearer uses its own revocation key (§27.3.1) and lenny-ops rule 7. That precedent is not the smaller fix here, because it would need a new lenny-ops rule.
- A general rule that every gateway mint records `issued_tokens` would pull in the playground bearer, which deliberately uses its own key, so the problem stays specific to impersonation. Impersonation is the only gateway mint without a revocable record.

## Validated premises

Premise lens (verdict: revise)

- P1 stands. `Issue` signs a target-user `user_bearer` with `JWTID` equal to the ticket ID, writes no `issued_tokens` row, and carries no impersonation marker.
- P2 stands. `End` and `end` write the ended event and call `MarkEnded` only, on a replica-local `MemStore`.
- P3 stands. No verifier reads the impersonation store, and the revocation cache is filled only from `issued_tokens` and explicit revokes, so the bearer cannot become revoked.
- P4 stands. lenny-ops accepts the bearer until `exp` under §25.4 rules 1 to 5; rule 7 refuses only the playground origin.
- P5 stands. A §11.4 full revoke misses the bearer. `RevokeBySubject` in the target tenant would reach a recorded row with no new mechanism, while revoking the admin would not, because the admin acts from the platform tenant.
- P6 stands and is extended. The expiry sweep is also local to the replica, so a restart of the issuing replica loses the session and the ended event is never written.
- P7 partly refuted in detail. §15 does not define the impersonation endpoints; `/v1/admin/impersonation` has zero occurrences in `spec/`, and the one-hour cap exists only in code. The spec gap is wider than the original statement said.
- P8 stands. The specification is silent on revoke-on-end and durable session storage, and a recorded row would also make a revoked bearer refusable as a `/v1/oauth/token` `subject_token`.

Evidence lens (verdict: stands)

- Verified the `Issue`, `End`, and `end` code paths, the `NewMemStore()` wiring, the `MemStore` doc comment, and `defaultMaxDuration = time.Hour`.
- Verified BUILD-GAPS F-13.3.20 as an audit lead confirmed independently by the code.
- Verified §13.3, §16.7, §11.4 step 5, §11.7 CMP-058, and §10.2 "Signing topology" say what the statement relies on.
- Drifted: §25.4 never mentions impersonation; the "honored until `exp`" conclusion is an inference from rules 1 to 5 recorded in 0097's summary.
- Partly false: §15 defines no impersonation endpoint; the routes exist only in code and generated artifacts.
- Verified the cited code directories exist and the revocation package does not reference impersonation.

Prior-art lens (verdict: stands)

- No proposal stages the change; 0097 records the gap and scopes it out under D25.
- F-13.3.20 is OPEN and queued in the remediation plan.
- No spec mechanism already answers the problem.
- The routes and any durable session table are absent from `spec/`, `schemas/`, and `migrations/`.
- Reusable mechanisms exist (`issued_tokens`, `revoked_at`, `token.revoked`), with 0097's caution about a cross-tenant `token.exchanged` row. On verification, that caution concerns the `token.exchanged` audit row; an `issued_tokens` row with the target's `tenant_id` and `sub` is single-tenant.

Scope lens (verdict: stands)

- The problem is one proposal, because every part comes from one defect: the session has no durable record that the revocation path reaches.
- Bearer revocation and the durable session record cannot be split cleanly, because `issued_tokens` operations are tenant-scoped and an end on another replica holds only the ticket ID.
- The lenny-ops side needs no separate change once the bearer is recorded and revoked through the existing path.
- The endpoints are absent from the specification and must be specified, which enlarges the spec work.
- Boundary risk: whether a revoke of the impersonating admin reaches the bearer must be decided in the proposal.
- Boundary risk: a durable store brings duplicate ended events from per-replica sweeps and a CMP-058 residency question for the table.
- Impersonation is the only gateway mint without a revocable record, so the problem is not one instance of a wider class.

Impact lens (verdict: stands)

- The defect is reachable in the shipped wiring.
- The observers are admins and incident responders ending a session early, SIEM consumers, and data subjects reading their Article 15 export.
- The manual single-token revoke returns 404 for the bearer, leaving no workaround short of key rotation.
- The default two-replica topology makes end and list unreliable, and a restart loses the ended event.
- The exposure is bounded by the one-hour cap and the platform-admin requirement, and the issue still merits a proposal.

Alternatives lens (verdict: revise)

- A no-change reading does not hold; the code defect is real.
- Refuted in part: the claim that the specification is silent overstates the full-revoke half. §11.4 step 5 together with §13.3 "Authoritative durability for revocation" already requires the bearer to be revocable through `issued_tokens`, so the full-revoke miss is a code defect under the current specification. Only revoke-on-end, the admin cascade, the endpoints, and durability are new spec content.
- The lenny-ops half needs no §25.4 change if the bearer is recorded in `issued_tokens`.
- The endpoints have no spec basis and must be specified or scoped out. Ending by `jti` through `issued_tokens` alone is a smaller design that the proposal weighs against a separate durable session table.
- No smaller upstream problem dissolves this one, because generalizing recording to every gateway mint would wrongly include the playground bearer.
- The one-hour cap does not mitigate the problem enough to need no change.
