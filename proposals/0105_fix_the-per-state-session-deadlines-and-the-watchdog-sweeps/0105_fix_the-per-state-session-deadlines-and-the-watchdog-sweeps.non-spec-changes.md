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
- `pkg/gateway/session/sessionstore/memstore/memstore.go`: `createLocked`, and `Update` beside its `AccrueSessionAge` call.
- `pkg/gateway/session/sessionstore/pgstore/pgstore.go`: `create` (the insert), the select list, the scan, `updateSQL`, and `Update` beside its `AccrueSessionAge` call.
- `tests/testinfra/lifecycleharness/steppedstore.go`: `steppedStamp` carries the stepped `StateEnteredAt`; `Create` records `CreatedAt` as it, `Update` sets it with `StampStateEntry` from the translated `prev` and the stepped `at` beside its `AccrueSessionAge` call, and `translate` replaces the stored value with it.

Up migration:

```sql
ALTER TABLE sessions ADD COLUMN state_entered_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE sessions ADD COLUMN input_wait_id text;

CREATE FUNCTION sessions_stamp_state_entry() RETURNS trigger AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM OLD.state
       AND NEW.state_entered_at IS NOT DISTINCT FROM OLD.state_entered_at THEN
        NEW.state_entered_at := NEW.updated_at;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER sessions_stamp_state_entry
    BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION sessions_stamp_state_entry();
```

The trigger stamps a state change written by a binary that does not name the column, which is an old replica during the rolling window; the store's own state-changing write always carries a newer value, so the trigger leaves it as written. A row that exists when the migration runs takes the migration instant, so its per-state window restarts once, as migration 0182 restarts the active age. The down migration drops the trigger, its function, the `state_entered_at` column, and the `input_wait_id` column, in that order.

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
- The shared insert (`createLocked`, `create`) sets `StateEnteredAt` to `CreatedAt` after it defaults `CreatedAt`, and discards any caller value.

### CODE-2 · Watchdog anchors, locked-row deadline re-checks, and unconditional expiry reasons

Targets: `pkg/gateway/runtime/watchdog/watchdog.go` (the `Tick` pre-running loop and `elapsed`, `sweepDLQExpiry`, `sweepResumePending`, `sweepResuming`, `sweepAwaitingClientAction`, `sweepIdle`, `expireIdle`, and every doc comment in the file that names `UpdatedAt` as a deadline anchor), `pkg/gateway/runtime/watchdog/suspended.go` (the `suspendedSince` fallback), and `pkg/gateway/sessionserver/resume_driver.go` (`windowLeft` and its doc comment).

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

1. **Continuation.** A per-tenant `List` error skips that tenant, and a per-row `Update` error skips that row. Each is logged with `tenant_id` and, for a row, `session_id`, appended to a collected error, and the pass continues. `Tick` returns `errors.Join` of the collected errors. `ErrNotFound` is superseded, as in `expireSuspendedIfDue`; any other non-sentinel `Update` error takes this path. The next pass re-evaluates the row, and the locked-row guard turns a write that did apply despite the error into the superseded sentinel on that pass.
2. **Hook dispatch.** The terminal claim still opens synchronously before `Update`. On the applied path, hand the open `end` to a goroutine that runs the row's terminal hooks on `context.WithoutCancel(ctx)`, with `defer end()` inside the goroutine: the watchdog's awaiting notifier (where present) and then `recordCompleted`; orphan cleanup's `OnSessionTerminal`, or `archiveOrphan` when no terminal hook is wired; and the orphan-session reconciler's `OnSessionTerminal`. On the superseded and error paths, call `end()` synchronously.
3. **Trap.** `expireIdle`, `expireSuspendedIfDue`, `expireOrphan`, and the orphan-session reconciler's `failOrphan` close the claim with a function-level `defer end()`. Remove that defer in each, or the claim closes while the goroutine still runs the pipeline.
4. Keep the `w.clock()` read inside each mutator's `MarkTerminated` call.
5. Update the doc comments that describe the old behavior: `expireOrphan`'s "a store error ends the tenant's pass", the `Tick` and `sweepTenant` docs, and the `MarkTerminated` in-mutator clock comments, which now explain the read by the dispatched hooks of earlier rows.

### CODE-5 · Orphan cleanup anchors on the root's `TerminatedAt` and stamps unconditionally

Target: `pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go` (the cascade-window check in `sweepTenant` and the `expireOrphan` mutator).

- The window check becomes `if root.TerminatedAt.IsZero() || now.Sub(root.TerminatedAt) <= s.cascadeTimeout { continue }`.
- The mutator sets `r.FailureReason = string(session.FailureExpiredDeadline)` unconditionally. It keeps its non-terminal guard, which is the SPEC-4 orphan cleanup rule.

### CODE-6 · `/start` writes `ready → starting` before the launch, and the session transition table follows SPEC-2

