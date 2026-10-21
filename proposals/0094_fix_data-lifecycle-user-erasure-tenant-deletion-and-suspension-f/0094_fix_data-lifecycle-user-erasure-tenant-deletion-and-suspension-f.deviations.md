# Deviations: Data lifecycle operations do not terminate and erase every session and store they cover

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## the memstore creation-marker reader takes a context

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S8 (CODE-1: creation predicate, store fences, session reservation, and CreateWithTranscript).

**What the proposal says.** CODE-1 item 4 gives `memstore.WithCreationMarkers` a reader of type `read func(tenantID, userID string) (CreationMarkers, error)`.

**What landed instead.** The reader type is `memstore.MarkerReader func(ctx context.Context, tenantID, userID string) (sessionstore.CreationMarkers, error)`, defined in `pkg/gateway/session/sessionstore/memstore/fence.go`. A new helper, `memstore.StoreMarkers(tenants, users)`, builds the reader over the tenant and user stores. The gateway wiring in `stores.go` and the tests T1.2, T1.5, and T1.9 all build their reader through that helper.

**Why.** The code rules require a `context.Context` on any call that does I/O, and they forbid a `context.Background()` created deep in a call chain. The copier signature in CODE-1 item 8 already takes a context, so the reader and the copier now take the same leading parameter. The helper keeps a single marker-reading implementation that the wiring and the tests share.

**What a later reader would otherwise get wrong.** A reader working from the proposal text expects a context-free reader signature and no `StoreMarkers` helper. Code that constructs a reader with the proposal's signature does not compile against the tree.

## T1.4 tests the SoftDelete fence with the create holding the lock

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S8 (CODE-1: creation predicate, store fences, session reservation, and CreateWithTranscript).

**What the proposal says.** T1.4 states that a transaction holding `FOR UPDATE` through `userstore.SoftDelete` and `tenantstore.SoftDelete` blocks `pgstore.Create` until it commits, and that the create then refuses.

**What landed instead.** In `tests/tier2_component/stores/sessionstore_creation_fence_test.go`, the `SoftDelete` cases run in the opposite direction. An in-flight `CreateWithTranscript`, whose copier the test holds, keeps the fence's `FOR SHARE` locks open. The test asserts that `SoftDelete` waits on the lock. After the create commits, a fresh `Create` refuses with `USER_INVALIDATED` or `TENANT_NOT_ACTIVE`. The tenant `Update` and user `Update` cases keep the direction the proposal states.

**Why.** `SoftDelete` is a single auto-committed `UPDATE`, and the production method gives a test no way to hold its transaction open. The reversed direction distinguishes `FOR SHARE` from `FOR KEY SHARE`. The step checked this by switching the fence queries to `FOR KEY SHARE`, and both `SoftDelete` cases then failed with "returned without waiting on a lock".

**What a later reader would otherwise get wrong.** A reader working from T1.4 expects the `SoftDelete` cases to show a create blocked behind a soft delete. The landed cases show a soft delete blocked behind a create, followed by a refused create. Both `SoftDelete` cases differ in direction from the two `Update` cases in the same test.

## T1.6 reproduces DeleteByUser with its statements in an open transaction

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S8 (CODE-1: creation predicate, store fences, session reservation, and CreateWithTranscript).

**What the proposal says.** T1.6 states that a `CreateReserved` refuses once `DeleteByUser` commits, when `DeleteByUser` deletes the reservation while the insert transaction waits on the reservation's row lock.

**What landed instead.** The test in `tests/tier2_component/stores/sessionstore_reservations_test.go` opens its own transaction and runs the two `DELETE` statements that `pgstore.DeleteByUser` issues. It leaves that transaction uncommitted while `CreateReserved` waits on the reservation row lock, and then commits it. The test does not call `DeleteByUser` itself.

**Why.** `DeleteByUser` is one method call that opens and commits its own transaction. A test cannot hold that transaction open between its statements without a hook in production code.

**What a later reader would otherwise get wrong.** A reader working from T1.6 expects the test to exercise `pgstore.DeleteByUser` directly. The test reproduces the method's statements, so a later change to the statements `DeleteByUser` issues does not change what T1.6 exercises, and the two can drift apart.

## the gateway refuses a non-positive lifecycle tick budget at startup

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S11 (CODE-5: tenant-deletion Phase 2 terminator wiring, in-phase retry, and fixture retargets).

