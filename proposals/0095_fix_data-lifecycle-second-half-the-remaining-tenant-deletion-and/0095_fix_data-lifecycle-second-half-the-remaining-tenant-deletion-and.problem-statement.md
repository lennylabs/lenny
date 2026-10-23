# Problem: Data lifecycle second half: erasure store coverage, revocation at user and tenant scope, and the audit-receipt foreign key

## Statement

This is proposal P5b of `gateway-runtime-comms-remediation.md` §10.2 ("P5b. Data lifecycle, second half"). It follows three landed changes:

- Proposal 0094 (P5, implemented 2026-10-21) landed the creation fence inside the session insert transaction, the session-ID reservation, the shared drain `admin.DrainSessions` used by tenant suspension, tenant-deletion Phase 2, and user erasure, and the durable Postgres erasure job run by the gateway leader's sweep. Its decision D20 split this proposal off.
- Proposal 0093 (P1, implemented 2026-10-21, merge `77edce7b4`) defines how a pod, slot, claim, or credential lease is released: the per-session credential release (`Credentials.ReleaseSession`, called from `terminalReclaimPreRunning`), the full_revoke lease step (`leaserevoke.Revoker`), the full_revoke release routing, and the BeginTerminal fence. `ReleaseThrough` was introduced earlier by proposal 0092, and 0093 calls it. The phrase "P1's lease reclaimer" does not name a single 0093 mechanism; each P5b call site names the specific function it calls.
- The Redis Cluster readiness direct fix (merged 2026-10-21) routes every SCAN through `redisconn.ScanKeys` and `redisconn.ScanDelete` and builds tenant keys through `rediskeys.TenantPrefix`. A new Redis eraser over tenant-prefixed keys uses those helpers. Keys without a tenant prefix (the access-token cache, the routing cache, and the delegation budget keys) are outside that rule and are erased per identifier.

### Problem

After 0094, user erasure and tenant deletion stop the sessions they cover. Two independent defects remain, and they share only the word "lifecycle".

**Part A, erasure store coverage.** The user-erasure and tenant-deletion eraser lists are assembled by hand in `cmd/lenny-gateway/adminrouter.go` and `cmd/lenny-gateway/tenant_erasers.go`. `erasure.ValidateOrder` and `erasure.ValidateTenantOrder` check that each name is known, unique, and in rank order. Neither checks completeness against §12.8, so a missing store passes startup without a signal. The two lists have drifted in both directions: the user path erases session message queues and delegation budget keys and has no checkpoint erasers, and the tenant path has checkpoint erasers and no message-queue or delegation-budget eraser. Most of the missing erasure is wiring of `DeleteByUser`, `DeleteByTenant`, or `DeleteBySession` methods that already exist, into ranks that `erasure/order.go` already defines. Two rows need more than wiring: the audit eraser collides with the `audit_redaction_receipts` foreign key, and the audit and artifact erasers must skip Phase 3.5 legal-hold escrow records, which neither implements.

**Part B, revocation at user and tenant scope.** full_revoke runs its whole continuation on the request context, answers 500 with no counts on a per-session error, lists sessions once from the read pool, and deny-lists a shared pool credential for every user. Tenant-deletion Phase 3 is a no-op: no `CredentialRevoker` is wired, `CredentialPoolStore.RevokeByTenant` does not exist, and the tenant's issued tokens stay valid until the late Phase 4 `tokens` eraser. Under a Phase 3.5 legal-hold pause that window has no end. The fix pattern for full_revoke already exists in 0094's drain (primary listing, continue past a per-session failure and record it, `context.WithoutCancel`).

The two parts have no code dependency on each other. Whether they land as one proposal or two is an open question for the owner (see the scope lens below). One ordering constraint is fixed: the F-12.8.25 foreign-key decision lands before or together with the Phase 4 audit eraser.

### Members

Re-confirmed against the tree at `c9c706207`. Severity is the BUILD-GAPS label; the impact column records what the impact lens established.

Part A, erasure store coverage:

