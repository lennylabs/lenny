# Non-spec changes: Keep the pod's runtime listener across a session teardown

## 2. Decisions

**D1. The listener's lifetime is the adapter process's.** §1.2 establishes this from the specification.
The code states the opposite in three doc comments and implements the opposite in one statement, and both
are corrected.

**D2. The teardown moves rather than disappearing.** Leaving the listener unclosed would also correct the
scope error, because process exit releases the descriptor and a Linux abstract address vanishes with its
process. It would leave two consumers worse off. Go's `net` package unlinks a filesystem-path socket only
on listener close, and the `--runtime-socket <path>` developer loop and the tier-7a fixture
(`tests/tier7a_load_local/shutdown_drain_gate_race_test.go:415-416`) both use that form, so a stale inode
would survive an adapter restart in place. The in-package tests bind a per-case address in one test binary
and would hold every one of them for the binary's life, which is invisible at `-count=1` and surfaces
under `lenny-test stress`. An explicit method costs one function and gives every caller a correct
cleanup call. The adapter also has a real graceful path to call it from: the signal goroutine at
`cmd/lenny-adapter/main.go:412-427` already runs `ShutdownDemoteSDK`, `lifecycle.Close()`, and
`srv.GracefulStop()`.

**D3. The method is named `CloseListener`.** `Close` is taken by the `RuntimeProcess` contract with a
different arity and a different meaning, and two methods named `Close` on one type is the ambiguity this
proposal exists to remove. `Shutdown` collides with the adapter's `Shutdown` RPC
(`pkg/adapter/session.go:223`), which is itself a per-session teardown, so it reproduces the confusion in
a second place. `CloseListener` names the resource it closes.

**D4. `CloseListener` is on the concrete type and not on `RuntimeProcess`.** Only the socket transport
holds a pod-scoped listener. Widening the interface would drag a no-op method through the embedded,
subprocess, MCP, and SDK-warm implementations and through the test doubles in every package that carries
one, for a single caller. Proposal 0073's SCHEMA-1 declined an interface widening on the same reasoning,
and keeping the interface untouched keeps this change clear of that freeze. `cmd/lenny-adapter` holds the
concrete value at the point it assigns `adapterSrv.Runtime` (`cmd/lenny-adapter/main.go:354-358`).

**D5. `CloseListener` runs after `srv.GracefulStop()` returns.** `GracefulStop` blocks until in-flight
RPCs complete, and an in-flight `StartSession` may be parked in `accept`. Closing the listener under it
would fail that RPC with the exact error this proposal removes, on the one path where the failure is not
a defect. `lifecycle.Close()` sits before `GracefulStop` (`cmd/lenny-adapter/main.go:424`) deliberately,
to unblock the runtime operations read loop, and it is not a precedent for the ordering here.

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
unbinding a listener that should not have been unbound. `pkg/adapter/session.go:260` feeds that value into
`reportSessionScrub` (`:275`), where a non-nil result becomes the `leaked` outcome that drives the §5.2
unhealthy-threshold ledger, and into `ShutdownResponse.ExitedCleanly` (`:287`). After the change a socket
runtime's close always reports `released`. The grace-deadline overrun and the other `RuntimeProcess`
implementations keep their own error paths, so the `leaked` outcome stays reachable.

**D8. Nothing about the runtime's death changes.** The occupancy-zero gate, the connection close, the
child reap, and the grace window are untouched. §15.4.3 has the platform start a fresh runtime process for
each session, §6.1's `recycle.enabled: true` row has the whole-pod scrub terminate the SDK process along
with all other session processes, and `docs/runtime-author-guide/lifecycle.md:330` states the same to
authors.

