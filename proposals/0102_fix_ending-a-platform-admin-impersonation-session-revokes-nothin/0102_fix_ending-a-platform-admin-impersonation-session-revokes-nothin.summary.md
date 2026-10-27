# Summary: Record the platform-admin impersonation bearer in issued_tokens and end the session by revoking that row

## Summary

**Problem statement.** Ending a platform-admin impersonation session revokes nothing. The gateway mints the target-user bearer without an `issued_tokens` row and records the session only in a replica-local in-memory store that no verifier reads. The bearer therefore stays valid on every gateway replica and on `lenny-ops` until `exp`. A §11.4 full revoke of the target user misses it, and the single-token revoke returns 404 for it. A revoked bearer passes as a `subject_token`. `DELETE` and `GET` fail or return partial results on a replica that did not issue the session, and a restart of the issuing replica loses the session. The specification defines neither the impersonation endpoints nor what ending a session does.

**What changes.**
- §13.3 gains the **Platform-admin impersonation.** paragraph, which is the single normative home of the mechanism, and step (1a) of "Write-before-issue ordering" refuses an impersonation bearer (SPEC-1). §10.2 applies step (1a) to the session-creation and playground mints. §10.2, §12.2, §12.6, §16.7, §11.4, and §15.1 cite the paragraph, §11.4 states that a full revoke commits the invalidation level before step 1, and §15.1 gains the three endpoint rows and names step (1a) in the `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` row (SPEC-2 to SPEC-6). SPEC-7 points the §11.7 CMP-058 halt rule at rule 3 and trims the rows that restate it.
- `issuedtokenstore` gains the impersonation-scoped end, list, and actor-revoke queries and the fenced impersonation insert, the step (1a) re-read and the playground mint refuse an impersonation row, and migration 0190 adds the partial index (CODE-1, CODE-2).
- The impersonation service records the bearer in `issued_tokens` and ends a session by revoking the row. The replica-local store and the expiry sweep are deleted (CODE-3).
- The admin handlers use catalogued error codes and the narrowed response bodies (CODE-4).
- A §11.4 full revoke of a platform-admin by a platform-admin caller also revokes the bearers that admin minted, and partial revocation results survive an error (CODE-5).
- The operator guide, the admin API reference, and BUILD-GAPS follow the change (DOCS-1).

