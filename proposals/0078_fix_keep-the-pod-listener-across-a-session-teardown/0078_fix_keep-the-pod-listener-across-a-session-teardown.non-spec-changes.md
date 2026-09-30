# Non-spec changes: Keep the pod's runtime listener across a session teardown

## 2. Decisions

**D1. The listener's lifetime is the adapter process's.** §1.2 establishes this from the specification.
The code states the opposite in the comments §7.1 corrects and implements the opposite in one statement,
and both are corrected.

**D2. The teardown moves rather than disappearing.** Leaving the listener unclosed would also correct the
scope error, because process exit releases the descriptor and a Linux abstract address vanishes with its
process. It would leave the tests worse off. The in-package tests and the tier-7a fixture
(`socketDrainPod` in `tests/tier7a_load_local/shutdown_drain_gate_race_test.go`) bind a per-case address
in one test binary and would hold every one of them for the binary's life, which is invisible at
`-count=1` and surfaces under `lenny-test stress`. An explicit method costs one function and gives every
caller a correct cleanup call.

**D3. The method is named `CloseListener`.** `Close` is taken by the `RuntimeProcess` contract with a
different arity and a different meaning, and two methods named `Close` on one type is the ambiguity this
proposal exists to remove. `Shutdown` collides with the adapter's `Server.Shutdown` RPC
in `pkg/adapter/session.go`, which is itself a per-session teardown, so it reproduces the confusion in
a second place. `CloseListener` names the resource it closes.

**D4. `CloseListener` is on the concrete type and not on `RuntimeProcess`.** Only the socket transport
holds a pod-scoped listener. Widening the interface would drag a no-op method through the embedded,
subprocess, MCP, and SDK-warm implementations and through the test doubles in every package that carries
one, for a single caller. Proposal 0073's SCHEMA-1 declined an interface widening on the same reasoning,
and keeping the interface untouched keeps this change clear of that freeze. `cmd/lenny-adapter` holds the
concrete value at the point it assigns `adapterSrv.Runtime`.

**D5. `CloseListener` is deferred in `main` where the socket transport is constructed, so it runs after
`srv.Serve` returns.** `Serve` returns only after `GracefulStop` has let in-flight RPCs complete, and an
in-flight `StartSession` may be parked in `accept`. Closing the listener under it would fail that RPC
with the exact error this proposal removes, on the one path where the failure is not a defect. After a
non-graceful `Stop`, `Serve` returns without waiting for handlers, and a parked start then fails on the
closed listener while the process exits. The call does not go in the signal goroutine after
`GracefulStop`, because `Serve` returns at that same moment, `main` returns, and the process exits
without waiting for that goroutine.

**D6. The listener is not closed and rebound on the next `Start`.** The alternative would keep the code's
current doc comments true and is rejected. The address is written into the runtime container's
environment when the pod is rendered and is never re-delivered, and the runtime may dial at any point
after its container starts, including while the pod sits between sessions. A close-and-rebind cycle opens
a window in which the advertised name resolves to nothing, and the specification states no dial-retry
contract the runtime could be held to. The platform MCP server does close and re-arm its listener
(`pkg/adapter/platformmcp.go:52-58`), which is safe only because one owner does both; `CH-MSGSOCK` has no
rebinder.

**D7. `Close` returns `nil` on the last-session path, and the consequence is stated rather than
compensated for.** Every error `Close` could previously report on the socket transport came from
unbinding a listener that should not have been unbound. `Server.tearDownReclaimedSlot` in
`pkg/adapter/session.go` assigns that value to `closeErr`, which selects the `leaked` outcome in
`reportSessionScrub`, the outcome that drives the §5.2 unhealthy-threshold ledger, and feeds
`ShutdownResponse.ExitedCleanly`. After the change a socket runtime's close always reports `released`.
`MCPRuntime.Close` still returns an error on a non-zero exit, so the `leaked` outcome stays reachable.

**D8. `Close`'s occupancy-zero branch is left untouched.** The connection close, the child reap, and the
grace window are current behavior that this proposal does not change and that proposal 0079 owns.

## 3. Design overview

`SocketRuntimeProcess` holds resources at two lifetimes, and each gets its own release.

