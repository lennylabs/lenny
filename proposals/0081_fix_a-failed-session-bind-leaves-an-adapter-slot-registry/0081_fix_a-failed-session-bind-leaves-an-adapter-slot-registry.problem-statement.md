# Problem: A failed session bind leaves a stale adapter slot registry entry

## Statement

**A failed session bind leaves an adapter slot registry entry that nothing removes.** On a pod that still
holds a sibling slot at the moment the reservation is released, or on a recycling pool, the entry survives
for the rest of the pod's life and degrades both the session already running on that pod and every session
placed there afterwards.

This is proposal 0080's inventory entry §1.2, promoted to its own proposal. Every anchor below was
re-verified against the tree at commit f2a397b53 on 2026-09-08.

**The entry is created early.** The adapter's slot registry entry is created by the first workspace-prep RPC,
well before StartSession. `ensureSlotPaths` calls `ensureSlotStateLocked`, which inserts into `s.slots`
unconditionally, and its own doc comment states the case: "The §4.7 workspace-prep RPCs (PrepareWorkspace,
FinalizeWorkspace, RunSetup) run before StartSession, so this creates the slot tree the first time the
gateway materializes the slot's workspace, ahead of the slot's StartSession claim"
(`pkg/adapter/slot.go:135-148`, insertion at `:105-126`). Its three production callers are the
PrepareWorkspace staging resolver, FinalizeWorkspace, and RunSetup (`pkg/adapter/staging.go:134`, `:181`,
`:337`). `assignCredentialsSlot` later binds that entry to a session by writing `st.sessionID`, and can
still fail afterwards at the credential-file write (`pkg/adapter/slotcreds.go:23-44`). The registry is keyed
by the session identifier on every path: the RPC handler passes `sessionID` as both the session and the slot
argument (`pkg/adapter/credentials.go:74`), so there is no slot-identifier divergence.

**Nothing removes it on a failed bind.** When a bind fails, the gateway calls `ReleaseSlotReservation`, whose
doc comment states plainly that it releases the SandboxClaim and the active_slots count "without an adapter
Shutdown", on the stated ground that "the failed attempt already closed its adapter connection"
(`pkg/gateway/podlifecycle/podsession/slotbinder.go:487-503`). Its body reaches only
`podclaim.SlotClaimer.ReleaseSlot` and issues no adapter RPC. Closing the connection does not remove the
adapter's registry entry. There is exactly one `delete(s.slots, ...)` in the whole adapter, inside
`deregisterSlotLocked` (`pkg/adapter/slotsession.go:174-188`). Every path into it is a start-path rollback
(`releaseSessionSlot`, `pkg/adapter/session.go:133,147,157`), the Shutdown handler
(`pkg/adapter/session.go:238`), or `deregisterStartedSessions`, which collects an entry only when
`st.started` is true (`pkg/adapter/slotsession.go:375-395`) and runs only from the §10.1 coordinator-hold
timeout (`pkg/adapter/holdstate.go:190`). None of them runs on a gateway-side bind failure. The §5.2
whole-pod scrub does not clear the registry either: it enumerates residue from disk and states the reason in
its own comment, "because the residue the scrub must reach belongs to a leaked slot whose registry entry is
already gone" (`pkg/adapter/podscrub.go:119-158`).

**The trigger is four of the five slot failure stages.** `slotbinder.go:285-325` carries
`slotFailureWorkspacePrep` (a PrepareWorkspace or FinalizeWorkspace failure), `slotFailureSetup` (a RunSetup
failure), `slotFailureCredentialAssignment` (including a credential-file write failure), and
`slotFailureSessionStart` (including a lost or cancelled StartSession RPC). Each closes the client and
returns. There are five stage constants rather than six: the fifth, `slotFailureConnect`, refuses before any
adapter entry exists and is documented as not a `lenny_slot_failure_total` error_type value at all
(`pkg/gateway/podlifecycle/podsession/binder.go:286-299`, `slotbinder.go:233-250`). A server-side StartSession
rejection is a sub-case of `slotFailureSessionStart` rather than a stage of its own; the adapter compensates
it with `releaseSessionSlot` and leaves no residue (`pkg/adapter/session.go:133,147,157`,
`pkg/adapter/slotsession.go:214-220`). The workspace-prep stage is a trigger rather than a guarantee: a
transport-level failure before the PrepareWorkspace handler reaches `resolvePrepareStagingDir` creates no
entry (`pkg/adapter/staging.go:122-140`). The other three stages run strictly after PrepareWorkspace
succeeded, so for them the entry always exists.

**There are three residue classes, and a remedy must close all three.**

Registered but unbound, left by the workspace-prep and setup stages. The entry exists with `sessionID`
empty. `len(s.slots) != 1` then holds forever, so `claimPodMCPStartLocked` returns false for every later
session on that pod and the pod's intra-pod MCP surface never arms (`pkg/adapter/slotsession.go:107-115`,
reached from `:75-88` on every start path). The same entry holds §28.5.3's slot count above one for the life
of the pod.

