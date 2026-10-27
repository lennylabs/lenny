# Summary: Delegation tree membership and lifecycle: derive and replay create roots, tree recovery defers the resume window to §6.2 and fails only resume_pending nodes per level, orphan cleanup selects by direct parent and completes interrupted cancel_all cascades, and the deadlock, cap-fallback, and rollback paths conform

## Summary

**Problem statement.** Derive and replay write the source session as the new session's delegation parent, so the orphan sweeper, the cascade, `cancel_child`, sibling scope, and tree recovery treat an independent root as a delegated child, and neither path registers the new root with the §8.6 lease-extension source. §8.10 says a node whose resume window elapses during tree recovery becomes `expired`, while §6.2, §7.2, and §7.3 send it to `awaiting_client_action`, and the code ends it `failed` instead. Tree recovery's terminal write admits a node in `resuming` or one whose restore committed, and it fails nodes only after the whole traversal rather than level by level. The `cancel_all` cascade and `cancelSubtree` stop on a store error with nothing to retry them, orphan cleanup is keyed on the tree root so children of a terminal non-root parent are unbounded, and a detach descendant the cascade cancels skips the per-tenant orphan cap. The deadlock detector fails a task without re-checking that its wait is still pending and records `timeout` before the write. The delegated child's pod rollbacks and coordination-lease release run on the request context.

**What changes.**
- §8.10 tree recovery defers an elapsed resume window to the §6.2 cap and writes `failed` only over `resume_pending`, and §7.2 gains the `resume_pending → failed` edge (SPEC-1, SPEC-2). The recovery engine fails each level before continuing upward, through a guard that admits only `resume_pending` (CODE-B).
- §8.10 orphan cleanup selects every live delegated session with a terminal or absent ancestor, cancels it at once under a terminal `cancel_all` parent, and otherwise bounds it from the earliest terminal ancestor, and the §7.2 any-state clause names the `cancelled` write (SPEC-3). The sweeper and the orphan-cap count implement that set (CODE-C), and the cap fallback applies to every node the cascade cancels (CODE-D).
- Derive and replay stop writing a delegation parent and register the new root for lease extension (CODE-A).
- The deadlock fail write re-checks the live wait under the row lock, and the `timeout` resolution follows an applied write (CODE-E).
- The pod rollbacks and the coordination-lease release run detached from the caller's cancellation, bounded by the release allowance (CODE-F).
- Tests across the reached tiers (TEST-1) and the BUILD-GAPS and plan records (REC-1).

