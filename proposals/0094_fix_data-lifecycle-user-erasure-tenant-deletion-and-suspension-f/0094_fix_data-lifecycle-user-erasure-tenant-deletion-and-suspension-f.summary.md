# Summary: Lifecycle fence and session drain for user erasure, tenant deletion, and tenant suspension, with a durable erasure job

## Summary

**Problem statement.** User erasure, tenant deletion, and tenant suspension each act on a scope, which is one user of a tenant or a whole tenant. None of them reliably stops new sessions in the scope or brings the scope's live sessions to a terminal state before it acts. The creation checks are plain reads that run before the insert, several creation paths skip the tenant check, and two fallback creators run no check. Tenant-deletion Phase 2 does nothing, user erasure hard-deletes live session rows, and the suspend drain lists once, stops at the first error, discards that error, and cannot be retried. The erasure job lives in memory on one replica, so the database-level Article 18 trigger has no rows to act on, and the restriction write and clear discard their errors. `DELETE /v1/responses/{id}`, which the spec does not define, lets any caller hard-delete any session in its tenant. The spec says erasure lets in-flight sessions complete naturally, defines no atomic creation fence or primary-read drain, and leaves two emitted error codes uncatalogued.

**What changes.**
- Session store and every creation path: a creation fence inside the session insert transaction, a fenced session reservation for the paths that write data before their insert, and matching early checks and refusal mapping at each create site and at delegation, as the non-spec **Creation-path table** states (SPEC-1, MIG-2, CODE-1, CODE-2).
- One shared drain over a primary listing with a tenant-scoped forced termination, used by tenant suspension, tenant-deletion Phase 2, and user erasure (SPEC-1, SPEC-3, SPEC-4, CODE-3, CODE-4, CODE-5).
- Erasure job: a Postgres store over the migration 0042 table with a transactional start and completion, run from the existing advisory-locked lifecycle tick with phase dispatch, and a §12.8 step 0 that states the legal-hold preflight as a handler check with a runner re-check (SPEC-2, MIG-1, CODE-6, CODE-7).
- §15.1, §15.2.1, §16.5, and §16.7: the suspend row's repeat and response contract, the clear-restriction row's `failed`-job and other-job rules, the erase row's pointer to the in-progress-job rule, catalog rows for `TENANT_NOT_ACTIVE` and `USER_INVALIDATED`, both codes in rule 5, the reservation-ended case in the `ERASURE_IN_PROGRESS` row, the retry endpoint in the `ERASURE_BLOCKED_BY_LEGAL_HOLD` row, the §15.1 retry row, and the `gdpr.erasure_blocked_by_hold` audit entry, and the `ErasureJobFailed` legal-hold clause (SPEC-4).
- Removal of `DELETE /v1/responses/{id}` (CODE-8).
- Reader documentation and records (DOCS-1, REC-1).

