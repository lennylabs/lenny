# Summary: Keep the pod's runtime listener across a session teardown

## Scope

- **Scope:** `SocketRuntimeProcess.Close` is a per-session teardown and its last statement unbinds the
  pod-scoped `CH-MSGSOCK` listener that `NewSocketRuntimeProcess` bound once at adapter boot, so the
  address every later runtime connection needs is destroyed by the first session that ends. The listener
  close moves out of the per-session path and into a pod-scoped `CloseListener` the adapter process calls
  when it exits. This discharges part (a) of BUILD-GAPS finding F-5.2.33 against specification text that
  already stands, and it changes no specification sentence. Part (b), which names the component that
  creates the successor runtime process on the sidecar deployment model, is proposal 0079 and is out of
  scope here. This proposal applies after proposal 0073's code phase completes.

This document stages the proposed code, test, and documentation changes. It does not modify any spec,
code, or doc file. Apply the changes in the Proposed changes section after sign-off.

## Summary

**What changes**

- `pkg/adapter/socketruntime.go`: `Close` stops closing the listener and returns `nil` on the
  last-session path. The occupancy-zero gate, the shared-connection close, and the graced child reap are
  unchanged, so the runtime still dies at the session boundary.
- `pkg/adapter/socketruntime.go`: a new idempotent `CloseListener() error` on the concrete type releases
  the listener, and the three doc comments that describe the listener as part of a per-slot teardown are
  corrected to state its pod lifetime.
- `cmd/lenny-adapter/main.go`: the signal-handler goroutine calls `CloseListener` after
  `srv.GracefulStop()` returns, which is where the listener's lifetime actually ends.
- Tests: tier-1 cases that a second session accepts a second runtime connection on the same address and
  that `CloseListener` is what unbinds it, a tier-4 case that one adapter serves two sessions in
  sequence over one bound address, and a tier-7a case for a last-session `Close` racing an arriving
  `Start`. The existing `pkg/adapter` and tier-4 cleanups move to `CloseListener`, and the tier-7a drain
  fixture's accept timeout is bounded because its failing leg now waits rather than failing at once.
- `docs/runtime-author-guide/lifecycle.md` states that the adapter's socket address is stable for the
  pod's lifetime.

**Fixed decisions**

- The occupancy-zero gate stays as it is. `releaseActiveLocked` returning `len(p.active) == 0`
  (`pkg/adapter/socketruntime.go:384-387`) implements §5.2's slot-independence rule, and closing the
  shared connection on that release is the runtime death §15.4.3 requires.
- The listener is bound once, in `NewSocketRuntimeProcess` (`pkg/adapter/socketruntime.go:156-162`), and
  is never rebound. Its owner is the adapter process.
- `RuntimeProcess` (`pkg/adapter/session.go:46`) gains no method, and `Close` keeps its signature.
  `CloseListener` is declared on the concrete type alone.
- This proposal does not decide who creates the runtime process for session N+1 on the sidecar
  deployment model. After it lands, a recycling sidecar pod still serves one session, and it fails at the
  accept timeout rather than by dialing an address that no longer exists.
- The proposal applies after proposal 0073's code phase completes through S18.

**Watch out for**

- The one-line deletion is not the whole change. Nothing else in the tree closes that listener: the
  adapter's signal goroutine tears down the SDK, the runtime operations channel, and the gRPC server, and
  never touches `Runtime` (`cmd/lenny-adapter/main.go:412-427`). Deleting the close with no replacement
  leaves a filesystem-path socket file behind for the `--runtime-socket <path>` developer loop and for the
  tier-7a fixture, which binds a path under a temp directory
  (`tests/tier7a_load_local/shutdown_drain_gate_race_test.go:415-416`).
- `Close`'s return value is load-bearing beyond logging. `pkg/adapter/session.go:260` assigns it to
  `closeErr`, which selects `leaked` over `released` in `reportSessionScrub` (`:275`) and sets
  `ShutdownResponse.ExitedCleanly` (`:287`). A `leaked` outcome feeds the §5.2 unhealthy-slot ledger
  (`pkg/gateway/sessionserver/start.go:2743-2771`). Returning a pod-scoped resource's error there charged
  a pod failure to whichever session happened to be last.
- `Interrupt` (`pkg/adapter/socketruntime.go:398-417`) already performs the same occupancy-zero teardown
  and does not close the listener. The two teardown paths disagree today, and `Interrupt` is the one that
  is right. Do not make them consistent in the other direction.
- `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`
  (`pkg/adapter/socketruntime_test.go:252`) looks like the coverage for this area and passes both before
  and after. It asserts the sibling and last-slot connection semantics and never dials a second time,
  which is why the defect survived it.
- `tests/tier7a_load_local/shutdown_drain_gate_race_test.go:352-356` already comments that "The pod's
  listener stays bound". The comment is false today and becomes true with this change. Read it as a
  schedule hazard rather than as evidence the behavior is already correct: the leg's incoming start goes
  from an immediate closed-listener error to a full accept timeout, and `socketDrainPod` sets that
  timeout to 5s (`:421`) across `raceAttempts` of 40
  (`tests/tier7a_load_local/racestart_testsupport_test.go:29`).
- A prior attempt on this surface failed. Six commits (`8cdd5d6d` through `ccaeb30d`) alternated between
  making the runtime survive the recycle boundary and restoring its death, and `39e08bb4` reverted the
  excursion. The runtime's death at occupancy zero is specified behavior and this proposal keeps it.
- A 0073 review round staged this same listener-close drop inside SCHEMA-1
  (`proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:8583-8589`) and a later
  redesign withdrew it. The withdrawal was a scope decision rather than a refutation, and 0073 §9 records
  the resulting behavior as an accepted limit (`:1997`, `:6638`).
