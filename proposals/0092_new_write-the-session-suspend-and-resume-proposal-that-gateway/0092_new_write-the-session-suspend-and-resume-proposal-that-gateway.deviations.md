# Deviations: Session suspend, idle suspension, and resume driver

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Adapter-contract heartbeat section gains the per-session cadence sentence

**Status:** proposed

Reported by step S8 (Heartbeat frames: schema, adapter-contract reference, and tier-3 address test).

**What the proposal says.** The Heartbeat frames paragraph of the non-spec changes limits the `heartbeat` section of `docs/reference/adapter-contract.md` to three edits: the example gains `sessionId`, the hung-runtime sentence is replaced, and the sentence after it stays. The paragraph does not stage the per-session cadence sentence from SPEC-5ac item 1 for the reference page.

**What landed instead.** The `heartbeat` section of `docs/reference/adapter-contract.md` gains a sentence before "Your runtime MUST respond". The sentence states that the adapter writes one heartbeat per interval for each session, from `session_start` until `session_end` or release, whether or not a gateway stream is open, and names the session in `sessionId`. The wording follows the SPEC-5ac item 1 card, with reader-facing terms in place of "registry entry" and "Attach".

**Why.** Without the sentence, the reference example carries a `sessionId` that nothing on the page explains. The doc-content rule requires a reference page to be precise about the surface it documents. The added sentence states only behavior that §28.5.3 already defines.

**What a later reader would otherwise get wrong.** A reader comparing the reference page against the staged non-spec changes would find a cadence sentence the proposal did not stage and could treat it as unreviewed drift or remove it as out of scope.

## Proposed: heartbeat_ack Session seal citation restated in prose on the adapter-contract page

**Status:** proposed

Reported by step S8 (Heartbeat frames: schema, adapter-contract reference, and tier-3 address test).

**What the proposal says.** The Heartbeat frames paragraph replaces the `heartbeat_ack` sentence "No other fields." with the SPEC-5ac field table and rule. The second sentence of the SPEC-5ac rule cites §4.7.1 **Session seal.**

**What landed instead.** In `docs/reference/adapter-contract.md`, the field table and the MUST rule are copied verbatim. The sentence that cites §4.7.1 is restated in prose: the adapter counts a `busy: true` ack that names a session as activity for that session when it decides whether the session is idle, and an ack that names no session, or whose `busy` is false or absent, counts as activity for no session.

**Why.** `doc-content.md` does not allow spec section numbers in published documentation. The restatement follows the "busy reports" bullet of §4.7.1 **Session seal.** The adapter-contract page has no Session seal section to link to yet; a later documentation step adds the SealSession rows.

**What a later reader would otherwise get wrong.** A reader checking the page against SPEC-5ac would find the §4.7.1 citation absent and the rule's second sentence reworded, and could conclude the staged text was applied incompletely. A later step that adds the Session seal section could also leave the prose restatement in place without linking it to that section.

## Proposed: tests/spec-map.json credits the S8 heartbeat tier-3 cases

**Status:** proposed

Reported by step S9 (DOCS-1a: API, error-catalog, and SDK-example pages for resume and QUOTA_EXCEEDED).

**What the proposal says.** The DOCS-1a targets list edits to `rest.md`, `mcp.md`, `internal.md`, `error-catalog.md`, `curl.md`, and the `go`, `python`, and `typescript` SDK example pages. The list does not name `tests/spec-map.json`.

**What landed instead.** `tests/spec-map.json` credits the two S8 tier-3 cases `session_scoped_frame_address_test.go::TestHeartbeatFramesDeclareOptionalSessionAttribution` and `session_scoped_frame_address_test.go::TestHeartbeatAckBusyRequiresSessionID` under section 28.5.3.

**Why.** The tier-0 check `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` was already failing at HEAD before step S9, because step S8 added the two cases without crediting them in the map. The change adds two map lines and returns tier 0 to green.

**What a later reader would otherwise get wrong.** A reader comparing the S9 diff against the DOCS-1a targets would find a test-map edit outside the step's documentation scope and could treat it as unrelated drift. A reader auditing S8 could also assume S8 added the credits itself.

## Proposed: mcp.md resume_session output example shows resume_pending

**Status:** proposed

Reported by step S9 (DOCS-1a: API, error-catalog, and SDK-example pages for resume and QUOTA_EXCEEDED).

**What the proposal says.** The DOCS-1a `mcp.md` item covers the `resume_session` entry text and its `suspended` and `awaiting_client_action` bullets. The item does not mention the output-schema example.

**What landed instead.** In `docs/api/mcp.md`, the `resume_session` output example shows `status` `"resume_pending"` where it previously showed `"running"`. The entry also states that the tool returns the session in its new state.

**Why.** The DOCS-1a Content line states that the call returns the session in its new state and that the restore's outcome arrives on the event stream. An example that always showed `running` contradicted the rewritten bullets.

