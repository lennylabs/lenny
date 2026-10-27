# Non-spec changes — Delegation tree membership and lifecycle are under-specified and mis-implemented

## Design (implementation-facing)

- Derive and replay rows carry no delegation parent, so the store makes each its own root (CODE-A).
- Tree recovery reports each node's outcome as it is decided and fails a node only over a locked `resume_pending` row (CODE-B, implementing SPEC-1 and SPEC-2).
- The orphan-cleanup sweeper implements the SPEC-3 Edit (a) orphan set through one exported predicate, which the orphan-cap count shares (CODE-C). Its write runs the full terminal funnel, so a node it ends cascades to its own children.
- The cascade resolves every node's policy through one helper that applies the orphan-cap fallback (CODE-D).
- CODE-E and CODE-F are local fixes with no specification change.

**Sequencing against other proposals.** 0106 applies on top of approved 0104 and 0105 and edits neither.
- CODE-A, CODE-E, and CODE-F have no dependency on another proposal.
- SPEC-2 and SPEC-3 Edit (b) apply after 0105 SPEC-2.
- CODE-B applies after 0105 CODE-6 item 6, which rewrites `sessionStateMachineEdges`.
- CODE-C applies after 0105 CODE-4 and CODE-5, whose per-row continuation and unconditional stamp it keeps and whose root-anchored window check it replaces.
- CODE-D applies after CODE-C and after 0104 CODE-2 item 7, whose per-level primary re-listing stays as staged.

## Staged code changes

### CODE-A · Derive and replay create roots

Targets: `pkg/gateway/sessionserver/derive.go` (the `derived` literal, `DeriveResponse` construction, `persistDeriveFailure`), `pkg/gateway/sessionserver/replay.go` (the `replayed` literal, `ReplayResponse` construction), `pkg/gateway/session/sessionstore/sessionstore.go` (the `ParentSessionID` doc comment), `pkg/gateway/session/sessionstore/creation_fence.go` (the `IsDelegatedChild` comment), `pkg/gateway/session/sessionstore/pgstore/pgstore.go` (the `root_session_id` comment in the insert), `pkg/gateway/session/sessionstore/memstore/memstore.go` (the root comment in the insert).

`derive.go`:
1. Remove `ParentSessionID: source.ID` from the `derived` literal. Keep `ParentWorkspaceRef`.
2. Set `DeriveResponse.ParentSessionID` from `source.ID`, so the `parentSessionId` wire echo is unchanged. Do not copy it from `derived`, which no longer carries it.
3. In `persistDeriveFailure`, remove `ParentSessionID`, and set `ParentWorkspaceRef: source.WorkspaceSnapshot.Ref` when `source.WorkspaceSnapshot != nil`. The audit row keeps lineage and is no longer a node in the source's tree (§15.1 "no tree node is materialised").
4. After `CreateReserved` succeeds, call `s.registerLeaseTree(derived)`. An insert error registers nothing.

`replay.go`:
1. Remove `ParentSessionID` from the `replayed` literal.
2. Set `ReplayResponse.ParentSessionID` from `source.ID`.
3. After `insertReplayed` succeeds, call `s.registerLeaseTree(replayed)`.

Comments:
- Rewrite the sessionstore `ParentSessionID` doc: it is the §8.2 delegating parent, set only by the delegation service; derive and replay leave it empty and record lineage in `ParentWorkspaceRef` (§4.5).
- Update the `creation_fence.go`, `pgstore.go`, and `memstore.go` comments that say a derived session sets the parent or shares the parent's tree.

No store logic changes and no migration. Cite `// spec: §12.6 (StoreRouter), §15.1 (Session Replay Semantics), §12.8 step 16, §4.5 (Workspace lineage), §8.6`.

### CODE-B · Tree recovery fails per level, only over `resume_pending`, and drops the window path

Targets: `pkg/delegation/recovery/recovery.go` (`Node`, `Config`, the reason constants, `Recover`, `budgetElapsed`, `nodeDeadline`), `pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery.go` (package doc, `RecoverTree`, `remainingResumeWindow`), `pkg/gateway/sessionserver/treerecovery.go` (`sessionTerminalMarker` and `FailNode`, the `nodeNeedsRecovery` doc), `pkg/gateway/sessionserver/session_terminal.go` (`expireSession`, `failSession`, new `resumePendingGuard`), `pkg/session/state/state_test.go`, and the test files item 6 names.

