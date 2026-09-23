# Non-spec changes: Concurrent finalize calls both prepare the session's pod

These changes are staged. Applying them is the implementation's work, under the
implementation checklist. Every code deliverable here lands after the spec deliverables it
implements.

## Design (implementation-facing)

Every finalize state write moves inside the existing locked `sessionstore.Store.Update` and re-checks the locked row before it writes. Both stores already run the mutation under the row lock and return a mutation error before writing: pgstore after `SELECT ... FOR UPDATE` inside `pgtenant.InTx`, which returns the mutation error unwrapped, and memstore under `s.mu`. A mutation that returns `*session.PreconditionError` therefore reaches the handler on both stores, `errors.As` finds it, and the existing `writePreconditionError` renders it as `409 INVALID_STATE_TRANSITION` with `details.currentState` and `details.allowedStates`.

The design has two guards:

- **Entry guard (CODE-2).** The created → finalizing mutation calls the existing `session.Validate` for `EndpointFinalize` against the locked row's state. `created` is written only by row inserts and no edge leads back into it, so exactly one call per session lifetime commits `finalizing`. The refused call returns before `storedWorkspacePlanForFinalize`, `prepareAtFinalize`, any reclaim, and any failure write, so it makes no pod RPC and writes no WorkspacePlan. The pre-lock `Get` and `Validate` stay as an early 404 or 409, because `resolveFinalizePlan` needs the row.
- **Exit guards (CODE-1, CODE-3).** The finalizing → ready write and every finalize failure write admit only `finalizing`, through CODE-1's `finalizingPrecondition`. `session.Validate` cannot express this from-state, because the endpoint table admits `created`. CODE-3's **handleFinalize rules** state what the handler does after a lost exit write.

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
// successor session's claim on a recycled pod. The revoke covers any lease
// this call's prepare phase minted after the terminal revoke, whether
// AssignCredentials completed or failed partway, and is a no-op when the
// session holds no lease.
// spec: §7.1 step 23 (lease release), §4.9
func (s *Server) revokeFinalizeLease(sessionID string) {
	if s.podBinder != nil && s.podBinder.Credentials != nil {
		s.podBinder.Credentials.ReleaseSession(sessionID)
	}
}
```

Re-cite the `reclaimFinalizedPod` comment from "§4.3 (Gap 2)" to `§7.1 steps 11-13 (finalize prepare phase)`, keeping its §7.1 step 23 and §4.6.1 citations.

handleFinalize rules:

No lost branch calls `reclaimFinalizedPod`, because `podclaim.DeleteClaim` deletes `claim-<podName>` with no owner check and the terminal writer's `terminalReclaimPreRunning` has already released the pod.

1. **Plan-parse failure** (`storedWorkspacePlanForFinalize` error). Call `ferr := s.failFinalizing(...)` first. If lost, call `writePreconditionError(w, ferr)` and return without reclaiming. Otherwise, whether the failure write committed or returned another store error, call `reclaimFinalizedPod` as today and return the existing 500.
2. **Prepare failure** (`prepareAtFinalize` error). The binder's `failPhase` or `prepareAtFinalize` has already reclaimed. Call `ferr := s.failFinalizing(...)`. If lost, call `revokeFinalizeLease(id)`, then `writePreconditionError(w, ferr)`. The revoke runs whether or not `prep` is set, because `prepareAtFinalize` returns a nil `prep` on every error (`pkg/gateway/sessionserver/finalize.go:280-283`). `failPhase` releases a lease only when `leaseAssigned` holds, and `Binder.Prepare` sets that flag only after `assignCredentials` returns nil (`pkg/gateway/podlifecycle/podsession/binder.go:948-953`, `:1072-1076`), so leases a partial assignment minted stay recorded under the session, and rule 4 sub-branch (ii) states why the terminal writer's own revoke can miss them. Otherwise use the existing `writePodClaimError` envelope, and log a non-precondition `ferr` through the package's existing logger.
3. **Upload-token consume.** The consume stays where it runs today, after the rule-4 ready write commits, and has no failure branch. `ConsumeDigest` fails only through `ConsumedTracker.MarkConsumed`, whose contract admits one error, `ErrConsumed`, for a digest that is already invalidated (`pkg/uploadtoken/uploadtoken.go:190-193`). Replace the consume block's `failSession`, `reclaimFinalizedPod`, and 500 with a log of any non-nil result through the package's existing logger (`log.Printf`, as in `pkg/gateway/sessionserver/finalize.go:384`), then continue to the upload-channel close. Rewrite the block's code comment to drop its "Gap 2: a consume failure" rationale and keep the §7.1 single-use citation. No failure write follows the ready write, so `finalizing` stays the only from-state of a finalize failure write, and a token left unconsumed mints no upload because uploads are admitted only in `created` (`pkg/gateway/sessionserver/upload.go:307-315`).
4. **Ready write.** Replace the mutation with one that calls `finalizingPrecondition(row)` and returns its error, then calls `transitionReady(row)`. If lost, call `revokeFinalizeLease(id)` when `prep != nil` and return `writePreconditionError(w, err)`, skipping the upload-channel close, the uploadLimits close, the SSE status change, and the finalize audit row. On any other store error (Gap 2), call `ferr := s.failFinalizing(...)` and branch on its result:
   - (i) `ferr` is nil or a non-precondition store error: call `reclaimFinalizedPod` when `prep != nil` and return the existing 500, as today.
   - (ii) Lost, and `session.IsTerminal(pe.CurrentState)` holds: a terminal writer ended the session. Call `revokeFinalizeLease(id)` when `prep != nil`, do not reclaim, and return `writePreconditionError(w, ferr)`. The lease store is in-memory and per-replica (`pkg/gateway/credentials/credleasestore/credleasestore.go:9-10`), so a terminal writer on another replica, or one whose `ReleaseSession` ran before `AssignCredentials` completed, cannot release this lease.
   - (iii) Lost, and the locked state is non-terminal: the ready write committed despite its error, and the session may already have moved on through `/start`. Neither revoke nor reclaim, and return the existing 500 carrying the ready-write error. The session legitimately holds the lease.

   Only this branch can find a non-terminal row, because `transitionReady` is the only writer of `ready` (`pkg/gateway/sessionserver/sessionserver.go:3030`), and rules 1 and 2 and the plain lost ready write run while only a terminal writer can move the row off `finalizing`.

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

Targets:

- `pkg/gateway/sessionserver/finalize_race_test.go` (new, package `sessionserver_test`, no build tag). It holds every `hookStore` case and runs on the `start_pod_test.go` pod-bind fixture that `TestFinalizePostCredentialWriteFailureRevokesLease_spec_7_1` uses: `podBindRuntime`, `podBindClient`, `podBindWarmPool`, `podBindTemplate`, `podBindIdleSandbox`, `podBindAdapterDialer`, `podBindBinder`, and `recordingLeaseAssigner` (`pkg/gateway/sessionserver/start_pod_test.go:63-210`, `:1142-1162`, and `:1175-1266`). Every adapter-backed case assigns `&podBindRuntime{}` to the in-process adapter's `Runtime`, as that test does at `:1179`. The in-process adapter over bufconn runs Prepare to success, so `prep != nil` in every case that reaches the prepare phase.
- `pkg/gateway/sessionserver/finalize_race_internal_test.go` (new, package `sessionserver`, memstore-backed like `finalize_plan_internal_test.go`). It holds the unit cases that call unexported symbols, listed under **Cases in `finalize_race_internal_test.go`** below.

Fixture of `finalize_race_test.go`:

- `hookStore` wraps `sessionstore.Store`, and `sessionserver.New` takes it as the store interface. It selects the Update to act on by the State the mutation writes when probed on a copy of the current row, plus an occurrence count of that State, following the probe in `readyTransitionFailStore` (`pkg/gateway/sessionserver/start_pod_test.go:1113-1136`). It does not select by the Nth Update, because `applyFinalizePrepareResult` adds a store Update only when `prep != nil`. On the selected Update it runs in one of three modes: run a callback and then delegate; return an injected error without committing; or delegate, commit, and then return an injected error (the ambiguous commit). A case can arm more than one selection, each with its own mode. The occurrence counter is never reset, because each case builds a fresh wrapper. A probe that selects the wrong Update leaves the hook unfired, and the case then observes an unraced finalize (200 and `ready`) and fails its 409 or 500 assertion.
- Every callback that commits a terminal state, or moves the row to `finalizing` for a simulated winner, writes directly to the wrapped inner store and bypasses the handlers. A terminal write through `handleDelete` runs `recordSessionCompleted` (`pkg/gateway/sessionserver/sessionserver.go:2958`), which emits the terminal lifecycle and reclaims the pod through `terminalReclaimPreRunning`, releasing the lease and deleting the claim (`pkg/gateway/sessionserver/usage.go:428` and `:446`, and `pkg/gateway/podlifecycle/podsession/binder.go:1097-1102`). A direct write runs none of these, so every lease release, claim delete, and lifecycle emission a case observes is the finalize handler's own. The handler-driven terminal path is TEST-3 Case B's subject.
- Observe a reclaim as the absence of `claim-sbx-1`, a revoke as `recordingLeaseAssigner.released`, SSE status changes through `Options.Events`, and audit rows and terminal lifecycle emissions through `Options.LifecycleAuditSink`. Do not write a fake podBinder: `Server.podBinder` is the concrete `*podsession.Binder`.
- **Malformed stored plan.** After the fixture's /create claims the pod, write WorkspacePlan bytes that `workspaceplan.ParseStored` (`pkg/workspaceplan/plan.go:312`) rejects to the row through the wrapped inner store, because /create validates the plan it accepts and an empty finalize body leaves the handler parsing the stored plan (`pkg/gateway/sessionserver/finalize.go:62-63`, `pkg/gateway/sessionserver/start.go:1218-1224`).

Cases in `finalize_race_test.go`. Each exit case cites the CODE-3 rule, and the sub-branch where the rule has them, that it drives, and asserts the outcome that rule gives:

- **Entry refusal.** Both calls pass the pre-lock read; the hook commits the winner's `finalizing` before the loser's entry Update. Assert 409 with `currentState` set to the locked state and `allowedStates=[created]`; WorkspacePlan unchanged when the loser sent a body; no terminal lifecycle event; `claim-sbx-1` still present.
- **Terminal write between the Get and the lock.** The hook commits `cancelled` before the entry Update. Assert 409 with `currentState=cancelled` and the row unchanged.
- **Row deleted before the lock.** The hook deletes the row before the entry Update. Assert 404 `RESOURCE_NOT_FOUND`.
- **Ready-write loss** (CODE-3 rule 4, lost ready write), one sub-case each for a terminal write of `cancelled`, of `completed`, and of `failed` with `FINALIZE_TIMEOUT`, each committed by the hook before the ready Update. Assert 409 with `currentState` set to the terminal state and `allowedStates=[created]`; State and FailureReason unchanged; no `status_change` to `ready` on the SSE bus; no `session.finalize_workspace` audit row; `recordingLeaseAssigner.released` equal to exactly `[id]`, which only `revokeFinalizeLease` can issue here and which fails when that call is removed; and `claim-sbx-1` still present.
- **Prepare failure after a terminal write** (CODE-3 rule 2). A setup command that exits non-zero fails the prepare phase, as in `TestFinalizeFailsSessionAndReclaimsPodOnSetupError_spec_7_5`. The hook commits `cancelled` before the `failFinalizing` Update. Assert 409 with `currentState=cancelled`, the row stays `cancelled`, and zero terminal lifecycle emissions, because the lost `failFinalizing` emits nothing.
- **Credential-assignment failure after a terminal write** (CODE-3 rule 2). On the pod-bind fixture, make the in-process adapter's `AssignCredentials` RPC fail after `recordingLeaseAssigner.AssignProto` has recorded the lease; the fixture has one pool and an `AssignProto` that never fails, so the RPC failure is the reachable partial-assignment trigger. The hook commits `cancelled` before the `failFinalizing` Update. Assert 409 with `currentState=cancelled`, `recordingLeaseAssigner.assigns` equal to `[id]`, and `recordingLeaseAssigner.released` equal to exactly `[id]`. `failPhase` issues no release when `leaseAssigned` is false, so only `revokeFinalizeLease` produces that entry, and the case fails when that call is removed.
  **IMPLEMENTOR'S CHOICE:** how the `AssignCredentials` RPC failure is injected, since the pod-bind fixture has no hook for it today — the injection must make `assignCredentials` return an error after `AssignProto` has run, and must leave every other pod-bind case's behavior unchanged.
- **Plan-parse failure after a terminal write** (CODE-3 rule 1). Seed the **Malformed stored plan** and send finalize with an empty body. The hook commits `cancelled` before the `failFinalizing` Update. Assert 409 with `currentState=cancelled` and `claim-sbx-1` still present, which shows the lost branch skips the reclaim.
- **Plan-parse failure, committed** (CODE-3 rule 1). Seed the **Malformed stored plan** and send finalize with an empty body. Arm no hook. Assert the existing 500, row State `failed`, `claim-sbx-1` absent, and exactly one terminal lifecycle emission through `Options.LifecycleAuditSink`. This case fails when the reclaim is dropped, and the lost case above fails when the reclaim runs before or regardless of the failure write.
- **Consume after the ready write** (CODE-3 rule 3). The server carries an `uploadtoken.Verifier` over a `ConsumedTracker` whose `MarkConsumed` reads the row's State from the wrapped inner store, records it, and returns `ErrConsumed`, and the row carries an `UploadTokenDigest`. Assert that the tracker recorded `ready`, the response is 200, the row stays `ready`, `claim-sbx-1` is still present, and `recordingLeaseAssigner.released` is empty.
- **Ready-write store error, failure write lost to a terminal writer** (CODE-3 rule 4, Gap-2 sub-branch (ii)). The hook returns an injected error on the ready Update without committing, then commits `cancelled` before the `failFinalizing` Update. Assert 409 with `currentState=cancelled`, the row stays `cancelled`, `recordingLeaseAssigner.released` equal to exactly `[id]`, and `claim-sbx-1` still present.
- **Ready-write ambiguous commit** (CODE-3 rule 4, Gap-2 sub-branch (iii)). The hook commits the ready Update and then returns an injected error, so `failFinalizing` loses to `ready`. Assert the existing 500, the row stays `ready`, `claim-sbx-1` still present, `recordingLeaseAssigner.released` empty, and no terminal lifecycle emission.
- **Unraced call.** Assert the row reaches `ready` with no change in the response.

CODE-3 rule 4 Gap-2 sub-branch (i) is pinned by the existing `TestFinalizePostCredentialWriteFailureRevokesLease_spec_7_1` (`pkg/gateway/sessionserver/start_pod_test.go:1175-1266`), which still passes under the guarded mutation because its probe writes `ready` on a `finalizing` row. `finalize_race_test.go` adds no duplicate of it.

Cases in `finalize_race_internal_test.go`:

- **failSession stays unconditional.** A `failSession` call on a `cancelled` row still writes `failed`, pinning that non-finalize callers are unchanged.
- **finalizingPrecondition.** A refused state leaves the memstore row unchanged, and `errors.As` finds `*session.PreconditionError` with the locked `CurrentState`.
- **failFinalizing commits and emits the terminal tail.** Seed a `finalizing` memstore row, build the server with `Options{Events: bus, LifecycleAuditSink: sink}`, and call CODE-3's `failFinalizing`. Assert a nil error, State `failed`, exactly one `status_change`, exactly one `session_complete`, and exactly one `session.failed` audit event, using the helpers `TestFailSessionEmitsTerminalLifecycle_spec_7_2_2` uses (`pkg/gateway/sessionserver/lifecycle_internal_test.go:290-312`). The case carries `// spec: 7.2 (terminal states), 11.7`. It pairs with the **Prepare failure after a terminal write** case, which asserts zero emissions on a lost write.