**Decisions.**
- D1. The change adds no field, column, state, error code, RPC, metric, or label. Every addition beyond deleting writes and narrowing one sentence and one guard is required by the problem or by a failure mode the minimal change itself creates.
- D2. Derive and replay create roots by deleting their three `ParentSessionID` writes. §12.6 StoreRouter, §15.1 Session Replay Semantics, and §12.8 step 16 already fix the behavior, and §4.5 `parent_workspace_ref` already carries lineage, so no specification sentence is added. The store's `root_session_id` COALESCE then makes each row its own root, which corrects every downstream reader without editing it.
- D3. Derive and replay call `registerLeaseTree` after their insert, as `create.go` and `create_and_start.go` do. Removing the parent write alone leaves the derived root unregistered, because neither path calls the registrar.
- D4. An elapsed resume window is owned only by the §6.2 watchdog. §8.10 is narrowed to agree with §6.2, §7.2, and §7.3, which removes a second writer on that timer and needs no 0105 `state_entered_at` dependency and no `resume_pending → expired` edge. Tree recovery reads no per-node window. The delegated child row never sets `ResumeEligibleUntil`, so the deleted window path was already dead for every node tree recovery handles.
- D5. Tree recovery writes `failed` only over a locked row in `resume_pending`. That one guard leaves a `resuming` node to the resume driver and the §6.2 `resuming` watchdog, leaves `awaiting_client_action` to §7.3, and never overwrites a committed restore. One per-state §7.2 edge legalizes exactly what the guard admits. Tree recovery is not added to 0105's any-state paragraph, because that would legalize writing over `resuming` and `running`.
- D6. Per-level failure is a per-result callback on the recovery engine, called before the next level's start calls. §8.10 already states the requirement, so this part is code only.
- D7. The existing orphan-cleanup sweeper is the single retry owner for an interrupted `cancel_all` cascade, an interrupted `cancelSubtree`, and an orphan under a terminal non-root parent. Every node either walk left live has a terminal parent, so the next pass selects it, and the sweeper's write runs the full terminal funnel, so convergence propagates down the subtree. Neither walk gets a retry loop, and `cancelSubtree` keeps its walk and result.
- D8. Orphan selection is over live rows with `DelegationDepth > 0` and a terminal or absent ancestor. The `parent_session_id` foreign key is `ON DELETE SET NULL`, so a selection keyed on `ParentSessionID` misses the child of a deleted parent, and only the delegation service writes a non-zero depth. Every disposition is keyed on the direct parent, and a session under a live parent waits for it, so its end never depends on which write reached the parent first. Expiry is anchored on the earliest terminal ancestor's `TerminatedAt`, which replaces 0105 D5's root anchor: a direct-parent anchor would give each detach generation its own window, so a depth-k detach chain would live about k windows, against the §8.10 statements that `cascadeTimeoutSeconds` bounds orphan lifetime and exposure.
- D9. The §7.2 any-state clause names orphan cleanup's `cancelled` write, because the sweeper now writes a state that paragraph does not list.
- D10. The orphan-cap count and the sweeper share one predicate, so the cap, the gauges, and the §8.10 definition describe one set, and the count loses its per-row `Get`.
- D11. One helper resolves a session's effective cascade policy with the orphan-cap fallback, and the cascade calls it for the top session and for each descendant whose cancel applied. Inlining the check at the enqueue site would duplicate the top-level block.
- D12. The deadlock fail mutator re-checks this replica's live wait registry, the same source the detector's snapshot read, under the row lock. A check on `State == input_required` is wrong, because a wait served on the gateway-edge `/mcp` endpoint leaves the row `running`, and DEADLOCK_TIMEOUT would never fire for it. The registry check needs no 0105 dependency. `timeout` is recorded only when a fail applied, and a refused write records nothing, because a refusal is not one of the §16.1 resolutions.
- D13. The rollback helpers and `releaseCoordinationLease` detach from the caller's cancellation and are bounded by the §7.1 release allowance. `terminalContext` is not used, because its budget adds the seal window and a rollback runs no seal and is not a §7.1 terminal pipeline.
- D14. F-8.10.17 is re-owned to approved 0104 (D6, CODE-2, SPEC-3 Edit (a)) and stays open until 0104 is implemented; the SPEC-3 sweeper bounds any residual to one interval. F-8.10.24 is closed citing 3f943f56f.
- D15. 0106 applies on top of 0104 and 0105 by quoted anchor and edits neither proposal. Non-spec changes, **Sequencing against other proposals** is the one home for the ordering.

**Watch out for.**
- `registerLeaseTree` skips a row that carries a parent, so the call CODE-A adds registers nothing unless the parent write is removed in the same change.
- CODE-D's cap check on a cascaded descendant counts that descendant's children only under CODE-C's predicate. Landing CODE-D before CODE-C leaves the fallback silent for a cascade under a live root.
- Do not gate the deadlock fail on `input_required`; see D12.
- The orphan sweeper's selection must not read `ParentSessionID != ""` as "is delegated"; see D8.
- SPEC-2 and SPEC-3 Edit (b) anchor on text 0105 SPEC-2 lands. Applying them before 0105 fails to find the anchor.

## Goals

- Derived and replayed sessions are independent roots that orphan cleanup, the cascade, `cancel_child`, sibling scope, and tree recovery never treat as delegated children, and they are registered for lease extension (F-8.9.13).
- Tree recovery agrees with §6.2, §7.2, and §7.3 on an elapsed resume window, never writes over a node outside `resume_pending`, and fails each level before continuing upward (F-8.10.19, F-8.10.20, F-8.10.21).
- Every live delegated session under a terminated ancestor is ended within one sweeper interval under a `cancel_all` parent, or within one `cascadeTimeoutSeconds` window of the earliest terminal ancestor otherwise, whatever store errors or replica failures interrupted the inline walks (F-8.10.22, F-8.10.23, F-8.10.25).
- The orphan-cap fallback applies to every detach session the cascade cancels (F-8.10.26).
- The deadlock detector fails only a task whose wait is still pending and records `timeout` only for an applied write (F-8.8.18, F-8.8.19).
- A cancelled `delegate_task` request cannot strand a claimed pod or binding (F-8.2.30).

