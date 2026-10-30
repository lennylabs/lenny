# Non-spec changes — Ending an impersonation session does not revoke its bearer

## Design (implementation-facing)

The normative rules are the numbered rules of §13.3 **Platform-admin impersonation.** (SPEC-1), the §16.7 ended payload (SPEC-4), the §11.4 commit-order sentence and step 5 pointer (SPEC-5), and the §15.1 rows (SPEC-6). Each item below cites the rule it implements and adds only what the code needs: the symbols, the files, the order, and the traps.

- The store gains the predicate-guarded queries and the fenced impersonation insert, and its step (1a) re-read refuses an impersonation row (CODE-2). Migration 0190 adds a partial index (CODE-1).
- The impersonation service runs over `issued_tokens`, and the replica-local store and the expiry sweep are deleted (CODE-3).
- The admin handlers follow the new service signatures and the catalogued codes (CODE-4).
- Full revoke gains the platform-admin cascade, and partial results survive an error (CODE-5).

Every code step updates the existing fakes, constructors, test helpers, and tests that it breaks, so the tree compiles and tier 0 passes at every step. The TEST deliverables add the behaviour cases.

## Staged code changes

### CODE-2 · issuedtokenstore: impersonation-scoped record, end, list, and actor revoke, and the step (1a) refusal

Target: `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go`, for item 7 `pkg/gateway/storage/issuedtokenstore/recheck.go` and `pkg/tokenservice/tokenservice.go`, and for item 8 `pkg/gateway/mcpfabric/playground/token.go`, `pkg/gateway/mcpfabric/playground/playground.go`, and `cmd/lenny-gateway/httpsurface.go`.

1. Add `const impersonationPredicate = "act_sub IS NOT NULL AND parent_jti IS NULL"` (§13.3 **Platform-admin impersonation.** rule 2). Items 3, 4, and 7 write it as `<pred>`.
2. Add a typed error `NotActiveError{State string}`, where `State` is `revoked` or `expired`, and a sentinel `ErrNotActive` that `errors.Is` matches for every `*NotActiveError`.
3. `EndImpersonation(ctx, jti string, at time.Time) (IssuedToken, error)`. One `pgtenant.InAllTenants` transaction runs `UPDATE issued_tokens SET revoked_at=$2, revoked_reason='impersonation_ended' WHERE jti=$1 AND <pred> AND revoked_at IS NULL AND exp > $2 RETURNING <selectList>`. When the update matches no row, the same transaction re-reads `WHERE jti=$1 AND <pred>`. It returns `ErrNotFound` when no row exists, `&NotActiveError{State: "revoked"}` when `revoked_at` is set, and `&NotActiveError{State: "expired"}` otherwise (rule 3).
4. `ListActiveImpersonations(ctx, now time.Time) ([]IssuedToken, error)`. Under `InAllTenants`, it selects `WHERE <pred> AND revoked_at IS NULL AND exp > $1 ORDER BY issued_at` (rule 2).
5. `RevokeImpersonationsByActor(ctx, actorSub, reason string, at time.Time) ([]string, error)`. Under `InAllTenants`, it runs a conditional `UPDATE ... WHERE act_sub=$1 AND parent_jti IS NULL AND revoked_at IS NULL AND exp > $3 RETURNING jti` (rule 6).
6. Each query carries the `-- platform-admin-cross-tenant-allowed` annotation and a one-line justification, following the annotated queries in `pkg/gateway/audit/auditstore/translation.go`. The store does not emit `cross_tenant_read`; its callers do (CODE-3, CODE-5).
7. **Step (1a) refusal** (§13.3 "Write-before-issue ordering" step (1a)). `selectRevokedAt` also reads `(<pred>)` per `jti`. `recheckSources` returns the existing `*SourceRevokedError` for the first listed revoked source. Otherwise it returns a new `*SourceImpersonationError{Role, JTI}` for the first listed impersonation row, whose `Reason()` is `string(Role) + "_impersonation"`. In `tokenservice.go`, the `commitIssued` caller in `persistIssuedToken` matches both types with `errors.As`, and `rejectRevokedSource` takes the reason string, so both render through the existing rejection-audit discipline. Update the doc comments that name only `SourceRevokedError` (`IssuedTokenRotationStore`, `commitIssued`, `recheckSources`). Add the exported method `IssuedToken.SourceRefusal(role SourceRole) error`. It returns `*SourceRevokedError` when `RevokedAt` is set, otherwise `*SourceImpersonationError` when `ActSubject` is non-empty and `ParentJTI` is empty, and otherwise nil. Item 8 and CODE-3 item 3 evaluate their single row through it. `recheckSources` keeps its multi-source order.
8. **Playground mint** (§10.2 "Playground mint invariants" invariant 1). `playground.Options` gains `IssuedTokens`, a consumer-defined interface holding `Get(ctx, tenantID, jti string) (issuedtokenstore.IssuedToken, error)`, which `httpsurface.go` sets to `issuedtokenstore.New(w.pgPool)` when the pool is non-nil. After the `typ` check, `mintAPIKey` reads the subject's row by `claims.TenantID` and `claims.JWTID`. `ErrNotFound` passes. A row whose `SourceRefusal(SourceSubjectToken)` is non-nil is rejected with that error's `Reason()`. Each rejection emits `playground.bearer_mint_rejected` with that reason and returns the invariant 1 response without signing. Rename that response's details key from `presentedTyp` to `subjectTyp` in `mintAPIKey`'s `typ` check, so both branches send the key §10.2 invariant 1 names. Any other read error aborts the mint with `500 INTERNAL_ERROR` without signing. A nil `IssuedTokens` skips the read, because D17 registers no impersonation route without the pool. The session-creation mint is unbuilt (BUILD-GAPS F-10.2.25), so no code item applies step (1a) there.
9. **Impersonation record** (§13.3 **Platform-admin impersonation.** rule 1 (c)). `RecordImpersonation(ctx, tok IssuedToken, callerTenantID string) error`. An empty `callerTenantID` returns an error and writes nothing. In order:
   1. `pgtenant.InTx(ctx, s.pool, callerTenantID, ...)` runs `SELECT deleted_at IS NOT NULL FROM users WHERE tenant_id=$1 AND subject=$2 FOR SHARE` with `tok.ActSubject`.
   2. Inside that callback, `pgtenant.InTx(ctx, s.pool, tok.TenantID, ...)` runs the same `users` read with `tok.Subject`, then `insertIssued`, mapping 23505 to `ErrAlreadyExists` as `Record` does. It commits before the step 1 callback returns.

   A `users` read that returns true returns a new `*UserInvalidatedError{TenantID, Subject}` and writes nothing. A missing `users` row passes. The method returns nil only when the step 1 transaction commits, never uses `__all__`, and carries no cross-tenant annotation. **Trap:** each `users` row is read under its own tenant's RLS context; under another tenant's context it reads as absent and passes. **Trap:** the `users` reads take `FOR SHARE`, for the reason the `readTenantMarkersSQL` comment in `pkg/gateway/session/sessionstore/pgstore/creation_fence.go` gives; a plain SELECT lets the full revoke's marker commit between the read and the insert. **Trap:** step 2's `users` read and insert share one transaction; confirm that the pool passed to `issuedtokenstore.New` is the pool `stores.go` passes to `userpg.New`.