Annotation on both files: `// spec: 15.1 (finalize precondition), 7.1 (steps 11-13, step 23), 6.2 (finalize timeout), 7.2 (terminal states)`.

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
- Wire one `blobstore.NewMemoryStore(nil)` on the binder's `Blobs` and on `sessionserver.Options.Blobs`, as `TestEagerClaimLifecycleCreateUploadFinalizeStart` does (`tests/tier4_integration/eager_claim_lifecycle_test.go:237-238` and `:282`), because `eagerCluster` wires no blob store.
- Extend `eagerAdapterDialer`, or add a sibling, to pass a `grpc.ServerOption` to `adapter.NewGRPCServer`. The option installs stream and unary interceptors that count PrepareWorkspace, RunSetup, AssignCredentials, and DemoteSDK.
- Wrap the envtest `client.Client` in a counting wrapper that records `Delete` calls on SandboxClaim objects by name.
- Wrap memstore in a hook store equivalent to TEST-1's `hookStore`, defined in this package because a test file's types cannot be imported from another package.
- Do not route through the idempotency middleware. Keyless and distinct-key calls reach the handler identically.

Case A (deterministic entry race):

- Before the race, POST one upload to the `created` session, as the lifecycle test does (`tests/tier4_integration/eager_claim_lifecycle_test.go:316-318`). The binder issues PrepareWorkspace only for a non-empty upload set (`pkg/gateway/podlifecycle/podsession/binder.go:1323-1330`), so without the upload no finalize reaches PrepareWorkspace.
- Issue two keyless `POST /finalize` calls on that session, each carrying a `workspacePlan` with one `uploadFile` source that names the upload's returned `uploadRef`, following the lifecycle test (`tests/tier4_integration/eager_claim_lifecycle_test.go:339-345`).
- Call 1's hook, placed before its entry Update, blocks until call 2 has completed its pre-lock Get. Call 2's entry Update then runs after call 1 commits.
- Expect exactly one 200 and one 409 with `details.currentState` of `finalizing` or `ready` and `allowedStates=[created]`.
- Expect adapter counters of exactly one PrepareWorkspace, at most one RunSetup, and zero DemoteSDK.
- Expect `assigner.assignCount()==1` and zero SandboxClaim deletes.