## Non-goals

- A new lineage column or field (for example `derived_from_session_id`) in §8.9 or §15.1. It rests on the refuted premise that the specification has no lineage pointer, and `parent_workspace_ref` is that pointer.
- A §8.9 sentence naming the tree-membership columns. §12.6, §15.1, and §12.8 step 16 already fix the behavior, the defect is the code's writes, and SPEC-3 defines a delegated session by `lenny/delegate_task` creation for the one rule that needs it.
- Adding replay to the §4.5 `parent_workspace_ref` writer list. It is outside the validated problem and is listed under the defects below.
- A store-level check that refuses a row with a `ParentSessionID` and `DelegationDepth` 0. Once the three writes are removed, delegation is the only writer, and a second rule would restate that.
- A store rule that inherits `root_session_id` only when `DelegationDepth > 0`. The COALESCE already yields the row's own id once no parent is written.
- Rewriting every `ParentSessionID` reader to classify membership by `DelegationDepth`. Removing the writes corrects them all. Only the sweeper switches to `DelegationDepth`, to catch the SET NULL case.
- Editing the `migrations/0001` comment that calls `parent_session_id` a derive lineage pointer. It is an applied migration. The sessionstore and store comments are corrected instead.
- Keeping §8.10's `expired` for an elapsed window by anchoring tree recovery on 0105's `state_entered_at` or the resume driver's window (D4).
- Having the watchdog write `expired` for delegated `resume_pending` rows. That contradicts §6.2's "no additional expiry path" and the §7.2 edge list.
- Re-anchoring `ResumeEligibleUntil` on `resume_pending` entry. 0105 D3 rejects that anchor, the field surfaces to clients with creation semantics, and tree recovery reads no window under this design.
- Deleting or re-defining `ResumeEligibleUntil`. It is outside the validated problem and is listed under the defects below.
- Listing tree recovery in 0105's §7.2 any-state terminal-write paragraph (D5).
- Admitting `resuming` in tree recovery's guard (D5).
- Admitting `awaiting_client_action` in tree recovery's guard (D5).
- Guarding tree recovery's write on the `coordination_generation` the traversal listed. The `resume_pending`-only guard already excludes every in-flight or committed restore, and a node back in `resume_pending` is legitimately unrecovered.
- Persisting tree-recovery progress or budgets across a replica crash. The watchdog's per-node `resume_pending` cap bounds every unsettled node, and the next restore starts a new traversal.
- Moving `cascadeToChildren` into the post-terminal hook and replacing the breadth-first walk with per-node recursion. It replaces 0104 CODE-2 item 7's staged loop, adds a primary tenant listing per cancelled node or a new list filter, delays `cancel_all` by a hop per level, and changes behavior for writers the validated problem does not name.
- Deleting `cancelSubtree` and shrinking the `cancel_child` result to the target. The sweeper converges the subtree within one interval, and the result change would churn the mcptools tests for no requirement.
- Making `cancelSubtree` continue past a per-node error with a joined error. With the sweeper as retry owner this is unnecessary, and it would change `cancel_child`'s result semantics.
- In-process retry or backoff loops in `cascadeToChildren` and `cancelSubtree`. They do not survive a replica crash, they add tuning settings, and the sweeper covers both walks from durable rows.
- A durable cascade-pending marker, outbox table, or persisted orphan-cap fallback decision (D7). The residual, in which children a fallen-back cascade missed are expired after `cascadeTimeoutSeconds` rather than cancelled at once, is bounded and accepted.
- Having the sweeper write `expired` for children of a `cancel_all` parent. The same parent policy would then end children `cancelled` or `expired` depending on a transient error.
- Having the sweeper re-run the parent's full cascade, including a fresh orphan-cap check. That would cancel a detach parent's children retroactively when the tenant later crosses the cap, which §8.10 does not allow.
- Re-checking `maxOrphanTasksPerTenant` inside the sweeper, or moving the cap check there. §8.10 places the fallback at detach time.
- Root-anchored or direct-parent-anchored orphan expiry. The root anchor leaves F-8.10.25 open, and the direct-parent anchor gives each detach generation its own window (D8).
- A grace period before the sweeper cancels children of a `cancel_all` parent. The locked-row guard and the terminal claim already give one write and one hook when the sweeper and the inline cascade race.
- Leader-gating orphan cleanup. 0105 D2 shows that the locked-row write makes every-replica sweeping safe.
- Changing the `parent_session_id` foreign key to RESTRICT or CASCADE. That interacts with §12.8 erasure ordering, and selecting by `delegation_depth` handles SET NULL with no schema change.
- Applying the orphan-cap fallback inside `cancelSubtree` (`lenny/cancel_child`). The validated F-8.10.26 names only the `usage.go` cascade, so this is listed under the defects below.
- Gating the deadlock fail on `State == input_required`, or matching 0105's `input_wait_id` in the re-check. The state check blocks edge-served waits (D12), and the registry re-check under the row lock fences an answered wait without carrying wait identities through the detector.
- Recording `resolved` for a refused deadlock write (D12).
- Keeping the deadlock Manager's active entry on a store error so the next tick retries at once. Re-detection with a fresh window bounds that case, and the problem does not exhibit it.
- Persisting the deadlock active set or detecting deadlocks across replicas. Each wait is bounded by `maxRequestInputWaitSeconds`, and this is outside the problem.
- Fixing the rollback context only at the materializer's call sites. The rollback helpers are the single choke point for every caller.
- An unbounded `context.WithoutCancel` in the rollback helpers, or `terminalContext`'s seal-inclusive budget. The first leaves the outbound release calls unbounded, and the second is the wrong bound (D13).
- Splitting the members into separate proposals, or landing the code-only members as direct BUILD-GAPS fixes. Each member's sequencing is stated per step, and the cascade and orphan members share one §8.10 edit.
- Restaging F-8.10.17 or F-8.10.24. Only records are needed (D14).