**Decisions.**
- D1. The fence reads the tenant row and the owning user's row with `SELECT ... FOR SHARE` inside `pgstore.Create`'s transaction, before the insert. A post-insert re-check leaves a refused row visible to a concurrent drain, which can then double-release the claim and emit terminal events for a row that is deleted. `FOR KEY SHARE` does not conflict with the plain `UPDATE` that `SoftDelete` issues.
- D2. A tenant's row, its users' rows, and its session rows are on one Postgres instance, because the fence reads the first two in the transaction that inserts the third. SPEC-1 states this as a placement rule. The tree meets it: `cmd/lenny-gateway/stores.go` builds the session, tenant, and user stores over one pool, and the §12.8 **Data residency** storage routing is not built for those stores (REC-1 item 13). A router that splits them fails the tenant half closed through the `tenant_id` foreign keys of `sessions` and `users`, and it would fail the user half open, so SPEC-1 requires such a router to replace the fence before it is deployed. No deployed path splits them, so no claim-register row is added.
- D3. One predicate, `CreationMarkers.Refusal`, serves the store fence, every creation gate, and every reservation, evaluated on the row's owner. Each path's gates are the **Gates** cell of its row in the non-spec **Creation-path table**, C6 and C7 have no gate, and C10 has only the caller check, because none of them writes session data before its insert and the fence answers with the same code.
- D4. The predicate follows §11.4 per level, and a delegated child is a row with `DelegationDepth > 0`, as the **Creation-path table** column rules state. `ParentSessionID` is not the classifier, because derive and replay set it, which would admit a soft-disabled owner's derive or replay at the fence. Tenant markers and `processing_restricted` refuse every insert, terminal derive-failure rows included (row C9).
- D5. The fence's refusal precedence and the order of each path's gates are the **Gates** column rule of the **Creation-path table**. Every gate evaluates `Refusal` over the tenant row and the owner's row in one call, so a gate and the fence answer the same code; separate tenant and owner checks in sequence answered `TENANT_SUSPENDED` where the fence answers `ERASURE_IN_PROGRESS`. A missing user row admits, and a marker read error fails closed.
- D6. The drain reuses the §24.11 forced transition unchanged (`failed`, `FailureClassRuntime`, `FORCE_TERMINATED`). The spec names no lifecycle termination cause, the Phase 2 row says force-terminate, and suspension already records `FORCE_TERMINATED`.
- D7. `SessionAdmin.ForceTerminate` takes the tenant and drops its replica `GetByID`, which can miss a row the drain just listed from the primary. One method serves both the §24.11 handler and the drain.
- D8. The drain makes one pass, continues past per-session errors, and verifies with a second primary listing. The fence fixes the scope's set once the marker commits, and a repeat-until-empty loop could run without bound and terminate sessions created after a concurrent resume.
- D9. A successful drain proves only that every scoped row is terminal on the primary. A session bound on another replica releases its pod later through that replica's Sweeper. This residual is filed as a P2-owned finding, and this proposal adds no quiesce mechanism.
- D10. User erasure drains the user's sessions before any eraser (SG-1). A wait cannot meet the T4 one-hour SLA when `maxSessionAge` defaults to 7200 seconds. This replaces the §12.8 "complete naturally" sentence. Confirmed 2026-10-16 under the owner's standing delegation.
- D11. Phase 2 is narrowed to the drain. No session-level graceful-shutdown signal is defined, the 5-minute timeout knob was never implemented, and drained sessions still run their terminal pipeline. A drain failure keeps the job in Phase 2 and the next tick retries, because `Reconciler.Retry` has no production caller and a failed job is terminal. Confirmed 2026-10-16 under the owner's standing delegation.
- D12. The Phase 1 row keeps its API-key issuance and user sign-up requirement. Only the session clause cites the fence. The missing issuance gate is filed for a later fix, because Phase 3.5 can hold a tenant in `deleting` after Phase 3 revoked its tokens.
- D13. Lifecycle listings read the primary through one new field, `ListFilter.Primary`. §12.3 is not amended, because SPEC-1 states the primary-read requirement and §12.3's replica routing is a recommendation for read-heavy queries.
- D14. The erasure job store is Postgres over migration 0042 with one jsonb `detail` column and a partial unique index allowing one in-progress job per user. Without the index, the 0042 trigger rejects one job's completion while a second job for the user is in progress.
- D15. An erase request follows rows E1 and E2 of the non-spec **Erasure entry-point table**. `Start` writes the job and the restriction in one transaction, because a separate flag write can strand a job with no restriction.
- D16. The existing advisory-locked tenant-deletion tick is the single erasure executor, resuming each job from its persisted phase. §12.8 already requires crash resume, and a per-handler goroutine can run one job on two replicas.
- D17. The completion receipt is written first, and one transaction then marks the job completed and clears the restriction where `erasure_job_id` matches. Audit can live on a separate pool, so the receipt cannot share the transaction in every deployment. Writing it first means the job never lifts the restriction without a receipt, at the cost of a possible identical duplicate receipt.
- D18. Suspension commits the marker under the row lock, emits `tenant.suspended` on a detached context for every request, recording whether the marker was already set, and then drains. A repeat suspend re-drains and is audited, because its drain terminates sessions and §11.8 audits every admin operation. An incomplete drain answers `200` with `failedSessions`, following the §11.4 full-revoke pattern of reporting partial failure in the body. A listing failure answers `500 INTERNAL_ERROR`.
- D19. `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` are catalogued and added to rule 5. Both are already on the wire, and today MCP classifies them as transient and retryable while REST does not.
- D20. Scope is piece A without the full_revoke fixes, plus piece D. Pieces B and C and the full_revoke fixes go to a follow-on proposal, P5b, which lands after P1 (0093): the full_revoke fixes edit the function P1 changes, and piece B's Phase 3 revocation depends on P1's lease reclaimer. The fence closes the creation-race half of F-11.4.14. Confirmed 2026-10-16 under the owner's standing delegation.
- D21. F-12.1.3 (the `LeaseStore` step-1 no-op) holds as an invariant once the drain precedes the erasers, and F-12.2.16 (the step-3 Redis caches) stands. F-12.8.20 is reopened and closed by the durable store, F-12.8.36 is absorbed, and the `tenant.suspended` half of F-11.7.22 moves from P2 to this proposal.
- D22. The owner's triage rule governs failures the tests find, as non-spec Testing **Failures the tests find.** states.
- D23. An erasure retry follows row E3, and crash resume follows row E4. The retry restarts at `initiated` because after an operator clear only a restart from `initiated` drains and erases what the cleared window admitted.
- D24. The runner re-checks legal holds as row E6 states, because a hold set between the handler preflight and the first eraser would otherwise be destroyed, and the owner's standing preference on compliance paths is fail-closed.
- D25. A path that writes data keyed to a new session ID before its insert (rows C8 and C11) reserves the ID for the owner under the fence and inserts through `CreateReserved`, which refuses once the reservation is gone, and erasure of the owner reaches reserved IDs. A crash between a fence refusal and the undo, or during a copy, otherwise leaves data no eraser reaches, because erasers enumerate the owner's session rows. The only residual is a gateway crash between the end of the owner's erasers and the undo of an in-flight write (REC-1 item 11). Decided 2026-10-16 under the owner's standing delegation.
- D26. The operator clear follows row E5: it refuses a job that is not `failed` and a flag that records another job, because a clear during an in-progress job reopens the fence after the drain, and §15.1 scopes the clear to a failed job. Decided 2026-10-16 under the owner's standing delegation.

