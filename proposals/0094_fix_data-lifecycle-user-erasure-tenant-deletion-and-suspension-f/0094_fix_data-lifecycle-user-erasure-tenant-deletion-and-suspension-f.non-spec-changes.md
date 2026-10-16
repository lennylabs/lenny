# Non-spec changes: Data lifecycle operations do not terminate and erase every session and store they cover

## Design (implementation-facing)

**Store-and-phase-to-owner table.** Each row names one step of a lifecycle operation, or one store it erases, and the deliverable or owner that covers it. "Existing" means the tree at the merge base already covers it and this proposal changes nothing there.

| Operation | Step or store | Owner |
|:--|:--|:--|
| All lifecycle operations | Creation fence on every session insert | 0094: CODE-1, CODE-2 |
| All lifecycle operations | Shared drain | 0094: CODE-3 |
| User erasure | Job record, restriction set, and restriction clear | 0094: MIG-1, CODE-6 |
| User erasure | Drain in `initiated`, phase dispatch, receipt, and completion | 0094: CODE-7 |
| User erasure | Step 1 `LeaseStore` release | Existing no-op recorded in F-12.1.3, which holds as an invariant once the drain runs before the erasers |
| User erasure | Step 3 Redis access-token and routing caches | Existing decision F-12.2.16, which stands |
| User erasure | Session, transcript, interaction, queue, and other wired erasers | Existing |
| User erasure | Checkpoint manifest, retention, and metadata rows (N-3) | Piece B follow-on |
| User erasure | `coordination_lease` mirror rows | Piece B follow-on |
| User erasure | User registry row (SG-9 user half) | Piece B follow-on |
| User erasure, tenant deletion, tenant suspension | Pod release and terminal tail of a session bound on another replica | P2 (residual; REC-1 files it) |
| Tenant deletion | Phase 1 creation fence | 0094: CODE-1, CODE-2 |
| Tenant deletion | Phase 1 API-key issuance and user sign-up gate | Unassigned (REC-1 files it) |
| Tenant deletion | Phase 2 drain | 0094: CODE-5 |
| Tenant deletion | Phase 3 token and credential-lease revocation (F-12.8.30) | Piece B follow-on, gated on P1's lease reclaimer |
| Tenant deletion | Phase 3.5 legal-hold segregation | Existing |
| Tenant deletion | Phase 4 stores already wired | Existing |
| Tenant deletion | Phase 4 missing stores (F-12.8.34, F-12.8.35, F-12.8.37) | Piece B follow-on |
| Tenant deletion | Phase 4 audit eraser foreign key (F-12.8.25) | Piece C follow-on, before B's audit eraser |
| Tenant deletion | Phases 5 and 6 | Existing |
| Tenant suspension | Marker, audit, repeat drain, and response | 0094: CODE-4 |
| full_revoke | Creation race (F-11.4.14 creation half) | 0094: CODE-1 |
| full_revoke | Primary listing, detached context, error continuation, and per-effect counts (F-11.4.12, F-11.4.13, F-11.4.14 listing half) | full_revoke follow-on, after P1 |
| full_revoke | Release mechanics | P1 (0093) |
| `DELETE /v1/responses/{id}` | Route removal (F-15.1.48) | 0094: CODE-8 |
| `GET /v1/responses/{id}` | User check (F-15.1.54) | Unassigned |

**Lock discipline of the creation fence.** The normative rule is SPEC-1. In code, the fence takes `FOR SHARE` on the tenant row and then on the owning user's row, inside the session insert's existing transaction, before the `INSERT`. `FOR SHARE` conflicts with `FOR UPDATE` (taken by `tenantstore` and `userstore` `Update` and by `ClearProcessingRestriction`) and with `FOR NO KEY UPDATE` (taken by any plain `UPDATE`, such as `userstore` and `tenantstore` `SoftDelete`); summary D1 records why no weaker mode works. The lock order is tenant row, then user row, and every marker writer locks exactly one of the two rows, so no lock cycle exists.

**Pre-gate and fence agree.** The handler pre-gate (CODE-2) and the store fence (CODE-1) evaluate one predicate, `sessionstore.CreationMarkers.Refusal`, on the same input: the tenant of the row and the user the row will carry (`Session.UserID`). The pre-gate exists so that a refused create does not claim a pod, reserve budget, or copy a snapshot. The fence is the authoritative check.

**One drain.** `admin.DrainSessions` (CODE-3) is the only drain. Suspension (CODE-4), tenant-deletion Phase 2 (CODE-5), and the erasure runner (CODE-7) call it. What a successful drain proves is stated once, in the doc comment CODE-3 item 4 specifies.

**One erasure executor.** The erasure job runs only inside the existing advisory-locked `tenantDeletionRunner` tick. No handler starts a goroutine for it.

## Staged code changes

### CODE-1 · Creation predicate and the fence inside the session insert

**Targets:** `pkg/gateway/session/sessionstore/sessionstore.go` (new `CreationMarkers`, `CreationRefusal`, and `CreationMarkers.Refusal`); `pkg/gateway/session/sessionstore/pgstore/pgstore.go` `Create`; `pkg/gateway/session/sessionstore/memstore/memstore.go` `Create` and a new `WithCreationMarkers` option; `cmd/lenny-gateway/stores.go` memory branch.

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
       IsDelegatedChild     bool   // Session.ParentSessionID != ""
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

   Rule 1 is the one marker-specific case; state that in the doc comment and cite `// spec: §11.4 (User Invalidation)`. The messages reuse the wording of `requireActiveUserForAction` and `requireTenantState`.

