# Non-spec changes: Data lifecycle operations do not terminate and erase every session and store they cover

## Design (implementation-facing)

**Store-and-phase-to-owner table.** Each row names one step of a lifecycle operation, or one store it erases, and the deliverable or owner that covers it. "Existing" means the tree at the merge base already covers it and this proposal changes nothing there.

| Operation | Step or store | Owner |
|:--|:--|:--|
| All lifecycle operations | Creation fence on every session insert, and the session reservation | 0094: MIG-2, CODE-1, CODE-2 |
| All lifecycle operations | Shared drain | 0094: CODE-3 |
| User erasure | Job record, restriction set, and restriction clear | 0094: MIG-1, CODE-6 |
| User erasure | Drain in `initiated`, phase dispatch, receipt, and completion | 0094: CODE-7 |
| User erasure | Step 1 `LeaseStore` release | Existing no-op recorded in F-12.1.3, which holds as an invariant once the drain runs before the erasers |
| User erasure | Step 3 Redis access-token and routing caches | Existing decision F-12.2.16, which stands |
| User erasure | Session, transcript, interaction, queue, and other wired erasers | Existing |
| User erasure | Checkpoint manifest, retention, and metadata rows (N-3) | P5b (piece B) |
| User erasure | `coordination_lease` mirror rows | P5b (piece B) |
| User erasure | User registry row (SG-9 user half) | P5b (piece B) |
| User erasure, tenant deletion, tenant suspension | Pod release and terminal tail of a session bound on another replica | P2 (residual; REC-1 files it) |
| Tenant deletion | Phase 1 creation fence | 0094: CODE-1, CODE-2 |
| Tenant deletion | Phase 1 API-key issuance and user sign-up gate | Unassigned (REC-1 files it) |
| Tenant deletion | Phase 2 drain | 0094: CODE-5 |
| Tenant deletion | Phase 3 token and credential-lease revocation (F-12.8.30) | P5b (piece B), gated on P1's lease reclaimer |
| Tenant deletion | Phase 3.5 legal-hold segregation | Existing |
| Tenant deletion | Phase 4 stores already wired | Existing |
| Tenant deletion | Phase 4 missing stores (F-12.8.34, F-12.8.35, F-12.8.37) | P5b (piece B) |
| Tenant deletion | Phase 4 audit eraser foreign key (F-12.8.25) | P5b (piece C), before piece B's audit eraser |
| Tenant deletion | Phases 5 and 6 | Existing |
| Tenant suspension | Marker, audit, repeat drain, and response | 0094: CODE-4 |
| full_revoke | Creation race (F-11.4.14 creation half) | 0094: CODE-1 |
| full_revoke | Primary listing, detached context, error continuation, and per-effect counts (F-11.4.12, F-11.4.13, F-11.4.14 listing half) | P5b (full_revoke fixes), after P1 |
| full_revoke | Release mechanics | P1 (0093) |
| `DELETE /v1/responses/{id}` | Route removal (F-15.1.48) | 0094: CODE-8 |
| `GET /v1/responses/{id}` | User check (F-15.1.54) | Unassigned |

**Lock discipline of the creation fence.** The normative rule is SPEC-1. In code, the fence takes `FOR SHARE` on the tenant row and then on the owning user's row, inside the session insert's existing transaction, before the `INSERT`. `FOR SHARE` conflicts with `FOR UPDATE` (taken by `tenantstore` and `userstore` `Update` and by `ClearProcessingRestrictionTx`) and with `FOR NO KEY UPDATE` (taken by any plain `UPDATE`, such as `userstore` and `tenantstore` `SoftDelete`); summary D1 records why no weaker mode works. The lock order is tenant row, then user row, and every marker writer locks exactly one of the two rows, so no lock cycle exists.

**Creation-path table.** This table is the single normative statement, within this proposal, of how each session-insert path meets the creation fence: the owner the fence reads, whether the row is a delegated child, the gate that runs before any side effect, the side effects before the insert and how each is undone on a `*sessionstore.CreationRefusal`, the refusal surface, and the derive-audit metric outcome. Summary D3, D4, D5, and D25, SPEC-1 Edits 1a and 1d, CODE-1, CODE-2, T1.1, T2.1 to T2.3, and X1 cite it and do not restate it. Each row states the behavior after this proposal lands. The column rules below the table hold for every row, and the verification notes after them state what the tree does at the merge base.

| Row | Path and insert site | Owner the fence reads | Delegated child | Gates before any side effect, in order | Side effects before the insert, and the undo on a refusal | Refusal surface | Derive-audit outcome |
|:--|:--|:--|:--|:--|:--|:--|:--|
| C1 | `POST /v1/sessions` and `POST /v1/environments/{name}/sessions` (`create.go` `claimMintPersist`) | `req.UserID` from the body (`validateAndBuildCreateRow`). Empty: no user row is read. | No | `requireActiveUser`, then `requireCreationAllowed(tenantID, req.UserID)` in `createAdmissionGates`, before the quota, concurrency, rate-limit, and policy gates. | Admission rate counter (`requireAdmissionRateLimit`): kept, as on every later failure. Pod claim (`claimAtCreate`, when a pod binder is wired): `rollbackClaim`. Upload token: minted in memory, nothing to undo. | REST `403` with the code and details. | None |
| C2 | MCP `create_session` (`mcptools_register.go`, `SessionCreator.CreateSessionService`, which runs `handleCreate`) | `in.UserID` | No | As C1. | As C1. | MCP tool error with the code and details, through the existing `ServiceError` mapping. | None |
| C3 | `POST /v1/sessions/start` (`create_and_start.go` `mintClaimStartPersist`) | `req.UserID` | No | `requireActiveUser`, then `requireCreationAllowed(tenantID, req.UserID)` in `createAndStartGates`, in place of `requireTenantState`. | Pod claim, prepare, and launch with the credential lease (`claimAtCreate`, `startOnPod`): `rollbackBinding`. Coordination lease (`acquireCoordinationLease`): `releaseCoordinationLease`. Admission rate counter: kept. | REST `403`. | None |
| C4 | Built-in adapter implicit create for OpenAI Chat Completions and Open Responses (`translator` `BindSingleShot`, `cmd/lenny-gateway` `sessionSingleShotBinder`, `CreateAndStartService`) | The request's OpenAI `user` field (`SingleShotSpec.UserID`) | No | As C3. | As C3. | Native error envelope with status `403` and the code (`singleShotErrorFrom`, `writeSingleShotError`). | None |
| C5 | `type: mcp` implicit create (`/mcp/runtimes/{name}`) | No insert exists. | No insert exists. | No insert exists. | No insert exists. | No insert exists. A later insert goes through `store.Create` and maps the refusal as C2 does. | None |
| C6 | Single-shot binder fallback (`translator/singleshot.go` `noopSingleShotBinder.BindSingleShot`, used when no binder is injected) | `SingleShotSpec.UserID` | No | None. | None. | `*SingleShotError{HTTPStatus: 403, Code: ref.Code, Message: ref.Message}`, rendered in the native envelope. | None |
| C7 | mcptools minimal fallback (`mcptools_register.go` `create_session` with no `SessionCreator`) | `in.UserID` | No | None. | None. | `mcp.NewToolError(ref.Code, ref.Message, ref.Details)`. | None |
| C8 | Derive main insert (`derive.go` `handleDerive`), through `store.CreateReserved` | `req.UserID`, else `source.UserID` | No | `requireActiveUser`, then `store.Reserve(tenantID, owner, derivedID, false)` after the owner resolves and before the copy. | The isolation-downgrade audit event, when a platform-admin downgrade is authorized (`EmitDeriveIsolationDowngrade`): kept. Snapshot copy under `derivedID`: the chunk objects and the manifest `Put`, `ConfirmChunk`, and `Finalise` (`deriveChunkedCheckpoint`), or the legacy single object. Undo: the blob store's `DeleteBySession(tenantID, derivedID)` and the manifest store's `DeleteByUser(tenantID, "", []string{derivedID})`, then `ReleaseReservation`. A deletion error or a blob store without `DeleteBySession` keeps the reservation. | REST `403`. | None |
| C9 | Derive-failure audit row (`derive.go` `persistDeriveFailure`, written only under `gateway.persistDeriveFailureRows`) | `source.UserID` | No | None of its own. It runs after C8's reservation and a failed copy. | The failed copy's partial objects and manifest rows are not deleted, as at the merge base. When the C9 row commits, it carries the ID for `source.UserID`, and it ends C8's reservation only when that is also the derive's owner; otherwise C8's reservation stays. Either way, erasure of the owner reaches the data. | None. The handler returns the copy failure (`503 DERIVE_SNAPSHOT_UNAVAILABLE` or `500 INTERNAL_ERROR`). | `error` |
| C10 | Replay (`replay.go` `handleReplay`) | `source.UserID` | No | `requireActiveUser`. The fence is the only creation check, because replay writes no session data before its insert. | The isolation-downgrade audit event, when one is emitted: kept. No session data: the snapshot reference is rewritten rather than copied, and the transcript copy runs after the insert. | REST `403`. | None |
| C11 | Delegated child (`delegation/service.go` `insertChildSession`, reached only from mcptools `delegate_task`), through `store.CreateReserved` when a reservation was taken | `parent.UserID` (`buildChildSession`; `delegate_task` sets no `Request.UserID`) | Yes | `requireActiveDelegator` over the parent's user row and the tenant row with `IsDelegatedChild` true, before `Delegate`. When the lease declares `fileExport`, `store.Reserve(tenantID, owner, childID, true)` before `materializeExport`. | Tree budget (`reserveTreeBudget`): returned by `Delegate`'s deferred `treeBudget.Return`. Child token: minted in memory and not persisted. File export objects under `childID` (`materializeExport`): the injected `SessionObjects.DeleteBySession(tenantID, childID)`, then `ReleaseReservation`. A deletion error or an unwired `SessionObjects` keeps the reservation. | MCP tool error with the code, through the `delegate_task` mapping. | None |

Column rules:

- **Delegated child** is `Session.DelegationDepth > 0`. Only `delegation.Service.buildChildSession` writes a non-zero depth (the parent's depth plus one). `ParentSessionID` does not classify a row, because `handleDerive`, `persistDeriveFailure`, and `handleReplay` set it to the source session.
- **Gates.** `requireActiveUser` is the §11.4 check on the caller. It runs first on C1 to C4, C8, and C10, and it is not the fence. Every other gate evaluates `CreationMarkers.Refusal` itself, over the tenant row and the owner's user row, so it answers the fence's code for the same marker state. That code is the first match in the order `USER_INVALIDATED`, `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`, `TENANT_NOT_ACTIVE` (CODE-1 item 2). A read error in a gate answers `500 INTERNAL_ERROR`, or the `INTERNAL_ERROR` tool error, and writes nothing.
- **Reservation.** `store.Reserve` runs the fence in its own transaction and records the session ID for its owner, and records nothing for an empty owner. A path takes one when it writes data keyed to the new session ID before the insert (C8, and C11 with `fileExport`). A path that reserved inserts through `store.CreateReserved`, which, for a row with an owner, deletes its reservation in the insert transaction and refuses when that reservation no longer exists; a row with no owner inserts as `Create` does. Only the owner's erasure (`DeleteByUser`) or the tenant's deletion (`DeleteByTenant`) removes another writer's reservation, so a write that outlives a completed erasure is refused and undone rather than inserted. `store.Create` also deletes a reservation whose ID and owner match the row. The erasure's session lister returns the owner's reserved IDs with the owner's session IDs (CODE-7 item 8). A reservation is released only after the undo deleted what was written, and a non-refusal `Create` error deletes nothing and keeps the reservation, because such an error can follow a committed insert.
- **Undo** runs only on `errors.As(err, &ref)` for `*sessionstore.CreationRefusal`, after the branch's existing rollback and before the refusal is written.

Verification against the tree at the merge base:

- C1 and C2: `createAdmissionGates` runs `requireActiveUser` on the principal and no tenant or owner gate. The `store.Create` branch of `claimMintPersist` rolls back the claim and answers `503 SESSION_CREATION_FAILED`.
- C3 and C4: `createAndStartGates` runs `requireActiveUser` and then `requireTenantState`, with no owner gate. The `store.Create` branch of `mintClaimStartPersist` releases the coordination lease and the binding and answers `503`. `singleShotErrorFrom` copies the status and the code.
- C5: `cmd/lenny-gateway/httpsurface.go` mounts `mcpruntimes.New(w.runtimes, nil)`, and the handler creates no session row.
- C6: the binder wraps the `Create` error, and `writeSingleShotError` renders it as `500 server_error`.
- C7: the fallback returns the raw `Create` error.
- C8: no tenant or owner gate runs before the copy. The `Create` branch answers `500 INTERNAL_ERROR` and deletes nothing.
- C9: `persistDeriveFailure` records `error` on any `Create` error and `fenced` on the primary pre-read's error or generation mismatch.
- C10: the `Create` branch answers `500 INTERNAL_ERROR`.
- C11: `requireActiveDelegator` reads only the parent's user row and refuses on `!IsActive()`, so it refuses the children of a soft-disabled user, which §11.4 admits, and it reads no tenant marker. `buildChildSession` mints `childID` and runs `materializeExport` before `insertChildSession` calls `Create`. A `Create` error reaches the generic return of the `delegate_task` mapping.

Spec sites that state part of this table, each regenerated from it:

| Spec site | Rows it states | Staged edit |
|:--|:--|:--|
| §12.8 **Lifecycle fence and drain** *Creation fence* | The path list (C1 to C11), the owner, the delegated-child rule, the response scope (C9 excluded), the early check, and the reservation and undo (C8, C11) | Edit 1a |
| §16.1 `lenny_session_derive_failure_audit_total` | C9 | Edit 1d |
| §15.1 `USER_INVALIDATED` catalog row | The paths that return it | Edit 4c |
| §15.1 `ERASURE_IN_PROGRESS` catalog row | The reservation-ended refusal (C8, C11) | Edit 4k |

**One drain.** `admin.DrainSessions` (CODE-3) is the only drain. Suspension (CODE-4), tenant-deletion Phase 2 (CODE-5), and the erasure runner (CODE-7) call it. What a successful drain proves is stated once, in the doc comment CODE-3 item 4 specifies.

**Erasure entry-point table.** This table is the single normative statement, within this proposal, of how each erasure entry point treats the legal-hold preflight, the job record, the failure counter, the audit events, the `processing_restricted` flag, the session drain, and the HTTP response. Summary D15, D23, and D24, SPEC-2 Edits 2a, 2d, 2g, 2h, 2i, 2j, and 2k, SPEC-4 Edits 4e to 4j, CODE-6, CODE-7, and the T8 tests cite it and do not restate it. The legal-hold preflight is a handler check that writes nothing when it blocks. The job carries a second check, the runner re-check in row E6, which fails the job through the ordinary failed-job path. Each row states the behavior after this proposal lands. The verification notes after the table state what the tree does at the merge base and which deliverable changes it.

| Row | Entry point | Legal-hold preflight | Job row written or changed | Status and `failure_phase` | `lenny_erasure_job_failed_total` | Audit event | `processing_restricted` | Session drain | HTTP response |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| E1 | `POST /v1/admin/users/{userId}/erase` without `acknowledgeHoldOverride` | Runs in `handleEraseUser` after the registered-user check and before `Start`. A hold found blocks the request. | Blocked: none. Admitted: `Start` inserts the job. The user already has an in-progress job: none. | Blocked: no job exists. Admitted: `initiated`, no `failure_phase`. In-progress job: unchanged. | Not incremented. | Blocked: `gdpr.erasure_blocked_by_hold` with `tenantId`, `userId`, `holdCount`, `holds`, and `heldSessions`, and no `jobId`. Admitted: `admin.user.erasure_initiated` with `tenantId` and `jobId`. In-progress job: none. | Blocked: unchanged. Admitted: set, with `erasure_job_id` naming the new job, in the transaction that inserts the job. In-progress job: unchanged. | None in the handler. The tick drains in row E6. | Blocked: `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` with `details.holds` and `details.heldSessions`. Preflight read error: `500 INTERNAL_ERROR`. Admitted: `202` with `jobId`, `userId`, `tenantId`, and `phase: initiated`. In-progress job: `202` with that job's `jobId`. `Start` error: `500 INTERNAL_ERROR`. Unregistered user: `404 RESOURCE_NOT_FOUND`. |
| E2 | `POST /v1/admin/users/{userId}/erase` with `acknowledgeHoldOverride: true` | Runs in `handleEraseUser` as in E1. When holds are found, an empty `justification` answers `400` and a caller without `platform-admin` answers `403`; otherwise the request is admitted with the override. When no hold is found, the override is ignored and the row is E1. | Admitted: `Start` inserts the job with `InitiatedBy` and `OverrideReceipt`. In-progress job: none, and that job keeps its own `OverrideReceipt`. | Admitted: `initiated`. | Not incremented. | Admitted: `gdpr.legal_hold_overridden` after `Start`, with `tenantId`, `userId`, `jobId`, `holdCount`, and the override fields (`legalHoldOverride`, `overrideBy`, `overrideJustification`, `overrideAt`, `overriddenHolds`, and `holds`), then `admin.user.erasure_initiated`. No `gdpr.erasure_blocked_by_hold`. In-progress job: none. | Admitted: set in the transaction that inserts the job. | None in the handler. | Admitted: `202`. Missing justification: `400 VALIDATION_ERROR`. Not `platform-admin`: `403 FORBIDDEN`. In-progress job: `409 INVALID_STATE_TRANSITION`. |
| E3 | `POST /v1/admin/erasure-jobs/{jobId}/retry` | Runs in `handleRetryErasureJob` after the job lookup, the tenant-visibility check, and the `failed`-phase check, and before `Restart`. No override applies, including the job's own `OverrideReceipt`. | Blocked: unchanged. Admitted: `Restart` sets `initiated`, clears `Failure`, `CompletedAt`, `ErasedAt`, `ReceiptAt`, and `Billing`, and appends `initiated` to `PhaseLog`. `OverrideReceipt` stays on the job for the receipt and exempts no hold in E6. Another job for the user in progress: unchanged. | Blocked: stays `failed` with its recorded `failure_phase`. Admitted: `initiated`. | Not incremented. | Blocked: `gdpr.erasure_blocked_by_hold` with the E1 fields plus `jobId` naming the retried job. Admitted: `gdpr.erasure_job_retried` with `tenantId`, `userId`, and `jobId`. | Blocked: unchanged, whether set or cleared by an earlier operator clear. Admitted: set, with `erasure_job_id` naming the job, in the transaction that restarts it. | None in the handler. The tick drains in row E6. | Blocked: `409 ERASURE_BLOCKED_BY_LEGAL_HOLD`. Unknown job or another tenant's job: `404 RESOURCE_NOT_FOUND`. Job not `failed`: `409 ERASURE_JOB_NOT_FAILED`. Preflight read error: `500 INTERNAL_ERROR`. Another job in progress: `409 INVALID_STATE_TRANSITION`. Admitted: `202` with `jobId`, `userId`, `tenantId`, and `phase: initiated`. |
| E4 | Crash resume: `RunPending` on the next tick of the gateway leader | None in a handler. The E6 re-check runs again only when the persisted phase is `initiated`. | Advances from the persisted phase with one `Update` per phase and re-runs no step of an earlier phase. A job with `ErasedAt` set goes to the receipt and `Complete`. | From the persisted phase onward. A failure records the label of the phase that failed (CODE-7 item 1). | Incremented once per failure, under that label. | `gdpr.erasure_completed` at completion. A crash between the receipt and `Complete` writes an identical second receipt. | Stays set until `Complete` clears it where `erasure_job_id` matches. | Runs only when the persisted phase is `initiated`. | None. |
| E5 | `POST /v1/admin/erasure-jobs/{jobId}/clear-processing-restriction` | None. | Unchanged. `ClearRestriction` admits only a `failed` job. | Unchanged. | Not incremented. | Cleared: `gdpr.processing_restriction_cleared` with `tenantId`, `userId`, `jobId`, and `justification`. Refused: none. | Cleared, with `erasure_job_id` emptied, through `ClearRestriction` and its session-local trigger bypass, when `erasure_job_id` names this job or is empty. Refused: unchanged. | None. | `200` with `jobId`, `userId`, `tenantId`, and `processingRestricted: false`. Job not `failed`: `409 ERASURE_JOB_NOT_FAILED`. `erasure_job_id` names another job of the user: `409 INVALID_STATE_TRANSITION`. Missing justification: `400 VALIDATION_ERROR`. Unknown job, another tenant's job, or unknown user: `404 RESOURCE_NOT_FOUND`. Store error: `500 INTERNAL_ERROR`. |
| E6 | The runner's `initiated` phase, inside the tick | The runner re-check, after the drain and before the advance to `store_deleting`, through the same enumeration as the handlers. It exempts the holds listed in `OverrideReceipt["holds"]` only while `PhaseLog` records no `failed` transition, so a retried job exempts no hold. | Success: `Update` to `store_deleting`. Failure: `fail` sets `failed`. | Memory preflight failure: `memory_store_preflight`. Drain failure: `store_delete`. A hold the re-check does not exempt, or a hold read error: `store_delete`, with the hold tuples or the read error in `Failure`. | Incremented once, under the failure's label. | None. `ErasureJobFailed` fires from the counter. | Stays set on every failure. | `DrainSessions(tenantID, userID)` over the primary listing, before the re-check. | None. |

The table orders the checks in E1 and E2 as follows: the registered-user check, the preflight, the override validation, and `Start`. A duplicate erase for a user with a hold therefore answers `409` rather than the existing job's id. In E3 the order is the job lookup, the tenant-visibility check, the `failed`-phase check, the preflight, and `Restart`. In E5 the order is the justification check, the job lookup, the tenant-visibility check, and `ClearRestriction`, which checks the `failed` phase before the flag's job. Both handlers and the runner enumerate holds through one exported function that lists the user's sessions with `ListFilter{UserID: userID, Primary: true}` and checks each session's artifact hold (CODE-7 item 7). A hold set after the job advances to `store_deleting` is not re-checked; the re-check closes the window up to the first eraser.

Verification against the tree at the merge base:

- E1: `handleEraseUser` (`pkg/gateway/externalapi/admin/erasure.go`) runs `heldResourcesForUser` (`pkg/gateway/externalapi/admin/legalhold.go`) after `users.Get`, and on a hold without override emits `gdpr.erasure_blocked_by_hold` with no `jobId`, answers `409`, and writes nothing. The preflight lists every tenant session from the read pool (`ListFilter{}`) and filters the user in Go. An admitted erase calls `Runner.Start`, which writes the in-memory job store, then sets the flag in a separate `users.Update` whose error is discarded, and starts `runErasureToCompletion` in a goroutine. No duplicate check exists. CODE-6 and CODE-7 items 6 and 7 change this.
- E2: the override is validated and `gdpr.legal_hold_overridden` is emitted after `Start` with `jobId`, as the row states. `overrideReceipt` lives only in the goroutine's arguments and is not persisted. CODE-6 item 2 persists it.
- E3: `handleRetryErasureJob` answers `409 ERASURE_JOB_NOT_FAILED` for a job not `failed`, runs no preflight, resets the job to `initiated` with an `Update` that clears `Failure` and `CompletedAt`, leaves the flag untouched, starts the goroutine with a nil override, and emits `gdpr.erasure_job_retried`. CODE-6 `Restart` and CODE-7 item 7 change this.
- E4: the tree has no crash resume. The job store is in memory, and `Runner.Run` re-runs every step from the top. CODE-6 and CODE-7 items 1 to 5 add it.
- E5: `handleClearErasureRestriction` checks no job phase, and `pgstore.ClearProcessingRestriction` sets `lenny.clear_processing_restriction` and empties `erasure_job_id` whichever job set it. While a job is in progress, the clear therefore reopens the creation fence after the drain has run. CODE-6 item 4 and CODE-7 item 10 change this.
- E6: `Runner.Run` (`pkg/gateway/storage/erasurejob/runner.go`) runs the memory preflight and then store deletion, with no drain and no hold check. `fail` invokes the `WithFailureObserver` hook, which increments `lenny_erasure_job_failed_total`. CODE-7 items 1 and 7 add the drain and the re-check.

Spec sites that state part of this table, each regenerated from it:

| Spec site | Rows it states | Staged edit |
|:--|:--|:--|
| §12.8 **`DeleteByUser` dependency-ordered deletion sequence** step 0 | Preflight placement in E1 to E3, the blocked outcome, the retry's override rule, and the E6 re-check | Edit 2j |
| §12.8 **`--acknowledge-hold-override` admin action** | The E2 missing-justification response | Edit 2k |
| §12.8 **Processing restriction during erasure (GDPR Article 18)** | The flag write in E1 to E3, and the E6 drain failure | Edits 2a and 2d |
| §12.8 **User-level erasure invocation** | The single in-progress job (E1, E2) and the E3 order and `INVALID_STATE_TRANSITION` | Edit 2g |
| §12.8 **Erasure job phase field (crash recovery)** | E4 and the E6 order | Edit 2h |
| §15.1 erase row | The E1 and E2 preflight, and the in-progress-job answers of E1 and E2 | Edit 4j points to the invocation paragraph for the in-progress-job answers. The row already states the handler preflight. |
| §15.1 retry row | E3 preflight | Edit 4g |
| §15.1 clear-processing-restriction row | The E5 `failed`-job and other-job refusals | Edit 4i |
| §15.1 `ERASURE_BLOCKED_BY_LEGAL_HOLD` catalog row | The E1 and E3 blocked response | Edit 4e |
| §16.5 `ErasureJobFailed` | Which rows increment the counter | Edit 4h |
| §16.7 `gdpr.erasure_blocked_by_hold` entry | The E1 and E3 blocked event | Edit 4f |
| §24.12 retry row | E3 preflight | Edit 2i |

## Staged code changes

### CODE-1 · Creation predicate and the fence inside the session insert

**Targets:** `pkg/gateway/session/sessionstore/sessionstore.go` (new `CreationMarkers`, `CreationRefusal`, and `CreationMarkers.Refusal`, and the new `Store` methods `Reserve`, `ReleaseReservation`, and `ReservedSessionIDs`); `pkg/gateway/session/sessionstore/pgstore/pgstore.go` `Create`, `DeleteByUser`, `DeleteByTenant`, and the three new methods; `pkg/gateway/session/sessionstore/memstore/memstore.go` the same methods and a new `WithCreationMarkers` option; the `Store` test doubles in `pkg/gateway/mcpfabric/delegation/service_test.go` and `pkg/gateway/mcpfabric/mcptools/elicitation_perhop_internal_test.go`; `cmd/lenny-gateway/stores.go` memory branch.

1. Add to package `sessionstore`:

   ```go
   // CreationMarkers is the lifecycle-marker state the creation fence reads
   // for one session insert. spec: §12.8 (Lifecycle fence and drain),
   // §11.4 (User Invalidation).
   type CreationMarkers struct {
       TenantFound          bool
       TenantSuspended      bool
       TenantState          string // "" and "active" admit, as tenantstore.Tenant.AcceptsNewWork
       UserFound            bool
       UserDisabled         bool   // users.disabled (§11.4 soft_disable)
       UserDeleted          bool   // users.deleted_at IS NOT NULL (§11.4 hard_disable, full_revoke)
       ProcessingRestricted bool
       ErasureJobID         string
       IsDelegatedChild     bool   // Session.DelegationDepth > 0 (Creation-path table, Delegated child)
   }

   // CreationRefusal is the typed error the fence returns. Every refusal
   // is HTTP 403.
   type CreationRefusal struct {
       Code    string
       Message string
       Details map[string]any
   }

   func (e *CreationRefusal) Error() string
   func (m CreationMarkers) Refusal(tenantID, userID string) *CreationRefusal
   ```

2. `Refusal` returns the first matching rule, in this order, and nil when none matches:
   1. `UserFound` and (`UserDeleted`, or `UserDisabled` and not `IsDelegatedChild`): `USER_INVALIDATED`, no details.
   2. `UserFound` and `ProcessingRestricted`: `ERASURE_IN_PROGRESS`, details `userId` and `jobId` (from `ErasureJobID`).
   3. `TenantFound` and `TenantSuspended`: `TENANT_SUSPENDED`, details `tenantId`.
   4. `TenantFound` and `TenantState` is neither `""` nor `"active"`: `TENANT_NOT_ACTIVE`, details `tenantId` and `state`.

   Rule 1 is the one marker-specific case; state that in the doc comment and cite `// spec: §11.4 (User Invalidation)`. The messages reuse the wording of `requireActiveUserForAction` and `requireTenantState`. The doc comment of `IsDelegatedChild` states why `ParentSessionID` does not classify a row, as the **Creation-path table** column rules state.

3. `pgstore.Create`: inside the existing `pgtenant.InTx`, before the `INSERT`, read the tenant row's `suspended` and `state` with `SELECT ... FROM tenants WHERE id = $1 FOR SHARE`, then, when `sess.UserID != ""`, read the user row's `disabled`, `deleted_at IS NOT NULL`, `processing_restricted`, and `erasure_job_id` with `SELECT ... FROM users WHERE tenant_id = $1 AND subject = $2 FOR SHARE`. A missing row sets the matching `Found` field false. Any other read error aborts the transaction and returns the wrapped error, so the insert fails closed. A non-nil `Refusal` aborts the transaction and returns the `*CreationRefusal`. `IsDelegatedChild` is `sess.DelegationDepth > 0`. The fence has no exemption: it applies to every row, including a row created terminal, such as the derive-failure row. After the `INSERT`, in the same transaction, delete the `session_reservations` row whose `tenant_id`, `session_id`, and `user_id` match the inserted row.

4. `memstore`: add `WithCreationMarkers(read func(tenantID, userID string) (CreationMarkers, error))`. `Create` calls it under `s.mu`, before the insert, and applies `Refusal` the same way. `cmd/lenny-gateway/stores.go` wires it in the memory branch over the in-memory tenant and user stores. No closure passed to `tenants.Update` or `users.Update` touches the session store, so taking those stores' locks under `s.mu` creates no lock-order cycle; state that in the option's doc comment.

5. Document in `Create`'s doc comment and in the option's doc comment that `TenantFound == false` is reachable only in `memstore`. In `pgstore`, the `sessions.tenant_id` foreign key rejects an insert for a missing tenant first.

6. Cite `// spec: §12.8 (Lifecycle fence and drain)` on `Refusal`, on the `pgstore` fence, on the `memstore` fence, and on each reservation method.

7. Session reservation (the **Creation-path table** column rule **Reservation**). Add to `sessionstore.Store`:

   ```go
   // Reserve runs the creation fence for a row that ownerUserID will own
   // and records sessionID for that owner, in one transaction. It returns
   // *CreationRefusal when the fence refuses. spec: §12.8 (Lifecycle fence and drain).
   Reserve(ctx context.Context, tenantID, ownerUserID, sessionID string, delegatedChild bool) error
   // ReleaseReservation deletes the reservation of sessionID. Idempotent.
   ReleaseReservation(ctx context.Context, tenantID, sessionID string) error
   // ReservedSessionIDs lists the owner's reserved session IDs from the primary.
   ReservedSessionIDs(ctx context.Context, tenantID, ownerUserID string) ([]string, error)
   // CreateReserved is Create for a row whose ID was reserved through Reserve.
   // spec: §12.8 (Lifecycle fence and drain).
   CreateReserved(ctx context.Context, s Session) error
   ```

   `CreateReserved` runs `Create`'s fence, then the insert, then deletes the reservation with `DELETE ... RETURNING`. Its refusal is the **Reservation** column rule's, returned as `*CreationRefusal{Code: "ERASURE_IN_PROGRESS"}` with detail `userId` only (Edit 4k), and the transaction aborts.

   `pgstore.Reserve` takes the same `FOR SHARE` reads as `Create`, applies `Refusal` with `IsDelegatedChild` equal to `delegatedChild`, and inserts into `session_reservations` (MIG-2). An empty `ownerUserID` reads only the tenant row. `DeleteByUser` deletes the user's reservations and `DeleteByTenant` deletes the tenant's, each in the transaction that deletes the session rows. `memstore` keeps a reservation map under `s.mu` with the same semantics and applies `WithCreationMarkers` in `Reserve`. Update the two test doubles.

### CODE-2 · Creation gates, reservations, and refusal mapping at every create site

**Targets:** `pkg/gateway/sessionserver/create.go` (`createAdmissionGates` and the `store.Create` error branch); `pkg/gateway/sessionserver/create_and_start.go` (`createAndStartGates` and the `store.Create` error branch); `pkg/gateway/sessionserver/quota.go` (`requireTenantState` is deleted); `pkg/gateway/sessionserver/derive.go` (`handleDerive` and its main `store.Create` error branch); `pkg/gateway/sessionserver/replay.go` (the `store.Create` error branch); `pkg/gateway/sessionserver/checkpoint_seams.go` (`CheckpointManifestWriter` gains `DeleteByUser`, which `pgstore.Store` and `partialmanifeststore.MemoryStore` already implement); `pkg/gateway/sessionserver/user_gate.go` (new `requireCreationAllowed` and `writeCreationRefusal`); `pkg/gateway/environment/translator/singleshot.go` (no-op single-shot binder); `pkg/gateway/mcpfabric/delegation/service.go` (`buildChildSession`, `insertChildSession`, and a new `SessionObjects` option); `cmd/lenny-gateway/mcpsurface.go` (the `SessionObjects` wiring); `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`requireActiveDelegator`); `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (the minimal-gateway fallback create and the `delegate_task` `Delegate` error mapping); `pkg/gateway/externalapi/errorclassify/errorclassify.go`.

Each item implements the cells of the **Creation-path table** row it names.

1. Keep `requireActiveUser` and `requireActiveUserForAction` unchanged. They remain the §11.4 check on the caller.
2. Add `requireCreationAllowed(w, r, tenantID, ownerUserID string) bool` in `user_gate.go`. It reads the tenant row (`s.tenants.Get`) and, when `ownerUserID` is non-empty, the owner's user row (`s.users.Get`), builds `CreationMarkers` with `IsDelegatedChild` false, and writes the refusal from `Refusal` when it is non-nil. `ErrNotFound` on either read leaves the matching `Found` field false. Any other read error writes `500 INTERNAL_ERROR` and returns false. Call it at the position the **Gates** cell of C1 and C3 states. Delete `requireTenantState`, which then has no caller, and retarget its tests to `requireCreationAllowed`.
3. Rows C8 and C11 take a reservation (the **Reservation** column rule) at the position their **Gates** cell states and insert through `store.CreateReserved`. A `*CreationRefusal` from `Reserve` is written through `writeCreationRefusal` on C8 and returned unwrapped from `Delegate` on C11. Any other `Reserve` error answers `500 INTERNAL_ERROR` on C8 and is returned on C11.
4. Add `writeCreationRefusal(w, ref *sessionstore.CreationRefusal)`, which writes `403` with `ref.Code`, `ref.Message`, and `ref.Details`. At the `store.Create` error branch of C1, C3, C8, and C10, add `var ref *sessionstore.CreationRefusal; if errors.As(err, &ref) { ...; writeCreationRefusal(w, ref); return }` after the branch's existing rollback and before the existing 5xx mapping, running the C8 undo where the row states one. On C8, assert the blob deleter on `s.blobs` the way `blobstore.Copier` is, and add `DeleteByUser` to `CheckpointManifestWriter`. Log an undo error with the session ID and still write the refusal. `persistDeriveFailure` (C9) gains no branch, because it has no response writer and already records `error`.
5. Map the refusal in the fallback creators, C6 and C7, as their **Refusal surface** cells state.
6. Rewrite `requireActiveDelegator` (C11) to read the parent's user row and the tenant row (through `deps.Tenants`) and to return `mcp.NewToolError(ref.Code, ref.Message, ref.Details)` from `Refusal` with `IsDelegatedChild` true. In `delegation.Service`, add the `SessionObjects` option, a one-method consumer interface holding `DeleteBySession(ctx, tenantID, sessionID string) (int, error)`, wired in `cmd/lenny-gateway/mcpsurface.go` by type-asserting `w.blobs` the way `buildSessionScopedErasers` asserts `sessionArtifactDeleter`; when the assertion fails, the option stays nil and the C11 undo keeps the reservation. Take the reservation in `buildChildSession` and run the C11 undo in `insertChildSession` on a `*CreationRefusal`. Add an `errors.As(err, &ref)` branch at the `delegate_task` `Delegate` error mapping that returns the same tool error.
7. Add `"TENANT_NOT_ACTIVE": {CategoryPolicy, false}` and `"USER_INVALIDATED": {CategoryPolicy, false}` to the `errorclassify` table beside `TENANT_SUSPENDED`, each with a `// spec: §15.1 (Error code catalog)` comment. Without them MCP classifies both codes as transient and retryable.
8. Cite `// spec: §12.8 (Lifecycle fence and drain)` on every new gate call, reservation, undo, and mapping branch.

### CODE-3 · Primary listing, tenant-scoped forced termination, and the shared drain

**Targets:** `pkg/gateway/session/sessionstore/sessionstore.go` `ListFilter`; `pkg/gateway/session/sessionstore/pgstore/pgstore.go` `List`; `pkg/gateway/session/sessionstore/memstore/memstore.go` `List` (accepts the field); `cmd/lenny-gateway/session_admin.go` `sessionAdminAdapter.ForceTerminate`; `pkg/gateway/externalapi/admin/sessions.go` `SessionAdmin` and `handleForceTerminateSession`; `pkg/gateway/externalapi/admin/tenant_suspend.go` `drainTenantSessions`; the `SessionAdmin` test doubles in `pkg/gateway/externalapi/admin/sessions_test.go` and `pkg/gateway/externalapi/openapi/routestub_test.go` (`sessionAdminStub`), and the adapter tests in `cmd/lenny-gateway/session_admin_test.go`.

1. Add `Primary bool` to `ListFilter`, documented as "read from the Postgres primary; used by lifecycle decisions. spec: §12.8 (Lifecycle fence and drain)". `pgstore.List` reads `s.pool` when it is set and `s.read` otherwise. `memstore` ignores it.
2. Change `SessionAdmin.ForceTerminate` to `ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error)`. The adapter deletes its internal `GetByID` and performs the existing locked `Update` under `tenantID`. `handleForceTerminateSession` first calls `r.sessionAdmin.GetByID` (mapping `ErrNotFound` to `404 RESOURCE_NOT_FOUND` as today) and then `ForceTerminate(ctx, sess.TenantID, sess.ID)`. Update the two test doubles and the adapter tests. The transition itself (state `failed`, class `FailureClassRuntime`, reason `FORCE_TERMINATED`, the terminal claim, and `onTerminal`) is unchanged.
3. Replace `drainTenantSessions` with an exported function in package `admin`:

   ```go
   // DrainLister and DrainTerminator are the drain's consumer interfaces.
   type DrainLister interface {
       List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error)
   }
   type DrainTerminator interface {
       ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error)
   }

   // DrainResult reports one drain.
   type DrainResult struct {
       Terminated int      // sessions this drain transitioned
       Failed     []string // ids whose transition returned an error
       Remaining  []string // ids the verifying listing found non-terminal, excluding Failed
   }

   // ErrDrainIncomplete is returned, wrapped, when Failed or Remaining is non-empty.
   var ErrDrainIncomplete = errors.New("session drain incomplete")

   func DrainSessions(ctx context.Context, l DrainLister, t DrainTerminator, tenantID, userID string) (DrainResult, error)
   ```

   The body follows SPEC-1 *Drain*: list with `ListFilter{UserID: userID, Primary: true}`; for each non-terminal row, return the partial result with `ctx.Err()` wrapped when `ctx` has ended, and otherwise call `ForceTerminate(context.WithoutCancel(ctx), tenantID, row.ID)`, so a caller's deadline stops the drain between sessions and never inside a session's terminal pipeline; treat `ErrNotFound` as nothing to drain, record any other error in `Failed` and continue, and count `transitioned`; list again with the same filter and record each non-terminal id not already in `Failed` in `Remaining`; return `ErrDrainIncomplete` wrapped with both counts when either slice is non-empty. A listing error returns the wrapped listing error and the partial result.
4. The doc comment of `DrainSessions` states three things: a nil error means every scoped row is terminal on the primary; a nil error does not mean the scope's pods are released, with the release outcomes REC-1 item 6 lists; and the verifying listing is a fence invariant check that can report a session legitimately created after a concurrent tenant resume.
5. **IMPLEMENTOR'S CHOICE:** the file in package `admin` that holds `DrainSessions` — it must be one function in package `admin` with no second drain anywhere in the tree.

### CODE-4 · Suspend handler: locked newness, detached audit, repeat drain, and reported failures

**Targets:** `pkg/gateway/externalapi/admin/tenant_suspend.go` `handleSuspendTenant`, `tenantSuspendPayload`, and `newTenantSuspendPayload`; `pkg/gateway/externalapi/admin/tenant_suspend_test.go`.

1. Keep the tombstone check and the `r.tenants.Get` pre-read for the `404` and tombstone answers.
2. Decide newness under the tenant row lock. Call `r.tenants.Update` with a mutate closure that returns a package sentinel `errAlreadySuspended` and writes nothing when `t.Suspended` is already true, and otherwise sets `Suspended`, `SuspendedReason`, `SuspendedAt`, and `SuspendedBy` as today.
3. Set `dctx := context.WithoutCancel(req.Context())`. It keeps the request values the audit emit reads.
4. Emit `tenant.suspended` on `dctx` before the drain with detail `{tenant_id, reason, already_suspended}`, where `already_suspended` is true when `Update` returned `errAlreadySuspended`. The detail carries no session count. When `Update` returns `errAlreadySuspended`, take the payload row from the pre-read.
5. When the session seams are wired, run `res, err := DrainSessions(dctx, r.sessions, r.sessionAdmin, id, "")` in both cases.
   - `errors.Is(err, ErrDrainIncomplete)`: answer `200` with the payload, `drainedSessions: res.Terminated`, and `failedSessions` set to `res.Failed` followed by `res.Remaining`.
   - Any other non-nil `err` (a listing failure): answer `500 INTERNAL_ERROR` with details `{drainedSessions: res.Terminated}`.
   - Nil: answer `200` with `drainedSessions: res.Terminated` and an empty `failedSessions`.
   When the seams are unwired, answer `200` with `drainedSessions: 0` and an empty `failedSessions`.
6. Add a `FailedSessions []string` field with the JSON name `failedSessions` to `tenantSuspendPayload`, serialized as an empty array when every session reached a terminal state.
7. Rewrite the handler doc comment: a repeat call re-drains, and every call writes one audit row whose `already_suspended` records the newness decided under the row lock. Cite `// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain)`.

### CODE-5 · Tenant deletion: wire the Phase 2 terminator and keep a failed drain in phase

**Targets:** `cmd/lenny-gateway/adminrouter.go` (the `tenantdeletion.Reconciler` construction); `cmd/lenny-gateway/tenantdeletion.go` (new `tenantSessionTerminator`); `cmd/lenny-gateway/flags.go` (`--lifecycle-tick-budget`); `pkg/controller/tenantdeletion/controller.go` (the seam comment on `Terminator`, `ReconcileTenant`, new `pauseDrain`, the `PhaseTerminateSessions` case of `runPhase`, and the doc comment on `fail`); `pkg/controller/tenantdeletion/lifecycle.go` (the `BlockedReason` doc comment, which gains the Phase 2 pause).

1. Add `tenantSessionTerminator{sessions, term, budget}` in `cmd/lenny-gateway/tenantdeletion.go`, implementing `tenantdeletion.SessionTerminator`. `TerminateTenantSessions(ctx, tenantID)` calls `admin.DrainSessions(tctx, sessions, term, tenantID, "")` under `tctx := context.WithTimeout(ctx, budget)` and returns its error, so a budget expiry is a drain error that item 4 pauses. `budget` is a new operator-tunable flag, `--lifecycle-tick-budget` (environment `LENNY_LIFECYCLE_TICK_BUDGET`, default 10 minutes).
2. Assign `Terminator: tenantSessionTerminator{sessions: w.sessions, term: sessionAdminAdapter{store: w.sessions, onTerminal: sessionSrv.OnSessionTerminal, beginTerminal: sessionSrv.BeginTerminal}, budget: *lifecycleTickBudget}` in the reconciler construction.
3. Rewrite the seam comment so `Terminator` is required alongside `Eraser` and `Receipts`. `Revoker` stays optional; Phase 3 belongs to P5b (piece B).
4. In `runPhase`, `PhaseTerminateSessions`: a nil `Terminator` returns a permanent misconfiguration error, which `ReconcileTenant` fails. A `TerminateTenantSessions` error is returned wrapped in a new unexported sentinel `errSessionsNotDrained`. `ReconcileTenant` matches it beside `ErrEscrowRegionUnresolvable` and calls a new `pauseDrain`, which writes the error text (the failed and remaining session ids) to `BlockedReason`, sets `UpdatedAt`, logs at WARN with the tenant id, and returns nil, so `ReconcileAll` continues and the next tick re-runs the drain. `pauseDrain` writes neither `Phase` nor `State`. `State` must stay `disabling`, because `syncBack` copies it onto the tenant row and `ensureJob` resumes a `deleting` tenant at Phase 3; do not model the pause on `pauseEscrowUnresolvable` or `block`, which write `deleting`. The advance after a successful drain already clears `BlockedReason`.
5. Correct the doc comment on `fail`: a failed job is terminal for the controller, and no production code path retries it. Cite `// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)`.

### CODE-6 · Durable erasure job store with transactional start, restart, and completion

**Targets:** `pkg/gateway/storage/erasurejob/erasurejob.go` (`Store`, `Job`, `Memory`); `pkg/gateway/storage/erasurejob/pgstore/` (new package); `pkg/gateway/storage/erasurejob/sampler.go`; `pkg/gateway/environment/userstore/pgstore/pgstore.go` (new `SetErasureRestrictionTx`, and `ClearProcessingRestriction` made the transaction-scoped `ClearProcessingRestrictionTx`); `pkg/gateway/environment/userstore/userstore.go` (`ClearProcessingRestriction` removed from `Store`, `ErrRestrictionHeldByOtherJob`, and `Memory` parity); `cmd/lenny-gateway/adminrouter.go` (store construction); `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go` (new `SetErasureJobsActive`); `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_erasure.go` (the `lenny_erasure_jobs_active` help text drops "on this replica", because every replica publishes the cluster-wide count).

1. Replace the `Store` interface with:

   ```go
   // SameAttempt reports whether stored is still the job read was taken
   // from: equal Phase and equal len(PhaseLog). The PhaseLog length refuses a
   // writer that read `initiated` before a `fail` and a `Restart`, which a
   // Phase comparison alone admits.
   func SameAttempt(stored, read Job) bool

   type Store interface {
       // Start applies row E1's admitted job write and processing_restricted
       // write in one transaction (Erasure entry-point table). For row E1's
       // in-progress case it writes nothing and returns *ErrActiveJob carrying
       // the in-progress job.
       Start(ctx context.Context, j Job) (Job, error)
       Get(ctx context.Context, id string) (Job, error)
       // Update applies mutate to the job as read inside the write's own transaction (the row locked FOR UPDATE on Postgres) and writes nothing when mutate returns an error.
       Update(ctx context.Context, id string, mutate func(*Job) error) (Job, error)
       // Restart applies row E3's admitted job write and processing_restricted
       // write in one transaction (Erasure entry-point table). Returns
       // *ErrActiveJob when another job for the user is in progress, and
       // ErrNotFailed when the job is not `failed`.
       Restart(ctx context.Context, id string) (Job, error)
       // ClearRestriction is the operator clear of row E5 of the Erasure
       // entry-point table. It returns ErrNotFailed for row E5's
       // `failed`-phase refusal and userstore.ErrRestrictionHeldByOtherJob for
       // its other-job refusal.
       ClearRestriction(ctx context.Context, id string) (Job, error)
       // Complete marks the job `completed` at completedAt and clears the
       // restriction WHERE erasure_job_id = id, in one transaction. It returns
       // ErrJobMoved and writes nothing unless SameAttempt(stored, expect).
       Complete(ctx context.Context, id string, completedAt time.Time, expect Job) (Job, error)
       // ListActive returns every job in a non-terminal phase.
       ListActive(ctx context.Context) ([]Job, error)
   }
   ```

   `Create` and `List` are removed and every caller is updated.
2. Add to `Job`: `InitiatedBy string`, `OverrideReceipt map[string]any`, `ErasedAt time.Time`, and `ReceiptAt time.Time`. `ErasedAt` is set as CODE-7 item 2 states. `ReceiptAt` is the completion instant chosen once before the receipt (CODE-7 item 3).
3. Postgres store (`erasurejob/pgstore`): rows live in `erasure_jobs`. `id`, `tenant_id`, `user_id`, `status`, `total`, `failure`, `started_at`, and `completed_at` map to their columns. `Deadline`, `Deleted`, `Billing`, `DeadLetterRedacted`, `PhaseLog`, `InitiatedBy`, `OverrideReceipt`, `ErasedAt`, and `ReceiptAt` are serialized into the `detail` jsonb column (MIG-1). `Get` and `ListActive` run under the existing all-tenants read pattern on the primary. `Start` inserts with `INSERT ... ON CONFLICT (tenant_id, user_id) WHERE status IN ('initiated','store_deleting','pseudonymizing','verifying') DO NOTHING RETURNING id`; when no row is returned, it `SELECT`s the existing in-progress job in the same transaction and returns `*ErrActiveJob{Job}` without touching the users row. `Restart` locks the job row `FOR UPDATE` and updates it with no in-progress guard. A unique violation on `idx_erasure_jobs_in_progress_per_user` (MIG-1) aborts the transaction, so `Restart` rolls back, reads the in-progress job in a new transaction, and returns `*ErrActiveJob{Job}`.
4. Every users write in `Start`, `Restart`, and `Complete` goes through a new transaction-scoped helper in the userstore Postgres store: `SetErasureRestrictionTx(ctx, tx pgx.Tx, tenantID, subject, jobID string, restricted bool, requireJobID string) (rows int64, err error)`. It selects the row `FOR UPDATE`, applies `pgtenant.MonotonicNext` to `updated_at`, increments `version`, and writes `processing_restricted` and `erasure_job_id`. When `requireJobID` is non-empty, it writes only if `erasure_job_id` equals it and returns 0 rows otherwise. A missing user row in `Start` aborts the transaction with `userstore.ErrNotFound`. `ClearProcessingRestriction` becomes the transaction-scoped `ClearProcessingRestrictionTx(ctx, tx, tenantID, subject, jobID)` and leaves `userstore.Store`. It keeps setting its session-local bypass, returns a new `userstore.ErrRestrictionHeldByOtherJob` without writing when the locked row fails row E5's flag condition, and otherwise calls the same helper, so one users write path remains. `ClearRestriction` selects the job row `FOR UPDATE`, makes row E5's phase check, and then calls it in the same transaction. Locking the job row first, as `Restart` does, keeps a concurrent retry from restarting a job whose flag the clear then empties. The erasurejob store does not hand-write a users `UPDATE`.
5. `Complete` locks the job row `FOR UPDATE`, compares it with `expect`, marks it `completed`, and then calls the helper with `restricted=false` and `requireJobID=id`, in the same transaction. A job whose restriction was already cleared by the operator endpoint or replaced by a newer job writes 0 users rows and still completes.
6. `Memory` keeps parity through an injected restriction writer with the same semantics (`WithRestrictionWriter`), implemented over `userstore.Memory`. It enforces the single in-progress job per (tenant, user).
7. Sampler: `Sampler.Sample` calls `ListActive`, publishes the age of each returned job, sets `lenny_erasure_jobs_active` to the number of returned jobs through a new `AgeSink.SetErasureJobsActive`, keeps the set of `(tenantID, jobID)` it published in the previous round, calls `ClearErasureJobAge` for every previously published job absent from the current result, and then replaces the set.
8. `cmd/lenny-gateway/adminrouter.go` builds the Postgres store when the Postgres pool is configured and `Memory` otherwise.
9. Cite `// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)), §12.8 (Erasure job phase field (crash recovery))`.

### CODE-7 · Erasure runner: drain step, phase dispatch, receipt before completion, and the leader-gated erasure sweep

**Targets:** `pkg/gateway/storage/erasurejob/runner.go` (`Start`, `Run`, `fail`, new `WithSessionDrain`, new `WithHoldCheck`, new `WithCompletionReceipt`, new `RunPending`, new `Poll`, new `WithMaxConcurrentJobs`); `pkg/gateway/externalapi/admin/erasure.go` (`ErasureRunner`, `handleEraseUser`, `runErasureToCompletion`, `handleRetryErasureJob`, `handleClearErasureRestriction`); `pkg/gateway/externalapi/admin/legalhold.go` (`heldResourcesForUser`); `cmd/lenny-gateway/tenantdeletion.go` (`tenantDeletionRunner.Start`, `tick`, and `advisoryLockKey`); `cmd/lenny-gateway/adminrouter.go` (the erasure `Sessions` lister and the runner start); `cmd/lenny-gateway/leader_sweeps.go` (`startLeaderElectedSweeps` and new `addErasureSweep`); `cmd/lenny-gateway/wiring_fields.go` (`gatewayWiringFields`); `cmd/lenny-gateway/flags.go` (`--erasure-max-concurrent-jobs`); `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go` (delete `IncErasureJobsActive` and `DecErasureJobsActive`).

1. `Run` dispatches on the persisted `job.Phase`:
   - `initiated`: run the memory preflight, then the session drain hook (`WithSessionDrain(func(ctx, tenantID, userID string) error)`), then the legal-hold re-check hook (`WithHoldCheck`, item 7), then advance to `store_deleting`. A runner with no drain hook or no hold-check hook counts the missing hook as that hook's failure. The outcomes are row E6 of the **Erasure entry-point table**.
   - `store_deleting`: run `DeleteByUser` and the dead-letter redaction. Merge each store's count into `j.Deleted` with a per-store maximum, so a re-run that deletes nothing never lowers a recorded count, and recompute `Total` from the merged map. Then advance to `pseudonymizing`.
   - `pseudonymizing`: when `job.Billing.Disposition` is already recorded, skip `Pseudonymize`. Otherwise run it and persist its outcome in the same `Update` that advances to `verifying`, or, for an exempt tenant, that records the erasers done.
   - `verifying`: re-run `Verify` only.
   A resumed job never re-runs a step of an earlier phase. Each phase advance is one `Update`. Every `Update` the runner makes, including `fail`'s and item 3's, goes through one helper whose mutate returns the sentinel `ErrJobMoved` when `SameAttempt` (both CODE-6 item 1) fails between the stored job and the job the runner last read or wrote (Edit 2h). On `ErrJobMoved` the runner stops work on the job for this tick and calls neither `fail` nor the failure observer.
2. Set `ErasedAt` in the `Update` that records the last step's success: a passing `Verify` in `verifying`, the exempt billing outcome in `pseudonymizing`, or the end of `store_deleting` when no `BillingEraser` is wired. When `Run` or `RunPending` finds `ErasedAt` set, it skips straight to the receipt and `Complete`.
3. Completion order: run an `Update` whose mutate sets `ReceiptAt = now` only when it is zero, and build the `gdpr.erasure_completed` receipt from the `Job` that `Update` returns (counts, billing outcome, phase log with a final `{phase: completed, at: ReceiptAt}` entry, `InitiatedBy`, and `OverrideReceipt`) and write it through `WithCompletionReceipt(func(ctx, Job) error)`. Only after the receipt write returns nil, call `store.Complete(ctx, id, ReceiptAt, j)` with the `Job` that `Update` returned; its `ErrJobMoved` takes item 1's outcome. A receipt error, or a runner with no receipt hook, leaves the job in progress for the next tick. A crash or error between the receipt and `Complete` makes the next tick write a byte-identical receipt. `runErasureToCompletion` becomes the receipt builder over `Job`, and its separate restriction clear is deleted.
4. Add `RunPending(ctx)`: list `ListActive` and, oldest `StartedAt` first, run in its own goroutine each listed job whose tenant has no run in flight on this runner, while fewer runs than the runner's limit are in flight, and return when those runs return. A listed job that either condition holds back waits for a later call. A mutex-guarded map on the runner holds each running job's ID and tenant from before its goroutine starts until its `Run` returns, and its size is the in-flight count across overlapping calls. `WithMaxConcurrentJobs(n)` sets the limit, and an unset limit is 1. `adminrouter.go` sets it from a new operator-tunable flag, `--erasure-max-concurrent-jobs` (environment `LENNY_ERASURE_MAX_CONCURRENT_JOBS`, default 2). The gateway refuses to start when the flag is below 1, or, with the Postgres pool configured, when the flag is not below the pool's `MaxConns` (D16). `Run` logs the elapsed drain time. When `ctx` has ended, `fail` returns its cause without writing, calling the failure observer, or observing a duration, so the job stays in its persisted phase and a later `RunPending` resumes it (row E4). The job's SLA deadline (`StartedAt + Deadline`) bounds nothing and only raises `ErasureJobOverdue`. Its doc comment states that the gateway-leader gate does not guarantee a single executor (item 1). `Run` observes `lenny_erasure_job_duration_seconds` only after the write that moves the job to `completed` (a `Complete` that returns nil) or to `failed` (a `fail` whose `Update` applied, so an `ErrJobMoved` records nothing). It no longer touches `lenny_erasure_jobs_active`, which the sampler sets (CODE-6 item 7). `LifecycleMetrics` keeps only `ObserveErasureJobDuration`, and `gatewaymetrics` deletes `IncErasureJobsActive` and `DecErasureJobsActive`.
5. Add `Runner.Poll(ctx, interval)`, which starts `RunPending` in a new goroutine at once and then every `interval` until `ctx` ends. `adminrouter.go` passes the erasure runner to `startLeaderElectedSweeps` on a `gatewayWiringFields` field, and when the field is non-nil that function calls a new `addErasureSweep(leaderGate, runner, interval)`, which registers `Poll` with `Gate.Add` at `--tenant-deletion-interval`. `tenantDeletionRunner`: `tick` runs `reconcileOnce` only while the `gatewayleader.Elector` that `startLeaderElectedSweeps` builds reports `IsLeader`, in place of `pg_try_advisory_lock`; delete the `pool` field, `advisoryLockKey`, and the comments that name the lock. `adminrouter.go` keeps the tenant-deletion runner's `Receipts` start gate and passes it to `startLeaderElectedSweeps` on a `gatewayWiringFields` field, and that function sets the runner's elector and launches `Start`. Do not register `tenantDeletionRunner.Start` with `leaderGate.Add`: a lease loss cancels the leader context, which fails the replica's in-memory tenant-deletion job.
6. `handleEraseUser` implements rows E1 and E2 of the **Erasure entry-point table**: it calls `Runner.Start` and answers `*ErrActiveJob`, any other `Start` error, and success with rows E1's and E2's cells. `Runner.Start(ctx, tenantID, userID, initiatedBy string, overrideReceipt map[string]any) (Job, error)` sets `InitiatedBy` and `OverrideReceipt` on the `Job` it builds today and returns `store.Start`'s result; `admin.ErasureRunner` becomes that one method, and its callers and `erasureRunnerStub` (`pkg/gateway/externalapi/openapi/routestub_test.go`) follow it. Delete the goroutine and the separate `users.Update` call.
7. Legal-hold enumeration and the retry handler, rows E3 and E6:
   - Replace `Router.heldResourcesForUser` with an exported function in package `admin`, `HeldResourcesForUser(ctx, sessions DrainLister, artifacts ArtifactHoldChecker, tenantID, userID string) ([]HeldResource, error)`, which lists with `ListFilter{UserID: userID, Primary: true}` and keeps the existing session and artifact checks. `ArtifactHoldChecker` is a new one-method consumer interface holding `IsLegalHeldAt`, and `HeldResource` is the exported form of `heldResource`. Both handlers call the function.
   - Extract the block branch of `handleEraseUser` into a helper both handlers call. It takes the job id, which is empty on the erase path, and adds `jobId` to `gdpr.erasure_blocked_by_hold` when the id is non-empty.
   - `handleRetryErasureJob` runs row E3 in the order the order paragraph gives. It calls `HeldResourcesForUser` with no override, answers a hold through the block helper without calling `Restart`, and answers each sentinel of CODE-6 item 1's `Restart` comment with row E3's cells. Delete the goroutine.
   - Add `Runner.WithHoldCheck(func(ctx context.Context, tenantID, userID string) ([]erasurejob.Hold, error))`, where `Hold` carries `ResourceType` and `ResourceID`. In `initiated`, after the drain, the runner calls it and applies row E6's exemption and failure outcome, through `fail` with a `Failure` text naming the holds or the error. The doc comment cites `// spec: §12.8 (DeleteByUser dependency-ordered deletion sequence)` and states that a hold set after the advance to `store_deleting` is not re-checked.
8. `adminrouter.go`: the erasure `Sessions` lister calls `List(ctx, tenantID, ListFilter{UserID: userID, Primary: true})`, stops filtering the tenant in Go, and appends `ReservedSessionIDs(ctx, tenantID, userID)`, so the session-scoped erasers reach every reserved ID (the **Creation-path table** column rule **Reservation**). Wire `WithSessionDrain` to `admin.DrainSessions` over the session store and the `sessionAdminAdapter`, and wire `WithHoldCheck` to `admin.HeldResourcesForUser` over the same session store and the artifact-hold checker the admin router uses. Wire `WithCompletionReceipt` to a function that appends item 3's receipt as `gdpr.erasure_completed` on the job's tenant chain through the gateway's `policy.AuditAppender` and returns `Append`'s error, in the payload layout `ChainAuditSink.EmitAdminEvent` writes (the receipt under `detail` and the user as `target_resource`), rather than through `Router.emit`, whose `AuditSink` drops the error.
9. Cite `// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)), §12.8 (Erasure job phase field (crash recovery)), §24.12 (Erasure Job Management)`.
10. `handleClearErasureRestriction` implements row E5 in the order its order paragraph gives: it calls `store.ClearRestriction` with the job's ID and answers each sentinel of CODE-6 item 1's `ClearRestriction` comment with row E5's refusal cells. It does not decide the phase from its own `Get`, which runs outside the clear's lock.

### CODE-8 · Remove `DELETE /v1/responses/{id}`

**Targets:** `pkg/gateway/environment/translator/open_responses.go` (the `DELETE /v1/responses/{id}` route registration and `handleDelete`); `pkg/gateway/externalapi/openapi/openapi.json` (`paths./v1/responses/{id}.delete`); `pkg/gateway/environment/translator/open_responses_test.go` (`TestOpenResponsesDelete`); `tests/tier4_integration/openai_cross_tenant_test.go`.

1. Delete the route registration, `handleDelete`, and the openapi `delete` operation. `sessionstore.Delete` stays because other callers use it. No spec edit accompanies this deliverable.
2. `TestOpenResponsesDelete` asserts `405` with an `Allow` header containing `GET`, and that `store.Get` still returns the row. Annotate it `// spec: §15 (Built-in adapter single-shot compute model)`.
3. In the tier-4 cross-tenant test, remove the cross-tenant `DELETE` block, including its `errorType == "not_found_error"` assertion, because ServeMux answers `405` with a plain-text body. Replace it with one owner-tenant `DELETE` that asserts `405`, keep the owner `GET` 200 survival check, and rewrite the function doc comment and the `// diagnosis:` comment so neither describes a `DELETE` rejected with the not-found isolation error.

## Staged schema, chart, and migration changes

### MIG-1 · Migration 0186: `erasure_jobs.detail` and one in-progress job per user

**Targets:** `migrations/0186_erasure_jobs_detail.up.sql`; `migrations/0186_erasure_jobs_detail.down.sql`; `tests/tier2_component/migrations/prod_columns_test.go`. The number 0186 is provisional and moves to the next free number if another migration lands first.

```sql
-- 0186_erasure_jobs_detail.up.sql
-- §12.8 erasure job record: the job fields no query filters on, and at
-- most one in-progress job per user. The index predicate must stay
-- identical to the erasure_jobs_status_check in-progress values and to
-- the lenny_processing_restriction_guard trigger's in-progress set (0042).
ALTER TABLE erasure_jobs ADD COLUMN detail JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE UNIQUE INDEX idx_erasure_jobs_in_progress_per_user
    ON erasure_jobs (tenant_id, user_id)
    WHERE status IN ('initiated', 'store_deleting', 'pseudonymizing', 'verifying');
```

```sql
-- 0186_erasure_jobs_detail.down.sql
DROP INDEX IF EXISTS idx_erasure_jobs_in_progress_per_user;
ALTER TABLE erasure_jobs DROP COLUMN IF EXISTS detail;
```

Register `{migration: "0186", table: "erasure_jobs", columns: []string{"detail"}}` in `prod_columns_test.go`. Without a test that names 0186 under `tests/tier2_component/migrations/`, `scripts/lint-migrations.sh` rule 3 fails tier 0.

### MIG-2 · Migration 0187: `session_reservations`

**Targets:** `migrations/0187_session_reservations.up.sql`; `migrations/0187_session_reservations.down.sql`; `tests/tier2_component/migrations/session_reservations_test.go`. The number 0187 is provisional and follows MIG-1's, and checklist step S6 lands it after S5 lands MIG-1.

```sql
-- 0187_session_reservations.up.sql
-- §12.8 Lifecycle fence and drain: a session ID reserved for its owner
-- before a creation path writes data keyed to it, so that erasure of
-- the owner reaches the data when no session row is ever inserted.
CREATE TABLE session_reservations (
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    session_id UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, session_id)
);
CREATE INDEX idx_session_reservations_user ON session_reservations (tenant_id, user_id);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_reservations
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_reservations
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );

GRANT SELECT, INSERT, DELETE ON session_reservations TO lenny_app;
```

```sql
-- 0187_session_reservations.down.sql
DROP TABLE IF EXISTS session_reservations;
```

## Staged docs changes

### DOCS-1 · Reader documentation for the fence, the drain, the erasure job, and the suspend response

**Targets:** `docs/operator-guide/multi-tenancy.md`; `docs/reference/error-catalog.md`; `docs/api/index.md`; `docs/api/mcp.md`; `docs/api/rest.md` (optional); `docs/operator-guide/security.md`; `docs/api/admin.md`.

1. `docs/operator-guide/multi-tenancy.md`, tenant deletion phase table: leave the Phase 1 row unchanged. Replace the Actions cell of the `2. Drain active sessions` row with: "Force-terminate every active session in the tenant. The phase completes when no active session remains. When a session cannot be terminated, the phase stays incomplete and the controller retries it on its next pass." Do not describe locks or atomicity. Leave the page's existing phase count alone.
2. Error tables: add `TENANT_SUSPENDED` (403), `TENANT_NOT_ACTIVE` (403, `details.tenantId` and `details.state`), and `USER_INVALIDATED` (403, not retryable) as session-creation rejections, worded after the §15.1 catalog rows:
   - `docs/reference/error-catalog.md`, beside `ERASURE_IN_PROGRESS`, and reword its `ERASURE_IN_PROGRESS` row after the §15.1 catalog row as Edit 4k leaves it;
   - `docs/api/index.md`, beside `ERASURE_IN_PROGRESS`, with category `POLICY` and retryable `No`, and reword its `ERASURE_IN_PROGRESS` row after the §15.1 catalog row as Edit 4k leaves it;
   - `docs/api/mcp.md`, in the `create_session` error table;
   - optionally the key error codes list in `docs/api/rest.md`.
3. `docs/operator-guide/security.md`, **Erasure Process**: add one sentence stating that before any data is deleted, the erasure job force-terminates the user's running sessions, and that a failure to terminate them fails the job and leaves the restriction set.
4. `docs/api/admin.md`: under `POST /v1/admin/users/{userId}/erase`, state that a request for a user with an erasure job already in progress returns that job's ID and starts no second job when the user has no active legal hold, and returns `409 INVALID_STATE_TRANSITION` when the request overrides a legal hold. On the erasure-job retry row, state that it returns `409 INVALID_STATE_TRANSITION` when the user already has another job in progress, and `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` when a legal hold on the user's sessions or artifacts blocks it. On the clear-processing-restriction row, state that it accepts only a failed job and returns `409 INVALID_STATE_TRANSITION` while the user's restriction belongs to another erasure job.
5. `docs/api/admin.md`, Tenants section: add a `POST /v1/admin/tenants/{id}/suspend` entry. It drains active sessions; a repeat request on a suspended tenant re-runs the drain; the response reports `drainedSessions` and `failedSessions`, and a non-empty `failedSessions` means the drain is incomplete and the request can be repeated; a listing failure returns `500 INTERNAL_ERROR`.
6. Apply `doc-style.md` and `doc-content.md`. Quote no log line and cite no spec section number in prose. `docs/api/open-responses.md` documents no `DELETE`, so CODE-8 needs no docs edit.

### REC-1 · Records: findings, the remediation-plan bullet, and the claim register

**Targets:** `BUILD-GAPS.md`; `gateway-runtime-comms-remediation.md` (the P5 bullet); `scripts/seed-claim-register.py` and the generated `tests/claim-map.json` (only if a claim row is added).

1. Close, with a resolution naming 0094: F-12.8.26, F-12.8.27, F-12.8.28, F-12.8.31, F-12.8.32, F-12.8.33, F-15.1.48, F-15.1.50, F-15.1.51, and F-12.8.36.
2. Reopen F-12.8.20 (the in-memory erasure job store with no writer of `erasure_jobs`) and close it under 0094 with a resolution.
3. File N-1 (the Phase 2 terminator is unwired) and N-5 (the completion clear ignores its error and is not atomic with the receipt) as findings closed by 0094.
4. Keep F-11.7.22 and F-11.4.14 OPEN and annotate them. F-11.7.22: the `tenant.suspended` half landed in 0094, and the `session.force_terminated` half stays with P2. F-11.4.14: 0094's creation fence covers the concurrent-insert case, and the read-pool single listing stays with P5b.
5. File N-3 and the `coordination_lease` mirror gap under P5b. Keep F-11.4.12 and F-11.4.13 OPEN and annotate them as owned by P5b.
6. File an OPEN finding owned by P2 for the terminal-tail residual that SPEC-1 *Drain* accepts. The pod of a drained session bound on another replica is released by that replica's coordination Sweeper through `ReleaseTerminalBinding` when the Sweeper lists the terminal row, within the Sweeper interval plus `maxWorkspaceSealDurationSeconds`. User erasure deletes the session rows in the same runner pass, usually before that listing. That replica then never releases the pod or evicts its binding: orphan-GC predicate 1 drains the pod after `claimOrphanTimeout`, counted in `lenny_orphaned_claims_total`, and does not fire while another active session references the pod.
7. File an OPEN finding, unassigned, for the §12.8 Phase 1 API-key issuance and user sign-up rejection: `POST /v1/oauth/token` mints, including the playground mint, are not refused for a tenant whose `TenantState` is not `active`.
8. No claim-register row is added for a duplicate receipt, because SPEC-2 makes it specified behavior. When the owner wants a claim row for the terminal-tail residual, add it to `EXPLICIT` in `scripts/seed-claim-register.py` with a spec anchor that resolves in `spec/28_communication-channels.md` and a `deferral_id` naming an R step the plan declares, then regenerate `tests/claim-map.json` with the script. Never hand-edit `tests/claim-map.json`.
9. Update the P5 bullet of `gateway-runtime-comms-remediation.md`: 0094 covers pieces A and D; pieces B and C and the full_revoke fixes go to the follow-on proposal P5b, which lands after P1 (0093); replace "waits for their terminal hooks" with the P2-owned residual of item 6.
10. File an OPEN finding, unassigned, for the legal-hold preflight's coverage of §12.8 step 0: the enumeration skips the audit-range and workspace-snapshot holds of the legal-hold ledger, the `gdpr.erasure_blocked_by_hold` tuples omit `holdSetAt`, `holdSetBy`, and `note`, and the `409` details omit `userId` and `holdCount`. SPEC-2 Edit 2j leaves step 0's hold list unchanged and Edit 4f leaves the §16.7 tuple list unchanged, so the finding records a code divergence from text this proposal keeps.
11. File an OPEN finding, unassigned, for the reservation residual that non-spec Edge case 4 states.
12. Annotate N-3 under P5b: the checkpoint manifest eraser takes its session IDs from the erasure lister, which includes reserved IDs (CODE-7 item 8), so a crashed derive's manifest row is reached once that eraser is wired.
13. File an OPEN finding, unassigned, unless one exists, for §12.8 **Data residency** item 2: the session, tenant, and user stores do not route a residency tenant's Postgres writes to the region-local endpoint, because `cmd/lenny-gateway/stores.go` builds all three over one pool. When that routing is built, it moves the tenant's row, its users' rows, and its session rows together, as SPEC-1 *Creation fence* requires.
14. File an OPEN finding, unassigned, for `ERASURE_JOB_NOT_FAILED`: rows E3 and E5 answer it and the §15.1 **Error code catalog** has no row for it.
15. File an OPEN finding, unassigned, for `saltlockpg.Lock.WithSaltLock`: it takes and releases a session-level `pg_advisory_lock` in two statements on a pooled connection, which the §12.3 transaction-mode pooler can route to different backends, so the lock can leak and every later pseudonymize of the tenant waits on it while holding one erasure runner slot (CODE-7 item 4).

## Testing

Tests are stated as intents. Each intent is pinned by at least one test at each tier it names and carries a `// spec:` annotation naming the sections it exercises, such as `// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)`. Every tier-2-and-higher test carries a `// diagnosis:` comment above the function stating what a failure means. Each intent listed under a deliverable lands in that deliverable's checklist step, together with every update to an existing test that the deliverable's code breaks.

**Failures the tests find.** The owner's triage rule applies to every failure a test exposes during implementation. Fix in this proposal a failure that is relevant to it, meaning the faulty code is a line this proposal changes or the defect stops one of its intents from holding, and a failure whose fix is straightforward and not owned elsewhere, meaning it changes no spec text, schema, migration, flag, or chart value and stays within one package. File everything else: add a `BUILD-GAPS.md` finding with the next free identifier, skip only the failing case with a `t.Skip` reason that opens with `blocked:` and the finding id, and record a deviation entry naming the intent, the finding, and the owner. Run the environment preflight of `.claude/rules/test-coverage.md` before reading a failure above tier 1 as a defect.

### Tests that land with CODE-1

- **T1.1 (tier 1).** A table over the full cross-product of `CreationMarkers` fields, including root insert and child insert, asserts `Refusal` returns the code and details of the first matching CODE-1 rule. A case per `tenantstore` state constant asserts that `Refusal` admits exactly the states `tenantstore.Tenant.AcceptsNewWork` admits. For a soft-disabled owner, `memstore.Create` refuses a row with `ParentSessionID` set and `DelegationDepth` 0 (C8 to C10) and admits a row with `DelegationDepth` 1 (C11).
- **T1.2 (tier 1).** `memstore.Create` with `WithCreationMarkers` refuses with `*CreationRefusal` for each rule, admits a row with no `UserID` under user markers, and returns a marker-reader error without inserting. Every T1.6 case also runs against `memstore` with `WithCreationMarkers`.
- **T1.3 (tier 2).** Against real Postgres, `pgstore.Create` refuses for each marker, inserts nothing on refusal, fails closed when the user read errors (injected through a revoked grant or a closed transaction), and refuses a derive-failure row created terminal.
- **T1.4 (tier 2, discriminating).** Lock conflict: a transaction holding `FOR UPDATE` on the tenant row (through `tenantstore.Update` with a blocking mutate) blocks `pgstore.Create` until it commits, and the create then observes the new marker and refuses. Repeat for `userstore.Update`, `userstore.SoftDelete`, and `tenantstore.SoftDelete`. The `SoftDelete` cases are the ones that distinguish `FOR SHARE` from `FOR KEY SHARE`.
- **T1.5 (tier 7a).** Randomized interleavings under `-race` of concurrent creates against marker writes and a drain over both stores. The invariant checker asserts that after the marker commit and one drain, no non-terminal row of the scope exists and every refused create inserted nothing.
- **T1.6 (tier 2).** Against real Postgres, `CreateReserved` refuses with `ERASURE_IN_PROGRESS` and inserts nothing after `DeleteByUser` removed its reservation, and inserts and removes the reservation otherwise. `Reserve` refuses under each marker and records nothing, serializes with `userstore.Update` and `tenantstore.Update` as T1.4 does for `Create`, and records the ID when admitted. `Create` deletes the reservation whose ID and owner match and keeps one whose owner differs. `DeleteByUser` and `DeleteByTenant` delete reservations with the session rows. `ReservedSessionIDs` returns a reservation the read pool has not seen. `Reserve` with an empty owner records nothing, and `CreateReserved` of a row with no `UserID` then inserts it; this case is the one that distinguishes the owner predicate of the **Reservation** column rule.

### Tests that land with CODE-2

- **T2.1 (tier 1).** One case per row of the **Creation-path table** except C5, driven by a fake store whose `Create` returns `*CreationRefusal`, asserts the row's **Refusal surface** and **Derive-audit outcome** cells and that each undo in its **Side effects** cell ran. For C8 and C11 the refusal also leaves no object, no manifest row, and no reservation under the session ID, and a non-refusal `Create` error leaves the objects and the reservation. Without the mapping the same case yields a 5xx on C1, C3, C6 to C8, and C10. On C8 and C11, an undo whose deletion errors, or whose deleter is absent (a blob store without `DeleteBySession`, an unwired `SessionObjects`), still writes the refusal and leaves the reservation, as the rows' **Side effects** cells state.
- **T2.2 (tier 1).** For every combination of tenant markers and owner markers, `requireCreationAllowed` answers the code `Refusal` answers, and on C1 and C3 it refuses before any claim and before the rate counter moves. A caller creating with the `userId` of a restricted user is refused, and a restricted caller replaying an active user's session (C10) is refused by `requireActiveUser` alone. A tenant or user read error gives the **Gates** column-rule outcome before any claim and before the rate counter moves.
- **T2.3 (tier 1).** `requireActiveDelegator` admits a child of a soft-disabled user, refuses a child of a hard-disabled user, and refuses a child under each tenant marker and under `processing_restricted`. On C8 and C11, a `Reserve` refusal writes or copies nothing. A non-refusal `Reserve` error on C8 and C11 takes the CODE-2 item 3 outcome and copies or exports nothing, and a read error in `requireActiveDelegator` gives the **Gates** column-rule outcome.
- **T2.4 (tier 1).** `errorclassify` rows: `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` classify as `{CategoryPolicy, false}`.
- **T2.5 (tier 3).** Add `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` to the matrix list in `tests/tier3_contract/rest_mcp_consistency/error_matrix_test.go`, each with a canonical trigger on `POST /v1/sessions` and MCP `create_session`, asserting identical code, category, retryable, and status.
- Retarget the existing `sessionserver` user-gate and tenant-state tests to `requireCreationAllowed`, because CODE-2 item 2 deletes `requireTenantState`.

### Tests that land with CODE-3

- **T3.1 (tier 1).** `DrainSessions` table over: empty scope; all terminal; a mix; a `ForceTerminate` error on one session (the others still terminate and the id lands in `Failed`); `ErrNotFound` on one session; a first-listing error; a verifying-listing error; a verifying listing that returns a new non-terminal row (the id lands in `Remaining`); and a parent context that ends during one `ForceTerminate` (that call's context is still live when the fake checks it afterwards, no later `ForceTerminate` runs, and the error wraps `context.DeadlineExceeded`). Each case asserts the result and whether the error wraps `ErrDrainIncomplete`. The fake lister records each filter, and every case asserts that each listing it recorded passed `ListFilter{UserID: userID, Primary: true}`.
- **T3.2 (tier 2).** With a read pool that has not seen a freshly inserted row (built with `sessionpg.WithReadPool` over a second pool, as the existing split-pool tests do), `List` with `Primary: true` returns the row and `List` without it does not.
- **T3.3 (tier 1).** `handleForceTerminateSession` maps an unknown id to `404` before calling `ForceTerminate`, and passes the row's tenant.

### Tests that land with CODE-4

- **T4.1 (tier 1).** First suspend: one marker write, one `tenant.suspended` row emitted before the drain, and a `200` body with `drainedSessions` and an empty `failedSessions`.
- **T4.2 (tier 1).** Repeat suspend: no marker write, one `tenant.suspended` row with `already_suspended: true` emitted before the drain, the drain runs again, and its count is reported.
- **T4.3 (tier 1).** Incomplete drain yields `200` with the failed and remaining ids in `failedSessions`; a listing failure yields `500 INTERNAL_ERROR` with the marker set.
- **T4.4 (tier 1).** A request context cancelled mid-drain still completes the audit emit and the drain.
- **T4.5 (tier 7a).** Two concurrent first-time suspends under `-race` produce exactly one marker write and two `tenant.suspended` rows, exactly one of them with `already_suspended: false`.
- **T4.6 (tier 3).** New `tests/tier3_contract/admin_tenant_suspend/suspend_response_test.go` drives the production admin router over httptest and asserts the CODE-4 items 5 and 6 responses on the wire; the discriminating assertion is that `failedSessions` encodes as `[]` rather than `null` on a complete drain.
- Retarget the existing idempotency and drain cases in `tenant_suspend_test.go`.

### Tests that land with CODE-5

- **T5.1 (tier 1).** A drain error leaves the job at `PhaseTerminateSessions` with `State` `disabling` and the error in `BlockedReason`. The `State` assertion is the one that discriminates. The next `ReconcileTenant` re-runs the drain and advances on success.
- **T5.2 (tier 1).** A nil `Terminator` fails the job at Phase 2.
- **T5.3 (tier 1, cmd).** After a failed Phase 2 drain, `syncBack` leaves the tenant row `disabling`. With a fresh job store standing in for a restart, `ensureJob` and the next ticks re-run the drain before any Phase 3 call. With the fixture's `Terminator` replaced by a `tenantSessionTerminator` whose budget is shorter than its fake `DrainTerminator`'s per-call delay (the fake must sleep rather than wait on its context, which `DrainSessions` detaches), one tick leaves a tenant with two non-terminal sessions at `PhaseTerminateSessions` with `State` `disabling` and advances a second tenant with no sessions past Phase 2.
- Retarget `TestLifecycleCompletesWithOnlyRequiredSeams` and `TestNilKMST4TenantCompletesWithoutDestroy` in `pkg/controller/tenantdeletion/legalhold_test.go` to wire a fake `Terminator`, rename the first to reflect that `Terminator` is required, and wire a `Terminator` in the `cmd/lenny-gateway/tenantdeletion_test.go` fixture.

### Tests that land with MIG-1

- **T6.1 (tier 2).** `TestErasureJobInProgressIndexMigration_spec_12_8`: a second in-progress row for the same `(tenant_id, user_id)` fails with `unique_violation`; a `completed` or `failed` row beside an in-progress row is admitted; the down migration removes both the index and the column.

### Tests that land with MIG-2

- **T6.2 (tier 2).** `TestSessionReservationsMigration_spec_12_8`: the row-level security policy hides another tenant's reservation, a read with `app.current_tenant` unset raises an error, `__all__` reads every tenant's reservation, a duplicate `(tenant_id, session_id)` fails, and the down migration drops the table.

### Tests that land with CODE-6

- **T7.1 (tier 2).** `Start` writes row E1's admitted job-write and `processing_restricted` cells; a forced failure of the users write leaves neither row; a second `Start` for the same user returns `*ErrActiveJob` carrying the first job and writes nothing.
- **T7.2 (tier 2).** `Complete` passes the 0042 trigger, clears only when `erasure_job_id` matches, and completes with 0 users rows after the operator clear. `Complete` with a stale `expect` returns `ErrJobMoved` and writes neither row. `ClearRestriction` of a failed job whose flag names another job returns `ErrRestrictionHeldByOtherJob` and writes nothing. A `ClearRestriction` that starts while a `Restart` of the same job holds the job row returns `ErrNotFailed` once the `Restart` commits, and a `ClearRestriction` that commits first is followed by a `Restart` that sets the flag again; both orders end with the job `initiated` and the flag naming it, and these are the cases that discriminate the job-row lock. The users row's `version` increments on every write.
- **T7.3 (tier 2).** `Restart` of a failed job writes row E3's admitted job-write and `processing_restricted` cells, and returns `*ErrActiveJob` while another job for the user is in progress, including when that job's `Start` or `Restart` commits while `Restart` waits on the index, which is the case a read-before-write guard misses.
- **T7.4 (tier 1).** `Memory` parity for T7.1 to T7.3.
- **T7.5 (tier 1).** The sampler clears the age series of a job that completed and of a job another replica finished, on the next sample, and sets the active-jobs gauge to the `ListActive` count.
- **T7.6 (tier 2).** Two concurrent `Update` calls on one job in the Postgres store serialize, so the second mutate sees the first's write, and a mutate that returns an error writes nothing.
- Add `SetErasureJobsActive` to the `fakeAgeSink` in `pkg/gateway/storage/erasurejob/sla_test.go`.

### Tests that land with CODE-7

- **T8.1 (tier 1).** For each persisted phase, `Run` resumes there and does not re-run an earlier step (asserted by call counters on the preflight, drain, `DeleteByUser`, `Pseudonymize`, and `Verify` fakes); a job resumed in `verifying` with `ErasedAt` zero calls `Verify` exactly once before the receipt.
- **T8.2 (tier 1).** CODE-7 item 1's missing-hook rule: one case for a runner with no drain hook and one for a runner with no hold-check hook, each asserting row E6's status, counter, and flag cells for that hook's failure and that no `DeleteByUser` runs.
- **T8.3 (tier 1).** A receipt error, or a runner with no receipt hook, leaves the job in progress and the flag set; the next `RunPending` re-runs no eraser, and the receipt counts equal the first attempt's.
- **T8.4 (tier 1).** A no-op re-run of `store_deleting` does not lower any per-store count.
- **T8.5 (tier 1).** A table over rows E1, E2, E3, and E5 of the **Erasure entry-point table** asserts each cell of the row: the job write or its absence, the status, the counter, the audit event and its `jobId`, the flag, and the HTTP response. The cases include a duplicate erase with and without a hold and with and without the override, a retry on `*ErrActiveJob`, a clear of a job not `failed`, a clear while the flag names another job, a retry of a job whose `OverrideReceipt` lists a still-active hold, and an admitted erase, which persists the deadline `WithDeadlineResolver` returns.
- **T8.6 (tier 4).** The erasure journey in `tests/tier4_integration/erasure_job_test.go` runs with a short `--tenant-deletion-interval`, and two gateway replicas against one Postgres resume a job after the replica running it is stopped. Its `completed` assertion is the one that fails when CODE-7 item 8 leaves the drain or hold-check hook unwired (CODE-7 item 1). A run whose gateway DSN sets `pool_max_conns=4` erases eight users, one in each of eight tenants, with billing pseudonymization at once, and every job reaches `completed`.
- **T8.7 (tier 8).** A crash between the receipt and `Complete` yields a byte-identical second receipt and a completed job with the restriction cleared.
- **T8.8 (tier 1).** After an operator clear of a job that failed in `verifying`, a retry followed by `RunPending` calls the drain before `DeleteByUser`, the drain terminates a session created while the restriction was cleared, and `Pseudonymize` runs again before `Verify`.
- **T8.9 (tier 1).** Row E6 of the **Erasure entry-point table**: one case per failure cause its status cell names, each asserting the row's status, counter, and flag cells and that no `DeleteByUser` runs; a hold listed in `OverrideReceipt` before and after a `Restart`, asserting the row's exemption; and a crash-resumed job in `store_deleting`, asserting row E4's re-check cell.
- **T8.10 (tier 1).** A job past its SLA deadline is retried and completes. A context cancelled inside a phase leaves the job in that phase, increments no counter, and records no duration observation, and a `RunPending` on a live context then finishes it. With a limit of two, while one job's `DeleteByUser` blocks, a second `RunPending` starts no second run of that job and completes a job of another tenant. With a limit of two, while jobs of two tenants block, a third tenant's job starts no run until one of them returns, and while a held `acme` salt lock blocks `acme` jobs in `Pseudonymize`, a second `acme` job listed before a `globex` job by `StartedAt` starts no run and the `globex` job completes. The `globex` completion is the assertion that separates the tenant rule from the limit. In `cmd/lenny-gateway`, startup refuses a limit of 0 and a limit equal to the Postgres pool's `MaxConns`.
- **T8.11 (tier 1).** CODE-7 item 1's guard: two runners over one `Memory` store run one job with their steps interleaved by blocking fakes. Each transition appears once in `PhaseLog`, the two receipts are byte-identical, the failure counter increments at most once, a stale runner's `fail` leaves a `completed` job `completed`, a `fail`, alone or followed by a `Restart`, that lands between the receipt and `Complete` leaves the job `failed` or `initiated` and the flag set, and a runner holding an `initiated` read across a `fail` and a `Restart` writes nothing. The last case is the one that separates the `PhaseLog`-length guard from a `Phase`-only guard.
- **T8.12 (tier 1, cmd).** `tick` with a fake elector that reports follower does not call `ReconcileAll`, and with one that reports leader calls it. While a tenant's Phase 4 eraser blocks inside `tenantDeletionRunner.Start`, `Runner.Poll` completes a pending erasure job. `addErasureSweep` on a `gatewayleader.NewGate` over a fake elector calls a pending job's drain hook only after the elector acquires, and calls it for no job created after the elector loses. The receipt sink of CODE-7 item 8 returns its appender's `Append` error.
- **T8.13 (tier 1).** `HeldResourcesForUser` and the erasure `Sessions` lister of CODE-7 item 8 each pass `ListFilter{UserID: userID, Primary: true}` to a recording fake session store.
- **T8.14 (tier 3).** Extend `tests/tier3_contract/admin_conflict_codes/admin_conflict_codes_test.go` with one case per `409` cell of rows E2, E3, and E5 whose code the §15.1 **Error code catalog** enumerates, asserting the cell's code and the `errorclassify` verdict of its catalog row.
- **T8.15 (tier 7a).** The job-write guard of CODE-7 item 1 under randomized interleavings with `-race`: two runners call `RunPending` over one job in one `Memory` store while `Restart` and `ClearRestriction` of that job run concurrently, and every eraser, hook, and receipt fake yields at random points. After each run the checker asserts that the failure counter's increments equal the `failed` entries in `PhaseLog`, that no phase repeats in `PhaseLog` between two `initiated` entries, that nothing writes the job after it is `completed`, that receipts sharing a `ReceiptAt` are byte-identical, and that the flag names the job while it is in progress and is cleared once it is `completed`.
- Retarget `pkg/gateway/externalapi/admin/erasure_test.go` and `erasure_retry_test.go` to drive `Runner.RunPending` or the store directly instead of polling a goroutine, and assert the receipt through a recording `WithCompletionReceipt` fake rather than the Router's `AuditSink`. Wire no-op `WithSessionDrain` and `WithHoldCheck` fakes into every existing runner fixture that drives a job past `initiated`, in these files (`newErasureAdmin` included) and in `pkg/gateway/storage/erasurejob/runner_test.go`, `completion_hook_test.go`, and `sla_test.go`, because CODE-7 item 1's missing-hook rule fails such a job at `initiated`.
- Retarget `TestRunnerLifecycleMetrics` and `TestRunnerLifecycleMetricsOnFailure` in `pkg/gateway/storage/erasurejob/sla_test.go` to assert only the duration observation, and replace the `IncErasureJobsActive` and `DecErasureJobsActive` calls in `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_erasure_test.go` with `SetErasureJobsActive`.

### Tests that land with CODE-8

- **T9.1 (tier 1).** `TestOpenResponsesDelete` as CODE-8 item 2 states.
- **T9.2 (tier 3).** Extend `tests/tier3_contract/rest_sessions/openapi_document_test.go` to assert that `/v1/responses/{id}` has no `delete` operation.
- **T9.3 (tier 4).** The rewritten cross-tenant test as CODE-8 item 3 states.

### TEST-1 · Cross-cutting lifecycle scenarios

These scenarios exercise the deliverables together and land after the code steps.

- **X1 (tier 7a).** Randomized interleavings under `-race` of user erasure, tenant suspension, tenant resume, and tenant-deletion Phase 1 and Phase 2 against concurrent creates on every create path and concurrent terminal writes. After each scenario the checker asserts: no session of a fenced scope is live after its drain succeeded; no refused create left a row, an object, a manifest row, or a reservation under its session ID; no object or reservation remains under a session ID reserved for a user whose erasure completed; exactly one `tenant.suspended` row with `already_suspended: false` per marker transition; and exactly one in-progress erasure job per user.
- **X2 (tier 8).** Fault injection at each step: a store error in the drain listing, in one `ForceTerminate`, in `Start`, in each erasure phase, and in the receipt; context cancel on the suspend request; a crash between erasure phases; a crash between a derive's copy and its insert, after which the next erasure of the owner deletes the copied objects; replica takeover of the lifecycle tick; and a graceful stop of the leader inside an erasure phase, after which another replica resumes the job. Each case asserts the restriction stays set until completion, the tenant-deletion job stays in Phase 2 until the drain succeeds, and a resumed job re-runs no completed step.
- **X3 (tier 4).** Extend `tests/tier4_integration/tenant_deletion_phase4_test.go` with a Phase 2 drain of live sessions before Phase 4. This advances T-12.8.9 and does not close it.
- **X4 (tier 5).** On Kind, suspend a tenant with sessions bound on two gateway replicas, assert every session reaches a terminal state, every pod returns to the pool within the Sweeper interval plus `maxWorkspaceSealDurationSeconds`, and a create after the suspend is refused with `TENANT_SUSPENDED`. An erasure variant on a pool without concurrent sessions, with `claimOrphanTimeout` lowered, erases a user whose session is bound on the other replica and asserts the session's claim is no longer `bound` after `claimOrphanTimeout` plus one orphan-GC pass.
- **X5 (tier 9).** New `tests/tier9_security/erasure_isolation_test.go` against the Postgres erasure job store: a `globex` tenant-admin receives `404` on get and `403 FORBIDDEN` on retry and clear-processing-restriction for an `acme` job, and the store's all-tenants `Get` returns no field of the job to that caller. Against a live gateway with real auth, `POST /v1/sessions`, `POST /v1/sessions/start`, MCP `create_session`, and `delegate_task` each refuse under each marker; the full path-by-marker cross-product stays at tier 1. `DELETE /v1/responses/{id}` returns `405`.

## Edge cases and accepted failure modes

1. **A long Phase 2 drain delays the tenant-deletion tick.** The tick budget bounds each drain (CODE-5 item 1), so a long drain delays other tenants' deletion by at most one budget per drain. The tier-4 and tier-8 runs log the drain time.
2. **Drained sessions are recorded as runtime failures.** The §24.11 transition writes `failed` with `FailureClassRuntime` and `FORCE_TERMINATED`, so failure metrics count lifecycle drains.
3. **The tenant-deletion job store stays in memory.** It is rebuilt from `TenantState` on restart, and a Phase 2 that had not completed resumes there.
4. **A reserved ID's data written after the erasers ran.** A derive copy or a delegated child's export that is still writing when the owner's erasure runs its session-scoped erasers is refused at its insert, whether the erasure is still in progress (the fence) or has completed (`CreateReserved` finds no reservation), and deletes what it wrote. Only a gateway crash between the end of the erasers and the undo leaves data no eraser reaches, because the erasure already deleted the reservation. REC-1 item 11 files this residual.
5. **Reservations of creates that end without a row or an undo.** A copy failure (C9) or a crash keeps its reservation. The row is a few bytes, the owner's erasure or the tenant's deletion removes it, and no sweep is added.

## Files touched on application (non-spec)

- `pkg/gateway/session/sessionstore/sessionstore.go`, `pkg/gateway/session/sessionstore/pgstore/pgstore.go`, `pkg/gateway/session/sessionstore/memstore/memstore.go`
- `pkg/gateway/sessionserver/create.go`, `create_and_start.go`, `quota.go`, `derive.go`, `replay.go`, `checkpoint_seams.go`, `user_gate.go`
- `pkg/gateway/mcpfabric/delegation/service.go`
- `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/environment/translator/open_responses.go`, `pkg/gateway/environment/translator/open_responses_test.go`
- `pkg/gateway/mcpfabric/mcptools/mcptools.go`, `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`
- `pkg/gateway/externalapi/errorclassify/errorclassify.go`, `pkg/gateway/externalapi/openapi/openapi.json`
- `pkg/gateway/externalapi/admin/sessions.go`, `tenant_suspend.go`, `erasure.go`, `legalhold.go`, and the file holding `DrainSessions`
- `pkg/controller/tenantdeletion/controller.go`, `pkg/controller/tenantdeletion/lifecycle.go`
- `pkg/gateway/storage/erasurejob/erasurejob.go`, `runner.go`, `sampler.go`, and the new `pkg/gateway/storage/erasurejob/pgstore/`
- `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_erasure.go`
- `pkg/gateway/environment/userstore/userstore.go`, `pkg/gateway/environment/userstore/pgstore/pgstore.go`
- `cmd/lenny-gateway/stores.go`, `adminrouter.go`, `session_admin.go`, `tenantdeletion.go`, `leader_sweeps.go`, `wiring_fields.go`, `mcpsurface.go`, `flags.go`
- `migrations/0186_erasure_jobs_detail.up.sql`, `migrations/0186_erasure_jobs_detail.down.sql`, `migrations/0187_session_reservations.up.sql`, `migrations/0187_session_reservations.down.sql`
- Tests: the existing test files the Testing section retargets, `pkg/gateway/externalapi/openapi/routestub_test.go`, `tests/tier2_component/migrations/prod_columns_test.go`, `tests/tier2_component/migrations/session_reservations_test.go`, `tests/tier3_contract/admin_conflict_codes/admin_conflict_codes_test.go`, `tests/tier3_contract/admin_tenant_suspend/suspend_response_test.go`, `tests/tier3_contract/rest_mcp_consistency/error_matrix_test.go`, `tests/tier3_contract/rest_sessions/openapi_document_test.go`, `tests/tier4_integration/openai_cross_tenant_test.go`, `tests/tier4_integration/erasure_job_test.go`, `tests/tier4_integration/tenant_deletion_phase4_test.go`, and `tests/tier9_security/erasure_isolation_test.go`
- Docs: `docs/operator-guide/multi-tenancy.md`, `docs/reference/error-catalog.md`, `docs/api/index.md`, `docs/api/mcp.md`, `docs/api/rest.md`, `docs/operator-guide/security.md`, `docs/api/admin.md`
- Records: `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md`, and, only if a claim row is added, `scripts/seed-claim-register.py` and `tests/claim-map.json`
