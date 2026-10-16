# Summary: Lifecycle fence and session drain for user erasure, tenant deletion, and tenant suspension, with a durable erasure job

## Summary

**Problem statement.** User erasure, tenant deletion, and tenant suspension each act on a scope, which is one user of a tenant or a whole tenant. None of them reliably stops new sessions in the scope or brings the scope's live sessions to a terminal state before it acts. The creation checks are plain reads that run before the insert, several creation paths skip the tenant check, and two fallback creators run no check. Tenant-deletion Phase 2 does nothing, user erasure hard-deletes live session rows, and the suspend drain lists once, stops at the first error, discards that error, and cannot be retried. The erasure job lives in memory on one replica, so the database-level Article 18 trigger has no rows to act on, and the restriction write and clear discard their errors. `DELETE /v1/responses/{id}`, which the spec does not define, lets any caller hard-delete any session in its tenant. The spec says erasure lets in-flight sessions complete naturally, defines no atomic creation fence or primary-read drain, and leaves two emitted error codes uncatalogued.

**What changes.**
- Session store and every creation path: a creation fence inside the session insert transaction, with matching early checks and refusal mapping at each create site and at delegation (SPEC-1, CODE-1, CODE-2).
- One shared drain over a primary listing with a tenant-scoped forced termination, used by tenant suspension, tenant-deletion Phase 2, and user erasure (SPEC-1, SPEC-3, SPEC-4, CODE-3, CODE-4, CODE-5).
- Erasure job: a Postgres store over the migration 0042 table with a transactional start and completion, run from the existing advisory-locked lifecycle tick with phase dispatch, and a §12.8 step 0 that states the legal-hold preflight as a handler check with a runner re-check (SPEC-2, MIG-1, CODE-6, CODE-7).
- §15.1, §15.2.1, §16.5, and §16.7: the suspend row's repeat and response contract, catalog rows for `TENANT_NOT_ACTIVE` and `USER_INVALIDATED`, both codes in rule 5, the retry endpoint in the `ERASURE_BLOCKED_BY_LEGAL_HOLD` row, the §15.1 retry row, and the `gdpr.erasure_blocked_by_hold` audit entry, and the `ErasureJobFailed` legal-hold clause (SPEC-4).
- Removal of `DELETE /v1/responses/{id}` (CODE-8).
- Reader documentation and records (DOCS-1, REC-1).