3. `pgstore.Create`: inside the existing `pgtenant.InTx`, before the `INSERT`, read the tenant row's `suspended` and `state` with `SELECT ... FROM tenants WHERE id = $1 FOR SHARE`, then, when `sess.UserID != ""`, read the user row's `disabled`, `deleted_at IS NOT NULL`, `processing_restricted`, and `erasure_job_id` with `SELECT ... FROM users WHERE tenant_id = $1 AND subject = $2 FOR SHARE`. A missing row sets the matching `Found` field false. Any other read error aborts the transaction and returns the wrapped error, so the insert fails closed. A non-nil `Refusal` aborts the transaction and returns the `*CreationRefusal`. The fence has no exemption: it applies to every row, including a row created terminal, such as the derive-failure row.

4. `memstore`: add `WithCreationMarkers(read func(tenantID, userID string) (CreationMarkers, error))`. `Create` calls it under `s.mu`, before the insert, and applies `Refusal` the same way. `cmd/lenny-gateway/stores.go` wires it in the memory branch over the in-memory tenant and user stores. No closure passed to `tenants.Update` or `users.Update` touches the session store, so taking those stores' locks under `s.mu` creates no lock-order cycle; state that in the option's doc comment.

5. Document in `Create`'s doc comment and in the option's doc comment that `TenantFound == false` is reachable only in `memstore`. In `pgstore`, the `sessions.tenant_id` foreign key rejects an insert for a missing tenant first.

6. Cite `// spec: §12.8 (Lifecycle fence and drain)` on `Refusal`, on the `pgstore` fence, and on the `memstore` fence.

### CODE-2 · Pre-gates and refusal mapping at every create site

**Targets:** `pkg/gateway/sessionserver/create.go` (`createAdmissionGates` and the `store.Create` error branch); `pkg/gateway/sessionserver/create_and_start.go` (the `store.Create` error branch); `pkg/gateway/sessionserver/derive.go` (`handleDerive` and both `store.Create` error branches); `pkg/gateway/sessionserver/replay.go` (`handleReplay` and the `store.Create` error branch); `pkg/gateway/sessionserver/user_gate.go` (new `requireOwnerNotRestricted` and `writeCreationRefusal`); `pkg/gateway/environment/translator/singleshot.go` (no-op single-shot binder); `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`requireActiveDelegator`); `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (the minimal-gateway fallback create and the `delegate_task` `Delegate` error mapping); `pkg/gateway/externalapi/errorclassify/errorclassify.go`.

1. Keep `requireActiveUser` and `requireActiveUserForAction` unchanged. They remain the §11.4 check on the caller.
2. Call the existing `requireTenantState` in `createAdmissionGates`, directly after `requireActiveUser`. This covers `POST /v1/sessions` and MCP `create_session`, which runs through `CreateSessionService` and `handleCreate`. Call it in `handleDerive` and in `handleReplay` directly after `resolveTenant`. `POST /v1/sessions/start` keeps its existing call, because it does not run `createAdmissionGates`.
3. Add `requireOwnerNotRestricted(w, r, tenantID, ownerUserID string) bool`. It reads `users.Get(tenantID, ownerUserID)`, builds `CreationMarkers` from the user fields (with `IsDelegatedChild` false), and writes the refusal from `Refusal` when it is non-nil. `ErrNotFound` admits. Any other error writes `500 INTERNAL_ERROR` and returns false. Call it:
   - in create and create-and-start when the request's `UserID` is non-empty and differs from the principal's subject;
   - in derive after the owner is resolved from the request or the source row, before the snapshot copy;
   - in replay after the source read, with `source.UserID`, before the copy.
4. Add `writeCreationRefusal(w, ref *sessionstore.CreationRefusal)`, which writes `403` with `ref.Code`, `ref.Message`, and `ref.Details`. At every `store.Create` error branch in create, create-and-start, derive (both branches), and replay, add `var ref *sessionstore.CreationRefusal; if errors.As(err, &ref) { writeCreationRefusal(w, ref); return }` after the branch's existing claim rollback, binding rollback, or lease release, and before the existing 5xx mapping.
5. Map the refusal in the two fallback creators:
   - the no-op single-shot binder returns `*SingleShotError{HTTPStatus: 403, Code: ref.Code}`;
   - the mcptools minimal fallback returns `mcp.NewToolError(ref.Code, ref.Message, ref.Details)`.
6. Extend `requireActiveDelegator` with the parent's tenant markers (through `deps.Tenants`) and `processing_restricted`, evaluated by `Refusal` with `IsDelegatedChild` true, keyed on the parent session's user. A soft-disabled user's children are therefore admitted and a hard-disabled user's are refused. Add an `errors.As(err, &ref)` branch at the `delegate_task` `Delegate` error mapping that returns the refusal code as the tool error.
7. Add `"TENANT_NOT_ACTIVE": {CategoryPolicy, false}` and `"USER_INVALIDATED": {CategoryPolicy, false}` to the `errorclassify` table beside `TENANT_SUSPENDED`, each with a `// spec: §15.1 (Error code catalog)` comment. Without them MCP classifies both codes as transient and retryable.
8. Cite `// spec: §12.8 (Lifecycle fence and drain)` on every new gate call and mapping branch.

### CODE-3 · Primary listing, tenant-scoped forced termination, and the shared drain