**D9. This proposal applies after proposal 0073 completes.** 0073's SCHEMA-1 states that `Runtime.Close`
is unchanged by that deliverable in signature and in behavior, and 0073 §9 records the unbound listener
as an accepted pre-existing limit (`:1997`, `:6638`). Its remaining steps are S17, which touches the
client SDKs, the tool-approval detail, the SSE payload, and the `/start` 422 body, and S18, which touches
the channel register. Neither names a file this proposal edits, so the ordering is imposed by 0073's own
text and by the `proposal-conformance` pass that reads it against the tree rather than by a merge
conflict.

## 3. Design overview

`SocketRuntimeProcess` holds resources at two lifetimes, and each gets its own release.

| Lifetime | Resources | Created by | Released by |
|:--|:--|:--|:--|
| Pod, meaning the adapter process §5.2 keeps alive across the recycle boundary | `listener` | `NewSocketRuntimeProcess` at adapter boot | `CloseListener` at adapter-process exit |
| Session cohort, meaning one runtime connection serving one or more slots | `conn`, `connected`, `cmd`, `subscribers`, the fan-out reader, the `active` entries | The first `Start` that finds no live connection | `Close` or a terminal `Interrupt` of the last active session |

The invariant is that a teardown releases resources at its own lifetime and never above it.
`Close(ctx, sessionID)` enters at the session lifetime and escalates to the cohort when the release
empties the active set. The listener sits one level above the highest point `Close` can reach, so `Close`
must not touch it. The escalation to the cohort stays, because at occupancy zero the pod holds no session
and the runtime that served them has nothing left to serve, which is the §15.4.3 fresh-process-per-session
behavior.

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

Three doc comments state the behavior being removed and are corrected in the same commit.

- The type comment (`:52-55`) groups "The shared connection, the spawned child, and the listener" under
  one release condition. The listener leaves that list and gains its own sentence naming its pod
  lifetime and `CloseListener`.
- The `Close` comment (`:419-434`) states that the last release closes the shared connection, waits the
  grace window, "and close[s] the listener". The final clause is replaced by the reason the listener is
  left alone.
- The constructor comment (`:151-155`) already says the socket "is bound immediately so it is ready
  before the §4.7 startup sequence spawns or schedules the runtime", which is correct. It gains the
  second half of the invariant.

Each rewritten comment cites the specification by heading or `§X.Y`, per the naming law's N8. The comments
this change does not rewrite keep their present text and are left to the line-citation migration.

### 4.2 `CloseListener` (CODE-2)

```go
// CloseListener releases the pod-scoped CH-MSGSOCK address. The listener is
// bound once, in NewSocketRuntimeProcess, before any session exists, and
// every session the pod serves is accepted on it, so its teardown belongs to
// the adapter process's exit rather than to a session's Close. It leaves the
// shared connection and the active set alone; a caller that wants those
// closed calls Close for each active session first. It is safe to call more
// than once.
// spec: §5.2 (the adapter keeps the pod process alive across the recycle
// boundary and reuses it for the next session), §4.7.10 (sidecar deployment
// model), §28.5.3 (the runtime is the dialling participant).
func (p *SocketRuntimeProcess) CloseListener() error
```

Idempotence is held by a `listenerClosed bool` under the existing `p.mu`, the form `RuntimeOps.Close`
(`pkg/adapter/runtimeops.go:602-621`) already uses in this package, so a second call returns nil rather
than the error a second `net.Listener.Close` produces. That matters because the signal path can overlap a
session teardown and because test cleanups call it after a case has already exercised it.

An accept goroutine blocked in `p.listener.Accept()` (`:286`) when `CloseListener` runs returns
`net.ErrClosed` and sends its result into the buffered channel at `:284`, so the goroutine exits whether
or not a caller is still selecting on it. That is the behavior the listener close produces today; this
change moves when it happens rather than introducing it.

### 4.3 The call site (CODE-2)

`sp` is declared inside the `case *runtimeSocket != "":` arm of the transport switch
(`cmd/lenny-adapter/main.go:349-360`), so the signal goroutine cannot see it. A
`var socketRuntime *adapter.SocketRuntimeProcess` is declared before the switch and assigned in that arm
beside `adapterSrv.Runtime = sp`, and the signal goroutine gains, after `srv.GracefulStop()` (`:426`):