- F-12.8.34 (High): tenant-deletion Phase 4 wires `leases`, `quota`, `transcripts`, `interactions`, `memory`, `session_tree_archive`, `eval_results`, the three checkpoint erasers, `sessions`, `tokens`, and `credential_pool`, and nothing else. Missing relative to the §12.8 Phase 4 order: `SemanticCache`, Redis caches, experiment sticky, billing Redis stream, `ArtifactStore`, `EventStore` audit and billing, `EvictionStateStore`, session message queues (F-12.8.37), delegation budget keys, `runtime_tenant_access`, `pool_tenant_access`, user role mappings (`userstore.DeleteByTenant` exists and is unwired), custom role definitions, and the tenant configuration record. Impact: object-store artifacts stay readable after the tombstone and Phase 6 receipt for every tenant below T4, because Phase 4a destroys the KMS key only for T4.
- The §12.8 `TokenStore` mapping. §12.2 defines `TokenStore` as downstream OAuth and refresh tokens (`TokenStore.save_refresh_token(user_id, connector_id, ...)`). The tree holds those in `connectorcredstore`, which implements `DeleteByUser` and `DeleteByTenant` and is wired into neither erasure path. Both paths wire `issuedtokenstore` (the §12.2 `TokenIssuanceStore`) under the eraser name `tokens`. A store table built from the eraser names would mark `TokenStore` as erased while the erased user's or tenant's connector refresh tokens remain. Also unwired with existing erasure methods: `credentialstore`, `connectorstore`, `environmentstore`, `experimentstore`, and the delegation budget pgstore. Each is in scope or is recorded as a spec gap.
- F-12.8.37 (High): tenant deletion erases no session message queues. The user path already erases them per session through `messagingCoord.EraseSession`. Impact after 0094: small, because the Phase 2 drain runs each session's terminal pipeline, which drains its inbox and DLQ best-effort; residue remains where that drain failed.
- F-12.8.41 (High): user erasure has no `checkpoint_manifests`, `checkpoint_retention`, or `checkpoint_meta` eraser, although the rank table ranks all three. The tenant path's erasers already exist. Impact: small, because these rows hold object keys and generations rather than content.
- F-12.8.35 (Low): without Postgres, the preStop barrier builds a private in-memory checkpoint metadata store that no eraser reaches. Impact: bounded by process restart; a departure with that bound is an acceptable response.
- N-4 step 1: the user-erasure `leases` eraser calls `leasestore.DeleteByUser`, which returns `(0, nil)` in the Redis, Postgres, and failover implementations. Its comment says the orchestrator drives a per-session release; the orchestrator performs none. §12.8 step 1 concerns session coordination leases (Redis, 60-second expiry), unrelated to credential leases. Fix: a per-session delete over the lister's session IDs. Impact: TTL-bounded.
- N-4 step 3, routing-entry half: `redis_caches` is ranked and wired nowhere. `routingcache.RedisCache` has `Invalidate(sessionID)` and a 5-minute TTL, and no production code imports the package. Impact: none today; record it as a spec-alignment item.
- F-12.8.42 (coordination_lease mirror): `coordlease/pgstore` implements `DeleteByUser` and `DeleteByTenant`, and no eraser list references them. The §12.8 scope table and Phase 4 order do not name the mirror; the scope table's `LeaseStore` row is Redis. The mirror is specified in §10.1.8 and REG-COORDMIRROR. It holds no user column and no content. Wiring it as an erasure obligation needs a §12.8 edit, or it is filed as hygiene.
- F-12.8.25 (High), the audit-receipt foreign key: migration 0160 declares `audit_event_id UUID NOT NULL REFERENCES audit_log (id)` with no `ON DELETE` clause. Its live victim today is the §16.4 retention pruner: `PruneRetention` deletes non-`gdpr.%` rows past the cutoff with no exclusion for a redacted dead-letter row, so once such a row ages out, that tenant's prune fails with SQLSTATE 23503 on every cycle (at least when no SIEM endpoint is configured, which bypasses the delivery guard). Phase 4 runs no audit eraser today, so the Phase 4 abort the original statement described is latent and becomes live when F-12.8.34 wires the audit eraser. §12.8 mandates the foreign key and also says a receipt outlives a force-dropped row and is exempt from `DeleteByTenant`, so the fix needs a spec decision first.
- Legal-hold escrow exemption: §12.8 Phase 4 requires the `ArtifactStore` and `EventStore` audit `DeleteByTenant` to skip records Phase 3.5 marked `legal_hold_escrow`. `auditstore.DeleteByTenant` skips only `gdpr.%` rows and `artifactcatalog` has no escrow handling. This is a precondition of wiring those two erasers. F-12.8.45 (legal-hold preflight coverage) touches the same mechanism and joins Part A only if the proposal takes the exemption on.

Part B, revocation:

- F-11.4.10 (High), its two halves that 0093 left open for P5b. Deny-list granularity: a pool lease's `CredentialKey` carries no user, so `leaserevoke.Revoker.RevokeUserLeases` deny-lists a shared pool credential for every user, and it returns that lease to its pool while the credential is deny-listed. Session set: the lease step covers only the sessions `terminateUserSessions` lists as non-terminal, so it skips sessions another writer made terminal.
- F-11.4.12 (High): `terminateUserSessions`, both `runFullRevokeFanOut` calls, and `DismissByUser` run on `req.Context()`. A client disconnect or ingress timeout mid-revoke leaves the user's issued tokens live, because the token revoke runs after the cancel writes and binding release.
- F-11.4.13: a per-session cancel error or an elicitation-dismissal error answers 500 with no counts and no partial-failure detail, contrary to the §11.4 Note. `terminateUserSessions` already returns the sessions cancelled so far and the handler runs the fan-out for them, so the original "drops applied sessions" wording no longer holds. The lease step has already deny-listed the leases of sessions the loop never reached.
- F-11.4.17 (Medium): a cancel write that commits and then reports a store error skips `AfterRevokeWrite`, so that session's inbox and DLQ keys (no TTL) are never drained and an in-flight resume is not cancelled. 0093 recorded the case as accepted departure A25 and filed this finding as owned by P5b.
- F-11.4.14 remainder: `terminateUserSessions` lists with `ListFilter{UserID}` and no `Primary`, so the pgstore reads the read pool. A session committed just before the revoke can escape within the replica-lag window. `ListFilter{Primary: true}`, as `DrainSessions` uses, closes it. §11.4 step 1 says only "via SessionStore", so the proposal decides whether the spec states the primary listing.
- F-12.8.30 (High): no production code assigns `Reconciler.Revoker`, so Phase 3 returns nil. `CredentialPoolStore.RevokeByTenant` exists only in the spec's Phase 3 row. The controller comment says Phase 4 removes the rows "a pass later"; that fails under a Phase 3.5 legal-hold pause or any earlier Phase 4 eraser failure, during which the tenant's issued tokens remain valid. No tenant-state check in the auth path closes the window. Tenant-deletion jobs are rebuilt from `TenantState` and resume at Phase 3 on restart or takeover, so a Phase 3 revoker must be idempotent. There is no tenant-wide credential-lease enumerator (`credleasestore` offers `LeasesBySession` and `LeasesByCredential`), so a tenant revoker lists the tenant's sessions and calls the 0093 release over them. The F-11.4.10 granularity decision applies to it.
- The access-token cache flush (Phase 3, and the token half of N-4 step 3): the cache lives in `lenny-token-service`, keyed `lenny:token:{jti}` with no tenant or user component, with `Invalidate(jti)` and a TTL equal to the token lifetime. `cmd/lenny-token-service/server.go` constructs it and discards it (`_ = w.accessCache`), and nothing calls `Get`, `Put`, or `Invalidate`. A flush deletes nothing today. When the cache is used, a user or tenant flush needs a jti enumeration (for example from the issued-token index, or the IDs `RevokeUserTokens` returns) routed to the Token Service; a tenant-prefix SCAN cannot reach these keys. Record this as a spec-alignment item with a design decision rather than a missing `ScanDelete` call.
- F-12.8.44, token-issuance half (Medium, filed unassigned by 0094): a tenant in `deleting` can still be issued API tokens, which outlive Phase 3. Member of Part B if Part B is in this proposal.

Not members:

- The user registry row. The §12.8 scope table and the `DeleteByUser` steps never name `UserStore`, and **Processing restriction during erasure** sets and later clears `processing_restricted` on the `UserStore` record, which requires the row to survive. Erasing it needs a spec change. At tenant scope, user role mappings stay under F-12.8.34.
- `EvictionStateStore` on the user path: `session_eviction_state` has no foreign key to `sessions`, and the store has no production writer (`evictionfallback` is imported nowhere outside its package). The store table records the row as "no production writer".
- `session_dlq_archive` on the user path: erased by `ON DELETE CASCADE` from `sessions`. The store table records that.
- `session_tree_archive`: already erased on both paths (per-session eraser on the user path; Phase 4 eraser since `4cba97b38`).
- F-12.8.43 (P2, ABSENT claim-register row deferred to R21) and the `session.force_terminated` half of F-11.7.22 (P2).
- F-12.3.25, F-12.8.47, F-12.8.48, and F-15.1.55 are outside the data-lifecycle boundary and stay filed. F-12.8.46 (orphaned derive and export objects) is optional for Part A; the proposal decides and records the decision.