1. **Engine.** In `recovery.go`, delete `Node.ResumeWindow` and `ReasonResumeWindow`, and remove the window branch from `budgetElapsed` and `nodeDeadline`, leaving the level and tree budgets. Add `Config.OnResult func(NodeResult)`. `Recover` calls it synchronously on its own goroutine, once per node, at the point it appends that node's result: for a node failed without a start, and for each driven node when `waitNode` returns. Every call for a level therefore precedes the next level's `start` calls. `OnResult` may be nil. `Recover` keeps its signature and return value. Update the `Recover` doc.
2. **Orchestrator.** In `RecoverTree`, build nodes without a window, delete `remainingResumeWindow`, and pass an `OnResult` that does what the loop after `Recover` does today: count the summary, call `o.terminal.FailNode` for a failed node, and increment the level or tree timeout counter. Delete that loop. Update the package doc to drop the per-node window.
3. **Guard.** In `session_terminal.go`, add `resumePendingGuard(r *sessionstore.Session) error`, which returns `sessionstore.ErrSuperseded` unless `r.State == session.StateResumePending`. `failSession` uses it in place of `nonTerminalGuard`. Rewrite the `failSession` doc: its caller is §8.10 tree recovery, it writes `failed` only over a locked `resume_pending` row, and a refused write runs no hook. Delete `expireSession`.
4. **Marker.** `FailNode` calls `failSession` for every reason. The reason is not used to choose the state. Rewrite the `sessionTerminalMarker` doc to match, and rewrite the `nodeNeedsRecovery` doc so it agrees with the new `failSession` doc.
5. **State table.** `sessionStateMachineEdges` in `state_test.go` gains `ResumePending → Failed`, mirroring SPEC-2.
6. **Existing tests the guard and the deletion break.**
   - `pkg/gateway/sessionserver/lifecycle_internal_test.go`: `TestFailSessionEmitsTerminalLifecycle_spec_7_2_2` seeds `session.StateResumePending` in place of `StateRunning`, and its comment describes tree recovery's `failSession` rather than "the start-path failure".
   - `pkg/gateway/sessionserver/terminal_writers_internal_test.go`: the `failSession` case seeds `resume_pending`.
   - `pkg/gateway/sessionserver/terminal_context_internal_test.go`: the row keyed `session_terminal.go writeTerminal` seeds `resume_pending`.
   - `pkg/gateway/sessionserver/terminal_writer_inventory_test.go`: remove `expireSession` from `writeTerminalCallers` and from its comment.
   - `pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery_test.go`: replace `TestRecoverTreeNodeResumeWindow_spec_8_10_1027` with the TEST-1 item 2 orchestrator case.

Cite `// spec: §8.10 (Delegation Tree Recovery), §7.2 (Interactive Session Model), §6.2 (resume_pending wall-clock cap)`.

### CODE-C · Orphan sweeper over the SPEC-3 orphan set

Targets: `pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go` (package doc, `sweepTenant`, `expireOrphan`, `treeRoot`, `archiveOrphan`), `pkg/gateway/sessionserver/usage.go` (`detachExceedsOrphanCap`).

1. **Predicate.** Add to `orphancleanup`:

   ```go
   // Orphan is why a row is in the §8.10 active orphan set.
   type Orphan struct {
       Parent        sessionstore.Session // the direct parent; zero when ParentPresent is false
       ParentPresent bool
       Anchor        time.Time // minimum non-zero TerminatedAt among the row's terminal ancestors; zero when none
   }

   // Orphaned reports whether row is an active orphan under §8.10.
   func Orphaned(row sessionstore.Session, byID map[string]sessionstore.Session) (Orphan, bool)
   ```

   `ParentPresent` is true when `row.ParentSessionID != ""` and `byID` holds it. The ancestor walk starts at the parent and follows `ParentSessionID` through `byID`, with a visited set against a cyclic chain, to the end of the chain, collecting every terminal ancestor's `TerminatedAt`. `ok` is true exactly when `!session.IsTerminal(row.State)`, `row.DelegationDepth > 0`, and any of the following holds: the parent is absent; an ancestor is terminal; or the walk ends at an ancestor with `DelegationDepth > 0` whose own parent link is empty or not in `byID`. `ParentSessionID != ""` is never the delegated-session test, because the foreign key clears it when the parent is deleted.

