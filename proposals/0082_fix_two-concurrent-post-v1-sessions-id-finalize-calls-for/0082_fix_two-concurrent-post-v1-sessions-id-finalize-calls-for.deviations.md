# Deviations: Concurrent finalize calls both prepare the session's pod

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Finalize branch logic extracted into helpers in finalize.go

**Status:** proposed

**Reported by:** step S3 (CODE-1 and CODE-3: exit guards, `failFinalizing`, `afterFailed`, `recordPodClaimFailure`, and `revokeFinalizeLease`).

**What the proposal says:** CODE-3 in the non-spec changes places the branch logic for rules 1, 2, and 4 in `handleFinalize` in `pkg/gateway/sessionserver/sessionserver.go`, and lists `revokeFinalizeLease` as the only new function in `finalize.go`.

**What landed instead:** The branch bodies are extracted into three helpers in `pkg/gateway/sessionserver/finalize.go`: `finalizePlanParseFailed`, `finalizePrepareFailed`, and `finalizeReadyWriteFailed`. The same file also gains `isPreconditionError`. `handleFinalize` in `sessionserver.go` calls these helpers. The step reports that the rule semantics match the proposal.

**Why:** `code-best-practices.md` asks for small single-purpose functions, and `handleFinalize` was already about 190 lines long. The helpers sit in `finalize.go` beside `reclaimFinalizedPod` and `revokeFinalizeLease`, where the finalize-specific logging already lives.

**What a later reader would otherwise get wrong:** A reader following CODE-3 would look for the rule 1, 2, and 4 branches inline in `handleFinalize` and would expect `revokeFinalizeLease` to be the only new function in `finalize.go`. The branches are in the named helpers in `finalize.go`.

## Proposed: recordPodClaimFailure called once before writePodClaimError's switch

**Status:** proposed

**Reported by:** step S3 (CODE-1 and CODE-3: exit guards, `failFinalizing`, `afterFailed`, `recordPodClaimFailure`, and `revokeFinalizeLease`).

**What the proposal says:** CODE-3 names `recordPodClaimFailure` as extracted from the two recording calls in `writePodClaimError`, with `writePodClaimError` calling it in their place.

**What landed instead:** `writePodClaimError` in `start.go` calls `recordPodClaimFailure(err)` once, before its switch. `recordPodClaimFailure` has its own switch that repeats the arm order of `writePodClaimError` up to the credential-assignment arm. The earlier arms record nothing. A comment instructs maintainers to keep the two switches in the same order.

**Why:** Calling the helper inside the two arms would classify the error twice. Calling it once before the switch keeps the recording ahead of the response write, as before, and gives the precedence the proposal requires.

**What a later reader would otherwise get wrong:** A reader would expect the recording calls inside the two arms of `writePodClaimError`. The classification exists in two switches that must stay in the same arm order, and a change to the arm order of one switch without the other changes which failures are recorded.

## Proposed: Additional exit-guard regression test file

**Status:** proposed

**Reported by:** step S3 (CODE-1 and CODE-3: exit guards, `failFinalizing`, `afterFailed`, `recordPodClaimFailure`, and `revokeFinalizeLease`).

**What the proposal says:** TEST-1, which belongs to a later step, lists the test files as `finalize_race_test.go` and `finalize_race_internal_test.go`.

**What landed instead:** Step S3 adds `finalize_race_internal_test.go` with the internal cases the proposal names, and one more file, `finalize_exit_guard_internal_test.go`. That file holds memstore and fake-client handler regression tests for each exit branch.

**Why:** The step brief requires a regression test for each corrected behavior that fails against the pre-fix code. `finalize_exit_guard_internal_test.go` does not use the pod-bind fixture, so it does not duplicate the `finalize_race_test.go` cases that TEST-1 adds.

**What a later reader would otherwise get wrong:** A reader using TEST-1's file list as the complete inventory would miss `finalize_exit_guard_internal_test.go`, and would miss that `finalize_race_internal_test.go` was created in step S3 rather than in the TEST-1 step.

## Proposed: Committed credential-assignment failure asserts 503 SESSION_CREATION_FAILED

**Status:** proposed

**Reported by:** step S4 (TEST-1: tier-1 deterministic entry and exit interleavings).

**What the proposal says:** The TEST-1 case "Credential-assignment failure, committed" asserts "the existing writePodClaimError response". The proposal does not name a status code or an error code.

**What landed instead:** `TestFinalizeCredentialFailureCommittedRevokes_spec_7_1` in `finalize_race_test.go` asserts 503 with `SESSION_CREATION_FAILED`. `writePodClaimError` returns that response today when the adapter's `AssignCredentials` RPC fails, because that failure is not a `*CredentialAssignmentError`.

**Why:** The assertion records the handler's existing response for the chosen injection point. The step changes no behavior.

**What a later reader would otherwise get wrong:** A reader would expect the committed credential-assignment case to assert `CREDENTIAL_POOL_EXHAUSTED`. The test injects the failure at the adapter's `AssignCredentials` RPC, and `writePodClaimError` maps that failure to 503 `SESSION_CREATION_FAILED`.