Bound but unstarted, left by the credential-assignment stage and by a `StartSession` the adapter refused
before `Runtime.Start`. The entry additionally carries `sessionID`, so it has the same two consequences and
also keeps `boundRemains` true, which suppresses the §15.4.2 drain on every later Shutdown for the life of
the pod (`pkg/adapter/slotsession.go:181-186`, `pkg/adapter/session.go:238`, `:259-261`). Because it follows a
successful `AssignCredentials`, it also leaves a written `/run/lenny/slots/{sessionId}/credentials.json` and
an armed per-provider direct-mode expiry timer (`pkg/adapter/slotcreds.go:44-51`, `:216-233`).

Bound and started, left by a lost or cancelled `StartSession` RPC whose handler ran to completion. The
adapter handler has no failure point after `Runtime.Start` returns, so a deadline or a cancellation that the
gateway reads as a failure leaves `started` true, the session in `runtimeLive`, `runtimeCohort` raised, and
the runtime actually running for a session the gateway has abandoned (`pkg/adapter/session.go:155-163`,
`pkg/adapter/runtimegeneration.go:20-49`). This class needs a runtime close rather than a registry delete
alone. It adds consequences the other two do not have: `runtimeIdleLocked` is false forever, so
`cancelPodMCPIfRuntimeIdle` can never tear a surface down (`pkg/adapter/runtimegeneration.go:90-93`,
`pkg/adapter/slotsession.go:238-247`); `soleSessionLocked` returns empty as soon as one further session
starts, which refuses every intra-pod MCP `tools/list` and `tools/call` on the pod under the exactly-one-
session rule (`pkg/adapter/runtimegeneration.go:76-88`; spec/15_external-api-surface.md:1734;
spec/04_system-components.md:962); and `hasStartedSession` returns true, so the §10.1 coordinator hold would
arm on a pod the gateway believes holds nothing. That last consequence is latent rather than live. The hold
arms only from the close of the `AdapterEvents` stream (`pkg/adapter/holdstate.go:89-99`;
`pkg/adapter/adapterevents.go:100-108`), and no gateway code opens that stream today, so it waits on the
control-stream consumer remediation step R12 builds.