**Targets:** `pkg/gateway/session/sessionstore/sessionstore.go` `ListFilter`; `pkg/gateway/session/sessionstore/pgstore/pgstore.go` `List`; `pkg/gateway/session/sessionstore/memstore/memstore.go` `List` (accepts the field); `cmd/lenny-gateway/session_admin.go` `sessionAdminAdapter.ForceTerminate`; `pkg/gateway/externalapi/admin/sessions.go` `SessionAdmin` and `handleForceTerminateSession`; `pkg/gateway/externalapi/admin/tenant_suspend.go` `drainTenantSessions`; the `SessionAdmin` test doubles in `pkg/gateway/externalapi/admin/sessions_test.go`, `pkg/gateway/externalapi/admin/routestub_test.go`, and `cmd/lenny-gateway/session_admin_test.go`.

1. Add `Primary bool` to `ListFilter`, documented as "read from the Postgres primary; used by lifecycle decisions. spec: §12.8 (Lifecycle fence and drain)". `pgstore.List` reads `s.pool` when it is set and `s.read` otherwise. `memstore` ignores it.
2. Change `SessionAdmin.ForceTerminate` to `ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error)`. The adapter deletes its internal `GetByID` and performs the existing locked `Update` under `tenantID`. `handleForceTerminateSession` first calls `r.sessionAdmin.GetByID` (mapping `ErrNotFound` to `404 RESOURCE_NOT_FOUND` as today) and then `ForceTerminate(ctx, sess.TenantID, sess.ID)`. Update the three test doubles. The transition itself (state `failed`, class `FailureClassRuntime`, reason `FORCE_TERMINATED`, the terminal claim, and `onTerminal`) is unchanged.
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

   The body follows SPEC-1 *Drain*: list with `ListFilter{UserID: userID, Primary: true}`; for each non-terminal row call `ForceTerminate(ctx, tenantID, row.ID)`, treat `ErrNotFound` as nothing to drain, record any other error in `Failed` and continue, and count `transitioned`; list again with the same filter and record each non-terminal id not already in `Failed` in `Remaining`; return `ErrDrainIncomplete` wrapped with both counts when either slice is non-empty. A listing error returns the wrapped listing error and the partial result.
4. The doc comment of `DrainSessions` states three things: a nil error means every scoped row is terminal on the primary; the pod of a session bound on another replica is released afterwards by that replica's coordination Sweeper through `ReleaseTerminalBinding`; and the verifying listing is a fence invariant check that can report a session legitimately created after a concurrent tenant resume.
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

**Targets:** `cmd/lenny-gateway/adminrouter.go` (the `tenantdeletion.Reconciler` construction); `cmd/lenny-gateway/tenantdeletion.go` (new `tenantSessionTerminator`); `pkg/controller/tenantdeletion/controller.go` (the seam comment on `Terminator`, the `PhaseTerminateSessions` case of `runPhase`, and the doc comment on `fail`); `pkg/controller/tenantdeletion/lifecycle.go` (the reason field the pause writes).

1. Add `tenantSessionTerminator{sessions, term}` in `cmd/lenny-gateway/tenantdeletion.go`, implementing `tenantdeletion.SessionTerminator`. `TerminateTenantSessions(ctx, tenantID)` calls `admin.DrainSessions(ctx, sessions, term, tenantID, "")` and returns its error. The runner's context is already detached, so the adapter adds no context wrapping.
2. Assign `Terminator: tenantSessionTerminator{sessions: w.sessions, term: sessionAdminAdapter{store: w.sessions, onTerminal: sessionSrv.OnSessionTerminal, beginTerminal: sessionSrv.BeginTerminal}}` in the reconciler construction.
3. Rewrite the seam comment so `Terminator` is required alongside `Eraser` and `Receipts`. `Revoker` stays optional; Phase 3 belongs to piece B.
4. In `runPhase`, `PhaseTerminateSessions`:
   - a nil `Terminator` calls `r.fail` with a permanent misconfiguration error;
   - a `TerminateTenantSessions` error leaves `Phase` unchanged, records the error text (the failed and remaining session ids) in the job's reason field, logs it at WARN with the tenant id, and returns nil, so `ReconcileAll` continues and the next tick re-runs the drain. Follow `pauseEscrowUnresolvable`. A later successful drain clears the reason field when the phase advances.
5. Correct the doc comment on `fail`: a failed job is terminal for the controller, and no production code path retries it. Cite `// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)`.

### CODE-6 · Durable erasure job store with transactional start, restart, and completion

**Targets:** `pkg/gateway/storage/erasurejob/erasurejob.go` (`Store`, `Job`, `Memory`); `pkg/gateway/storage/erasurejob/pgstore/` (new package); `pkg/gateway/storage/erasurejob/sampler.go`; `pkg/gateway/environment/userstore/pgstore/pgstore.go` (new `SetErasureRestrictionTx`, and `ClearProcessingRestriction`); `cmd/lenny-gateway/adminrouter.go` (store construction).

1. Replace the `Store` interface with:

   ```go
   type Store interface {
       // Start inserts j as `initiated` and sets the user's restriction
       // (processing_restricted true, erasure_job_id j.ID) in one transaction.
       // When the user already has an in-progress job it writes nothing and
       // returns *ErrActiveJob carrying that job.
       Start(ctx context.Context, j Job) (Job, error)
       Get(ctx context.Context, id string) (Job, error)
       Update(ctx context.Context, id string, mutate func(*Job) error) (Job, error)
       // Restart moves a `failed` job to `initiated`, clears ErasedAt and the
       // recorded Billing outcome, and re-sets the restriction in one
       // transaction. Returns *ErrActiveJob when
       // another job for the user is in progress, and ErrNotFailed when the job
       // is not `failed`.
       Restart(ctx context.Context, id string) (Job, error)
       // Complete marks the job `completed` at completedAt and clears the
       // restriction WHERE erasure_job_id = id, in one transaction.
       Complete(ctx context.Context, id string, completedAt time.Time) (Job, error)
       // ListActive returns every job in a non-terminal phase.
       ListActive(ctx context.Context) ([]Job, error)
   }
   ```

   `Create` and `List` are removed and every caller is updated.