### Spec obligations and open decisions

Most members are code that does not yet implement §12.8 and §11.4 as written; they need no spec edit. The members below carry a spec obligation or an open decision:

1. F-12.8.25: §12.8 states the receipt foreign key, the receipt's survival past a force-dropped row, and the receipt table's exemption from `DeleteByTenant`. Options include dropping the foreign key, `ON DELETE SET NULL` with a nullable column, or retaining receipt-referenced rows in both `DeleteByTenant` and `PruneRetention`. Proposal 0028's decision that receipts outlive the tenant constrains the choice.
2. F-12.8.30: the spec names `CredentialPoolStore.RevokeByTenant`; the proposal either renames the Phase 3 row to the 0093 mechanism it calls or adds the method.
3. F-11.4.10 deny-list granularity (0093 Decision 18, C3): §11.4 step 1 deny-lists every returned lease, and §4.9 **Revocation granularity** scopes revocation to one credential. The proposal decides what full_revoke and a tenant revoker deny-list for a shared pool credential.
4. F-12.8.42: a §12.8 scope-table edit adding the coordination mirror, or a hygiene filing.
5. The access-token cache flush: per-jti invalidation routed to the Token Service, or a spec edit acknowledging the TTL bound.
6. Rank order: `canonicalRank` encodes the user sequence (`eval_results` 120 before `audit` 130 and `billing` 150), and the §12.8 Phase 4 order runs audit, then billing, then `EvalResultStore`. The rank map has no entries for the Phase 4 tail (`runtime_tenant_access`, `pool_tenant_access`, user role mappings, custom role definitions, tenant configuration record), so `ValidateTenantOrder` refuses to start the gateway once any of them is wired. The proposal states one order for the tenant path.
7. Whether full_revoke reports per-session failures in a response field, which touches the §11.4 and §15.1 response.

### Settled context

- P5b decides which stores each erasure path erases, in what order, and when a lease is revoked; 0093 decides how a lease or pod is released. Each call site names the 0093 function it calls.
- Build on 0094's drain, fence, and durable erasure job without redesigning them. A new eraser runs inside the existing erasure job phases or the tenant-deletion phases, idempotently, so a job resumed by another replica re-runs it safely. Tenant-scoped session erasers run over session IDs listed from the primary before the `sessions` eraser, reusing the user path's session-scoped eraser set.
- Out of scope, with no remedies designed here: the terminal pipeline (P2), timers and sweeps (P3), the delegation tree and budget mechanics (P4), Redis ops-lock keys (P6), and release mechanics (P1).

### Drafting rules (owner, plan §10.2)

- Open with the member defect list, re-confirmed against the tree.
- Keep the scope to the data-lifecycle boundary. A defect outside it goes to BUILD-GAPS or its owning group.
- Write a store-to-eraser-to-phase table first. Build it from the §12.8 scope table, the `DeleteByUser` steps, and the Phase 4 order together, because the Phase 4 order names stores the scope table omits. Map each §12.8 role to the tree store that holds its data (for example `TokenStore` to `connectorcredstore`), and record each row's current status for both erasure paths, including "no production writer" and "erased by cascade" where they apply.
- Add a completeness gate so a §12.8 store that is neither wired nor exempted with a recorded reason fails startup or tier 0.
- Add no new per-replica state unless the spec requires it. Prefer durable state.
- Record a residual gap as a departure with a bounding fallback and a claim-register row.
- Rank members by observed impact rather than by label: the Phase 3 token window, the pruner failure, and below-T4 artifact residue first; TTL-bound, content-free, or in-memory residue last.
- State tests as intents with tiers, and make them expansive: table-driven over every store and phase for both erasure paths; an invariant checker that, after every scenario, finds no row or key of the erased principal or tenant in any listed store (Postgres, Redis via the cluster-safe scan helpers, and object storage); randomized interleavings of erasure and tenant deletion against concurrent terminal writes and lease renewals under `-race` (tier 7a); fault injection at each eraser, including store errors, context cancel, a crash between erasers, and replica takeover of a resumed job (tier 8); a full_revoke matrix over session states, per-session store errors, commit-then-error writes, client disconnect, and late-created sessions; multi-replica tier-4 scenarios; and tier-9 checks for cross-tenant isolation of every eraser. TEST-GAPS T-12.8.4, T-12.8.9, T-12.1.3, and T-4.5.4 overlap these intents and close with them.
- Stage the owner's triage rule for failures the tests find. Fix in this proposal what is relevant to it, or what is straightforward and not owned elsewhere. File everything else, with a deviation entry and a `blocked:` skip.