**Watch out for.**
- `FOR KEY SHARE` looks sufficient and is not: it lets `userstore.SoftDelete` and `tenantstore.SoftDelete` race the insert. T1.4's `SoftDelete` cases are the discriminating assertions.
- The caller check reads the principal, while the fence reads the row's owner, which the **Owner** column of the **Creation-path table** names per row. A gate keyed on the principal admits a create the fence then refuses after a pod claim.
- The **Creation-path table** is the only rule site for the fence's per-path behavior. Change a row first and regenerate from it the spec edits in the map of spec sites below the table, CODE-1, CODE-2, T2.1 to T2.3, and X1.
- Undo and `ReleaseReservation` run only on a `*CreationRefusal`. A non-refusal `Create` error can follow a committed insert, so it deletes nothing and keeps the reservation.
- Every `store.Create` error branch maps errors to a retryable 5xx today. Without the `errors.As(*CreationRefusal)` branch, a fence refusal reaches the client as `503` or `500`.
- `errorclassify` lacks both new codes. The tier-3 parity test cannot pass until CODE-2 item 7 lands.
- A drain that succeeded has not released pods bound on other replicas. Do not describe it as completing the terminal pipeline.
- `tenantdeletion.Reconciler.fail` makes a job terminal, and nothing in production calls `Retry`. A Phase 2 drain error must stay in phase.
- `Runner.Run` today re-runs every step from the top. Once a receipt error keeps a job in progress, a re-run without phase dispatch and `ErasedAt` zeroes the recorded counts and re-pseudonymizes with a fresh salt.
- A unique violation aborts the Postgres transaction. Use `INSERT ... ON CONFLICT ... DO NOTHING` and read the existing job in the same transaction.
- `ListActive` stops returning finished jobs, so the sampler must clear their age series itself.
- The 0042 trigger rejects a clear while any in-progress job exists for the user. `Complete` marks the job completed before it clears, in the same transaction.
- `ClearProcessingRestriction` sets a session-local trigger bypass. The shared users helper must not set it.
- ServeMux answers a removed method with a plain-text `405`, so the tier-4 assertion on the OpenAI error envelope must go.
- Row E6's override exemption depends on `PhaseLog`. Keep the condition as the row states it.
- A tick-budget expiry inside a phase is not a job failure. Treating the budget's `context.DeadlineExceeded` as one fails every long erasure terminally (CODE-7 item 4).
- `heldResourcesForUser` lists every tenant session from the read pool today. The exported enumeration lists the user's sessions from the primary, or a hold set just before the request can be missed.
- `scripts/lint-migrations.sh` rule 3 fails tier 0 unless a tier-2 test names migration 0186. The migration number is provisional.
- `tests/claim-map.json` is generated by `scripts/seed-claim-register.py` and is never hand-edited.