| Lifetime | Resources | Created by | Released by |
|:--|:--|:--|:--|
| Pod, meaning the adapter process §5.2 keeps alive across the recycle boundary | `listener` | `NewSocketRuntimeProcess` at adapter boot | `CloseListener` at adapter-process exit |
| Session cohort, meaning one runtime connection serving one or more slots | `conn`, `connected`, `cmd`, `subscribers`, the fan-out reader, the `active` entries | The first `Start` that finds no live connection | `Close` of the last active session |

The invariant is that a teardown releases resources at its own lifetime and never above it.
`Close(ctx, sessionID)` enters at the session lifetime and escalates to the cohort when the release
empties the active set. The listener sits one level above the highest point `Close` can reach, so `Close`
must not touch it. The escalation to the cohort is left as it is (D8).

```
adapter process starts
  |
  +-- NewSocketRuntimeProcess: bind the pod's CH-MSGSOCK address
  |
  |     session A: Start -> accept -> connection up
  |     session A: Close -> occupancy zero -> connection closed, child reaped
  |                         listener untouched
  |
  |     session B: Start -> accept on the SAME address -> connection up
  |     session B: Close -> occupancy zero -> connection closed
  |
  +-- SIGTERM: demote the SDK, close CH-RUNTIMEOPS, GracefulStop, CloseListener
adapter process exits
```

## 4. Detailed design

### 4.1 `Close` (CODE-1)

`Close` keeps its signature, its `!p.connected` early return (`pkg/adapter/socketruntime.go:436-440`), its
sibling-active early return (`:441-446`), the connection close, and the graced child reap. Its final
statement becomes `return nil`. The function is still idempotent, which is the property 0073's merged
`Shutdown` handler relies on.

The comments that credit the listener's teardown to `Close` are corrected in the same commit, as §7.1
stages.

Each rewritten comment cites the specification by heading or `§X.Y`, per the naming law's N8. The comments
this change does not rewrite keep their present text and are left to the line-citation migration.

### 4.2 `CloseListener` (CODE-2)

```go
// CloseListener closes the pod-scoped listener (see the type comment). It
// leaves the shared connection and the active set alone, and it is safe to
// call more than once.
func (p *SocketRuntimeProcess) CloseListener() error
```

Idempotence is held by a `listenerClosed bool` under the existing `p.mu`, the form `RuntimeOps.Close`
(`pkg/adapter/runtimeops.go:602-621`) already uses in this package, so a second call returns nil rather
than the error a second `net.Listener.Close` produces. That matters because test cleanups call it after a
case has already exercised it.

An accept goroutine blocked in `p.listener.Accept()` (`:286`) when `CloseListener` runs returns
`net.ErrClosed` and sends its result into the buffered channel at `:284`, so the goroutine exits whether
or not a caller is still selecting on it. That is the behavior the listener close produces today; this
change moves when it happens rather than introducing it.

### 4.3 The call site (CODE-2)

In the socket-transport arm of `main`'s transport switch (`case *runtimeSocket != "":` in
`cmd/lenny-adapter/main.go`), immediately after `adapterSrv.Runtime = sp`, add:

```go
		// Release the pod-scoped listener (see adapter.SocketRuntimeProcess) at
		// process exit. main returns only after srv.Serve returns, so this runs once
		// the gRPC server has stopped serving.
		defer func() {
			if err := sp.CloseListener(); err != nil {
				log.Printf("lenny-adapter: close runtime socket listener: %v", err)
			}
		}()
```

The subprocess and embedded transports never enter that arm and are unaffected.

### 4.4 What the change does not make work