### CODE-3 · Impersonation service over issued_tokens; MemStore, Store, and the sweep deleted

Targets:
- `pkg/gateway/credentials/impersonation/service.go`
- `pkg/gateway/credentials/impersonation/store.go` (delete)
- `pkg/gateway/credentials/impersonation/impersonation.go`
- `pkg/gateway/credentials/impersonation/service_test.go` (compile maintenance only; see item 7)
- `cmd/lenny-gateway/workers.go`
- `cmd/lenny-gateway/adminrouter.go`
- `cmd/lenny-gateway/wiring_fields.go`
- `tests/tier2_component/tokensigning/kms_outcome_test.go` (it constructs `impersonation.New` with `NewMemStore`)
- the existing-test rewrites of item 7: `tests/tier9_security/ops_revocation_enforcement_test.go`, `tests/tier4_integration/tenant_deletion_phase4_test.go`, `tests/tier4_integration/jwt_keyset_cross_process_test.go`, `tests/tier8_chaos/jwt_keyset_chaos_test.go`, and `tests/tier5_e2e_kind/jwt_keyset_cross_replica_test.go`

1. **Collaborators.** `New` takes three consumer-defined interfaces in place of `Store`. `IssuedTokens` is the slice of `*issuedtokenstore.Store` holding `Get`, `RecordImpersonation`, `EndImpersonation`, and `ListActiveImpersonations`. `RevocationCache` holds `Revoke(jti string)`. `CrossTenantReads` holds `EmitCrossTenantRead(ctx, auditstore.CrossTenantRead) error`. Delete `Store`, `MemStore`, `NewMemStore`, `SweepExpired`, `EndReason`, `EndReasonExplicit`, `EndReasonExpired`, the `Ticket` fields `EndedAt`, `EndedBy`, and `EndReason`, and the method `Ticket.Active`.
2. **Duration cap.** `New` clamps `Config.MaxDuration` to at most one hour, so no configuration admits a bearer longer than the §15.1 cap of 3600 seconds. `Issue` keeps returning `ErrInvalidDuration` for a duration below the §15.1 minimum or above the clamped maximum.
3. **Issue.** Order per rule 1: first the caller check, `Get(ctx, req.CallerTenantID, req.CallerJTI)`, where an empty `CallerJTI` or `ErrNotFound` passes, a row whose `SourceRefusal(SourceSubjectToken)` is non-nil returns `ErrIssuanceRefused` wrapping it, and any other read error is returned, each with no append and no sign; then append `admin.impersonation_started` with the existing payload, which already carries `impersonation_session_id`; then sign; then `RecordImpersonation(ctx, tok, req.CallerTenantID)` with an `issuedtokenstore.IssuedToken` carrying `JTI` set to the session id, `TenantID` and `Subject` set to the target, `ActSubject` set to `req.AdminSub`, an empty `ParentJTI`, `TokenHash` set to the SHA-256 of the signed bearer, `Audience`/`Audiences`, `IssuedAt`, and `ExpiresAt`. Return the bearer only when `RecordImpersonation` returns nil. `IssueRequest` gains `CallerTenantID` and `CallerJTI`. A `*issuedtokenstore.UserInvalidatedError` returns `ErrIssuanceRefused` wrapping it. Call no `RecordWithAudit` variant, because the mint writes no `token.exchanged` row.
4. **End.** `End(ctx, id, endedBy string) (EndResult, error)`, where `EndResult` carries `SessionID` and `EndedAt`. The order is fixed:
   1. `EndImpersonation(ctx, id, now)`.
   2. On success, `RevocationCache.Revoke(id)`.
   3. Append `admin.impersonation_ended` with the §16.7 payload from SPEC-4. Build it from the returned row and the caller: `admin_sub` from `ActSubject`, `admin_tenant_id` from `Config.PlatformTenantID`, `target_tenant_id` and `target_user_id` from `TenantID` and `Subject`, `impersonation_session_id` from `JTI`, `ended_by`, `started_at` from `IssuedAt`, and `ended_at`. The payload carries no `ticket_id` and no `end_reason`.
   4. `EmitCrossTenantRead` with category `impersonation_session`, endpoint `DELETE /v1/admin/impersonation/{id}`, `RowCount` 1, and the caller subject.

   Steps 3 and 4 are each attempted regardless of the other's outcome, and `End` returns `errors.Join` of their errors together with the `EndResult`. Neither error undoes the revoke. A `cross_tenant_read` failure must never suppress the ended event, so that the only no-ended-event outcome after a winning `DELETE` is the ended-append failure that rule 3 names. On `ErrNotFound` or `ErrNotActive` from step 1, `End` emits the `cross_tenant_read` event with `RowCount` 0, because every call that sets `__all__` emits one (rule 7). It then returns `ErrNotFound`, or `ErrAlreadyEnded` wrapping the `*NotActiveError` so that `errors.As` recovers the state, joined with any emit error.
