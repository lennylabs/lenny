# Summary: Keep the pod's runtime listener across a session teardown

## Summary

**Problem statement.** `SocketRuntimeProcess.Close` is a per-session teardown and its last statement
unbinds the pod-scoped `CH-MSGSOCK` listener that `NewSocketRuntimeProcess` bound once at adapter boot, so
the address every later runtime connection needs is destroyed by the first session that ends.

**What changes.**

- `pkg/adapter/socketruntime.go`: `Close` stops closing the listener and returns `nil` on the
  last-session path. The occupancy-zero gate, the shared-connection close, and the graced child reap are
  unchanged (non-spec-changes.md D8).
- `pkg/adapter/socketruntime.go`: a new idempotent `CloseListener() error` on the concrete type releases
  the listener, and the `pkg/adapter` comments that describe the listener as part of a per-slot teardown
  are corrected.
- `cmd/lenny-adapter/main.go`: `main` defers `CloseListener` where it constructs the socket transport, so
  the listener is released after `srv.Serve` returns (non-spec-changes.md D5).
- `pkg/adapter/socketruntime.go`: each accepted connection's fan-out reader delivers to and closes only
  the subscribers registered against that connection (CODE-3).
- Tests: tier-1 cases that a second session accepts a second runtime connection on the same address,
  that `CloseListener` is what unbinds it, and that a departing connection's reader leaves the next
  connection's subscribers alone, tier-4 cases that one adapter serves two sessions in sequence
  over one bound address and that the adapter process unlinks a filesystem-path socket when it exits on
  SIGTERM, and a tier-7a case for a last-session `Close` racing an arriving `Start`. Every test that
  constructs a `SocketRuntimeProcess` gains a `CloseListener` cleanup (non-spec-changes.md §7.5), and the
  tier-7a drain fixture's accept timeout is bounded because its failing leg now waits rather than failing
  at once.
- `docs/runtime-author-guide/lifecycle.md` states that the adapter's socket address is stable for the
  pod's lifetime.

**Decisions.**

- The occupancy-zero gate stays as it is. `releaseActiveLocked` returning `len(p.active) == 0`
  (`pkg/adapter/socketruntime.go:384-387`) implements §5.2's slot-independence rule, and `Close`'s
  occupancy-zero branch is left untouched (non-spec-changes.md D8).
- The listener is bound once, in `NewSocketRuntimeProcess` (`pkg/adapter/socketruntime.go:156-162`), and
  is never rebound. Its owner is the adapter process.
- On the socket transport, `Close` no longer reports an error, so a socket runtime's session scrub always
  reports the `released` outcome. `MCPRuntime.Close` keeps the `leaked` outcome reachable, and the change
  adds no compensation for it (non-spec-changes.md D7).
- `RuntimeProcess` gains no method, and `Close` keeps its signature. `CloseListener` is declared on the
  concrete type alone.
- This proposal lands together with proposal 0079 or immediately before it. Landed alone, it turns the
  immediate failure of a second `StartSession` on a recycling sidecar pod into a wait for the full accept
  bound (non-spec-changes.md §5, first row).

**Watch out for.**

- The one-line deletion is not the whole change; see non-spec-changes.md D2.
- `Interrupt` (`pkg/adapter/socketruntime.go:398-417`) already performs the same occupancy-zero teardown
  and does not close the listener. The two teardown paths disagree today, and `Interrupt` is the one that
  is right. Do not make them consistent in the other direction.
- `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`
  (`pkg/adapter/socketruntime_test.go:252`) looks like the coverage for this area and passes both before
  and after. It asserts the sibling and last-slot connection semantics and never dials a second time,
  which is why the defect survived it.

## Goals

The listener close moves out of the per-session path and into a pod-scoped `CloseListener` the adapter
process calls when it exits. This discharges part (a) of BUILD-GAPS finding F-5.2.33 against
specification text that already stands, and it changes no specification sentence.

## Non-goals

- Part (b) of BUILD-GAPS finding F-5.2.33 and the runtime process's lifetime across sessions, which
  proposal 0079 owns.

## Open decisions for human to make

None. The last-slot `Interrupt` window was adjudicated by the human on 2026-09-30 and 2026-10-01: it stays
out of this proposal, and proposal 0079 (approved 2026-10-02) closes it, because its D3 makes per-session
`Close` and `Interrupt` leave the shared runtime connection open and its staged test pins that a last-slot
`Interrupt` followed by a sibling `Start` binds to a live connection.

## Defects in the shipped tree that this proposal does not stage

- **A timed-out `Start` leaves its accept goroutine parked in `Accept`** (non-spec-changes.md §5 and §8,
  **Restructuring the accept path**).
- **`SocketRuntimeProcess.AcceptTimeout` has no operator override.** Its 30s default is set nowhere in
  `cmd/lenny-adapter`, which `code-best-practices.md` does not permit for a non-spec default. After this
  change it bounds how long a start on a recycling sidecar pod waits (non-spec-changes.md §5, first row).
- **The `CH-MSGSOCK` accept path performs no manifest-nonce handshake.** §28.5.3 (Intra-pod) states the
  handshake as the first message on this socket (`spec/28_communication-channels.md:533-534`), and §4.7.11
  item 1 requires it (`spec/04_system-components.md:937`). `SocketRuntimeProcess.accept` returns the raw
  accepted connection (`pkg/adapter/socketruntime.go:279-300`), and `Start` passes it to the fan-out reader
  without reading a first message (`:202-227`). The next `Start` therefore accepts a stale dial queued in
  the listener's backlog (non-spec-changes.md §5, row "A runtime dials between two sessions"). This
  proposal does not stage the handshake because the gap predates the listener change, the handshake is a
  separate authentication contract with its own nonce-only challenge-response branch, and no staged
  deliverable relies on it.
- **The `Ops.KillUserProcesses` doc comment overstates what the whole-pod scrub kills.** The comment
  states that the scrub's `kill -9 -1` terminates the runtime's SDK process
  (`pkg/adapter/scrub/scrub.go:75-77`). `DefaultOps.KillUserProcesses` runs that command inside the
  adapter container (`pkg/adapter/scrub/defaultops.go:28-35`). On the sidecar transport the runtime runs
  in a separate container, because §4.7.10 sets `shareProcessNamespace: false`
  (`spec/04_system-components.md:915`) and §13.1 forbids setting it to true
  (`spec/13_security-model.md:17-19`), so the command cannot reach the runtime there. This proposal does
  not correct the comment because the accurate sentence depends on the runtime-lifetime rule proposal 0079
  owns, and no staged deliverable relies on the claim (non-spec-changes.md §8).

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0073_fix_give-every-session-a-slot-and-absence-one-meaning | Implemented (2026-08-31). | See non-spec-changes.md §10. | Nothing. 0073 is a landed proposal and is not edited. |
| 0071_fix_route-a-runtime-frame-to-one-consumer-instead-of-broadcasting-it | Draft for review. | CODE-3 gives each accepted connection its own subscriber set, which `fanOut` delivers to and closes on exit. | Keep its consumer table scoped to the connection whose reader delivers to it. |
| 0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod | Draft, being redesigned to keep the runtime process across sessions on recycling pools with `maxSessionsPerPod` > 1, except `scrubProfile: vm-restart` pools, which restart it after every session, with pod retirement when the runtime has exited. | Keeps the pod's `CH-MSGSOCK` address bound across session teardowns and leaves `Close`'s occupancy-zero branch to 0079 (non-spec-changes.md D8). | Land together with this proposal or immediately after it (**Decisions.**). |
