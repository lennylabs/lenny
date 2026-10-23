# Problem: Data lifecycle operations neither fence new sessions nor quiesce live ones before they erase, and tenant deletion skips Phases 2 and 3

## Statement

This is proposal P5 of `gateway-runtime-comms-remediation.md` §10.2 ("P5. Data lifecycle: user erasure, tenant deletion and suspension, full_revoke, and `DELETE /v1/responses/{id}`"). It follows proposal 0092, which landed in merge 35d923b0d. 0092 added the user-erasure eraser for the session message queues (commit 5c5da42e6) and the `AfterRevokeWrite` hook in the full_revoke handler (commit 3f943f56f). P5 runs beside proposal P1 (release ownership), which is numbered 0093. 0093 is an unapproved draft that exists only in the `draft-0093` worktree and is absent from `proposals/` on this branch, so every P5 call site that depends on a P1 mechanism depends on a draft that can still change.

The lifecycle operations covered here are user erasure, tenant deletion, tenant suspension, and full_revoke. Each one has to do two things before it acts on a scope (a user or a tenant). It has to fence the scope against new sessions, and it has to bring every live session in the scope to a terminal state. The tree at d05c25630 does neither reliably. Several smaller defects in store coverage, durability, and one unspecified route sit around those two mechanisms.

**Creation fence.** Two separate helpers gate session creation, and each is a plain read that runs before the insert. `requireActiveUser` checks user invalidation and `processing_restricted`. `requireTenantState` checks the suspension marker and the `disabling` and `deleting` tenant states. `requireTenantState` has one caller, `POST /v1/sessions/start`. `POST /v1/sessions`, MCP `create_session` (which runs through the same handler as `POST /v1/sessions`), derive, and replay call only `requireActiveUser`, so a suspended tenant or a tenant in the deletion lifecycle can still create sessions through them. Delegation checks only `IsActive` and never checks `processing_restricted`. Neither helper is atomic with the insert, so a session created between the read and the marker write escapes both the gate and the later listing. The erasure handler writes `processing_restricted` after it starts the job and discards the write error, so a store failure leaves the user able to create sessions during erasure. §12.8 Phase 1 also requires rejecting API-key issuance and user sign-ups for a tenant in deletion, and no code gates either.

**Quiesce and drain.** No lifecycle operation drives the sessions in its scope to a terminal state before it acts.

- User erasure lists the user's sessions once and then runs fixed erasers, and `pgstore.DeleteByUser` deletes session rows in every state. A running session is therefore hard-deleted mid-run while its pod keeps writing transcripts, usage, and billing as the erasers run. A terminal tail writes back after erasure only when its terminal update committed before the `sessions` eraser. After the row is deleted, a later terminal update matches no row and runs no tail. `session_tree_archive.root_session_id` references `sessions(id)` with no `ON DELETE` action. §12.8 says in-flight sessions "are allowed to complete naturally (they will be erased by the job)". A single-pass job cannot meet that sentence. The code neither waits for those sessions nor terminates them. Whether the job waits for live sessions to finish or terminates them is an open owner decision.
- Tenant deletion Phase 2 does nothing because nothing assigns `Reconciler.Terminator`, and Phase 3 does nothing because nothing assigns `Reconciler.Revoker` and no `RevokeByTenant` method exists. The spec already defines Phase 2: send graceful shutdown, wait for a terminal state or a 5-minute default timeout, and then force-terminate. Phase 4 then runs `DeleteByTenant` against live sessions.
- The tenant-suspension drain lists the tenant's sessions once, runs on the request context, returns at the first `ForceTerminate` error, and has its error discarded. A repeated suspend call on an already-suspended tenant returns 200 without draining again, so the operator has no retry path short of a resume and suspend cycle. The `tenant.suspended` audit row is emitted on the request context after the drain.
- full_revoke terminates sessions, fans out, and dismisses elicitations on the request context. It returns at the first `Update` error other than `ErrSuperseded`, and the handler answers 500 with no per-effect counts. §11.4 already requires per-effect counts and partial-failure detail in the response.

The erasure and full_revoke listings read `s.read`. That is a separate read pool only when the deployment configures one. Otherwise reads go to the primary. Replica lag is therefore a defect only in deployments with a read pool. The single listing and the race between the read and the insert apply in every deployment.