## D1 · step S5 · 2026-09-27 · accepted
**Status:** accepted
**Proposal says:** TEST-2 step 4 in `0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for.non-spec-changes.md` (line 295) directs the test to poll `pg_stat_activity` until a backend whose query matches `ILIKE '%FOR UPDATE%'` has `wait_event_type='Lock'`, and then to release goroutine A.
**Implemented instead:** `awaitSessionRowLockWaiter` in `tests/tier2_component/stores/sessionstore_test.go:96-134` identifies the blocked backend as one in `current_database()`, other than the polling backend, with `wait_event_type = 'Lock'` that holds a `pg_locks` lock of type `tuple` on `'sessions'::regclass`. PostgreSQL takes the heavyweight tuple lock before the waiter waits on the holder's transaction ID, so the predicate matches only while B is blocked on A's row lock. The helper also fails the test when B's Update returns before it blocks. The comment at `sessionstore_test.go:98-103` records the reason for the predicate.
**Why:** `pg_stat_activity.query` is truncated at `track_activity_query_size`, which is 1024 bytes by default. The select list of the sessions query is longer than that, so the `FOR UPDATE` clause never appears in the recorded query text. The predicate TEST-2 step 4 prescribes therefore never matches, and a test that follows it reaches its deadline and fails on every run. The only change that closes the divergence is an amendment to TEST-2 step 4, which the fixer may not make. The judges found that no legal code change exists: rounds 2 through 5 produced no commit, and every reviewer concluded that the landed code is correct.
**Consequence if the proposal is not corrected:** An implementor who follows TEST-2 step 4 writes a poll that always times out and reads the failure as a store defect. A reader who compares the test to the proposal finds a waiter predicate the proposal does not describe and has no record in the proposal of why the query-text match was abandoned.
**Suggested next step:** correct the proposal
**Evidence:** Commit c7609e923 ("sessionstore: pin finalize admission serializing on the Postgres row lock, and persist the plan on Update") introduced the helper and documents the truncation in its message. Rounds 2 through 5 of step S5 reported the same commit with no new changes. All three judges returned high-confidence verdicts that the code is correct, that the proposal's prescribed predicate cannot work because of query-text truncation, and that closing the finding requires amending TEST-2 step 4 or recording this deviation.

## Proposed: Row-lock waiter identified by tuple lock instead of query text

**Status:** proposed

**Reported by:** step S5 (TEST-2: tier-2 Postgres row-lock serialization subtest).

**What the proposal says:** TEST-2 step 4 in the non-spec changes directs the test to poll `pg_stat_activity` for a backend whose query matches `ILIKE '%FOR UPDATE%'` with `wait_event_type = 'Lock'`.

**What landed instead:** `awaitSessionRowLockWaiter` in `tests/tier2_component/stores/sessionstore_test.go` keeps the `datname` filter and the `wait_event_type = 'Lock'` condition. It identifies the waiter as the backend that holds a `pg_locks` tuple lock on `'sessions'::regclass` rather than by matching query text. The helper also fails the test when B returns before it blocks.

**Why:** `pg_stat_activity.query` is truncated at `track_activity_query_size`, which is 1024 bytes by default. The select list of the sessions query is longer than that, so the `FOR UPDATE` clause never appears in the recorded text and the proposal's predicate times out on every run. PostgreSQL takes the heavyweight tuple lock before a blocked backend waits on the holder's transaction ID, so the substitute predicate matches only while B is blocked on A's row lock. The step reports that this was already judged unresolvable in code and is recorded for a human decision on TEST-2 step 4.

**What a later reader would otherwise get wrong:** A reader following TEST-2 step 4 would write a query-text poll that never matches and would read the resulting timeout as a store defect. A reader comparing the test to the proposal would find a waiter predicate the proposal does not describe, with no record in the proposal of why the query-text match was replaced.

## Proposed: pgstore.Update now persists workspace_plan

**Status:** proposed

**Reported by:** the operator, from commit c7609e923 (step S5, TEST-2).

**What the proposal says:** The proposal stages no change to `pkg/gateway/session/sessionstore/pgstore`. CODE-2 adds one doc sentence to `Store.Update` and relies on the created → finalizing Update binding the WorkspacePlan into the row.

**What landed instead:** `pgstore.Store.Update` in `pkg/gateway/session/sessionstore/pgstore/pgstore.go` now writes the `workspace_plan` column. Before this change it never wrote that column, so on Postgres the plan that `POST /v1/sessions/{id}/finalize` binds in its entry Update was dropped, while memstore kept it. Two `TestSessionStoreContract` subtests pin the fix: a plan set by the mutation persists and survives a later Update that leaves it alone, and a terminal write queued behind the finalize entry write carries the entry write's plan (commit fd07796d6).

**Why:** TEST-2's row-lock subtest asserts that the stored plan is the first writer's, and that assertion cannot hold against a store that never writes the plan. The step found and fixed the store defect rather than weaken the assertion.

**What a later reader would otherwise get wrong:** A reader of the proposal would conclude that 0082 changed no store behavior. The Postgres store's Update contract changed: every Update now rewrites `workspace_plan` from the row it read under `SELECT ... FOR UPDATE`.