On the sidecar deployment model nothing offers a second runtime connection once the first session's
`Close` has closed the first: the runtime container runs under `RestartPolicy: Never`, and the adapter
does not spawn in that model (`SocketRuntimeProcess` type comment). A second session's `Start` on a
recycling sidecar pod therefore still fails, at the accept timeout rather than at once (§5, first row).

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| A recycling sidecar pod is asked for a second session | The address is live and nothing dials it, so each `Start` on the pod waits out `AcceptTimeout` (30s by default) and fails with `adapter: runtime did not connect within 30s`. On an exclusive pool the gateway then drains the pod (`failPhase` in `pkg/gateway/podlifecycle/podsession/reclaim.go`) and the client's call returns 503 with Retry-After. On a concurrent pool `accountSlotFailure` counts the failure and `applySlotRetryPolicy` (`pkg/gateway/sessionserver/slot_bind.go`) retries once on a fresh slot, which can land on the same pod and wait the bound again. | Accepted only under the landing order in summary **Decisions.** |
| The last session ends through `Interrupt` rather than `Close` | The connection closes, a spawned child is killed on a hard interrupt, and the listener stays bound. Unchanged behavior, which after this change agrees with `Close`. | §5.2 slot independence; pinned by TEST-2. |
| A last-slot `Interrupt` is not followed by a `Close` | `Interrupt` closes the shared connection and leaves `p.connected` true and `p.conn` set (`pkg/adapter/socketruntime.go:398-417`), so a `Start` in that window returns nil without accepting and the session's writes fail on a closed socket. A `Start` after the interrupted session's `Close` accepts normally; a session whose `Start` landed in the window stays on the dead connection until its own `Close`, because the interrupted session's `Close` is then not the last release. Pre-existing, neither created nor cured here, and reachable more often once the object outlives a session. | Not stated in spec or docs. The summary's open decision. |
| A `Start` whose accept timed out leaves its accept goroutine parked in `Accept` | The parked goroutine can take a later dial into a buffered channel nobody reads, so a runtime that dials after a timed-out `Start` may be swallowed and the next `Start` times out too. The listener's survival widens the window from one pod boot's worth of starts to the pod's whole life. | Not stated in spec or docs. Recorded here and declined in §8. |
| A `Start` arrives concurrently with the last session's `Close` | The arriving session either reuses the connection, when it takes `p.mu` before the close clears `p.connected`, or waits out its own accept against a live listener. Neither ordering produces a closed-listener error, which the previous behavior did produce, and the departing connection's reader leaves the arriving session's subscribers alone (CODE-3). | New behavior; pinned by TEST-6 and TEST-9. |
| A runtime dials between two sessions, while no `Start` is in flight | The connection waits in the listener's backlog and the next `Start` accepts it. Before this change the dial was refused. If the dialer has since exited, the fan-out reader hits EOF at once, every subscriber's channel closes, and that session fails and is retried. | Accepted: queueing is the intended behavior of a bound listening socket. §28.5.3 states a manifest-nonce handshake as the first message on this channel, which would reject a stale peer; that handshake is unimplemented on this transport and is out of scope here. |
| The listener stays bound between sessions, so a process in the pod's network namespace can complete a connection at any time | Under §4.7.10 the pod's network namespace holds the adapter and the runtime container alone, and `shareProcessNamespace` is false. §4.7.11 item 1 requires the `SO_PEERCRED` peer-UID check and the manifest-nonce handshake on this socket, and `SocketRuntimeProcess.accept` implements neither. The gap is pre-existing, and the listener's survival extends that unauthenticated accept from the first cohort to every later one. | §4.7.10 (Deployment Model); §4.7.11 (Adapter-Agent Security Boundary). |
| `Close`'s error becomes unreachable on the socket transport | A socket-runtime session never reports the `leaked` per-session cleanup outcome from the close itself. | Accepted, per D7. §5.2's per-session cleanup outcome and §4.7's `ReportSessionScrub`; pinned by TEST-3. |
| `Close` for a session while a sibling is active, or a repeated `Close` for the same session | Unchanged: the connection, the child, and now the listener all survive, and the call returns nil. | The `Close` doc comment's idempotence clause; §5.2 slot independence. |
| `Start` after `CloseListener` | Fails in accept with the wrapped closed-listener error. The adapter is exiting on every path that reaches it. | New behavior; pinned by TEST-4. |
| The adapter is SIGKILLed or OOM-killed rather than signalled | `CloseListener` does not run. The kernel reclaims an abstract address with the process; a filesystem-path socket leaves its file behind and a restarted adapter in place would fail to bind. Accepted, and unchanged: no path closed the listener on that route before either. The specified in-pod transport is the abstract address. | §4.7.10 fixes the abstract socket as the deployment default. |
| `CloseListener` returns an error | It is logged and the process continues to exit. Nothing retries it. | Accepted; the process is exiting and the kernel releases the socket. |

## 6. Observability surface

No metric, alert, event, or log line is added, and none is removed. The failures this path produces are
already carried: a `Start` that cannot accept fails `StartSession`, and the gateway handles it as §5's
first row states. `CloseListener`
logs a non-nil close error through the `cmd/lenny-adapter` logger.