5. **ListActive.** `ListActive(ctx, callerSub string) ([]Session, error)`. First emit `cross_tenant_read` (category `impersonation_session`, endpoint `GET /v1/admin/impersonation`, the caller subject). When the emit fails, return its error and no list. Then call `ListActiveImpersonations`. `Session` carries the six fields of the §15.1 `GET` item.

   **IMPLEMENTOR'S CHOICE:** the `RowCount` of the listing's `cross_tenant_read` event. The event must be emitted before any row is returned. Either 0 or the count of a preceding `SELECT count(*)` under the same predicate satisfies §12.3.
6. **Wiring.** In `adminrouter.go`, construct the service, call `adminRouter.WithImpersonation`, and assign `w.impersonationSvc` only inside `if w.pgPool != nil && w.jwtSigner != nil && revProp != nil && auditOpsStore != nil { ... }`. Pass `auditOpsStore` as the `CrossTenantReads` emitter and `revProp` as the `RevocationCache`. **Trap:** never pass a nil `*impersonation.Service` into the `ImpersonationService` interface. A typed nil passes the router's `r.impersonation == nil` guard in `routes.go`, registers the routes, and panics on the first request. Delete the expiry-sweep goroutine in `workers.go` and its `impersonation` import.
7. **Existing-test maintenance.** In `service_test.go`, delete `TestSweepExpired_EndsExpiredOnly_spec_16_7` and `TestSweepExpired_FailClosedRetries_spec_11_7`, and move the remaining tests onto the CODE-3 signatures (`End`, `ListActive`, no `Ticket.Active` or `EndReason`) over the fakes TEST-1 extends. In `kms_outcome_test.go`, replace `NewMemStore` with a fake `IssuedTokens`.

   Existing tests that present an impersonation bearer as `subject_token` are refused once CODE-2 item 7 and CODE-3 item 3 land. They are `opsRevStack.exchange` in `ops_revocation_enforcement_test.go`, `lennyIssuedProbes` in `tenant_deletion_phase4_test.go`, the `mintExchanged` calls in `tests/tier4_integration/jwt_keyset_cross_process_test.go`, `keySetChaosExchange` in `tests/tier8_chaos/jwt_keyset_chaos_test.go`, and the exchange in `TestLennyIssuedBearerVerifiesAcrossChartReplicas_spec_10_2` in `tests/tier5_e2e_kind/jwt_keyset_cross_replica_test.go`. `mintPlayground` in `TestPlaygroundBearerRefusedOnOpsAndAsCaller_spec_25_4` pastes an impersonation bearer as the playground subject and is refused once CODE-2 item 8 lands. Each takes its subject, or its pasted subject, from a token for the same tenant and `sub` signed through `stackkey.Signer`, as `TestEveryVerifierRefusesForgedLennyTokens_spec_10_2` does. The impersonation bearer may stay the caller.

   **IMPLEMENTOR'S CHOICE:** how the tier-5 test signs its subject — it must be signed by the installed gateway's `platform:jwt-signing` key and carry no impersonation row.

### CODE-4 · Admin handlers and the served OpenAPI document

Targets: `pkg/gateway/externalapi/admin/impersonation.go`, `pkg/gateway/externalapi/openapi/openapi.json` (hand edit), and the fakes in `pkg/gateway/externalapi/admin/impersonation_test.go`, `signing_unavailable_test.go`, and `pkg/gateway/externalapi/openapi/routestub_test.go` (compile maintenance).

1. Update the `ImpersonationService` interface to the CODE-3 signatures: `End(ctx, id, endedBy) (impersonation.EndResult, error)` and `ListActive(ctx, callerSub string) ([]impersonation.Session, error)`. The list handler passes `principal.Subject`.
2. `handleStartImpersonation` sets `CallerTenantID` and `CallerJTI` from `principal.TenantID` and `principal.JTI`, and maps `impersonation.ErrIssuanceRefused` to `403 FORBIDDEN` before `writeImpersonationError`.
3. In `handleEndImpersonation`:
   - Map `impersonation.ErrAlreadyEnded` to `409 INVALID_STATE_TRANSITION`, with `details.currentState` taken from the wrapped `*issuedtokenstore.NotActiveError` and `details.allowedStates: ["active"]` (§15.1 `DELETE` row).
   - Map `impersonation.ErrNotFound` to `404 RESOURCE_NOT_FOUND`.
   - Pass every other error to `writeImpersonationError`, which maps CMP-058 to 422 and anything else to 500.

   Check `ErrAlreadyEnded` and `ErrNotFound` before the default branch. A joined emit error on the 404 or 409 path then still yields 404 or 409.
