# Summary: Per-state session deadlines measured from a store-owned state-entry instant, and watchdog sweeps that re-check, continue, and run on every replica

## Summary

**Problem statement.** The watchdog sweeps that enforce the §6.2, §7.2, and §7.3 per-state deadlines measure each deadline from `UpdatedAt`, which every write rewrites. Three of the sweeps re-check only the state on the locked row, and the expiry writers keep a stale `FailureReason` from an earlier recovery. A sweep pass stops on the first error and runs terminal hooks inline. The orphan-session reconciler runs on every replica although §10.1.4 makes it leader-only. No code writes `input_required` to the row, so a `lenny/request_input` wait can be idle-expired. `/start` launches while the row is still `ready`, so the ready watchdog can fail a timely start. No sweep enforces `perChildMaxAge`. The specification has no persisted state-entry instant. It lists no edges for terminal writes the code makes from the recovery states, assigns no replica to most sweeps, and does not say which session `perChildMaxAge` bounds.

**What changes.**
- §6.2 gains the persisted `state_entered_at` instant and restarts the idle clock on every entry to `running` (SPEC-1). The session store owns the column (CODE-1), and every watchdog sweep and orphan cleanup read it or the root's `TerminatedAt` (CODE-2, CODE-5).
- §7.2 gains the `ready → starting` edge for `/start`, the `/start` failure edge in place of `starting → resume_pending`, one paragraph of terminal writes from any non-terminal state, and `maxSessionAge` expiry edges from `starting`, `resume_pending`, and `resuming`, with conforming edits in §6.2, §7.1, §7.3, and §15.1 (SPEC-2). `/start` writes `starting` before the launch, and each client SDK's `Start` sends its request once (CODE-6).
- §8.2 states that `perChildMaxAge` counts from the child's creation (SPEC-3), and `sweepMaxAge` enforces it from the child's own row (CODE-3).
- §10.1.1 assigns the watchdog sweeps and orphan cleanup to every replica, with a locked-row re-check and per-row continuation (SPEC-4). The sweeps continue past errors and run hooks off the pass (CODE-4), and the orphan-session reconciler moves under the leader gate (CODE-8).
- `lenny/request_input` writes `input_required` and back on the row and emits `status_change` on both edges (CODE-7). The row names the newest wait, and the exit write applies only when the locked row names its own wait. A replica that accepts a pod process's `LNK-GWCONTROL` connection returns that pod's `input_required` rows with no local pending wait to `running` (D13).
- TEST-1 adds tests across the reached tiers, and REC-1 updates the BUILD-GAPS and remediation-plan records.