## Goals

- Every session insert in a fenced scope is refused atomically with the insert, on every creation path, with the refusal surfaced as the path's row of the non-spec **Creation-path table** states.
- Erasure of a user reaches every session ID a creation path wrote data under for that user, whether or not the session row was inserted.
- Tenant suspension, tenant-deletion Phase 2, and user erasure each drain their scope through one shared drain that reads the primary, continues past per-session errors, verifies completion, and can be re-run.
- The erasure job persists in Postgres, resumes after a crash from its persisted phase on any replica, and sets and clears the restriction transactionally with its own record.
- The erasure job never lifts the restriction before its completion receipt is written.
- `DELETE /v1/responses/{id}` no longer exists.

## Non-goals

- A post-insert marker re-check with a compensating delete. It leaves a refused row visible to a concurrent drain (D1).
- A `BEFORE INSERT` trigger on sessions, users, and issued tokens. It needs SQLSTATE-to-code parsing, gives `memstore` no coverage, and the Go read inside `pgstore.Create`'s transaction gives the same atomicity.
- `FOR KEY SHARE` as the lock mode (D1).
- A conditional `INSERT ... SELECT` without a row lock, `SERIALIZABLE` isolation for creates, or per-scope advisory locks on every insert and marker writer. The first does not serialize with the marker `UPDATE` under `READ COMMITTED`, the second puts serialization retries on the create path, and the third requires every future writer to remember the lock.
- A gating decorator around `sessionstore.Store`. It hides optional capabilities that `sessionstore.ReadPrimary` type-asserts.
- Fixing the gap with `requireTenantState` calls alone. A read before the insert stays racy, and the fallback creators run no handler gate.
- Exempting rows created terminal from the fence (D4).
- A reason and class parameter on `ForceTerminate`, ending drained sessions as `cancelled`, or a new terminated-reason vocabulary such as `TENANT_SUSPENDED` on the session row (D6).
- A drain loop that repeats until the listing is empty (D8), and a per-pass "scope still fenced" read. The resume-overlap race that read closes is bounded by one listing pass.
- A quiesce predicate over `terminated_at` plus `maxWorkspaceSealDuration`, or a grace window with a new flag (D9).
- Waiting for erasure-scope sessions to finish with a forced deadline fallback (D10).
- Keeping Phase 2's graceful-shutdown signal and 5-minute wait, including reusing `session_expiring_soon` as the signal or persisting a Phase 2 start time (D11).
- A separate Phase 4 "no live session" precondition. Phase 2 completes only on a verified drain, and the fence holds from Phase 1.
- A tenant-deletion retry endpoint. A failed Phase 2 drain stays in phase (D11).
- A background sweep that re-drains every suspended tenant. The repeat suspend restores the missing retry path, and the marker blocks creation durably.
- A suspend handler that returns before draining. A synchronous drain is smaller than a background executor plus a progress surface.
- Separate `remainingSessions` and `drainFailures` response fields. `failedSessions` carries both lists (D18).
- A `NonTerminal` or `LiveOnly` list filter, an `UnsettledSince` filter, a `ListPrimary` method, or a `PrimaryLister` interface. One boolean on `ListFilter` covers the need (D13).
- A §12.3 lifecycle exception sentence (the dropped "§12.3 Read replicas: lifecycle exception" edit). SPEC-1 already requires the primary listing, the fence's locking read cannot run on a standby, and an exception specific to lifecycle operations would state one rule in two sections (D13).
- A new drain package. The existing drain is extended in package `admin`.
- A durable drain record or table. Session rows are the progress record.
- Concurrent drain workers with a new concurrency flag. The drain stays sequential.
- Re-issuing the erase as the recovery path for an interrupted job. §12.8 requires crash resume from the persisted phase (D16). A new erase request with `acknowledgeHoldOverride` is the recovery path only for a job a legal hold blocks (D23).
- A failed job record for a request the legal-hold preflight blocks. The preflight is a handler check that writes nothing (rows E1 to E3).
- Accepting the derive crash residue as a recorded residual. Erasure could not reach it (D25).
- A derive-intent table separate from the reservation, or reserving every session ID. Only C8 and C11 write before their insert (D25).
- Classifying delegated children by `ParentSessionID` (D4).
- A `tenant.suspended` entry in §16.7. That list holds the Section 25 audit events, and the §15.1 suspend row names no event (Edit 4a).
- A `legal_hold_preflight` failure label, or a `gdpr.erasure_blocked_by_hold` event emitted by the runner (D24).
- A separate erasure loop with its own advisory key, per-job advisory locks, Kubernetes Lease election, or an immediate goroutine kick from the handler (D16).
- Restriction-first ordering, or job-first ordering with a checked flag write (D15).
- Binding the receipt into the completion transaction with `auditstore.AppendInTx`, writing the receipt after the completion commit, or exactly-once receipts through an audit existence check or an outbox column (D17).
- A new `draining` erasure phase value. It would change the 0042 check constraint and the trigger's in-progress set, and `initiated` covers the step.
- One column per `Job` field (D14).
- Moving the tenant-deletion job store to Postgres. The job is rebuilt from `TenantState`.
- Answering a duplicate erase with `409 ERASURE_IN_PROGRESS`. That code is a session-creation rejection, and returning the existing job's id is idempotent (D15).
- Gating `POST /v1/admin/users` and `user_bearer` mints on `POST /v1/oauth/token` on the tenant state in this proposal (D12).
- Wiring the Phase 3 revoker and renaming `RevokeByTenant`. That is piece B, owned by P5b, and lease invalidation depends on P1's reclaimer.
- Pieces B and C, and the full_revoke fixes, which P5b owns (D20).
- Narrowing session `DeleteByUser` and `DeleteByTenant` to terminal rows. The fence and the drain already make live rows absent at eraser time.
- An owner, state, and kind check on `DELETE /v1/responses/{id}`, or a spec clause defining a native `405`. The route has no spec basis, so removal needs no spec edit.
- `ON DELETE SET NULL` on `audit_redaction_receipts.audit_event_id` (piece C).
- Renaming the `--tenant-deletion-interval` flag.
- Handling every member in the direct-fix lane. SG-1, the Phase 2 narrowing, and the fence paragraph are normative spec changes, and the fence and the drain ship together.
- Erasing the user registry row during user erasure. It holds the invalidation tombstone and the restriction anchor, and the decision belongs to P5b.
- Edits to the §11.4 full-revoke note (P5b).

