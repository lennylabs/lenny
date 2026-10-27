# Non-spec changes — Per-state session deadlines and watchdog sweeps are under-specified and mis-implemented

## Design (implementation-facing)

- The session store owns `StateEnteredAt` (CODE-1). Every watchdog anchor reads it (CODE-2), and orphan cleanup reads the root's `TerminatedAt` (CODE-5). `UpdatedAt` keeps its role in change detection and in `AccrueSessionAge`.
- Every terminal sweep mutator applies the SPEC-4 locked-row rule (§10.1.1 **Primary:**) and stamps its own `FailureReason` unconditionally (CODE-2, CODE-3, CODE-5).
- Sweep passes collect per-row and per-tenant errors and continue, and the applied path hands the open terminal claim to a goroutine (CODE-4).
- `/start` (CODE-6), `lenny/request_input` (CODE-7), and the orphan-session reconciler (CODE-8) move onto the edges and the replica assignment the specification states.

## Staged code changes

### CODE-1 · Store-owned `StateEnteredAt`

Targets:
- `migrations/0192_sessions_state_entered_at.up.sql` and `migrations/0192_sessions_state_entered_at.down.sql` (the next free number after `0191_sessions_pod_released_at`).
- `pkg/gateway/session/sessionstore/sessionstore.go`: the `Session.StateEnteredAt` field and `StampStateEntry` beside `AccrueSessionAge`.
- `pkg/gateway/session/sessionstore/memstore/memstore.go`: `Create`, and `Update` beside its `AccrueSessionAge` call.
- `pkg/gateway/session/sessionstore/pgstore/pgstore.go`: the insert, the select list, the scan, `updateSQL`, and `Update` beside its `AccrueSessionAge` call.

Up migration:

```sql
ALTER TABLE sessions ADD COLUMN state_entered_at timestamptz;
UPDATE sessions SET state_entered_at = updated_at;
ALTER TABLE sessions ALTER COLUMN state_entered_at SET NOT NULL;
```

The down migration drops the column.

```go
// StampStateEntry sets row.StateEnteredAt to now when the write changes the
// session's state, and restores the stored value otherwise, so a mutator can
// neither forget nor forge the state-entry instant.
// spec: §6.2 (Pod State Machine)
func StampStateEntry(prev session.State, prevEntered time.Time, row *Session, now time.Time) {
	if row.State != prev {
		row.StateEnteredAt = now
		return
	}
	row.StateEnteredAt = prevEntered
}
```

- Both stores call `StampStateEntry` inside the locked `Update`, after the mutator returns, with the same `now` as the `UpdatedAt` stamp and `AccrueSessionAge`. `prev` and `prevEntered` come from the row as read under the lock, before the mutator ran.
- `Create` sets `StateEnteredAt` to `CreatedAt` and discards any caller value.

### CODE-2 · Watchdog anchors, locked-row deadline re-checks, and unconditional expiry reasons

Targets: `pkg/gateway/runtime/watchdog/watchdog.go` (the `Tick` pre-running loop and `elapsed`, `sweepDLQExpiry`, `sweepResumePending`, `sweepResuming`, `sweepAwaitingClientAction`, `sweepIdle`, `expireIdle`, and every doc comment in the file that names `UpdatedAt` as a deadline anchor) and `pkg/gateway/runtime/watchdog/suspended.go` (the `suspendedSince` fallback).

- Replace every `UpdatedAt` deadline anchor with `StateEnteredAt`. The `sweepIdle` and `expireIdle` anchor is `LastAgentActivityAt`, or `StateEnteredAt` when that is zero. `suspendedSince` keeps `SuspendedAt` and falls back to `StateEnteredAt` when it is zero.
- Pre-running mutator: `if r.State != st || !w.elapsed(r.StateEnteredAt, st, now) { return ErrSuperseded }`.
- `sweepAwaitingClientAction` mutator: also returns `ErrSuperseded` when `now.Sub(r.StateEnteredAt) <= deadline(*r)`.
- `expireIdle` takes the idle cap and recomputes the anchor on the locked row. It returns `ErrSuperseded` when the row is within the cap, so an activity stamp that lands after the listing keeps the row.
- `sweepResuming` keeps its `CoordinationGeneration` guard.
- Replace each `if r.FailureReason == "" { r.FailureReason = X }` in `sweepAwaitingClientAction` and `expireIdle` with `r.FailureReason = X`.