4. The `DELETE` response is `{impersonationSessionId, endedAt}`, with no `endReason`. The list entry drops `reason` and `ticketId`. The list handler writes the entries through `writePaginatedList` (`admin/pagination.go`) in place of `writeJSON`, with the single sort field `created_at` keyed on `issuedAt`, `impersonationSessionId` as tiebreaker, and the `adminListDefaultSort` default. `writeImpersonationError` keeps its existing branches unchanged.
5. Hand-edit the `DELETE` operation of `/v1/admin/impersonation/{id}` in `openapi.json`. Change `204 Ended` to `200` with the `{impersonationSessionId, endedAt}` body, and add `404` and `409` responses that reference `#/components/schemas/Error`, following the existing `409` entries in the document. Do not run `go generate` for this edit: its directive merges only `lenny-ops` route schemas. `pkg/ops/mcp/generated_tools.go` is unchanged, because the tool input schemas do not change.

### CODE-5 · Full revoke: platform-admin impersonation cascade

Targets:
- `cmd/lenny-gateway/user_revocation.go`
- `cmd/lenny-gateway/adminrouter.go` (the `userTokenRevoker` construction)
- `pkg/gateway/externalapi/admin/users_invalidate.go` (the `UserTokenRevoker` interface and `runFullRevokeFanOut` step 5)
- `pkg/gateway/externalapi/admin/users.go` (`handleInvalidateUser` and `fullRevoke` pass the cascade decision)
- the `UserTokenRevoker` fakes in `pkg/gateway/externalapi/admin` tests (compile maintenance)

1. **Decision (rule 6), in the router.** `handleInvalidateUser` passes `revokeImpersonations = principal.HasRole(auth.RolePlatformAdmin)` through `fullRevoke` to `runFullRevokeFanOut` and reads no role from the user record. When `revokeImpersonations` is false, step 5 sets `tokenRevokeError` to name the skipped impersonation revocation, joined with any error `RevokeUserTokens` returns, and runs no `__all__` query.
2. **Interface.** `RevokeUserTokens(ctx, tenantID, subject, reason string, at time.Time, revokeImpersonations bool) ([]string, error)`. Update its doc comment to state that the returned JTIs are every durably revoked token, returned even when the error is non-nil.
3. **Revoker.** In `user_revocation.go`, `userTokenRevoker.store` becomes a consumer-defined interface holding `RevokeBySubject` and `RevokeImpersonationsByActor`, as `adminIssuedTokenStore` is, and the revoker gains `reads`, a consumer-defined interface holding `EmitCrossTenantRead(ctx, auditstore.CrossTenantRead) error`, set to `auditOpsStore` only when that pointer is non-nil. `RevokeUserTokens` works as follows:
   - It calls `jtis, err := store.RevokeBySubject(...)`.
   - When `revokeImpersonations` is true, it calls `more, err2 := store.RevokeImpersonationsByActor(ctx, subject, reason, at)` even when `RevokeBySubject` failed, and appends `more`. It then emits one `cross_tenant_read` (category `impersonation_session`, endpoint `POST /v1/admin/users/{userId}/invalidate`, `RowCount` `len(more)`, and the caller subject from the context principal), including when `more` is empty, because the call set `__all__` (rule 7).
   - It returns every JTI that was durably revoked together with `errors.Join(err, err2, emitErr)`. A failed step contributes no JTIs, and a committed step's JTIs are returned alongside the error.

   When `revokeImpersonations` is true but `reads` is nil, it fails closed: it runs no `__all__` query and joins an error naming the missing emitter.
4. **Router step 5.** `runFullRevokeFanOut` pushes every returned JTI into `revocationCache` and sets `tokensRevoked = len(jtis)` whether or not `err` is nil, and sets `tokenRevokeError` when `err` is non-nil. **Trap:** today's branch discards every JTI on a non-nil error. Leaving that branch in place would make a failed cascade drop the cache push for the user's own tokens, which `RevokeBySubject` already revoked, and those tokens would then wait for the 30-second rehydrate.
5. **Commit order** (§11.4 **Full revoke propagation mechanism:** lead sentence; §13.3 rule 1 (c) depends on it). `handleInvalidateUser` already commits `r.users.Update` before it calls `fullRevoke`. Keep that order. **Trap:** moving any step 5 call ahead of the update, or onto a goroutine started before the update returns, reopens the rule 1 (c) window.

## Staged schema, chart, and migration changes

### CODE-1 · Migration 0190: partial index for impersonation rows

