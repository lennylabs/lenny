# Non-spec changes: Concurrent finalize calls both prepare the session's pod

These changes are staged. Applying them is the implementation's work, under the
implementation checklist. Every code deliverable here lands after the spec deliverables it
implements.

## Design (implementation-facing)

Every finalize state write moves inside the existing locked `sessionstore.Store.Update` and re-checks the locked row before it writes. Both stores already run the mutation under the row lock and return a mutation error before writing: pgstore after `SELECT ... FOR UPDATE` inside `pgtenant.InTx`, which returns the mutation error unwrapped, and memstore under `s.mu`. A mutation that returns `*session.PreconditionError` therefore reaches the handler on both stores, `errors.As` finds it, and the existing `writePreconditionError` renders it as `409 INVALID_STATE_TRANSITION` with `details.currentState` and `details.allowedStates`.

The design has two guards:

- **Entry guard (CODE-2).** The created → finalizing mutation calls the existing `session.Validate` for `EndpointFinalize` against the locked row's state. `created` is written only by row inserts and no edge leads back into it, so exactly one call per session lifetime commits `finalizing`. The refused call returns before `storedWorkspacePlanForFinalize`, `prepareAtFinalize`, any reclaim, and any failure write, so it makes no pod RPC and writes no WorkspacePlan. The pre-lock `Get` and `Validate` stay as an early 404 or 409, because `resolveFinalizePlan` needs the row.
- **Exit guards (CODE-1, CODE-3).** The finalizing → ready write and every finalize failure write admit only `finalizing`, through CODE-1's `finalizingPrecondition`. `session.Validate` cannot express this from-state, because the endpoint table admits `created`. A lost exit write means another writer already moved the row to a terminal state (the outcome SPEC-1 states). The handler then revokes only the session-keyed lease and never reclaims the pod, because `podclaim.DeleteClaim` deletes `claim-<podName>` with no owner check and the terminal writer's `terminalReclaimPreRunning` has already released it.

"Lost" throughout this file means the Update returned an error for which `errors.As(err, &pe)` with `pe *session.PreconditionError` succeeds.

The behavior these guards enforce is stated by the §15.1 preamble, the §15.1 terminate and DELETE rows, §6.2, §7.1 step 23, and SPEC-1. The code cites them rather than restating them.

## Staged code changes

### CODE-1 · pkg/gateway/sessionserver/start.go · finalizingPrecondition

Add one unexported function beside `failSession`, used by CODE-3's ready write and by `failFinalizing`:

```go
// finalizingPrecondition refuses a finalize exit write unless the locked row
// is still `finalizing`. Another writer (for example terminate, DELETE, admin
// force-terminate, the finalizing watchdog, or the orphan session reconciler)
// can move the row to a terminal state while the prepare phase runs; the exit
// write must not overwrite that state. The refusal reuses the §15.1
// precondition error so writePreconditionError renders the 409 with the
// locked state. EndpointFinalize has no capability-gated states, so passing
// nil capabilities to AllowedStates yields the complete allowed set.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states)
func finalizingPrecondition(row *sessionstore.Session) error {
	if row.State == session.StateFinalizing {
		return nil
	}
	return &session.PreconditionError{
		Endpoint:      session.EndpointFinalize,
		CurrentState:  row.State,
		AllowedStates: session.AllowedStates(session.EndpointFinalize, nil),
	}
}
```

No new file and no generic transition-guard helper is added. A shared guard for handleTransition and handleDelete belongs to that follow-up finding and must be built on `session.Validate` so it stays capability-aware.

### CODE-2 · pkg/gateway/sessionserver/sessionserver.go handleFinalize · entry compare-and-swap

Targets: handleFinalize's created → finalizing Update; the doc comments of `transitionFinalizing`, `transitionReady`, and `handleFinalize`; and the `Store.Update` doc comment in `pkg/gateway/session/sessionstore/sessionstore.go`.

Replace the unconditional created → finalizing mutation and its error branch with the block below. The closure parameter is named `row` so it does not shadow the `*http.Request` `r`.

```go
// spec: §15.1 (finalize precondition); §7.1 steps 11-13. The pre-lock
// Validate above is an early rejection. This check against the locked row is
// authoritative, so of overlapping calls that both read `created`, exactly
// one commits `finalizing`.
updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointFinalize,
		CurrentState: row.State,
	}); err != nil {
		return err
	}
	transitionFinalizing(row)
	if hasPlan {
		row.WorkspacePlan = planJSON
	}
	return nil
})
if errors.Is(err, sessionstore.ErrNotFound) {
	s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
	return
}
if err != nil {
	s.writePreconditionError(w, err) // 409 for *PreconditionError, 500 otherwise
	return
}
```