### CODE-3 · The `perChildMaxAge` leg of `sweepMaxAge`

Targets: `pkg/gateway/runtime/watchdog/watchdog.go` (`sweepMaxAge`, `maxAgeExpirable`, and the `sweepMaxAge` doc comment on over-cap paused rows), `pkg/delegation/lease/lease.go` (the `PerChildMaxAge` doc comment), and `pkg/gateway/session/sessionstore/sessionstore.go` (the `DelegationLease.PerChildMaxAge` doc comment).

- Keep `maxAgeExpirable` and the existing age test (`maxAgeExpirable(r.State) && ActiveAge(r, now) > cap`) unchanged. Do not narrow it to `SessionAgeCounts` (summary D12).
- Rewrite the `sweepMaxAge` doc comment that says over-cap `resume_pending` and `awaiting_client_action` rows are still expired so that it cites the §7.2 `resume_pending → expired` and `awaiting_client_action → expired` edges and the `readResumable` refusal in `DriveResume` as the reason.
- Add the lease leg:

  ```go
  leaseDue := r.ParentSessionID != "" && r.DelegationLease != nil &&
      r.DelegationLease.PerChildMaxAge > 0 &&
      now.Sub(r.CreatedAt) > time.Duration(r.DelegationLease.PerChildMaxAge)*time.Second
  ```

  It is evaluated for every non-terminal row, `suspended` included. The listing filter becomes `!session.IsTerminal(st) && ((maxAgeExpirable(st) && ageDue) || leaseDue)`.
- The mutator recomputes both conditions on the locked row and returns `errMaxAgeSuperseded` when neither holds. It stamps `expired:lease` (`session.FailureExpiredLease`) when only `leaseDue` holds and `expired:deadline` otherwise, unconditionally in both cases, replacing the `FailureReason == ""` guard.
- Call `notifySessionExpiry` with `max_session_age` only when `ageDue` holds.
- Rewrite the `PerChildMaxAge` doc comments in `lease.go` and `sessionstore.go` to "wall-clock seconds the session holding this slice may live, counted from its creation", with `// spec: §8.2`. Do not state that the value is always bounded by the parent's, because `ValidateChildSlice` compares only when both values are above zero.

### CODE-4 · Sweeps continue past errors and run terminal hooks off the pass

Targets:
- `pkg/gateway/runtime/watchdog/watchdog.go`: `Tick`, each sub-sweep's per-row error return, and the `recordCompleted` call sites.
- `pkg/gateway/runtime/watchdog/suspended.go`.
- `pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go`: `Tick`, `sweepTenant`, and `expireOrphan`.
- `pkg/gateway/session/orphansession/orphansession.go`: `Tick` and the expiry write that calls `OnSessionTerminal`.

1. **Continuation.** A per-tenant `List` error skips that tenant, and a per-row `Update` error skips that row. Each is logged with `tenant_id` and, for a row, `session_id`, appended to a collected error, and the pass continues. `Tick` returns `errors.Join` of the collected errors. A non-sentinel `Update` error, `ErrNotFound` included, takes this path. Follow the existing handling in `expireSuspendedIfDue`. The next pass re-evaluates the row, and the locked-row guard turns a write that did apply despite the error into the superseded sentinel on that pass.
2. **Hook dispatch.** The terminal claim still opens synchronously before `Update`. On the applied path, hand the open `end` to a goroutine that runs the awaiting notifier (where present) and then `recordCompleted`, both on `context.WithoutCancel(ctx)`, with `defer end()` inside the goroutine. On the superseded and error paths, call `end()` synchronously. Apply the same claim-then-goroutine dispatch to `OnSessionTerminal` in the orphan-session reconciler.
3. **Trap.** `expireIdle`, `expireSuspendedIfDue`, and `expireOrphan` close the claim with a function-level `defer end()`. Remove that defer in each, or the claim closes while the goroutine still runs the pipeline.
4. Keep the `w.clock()` read inside each mutator's `MarkTerminated` call.
5. Update the doc comments that describe the old behavior: `expireOrphan`'s "a store error ends the tenant's pass", the `Tick` and `sweepTenant` docs, and the `MarkTerminated` in-mutator clock comments, which now explain the read by the dispatched hooks of earlier rows.