**Decisions.**
- D1. The fence reads the tenant row and the owning user's row with `SELECT ... FOR SHARE` inside `pgstore.Create`'s transaction, before the insert. A post-insert re-check leaves a refused row visible to a concurrent drain, which can then double-release the claim and emit terminal events for a row that is deleted. `FOR KEY SHARE` does not conflict with the plain `UPDATE` that `SoftDelete` issues.
- D2. The fence is valid only while sessions, tenants, and users share one Postgres instance, as they do in v1. SPEC-1 states the dependency so a multi-shard `StoreRouter` has to revisit it.
- D3. One predicate, `CreationMarkers.Refusal`, serves the store fence, the handler pre-gates, and `requireActiveDelegator`, evaluated on the row's owner. §12.8 already requires the restriction upstream of endpoint logic, and without a pre-gate a fenced create would claim a pod and roll it back on every request.
- D4. The predicate follows §11.4 per level: soft disable refuses root sessions and admits delegated children, and hard disable refuses both. Tenant markers and `processing_restricted` refuse every insert, terminal derive-failure rows included, with no exemption.
- D5. Refusal precedence keeps the existing order: `USER_INVALIDATED`, `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`, `TENANT_NOT_ACTIVE`. A missing user row admits, and a marker read error fails closed.
- D6. The drain reuses the §24.11 forced transition unchanged (`failed`, `FailureClassRuntime`, `FORCE_TERMINATED`). The spec names no lifecycle termination cause, the Phase 2 row says force-terminate, and suspension already records `FORCE_TERMINATED`.
- D7. `SessionAdmin.ForceTerminate` takes the tenant and drops its replica `GetByID`, which can miss a row the drain just listed from the primary. One method serves both the §24.11 handler and the drain.
- D8. The drain makes one pass, continues past per-session errors, and verifies with a second primary listing. The fence fixes the scope's set once the marker commits, and a repeat-until-empty loop could run without bound and terminate sessions created after a concurrent resume.
- D9. A successful drain proves only that every scoped row is terminal on the primary. A session bound on another replica releases its pod later through that replica's Sweeper. This residual is filed as a P2-owned finding, and this proposal adds no quiesce mechanism.
- D10. User erasure drains the user's sessions before any eraser (SG-1). A wait cannot meet the T4 one-hour SLA when `maxSessionAge` defaults to 7200 seconds. This replaces the §12.8 "complete naturally" sentence. Confirmed 2026-10-16 under the owner's standing delegation.
- D11. Phase 2 is narrowed to the drain. No session-level graceful-shutdown signal is defined, the 5-minute timeout knob was never implemented, and drained sessions still run their terminal pipeline. A drain failure keeps the job in Phase 2 and the next tick retries, because `Reconciler.Retry` has no production caller and a failed job is terminal. Confirmed 2026-10-16 under the owner's standing delegation.
- D12. The Phase 1 row keeps its API-key issuance and user sign-up requirement. Only the session clause cites the fence. The missing issuance gate is filed for a later fix, because Phase 3.5 can hold a tenant in `deleting` after Phase 3 revoked its tokens.
- D13. Lifecycle listings read the primary through one new field, `ListFilter.Primary`. §12.3 is not amended, because SPEC-1 states the primary-read requirement and §12.3's replica routing is a recommendation for read-heavy queries.
- D14. The erasure job store is Postgres over migration 0042 with one jsonb `detail` column and a partial unique index allowing one in-progress job per user. Without the index, the 0042 trigger rejects one job's completion while a second job for the user is in progress.
- D15. `Start` inserts the job and sets the restriction in one transaction. A separate flag write can strand a job with no restriction. A duplicate erase for a user with no active hold returns the existing job's id with `202`. A duplicate that the preflight admits only through the override answers `409 INVALID_STATE_TRANSITION`, because the in-progress job neither records nor honors an override it was not started with. The legal-hold preflight runs in the handler before `Start` and writes nothing when it blocks, as rows E1 and E2 of the non-spec **Erasure entry-point table** state, so no failed job exists for a blocked erase.
- D16. The existing advisory-locked tenant-deletion tick is the single erasure executor, resuming each job from its persisted phase. §12.8 already requires crash resume, and a per-handler goroutine can run one job on two replicas.
- D17. The completion receipt is written first, and one transaction then marks the job completed and clears the restriction where `erasure_job_id` matches. Audit can live on a separate pool, so the receipt cannot share the transaction in every deployment. Writing it first means the job never lifts the restriction without a receipt, at the cost of a possible identical duplicate receipt.
- D18. Suspension commits the marker under the row lock, emits `tenant.suspended` on a detached context for every request, recording whether the marker was already set, and then drains. A repeat suspend re-drains and is audited, because its drain terminates sessions and §11.8 audits every admin operation. An incomplete drain answers `200` with `failedSessions`, following the §11.4 full-revoke pattern of reporting partial failure in the body. A listing failure answers `500 INTERNAL_ERROR`.
- D19. `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` are catalogued and added to rule 5. Both are already on the wire, and today MCP classifies them as transient and retryable while REST does not.
- D20. Scope is piece A without the full_revoke fixes, plus piece D. Pieces B and C and the full_revoke fixes go to a follow-on proposal, P5b, which lands after P1 (0093): the full_revoke fixes edit the function P1 changes, and piece B's Phase 3 revocation depends on P1's lease reclaimer. The fence closes the creation-race half of F-11.4.14. Confirmed 2026-10-16 under the owner's standing delegation.
- D21. F-12.1.3 (the `LeaseStore` step-1 no-op) holds as an invariant once the drain precedes the erasers, and F-12.2.16 (the step-3 Redis caches) stands. F-12.8.20 is reopened and closed by the durable store, F-12.8.36 is absorbed, and the `tenant.suspended` half of F-11.7.22 moves from P2 to this proposal.
- D22. The owner's triage rule governs failures the tests find, as non-spec Testing **Failures the tests find.** states.
- D23. An erasure retry follows row E3 of the non-spec **Erasure entry-point table**. The retry restarts the job at `initiated` and sets `processing_restricted` again in the same transaction, because after an operator clear the fence admits new sessions and new data into stores whose phase already ran, and only a restart from `initiated` drains and erases them. The legal-hold preflight runs in the retry handler before `Restart`, so a blocked retry leaves the job and the flag unchanged. The retry does not inherit the job's `OverrideReceipt`, because the retry endpoint has no body to carry a fresh justification, and the recovery path for a held user is a new erase request with `acknowledgeHoldOverride`. `Restart` resets `ErasedAt` and `Billing`: a kept `ErasedAt` would skip the drain and the erasers, and a kept billing outcome would skip pseudonymizing billing rows written while the restriction was cleared. Crash resume is a separate path. It continues from the persisted phase, re-runs no earlier step, and is the recovery §12.8 requires for an interrupted job (D16).
- D24. The runner re-checks legal holds after the drain and before the first eraser (row E6). A hold set between the handler preflight and the erasers would otherwise be destroyed, and the owner's standing preference on compliance paths is fail-closed. The re-check fails the job with `failure_phase: store_delete` and leaves the flag set, which is the outcome a drain failure already has (Edit 2d). This keeps step 0's handler framing: the request-time preflight still writes nothing, and the re-check is an ordinary failure of a job whose flag is already set. A distinct `legal_hold_preflight` label would need a CMP-026 enumeration change, a §16.5 clause, and a new code constant for a case the job's `Failure` text already names. The re-check exempts the holds the job's own override lists until the job is first retried. It emits no `gdpr.erasure_blocked_by_hold`, because the tick runs with no request principal and `ErasureJobFailed` already alerts. A hold set after the job enters `store_deleting` is not re-checked.