**Decisions.**
- D1. Most items are code catching up to existing text. Spec edits stay to one clause or paragraph on an existing surface in §6.2, §7.1, §7.2, §7.3, §8.2, §10.1.1, and §15.1.
- D2. The watchdog and orphan cleanup stay on every replica. A leader-gated watchdog would break §6.2 and §7.3, because `DriveResume`, `SuspendIdle`, `ReleaseSuspendedPod`, and `ResumeBacklog` act only on the coordinating replica. Since 0092, the row lock and the sweep sentinels allow one write and one hook per session.
- D3. One anchor, `state_entered_at`, is written by the store inside `Update` only when `State` changes. The store discards a mutator's value, and `Create` sets it to the creation instant. `UpdatedAt` keeps its change-detection and age-accrual role, because `AccrueSessionAge` depends on it advancing on every write.
- D4. `SuspendedAt` stays the suspended anchor and identity guard that 0092 made it. Only its zero-value fallback moves from `UpdatedAt` to `StateEnteredAt`. SPEC-1 carves the suspended deadlines out of the `state_entered_at` rule for this reason.
- D5. Orphan cleanup measures the cascade window from the root's `TerminatedAt`. §8.10 "root session has reached a terminal state" already implies that anchor, so neither a column nor a spec edit is needed.
- D6. Each terminal watchdog mutator recomputes its deadline from the locked row. A row that left and re-entered the state has a newer `StateEnteredAt`, so the same check covers it.
- D7. Hooks leave the serial pass. A goroutine owns the terminal claim's `end()`, following `budgetSessionTerminator`. §7.1 **Terminal pipeline context:** already lets the pipeline outlive the task that made the write, so no spec text is added.
- D8. `/start` reuses the existing `starting` state, its watchdog, and `STARTING_TIMEOUT`, as §15.1 already describes. Every `/start` failure after the `ready → starting` write takes `starting → failed`: on the launch-failure path the binder has already reclaimed the prepared pod, so a same-row retry has nothing to reconnect to, and one outcome keeps `STARTING_FAILED` one client contract. The `starting → resume_pending` edge is removed, because the launch runs inside the `/start` request and no writer produces it. No `start_requested_at` field and no `starting → ready` edge are added. The `starting` watchdog also bounds a concurrent-workspace slot's setup at `/start` (SPEC-2 Edit (i)). A finalizing-style `maxStartingTimeoutSeconds ≥ setupTimeoutSeconds` rejection would refuse every such pool under the default 120 s and 300 s unless the starting default rose for every pool.
- D9. `perChildMaxAge` binds the session whose row stores it, which is the slice the parent granted. It is measured from `CreatedAt` in every non-terminal state and expires the session with `expired:lease`. Reading the child's own row keeps §8.6 "Existing children are unaffected" true.
- D10. The proposal does not store a policy-derived `perChildMaxAge` default on a child that declares no slice. The code carries no `DelegationPolicy` `perChildMaxAge` default to store. SPEC-3 therefore states no parent-cap rule, and the unbounded child is filed as a defect.
- D11. F-7.2.31 is resolved by one §7.2 paragraph that legalizes the existing any-state terminal writers. The budget terminator is kept unnarrowed, because it also carries credential-lease expiry and fallback exhaustion and no re-enqueue budget evaluation exists to catch a narrowed-away case.
- D12. `sweepMaxAge` keeps expiring an over-cap paused row on the `maxSessionAge` expiry edges of §7.2 (SPEC-2 Edits (a), (d), and (e), and the existing `awaiting_client_action → expired` edge). §6.2 pauses the timer in `suspended` and does not run it in `ready` or `finalizing`, so the expiry is listed per source state rather than in the any-state paragraph. `readResumable` refuses an over-cap `resume_pending` row, which would otherwise wait out `maxResumeWindowSeconds`. The F-6.2.28 remainder is recorded as resolved without a code change.
- D13. `lenny/request_input` persists `input_required` on the row, and two mechanisms keep the persisted state consistent with the live wait (owner decision, 2026-10-27). First, the row carries the request ID of the newest wait. The entry write stamps it, including on a row that is already `input_required`, and the exit write applies only when the locked row names its own wait, so an earlier call's exit cannot return a later call's wait to `running`. Any new column goes in migration 0192, which CODE-1 already adds. Second, recovery runs at the connection boundary. §28.3 **Link register** makes `LNK-GWCONTROL` one connection per pod process to one replica, so a replica that accepts a pod process's connection returns that pod's `input_required` rows that have no local pending wait to `running`. This covers a replica restart and an adapter reconnect. Idle evaluation therefore skips a waiting session on every replica, which is the existing §7.2 `input_required` edge and §6.2 idle row that D1 has the code catch up to, and the idle-expiry fix does not wait on plan item R20. Five alternatives are rejected. Accepting a stale `input_required` row and disclosing it in the specification is rejected because it writes the mechanism's residual failure modes into normative text and contradicts Goal 4 and D1. A time-bounded restore is rejected because it needs a wait-deadline column, a sweep, and a per-runtime wait-cap resolver. An exit-write retrier is rejected because it covers a failed write and leaves a process exit uncovered. A restore from a replica that is not the waiter is rejected because only the waiter's in-memory `InputWaits` can tell a stale row from a live wait. A restore by the coordination Sweeper is rejected for the same reason, because the coordination lease holder is not the waiter. The redesign pass designs CODE-7 items 1 to 3, SPEC-2 Edit (l), and the TEST-1 cases for them against this decision.
- D14. Only the §10.1.4 orphan-session reconciler moves under the existing `leaderGate`, because §10.1.4 already says leader-only.

**Watch out for.**
- CODE-7 item 4 is the set of gates to decide. Skipping a row turns a `request_input` wait into a refusal or a misreported state.
- SPEC-2 changes edges in three spec files. Apply all of its edits in one step, or §6.2 and §7.2 contradict each other on the `/start` launch failure.

## Goals

- Measure every per-state deadline from the session's entry to the state, so a write that does not change state does not extend a deadline.
- Make each terminal sweep's write apply only when the locked row still meets the sweep's deadline, and stamp the sweep's own reason.
- Keep one failing row or tenant from stopping a sweep pass, and keep a slow terminal hook from delaying the rest of the pass.
- Persist the `lenny/request_input` wait on the row with the identity of the newest wait, and return a row whose wait no longer exists to `running` when the pod process's connection is accepted, so idle evaluation skips a waiting session on every replica (D13).
- Route `/start` through `starting`, so the ready watchdog cannot fail a timely start.
- Enforce `perChildMaxAge` on the child it was granted to.
- State the persisted anchor, the any-state terminal writes, the `perChildMaxAge` binding, and the sweep replica rules in the specification.
- Run the §10.1.4 orphan-session reconciler on the gateway leader only.

