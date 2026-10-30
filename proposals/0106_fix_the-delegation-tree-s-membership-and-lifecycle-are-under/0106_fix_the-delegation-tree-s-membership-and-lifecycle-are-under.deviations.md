# Deviations — 0106_fix_the-delegation-tree-s-membership-and-lifecycle-are-under

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: anyStateWriters row for resume_pending to failed removed

**Status:** accepted: once the §7.2 per-state block lists `resume_pending -> failed`, the `anyStateWriters` row breaks that table's stated invariant, and the loop over every non-terminal state still checks the edge (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S5 (CODE-B: tree recovery fails per level, only over resume_pending, window path deleted).

**What the proposal says.** Non-spec changes, CODE-B item 5, states that `sessionStateMachineEdges` in `pkg/session/state/state_test.go` gains the edge `ResumePending -> Failed`. It says nothing about the `anyStateWriters` table in the same file.

**What landed instead.** The edge was added, and the `{"operator force-terminate", ResumePending, Failed}` row was also removed from `anyStateWriters` in `pkg/session/state/state_test.go`. The comment on that table says it lists only edges that the per-state block does not list.

**Why.** Once the §7.2 per-state block lists `resume_pending -> failed`, that row breaks the table's own stated invariant. The same test's loop over every non-terminal state still checks that `resume_pending -> failed` is valid, so the step reports no loss of coverage.

**What a later reader would otherwise get wrong.** A reader comparing the tree to CODE-B item 5 would find the `anyStateWriters` row missing and could take it for an unintended deletion, or restore it and break the table's invariant.

## Proposed: tree recovery race test drives the resume entry hook rather than all of DriveResume