One operator-visible string changes. The second session on a recycling sidecar pod fails with
`adapter: runtime did not connect within 30s` instead of
`adapter: accept runtime connection: ... use of closed network connection`. The new text names the
condition that actually holds after this change, which is the absence of a runtime rather than the
absence of an address, and it is the text a 0079 diagnosis starts from. No runbook quotes either string.

## 7. Proposed changes

### 7.1 `pkg/adapter/socketruntime.go` and the comments that credit the listener to `Close` (CODE-1)

Replace the final statement of `Close`:

```go
	return p.listener.Close()
```

with:

```go
	// The listener is pod-scoped and stays bound; see the type comment.
	return nil
```

In the `SocketRuntimeProcess` type doc comment, replace

```
// The shared connection, the spawned child, and the listener
// are torn down only when the last active session is released,
```

with

```
// The shared connection and the spawned child are torn down only when the
// last active session is released,
```

and append to that paragraph:

```
// The listener is pod-scoped: it is bound once at construction, before the
// pod is claimable, every session the pod serves is accepted on it, and
// CloseListener closes it when the adapter process exits. No session
// teardown closes it, because nothing rebinds the address.
// spec: §4.7.9, §5.2, §28.5.3.
```

In the `Close` doc comment, replace

```
// connection (the §15.4 clean-exit signal), wait the resolved grace window
// for a spawned child to exit, and close the listener.
```

with

```
// connection (the §15.4 clean-exit signal) and wait the resolved grace window
// for a spawned child to exit. The listener is pod-scoped and stays bound;
// see the type comment.
```

Outside `socketruntime.go`, the comments that credit the listener's teardown to `Close` change as follows.

- `pkg/adapter/session.go`, the `Server.Shutdown` doc comment: "the shared connection, the child and the
  never-rebound listener" becomes "the shared connection and the child".
- `pkg/adapter/slotsession_test.go`, the comment of
  `TestShutdownOfAnUnstartedEntryLeavesTheSocketRuntimeIntact_spec_4_7_1`: ", the child and the listener"
  becomes " and the child", and its last sentence ends "so the shared connection stays up". Its
  `connected` check is the assertion that discriminates, and it is unchanged.
- `pkg/adapter/socketruntime_test.go`: delete
  `TestSocketRuntimeProcessSurvivesTheReclaimOfAnUnstartedSlot_spec_4_7_1`. Once `Close` leaves the
  listener bound its closing dial cannot fail, and the `Interrupt` it drives has already closed the shared
  connection, so no black-box assertion is left to discriminate.

### 7.2 `pkg/adapter/socketruntime.go` (CODE-2)

Add the guard field to the struct, beside `active` (`:76-80`), documented as guarded by `mu`:

```go
	// listenerClosed records that CloseListener has released the pod-scoped
	// listener, so a second call is a no-op. Guarded by mu.
	listenerClosed bool
```

Add after `Close`, with the doc comment given in §4.2:

```go
func (p *SocketRuntimeProcess) CloseListener() error {
	p.mu.Lock()
	if p.listenerClosed {
		p.mu.Unlock()
		return nil
	}
	p.listenerClosed = true
	p.mu.Unlock()
	return p.listener.Close()
}
```

### 7.3 `cmd/lenny-adapter/main.go` (CODE-2)

Add the deferred block given in §4.3, in the arm and at the position §4.3 names.

### 7.4 `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` (TEST-7)

Replace the `rt.AcceptTimeout` assignment in `socketDrainPod`:

```go
	// A start that has to accept a connection nobody will make is the
	// failure the first leg asserts; bound it well under the case timeout.
	rt.AcceptTimeout = 5 * time.Second
```

with a short window and the reason:

```go
	// The listener is pod-scoped and survives a session teardown, so a start
	// that nobody re-dials now fails by accept timeout rather than on a
	// closed listener. The legs assert only that the start fails, and the
	// unsequenced loop pays this window once per attempt, so keep it short.
	rt.AcceptTimeout = 250 * time.Millisecond
```

**IMPLEMENTOR'S CHOICE:** the exact window. Any value must be long enough that the first leg's already
queued dial is accepted reliably under `-race`, and short enough that `raceAttempts` multiplied by it
stays well inside the case's budget.