```go
		if socketRuntime != nil {
			if err := socketRuntime.CloseListener(); err != nil {
				log.Printf("lenny-adapter: close runtime socket listener: %v", err)
			}
		}
```

`socketRuntime` is nil unless `--runtime-socket` selected the sidecar transport, so the subprocess and
embedded transports are unaffected.

### 4.4 What the change does not make work

With the listener bound across the boundary, an arriving session's `Start` accepts whatever connection is
offered. On the sidecar deployment model no component creates a successor runtime process to offer one:
the runtime container runs under `RestartPolicy: Never` so the kubelet does not re-run it, and the
adapter does not spawn it in that model, as the type's own doc comment states
(`pkg/adapter/socketruntime.go:33-35`). Session B's `Start` therefore still fails, at the accept timeout
rather than instantly, until proposal 0079 names the actor. What this change buys on its own is that the
address survives, which is the precondition any answer to 0079 needs, and that the developer-loop
`SpawnPath` path works across successive sessions, because its second `Start` spawns a second runtime
that dials the still-bound socket.

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| A recycling sidecar pod is asked for a second session | The address is live, nothing has dialled it, and `Start` fails after `AcceptTimeout` with `adapter: runtime did not connect within 30s`. The bind failure is counted toward the §5.2 unhealthy-slot threshold and the gateway retries onto another pod. | Accepted and deferred to proposal 0079. §15.4.3's "starts a fresh runtime process for each session" and §5.2's recycle lifecycle continue to describe the intended behavior; `docs/runtime-author-guide/lifecycle.md` carries the reader-facing statement. |
| The same case, timing | The failure takes up to the accept timeout (30s by default, `pkg/adapter/socketruntime.go:192-194`) rather than returning at once, so the gateway's retry onto another pod is delayed by that much. | Accepted. §11 carries the open decision on making the timeout operator-tunable. |
| The last session ends through `Interrupt` rather than `Close` | The connection closes, a spawned child is killed on a hard interrupt, and the listener stays bound. Unchanged behavior, which after this change agrees with `Close`. | §5.2 slot independence; pinned by TEST-2. |
| A last-slot `Interrupt` is not followed by a `Close` | `Interrupt` closes the shared connection and leaves `p.connected` true and `p.conn` set (`pkg/adapter/socketruntime.go:398-417`), so a `Start` in that window returns nil without accepting and the session's writes fail on a closed socket. The window closes when the gateway's `Shutdown` reaches `Close`, which re-enters the teardown branch and repairs the state. Pre-existing, neither created nor cured here, and reachable more often once the object outlives a session. | Not stated in spec or docs. Recorded here and routed to a new BUILD-GAPS finding in §11. |
| A `Start` whose accept timed out leaves its accept goroutine parked in `Accept` | The parked goroutine can take a later dial into a buffered channel nobody reads, so a runtime that dials after a timed-out `Start` may be swallowed and the next `Start` times out too. Pre-existing: `Close` returns at `:436-440` when the process never connected, so the failed-start path never reached the listener close even today. The listener's survival widens the window from one pod boot's worth of starts to the pod's whole life. | Not stated in spec or docs. Recorded here, declined in §8, and raised in §11. |
| A `Start` arrives concurrently with the last session's `Close` | The arriving session either reuses the connection, when it takes `p.mu` before the close clears `p.connected`, or waits out its own accept against a live listener. Neither ordering produces a closed-listener error, which the previous behavior did produce. | New behavior; pinned by TEST-6. |
| A runtime dials between two sessions, while no `Start` is in flight | The connection waits in the listener's backlog and the next `Start` accepts it. Before this change the dial was refused. If the dialer has since exited, the fan-out reader hits EOF at once, every subscriber's channel closes, and that session fails and is retried. | Accepted: queueing is the intended behavior of a bound listening socket. §28.5.3 states a manifest-nonce handshake as the first message on this channel, which would reject a stale peer; that handshake is unimplemented on this transport and is out of scope here. |
| The listener stays bound between sessions, so a process in the pod's network namespace can complete a connection at any time | Unchanged authorization surface. Under §4.7.10 the pod's network namespace holds the adapter and the runtime container alone, `shareProcessNamespace` is false, and the socket carried no peer authentication during a session either. A dialer identity check belongs with 0079, which decides who legitimately dials. | §4.7.10 (Deployment Model); §4.7.11 (Adapter-Agent Security Boundary). |
| `Close`'s error becomes unreachable on the socket transport | A socket-runtime session never reports the `leaked` per-session cleanup outcome from the close itself. The grace-deadline overrun and the other runtime implementations keep reporting it. | Accepted, per D7. §5.2's per-session cleanup outcome and §4.7's `ReportSessionScrub`; pinned by TEST-3. |
| `Close` for a session while a sibling is active, or a repeated `Close` for the same session | Unchanged: the connection, the child, and now the listener all survive, and the call returns nil. | The `Close` doc comment's idempotence clause; §5.2 slot independence. |
| `Start` after `CloseListener` | Fails in accept with the wrapped closed-listener error. The adapter is exiting on every path that reaches it. | New behavior; pinned by TEST-4. |
| The adapter is SIGKILLed or OOM-killed rather than signalled | `CloseListener` does not run. The kernel reclaims an abstract address with the process; a filesystem-path socket leaves its file behind and a restarted adapter in place would fail to bind. Accepted, and unchanged: no path closed the listener on that route before either. The specified in-pod transport is the abstract address. | §4.7.10 fixes the abstract socket as the deployment default. |
| `CloseListener` returns an error | It is logged and the process continues to exit. Nothing retries it. | Accepted; the process is exiting and the kernel releases the socket. |