**The exposure is narrower than a failed bind and wider than a single occupancy episode.** The exclusive path is
outside the problem: `failPhase` reclaims through a claim DELETE and the pod retires, so the residue dies
with it (`pkg/gateway/podlifecycle/podsession/binder.go:996-999`, `:1072-1082`). The concurrent path retires
the pod too whenever the failed bind's slot was the pod's last occupant. `ReleaseSlotReservation` passes
recycle=false, `SlotClaimer.ReleaseSlot` deletes the per-pod claim at remaining==0 with the stated reason
"delete the per-pod occupancy claim so the pod retires", and the §4.6.1 occupancy projection moves a claimed
pod with no claim to draining (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:845-885`;
`pkg/controller/warmpool/occupancy.go:78-80`, `:132-140`; spec/06_warm-pod-model.md:144). The residue
therefore requires a co-tenant occupied at the moment of release, which is steady-state overlap on a
concurrent pool rather than any failure. Against that, the residue also outlives the occupancy episode on a
recycling pool: the recycle branch patches the claim bound → recycling and runs the whole-pod scrub, the
scrub touches disk only, and the pod returns to inventory still holding the entry, whose paths now point at
directories the scrub removed.

**Why it matters and why now.** The trigger is any transient error on those stages rather than a race, so it
needs no unusual timing, only co-tenancy. One mitigation caps the compounding, and only on one path: the
§5.2 retry policy records each failed bind on the per-pod slot-health tracker and drains the whole pod once
the rolling-window failures plus persistent leaks reach `ceil(maxConcurrentSessions/2)`
(`pkg/gateway/sessionserver/start.go:2834-2860`; `pkg/gateway/runtime/slothealth/slothealth.go:136-140`,
`:211-220`). That threshold is 1 at `maxConcurrentSessions: 2` and 2 at 3, so clustered failures on a
low-concurrency pool retire the pod. The mainline create-time path carries no such accounting:
`BindReservedSlot` releases the reservation, records nothing on the tracker, never evaluates the threshold,
and returns terminally (`slotbinder.go:210-224`; `pkg/gateway/sessionserver/start.go:2594-2605`). Proposal
0078 keeps the pod's runtime listener bound across a session teardown, and it does not widen this
exposure: its own scope note states that after it lands a recycling sidecar pod still serves one session
and fails at the accept timeout. It is sequenced after this one for the test-file reason the summary's
impacts row records. Proposal 0079 does not widen it, because it retires a sidecar pod at each
occupancy-zero recycle boundary.

**The missing compensation is on the gateway side**, so this is not an adapter-only change, and it is the change
most able to break a working bind path. The remedy is closer to hand than the original statement assumed.
The `Shutdown` RPC already deregisters an entry whether or not it is bound (`removed` is true for an unbound
entry; `bound` gates only the teardown), it is documented as deliberately idempotent, and the gateway
already has `Client.Shutdown` (`pkg/adapter/session.go:214-241`;
`pkg/gateway/runtime/adapterclient/client.go:807-809`). Sending it on the still-open connection before
`cl.Close()` at the four stages needs no new RPC. It does need one additive edit to
`schemas/lenny-adapter.proto`, because a reclaim dispatched after the gateway stopped waiting can reach the
adapter after the client's retry has taken the slot, and nothing on the wire today states which bind attempt
a reclaim is compensating. It is not a drop-in for two further reasons, and the proposal must say so: on a bound-but-unstarted entry `Shutdown` runs the full teardown, the
final usage flush, the §15.4.2 drain frame, `Runtime.Close`, and a `reportSessionScrub` that advances the
pod's sessions_served, for a session that never ran; and on a registered-but-unbound entry it skips
`removeSlotTree`, so the on-disk tree survives the registry removal (`pkg/adapter/session.go:243-282`).

**What the proposal must decide.** Whether the compensation is gateway-driven, adapter-driven, or both, and what
happens to the third class's running runtime and to the armed expiry timers. What the spec says about it:
§5.2's "Slot cleanup" bullet already states the rule for slot completion or failure, but §6.2's per-slot
sub-state machine has no edge into `slot_cleanup` from `slot_assigned` or `receiving_uploads`, which is
exactly the failed-bind window, and §4.7.9 states the bind sequence as a linear happy path with no failure
branch. The candidate sections are §4.7.9 (the bind sequence and its compensation obligation), §6.2 (the
missing sub-state edge), §5.2 (slot cleanup and the slot retry policy), §28.5.3 (the inbound count), §15.4.3
(the MCP arming), and §15.4.2 (the drain gate). §4.6.1 is not among them: it is the Warm Pool Controller
section and states no inbound count.

**Named out of scope.** `claimPodMCPStartLocked` gates on `len(s.slots) != 1`, a raw entry count, so two
interleaved binds on a healthy concurrent pod each observe two entries and neither arms the pod-wide MCP
surface, with no failed bind anywhere. That is an independent defect this residue makes permanent rather
than causes, and it needs its own fix. Also out of scope: retuning the `ceil(maxConcurrentSessions/2)`
unhealthy threshold, and the exclusive path's abandoned-Prepare case, where a session that prepares and never
calls Launch leaves the same entry by a different trigger with a different owner.

## Evidence

**Verified against the tree at commit f2a397b53, branch proposal-b/gateway-runtime-comms-remediation, on
2026-09-08.** Every citation below was opened and read in this run. Line numbers are as of that commit.

**Creation and binding.** `pkg/adapter/slot.go:97-126` (`ensureSlotStateLocked` inserts `s.slots[slotID]`) and
`:135-148` (the `ensureSlotPaths` doc comment, quoted verbatim in the statement) — verified.
`pkg/adapter/staging.go:134`, `:181`, `:337` (its three production callers) — verified.
`pkg/adapter/slotcreds.go:23-44` (`ensureSlotStateLocked`, then `if st.sessionID == "" { st.sessionID =
sessionID }` under the comment "The §4.7 bind sequence assigns credentials before StartSession", then a
`writeSlotCredentialFile` that can fail after the bind) — verified. `pkg/adapter/credentials.go:74`
(`assignCredentialsSlot(sessionID, sessionID, ...)`, so the registry key is the session identifier) —
verified.

**Absent removal.** `pkg/gateway/podlifecycle/podsession/slotbinder.go:487-503` (`ReleaseSlotReservation`; both
quoted phrases are verbatim, and the body reaches only `claimer.ReleaseSlot`) — verified.
`pkg/adapter/slotsession.go:174-188` (`deregisterSlotLocked`, the only `delete(s.slots, ...)` in the adapter;
a repository-wide grep returns that one site) — verified. `pkg/adapter/slotsession.go:375-395`
(`deregisterStartedSessions` filtering on `st.started`) and `pkg/adapter/holdstate.go:190` (its only caller)
— verified. `pkg/adapter/session.go:133,147,157` and `pkg/adapter/slotsession.go:214-220`
(`releaseSessionSlot` on the server-side start rollbacks) — verified. `pkg/adapter/podscrub.go:119-158` (the
scrub enumerates on-disk children and never reads `s.slots`) — verified.

**Failure stages.** `pkg/gateway/podlifecycle/podsession/binder.go:286-299` (five stage constants, with
`slotFailureConnect` documented as "not a `lenny_slot_failure_total` error_type value") — verified, and the
original statement's count of six is refuted. `slotbinder.go:285-325` (the four post-connection stages, each
preceded by `cl.Close()`) and `:233-250` (the four connect-stage returns, all before any workspace-prep RPC)
— verified. `pkg/adapter/staging.go:122-140` (the entry is created inside the PrepareWorkspace handler, so
the workspace-prep stage is conditional) — verified.

**Consequences.** `pkg/adapter/slotsession.go:107-115` (`claimPodMCPStartLocked` returns false when
`len(s.slots) != 1`), reached from `:75-88` on every start path — verified.
`pkg/adapter/slotsession.go:398-407` (`slotCount`, cited in code to §28.5.3, counting registered-but-unbound
entries deliberately) and `pkg/adapter/attach.go:344-362` (`deliverToSession` rejecting an unaddressed
session-scoped frame when the count exceeds one) — verified. `pkg/adapter/slotsession.go:181-186` with
`pkg/adapter/session.go:238`, `:259-261` (the `boundRemains` drain gate) — verified.
`pkg/adapter/slotcreds.go:44-51`, `:216-233`, `:245-275` and `pkg/adapter/adapterevents.go` (armed expiry
timers, cancelled only inside `deregisterSlotLocked`, firing `EmitAuthExpired`) — verified.
`pkg/adapter/slotsession.go:64-73` (an `sdkWarm` pod refuses every later claim outright with `Unavailable:
"pod is not idle: session %s is already bound on this pod"` while any other entry carries a non-empty
`sessionID`), reached from `pkg/adapter/session.go:111`, `pkg/adapter/resume.go:50`,
`pkg/adapter/sdkwarm.go:217` — verified. `pkg/adapter/runtimegeneration.go:20-93` (the cohort the third
residue class raises, and `runtimeIdleLocked`) — verified.

**Spec anchors.** spec/15_external-api-surface.md:1593 ("on a pod holding more than one slot an unaddressed
session-scoped frame is rejected and relayed to no stream") — verified. spec/15_external-api-surface.md:1734
and spec/04_system-components.md:962 (the intra-pod MCP call-time exactly-one-session rule) — verified.
spec/16_observability.md:189 (`lenny_adapter_unaddressed_frame_rejected_total`, and its scrape-target
caveat) — verified. spec/05_runtime-registry-and-pool-model.md:545 ("Slot cleanup: On slot completion or
failure, the adapter removes the slot's workspace directory, kills any processes owned by the slot's process
group, and releases the `slotId`") and `:553` (the slot retry policy) — verified.
spec/06_warm-pod-model.md:144-156 (the per-slot sub-state machine, with no edge into `slot_cleanup` from
`slot_assigned` or `receiving_uploads`) — verified. spec/04_system-components.md:848-858 (§4.7.9, the bind
sequence as a linear happy path) — verified. spec/04_system-components.md:338 (§4.6.1 is "Warm Pool
Controller (Pod Lifecycle)") — verified, and the original statement's "§4.6.1's inbound count" is refuted.

**Pod retirement and mitigation.** `pkg/gateway/podlifecycle/podclaim/slotclaimer.go:845-885` (remaining > 0
keeps the claim; remaining == 0 with recycle=false deletes it "so the pod retires"; the recycle branch
patches bound → recycling and contacts no registry) — verified. `pkg/controller/warmpool/occupancy.go:78-80`,
`:132-140` ("no claim on a claimed pod → draining") — verified.
`pkg/gateway/podlifecycle/podsession/binder.go:996-999`, `:1072-1082` (the exclusive path's `failPhase`
drain) — verified. `pkg/gateway/sessionserver/start.go:2834-2860` (`applySlotRetryPolicy`: release, then
`RecordLeak`/`RecordFailure`, then `Unhealthy` → `DrainSandbox`) with
`pkg/gateway/runtime/slothealth/slothealth.go:136-140`, `:211-220`
(`UnhealthyThreshold = (maxConcurrent+1)/2`) — verified.
`pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224` and
`pkg/gateway/sessionserver/start.go:2594-2605` (`BindReservedSlot` releases without any health accounting;
the create-time reserved row routes there) — verified.
`pkg/gateway/podlifecycle/podsession/binder.go:1031` is the only `verifyIntegrationLevel` call site, on the
exclusive Launch, so `materializeSlot` runs no declared-versus-observed check — verified.

**Sequencing.** Proposal 0073 (give every session a slot) is Implemented and created this surface. Proposal 0076
is Implemented and moved the coordination generation onto the slot entry, so `slotState` already carries
per-session fence state. Proposals 0078 and 0079 are "Draft for review" and are sequenced after this one, so
this must not depend on them. Proposal 0080 is an unconverged inventory that stages no changes; its §1.2 is
this problem — verified from each proposal's own status text.

**Programme constraints.** Rule S-2 in `gateway-runtime-comms-remediation.md` states that exactly one step
(R1b) edits `schemas/lenny-adapter.proto` and runs `make generate-proto`. The original reading of that rule,
that this proposal must not open the file at all, is refuted by the rest of the rule: S-2 reserves the FIRST
window to R1b and states that "a later step needing a field the plan did not enumerate opens a second narrow
window, and that window requires every in-flight `pkg/adapter` handler edit to have merged first". R1b's end
state is in the tree and the file has already been reopened once, for proposal 0076's comment-only edit, so
the second window is available — verified against the rule's own text. The precondition binds this proposal's
step ordering rather than its scope, because it opens three of S-2's covered handler files (`session.go`,
`slotcreds.go`, `sdkwarm.go`): the schema step and its regenerated stubs land before those handler steps.

**Testing surface.** `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` exercises the drain gate this
residue suppresses, and `pkg/adapter/slotsession_test.go:308`
(`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2`) already pins the registered-but-unbound
case of that gate — verified. The concurrent bind path is exercised in `tests/tier4_integration`.

**A standing hazard in this repository.** Work has repeatedly landed by a route nobody recorded, and a
proposal's assertion that something is absent has repeatedly proved stale. Verify absence by reading the
tree, never by trusting a register or another proposal's text.

## Who observes it

**The incumbent session on the pod, first and without waiting for a later session.** `slotCount()` counts
registered-but-unbound entries, so one residue entry holds the count permanently above one, and
`deliverToSession` then rejects every session-scoped runtime output frame that omits `sessionId`
(`pkg/adapter/attach.go:344-362`; `pkg/adapter/slotsession.go:398-407`). The spec permits a runtime to omit
that field precisely while the pod holds at most one slot (spec/15_external-api-surface.md:1593). A pod
serving one session, on which a second session's bind fails, therefore drops the incumbent's unaddressed
output from that moment on.

**Every later session placed on that pod**, for as long as the pod keeps taking sessions. Such a session arms
neither the platform MCP server nor any connector server, because `claimPodMCPStartLocked` refuses on the
entry count, while `writeSessionManifest` still hands its runtime a nonce and a socket path
(`pkg/adapter/session.go:124-153`). On an SDK-warm pod it is worse: the claim itself is refused with
`Unavailable`, so `StartSession`, `Resume`, and `ConfigureWorkspace` all fail
(`pkg/adapter/slotsession.go:64-73`).

**Operators, only if they wired an adapter scrape target.** The one metric that moves is
`lenny_adapter_unaddressed_frame_rejected_total`, which the spec records as emitted by the adapter process
inside the agent pod and therefore outside the default scrape target set until a deployer wires an adapter
scrape target (spec/16_observability.md:189). The only other signal is an adapter log line. Nothing on the
gateway side observes the residue at all, and `materializeSlot` runs no `verifyIntegrationLevel` check, so a
session that arms no MCP surface is never flagged as underperforming its declared level
(`pkg/gateway/podlifecycle/podsession/binder.go:1031` is the sole call site, on the exclusive Launch).

**The exposed population** is deployments that opted into concurrent sessions. `maxConcurrentSessions` defaults
to 1 and a value above 1 requires `acknowledgeProcessLevelIsolation`; the gateway dispatches to the slot path
only above 1, and the exclusive path drains the pod on failure. Single-session pools are unaffected.

## What breaks if nothing changes

**The §28.5.3 resolve-or-reject rule fails closed forever** on the affected pod. Unaddressed session-scoped
runtime output is relayed to no stream for the rest of the pod's life, which is a silent loss of a live
session's output rather than a degraded start.

**The pod's intra-pod MCP surface never arms again.** No later session on that pod gets a platform MCP server or
a connector server, and the third residue class additionally drives `soleSession` empty as soon as one
further session starts, which refuses every `tools/list` and `tools/call` on the pod under the
exactly-one-session rule. A remedy that removes the residue restores the arming only for a session that
claims while it holds the pod alone; wherever two binds overlap, the entry-count guard named out of scope
above refuses it with no residue involved.

**The §15.4.2 drain is suppressed** for every later Shutdown on that pod, because a bound residue entry keeps
`boundRemains` true. A Full-level runtime is then hard-closed with no DRAINING signal.

**Credential material and armed timers outlive the session.** A residue left after a successful
`AssignCredentials` keeps `/run/lenny/slots/{sessionId}/credentials.json` on disk and an armed direct-mode
expiry timer per provider. When one fires, `onSlotLeaseExpired` rewrites that file and emits AUTH_EXPIRED on
`CH-ADAPTEREVENTS` for a lease belonging to a session the gateway has since re-placed on another pod. This is
the same hazard class `hasStartedSession`'s own comment names for the §10.1 hold, left unmitigated on the
expiry path.

**A runtime runs for an abandoned session**, in the third class. The gateway has re-placed the session
elsewhere while the adapter's shared runtime process still holds it, `runtimeIdleLocked` is false forever so
no MCP surface can ever be torn down, and the §10.1 coordinator hold would arm on a pod the gateway believes
holds nothing once remediation step R12 opens the `AdapterEvents` stream whose close arms it.

**The residue crosses the recycle boundary** on a recycling pool, so a pod returns to inventory carrying an entry
whose paths point at directories the whole-pod scrub has removed, and later tenants inherit it after the
pinned-tenant hold expires.

**The compounding is bounded on one path only.** The §5.2 unhealthy threshold retires a repeatedly-failing pod on
the retry path, at the first failure when `maxConcurrentSessions` is 2. The mainline create-time
`BindReservedSlot` path records no health event and never evaluates the threshold, so residue accumulates
there at any concurrency.

## Findings this unblocks

**BUILD-GAPS finding ids: none.** No BUILD-GAPS.md finding references this problem. What follows is
the downstream work this change unblocks or constrains.

PROPOSAL 0080 §1.19, three fence-refusal classes sharing one status code. One of those classes is produced by
`boundSlotState` and `checkSessionBound` (`pkg/adapter/slotsession.go:262-290`), called on the fence path at
`pkg/adapter/coordination.go:116`. This change alters the membership of the bound and unbound sets those
predicates read, so it is sequenced first deliberately. State the effect on that predicate rather than
leaving it for the later work to discover.

**Files a later position also rewrites**, so prefer a remedy that does not force a second restructure of them:
`pkg/adapter/slotsession.go` and `pkg/adapter/slot.go` are rewritten by a later position covering 0080 §1.1,
§1.3, §1.4, §1.5, §1.16, §1.19, and §1.20.

## Prior art considered

**No proposal stages a remedy.** Proposal 0080 is an inventory that explicitly stages no changes, and its §1.2 is
this entry. Proposal 0073, which created the surface, recorded the gap in its §9 recorded limits and states
that discharging it is an obligation it does not take. The gateway-runtime-comms remediation programme's step
list carries no step touching the adapter slot registry's failed-bind compensation; its only slot mention is
per-slot hold state in R12.

**The adapter-side removal mechanism already exists and is reachable from the gateway.**
`Shutdown`'s clause two runs `deregisterSlotLocked(sessionID)` unconditionally and gates only the teardown on
`bound := removed && st.sessionID != ""`, and its own doc comment states that the conditional structure is
what makes the handler idempotent, because the §11.4 full revoke and the occupancy-zero edge each send a
second request for an already-released session (`pkg/adapter/session.go:214-241`). The registry key is the
session identifier, so `Shutdown(sessionID)` removes a registered-but-unbound entry as well as a bound one.
`Client.Shutdown` already exists (`pkg/gateway/runtime/adapterclient/client.go:807-809`) and the gateway
already calls it from `Binder.ReleaseSlot`; `ReleaseSlotReservation` is the deliberate variant that skips it.
No new RPC is needed, so `Shutdown` clause two stays the single removal entry point. What the existing RPC
does not carry is any statement of which bind attempt a reclaim compensates, and that is what the proposal
adds as an additive field on the same message.

**Reusing `Shutdown` unmodified is not a drop-in.** On a bound-but-unstarted entry it takes the full teardown
branch, the final usage flush, the §15.4.2 drain frame when no bound entry remains, `Runtime.Close`, and a
`reportSessionScrub` that advances the pod's sessions_served and feeds the leaked ledger, for a session that
never ran. On a registered-but-unbound entry it skips `removeSlotTree`, so the on-disk slot tree survives the
registry removal (`pkg/adapter/session.go:243-282`).

**A spec surface already states the compensation rule, partly.** §5.2's "Slot cleanup" bullet states that on slot
completion or failure the adapter removes the slot's workspace directory, kills the slot's processes, and
releases the `slotId`. What the spec does not model is the pre-start case: §6.2's per-slot sub-state machine
has edges only `slot_assigned → receiving_uploads → running → slot_cleanup → released|leaked`, with no edge
into cleanup from `slot_assigned` or `receiving_uploads`, which is exactly the failed-bind window. §4.7.9
states the bind sequence with no failure branch at all.

**The tree has made the bound-to-started refinement twice already**, so a third, cheaper option exists for part
of the problem and should be weighed and rejected explicitly rather than overlooked. `hasStartedSession`
chose the `started` flag over the bound state expressly because a bind that failed after credential
assignment leaves a bound entry for a re-placed session (`pkg/adapter/slotsession.go:326-340`), and
`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2` pins the registered-but-unbound case of the
drain gate (`pkg/adapter/slotsession_test.go:308`). Re-scoping predicates cannot be the whole remedy: the
§28.5.3 count deliberately includes registered-but-unbound entries and fails closed on purpose, and the
residue's credential file, its armed timers, and the third class's running runtime survive any predicate
change. The entry must actually be removed.

## Validated premises

Six lenses validated this problem independently, without seeing each other's work. Every finding below was
re-verified against the tree in this run. A refuted premise is kept with its refutation so a later reader does
not re-derive it.

### Premise lens — revise

- STANDS: the adapter creates a slot registry entry during the workspace-prep RPCs, before StartSession.
  `ensureSlotStateLocked` inserts unconditionally and its three production callers are the PrepareWorkspace
  staging resolver, FinalizeWorkspace, and RunSetup. Load-bearing.
- STANDS: `assignCredentialsSlot` binds the entry to the session ahead of StartSession and can then fail with
  the bind already written. The registry is keyed by session identifier on every path, so no slot/session
  divergence invalidates the claim. Load-bearing.
- STANDS: the gateway's only compensation is `ReleaseSlotReservation`, which sends the adapter nothing and
  touches only the SandboxClaim and the Redis slot counter. Load-bearing.
- STANDS, more strongly than the original statement claimed: nothing removes the entry. Exactly one
  `delete(s.slots, ...)` exists in the adapter, and every caller of it is a start-path rollback, the Shutdown
  handler, or a sweep filtered on `st.started`. The whole-pod recycle scrub enumerates disk, never `s.slots`.
  Load-bearing.
- STANDS: both named consequences are real. `claimPodMCPStartLocked` returns false whenever
  `len(s.slots) != 1`, and `boundRemains` is computed over every surviving entry with a non-empty
  `sessionID`. Load-bearing.
- REFUTED IN PART, load-bearing: "concurrent-pods-only, because the exclusive path reclaims the whole pod".
  The exclusive half stands. The concurrent half is over-broad: `ReleaseSlotReservation` passes recycle=false,
  `SlotClaimer.ReleaseSlot` deletes the per-pod claim at remaining==0, and the occupancy projection moves a
  claimed pod with no claim to draining, so a failed bind on a pod with no sibling occupant retires the pod
  and the residue dies with it. The trigger is a transient error on those stages on a co-tenanted pod.
- REVISED, load-bearing: "there are two residue classes" undercounts. The session-start stage's lost-or-
  cancelled-RPC sub-case leaves a third class carrying `started=true`, a raised `runtimeCohort`, and a running
  runtime. It needs a runtime close rather than a registry delete, and it adds the permanently-non-idle
  runtime and the refused intra-pod MCP calls.
- REFUTED (citation): "§4.6.1's inbound count". §4.6.1 is the Warm Pool Controller and states no inbound
  count. The rule the residue breaks is the §28.5.3 resolve-or-reject rule, which the adapter code cites for
  exactly this.
- REFUTED (count): "four of the six gateway failure stages". Five stage constants exist, and a server-side
  StartSession rejection is a sub-case of `slotFailureSessionStart` rather than a stage of its own.
- SUPPORTING: a residue left after a successful `AssignCredentials` carries armed §4.9 direct-mode
  lease-expiry timers, cancelled only in `deregisterSlotLocked`, so one will fire AUTH_EXPIRED for a session
  the gateway has re-placed elsewhere.
- SUPPORTING: on a recycling pool the residue survives the recycle boundary into later cohorts, with paths
  pointing at directories the scrub removed.
- CORROBORATION: the bound-but-unstarted residue is already an acknowledged condition in the tree.
  `hasStartedSession`'s doc comment names it as the reason the §10.1 hold arms on the started flag rather than
  the binding.

### Evidence lens — revise

- VERIFIED: the `ensureSlotPaths` doc comment is quoted verbatim in the statement, and it does call
  `ensureSlotStateLocked`, which creates and inserts the entry.
- VERIFIED: `pkg/adapter/slotcreds.go:32-34` is the bind, under a comment confirming it precedes
  StartSession, and the RPC entry passes `sessionID` as both arguments.
- VERIFIED: `ReleaseSlotReservation` carries both quoted phrases verbatim and reaches no adapter RPC.
  Load-bearing.
- VERIFIED: `deregisterStartedSessions` filters `st.started`, its only caller is the hold-timeout pass, and
  the other removal sites are the start-path rollbacks and Shutdown. Load-bearing.
- VERIFIED: the four post-connection stages each `cl.Close()` before returning, and the connect-stage returns
  all precede any workspace-prep RPC. Load-bearing.
- VERIFIED: `claimPodMCPStartLocked`'s guard, and the fact that the claim path registers the claimant's own
  entry before calling it, so any surviving residue makes `len(s.slots) >= 2`. Load-bearing.
- VERIFIED: the `boundRemains` drain gate behaves exactly as the two classes are drawn. Load-bearing.
- FALSE CITATION: "§4.6.1's inbound count" does not exist. §4.6.1 is "Warm Pool Controller (Pod Lifecycle)".
  The code names §28.5.3 for the count and §15.4.3 for the MCP arming. The error is inherited verbatim from
  proposal 0080 §1.2.
- FALSE COUNT: five stage labels exist, not six; the second "stage outside the problem" is double-counted.
- OVERSTATED: "each closes the client and returns with the adapter entry already created" is conditional for
  the workspace-prep stage, because the entry is created inside the PrepareWorkspace handler.
- VERIFIED with minor line drift: the exclusive path's reclaim comment sits just above the cited range and
  `failPhase` drains the sandbox, so the substance holds.
- VERIFIED: `boundSlotState` and `checkSessionBound` are where the statement says, and the fence path calls
  the former.
- ADDITION: on an SDK-warm pod, `claimSessionSlotUnderLock` refuses every later claim outright while any
  other entry carries a non-empty `sessionID`, so `StartSession`, `Resume`, and `ConfigureWorkspace` all fail.

### Prior-art lens — revise

- No proposal, landed or drafted, stages a remedy. 0080 stages no changes, 0073 recorded the gap and declined
  to discharge it, and the remediation programme's step list contains no step for it.
- The adapter-side removal mechanism already exists and is reachable through the existing idempotent
  `Shutdown` RPC, so no new RPC is needed. REFUTED in its original form, which read "with no proto change":
  the RPC carries no per-attempt identity, so an additive field on `ShutdownRequest` is needed to fence a
  reclaim that arrives after a retry took the slot.
- Reusing `Shutdown` unmodified is not a drop-in: full teardown on a bound-but-unstarted entry, and no
  `removeSlotTree` on an unbound one.
- §5.2's "Slot cleanup" already states the compensation rule for completion or failure; §6.2's per-slot
  sub-state machine has no edge into cleanup from `slot_assigned` or `receiving_uploads`, which is the
  failed-bind window. Load-bearing.
- REFUTES the §4.6.1 citation, on the same evidence as the other lenses. Load-bearing.
- REFUTES "any transient error on those four stages": the residue requires a co-tenant occupied at release
  time. Load-bearing.
- The §5.2 unhealthy threshold partially caps the compounding for failures clustered inside the rolling
  five-minute window.
- One consequence is wider than stated: the whole-pod scrub never clears `s.slots`, so the residue survives
  the recycle episode.

### Scope lens — revise

- One root cause, one map entry, one removal operation: the problem is singular and should stay whole.
  Splitting by residue class would produce two proposals editing the same `delete`. Load-bearing.
- The statement cuts by symptom and under-counts the symptoms. The residue trips at least five downstream
  sites: the §15.4.3 MCP arming, the §28.5.3 unaddressed-frame rule, the §15.4.2 drain gate, the on-disk slot
  tree and per-slot credentials file that `removeSlotTree` never reclaims, and the armed expiry timers.
  Load-bearing.
- The §4.6.1 misattribution matters for scope as well as accuracy: directing the proposal to establish the
  compensation rule there would land it in pod-lifecycle territory. Load-bearing.
- The proposal has a spec-authorship leg: §4.7.9 states the bind sequence as a linear happy path with no
  failure branch, and no section under spec/04, /05, /06, or /15 mentions the slot registry or a failed bind.
  Load-bearing.
- Three adjacent problems should be named out of scope: the exclusive path's abandoned-Prepare case, any
  retuning of the `ceil(maxConcurrentSessions/2)` threshold, and the session-versus-slot keying, which is a
  design constraint on the remedy rather than a separable problem.
- The "one remedy or two" question is answerable now and should be closed in the statement.

### Impact lens — revise

- The worst consequence is one the original statement never named, and it lands on a session already running:
  `slotCount` holds the §28.5.3 count above one permanently, so `deliverToSession` rejects the incumbent's
  unaddressed output for the rest of its life. No pod reuse is required. Load-bearing.
- REFUTES "every later session placed on that pod inherits it" as unconditional: when the failed slot was the
  pod's only occupant the pod retires and the residue dies with it. Later sessions inherit it only when a
  co-tenant was live at release time, or when the pool recycles. Load-bearing.
- The mainline concurrent start path carries no mitigation: `BindReservedSlot` releases the reservation,
  records nothing on the slot-health tracker, and never evaluates the §5.2 threshold. Load-bearing.
- The MCP consequence goes undetected: `materializeSlot` never calls `verifyIntegrationLevel`, which exists
  only on the exclusive Launch, so a session that arms no MCP surface is never flagged as underperforming.
  Load-bearing.
- The failure is invisible to operators by default; the one metric that moves sits outside the default scrape
  target set.
- Counterweight: on the retry path with a low concurrency ceiling the pod is retired on the first or second
  failure, which destroys the residue.
- Bounded in one place: the residue does not reach intra-pod MCP calling-session resolution for the first two
  classes, because `soleSession` reads the runtime cohort rather than the slot registry. It does reach it for
  the third class, whose entry raised that cohort.
- The exposed population is deployments that opted into concurrent sessions; the statement's concurrent-only
  scoping holds.

### Alternatives lens — revise

- The defect is confirmed; the lens does not refute it.
- The stated MCP consequence is also a symptom of a separate defect: `claimPodMCPStartLocked` gates on a raw
  entry count, so two interleaved binds on a healthy pod each observe two entries and neither arms the
  surface, with no failed bind anywhere, and nothing re-arms it afterwards. Load-bearing, and named out of
  scope here.
- The gateway-side compensation already exists as a shipped idempotent RPC, so the remedy needs no new
  mechanism. Load-bearing. REFUTED in its original form, which read "and no proto edit": the shipped RPC
  carries no per-attempt identity, so the remedy takes one additive field on `ShutdownRequest` and one on
  each request and each response of the bind sequence.
- The consequence that decides between remedy classes is the surviving credential file and its armed expiry
  timers, which no predicate change can fix. Load-bearing.
- The tree has already made the bound-to-started refinement twice, so "finish that refinement" is a third
  design option the statement should weigh rather than omit. Load-bearing.
- One remedy class is ruled out: the §28.5.3 count deliberately includes registered-but-unbound entries and a
  landed test pins that, so it cannot be re-scoped away. Load-bearing, and it carries the same §4.6.1
  refutation.
- The exposure is narrower on the retry path (the threshold retires a two-slot pod on its first failure) and
  wider on the create-time path (no health accounting at any concurrency).
- "Scoped to the life of the pod" is right for the in-memory entry but overstated for its on-disk half, which
  the whole-pod scrub reclaims at the occupancy-zero recycle boundary.