Targets: `pkg/gateway/sessionserver/start.go` (`handleStart`, `commitStart`, and the doc comment saying the row stays `ready`), `pkg/gateway/sessionserver/coordination_lease.go` (the `acquireCoordinationLease` doc comment's list of direct callers), `pkg/gateway/sessionserver/session_terminal.go` (`failStarting`), `pkg/gateway/sessionserver/session_transitions.go` (`transitionStart` and its short-circuit comment), `pkg/gateway/sessionserver/usage.go` (`isPreRunningClaimState`), `pkg/gateway/externalapi/admin/users_invalidate.go` (`revokedPreRunning`), `pkg/session/state/state.go` (`ValidTransitions`), `sdks/client/go/lenny/client.go` (`Start`, `transition`, `do`), `sdks/client/python/lenny/client.py` (`Client.start`, `_transition`, `_do`), `sdks/client/typescript/src/client.ts` (`start`, `transition`, `do`), and the test files items 3, 4, and 6 name.

1. **`/start` exits.** `handleStart` runs these steps in order: (a) the entry read, the §15.1 precondition, and the stored-plan parse, as today; (b) the `ready → starting` write, whose mutator re-runs `session.Validate(EndpointStart)` on the locked row, followed on an applied write by `emitStatusChange` with `starting`; (c) `acquireCoordinationLease`, when the row read at entry names a pod (`PodAssignment` is non-empty), so a row on a service-mode pool or on a gateway with no binder acquires no lease, as the `bound != nil` acquire in `create_and_start.go` does; (d) `launchOnPod`, `stampBindingGeneration`, and `registerBinding`, which self-renews the lease, with `rollbackBinding` on a `registerBinding` error as today; (e) the `SetupOutput` persist, which logs a store error and continues, as `applyFinalizePrepareResult` does at `/finalize`; and (f) `commitStart` (item 2). The write precedes the acquire so that the locked row alone serializes concurrent starts, which gives a losing `/start` the same answer on every replica, and so that no `ready` row holds a lease (§10.1.1 **Primary:**).

   - R1. An exit before the step (b) write applies changes no row and acquires no lease. A missing row (404 `RESOURCE_NOT_FOUND`), an entry-read error, and a stored-plan parse error (500 `INTERNAL_ERROR`) answer as today.
   - R2. On every error return after the step (b) write applies, `handleStart` calls `failStarting` (item 3) and then `releaseCoordinationLease` on `context.WithoutCancel(ctx)`, whatever `failStarting` returned. When `failStarting` returns its `startingPrecondition` refusal, `handleStart` answers that refusal through `writePreconditionError`. Otherwise it answers the exit's own error as today.

   | Row | Exit | Row afterwards | Response | Coordination lease | `status_change` | Terminal writer |
   |:--|:--|:--|:--|:--|:--|:--|
   | P1 | The entry read finds the row in a state other than `ready` | unchanged | 409 `INVALID_STATE_TRANSITION` with the entry row's state | none acquired | none | none |
   | P2 | The step (b) write finds the locked row in a state other than `ready`, because a concurrent `/start` on this or another replica committed its write first | unchanged | 409 `INVALID_STATE_TRANSITION` with the locked row's state | none acquired | none | none |
   | P3 | The step (b) write returns any other error | `ready`, or `starting` when the store committed the write before the error | 500 `INTERNAL_ERROR` | none acquired | none | none; the `starting` watchdog fails a committed `starting` row with `STARTING_TIMEOUT` |
   | F1 | Step (c) returns `leasestore.ErrHeld` from a foreign holder or another lease-store error | `failed` | 503 `STARTING_FAILED` | released (a no-op against a foreign holder) | `starting`, then `failed` from the `failStarting` pipeline | `failStarting`; `afterFailed` reclaims the prepared pod by name (item 4) |
   | F2 | `launchOnPod` fails before the binder runs: `ResolvePool`, `PoolWarmingError`, or a concurrent-workspace pool's credential resolution or delivery-isolation check | `failed` | the `writePodClaimError` mapping, with `STARTING_FAILED` as the fallback | released | `starting`, then `failed` from the `failStarting` pipeline | `failStarting`; `afterFailed` reclaims the still-claimed pod by name (item 4) |
   | F3 | `launchOnPod` fails inside the binder: the launch RPC, the slot bind, or setup | `failed` | the `writePodClaimError` mapping, with `STARTING_FAILED` as the fallback | released | `starting`, then `failed` from the `failStarting` pipeline | `failStarting`; the binder already released the pod, so the by-name reclaim finds nothing |
   | F4 | `registerBinding` returns an error | `failed` | 503 `STARTING_FAILED` | released | `starting`, then `failed` from the `failStarting` pipeline | `failStarting`, after `rollbackBinding` releases the unpublished bind |
   | F5 | `commitStart` returns an error other than its `startingPrecondition` refusal (the delivery lock or the store) | `failed`, or `running` when the store committed the `starting → running` write before the error | 500 `INTERNAL_ERROR`, or, on a committed write, 409 `INVALID_STATE_TRANSITION` with `running` (R2, from `failStarting`'s refusal) | released, including on a committed write, where the binding stays published | `starting`, then `failed` from the `failStarting` pipeline; on a committed write `starting` only, and the held messages stay undelivered, because `commitStart` returned before `emitStatusChange` and `deliverBuffered` | `failStarting`; `afterFailed` releases the published binding through the executor path. On a committed write none, because `failStarting` refuses the `running` row |
   | X1 | Another writer makes the row terminal after the step (b) write (the `starting` watchdog with `STARTING_TIMEOUT`, terminate, `DELETE`, force-terminate, full_revoke, or a §7.2 **Terminal writes from any non-terminal state.** writer), so `failStarting` or `commitStart` finds the locked row terminal | the other writer's terminal state and reason | 409 `INVALID_STATE_TRANSITION` with the locked row's state | released | `starting`; the other writer's pipeline publishes its terminal state | the other writer; a binding published after its write is released by the terminal binding release that the `commitStart` doc comment names |
   | X2 | `failStarting`'s own write returns a store error | `starting` until the `starting` watchdog writes `failed` with `STARTING_TIMEOUT` | the response of the F row that called `failStarting` | released | `starting`; `failed` from the watchdog's pipeline | the `starting` watchdog |
   | S1 | Success | `running` | 200 with the session | as step (c) leaves it, and renewed by the Sweeper | `starting`, then `running` | none |
2. `commitStart` applies `startingPrecondition` (item 3) to the locked row in place of `session.Validate(EndpointStart)` and writes `starting → running`. Its refusal is handled by item 1 R2.
3. `failStarting(ctx, tenantID, id)` is a new hook-path terminal writer in `session_terminal.go`, modeled on `failFinalizing`. It runs on `context.WithoutCancel(ctx)` and opens `BeginTerminal` before `writeTerminal`. Its guard applies `startingPrecondition` and, on a `starting` row, sets `FailureReason` to `STARTING_FAILED`. The reason is fixed because item 1 R2 chooses the response after the write. It sets no `FailureClass`, as `failFinalizing` sets none. On an applied write it runs `afterFailed`. `startingPrecondition`, modeled on `finalizingPrecondition`, admits a locked row in `starting` and otherwise returns a `session.PreconditionError` for `EndpointStart` that carries the locked row's state. Add `failStarting` to `writeTerminalCallers` in `terminal_writer_inventory_test.go`, and name it in the `afterFailed` and `terminalWriterInventory` doc comments that list `writeTerminal`'s callers. Cite §7.2 **Session state machine:** (`starting → failed`) and §7.1 **Gateway-owned terminal writes:**.
4. Add `starting` to `isPreRunningClaimState`, so a `starting` row failed on a replica that holds no binding for it takes the by-name reclaim. Add it also to `revokedPreRunning` in `users_invalidate.go`, so a full_revoke of an unbound `starting` session takes the §11.4 step 4 reclaim. `startHeldBeforeCommit` in `tests/tier7a_load_local/full_revoke_sweep_terminal_race_test.go` expects the held session to be `starting` in place of `ready`.
5. `transitionStart` cites §15.1 `ready → starting → running` and no longer short-circuits.
6. Bring `ValidTransitions` in `pkg/session/state/state.go`, and `sessionStateMachineEdges` in `state_test.go`, to the §7.2 **Session state machine:** block as SPEC-2 Edits (a), (e), and (l) leave it. `ValidTransitions` also lists the `→ failed` and `→ expired` edges that §7.2 **Terminal writes from any non-terminal state.** (SPEC-2 Edit (c)) adds from every non-terminal state, and `sessionStateMachineEdges` mirrors the block alone.
7. In each client SDK, `Start` makes a single attempt through a private parameter of the request loop that no public request option exposes, and retries neither an error response nor a transport failure, because a repeated `/start` after an exit that follows the `ready → starting` write is refused, and neither a 500 nor a transport failure tells the SDK which side of that write the exit fell on (item 1, §15.6); its doc comment says so.

`create_and_start.go` and `delegated_child_materialize.go` are out of scope: neither holds a `ready` row across a launch.

### CODE-7 · `lenny/request_input` persists `input_required`

Targets:
- `pkg/gateway/session/sessionstore/sessionstore.go`: the `Session.InputWaitID` field. `pgstore/pgstore.go`: the insert, the select list, the scan, and `updateSQL`. The column is in CODE-1's migration 0192.
- `pkg/gateway/session/inputwait/inputwait.go`: `RegisterPersisted(sessionID, requestID, waitID, parts)`, which registers as `Register` does, records `waitID` on the entry, and marks the entry persisted; the `WaitID` and `Persisted` fields on `PendingRequest`, filled by `PendingDetailsForSession`; and `Close` with `ErrClosed`.
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: the `lenny/request_input` handler around the `InputWaits` registration and its wait `select`.
- `pkg/gateway/gatewaycontrol/inputwaitrecovery/` (new): the replica's re-arm generation; the connection record and `NewContext`, which attaches to a context a fresh, not-recovered record referencing a replica's generation (its recovered generation starts below the replica's); the `stats.Handler` whose `TagConn` calls `NewContext` on each connection's context; and the unary interceptor that runs R1.
- `cmd/lenny-gateway/gwcontrol_server.go`: install the stats handler beside the otelgrpc one, and the interceptor after `RequireSATokenInterceptor`. `newGatewayControlServer` also takes the session store, `InputWaits`, and the session event bus, which its call site in `controlserver.go` passes (`w.sessions`, `w.inputWaits`, and `w.eventBus`). `cmd/lenny-gateway/shutdown.go`: the `gateway-control` step.
- The gates listed in item 4.

1. **Wait identity.** `Session.InputWaitID` (column `input_wait_id`, `text`, NULL read as empty) names the wait that the row's `input_required` state stands for. The handler draws the wait ID from its ID source (`idFn`) when it calls `RegisterPersisted`, so the ID is unique across replicas and connections; the caller-supplied `requestId` is not used, because a runtime can reuse it on its next connection (row N2). Only W1, W2, and R1 write the field. Every rule that reads it also requires the locked row to be `input_required`, so a value left on a row in another state is never read and no writer clears it.
2. **Rules.**
   - **W0 · Scope.** W1, W2, and W4 apply only to a call whose context carries a connection record (`inputwaitrecovery.FromContext`). Every call the platform tool bridge dispatches carries one, because the record is attached to the GatewayControl connection the call arrives on. A call served on the gateway-edge `/mcp` endpoint registers with `Register`, which leaves its wait unpersisted, and writes nothing to the row.
   - **W1 · Entry write.** After `InputWaits.RegisterPersisted` succeeds and before `elicitation_request` is published, the handler updates the row on the request context. On a locked `running` row the mutator sets `State` to `input_required` and `InputWaitID` to the call's wait ID. On a locked `input_required` row it sets `InputWaitID` to the call's wait ID and leaves `State`. On any other state it returns `sessionstore.ErrSuperseded`, and the wait proceeds. When W1 fails with another error, the handler calls `InputWaits.Cancel`, takes W2, and returns the W1 error. Cite §7.2 **Session state machine:** (`running → input_required`).
   - **W2 · Exit write.** Once the call's wait has left `InputWaits` (by `Resolve` on a reply, by a peer's `Cancel`, by `Close`, or by the handler's own `Cancel` on the timeout, on the request context ending, or after a W1 error), the handler updates the row once on `context.WithoutCancel(ctx)` bounded by `sessionstore.DefaultPrimaryReadTimeout`, with no retry, as `RecordPodRelease` does, and then returns. The mutator returns `ErrSuperseded` unless the locked row is `input_required` and its `InputWaitID` is the call's wait ID. When it is, and `InputWaits.PendingDetailsForSession` returns a persisted entry for the session, the mutator sets `InputWaitID` to the first such entry's `WaitID` and leaves `State`. Otherwise it sets `State` to `running` and `LastAgentActivityAt` to the write's instant (SPEC-1 Edit (b)). Cite §7.2 (`input_required → running`).
   - **W3 · Events.** A W1, W2, or R1 write that changes `State` publishes `status_change` through the session event bus's `PublishForTenant` under the locked row's `TenantID`, with the payload that `emitStatusChange` in `pkg/gateway/sessionserver/lifecycle.go` sends. A write that leaves `State` unchanged, returns `ErrSuperseded`, or fails publishes no `status_change`.
   - **W4 · Re-arm.** The replica holds one re-arm generation, which every connection record references. A W2 write that fails with an error other than `ErrSuperseded` increments it, so every connection on the replica stops being recovered, and a wait kept from a pod's earlier connection (row A2) re-arms the connection that pod now uses.
   - **R1 · Connection recovery.** The interceptor runs R1 before it dispatches a call whose request carries a session ID, on a connection whose peer presents a verified agent SPIFFE identity (`spiffe.Parse` on the verified leaf certificate) and whose record is not recovered. That is the first such call after the connection is accepted, and the next one after W4 or after an R1 run that did not complete. R1 reads the call's session with `GetByID` and continues only when that row's `PodAssignment` equals the identity's pod name, because the request's session ID is the pod's own claim; a run that stops here writes and records nothing. It lists that pod's sessions with `GetActiveSlotsByPod`, and updates each listed session under the call's session's `TenantID` (§5.2 **Tenant pinning:**). No read filters the listed sessions, so the locked-row mutator makes the only state decision. The mutator calls `InputWaits.PendingDetailsForSession` under the row lock, and returns `ErrSuperseded` unless the locked row is `input_required` and no entry returned for the session carries the row's `InputWaitID`. Otherwise the mutator applies W2's re-point-or-restore step. A run reads the replica's generation when it starts and, when every read and write of the run has returned without an error other than `ErrSuperseded`, raises the record's recovered generation to that value. The record is recovered while its recovered generation equals the replica's. R1 is idempotent, so concurrent runs on one connection need no lock. A pod process's calls share its one `LNK-GWCONTROL` connection (§28.3 **Link register**), so a wait that a pod row names and that this replica does not hold sits on a connection the adapter has left, and its result cannot reach the runtime. Cite §7.2 (`input_required → running`) and §28.3.
   - **C1 · Drain.** The `gateway-control` shutdown step calls `InputWaits.Close` before `GracefulStop`. `Close` makes every later `Register` and `RegisterPersisted` return `inputwait.ErrClosed` and cancels every pending wait, so no wait stays live on this replica once GOAWAY moves the adapter's next call to another replica. `GracefulStop` returns after each handler's W2, before the `postgres` step closes the pool. `GracefulStop` is the only GOAWAY source on this server, which sets no `MaxConnectionAge`.
3. **Outcomes.** The rules W0 to W4, R1, and C1 are normative. The table lists only the failure, crash, and concurrency cases they leave implicit, and the owner that recovers each.

   | Row | Case | Session row afterwards | Recovery owner |
   |:--|:--|:--|:--|
   | E0 | Registration refused (`ErrDuplicate`, or `ErrClosed` after C1). Nothing is registered, and the handler returns the error | Unchanged, no write | None needed |
   | E4 | W1 fails with an error other than `ErrSuperseded`, and the handler's `Cancel` removes the wait | `running`, or `input_required` naming the wait when the store committed before the error | This wait's W2 |
   | X4 | W2 fails before the store commits, and W4 re-arms every connection record on this replica | `input_required`, naming a wait no registry holds | R1 before the pod's next call on this connection, or on the replica that accepts its next connection |
   | X5 | W2 commits and the store then returns an error, and W4 re-arms every connection record on this replica | As the committed W2 leaves it | The W2 of the wait the row names when W2 re-pointed it, and none when W2 restored `running` |
   | K1 | The waiter's process exits during the wait without C1, and the wait is lost with the process | `input_required`, naming a wait no registry holds | R1 on the replica that accepts the pod process's next connection |
   | N1 | Concurrent waits on one session over one connection | While a persisted wait of the session is registered and its W1 has applied, the row is `input_required` and names a registered wait. After the last exit, the row is `running` | The W2 of the wait the row names |
   | N2 | Waits on the connection the adapter left and on its next connection, with one `InputWaits` per replica, including a later wait that reuses an earlier wait's `requestId` | Once a W1 or an R1 restore on the next connection has applied, every W2 of a wait on the left connection is superseded and leaves the row unchanged | The W2 of the wait the row names |
   | A2 | R1 finds the named wait pending here: a wait on this connection whose W1 applied during the run, or a wait of the pod's earlier connection to this replica whose handler has not yet seen the close. The wait stays registered | Unchanged | That wait's W2 |
   | A3 | An R1 read or write fails, or the call's row names a pod other than the peer's | Unchanged | R1 again before the connection's next call carrying a session ID |
   | P1 | After K1 or X4, no later call of the pod process runs R1 | `input_required`, naming a wait no registry holds | The client's terminate or another §7.2 edge out of `input_required`, or `maxSessionAge`, whose timer runs in `input_required` (§6.2) |
4. **State gates.** Each gate below admits `running` and not `input_required`. Persisting `input_required` changes what each gate sees for a waiting session, so each takes the decision in the table, and a test pins each decision (TEST-1 item 22).

   | Gate | Decision |
   |:--|:--|
   | Delegation admission (`ErrParentNotRunning`) in `pkg/gateway/mcpfabric/delegation/service.go` | Admit `input_required`, under the §7.2 sub-state rule. |
   | `pkg/gateway/sessionserver/eval.go` eligibility | Admit `input_required`, under the §7.2 sub-state rule. |
   | `isActiveForFreshness` in `pkg/gateway/checkpoint/checkpointer/freshness.go` | Admit `input_required`, under the §7.2 sub-state rule, so the §4.4 freshness gauge counts a waiting session. |
   | `mapStateToResponsesStatus` in `pkg/gateway/environment/translator/open_responses.go` | Map `input_required` to `in_progress`, as `running` maps. |
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

The `state_entered_at` migration is part of CODE-1, because the column, its scan, and the store stamp have to land in one commit. The same migration adds CODE-7's `input_wait_id` column (summary D13). No schema or chart change is staged.

## Staged docs changes

### REC-1 · Records

Targets: `BUILD-GAPS.md`; `gateway-runtime-comms-remediation.md` §10, the P3 entry (Scope, Members, Dependencies, and Overlapping plan steps) and the line that moves the reconciler's leader gating to P3.

1. Add resolution notes to F-6.2.28 (the `resume_pending` and `awaiting_client_action` remainder, resolved without a code change: 0092's `ActiveAge` plus the §7.2 `→ expired` edges and the `DriveResume` refusal), F-7.2.31, F-8.3.1, F-10.1.34 (the reconciler leader-gating remainder), and F-11.3.37 through F-11.3.43, where the F-11.3.40 note resolves the sweep-write half and names the item 5 entry for the expiry-warning half.
2. The F-8.3.1 note records that its "stored nowhere" premise was already false: `storeLeaseFromSlice` in `pkg/gateway/mcpfabric/delegation/service.go` persists the granted slice on the child row under F-8.2.2. The closing work is the enforcement in `sweepMaxAge` and the §8.2 text.
3. The F-11.3.39 note records that CODE-7 emits `status_change(input_required)` and `status_change(running)` under CODE-7 item 2's W3, which discharges the deferral recorded in the F-7.1.11 and F-7.2.1 resolution notes.
4. Rewrite the P3 plan entry: the watchdog and orphan cleanup run on every replica under the locked-row re-check, and only the §10.1.4 orphan-session reconciler is leader-gated. The specification gains `state_entered_at`, the §7.2 `/start` edges through `starting`, the §7.2 any-state terminal-write paragraph, the per-state `maxSessionAge` expiry edges, the §7.2 interrupt and terminate edges from `input_required`, and the §8.2 `perChildMaxAge` binding, with no new storage field. Record the R20 overlap as summary D13 resolves it. Record P3's position.
5. File a new entry for each defect the summary's **Defects in the shipped tree that this proposal does not stage** lists.
6. Leave F-10.1.36 and F-5.2.57 open.

### DOC-1 · Docs pages

Targets: `docs/reference/error-catalog.md`, the `STARTING_FAILED` row of the TRANSIENT errors table; the **Retries** section and the method table of `docs/client-guide/sdk-examples/go.md`, `python.md`, and `typescript.md`; `docs/api/rest.md`; `docs/api/mcp.md`; `docs/client-guide/session-lifecycle.md`; `docs/client-guide/error-handling.md`; `docs/reference/state-machines.md`; and `docs/getting-started/concepts.md`.

- Error catalog: replace "Retryable for the same reason as `SESSION_CREATION_FAILED`: a retry rebinds a fresh pod. | Retry after the `Retry-After` window." with "The session is `failed`, so a retry creates a new session, which binds a fresh pod. | Create a new session after the `Retry-After` window." The row stays one physical line.
- SDK pages: in the paragraph that begins "The client retries a retryable error on its own", directly before its closing sentence that begins "Override the policy" and introduces the code block, add one sentence using the page's method name (`Start` or `start`): the method sends its request once and is not retried, because a start can fail after the session has left `ready`, and the caller reads the session and creates a new session when it is `failed`. Link `docs/reference/state-machines.md` and give no spec section number.
- Interrupt precondition (SPEC-2 Edit (l)): state the interrupt as valid in `running` and `input_required` at the **Valid precondition states:** line of the `/interrupt` section of `docs/api/rest.md`; in the `interrupt_session` description and its `INVALID_STATE_TRANSITION` row in `docs/api/mcp.md`; in the sentence "Valid only when the session is `running`." and the `/interrupt` row of the precondition table in `docs/client-guide/session-lifecycle.md`; in the `Interrupt`/`interrupt` row of each sdk-examples method table; and in the illustrative `allowedStates` array of `docs/client-guide/error-handling.md`.
- `input_required` edges (SPEC-2 Edit (l)): add `input_required → suspended` (interrupt) and `input_required → completed` (terminate) to the session diagram and the transition table of `docs/reference/state-machines.md`, to the session diagram of `docs/getting-started/concepts.md`, and to the sentence listing the transitions out of `input_required` in `docs/client-guide/session-lifecycle.md`. Add `input_required → completed` to the canonical task diagram of `docs/reference/state-machines.md`. Insert new diagram lines and leave the existing `input_required -->` lines unedited.

## Testing

### TEST-1 · Tests across the reached tiers

Every test carries a `// spec:` annotation, and every test at tier 2 and above carries a `// diagnosis:` comment. Add each new test to `tests/spec-map.json` under the section its `// spec:` names (§6.2, §7.1, §7.2, §7.3, §8.2, §10.1.1, §10.1.4, §10.5, §15.1, §15.6, §28.3).

**Tier 1**
1. Store state-entry table (memstore, `pkg/gateway/session/sessionstore/memstore`): create, through `Create` and through `CreateIfGeneration`, sets `StateEnteredAt == CreatedAt`; a same-state write leaves it unchanged; a state-changing write sets it to the store's `now`; a mutator-written value is discarded on both kinds of write. `tests/tier1_unit/lifecycle/stepped_store_test.go`: a row read through `SteppedClockStore` after a state-changing write carries the stepped `StateEnteredAt`. `// spec: §6.2`.
2. Watchdog anchors (`pkg/gateway/runtime/watchdog/state_entry_anchor_test.go`): for each pre-running state, `resume_pending`, `resuming`, `awaiting_client_action`, the DLQ TTL, the idle fallback, and the suspended lifetime with a zero `SuspendedAt`, a row with a recent `UpdatedAt` but an old `StateEnteredAt` is expired, and a row with an old `UpdatedAt` but a recent `StateEnteredAt` is not. The second half is the discriminating assertion. The same pair for the resume driver's `windowLeft` (`pkg/gateway/sessionserver`). `// spec: §6.2`.
3. Locked-row re-checks: the row leaves and re-enters the state between listing and `Update` (pre-running, awaiting), and an activity stamp lands between listing and `Update` (idle). Each write returns `ErrSuperseded` and runs no hook. `// spec: §10.1.1`.
4. Unconditional reasons (`terminal_stamp_test.go`): a row carrying a funnel reason from `resume_pending` or `awaiting_client_action` is expired by the awaiting, idle, max-age, and orphan writers with each writer's own reason. `// spec: §6.2, §7.2`.
5. `sweepMaxAge` (`active_age_test.go`, `per_child_max_age_test.go`): an over-cap `starting`, `resume_pending`, `resuming`, or `awaiting_client_action` row is expired with `expired:deadline`, and an under-cap paused row is not. A child past `perChildMaxAge` from `CreatedAt` is expired with `expired:lease` in `running`, `suspended`, `resume_pending`, and `awaiting_client_action`; a child one second short is not; a root with a `DelegationLease` and a child with a zero value are not; a row past both bounds gets `expired:deadline`; `notifySessionExpiry` is called only for the age leg. `// spec: §8.2, §6.2, §7.2`.
6. Orphan cleanup (`orphancleanup_test.go`): a root whose `UpdatedAt` is rewritten after termination does not delay the cascade window; a root with zero `TerminatedAt` is skipped; the boundary at exactly `cascadeTimeout` does not expire. `// spec: §8.10`.
7. `lenny/request_input` row writes (`pkg/gateway/mcpfabric/mcptools`), table-driven, on a context carrying an `inputwaitrecovery` connection record unless a case states otherwise. One case per rule of CODE-7 item 2 covers its applied and superseded branches. W0: a call on a context with no connection record registers with `Register` and writes nothing to the row. W1: applied on a `running` row, the row becomes `input_required` naming the call's wait ID and a later idle sweep does not list it; applied on an `input_required` row, only `InputWaitID` changes; superseded on a row in each other state, the row is unchanged and the wait proceeds. W2: applied with no other persisted wait of the session registered, once for each way the wait ends that W2 names (reply, timeout, peer `Cancel`, request context ending, `Close`, and a W1 error), the row is `running` with `LastAgentActivityAt` set; applied while another persisted wait of the session is registered, `InputWaitID` names that wait and `State` stays `input_required`; superseded on a row that is not `input_required` and on one that names another wait, the row is unchanged. W3: every case above asserts one `status_change` under the row's `TenantID` when the write changes `State`, ahead of `elicitation_request` on entry, and none when the write leaves `State` unchanged, is superseded, or fails. One case per kept row of the CODE-7 item 3 table follows. E0: `ErrDuplicate`, and `ErrClosed` after `Close`, write nothing. E4: against a store that errors before committing and against one that commits and then errors, the wait is removed and its W2 leaves the row `running`. `// spec: §7.2, §6.2`.
8. Connection recovery (`pkg/gateway/gatewaycontrol/inputwaitrecovery`, an mTLS GatewayControl server with the stats handler and the interceptor installed, whose client certificates carry the agent SPIFFE URI of the pod the rows name), table-driven. One case per rule of CODE-7 item 2 covers its applied and superseded branches. W4: a W2 that fails with an error other than `ErrSuperseded` increments the generation, and the next call carrying a session ID on every connection of the replica runs R1; a superseded W2 increments nothing; a store hook holds one R1 run after it has processed a session whose wait is still pending while that wait's W2 fails, then releases the run, and the next call on the connection runs R1. R1: applied on a fresh connection, the first call carrying a session ID restores an `input_required` row whose named wait is not pending here to `running` with one `status_change(running)`, and re-points a row whose named wait is not pending here to a persisted wait of the session that is pending here, with no `status_change`; superseded, a row that is not `input_required` and a row that names a pending wait are unchanged; a call whose request carries no session ID and a later call on a recovered connection run no R1. C1: `Close` ends a blocked wait, whose W2 restores `running`, and a later registration returns `ErrClosed`; in `cmd/lenny-gateway`, the `gateway-control` shutdown step calls `InputWaits.Close` before `GracefulStop`, and `newGatewayControlServer` installs the stats handler and the interceptor and passes the interceptor a non-nil event bus. One case per kept row of the CODE-7 item 3 table follows, each asserting that row's session row and recovery owner. X4 fails the W2 write and sends a second call on the same connection, once with no other wait of the session registered and once with a second persisted wait of the session still registered. X5 runs against a store that commits the W2 write and then errors, in the same two variants, and sends a second call on the same connection. K1 sends the pod's first call to a second server over the same store with a fresh `InputWaits`. A2 keeps a wait registered from an earlier connection to the same server; in a concurrent variant, a store hook holds one R1 run between its `GetActiveSlotsByPod` listing and its update while a `lenny/request_input` on the same connection registers and applies W1; in a sequential variant, a new connection becomes recovered while the earlier connection's wait stays registered, that wait's W2 fails, and the new connection's next call runs R1. A3 fails R1's write and sends the next call, and separately sends a call whose row names another pod. P1 leaves the row of K1 and of X4 with no later call, asserts it stays `input_required`, and runs `sweepMaxAge` over the same store past `maxSessionAge`. A randomized test, which logs its seed, interleaves wait entries, wait exits by each way W2 names, W2 store failures, and connection accepts for two pods over one and two servers sharing one store. An invariant checker runs after every step at which no W2 or R1 is in flight and each pod's current connection has completed an R1 run since the last W4 on its replica, and it asserts that no row is `input_required` unless its `InputWaitID` names a wait pending in a registry. `// spec: §7.2, §28.3`.
9. `/start` exits (`pkg/gateway/sessionserver/start_starting_state_test.go`): one case per row of CODE-6 item 1's **`/start` exits** table, each asserting that row's columns, with these discriminating assertions. P1: the lease store holds no entry for the session. P2: the two calls are served by two `Server` instances sharing one store and one lease store, and the loser's answer is 409 with `currentState` `starting`. P3: one sub-case against a store that errors before committing, after which the lease store holds no entry, and one against a store that commits the write and then errors. F1: with a foreign holder seeded in the lease store, the launch is never called and the foreign lease survives. F2: `ReclaimClaimed` runs once for the row's `PodAssignment`. F3: the by-name reclaim after the binder's own release is a no-op. F4: `rollbackBinding` runs before the `failed` write. F5: one sub-case against a store that errors before committing, after which the registry holds no binding, and one against a store that commits the `starting → running` write and then errors. Every F row outcome that leaves the row `failed` runs on a request context cancelled before `failStarting`, asserts CODE-6 item 3's guard and context, and also asserts that `RetryCount` is unchanged (§7.3 **Retry accounting.**) and that exactly one `status_change(failed)` and one `session_complete` are published. X1: one sub-case refused at `commitStart` after a successful launch and one refused at `failStarting` after a failed launch; in both, `FailureReason` stays `STARTING_TIMEOUT` and no `status_change(running)` is published. S1: `status_change(starting)` precedes the launch, the lease names the serving replica while the launch is blocked, a service-mode row ends `running` with no lease entry, and an injected `SetupOutput` store error still answers 200 `running`. In each client SDK's existing test package, `Start` against a stub answering 503 `STARTING_FAILED`, and against a stub that drops the connection, sends exactly one request and surfaces that error; the request count is the discriminating assertion. `// spec: §7.1, §7.2, §7.3, §15.1, §15.6`.
10. `isPreRunningClaimState` (usage reclaim test): a `starting` row failed on a replica without the binding takes the by-name reclaim. `// spec: §7.2`. `TestFullRevokeRunsItsStepsInOrder_spec_11_4` gains a `starting` row and expects it in the reclaimed set (`revokedPreRunning`).

**Tier 2**
11. `tests/tier2_component/migrations/prod_columns_test.go`: `state_entered_at` exists and is `NOT NULL` with a default; an INSERT that omits it succeeds; an UPDATE that changes `state` without naming it sets it to the written `updated_at`, and a same-state UPDATE leaves it; `input_wait_id` exists and is nullable; the down migration drops the trigger and both columns. `// spec: §6.2, §10.5`.
12. `tests/tier2_component/stores/`: test 1's state-entry table against pgstore, and an `InputWaitID` written through `Update` reads back through `Get` and `GetByID`. `// spec: §6.2, §7.2`.

**Tier 4**
13. CODE-6 item 1's **`/start` exits** rows S1, with a launch that outlasts the remaining ready window; F3; and X1, with the `starting` watchdog firing while the launch, or a concurrent-workspace slot's setup, is held. Each case asserts its row's columns and, once the harness settles, that the session holds no binding, claim, or lease. `// spec: §6.2, §7.2, §15.1`.
14. A `lenny/request_input` wait longer than `maxClientIdleSeconds`, called through `Harness.GatewayControlCall` on a `lifecycleharness` replica that does not hold the session's coordination lease, through a W1 applied on a `running` row and a W2 that restores `running` (CODE-7 item 2), with at least one coordination Sweeper pass on the lease holder between them. The client's event stream carries `status_change(input_required)` and then one `status_change(running)`, and the session is neither idle-suspended nor expired. `Harness.GatewayControlCall` is new in `tests/testinfra/lifecycleharness/cells.go`: it builds the server `MCPCall` builds with `Events` set to the replica's session event bus, which `AddReplica` stores on a new `Replica.Events` field in `harness.go`, on a harness built with `Options.Events`, and dispatches through `platformtools.New(srv, h.Store).CallPlatformTool` on a context from `inputwaitrecovery.NewContext`, so the call carries a connection record (CODE-7 item 2, W0). `// spec: §7.2, §6.2, §10.1.1`.

**Tier 7a**
15. Two watchdogs and orphan cleanup under randomized interleavings over one session set produce one terminal write and one hook per session. `// spec: §10.1.1`.
16. Concurrent `/start` calls on one session, spread across two `Server` instances that share one store and one lease store: one reaches `running`, and every other call gets the answer of CODE-6 item 1's **`/start` exits** row P1 or P2, whichever instance serves it. `// spec: §15.1`.
17. Concurrent `request_input` waits on one session under randomized entry and exit interleavings, over one registry (CODE-7 item 3 row N1) and over two registries standing for the connection the adapter left and its next connection (row N2), with a case in which the later wait reuses the earlier wait's `requestId`. After every step the test asserts the row state that N1 and N2 give, and after the last exit it asserts the row is `running`. `// spec: §7.2`.

**Tier 8**
18. List and `Update` fault injection: one tenant's `List` error and one row's `Update` error leave every other tenant and row processed in the same pass, `Tick` returns the joined error, and the failed row is expired on the next pass. Cover the watchdog, orphan cleanup, and the orphan-session reconciler. An `Update` returning `ErrNotFound` in the same pass is absent from the joined error, ends its claim synchronously, and runs no hook. `// spec: §10.1.1`.
19. A blocking hook holds the terminal claim open (the shutdown wait observes it) and does not delay the next row, in each function CODE-4 item 3 names and in one watchdog sweep that ends its claim inline, such as `sweepMaxAge`; cancelling the pass context while the hook blocks leaves the context the hook received uncancelled, and the claim closes only after the hook returns. `// spec: §10.1.1, §7.1`.
20. The starting watchdog firing on a replica without the binding reclaims the pod by name. `// spec: §6.2, §7.2`.

**Wiring and gate tests**
21. Leader wiring (`cmd/lenny-gateway`), modeled on `slot_reaper_test.go`: `addOrphanSessionReconciler` registers `"orphan-session-reconciler"` through the add-func hook; `leader_sweeps.go` contains the `leaderGate.Add` registration before `leaderGate.Run`; `reconciler_workers.go` no longer contains `go orphanSessionReconciler.Run`; losing the lease clears the published lag series. `// spec: §10.1.4, §12.5`.
22. One test per row of the CODE-7 item 4 table, asserting that row's decision for an `input_required` session. `// spec: §7.2, §15.1`.
23. One test per existing any-state writer, such as `ForceTerminate` from `resume_pending` and from `awaiting_client_action`, and the budget terminator from `resuming`. `// spec: §7.2 (Terminal writes from any non-terminal state)`.

**Tier 9**
24. `tests/tier9_security/input_wait_recovery_pod_scope_test.go`: item 8's server with `clientCA` set and `trustDomain` empty, so the handshake does not check the URI SAN and every case reaches CODE-7 item 2 R1, over stale `input_required` rows of pods A and B whose waits no registry on the server holds. A call naming pod A's session leaves pod A's row `input_required` with its `InputWaitID` and publishes no `status_change` on a connection whose certificate carries pod B's agent SPIFFE URI, on one whose verified certificate carries an interceptor SPIFFE URI, on one whose certificate carries no URI SAN, and on a plaintext server. After the cross-pod call, that connection's next call naming pod B's session restores pod B's row; this is the assertion that catches a denied run marking the connection recovered. `// spec: §7.2, §28.3`.

**Tier 11**
25. `tests/tier11_docs/session_transition_doc_reconciliation_test.go`: the `STARTING_FAILED` row of `docs/reference/error-catalog.md` carries DOC-1's replacement text and no longer carries "a retry rebinds a fresh pod" (scope the check to that row, because the `SESSION_CREATION_FAILED` row keeps the phrase). The **Retries** paragraph of each of go.md, python.md, and typescript.md carries the DOC-1 sentence that `Start`/`start` sends its request once. The `/interrupt` **Valid precondition states:** line of `docs/api/rest.md` names `input_required`, and the transition table of `docs/reference/state-machines.md` carries `input_required` rows to `suspended` and to `completed`. `// spec: §7.2, §15.1, §15.6`.

## Edge cases and accepted failure modes

- Every replica still lists every tenant for every sweep. F-10.1.36 covers that cost.
- A child that declares no `perChildMaxAge` is not bounded by it (summary D10).
- A stale `input_required` row that no later call of its pod process recovers: CODE-7 item 3 row P1.

## Files touched on application (non-spec)

- migrations/0192_sessions_state_entered_at.up.sql, migrations/0192_sessions_state_entered_at.down.sql
- pkg/gateway/session/sessionstore/sessionstore.go, memstore/memstore.go, pgstore/pgstore.go
- tests/testinfra/lifecycleharness/steppedstore.go
- tests/testinfra/lifecycleharness/cells.go
- tests/testinfra/lifecycleharness/harness.go
- pkg/gateway/runtime/watchdog/watchdog.go, suspended.go
- pkg/delegation/lease/lease.go
- pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go
- pkg/gateway/session/orphansession/orphansession.go
- pkg/gateway/sessionserver/start.go, coordination_lease.go, session_terminal.go, session_transitions.go, usage.go, eval.go, resume_driver.go, terminal_writer_inventory_test.go
- pkg/gateway/externalapi/admin/users_invalidate.go
- pkg/session/state/state.go, state_test.go
- sdks/client/go/lenny/client.go, sdks/client/python/lenny/client.py, sdks/client/typescript/src/client.ts
- tests/tier7a_load_local/full_revoke_sweep_terminal_race_test.go
- pkg/gateway/mcpfabric/mcptools/mcptools_register.go
- pkg/gateway/session/inputwait/inputwait.go
- pkg/gateway/gatewaycontrol/inputwaitrecovery/ (new)
- cmd/lenny-gateway/gwcontrol_server.go, controlserver.go, shutdown.go
- pkg/gateway/checkpoint/checkpointer/freshness.go
- pkg/gateway/environment/translator/open_responses.go
- pkg/gateway/mcpfabric/delegation/service.go
- pkg/gateway/metrics/gatewaymetrics/ (the `ClearAgentPodStateMirrorLag` emitter)
- cmd/lenny-gateway/reconciler_workers.go, leader_sweeps.go
- The test files TEST-1 names, and tests/spec-map.json
- BUILD-GAPS.md, gateway-runtime-comms-remediation.md
- docs/reference/error-catalog.md, docs/client-guide/sdk-examples/go.md, python.md, typescript.md, docs/api/rest.md, docs/api/mcp.md, docs/client-guide/session-lifecycle.md, docs/client-guide/error-handling.md, docs/reference/state-machines.md, docs/getting-started/concepts.md