## Non-goals

- Leader-gating the whole watchdog. `DriveResume`, `SuspendIdle`, `ReleaseSuspendedPod`, and `ResumeBacklog` act only on the coordinating replica, so a leader-only watchdog stops them for every session the leader does not coordinate.
- Splitting the watchdog into a leader pass and a coordinated pass. It adds a second runner and a scheduling rule to save duplicate listing, which the locked-row rule already makes harmless and which F-10.1.36 covers. It would also move the §10.1.1 `resuming` watchdog to the leader.
- Leader-gating orphan cleanup. Every replica with the locked-row write and the `TerminatedAt` anchor resolves the replica-assignment gap with no code change.
- A `start_requested_at` column with a ready-sweep condition. It adds a field and a spec rule where §15.1 already routes `/start` through `starting`.
- A `starting → ready` rollback edge. The binder has already reclaimed the prepared pod on the launch-failure path, so a retry on the same row has nothing to reconnect to.
- Extending the ready window by the launch bound. It would hold abandoned pods past the stated `maxReadyTimeoutSeconds`.
- Bounding the `/start` launch context by `maxStartingTimeoutSeconds`. A late commit takes row X1 of CODE-6 item 1's **`/start` exits** table.
- A dedicated §7.1 `failureClass` for a failed `/start`. `failStarting` leaves the class empty, as `failFinalizing` does for `finalizing → failed`, and §7.1 makes a new class a coordinated change across §7.1, §7.2, §15.1, and §16.
- Acting on a pod failure report that arrives between the launch and the `starting → running` commit. `ReportSessionFailure` treats a report on `created`, `finalizing`, `ready`, or `starting` as a no-op, so the window existed for the `ready` row the launch held before this change, and CODE-6 moves it to `starting` without widening it.
- Asserting how many Shutdown RPCs reach the pod in row X1 of CODE-6 item 1's **`/start` exits** table. The watchdog's by-name reclaim and the terminal binding release can both reach the pod, as they can for a `ready` row today, and TEST-1 item 13 asserts the settled end state.
- Remapping `writePodClaimError`'s typed codes at `/start`. After the `ready → starting` write every launch failure leaves the session `failed`, and the SDK `Start` retries none of them (CODE-6 item 7).
- Restoring a stale `input_required` row from a replica that does not hold the pod process's `LNK-GWCONTROL` connection, including the coordination Sweeper (D13).
- Sequencing F-11.3.39 behind R20, the shared input-wait store (D13).
- A `wall_clock_deadline` column on the child. The child's own stored slice already holds its bound, and an extension does not change it.
- Reading `perChildMaxAge` from the parent's live lease. It contradicts §8.6 "Existing children are unaffected".
- Writing a granted §8.6 seconds extension back to the requesting session's row. Under §8.6 an extension benefits only new children.
- A separate `perChildMaxAge` sweep or a new expiry reason. `sweepMaxAge` already lists every row, and `expired:lease` already names a delegation-lease expiry.
- Emitting `dev.lenny.session_expired` for a `perChildMaxAge` expiry. §14 lists only the platform clocks.
- Narrowing the budget terminator to `running`, `input_required`, and `awaiting_client_action` (D11).
- Narrowing `sweepMaxAge` to the states where active age counts (D12).
- Routing `ForceTerminate` onto the administrative-cancel edges, or refusing off-table terminal writes. Either breaks §24.11 and the §12.8 drain, which applies the §24.11 transition to every non-terminal session.
- Enumerating a per-state `→ failed` and `→ expired` edge for each any-state writer. One any-state paragraph states the rule once.
- Moving F-7.2.31 to P2b or its own proposal. The sweeps fixed here write on those edges, and the fix is one paragraph.
- Per-state stamp columns, or replacing `SuspendedAt` with `state_entered_at` (D4).
- Making `UpdatedAt` advance only on state changes (D3).
- Reusing `resume_eligible_until` as the `resume_pending` anchor. The specification does not state it, and it covers one state only.
- Stating the anchor in a §4.2 data-model list. The §6.2 paragraph that defines the persisted age column carries it.
- A §8.10 sentence naming the cascade-window anchor (D5).
- A §11.3 deadline-sweeps paragraph. SPEC-4's §10.1.1 sentence states the same rules on an existing surface.
- Spec text for where sweeps run terminal hooks (D7).
- Clearing `FailureReason` on recovery to `running`. An `awaiting_client_action → expired` write never passes through `running`, so the writers stamp unconditionally instead.
- A bounded worker pool for dispatched hooks, or a sweep-error metric. The terminal pipeline budget and the shutdown claim already bound each pipeline.
- Storing a policy-derived `perChildMaxAge` default on a child that declares no slice (D10).
- F-10.1.36 (every replica lists every tenant per sweep), F-5.2.57 (`claimOrphanTimeout`), and cross-replica de-duplication of `session_expiring_soon`.