Targets:
- `migrations/0190_issued_tokens_impersonation_index.up.sql`: `CREATE INDEX idx_issued_tokens_impersonation ON issued_tokens (act_sub) WHERE act_sub IS NOT NULL AND parent_jti IS NULL;`
- `migrations/0190_issued_tokens_impersonation_index.down.sql`: `DROP INDEX IF EXISTS idx_issued_tokens_impersonation;`
- `migrations/issued_tokens_impersonation_index_test.go` (new). Following the repository's per-migration test convention, it reads both 0190 files from `migrations.FS`. It asserts that the up file creates the partial index with exactly the SPEC-3 predicate and that the down file drops it. `// spec: §12.2`.
- `tests/tier2_component/migrations/prod_schema_test.go`: add `mustHaveIndex(t, ctx, pg, "issued_tokens", "idx_issued_tokens_impersonation")` under the comment `// 0190 adds the §13.3 impersonation partial index.`, whose number is the reference `scripts/lint-migrations.sh` requires a file in this directory to carry. `// spec: §12.2`.

Confirm that 0189 is still the latest migration when applying, and take the next free number otherwise, in both file names and the tier-2 comment.

## Staged docs changes

### DOCS-1 · Operator guide, admin API reference, and BUILD-GAPS closure

Targets: `docs/operator-guide/security.md` (bearer-tokens item 3), `docs/api/admin.md`, the `PlatformAuditResidencyViolation` and `PLATFORM_AUDIT_REGION_UNRESOLVABLE` rows in `docs/operator-guide/observability.md`, `docs/reference/metrics.md`, and `docs/reference/error-catalog.md`, and `BUILD-GAPS.md` F-13.3.20 and F-10.2.25; and the tier-11 test `tests/tier11_docs/impersonation_revocation_docs_test.go` (new) with its `tests/spec-map.json` entry.

(a) Append to the item 3 line (same physical line) under "What It Manages" in `docs/operator-guide/security.md`:

```
The gateway records each platform-admin impersonation bearer in the issued-token index, so the bearer can be revoked like any other recorded token. The bearer is revoked when an administrator ends the impersonation session with `DELETE /v1/admin/impersonation/{id}`, when the target user is fully revoked, or when the bearer is revoked by its token id. Every gateway replica refuses a revoked bearer, and `lenny-ops` refuses it after its next successful revocation-set read from Postgres, which it attempts every 30 seconds. Any gateway replica can list or end an impersonation session.
```

(b) In `docs/api/admin.md`, add sections `POST /v1/admin/impersonation`, `GET /v1/admin/impersonation`, and `DELETE /v1/admin/impersonation/{id}` immediately after the `POST /v1/admin/users/{userId}/rotate-token` section. Each carries the `platform-admin` label block that the rotate-token section uses, and states the request fields, response fields, and errors of its SPEC-6 row, including the `409 INVALID_STATE_TRANSITION` for an already revoked or expired bearer. The `POST` section also states that the call returns `403 FORBIDDEN` when the target user or the calling administrator is hard-disabled or fully revoked, and that the bearer cannot be used to call `POST /v1/admin/impersonation`, cannot obtain a playground token, and cannot be presented to `/v1/oauth/token` as a subject or actor token. The `DELETE` section also states that when the ended audit write fails after the revoke commits, the call returns the audit error and the bearer stays revoked. The page cites no spec section numbers.

(c) Extend the `POST /v1/admin/users/{userId}/invalidate` section in `docs/api/admin.md` with one sentence. In `full_revoke` mode, when a platform administrator invalidates a user, the call also revokes, in every tenant, the impersonation bearers that user started, and counts them in the revoked-token total.

(d) Mark F-13.3.20 resolved in `BUILD-GAPS.md`, with a pointer to proposal 0102, once CODE-2 to CODE-5 and TEST-1 and TEST-2 have landed. In F-10.2.25, append to **Suggested resolution:** "The mint applies §13.3 "Write-before-issue ordering" step (1a) to the caller's bearer, as §10.2 **Signing topology.** states (proposal 0102). When it lands, the `POST /v1/admin/impersonation` section of `docs/api/admin.md` gains the statement that the impersonation bearer cannot create a session."

(e) Mirror SPEC-7 Edits (b) and (d). In `docs/operator-guide/observability.md`, in the `PlatformAuditResidencyViolation` row, delete "; the originating operation will succeed once audit can commit". In `docs/reference/metrics.md`, in the `PlatformAuditResidencyViolation` alert row, delete "The originating operation halts. ". In `docs/reference/error-catalog.md`, in the `PLATFORM_AUDIT_REGION_UNRESOLVABLE` row, delete "The originating operation halts (impersonation issuance, Phase 3.5 escrow ledger write, compliance decommission). ". Each row stays one physical table line.

(f) Add `tests/tier11_docs/impersonation_revocation_docs_test.go` (new, tier 11, `// spec: 13.3, 15.1, 11.7`), built on `runDocPageChecks`, and map it in `tests/spec-map.json` under those sections. It checks that the `3. **Bearer tokens**` line of `operator-guide/security.md`, which (a) extends, contains `DELETE /v1/admin/impersonation/{id}`. It checks that `api/admin.md` contains the three section headings from (b), `409 INVALID_STATE_TRANSITION`, and `bearer stays revoked`. It checks that the `` | `PlatformAuditResidencyViolation` | `` line of `operator-guide/observability.md` and of `reference/metrics.md`, and the `PLATFORM_AUDIT_REGION_UNRESOLVABLE` row of `reference/error-catalog.md`, contain neither `originating operation halts` nor `once audit can commit`.

## Testing

Every test carries `// spec:` for the sections it verifies, and every tier-2-and-higher test carries a `// diagnosis:` comment.

### TEST-1 · Unit, Postgres-tier, and tier-3 contract tests