2. Add to `Job`: `InitiatedBy string`, `OverrideReceipt map[string]any`, `ErasedAt time.Time`, and `ReceiptAt time.Time`. `ErasedAt` is set when the erasers have finished (CODE-7 item 2). `ReceiptAt` is the completion instant chosen once before the receipt (CODE-7 item 3).
3. Postgres store (`erasurejob/pgstore`): rows live in `erasure_jobs`. `id`, `tenant_id`, `user_id`, `status`, `total`, `failure`, `started_at`, and `completed_at` map to their columns. `Deadline`, `Deleted`, `Billing`, `DeadLetterRedacted`, `PhaseLog`, `InitiatedBy`, `OverrideReceipt`, `ErasedAt`, and `ReceiptAt` are serialized into the `detail` jsonb column (MIG-1). `Get` and `ListActive` run under the existing all-tenants read pattern on the primary. `Start` and `Restart` insert or update with `INSERT ... ON CONFLICT (tenant_id, user_id) WHERE status IN ('initiated','store_deleting','pseudonymizing','verifying') DO NOTHING RETURNING id`, or the equivalent guarded `UPDATE`. When no row is returned, they `SELECT` the existing in-progress job in the same transaction and return `*ErrActiveJob{Job}` without touching the users row. Do not catch a unique-violation error, because Postgres aborts the transaction on it.
4. Every users write in `Start`, `Restart`, and `Complete` goes through a new transaction-scoped helper in the userstore Postgres store: `SetErasureRestrictionTx(ctx, tx pgx.Tx, tenantID, subject, jobID string, restricted bool, requireJobID string) (rows int64, err error)`. It selects the row `FOR UPDATE`, applies `pgtenant.MonotonicNext` to `updated_at`, increments `version`, and writes `processing_restricted` and `erasure_job_id`. When `requireJobID` is non-empty, it writes only if `erasure_job_id` equals it and returns 0 rows otherwise. A missing user row in `Start` aborts the transaction with `userstore.ErrNotFound`. `ClearProcessingRestriction` keeps setting its session-local bypass and then calls the same helper, so one users write path remains. The erasurejob store does not hand-write a users `UPDATE`.
5. `Complete` marks the job row `completed` first and then calls the helper with `restricted=false` and `requireJobID=id`, in the same transaction. A job whose restriction was already cleared by the operator endpoint or replaced by a newer job writes 0 users rows and still completes.
6. `Memory` keeps parity through an injected restriction writer with the same semantics (`WithRestrictionWriter`), implemented over `userstore.Memory`. It enforces the single in-progress job per (tenant, user).
7. Sampler: `Sampler.Sample` calls `ListActive`, publishes the age of each returned job, keeps the set of `(tenantID, jobID)` it published in the previous round, calls `ClearErasureJobAge` for every previously published job absent from the current result, and then replaces the set.
8. `cmd/lenny-gateway/adminrouter.go` builds the Postgres store when the Postgres pool is configured and `Memory` otherwise.
9. Cite `// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)), §12.8 (Erasure job phase field (crash recovery))`.

### CODE-7 · Erasure runner: drain step, phase dispatch, receipt before completion, and execution in the lifecycle tick

**Targets:** `pkg/gateway/storage/erasurejob/runner.go` (`Run`, new `WithSessionDrain`, new `WithCompletionReceipt`, new `RunPending`); `pkg/gateway/externalapi/admin/erasure.go` (`handleEraseUser`, `runErasureToCompletion`, `handleRetryErasureJob`); `cmd/lenny-gateway/tenantdeletion.go` (`tenantDeletionRunner.Start` and `reconcileOnce`); `cmd/lenny-gateway/adminrouter.go` (the erasure `Sessions` lister and the runner start).

1. `Run` dispatches on the persisted `job.Phase`:
   - `initiated`: run the memory preflight, then the session drain hook (`WithSessionDrain(func(ctx, tenantID, userID string) error)`), then advance to `store_deleting`. A drain error fails the job with failure phase `store_delete` and leaves the restriction set.
   - `store_deleting`: run `DeleteByUser` and the dead-letter redaction. Merge each store's count into `j.Deleted` with a per-store maximum, so a re-run that deletes nothing never lowers a recorded count, and recompute `Total` from the merged map. Then advance to `pseudonymizing`.
   - `pseudonymizing`: when `job.Billing.Disposition` is already recorded, skip `Pseudonymize`. Otherwise run it and persist its outcome in the same `Update` that advances to `verifying`, or, for an exempt tenant, that records the erasers done.
   - `verifying`: re-run `Verify` only.
   A resumed job never re-runs a step of an earlier phase. Each phase advance is one `Update`.