**What the proposal says.** Non-spec CODE-5 item 1 adds `--lifecycle-tick-budget` (environment `LENNY_LIFECYCLE_TICK_BUDGET`, default 10 minutes) as an operator-tunable flag. The proposal does not state how the gateway handles a non-positive value, and it does not list `cmd/lenny-gateway/startupgates.go` among the files it changes.

**What landed instead.** A new function, `validateLifecycleTickBudget` in `cmd/lenny-gateway/startupgates.go`, stops the gateway at startup with `log.Fatalf` when the budget is not positive. It follows the pattern of the existing `validatePrimaryReadTimeout`.

**Why.** With a non-positive budget, `context.WithTimeout` expires every drain before the drain reaches its first session, so every tenant deletion stalls at Phase 2 with no visible cause. Refusing the value at startup fails closed and matches the validation of the neighbouring flags.

**What a later reader would otherwise get wrong.** A reader working from the proposal text expects any duration to be accepted and does not expect `startupgates.go` to change. A deployment that sets the budget to zero or a negative value fails to start rather than running with stalled tenant deletions.

## the tenant-deletion reconciler logs through an injected slog logger

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S11 (CODE-5: tenant-deletion Phase 2 terminator wiring, in-phase retry, and fixture retargets).

**What the proposal says.** Non-spec CODE-5 item 4 states that `pauseDrain` logs at WARN with the tenant id and the same error text. It does not name a logging mechanism.

**What landed instead.** `Reconciler` in `pkg/controller/tenantdeletion/controller.go` gains an optional injected field, `Logger *slog.Logger`, and a `logger()` accessor that falls back to `slog.Default()` when the field is nil. `pauseDrain` calls `WarnContext` with the attributes `tenant_id` and `error`.

**Why.** The reconciler runs inside the gateway, where no controller-runtime `logr` logger is configured in the context. An injected `slog` logger follows the code rule on dependency injection, and it lets test T5.1 capture and assert the warning.

**What a later reader would otherwise get wrong.** A reader expecting the `logr` logger that controller-runtime code conventionally uses would look for the warning in the controller-runtime log and not find it. The warning goes to the injected `slog` logger, or to the process default `slog` logger when none is injected.

## the salt lock's dedicated connection cancels a lock wait on the server

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-7: runner contract and phase dispatch, drain and hold hooks with wiring, receipt before completion, leader-gated sweep, cross_tenant_read wiring, salt lock, OAuth callback attribution, ERASURE_JOB_NOT_FAILED classifier).

**What the proposal says.** CODE-7 item 11 states that `WithSaltLock` opens its dedicated connection with `pgx.ConnectConfig(ctx, l.pool.Config().ConnConfig)`, as `pgnotify` does.

**What landed instead.** `lockConnConfig` in `pkg/gateway/storage/erasurejob/saltlockpg/saltlockpg.go` copies the pool's `ConnConfig` and installs `pgconn.CancelRequestContextWatcherHandler` with a 2-second deadline delay, so a cancelled lock wait is also cancelled on the server.

**Why.** The default pgx context handler only drops the socket. The backend keeps waiting on the lock, takes it when the holder releases it, and holds it until the backend notices the closed socket. The "cancel during the wait" case of T8.16 failed with one advisory lock left in `pg_locks` until this change.

**What a later reader would otherwise get wrong.** A reader working from the proposal text expects the connection to use the pool's `ConnConfig` unchanged, as `pgnotify` does. Replacing `lockConnConfig` with the plain pool configuration reintroduces an advisory lock that outlives a cancelled wait.

## the runner's write helper also refuses a stale write over a recorded ErasedAt

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-7: runner contract and phase dispatch, drain and hold hooks with wiring, receipt before completion, leader-gated sweep, cross_tenant_read wiring, salt lock, OAuth callback attribution, ERASURE_JOB_NOT_FAILED classifier).

**What the proposal says.** CODE-7 item 1 states that every runner `Update` goes through one helper whose mutate function returns `ErrJobMoved` when `SameAttempt` (equal `Phase` and equal `len(PhaseLog)`) fails.

**What landed instead.** `write()` in `pkg/gateway/storage/erasurejob/runner_phases.go` also returns `ErrJobMoved` when the stored job has `ErasedAt` set and the runner's read of the job did not.