## 6. Observability surface

No metric, alert, event, or log line is added, and none is removed. The failures this path produces are
already carried: a `Start` that cannot accept fails `StartSession`, and the gateway records the slot
failure and retries onto another pod (`pkg/gateway/sessionserver/start.go:2743-2771`). `CloseListener`
logs a non-nil close error through the `cmd/lenny-adapter` logger, matching the treatment of
`lifecycle.Close()` on the same path.

One operator-visible string changes. The second session on a recycling sidecar pod fails with
`adapter: runtime did not connect within 30s` instead of
`adapter: accept runtime connection: ... use of closed network connection`. The new text names the
condition that actually holds after this change, which is the absence of a runtime rather than the
absence of an address, and it is the text a 0079 diagnosis starts from. No runbook quotes either string.

## 7. Proposed changes

### 7.1 `pkg/adapter/socketruntime.go` (CODE-1)

Replace the final statement of `Close` (`:467`):

```go
	return p.listener.Close()
```

with:

```go
	// The listener is pod-scoped: NewSocketRuntimeProcess binds it once at
	// adapter boot, before the pod is claimable, and every session the pod
	// serves is accepted on it. Closing it here would destroy the address
	// the next session's runtime dials, and nothing rebinds it.
	// CloseListener releases it at adapter-process exit.
	// spec: §4.7.9 (the pod signals READY before any session is assigned),
	// §5.2 (the adapter keeps the pod process alive across the recycle
	// boundary and reuses it for the next session), §15.4.3 (a fresh runtime
	// process for each session), §28.5.3 (the runtime is the dialling
	// participant). F-5.2.33(a).
	return nil
```