**Decisions.**
- D1. The session is the bearer's `issued_tokens` row, and the gateway keeps no other session state. A separate table, a migration with RLS apparatus, a residency statement, and a sweep are all avoided, and the POST, GET, and DELETE endpoints the problem requires are kept.
- D2. After the caller check (D20), issuance stays audit first, then sign, then record. This keeps the CMP-058 fail-closed gate unchanged, and a failure leaves only an overstating started event, which a sign failure already leaves today.
- D3. The mint writes no `token.exchanged` row. This follows the bootstrap admin-token `Record` precedent and 0097's warning that a cross-tenant `token.exchanged` row falsifies §13.3's single-`tenant_id` statement.
- D4. An impersonation row is identified by `act_sub IS NOT NULL AND parent_jti IS NULL`. No production code writes `ActSubject` today, and delegation children set `ParentJTI`, so no marker column is needed.
- D5. Cross-replica `DELETE`, `GET`, and the cascade use the existing `__all__` path (`pgtenant.InAllTenants`; migration 0047 already covers `issued_tokens`). Each call emits one `cross_tenant_read` through the existing synchronous `EmitCrossTenantRead`.
- D6. `DELETE` revokes first and writes the audit event second. The conditional update picks one winner across replicas. An audit or CMP-058 outage never blocks revoking a leaked bearer.
- D7. When the ended append fails after the revoke, `DELETE` returns the append's error and the bearer stays revoked. This keeps a single writer, and was chosen over returning 200 and relying on a sweep.
- D8. §16.7 is narrowed so that only a winning `DELETE` writes `admin.impersonation_ended`. Expiry, full revokes, and single-token revokes write none. This removes the duplicate-sweep problem and the restart-loss problem together, and loses no fail-closed check, because the sweep wrote its row after the bearer had already expired.
- D9. The ended payload is built from the row and the caller, without `ticket_id` or `end_reason`. A join on `impersonation_session_id` recovers the ticket.
- D10. The admin cascade is adopted. Without it, fully revoking a compromised platform-admin leaves the bearers that admin minted live for up to an hour. The §11.4 playground extension is the precedent.
- D11. The cascade is gated on the caller holding `platform-admin` alone (§13.3 rule 6). It reads none of the user's roles, because the bearers were minted under the role the user held then, and a demotion before the revoke would otherwise skip the cascade. The gate is not keyed on tenant, because platform-admins live in ordinary tenants and a `platform` tenant guard never matches. A caller without `platform-admin` must never reach `__all__` (§4.2, §12.3), so its full revoke always reports the skipped cascade: without that read the gateway cannot tell whether the user started a session.
- D12. The cascade's JTIs travel through `RevokeUserTokens`, and the router pushes and counts every returned JTI even when an error is returned. Counts and failures use the existing `tokensRevoked` and `tokenRevokeError` fields.
- D13. `lenny-ops` refuses an ended bearer at its next §25.4 rule 1 read (30 s). §25.4 is not edited, and no text claims immediate refusal.
- D14. The row is a target-tenant `issued_tokens` row, so no CMP-058 table-placement question arises. §12.8 erasure and tenant deletion already cover it.
- D15. `GET` items carry only the fields `issued_tokens` holds. `reason` and `ticketId` stay on the started audit event.
- D16. Every error code is catalogued. `DELETE`'s uncatalogued `CONFLICT` becomes `409 INVALID_STATE_TRANSITION` with `currentState`, and a record failure keeps the existing `500 INTERNAL_ERROR`.
- D17. Without a Postgres pool, signer, propagator, or audit store, the gateway registers no impersonation routes, rather than minting a bearer nobody can revoke.
- D18. Migration 0190 adds the partial index. Without it, `GET` and the cascade range-scan every live token in every tenant.
- D19. `Config.MaxDuration` stays and is clamped to one hour, so no setting exceeds the §15.1 cap.
- D20. Every mint that applies step (1a) to a presented bearer refuses an impersonation bearer, revoked or not (§13.3 rule 5), and `POST /v1/admin/impersonation` applies it to the caller's bearer (rule 1). A token minted from the bearer, including a second impersonation bearer whose `act_sub` is the impersonated user, would outlive `DELETE` of the outer session, the rule 4 bound, or the rule 6 cascade on the admin who started it. Rule 1 runs the caller check before the started event, because a nested attempt would otherwise write an `admin.impersonation_started` naming the impersonated user as `admin_sub`. A mint that only the caller's roles authorize, such as rotate-token, applies no step (1a) and is outside the rule, and it returns no credential to the caller. The session-creation refusal holds in code once the session-creation mint lands (BUILD-GAPS F-10.2.25).
- D21. Rule 1 (c) serialises the recording with full revokes through the user rows, as the §12.8 creation fence does, and holds the caller's lock in its own transaction because §12.8 **Data residency** can place the caller's and the target's user rows on different instances. A full revoke writes the invalidation level under an exclusive row lock and commits it before §11.4 step 1, so one fence covers the target's full revoke and the admin's, needs no order between step 5's two revocations, and does not depend on the caller's bearer having an `issued_tokens` row. The caller check (D20) reads the caller's bearer row without a lock, because the impersonation half of step (1a) never changes after insert, and an operator revocation of the caller's bearer does not reach a session started before it either. The fence also refuses a hard-disabled target or admin, because hard disable sets the same `deleted_at` marker.
- D22. A session-creation `session_capability` mint that step (1a) refuses writes the rejection audit row under the §13.3 rejected-exchange discipline. It returns `403 FORBIDDEN` once the row is durable and the §13.3 `500` response when the audit write fails (SPEC-2 (b)). The session-creation, `/v1/oauth/token`, and playground mints then follow one rejection discipline, and a refused impersonation-sourced mint at any of them is audited fail-closed. This was decided by the owner's delegate on 2026-10-26 (OD-1, option A). D22 covers those mints only. The `POST /v1/admin/impersonation` caller-check refusals keep the staged "writes no audit row" behaviour in rule 1; whether a refused impersonation start is audited is recorded under Defects for a follow-up, because changing rule 1 touches D20, the §15.1 POST row, CODE-3, and TEST-1 and TEST-2 in a converged proposal.