### CODE-5 · Orphan cleanup anchors on the root's `TerminatedAt` and stamps unconditionally

Target: `pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go` (the cascade-window check in `sweepTenant` and the `expireOrphan` mutator).

- The window check becomes `if root.TerminatedAt.IsZero() || now.Sub(root.TerminatedAt) <= s.cascadeTimeout { continue }`.
- The mutator sets `r.FailureReason = string(session.FailureExpiredDeadline)` unconditionally. It keeps its non-terminal guard, which is the SPEC-4 orphan cleanup rule.

### CODE-6 · `/start` writes `ready → starting` before the launch

Targets: `pkg/gateway/sessionserver/start.go` (`handleStart`, `commitStart`, and the doc comment saying the row stays `ready`), `pkg/gateway/sessionserver/session_transitions.go` (`transitionStart` and its short-circuit comment), and `pkg/gateway/sessionserver/usage.go` (`isPreRunningClaimState`).

1. `handleStart` writes `ready → starting` under the row lock before `launchOnPod`. A row not in `ready` gets the existing `409 INVALID_STATE_TRANSITION` precondition error, so a second concurrent `/start` is refused at this write.
2. `commitStart` requires the locked row to be `starting` and writes `starting → running`.
3. Every error return of `handleStart` after the item 1 write first writes `starting → failed` on the locked row when it is still `starting`, stamping the code of the error it returns as `FailureReason`, and then returns that error as today. Cite §7.2 **Session state machine:** (`starting → failed`).
4. Add `starting` to `isPreRunningClaimState`, so a `starting` row failed on a replica that holds no binding for it takes the by-name reclaim.
5. `transitionStart` cites §15.1 `ready → starting → running` and no longer short-circuits.

`create_and_start.go` and `delegated_child_materialize.go` are out of scope: neither holds a `ready` row across a launch.

### CODE-7 · `lenny/request_input` persists `input_required`