In the type doc comment (`:52-55`), replace

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
// The listener is not session state. It is bound once at construction and
// released by CloseListener when the adapter process exits, so the pod's
// runtime address survives every session teardown and a runtime process
// started for a later session dials the same address.
// spec: §5.2, §4.7.10, §28.5.3.
```

In the `Close` doc comment (`:419-434`), replace the clause

```
does it close the shared socket
// connection (the §15.4 clean-exit signal), wait the resolved grace window
// for a spawned child to exit, and close the listener.
```

with

```
does it close the shared socket
// connection (the §15.4.3 clean-exit signal) and wait the resolved grace
// window for a spawned child to exit. The listener stays bound: it is
// pod-scoped, outlives every session the pod serves, and is released by
// CloseListener at adapter-process exit. spec: §5.2.
```

Extend the constructor doc comment (`:151-155`) with:

```
// The binding lasts for the life of the adapter process: every session the
// pod serves reaches the runtime over this one address, and CloseListener is
// its only closer. spec: §5.2, §4.7.10.
```

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

Declare, immediately before the transport switch at `:349`:

```go
	// The §4.7 sidecar listener is pod-scoped: it is bound once here and
	// released when this process exits, rather than at a session teardown.
	var socketRuntime *adapter.SocketRuntimeProcess