## Open decisions for human to make

None yet. The review loops add entries here.

## Defects in the shipped tree that this proposal does not stage

- **(a) Terminal writers outside the `usage.go` cascade skip the cascade and the orphan-cap fallback.** MCP `cancelSession` passes no terminal signals, `cancelSubtree`'s signals are only `archiveCancelled`, and `failSession`'s tail is `settledChildSignals`, which runs only the archive and the lifecycle signal. The validated F-8.10.26 names only the `usage.go` cascade. The SPEC-3 sweeper bounds the `cancel_all` children these writers leave live.
- **(b) A cascaded descendant's post-terminal hook runs `archiveSettledChild` as its signal tail.** It runs the archive tail rather than the funnel's `terminalSignals`. CODE-D moves only the descendant's cascade decision into the walk, and the rest of the signal set is outside the validated problem.
- **(c) `ResumeEligibleUntil` is creation-relative and echoed to clients.** Only `create.go` and `create_and_start.go` write it, and once CODE-B deletes `remainingResumeWindow` nothing reads it as a deadline. Whether to delete or redefine the field is outside the validated problem.
- **(d) §4.5 Workspace lineage names derive as the `parent_workspace_ref` writer but omits replay, which also writes it.** The lineage-doc inconsistency is outside the validated problem.
- **(e) The deadlock Manager emits `resolution="resolved"`, which §16.1 does not list, and the resolution metric carries no `pool` label.** The conformance gap is outside F-8.8.18 and F-8.8.19. CODE-E neither extends nor removes the existing emission.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0092 (session suspend and resume) | Implemented | REC-1 closes F-8.10.24 citing 0092's commit 3f943f56f, and resolves the F-8.10.19, F-8.10.20, and F-8.8.18 residuals 0092 handed to P4. | Nothing. 0092 is not edited. |
| 0104 (session-scoped and delegation token gaps) | Approved (2026-10-27, status record) | CODE-D edits the enqueue decision in `cascadeToChildren` on top of 0104 CODE-2 item 7's per-level primary re-listing, which stays as staged. CODE-A rewrites only comments in `creation_fence.go`, where 0104 CODE-2 adds `ParentSessionID` and `ParentTerminal` to `CreationMarkers`. 0104's rule 5 keys on `IsDelegatedChild`, which is delegation depth, so removing `ParentSessionID` from derived and replayed rows does not change what rule 5 refuses. REC-1 re-owns F-8.10.17 to 0104 D6, CODE-2, and SPEC-3 Edit (a). Every 0104 deliverable survives. | 0104 lands its CODE-2 before 0106 CODE-D. Whichever proposal lands second applies the `creation_fence.go` comment edits by quoted anchor. 0104's files are not edited. |
| 0105 (per-state deadlines and watchdog sweeps) | Approved (2026-10-27, status record) | SPEC-2 inserts a line into the §7.2 block 0105 SPEC-2 edits, and SPEC-3 Edit (b) amends the orphan-cleanup clause of the any-state paragraph 0105 SPEC-2 Edit (c) lands. CODE-C replaces 0105 CODE-5's root-anchored window check with the earliest-terminal-ancestor anchor and keeps CODE-4 and CODE-5's unconditional `expired:deadline` stamp. CODE-B adds one edge to the `sessionStateMachineEdges` table 0105 CODE-6 item 6 rewrites. CODE-C item 6 reseeds the orphan cases of 0105 TEST-1 items 4 and 6. 0105 D5's root anchor is superseded; no other 0105 decision reopens. 0105 CODE-5 has not landed, so CODE-C replaces a staged deliverable rather than landed code. | Approving 0106 supersedes 0105 D5 before 0105 is implemented, and that is the human's call. 0105 lands SPEC-2, CODE-4, CODE-5, and CODE-6 before the 0106 steps that depend on them. 0105's files are not edited. |