2. **Dispositions.** `sweepTenant` lists the tenant once, builds `byID`, visits the rows in ascending `DelegationDepth`, calls `Orphaned` as it visits each row, and for each row with `ok`:
   1. increments `activeOrphans`;
   2. when `!ParentPresent`, ends the row `expired` now;
   3. else when the parent is not terminal, skips it;
   4. else when `Parent.CascadeOnFailure.Resolve() == session.CascadeCancelAll`, ends it `cancelled` now;
   5. else skips it while `Anchor.IsZero() || now.Sub(Anchor) <= s.cascadeTimeout`, and ends it `expired` after that.

   An applied end decrements `activeOrphans`, as today, and writes the ended state and `now` into the row's `byID` entry, so a child visited later in the pass sees its parent's end.

3. **Writer.** Generalize `expireOrphan` into `endOrphan(ctx, tenant, row sessionstore.Session, to session.State, now time.Time) (bool, error)`. `cancelled` stamps no `FailureReason`. `expired` keeps 0105 CODE-5's unconditional `expired:deadline` stamp. Both keep the non-terminal locked-row guard, `BeginTerminal`, `MarkTerminated`, and `OnSessionTerminal`, so the funnel's `terminalSignals` runs `cascadeToChildren` for the ended row. Keep 0105 CODE-4's per-row continuation.

4. **Cleanup.** Delete `orphancleanup.treeRoot`. `archiveOrphan` takes `row.RootSessionID`. Update the package doc to the SPEC-3 Edit (a) set and dispositions.

5. **Cap count.** `detachExceedsOrphanCap` builds `byID` from its one `List` and counts rows for which `orphancleanup.Orphaned` is true. Delete its per-row `store.Get` and its `s.treeRoot` call. `Server.treeRoot` stays for its other callers. Update its doc.

Cite `// spec: §8.10 (Orphan cleanup), §7.2 (Terminal writes from any non-terminal state)`.

### CODE-D · The orphan-cap fallback for every node the cascade cancels

Target: `pkg/gateway/sessionserver/usage.go` (`cascadeToChildren`).

1. Add `func (s *Server) effectiveCascade(ctx context.Context, sess sessionstore.Session) session.CascadePolicy`. It resolves `sess.CascadeOnFailure`, and when the policy is `detach` and `detachExceedsOrphanCap` reports over cap, it calls `recordCascadeApplied` with reason `orphan_cap_fallback` and returns `cancel_all`. Otherwise it returns the resolved policy.
2. `cascadeToChildren` calls it at the top in place of the inline block, and delete the `_ = orphanCapFallback` line.
3. In the breadth-first walk, a descendant's children are queued only when `s.effectiveCascade(ctx, updated) == session.CascadeCancelAll`, where `updated` is the descendant's row after its terminal write committed. Under CODE-C's count, that descendant's own live children are then counted as orphans. A descendant whose write did not apply is not evaluated.
4. The walk's listing and `archiveSettledChild` tail stay as 0104 CODE-2 item 7 leaves them.

Cite `// spec: §8.10 (maxOrphanTasksPerTenant fallback)`.

### CODE-E · The deadlock fail write re-checks the live wait, and `timeout` follows the write

Targets: `cmd/lenny-gateway/reconciler_workers.go` (`deadlockFail`), `pkg/gateway/mcpfabric/delegationtree/deadlock/manager.go` (`FailFunc`, `TimeoutAction`, `Manager.Observe`, `Detector.RunOnce`).

1. The `deadlockFail` mutator returns `sessionstore.ErrSuperseded` when the locked row is terminal, when `w.inputWaits` is nil, or when `len(w.inputWaits.PendingDetailsForSession(sessionID)) == 0`. This re-checks the registry the detector's snapshot read, so it covers persisted waits and edge-served waits that leave the row `running`. Do not gate on `State == input_required` (summary D12).
2. `FailFunc` becomes `func(ctx context.Context, tenantID, sessionID string) bool`. It returns true only when the write applied. It returns false on `ErrSuperseded`, and false on a store error, which it logs.
3. `TimeoutAction` gains `DetectedAt time.Time`, which `Observe` fills from the tracked entry.
4. `Observe` no longer calls `ObserveDelegationDeadlockResolution("timeout", ...)`. It still deletes the active entry and returns the action. The existing `resolved` emission for a subtree no longer detected is unchanged.
5. `RunOnce` calls `fail` for each deepest task of an action and records `timeout` once per action, with duration `now.Sub(action.DetectedAt)`, only when at least one call returned true. When none applied it records nothing, and the next sweep re-detects a subtree that is still deadlocked. A store error and a refusal therefore have the same metric outcome.

