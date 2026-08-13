# Proposal: Route a runtime frame to one consumer instead of broadcasting it

- **Status:** Draft for review.
- **Date:** 2026-08-13
- **Scope:** Replaces the adapter's output fan-out with a routing table keyed on the frame's address, so a
  session-scoped frame reaches exactly one Attach stream or none. Proposal 0069 filters one frame type at
  the far end of the broadcast; this proposal removes the broadcast, which is the property that made the
  defect writable. Hoisting the heartbeat is the prerequisite and is the risk-bearing half.

This document stages the proposed code and specification changes. It does not modify any spec, code, or
doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

Proposal 0069 fixes a live cross-session write by filtering `set_tracing_context` inside the existing
fan-out. It records this change as its intended end state and defers it, because the prerequisite touches
the pod's liveness escalation. 0071 assumes 0069 has landed and removes the filter it added, along with
the mechanism that made the filter necessary. §7 states what happens if the order is reversed.

## 1. Problem

### 1.1 Correctness is currently a property every consumer must independently uphold

A pod runs one runtime process behind one socket and one reader.
`SocketRuntimeProcess.broadcast` hands the identical line to every registered subscriber
(`pkg/adapter/socketruntime.go:248-258`), and `Output` ignores its session-id argument entirely — the
parameter is declared `_ string` (`:340`). Addressing happens afterwards, N times over, in a
`demuxSlotOutput` goroutine per Attach stream (`pkg/adapter/attach.go:275-303`) whose filter is

    if fs := frameSlotID(line); fs != "" && fs != slotID { continue }

On a four-slot pod, four goroutines each receive every frame and discard three-quarters of them. A frame
reaches the right session because every other consumer agreed to drop it.

That is the defect class rather than a defect. "One frame reached two sessions" is a state the design can
represent, and a consumer that forgets to filter produces it. `set_tracing_context` was such a consumer:
`attach.go:114-115` called `handleSetTracingContext` with each stream's own session, so one untagged frame
wrote four session rows. 0069 corrects that consumer. The next session-scoped frame type added to §28.5.3
arrives with the same obligation and no structural reason to meet it.

### 1.2 The broadcast exists for one frame type, and that type is pod-scoped by accident

The `fs != ""` disjunct is what leaks: a frame carrying no `slotId` is delivered to every stream. The
comment at `attach.go:285-288` states why — `heartbeat_ack` is per-connection and each slot's monitor must
observe it.

The heartbeat is per-Attach rather than per-process, and the arrangement reads as unintended.
`startHeartbeat` is called from the Attach handler (`attach.go:78`), so a pod runs one monitor per open
stream. Each writes with `rt.WriteEnvelope(sessionID, frame)` (`pkg/adapter/heartbeat.go:152-153`),
unstamped, onto the shared connection. Any single ack satisfies whichever monitors see it
(`attach.go:103`). A four-slot pod therefore probes one process four times per interval and answers all
four monitors with one ack.

The probe is already pod-scoped in effect while presenting as session-scoped, and it is the sole reason
untagged frames are broadcast at all.

### 1.3 The escalation is already reference-counted, which the hoist should preserve rather than invent

`onHeartbeatHung` calls `rt.Interrupt(ctx, sessionID, false)` (`pkg/adapter/heartbeat.go:163-166`).
`SocketRuntimeProcess.Interrupt` releases that session from the active set and **returns without touching
the process when other sessions remain active** (`pkg/adapter/socketruntime.go:397-406`): it closes the
connection only when the release was the last one.

So a hang detected by one slot's monitor on a four-slot pod does not signal the runtime today. It
deregisters that session and leaves the process serving its siblings. Only the last active session's hang
closes the connection.

This matters for the hoist in two ways. It removes the objection that a single monitor would change a
per-session escalation into a pod-wide one, because the escalation is already pod-wide-on-last-session and
is a no-op before that. And it identifies the behaviour the hoist must reproduce rather than replace.

## 2. Decisions

1. **The reader resolves the address; consumers do not filter.** `SocketRuntimeProcess` holds a map from
   address to one consumer and delivers each frame to at most one. `demuxSlotOutput` and the per-stream
   filtering it performs are removed, and with them 0069's `set_tracing_context` predicate, which routing
   subsumes.