## Deliverable index

- SPEC-1 — spec/08_recursive-delegation.md — §8.10 tree recovery fails only `resume_pending` nodes on a level or tree budget and leaves an elapsed resume window to the §6.2 cap
- SPEC-2 — spec/07_session-lifecycle.md — the §7.2 `resume_pending → failed` tree-recovery edge
- SPEC-3 — spec/08_recursive-delegation.md, spec/07_session-lifecycle.md — §8.10 orphan selection and disposition, and the §7.2 any-state clause naming `cancelled` and `expired`
- CODE-A — pkg/gateway/sessionserver/derive.go, pkg/gateway/sessionserver/replay.go, pkg/gateway/session/sessionstore/, and the existing tests it names — derive and replay create roots and register them for lease extension
- CODE-B — pkg/delegation/recovery/recovery.go, pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery.go, pkg/gateway/sessionserver/treerecovery.go, pkg/gateway/sessionserver/session_terminal.go, pkg/session/state/state_test.go, and the existing tests it names — per-level failure through a `resume_pending`-only guard, with the window path deleted
- CODE-C — pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go, pkg/gateway/sessionserver/usage.go, and the existing tests it names — the orphan predicate, sweeper dispositions, and the cap counting the same set
- CODE-D — pkg/gateway/sessionserver/usage.go — the orphan-cap fallback for every node the cascade cancels
- CODE-E — cmd/lenny-gateway/reconciler_workers.go, pkg/gateway/mcpfabric/delegationtree/deadlock/manager.go, and the existing tests it names — the live-wait re-check and the `timeout` metric after an applied write
- CODE-F — pkg/gateway/sessionserver/bind_rollback.go, pkg/gateway/sessionserver/coordination_lease.go — detached, release-allowance-bounded rollbacks
- TEST-1 — pkg/, cmd/lenny-gateway/, tests/tier2_component/stores/, tests/tier4_integration/, tests/tier7a_load_local/, tests/tier8_chaos/, tests/spec-map.json — tests across the reached tiers
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — finding re-ownership and closure, new defect entries, and the P4a plan entry