## Open decisions for human to make

The spec loop routed each decision below to the owner and derived no recommendation for any of them. The open-decisions-and-impact-review phase supplies the recommendations.

- **OD1. Terminal-tail residual after a drain.** After a drain succeeds, a session bound on another gateway replica can still be running its terminal steps (workspace seal and pod release) when the user's erasers or the next tenant-deletion phase start. The problem statement asked the spec to state that erasers run only after those steps finish. The staging instead states the residual in the SPEC-1 *Drain* text, and REC-1 item 6 files it as a P2 finding (D9). Is that sentence sufficient as the spec-side record of the departure, and should a claim-register row also record it? REC-1 item 8 adds the row only if the owner asks for it. Source: review log [spec.1.review-fresh.1].
- **OD2. Catalog row for `ERASURE_JOB_NOT_FAILED`.** The erasure-job retry endpoint already answers `409 ERASURE_JOB_NOT_FAILED` for a job that is not `failed`, and this proposal makes the clear-processing-restriction endpoint a second emitter (row E5). Edit 4i states that rejection without naming the code, and the §15.1 error catalog has no row for it. Should this proposal add the catalog row, or leave the gap to the unassigned finding that REC-1 item 14 files? The staged default is the REC-1 finding. Source: review log [spec.9.review-client-surface.1].
- **OD3. `ERASURE_IN_PROGRESS` on interaction resolution.** The gateway answers `403 ERASURE_IN_PROGRESS` on the interaction-resolution endpoints for a user under erasure (`requireActiveUserForAction`, which CODE-2 item 1 keeps unchanged). The §15.1 catalog row for that code names only session creation, and the §12.8 Article 18 paragraph does not say whether interaction resolution is restricted. Edit 4c already names interaction resolution for `USER_INVALIDATED`. Should the spec state that a user under erasure cannot resolve interactions, which adds the path to the `ERASURE_IN_PROGRESS` row, or should the code stop refusing it? The behavior predates this proposal. Source: review log [spec.11.review-client-surface.1].
- **OD4. Derive and replay of a restricted user's session into another owner.** A caller authorized on a session whose owner is under erasure, such as a tenant-admin, can derive or replay that session into a session owned by a different user. The copy holds the restricted user's workspace, and that user's erasure never reaches it, because the creation fence checks only the new session's owner. Should the Article 18 restriction also refuse a derive or replay whose source session's owner has `processing_restricted` set, in this proposal or in P5b? The current Article 18 text has the same scope, so the gap predates this proposal. Source: review log [spec.13.review-security.1].