Do not add a `writeFinalizeStoreError` helper; the inline `ErrNotFound` branch plus the existing `writePreconditionError` cover every case.

Citation repair: in the rewritten comments of `handleFinalize`, `transitionFinalizing`, and `transitionReady`, replace "§4.3 preparation barrier" and "§4.3 (proposal)" with `§7.1 steps 11-13` and `§15.1`.

`Store.Update` doc comment: after the sentence stating that the store does not validate the transition, add one sentence: "A caller that must refuse a stale transition re-checks the state inside mutate against the locked row and returns an error, which aborts the write."

### CODE-3 · handleFinalize exit writes, failFinalizing, afterFailed, and revokeFinalizeLease

Targets:

- `pkg/gateway/sessionserver/sessionserver.go` handleFinalize: the plan-parse failure branch, the prepare-failure branch, the upload-token consume, and the finalizing → ready write with its Gap-2 branch.
- `pkg/gateway/sessionserver/start.go`: new `failFinalizing` and `afterFailed` beside `failSession`.
- `pkg/gateway/sessionserver/finalize.go`: new `revokeFinalizeLease` beside `reclaimFinalizedPod`, and a re-cited `reclaimFinalizedPod` comment.

`start.go`:

```go
// afterFailed runs the terminal tail of a `failed` write: archive a settled
// child and emit the terminal lifecycle. failSession and failFinalizing share it.
// spec: §7.2, §11.7, §7.1
func (s *Server) afterFailed(ctx context.Context, updated sessionstore.Session) {
	s.archiveSettledChild(ctx, updated)
	s.emitTerminalLifecycle(ctx, updated)
}

// failFinalizing marks a finalizing session failed, but only while the locked
// row is still `finalizing`. A lost write returns *session.PreconditionError
// and emits nothing, so a terminal state another writer committed is kept and
// no second terminal lifecycle is emitted.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states)
func (s *Server) failFinalizing(ctx context.Context, tenantID, id string) error {
	updated, err := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		if err := finalizingPrecondition(row); err != nil {
			return err
		}
		row.State = session.StateFailed
		return nil
	})
	if err != nil {
		return err
	}
	s.afterFailed(ctx, updated)
	return nil
}
```

`failSession` keeps its unconditional write and calls `afterFailed` for its tail. Its other callers (the `/start` path and tree recovery) are unchanged. The parameter type of `afterFailed` matches whatever `store.Update` returns today.

`finalize.go`:

```go
// revokeFinalizeLease releases the session-keyed credential lease without
// touching the pod claim. It runs when a finalize exit write loses to a
// terminal writer: that writer has already reclaimed the pod, and a second
// DeleteClaim on claim-<podName> has no owner check and could remove a
// successor session's claim on a recycled pod. The revoke covers an
// AssignCredentials that completed after the terminal revoke, and is a no-op
// when the session holds no lease.
// spec: §7.1 step 23 (lease release), §4.9
func (s *Server) revokeFinalizeLease(sessionID string) {
	if s.podBinder != nil && s.podBinder.Credentials != nil {
		s.podBinder.Credentials.ReleaseSession(sessionID)
	}
}
```

Re-cite the `reclaimFinalizedPod` comment from "§4.3 (Gap 2)" to `§7.1 steps 11-13 (finalize prepare phase)`, keeping its §7.1 step 23 and §4.6.1 citations.

handleFinalize rules, in handler order:

1. **Plan-parse failure** (`storedWorkspacePlanForFinalize` error). Call `ferr := s.failFinalizing(...)` first. If lost, call `writePreconditionError(w, ferr)` and return without reclaiming. Otherwise, whether the failure write committed or returned another store error, call `reclaimFinalizedPod` as today and return the existing 500.
2. **Prepare failure** (`prepareAtFinalize` error). The binder's `failPhase` or `prepareAtFinalize` has already reclaimed. Call `ferr := s.failFinalizing(...)`. If lost, call `writePreconditionError(w, ferr)`. Otherwise use the existing `writePodClaimError` envelope, and log a non-precondition `ferr` through the package's existing logger.
3. **Upload-token consume.** Move the consume ahead of the ready write, still after `applyFinalizePrepareResult`. Uploads are admitted only in `created` and the row is already `finalizing`, so the upload window is unchanged. On a consume failure, call `ferr := s.failFinalizing(...)`. If lost, call `revokeFinalizeLease(id)` when `prep != nil` and return `writePreconditionError(w, ferr)`. Otherwise call `reclaimFinalizedPod` when `prep != nil` and return the existing 500.
4. **Ready write.** Replace the mutation with one that calls `finalizingPrecondition(row)` and returns its error, then calls `transitionReady(row)`. If lost, call `revokeFinalizeLease(id)` when `prep != nil` and return `writePreconditionError(w, err)`, skipping the upload-channel close, the uploadLimits close, the SSE status change, and the finalize audit row. On any other store error (Gap 2), call `ferr := s.failFinalizing(...)`. If that is lost (for example an ambiguous commit that left `ready`, or a terminal writer), do not reclaim and return `writePreconditionError(w, ferr)`. Otherwise call `reclaimFinalizedPod` when `prep != nil` and return the existing 500.

Comments on these branches cite `// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states), §7.1 step 23 (lease release)`. `applyFinalizePrepareResult` stays unguarded, because it writes no state.

## Staged schema, chart, and migration changes

This proposal stages no schema, chart, or migration change.

## Staged docs changes

### DOCS-1 · docs/api/rest.md and docs/api/mcp.md · finalize overtaken-call outcome

(a) `docs/api/rest.md`, section `### POST /v1/sessions/{id}/finalize`: insert the paragraph below after the line "**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409)." and before `### POST /v1/sessions/{id}/start`.

```markdown
A finalize call that is still running when the session ends, for example through `POST /v1/sessions/{id}/terminate`, `DELETE /v1/sessions/{id}`, an administrator force-terminate, or the gateway's finalizing timeout (`maxFinalizingTimeoutSeconds`), returns `INVALID_STATE_TRANSITION` (409), even when its own setup also failed. The session keeps the terminal state in `details.currentState`, and the gateway releases any credential lease the call assigned.
```

Do not add an overlapping-calls sentence. The section's precondition line and the page's Error Handling envelope already cover it, and a sentence saying every other call returns 409 would misstate same-`Idempotency-Key` replay.

(b) `docs/api/mcp.md`, the `finalize_workspace` error table: replace the `INVALID_STATE_TRANSITION` row's "When" cell `Session not in `created` state` with:

```markdown
Session not in `created` state, or the session ended while finalization was in progress
```

This keeps the REST and MCP reference pages consistent. Include no spec section numbers, apply `doc-style.md`, and run tier 11.

## Testing

Each test carries a `// spec:` annotation, and every test at tier 2 and above carries a `// diagnosis:` comment immediately above the function declaration.

### TEST-1 · Tier 1 · deterministic entry and exit interleavings

Target: `pkg/gateway/sessionserver/finalize_race_internal_test.go` (new, package `sessionserver`, memstore-backed like `finalize_plan_internal_test.go`).

Fixture:

- `hookStore` wraps `sessionstore.Store` and runs a callback before the Nth `Update`, following the barrier-store pattern in `tests/tier7a_load_local/tracing_context_concurrent_registration_test.go`. `sessionserver.New` takes the store interface, so the wrapper plugs in directly.
- Where a case needs a binder, build a real `*podsession.Binder{Client: fake.NewClientBuilder()..., Namespace, Credentials: recordingAssigner}`, following `reclaimTestServer` in `terminal_reclaim_internal_test.go`. Observe a reclaim as the absence of the seeded `claim-<pod>` SandboxClaim, and a revoke as the recording assigner's released list. Do not write a fake podBinder: `Server.podBinder` is the concrete `*podsession.Binder`.
- Prepare cannot succeed without an adapter, so `prep` is nil in every tier-1 case. Tier 1 asserts no Prepare count and no ReleaseSession call from the finalize handler; TEST-3 owns those.

Cases:

- **Entry refusal.** Both calls pass the pre-lock read; the hook commits the winner's `finalizing` before the loser's entry Update. Assert 409 with `currentState` set to the locked state and `allowedStates=[created]`; WorkspacePlan unchanged when the loser sent a body; no terminal lifecycle event; with a fake-client binder, the seeded SandboxClaim still present.
- **Terminal write between the Get and the lock.** The hook commits `cancelled` before the entry Update. Assert 409 with `currentState=cancelled` and the row unchanged.
- **Row deleted before the lock.** The hook deletes the row before the entry Update. Assert 404 `RESOURCE_NOT_FOUND`.
- **Ready-write loss**, one sub-case each for DELETE → `cancelled`, terminate → `completed`, and a watchdog-style guarded write → `failed` with `FINALIZE_TIMEOUT`, each committed by the hook before the ready Update. Assert 409 with `currentState` set to the terminal state and `allowedStates=[created]`; State and FailureReason unchanged; no `status_change` to `ready` on the SSE bus; no `session.finalize_workspace` audit row; zero ReleaseSession calls from the finalize handler, which pins CODE-3's `prep != nil` gate.
- **Prepare failure after DELETE.** Fake-client binder with no matching pool, so `ResolvePool` fails inside `prepareAtFinalize`. The hook commits DELETE before the `failFinalizing` Update. Assert 409 with `currentState=cancelled`, the row stays `cancelled`, and exactly one terminal lifecycle emission.
- **Plan-parse failure after DELETE.** Assert 409 with `currentState=cancelled` and the seeded claim still present, which shows the lost branch skips the reclaim.
- **Consume failure from `finalizing`.** An upload verifier whose consume fails. Assert the row reaches `failed` and the existing 500.
- **Unraced call.** Assert the row reaches `ready` with no change in the response.
- **failSession stays unconditional.** A `failSession` call on a `cancelled` row still writes `failed`, pinning that non-finalize callers are unchanged.
- **finalizingPrecondition.** A refused state leaves the memstore row unchanged, and `errors.As` finds `*session.PreconditionError` with the locked `CurrentState`.

Annotation: `// spec: 15.1 (finalize precondition), 7.1 (steps 11-13, step 23), 6.2 (finalize timeout), 7.2 (terminal states)`.

### TEST-2 · Tier 2 · a guarded mutation serializes on the Postgres row lock

Target: `tests/tier2_component/stores/sessionstore_test.go`, a new `t.Run` subtest "guarded mutation serializes on the locked row" inside `TestSessionStoreContract`. It is not a new top-level function. Keep the parent's `// spec: 12.2.1` and add a comment inside the subtest citing `15.1 (finalize precondition)`. Extend the parent's `// diagnosis:` with one clause: a guarded Update that runs concurrently must see the committed state and refuse without writing.

Use an inline closure in the test body, because the CODE-1 helper is unexported in another package. The closure admits only `StateCreated` and otherwise returns `&session.PreconditionError{CurrentState: row.State}`.

Steps:

1. Seed a `created` row.
2. Goroutine A's mutation sets `StateFinalizing` and a WorkspacePlan, signals that it has entered, and blocks on a release channel.
3. After A has entered, start goroutine B's Update with the same closure.
4. Poll `pg_stat_activity` through a separate connection until a backend whose query matches `ILIKE '%FOR UPDATE%'` has `wait_event_type='Lock'`. Filter on `datname` or `application_name` so parallel subtests do not match. Use a bounded deadline and call `t.Fatal` on timeout. Then release A.

Assertions:

- A succeeds.
- `errors.As` on B's error yields `*session.PreconditionError` with `CurrentState=finalizing`.
- The stored row is `finalizing` with A's WorkspacePlan.
- The stored `UpdatedAt` equals A's returned `UpdatedAt`, which proves B wrote nothing.

Run under tier 2 with `-race`.

### TEST-3 · Tier 4 · real binder, no pod RPC from the refused call, and a lease-only revoke on exit loss

Target: `tests/tier4_integration/finalize_admission_race_test.go` (new).

Fixture:

- Reuse `eagerCluster` and `recordingAssigner` from `eager_claim_lifecycle_test.go`.
- Extend `eagerAdapterDialer`, or add a sibling, to pass a `grpc.ServerOption` to `adapter.NewGRPCServer`. The option installs stream and unary interceptors that count PrepareWorkspace, RunSetup, AssignCredentials, and DemoteSDK.
- Wrap the envtest `client.Client` in a counting wrapper that records `Delete` calls on SandboxClaim objects by name.
- Wrap memstore in a hook store equivalent to TEST-1's `hookStore`, defined in this package because TEST-1's type is internal to `sessionserver`.
- Do not route through the idempotency middleware. Keyless and distinct-key calls reach the handler identically.

Case A (deterministic entry race):

- Issue two keyless `POST /finalize` calls on one `created` session.
- Call 1's hook, placed before its entry Update, blocks until call 2 has completed its pre-lock Get. Call 2's entry Update then runs after call 1 commits.
- Expect exactly one 200 and one 409 with `details.currentState` of `finalizing` or `ready` and `allowedStates=[created]`.
- Expect adapter counters of exactly one PrepareWorkspace, at most one RunSetup, and zero DemoteSDK.
- Expect `assigner.assignCount()==1` and zero SandboxClaim deletes.