**Watch out for.**
- `FOR KEY SHARE` looks sufficient and is not: it lets `userstore.SoftDelete` and `tenantstore.SoftDelete` race the insert. T1.4's `SoftDelete` cases are the discriminating assertions.
- The handler gate reads the principal, while the fence reads the row's owner. Replay and the derive-failure row carry the source row's user. A pre-gate keyed on the principal admits a create the fence then refuses after a pod claim.
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
- The runner re-check exempts override holds only while the job's `PhaseLog` records no `failed` transition. Dropping that condition lets a retry inherit an override, which D23 forbids.
- `heldResourcesForUser` lists every tenant session from the read pool today. The exported enumeration lists the user's sessions from the primary, or a hold set just before the request can be missed.
- `scripts/lint-migrations.sh` rule 3 fails tier 0 unless a tier-2 test names migration 0186. The migration number is provisional.
- `tests/claim-map.json` is generated by `scripts/seed-claim-register.py` and is never hand-edited.

## Goals

- Every session insert in a fenced scope is refused atomically with the insert, on every creation path, with the refusal surfaced as a `403` with a catalogued code.
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
- A failed job record for a request the legal-hold preflight blocks. The preflight is a handler check that writes nothing (D15, D24).
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

None yet. The review loops write this section.

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
- **`session_tree_archive.root_session_id` references `sessions(id)` with no `ON DELETE` action.** The drain removes the live-row case. The foreign key itself belongs to P5b's store review.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0093 (P1, release ownership) | Draft, only in the `draft-0093` worktree | CODE-3 changes `SessionAdmin.ForceTerminate` and `sessionAdminAdapter.ForceTerminate` to take the tenant, and the drain becomes a caller of the forced transition. P5b, which carries the full_revoke fixes and reuses the drain, lands after 0093. | Stage any change to `cmd/lenny-gateway/session_admin.go` against the new signature, and treat `admin.DrainSessions` as a caller of whatever release path 0093 defines. |