Cite `// spec: §8.8 (Subtree deadlock detection), §16.1`.

### CODE-F · Pod rollbacks detached from the caller's cancellation

Target: `pkg/gateway/sessionserver/bind_rollback.go` (`rollbackBinding`, `rollbackClaim`).

1. The first statements of both helpers are `ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.streamFailureReportTimeout)` and `defer cancel()`. That field is the §7.1 release allowance, and `Server` init already defaults it. Do not use `terminalContext` (summary D13).
2. Add one doc sentence to each helper: the rollback runs detached from the caller's cancellation, so a client disconnect or a parent teardown cannot strand the claim; the release allowance bounds it; and the §4.6.1 orphan-claim GC bounds a crash mid-rollback.
3. Callers are unchanged.

Cite `// spec: §7.1 (Atomicity of session creation (steps 2–8)); §7.1 (Terminal pipeline context: release allowance bounds the release); §4.6.1 (Orphaned SandboxClaim detection)`.

## Staged schema, chart, and migration changes

None.

## Staged docs changes

The docs state-machine pages list the exits of `resume_pending` and do not draw the `resume_pending → failed` edge SPEC-2 adds: the diagram and the transition table in `docs/reference/state-machines.md`, and the diagrams in `docs/client-guide/session-lifecycle.md` and `docs/getting-started/concepts.md`. No docs edit that adds the edge is staged yet, and the review log carries it as open. The docs cascade table states only the per-policy behavior SPEC-3 keeps.

### REC-1 · Records

Targets: `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` (the §10 P4a entry).

1. On approval, re-own F-8.2.30, F-8.8.18, F-8.8.19, F-8.9.13, F-8.10.19, F-8.10.20, F-8.10.21, F-8.10.22, F-8.10.23, F-8.10.25, and F-8.10.26 to proposal 0106, naming the checklist step for each. When implement-proposal finishes, mark each resolved with the commit and the test names.
2. For F-8.10.19, record the corrected mechanism: the delegated child row never sets `ResumeEligibleUntil`, which only `create.go` and `create_and_start.go` write, so the window path was dead, and SPEC-1 resolves the finding.
3. Re-own F-8.10.17 to approved proposal 0104 (D6, CODE-2, SPEC-3 Edit (a)), with the 0106 SPEC-3 sweeper as a one-interval backstop. Leave it OPEN until 0104 is implemented.
4. Close F-8.10.24 now, citing 3f943f56f and the cascade's `nonTerminalGuard` terminal write in `cascadeToChildren`.
5. File one new entry per item in the summary's **Defects in the shipped tree that this proposal does not stage**.
6. Update the P4a plan entry and the plan position.

## Testing

### TEST-1 · Tests across the reached tiers

Every test carries `// spec:`. Every tier-2-and-up test carries `// diagnosis:`. Randomized tests log their seed.

1. **Derive and replay roots** (tier 1: `pkg/gateway/sessionserver/derive_test.go`, `replay_test.go`, `derive_failure_audit_test.go`). Table over derive from a terminal source, derive with `allowStale` from a live source, and each replay mode. Assert that the row has an empty `ParentSessionID`, that `RootSessionID` equals its own id, that `ParentWorkspaceRef` equals the source snapshot ref, and that the response `parentSessionId` equals the source id. Assert that a recording lease registrar receives the new row once; this is the discriminating assertion, because `registerLeaseTree` skips a row with a parent. An insert error registers nothing. The failure audit row has an empty parent, carries `ParentWorkspaceRef` when the source has a snapshot and none otherwise, and is absent from the source's `ListByRoot`. `// spec: §12.6 (StoreRouter), §15.1 (Session Replay Semantics), §4.5 (Workspace lineage), §8.6`.
   - Tier 2 (`tests/tier2_component/stores/`): a pgstore insert with an empty parent sets `root_session_id` to the row's id. Deleting a delegated child's parent row leaves the child with `parent_session_id` NULL and its `delegation_depth` unchanged, and `orphancleanup.Orphaned` selects it. `// spec: §12.8 step 16, §8.10 (Orphan cleanup)`.
   - Tier 4 (`tests/tier4_integration`): a session derived from a terminal source whose tree root ended more than `cascadeTimeoutSeconds` ago (set short) stays non-terminal across two orphan-cleanup intervals, and a live source's `cancel_all` cascade does not reach it. `// spec: §12.6 (StoreRouter), §8.10 (Orphan cleanup)`.