Case B (exit race):

- A hook before the ready Update issues `DELETE /v1/sessions/{id}` through the same handler.
- Expect finalize to return 409 with `currentState=cancelled`, and the stored row to stay `cancelled` with no `ready` status change.
- Expect `assigner.released` to contain the session ID. The terminal ReclaimClaimed revokes, and the finalize handler's lease-only revoke may add a second no-op entry.
- Expect the counting client to record exactly one Delete of `claim-sbx-1`, which is the terminal reclaim.

Annotations and preflight:

- `// spec: 15.1, 7.1 (steps 11-13), 4.9, 7.2` and a `// diagnosis:` stating that a failure means a refused or overtaken finalize reached the pod or deleted a claim it no longer owned.
- Call `envtest.SkipUnlessAvailable`.
- Run the `test-coverage.md` environment preflight, in particular reaping orphaned envtest processes.

### TEST-4 · Tier 7a · concurrent keyless finalizes and DELETE under `-race`

Target: `tests/tier7a_load_local/finalize_admission_race_test.go` (new, reusing `raceStart` from `racestart_testsupport_test.go`). Do not add a `tests/flake-budget.yaml` entry; that file is the quarantine registry. Declare the stress budget in the file's doc comment in the established form:

```
lenny-test stress --test TestConcurrentFinalizeAdmitsExactlyOne_spec_15_1 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
```

Fixture: sessionserver on memstore wrapped in a store that records each committed State transition, with `podBinder` nil and `uploadVerifier` nil.

- **TestConcurrentFinalizeAdmitsExactlyOne_spec_15_1.** For each of `raceAttempts` fresh fixtures, create one session, then release two keyless `POST /finalize` calls through `newRaceStart(2)`. Assert exactly one 200 and one 409; the 409 has `currentState` in {`finalizing`, `ready`} and `allowedStates=[created]`; exactly one created → finalizing transition is recorded; the final state is `ready`.
- **TestConcurrentFinalizeAndDeleteKeepTerminal_spec_15_1.** Per attempt, use `newRaceStart(3)` for two finalizes and one DELETE. Assert at most one finalize returns 200; at most one created → finalizing transition; each finalize 409 has `currentState` in {`finalizing`, `ready`, `cancelled`}; the final state is `cancelled` whenever DELETE returned 200; and the recorder shows no State change away from a terminal value.

Annotations: `// spec: 15.1 (finalize precondition), 7.2 (terminal states)` and a `// diagnosis:` stating that a failure means the finalize state writes are no longer checked against the locked session row. Run with `-race`.

## Edge cases and accepted failure modes

- **Ambiguous entry commit.** If the entry Update returns an error after committing, the handler returns 500 and does not re-read or fail the row, because it cannot tell its own commit from a concurrent winner's. The §6.2 watchdog resolves an orphaned `finalizing` row.
- **Client disconnect during Prepare.** The request context is cancelled, so `failFinalizing` runs on a cancelled context and the row stays `finalizing` until the watchdog fires. This predates the race and is recorded in the summary's shipped-tree defects.
- **Terminate from `finalizing` does not abort a running Prepare.** The finalize call runs to its exit write, loses, and revokes its lease. The pod was already released by the terminal writer.
- **SetupOutput and WorkspaceRoot persist on a terminal row.** `applyFinalizePrepareResult` stays unguarded; the persisted setup trail is audit data and changes no state.
- **Double lease revoke.** On exit loss, the terminal reclaim and `revokeFinalizeLease` can both release the lease. `ReleaseSession` is a no-op when the session holds no lease.

## Files touched on application (non-spec)

- `pkg/gateway/sessionserver/sessionserver.go`
- `pkg/gateway/sessionserver/start.go`
- `pkg/gateway/sessionserver/finalize.go`
- `pkg/gateway/session/sessionstore/sessionstore.go`
- `pkg/gateway/sessionserver/finalize_race_internal_test.go` (new)
- `tests/tier2_component/stores/sessionstore_test.go`
- `tests/tier4_integration/finalize_admission_race_test.go` (new)
- `tests/tier4_integration/eager_claim_lifecycle_test.go` (dialer option, if extended in place)
- `tests/tier7a_load_local/finalize_admission_race_test.go` (new)
- `docs/api/rest.md`
- `docs/api/mcp.md`