**Status:** accepted: the entry hook makes the same `resume_pending -> resuming` write `DriveResume` makes, and the full driver needs an envtest pod binder at tier 2 (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S5 (CODE-B: tree recovery fails per level, only over resume_pending, window path deleted).

**What the proposal says.** TEST-1 item 3 calls for a randomized interleaving of `DriveResume`'s entry write against `FailNode` on one `resume_pending` row.

**What landed instead.** `TestTreeRecoveryFailRacesResumeEntry_spec_8_10` in `pkg/gateway/sessionserver/treerecovery_internal_test.go` calls the resume driver's entry write directly (`newEntryHook(...).run`) and races it against `sessionTerminalMarker.FailNode`. It does not run the whole of `DriveResume`.

**Why.** At tier 1 the full `DriveResume` path either runs without a binder, in which case it makes no entry write, or needs a pod binder on envtest, which is tier 2. The entry hook makes the same `resume_pending -> resuming` write that `DriveResume` makes, so the step reports that the race under test is the same one.

**What a later reader would otherwise get wrong.** A reader would assume the test exercises the whole `DriveResume` path at tier 1, including the steps before and after the entry write, when it covers only the entry write racing `FailNode`.

## Proposed: doc comment updated in finalize_race_internal_test.go

**Status:** accepted: only the doc comment changed, and it now describes the `resume_pending`-only guard the test exercises (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S5 (CODE-B: tree recovery fails per level, only over resume_pending, window path deleted).

**What the proposal says.** Non-spec changes, CODE-B item 6, lists the existing test files to fix. `pkg/gateway/sessionserver/finalize_race_internal_test.go` is not among them.

**What landed instead.** Only the doc comment of `TestFailSessionWritesNothingOverATerminalRow_spec_7_2` in `pkg/gateway/sessionserver/finalize_race_internal_test.go` was changed, so that it says the `failSession` guard admits only a `resume_pending` row.

**Why.** The old comment described the replaced guard, which admitted any non-terminal row. The test's behavior is unchanged.

**What a later reader would otherwise get wrong.** A reader would find a file touched that CODE-B item 6 does not list and could take it for scope creep or a behavioral change, when the change is limited to the comment.

## Proposed: endOrphan signature takes no now argument

**Status:** accepted: `TerminatedAt` is stamped from the clock inside the row's locked `Update`, as the proposal says to keep, so a `now` parameter would be unused; the proposal text is a historical record and is not corrected (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S6 (CODE-C: orphan sweeper over the SPEC-3 orphan set; cap count shares the predicate).

**What the proposal says.** The non-spec changes for the orphan sweeper (the `endOrphan` generalization, §8.10 Orphan cleanup, and §7.2 Terminal writes from any non-terminal state) give the signature `endOrphan(ctx, tenant, row sessionstore.Session, to session.State, now time.Time) (bool, error)`.

**What landed instead.** `endOrphan` in `pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go` is declared as `endOrphan(ctx context.Context, tenant string, row sessionstore.Session, to session.State) (bool, error)` and takes no `now` argument. `sweepTenant` still writes the pass's `now` into the `byID` entry after an applied end.

**Why.** The same item says to keep the earlier terminal-stamp behavior, in which `TerminatedAt` is read from the clock inside the row's locked `Update` because hooks dispatched for earlier rows can run past the pass's `now`. A `now` parameter would go unused, so the step left it out of the landed signature. The step reports that the proposal's signature does not match the behavior the proposal says to keep, and that a human should decide whether to correct the proposal text.

**What a later reader would otherwise get wrong.** A reader comparing the tree to the proposal would expect `endOrphan` to take a `now` argument and could add one, or could conclude that `TerminatedAt` is stamped from the pass's `now` rather than from the clock inside the locked `Update`.

## Proposed: rollback context checks observed at the adapter Shutdown RPC

**Status:** accepted: `podsession.Binder` runs every claim write on its own detached release context, so the adapter `Shutdown` RPC is the only binder call where the caller's allowance is observable, and `ReclaimClaimed` was already detached (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S9 (CODE-F: rollbacks and coordination-lease release detached from caller cancellation).

**What the proposal says.** Testing, Rollbacks: `bind_rollback_test.go` asserts that the binder's `Release`, `ReleaseSlot`, and `ReclaimClaimed` each receive a context that is not done and whose deadline lies within the release allowance.

**What landed instead.** In `pkg/gateway/sessionserver/bind_rollback_test.go`, the test observes the context for `Release` and `ReleaseSlot` at the adapter `Shutdown` RPC on a fake gRPC adapter, which is the only binder call that receives the caller's context. For `ReclaimClaimed`, the test asserts only that the claim delete receives a live context that has a deadline. It does not compare that deadline with the release allowance.

**Why.** `podsession.Binder` is a concrete type, and every claim write inside it runs on its own `detachedReleaseContext` (`WithoutCancel` plus the `slotCleanupFloor` of 5s). That context drops the rollback's allowance deadline, so the allowance is not observable at the cluster client. `ReclaimClaimed` makes no call that carries the caller's context, so it was already detached from caller cancellation before this change.

**What a later reader would otherwise get wrong.** A reader would assume the test pins the release-allowance deadline on all three binder calls at their entry points. The test checks the allowance only at the adapter `Shutdown` RPC for `Release` and `ReleaseSlot`, and it does not check the allowance at all for `ReclaimClaimed`.

## Proposed: F-8.10.17 closed rather than left open for 0104

**Status:** accepted: proposal 0104 is Implemented (merge `aab694190`), its creation fence and the commits the resolution note cites are on this branch, and REC-1 item 3's hold condition is met (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S11 (REC-1 remainder: resolve the re-owned findings and update the plan position).

**What the proposal says.** Non-spec changes, REC-1 item 3, re-owns F-8.10.17 to proposal 0104 and leaves it OPEN until 0104 is implemented.

**What landed instead.** Proposal 0104 is Implemented (merge aab694190), and its own records left F-8.10.17 open. This step closed F-8.10.17 in `BUILD-GAPS.md` with a `**Resolution (proposal 0104, 2026-10-29)**` note. The note cites the creation fence commits 61296a96a, f180369d1, and d942fc054, the materializer's created-state guard from proposal 0092 (3f943f56f), and the 0106 sweeper backstop (4ce1eb45b), and it names the existing pinning tests.

**Why.** The step's task said to close or annotate F-8.10.17 once 0104's hold condition was met. The step reports that the fence closes the race the finding describes, so it closed the finding rather than annotating it.

**What a later reader would otherwise get wrong.** A reader comparing `BUILD-GAPS.md` to REC-1 item 3 would expect F-8.10.17 to be OPEN under 0104, or annotated without being closed. The reader could also attribute the closure to 0104's own records, which left the finding open, when 0106's step S11 recorded it.

## Proposed: F-8.8.18 closed on the answered-wait re-check only

**Status:** accepted: F-8.8.18's title defect, the answered wait, is discharged; the `lenny/request_input` handler in `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` cancels its registry wait when the runtime call's context ends, so a pod loss that ends the call drops the wait on that replica, and the cross-replica wait store stays with R20 and F-8.8.17 (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S11 (REC-1 remainder: resolve the re-owned findings and update the plan position).

**What the proposal says.** REC-1 item 1 says to mark F-8.8.18 resolved when the implementation finishes.

**What landed instead.** F-8.8.18 is closed on the answered-wait re-check. Its resolution note in `BUILD-GAPS.md` and the P4a plan entry also record that the finding's other suggested half, which drops a wait on a pod loss, is not part of this change.

**Why.** The finding's Gap text also names a stale wait that survives a pod loss. Proposal 0106 did not stage that half, and this step did not confirm whether a defect remains there.

**What a later reader would otherwise get wrong.** A reader seeing F-8.8.18 marked resolved would assume both halves of its Gap text are fixed, including the stale wait that survives a pod loss. That half is unaddressed and unverified.

## Proposed: maxLevelRecoverySeconds flag help text left unchanged

**Status:** resolved: the help text was a defect rather than a behavior change, and commit `fee24e1bf` now states that the level deadline fails each unrecovered node at that depth that is still in `resume_pending` (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `cmd/lenny-gateway/flags.go`.

**What landed instead.** `cmd/lenny-gateway/flags.go` is unchanged. The help text of the `delegation.maxLevelRecoverySeconds` flag still states: "§8.10 delegation.maxLevelRecoverySeconds: maximum time the gateway waits for all nodes at a single tree depth to complete recovery before marking the unrecovered ones as terminally failed. Default 120s. ..."

**Why.** The text contradicts the landed spec. §8.10 Delegation Tree Recovery now states: "the gateway marks each unrecovered node at that level that is still in `resume_pending` as terminally `failed` ... Tree recovery does not write a node that has left `resume_pending`." The text is a flag definition rather than a comment, so changing it changes behavior the proposal did not stage, and the sweep left it as it was.

**What a later reader would otherwise get wrong.** A reader of `cmd/lenny-gateway/flags.go` would take the old contract, in which every unrecovered node at the level is failed, as current.

## Proposed: anyStateTerminalTargets admits cancelled in pkg/session/state/state.go

**Status:** accepted: the any-state `-> cancelled` edge is the one the landed §7.2 "Terminal writes from any non-terminal state" paragraph lists for orphan cleanup, and `TestValidTransitionsAdmitEveryAnyStateTerminalWriter_spec_7_2` and `TestOrphanCancelEdgeIsValidFromEveryNonTerminalState_spec_7_2` pin it (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step sweep (stale code the landed spec edits left behind), and corrected by the design-conformance fix pass after commit 340066028.

**What the proposal says.** Spec changes, the §7.2 edit of the "Terminal writes from any non-terminal state" paragraph, states that orphan cleanup moves an orphaned delegated session to `cancelled` or `expired`. The proposal's non-spec file list stages no change to `pkg/session/state/state.go` or `pkg/session/state/state_test.go`.

**What landed instead.** Commit 340066028 changed `pkg/session/state/state.go`, a file outside the proposal's staged non-spec file list. The declaration is now:

```go
var anyStateTerminalTargets = []State{Failed, Expired, Cancelled}
```

`appendAnyStateTerminalEdges` therefore adds a `-> cancelled` edge from every non-terminal state, so `ValidTransitions` now admits `created`, `finalizing`, `ready`, and `starting` to `cancelled`. The same commit added the orphan cleanup `-> cancelled` rows (`created`, `finalizing`, `ready`, and `starting`) to `anyStateWriters` in `pkg/session/state/state_test.go`, and added `pkg/gateway/mcpfabric/delegationtree/orphancleanup/state_contract_test.go`. The tier-1 tests `TestValidTransitionsAdmitEveryAnyStateTerminalWriter_spec_7_2` and `TestOrphanCancelEdgeIsValidFromEveryNonTerminalState_spec_7_2` pin the edge.

**Why.** `endOrphan` in `orphancleanup.go` writes `StateCancelled` over an orphan of any non-terminal state under a terminal `cancel_all` parent, as §8.10 Orphan cleanup now requires. Without the edge, that write was a transition the landed §7.2 paragraph admits and the state contract rejected. The change makes the code match the landed §7.2 text. It stays a proposed deviation because the proposal did not stage `state.go` or `state_test.go`.

**What a later reader would otherwise get wrong.** A reader comparing the tree to the proposal's staged file list would find `state.go` and `state_test.go` changed without a staged item and could take the any-state `-> cancelled` edge for an unreviewed widening of the state machine, or revert it and break the orphan sweeper's cancel of a pre-running orphan.