## Open decisions for human to make

None. The spec loop and the post-spec-loop open-decisions review routed no decision to the human.

## Defects in the shipped tree that this proposal does not stage

- **A child that declares no `perChildMaxAge` is unbounded by it.** `storeLeaseFromSlice` stores nil for an all-zero slice, and `ValidateChildSlice` compares the two values only when both are above zero, so a child that omits the slice under a bounded parent has no `perChildMaxAge`. §8.2 says defaults derive from `DelegationPolicy`, but the code carries no policy `perChildMaxAge` default to store (D10). Closing it needs a policy default source, which is outside the deadline and sweep scope of this proposal.
- **Duplicate `session_expiring_soon` emissions across replicas.** Every replica runs the watchdog, and the expiry warning is not de-duplicated across replicas. It is outside the validated problem and sits with F-10.1.36.
- **The §15.1 upload and stale-derive precondition rows do not list `input_required`.** Once CODE-7 persists the sub-state, a session waiting in `lenny/request_input` is refused a mid-session upload and a stale derive that the same session is admitted to while `running`. CODE-7 keeps the code on the §15.1 rows. Whether the rows should admit the sub-state is a §15.1 contract question outside the deadline and sweep scope.
- **The Open Responses status mapping disagrees with its documentation.** `mapStateToResponsesStatus` returns `queued` for `created`, `finalizing`, `ready`, `suspended`, `resume_pending`, and `awaiting_client_action`, and the status table in `docs/api/open-responses.md` maps each of them to `in_progress`. It also returns `queued` for `expired`, which the page maps to `failed`. CODE-7 adds only the `input_required` mapping.
- **The delegated child commit reuses the `/start` transition helper.** `delegated_child_materialize.go:181` calls `transitionStart` (`session_transitions.go:111`) to commit a child's `created → running`, and the helper's comment cites the §15.1 `/start` path `ready → starting → running`. CODE-6 does not touch `create_and_start.go` or `delegated_child_materialize.go`, because neither holds a `ready` row across a launch. `create_and_start.go` creates the row already `running` after the launch (`create_and_start.go:309`, `:501`), and the delegated child holds a `created` row and commits under a lock guarded on `created` (`delegated_child_materialize.go:85`, `:178-181`). The delegated child commit therefore has no `/start` ready-row race to fix, and it lands on `running` from `created`.
- **F-10.1.36 and F-5.2.57 stay open.** Both findings touch the same sweeps but are not in the validated problem.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0092 (session suspend and resume) | Implemented | 0105 keeps `SuspendedAt` and `sweepSuspended` as 0092 left them and moves only the `suspendedSince` zero-value fallback to `StateEnteredAt` (CODE-2). REC-1 closes the halves of F-6.2.28, F-10.1.34, and F-11.3.38 that 0092's resolution notes handed to P3. | Nothing. 0092 is not edited. |
| 0100 (terminal pipeline, P2a) | Implemented (2026-10-27) | CODE-4 moves the terminal hook off the sweep pass and relies on 0100's `terminalContext` bound and the terminal claim it waits on at shutdown. SPEC-2 Edit (k) amends the §7.1 **Gateway-owned terminal writes:** paragraph that 0100 SPEC-1 landed, adding the `starting → failed` write a failed `/start` makes as a third gateway-owned write. The two writes 0100 listed stay as written. | Nothing. 0100 is not edited. |
| 0101 (off-holder request forwarding) | Approved (2026-10-26) | 0101 SPEC-2 inserts a bullet directly after the §10.1.1 **Primary:** bullet. SPEC-4 inserts text inside that bullet and leaves the bullet's opening, which 0101 quotes as its anchor, unchanged. 0101 CODE-2 edits `mcptools_register.go`, where CODE-7 adds the `input_required` writes, and edits `send_delivery.go`, whose gates CODE-7 item 4 reviews and keeps without editing the file. 0101's `forwardStates` lists `input_required` for a message or an interrupt and `starting` for a terminate or a delete. CODE-7 makes the row carry `input_required`, and CODE-6's `ready → starting` write makes the row carry `starting`, so both entries match live rows after 0105 lands. All of 0101's deliverables survive, and no 0101 decision reopens. | Whichever lands second applies its §10.1.1 edit by quoted anchor and rebases `mcptools_register.go` with no change to content. Proceeding while 0101 is Approved is the human's call. 0101's files are not edited. |
| 0102 (impersonation-session revocation) | Approved (2026-10-26) | 0102 edits the `UserTokenRevoker` interface and `runFullRevokeFanOut` step 5 in `pkg/gateway/externalapi/admin/users_invalidate.go`. CODE-6 adds `starting` to `revokedPreRunning` in the same file. Neither edit touches the function the other changes, and all of 0102's deliverables survive. | Whichever lands second rebases `users_invalidate.go` with no change to content. Proceeding while 0102 is Approved is the human's call, and the overlap reopens no 0102 decision. 0102's files are not edited. |
| 0104 (session-scoped and delegation token gaps) | Approved | 0104 CODE-2 edits the creation fence in the memstore and pgstore insert path, where CODE-1 sets `StateEnteredAt` in the shared insert. 0104 SPEC-2 edits §8.2 paragraphs other than the `LeaseSlice` table row SPEC-3 changes. | Whichever lands second rebases the store `Create` paths with no change to content. 0104's files are not edited. |