Targets: `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (the `lenny/request_input` handler around the `InputWaits` registration and its wait `select`), `pkg/gateway/coordination/coordination/coordination.go` (the Sweeper adoption path), the gateway wiring that builds the Sweeper, and the gates listed in item 4.

1. **Entry.** After the wait is registered, write `running → input_required`. The mutator returns `ErrSuperseded` unless the locked row is `running`; a row already `input_required` (a second concurrent wait) is left as it is. After an applied write, publish `status_change` with `{"state":"input_required"}` through `deps.Events.Publish`, the path the handler already uses for `elicitation_request`. The payload matches `emitStatusChange` in `pkg/gateway/sessionserver/lifecycle.go`. Cite §7.2.
2. **Exit.** When the wait ends by reply, timeout, or cancel, and after it is unregistered, write `input_required → running` with `LastAgentActivityAt = now`. The mutator returns `ErrSuperseded` unless the locked row is `input_required` and `InputWaits.PendingForSession` is empty for the session. After an applied write, publish `status_change` with `{"state":"running"}`. No `status_change` is published when a write returns `ErrSuperseded`.
3. **Crashed waiter.** When the Sweeper adopts or re-adopts the coordination lease of an `input_required` row, it writes `input_required → running` with `LastAgentActivityAt = now`, guarded on the locked row still being `input_required` and on the adopter having no pending wait for the session. The Sweeper reads pending waits through a small consumer interface, wired to the replica's `inputwait.Registry`. Cite §7.2 and §10.1.2. Inbox loss on that crash stays as §7.2 documents it.
4. **Running-only gates.** Each gate below compares the state with `running` only. Persisting `input_required` changes what each gate sees for a waiting session, so each takes the decision in the table, and a test pins each decision (TEST-1 item 21).

   | Gate | Decision |
   |:--|:--|
   | Delegation admission (`ErrParentNotRunning`) in `pkg/gateway/mcpfabric/delegation/service.go` | Admit `input_required`, under the §7.2 sub-state rule. |
   | `pkg/gateway/sessionserver/eval.go` eligibility | Admit `input_required`, under the §7.2 sub-state rule. |
   | The post-send-error re-read in `pkg/gateway/mcpfabric/mcptools/send_delivery.go` and in `pkg/gateway/sessionserver/messages_delivery.go` | Keep. An `input_required` session is re-routed, and the router sends it to path 3. |
   | `deliverChildHeld` in `send_delivery.go` | Keep. It runs right after a child's task input, and a child in `input_required` keeps its held messages under §7.2 path 6. |
   | The mid-session upload and stale-derive rows of the precondition table in `pkg/api/v1/session/session.go` | Keep. The §15.1 precondition rows for `POST /v1/sessions/{id}/upload` and `POST /v1/sessions/{id}/derive` do not list `input_required`, and the code follows them. |

### CODE-8 · The orphan-session reconciler under the gateway leader gate

Targets: `cmd/lenny-gateway/reconciler_workers.go`, `cmd/lenny-gateway/leader_sweeps.go` (`startLeaderElectedSweeps`), the gateway wiring struct, and the gateway metrics emitter beside `SetAgentPodStateMirrorLag`.

1. Build the orphan-session reconciler in `reconciler_workers.go` as today, and keep its `Terminal: w.sessionSrv,` there. `terminal_seams_wiring_test.go` counts exactly two in that file.
2. Store the instance on the wiring struct (`w.orphanSessionReconciler`) in place of `go orphanSessionReconciler.Run(...)`.
3. In `startLeaderElectedSweeps`, call `w.addOrphanSessionReconciler(func(name string, run func(context.Context)) { leaderGate.Add(name, run) })` beside `addStaleSlotReaper` and before `go leaderGate.Run`. It registers `"orphan-session-reconciler"` only when the instance is non-nil, which is when the Postgres mirror is wired.
4. When the leader context ends, the registered run clears every per-pool `lenny_agent_pod_state_mirror_lag_seconds` series this replica published, through a new `ClearAgentPodStateMirrorLag` emitter beside `SetAgentPodStateMirrorLag`. Without the clear, a former leader keeps exporting its last value into the `max by (pool)` `PodStateMirrorStale` alert. The erasure sampler's clear on lease loss in `leader_sweeps.go` is the precedent.
5. Rewrite the `reconciler_workers.go` comment that calls the reconciler idempotent across replicas, citing §10.1.4 and §12.5.

## Staged schema, chart, and migration changes

The `state_entered_at` migration is part of CODE-1, because the column, its scan, and the store stamp have to land in one commit. No schema or chart change is staged.

## Staged docs changes

### REC-1 · Records

Targets: `BUILD-GAPS.md`; `gateway-runtime-comms-remediation.md` §10, the P3 entry (Scope, Members, Dependencies, and Overlapping plan steps) and the line that moves the reconciler's leader gating to P3.

1. Add resolution notes to F-6.2.28 (the `resume_pending` and `awaiting_client_action` remainder, resolved without a code change: 0092's `ActiveAge` plus the §7.2 `→ expired` edges and the `DriveResume` refusal), F-7.2.31, F-8.3.1, F-10.1.34 (the reconciler leader-gating remainder), and F-11.3.37 through F-11.3.43.
2. The F-8.3.1 note records that its "stored nowhere" premise was already false: `storeLeaseFromSlice` in `pkg/gateway/mcpfabric/delegation/service.go` persists the granted slice on the child row under F-8.2.2. The closing work is the enforcement in `sweepMaxAge` and the §8.2 text.
3. The F-11.3.39 note records that CODE-7 emits `status_change(input_required)` and `status_change(running)` as §7.2 requires, which discharges the deferral recorded in the F-7.1.11 and F-7.2.1 resolution notes.
4. Rewrite the P3 plan entry: the watchdog and orphan cleanup run on every replica under the locked-row re-check, and only the §10.1.4 orphan-session reconciler is leader-gated. The specification gains `state_entered_at`, the §7.2 `/start` edges through `starting`, the §7.2 any-state terminal-write paragraph, the per-state `maxSessionAge` expiry edges, and the §8.2 `perChildMaxAge` binding, with no new storage field. Remove the R20 overlap. Record P3's position.
5. File new entries for the defects the summary's **Defects in the shipped tree that this proposal does not stage** lists: the unbounded child with no `perChildMaxAge`, duplicate `session_expiring_soon` emissions across replicas, and the §15.1 upload and stale-derive rows that omit `input_required`.
6. Leave F-10.1.36 and F-5.2.57 open.

### DOC-1 · Error catalog page

Target: `docs/reference/error-catalog.md`, the `STARTING_FAILED` row of the TRANSIENT errors table. Replace "Retryable for the same reason as `SESSION_CREATION_FAILED`: a retry rebinds a fresh pod. | Retry after the `Retry-After` window." with "The session is `failed`, so a retry creates a new session, which binds a fresh pod. | Create a new session after the `Retry-After` window." The row stays one physical line.

## Testing

### TEST-1 · Tests across the reached tiers

Every test carries a `// spec:` annotation, and every test at tier 2 and above carries a `// diagnosis:` comment. Add each new test to `tests/spec-map.json` under the section its `// spec:` names (§6.2, §7.2, §8.2, §10.1.1, §10.1.4, §15.1).