2. Set `ErasedAt` in the `Update` that records the final billing outcome, or after store deletion when no `BillingEraser` is wired. When `Run` or `RunPending` finds `ErasedAt` set, it skips straight to the receipt and `Complete`.
3. Completion order: when `ReceiptAt` is zero, persist `ReceiptAt = now` with an `Update`. Build the `gdpr.erasure_completed` receipt from the persisted `Job` (counts, billing outcome, phase log with a final `{phase: completed, at: ReceiptAt}` entry, `InitiatedBy`, and `OverrideReceipt`) and write it through `WithCompletionReceipt(func(ctx, Job) error)`. Only after the receipt write returns nil, call `store.Complete(ctx, id, ReceiptAt)`. A receipt error leaves the job in progress for the next tick. A crash or error between the receipt and `Complete` makes the next tick write a byte-identical receipt. `runErasureToCompletion` becomes the receipt builder over `Job`, and its separate restriction clear is deleted.
4. Add `RunPending(ctx)`: list `ListActive` and run each job sequentially under a context bounded by the job's remaining deadline (`StartedAt + Deadline`), logging the elapsed drain time per job. Its doc comment states that erasure shares the tenant-deletion tick's advisory lock and runs sequentially with it.
5. `tenantDeletionRunner`: `reconcileOnce` calls `RunPending` after the tenant-deletion reconcile, under the same advisory lock. `Start` runs the loop whenever the erasure runner or the deletion reconciler is wired; the deletion reconcile body still runs only when `Receipts` is wired.
6. `handleEraseUser`: build the `Job` (with `InitiatedBy` and `OverrideReceipt`) and call `store.Start`. On `*ErrActiveJob`, answer `202` with the existing job's id. On any other error, answer `500 INTERNAL_ERROR` and start nothing. On success, answer `202` with the new id. Delete the goroutine and the separate `users.Update` call.
7. `handleRetryErasureJob`: before `store.Restart`, run `heldResourcesForUser` with no override and, when it returns a hold, answer through the block branch of `handleEraseUser`, extracted into a helper both handlers call, without calling `Restart`. Map `*ErrActiveJob` to `409 INVALID_STATE_TRANSITION`. Delete the goroutine.
8. `adminrouter.go`: the erasure `Sessions` lister calls `List(ctx, tenantID, ListFilter{UserID: userID, Primary: true})` and stops filtering the tenant in Go. Wire `WithSessionDrain` to `admin.DrainSessions` over the session store and the `sessionAdminAdapter`.
9. Cite `// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)), §12.8 (Erasure job phase field (crash recovery)), §24.12 (Erasure Job Management)`.

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

## Staged docs changes

### DOCS-1 · Reader documentation for the fence, the drain, the erasure job, and the suspend response

**Targets:** `docs/operator-guide/multi-tenancy.md`; `docs/reference/error-catalog.md`; `docs/api/index.md`; `docs/api/mcp.md`; `docs/api/rest.md` (optional); `docs/operator-guide/security.md`; `docs/api/admin.md`.

1. `docs/operator-guide/multi-tenancy.md`, tenant deletion phase table: leave the Phase 1 row unchanged. Replace the Actions cell of the `2. Drain active sessions` row with: "Force-terminate every active session in the tenant. The phase completes when no active session remains. When a session cannot be terminated, the phase stays incomplete and the controller retries it on its next pass." Do not describe locks or atomicity. Leave the page's existing phase count alone.
2. Error tables: add `TENANT_SUSPENDED` (403, `details.tenantId`), `TENANT_NOT_ACTIVE` (403, `details.tenantId` and `details.state`), and `USER_INVALIDATED` (403, not retryable) as session-creation rejections, worded after the §15.1 catalog rows:
   - `docs/reference/error-catalog.md`, beside `ERASURE_IN_PROGRESS`;
   - `docs/api/index.md`, beside `ERASURE_IN_PROGRESS`, with category `POLICY` and retryable `No`;
   - `docs/api/mcp.md`, in the `create_session` error table;
   - optionally the key error codes list in `docs/api/rest.md`.
3. `docs/operator-guide/security.md`, **Erasure Process**: add one sentence stating that before any data is deleted, the erasure job force-terminates the user's running sessions, and that a failure to terminate them fails the job and leaves the restriction set.
4. `docs/api/admin.md`: under `POST /v1/admin/users/{userId}/erase`, state that a request for a user with an erasure job already in progress returns that job's ID and starts no second job. On the erasure-job retry row, state that it returns `409 INVALID_STATE_TRANSITION` when the user already has another job in progress, and `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` when a legal hold on the user's sessions or artifacts blocks it.
5. `docs/api/admin.md`, Tenants section: add a `POST /v1/admin/tenants/{id}/suspend` entry. It drains active sessions; a repeat request on a suspended tenant re-runs the drain; the response reports `drainedSessions` and `failedSessions`, and a non-empty `failedSessions` means the drain is incomplete and the request can be repeated; a listing failure returns `500 INTERNAL_ERROR`.
6. Apply `doc-style.md` and `doc-content.md`. Quote no log line and cite no spec section number in prose. `docs/api/open-responses.md` documents no `DELETE`, so CODE-8 needs no docs edit.

### REC-1 · Records: findings, the remediation-plan bullet, and the claim register

**Targets:** `BUILD-GAPS.md`; `gateway-runtime-comms-remediation.md` (the P5 bullet); `scripts/seed-claim-register.py` and the generated `tests/claim-map.json` (only if a claim row is added).