**What a later reader would otherwise get wrong.** A reader checking `mcp.md` against the DOCS-1a item would find a changed output example the proposal did not stage and could revert it to `running`, which would reintroduce the contradiction with the entry's bullets.

## Proposed: detached release covers every reclaim caller

**Status:** proposed

Reported by step S11 (CODE-3: binder post-claim callback and detached release after claim).

**What the proposal says.** CODE-3 item 3, **Detached release**, states that the `reclaimOnFailure` drain and the `ReclaimClaimed` delete in `reclaim.go` run on `context.WithoutCancel(ctx)`, bounded by `slotCleanupFloor`, when `Bind` reaches them.

**What landed instead.** The `failPhase` drain and the `ReclaimClaimed` claim delete always run on the detached context, through `deleteClaimDetached` in `pkg/gateway/podlifecycle/podsession/reclaim.go`. The detached release applies to every caller that reaches reclaim: `Bind`, the decomposed `/finalize` and `/start` reclaim, the created-expiry sweeper, and `/terminate`.

**Why.** The step reported that reclaim does not know which caller reached it. Bounding the release by the cleanup floor rather than by the caller's context is never less safe, and it closes the same orphan-claim leak on the decomposed path. Scoping the detach to `Bind` alone would have needed an additional flag.

**What a later reader would otherwise get wrong.** A reader checking `reclaim.go` against CODE-3 item 3 would find the detached context on paths the item does not name and could conclude the scope drifted unintentionally, or could narrow the detach back to `Bind` and reopen the orphan-claim leak on the decomposed `/finalize` and `/start` path, the created-expiry sweeper, and `/terminate`. A reader who relies on the caller's context to cancel a reclaim from one of those paths would also expect a cancellation that no longer applies.

## Proposed: CODE-5 edits outside the Targets line

**Status:** proposed

Reported by step S12 (CODE-5: Sweeper adoption of SPEC-2a states and hooks).

**What the proposal says.** The CODE-5 Targets line limits the code edits to `coordination.go`, `coordination_seams.go`, `coordfixture.go`, and the two named fake files.

**What landed instead.** `cmd/lenny-gateway/redis_quota.go` received comment-only edits. Its Sweeper wiring comment now names the binding stamp and states that `HeldDeliverer` and `TerminalReleaser` stay unset. In `coordination_mirror_test.go`, the expected mirror generation changed from 2 to 3, because a takeover's mirror row now carries the `RecordHandoff` generation that CODE-5 item 2 introduces.

**Why.** The step reported that the wiring comment described the renewed-generation check that CODE-5 deletes, and that the mirror test asserted the behavior item 2 replaces, so the test would fail unless updated.

**What a later reader would otherwise get wrong.** A reader checking the S12 diff against the CODE-5 Targets line would find edits to `redis_quota.go` and `coordination_mirror_test.go` that the proposal did not stage, and could treat them as unrelated drift. A reader who reverted the mirror test's expected generation to 2 would reintroduce a failing assertion against the item 2 behavior.

## Proposed: adoptable predicate inlined into sweepRow guard

**Status:** proposed

Reported by step S12 (CODE-5: Sweeper adoption of SPEC-2a states and hooks).

**What the proposal says.** CODE-5 item 1 sets `adoptable := leaseUnheld && adoptedState(row) && !inAdoptionBackoff` and rewrites the comment above `eligible`.

**What landed instead.** The `eligible` variable is inlined into the guard `if !bound && priorHolder != s.replicaID && !adoptable` in `sweepRow` in `coordination.go`. The Sweep loop is split into `sweepRow`, `sweepTerminal`, `takeover`, and `deliverHeld`. The rewritten comment sits above that guard.

**Why.** The step reported that the split follows the function-size rule in the code best practices, and that the behavior matches the staged predicate.

**What a later reader would otherwise get wrong.** A reader looking for the `eligible` variable or a single Sweep loop body named in CODE-5 item 1 would not find either, and could conclude that item 1 did not land. The predicate and its comment are at the guard in `sweepRow`.