2. **Recovery engine and orchestrator** (tier 1: `pkg/delegation/recovery/recovery_test.go`, `pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery_test.go`). Record `start` and `OnResult` calls on one sequence. Assert that `OnResult` is called once per node, in traversal order, and that every call for a level precedes the next level's first `start`. Cover a node failed without a start, the tree deadline failing every remaining node, and a nil `OnResult`. In the orchestrator, a node past `ResumeEligibleUntil` gets no special treatment, and a node the watchdog moved to `awaiting_client_action` is counted failed in the summary and its row is unchanged. `// spec: §8.10 (Delegation Tree Recovery)`.
3. **Tree-recovery guard** (tier 1: `pkg/gateway/sessionserver/treerecovery_internal_test.go`, `pkg/session/state/state_test.go`, and the CODE-B item 6 files).
   - Table over every non-terminal state seeded on the locked row: only `resume_pending` is written `failed`, with one hook. Every other state is unchanged, with no hook and no event.
   - A restore that commits `running` after the level deadline is not overwritten.
   - Randomized interleaving of `DriveResume`'s entry write against `FailNode` on one `resume_pending` row: exactly one of `resuming` and `failed` wins, at most one hook runs, and `failed` never follows `resuming`.
   - `sessionStateMachineEdges` holds `ResumePending → Failed`.
   `// spec: §8.10 (Delegation Tree Recovery), §7.2 (Interactive Session Model), §6.2 (resume_pending wall-clock cap)`.
4. **Orphan sweeper** (tier 1: `pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup_test.go`). One case per CODE-C item 2 disposition and item 1 selection clause:
   - a terminal `cancel_all` parent ends the child `cancelled` on the first pass with no `FailureReason`;
   - an absent parent, both an empty link with depth above 0 and a dangling id, ends the child `expired` on the first pass;
   - under a terminal `await_completion` or `detach` parent the child is not ended at exactly `cascadeTimeout` after the anchor and is ended after it;
   - the anchor is the earliest terminal ancestor when a mid-tree ancestor ended before the root;
   - a row whose terminal ancestors all have a zero `TerminatedAt` is counted and not ended;
   - a derived or replayed root (depth 0, no parent) is never selected;
   - a live parent under a live root is not selected;
   - an absent ancestor above a live `detach` parent leaves the row counted and not ended while its parent is ended on the same pass;
   - a row under a live parent is not ended while that parent stays live, and is cancelled on the pass that ends a `cancel_all` parent;
   - a store error on one row's `Update` does not stop the pass;
   - the gauge equals the selected count minus the applied ends.
   A terminal parent above a depth-3 `detach` chain ends every descendant at one `cascadeTimeout` after the parent's `TerminatedAt`, rather than at a multiple of it; this is the discriminating case for the earliest-ancestor anchor. Two sweepers over one store make one write and one hook per row, against memstore here and against pgstore at tier 2 (`tests/tier2_component/stores/`). `// spec: §8.10 (Orphan cleanup), §7.2 (Terminal writes from any non-terminal state)`.
5. **Convergence under faults.**
   - Tier 1 (`pkg/gateway/sessionserver/cascade_on_expire_test.go`, `pkg/gateway/mcpfabric/mcptools/cancel_release_test.go`): a fault-injecting store fails a randomized subset of `List` and `Update` calls in `cascadeToChildren` and `cancelNode`. After the walk and sweeper passes to quiescence, within a bounded pass count, no non-terminal delegated row has a terminal `cancel_all` parent, and every row has at most one terminal write and one hook.
   - Tier 7a (`tests/tier7a_load_local/`): the inline `cascadeToChildren` and two sweepers run as goroutines over one store. Each row gets one terminal write and at most one post-terminal hook, and after quiescence no live delegated row has a terminal `cancel_all` parent.
   `// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)`.