## Deliverable index

- SPEC-1 — spec/06_warm-pod-model.md — the persisted `state_entered_at` instant and the idle-clock restart on every entry to `running`
- SPEC-2 — spec/07_session-lifecycle.md, spec/06_warm-pod-model.md, spec/15_external-api-surface.md — the `/start` edges through `starting`, the terminal writes from any non-terminal state, the per-state `maxSessionAge` expiry edges, the §6.2 `input_required` idle-row pointer to §7.2 (Edit (l), pending redesign under D13), and the conforming §6.2, §7.1, §7.3, and §15.1 edits
- SPEC-3 — spec/08_recursive-delegation.md — `perChildMaxAge` counted from the child session's creation
- SPEC-4 — spec/10_gateway-internals.md — every replica runs the watchdog sweeps and orphan cleanup, with the locked-row re-check and per-row continuation
- CODE-1 — migrations/, pkg/gateway/session/sessionstore/, tests/testinfra/lifecycleharness/ — the store-owned `StateEnteredAt` column and stamp
- CODE-2 — pkg/gateway/runtime/watchdog/, pkg/gateway/sessionserver/ — state-entry anchors, locked-row deadline re-checks, and unconditional expiry reasons
- CODE-3 — pkg/gateway/runtime/watchdog/watchdog.go, pkg/delegation/lease/lease.go, pkg/gateway/session/sessionstore/sessionstore.go — the `perChildMaxAge` leg of `sweepMaxAge`
- CODE-4 — pkg/gateway/runtime/watchdog/, pkg/gateway/mcpfabric/delegationtree/orphancleanup/, pkg/gateway/session/orphansession/ — sweeps continue past errors and run terminal hooks off the pass
- CODE-5 — pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go — the cascade window from the root's `TerminatedAt` and the unconditional stamp
- CODE-6 — pkg/gateway/sessionserver/, pkg/gateway/externalapi/admin/users_invalidate.go, pkg/session/state/, sdks/client/ — `/start` writes `ready → starting` before the launch, the session transition table follows SPEC-2, and the client SDKs' single-attempt `Start`
- CODE-7 — pkg/gateway/mcpfabric/mcptools/, pkg/gateway/mcpfabric/delegation/service.go, pkg/gateway/sessionserver/eval.go, pkg/gateway/checkpoint/checkpointer/freshness.go, pkg/gateway/environment/translator/open_responses.go — `lenny/request_input` persists `input_required`
- CODE-8 — cmd/lenny-gateway/, pkg/gateway/metrics/gatewaymetrics/ — the orphan-session reconciler under the gateway leader gate
- TEST-1 — pkg/, cmd/lenny-gateway/, tests/, tests/spec-map.json — tests across the reached tiers
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — finding resolutions, new defect entries, and the P3 plan entry
- DOC-1 — docs/reference/error-catalog.md, docs/client-guide/sdk-examples/ — the `STARTING_FAILED` row retried by creating a new session, and the SDK `Start` retry sentence