```

In the `case *runtimeSocket != "":` arm, assign `socketRuntime = sp` beside `adapterSrv.Runtime = sp`
(`:358`).

In the signal goroutine, after `srv.GracefulStop()` (`:426`), add the block given in §4.3, with the
comment stating why it follows `GracefulStop` (D5).

### 7.4 `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` (TEST-7)

Replace the accept-timeout assignment in `socketDrainPod` (`:419-421`):

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

Tighten the sequenced leg's assertion (`:355-361`) from "the start returned an error" to "the start
returned the accept-timeout error", so the leg cannot pass because the listener was closed. Its comment
at `:352-356`, which already asserts that the pod's listener stays bound, becomes accurate as written and
needs no edit.

### 7.5 Existing test cleanups (TEST-1, TEST-5)

`Close` no longer releases the address, so the calls that used it as a socket cleanup release nothing.

- `pkg/adapter/socketruntime_test.go`: the `defer sp.Close(context.Background(), "s1")` cleanups at
  `:62`, `:110`, `:129`, `:165`, and `:207` gain `t.Cleanup(func() { _ = sp.CloseListener() })`, and the
  two per-slot cases at `:258` and `:316` keep their session `Close` calls and gain the same cleanup.
- `pkg/adapter/socketruntime_e2e_test.go`: the same addition at `:58` and in
  `TestSpawnedRuntimeIsSignalledOnClose` (`:191`), whose grace-window assertion is unchanged.
- `tests/tier4_integration/concurrent_workspace_test.go:126` and
  `tests/tier4_integration/concurrent_delegation_proxy_test.go:168` call
  `rt.Close(context.Background(), "pod-teardown")` as a pod cleanup. That session identifier is not in the
  active set, so the call already returned at one of the early returns and released nothing. Both become
  `_ = rt.CloseListener()`.

On Linux `runtimeSocketAddr` derives an abstract address from the process id and the test name
(`pkg/adapter/socketruntime_test.go:24-38`), so a missed conversion is invisible in a normal run and
surfaces only under repetition. Apply the conversion to every construction site rather than to the cases
the new tests touch.

### 7.6 `docs/runtime-author-guide/lifecycle.md` (DOCS-1)

Append to the paragraph at `:330`, which already tells authors that "On a recycling pod the runtime exits
at each session end":

```
The adapter's socket address is bound for the pod's lifetime and does not change between sessions, so a
runtime process started for a later session on the same pod connects to the same address.
```

## 8. Non-goals

- **Keeping the runtime process alive across the recycle boundary.** §15.4.3 has the platform start a
  fresh runtime process for each session, §6.1's `recycle.enabled: true` row has the whole-pod scrub
  terminate the SDK process along with all other session processes, and §5.2 scrub step 1 kills the pod's
  user processes. The occupancy-zero connection close is that death and stays. An earlier attempt to make
  the runtime survive the boundary was reverted in `39e08bb4`.
- **Naming who creates the successor runtime process.** Proposal 0079 owns it. A restartable runtime
  container, an adapter-side spawn equivalent to the `SpawnPath` path, and a narrowing of §15.4.3's
  promise by `deploymentModel` are all live candidates, and each of them needs the address this proposal
  preserves.
- **Restructuring the accept path.** Several readings of this defect treat a single-flight accept, a
  long-lived acceptor goroutine, or a late-connection reaper as a precondition of the listener fix,
  because a `Start` that times out leaves a goroutine parked in `Accept` whose eventual connection is
  neither used nor closed. The hazard is real and is recorded in §5, and it is declined here for two
  reasons. It is strictly pre-existing: `Close` returns at `pkg/adapter/socketruntime.go:436-440` when the
  process never connected, so the failed-start path never reached the listener close even today, and the
  reaping that a later teardown performed was incidental. And the property this proposal establishes, that
  a later `Start` accepts, holds unconditionally when no earlier `Start` timed out, which is every
  sequential-session path. Restructuring `accept` changes connection-adoption semantics and needs its own
  race coverage, which is a second mechanism rather than this scope error. §11 asks for a separate
  finding.
- **Clearing `p.cmd` after the grace-window reap.** The reaped handle survives on the struct, and a second
  `Wait` on it would be wrong. It is unreachable: `Close` returns early while `p.connected` is false, and
  the only way back to a connected state is a `Start`, whose `spawn` overwrites `p.cmd`
  (`pkg/adapter/socketruntime.go:311-313`) before the accept. Clearing it would be a second change with no
  failure behind it.
- **Clearing `connected` and `conn` on a terminal `Interrupt`, and scoping the fan-out reader to a
  connection generation.** Both are recorded in §5 as accepted pre-existing failure modes of the session
  cohort's release rather than of the pod-scoped scope error this proposal fixes, and the first changes
  `Interrupt`'s observable behavior. §11 routes them to a finding.
- **Adding a typed sentinel for a closed listener, adapter metric series, or new log lines.** An
  `outcome`-labelled accept counter and a bound gauge would make the condition alertable, and they would
  need §16.1 catalog rows and `docs/reference/metrics.md` rows, which is a specification edit this
  code-only remediation is not the place for. The existing slot-failure signals carry the condition, and
  the error text an operator meets is stated in §6.
- **Adding `CloseListener` to `RuntimeProcess`, or naming it `Shutdown`.** Rejected in D3 and D4.
- **Rebinding the listener at session start.** Rejected in D6.
- **Correcting `pkg/adapter/scrub/scrub.go`'s `Ops` claim that the whole-pod scrub terminates the
  runtime's SDK process, and reconciling §4.7.9 step 7 with §4.7.10.** The claim is false on the sidecar
  transport because §13.1 forbids `shareProcessNamespace`, and the correct sentence depends on which actor
  part (b) picks.
- **Giving the tier-10 recycle-scrub conformance property a `RuntimeProcess` that can fail it, and
  un-skipping `TestTaskModeRecycleScrubsWorkspaceBetweenSessions`.** Both need a successor runtime process
  to exist, so both stay with 0079. The tier-5 skip entry in `tests/registers/skip-reasons.yaml` stays as
  written.
- **Renaming the file or the tests to carry the `CH-MSGSOCK` identifier under the naming law's N4.** No
  file or test in `pkg/adapter` has adopted the channel-identifier convention yet, and migrating one file
  leaves the package half done. The identifier appears in the new doc comments; the rename belongs with
  the migration that does the rest.
- **Converting the remaining line citations in `pkg/adapter/socketruntime.go`.** Only the comments this
  change rewrites are converted to heading citations; the rest belong to the line-citation migration.

## 9. Testing

Tiers reached: 0, 1, 4, 7a, and 11. Tier 3 is not reached, because no proto message, JSONL frame, HTTP
request, or CRD schema changes. Tier 5 is not reached, because the Kind recycle case needs a successor
runtime process. Tier 10 is not reached, for the reason in §8. Every case carries a `// spec:` annotation
naming §5.2 and §15.4.3, and every tier-2-and-higher case carries a `// diagnosis:` comment.