## D1 · step S15 · 2026-10-13 · accepted
**Status:** accepted
**Proposal says:** CODE-6 item 18.3 in `0092_new_write-the-session-suspend-and-resume-proposal-that-gateway.non-spec-changes.md:698` starts a heartbeat monitor only "when `written` is true, `entry.hb` is nil, and `s.heartbeatsReachRuntime()` holds". The condition rests on the review-log argument (`review-log.md:292` and `review-log-archive.md:7855, 8144, 8401`) that no entry receives a second session_start after its monitor stopped.
**Implemented instead:** `startHeartbeatLocked` in `pkg/adapter/heartbeat.go:208-221` builds a new monitor when `entry.hb` is nil or when `entry.hb.replaceable()` is true, and skips the start only when `entry.hb != nil && !entry.hb.replaceable()` (`heartbeat.go:212`). `replaceable()` (`heartbeat.go:225-237`) is true only when the monitor's `done` channel is closed and its `hung` channel is not. The only start site is `markSessionStartWritten` in `pkg/adapter/sessionframes.go:280-292`, which calls `startHeartbeatLocked` only when `written` is true. A hung monitor is never replaced, so a hang stays visible until the entry is removed. A monitor that stopped without finding a hang is replaced on a re-written session_start, which keeps the item 18.3 invariant that every entry whose session_start is written and whose session_end is not holds exactly one running monitor. `TestHeartbeatRestartsOnSessionStartRewrittenOnSameEntry_spec_28_5_3` in `pkg/adapter/heartbeat_session_internal_test.go` pins both behaviors.
**Why:** The literal condition omits the case where the monitor stopped without hanging, so code that follows it literally breaks the item's own one-running-monitor invariant whenever that case occurs. The judges found no code defect: the reviewer reported the landed code as right, and its fix field reads "No code change". Changing the code to the nil-only condition would delete correct code and a correct test and would be a regression the reviewer would report. The only changes that close the finding are an amendment to item 18.3 or this deviation entry, and the fix loop may not edit the proposal. When the re-start case cannot occur, as the review log argues, the code behaves exactly like the literal condition, so the extra branch is a defensive superset of the proposal rather than a contradiction of it.
**Consequence if the proposal is not corrected:** A reader comparing `heartbeat.go` with item 18.3 would see the `replaceable()` branch as unstaged drift. An implementor who aligned the code with the literal "`entry.hb` is nil" wording would remove the replacement branch, fail the re-start subtest, and leave an entry with a written session_start and no running monitor if a re-written session_start ever occurs.
**Suggested next step:** correct the proposal. Amend item 18.3's start condition to read "`entry.hb` is nil or has stopped without finding the runtime hung".
**Evidence:** Commit df961e74e added the replaced-when-stopped and kept-when-hung subtests in `pkg/adapter/heartbeat_session_internal_test.go`. Rounds 4 (c1f8bbb08, a separate budget-key TTL finding) and 5 (no commit) left the heartbeat code unchanged, and the reviewer re-reported the same proposal-wording gap each round while calling the code right. Three judges ruled the finding unresolvable by the fix loop (confidence high, medium, and medium): each found that the code keeps the proposal's invariants and that only a proposal amendment or a deviation entry closes the finding.

## Proposed: REST resume entry check skipped only when a sealed re-route delivered messages

**Status:** proposed

Reported by step S16 (CODE-7: concurrent-session caps exclude suspended; resume admission).

**What the proposal says.** CODE-7 item 4 and the path 6 **Admission.** paragraph of spec §7.2 place the resume admission check before any message of the request is held or delivered. The second review finding suggested a fix that is not proposal text: skip the REST entry admission check on any sealed re-route, meaning either `d.rerouted` is set or the delivered slice is non-empty.

**What landed instead.** `resumeAndDeliver` in `pkg/gateway/sessionserver/messages_delivery.go` skips the entry check only when `restDelivery.deliveredAny` is set. `rerouteSealed` sets that field when a sealed refusal reports one or more delivered messages. A sealed re-route that delivered nothing and finds the row suspended still runs the entry check.

**Why.** The step reported that the finding accepts both conditions, and that the narrower one follows the spec ordering exactly, because in that case no message has yet been held or delivered. The step also reported that the narrower condition keeps REST consistent with MCP (§15.2.1): on MCP, a sealed re-route that lands on a row classified suspended runs the check in the `ActionResumeAndDeliver` case of `route`.

**What a later reader would otherwise get wrong.** A reader comparing the code with the finding's suggested fix would expect the entry check to be skipped whenever `d.rerouted` is set, and could treat the `deliveredAny` condition as an incomplete fix. A reader who widened the skip to every sealed re-route would let a re-route that delivered nothing resume a suspended session without the admission check, and REST would diverge from MCP.

## Proposed: executor returns ErrSessionSealed on a held seal grant as well as on the sealed end error

**Status:** proposed

Reported by step S18 (TEST-1: remaining tests at tier 2 and above and tier-0 and tier-11 checks).

**What the proposal says.** CODE-6 item 4 (Session seal, Executor bullet) names one `ErrSessionSealed` predicate. A wait that ends on done returns `ErrSessionSealed` when the stored end error is `FailedPrecondition` with the `session_sealed:` prefix.

**What landed instead.** The executor also returns `ErrSessionSealed` for a turn whose envelope was written under a seal epoch the adapter granted, when that grant still held at the end of the stream, whatever status ended the stream. The predicate is `endCause` in `pkg/gateway/session/executor/podstream.go`. The seal epoch and grant live in `pkg/gateway/podlifecycle/podsession/bindrequest.go`. `sealSession` and `unsealSession` in `pkg/gateway/sessionserver/session_seal.go` mark, grant, withdraw, and clear them. This code landed in an earlier attempt, and step S18 left it unchanged.