**Tier 1**
1. Store state-entry table (memstore, `pkg/gateway/session/sessionstore/memstore`): create sets `StateEnteredAt == CreatedAt`; a same-state write leaves it unchanged; a state-changing write sets it to the store's `now`; a mutator-written value is discarded on both kinds of write. `// spec: §6.2`.
2. Watchdog anchors (`pkg/gateway/runtime/watchdog/state_entry_anchor_test.go`): for each pre-running state, `resume_pending`, `resuming`, `awaiting_client_action`, the DLQ TTL, and the idle fallback, a row with a recent `UpdatedAt` but an old `StateEnteredAt` is expired, and a row with an old `UpdatedAt` but a recent `StateEnteredAt` is not. The second half is the discriminating assertion. `// spec: §6.2`.
3. Locked-row re-checks: the row leaves and re-enters the state between listing and `Update` (pre-running, awaiting), and an activity stamp lands between listing and `Update` (idle). Each write returns `ErrSuperseded` and runs no hook. `// spec: §10.1.1`.
4. Unconditional reasons (`terminal_stamp_test.go`): a row carrying a funnel reason from `resume_pending` or `awaiting_client_action` is expired by the awaiting, idle, max-age, and orphan writers with each writer's own reason. `// spec: §6.2, §7.2`.
5. `sweepMaxAge` (`active_age_test.go`, `per_child_max_age_test.go`): an over-cap `resume_pending` or `awaiting_client_action` row is expired with `expired:deadline`, and an under-cap paused row is not. A child past `perChildMaxAge` from `CreatedAt` is expired with `expired:lease` in `running`, `suspended`, `resume_pending`, and `awaiting_client_action`; a child one second short is not; a root with a `DelegationLease` and a child with a zero value are not; a row past both bounds gets `expired:deadline`; `notifySessionExpiry` is called only for the age leg. `// spec: §8.2, §6.2, §7.2`.
6. Orphan cleanup (`orphancleanup_test.go`): a root whose `UpdatedAt` is rewritten after termination does not delay the cascade window; a root with zero `TerminatedAt` is skipped; the boundary at exactly `cascadeTimeout` does not expire. `// spec: §8.10`.
7. `lenny/request_input` (`pkg/gateway/mcpfabric/mcptools`): the wait writes `input_required` and publishes `status_change(input_required)`; reply, timeout, and cancel each restore `running`, stamp `LastAgentActivityAt`, and publish `status_change(running)`; a write returning `ErrSuperseded` publishes nothing; a later idle sweep does not list the waiting row. `// spec: §7.2, §6.2`.
8. Crashed waiter (`pkg/gateway/coordination/coordination`): an adopted `input_required` row with no pending wait returns to `running` and a later idle sweep evaluates it; an adopted row with a pending wait on the adopter stays `input_required`. `// spec: §7.2, §10.1.2`.
9. `/start` (`pkg/gateway/sessionserver/start_starting_state_test.go`): `/start` writes `starting` before the launch; a failure before the bind (a `PoolWarmingError`), a launch failure, and a `registerBinding` failure each leave the row `failed` with the code of the returned error as `FailureReason`; a `/start` on a `starting` row returns 409 `INVALID_STATE_TRANSITION`. `// spec: §7.2, §15.1`.
10. `isPreRunningClaimState` (usage reclaim test): a `starting` row failed on a replica without the binding takes the by-name reclaim. `// spec: §7.2`.