Tighten the `StartSession` assertion in the `teardown_lands_before_the_incoming_start` subtest from "the
start returned an error" to "the start returned the accept-timeout error", so the leg cannot pass because
the listener was closed. Its comment, which already asserts that the pod's listener stays bound, becomes accurate as written and
needs no edit.

### 7.5 Existing test cleanups (TEST-1, TEST-5, TEST-7)

`Close` no longer releases the address (D2). Every test that constructs a `SocketRuntimeProcess`,
including the new cases in §9, registers `t.Cleanup(func() { _ = sp.CloseListener() })` immediately after
the constructor's error check and keeps its existing `Close` and `Interrupt` calls. Registered first, the
cleanup runs last, after every deferred `Close` and every later cleanup that reaps a spawned child. The
existing construction sites are:

- `pkg/adapter/socketruntime_test.go`: `TestSocketRuntimeProcessBridgesJSONLFrames`,
  `TestSocketRuntimeProcessStartTimesOutWithoutAConnection`,
  `TestSocketRuntimeProcessOutputClosesOnRuntimeDisconnect`,
  `TestSocketRuntimeProcessStartIsIdempotentAcrossSlots_spec_5_2`,
  `TestSocketRuntimeProcessFansOutToConcurrentSubscribers_spec_15_4`,
  `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`, and
  `TestSocketRuntimeProcessInterruptScopedToSlot_spec_5_2`.
- `pkg/adapter/socketruntime_e2e_test.go`: `TestSidecarSocketTransportDrivesTheEchoRuntime` and
  `TestSpawnedRuntimeIsSignalledOnClose`.
- `pkg/adapter/slotsession_test.go`: `TestShutdownOfAnUnstartedEntryLeavesTheSocketRuntimeIntact_spec_4_7_1`,
  where the cleanup replaces the deferred `sp.listener.Close()`.
- `tests/tier4_integration/concurrent_workspace_test.go`: `TestConcurrentWorkspacePerSlotExecution_spec_5_2`
  and `newAbandonFixture`; and `tests/tier4_integration/concurrent_delegation_proxy_test.go`:
  `TestConcurrentSlotsDelegationAndProxyIsolation_spec_5_2`.
- `tests/tier7a_load_local/shutdown_drain_gate_race_test.go`: `socketDrainPod`.

### 7.6 `docs/runtime-author-guide/lifecycle.md` (DOCS-1)

Append to the paragraph at `:330`, which already tells authors that "On a recycling pod the runtime exits
at each session end":

```
The adapter's socket address is bound for the pod's lifetime and does not change between sessions.
```

### 7.7 `pkg/adapter/socketruntime.go` (CODE-3)

Once the listener survives, a `Start` can accept a second connection while the departing connection's
`fanOut` reader is still running, and that reader would broadcast its remaining frames into the arriving
connection's subscribers and close them on EOF. Each accepted connection gets its own subscriber set:

- `Start` builds the new connection's set in a local `subs`, assigns `p.subscribers = subs` under `p.mu`
  as today, and launches `go p.fanOut(scanner, subs)`.
- `fanOut(scanner, subs)` defers `p.closeSubscribers(subs)` and calls `p.broadcast(subs, line)`.
- `broadcast` and `closeSubscribers` take the set as a parameter and iterate over it, or delete from it,
  in place of `p.subscribers`, still under `p.mu`.
- `Output` and `unsubscribe` are unchanged. A subscriber whose entry sits in an earlier connection's set
  is still closed by `unsubscribe`, and that connection's reader removes the entry when it exits;
  `subscriber.close` is idempotent.

The `fanOut` and `closeSubscribers` doc comments state that the reader acts on its own connection's set,
and cite `spec: §5.2, §28.5.3`.

## 8. Non-goals

- **The runtime process's lifetime across sessions.** Proposal 0079 owns it (summary **Impacts on other
  proposals**).
- **Restructuring the accept path.** Several readings of this defect treat a single-flight accept, a
  long-lived acceptor goroutine, or a late-connection reaper as a precondition of the listener fix,
  because a `Start` that times out leaves a goroutine parked in `Accept` whose eventual connection is
  neither used nor closed. The hazard is real and is recorded in §5, and it is declined here for two
  reasons. It is strictly pre-existing: `Close` returns at `pkg/adapter/socketruntime.go:436-440` when the
  process never connected, so the failed-start path never reached the listener close even today, and the
  reaping that a later teardown performed was incidental. And the property this proposal establishes, that
  a later `Start` accepts, holds unconditionally when no earlier `Start` timed out, which is every
  sequential-session path. Restructuring `accept` changes connection-adoption semantics and needs its own
  race coverage, which is a second mechanism rather than this scope error.