**Watch out for.**
- A typed-nil `*impersonation.Service` passed to `WithImpersonation` registers the routes and panics (CODE-3 item 6).
- Today's step 5 discards every JTI on a non-nil error. Leaving it would drop the user's own tokens from the cache when the cascade fails (CODE-5 item 4).
- `End` must attempt the ended append even when `cross_tenant_read` fails, and the reverse (CODE-3 item 4).
- The single-winner guarantee is a Postgres property. A concurrency test against the fake store proves nothing, so the discriminating test is tier 2 (TEST-1).
- `go generate` does not regenerate the impersonation entries in `openapi.json`, and `generated_tools.go` needs no change (CODE-4 item 5).
- Deleting `MemStore` breaks `service_test.go` and `tests/tier2_component/tokensigning/kms_outcome_test.go` in the same step (CODE-3 item 7).
- Existing tier-4, tier-5, tier-8, and tier-9 tests that present an impersonation bearer as `subject_token` fail once CODE-2 item 7 and CODE-3 item 3 land (CODE-3 item 7).
- Confirm that 0190 is still the next free migration number when applying (CODE-1).
- Step 5 must stay after the users-row commit in `handleInvalidateUser` (CODE-5 item 5).

## Goals

- Make an ended impersonation bearer refused on every gateway replica, on `lenny-ops`, and as a `subject_token`.
- Make a §11.4 full revoke of the target user, and of the impersonating platform-admin, reach the bearer.
- Make list and end work from any replica and survive a restart of the issuing replica.
- Specify the impersonation endpoints, the duration cap, and the audit trail in one normative home.
- Close BUILD-GAPS F-13.3.20.

## Non-goals

- A separate `impersonation_sessions` table. Its only content beyond `issued_tokens` is the reason, the ticket reference, and an end-pending state that exists only to guarantee an ended event at expiry, which the narrowed §16.7 no longer requires.
- A session table in platform Postgres, global or regional. Regulated target identifiers would move out of region, and the table cannot commit atomically with the target-tenant revoke.
- Keeping the expiry ended event through a leader-gated sweep, a guarded audit append, or a `SKIP LOCKED` per-replica sweep. Each adds coordination to record an end that the started event and `exp` already bound.
- Writing `admin.impersonation_ended` for a full revoke or a single-token revoke. Each needs a second writer and an `end_reason` enum (D8).
- A separate §11.4 step for sessions targeting the user. `RevokeBySubject` already reaches the row.
- An `issue_failed` ended event. An unpaired started event already reads as "ended no later than started plus duration".
- Reordering issuance to sign, record, then audit. It changes the CMP-058 ordering and needs a compensating revoke (D2).
- Writing the audit event before the revoke on end. An audit outage would then block revoking a leaked bearer (D6).
- Returning 200 from `DELETE` when the ended append fails, and relying on a sweep (D7).
- Re-attempting the ended append on a 409 retry. It can duplicate the event after a lost response.
- Two-phase `revoked_reason` values with a reconciler. This adds a state and a worker for a failure mode that already overstates.
- A `token.revoked` audit row on end. The §16.7 `revocation_reason` enum has no value for it, and the ended event is the receipt.
- A `token.exchanged` row for the mint (D3).
- An impersonation JWT claim with a new `lenny-ops` refusal rule, or a playground-style revocation key. Gateway replicas would still accept the token, and a new §25.4 rule would be needed.
- A `kind` or `ended_audited_at` marker column (D4).
- Using the `imp-` `jti` prefix as the discriminator. It puts semantics in an opaque identifier.
- Requiring `tenantId` on `DELETE` and deleting `GET`. The problem requires `GET`, and `__all__` is an existing spec surface.
- Forwarding `DELETE` and `GET` to the issuing replica over `CH-HOLDERFORWARD`. State is still lost on restart, and an impersonation session is not a coordinated session.
- Ending sessions only through the single-token revoke endpoint. `DELETE` is the generated contract and writes the ended event.
- Declining the admin cascade (D10).
- New full-revoke response fields (D12).
- Mapping a record failure to `503 SERVICE_UNAVAILABLE` (D16).
- Removing `Config.MaxDuration` (D19).
- A general rule that every gateway mint records `issued_tokens`. It would pull in the playground bearer, which uses its own revocation key (§27.3.1).
- Carrying `act_sub` or a `parent_jti` link onto a token minted from an impersonation bearer so that `GET`, `DELETE`, and rule 6 reach it. It needs a recursive revoke in rule 3 and a §8.2 exception to step (1a), and the playground bearer has no row to link (D20).
- An EventBus `token.revoked` publish so that `lenny-ops` refuses faster than 30 s (D13).