**Tier 2**
11. `tests/tier2_component/migrations/prod_columns_test.go`: `state_entered_at` exists, is `NOT NULL`, and is backfilled from `updated_at`; the down migration drops it. `// spec: §6.2`.
12. `tests/tier2_component/stores/`: test 1's state-entry table against pgstore. `// spec: §6.2`.

**Tier 4**
13. A `/start` whose launch outlasts the remaining ready window commits `running`; a failed launch leaves the row `failed` and the pod reclaimed. `// spec: §6.2, §15.1`.

**Tier 7a**
14. Two watchdogs and orphan cleanup under randomized interleavings over one session set produce one terminal write and one hook per session. `// spec: §10.1.1`.
15. Concurrent `/start` calls: one reaches `running`, the rest get 409. `// spec: §15.1`.
16. Concurrent `request_input` waits on one session: the row returns to `running` only after the last wait ends. `// spec: §7.2`.

**Tier 8**
17. List and `Update` fault injection: one tenant's `List` error and one row's `Update` error leave every other tenant and row processed in the same pass, `Tick` returns the joined error, and the failed row is expired on the next pass. Cover the watchdog, orphan cleanup, and the orphan-session reconciler. `// spec: §10.1.1`.
18. A blocking hook holds the terminal claim open (the shutdown wait observes it) and does not delay the next row, in the watchdog and in the orphan-session reconciler. `// spec: §10.1.1, §7.1`.
19. The starting watchdog firing on a replica without the binding reclaims the pod by name. `// spec: §6.2, §7.2`.

**Wiring and gate tests**
20. Leader wiring (`cmd/lenny-gateway`), modeled on `slot_reaper_test.go`: `addOrphanSessionReconciler` registers `"orphan-session-reconciler"` through the add-func hook; `leader_sweeps.go` contains the `leaderGate.Add` registration before `leaderGate.Run`; `reconciler_workers.go` no longer contains `go orphanSessionReconciler.Run`; losing the lease clears the published lag series. `// spec: §10.1.4, §12.5`.
21. One test per CODE-7 item 4 gate decision: delegation admission and eval eligibility accept an `input_required` session, a send error on an `input_required` session re-routes to path 3, and upload and stale derive refuse it. `// spec: §7.2, §15.1`.
22. One test per existing any-state writer, such as `ForceTerminate` from `resume_pending` and from `awaiting_client_action`, and the budget terminator from `resuming`. `// spec: §7.2 (Terminal writes from any non-terminal state)`.

## Edge cases and accepted failure modes

- A row whose `Update` fails with an ambiguous error and did apply is superseded on the next pass, and its hook runs once, on the pass that saw the nil `Update` (CODE-4).
- Every replica still lists every tenant for every sweep. F-10.1.36 covers that cost.
- A child that declares no `perChildMaxAge` is not bounded by it (summary D10).

## Files touched on application (non-spec)

- migrations/0192_sessions_state_entered_at.up.sql, migrations/0192_sessions_state_entered_at.down.sql
- pkg/gateway/session/sessionstore/sessionstore.go, memstore/memstore.go, pgstore/pgstore.go
- pkg/gateway/runtime/watchdog/watchdog.go, suspended.go
- pkg/delegation/lease/lease.go
- pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go
- pkg/gateway/session/orphansession/orphansession.go
- pkg/gateway/sessionserver/start.go, session_transitions.go, usage.go, eval.go
- pkg/gateway/mcpfabric/mcptools/mcptools_register.go
- pkg/gateway/mcpfabric/delegation/service.go
- pkg/gateway/coordination/coordination/coordination.go
- pkg/gateway/metrics/gatewaymetrics/ (the `ClearAgentPodStateMirrorLag` emitter)
- cmd/lenny-gateway/reconciler_workers.go, leader_sweeps.go, and the wiring that builds the coordination Sweeper
- The test files TEST-1 names, and tests/spec-map.json
- BUILD-GAPS.md, gateway-runtime-comms-remediation.md
- docs/reference/error-catalog.md