## Deliverable index

- SPEC-1 — `spec/12_storage-architecture.md` and `spec/16_observability.md` — the new §12.8 **Lifecycle fence and drain** paragraph, the §12.6 and §16.5 swap-scope edits, and the §16.1 derive-audit `error` outcome.
- SPEC-2 — `spec/12_storage-architecture.md` and `spec/24_lenny-ctl-command-reference.md` — the §12.8 Article 18, invocation, phase-field, and step-0 edits for the drain step, the one-transaction start, the single in-progress job, the legal-hold preflight and re-check, and the completion order, and the §24.12 retry row.
- SPEC-3 — `spec/12_storage-architecture.md` — the tenant deletion Phase 1 and Phase 2 rows.
- SPEC-4 — `spec/15_external-api-surface.md` and `spec/16_observability.md` — the §15.1 suspend row, the `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` catalog rows, the retry endpoint in the `ERASURE_BLOCKED_BY_LEGAL_HOLD` row and the §16.7 `gdpr.erasure_blocked_by_hold` entry, the §15.1 retry row, the §16.5 `ErasureJobFailed` row, and the §15.2.1 rule 5 list.
- CODE-1 — `pkg/gateway/session/sessionstore/` and `cmd/lenny-gateway/stores.go` — the creation predicate and the fence in `pgstore.Create` and `memstore.Create`.
- CODE-2 — `pkg/gateway/sessionserver/`, `pkg/gateway/environment/translator/singleshot.go`, `pkg/gateway/mcpfabric/mcptools/`, and `pkg/gateway/externalapi/errorclassify/errorclassify.go` — the pre-gates, the refusal mapping at every create site, and the classifier entries.
- CODE-3 — `pkg/gateway/session/sessionstore/`, `cmd/lenny-gateway/session_admin.go`, and `pkg/gateway/externalapi/admin/` — `ListFilter.Primary`, the tenant-scoped `ForceTerminate`, and `DrainSessions`.
- CODE-4 — `pkg/gateway/externalapi/admin/tenant_suspend.go` — the suspend handler's locked newness, detached audit, repeat drain, and response.
- CODE-5 — `cmd/lenny-gateway/adminrouter.go`, `cmd/lenny-gateway/tenantdeletion.go`, and `pkg/controller/tenantdeletion/` — the wired Phase 2 terminator and the in-phase retry.
- MIG-1 — `migrations/0186_erasure_jobs_detail.*.sql` — the `detail` column and the in-progress-per-user index.
- CODE-6 — `pkg/gateway/storage/erasurejob/`, `pkg/gateway/environment/userstore/pgstore/pgstore.go`, and `cmd/lenny-gateway/adminrouter.go` — the Postgres erasure job store, the shared users restriction helper, and the sampler change.
- CODE-7 — `pkg/gateway/storage/erasurejob/runner.go`, `pkg/gateway/externalapi/admin/erasure.go`, `cmd/lenny-gateway/tenantdeletion.go`, and `cmd/lenny-gateway/adminrouter.go` — phase dispatch, the drain step, receipt then completion, and execution in the lifecycle tick.
- CODE-8 — `pkg/gateway/environment/translator/open_responses.go` and `pkg/gateway/externalapi/openapi/openapi.json` — removal of `DELETE /v1/responses/{id}`.
- DOCS-1 — `docs/` — the reader pages for the phase table, the error codes, the erasure drain and job, and the suspend response.
- TEST-1 — `tests/` — the cross-cutting lifecycle scenarios at tiers 4, 5, 7a, 8, and 9.
- REC-1 — `BUILD-GAPS.md` and `gateway-runtime-comms-remediation.md` — finding closures, new findings, and the P5 bullet.