## Open decisions for human to make

None.

## Defects in the shipped tree that this proposal does not stage

- **Refused impersonation starts write no audit row.** Under the staged rule 1, a `POST /v1/admin/impersonation` that the caller check refuses (an impersonation bearer as the caller, or a revoked caller bearer) writes no audit row, while D22 audits the session-creation, `/v1/oauth/token`, and playground refusals. The shipped handler has no caller check, so this is a follow-up to the staged behaviour rather than a defect in the shipped tree. A follow-up decides whether refused impersonation starts are audited under the §13.3 rejected-exchange discipline (owner's delegate, 2026-10-26).
- **Single-token revoke endpoint absent from §15.1.** The gateway serves `POST /v1/admin/issued-tokens/{jti}/revoke`, but no spec file names it. This proposal relies on the endpoint only in tests and refers to it in spec text as an operator revocation of a `jti`. Specifying it is left to a §15.1 proposal.
- **`issued_tokens` ignores regional storage routing.** §12.8 routes a regional tenant's Postgres writes to a region-local backend, while the gateway opens `issued_tokens` on its single pool everywhere. The defect covers the whole revocation store rather than this change, which follows the existing single-pool behaviour.
- **`docs/operator-guide/agent-operability.md` "rolled back" sentence.** The sentence "An operation that succeeds but fails to audit is treated as failed and rolled back." under **Audit trail** has no spec home and contradicts §13.3 rules 3 and 7, under which a revocation stands when its audit write fails. It predates this proposal, DOCS-1 does not edit it, and it is left to its own finding.
- **Playground mint rejections are not audited fail-closed.** §10.2 states that a rejected playground mint emits `playground.bearer_mint_rejected` under the write-before-reject discipline of §13.3. The playground `ChainAuditEmitter.EmitMintRejected` and the `ChainAuditSink.EmitAdminEvent` it calls return no error, and the sink logs an append failure, so every playground rejection branch returns its error response whether or not the audit row was written. The step (1a) branch that CODE-2 item 8 adds goes through the same emitter. Making the emitter fail closed changes the shared admin audit-sink contract and is left to its own finding.
- **Per-item `etag` on admin list items.** §15.1 "GET responses" requires a per-item `etag`, which is a resource's integer `version` column. `issued_tokens` has no `version` column, and the §15.1 row for `GET /v1/admin/tenants/{id}/users` lists no `etag` among its fields. The `GET /v1/admin/impersonation` items carry no `etag` (OD-3, resolved in the review log entry [f2.apply-OD-3]), and reconciling §15.1's per-item `etag` coverage with lists over unversioned rows is left to its own finding.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0097 (Lenny-issued bearer tokens verify on every process) | Implemented | 0102 makes the impersonation bearer revocable, which 0097 left out of scope as D25, and follows its warning against a cross-tenant `token.exchanged` row | Nothing. 0097 is not edited |
| 0101 (off-holder request forwarding on `CH-HOLDERFORWARD`) | Approved 2026-10-26, being implemented | 0101 CODE-4 renames the propagator package that `cmd/lenny-gateway/user_revocation.go` imports, and 0102 CODE-5 edits the same file. In §11.4 **Full revoke propagation mechanism:**, 0101 SPEC-6 (b) and (c) edit the two-tier propagation note and step 2, and 0102 SPEC-5 adds a sentence after the **Full revoke propagation mechanism:** label and appends to step 5. The anchors are disjoint, and no 0101 deliverable loses its subject | Whichever proposal lands second re-reads `user_revocation.go` and confirms that both changes are present, and applies its §11.4 edit against the other's text by its quoted anchor with no change to content |

## Deliverable index

- SPEC-1 — spec/13_security-model.md — §13.3 **Platform-admin impersonation.** paragraph with rules 1 to 7, the tenant-scope enforcement pointer, and the step (1a) impersonation refusal.
- SPEC-2 — spec/10_gateway-internals.md — §10.2 **Signing topology.** citation and step (1a) on the session-creation mint; playground mint invariant 1 and its failure row.
- SPEC-3 — spec/12_storage-architecture.md — §12.2 `TokenIssuanceStore` row: `act_sub` for impersonation rows and the partial index; §12.6 **Scaling extension interfaces.** names the §13.3 rule 1 (c) fence.
- SPEC-4 — spec/16_observability.md — §16.7 started payload gains `impersonation_session_id`; the ended event cites §13.3 rules 3 and 4 and its payload is defined.
- SPEC-5 — spec/11_policy-and-controls.md — §11.4 commit-order sentence; step 5 points at the §13.3 rule 6 cascade.
- SPEC-6 — spec/15_external-api-surface.md — §15.1 `POST`, `GET`, and `DELETE /v1/admin/impersonation` rows, and the `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` catalog row.
- SPEC-7 — spec/11_policy-and-controls.md, spec/15_external-api-surface.md, spec/25_agent-operability.md, spec/16_observability.md — §11.7 item 3 pointer to §13.3 rule 3; CMP-058 catalog and alert rows trimmed to §11.7.
- CODE-1 — migrations, tests/tier2_component/migrations — migration 0190 partial index and its tests.
- CODE-2 — pkg/gateway/storage/issuedtokenstore, pkg/tokenservice, pkg/gateway/mcpfabric/playground, cmd/lenny-gateway — impersonation-scoped record, end, list, and actor revoke, the step (1a) impersonation refusal, and the playground mint's step (1a) check.
- CODE-3 — pkg/gateway/credentials/impersonation, cmd/lenny-gateway, tests/tier2_component/tokensigning, tests/tier4_integration, tests/tier5_e2e_kind, tests/tier8_chaos, tests/tier9_security — service over `issued_tokens`; MemStore, Store, and the sweep deleted; fail-closed wiring; existing subject-token tests rewritten.
- CODE-4 — pkg/gateway/externalapi/admin, pkg/gateway/externalapi/openapi — handler signatures, catalogued 409, narrowed bodies, and the `DELETE` OpenAPI entry.
- CODE-5 — cmd/lenny-gateway, pkg/gateway/externalapi/admin — full-revoke platform-admin cascade, partial-result handling, and the step 5 commit order.
- TEST-1 — pkg/gateway/credentials/impersonation, pkg/gateway/externalapi/admin, cmd/lenny-gateway, pkg/tokenservice, pkg/gateway/storage/issuedtokenstore, pkg/gateway/mcpfabric/playground, tests/tier2_component/stores, tests/tier3_contract, tests/spec-map.json — unit, Postgres-tier, and tier-3 contract tests.
- TEST-2 — tests/tier4_integration, tests/tier9_security — two-replica and `lenny-ops` tests.
- DOCS-1 — docs/operator-guide/security.md, docs/api/admin.md, docs/operator-guide/observability.md, docs/reference/metrics.md, docs/reference/error-catalog.md, BUILD-GAPS.md, tests/tier11_docs, tests/spec-map.json — operator guide sentence, admin API reference sections, CMP-058 row trims, F-13.3.20 closure and the F-10.2.25 pointer, and their tier-11 test.