1. Close, with a resolution naming 0094: F-12.8.26, F-12.8.27, F-12.8.28, F-12.8.31, F-12.8.32, F-12.8.33, F-15.1.48, F-15.1.50, F-15.1.51, and F-12.8.36.
2. Reopen F-12.8.20 (the in-memory erasure job store with no writer of `erasure_jobs`) and close it under 0094 with a resolution.
3. File N-1 (the Phase 2 terminator is unwired) and N-5 (the completion clear ignores its error and is not atomic with the receipt) as findings closed by 0094.
4. Keep F-11.7.22 and F-11.4.14 OPEN and annotate them. F-11.7.22: the `tenant.suspended` half landed in 0094, and the `session.force_terminated` half stays with P2. F-11.4.14: 0094's creation fence covers the concurrent-insert case, and the read-pool single listing stays with the full_revoke follow-on.
5. File N-3 and the `coordination_lease` mirror gap under piece B. Keep F-11.4.12 and F-11.4.13 OPEN.
6. File an OPEN finding owned by P2 for the terminal-tail residual that SPEC-1 *Drain* accepts, with the bound of the other replica's Sweeper interval plus `maxWorkspaceSealDurationSeconds`.
7. File an OPEN finding, unassigned, for the §12.8 Phase 1 API-key issuance and user sign-up rejection: `POST /v1/oauth/token` mints, including the playground mint, are not refused for a tenant whose `TenantState` is not `active`.
8. No claim-register row is added for a duplicate receipt, because SPEC-2 makes it specified behavior. When the owner wants a claim row for the terminal-tail residual, add it to `EXPLICIT` in `scripts/seed-claim-register.py` with a spec anchor that resolves in `spec/28_communication-channels.md` and a `deferral_id` naming an R step the plan declares, then regenerate `tests/claim-map.json` with the script. Never hand-edit `tests/claim-map.json`.
9. Update the P5 bullet of `gateway-runtime-comms-remediation.md`: 0094 covers pieces A and D; pieces B and C and the full_revoke fixes are follow-ons; replace "waits for their terminal hooks" with the P2-owned residual of item 6.

## Testing

Tests are stated as intents. Each intent is pinned by at least one test at each tier it names and carries a `// spec:` annotation naming the sections it exercises, such as `// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)`. Every tier-2-and-higher test carries a `// diagnosis:` comment above the function stating what a failure means. Each intent listed under a deliverable lands in that deliverable's checklist step, together with every update to an existing test that the deliverable's code breaks.

**Failures the tests find.** The owner's triage rule applies to every failure a test exposes during implementation. Fix in this proposal a failure that is relevant to it, meaning the faulty code is a line this proposal changes or the defect stops one of its intents from holding, and a failure whose fix is straightforward and not owned elsewhere, meaning it changes no spec text, schema, migration, flag, or chart value and stays within one package. File everything else: add a `BUILD-GAPS.md` finding with the next free identifier, skip only the failing case with a `t.Skip` reason that opens with `blocked:` and the finding id, and record a deviation entry naming the intent, the finding, and the owner. Run the environment preflight of `.claude/rules/test-coverage.md` before reading a failure above tier 1 as a defect.

### Tests that land with CODE-1

- **T1.1 (tier 1).** A table over the full cross-product of `CreationMarkers` fields, including root insert and child insert, asserts `Refusal` returns the code and details of the first matching CODE-1 rule. A case per `tenantstore` state constant asserts that `Refusal` admits exactly the states `tenantstore.Tenant.AcceptsNewWork` admits.
- **T1.2 (tier 1).** `memstore.Create` with `WithCreationMarkers` refuses with `*CreationRefusal` for each rule, admits a row with no `UserID` under user markers, and returns a marker-reader error without inserting.
- **T1.3 (tier 2).** Against real Postgres, `pgstore.Create` refuses for each marker, inserts nothing on refusal, fails closed when the user read errors (injected through a revoked grant or a closed transaction), and refuses a derive-failure row created terminal.
- **T1.4 (tier 2, discriminating).** Lock conflict: a transaction holding `FOR UPDATE` on the tenant row (through `tenantstore.Update` with a blocking mutate) blocks `pgstore.Create` until it commits, and the create then observes the new marker and refuses. Repeat for `userstore.Update`, `userstore.SoftDelete`, and `tenantstore.SoftDelete`. The `SoftDelete` cases are the ones that distinguish `FOR SHARE` from `FOR KEY SHARE`.
- **T1.5 (tier 7a).** Randomized interleavings under `-race` of concurrent creates against marker writes and a drain over both stores. The invariant checker asserts that after the marker commit and one drain, no non-terminal row of the scope exists and every refused create inserted nothing.

### Tests that land with CODE-2

- **T2.1 (tier 1).** For each create site (create, create-and-start, derive, replay, the no-op single-shot binder, the mcptools fallback, and `delegate_task`), a fake store returning `*CreationRefusal` yields `403` with the refusal code, and the site's claim, binding, or lease rollback ran. Without the mapping the same case yields a 5xx.
- **T2.2 (tier 1).** The pre-gate keys on the row owner: a caller creating with `userId` of a restricted user is refused before any claim, and a restricted caller replaying an active user's session is admitted by the owner check while `requireActiveUser` applies its own §11.4 rule to the caller.
- **T2.3 (tier 1).** `requireActiveDelegator` admits a child of a soft-disabled user, refuses a child of a hard-disabled user, and refuses a child under each tenant marker and under `processing_restricted`.
- **T2.4 (tier 1).** `errorclassify` rows: `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` classify as `{CategoryPolicy, false}`.
- **T2.5 (tier 3).** Add `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` to the matrix list in `tests/tier3_contract/rest_mcp_consistency/error_matrix_test.go`, each with a canonical trigger on `POST /v1/sessions` and MCP `create_session`, asserting identical code, category, retryable, and status.
- Retarget the existing `sessionserver` user-gate and tenant-state tests so they cover the new `requireTenantState` call sites and `requireOwnerNotRestricted`.