**Erasure job durability.** The erasure job store is `erasurejob.NewMemory()`, and no Go code writes the `erasure_jobs` table that migration 0042 creates. The job runs in a goroutine on `context.Background()` on one replica. A restart loses the job status, the 72-hour deadline sampler entry, and the record that the clear-processing-restriction endpoint looks up, which then returns 404. The user is not locked out, because re-issuing the erase starts a new job that clears the flag on completion. Because the table stays empty, the `BEFORE UPDATE` trigger that §12.8 names as the database-level Article 18 enforcement has no effect. On completion, clearing the restriction discards its error and is a write separate from the receipt emit, while §12.8 requires the clear to be atomic with the completion receipt.

**Store coverage.** Tenant Phase 4 wires erasers for `sessions`, `tokens`, `credential_pool`, `leases`, `quota`, `transcripts`, `interactions`, `memory`, `session_tree_archive`, `eval_results`, and the checkpoint stores. It omits the semantic cache, artifact blobs and catalog, audit, billing, users, custom roles, the session message queues, and the delegation-budget keys. `DeleteByTenant` already exists for most of these stores. User erasure has no eraser for the checkpoint manifest, retention, or metadata rows, which reference tenants only and do not cascade from a session delete. Neither path erases the `coordination_lease` mirror, although `coordlease` pgstore already implements `DeleteByUser` and `DeleteByTenant`. The `audit_redaction_receipts.audit_event_id` foreign key to `audit_log(id)` has no `ON DELETE` action, so a Phase 4 audit eraser would fail for any tenant with a redacted row.

**`DELETE /v1/responses/{id}`.** The handler calls `store.Delete(tenantID, id)` with no kind, state, or owner check, and the store runs an unconditional `DELETE`. The auth middleware sets the tenant header from the principal, so isolation between tenants holds. Within a tenant, any authenticated caller can hard-delete any other user's session, including a running one. The spec does not define this route. It says only that the responses adapter creates an implicit session record per request "for audit and billing", and that `DELETE /v1/sessions/{id}` on a terminal session returns 409.

**Recommended cut (owner decision).** The scope lens found that this statement covers more than the plan's grain rule allows ("Keep the scope to one subsystem boundary"), and the repository evidence supports the cut it proposes. The fence and the drain are each one mechanism shared by every operation, so they must not be split by operation. The other pieces have independent remedies. The recommended cut is as follows.

- **A (keep as 0094): lifecycle fence and quiesce.** This piece covers one creation gate, atomic with the insert, on every create path (create, MCP create, create-and-start, derive, replay, and delegation) for every marker. It also covers one durable, re-runnable drain that reads the primary, terminates on a detached context, continues past per-session errors, re-lists until no live session remains, and reports counts. The drain is applied to user erasure, tenant Phase 2 with a Phase 4 precondition that no live session remains, and tenant suspension. This piece also moves the erasure job to Postgres. full_revoke either joins as a final checklist step gated on P1 or follows as a small proposal that reuses the drain.
- **B: erasure store coverage.** Wire the missing user and tenant erasers from a store-and-phase table.
- **C: the audit-receipt foreign key** as its own fix. B's audit eraser depends on it.
- **D: `DELETE /v1/responses/{id}`** as an API-definition and authorization fix, which the owner could pair with F-15.1.54 (the sibling `GET` user check).

Members, labelled with the recommended piece:

- A: F-12.8.26, F-12.8.27, F-12.8.28, F-12.8.31, F-12.8.32, F-12.8.33 (the fix is Phase 2 plus a Phase 4 precondition rather than a reorder), F-15.1.50, F-15.1.51 (broader than filed), and the `tenant.suspended` half of F-11.7.22. F-11.7.22 is owned by P2 in BUILD-GAPS, so this is a stated reassignment.
- A, as a final step gated on P1 or a follow-on: F-11.4.12, F-11.4.13, and F-11.4.14 (the creation-race half of F-11.4.14 moves into the shared gate).
- B: F-12.8.30 (depends on P1's lease reclaimer), F-12.8.34, F-12.8.35 (weakly supported; see Validated premises), and F-12.8.37.
- C: F-12.8.25.
- D: F-15.1.48.
- Defects with no open finding:
  - N-1 (A, High): tenant-deletion Phase 2 is unwired. This reverses the optional-seam relaxation that closed F-12.8.1 and is a code defect against the existing Phase 2 text.
  - N-2 (A): the erasure job store is in memory and `erasure_jobs` is never written. This was already recorded as F-12.8.20, closed as gated on F-12.8.5, and F-12.8.5 then closed without building the store. The proposal reopens or supersedes F-12.8.20 instead of filing N-2 fresh.
  - N-3 (B): user erasure leaves the checkpoint manifest, retention, and metadata rows. No prior finding covers it. F-12.8.29's tenant-side fix is the pattern to mirror.
  - N-5 (A): clearing the processing restriction at completion ignores its error and is not atomic with the receipt.
  - The `coordination_lease` mirror rows survive both erasure paths (B; this is the code half of SG-9).
  - Phase 1's API-key issuance and user sign-up rejection is unimplemented (A).
- N-4 is withdrawn as a new defect. Its step-3 half (the Redis access-token and routing caches) was decided in F-12.2.16, because `routingcache` has no non-test caller and `credcache` keys on pool credentials. Its step-1 half is the deliberate TTL-lease no-op recorded in F-12.1.3. §12.8 steps 1 and 3 still name a release and a flush, so the proposal confirms or overturns those recorded decisions explicitly. Once the drain lands, step 1 can reduce to an invariant check.
- F-11.4.16 is not a member. It duplicates F-11.4.15 and belongs to P1. P5 provides only the call site that asks for each revoked session to be released.

Spec gaps, restated to the parts the spec does not already answer:

- SG-1 (open, owner decision): §12.8's "complete naturally" sentence cannot hold with a single-pass job. The spec must state whether erasure waits for the user's live sessions to reach a terminal state, with forced termination as a deadline fallback (the Phase 2 pattern), or terminates them at once. In either case it must state that the erasers run only after the terminal hooks finish.
- SG-2 (partly answered): §12.8 already places the restriction "at the session creation gate upstream of endpoint-specific logic", and §15.2.1 rule 5 already requires MCP `create_session` to return the same creation rejections as `POST /v1/sessions`. The remaining gaps are delegation coverage, atomicity with the insert, and failing the erase request when the restriction write fails.
- SG-3 (open): lifecycle listings read the primary. §12.3 routes session-status reads to replicas with no exception for lifecycle decisions, and no list-from-primary primitive exists. 0092's `ReadPrimary` reads a single row.
- SG-4 (mostly answered): Phase 1 already rejects new session creation, API-key issuance, and user sign-ups with no endpoint restriction. The open part is atomicity of the Phase 1 gate with the insert.
- SG-5 (partly answered): Phase 2's graceful shutdown, wait, and force-terminate are already specified, so N-1 is a wiring defect. The open part is Phase 4's precondition that no live session remains.
- SG-6 (partly answered): the §15.1 resume row already states "Existing sessions remain terminated — suspension is not a pause". The open part is the drain mechanics: durability, completeness, re-runnability, and the race with the marker.
- SG-7 (open): the spec does not define `DELETE /v1/responses/{id}` or how it maps onto the implicit per-request session record.
- SG-8 (partly answered): §11.4 already requires per-effect counts and partial-failure detail, so F-11.4.13 is a code defect. The open parts are behavior on client disconnect, ambiguous writes, and sessions created after the listing.
- SG-9 (open): the §12.8 erasure scope table omits the coordination mirror row and the user registry row. The code fix is wiring existing primitives.
- SG-10 (open): the receipt schema declares `audit_event_id` as a foreign key to `audit_log.id`, while the receipts must survive a `DeleteByTenant` that purges the referenced rows. This contradiction is internal to the spec. F-12.8.25 lists candidate fixes and favors `ON DELETE SET NULL`.

Settled context:

- P5 decides when and for which sessions a release runs, and P1 decides how. The termination call sites do not need to wait for P1. `ForceTerminate` already serializes against concurrent terminal writers under the row lock and runs the full terminal pipeline synchronously. It hard-codes state `failed`, class `FailureClassRuntime`, and reason `FORCE_TERMINATED`, so reuse for suspension, deletion, and erasure needs a reason and class parameter. It does not perform Phase 2's graceful step. Where a call site needs a P1 release mechanism that the spec does not yet define, P5 depends on P1 and says so, and does not design the release.
- `GET /v1/responses/{id}` having no user check is F-15.1.54, filed in commit 8396409d4 after this worktree's base. It is out of scope unless the owner pairs it with piece D.
- Also out of scope, with no remedies designed here: the terminal pipeline (P2), timers and sweeps (P3), the delegation tree and budget (P4), and release mechanics (P1).
- P5's full_revoke edits land after P1's. The P1-independent work (the fence, Phase 2 and Phase 3 wiring, the durable erasure job, store coverage, and full_revoke error handling and counts) can land first.

Drafting rules (owner, plan §10.2, from lessons.md):

- Open with the member defect list, re-confirmed against the tree.
- Keep the scope to the data-lifecycle boundary. A defect outside it goes to BUILD-GAPS or its owning group.
- Write a store-and-phase-to-owner table first.
- Add no new per-replica state unless the spec requires it. Prefer durable state: the erasure job moves to Postgres against the existing migration 0042 schema. An in-memory set must be safe to lose, with a named durable backstop.
- Record a residual gap as a departure with a bounding fallback and a claim-register row.
- State tests as intents with tiers, and make them expansive: table-driven over every store, phase, create path, and session state; randomized interleavings of erasure, deletion, and suspension against concurrent session creation and terminal writes under -race (tier 7a); fault injection at each phase step, including store errors, context cancel, a crash between phases, and replica takeover (tier 8); invariant checkers after every scenario (no session left live past its phase, no row or key of the erased principal left in any listed store, exactly-once audit and receipt); multi-replica tier-4 and tier-5 scenarios; and tier-9 checks that every create path honors every gate.
- Stage the owner's triage rule for failures the tests find. Fix in this proposal what is relevant to it, or what is straightforward and not owned elsewhere. File everything else, with a deviation entry and a `blocked:` skip.

## Evidence

All citations below were checked against the `draft-0094` worktree at d05c25630.

- (verified) Reconciler construction sets only `Jobs`, `Eraser`, `Disabler`, and `Clock`, and later assignments never set `Terminator` or `Revoker`: `cmd/lenny-gateway/adminrouter.go:719-724` and the assignments that follow. `runPhase` returns nil for a nil `Terminator` or `Revoker`: `pkg/controller/tenantdeletion/controller.go:390-405`. The optional-seam comment: `controller.go:178-191`. No `RevokeByTenant` exists in `pkg/` or `cmd/`.
- (verified) `requireTenantState` is defined at `pkg/gateway/sessionserver/quota.go:86` and called only at `create_and_start.go:124`. `requireActiveUser` is called at `create.go:88`, `create_and_start.go:118`, `derive.go:140`, and `replay.go:65`. MCP create runs through `CreateSessionService` (`pkg/gateway/sessionserver/service.go:153`), which builds a `POST /v1/sessions` request for `handleCreate`.
- (verified) `requireActiveDelegator` checks only `user.IsActive()`: `pkg/gateway/mcpfabric/mcptools/mcptools.go:1408-1435`.
- (verified) The erasure handler starts the job, then writes the restriction with `_, _ = r.users.Update`, then runs the job in a goroutine: `pkg/gateway/externalapi/admin/erasure.go:166, 192-196, 202`. The completion clear discards its error and runs separately from the receipt: `erasure.go:223-237`.
- (verified) The erasure job store is `erasurejob.NewMemory()` (`cmd/lenny-gateway/adminrouter.go:593`). `NewMemory` is the only store constructor (`pkg/gateway/storage/erasurejob/erasurejob.go:139`). No Go code writes `erasure_jobs`: the only references are `pkg/audit/integrity/integrity.go` and metric names. Table and trigger: `migrations/0042_erasure_jobs_processing_restriction_trigger.up.sql`.
- (verified) `pgstore.Delete` and `pgstore.DeleteByUser` run unconditional deletes: `pkg/gateway/session/sessionstore/pgstore/pgstore.go:772-800`. `session_tree_archive.root_session_id REFERENCES sessions(id)`: `migrations/0100_session_tree_archive.up.sql:35`.
- (verified) Suspend handler: the already-suspended no-op at `pkg/gateway/externalapi/admin/tenant_suspend.go:85-88`, the drain on `req.Context()` with its error discarded at `:112`, and the single listing with return on the first error at `:179-203`. `ForceTerminate` hard-codes `FailureClassRuntime` and `FORCE_TERMINATED`: `cmd/lenny-gateway/session_admin.go:51-78`.
- (verified) full_revoke runs on `req.Context()` and returns at the first non-`ErrSuperseded` error: `pkg/gateway/externalapi/admin/users.go:463-499, 560-609`.
- (verified) The `DELETE /v1/responses/{id}` route is at `pkg/gateway/environment/translator/open_responses.go:242`, and the handler at `:472-484` calls `h.store.Delete` with tenant and id only. The tenant header is overwritten from the principal at `pkg/gateway/middleware/auth/auth.go:588, 629, 668`. The adapter is mounted at `cmd/lenny-gateway/httpsurface.go:331-333`. The original statement placed these handlers in `pkg/gateway/sessionserver`, which was wrong.
- (verified) Tenant Phase 4 eraser names: `cmd/lenny-gateway/tenant_erasers.go:30-112`. Unwired `DeleteByTenant` primitives: `semanticcache/redisstore`, `artifactcatalog`, `auditstore`, `billingstore/pgstore`, `userstore/pgstore`, and `customrolestore/pgstore`.
- (verified) `coordlease` pgstore implements `DeleteByUser` and `DeleteByTenant` (`pkg/gateway/coordination/coordlease/pgstore/pgstore.go:99, 114`), and neither eraser builder references it.
- (verified) `audit_redaction_receipts.audit_event_id ... REFERENCES audit_log (id)` with no `ON DELETE` action: `migrations/0160_audit_redaction_receipts.up.sql:16-21`.
- (verified) `leasestore.DeleteByUser` is a documented no-op: `pkg/gateway/storage/leasestore/leasestore.go:208-224`. `routingcache` has no caller outside its own package.
- (verified) Spec: §12.8 "Processing restriction during erasure (GDPR Article 18)" paragraph (`spec/12_storage-architecture.md:807`); the tenant deletion phase table (`:911-918`); §12.3 "Read replicas" (`:146`); the §11.4 full-revoke Note (`spec/11_policy-and-controls.md:278`); the §15.1 suspend and resume rows (`spec/15_external-api-surface.md:848-849`); the `TENANT_SUSPENDED` and `ERASURE_IN_PROGRESS` error rows; §15.2.1 rule 5 (`:1444`); and the responses adapter's implicit session record (`:612`).
- (lead, not re-read) The opening defect dossier `/home/ec2-user/lenny/scratchpad/p1-p5/p5-dossier.md` and the source records under `scratchpad/terminal-transition/`. Every claim taken from them above was re-verified in the tree.

## Who observes it

The platform is pre-deployment, so the exposure is prospective. It applies from the first deployment to the following parties.

- Any authenticated user in a tenant can call `DELETE /v1/responses/{id}` to hard-delete another user's session in the same tenant, including a running interactive session.
- An operator who deletes a tenant sees Phase 2 and Phase 3 report success without acting, and receives a Phase 6 erasure receipt while artifacts, audit, billing, semantic-cache entries, and other in-scope data remain.
- An operator who suspends a tenant sees the tenant's users continue to create sessions through `POST /v1/sessions`, MCP create, derive, and replay. The same bypass applies to a tenant in `disabling` or `deleting`.
- An operator whose suspend drain fails part-way has no retry, because a repeat suspend call is a no-op.
- A data subject whose erasure is requested keeps the ability to create sessions if the restriction write fails, and keeps the ability to delegate in every case.
- An operator issuing full_revoke receives a 500 or a dropped connection with no counts when one store write fails, and sessions remain live if the operator does not retry.
- An operator whose gateway restarts during an erasure loses the job status and the overdue alert, and the clear-processing-restriction endpoint returns 404 for the lost job.

## What breaks if nothing changes

- Intra-tenant authorization fails on `DELETE /v1/responses/{id}`, which a tier-9 review would flag by itself, and audit and billing lose the implicit session record that §15 says exists for them.
- Tenant deletion produces a false compliance attestation: the receipt records a deletion that left in-scope data behind, and live sessions are hard-deleted mid-run instead of drained.
- Suspension loses its value as an operator control. §12.5's T4 KMS rotation procedure relies on suspension to stop writes, so the bypass leaves writes under the old key possible during a prescribed procedure.
- The GDPR Article 18 restriction fails open on a store error and on the delegation path. The database-level enforcement §12.8 describes has no effect because `erasure_jobs` is empty.
- User erasure deletes rows that running sessions continue to write to, so erased data can reappear or writes can fail against missing rows.
- full_revoke leaves sessions live for a user the operator believes revoked whenever one write fails and the operator does not retry, and no audit row records the attempt.

## Findings this unblocks

- Closed by this proposal: F-12.8.26, F-12.8.27, F-12.8.28, F-12.8.31, F-12.8.32, F-12.8.33, F-12.8.36, F-15.1.48, F-15.1.50, F-15.1.51, and F-12.8.20 (reopened and closed by the durable erasure job store). The new findings N-1 and N-5 are filed and closed by it.
- Advanced and left OPEN: F-11.7.22 (the `tenant.suspended` half) and F-11.4.14 (the creation-race half).
- TEST-GAPS advanced: T-12.8.4 (the Article 18 bypass and the cross-tenant erasure-job checks), T-12.8.9 (Phase 2 before Phase 4 against a real backend), and T-12.8.16 (persisted-phase resume).

## Prior art considered

- No proposal in `proposals/` stages this change. 0012, 0018, 0019, 0025, 0028, 0057, and 0059 touch erasure, tenant deletion, or full_revoke and are landed and narrower. 0028 explicitly left F-12.8.25 out of scope because it needs a new migration. 0092 landed only the session-message-queue user eraser and recorded "Erasure racing a live session" as an accepted gap owned by P5.
- The 0093 (P1) draft is a skeleton with no erasure, tenant, or responses content. Its problem statement takes F-11.4.10, F-11.4.11, F-11.4.15, and F-11.4.16, which matches the boundary drawn here.
- F-12.8.1 (closed, commit 65df24ad) wired the tenant-deletion controller and relaxed `Terminator`, `Revoker`, and `Cleaner` to optional seams. N-1 reverses that relaxation for `Terminator` and `Revoker`, and the proposal retires the optional-seam comment.
- F-12.8.20 (closed) recorded the in-memory erasure job store and the unwritten `erasure_jobs` table, and closed as gated on F-12.8.5. F-12.8.5 closed without building an erasure-job store, so the closure is stale.
- F-12.1.3 and F-12.2.16 (closed) recorded the lease no-op and the out-of-scope Redis caches. The proposal confirms or overturns them explicitly.
- F-12.8.29 (closed) wired checkpoint erasers into tenant Phase 4 and is the pattern for N-3.
- Reusable surfaces: `requireTenantState`, `requireActiveUser`, `requireActiveDelegator`, `CreateSessionService`, `ForceTerminate` and the terminal pipeline, 0092's single-row `sessionstore.ReadPrimary`, the existing `DeleteByTenant` primitives, `coordlease` `DeleteByUser` and `DeleteByTenant`, and the migration 0042 schema and trigger.

## Validated premises

Each premise lists its verdict and the lenses that judged it.

- **Phase 2 is unwired (N-1).** Confirmed by the premise, evidence, impact, and alternatives lenses and re-checked: no assignment of `Terminator` exists. Prior art adds that the gap originates in F-12.8.1's deliberate relaxation, and that the spec already defines Phase 2, so this is a code defect.
- **Phase 3 has no revoker (F-12.8.30).** Confirmed by the premise, evidence, and impact lenses. No `RevokeByTenant` exists.
- **The tenant-state gate covers only create-and-start (F-12.8.31, F-15.1.51).** Confirmed by every lens that examined it. The finding is broader than filed.
- **The restriction write fails open and delegation skips it (F-12.8.26).** Confirmed. The premise and prior-art lenses refined it: MCP create already reaches `requireActiveUser` through `handleCreate`, so the gap is delegation, atomicity, and the discarded write error. The impact lens bounded it: the gate is correct when the flag is set.
- **The erasure job store is in memory (N-2).** Confirmed by every lens. Prior art refuted "no finding yet" (F-12.8.20). The evidence and alternatives lenses added that the database trigger has no effect. The impact lens narrowed the consequence: a restart loses status, the deadline alert, and the manual-clear target, and re-issuing the erase clears the flag.
- **The restriction clear is not atomic with the receipt (N-5).** Confirmed by the premise and evidence lenses.
- **User erasure never quiesces live sessions (F-12.8.28, SG-1).** Confirmed in substance. The alternatives lens refuted the framing that not quiescing violates the spec, because §12.8 lets sessions complete naturally. The narrower verified defect is that the job lists once and deletes live rows without waiting for or terminating them. Terminating sessions on every erasure request is a policy choice left to the owner.
- **Terminal tails write data back after erasure.** Revised by the premise lens and re-checked. A tail writes back only when its terminal update committed before the `sessions` eraser. The larger source is the running session itself.
- **Lifecycle listings read a replica (F-11.4.14, F-12.8.27).** Revised. The replica half applies only when a read pool is configured. The single listing and the race between read and insert apply everywhere. SG-3 remains a real spec gap.
- **full_revoke runs on the request context and aborts on the first error (F-11.4.12, F-11.4.13).** Confirmed. Prior art and alternatives found that the spec already requires counts and partial-failure detail, so the SG-8 counts half is a code defect. The impact lens bounded the consequence, since re-issuing the call continues.
- **The suspend drain is one-shot (F-15.1.50, F-11.7.22 half).** Confirmed by every lens that examined it. The §15.1 resume row already defines that drained sessions remain terminated.
- **`DELETE /v1/responses/{id}` hard-deletes any session (F-15.1.48, SG-7).** Confirmed. The impact lens established that the exposure is cross-user within a tenant. The evidence lens corrected the file location. The scope and alternatives lenses found that the defect is an authorization and API-definition problem separable from the data-lifecycle mechanism. The alternatives lens's claim that the external Open Responses specification defines the delete cannot be verified in this repository, and the Lenny spec does not define the route.
- **Phase 4 omits most stores (F-12.8.34, F-12.8.37).** Confirmed by the premise, evidence, and impact lenses. Most fixes are wiring of existing primitives.
- **No-Postgres checkpoint metadata store (F-12.8.35).** Weakly supported by the premise lens. Without Postgres the whole gateway is in memory, and the eraser comment says the store holds no durable tenant data. The finding is Low and is kept as a member only for completeness.
- **The audit-receipt foreign key blocks the audit eraser (F-12.8.25, SG-10).** Confirmed. The defect stays latent until an audit eraser is wired. The contradiction is internal to the spec.
- **User erasure leaves checkpoint rows (N-3).** Confirmed by the premise, evidence, and prior-art lenses. No prior finding covers it.
- **User erasure steps 1 and 3 do nothing (N-4).** The evidence lens confirmed the code fact. The premise and prior-art lenses refuted it as a new defect, because both halves are recorded decisions in F-12.1.3 and F-12.2.16, and `routingcache` has no non-test caller. It is withdrawn as a new defect and kept as a decision for the proposal to confirm or overturn.
- **The coordination mirror is never erased (SG-9).** Confirmed. The premise lens revised the remedy to wiring of existing `coordlease` primitives plus a spec-table row.
- **Phase 1 also gates API-key issuance and sign-ups.** Added by the premise lens and confirmed against the Phase 1 row. No code gates either.
- **P1 is 0093.** Holds only as a draft in a separate worktree. The alternatives lens found that the termination call sites can use `ForceTerminate` today with a reason parameter, so the P1 coupling covers how release works rather than whether P5 can terminate.
- **The member findings are live.** The premise and evidence lenses confirmed that every cited member is OPEN. Some member findings carry line numbers that have drifted (for example F-11.4.12), with unchanged substance.
- **Scope.** The scope lens found that the statement spans several problems and exceeds the plan's grain rule. The alternatives lens reached the same conclusion from a different direction: most members are symptoms of two missing shared primitives, the fence and the drain. The recommended cut under Statement records both views and is left to the owner.
- **Viability.** No lens refuted the problem. The impact lens established concrete consequences: intra-tenant cross-user deletion, a false tenant-deletion receipt, a suspension bypass, and a fail-open erasure restriction.