Case B (exit race):

- A hook before the ready Update issues `DELETE /v1/sessions/{id}` through the same handler.
- Expect finalize to return 409 with `currentState=cancelled`, and the stored row to stay `cancelled` with no `ready` status change.
- Expect `assigner.released` to hold exactly two entries for the session: one from the terminal ReclaimClaimed and one from the `revokeFinalizeLease` call of CODE-3 rule 4.
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
- **Ambiguous ready commit.** CODE-3 rule 4 states the response. The accepted consequences are: the client receives 500 for a session that is `ready`, and it learns the state from GET; the call emits no `status_change` to `ready` on the SSE bus and no `session.finalize_workspace` audit row; and the call skips the upload-channel close and the per-session upload-byte release, as every other exit-write failure branch does.
- **Client disconnect during Prepare.** The request context is cancelled, so `failFinalizing` runs on a cancelled context and the row stays `finalizing` until the watchdog fires. This predates the race and is recorded in the summary's shipped-tree defects.
- **Terminate from `finalizing` does not abort a running Prepare.** The finalize call runs to its exit write and loses; CODE-3's handleFinalize rules state its response. The pod was already released by the terminal writer.
- **SetupOutput and WorkspaceRoot persist on a terminal row.** `applyFinalizePrepareResult` stays unguarded; the persisted setup trail is audit data and changes no state.
- **Double lease revoke.** On exit loss, the terminal reclaim and `revokeFinalizeLease` can both release the lease. `ReleaseSession` is a no-op when the session holds no lease.

## Files touched on application (non-spec)

- `pkg/gateway/sessionserver/sessionserver.go`
- `pkg/gateway/sessionserver/start.go`
- `pkg/gateway/sessionserver/finalize.go`
- `pkg/gateway/session/sessionstore/sessionstore.go`
- `pkg/gateway/sessionserver/finalize_race_test.go` (new)
- `pkg/gateway/sessionserver/finalize_race_internal_test.go` (new)
- `tests/tier2_component/stores/sessionstore_test.go`
- `tests/tier4_integration/finalize_admission_race_test.go` (new)
- `tests/tier4_integration/eager_claim_lifecycle_test.go` (dialer option, if extended in place)
- `tests/tier7a_load_local/finalize_admission_race_test.go` (new)
- `docs/api/rest.md`
- `docs/api/mcp.md`
