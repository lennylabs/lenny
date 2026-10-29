# Deviations — 0105_fix_the-per-state-session-deadlines-and-the-watchdog-sweeps

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: The state-entry migration lands as 0195

**Status:** accepted: the owner pre-accepted the renumbering; migrations 0192 to 0194 hold unrelated changes (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** The CODE-1 targets name `migrations/0192_sessions_state_entered_at.up.sql` and `migrations/0192_sessions_state_entered_at.down.sql` as the next free number after 0191. Summary decision D13 also places the `input_wait_id` column in migration 0192.

**What landed instead.** Step S5 (CODE-1: store-owned StateEnteredAt column and stamp) added `migrations/0195_sessions_state_entered_at.up.sql` and `migrations/0195_sessions_state_entered_at.down.sql` with the content the proposal stages. The tier-2 migration test uses 194 as the prior version.

**Why.** Migrations 0192, 0193, and 0194 landed after the proposal was written and occupy those numbers. The step instruction directed 0195.

**What a later reader would otherwise get wrong.** A reader following the proposal text looks for the `state_entered_at` and `input_wait_id` columns in migration 0192 and finds an unrelated migration there. The columns are in migration 0195, and a test that pins the prior schema version uses 194.

## Proposed: TEST-1 item 12 covers only the StateEnteredAt round trip

**Status:** resolved: a later step added `Session.InputWaitID`, its pgstore read and write wiring in `Create`, `Update`, and the row scan, and `TestSessionStoreRoundTripsInputWaitID_spec_7_2` in `tests/tier2_component/stores/sessionstore_state_entry_test.go`, which checks the round trip through `Update`, `Get`, and `GetByID` (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** TEST-1 item 12 requires the pgstore test to check that an `InputWaitID` written through `Update` reads back through `Get` and `GetByID`, alongside the `StateEnteredAt` check.

**What landed instead.** Step S5 implemented only the `StateEnteredAt` half of item 12. Migration 0195 creates the `input_wait_id` column, but the tree has no `Session.InputWaitID` field and no pgstore wiring that reads or writes the column.

**Why.** The step scoped item 12 to the `StateEnteredAt` half. The `InputWaitID` field and its store wiring belong to a later step.

**What a later reader would otherwise get wrong.** A reader who sees item 12 as delivered after S5 assumes the `InputWaitID` round trip through `Update`, `Get`, and `GetByID` is tested and wired. Until the later step lands, the column exists in the schema with no Go field or store code behind it, and no test covers the `InputWaitID` round trip.

## Proposed: A watchdog listing error skips one listing rather than the tenant

**Status:** accepted: skipping only the failed listing keeps more of the pass running, as §10.1.1 **Primary:** requires (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** CODE-4 item 1 in the non-spec changes states: "A per-tenant List error skips that tenant."

**What landed instead.** Step S9 (CODE-4: sweep continuation and off-pass hook dispatch) left the watchdog listing rows separately in each sub-sweep, and per state in the pre-running sweep. A listing error there skips only that one listing, and the tenant's other sub-sweeps still run (`pkg/gateway/runtime/watchdog/watchdog.go` `sweepTenant` and `sweepPreRunning`, and `pass.go` `listError`). Orphan cleanup and the orphan-session reconciler list once per tenant, so a listing failure there skips the whole tenant as the proposal states.

**Why.** The step reported that the watchdog has no single per-tenant listing to skip. Skipping only the failed listing lets more of the pass continue, and the step cited §10.1.1, which requires that a failure to list does not stop the sweep for the other sessions.

**What a later reader would otherwise get wrong.** A reader following the proposal text expects a watchdog listing error to skip every sub-sweep for that tenant. In the watchdog, only the failed listing is skipped and the tenant's other sub-sweeps run in the same pass. The per-tenant skip holds only for orphan cleanup and the orphan-session reconciler.

## Proposed: The session_expiry counter fires synchronously before hook dispatch

**Status:** accepted: the counter is a metric increment outside the terminal pipeline, and the audit-chain order on the dispatched goroutine is unchanged (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** CODE-4 item 2 lists the hooks the dispatched goroutine runs: the awaiting notifier, where present, and then `recordCompleted`. Before the change, the order was the awaiting audit, then the `session_expiry` counter, then the terminal hook.

**What landed instead.** Step S9 kept `notifySessionExpiry`, which increments `lenny_session_expiry_total`, synchronous, and it now fires before dispatch. The awaiting notifier and `recordCompleted` run on the goroutine in that order (`watchdog.go` `expireAwaiting`).

**Why.** The step reported that the proposal names only the awaiting notifier and `recordCompleted` for the goroutine, and that the counter is a metric increment outside the terminal pipeline. The audit-chain order between the awaiting row and the `session.expired` row is unchanged.

**What a later reader would otherwise get wrong.** A reader who reconstructs the hook order from the prior code expects the counter to sit between the awaiting audit and the terminal hook on the dispatched goroutine. The counter increments on the sweep goroutine before dispatch, so `lenny_session_expiry_total` can rise before the awaiting audit row and the terminal hook have run.

## Proposed: The gateway onTick log callbacks change outside the CODE-4 targets

**Status:** accepted: the callbacks follow from `Tick` returning a joined error while still applying writes (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** CODE-4 targets only the `watchdog`, `orphancleanup`, and `orphansession` packages.

**What landed instead.** Step S9 also changed `cmd/lenny-gateway/workers.go` and `cmd/lenny-gateway/reconciler_workers.go`. The `onTick` log callbacks for the watchdog, orphan cleanup, and the orphan-session reconciler no longer return early on a non-nil error, so they still log a pass's applied counts.

**Why.** The step reported that `Tick` now returns a joined error while still applying writes. With the early return kept, the callbacks would stop logging the counts of any pass that had one failing row.

**What a later reader would otherwise get wrong.** A reader who takes the CODE-4 target list as the full set of files the step touched misses the edits to the two gateway worker files, and could read the changed `onTick` behavior there as unrelated or unreviewed.

## Proposed: Each sweeper counts dispatched hook goroutines for its tests

**Status:** accepted: the wait group is unexported and reachable only from in-package tests through `export_test.go` (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** CODE-4 does not mention test-observability hooks.

**What landed instead.** Step S9 gave each of the three sweepers an unexported `sync.WaitGroup` that counts dispatched hook goroutines (`watchdog.go` `Watchdog.dispatched`, `orphancleanup.go` `Sweeper.dispatched`, and `orphansession.go` `Reconciler.dispatched`). An `export_test.go` `WaitDispatched` in each package exposes it to that package's tests only.

**Why.** The step reported that the no-hook fallback for billing and archive holds no terminal claim, so in-package tests need a deterministic way to observe dispatched hooks. Tests in other packages wait on the terminal claims instead, through `sessionserver` `WaitTerminalWork` or a claim-tracking fake.

**What a later reader would otherwise get wrong.** A reader who works only from the proposal does not expect the `dispatched` field on each sweeper or the `WaitDispatched` test helper, and could take them for production synchronization. The wait group serves only test observation, and code outside the package cannot reach it.

## Proposed: The unstaged defects already had BUILD-GAPS entries

**Status:** accepted: the ten defects are tracked under their 2026-10-27 entries (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** REC-1 item 5 in the non-spec changes (REC-1 Records) directs the implementor to file a new BUILD-GAPS entry for each defect the summary lists under "Defects in the shipped tree that this proposal does not stage".

**What landed instead.** Step S15 (REC-1: BUILD-GAPS notes and entries and P3 plan entry) filed no new entries. All ten defects already had entries, filed on 2026-10-27 ahead of the implementation: F-5.2.58, F-7.1.32, F-7.2.45, F-8.2.32, F-9.1.12, F-11.3.45, F-11.3.46, F-15.1.61, F-15.1.62, and F-15.4.5. Each entry asked the implementor to update it rather than file a duplicate. The step confirmed each entry in place against the "Filed from proposal 0105's unstaged-defects list" sentence in `BUILD-GAPS.md`. It changed the text in F-8.2.32 and F-15.1.61 that still said 0105 had not landed, and it updated the matching 0105 block in `gateway-runtime-comms-remediation.md` §10.2.

**Why.** The step reported that filing the entries again would have created duplicate entries for the same defects.

**What a later reader would otherwise get wrong.** A reader following the proposal text expects ten BUILD-GAPS entries dated at the implementation and looks for them among the entries filed on 2026-10-28. The defects are tracked under the pre-existing 2026-10-27 entries listed above, which the step updated in place.

## Proposed: F-5.2.57 gains an owner note and loses its P3 routing

**Status:** accepted: the F-5.2.57 note and the routing change remove a stale P3 owner (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** REC-1 item 6 directs the implementor to leave F-10.1.36, F-5.2.57, and F-7.2.43 open, and to record an owner note on F-7.2.43 only.

**What landed instead.** Step S15 left F-7.2.43 unchanged, because it already carried the note (owner unassigned, 0105 does not stage it). The step also added a "**Note (proposal 0105, 2026-10-28)**" to F-5.2.57, and it changed the F-5.2.57 routing line in the plan's 0100 block from "with P3" to an unassigned owner.

**Why.** The step reported that F-5.2.57 named P3 as its owner and that 0105 does not stage it, so leaving the P3 routing in place would have left a stale owner on the record.

**What a later reader would otherwise get wrong.** A reader following the proposal text expects F-5.2.57 to carry no 0105 note and to remain routed to P3. F-5.2.57 carries a 0105 note, and the plan's 0100 block lists its owner as unassigned. F-7.2.43 received no new note from this step because its note predates the step.

## Proposed: A landed commit body names the SPEC-2 deliverable label

**Status:** accepted (history is not rewritten) (2026-10-29, orchestrator under the owner's delegation)

**What the proposal says.** The proposal's deliverable ids, such as SPEC-2, name parts of the proposal document. The implementation pipeline keeps those ids out of source, comments, tests, and commit messages, and describes each change by its behavior and its specification section instead.

**What landed instead.** Commit f1bf2f8eb ("spec(session-lifecycle): route /start through starting, list any-state terminal writes, and admit terminate from input_required") opens its body with "Applies proposal 0105 SPEC-2: the ready → starting edge for /start ...". The conformance review found no other proposal id in the added lines under `pkg/`, `cmd/`, `sdks/`, `tests/`, `migrations/`, or `docs/`.

**Why.** Committed history is not rewritten on this branch, so the commit message stays as written. No lint or gate over commit messages was added, because the proposal stages none and a new gate is scope the proposal does not contain.

**What a later reader would otherwise get wrong.** A reader of the first-parent history who meets "SPEC-2" in commit f1bf2f8eb cannot resolve it from the specification. The id refers to the summary's deliverable index entry for the §7.2 session-lifecycle edits of proposal 0105.