### Tests that land with CODE-3

- **T3.1 (tier 1).** `DrainSessions` table over: empty scope; all terminal; a mix; a `ForceTerminate` error on one session (the others still terminate and the id lands in `Failed`); `ErrNotFound` on one session; a first-listing error; a verifying-listing error; and a verifying listing that returns a new non-terminal row (the id lands in `Remaining`). Each case asserts the result and whether the error wraps `ErrDrainIncomplete`.
- **T3.2 (tier 2).** With a read pool that has not seen a freshly inserted row (built with `sessionpg.WithReadPool` over a second pool, as the existing split-pool tests do), `List` with `Primary: true` returns the row and `List` without it does not.
- **T3.3 (tier 1).** `handleForceTerminateSession` maps an unknown id to `404` before calling `ForceTerminate`, and passes the row's tenant.

### Tests that land with CODE-4

- **T4.1 (tier 1).** First suspend: one marker write, one `tenant.suspended` row emitted before the drain, and a `200` body with `drainedSessions` and an empty `failedSessions`.
- **T4.2 (tier 1).** Repeat suspend: no marker write, one `tenant.suspended` row with `already_suspended: true` emitted before the drain, the drain runs again, and its count is reported.
- **T4.3 (tier 1).** Incomplete drain yields `200` with the failed and remaining ids in `failedSessions`; a listing failure yields `500 INTERNAL_ERROR` with the marker set.
- **T4.4 (tier 1).** A request context cancelled mid-drain still completes the audit emit and the drain.
- **T4.5 (tier 7a).** Two concurrent first-time suspends under `-race` produce exactly one marker write and two `tenant.suspended` rows, exactly one of them with `already_suspended: false`.
- Retarget the existing idempotency and drain cases in `tenant_suspend_test.go`.

### Tests that land with CODE-5

- **T5.1 (tier 1).** A drain error leaves the job at `PhaseTerminateSessions` with the reason recorded, and the next `ReconcileTenant` re-runs the drain and advances on success.
- **T5.2 (tier 1).** A nil `Terminator` fails the job at Phase 2.
- **T5.3 (tier 1, cmd).** `ensureJob` plus the next tick re-drain without a restart.
- Retarget `TestLifecycleCompletesWithOnlyRequiredSeams` and `TestNilKMST4TenantCompletesWithoutDestroy` in `pkg/controller/tenantdeletion/legalhold_test.go` to wire a fake `Terminator`, rename the first to reflect that `Terminator` is required, and wire a `Terminator` in the `cmd/lenny-gateway/tenantdeletion_test.go` fixture.

### Tests that land with MIG-1

- **T6.1 (tier 2).** `TestErasureJobInProgressIndexMigration_spec_12_8`: a second in-progress row for the same `(tenant_id, user_id)` fails with `unique_violation`; a `completed` or `failed` row beside an in-progress row is admitted; the down migration removes both the index and the column.

### Tests that land with CODE-6

- **T7.1 (tier 2).** `Start` writes the job and the restriction together; a forced failure of the users write leaves neither; a second `Start` for the same user returns `*ErrActiveJob` with the first job and writes nothing.
- **T7.2 (tier 2).** `Complete` passes the 0042 trigger, clears only when `erasure_job_id` matches, and completes with 0 users rows after the operator clear. The users row's `version` increments on every write.
- **T7.3 (tier 2).** `Restart` of a failed job moves it to `initiated`, clears `ErasedAt` and `Billing`, and re-sets the restriction, and returns `*ErrActiveJob` while another job for the user is in progress.
- **T7.4 (tier 1).** `Memory` parity for T7.1 to T7.3.
- **T7.5 (tier 1).** The sampler clears the age series of a job that completed and of a job another replica finished, on the next sample.

### Tests that land with CODE-7

- **T8.1 (tier 1).** For each persisted phase, `Run` resumes there and does not re-run an earlier step (asserted by call counters on the preflight, drain, `DeleteByUser`, `Pseudonymize`, and `Verify` fakes).
- **T8.2 (tier 1).** A drain error fails the job with failure phase `store_delete` and leaves the restriction set.
- **T8.3 (tier 1).** A receipt error leaves the job in progress; the next `RunPending` re-runs no eraser, and the receipt counts equal the first attempt's.
- **T8.4 (tier 1).** A no-op re-run of `store_deleting` does not lower any per-store count.
- **T8.5 (tier 1).** The handler answers `202` with the existing id on a duplicate erase, and the retry handler answers `409 INVALID_STATE_TRANSITION` on `*ErrActiveJob` and, when the user has a legally-held session, answers `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` without calling `Restart` or the drain fake.
- **T8.6 (tier 4).** The erasure journey in `tests/tier4_integration/erasure_job_test.go` runs with a short `--tenant-deletion-interval`, and two gateway replicas against one Postgres resume a job after the replica running it is stopped.
- **T8.7 (tier 8).** A crash between the receipt and `Complete` yields a byte-identical second receipt and a completed job with the restriction cleared.
- **T8.8 (tier 1).** After an operator clear of a job that failed in `verifying`, a retry followed by `RunPending` calls the drain before `DeleteByUser`, the drain terminates a session created while the restriction was cleared, and `Pseudonymize` runs again before `Verify`.
- Retarget `pkg/gateway/externalapi/admin/erasure_test.go` and `erasure_retry_test.go` to drive `Runner.RunPending` or the store directly instead of polling a goroutine.

### Tests that land with CODE-8