## Defects in the shipped tree that this proposal does not stage

- **Follow-on proposal P5b owns pieces B and C and the full_revoke fixes (D20).** It lands after P1 (0093). Its members are F-11.4.12, F-11.4.13, the F-11.4.14 remainder (the read-pool single listing), F-12.8.25, F-12.8.30, F-12.8.34, F-12.8.35, F-12.8.37, N-3, N-4 (the §12.8 step 1 and step 3 text that still names a release and a flush), the `coordination_lease` mirror rows, and the user registry row.
- **Phase 3 revokes nothing (F-12.8.30).** No `Revoker` is wired and no `RevokeByTenant` exists. P5b owns it, and it depends on P1's lease reclaimer.
- **Phase 4 and user erasure omit stores (F-12.8.34, F-12.8.35, F-12.8.37, N-3, the `coordination_lease` mirror).** P5b wires the missing erasers from the store table.
- **The audit-receipt foreign key blocks an audit eraser (F-12.8.25).** P5b owns it as piece C.
- **full_revoke runs on the request context, stops at the first error, lists once from the read pool, and reports no counts (F-11.4.12, F-11.4.13, F-11.4.14 listing half).** P5b owns it, because it edits the function P1 changes.
- **The legal-hold preflight covers less than §12.8 step 0 names.** It enumerates session holds and artifact holds and skips the audit-range and workspace-snapshot holds of the legal-hold ledger. The blocked event's tuples carry `resourceType` and `resourceId` without `holdSetAt`, `holdSetBy`, and `note`, and the `409` details carry `holds` and `heldSessions` without `userId` and `holdCount`. Edit 2j keeps step 0's hold list unchanged, and Edit 4f keeps the event's tuple list unchanged. REC-1 item 10 files the divergence.
- **Phase 1 does not refuse API-key issuance or user sign-ups.** No gate on `POST /v1/oauth/token` checks the tenant state. REC-1 files it unassigned.
- **A remote-bound session's pod release can trail the drain.** P2 owns the terminal pipeline. REC-1 files it.
- **`GET /v1/responses/{id}` has no user check (F-15.1.54).** It is an authorization fix outside the data-lifecycle boundary.
- **The gateway runs the erasure job as `lenny_app`.** §11.7 item 7 says only the erasure job connects as `lenny_erasure` and that gateway replicas never hold its credentials. The divergence predates this proposal and is recorded here rather than written into §12.8.
- **The `lenny_erasure` trigger exemption is now unused by the completion path.** Retiring it is a follow-on.
- **§12.8 Data residency storage routing is not built for the session, tenant, and user stores (REC-1 item 13).** All three share one pool, which the fence's placement rule permits.
- **`ERASURE_JOB_NOT_FAILED` has no §15.1 catalog row (REC-1 item 14).** Rows E3 and E5 answer it.
- **`CountActiveDelegatedChildrenByUser` counts derived and replayed sessions as delegated children.** It filters on `parent_session_id`, which derive and replay set. The §11.1 per-user cap is outside this proposal.
- **`session_tree_archive.root_session_id` references `sessions(id)` with no `ON DELETE` action.** The drain removes the live-row case. The foreign key itself belongs to P5b's store review.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0093 (P1, release ownership) | Draft, only in the `draft-0093` worktree | CODE-3 changes `SessionAdmin.ForceTerminate` and `sessionAdminAdapter.ForceTerminate` to take the tenant, and the drain becomes a caller of the forced transition. P5b, which carries the full_revoke fixes and reuses the drain, lands after 0093. | Stage any change to `cmd/lenny-gateway/session_admin.go` against the new signature, and treat `admin.DrainSessions` as a caller of whatever release path 0093 defines. |

## Deliverable index