- **Clearing `p.cmd` after the grace-window reap.** The reaped handle survives on the struct, and a second
  `Wait` on it would be wrong. It is unreachable: `Close` returns early while `p.connected` is false, and
  the only way back to a connected state is a `Start`, whose `spawn` overwrites `p.cmd`
  (`pkg/adapter/socketruntime.go:311-313`) before the accept. Clearing it would be a second change with no
  failure behind it.
- **Clearing `connected` and `conn` on a terminal `Interrupt`.** The window it leaves is recorded in §5 and
  is the summary's open decision.
- **Adding a typed sentinel for a closed listener, adapter metric series, or new log lines.** An
  `outcome`-labelled accept counter and a bound gauge would make the condition alertable, and they would
  need §16.1 catalog rows and `docs/reference/metrics.md` rows, which is a specification edit this
  code-only remediation is not the place for. The existing slot-failure signals carry the condition, and
  the error text an operator meets is stated in §6.
- **Adding `CloseListener` to `RuntimeProcess`, or naming it `Shutdown`.** Rejected in D3 and D4.
- **Rebinding the listener at session start.** Rejected in D6.
- **Correcting `pkg/adapter/scrub/scrub.go`'s `Ops` claim that the whole-pod scrub terminates the
  runtime's SDK process, and reconciling §4.7.9 step 7 with §4.7.10.** The claim is false on the sidecar
  transport because §13.1 forbids `shareProcessNamespace`, and the correct sentence depends on proposal
  0079.
- **Giving the tier-10 recycle-scrub conformance property a `RuntimeProcess` that can fail it, and
  un-skipping `TestTaskModeRecycleScrubsWorkspaceBetweenSessions`.** Both depend on proposal 0079 and stay
  with it. The tier-5 skip entry in `tests/registers/skip-reasons.yaml` stays as
  written.
- **Renaming the file or the tests to carry the `CH-MSGSOCK` identifier under the naming law's N4.**
  `runtimeops.go` and `adapterevents.go` already carry their channels' stems; renaming `socketruntime.go`
  and its tests is a file move outside this fix. The identifier appears in the new doc comments.
- **Converting the remaining line citations in `pkg/adapter/socketruntime.go`.** Only the comments this
  change rewrites are converted to heading citations; the rest belong to the line-citation migration.

## 9. Testing

Tiers reached: 0, 1, 4, 7a, and 11. Tier 3 is not reached, because no proto message, JSONL frame, HTTP
request, or CRD schema changes. Tier 5 is not reached, because the Kind recycle case depends on proposal
0079. Tier 10 is not reached, for the reason in §8. Every case carries a `// spec:` annotation
naming §5.2 and §15.4.3, and every tier-2-and-higher case carries a `// diagnosis:` comment.

**TEST-1, tier 1, `pkg/adapter/socketruntime_test.go`.**
`TestSocketRuntimeListenerOutlivesTheLastSessionAndAcceptsTheNext_spec_15_4_3`. Bind, dial,
`Start("sess-a")`, round-trip a frame, `Close("sess-a")`, then dial the same `SocketPath()` again,
`Start("sess-b")`, and round-trip a frame over the second connection. Against the unpatched tree the
second dial is refused and the second `Start` fails with `use of closed network connection`, which the
case names in its failure message so a regression is diagnosable from the text alone.

**TEST-2, tier 1.** `TestSocketRuntimeInterruptLeavesTheListenerBound_spec_5_2`. `Start("sess-a")`, end it
with a clean last-slot `Interrupt`, and assert that a fresh dial to `SocketPath()` connects. It passes today
and pins the parity §1.3 identifies, so a later change that moves the listener close into `Interrupt` fails
here.

**TEST-3, tier 1.** `TestSocketRuntimeCloseReportsNoListenerErrorAcrossGenerations_spec_5_2`. Drive two
generations and assert that both last-session `Close` calls return nil, that a repeated `Close` for an
already-released session returns nil, and that a `Close` for a session on a never-connected process
returns nil and leaves the address dialable. This is the guard on D7: after the fix no teardown can
produce the `leaked` scrub outcome through a listener error, and the `!p.connected` early return still
holds.