- **T9.1 (tier 1).** `TestOpenResponsesDelete` as CODE-8 item 2 states.
- **T9.2 (tier 3).** Extend `tests/tier3_contract/rest_sessions/openapi_document_test.go` to assert that `/v1/responses/{id}` has no `delete` operation.
- **T9.3 (tier 4).** The rewritten cross-tenant test as CODE-8 item 3 states.

### TEST-1 · Cross-cutting lifecycle scenarios

These scenarios exercise the deliverables together and land after the code steps.

- **X1 (tier 7a).** Randomized interleavings under `-race` of user erasure, tenant suspension, tenant resume, and tenant-deletion Phase 1 and Phase 2 against concurrent creates on every create path and concurrent terminal writes. After each scenario the checker asserts: no session of a fenced scope is live after its drain succeeded; no refused create left a row; exactly one `tenant.suspended` row with `already_suspended: false` per marker transition; and exactly one in-progress erasure job per user.
- **X2 (tier 8).** Fault injection at each step: a store error in the drain listing, in one `ForceTerminate`, in `Start`, in each erasure phase, and in the receipt; context cancel on the suspend request; a crash between erasure phases; and replica takeover of the lifecycle tick. Each case asserts the restriction stays set until completion, the tenant-deletion job stays in Phase 2 until the drain succeeds, and a resumed job re-runs no completed step.
- **X3 (tier 4).** Extend `tests/tier4_integration/tenant_deletion_phase4_test.go` with a Phase 2 drain of live sessions before Phase 4. This advances T-12.8.9 and does not close it.
- **X4 (tier 5).** On Kind, suspend a tenant with sessions bound on two gateway replicas, assert every session reaches a terminal state, every pod returns to the pool within the Sweeper interval plus `maxWorkspaceSealDurationSeconds`, and a create after the suspend is refused with `TENANT_SUSPENDED`.
- **X5 (tier 9).** New `tests/tier9_security/erasure_isolation_test.go` against the Postgres erasure job store: a `globex` tenant-admin receives `404` on get, retry, and clear-processing-restriction for an `acme` job, and the store's all-tenants `Get` returns no field of the job to that caller. Against a live gateway with real auth, `POST /v1/sessions`, `POST /v1/sessions/start`, MCP `create_session`, and `delegate_task` each refuse under each marker; the full path-by-marker cross-product stays at tier 1. `DELETE /v1/responses/{id}` returns `405`.

## Edge cases and accepted failure modes

1. **Erasure drains run sequentially inside the shared tick.** A long drain delays tenant-deletion progress and other erasure jobs. Each job's run is bounded by its remaining SLA deadline (CODE-7 item 4), and the tier-4 and tier-8 runs log the drain time.
2. **Drained sessions are recorded as runtime failures.** The §24.11 transition writes `failed` with `FailureClassRuntime` and `FORCE_TERMINATED`, so failure metrics count lifecycle drains.
3. **A refused derive-failure row.** It is counted under the existing `error` outcome of the derive metric.
4. **The tenant-deletion job store stays in memory.** It is rebuilt from `TenantState` on restart, and a Phase 2 that had not completed resumes there.

## Files touched on application (non-spec)

- `pkg/gateway/session/sessionstore/sessionstore.go`, `pkg/gateway/session/sessionstore/pgstore/pgstore.go`, `pkg/gateway/session/sessionstore/memstore/memstore.go`
- `pkg/gateway/sessionserver/create.go`, `create_and_start.go`, `derive.go`, `replay.go`, `user_gate.go`
- `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/environment/translator/open_responses.go`, `pkg/gateway/environment/translator/open_responses_test.go`
- `pkg/gateway/mcpfabric/mcptools/mcptools.go`, `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`
- `pkg/gateway/externalapi/errorclassify/errorclassify.go`, `pkg/gateway/externalapi/openapi/openapi.json`
- `pkg/gateway/externalapi/admin/sessions.go`, `tenant_suspend.go`, `erasure.go`, and the file holding `DrainSessions`
- `pkg/controller/tenantdeletion/controller.go`, `pkg/controller/tenantdeletion/lifecycle.go`
- `pkg/gateway/storage/erasurejob/erasurejob.go`, `runner.go`, `sampler.go`, and the new `pkg/gateway/storage/erasurejob/pgstore/`
- `pkg/gateway/environment/userstore/pgstore/pgstore.go`
- `cmd/lenny-gateway/stores.go`, `adminrouter.go`, `session_admin.go`, `tenantdeletion.go`
- `migrations/0186_erasure_jobs_detail.up.sql`, `migrations/0186_erasure_jobs_detail.down.sql`
- Tests: the existing test files the Testing section retargets, `tests/tier2_component/migrations/prod_columns_test.go`, `tests/tier3_contract/rest_mcp_consistency/error_matrix_test.go`, `tests/tier3_contract/rest_sessions/openapi_document_test.go`, `tests/tier4_integration/openai_cross_tenant_test.go`, `tests/tier4_integration/erasure_job_test.go`, `tests/tier4_integration/tenant_deletion_phase4_test.go`, and `tests/tier9_security/erasure_isolation_test.go`
- Docs: `docs/operator-guide/multi-tenancy.md`, `docs/reference/error-catalog.md`, `docs/api/index.md`, `docs/api/mcp.md`, `docs/api/rest.md`, `docs/operator-guide/security.md`, `docs/api/admin.md`
- Records: `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md`, and, only if a claim row is added, `scripts/seed-claim-register.py` and `tests/claim-map.json`