2. **The address is the slot id, with the empty string as the base path.** This is the addressing the tree
   already uses: `useSlot(slotID)` keys on presence (`pkg/adapter/slot.go:43`), a concurrent-pool Attach
   always carries a slot id (`pkg/gateway/session/executor/pod.go:147`), and slot ids are session ids and
   never reused (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:682`). Nothing new is invented.

3. **A registration carries `(address, sessionID)` and is removed when either ends.** Deregistration on
   stream close and on `releaseSlot` is what makes a stale stream unreachable rather than merely filtered,
   so 0069's live-binding condition becomes structural.

4. **A frame naming no live address is dropped and counted once, by the reader.** Today that observation is
   unrepresentable: no component sees the whole address set alongside the frame, so an unknown slot id is
   silently discarded by every stream independently. One reader makes it a single countable event.

5. **The heartbeat becomes one monitor per runtime process.** It is what the probe already is. The monitor
   is owned by `SocketRuntimeProcess`, its frame is written once per interval rather than once per stream,
   and its ack is consumed by the reader before routing, so it never enters an Attach stream and needs no
   address.

6. **The hang escalation keeps its current reference-counted shape.** A hung process is a pod-wide fact, so
   the monitor's escalation closes the connection and ends every attached stream, which is what
   `Interrupt` already does on the last active session. The change is that it happens once on a genuine
   process hang rather than once per stream that noticed, and no longer depends on which session's monitor
   fired.

7. **The frame types that may be untagged are enumerated, not inferred.** After the hoist the set is: every
   frame on a single-session pod, whose address is the empty string and whose sole consumer is the base
   stream. On a pod serving slots the set is empty, and an untagged frame names nothing. §28.5.3 states
   this rather than leaving it to the demux comment.

## 3. Detailed design

### 3.1 The routing table

`SocketRuntimeProcess` replaces `subscribers map[*subscriber]struct{}` with
`consumers map[string]*consumer`, keyed by address, where a `consumer` carries the buffered feed the
current `subscriber` carries plus the `sessionID` the registration was made for.

`Output` gains the address it currently ignores. Its signature stops taking a session id it does not use
and takes `(ctx, address string)`; `Attach` passes its own `slotID`. Registering an address already
present is refused, which makes the one-consumer-per-address invariant enforced rather than assumed —
`startSessionSlot` already refuses a claim on an occupied slot with `Unavailable`
(`pkg/adapter/slotsession.go:41`), so the refusal is reachable only on an adapter bug.

`fanOut` reads a line, extracts its address with the existing `frameSlotID` (`pkg/adapter/slotframe.go`),
and delivers to `consumers[address]` if present. Absent, it increments a counter and logs a protocol error
naming the address and the frame type. `broadcast` is deleted.

### 3.2 The heartbeat

`newHeartbeatMonitor` moves from `Server.startHeartbeat` to `SocketRuntimeProcess`, started when the
runtime connects and stopped when it disconnects. Its write path is unchanged apart from being called
once. Its ack is consumed in `fanOut` ahead of the routing lookup, because a `heartbeat_ack` has no
address and is not a consumer's frame.

`Server.onHeartbeatHung` stays where it is and keeps its escalation, invoked by the process monitor rather
than by an Attach loop. Each attached stream ends when the connection closes, which is the existing
mechanism.

The Attach loop's `heartbeat_ack` branch (`attach.go:98-104`) is removed along with the per-stream monitor.

### 3.3 What 0069 leaves behind

0069's `set_tracing_context` predicate in `attach.go` is deleted. Its condition 1, address equality, is the
routing lookup. Its condition 2, live-binding confirmation, is deregistration. Its tier-1 cases are kept
and re-pointed at the routing behaviour, because the outcomes they assert are unchanged: an untagged frame
on a concurrent pod registers nowhere, a slot-tagged frame on a single-session pod registers nowhere, and
a correctly addressed frame registers once.

## 4. Testing

**Tier 1, `pkg/adapter`.** One consumer per address is enforced and a duplicate registration is refused. A
frame naming a live address reaches that consumer and no other. A frame naming no live address reaches
none and increments the counter. A stream whose slot was released receives nothing further. The
0069 outcomes above, re-pointed.

**Tier 7a, `tests/tier7a_load_local`.** The concurrency surface, and the reason this proposal is separate
from 0069. Frames for several addresses interleaved under `-race` land one-to-one with no cross-delivery.
A slot released while its consumer is mid-drain deregisters without dropping a frame already handed to
another consumer and without blocking the reader. A consumer that stops draining does not stall delivery
to its siblings, which the current per-subscriber pump goroutine guarantees and the routing table must
preserve.

**Tier 1 and tier 8, the heartbeat.** One monitor per process rather than per stream. An ack satisfies it.
A missed deadline escalates once, closes the connection, and ends every attached stream. On a pod serving
several sessions the escalation fires once rather than once per stream. A pod whose runtime never hangs
sees no escalation across a full interval sweep under load, which is the regression guard for the failure
mode §5 names.

**Tier 9.** The 0069 cross-session isolation case, unchanged, asserting it still holds under routing.

## 5. The risk, stated plainly

`onHeartbeatHung` sends SIGTERM. A defect in the hoisted monitor's ownership or cancellation kills a
healthy runtime serving live sessions, which is a worse outcome than the wrong-row write 0069 fixes.

Three things bound it. The escalation path is unchanged and keeps its reference count (§1.3), so the hoist
changes what *triggers* it rather than what it *does*. The monitor's lifetime becomes the connection's,
which is a simpler lifetime than an Attach stream's and is already the lifetime `fanOut` has. And the
tier-8 case above asserts the negative — no escalation on a healthy pod under load — rather than only the
positive.

A reviewer who judges the risk unacceptable can take §3.1 alone: the routing table closes the defect class
for every frame type that carries an address, and leaves `heartbeat_ack` as a named exception the reader
consumes ahead of the lookup, with the per-stream monitors intact. That is a smaller change with most of
the structural benefit and one documented irregularity. §7 records it as the fallback rather than the
plan, because leaving N monitors probing one process keeps a per-stream mechanism whose only remaining
purpose is to be excepted.

## 6. Proposed changes

### CODE-1. Route rather than broadcast

`pkg/adapter/socketruntime.go`: the consumer map, the address-keyed delivery in `fanOut`, the refusal of a
duplicate registration, the unknown-address counter and log, and the deletion of `broadcast`. `Output`
takes an address.

`pkg/adapter/attach.go`: pass `slotID` to `Output`; delete `demuxSlotOutput` and its call site; delete
0069's `set_tracing_context` predicate.

### CODE-2. One heartbeat per runtime process

`pkg/adapter/heartbeat.go` and `pkg/adapter/socketruntime.go`: the monitor moves to the process, starts on
connect, stops on disconnect, and has its ack consumed in `fanOut`. `pkg/adapter/attach.go` loses
`startHeartbeat`, the `heartbeat_ack` branch, and the per-stream hung wiring.

### SPEC-1. State the addressing and the untagged set

`spec/28_communication-channels.md` §28.5.3 states that the adapter delivers each frame to the session
holding the address the frame names, that a frame naming no live address is dropped and logged, and that
the frames which may carry no address are exactly those on a pod serving one session. It states that
`heartbeat_ack` is protocol-level, answered by the process's own monitor, and never delivered to a
session's stream.

The §28.5.3 wording 0069 stages for `set_tracing_context` is reduced to the general rule, because the
frame is no longer a special case.

## 7. Ordering, and what breaks if it is reversed

0071 depends on 0069 having landed: it deletes a predicate 0069 adds and re-points tests 0069 writes.
Landing 0071 first would mean carrying the cross-session defect until the riskier change is ready, which
inverts the reason 0069 exists.

If the hoist is judged unacceptable, §5's fallback lands §3.1 without §3.2. 0069's predicate is still
deleted, because routing subsumes it; the per-stream monitors and the reader's `heartbeat_ack` exception
remain.

## 8. Non-goals

Changing the heartbeat interval, the ack timeout, or the escalation's signal. Changing what an Attach
stream delivers to the gateway. Per-slot sockets, which were evaluated for proposal 0069 and rejected:
one process writing to several descriptors can misaddress exactly as easily as it can misstamp, and the
model of one runtime process per pod is stated by both the specification and the code
(`pkg/adapter/slotsession.go:19-22`).

## 9. Files touched on application

- `pkg/adapter/socketruntime.go` — CODE-1 and the process-owned monitor.
- `pkg/adapter/attach.go` — CODE-1 and CODE-2 removals.
- `pkg/adapter/heartbeat.go` — CODE-2.
- `pkg/adapter/slotframe.go` — unchanged; `frameSlotID` becomes the reader's address extractor.
- `spec/28_communication-channels.md` §28.5.3 — SPEC-1.
- `pkg/adapter` tier-1 cases, `tests/tier7a_load_local`, `tests/tier8_chaos`, `tests/tier9_security`, and
  their `tests/spec-map.json` entries, per §4.