6. **Cap fallback** (tier 1: `pkg/gateway/sessionserver/cascade_on_expire_test.go`). A top-level `detach` session over the cap cancels its children. A cancelled `detach` descendant over the cap has its children cancelled and gets a `cascade_applied` row with reason `orphan_cap_fallback`. Under the cap its children are left running. A cascade that starts at a mid-tree session under a live root, with a cancelled `detach` descendant over the cap, cancels that descendant's children and writes the audit row; this case discriminates CODE-C's count from the root-keyed one. A store error in `detachExceedsOrphanCap` reports not over cap. `// spec: §8.10 (maxOrphanTasksPerTenant fallback)`.
7. **Deadlock** (tier 1: `pkg/gateway/mcpfabric/delegationtree/deadlock/manager_test.go`, and a `deadlockFail` test in `cmd/lenny-gateway`).
   - An answered wait (registry empty, row in any non-terminal state) gets no write and no `timeout`.
   - An edge-served pending wait on a `running` row is failed with DEADLOCK_TIMEOUT and records `timeout` once, with duration from `DetectedAt`.
   - An action with two deepest tasks of which one applies records `timeout` once.
   - A store error makes `FailFunc` return false and log. No resolution of any label is recorded, the entry is deleted, and the next sweep re-detects with a fresh `DetectedAt`.
   - `Observe` alone records no `timeout`.
   - A randomized interleaving of wait `Resolve` against the fail write never fails a task whose wait left the registry before the mutator ran.
   `// spec: §8.8 (Subtree deadlock detection), §16.1`.
8. **Rollbacks.**
   - `pkg/gateway/sessionserver/delegated_child_materialize_test.go` (extend): a table over the materializer's three rollback paths with an already-cancelled request context; each path reaches the binder.
   - `pkg/gateway/sessionserver/bind_rollback_test.go` (new): `rollbackBinding` and `rollbackClaim` reach binder `Release`, `ReleaseSlot`, and `ReclaimClaimed` with a context that is not done and carries a deadline within the release allowance. A binder `Release` that blocks returns once the release allowance, set short in the test, elapses.
   `// spec: §7.1 (Atomicity of session creation (steps 2–8)), §4.6.1 (Orphaned SandboxClaim detection)`.
9. **Spec map.** Add each new test to `tests/spec-map.json` under the sections its `// spec:` names.

## Edge cases and accepted failure modes

- A wait resolved after the `deadlockFail` mutator's registry check and before the write commits is failed anyway. The window is the mutator's duration.
- An orphan-cleanup pass reads a listing that its own ends make stale. A child of a row the pass ended is refused by the locked-row guard when the cascade already ended it, and is otherwise handled on the same or the next pass.
- A replica crash during tree recovery leaves `resume_pending` nodes to the §6.2 cap and the next traversal.

## Files touched on application (non-spec)

- pkg/gateway/sessionserver/derive.go
- pkg/gateway/sessionserver/replay.go
- pkg/gateway/session/sessionstore/sessionstore.go
- pkg/gateway/session/sessionstore/creation_fence.go
- pkg/gateway/session/sessionstore/pgstore/pgstore.go
- pkg/gateway/session/sessionstore/memstore/memstore.go
- pkg/delegation/recovery/recovery.go
- pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery.go
- pkg/gateway/sessionserver/treerecovery.go
- pkg/gateway/sessionserver/session_terminal.go
- pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go
- pkg/gateway/sessionserver/usage.go
- cmd/lenny-gateway/reconciler_workers.go
- pkg/gateway/mcpfabric/delegationtree/deadlock/manager.go
- pkg/gateway/sessionserver/bind_rollback.go
- pkg/session/state/state_test.go
- pkg/gateway/sessionserver/lifecycle_internal_test.go
- pkg/gateway/sessionserver/terminal_writers_internal_test.go
- pkg/gateway/sessionserver/terminal_context_internal_test.go
- pkg/gateway/sessionserver/terminal_writer_inventory_test.go
- pkg/gateway/sessionserver/derive_test.go
- pkg/gateway/sessionserver/replay_test.go
- pkg/gateway/sessionserver/derive_failure_audit_test.go
- pkg/gateway/sessionserver/treerecovery_internal_test.go
- pkg/gateway/sessionserver/cascade_on_expire_test.go
- pkg/gateway/sessionserver/delegated_child_materialize_test.go
- pkg/gateway/sessionserver/bind_rollback_test.go (new)
- pkg/delegation/recovery/recovery_test.go
- pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery_test.go
- pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup_test.go
- pkg/gateway/mcpfabric/mcptools/cancel_release_test.go
- pkg/gateway/mcpfabric/delegationtree/deadlock/manager_test.go
- cmd/lenny-gateway (a `deadlockFail` test file)
- tests/tier2_component/stores/
- tests/tier4_integration/
- tests/tier7a_load_local/
- tests/spec-map.json
- BUILD-GAPS.md
- gateway-runtime-comms-remediation.md