**Why.** Recording `ErasedAt` does not change the phase on the no-billing, exempt, and verify paths, so `SameAttempt` alone does not detect it. Without the extra check, a stale executor of the same attempt can rewrite `ErasedAt` after the receipt was built. The T8.15 randomized checker fails when the check is removed.

**What a later reader would otherwise get wrong.** A reader working from the proposal text expects `SameAttempt` to be the only condition for `ErrJobMoved`. Removing the `ErasedAt` check as redundant lets the receipt and the stored `ErasedAt` disagree.

## the gateway runs the MemoryStore erasure preflight one job at a time per replica

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-7: runner contract and phase dispatch, drain and hold hooks with wiring, receipt before completion, leader-gated sweep, cross_tenant_read wiring, salt lock, OAuth callback attribution, ERASURE_JOB_NOT_FAILED classifier).

**What the proposal says.** The proposal does not address whether the per-job MemoryStore preflight may run concurrently.

**What landed instead.** `cmd/lenny-gateway/adminrouter.go` wraps `memorystore.ValidateMemoryStoreErasure` in a per-replica mutex, so a replica runs the preflight for one job at a time.

**Why.** The preflight seeds and erases one fixed probe identity. When the runner runs several jobs at once, concurrent preflights see each other's probe rows and fail jobs with `memory_store_preflight_failed`. The eight-tenant case of T8.6 exposed this failure.

**What a later reader would otherwise get wrong.** A reader working from the proposal text has no reason to expect the preflight to be serialized. Removing the mutex makes concurrent erasure jobs fail their preflight intermittently. The mutex is per replica, so preflights on different replicas are not serialized against each other.

## the T8.6 small-pool case asserts completion and does not reproduce a merge-base deadlock

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-7: runner contract and phase dispatch, drain and hold hooks with wiring, receipt before completion, leader-gated sweep, cross_tenant_read wiring, salt lock, OAuth callback attribution, ERASURE_JOB_NOT_FAILED classifier).

**What the proposal says.** T8.6 states that the eight-user case with `pool_max_conns=4` and `--erasure-max-concurrent-jobs=8` deadlocks under the merge-base `saltlockpg`.

**What landed instead.** `TestErasureJobsCompleteWithSmallPool_spec_12_8` in `tests/tier4_integration/erasure_job_test.go` asserts that all eight jobs complete. The step ran the test against the merge-base `saltlockpg.go` as a check, and the test passed there as well.

**Why.** The eight tenants take eight different salt-lock keys, and the serialized memory preflight staggers job starts, so the jobs rarely overlap inside `Pseudonymize`. The `MaxConns=1` tier-2 case of T8.16 is the deterministic regression test for the pooled-connection salt lock.

**What a later reader would otherwise get wrong.** A reader working from T8.6 expects the tier-4 case to fail against the merge-base salt lock and to serve as the regression test for the deadlock. It does not fail there. The regression coverage for the deadlock is the T8.16 tier-2 case.

## the T8.6 replica-resume case resumes from initiated after a SIGKILL of an idle replica

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-7: runner contract and phase dispatch, drain and hold hooks with wiring, receipt before completion, leader-gated sweep, cross_tenant_read wiring, salt lock, OAuth callback attribution, ERASURE_JOB_NOT_FAILED classifier).

**What the proposal says.** T8.6 states that two gateway replicas against one Postgres resume a job after the replica running it is stopped.

**What landed instead.** `TestErasureJobResumesOnAnotherReplica_spec_12_8` records the job on replica A, which polls once at startup and then every hour, so A never runs the job. The test then sends SIGKILL to A through a new `gateway.Process.Kill` in `tests/testinfra/gateway/gateway.go`, and replica B resumes the job from `initiated` and completes it. The same test asserts the `cross_tenant_read` rows written by the runner, the sampler, and the GET route.

**Why.** A test running the gateway binary has no deterministic way to stop a job partway through a phase. Resuming a job mid-phase is covered at tier 1 (T8.1) and tier 8 (T8.7).

**What a later reader would otherwise get wrong.** A reader working from T8.6 expects the tier-4 case to stop a replica while that replica is running the job. The case resumes a job that replica A recorded and never started, so it does not exercise a mid-phase handover between replicas.

## the erasure completion receipt also carries the user id and the completion time

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-7: runner contract and phase dispatch, drain and hold hooks with wiring, receipt before completion, leader-gated sweep, cross_tenant_read wiring, salt lock, OAuth callback attribution, ERASURE_JOB_NOT_FAILED classifier).