Targets:
- `pkg/gateway/credentials/impersonation/service_test.go`
- `tests/tier2_component/stores/issuedtokenstore_test.go`
- `pkg/gateway/externalapi/admin/impersonation_test.go`
- `pkg/gateway/externalapi/admin/signing_unavailable_test.go`
- `cmd/lenny-gateway/user_revocation_test.go`
- `pkg/gateway/externalapi/admin` full-revoke handler tests
- `cmd/lenny-gateway/release_wiring_test.go`
- `pkg/tokenservice/source_recheck_test.go`
- `pkg/gateway/storage/issuedtokenstore/recheck_test.go`
- `pkg/gateway/mcpfabric/playground` (a new test file)
- `tests/tier3_contract/playground/playground_test.go`
- `tests/tier3_contract/admin_impersonation/openapi_document_test.go` (new)
- `tests/spec-map.json` (map `tests/tier3_contract/admin_impersonation/openapi_document_test.go` under section `15.1`)

Service unit tests, with fakes for `IssuedTokens`, `RevocationCache`, `CrossTenantReads`, the appender, and the signer:
- **Issue order (rule 1).** An append failure leads to no sign and no `RecordImpersonation`. A sign failure leads to no `RecordImpersonation`; this is the no-row assertion that `signing_unavailable_test.go` cannot make. A `RecordImpersonation` failure returns no bearer. The caller check reads the caller's tenant and jti, and `RecordImpersonation` receives the caller's tenant. A caller-check refusal returns `ErrIssuanceRefused` with no append, no sign, and no `RecordImpersonation` call; an empty `CallerJTI` and an `ErrNotFound` row each pass; any other `Get` error is returned with no append. A `*UserInvalidatedError` from `RecordImpersonation` returns `ErrIssuanceRefused` and no bearer. The recorded row carries the session `jti`, the target tenant and sub, `act_sub` set to the admin, an empty `parent_jti`, the bearer hash, and the rule 1 `exp`, asserted with a fake clock that advances between the append and the sign. No `RecordWithAudit` call is made.
- **Duration cap.** `durationSeconds` 59 is rejected, 60 is accepted, and 3601 is rejected even with `Config.MaxDuration` set to two hours.
- **End order (CODE-3 item 4).** The call order is store end, cache `Revoke`, ended append, then `cross_tenant_read`. When the ended append fails, the cache push has already happened, `cross_tenant_read` is still emitted, and the error is returned. When `cross_tenant_read` fails, the ended event is still appended. The ended payload has exactly the SPEC-4 fields. `ErrNotFound` maps to `ErrNotFound`, and `*NotActiveError` maps to `ErrAlreadyEnded` with the state recoverable. The 404 and 409 paths emit `cross_tenant_read` with `RowCount` 0.
- **List (rule 7).** `cross_tenant_read` is emitted before the store is read. On an emit error, the store is not called and no list is returned.

Handler tests:
- An unknown id returns 404.
- A revoked or expired session returns 409 `INVALID_STATE_TRANSITION`, with `details.currentState` set to `revoked` or `expired` and `details.allowedStates` set to `["active"]`.
- A `RecordImpersonation` failure returns 500 `INTERNAL_ERROR`, and `ErrIssuanceRefused` returns 403 `FORBIDDEN`.
- A sign failure returns 503 `KMS_SIGNING_UNAVAILABLE`.
- A joined CMP-058 error on `DELETE` returns 422.
- The `DELETE` body has no `endReason`, and the list response carries `hasMore` and items with no `reason` or `ticketId`.
- A tenant-admin caller of `GET /v1/admin/impersonation` and of `DELETE /v1/admin/impersonation/{id}` receives 403 `FORBIDDEN`, and the fake service records no `ListActive` or `End` call (rule 7); the no-call assertion is the one that discriminates.