**Why.** The step reported that the review found the second predicate a safe superset that closes the race in which the release ends the stream before the adapter's refusal is seen. The finding asked for no code change and asked for a human decision on the proposal side.

**What a later reader would otherwise get wrong.** A reader comparing `endCause` with CODE-6 item 4 would see the seal-grant branch as unstaged drift. A reader who narrowed the predicate to the `session_sealed:` end error alone would reopen the race in which a turn ended by the release surfaces as an executor failure instead of a sealed refusal.

## Proposed: message paths re-route on ErrNotBound as well as on ErrSessionSealed

**Status:** proposed

Reported by step S18 (TEST-1: remaining tests at tier 2 and above and tier-0 and tier-11 checks).

**What the proposal says.** CODE-6 item 7 Path 2 names a single re-route trigger, `errors.Is(err, executor.ErrSessionSealed)`. Every other send error keeps its existing handling, which is `EXECUTOR_FAILURE` on REST.

**What landed instead.** Both message paths also re-route on `executor.ErrNotBound` through `rerouteUnbound`, in `pkg/gateway/sessionserver/messages_delivery.go` and in `pkg/gateway/mcpfabric/mcptools/send_delivery.go`. The handler re-reads the row from the primary and routes the message again when the row is no longer running. A second failure takes the held branch, and so does a failed re-read. Only a re-read that finds the row still running keeps `EXECUTOR_FAILURE`.

**Why.** The step reported that a not-bound send reached no runtime, so routing it again or holding it cannot deliver it twice. The behavior matches the §6.2 rule to route a refused message by the session's current state or hold it in the DLQ, and it closes the race in which the idle suspension's release unbinds the pod after the classification read. The finding asked for no code change. This attempt extended the branch so that a failed re-read is held, as the third review finding requires.

**What a later reader would otherwise get wrong.** A reader comparing the message paths with CODE-6 item 7 Path 2 would see `rerouteUnbound` as unstaged drift. A reader who removed it would return `EXECUTOR_FAILURE` for a message sent while the idle suspension's release was unbinding the pod, instead of routing it to the suspended session or holding it in the DLQ.

## Proposed: off-holder running-row message case skipped on F-7.2.38

**Status:** proposed

Reported by step S18 (TEST-1: remaining tests at tier 2 and above and tier-0 and tier-11 checks).

**What the proposal says.** The Testing section of the non-spec changes requires the tier-7a interleaving test `TestIdleSuspensionRequiredPairs_spec_6_2` in `tests/tier7a_load_local/suspension_interleaving_test.go` to pass, including the case `message on rep-2 racing the zero-hold release`. That case asserts that a message a peer replica serves while the idle suspension's zero-hold release runs is answered as delivered or queued. Spec §7.2 (delivery path 2 and the forwarding sentence) and the off-holder `running` row of §29.3 state that the serving replica forwards a message for a `running` session to the coordinating replica, and that on an unreachable coordinator it buffers the message and answers `queued`. Testing **Failures the tests find.** item 6 requires every failure the tests expose to be fixed, or filed as a finding with a matching deviation entry.

**What landed instead.** The case calls `t.Skip("blocked: F-7.2.38 (owner: R19) ...")` when the peer replica answers HTTP 500 `EXECUTOR_FAILURE` with the `not bound to a pod` error. That answer occurs in either start order whenever the peer replica classifies the message on the `running` row before the idle suspension's write lands. The serving replica sends through its own executor in `deliverNow` (`pkg/gateway/sessionserver/messages_delivery.go`), and that executor's registry holds no binding for a session another replica coordinates. No §29.3 forward exists, so the message is neither delivered nor held. Step S18 filed the gap as OPEN finding F-7.2.38 in `BUILD-GAPS.md`.

**Why.** Cross-replica message forwarding (`ForwardMessage`, with an unreachable coordinator degraded to a DLQ enqueue under the path-6 custody rule) is owned by remediation step R19 in `gateway-runtime-comms-remediation.md` §10.1. Building it lies outside this proposal's code changes. The case is skipped rather than weakened so that it starts asserting the §29.3 forward as soon as R19 lands, and the skip reason names the finding and its owner.

**What a later reader would otherwise get wrong.** A reader of the test would see a required interleaving case that never fails and could take the off-holder `running` row of §29.3 as implemented. A reader who removed the skip before R19 lands would see a failure that this proposal's code did not cause and could treat the forward as a defect of the idle suspension. A reader who widened `rerouteUnbound` to cover this case would hold a message for a session that is still `running` on its coordinator, which is the forward's job rather than the re-route's.