**What the proposal says.** CODE-7 item 3 states that the receipt is built from the job: the counts, the billing outcome, the phase log with a final `completed` entry, `InitiatedBy`, and `OverrideReceipt`.

**What landed instead.** `admin.ErasureCompletionReceipt` in `pkg/gateway/externalapi/admin/erasure.go` also carries `tenantId` and `jobId`, as it did before, and adds `userId` and `completedAt`, where `completedAt` equals `ReceiptAt`.

**Why.** These fields identify the subject of the erasure and the instant it completed. They are derived from the job alone, so the receipt stays byte-identical when it is rewritten.

**What a later reader would otherwise get wrong.** A reader working from the proposal text expects the receipt to contain only the listed job fields. A consumer or test that checks the receipt against that list finds the additional `userId` and `completedAt` fields.

## the OAuth callback attribution needed only a test

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-7: runner contract and phase dispatch, drain and hold hooks with wiring, receipt before completion, leader-gated sweep, cross_tenant_read wiring, salt lock, OAuth callback attribution, ERASURE_JOB_NOT_FAILED classifier).

**What the proposal says.** CODE-7 item 8 states that `handleConnectorOAuthCallback` attributes its deferred `connector.oauth.credential_stored` event to the persisted flow's principal, in the same way as the receipt sink.

**What landed instead.** `connector_oauth.go` is unchanged, because the callback already emits the event with `ActorSubject=flow.UserID` and `ActorTenantID=flow.TenantID`. The step added `TestConnectorOAuthCredentialStoredAttributedToFlowPrincipal_spec_9_3`, which sends a callback carrying a different request principal and asserts that the event records the flow's principal.

**Why.** The behavior already held at the merge base, so the step added only the test case it requires.

**What a later reader would otherwise get wrong.** A reader working from the proposal text expects a code change in `connector_oauth.go` for this item and finds none in the step's diff. The attribution predates the proposal, and the new test pins it.

## the P5 plan item is ticked and records its outcome, the overlapping step, and the new findings

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S17 (REC-1: finding closures, new findings, annotations, terminal-tail claim row, and P5 bullet).

**What the proposal says.** REC-1 item 9 states that the P5 bullet in `gateway-runtime-comms-remediation.md` is updated so that 0094 covers pieces A and D, pieces B and C and the `full_revoke` fixes move to P5b after P1 (proposal 0093), and the phrase "waits for their terminal hooks" is replaced with the residual that P2 owns. The item does not state that the P5 item is ticked.

**What landed instead.** The P5 item carries those edits. It is also ticked as "(proposal 0094, implemented 2026-10-21)", it gains an "Outcome" sub-bullet and a "Filed unassigned" sub-bullet, it names R21 under "Overlapping plan steps", and the P5b "Members" list gains F-12.8.41 and F-12.8.42.

**Why.** The step followed the form in which the P1 item was recorded when the records step of proposal 0093 ran before that proposal merged, and it kept the plan consistent with the finding identifiers this proposal files.

**What a later reader would otherwise get wrong.** A reader working from item 9 expects only the coverage split and the residual wording to change in the P5 item. The item in the tree is also ticked, carries outcome and filing sub-bullets, names R21, and lists F-12.8.41 and F-12.8.42 under P5b.

## the salt-lock finding's resolution names the commit rather than the proposal step

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S17 (REC-1: finding closures, new findings, annotations, terminal-tail claim row, and P5 bullet).

**What the proposal says.** REC-1 item 14 states that the saltlockpg finding is filed as closed by proposal 0094 "through CODE-7 item 11".

**What landed instead.** The resolution of F-12.8.40 in `BUILD-GAPS.md` names commit 9420f39a2 and describes the fix, a transaction-scoped advisory lock held on a dedicated connection outside the pool. It does not contain the CODE-7 label.

**Why.** The step keeps proposal-internal step labels out of finding resolutions. The existing tier-11 gates check resolution text for step labels, and the run instructions exclude internal labels from durable artifacts.

**What a later reader would otherwise get wrong.** A reader searching `BUILD-GAPS.md` for "CODE-7 item 11" to trace the salt-lock closure finds no match. The closure is traceable through F-12.8.40 and commit 9420f39a2.