## Evidence

All entries verified against the tree at `c9c706207` unless marked otherwise. Spec citations name the section and paragraph.

- (verified) full_revoke on the request context: `pkg/gateway/externalapi/admin/users.go:651` (`terminateUserSessions(req.Context(), ...)`), `:656` and `:665` (`runFullRevokeFanOut(req.Context(), ...)`), `:670` (`DismissByUser(req.Context(), ...)`). Detached-context precedent: `pkg/gateway/externalapi/admin/tenant_suspend.go:112`, `pkg/gateway/externalapi/admin/drain.go:97`. The BUILD-GAPS line citations for F-11.4.12 have drifted.
- (verified) full_revoke error handling: `users.go:503-505` (returns the sessions cancelled so far), `:533-538` (return on the first `cancelForRevoke` error), `:650-660` (fan-out, then 500 with no detail), `:668-674` (elicitation error 500). §11.4 **Full revoke propagation mechanism** Note requires per-effect counts and partial-failure detail.
- (verified) Read-pool listing: `users.go:512` (`ListFilter{UserID: userID}`); `pkg/gateway/session/sessionstore/pgstore/pgstore.go` `List` reads `s.read` unless `filter.Primary`; `drain.go:83` (`ListFilter{UserID: userID, Primary: true}`).
- (verified) F-11.4.10 and F-11.4.17 OPEN and owned by P5b: `BUILD-GAPS.md` F-11.4.10 resolution note (proposal 0093) and F-11.4.17 suggested resolution; `gateway-runtime-comms-remediation.md` §10.2 ("The deny-list and session-set halves of F-11.4.10: P5b"); `pkg/gateway/credentials/leaserevoke/leaserevoke.go:83-97` (deny-lists `lease.CredentialKey()` after `assigner.Release`); `pkg/gateway/credentials/credleasestore/credleasestore.go:192`, `:222` (no tenant enumerator).
- (verified) Audit-receipt foreign key: `migrations/0160_audit_redaction_receipts.up.sql:20-21`; `migrations/0149_eventstore_partitioned_logs.up.sql:9-17`; `pkg/gateway/audit/auditstore/redaction.go` (UPDATE then INSERT of the receipt in one transaction); `pkg/gateway/audit/auditstore/auditstore.go` `DeleteByTenant` (`event_type NOT LIKE 'gdpr.%'`) and `PruneRetention` (non-`gdpr.%` rows past `GeneralCutoff`, guard bypassed by `opts.Force || !opts.SIEMConfigured`); `cmd/lenny-gateway/auditpipeline.go:209` (pruner wired); `cmd/lenny-gateway/tenant_erasers.go` (no audit eraser). §12.8 **DeleteByUser** step 14 (`audit_event_id (UUID, foreign key audit_log.id)`; receipt outlives a force-dropped row; exempt from `DeleteByUser` and `DeleteByTenant`).
- (verified) Phase 3 no-op: `pkg/controller/tenantdeletion/controller.go:456-464`; no `Revoker` assignment in `cmd/` or `pkg/`; no `RevokeByTenant` in `pkg/` or `cmd/`; §12.8 tenant deletion Phase 3 row. Phase 3.5 pause: `controller.go` `PhaseLegalHoldSegregation` returns `ErrBlockedByLegalHold`. Resume at Phase 3: `cmd/lenny-gateway/tenantdeletion.go:309-316`.
- (verified) Phase 4 eraser list: `cmd/lenny-gateway/tenant_erasers.go:33-111`; §12.8 Phase 4 row (dependency order, escrow exclusion); `pkg/gateway/storage/erasure/order.go:19-44` (`canonicalRank`), `:133-155` (`ValidateTenantOrder`).
- (verified) `TokenStore` mapping: §12.2 **Storage Roles** (`TokenStore` downstream OAuth tokens; `TokenIssuanceStore` the `issued_tokens` index); `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go` package doc; `pkg/gateway/connectors/connectorcredstore/pgstore/pgstore.go:274`, `:300`; `cmd/lenny-gateway/adminrouter.go` user eraser list (`tokens` is `issuedtokenstore`; no `connectorcredstore` eraser).
- (verified) User path checkpoint and queue erasers: `cmd/lenny-gateway/adminrouter.go:1026-1072` (`session_message_queues` via `EraseSession`, `delegation_budget` via `PurgeRoot`, no checkpoint erasers); `tenant_erasers.go:93-110` (checkpoint erasers, `checkpoint_meta` gated on `w.pgPool`); `cmd/lenny-gateway/httpsurface.go:579-584` (private in-memory store).
- (verified) Lease no-op: `pkg/gateway/storage/leasestore/leasestore.go:222-238`; §12.8 **DeleteByUser** steps 1 and 3.
- (verified) Caches: `pkg/tokenservice/cache/cache.go:41` (`lenny:token:`), `Invalidate(jti)`; `cmd/lenny-token-service/server.go:94-101` (`_ = w.accessCache`); no importer of `pkg/gateway/session/routingcache` outside the package.
- (verified) Coordination mirror: `pkg/gateway/coordination/coordlease/pgstore/pgstore.go:99`, `:114`; no `coordination_lease` in `spec/12_storage-architecture.md`; §10.1.8 and §28 REG-COORDMIRROR. The original statement's "§10.1.1 coordination mirror" citation was wrong.
- (verified) User registry row: §12.8 **Processing restriction during erasure** and the scope table (no `UserStore` row); `pkg/gateway/environment/userstore/pgstore/pgstore.go:308`, `:333`.
- (verified) Escrow: no `escrow` handling in `pkg/blobstore/artifactcatalog/catalog.go` or `pkg/gateway/audit/auditstore/auditstore.go`.
- (verified) Eviction state: no importer of `evictionfallback` outside its package.
- (verified) Proposal dates and attributions: 0093 and 0094 status files (`implemented-date: 2026-10-21`); merge `77edce7b4` at 2026-10-21 00:19 UTC; `ReleaseThrough` in proposal 0092's non-spec changes.
- (verified) Unassigned findings: `BUILD-GAPS.md` F-12.3.25, F-12.8.44 to F-12.8.48, F-15.1.55 (all OPEN).
- (unverified, lens report) No tenant-state check in the gateway auth path closes the Phase 3 window; a search for `AcceptsNewWork` and `StateDeleting` finds only the tenant store, the deployment config handler, and the creation fence.

