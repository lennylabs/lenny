# Problem: Keep the pod's runtime listener across a session teardown

## 1. Problem

### 1.1 The defect

`SocketRuntimeProcess.Close` ends with `return p.listener.Close()`
(`pkg/adapter/socketruntime.go:467`).

`Close` is a per-session teardown. Its parameter is a session identifier, its first act is
`releaseActiveLocked(sessionID)` (`:441`), and both production callers pass one ending session: the
`Shutdown` RPC (`pkg/adapter/session.go:260`) and the coordinator-lost hold teardown
(`pkg/adapter/holdstate.go:244`).

The listener is not per-session. `NewSocketRuntimeProcess` binds it (`pkg/adapter/socketruntime.go:157`)
and `cmd/lenny-adapter/main.go:354` calls that constructor at adapter boot, before the gRPC server
serves and therefore before any session exists. Nothing rebinds it, and no other code path calls
`net.Listen` for it. The address is a per-pod constant: `podspec.RuntimeSocketName` is `@lenny-runtime`
and the controller writes it into the runtime container's `LENNY_ADAPTER_SOCKET`
(`pkg/controller/sandbox/podspec/podspec.go:159`, `:164`), which `SocketPath`
(`pkg/adapter/socketruntime.go:167-169`) reports from the same listener.

A per-session operation therefore destroys a pod-scoped resource. Everything `Close` does before that
statement is correctly scoped: it releases the named session, and only when the release empties the
active set does it close the shared connection and reap a spawned child. The listener close is scoped to
nothing and removes the address for the rest of the adapter process's life.

The consequence is mechanical. After the last session's `Close`, `p.connected` is false, so a later
`Start` takes the accept path and calls `p.listener.Accept()` (`:286`) on a closed listener. Accept
returns `net.ErrClosed` at once and `accept` wraps it as
`adapter: accept runtime connection: use of closed network connection`. A runtime that dials the address
gets a refused connection, because nothing is bound to the name any more. Both directions of the
conversation are gone.

The failure is silent. The scrub reports success and `StartSession` fails at once. non-spec-changes.md
§5's first row gives the gateway's handling of the failed start.

### 1.2 What the specification already requires

No specification sentence needs to change. Four statements already fix the listener's lifetime as the
pod's.

- **The address exists before any session does.** §4.7.9's startup sequence orders the pod's boot so that
  the adapter signals READY and the pod enters the warm pool at step 4, and the gateway assigns a session
  at step 5. A socket the warm pod holds before it is claimable is not a resource a session owns.
- **The runtime is the dialling participant.** §28.5.3 states of the intra-pod boundary that "The runtime
  is the dialling participant on every channel here", and the `CH-MSGSOCK` register row places the
  channel on a Unix socket the adapter owns (§28.3). A dialled endpoint that is not bound is not an
  endpoint.
- **The adapter process, and what it holds, survives the recycle boundary.** §5.2's Lenny scrub procedure
  states that "The adapter closes the ending session's runtime, keeps the pod process alive across the
  recycle boundary, runs the scrub asynchronously, and reports the outcome for `podId` via
  `ReportPodScrub` on its GatewayControl link. On a `standard` or `in-place` pool the pod then keeps the
  process alive and reuses it for the next session." The sentence names exactly one thing that closes,
  the ending session's runtime, and requires the adapter process to continue in order to serve the next
  session. Reuse of a process that has destroyed its own listening address is not reuse. The scrub's own
  steps 0 through 6 enumerate what the boundary resets, and none of them rebinds a socket, because none
  of them was expected to have unbound one.
- **Recycling is promised without runtime cooperation at every level.** §15.4.3 states that "the platform
  scrubs the pod and starts a fresh runtime process for each session, so recycling requires no runtime
  cooperation and is available at every integration level".

§4.7.11 item 7 states the same lifetime rule for the sibling intra-pod listeners: the platform and
connector MCP servers "are pod-wide and started at most once per pod", and "A later session's manifest
write does not re-arm a running server." No specification statement supports the current code. Every
statement about closing something at session end names the runtime process or the connection, never the
endpoint.

### 1.3 The same judgement is already made twice in the tree

`RuntimeOps` owns the pod-scoped listener for the runtime operations channel. It binds in its constructor,
documents that "the channel must outlive any single runtime process" because "A resumed session ... starts
a fresh runtime that dials the same socket and re-handshakes" (`pkg/adapter/runtimeops.go:160-172`), and
releases the listener from a zero-argument `Close()` (`:602-621`) that `cmd/lenny-adapter/main.go:424`
calls on the signal path. `SocketRuntimeProcess` is the same arrangement on the same boundary with the
process-scoped teardown folded into the per-session one, and it has no process-scoped teardown at all.

`Interrupt` (`pkg/adapter/socketruntime.go:398-417`) performs the same occupancy-zero teardown for the
heartbeat-hung case, closing the shared connection and killing a spawned child, and it leaves the
listener bound. A pod whose last session ended through `Interrupt` keeps its address and a pod whose last
session ended cleanly loses it. The specification draws no such distinction, which is the evidence that
the listener close is accidental.

### 1.4 A second caller with the same expectation

The recycle boundary is the loudest caller and not the only one. `terminateHeldSession`
(`pkg/adapter/holdstate.go:225-245`) closes the runtime when a held session's coordinator is lost, and its
own comment gives the reason as keeping the pod's surfaces cancellable "for the next claim". That path
expects the pod to be reusable by construction and unbinds the pod's runtime address on the way past.

### 1.5 Why nothing catches it

Every case in `pkg/adapter/socketruntime_test.go` binds an address, accepts one runtime connection, and
ends. No case dials a second time. `TestSpawnedRuntimeIsSignalledOnClose`
(`pkg/adapter/socketruntime_e2e_test.go:191`) drives a real runtime binary through spawn, accept, and
`Close`, and stops at the `Close`. The tier-5 case that would surface it end to end,
`TestTaskModeRecycleScrubsWorkspaceBetweenSessions` in `tests/tier5_e2e_kind/execution_modes_test.go`, is
skipped with its reason recorded in `tests/registers/skip-reasons.yaml`. The tier-10 property that claims
to hold a conforming adapter to the recycle contract runs against a double whose `Start` returns nil
unconditionally (`tests/tier10_conformance/recycle_scrub_conformance_test.go:61`), so it cannot fail.

### 1.6 Finding

BUILD-GAPS **F-5.2.33**, part (a). Part (b) is proposal 0079's and stays open.