**TEST-4, tier 1.** `TestSocketRuntimeCloseListenerUnbindsTheAddress_spec_4_7`. After a `Start` and a
last-session `Close`, a fresh dial to `SocketPath()` succeeds; after `CloseListener` the same dial fails,
a following `Start` returns the wrapped closed-listener error rather than waiting out the accept timeout,
and a second `CloseListener` returns nil. On the filesystem-path form of the address, assert the socket
file is gone after `CloseListener` and present before it, which is the regression guard for deleting the
close without adding the method. Assert `SocketPath()` is unchanged before the first `Start`, after the
last-session `Close`, and after the second generation's `Start`, which is the guard against a future
rebind (D6).

**TEST-5, tier 4, `tests/tier4_integration/`.**
`TestAdapterServesTwoSequentialSessionsOverOneRuntimeSocket_spec_15_4_3`. Drive a real `adapter.Server`
whose transport is a `SocketRuntimeProcess` with `SpawnPath` set to the built `cmd/runtimes/echo` binary,
through `StartSession`, a message round trip, and `Shutdown` for one session, then the same for a second
session against the same adapter process. Assert the second session's response arrives. The fixtures in
`TestConcurrentWorkspacePerSlotExecution_spec_5_2` and `TestConcurrentSlotsDelegationAndProxyIsolation_spec_5_2`
already bind that transport. The second session's runtime comes from `SpawnPath`, a test-only spawn hook, so the
case asserts the adapter half alone. `// diagnosis:` a failure means the pod lost its runtime ingress at
the first session's end, so no recycling pod can serve a second session.

**TEST-6, tier 7a, `tests/tier7a_load_local/`.**
`TestLastSessionCloseRacingAnArrivingStartKeepsTheListener_spec_5_2`. From a common rendezvous, one
goroutine calls `Close` for the last active session while another dials and calls `Start` for a new
session, across `raceAttempts` iterations under `-race`. Assert that no iteration returns a
closed-listener error, that each iteration's `Start` either reuses the live connection or accepts its own,
and that the address is dialable at the end. Run through
`lenny-test stress --test TestLastSessionCloseRacingAnArrivingStartKeepsTheListener_spec_5_2 --runs 50`,
because the window it pins is a few microseconds wide. `// diagnosis:` a failure means the session
boundary can still unbind the pod's address under a concurrent start.

**TEST-7, tier 7a, `tests/tier7a_load_local/shutdown_drain_gate_race_test.go`.** The fixture's
accept-timeout bound and the tightened sequenced-leg assertion staged in §7.4. The existing assertions are
otherwise unchanged; the edit keeps the case's wall clock bounded now that the failure is a timeout rather
than an immediate error, and turns a leg that passed for the wrong reason into a gate on this change.

**TEST-8, tier 4, `tests/tier4_integration/`.** `TestAdapterProcessUnlinksItsRuntimeSocketOnSIGTERM_spec_5_2`.
Build `cmd/lenny-adapter` and start it with `--runtime-socket` set to a path under a temp directory,
`--addr` set to a loopback port the test reserves, and the other flags it needs to start outside a pod.
Wait until a `grpc.health.v1` check on that port returns `SERVING` (the socket file appears before `main`
installs its SIGTERM handler, so it is not a readiness signal), send SIGTERM, wait for the process to
exit, and assert a clean exit and that the socket file is gone. It is the only case that fails when the §4.3 call is dropped. `// diagnosis:` a failure means the
adapter exits without releasing its runtime socket, so a filesystem-path socket blocks a restarted
adapter from binding.

**TEST-9, tier 1, `pkg/adapter/socketruntime_test.go`.**
`TestSocketRuntimeDepartingReaderLeavesTheNextConnectionsSubscribers_spec_5_2`. Dial, `Start("sess-a")`,
and call `Output` for sess-a under a cancellable context without reading its channel. The first runtime
writes more frames than the subscriber's buffered intake holds, so `fanOut` parks. Then `Close("sess-a")`,
dial again, `Start("sess-b")`, and call `Output` for sess-b. Cancel sess-a's context to release the parked
reader, and assert that sess-b's channel yields no value and stays open for a bounded window (for example
500ms). This is the assertion that discriminates: against a shared set the released reader delivers an
earlier connection's frame or closes the channel inside the window. Then have the second runtime write one
sess-b frame and assert that it is the first value received on sess-b's channel, with `ok` true.