**TEST-1, tier 1, `pkg/adapter/socketruntime_test.go`.**
`TestSocketRuntimeListenerOutlivesTheLastSessionAndAcceptsTheNext_spec_15_4_3`. Bind, dial,
`Start("sess-a")`, round-trip a frame, `Close("sess-a")`, assert the first runtime observes EOF, then dial
the same `SocketPath()` again, `Start("sess-b")`, and round-trip a frame over the second connection. The
EOF assertion pins that the fix did not keep the runtime alive across the boundary. Against the unpatched
tree the second dial is refused and the second `Start` fails with `use of closed network connection`,
which the case names in its failure message so a regression is diagnosable from the text alone.

**TEST-2, tier 1.** `TestSocketRuntimeInterruptLeavesTheListenerBound_spec_5_2`. The same sequence with
the last session ended by a clean `Interrupt` rather than `Close`. It passes today and pins the parity
§1.3 identifies, so a later change that moves the listener close into `Interrupt` fails here.

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
session against the same adapter process. Assert the second session's response arrives and that the two
runtime processes are distinct, which is the fresh-process-per-session contract holding over one
pod-lifetime address. The fixtures at `tests/tier4_integration/concurrent_workspace_test.go:119-127` and
`concurrent_delegation_proxy_test.go:161-168` already bind that transport. The successor runtime here
comes from `SpawnPath`, which is the documented developer loop, so the case asserts the adapter half and
says nothing about who spawns under the sidecar deployment model. `// diagnosis:` a failure means the pod
lost its runtime ingress at the first session's end, so no recycling pod can serve a second session.

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

**Tier 11.** The existing documentation pass covers DOCS-1; no new case.

Existing cases that must stay green unmodified: `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`
(`pkg/adapter/socketruntime_test.go:252`) and `TestSocketRuntimeProcessInterruptScopedToSlot_spec_5_2`
(`:310`) pin the sibling-active behavior, and `TestSocketRuntimeProcessStartTimesOutWithoutAConnection`
(`:104`) pins the first-generation accept timeout. Their surviving unchanged is the evidence that the
occupancy-zero semantics did not move.

Every new case lands in an existing package. Confirm with `lenny-test validate-maps` at tier 0 whether
`tests/spec-map.json` needs an entry for the new tier-4 and tier-7a files, and add it if the gate asks.
Run `lenny-test --changed --max-tier 4` for S1 through S4, `--tier 7a` for S5, and `--tier 11` for S6.
Coverage: the change deletes one statement and adds `CloseListener` and its guard, so run
`lenny-test coverage --diff <base-ref>` and confirm the new lines are reached by TEST-4.

## 10. Findings closed on application

- **BUILD-GAPS F-5.2.33, part (a)**: the pod-scoped listener was torn down on a per-session teardown. The
  finding stays OPEN, because part (b) names the component that creates the fresh runtime process for
  session N+1 on the sidecar deployment model and states what the whole-pod scrub can reach while
  `shareProcessNamespace` is forbidden, which is proposal 0079's. Annotate the finding's evidence
  paragraph to record that its listener clause is resolved, so the remaining claim reads as the absent
  successor runtime process rather than the destroyed address. Leave the checkbox unticked.
- **Amendment to proposal 0073.** SCHEMA-1's statement that `Runtime.Close` is unchanged in signature and
  behavior, and §9's record of the unbound listener as an accepted limit (`:1997`, `:6638`), remain
  accurate statements about 0073's own diff. On application the behavior they describe no longer holds in
  the tree: the two sentences asserting that a later `Runtime.Start` fails in accept because the listener
  was closed are superseded, and the start instead fails on the accept timeout until 0079 lands. No file
  under `proposals/` is edited. A `proposal-conformance` pass on 0073 after this lands will report the
  difference, and this paragraph is the answer to it.