## Who observes it

- A data controller or regulator auditing a deleted tenant: object-store artifacts (workspace snapshots, transcripts, uploaded files), non-`gdpr` audit rows, connector refresh tokens, and role and access records remain after the tombstone and Phase 6 receipt. For tenants below T4 the artifacts stay readable.
- An erased user's data subject: connector OAuth refresh tokens (`connectorcredstore`) survive user erasure, and checkpoint metadata rows remain.
- Operators of any tenant with a redacted dead-letter audit row past retention: the §16.4 pruner fails on every cycle for that tenant.
- Users of a tenant in `deleting`: their issued tokens keep working until Phase 4's `tokens` eraser, indefinitely under a legal-hold pause.
- An operator responding to a compromised account with full_revoke: a disconnect or timeout leaves tokens live; a per-session error returns 500 with no counts; other users of a shared pool credential lose it.

## What breaks if nothing changes

- Tenant deletion completes with a receipt while personal data remains in stores §12.8 names. Wiring the audit eraser to fix that, without the F-12.8.25 decision, makes Phase 4 abort for any tenant with a redacted dead-letter row.
- Audit retention stops for any tenant whose redacted dead-letter row ages out.
- A tenant under a legal-hold pause keeps valid API access for the length of the hold.
- full_revoke on a compromised account can leave tokens valid and report nothing, and deny-lists shared pool credentials for unrelated users.
- The next store added to either path can be omitted without any signal, because nothing checks completeness.
- Low-impact residue (coordination lease keys, routing entries, coordination mirror rows, in-memory checkpoint metadata, queue residue after a failed best-effort drain) persists until TTL, restart, or never; none of it carries content beyond identifiers except the queue residue.

## Findings this unblocks

- Closes F-11.4.10 (the deny-list and session-set halves), F-11.4.12, F-11.4.13, the F-11.4.14 remainder, F-11.4.17, F-12.8.25, F-12.8.30, F-12.8.34, F-12.8.37, F-12.8.41, and F-12.8.42, and the N-4 dossier item.
- Records F-12.8.35 as a departure bounded by process restart.
- Narrows F-12.8.44 to Phase 1 issuance and sign-up refusal. F-12.8.46 stays open.
- Closes TEST-GAPS T-4.5.4 (the MinIO half) and extends T-12.8.9 with Phases 3 and 4. T-12.1.3 and T-12.8.4 are already covered by existing tests and are recorded as closed by them.