- SPEC-1 — `spec/12_storage-architecture.md` and `spec/16_observability.md` — the new §12.8 **Lifecycle fence and drain** paragraph with the reservation and the placement rule, the §12.6 and §16.5 swap-scope edits, and the §16.1 derive-audit `error` outcome.
- SPEC-2 — `spec/12_storage-architecture.md` and `spec/24_lenny-ctl-command-reference.md` — the §12.8 Article 18, invocation, phase-field, step-0, and override-action edits for the one-transaction start, the drain step, the single in-progress job, the legal-hold preflight and re-check, the completion order, and the missing-justification code, and the §24.12 retry row.
- SPEC-3 — `spec/12_storage-architecture.md` — the tenant deletion Phase 1 and Phase 2 rows.
- SPEC-4 — `spec/15_external-api-surface.md` and `spec/16_observability.md` — the §15.1 suspend, retry, clear-restriction, and erase rows, the `TENANT_NOT_ACTIVE`, `USER_INVALIDATED`, `ERASURE_IN_PROGRESS`, and `ERASURE_BLOCKED_BY_LEGAL_HOLD` catalog rows, the §15.2.1 rule 5 list, the §16.5 `ErasureJobFailed` row, and the §16.7 `gdpr.erasure_blocked_by_hold` entry.
- CODE-1 — `pkg/gateway/session/sessionstore/`, the `Store` test doubles under `pkg/gateway/mcpfabric/delegation/` and `pkg/gateway/mcpfabric/mcptools/`, and `cmd/lenny-gateway/stores.go` — the creation predicate, the fence in `pgstore.Create` and `memstore.Create`, and the session reservation.
- CODE-2 — `pkg/gateway/sessionserver/`, `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/mcpfabric/delegation/service.go`, `pkg/gateway/mcpfabric/mcptools/`, `cmd/lenny-gateway/mcpsurface.go`, and `pkg/gateway/externalapi/errorclassify/errorclassify.go` — the creation gates, the reservations and undo, the refusal mapping at every create site, and the classifier entries.
- CODE-3 — `pkg/gateway/session/sessionstore/`, `cmd/lenny-gateway/session_admin.go`, and `pkg/gateway/externalapi/admin/` — `ListFilter.Primary`, the tenant-scoped `ForceTerminate`, and `DrainSessions`.
- CODE-4 — `pkg/gateway/externalapi/admin/tenant_suspend.go` — the suspend handler's locked newness, detached audit, repeat drain, and response.
- CODE-5 — `cmd/lenny-gateway/adminrouter.go`, `cmd/lenny-gateway/tenantdeletion.go`, and `pkg/controller/tenantdeletion/` — the wired Phase 2 terminator and the in-phase retry.
- CODE-6 — `pkg/gateway/storage/erasurejob/`, `pkg/gateway/environment/userstore/`, and `cmd/lenny-gateway/adminrouter.go` — the Postgres erasure job store, the shared users restriction helper, and the sampler change.
- CODE-7 — `pkg/gateway/storage/erasurejob/runner.go`, `pkg/gateway/externalapi/admin/erasure.go`, `pkg/gateway/externalapi/admin/legalhold.go`, `cmd/lenny-gateway/flags.go`, `cmd/lenny-gateway/tenantdeletion.go`, and `cmd/lenny-gateway/adminrouter.go` — phase dispatch, the drain step and the hold re-check, receipt then completion, execution in the lifecycle tick under a per-tick budget, and the erase, retry, and clear handlers.
- CODE-8 — `pkg/gateway/environment/translator/open_responses.go`, `pkg/gateway/externalapi/openapi/openapi.json`, and their tests — removal of `DELETE /v1/responses/{id}`.
- MIG-1 — `migrations/0186_erasure_jobs_detail.*.sql` and `tests/tier2_component/migrations/prod_columns_test.go` — the `detail` column and the in-progress-per-user index.
- MIG-2 — `migrations/0187_session_reservations.*.sql` and `tests/tier2_component/migrations/session_reservations_test.go` — the `session_reservations` table.
- DOCS-1 — `docs/operator-guide/multi-tenancy.md`, `docs/operator-guide/security.md`, `docs/reference/error-catalog.md`, `docs/api/index.md`, `docs/api/mcp.md`, `docs/api/rest.md`, and `docs/api/admin.md` — the reader pages for the phase table, the error codes, the erasure drain and job, and the suspend response.
- TEST-1 — `tests/` — the cross-cutting lifecycle scenarios at tiers 4, 5, 7a, 8, and 9.
- REC-1 — `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md`, and, only if a claim row is added, `scripts/seed-claim-register.py` and `tests/claim-map.json` — finding closures, new findings, and the P5 bullet.