Tier-2 Postgres tests (`tests/tier2_component/stores`, extending `TestTokenIssuanceStoreContract`'s harness):
- **EndImpersonation.** The first call revokes the row with `revoked_reason = 'impersonation_ended'`. A retry returns `NotActiveError{revoked}`. An expired row returns `NotActiveError{expired}`. A plain user token's `jti` and a delegation child's `jti` each return `ErrNotFound` and stay unrevoked.
- **Single winner.** N concurrent `EndImpersonation` calls with randomized start jitter yield exactly one success and N−1 `ErrNotActive`. This is the assertion that discriminates the conditional update; a fake-backed service test cannot make it. Randomized interleavings of `EndImpersonation`, `RevokeBySubject` on the target, and `RevokeImpersonationsByActor` leave the row revoked exactly once, with the `revoked_at` of the first committer.
- **ListActiveImpersonations.** The listing excludes revoked rows, expired rows, delegation children, and plain tokens, and returns rows from two tenants.
- **RevokeImpersonationsByActor.** It revokes the actor's active rows in two tenants and returns their JTIs. It leaves another actor's rows, a delegation child whose `act_sub` equals the actor, and expired rows untouched.
- **Recording fence (rule 1 (c)).** The first two cases discriminate. A test transaction locks the target's `users` row `FOR UPDATE` and sets `deleted_at` without committing; a concurrent `RecordImpersonation` blocks, then returns `*UserInvalidatedError` after the test commits, and no row is written. The same case runs on the caller's `users` row in a tenant other than the target's. Randomized interleavings of `RecordImpersonation` with the full-revoke sequence (`userpg` `Update` setting `deleted_at`, then `RevokeBySubject`, then `RevokeImpersonationsByActor`), run once on the target and once on the admin, hold the invariant: either `RecordImpersonation` returned `*UserInvalidatedError` and no row exists, or the row exists with `revoked_at` set. The other cases: a missing `users` row passes, a `users` row with `disabled` set and `deleted_at` null (soft disable) passes, a hard-disabled row (`deleted_at` set without a following `RevokeBySubject`) is refused, and an empty `callerTenantID` writes nothing.
- **RLS.** Impersonation rows are invisible under a tenant `InTx` for another tenant and visible under `__all__`.
- **Invariant checker.** After the suite, every row with a non-null `act_sub` and a null `parent_jti` was written by the impersonation `RecordImpersonation` path.
- **Step (1a).** `RecordWithAudit` and `RecordWithRotationAudit` whose subject or actor is a live impersonation row return `*SourceImpersonationError` naming that role and commit no row. A revoked impersonation row returns `*SourceRevokedError`. A live impersonation subject with a revoked actor returns `*SourceRevokedError` naming the actor and commits no row; this case discriminates CODE-2 item 7's cross-source order from a per-source loop. A delegation child passes.

`IssuedToken.SourceRefusal` unit test (`recheck_test.go`, tier 1): one case per branch of CODE-2 item 7's method, plus a revoked impersonation row and a delegation child.

Token Service unit test (`source_recheck_test.go`): `*SourceImpersonationError` renders `400 invalid_grant` with its reason and a `rejected:<reason>` audit row.

**Playground step (1a) (CODE-2 item 8).** One assertion per branch: an `apiKey` mint whose subject row is a live impersonation row returns 401 `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` with `details.subjectTyp` = `user_bearer`, makes no signer call, and audits reason `subject_token_impersonation`. A revoked row audits `subject_token_revoked`, a missing row mints, and a read error returns 500 with no signer call. Extend `TestAPIKeyModeRejectsCapabilitySubjectToken` (tests/tier3_contract/playground/playground_test.go, tier 3) to assert `details.subjectTyp` = `session_capability` and no `details.presentedTyp`.

`userTokenRevoker` unit tests, with a fake store and emitter:
- The cascade revokes the actor's rows in two target tenants and returns the union of JTIs.
- Each cascade call emits exactly one `cross_tenant_read` carrying the fields CODE-5 item 3 names, also when the cascade revokes no row; the user also has own tokens revoked, so a `RowCount` equal to the union count fails.
- When the cascade fails after `RevokeBySubject` commits, the user's own JTIs are returned with the error.
- When `RevokeBySubject` fails and the cascade succeeds, the cascade JTIs are returned.
- When the emit fails, the JTIs are still returned and the returned error wraps the emit error.
- When `revokeImpersonations` is false, no `__all__` call is made and no event is emitted.
- A nil emitter fails closed.

Full-revoke handler tests, with fakes:
- With a platform-admin caller and a target whose mapping omits `platform-admin`, the cascade runs; this case discriminates a target-role test.
- With a tenant-admin caller of the target's tenant and a target whose mapping omits `platform-admin`, no `__all__` query runs and `tokenRevokeError` names the skipped cascade.
- On a partial error, every returned JTI is pushed into the cache and counted, and `tokenRevokeError` is set.
- Commit order (CODE-5 item 5). Extend `TestFullRevokeRunsItsStepsInOrder_spec_11_4`'s `orderLog` so the users-store `Update` is logged as returning before `RevokeUserTokens` is called.

Wiring (`release_wiring_test.go`):
- No impersonation route is registered when any one of the Postgres pool, signer, propagator, or audit store is absent.
- The `ImpersonationService` field is a true nil in that case.
- No impersonation sweep goroutine starts.

Served document (`tests/tier3_contract/admin_impersonation/openapi_document_test.go`, new, tier 3, modelled on `TestServedDocumentOpenResponsesByIDHasNoDeleteOperation`, `// spec: §15.1`): the `delete` operation of `/v1/admin/impersonation/{id}` in the document `openapi.Handler()` serves declares `200` with `impersonationSessionId` and `endedAt`, declares `404` and `409` referencing `#/components/schemas/Error`, and declares no `204`.

Tiers: 0, 1, 2, 3.

### TEST-2 · Multi-replica integration and lenny-ops security tests

`tests/tier4_integration/impersonation_revocation_cross_replica_test.go` (new) runs two gateway processes on one Postgres and one Redis, started as `tenant_deletion_phase4_test.go` starts them. Cases:
- **End on a non-issuing replica.** Issue on A and `DELETE` on B. The bearer gets 401 on A and B within the propagation bound. Run this once with Redis up and once with the Redis link cut through `tcpfault`, where the bound is the 30-second rehydrate.
- **Listing.** `GET` on B lists the session A issued.
- **Restart.** Stop A and start a new A before `exp`. B still lists the session, and `DELETE` on B succeeds.
- **Target full revoke.** A §11.4 full revoke of the target user makes both replicas refuse the bearer. A later `DELETE` returns 409 `INVALID_STATE_TRANSITION`, and no `admin.impersonation_ended` is written.
- **Admin cascade.** Create a second platform-admin, `dave@acme.com`, as a user in `auth.DefaultTenantID` with a platform-managed mapping that lists `platform-admin`, and send it as `X-Lenny-User-ID` on this case's mints. Mint bearers into two target tenants, then fully revoke `dave@acme.com` as the harness's default caller. Every one of those bearers is refused.
- **Expiry.** With `durationSeconds=60`, once `exp` has passed, no ended event is written and the session leaves the listing.
- **Concurrent `DELETE`.** Concurrent `DELETE` calls on A and B yield exactly one 200, one 409, and one ended event.

`tests/tier9_security/ops_revocation_enforcement_test.go`:
- Add an "impersonation end" subtest to `revocationPaths`, so it runs with Redis up and with Redis cut. Mint with `tenantAdmin`, wait until `lenny-ops` authenticates the bearer, call `DELETE /v1/admin/impersonation/{jti}`, then `requireRevokedOnOps`.
- In the same stack, `POST /v1/admin/issued-tokens/{jti}/revoke` with the target `tenantId` on an impersonation bearer returns 200, and the gateway and `lenny-ops` then refuse the bearer.
- A live impersonation bearer presented as both caller and `subject_token` (self-rotation) returns `invalid_grant` reason `subject_token_impersonation`, and `GET` still lists it. This is the discriminating case. After it is ended, its self-rotation is refused at caller authentication with 401 `TOKEN_REVOKED` (§13.3 "Client authentication on /v1/oauth/token" and impersonation rule 5), and the ended bearer presented as `subject_token` under the target's live user bearer as caller returns `invalid_grant` with reason `subject_token_revoked` (step (1a)).
- The existing "user invalidation" subtest also asserts that `lenny-ops` refuses the impersonation bearers `a` and `b`.
- A live impersonation bearer pasted into `POST /v1/playground/token` returns 401 `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED`.
- **Nested impersonation (rule 1 caller check).** Provision the target as a user whose platform-managed mapping lists `platform-admin`, as the tier-4 admin cascade case does. The live impersonation bearer is accepted by `GET /v1/admin/impersonation` and refused by `POST /v1/admin/impersonation` with `403 FORBIDDEN`. `GET` then lists only the outer session, and no `admin.impersonation_started` row carries the target's `sub` as `admin_sub`. The `GET` acceptance makes the case discriminating, because it shows the `403` comes from the caller check rather than the role check.

Tiers: 0, 4, 9.

## Edge cases and accepted failure modes

The spec-owned cases are in the spec-changes file, under **Edge cases and accepted failure modes**. The following cases are owned by the code alone:

- **The issuance `RecordImpersonation` commits but the response write fails.** The admin holds no bearer, and the row stays active until `exp` or a `DELETE`. `GET` lists it, so the session can be ended.
- **The cascade emit fails after `RevokeImpersonationsByActor` commits.** The JTIs are still pushed and counted, and `tokenRevokeError` carries the emit error (CODE-5 item 3).
- **A missing collaborator at startup.** The impersonation routes are absent rather than served without revocation (CODE-3 item 6).

## Files touched on application (non-spec)

- migrations/0190_issued_tokens_impersonation_index.up.sql (new)
- migrations/0190_issued_tokens_impersonation_index.down.sql (new)
- migrations/issued_tokens_impersonation_index_test.go (new)
- tests/tier2_component/migrations/prod_schema_test.go
- pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go
- pkg/gateway/storage/issuedtokenstore/recheck.go
- pkg/tokenservice/tokenservice.go
- pkg/tokenservice/source_recheck_test.go
- pkg/gateway/storage/issuedtokenstore/recheck_test.go
- pkg/gateway/mcpfabric/playground/token.go
- pkg/gateway/mcpfabric/playground/playground.go
- pkg/gateway/mcpfabric/playground step (1a) test file (new)
- cmd/lenny-gateway/httpsurface.go
- pkg/gateway/credentials/impersonation/service.go
- pkg/gateway/credentials/impersonation/store.go (deleted)
- pkg/gateway/credentials/impersonation/impersonation.go
- pkg/gateway/credentials/impersonation/service_test.go
- cmd/lenny-gateway/workers.go
- cmd/lenny-gateway/adminrouter.go
- cmd/lenny-gateway/wiring_fields.go
- cmd/lenny-gateway/user_revocation.go
- cmd/lenny-gateway/user_revocation_test.go
- cmd/lenny-gateway/release_wiring_test.go
- pkg/gateway/externalapi/admin/impersonation.go
- pkg/gateway/externalapi/admin/impersonation_test.go
- pkg/gateway/externalapi/admin/signing_unavailable_test.go
- pkg/gateway/externalapi/admin/users.go
- pkg/gateway/externalapi/admin/users_invalidate.go
- pkg/gateway/externalapi/admin full-revoke handler tests
- pkg/gateway/externalapi/openapi/openapi.json
- pkg/gateway/externalapi/openapi/routestub_test.go
- tests/tier2_component/tokensigning/kms_outcome_test.go
- tests/tier2_component/stores/issuedtokenstore_test.go
- tests/tier3_contract/admin_impersonation/openapi_document_test.go (new)
- tests/spec-map.json
- tests/tier3_contract/playground/playground_test.go
- tests/tier4_integration/impersonation_revocation_cross_replica_test.go (new)
- tests/tier4_integration/tenant_deletion_phase4_test.go
- tests/tier4_integration/jwt_keyset_cross_process_test.go
- tests/tier5_e2e_kind/jwt_keyset_cross_replica_test.go
- tests/tier8_chaos/jwt_keyset_chaos_test.go
- tests/tier9_security/ops_revocation_enforcement_test.go
- tests/tier11_docs/impersonation_revocation_docs_test.go (new)
- docs/operator-guide/security.md
- docs/api/admin.md
- docs/operator-guide/observability.md
- docs/reference/metrics.md
- docs/reference/error-catalog.md
- BUILD-GAPS.md