## Prior art considered

- No landed or draft proposal stages this change. Outside 0095, the member finding IDs appear only in 0094's files and, for F-12.8.37, in a 0092 deviation. 0094 D20 assigns pieces B and C and the full_revoke fixes to P5b; 0093's impact table assigns the full_revoke session set, the listing, and the deny-list granularity to P5b.
- 0094's `admin.DrainSessions` already implements the full_revoke fix pattern: primary listing, continue-and-record, and a detached per-session context.
- 0093's `leaserevoke.Revoker` and `Credentials.ReleaseSession` are the release mechanisms a Phase 3 tenant revoker calls over the tenant's listed sessions. `tenantdeletion.CredentialRevoker` is the existing seam.
- Existing erasure methods cover most of Part A: `coordlease` pgstore, `userstore`, `connectorcredstore`, `credentialstore`, `evictionstatestore`, `semanticcache` redisstore, `artifactcatalog`, `auditstore`, `billingstore`, `customrolestore`, `experimentstore`, the delegation budget pgstore, the checkpoint stores, and the user path's `session_message_queues` and `delegation_budget` session erasers. `canonicalRank` already ranks most of the missing names.
- Proposal 0028 (applied) chose to exclude `gdpr.%` rows from the Phase 4 audit teardown and keep the receipt remnant; that decision constrains the F-12.8.25 fix.
- §11.4 already requires per-effect counts, partial-failure detail, and bounded local effects, so F-11.4.12 and F-11.4.13 are code-only.
- `routingcache.RedisCache.Invalidate(sessionID)` fits a session eraser; `tokencache.Invalidate(jti)` and the IDs `RevokeUserTokens` returns (`cmd/lenny-gateway/user_revocation.go`) are the building blocks for a token-cache flush.

## Validated premises

Each entry gives the lens, its verdict on the premise, and the outcome after direct verification.

### Premise lens (verdict: revise)

- F-11.4.12 holds. Confirmed.
- F-11.4.13 holds as "first error aborts the loop and answers 500 with no counts". Confirmed; restated with the evidence lens's correction below.
- F-11.4.14 remainder holds, and the primary-listing primitive exists in `DrainSessions`. Confirmed.
- The member list omitted F-11.4.10 (both halves) and F-11.4.17. Confirmed; both added to Part B.
- F-12.8.25 is wrong in two details: the foreign key does not abort Phase 4 today because no audit eraser is wired, and the retention pruner is the live collision; the fix needs a spec decision. Confirmed; the original "aborts Phase 4" premise is refuted as a present-tense claim and kept as a latent one.
- F-12.8.30 holds; Phase 3 also omits the token delete and cache flush, and a revoker must be idempotent because jobs resume at Phase 3. Confirmed.
- The premise that §12.8 `TokenStore` is already erased is false: the `tokens` eraser is `issuedtokenstore`, and `connectorcredstore` is unwired. Confirmed; added to Part A.
- N-4 step 3's implied tenant-prefix scan fix does not fit the cache key layouts. Confirmed; moved to an open decision.
- N-4 step 1 holds. Confirmed.
- F-12.8.42 has a weak spec basis. Confirmed.
- The conditional user-registry member fails its own condition. Confirmed; dropped.
- F-12.8.34, F-12.8.35, F-12.8.37, F-12.8.41 hold. Confirmed.
- Dependency premises hold, and "P1's lease reclaimer" names no single mechanism. Confirmed.

### Evidence lens (verdict: revise)

- F-11.4.12 verified; line citations drifted. Confirmed.
- F-11.4.13 verified in part: the "drops applied sessions" half no longer holds, because the fan-out runs for the sessions cancelled so far; the remaining drop is the commit-then-error case (0093 A25, F-11.4.17). Confirmed; the original wording is refuted.
- F-11.4.14 remainder verified. Confirmed.
- F-11.4.10 omitted. Confirmed.
- F-12.8.25 verified as a schema fact needing a spec decision. Confirmed.
- F-12.8.30 verified. Confirmed.
- Token and routing cache flushes cannot be tenant-prefix scans. Confirmed.
- F-12.8.34, F-12.8.35, F-12.8.37, F-12.8.41 verified. Confirmed.
- The store table must start from the Phase 4 order and the `DeleteByUser` steps as well as the scope table. Confirmed; written into the drafting rules.
- F-12.8.42 verified in code with a weak spec basis; the "§10.1.1" citation is wrong (§10.1.8). Confirmed.
- N-4 verified; step 1 concerns coordination leases rather than credential leases. Confirmed.
- User registry row is not a member. Confirmed.
- Attribution drift: `ReleaseThrough` is from 0092; 0093 was implemented 2026-10-21. Confirmed; corrected.
- Not-member claims verified. Confirmed.