**Tier 11.** The existing documentation pass covers DOCS-1; no new case.

Existing cases whose assertions stay unchanged, with only the §7.5 cleanup added:
`TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` and
`TestSocketRuntimeProcessInterruptScopedToSlot_spec_5_2` pin the sibling-active behavior, and
`TestSocketRuntimeProcessStartTimesOutWithoutAConnection` pins the first-generation accept timeout. Their
assertions passing unchanged is the evidence that the occupancy-zero semantics did not move.

Every new case lands in an existing package. Confirm with `lenny-test validate-maps` at tier 0 whether
`tests/spec-map.json` needs an entry for the new tier-4 and tier-7a files, and add it if the gate asks.
Run `lenny-test --changed --max-tier 4` for S1 through S4, `--tier 7a` for S5, and `--tier 11` for S6.
Coverage: the change deletes one statement and adds `CloseListener` and its guard, so run
`lenny-test coverage --diff <base-ref>` and confirm the new lines are reached by TEST-4.

## 10. Findings closed on application

- **BUILD-GAPS F-5.2.33, part (a)**: the pod-scoped listener was torn down on a per-session teardown. The
  finding stays OPEN for part (b), which is proposal 0079's. Annotate the finding's evidence paragraph to
  record that its listener clause is resolved, and leave the checkbox unticked.
- **Amendment to proposal 0073.** SCHEMA-1's statement that `Runtime.Close` is unchanged in signature and
  behavior, and §9's record of the unbound listener as an accepted limit, remain
  accurate statements about 0073's own diff. On application the behavior they describe no longer holds in
  the tree: the two sentences asserting that a later `Runtime.Start` fails in accept because the listener
  was closed are superseded, and the start instead fails on the accept timeout until 0079 lands. No file
  under `proposals/` is edited. A `proposal-conformance` pass on 0073 after this lands will report the
  difference, and this paragraph is the answer to it.

## 12. Files touched on application

| File | Deliverable | Change |
|:--|:--|:--|
| `pkg/adapter/socketruntime.go` | CODE-1, CODE-2, CODE-3 | `Close`'s final statement, the type and `Close` doc comments, the `listenerClosed` field, `CloseListener`, and the per-connection subscriber set passed to `fanOut`, `broadcast`, and `closeSubscribers`. |
| `pkg/adapter/session.go` | CODE-1 | The `Server.Shutdown` doc comment (§7.1). |
| `pkg/adapter/slotsession_test.go` | CODE-1, TEST-1 | One test comment (§7.1) and §7.5's cleanup conversion. |
| `cmd/lenny-adapter/main.go` | CODE-2 | The deferred `CloseListener` call in the socket-transport arm. |
| `pkg/adapter/socketruntime_test.go` | CODE-1, TEST-1 to TEST-4, TEST-9 | The new tier-1 cases, the cleanup conversion on the existing cases, and CODE-1's deletion of the unstarted-slot reclaim case (§7.1). |
| `pkg/adapter/socketruntime_e2e_test.go` | TEST-1 | The cleanup conversion. |
| `tests/tier4_integration/` (new file) | TEST-5, TEST-8 | The two-sequential-sessions case and the SIGTERM socket-unlink case. |
| `tests/tier4_integration/concurrent_workspace_test.go` | TEST-5 | §7.5's cleanup conversion. |
| `tests/tier4_integration/concurrent_delegation_proxy_test.go` | TEST-5 | §7.5's cleanup conversion. |
| `tests/tier7a_load_local/` (existing package) | TEST-6 | The `Close`-versus-`Start` race case. |
| `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` | TEST-7 | The fixture's accept-timeout bound, the tightened sequenced-leg assertion, and §7.5's cleanup conversion in `socketDrainPod`. |
| `tests/spec-map.json` | TEST-5, TEST-6, TEST-8 | The new cases' spec-section mappings, if `validate-maps` requires them. |
| `docs/runtime-author-guide/lifecycle.md` | DOCS-1 | One sentence appended to the paragraph at `:330`. |
| `BUILD-GAPS.md` | — | F-5.2.33's evidence annotated per §10; the finding stays OPEN for part (b). |

No file under `spec/`, `schemas/`, or `charts/` is touched.