## 11. Open decisions for review

1. **Whether the accept-path restructure belongs in this change.** Most readings of this defect judged a
   single-flight or long-lived acceptor to be a precondition of the listener fix, on the argument that an
   abandoned accept goroutine can swallow the successor's dial once the listener outlives the session. §8
   declines it, on the grounds that the hazard is strictly pre-existing and that the property this change
   establishes holds unconditionally when no earlier `Start` timed out. The recommendation is a separate
   BUILD-GAPS finding against `accept` (`pkg/adapter/socketruntime.go:279-300`), filed on application. A
   reviewer who disagrees should fold it in here rather than after, because landing the two in the other
   order leaves the widened window open for the interval between them.
2. **Whether the session-cohort release defects are filed as one finding or several.** A terminal
   `Interrupt` leaves `connected` true and `conn` set, and the fan-out reader is not scoped to the
   connection that produced it, so a departing session's reader can reach the next session's subscribers.
   Both are recorded in §5 and both become reachable more often once the object outlives a session. The
   recommendation is one new finding covering the cohort's release, distinct from the accept-path finding
   in item 1.
3. **Whether the accept timeout becomes operator-tunable.** The 30s default
   (`pkg/adapter/socketruntime.go:192-194`) is set nowhere in `cmd/lenny-adapter`, so it is a non-spec
   default with no override, which `code-best-practices.md` does not permit. It is also the new upper
   bound on how long a start on a recycled sidecar pod blocks before the gateway can retry elsewhere,
   which is the one operator-visible cost this change adds. A `--runtime-accept-timeout` flag is a small
   change. It is left out of the staged set because it is a second scope error rather than this one; the
   alternative is to file it against 0079, where the successor dial's latency budget is decided.
4. **Whether the tier-4 case earns its tier.** TEST-5 demonstrates reuse through the `SpawnPath`
   developer loop rather than through the sidecar deployment model the defect matters for. It is staged
   because it is the highest tier that can fail today and because the fixture already exists. A reviewer
   may prefer to drop it and wait for 0079's tier-5 case, which leaves the change covered at tiers 1 and
   7a.

## 12. Files touched on application

| File | Deliverable | Change |
|:--|:--|:--|
| `pkg/adapter/socketruntime.go` | CODE-1, CODE-2 | `Close`'s final statement, the type, `Close`, and constructor doc comments, the `listenerClosed` field, and `CloseListener`. |
| `cmd/lenny-adapter/main.go` | CODE-2 | The hoisted `socketRuntime` variable and the `CloseListener` call after `srv.GracefulStop()`. |
| `pkg/adapter/socketruntime_test.go` | TEST-1 to TEST-4 | The four new tier-1 cases and the cleanup conversion on the existing cases. |
| `pkg/adapter/socketruntime_e2e_test.go` | TEST-1 | The cleanup conversion. |
| `tests/tier4_integration/` (new file) | TEST-5 | The two-sequential-sessions case. |
| `tests/tier4_integration/concurrent_workspace_test.go` | TEST-5 | The cleanup at `:126`. |
| `tests/tier4_integration/concurrent_delegation_proxy_test.go` | TEST-5 | The cleanup at `:168`. |
| `tests/tier7a_load_local/` (existing package) | TEST-6 | The `Close`-versus-`Start` race case. |
| `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` | TEST-7 | The fixture's accept-timeout bound and the tightened sequenced-leg assertion. |
| `tests/spec-map.json` | TEST-5, TEST-6 | The new cases' spec-section mappings, if `validate-maps` requires them. |
| `docs/runtime-author-guide/lifecycle.md` | DOCS-1 | One sentence appended to the paragraph at `:330`. |
| `BUILD-GAPS.md` | — | F-5.2.33's evidence annotated per §10; the finding stays OPEN for part (b). |

No file under `spec/`, `schemas/`, or `charts/` is touched.