### Prior-art lens (verdict: revise)

- No proposal already stages this change. Confirmed.
- F-11.4.10 and F-11.4.17 are owned by P5b. Confirmed.
- Most of Part A is wiring of existing methods. Confirmed.
- The Phase 3 seam and 0093's revoker are the building blocks; `RevokeByTenant` exists only in the spec. Confirmed.
- §11.4 already answers F-11.4.12 and F-11.4.13; the listing source is unspecified. Confirmed.
- F-12.8.25 is a residual of proposal 0028 and needs a spec edit. Confirmed.
- Routing cache and token cache invalidation primitives exist. Confirmed.
- N-4 step 1 confirmed; `session_tree_archive` is already handled on both paths. Confirmed.
- `EvictionStateStore` has no production writer. Confirmed.
- TEST-GAPS T-12.8.4, T-12.8.9, T-12.1.3, T-4.5.4 overlap. Not re-verified line by line; carried as a lead.

### Scope lens (verdict: revise)

- The problem bundles two independent problems, erasure store coverage and revocation. Accepted; the statement is organized as Part A and Part B. Whether to cut into two proposals is left to the owner.
- The full_revoke members share the drain's fix pattern rather than the erasure lists. Confirmed.
- F-11.4.10 omitted and inseparable from the F-11.4.14 listing. Confirmed.
- F-12.8.30 belongs with revocation; F-12.8.44's token half is its companion. Accepted.
- F-12.8.25 is a spec contradiction that the audit eraser would trigger. Confirmed.
- F-12.8.34 hides the legal-hold escrow exemption. Confirmed; added to Part A.
- N-4 splits per half: step 1 and routing entries to A, the token-cache half to B. Accepted.
- Unassigned membership must be decided in the statement. Accepted; decided above.
- No code dependency between the parts, because Phase 4's `tokens` and `credential_pool` erasers remove the rows regardless of Phase 3. Confirmed from the controller comment and eraser list.

### Impact lens (verdict: revise)

- F-12.8.25's live victim is the retention pruner. Confirmed by reading the `PruneRetention` predicate and the foreign key; not reproduced by a test run.
- F-12.8.30 leaves tokens valid through a legal-hold pause or a Phase 4 failure. Confirmed; the absence of an auth-path tenant-state gate is from a search and is marked unverified.
- The access-token and routing caches are unused in production. Confirmed.
- F-12.8.37, F-12.8.41, F-12.8.42, N-4 step 1, and F-12.8.35 have small impact after 0094. Accepted; the High labels on F-12.8.37 and F-12.8.41 overstate their impact.
- F-12.8.34 leaves below-T4 artifacts readable after the receipt. Confirmed from the eraser list and the T4-only KMS destruction.
- full_revoke defects are reachable and bounded. Confirmed.
- `EvictionStateStore` and `session_dlq_archive` need no new eraser. Confirmed.

### Alternatives lens (verdict: revise)

- Most members need no spec change. Confirmed.
- One structural cause: hand-assembled lists validated for order only; a completeness gate closes the class. Accepted; added to the drafting rules.
- Tenant-path queue and delegation-budget erasure reuse the user path's session-scoped erasers over primary-listed session IDs. Accepted.
- F-12.8.25 is latent in Phase 4 and must land with or before the audit eraser. Confirmed.
- The user registry row is not required by the spec. Confirmed.
- F-12.8.42 has a no-change reading. Accepted as an open decision.
- N-4 is TTL-bound or crosses a process boundary; per-ID invalidation or a bounded departure covers it. Accepted.
- full_revoke adopts the drain's semantics. Accepted.
- F-12.8.30 survives the controller's "Phase 4 covers it" reading because of the unbounded legal-hold pause. Confirmed.
- The rank map conflicts with the §12.8 Phase 4 order and lacks the tail stores. Confirmed from `order.go` and the Phase 4 row.
- F-12.8.35 has a low-priority reading bounded by restart. Accepted.
