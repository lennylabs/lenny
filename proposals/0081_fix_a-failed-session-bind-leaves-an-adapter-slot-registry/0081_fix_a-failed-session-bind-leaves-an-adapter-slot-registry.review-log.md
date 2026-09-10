# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

**Changelog (compaction pass 10).** Pass 10 read the whole ledger for this window: `spec.2` and
`spec.3`'s twenty-four lens shards, the four `f1` open-decision firings, the six `non-spec.1.fix-*`
groups, the fourteen `non-spec.1` lens shards (several filed twice under one id), the
`non-spec-recheck.1.fix-G1` correction block, the `spec.1.fix-bind-epoch` correction chain, the
`non-spec.1.fix-schema-1` correction, and the three index-and-checklist reconciliation passes.
Lifted: twenty-six new Settled entries, seventeen new Traps, thirteen new Open items and four new
Deferred entries. Applied five CORRECTS against the standing context. (1) The generated proto
package is `pkg/proto/adapter/v1`; every entry saying `pkg/gen/adapter/v1` is rewritten, and the
client-surface absence sweep that grepped `pkg/gen` had a vacuous arm. (2) Under the reverted
per-entry epoch, `ShutdownRequest` is the only message carrying both the epoch and
`coordination_generation`, so the corrected §4.7.1 form is the bare "as on `Shutdown`"; the
"as on `Resume` and `Shutdown`" form is stale and must not be restored. (3) `## Open decisions for
human to make` now carries entries 11, 15, 16, 17 and 18; entry 12 was resolved and deleted, so
every entry saying "11 and 12" is rewritten. (4) The checklist was resequenced and renumbered
(SPEC-5 leads at S1), which closes the S1/S5 forward-reference Open and stales every prose step
reference written against the old numbering. (5) The r1 fix pass's claim that
`checkpoint_stream_wire_test.go` carries no closed field-set pin is false; it pins `CheckpointStart`
to six fields, and the surviving true half is that both its pins are over messages SCHEMA-1 does
not open. Retired as closed: seven Open items (the hold's own timeout, the `isTransientPodClaimError`
arm, the S1/S5 forward reference, the `DemoteSDK` latch clear, `compensateFailedSlotBind`'s
signature, and the two spec-map items) and eleven Deferred entries applied by the reconciliation
passes and the non-spec fix groups. Did NOT reach 691 lines: this section is about 895, and it grew
rather than shrank, because this window opened the non-spec lane for the first time since the hand
amendment and its fourteen lens shards landed a whole surface (the generated registries, the tier-0
gates over them, the checklist resequence, and DOCS-2) that no earlier pass had any entry for. The window's
most expensive failure was nine lenses over seven sweeps checking proto field-number FREENESS and
missing the shipped tier-3 closed-field-set gate that the same fields turn red; the Traps that
record which gate reads which artifact, which registry is generator-produced, and which of the
refuted families is already repaired are what stop that repeating, so they stand at length.

### Settled

- **Adapter predicate nesting.** Entry present (`s.slots[id]`) ⊃ bound (`st.sessionID != ""`, set by both `assignCredentialsSlot` and `claimSessionSlotUnderLock`) ⊃ started (`st.started`, set only in `claimSessionSlotUnderLock`) ⊃ `runtimeLive` (set only by `noteRuntimeStarted`). EVIDENCE: pkg/adapter/slotcreds.go:32-34; slotsession.go:87-88; runtimegeneration.go:35-48; session.go:111,156,163.
- **`st.started` precedes `Runtime.Start`, and it has THREE setters.** `claimSessionSlotUnderLock` sets it at slotsession.go:88 and has three production callers: `StartSession` (session.go:111), `Resume` (resume.go:50) and SDK-warm `ConfigureWorkspace` (sdkwarm.go:217). All three also reach `noteRuntimeStarted` (session.go:163, resume.go:144, sdkwarm.go:261, the last guarded on `fresh`). On the start path `Runtime.Start` runs at :156 and `noteRuntimeStarted` at :163, and the gap spans `sessionConnectors`, `writeSessionManifest`, `startPlatformMCP` and `startConnectorMCPServers`, so it is a wide window rather than a hairline race. Pass 1 named only the `StartSession` caller, and the one-caller reading misled a fix round into naming `StartSession` in a spec predicate.
- **"The pod's shared runtime process has been given" is bound vocabulary.** It denotes `runtimeLive`/`runtimeCohort` membership in spec/04:962, spec/15:1734, spec/28:1194,1242,1735, spec/29:308,1521, docs/runtime-author-guide/integration-levels.md:99 and platform-tools.md:23, and pkg/adapter/runtimegeneration.go:8-25 defines it verbatim.
- **`runtimeLive`'s writers are exactly two.** `noteRuntimeStartedLocked` and `noteRuntimeClosed`; there is no third. EVIDENCE: pkg/adapter/runtimegeneration.go:36-49,:58-68.
- **DECISION: two predicates, not one.** The runtime teardown gates on `st.started` (over-approximates, fails closed toward closing); the cleanup-outcome report gates on `runtimeLive` membership through a new `runtimeHoldsLocked` accessor (fails closed toward not counting). CODE-1 is the first caller of that accessor.
- **DECISION: the teardown precondition names no RPC and is stated in one place.** §4.7's `Shutdown` row is the single home and reads "a session whose start the adapter has admitted", plus one sentence saying which RPC in that table starts a session depends on the pod's session mode and on whether the session is new or resumed. §4.1 points at §4.7 and restates nothing; every other site says "a start". The round-1 wording "a session whose `StartSession` the adapter has admitted" was wrong for a resumed session and for an SDK-warm pod, and enumerating the three RPCs was rejected as a closed enumeration repeated at six sites that goes stale on a fourth. The "has been given" phrase survives only where it denotes cohort membership (§6.2 `running`, the served-session count, §4.7 item 7).
- **DECISION: "the moment the adapter admits the start", not "the claim that admits the start".** `claim` in this specification denotes the `SandboxClaim` CRD, so the design's prescribed wording would have collided.
- **`RecordSessionScrub` has no per-session dedup.** It discards the session identifier and calls `IncrementSessionsServed` unconditionally before branching on `leaked`, then drives `maxSessionsPerPod` retirement off the post-increment count. Two reports for one session are two served sessions plus one bogus leak-ledger entry. EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481.
- **`ReportSessionScrub` is one channel carrying two things.** `IncrementSessionsServed` and `DrainLedger.RecordSessionScrub(..., leaked)`. Withholding the report withholds both, so suppressing it to protect the served-session count also suppresses the leak signal.
- **DECISION: the refused `StartSession` reports no outcome.** CODE-2's second `reportSessionScrub` call and the `errStartRaceReclaimed` sentinel are deleted. The adapter is the only place the at-most-one invariant can hold.
- **DECISION: one reporting rule, one home.** The withheld-report rule and the one-report-per-session-release rule are stated once, appended to spec/05 §5.2's `**Scrub model.**` paragraph (spec/05:453), which opens "uniform across session-mode configurations". SPEC-2 and SPEC-4 cite it rather than restating it.
- **§5.2's `**Slot cleanup:**` bullet is concurrency-scoped.** The bullet at spec/05:545 sits under the heading `**Slot failure and cleanup (`maxConcurrentSessions > 1`).**` at :542, so anything landed in it does not exist for a single-session pod. So is the `**Slot retry policy**` block at :553.
- **§5.2's action list is three actions.** Workspace directory, process group, `slotId`. It names neither the per-slot credential file nor the §4.9 expiry timers, while §5.2:455 ninety lines earlier already presupposes the wider list, and the code does both (`slotlayout.RemoveTree` sweeps `CredentialsDir` at tree.go:58-68; `deregisterSlotLocked` cancels every armed timer at slotsession.go:174-188). The code is ahead of the spec here.
- **DECISION: widen §5.2's action-list sentence (SPEC-3's first anchor)** rather than enumerate the slot release's actions in the §4.7 `Shutdown` row. One canonical list survives and §5.2 stops being incomplete for the ordinary session end too. No code deliverable changes, because both actions are shipped behaviour.
- **§6.2's per-slot fence is two blocks, and the split is load-bearing.** `running ──→ failed` and `slot_cleanup ──→ leaked` sit under "Per-slot sub-states scoped to concurrent occupancy" (spec/06:146-148); the other four sit under "a pod of either concurrency" (:150-156). docs/reference/state-machines.md:251 mirrors the scoping and a tier-11 test pins the pair. `pkg/sandbox/slotstate` ignores the split and holds all six edges in one set.
- **`{Running, SlotCleanup}` is already in `ValidTransitions()`,** so a session the runtime was given needs no new edge and CODE-3 stays the single added edge. EVIDENCE: pkg/sandbox/slotstate/slotstate.go:105-113.
- **The per-slot sub-state machine is a contract model.** Nothing in production drives `slotstate.Registry.Transition`; only `MarkLeaked`, `MarkReleased` and `ForgetPod` are called. A missing edge cannot break a runtime path today. EVIDENCE: pkg/sandbox/slotstate/registry.go:12-20; pkg/gateway/sessionserver/start.go:2833-2856.
- **§6.2's `receiving_uploads ──→ running` annotation** reads "workspace ready, session dispatched to runtime with its session identifier". DECISION: the staged new-edge annotation is left mirroring that vocabulary; rewording it would cascade into DOCS-1's row and CODE-3's doc comment.
- **DECISION: the `running` boundary is the runtime having been given the session,** with a start still in flight named on the `receiving_uploads` side, stated identically in the SPEC-4 fence annotation, the SPEC-4 prose, the Design paragraph, CODE-3's doc comment and DOCS-1's table row. UNVERIFIED: this reading and `spec.1.fix-G1.1`'s decision to leave the fence annotation as staged were written in different entries and are not obviously the same sentence; a later reviewer should confirm the four sites agree.
- **DECISION: the `leaked` disposition is scoped to the pod class that offers it.** §7.1, the Design paragraph and the edge-case bullet now assign an unacknowledged reclaim the `leaked` sub-state and the §5.2 replacement trigger only for a pod serving concurrent sessions; on an exclusive pod the failed attempt releases the pod's claim and the pod retires under §6.2's pre-attached failure disposition.
- **DECISION: the no-retry clause is replaced by a placement rule.** The reclaiming pod is disqualified from carrying another attempt at the same session; the §5.2 retry itself is kept. Staged as an `ExcludePod` field on `podsession.SlotBindRequest` and `podclaim.SlotRequest`, read by `ClaimSlot`'s two candidate passes. Nothing becomes non-retryable, so §5.2's three non-retryable reasons, §6.2's pre-attached retry policy, §15.1's error rows and docs/reference/error-catalog.md all stay true verbatim.
- **DECISION (superseded in round 2): §7.1 names no carrier and states no placement rule.** The carrier enumeration cannot be completed, because a §15.1 `/start` onto a create-time-reserved slot is pinned to `row.PodAssignment` and enters neither `applySlotRetryPolicy` nor the §7.3 idle claim. §5.2's `**Max retries:**` bullet now states the constraint itself, scoped to the retries that policy places, and the create-time-reserved start is recorded as an accepted failure mode rather than governed. §7.1 keeps the reclaim, its `leaked` disposition, the exclusive-pod case, the tree hazard, and a §5.2 pointer.
- **DECISION: the reclaim obligation is its own §7.1 paragraph.** Lifted out of the "Atomicity of session creation (steps 2–8)" paragraph into a paragraph immediately after it, re-scoped to any gateway bind attempt (creation finalize block, §15.1 start, §7.3 re-attach), with §7.3's resume flow, §6.2's mid-resume cancel bullet and §5.2's `**Max retries:**` parenthetical as one-sentence pointers.
- **Two sections own retryability, each with a closed enumeration.** §5.2 "Non-retryable failure categories" (spec/05:555-560, three reasons, plus the "Client error on exhaustion" two-way disjunction and the "Fresh workspace guarantee") and §6.2 "Pre-attached failure retry policy" (spec/06:283-290, two reasons). spec/15:1136 adds "Any other setup-window failure ... is recovered with a fresh pod". Any future rule that makes a class non-retryable must edit all of them.
- **`ClaimSlot` already has the sentinel an exclusion needs.** It returns `ErrNoConcurrentSlot` (mapped to `WARM_POOL_EXHAUSTED` with `details.reason: "concurrent_slots_exhausted"`) whenever the pool list is non-empty and no candidate is placeable, so an exclusion that empties the candidate set needs no new sentinel and no new reason value. EVIDENCE: podclaim/slotclaimer.go:516-522; podsession/slotbinder.go:430-441.
- **The exclusion is one `continue` per pass.** `ClaimSlot` has exactly two candidate loops (pass 1 same-tenant claimed pods, pass 2 fresh idle pods), each already using `continue`-based skips, and `expiredByUptime` is the existing read-only skip precedent. EVIDENCE: podclaim/slotclaimer.go:336-357,:416-478.
- **The placement-filter test tier is 2, not 1.** The precedent pair `TestClaimSlotSkipsOverUptimeClaimedPod_spec_6_2` / `...IdlePod...` is in the envtest-backed slotclaimer_test.go; maxpoduptime_test.go in the same package is fake-client tier 1 and is not the precedent to copy.
- **`applySlotRetryPolicy` takes the shipped `req podsession.SlotBindRequest` by value,** and CODE-5 changes it to `req *podsession.SlotBindRequest` so the exclusion survives a `queue` pool's re-entry of the same closure; the request value still belongs to one client request, so the exclusion is scoped to that request's remaining attempts. EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884; pkg/gateway/sessionserver/queue.go:143-146,:205.
- **`SlotID == SessionID` on every path** and the per-slot tree is `/workspace/slots/{sessionId}/`, so a §5.2 retry of the same session landing on the same pod is the same adapter registry key and the same on-disk tree. §5.2's "Fresh workspace guarantee" silently assumes a distinct slot and does not hold there.
- **`SlotClaimer.ReleaseSlot(leaked=true)` returns early:** no Redis decrement, no claim DELETE, no recycle signal, no occupancy-zero disposition. So "releases the slot reservation" and "the leaked disposition holds occupancy" are compatible rather than contradictory, and a leaked release never reaches the `DeleteClaim` that retires the pod today. EVIDENCE: podclaim/slotclaimer.go:830-843,:882-885.
- **Today's `ReleaseSlotReservation` hard-codes `ReleaseSlot(ctx, sandboxName, false, false)`,** so a failed bind on a pod with no co-tenant deletes the claim and the pod retires. That is the safety valve the problem statement relies on, and CODE-4/CODE-5 remove it in exactly the failure case. EVIDENCE: podsession/slotbinder.go:493-503.
- **The `leaked` disposition already exists end to end.** `Binder.ReleaseSlot` sets `leaked = err != nil || !cleanly` on the adapter `Shutdown`, and the gateway (not the adapter) emits `lenny_adapter_leaked_slots` and already calls `slots.MarkLeaked` from `applySlotRetryPolicy`. Nothing new is needed for §6.2's "leaked holds occupancy". EVIDENCE: podsession/slotbinder.go:536-559; gatewaymetrics_credential.go:219-223.
- **The §5.2 leak/failure ledger is replica-local.** `Tracker.leaked` and `Tracker.events` are in-process maps with `UnhealthyThreshold = (maxConcurrent+1)/2`, backed by neither Redis nor Postgres. EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:56-67,:215-221.
- **The churn change starts at concurrency 3.** At `maxConcurrentSessions: 2` the windowed `RecordFailure` alone reaches `ceil(2/2)=1` and drains the pod inside `applySlotRetryPolicy` before the retry iteration, so the persistent-leak disposition changes pod churn only at 3 and above.
- **`Shutdown` is unfenced.** `ShutdownRequest` carries `coordination_generation` (schemas/lenny-adapter.proto:1630-1635) and the adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`. The compensating `Shutdown` is therefore not refused by the §10.1 fence, and the generation is per-session-coordinator anyway, so it does not advance between two attempts of the same session on one replica and cannot fence a lagging `Shutdown`. Closes an earlier UNVERIFIED.
- **`Shutdown` is also one of the few adapter handlers with no `checkSessionBound` guard** (pkg/adapter/session.go:227 versus attach.go:41, lifecycle.go:30, usage.go:266), so SPEC-1's "answers with a clean-exit response" sentence is reachable today and needs no fence carve-out.
- **The shipped `Shutdown` handler gates the whole teardown on `bound := removed && st.sessionID != ""`,** and gates only the §15.4.2 drain signal additionally on `!boundRemains` ("some surviving entry has a non-empty sessionID"). EVIDENCE: pkg/adapter/session.go:238-241,259.
- **`ShutdownResponse` is `{exited_cleanly, exit_code}`,** the adapter sets `ExitedCleanly: closeErr == nil` from the runtime close alone and discards the tree-removal error (`_ = removeSlotTree(st)`), and `exited_cleanly` is defined nowhere in `spec/` or `docs/`. SPEC-1's clean-exit answer needs no proto field and no SDK change; programme rule S-2 bars widening the response with one.
- **No per-slot sub-state is client-visible.** SPEC-4's new edge has exactly two parallel representations outside spec/06: docs/reference/state-machines.md's per-slot table (DOCS-1) and `pkg/sandbox/slotstate` (CODE-3). There is no OpenAPI, SDK or CRD mirror. EVIDENCE: pkg/api/v1/session/session.go:9-13; spec/15:672.
- **`stageWorkspace` sends `PrepareWorkspace` only under `if len(uploads) > 0`,** so an upload-free plan reaches `FinalizeWorkspace` as the first adapter RPC. EVIDENCE: podsession/binder.go:1323-1330.
- **The occupancy-zero recycle `Shutdown` is a second RPC reusing the just-released session's identifier,** so it always names a session the adapter holds no entry for and its clause three runs the whole-pod scrub regardless. Any blanket "a request naming a session the adapter holds no entry for removes nothing" rule must except it.
- **The adapter's `Resume` reaches `Runtime.Start` and `noteRuntimeStarted` too,** so a lost or cancelled resume leaves the same class-3 residue as a lost `StartSession`. EVIDENCE: pkg/adapter/resume.go:50,:140-144.
- **The `SandboxClaim` a leaked reclaim leaves `bound` is not undeletable.** §4.6.1 orphan-GC predicate 1 drains a `bound`/`recycling` claim older than `claimOrphanTimeout` whose pod no active session references, so a level-triggered backstop exists.
- **Ownership is clean.** The gateway owns `SandboxClaim.spec` and `.status` and routes the unhealthy-threshold drain through the `lenny.dev/drain-request` annotation rather than writing `Sandbox.status`. No component writes another's status subresource and no finalizer or SSA force-ownership is involved.
- **The anchor sweep is done, and it now covers SIXTEEN sites rather than the six pass 1 named.** Rounds 2 to 6 minted the whole §7.2 block, §7.3's list tail, §6.2's `resuming → cancelled` clause, §5.2's `**Max retries:**` and `**Slot cleanup:**` sentences, and the §29.4 step-13 tail. Every "text to replace" block matches the tree byte for byte and occurs exactly once, and every "replace it with" block occurs zero times: spec/04:157 (§4.1 third sentence), :686 (§4.7 `Shutdown` row opening), :854 (§4.7.9 step 5); spec/05:453 (`**Scrub model.**`), :545 (`**Slot cleanup:**` action list), :555 (`**Max retries:**`); spec/06:150-153 (fence heading and the `receiving_uploads ──→ running` entry), :156-158 (fence close before `**\`reserved\` hold semantics.**`), :234 (`resuming → cancelled` clause); spec/07:23 (atomicity parenthetical) and :24 (the continuation line the new paragraph inserts before), :210 (§7.2 preamble premise), :213 (§7.2 step 2), :214 (§7.2 step 3), :414 (§7.3 list tail); spec/29:706-711 (§29.4 step 13). Roughly twenty lenses have now re-derived this mechanically, several of them by script over the fenced blocks, and every one came back clean; the count they report varies between twelve and sixteen only because some count fenced blocks and others count sites. Do not re-run it unless spec/ moves, and extend this entry when a fix round mints a new anchor rather than leaving the next reviewer to notice the list is stale. The §29.4 anchor is the one exception and it is non-unique INSIDE §29.4 as well as repo-wide: the same string closes step 6 at spec/29:645 and step 13 at :711, so the instruction resolves only through its "In §29.4's numbered step 13" scoping.
- **Every markdown anchor the staged text mints resolves.** `#47-runtime-adapter` (spec/04:657), `#479-startup-sequence-for-type-agent-runtimes` (:848), `#49-credential-leasing-service` (:1099), `#52-pool-configuration-and-execution-modes` (spec/05:365), `#62-pod-state-machine` (spec/06:78), `#71-normal-flow` (spec/07:3), `#72-interactive-session-model` (:115), `#73-retry-and-resume` (:378), `#151-rest-api` (spec/15:614), `#1542-rpc-lifecycle-state-machine` (:1686). No anchor is minted that spec/ does not already use.
- **§7.1's atomicity paragraph lives inside the fenced code block spanning spec/07:5-54.** Pre-existing, and not a defect of this proposal.
- **USEFUL (hand-off notes).** G1 and G2 each reported which sentences inside the SPEC-2 block they had already rewritten, which is what let G3 relocate and re-scope the block as a unit with their edits intact and append to the "Spec files touched" spec/05 entry rather than rewrite it, preserving SPEC-3's two anchors. It also let a later group discover that the design's line numbers had drifted and anchor on quoted strings instead of re-reading the file. Keep doing this.
- **Three RPCs admit a start, and §4.7's table carries all three.** `StartSession` (pod-warm, spec/04:672), `ConfigureWorkspace` (SDK-warm, :673) and `Resume` (:684) are all rows in the §4.7 Gateway→Adapter table, so the staged row's "which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed" resolves inside the table it names. spec/07:32-34 says SDK-warm pods skip `StartSession` outright, which is why naming that RPC in a predicate excludes every preConnect and resumed session.
- **The §7.3 re-attach claims from idle inventory.** `Binder.Resume` → `b.connect` → `podclaim.Claimer.Claim`, which takes only a Sandbox at `Status.Phase == Idle` (plus a same-tenant `reserved` rebind) and then CREATEs the deterministic per-pod claim; `ErrNoIdlePod` falls through to the §4.6.1 Postgres fallback, which re-reads the live Sandbox and refuses anything past idle. It never calls `ClaimSlot`. A pod holding a leaked slot keeps its `bound` claim, because `ReleaseSlot(leaked=true)` early-returns before `DeleteClaim`, so it is outside that candidate set with no `ExcludePod` needed. Nine lenses derived this independently; it closes the pass-1 OPEN "Resume path and the exclusion". EVIDENCE: podsession/binder.go:1590-1606,:1737-1768; podclaim/claimer.go:118-160.
- **That exclusion does not depend on Redis.** The reclaiming pod's per-pod `SandboxClaim` lives in etcd, so the §7.3 exclusion survives a Redis reset and the §12.4 rehydration hole does not reach it.
- **`ClaimSlot` pass 1 scans same-tenant CLAIMED pods with free capacity; pass 2 scans idle pods.** On the §5.2 bind path a leaked slot's held occupancy therefore filters nothing, and the exclusion there is the in-memory `ExcludePod` field alone. Do not generalise the idle-inventory argument to the bind path. UNVERIFIED: `spec.5.review-mechanism.1` states the stronger claim that held occupancy makes the pod *claimed* and therefore pass 1's own candidate, which reads as contradicting the §7.3 entry above; the two are reconcilable only if the resume path never reaches `ClaimSlot`, which every other lens asserts. Somebody should confirm the two entries are about different paths.
- **The three attempt kinds do not map onto three code paths.** A §7.3 resume-rebuild and a slotless row both reach `applySlotRetryPolicy` through `bindConcurrentSlot`; a create-time-reserved row takes `BindReservedSlot` with no retry loop. Each request builds a fresh `SlotBindRequest`, so `ExcludePod` never crosses a request boundary.
- **`applySlotRetryPolicy` wraps `binder.BindSlot` only,** and branches on `*podsession.SlotBindError`, so it governs BIND failures rather than only adapter-reported failures of a running slot. `BindReservedSlot` (start.go:2596) and `ClaimSlot` (:2148) are called directly and never traverse it. `maxSlotRetries == 1` bounds one invocation to two attempts, but a `queue` pool re-enters the whole closure with the budget starting over (queue.go:143-146,:205), so a single-valued exclusion field is NOT sufficient: a second unacknowledged reclaim on a second pod would replace the first name. CODE-5 stages `ExcludePods []string` and appends.
- **`BindReservedSlot` re-runs the whole `materializeSlot` sequence** (stage → finalize → setup → credentials → `StartSession`) against the persisted `row.PodAssignment` and `row.ID`, so a retried §15.1 start reaches the same pod, the same slot id and the same on-disk tree. Its failure keeps the row `ready` and returns a retryable `STARTING_FAILED` with a `Retry-After`, and nothing in `pkg/gateway/sessionserver` clears `PodAssignment`.
- **A create-time-reserved slot exists only on a concurrent pool.** `bindConcurrentSlot` is the sole caller of `BindReservedSlot` and is reachable only under `match.MaxConcurrentSessions > 1`; the exclusive create-time binding goes through `Binder.Launch`'s reconnect instead.
- **The slot routes are concurrency-gated end to end.** Every one sits behind `match.MaxConcurrentSessions > 1` (start.go:2139, :2351, :2465), and the exclusive path goes through `failPhase`, which drains the pod. So `ReleaseSlot(leaked=true)`'s early return can never falsify §7.1's "the pod retires" sentence, and §7.1's exclusive branch is grounded in the pool setting rather than in momentary occupancy.
- **`Launch`'s reconnect failure returns the pod to the pool rather than draining it,** through `ReclaimClaimed` (binder.go:977-991), but that failure precedes the attempt's first session RPC, so §7.1 owes no reclaim there. `ReclaimClaimed` sends no `Shutdown` at all: it deletes the per-pod claim and revokes the §4.9 lease, leaving the adapter's slot entry untouched on a concurrent pod.
- **`SlotClaimer.ReleaseSlot`'s `recycle` parameter, not `leaked`, patches the claim `bound → recycling`,** arms the missing-report timeout and returns `recycled`. `ReleaseSlotReservation` passes `recycle=false`, so a failed bind that drives occupancy to zero DELETEs the claim and the pod retires rather than recycling. That bounds the pass-1 "recycle boundary on a failed bind" question.
- **`Binder.Resume`'s failure branch releases nothing on an exclusive pool.** `reserveResumeSlot` returns `""` when `MaxConcurrentSessions <= 1` and `releaseResumeSlot` no-ops on an empty slot id; `resumeOnPod` returns the error with no rollback, and `holdOrFailOnResumeError` touches only the session row. A failed §7.3 re-attach therefore leaves the replacement pod `claimed` until §4.6.1 orphan GC.
- **§7.2 owns the mid-resume close sequence.** "Mid-resume terminal transitions — snapshot-close semantics" (spec/07:210-216) is the five-step sequence BOTH §6.2 `resuming` bullets defer to, and its step 3 carried the premise this proposal overturns ("no runtime was started on it, so no scrub beyond the pool's default post-session scrub is required"). SPEC-2 stages three §7.2 edits.
- **§7.2's sequence is only partly implemented.** Steps 4 and 5 exist (`bumpCoordinationGenerationOnSnapshotClose`, `recordSessionCompleted`); steps 1 and 3 have no implementation on the DELETE path. Correcting step 3's text therefore creates no code obligation and needs no code deliverable.
- **Neither §6.2 `resuming` bullet enumerates §7.2's sequence.** Both omit step 4's `coordination_generation` bump, which is why the sibling bullet's "abort / skip-seal / release-replacement-pod / run-terminal-handling" gloss is a pointer rather than a list that must grow, and why the `resuming → completed` bullet needs no edit when the reclaim joins the sequence.
- **The mid-resume reclaim rides the aborted attempt's own still-open connection,** under a detached context, before `cl.Close()` and the release. That is why it belongs at step 3 rather than step 1, and it is what makes "sends on the connection that attempt still holds" implementable.
- **§6.2's pod fence retires a failed pod.** `claimed ──→ draining (terminal claim disposition released or failed)`, `failed ──→ draining`, `draining ──→ terminated`, with no `failed → idle` edge, and spec/05:455 says a session that ends in failure always retires its pod. The prose at spec/06:283 ("marked `failed` and released back to the pool") is the loose statement and the fence governs.
- **A `leaked` slot cannot wedge a pod's retirement.** `draining ──→ terminated` is triggered by "pod replacement provisioned from warm pool" and is not gated on occupancy reaching zero, so occupancy held forever delays nothing. §4.6.1 orphan GC drains such a claim rather than returning the pod to idle.
- **DECISION: §7.1's obligation window ends when the attempt SUCCEEDS,** rather than when it "has the session running on the pod". `running` is bound vocabulary pinned by SPEC-4, so the old clause ended the obligation exactly where the next sentence says it must not.
- **DECISION: §7.1 states no cleanup-outcome report rule at all.** Its closing sentence was deleted outright rather than scoped. The report rule, its pre-`running` exception, its complement and the one-report-per-release rule are all SPEC-3's, appended to §5.2's `**Scrub model.**` paragraph, which the other sections cite.
- **DECISION: SPEC-3's §5.2 append states the complement.** "A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome." It is the spec statement of the staged code predicate `live := removed && s.runtimeHoldsLocked(sessionID)`, and it exists because stating only when the report is withheld left the positive half to implication.
- **DECISION: the SPEC-3 range is one canonical phrase,** "abandoned or fails after its slot enters `receiving_uploads` and before it reaches `running`", written identically into the staged §5.2 append, the Design rationale, and summary.md's What-changes bullet and SPEC-3 deliverable-index line. SPEC-4 adds an edge only out of `receiving_uploads`, and the `slot_assigned` case is deliberately left open.
- **DECISION: §7.2 takes three edits.** The preamble's premise sentence is deleted whole and the reason for skipping the live seal moves into step 2, which owns the decision; step 2 gains one sentence saying the aborted re-attach's pod-side state is reclaimed in step 3 rather than sealed; step 3 is replaced.
- **DECISION: the create-time-reserved retry bullet is keyed on acknowledgement.** The "bounded on both sides" claim was deleted and both arms re-keyed on whether the adapter acknowledged the reclaim, because the adapter's refusal is `st.started` while the gateway's `leaked` is `err != nil || !cleanly`, which made the old second arm exactly backwards and the old first arm false for every pre-start stage. The bullet's charter is to RECORD a residue no layer governs, and a record needs no bound.
- **DECISION: SPEC-1 stages a third block, on spec/29.** One sentence appended to §29.4 session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7, and `spec/29_communication-scenarios.md` joins "Spec files touched". §29.4 step 12 is left unedited.
- **The drain gate and the runtime close have different preconditions in shipped code.** `Shutdown` sends the CH-RUNTIMEOPS drain only under `if !boundRemains`, then closes the runtime unconditionally for a bound entry. No `spec/` text stated the drain gate before this proposal: it lives at docs/reference/adapter-contract.md:75 and in code, which is why applying SPEC-1 creates a new spec-internal contradiction rather than exposing an old one.
- **`terminate` is the only §29 site that emits the graceful-shutdown signal.** §29.7 (drain) and §29.9 (eviction) carry checkpoint frames, so SPEC-1's single §29 site is the complete §29 mirror for the new condition.
- **The `terminate` frame carries `type`, `deadlineMs` and `reason` and no session field,** on the per-pod abstract socket `@lenny-runtime-ops`, so "the signal is pod-global and names no session" is accurate. The Basic/Standard stdin `shutdown` frame is also pod-global (§28.5.3's CH-MSGSOCK card excludes `heartbeat` and `shutdown` from the session-addressed set), so the claim holds at every integration level.
- **`ShutdownRequest.reason` is a plain `string` with no enumeration,** so the code lane's `"slot_bind_failed"` mints no wire value and needs no proto or SDK edit. The closed four-value enum belongs to the intra-pod `terminate` frame only, and `drainReason` normalises anything unrecognised to `session_complete`.
- **§29's preamble subordinates a trace on DISAGREEMENT and says nothing about omission.** So §29.2's silence on the finalize-block failure branch and §29.6 step 4's `RESUME_FAILED` branch are omissions rather than edit sites. It is also not a licence to leave a trace stale: this proposal edited step 13 rather than relying on the resolution rule, so an argument that "§29 is only a restatement" has to explain why step 13 was edited and a sibling was not.
- **§29.2's atomicity restatement covers its own steps 2-10,** which end at the `session_id` return, and the first pod-side RPC in that trace is step 15 `PrepareWorkspace`. So the widened §7.1 parenthetical quantifies over an empty set there and spec/29:200 stays accurate unedited.
- **`spec/16_observability.md` needs no edit and can orphan no alert.** There is no metric or alert row for leaked slots, per-slot cleanup or session scrub; the rows that touch the surface (`lenny_gateway_pod_retirement_total` :12, `lenny_slot_failure_total` :14, `lenny_slot_pod_replacement_total` :15, `lenny_pod_session_reuse_count` :128) all stay true, and `WarmPoolExhausted` keys on the `lenny_warmpool_idle_pods` gauge rather than on the client-visible error code.
- **`ReportSessionScrub` has no missing-report timeout,** unlike `ReportPodScrub`, so a withheld report trips no gateway-side watchdog and produces no spurious retire. It is also emitted once with no retry, so the at-most-one invariant is not defeated by redelivery even though `RecordSessionScrub` has no dedup key.
- **`slotlayout.EnsureTree` creates five directories per slot and `RemoveTree` removes four trees** (slot root, `/sessions/{id}`, `/artifacts/{id}`, `CredentialsDir`). §6.4 names three of them and defers removal to §5.2; neither §5.2's whole-pod scrub steps 1-6 nor step 0 sweeps `/sessions/` or `/artifacts/`, so the per-slot cleanup is the only spec'd reclaim for them.
- **SPEC-3's two added actions are shipped and correctly qualified.** `RemoveTree` sweeps `p.CredentialsDir` and `deregisterSlotLocked` cancels every armed provider timer before deleting the entry. §4.9's timer is direct-delivery-mode only (spec/04:1169), so the qualifier is exact and the clause is vacuous rather than wrong in proxy mode. §5.2:455 and scrub step 0 already presuppose the wider list, so the widening is a catch-up rather than a new obligation.
- **§4.7.9 step 5 is `PrepareWorkspace → FinalizeWorkspace → RunSetup → AssignCredentials → StartSession`.** The connect stage sits outside it: `connectSlot` does only `resolveSandbox`, `DialAdapter` and `NegotiateVersion`, which is pod-scoped and creates no adapter entry. So "a bind abandoned at the connect stage owes no reclaim" is sound, and the narrowed SPEC-3 range and §7.1's "first pod-side RPC" name the same boundary.
- **On the create-time-reserved path every stage that can fail before `StartSession` is pre-start,** so `st.started` is false for each and `claimSessionSlotUnderLock` admits the retry. The adapter refuses a repeat start on `st.started` alone, never on entry presence and never on the binding.
- **`Shutdown`'s entry removal and its tree removal are two moments.** `deregisterSlotLocked` runs under `s.mu`; `removeSlotTree` runs afterwards outside the lock, synchronously inside the handler before the response is built. Because `compensateFailedSlotBind` blocks on `cl.Shutdown`, an ACKNOWLEDGED reclaim has finished its tree removal by the time the gateway returns the error to the client.
- **The `Shutdown` handler performs no context-expiry check,** so a reclaim whose gateway-side deadline has already lapsed still executes destructively when it reaches the handler: it removes the entry, cancels the timers, closes the runtime for a started entry and removes the tree.
- **The leak disposition is keyed on the gateway's acknowledgement,** `leaked = err != nil || !cleanly`, rather than on what the adapter did with the entry, so an entry the adapter removed and acknowledged is released. `applySlotRetryPolicy` sets `ExcludePod` under `sbe.Leaked || relErr != nil`, a third arm no spec sentence names.
- **`SocketRuntimeProcess.Start` has no acknowledgement step.** It adds the session to the active set and returns, and it returns nil immediately when `p.connected` with no ctx check, so a racing `StartSession` on a cancelled context still starts the session. That is what makes both orderings of the reclaim-versus-claim race reachable rather than theoretical, and it is why "whose session the runtime has not yet acknowledged" names a handshake the platform does not have.
- **`ensureSlotStateLocked` calls `slotlayout.EnsureTree` BEFORE registering `s.slots[slotID]`,** and `EnsureTree` has no rollback, so a mid-way failure leaves directories on disk with no registry entry, which the staged §4.7 no-op rule then certifies as clean. Judged inert: the directories are empty and `MkdirAll` is idempotent on the retry.
- **A retry inherits the slot TREE and nothing else.** `assignCredentialsSlot` rewrites `st.creds` and the on-disk credential file wholesale from the request's lease set, `reconcileSlotExpiryTimersLocked` cancels timers for absent providers and re-arms the rest keyed on lease id, and `onSlotLeaseExpired` re-reads the slot state and returns unless the captured lease id still matches.
- **The retried start runs on occupancy the counter no longer counts.** `BindReservedSlot` releases the create-time reservation on failure through `ReleaseSlotReservation` → `ReleaseSlot(..., false, false)`, a real `active_slots` decrement, while the retry reconnects to the persisted binding and re-reserves nothing. Pre-existing and outside this proposal.
- **The reclaim's deadline is bounded.** The code lane wraps `context.WithoutCancel(ctx)` in `slotCleanupBudget`, a `WithTimeout` of `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` with a 5s floor, and SPEC-1 plus SPEC-3 carry §5.2's formula across the concurrency boundary. §7.1's "states no deadline" reads as unbounded only in isolation.
- **The CRD cleanup-timeout rule is concurrency-gated in code.** `poolstore.validateSessionPolicy` puts the `cleanupTimeoutSeconds >= maxConcurrentSessions*5` check inside `if sp.MaxConcurrentSessions > 1`, so any spec sentence generalising that bullet across the concurrency boundary generalises an admission rule the code does not apply at concurrency 1.
- **The occupancy-zero concurrent release sends TWO RPCs.** `Binder.ReleaseSlot` sends the per-slot `Shutdown`, then a second `ShutdownRecycle` reusing the just-released session id over the same connection; the exclusive release sends one `ShutdownRecycle` for a session the adapter does hold. That asymmetry is why SPEC-1's no-op sentence reads differently on the two paths.
- **`SlotID == SessionID` IS stated in `spec/`, at spec/05:395** ("identified by the identifier of the session bound to it"), mirrored at docs/reference/glossary.md:371 and pinned by `tests/tier11_docs/slot_definition_glossary_reconciliation_test.go:85-104`; the code sets `SlotID: req.SessionID` at podclaim/slotclaimer.go:760. SPEC-2's §7.1 sentence restates an existing §5.2 definition. CORRECTED: an earlier form of this entry said the identity was stated nowhere and called §7.1 the first spec statement of it, so an argument shaped "§7.1 mints a new claim" is arguing from the stale form.
- **A pod-side reclaim destroys no client-retrievable setup output.** `GET /v1/sessions/{id}/setup-output` is served from the Postgres session row, which is what makes the §15.1 `SETUP_COMMAND_FAILED` promise survive a reclaim on the RunSetup stage.
- **The adapter manifest is pod-global,** at `/run/lenny/adapter-manifest.json` rather than under `/run/lenny/slots/{sessionId}/`, so SPEC-3's credential-directory removal cannot take it.
- **§7.1's three-kind parenthetical is exhaustive over the gateway's bind attempts.** `POST /v1/sessions/{id}/resume` routes through `resume_pending → running`, the same §7.3 flow, and a podless `suspended` session resuming is routed there too; `POST /v1/sessions/start` is the creation path. A fourth kind would be an unowned-residue finding, and there is none.
- **A concurrent pool has no `ready` gap.** `prepareAtFinalize` returns nil for `MaxConcurrentSessions > 1`, so the slot is materialised and launched together at `/start`, and the bound-but-never-started teardown class (READY_TIMEOUT, terminate-at-`ready`) can occur only on an exclusive pod, which spec/05:455 retires anyway.
- **No gate hard-fails on the staged spec text.** The tier-0 citation resolver and the line-citation ratchet target the retired `§X.Y line(s) L` form only and the staged text carries none; `per_slot_substate_scope_doc_reconciliation_test.go` pins four general edges by substring and the two scoped ones by exclusion, so SPEC-4's added edge passes; `recycle_scrub_trigger_consistency_test.go` reads the §4.7 row remainder SPEC-1 leaves untouched; `credential_path_literal_sweep_test.go` sweeps the retired pod-global path rather than the per-slot one; `code_blocks_test.go` parses only language-tagged fences and both the §7.1 and §6.2 fences are untagged.
- **The staged §5.2 sentences have no mirror outside spec/05.** Greps for "always assigned to a **new slot**", "fully saturated or unhealthy", "kills any processes owned by" and "releases the `slotId`" each return a single site.
- **The §5.2 slot retry policy runs on one path:** a row with no live `PodAssignment`, which is the §7.3 resume-rebuild or a slotless row. The create-time reservation and the `/start` reconnect to a create-reserved slot have no retry budget: `classifySlotBindFailure` answers the §5.2 `SLOT_FAILED` envelope, and a create-time `ErrNoConcurrentSlot` maps to `SESSION_CREATION_FAILED` rather than `WARM_POOL_EXHAUSTED`.
- **§5.2's non-retryable list includes `workspace_validation`,** a bind-time (FinalizeWorkspace) failure, so the policy demonstrably spans bind failures.
- **The snapshot diffs have been empty for most rounds.** `scratchpad/cp-snap/0081/spec-rN` was byte-identical to the live proposal in rounds 2, 4, 5 and 7, so "read the changed sections hardest" had no target. The real deltas are r2→r3 (the §7.2 block, the §15.1 create-time edge case, the racing-start rewrite, the `**Max retries:**` re-scoping and the `StartSession` → "start" sweep), r3→r4 (§7.1's "ends when that attempt succeeds", the §7.2 preamble deletion plus the step-2 sentence plus the step-3 rewording, the create-time-reserved bullet rewrite and SPEC-3's complement sentence) and r5→r6 (the §29.4 step-13 block). Diff against the named earlier snapshot rather than the current one; several lenses lost time on an empty diff. This has since become the rule rather than the exception: in every spec-recheck and non-spec-recheck round the snapshot the brief named was byte-identical to the live proposal, because it is taken at the round's own start. Run `diff -rq` over the whole `scratchpad/cp-snap/0081` directory first, take the newest snapshot that differs, and prefer the `-prefix` or `-start` form of the previous round's name. Later real deltas: `spec-r8` → the spec-recheck rounds (the §4.1 rationale, the §29.4 step-12 rationale, the blob-store edge-case rewrite and SPEC-3's leak-accounting clause), `spec-recheck-r1-prefix` → r2 (the clause's concurrency split), `spec-recheck-r4` → the open-decisions firing (summary only), and `non-spec-recheck-r1-prefix` → r2 (the CODE-2 `Resume` extension, the credential-lease relocation, the reserved-branch accounting and the two-arm tier-7a case).
- **CODE-1's response gate is `live`, not `started`.** It reads `ExitedCleanly: closeErr == nil && (live || treeErr == nil)` with `live := removed && s.runtimeHoldsLocked(sessionID)`, which is the same `running` boundary SPEC-3 and SPEC-4 install, so the tree-removal error is surfaced across the whole pre-`running` range including a start still in flight. Any lens checking that sentence against the earlier `(started || treeErr == nil)` form is reading a stale snapshot.
- **`started && !live` is unreachable at an ordinary session end,** which is what makes the `live` gate safe for the session-end classification. Every path that admits a start reaches `noteRuntimeStarted` unconditionally on success, and the two `noteRuntimeClosed` callers outside `Shutdown` deregister the entry in the same pass, so a later `Shutdown` has `removed == false`. EVIDENCE: pkg/adapter/session.go:156-163; resume.go:140-144; sdkwarm.go:217,:261,:296-298; holdstate.go:228-252.
- **`exited_cleanly` is read on the CONCURRENT slot-release path only.** `Binder.ReleaseSlot` computes `leaked = err != nil || !cleanly`, while session-mode `Binder.Release` calls `shutdownAdapter` best-effort and never reads the response (binder.go:1957-2008). CODE-1's widened `exited_cleanly` therefore cannot mark a slot `leaked` on a `maxConcurrentSessions: 1` pod.
- **DECISION: §5.2's leak-accounting sentence is concurrency-split.** On a concurrent pod a cleanup that does not complete is `leaked`, holds its occupancy, counts toward the whole-pod replacement trigger and moves `lenny_adapter_leaked_slots`; on a one-session pod the disposition is §7.1's claim release and retirement. Concurrency is the ONLY qualifier the sentence carries; the start-state and bind-path qualifiers are made true in the code lane instead.
- **DECISION: `accountSlotFailure` gains a THIRD caller** on `resumeOnPod`'s `podBinder.Resume` failure branch, rather than the §5.2 sentence being scoped down to the paths the gateway's accounting happens to reach. Its parameters narrow from `req podsession.SlotBindRequest` to `pool string, maxConcurrentSessions int32`, which changes both existing call sites, and the new caller is guarded on a non-empty resume slot id, which is exactly `MaxConcurrentSessions > 1`.
- **The §7.3 accounting gap was architectural, and it is now answered.** `pkg/gateway/podlifecycle/podsession` holds zero references to `slothealth`, `slotstate` or any leak gauge, and `Binder.Resume`'s failure branch returned a plain `fmt.Errorf`, so the disposition could not cross the package boundary at all. CODE-4 folds a `*SlotBindError` into that chain (transparent to `isTransientPodClaimError`, because `Unwrap` returns the cause) and CODE-5 accounts it.
- **Only the checkpoint-restore branch missed the accounting.** `resumeOnPod`'s snapshotless resume-rebuild goes `startOnPod` → `bindConcurrentSlot` → `applySlotRetryPolicy` and always reached it. Say "the checkpoint-restore re-attach", never "the resume path".
- **`applySlotRetryPolicy` is the only shipped producer of `MarkLeaked`, the leak gauge and `RecordLeak`.** `ReportSessionScrub(leaked=true)` feeds `drainLedger.RecordLeak` and never touches the gauge, so `lenny_adapter_leaked_slots` is the retry-policy path's signal rather than the general leaked-slot signal spec/05:545 and spec/06:160 describe.
- **`accountSlotFailure` is the only route a pre-`running` leak has to the §5.2 trigger.** The whole-pod replacement trigger has exactly two evaluation sites, `applySlotRetryPolicy` and `drainLedger.RecordLeak`, and SPEC-3 withholds the report that drives the second. Any future sentence asserting leak accounting on a pre-`running` reclaim must name a path that reaches `accountSlotFailure`.
- **DECISION: the credential-lease release moves out of `compensateFailedSlotBind` into the `materializeSlot` wrapper's error branch,** unconditional and outside the `errors.As` guard, so the compensation is pod-side only. `Binder.Resume` mints no §4.9 lease, so the shared form returned a live session's leases to their pool on a retryable failure.
- **No `materializeSlot` failure branch releases a credential lease today.** Its five stages do `cl.Close()` + `recordSlotFailure` + return, and `releaseCredentials`'s only callers are `failPhase`, `ReclaimClaimed`, `Release` and `ReleaseSlot`. CODE-4's wrapper release is a genuine new reclaim with no double-release site, and `ReleaseSession` is a documented no-op for a session holding none.
- **`releaseCredentials` is destructive rather than idempotent.** `Service.ReleaseSession` removes each lease from the store and decrements the credential's active counter, and nothing re-mints on a §7.3 retry.
- **DECISION: CODE-2's rollback covers the adapter's `Resume` as well as `StartSession`.** resume.go:50/:140/:144 is the identical claim → `Runtime.Start` → record sequence, and the staged §7.1 edge bullet is written RPC-neutrally, so the symmetric rollback needs no spec edit. `sdkwarm.go:261` is genuinely unraceable: its only gateway caller is `Binder.Launch`, whose failure runs `failPhase` and retires the pod.
- **`noteRuntimeStarted`'s caller list is exhaustive and its `bool` return breaks no build.** Three production sites (session.go:163, resume.go:144, sdkwarm.go:261) and six test sites (export_test.go:45, usage_test.go:233, adapterevents_test.go:95 and :184, podmcp_arming_internal_test.go:84/:185/:230). A bare call statement stays legal Go, so the `_ =` rewrites are cosmetic.
- **Exactly one shipped test goes red under CODE-2's guard.** `TestAdapterEventsEmitsControlEvents_spec_4_7` builds `New("served")` with nothing in `s.slots` and asserts the `soleSession` stamp. Both `adapterevents_test.go` sites take `bindSessionForTest`; `TestEmitFinalUsageOnShutdownPath_spec_4_7` stays green because `emitFinalUsage` passes the session id explicitly and the `soleSession` fallback never runs.
- **CODE-1's drain move withholds a frame that is sent today, and §15.4.2 owes none.** The shipped handler gates the teardown on `bound` (session.go:243) and the drain additionally on `!boundRemains` (:259), so a bound-but-unstarted reclaim currently gets a `terminate` frame. §15.4.2 is an adapter-PROCESS machine whose DRAINING obligation presupposes a runtime that was given a session, so nothing is lost. It is pinned by a no-frame assertion in the tier-1 "Bound but unstarted" case, whose no-other-bound-entry condition is load-bearing.
- **Every shipped adapter `Shutdown` test drives a started entry.** `claimSessionForTest`/`ClaimSessionForTest` binds, starts AND records (export_test.go:40-52), and every tier-7a drain-gate target goes through `startDrainSession`. That is why CODE-1 breaks no shipped test, and why every bound-but-unstarted assertion is new rather than a regression guard.
- **§29.4 is scoped to a started session.** Its `**Preconditions.**` paragraph (spec/29:586-591) scopes the WHOLE trace, interrupt and session-end alike, to a completed §29.2 startup, "so the runtime is running", with the interrupt path stated as an addition. Step 12 stays true under SPEC-1's started gate and needs no edit. Four lenses read the paragraph cold and reached the same reading; this settles the round-7 disagreement.
- **The creation finalize block engages the binder only on an EXCLUSIVE pool.** `prepareAtFinalize` returns nil for `MaxConcurrentSessions > 1`, so CODE-5's three accounting callers cover §7.1's three attempt kinds with no gap. A lens counting callers against §7.1's parenthetical will think one is missing; it is not.
- **`reserveResumeSlot` lands a real Redis increment** (`SlotClaimer.ReserveSlotOnPod`), so the §7.3 leaked hold is a held increment rather than a notional one. On an exclusive pool it returns `""` and `releaseResumeSlot` no-ops.
- **`ReleaseSlotReservation` hard-codes `recycle=false`,** so `bound → recycling` is unreachable on the failed-bind release path with either disposition (`WriteRecyclingStatus` sits inside `if recycle {`). The distinguishing counterfactual for a `leaked=true` release is the claim DELETE at occupancy zero.
- **`SlotClaimer.ReleaseSlot` has THREE dispositions, not two:** sibling-slot retained (:845-847), the recycle patch plus missing-report timeout inside `if recycle` (:850-878), and delete-and-retire at occupancy zero (:881-885). The recycling one is the disposition in which a bricked pod is handed to the next session.
- **`maxConcurrentSessions: 1` with `recycle.enabled: true` is a first-class configuration (sequential reuse),** so "one session per pod" does not imply "one session per pod lifetime". §4.6.1 projects `idle` for a claim deleted on a recycling pod and `draining` on a non-recycling one, and `failPhase`'s `drain` is a bare `DeleteClaim`, so the two code comments describe the same call oppositely.
- **Retirement is never a gateway act at the release.** `accountSlotFailure`'s tail reaches `DrainSandbox`, which only stamps `lenny.dev/drain-request`; the WarmPoolController owns the `draining` write. A test that asserts "the pod retires" is asserting something no gateway-side fixture can see.
- **The tier-4 fixture can show neither a retirement nor a threshold crossing.** `tests/tier4_integration/recycle_scrub_path_test.go` seeds exactly ONE Sandbox (`sbx-r` at :184, shared by all seven cases), runs no WarmPoolController, asserts the drain annotation directly, and its two concurrent pools carry `MaxConcurrentSessions: 4` against `UnhealthyThreshold = (n+1)/2`. Its `MaxConcurrentSessions: 1` line at :259 is the exclusive fixture, so a grep of the first hit misleads.
- **`UnhealthyThreshold` is `(maxConcurrent+1)/2`, which is 1 at concurrency 1 AND at 2.** Any argument that the trigger cannot fire at concurrency 1 is about §5.2's heading scope, never about the code.
- **Tier 7a and tier 4 are external test packages.** `tests/tier7a_load_local` compiles as `package tier7a_load_local_test` under the `load_local` tag, so `runtimeLive`, `runtimeIdleLocked` and `s.slots` are unreachable from it, and `pkg/adapter/export_test.go` does not help because a `_test.go` file's exported methods link only into its own package's test binary. The only exported cohort read is `SoleSessionID()`, which returns "" for an empty cohort and for a cohort of two alike.
- **`runtimeLive` is a pod-level cohort.** `noteRuntimeClosed` early-returns for a non-member and deletes only the named session, and `runtimeIdleLocked` is literally `len(s.runtimeLive) == 0`, so a started co-tenant keeps it non-empty. Any prose asserting "runtimeLive is empty" after a per-session teardown on a co-tenanted pod is false by construction.
- **The tier-7a race case parks the start inside `Runtime.Start`, which admits ONE ordering.** `noteRuntimeStarted` runs only after `Runtime.Start` returns, so while the start is parked the reclaim can only land before the record, which is the ordering CODE-2's guard exists for. The park fixture already exists as `gatedRuntime` (podmcp_arming_handoff_test.go:43-101) and there is a shipped two-RPC rendezvous precedent at podmcp_once_per_pod_start_race_test.go:239-256.
- **No gate reconciles the per-slot fence against its mirrors.** `per_slot_substate_scope_doc_reconciliation_test.go` holds two tests that never meet: one reads spec/06 and spec/07 against a fixed four-entry `generalSlotEdges` list, the other reads the doc's state NAMES and section placement only. Nothing compares `slotstate.ValidTransitions()` to spec/06 either. DOCS-1 now stages one new `generalSlotEdges` entry, which gates both the positive and the negative loop, plus one doc-row substring asserted as the `` `receiving_uploads` | `slot_cleanup` `` pair.
- **No tier-11 gate catches the `docs/reference/adapter-contract.md:75` drift.** `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts four substrings that all survive SPEC-1, so the false row passes silently. The rest of the shipped tier-11 battery also stays green on the whole staging, re-derived by four lenses.
- **The §10.1 coordinator hold cannot arm in production.** `enterHoldState`'s only production caller is `onCoordinatorChannelClosed`, reached only from the deferred close of the adapter's own `AdapterEvents` handler, and no gateway code opens `AdapterEvents`. The hold allowlist excludes `Shutdown`, so the staged edge case is a conditional fail-closed statement, inert until remediation step R12 ships the control-stream consumer.
- **The adapter implements no process-group kill.** `removeSlotTree` is one line and greps for `Setpgid`, `syscall.Kill` and `pgid` over `pkg/adapter/*.go` return nothing, so §5.2's action list is ahead of the code on that clause before and after SPEC-3. Pre-existing; do not re-derive it as a finding.
- **`schemas/lenny-adapter.proto` already carries SPEC-3's widened cleanup list.** `SESSION_SCRUB_OUTCOME_RELEASED` names the slot's runtime, credential timers and directory tree, so SPEC-3's first anchor brings spec INTO line with the shipped proto. Do not file it as a proto mirror gap.
- **`onSlotLeaseExpired` bails when the slot is no longer registered,** so a stale §4.9 direct-mode timer cannot scrub a credential file whose `RemoveTree` failed. The entry deletion is what defeats §4.9's MUST, and it is shipped on every teardown path.
- **The §5.2 whole-pod scrub sweeps the racing-start orphan.** Step 1 is `kill -9 -1` as the sandbox user and step 2 is `rm -rf /workspace/slots/*` at the occupancy-zero recycle boundary, and recycling pods are tenant-pinned, so the "orphan survives into another tenant's session" escalation does not hold.
- **`PoolMatch.CleanupTimeoutSeconds` is not zero on a non-recycling pool,** despite its own doc comment saying so twice, because `poolPolicyMirror.PoolPolicy` assigns it outside the `if r := sp.Recycle` block. `slotCleanupBudget` therefore reproduces §5.2's `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` on every concurrent pool.
- **A `Stage: "resume"` on `SlotBindError` mints no metric label value,** because `lenny_slot_failure_total` is emitted by explicit `recordSlotFailure` calls and `Stage` only refines `Reason()`. Adding a `*SlotBindError` to the resume error chain also changes no downstream classification: no classifier matches the type and `Unwrap` returns the cause.
- **`Binder.Resume`'s failure emits no `lenny_slot_failure_total`.** That counter comes only from `recordSlotFailure` inside `materializeSlot`, so a §7.3 re-attach can now move the leak gauge, trip the threshold and increment `lenny_slot_pod_replacement_total` with no slot-failure series on that pod at all.
- **The staged lane ADDS a producer of the unmodelled `slot_assigned → leaked` transition.** CODE-4 retires `BindReservedSlot`'s release-error swallow and CODE-5's reserved branch passes `sbe.Leaked` into `accountSlotFailure`. The scope call stands on the surviving ground: the hole predates the proposal, `MarkLeaked` writes the terminal with no `IsValid` call and `Registry.Assign`/`Transition` have no production caller, so nothing raises `InvalidTransitionError`, and closing it means an edge out of `slot_assigned` that SPEC-4 deliberately does not add.
- **DECISION: open decision 7 (the Redis rehydration hole) is resolved as "its own proposal".** It left `## Open decisions` and became a row under `## Defects in the shipped tree that this proposal does not stage`. The consequential form is shipped and common, because `leaked = err != nil || !cleanly` fires on every ordinary session end; the single rebuild source is restated at four carriers (spec/05:551, spec/12:191, spec/12:219, spec/10:62); and spec/05:551's index predicate is already wrong against migration 0080, which creates a wider one.
- **DECISION: open decision 12 is resolved as "no sentence is added".** SPEC-4's `**Pre-`running` slot cleanup.**` paragraph already states that a session the runtime has been given is a slot in `running`, so the proposition has a home and no staged sentence is left false.
- **DECISION: three out-of-scope defects stand.** The socket listener a last close retires, §6.2's missing terminal for a bind abandoned at the connect stage, and the coordinator hold that cannot arm each keep their summary row; none opens a decision and none is staged.
- **The impacts table now names 0073, 0075, 0078 and 0080.** SPEC-1's §4.1 sentence is 0073's own text (commit f37e867b8); 0075 reserved that paragraph as the ground its D3 rests on and the ground survives the edit; 0079 was withdrawn from the joint 0078/0079 row because its own fixed decision shortens the residue's life rather than widening the exposure, and the two proposals share no spec anchor and no code file.
- **`Binder.releaseCredentials` covers the compensation's whole obligation on the bind paths.** The concurrent bind path performs no §4.9 revoke on failure today, so a `StartSession` failure after `assignSlotCredentials` strands the lease's active-session slot; CODE-4's wrapper closes exactly that.
- **The tier-1 and tier-2 fixtures the staging names all exist.** `slotPod`, `slotTreeProbe`, `probeRuntime`, `startRuntimeOps`, `recordingSessionScrubReporter`, `assignOne`, `fakeExpiryClock`, `concurrentAdapter`, `recordingShutdownAdapter`, `fakeAssigner` (which already records every `ReleaseSession`), the envtest placement-filter precedent pair, and `pkg/gateway/sessionserver/slotretry_test.go`. Closes the pass-2 Open on tier-1 adapter fixtures: `export_test.go:45` is already a `noteRuntimeStarted` seam. Re-verified at round 4 with line numbers: `slotPod` (slotsession_test.go:61), `slotTreeProbe` (:77), `probeRuntime` (:33), `startRuntimeOps` (runtimeops_test.go:83), `recordingSessionScrubReporter` (sessionscrub_emit_test.go:20), `assignOne` (credexpiry_test.go:105), `fakeExpiryClock` (credexpiry_test.go:48), `bindSessionForTest` (usage_test.go:350), `concurrentAdapter` (podsession/slotbinder_test.go:71), `fakeAssigner.released` (podsession/binder_test.go:302), `TestClaimSlotSkipsOverUptime{Claimed,Idle}Pod_spec_6_2` (podclaim/slotclaimer_test.go:648,:682).
- **The round-4 snapshot is empty and the delta is one paragraph.** `non-spec-recheck-r4` and `-r4-start` are both byte-identical to the live proposal, and the only difference against `-r3` is the tier-7a paragraph rewrite at non-spec-changes.md:829-872; the test-coverage lens found the review log alone differing. The orchestrator's "the staging changed after this lane's last review converged" is not observable from the snapshot it hands you, so do not spend a round hunting a fix-stage delta in the staging text.
- **The checklist is clean as an execution sequence (do not re-derive).** Ten steps, ten staged deliverables, one deliverable per step, no deliverable named twice, no step naming an unstaged deliverable, every `Depends on` naming an earlier existing step, one lane per step (spec ×4 leading, docs ×1, code ×5), no checked box. A code step reaches a spec-lane dependency TRANSITIVELY rather than directly (S9 consumes SPEC-1's §4.7 no-op sentence and names S2, S3, S7, S8; S7 names S1, so S1 lands first), which is still checkable from the `Depends on` lines and is not a defect.
- **The gate state is green at every intermediate commit of the sequence.** Between S4 and S5 `per_slot_substate_scope_doc_reconciliation_test.go` neither requires nor forbids the new edge (`generalSlotEdges` is a fixed four-string slice at :32-37 driving a positive loop at :55 and a negative loop at :70); between S4 and S6 nothing reconciles `slotstate.ValidTransitions()` against spec/06; SPEC-1's row replacement keeps everything from "On the default disposition the pod is replaced." onward, which is what `TestRecycleTriggerConsistentAcrossSpec47And52_F5215` reads; `spec_28_register_writers_test.go:101` pins a §12.6 sentence this proposal does not touch.
- **Every code target in the non-spec staging exists with the stated signature.** `materializeSlot` (slotbinder.go:265) matches the staged wrapper's parameter list byte for byte and holds exactly the five `cl.Close()` calls the staging moves out; `b.releaseCredentials` at binder.go:1263; the five production `ReleaseSlotReservation` sites (start.go:2834, :3246, slotbinder.go:172, :217, binder.go:1714) plus the `slotBinder` interface at start.go:2727 and the two fakes (slotretry_test.go:52, slotretry_load_test.go:33); `connectSlot`'s `podclaim.SlotRequest` mapping at slotbinder.go:422-428 is the one line `ExcludePod` needs; `expiredByUptime` has its two candidate-pass sites at :433 and :491; `bindConcurrentSlot` holds `slotReq` at start.go:2594-2605; `resumeOnPod`'s Resume failure branch is start.go:4041-4043; types line up for `slotCleanupBudget` on both `SlotBindRequest` and `ResumeRequest`. Two lenses verified this mechanically; do not re-derive it.
- **`noteRuntimeStarted` has exactly the ten call sites CODE-2 enumerates and no eleventh,** `noteRuntimeClosed` the three production sites CODE-1's response paragraph names (holdstate.go:251, sdkwarm.go:297, session.go:267), `session.go:243` is `if bound {` and `:259` is `if !boundRemains {` (`:242` is `closeErr := error(nil)`, a mis-citation a fix pass already corrected), and `releaseSessionSlot` is slotsession.go:214-220.
- **The harness's tier is the test's DIRECTORY rather than its infrastructure.** Everything under `pkg/` runs in the unit tier even when it starts an envtest control plane, tier 2 is exactly `./tests/tier2_component/...`, and `validate-diagnosis` scans only `tests/tierN_*`, so nothing under `pkg/` is checked for the `// diagnosis:` annotation. This inverts a natural reading of `.claude/rules/test-coverage.md`. EVIDENCE: cmd/lenny-test/cmd_run.go:880-887,:1010; cmd_list_resolve.go:95; cmd/lenny-test/cmd_validate.go:120-140.
- **The tier-7a ordering argument holds mechanically.** Park inside `Runtime.Start` and release after the reclaim's `Shutdown` returns: the reclaim sees `st.started` true so the runtime teardown runs, `live` false so no report, then CODE-2's guard refuses and rolls back. No deadlock, because `gatedRuntime.park` releases `g.mu` before blocking and `Runtime.Start` is not called under `s.mu`. The co-tenanted variant's cohort assertion is observable from the external tier-7a package through `SoleSessionID()`, which returns the co-tenant's id in the correct state and `""` in the residue state because `runtimeCohort` goes 1→2.
- **CODE-1's move of `cancelPodMCPIfRuntimeIdle` into the `removed` branch is safe on every arm.** It is double-guarded by `runtimeIdleLocked()` and `mcpArmingHeldLocked()` (slotsession.go:238-260), and the one interesting arm, a session that claimed and armed with `Runtime.Start` still in flight, still holds a slot, so `mcpArmingHeldLocked()` is true and nothing is cancelled. No new hazard and no test is owed.
- **`ValidTransitions()` holds six edges today** (slotstate.go:106-113) and `TestValidTransitions_spec_6_2` mirrors them in a `want` map with a length fatal (slotstate_test.go:12-24), so the Testing section's "must move to seven edges" is arithmetically right; its `for e := range want { IsValid(...) }` loop already gives the positive assertion the section proposes to add, and the test's own `// spec:` comment ("exactly the six edges") needs one word changed.
- **DOCS-1's staged row and the staged tier-11 assertion match end to end.** The doc rows are spelled `` | `receiving_uploads` | `running` | ``, so the proposed `requireAllContain` substring matches DOCS-1's row verbatim, and adding `"receiving_uploads ──→ slot_cleanup"` to `generalSlotEdges` passes both the positive loop over the either-concurrency block and the negative loop over the concurrent-occupancy block, which carries only `running ──→ failed` and `slot_cleanup ──→ leaked`. The scoped header precedes the general header in spec/06, which the slice at :64 requires. Do not re-derive.
- **`tests/flake-budget.yaml` is a quarantine registry with `quarantined: []`,** rather than a register of stress-budgeted tests, so the new tier-7a case owes it no entry and it is not a missing edit site. EVIDENCE: tests/flake-budget.yaml:15-17.
- **The tier-4 `concurrent_workspace_test.go` is adapter-driven rather than gateway-driven.** It is `package tier4_integration_test` over bufconn with a real `adapter.Server`, a real `NewSocketRuntimeProcess` (:119), `echo-concurrent` (:307) and `maxConcurrentSessions: 2` at :96, and it holds no gateway binder and no pool object. The staged "on a `maxConcurrentSessions: 2` pool" is a gloss on the deployer configuration the flow assumes, and "after the compensation" means the case issues the adapter `Shutdown` itself. A reader taking it as gateway-driven will look for a binder that is not there.
- **The gateway tier-1 fixture inventory, corrected and landed.** `concurrentAdapter` (podsession/slotbinder_test.go:71-155) already declares `startErr`, `shutdownExitedCleanly` (default true) and `shutdownErr`, its `Shutdown` handler at :146-155 reads the latter two, it records no request, and it serves NegotiateVersion, FinalizeWorkspace, RunSetup, StartSession and Shutdown only. `recordingShutdownAdapter` (binder_test.go:1156-1173) carries per-request recording and `uncleanExit`, has no `shutdownErr` and no error arm, and implements `Shutdown` alone; same package, so `slotbinder_test.go` can borrow its recording pattern. Round 5 corrected the two false clauses at non-spec-changes.md:708-714. The genuinely absent fixture work is per-stage injection for the finalize, setup and credential-assignment stages, the `PrepareWorkspace` and `AssignCredentials` handlers, and request recording.
- **The files that create an unbound adapter entry call `Shutdown` nowhere.** `coordination_test.go`, `credentials_test.go`, `rotationgate_test.go`, `slotframe_test.go` and `tracingcontext_addressing_test.go` create an unbound entry or call `AssignCredentials` without starting, and `grep -n Shutdown` over them returns nothing, which is the other half of why CODE-1's report-gate move breaks no shipped adapter test.
- **DECISION: CODE-2's rollbacks answer `codes.Aborted`, not `codes.FailedPrecondition`.** `SlotBindError.Reason()` maps a `FailedPrecondition` at any stage but `workspace_prep` to `SlotReasonPolicyRejection`, which `NonRetryable()` reports true for, so the staged rollback turned a reclaim-versus-start race into a 422 `SLOT_FAILED{retryable:false, category:"policy_rejection"}` with no `Retry-After` and the retry budget unconsumed. `Aborted` falls through `Reason()`'s `default:` to `SlotReasonTransient` with zero gateway change. EVIDENCE: podsession/slotfailure.go:41-49,:84-102; sessionserver/start.go:2761-2780,:2873-2879.
- **`codes.Aborted` is free on this surface and is already the adapter's own vocabulary for a concurrency abort.** It appears in `pkg/adapter` only on the checkpoint op lock (checkpoint.go:115; oplock.go:36-40,:82) and nowhere on `StartSession` or `Resume`. `adapterclient.Client.StartSession` returns the gRPC error unmodified (client.go:142-143) and `slotErrCode` walks the chain with `errors.As` (slotfailure.go:106-114), so `materializeSlot`'s `%w` wrap does not hide it.
- **`Server.Resume` opens no span; `Server.StartSession` does.** resume.go:25-35 has no `tracing.NewTracer`; session.go:76-87 opens one and categorises through a deferred `tracing.RecordError`. The two rollbacks are therefore symmetric in every respect but the span line, and the `StartSession` arm sets `spanErr = tracing.CategorizeError(rollbackErr, tracing.CategoryTransient)`.
- **DECISION: CODE-2's rollback drops `releaseSessionSlot` and calls `cancelPodMCPIfRuntimeIdle()` directly, on both the `StartSession` and the `Resume` arm.** The guard has already established that the registry holds no entry bound to this session, so the deregister-and-`RemoveAll` half can only be a no-op or a destruction of somebody else's entry. `cancelPodMCPIfRuntimeIdle` is successor-safe through `mcpArmingHeldLocked` (slotsession.go:238-260). The identity-checked-deregister alternative was rejected as a pre-existing class fix wanting its own problem statement.
- **`st.sessionID` has exactly two production writers and neither ever clears it,** slotcreds.go:34 (`AssignCredentials`) and slotsession.go:87 (`claimSessionSlotUnderLock`), both setting it to the map key. So CODE-2's guard `!ok || st.sessionID != sessionID` is precisely "absent OR registered-but-unbound", and `st.sessionID != sessionID` is equivalent to `st.sessionID == ""` at every session-keyed call site.
- **The registered-but-unbound successor state is the ORDINARY state, not a rare interleaving.** `ensureSlotPaths` (slot.go:140-148) creates an entry with an empty `sessionID` for every `PrepareWorkspace`/`FinalizeWorkspace`/`RunSetup` (staging.go:134,:181,:337), so a §5.2 retry's workspace staging creates exactly the entry a lagging id-keyed release would delete.
- **DECISION: `slotretry_test.go`'s `req(pool, maxConcurrentSessions)` helper keeps its by-value return, and each `applySlotRetryPolicy` call site binds a local and passes its address.** The helper feeds two callees with different parameter modes: `applySlotRetryPolicy`, which CODE-5 changes to `*podsession.SlotBindRequest`, and `classifySlotBindFailure`, whose by-value signature (start.go:2761) no deliverable touches. No single return type leaves every call site unchanged.
- **The `req` helper's call-site inventory is twelve plus four.** Into `applySlotRetryPolicy` (the ones the pointer change edits): slotretry_test.go:81,:108,:137,:167,:208,:251,:294,:310,:344,:370,:421 and slotretry_load_test.go:86. Into `classifySlotBindFailure` (untouched): slotretry_test.go:442,:482,:499,:528. `bindSlotWithRetry` has no test caller; its only caller is `bindConcurrentSlot` at start.go:2608, staged as `&slotReq`. `classifySlotBindFailure`'s two production callers are start.go:2172 (inside `claimAtCreate`, declared :2085, its own create-time local) and :2602 (inside `bindConcurrentSlot`, declared :2594).
- **`maxSlotRetries` is a hard-coded `const = 1` at start.go:2720,** not read from `sessionPolicy.slotRetries`, so one `applySlotRetryPolicy` invocation is exactly two attempts. A single-valued exclusion would be sufficient WITHIN one invocation; the only bypass is the `queue`-pool re-entry, which is why `ExcludePods` is a slice carried by pointer.
- **The `queue` re-entry path, verified end to end.** `bindConcurrentSlot` captures `slotReq` in the `runWithQueue` closure (start.go:2606-2609); `applySlotRetryPolicy` is at :2807; `podclaim.ErrNoConcurrentSlot` is surfaced unwrapped (:2816-2819), classified as exhaustion (queue.go:103-107), routed to `waitInQueue` (:143-146) and the closure is re-invoked at :205. `waitInQueue` bounds the whole wait with `deadline := start.Add(waitBound)` (:155-205) and `yield` re-appends the ticket to the TAIL (:218-221,:258-278), so an excluded waiter round-robins rather than pinning the head and cannot stall unboundedly.
- **`validate-maps` is a TIER-0 check and it enforces per-file spec-map membership, not only existence and dangling paths.** `runValidateMaps` runs `validateTestFilesMapped`, which walks `componentAndAboveTierDirs()` and fails for any `_test.go` under `tests/tier{2,3,4,5,6,7a,7b,8,9,10,12}_*` that no `tests/spec-map.json` entry names; tier7a is mapped file by file with no directory glob. A NEW file under `tests/tier7a_load_local/` turns tier 0 red; a new test FUNCTION in a listed file costs nothing. `tests/tier11_docs` is absent from `componentAndAboveTierDirs()`, so the DOCS-1 half owes no entry. CORRECTED: three earlier entries recorded the ground for declining the spec-map item as "existence, version and dangling paths only", which is incomplete. EVIDENCE: cmd/lenny-test/cmd_run.go:761-770; cmd_validate.go:125-139,:716-792.
- **`s.podBinder` is a concrete `*podsession.Binder`, not an interface,** so `resumeOnPod`'s `podBinder.Resume` has no fake seam the way `applySlotRetryPolicy` has `slotBinder` (start.go:2725-2730). The package's own tests already build real `podsession.Binder` values against a fake or envtest client (start_preclaim_internal_test.go:655,:720,:779; terminal_reclaim_internal_test.go:52; start_pod_test.go:203), so the tier-1 resume accounting case is a fixture build rather than a fake swap. CORRECTED: no shipped test calls `resumeOnPod` directly. `start_pod_test.go` is `package sessionserver_test` and its `:1420` mentions the function only in a comment, so the case has to land in an internal file (`slotretry_test.go` or a new `*_internal_test.go`), and an external file can reach the path only through the HTTP resume handler.
- **`cl.ConfigureWorkspace` has exactly ONE production caller,** `Binder.Launch` at binder.go:1009, and `Binder.Launch` reaches only `StartSession` or `ConfigureWorkspace` (binder.go:967-976). So CODE-2's "no compensation races the SDK-warm record" holds, and there is no fourth start RPC on the exclusive path.
- **`slotBinder`'s only implementors are `*podsession.Binder` plus the two named fakes** (slotretry_test.go:52, slotretry_load_test.go:33), so CODE-4's signature change has no unlisted implementor.
- **`Registry.MarkLeaked` seeds an untracked slot at `Leaked` and never consults the edge list** (pkg/sandbox/slotstate/registry.go:99-116), so CODE-5's new callers cannot raise an invalid-transition error for a slot the gateway never `Assign`ed.
- **`deregisterSlotLocked` does NOT touch `runtimeLive`** (slotsession.go:174-189), which is what makes CODE-1's `live := removed && s.runtimeHoldsLocked(sessionID)` computable AFTER the deregistration inside the same critical section. A reviewer who assumes the deregistration clears cohort membership concludes `live` is always false and the report never fires.
- **The three leaked discriminators are one predicate across all three bind paths.** `compensateFailedSlotBind` returns `err != nil || !cleanly`; `applySlotRetryPolicy` accounts on `sbe.Leaked || relErr != nil`; `BindReservedSlot` folds its own release error into `sbe.Leaked` before the caller reads it; `Binder.Resume` folds `relErr` in before `resumeOnPod` reads it. The `leaked=true` release is a no-op inside `SlotClaimer.ReleaseSlot` (slotclaimer.go:830-836), so `relErr` is always nil on the leaked arm and the two-term discriminator never double-counts.
- **The `ExcludePods` pointer design has no aliasing or ordering hole.** `bindConcurrentSlot`'s `slotReq` is its own value parameter and `&slotReq` outlives every queue re-entry. Whether the append sits before or after the `reason.NonRetryable() || attempt == maxSlotRetries` return does not matter: a run reaching that return yields a `SlotFailedError`, which `isExhaustion` rejects, so no re-entry follows and an un-appended last-attempt pod is never re-placed.
- **`createClaimNeedsRollback` returns false for any `*SlotBindError`** (start.go:3211-3225), and under CODE-4 a leaked `BindReservedSlot` release performs no decrement, so the create-time reservation is deliberately held on the combined create-and-start path. It reads as a double-leak and is not one. It also cannot reach CODE-4's change: it is called only from the create path (start.go:833,:1026) with `startOnPod`'s error, while `resumeOnPod` (:3943) is reached only from treerecovery.go:29 and the resume handler at :3494.
- **`Credentials.ReleaseSession` DOES cover user-source leases.** `credassign.Service.ReleaseSession` iterates `s.leases.LeasesBySession([sessionID])` over the shared lease store, and the `UserCredentialAssigner` doc comment says so outright ("User leases share the lease store the pool assigner uses"). The reading that `releaseCredentials` touches only `b.Credentials` is right about the call and wrong about the consequence: `b.Credentials` is the store both minters write into. EVIDENCE: credentials/credassign/credassign.go:400-410; podsession/binder.go:335-343,:1263-1268.
- **`materializeSlot` today releases NO credential lease on any of its five failure branches** (slotbinder.go:264-325, five `cl.Close()` + return sites, none calling `releaseCredentials`), so CODE-4's unconditional wrapper release is a net reclaim rather than a re-arrangement. Each branch returns `b.slotBindError(...)`, so `errors.As` matches on every current stage and the wrapper-owns-the-close restructure loses no path.
- **A cancelled context cannot make `Runtime.Close` a no-op.** All three `RuntimeProcess.Close` implementations use ctx only to derive the SIGTERM grace: `SocketRuntimeProcess.Close` does `releaseActiveLocked` before any ctx read (socketruntime.go:435-446), `InProcessRuntime.Close` takes `_ context.Context` (embedded.go:188), `MCPRuntime.Close` ignores it until `resolveShutdownGrace` (mcpruntime.go:266-295,:312-324), which only uses a deadline when `time.Until(dl) > 0`. A cancelled ctx makes the close HARSHER, never absent. Closes the pass-4 Open on CODE-2's rollbacks running on the inbound context.
- **`adapterclient.Client` applies NO per-RPC timeout of its own;** every RPC rides the caller's ctx (client.go:133,:807). So a `StartSession` that fails on deadline means `applySlotRetryPolicy`'s own `ReleaseSlotReservation(ctx, …)` and its retry `BindSlot(ctx, …)` are already dead, which is why the compensation's `context.WithoutCancel` is the only thing that survives that case.
- **`SocketRuntimeProcess.Start` RELEASES `p.mu` before `p.accept(ctx, timeout)`** (socketruntime.go:183-203), so a compensating `Shutdown` does not block behind a parked start for the whole AcceptTimeout, and `Close` early-returns nil on `!p.connected` (:436-439). Consequence worth knowing: in that interleaving `closeErr == nil` and `live == false`, so `ExitedCleanly` is true, `Leaked` is false, and the `ExcludePods` exclusion does NOT fire, so the retry can return to the same pod. That is the recorded "rolled-back start can close a successor's runtime session" accepted failure mode reached by its most likely route.
- **`slotCleanupBudget`'s denominator is ≥ 1 at every reachable call site,** and `SlotBindRequest.CleanupTimeoutSeconds`'s own doc comment ("Empty/zero on a non-recycling concurrent pool") is stale: `slotBindRequest` sets it unconditionally from `match.CleanupTimeoutSeconds` (start.go:2567), which comes from `sessionPolicy.cleanupTimeoutSeconds` rather than from the recycle block. Bind path is gated on `match.MaxConcurrentSessions > 1` (start.go:2139,:2351,:2465); the resume path normalises through `maxConcurrentSessions()` (:3353-3358, applied at :4029) and `ResumeRequest`'s field doc says "normalized to a minimum of 1 by the caller". A test fake leaving the field zero would panic.
- **On an EXCLUSIVE pool reached through `Binder.Resume` the budget degenerates to the whole pool `cleanupTimeoutSeconds`** (`max(T/1, 5) == T`), so a pool at `cleanupTimeoutSeconds: 60` blocks the gateway request goroutine for up to 60s on a `context.WithoutCancel`. §5.2's CRD rule bounds the ratio only from below, and §16.5's creation SLO excludes the window (spec/16:626), so no stated budget is breached.
- **"per-session teardown" occurs EXACTLY ONCE in the whole `spec/`, `docs/`, `schemas/` corpus,** at spec/04:157, the sentence SPEC-1 replaces, and so do "per-slot teardown" and "whole-pod teardown". The §4.1 edit removes the term from the corpus and no second site needs to follow it.
- **`ReportSessionScrub`'s served-session effect is stated in `spec/` and need not be inferred from code.** The §4.7 row reads "The gateway increments `sessionsServed` on the pod's `agent_pod_state` row" (spec/04:692) and spec/12:494's column comment reads "gateway-written at each session release". SPEC-3's withheld-report rationale can cite spec rather than `scrubreport_server.go`.
- **spec/12:481 and :494 stay true under SPEC-3, and the word that makes them true is "also".** SPEC-3's append states the pre-`running` cleanup as something the per-slot cleanup ALSO runs for, so it is an additional trigger rather than a session release, while the complement sentence defines a post-`running` cleanup as "a session release like any other". Read the two sentences together before filing spec/12.
- **`lineContaining` is a case-sensitive FIRST-match scan, and `requireLine` inherits it.** tier11_docs/backup_status_enum_test.go:48-56; recycle_scrub_trigger_consistency_test.go:158-165. It does not require uniqueness, so neither SPEC-4's new fence edge nor its new prose paragraph can break the presence loops.
- **`generalBlock` in the per-slot tier-11 gate is `s62[index(generalHeader):]`, i.e. everything to the END of §6.2,** while `scopedBlock` is `s62[scopedHeader:generalHeader]` (per_slot_substate_scope_doc_reconciliation_test.go:53,:62-64). SPEC-4's new `**Pre-`running` slot cleanup.**` paragraph therefore sits inside `generalBlock`, which is harmless for both loops; it writes the edge with a single arrow (`receiving_uploads → slot_cleanup`) while the loops match on `──→`, and it does not contain "`leaked` slot semantics", so `TestLeakedSlotCountingLifetimeAgrees_F5231` still resolves to spec/06:160.
- **The shipped tier-3 and tier-10 per-slot-cleanup-outcome gates are green under CODE-1,** because every case drives a full `StartSession` first, so `live` is true and `ExitedCleanly` reduces to `closeErr == nil` exactly as today; for a session the adapter holds no entry for, `removed` is false and the answer is `true && (false || true)`, identical to the shipped `bound` gate. EVIDENCE: tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:135-152,:285,:318; tier10_conformance/recycle_scrub_conformance_test.go:175-197,:376-395.
- **`tests/tier11_docs/adapter_metric_catalog_test.go` cannot be reached by anything this proposal does:** its source of truth is a regex over `pkg/adapter/metrics.go` (:26,:53-60), and `lenny_adapter_leaked_slots` is registered gateway-side. SPEC-3 naming that gauge in §5.2 adds no catalog obligation.
- **No alert anywhere references any slot metric,** `grep 'slot'` over `pkg/alerting/rules/rules.go` returns only the §16.5 comment and unrelated `*Exhausted` names, `WarmPoolExhausted`/`WarmPoolLow` key on `lenny_warmpool_idle_pods`, and no runbook under `docs/runbooks/` mentions a slot except three unrelated hits (Redis hash slots, a Postgres replication slot, the credential path). So the alert half of the operational lens is structurally inert.
- **The `lenny_adapter_leaked_slots` gauge is wired in production and SPEC-3's clause is true on every path its antecedent binds.** `cmd/lenny-gateway/sessionsrv.go:378,:381-382` wires `SlotReplacement` and `SlotLeakGauge`, so neither callback is nil; `applySlotRetryPolicy`'s leaked arm is start.go:2846-2851; `BindReservedSlot` re-runs `materializeSlot` (slotbinder.go:255) so the reserved branch reaches the same discriminator; the resume branch is CODE-5's third caller. The gauge is absent from spec/16 and docs/reference/metrics.md, which is pre-existing.
- **`lenny_slot_pod_replacement_total` is registered with the single label `pool`** (gatewaymetrics_credential.go:211-216) while spec/16:15 and docs/reference/metrics.md:167 both state `pool, k8s_pod_name`. Live, pre-existing spec-versus-code divergence; CODE-5 only adds callers of the existing emitter.
- **The new-identifier sweep is complete and empty.** `grep -rn "ExcludePod\|slot_bind_failed\|runtimeHolds\|slotCleanupBudget\|accountSlotFailure\|materializeSlotStages\|compensateFailedSlotBind" spec/ docs/ schemas/ charts/` returns zero hits; every identifier the proposal mints is in-process Go. The only NAMES the staged spec text introduces are two bolded prose terms (**slot release**, **runtime teardown**), two bolded paragraph labels, one fence edge and one step-3 title, and each was greped clean. `charts/` carries none of the affected identifiers and `schemas/` carries exactly one affected file, `lenny-adapter.proto`, which programme rule S-2 bars.
- **The staged-anchor sweep now has a mechanical, repeatable form.** A ten-line python script over the 28 fenced blocks of `.spec-changes.md` against `spec/*.md` gives count 1 for blocks 0,2,5,8,10,12,14,16,18,20,22,24 (the anchors) and count 0 for the rest (replacements and insertions). Two independent lenses reproduced it with identical results. Re-run it after any spec landing rather than eyeballing anchors.
- **The per-slot vocabulary has no SDK, CRD or OpenAPI mirror,** and `pkg/api/v1/session/session.go:9-13` states the exclusion outright ("Internal-only states ... are never returned by the REST API"). Note the OpenAPI document is at `pkg/gateway/externalapi/openapi/openapi.json`, NOT the `pkg/gateway/openapi/` path some lens briefs give; `cleanupTimeoutSeconds` appears in no file under `charts/lenny/crds/` or `pkg/apis/`. The `receiving_uploads` hits in docs/runtime-author-guide/lifecycle.md and the two warm-path SVGs are the POD-level machine.
- **`schemas/lenny-adapter.proto` carries NO doc comment on `ShutdownResponse.exited_cleanly`** (:1665-1668 is a bare three-line message), so CODE-1's re-keying of that field falsifies no proto text. This narrows the standing proto Deferred to the `Shutdown` RPC comment at :203-206 plus the `ReportSessionScrub`/`SessionScrubOutcome` comments.
- **`docs/api/internal.md:95-160` describes the adapter RPCs under RETIRED names** (`StopSession`/`StopSessionRequest`/`StopSessionResponse.clean_exit`, and a `StartSessionResponse{success, error_code, error_message}` where the shipped proto has `refusal_reason` at :958-962). A client-surface lens grepping for `Shutdown` mirrors lands here; that block predates the proposal and is not an edit site for it.
- **There is a FOURTH `slot_assigned` reservation-leak site, and it is NOT a producer of `slot_assigned → leaked`.** `ClaimSlot`'s create-time connect-stage branch logs its `ReleaseSlotReservation` error and returns (slotbinder.go:168-177), and CODE-4's table deliberately passes `false` there, so a create-time claim-handshake failure whose release fails holds a Redis slot with no `MarkLeaked`, no gauge move and no threshold contribution. Shipped, outside §7.1 (no pod-side RPC issued). A future proposal closing the `slot_assigned` terminal should count it.
- **`bindReservedSlot`'s three connect-stage failures return a `SlotBindError` WITHOUT reaching `materializeSlot`** (slotbinder.go:230-254), so CODE-4's compensation never runs there and `sbe.Leaked` stays false until `BindReservedSlot`'s own release error sets it. `BindReservedSlot`'s release covers both that connect stage and every `materializeSlot` stage because it sits in the outer wrapper (:210-224), and the ordering is sound: `ReleaseSlotReservation` is passed the compensation's `sbe.Leaked` and only afterwards does the method raise it on a release error.
- **The orphan-GC backstop is real and level-triggered.** `ClaimGarbageCollector.classify` (pkg/controller/warmpool/gc.go:223-320) drains a `bound` claim once `Status.BindingStateTransitionTime` (falling back to `CreationTimestamp`) ages past `claimOrphanTimeout`, gated on `Sessions.PodHasActiveSession` returning false.
- **`expiredByUptime` is the worked precedent for `ExcludePods`, ownership assertion included.** It is skipped with one `continue` in each pass (pass 1 at :417-441, pass 2 at :483-500), after the per-pod claim GET, and its two envtest tests assert both the skip and that the gateway left `Sandbox.status.phase` untouched (`assertNotGatewayStatusOwned`, slotclaimer_test.go:648-716). Pass 1 reads the per-pod `SandboxClaim` and never `Sandbox.status.phase`; pass 2 reads `sb.Status.Phase != Idle` and then GETs the claim.
- **spec/05:551 and migration 0080 quote the same index differently.** The spec writes the predicate as `sessions(pod_assignment) WHERE state = 'active'`; `migrations/0080_sessions_active_by_pod_index.up.sql:19-21` QUOTES the spec as `sessions(pod_name) WHERE state = 'active'`. The summary's shipped-defect row quotes the spec correctly and the migration's quotation is the stale one.
- **§7.2 step 4 says the interrupted resume attempt "is *not* retried, so no new recovery generation is minted"** (spec/07:215), which is what closes the lagging-reclaim-destroys-a-successor hazard on the §7.2 path. The hazard survives only on the create-time-reserved §15.1 retry. §7.3 step 3c likewise bounds `maxResumeWindowSeconds` to "before pod is allocated" (spec/07:407-412), so the wall-clock timer cannot fire mid-replay.
- **"half-claimed" has exactly three sites in `spec/`:** spec/07:214 and spec/06:234, both in the edit list, and spec/07:221's "Pre-attach terminal collapse" bullet, which is about `resume_pending` where no pod exists and survives SPEC-2's step-3 rewrite unchanged.
- **`receiving_uploads` as a PER-SLOT state has exactly two spec sites (spec/06:151,:152) and one docs site** (docs/reference/state-machines.md:234-235, DOCS-1's target). Every other hit is the SESSION-model state of a different machine (spec/15:672, state-machines.md:149-151,:175-176, runtime-author-guide/lifecycle.md:39) and is not a SPEC-4 edit site. The sweep for missed §6.2 mirrors is finished.
- **Neither wire schema states WHEN the graceful-shutdown signal is sent, only what it means,** so SPEC-1's co-tenancy condition falsifies no schema: `lenny-adapter-jsonl.schema.json:109-115` and `runtime-ops-events.schema.json:174-179` describe the frame, and §28.5.3's `terminate` card plus its Timing and Degradation bullets bound `deadlineMs` rather than the trigger. §15.4.2's table describes DRAINING only, and spec/04 contains no "`terminate` frame" occurrence at all.
- **The specification's pod-self-report posture does not reach `exited_cleanly`.** It is written only about checkpoint chunk sizing (spec/11:37, spec/10:147, spec/13:196), the adapter is a platform-controlled container rather than the untrusted agent, and `leaked = err != nil || !cleanly` is shipped for every ordinary session end. A finding framed as "SPEC-3 sources the leak bound from an in-pod self-report" is refuted on all three grounds.
- **The staged edits touch NO §10.3 / §13.1 / §13.2 control,** verified by absence: no RBAC, NetworkPolicy, admission webhook, ServiceAccount, tenant-pin, `lenny-cred-readers` or ephemeral-container-guard text is added, removed or qualified, and there is no chart, podspec, schema or migration change. `ExcludePods` is a read-only skip that can only narrow a candidate set, so it cannot cross a tenant pin.
- **0079 shares NO spec anchor and no code file with 0081,** re-derived after the 0078 impacts row's rewrite deleted the sentence that recorded it. 0079's SPEC-1 replaces §4.7.9 step 7 (0081 appends to step 5), SPEC-2 is §4.7.10, SPEC-3 appends after §5.2 scrub step 6, SPEC-4 replaces §5.2's `**Recycling and integration levels**`, SPEC-5 is §5.2 sizing text, SPEC-6 is §6.1 and the §6.2 POD-level recycle edges; 0081's §5.2 anchors are `**Scrub model.**`, the `**Slot cleanup:**` list and `**Max retries:**`, and its §6.2 anchor is the PER-SLOT fence. The impacts table no longer carries a 0079 row, so this entry is the record.
- **BUILD-GAPS F-5.2.33 is open at BUILD-GAPS.md:4132 and its suggested resolution splits two ways:** part (a) drop `p.listener.Close()` (socketruntime.go:467), which is proposal 0078, and part (b) name the component that creates the successor runtime process on the sidecar model, which is proposal 0079. 0078's own fixed decision reads "After it lands, a recycling sidecar pod still serves one session, and it fails at the accept timeout rather than by dialing an address that no longer exists" (0078:46-48), which is why the 0078 impacts row withdrew its widened-exposure premise.
- **0080 §1.19's inventory is scoped to `CoordinatorFence` refusals,** so CODE-2's rollback status code does not enter it; the rollback deregisters nothing, and what changes `boundSlotState`/`checkSessionBound` membership is the reclaim's `Shutdown`. Choosing `Aborted` keeps a fourth class off the overloaded `FailedPrecondition` rather than adding one.
- **`tests/tier4_integration/concurrent_workspace_test.go` wires no `srv.Lifecycle`,** so `drainViaLifecycle` returns at its nil check (session.go:299-302) and no §15.4.2 frame is observable in that fixture at all; the only tier-4 file that wires one is `credential_lifecycle_test.go:170`, which builds `cmd/runtimes/streaming-echo` rather than `echo-concurrent`. `RuntimeOps.Terminate` additionally swallows `errLifecycleNotConnected`, so a nil `Lifecycle` is invisible.
- **The shipped tier-7a `TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` pins BOTH arms of the `boundRemains` gate in one body** (shutdown_drain_gate_race_test.go:232-304): two started co-tenants tear down from a rendezvous, the frame must arrive before the last close, and the frame count must be one across the pair. That is why the tier-4 §15.4.2 clause was deleted rather than wired: it pinned nothing the tree does not already pin.
- **The tier-4 case's step order is forced rather than arbitrary.** Carol must bind and start before alice's `Shutdown`, because alice's is the last close, `releaseActiveLocked` empties the set and `Close` then shuts the never-rebound listener (socketruntime.go:435-467), after which carol cannot accept.
- **`pkg/gateway/sessionserver/slotretry_test.go` already carries both tables the `Aborted` coupling rows go into:** `TestSlotBindErrorReason_spec_5_2` (a `{stage, code, want}` table with a `{"session_start", codes.PermissionDenied, SlotReasonPolicyRejection}` row) and `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` (:383-407,:468-492). No new fixture and no new file is owed.
- **`slotlayout.EnsureTree` creates `/run/lenny/slots/{sessionId}` at `ensureSlotStateLocked` time, before any binding,** so the tier-1 "Registered but unbound" case's credential-directory assertion is achievable against a workspace-RPC-only entry. `slotlayout.RemoveTree` accumulates the removal error across all four trees including `CredentialsDir` and returns the first (tree.go:58-70), so SPEC-3's "a cleanup that does not complete is still accounted" holds for the credential half as well as the workspace half.
- **`slotretry_load_test.go` builds its `req(...)` INSIDE each of its 64 goroutines,** so the pointer change gives each goroutine its own request and the `ExcludePods` append cannot race. Sharing one pointer across them would be a `-race` failure on a shipped threshold test. The file is in "Files touched" and in no `## Testing` case, which is correct: signature adaptation, not new coverage.
- **`resumeOnPod`'s only unreclaimed abandon is the `fenceResumedPod` failure at start.go:4066-4068,** which returns with no `rollbackBinding`; the sibling `acquireCoordinationLease` failure at :4054-4057 DOES call `rollbackBinding`, which routes through `Binder.ReleaseSlot` and therefore sends the adapter `Shutdown`. Outside this proposal because §7.1's obligation ends when the attempt succeeds and `podBinder.Resume` had already returned.
- **`noteRuntimeStartedLocked` is called from exactly one place, `noteRuntimeStarted` (runtimegeneration.go:32),** so the tier-7a paragraph's "`noteRuntimeStarted` is the only writer of `runtimeLive`" is true as written even though the write physically happens in the `Locked` form, and it does not contradict the Settled entry naming two writers.
- **The whole-staging citation sweep is COMPLETE and clean; do not re-run it.** `[non-spec-recheck.5.review-citations.1]` opened, line by line, every citation in the non-spec staging and the summary across `pkg/adapter`, `pkg/gateway/podlifecycle`, `pkg/gateway/sessionserver`, `pkg/sandbox/slotstate`, the tier-3/4/7a/10/11 test files, `spec/04,05,06,07,28,29`, `schemas/`, the migration, and the cross-proposal references, and every one resolved. Four later lenses spot-checked subsets and found no drift.
- **DECISION: the compensating `Shutdown` splits its two bounds by SHRINKING the adapter's grace.** `compensateFailedSlotBind` hoists `budget := slotCleanupBudget(req.CleanupTimeoutSeconds, req.MaxConcurrentSessions)`, uses it for the `context.WithTimeout(context.WithoutCancel(ctx), budget)`, and passes `budget/2` as `cl.Shutdown`'s fourth argument. `slotCleanupBudget` keeps its signature. ALTERNATIVES rejected: doubling the RPC bound to twice the §5.2 figure (the most faithful reading of §5.2, whose figure is the adapter's enforcement number, but it doubles a worst case that already degenerates to the whole 60s `cleanupTimeoutSeconds` on an exclusive pool reached through `Binder.Resume`, inside a path holding the client's HTTP request); a named `slotCleanupGrace` helper or a package constant (one relation belongs in one place, and a bare constant is a non-spec default with no override); `slotCleanupBudget` returning `(budget, grace)` (equivalent and acceptable, but take it INSTEAD of the inline `budget/2`, never both); leaving `deadlineMs` at zero and widening the RPC bound alone (impossible, because with zero the adapter derives its grace FROM the RPC context's deadline).
- **With `deadlineMs: 0` the adapter's SIGTERM pivot and the gateway's give-up instant are the SAME instant.** `contextWithGraceDeadline(ctx, 0)` returns the inbound RPC context unchanged (session.go:327-332), and `resolveShutdownGrace` PREFERS that context's remaining deadline over the runtime-configured grace and over the package default (mcpruntime.go:311-324). A runtime that uses its grace was therefore ALWAYS reported unacknowledged, so a completed reclaim was recorded `Leaked`. That is the defect the split closes.
- **The strongest production-reachable argument for the split is the `terminate` frame, not the runtime close.** `Shutdown` feeds `req.GetDeadlineMs()` into `drainViaLifecycle` (session.go:260), `RuntimeOps.Terminate` serialises `DeadlineMs int32` with `omitempty`, and the frame REQUIRES `deadlineMs` with `minimum: 100`. `budget/2` is at least 2500ms because the budget carries a five-second floor, so this call site becomes schema-correct as a side effect. EVIDENCE: pkg/adapter/runtimeops.go:65,:486-490; schemas/runtime-ops-events.schema.json:180-183.
- **The §11.4 precedent is exact and is the shipped 2:1 relation.** `userTerminateRPCTimeout = 20s` bounds the call and the shorter `userTerminateDeadline = 10s` is the fourth argument, with a doc comment stating the reason ("exceeds userTerminateDeadline so the gateway observes the pod's graceful exit before giving up on the call"). EVIDENCE: cmd/lenny-gateway/user_revocation.go:45,:50,:55,:128-129.
- **DECISION: the §11.4 precedent is named in the staged Go doc comment by the constant names,** `userTerminateRPCTimeout` and `userTerminateDeadline`, rather than by a `cmd/lenny-gateway/user_revocation.go:47-55` file:line, because a line citation inside a shipped Go comment drifts for the same reason `code-best-practices.md` and channel-naming N8 bar spec line numbers.
- **`Client.Shutdown`'s fourth parameter is a `time.Duration`,** converted inside the client with `int32(deadline.Milliseconds())`, so `budget/2` is type- and unit-correct and cannot overflow int32 at any admissible `cleanupTimeoutSeconds`. A reviewer expecting a millisecond integer reads the staged call as a type error. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807,:813-818.
- **A grace-expired close is reported as CLEAN.** `SocketRuntimeProcess.Close` returns `p.listener.Close()` on both the `cmd.Wait()` arm and the `time.After(grace)` kill arm, and `MCPRuntime.Close` returns nil on the kill arm; only a natural non-zero exit or a listener-close failure yields `closeErr != nil`. So shortening the window cannot manufacture a `leaked` classification, and the observable is "the RPC answered" rather than "the runtime exited gracefully". EVIDENCE: pkg/adapter/socketruntime.go:435-467; pkg/adapter/mcpruntime.go:294-305.
- **Only two of the three `RuntimeProcess.Close` implementations read the plumbed deadline.** `MCPRuntime.Close` and `SocketRuntimeProcess.Close` do, through `resolveShutdownGrace`; `InProcessRuntime.Close(_ context.Context, …)` discards it and blocks unbounded on `<-done`. `executor.SubprocessExecutor.Close` also ignores ctx and hard-codes a ten-second pivot, so it can already overrun a five-second budget. EVIDENCE: pkg/adapter/embedded.go:188-204; pkg/gateway/session/executor/subprocess.go:341-367.
- **`RuntimeOps.Terminate` does not block; it writes one frame and returns.** That is what keeps `drain + close` from consuming the whole RPC budget, so the handler's timed work is `Runtime.Close` at roughly `budget/2` plus `removeSlotTree`, comfortably inside `budget`. EVIDENCE: pkg/adapter/runtimeops.go:486-491; pkg/adapter/session.go:299-307,:327-332.
- **The adapter enforces NO per-slot cleanup timeout of its own.** `grep "cleanupTimeout\|CleanupTimeout" pkg/adapter/*.go` returns only the WHOLE-POD scrub's `CleanupTimeout` at podscrub.go:128, and `resolveShutdownGrace` applies no floor. So §5.2's "(minimum 5s enforced at runtime by the adapter)" has no implementation, a `deadline_ms` of 2500 is not clamped back up, and the split is not halving any figure the adapter enforces.
- **`slotCleanupBudget`'s divisor is safe in production and reachable in fixtures.** `SlotBindRequest` is built only under `match.MaxConcurrentSessions > 1`; `ResumeRequest` normalises through `maxConcurrentSessions()` at start.go:4029. The three `podsession.ResumeRequest{…}` literals in `binder_test.go` (:492, :533, :1650) leave the field zero, and :533 IS the `cl.Resume` failure branch CODE-4 puts the compensation on, so landing CODE-4 without giving that case a non-zero concurrency yields an integer-division panic rather than a failing assertion. `CleanupTimeoutSeconds` is `int` while `MaxConcurrentSessions` is `int32`, so the body needs an explicit conversion.
- **`slotBindRequest` DOES populate `CleanupTimeoutSeconds`** (start.go:2566-2567), despite the neighbouring `exclusiveBindRequest` comment at :2524-2529 reading as if the concurrent request omitted it. A reviewer trusting that comment concludes the budget always falls to its 5s floor. `ResumeRequest` carries both inputs too, populated from `match.CleanupTimeoutSeconds` at start.go:4037.
- **`SlotBindRequest.CleanupTimeoutSeconds` lives at slotbinder.go:108.** The three `CleanupTimeoutSeconds int` hits in binder.go (:478, :520, :667) belong to `BindRequest`, `LaunchRequest` and `ResumeRequest`, so a package-scoped grep misleads about which struct owns which field.
- **`ReleaseSlotReservation` has exactly SIX production sites and CODE-4's table names all six:** slotbinder.go:172 (`ClaimSlot`'s connect-stage release), :217 (`BindReservedSlot`), binder.go:1714 (`releaseResumeSlot`), start.go:2727 (the `slotBinder` interface declaration), :2834 (`applySlotRetryPolicy`), :3246 (`rollbackClaim`). No seventh exists. It also IGNORES its `slotID` parameter today, so "the staged call sites thread the slot id" is not evidence of anything.
- **The reclaim gate has exactly THREE production entry points:** `Binder.BindSlot` (start.go:2810 through `applySlotRetryPolicy`), `Binder.BindReservedSlot` (:2596) and `Binder.Resume` (:4005), and `bindReservedSlot` funnels into `materializeSlot` too, so one wrapper covers both bind paths. Do not re-derive the "is a bind path missed" question.
- **`spec/18` needs no edit, and the question is closed.** Its phase-12c deliverable lines (spec/18:531-533, exit criteria :538) name "the per-session slot cleanup and whole-pod scrub split" and the slot-counter gate without enumerating a precondition, a report rule or a state-machine edge, so nothing SPEC-1 through SPEC-4 stages can make a phase deliverable stale. Three lenses derived it independently.
- **`validate-maps` compares FILE paths, never test names.** `validateTestFilesMapped` strips `::TestName` and `/...` from every spec-map entry and matches the walked `_test.go` path or any ancestor directory against that set, which is why `concurrent_workspace_test.go` (mapped as a bare path) and `recycle_scrub_path_test.go` (mapped only as `::Test…` entries) both pass. A new test FUNCTION in a listed file is genuinely free. EVIDENCE: cmd/lenny-test/cmd_validate.go:733-741,:768-776; tests/spec-map.json:552,:1040.
- **The gateway test files split across the package boundary, and the split decides where a case can live.** `slotretry_test.go`, `slotretry_load_test.go`, `queue_internal_test.go`, `start_preclaim_internal_test.go` and `terminal_reclaim_internal_test.go` are `package sessionserver`; `start_test.go` and `start_pod_test.go` are `package sessionserver_test`. `s.slotStates` is built unconditionally by `slotstate.NewRegistry()` at sessionserver.go:1873 with no `Options` field, so an external test cannot observe `MarkLeaked` and must discriminate through the injectable `SlotLeakGauge`/`SlotReplacement`/`SlotHealth` callbacks.
- **The adapter test files split the same way.** `socketruntime_test.go` is `package adapter_test`; `slotsession_test.go`, `export_test.go`, `usage_test.go`, `adapterevents_test.go` and `podmcp_arming_internal_test.go` are `package adapter`. The "Co-tenancy hazard" case is split correctly across that boundary, because the shipped `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` already observes the connection behaviourally (EOF on the reader) rather than by reading `p.conn`.
- **`live && !started` is unreachable, and so is `live ⊄ started`.** `runtimeLive` membership is written only by `noteRuntimeStartedLocked`, which runs only after `claimSessionSlotUnderLock` set `st.started`, and `st.started` is never cleared; every deleting path either ran the `started` block (which calls `noteRuntimeClosed`) or ran pre-`Runtime.Start`. That is what makes CODE-1's `live` report gate safe in BOTH directions and gives the `if live` report a meaningful `closeErr`.
- **`deregisterSlotLocked` returns a NIL `*slotState` when `removed` is false,** so CODE-1's `started := removed && st.started` and `live := removed && s.runtimeHoldsLocked(sessionID)` are safe only because Go's `&&` short-circuits. EVIDENCE: pkg/adapter/slotsession.go:174-189.
- **CODE-1's staged clause two preserves the shipped ORDER exactly for a started session.** Shipped `if bound {}` runs emitFinalUsage → drain → Close → noteRuntimeClosed → removeSlotTree → cancelPodMCPIfRuntimeIdle → reportSessionScrub; staged runs `if started {…}` then `if removed {…}` then `if live {…}`, and `started ⊆ removed`, `live ⊆ removed`. An ordering finding stops here.
- **`removeSlotTree` cannot be widened into a cross-slot delete.** It takes `st.paths`, resolved once at `ensureSlotStateLocked` from the map KEY rather than from `st.sessionID`, and `ValidateSlotID` refuses an empty id before an entry can exist, so a registered-but-unbound entry has an empty `sessionID` and fully resolved paths. CODE-1's move of the removal from `bound` to `removed` cannot produce `RemoveAll("/workspace/slots")`. EVIDENCE: pkg/adapter/slot.go:112-126,:210-212; slotlayout/tree.go:59-71.
- **A §11.4 revoke cannot reach a bound-but-unstarted or a started-but-not-`runtimeLive` session at all.** `podTerminateFanOut.terminateLocal` iterates the per-replica `podsession.Registry` snapshot and skips any session with no binding, and a binding is registered only after the bind returns. So the only caller that reaches CODE-1's withheld branches is the new compensating `Shutdown`, where today nothing is sent. EVIDENCE: cmd/lenny-gateway/user_revocation.go:110-137. This also makes `emitFinalUsage`'s move to the `started` branch a no-op for every shipped caller.
- **`slotbinder.go:542` is the ONLY production reader of the `Shutdown` bool.** The other two adapter-RPC `Shutdown` call sites, binder.go:2043 and user_revocation.go:129, both discard it. That is the whole blast radius of CODE-1's re-keying of `exited_cleanly`, and `Binder.ReleaseSlot` also ignores the `ShutdownRecycle` response, so the occupancy-zero recycle edge is untouched.
- **`stageWorkspace` cannot fail on an upload-free plan,** so every reachable `stageWorkspace` failure carries uploads and `FinalizeWorkspace` (which calls `ensureSlotPaths` at the top of the handler, as `RunSetup` does) is the first RPC an upload-free plan reaches. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1284-1332,:1346-1354; pkg/adapter/staging.go:181,:337; slot.go:140-148.
- **`codes.Aborted` collides with nothing on this surface.** `grep -rn "codes.Aborted" pkg/ cmd/` returns only pkg/adapter/{checkpoint.go:115, oplock.go:36-40,:82}; no gRPC interceptor, service config or retry policy anywhere under `pkg/gateway/runtime/adapterclient` or `cmd/lenny-adapter` treats a status code specially; and the absence of any `retryPolicy`/`MaxAttempts` means a `Shutdown` that reached the handler is never re-sent, which keeps `RecordSessionScrub`'s missing dedup harmless on this path.
- **`isTransientPodClaimError` returns false for any bare gRPC status,** matching only `*PoolWarmingError`, `*CredentialAssignmentError`, `*SetupCommandFailure` and five sentinels (start.go:3648-3682). So the resume rollback's `Aborted` is inert there and a raced `Resume` demotes the row to `failed`, exactly as the adjacent shipped `codes.Internal` at resume.go:140 already does. The code choice adds no new class on that path.
- **`resumeOnPod`'s two branches are mutually exclusive** (the snapshotless rebuild returns at start.go:3977), so CODE-5's third `accountSlotFailure` caller cannot double-account with `applySlotRetryPolicy`'s. Every collaborator that caller needs is in scope: `match` (built at :3980-3984), `s.slotHealth`, `s.slotStates`, `s.slotReplacement`, `s.slotLeakGauge`, `s.podBinder`.
- **The `materializeSlot` wrapper's `errors.As(err, &sbe)` guard is TOTAL over today's stages.** All five stage failures return `b.slotBindError(…)`, a pointer, so no post-connection failure escapes the compensation and the unconditional `releaseCredentials` outside the guard is future-proofing rather than a live gap.
- **`ClaimSlot`'s terminal error selection is ordered `len(list.Items)==0 → ErrNoIdlePod`, else `sawTenantMismatch → ErrTenantMismatch`, else `ErrNoConcurrentSlot`,** and the `ExcludePods` skip sits AFTER the tenant check, so it empties the candidate set without touching `sawTenantMismatch` and cannot suppress an `ErrTenantMismatch` a fall-through depends on. EVIDENCE: podclaim/slotclaimer.go:428-441,:505-522.
- **`connectSlot` has NO Postgres fallback claim,** unlike the whole-pod `connect` (binder.go:1745-1755). `ClaimSlot` is the single placement route on the bind path, so `ExcludePods` honoured in its two candidate loops cannot be bypassed by a third route.
- **`runWithQueue` is a free generic function** (queue.go:134) rather than a `*Server` method, and `newPodClaimQueue(poll, clock)` is already driven that way at queue_edges_internal_test.go:42 and queue_internal_test.go:95, so the staged queue-re-entry tier-1 case can compose it directly over `applySlotRetryPolicy`.
- **Every `podsession.SlotBindRequest` and `podclaim.SlotRequest` composite literal in the tree is KEYED,** so adding `ExcludePods` breaks no build. There is also a NINTH `classifySlotBindFailure` call site the "twelve plus four" inventory does not name, `start_preclaim_internal_test.go:1019`, which passes a keyed literal by value and needs no edit.
- **Every import CODE-1, CODE-2 and CODE-4 need already exists at the target file.** session.go already imports `codes`, `status`, `time` and `pkg/observability/tracing`; resume.go already imports `codes` and `status`; slotbinder.go already imports `context`, `errors`, `log` and `time`. No import churn is an unlisted edit.
- **`claimSessionSlot` has exactly three production callers** (session.go:111, resume.go:50, sdkwarm.go:217), so `st.started` cannot be set by any path that is not one of the three start RPCs §4.7's staged row names. That is what makes "a session whose start the adapter has admitted" implementable as `st.started` with no residual class.
- **The startup and creation SLOs cannot be moved by the compensation.** `recordStartupDuration` has exactly two call sites and both sit after an `if err != nil { return }` (start.go:2401, :2673), so `lenny_session_startup_duration_seconds` samples successful starts only and is composed from the successful attempt's `BindTimings`; the burn-rate expressions divide by that histogram's count (alerting/rules/slo.go:214-242). §16.5's creation SLO is scoped to receipt through the `session_id` response and excludes the bind.
- **The served-session count reaches no observability surface.** `grep "sessions_served\|sessionsServed\|served-session\|served_session"` over spec/16, docs/reference/metrics.md, docs/runbooks/ and pkg/alerting/rules/rules.go returns ZERO hits. `lenny_pod_session_reuse_count` (spec/16:128) is the nearest row and SPEC-3 only stops it counting a session the pod never ran.
- **`MarkReleased` has NO production caller,** and `MarkLeaked`/`ForgetPod` have exactly one each, both in `applySlotRetryPolicy`. A second leak for the same session on a different pod keeps the first record's `pod` field (registry.go:99-115), so the second pod's gauge is published with the first pod's count. Pre-existing, metric-only, and the drain threshold is unaffected because `slothealth.Tracker.RecordLeak` is keyed by pod.
- **`lenny_adapter_leaked_slots` is registered `{pod_id, pool}` and is ZEROED rather than deleted** at the end of `applySlotRetryPolicy`'s drain arm (start.go:2869-2871). Pre-existing cardinality growth; CODE-5 only widens the set of pods reaching an existing emitter.
- **The Sandbox finalizer chain is bounded on both arms, and it is four files deep.** Every Sandbox carries `lenny.dev/session-cleanup`, held while `activeClaimReferences` finds a non-terminal `SandboxClaim` for it, which a `leaked=true` release leaves at `bound`. §4.6.1 orphan GC's `reclaimByDraining` drains the pod AND `Delete`s the claim, releasing the finalizer, gated on `PodHasActiveSession` being false. EVIDENCE: pkg/controller/sandbox/finalizer.go:60-84,:111-126; pkg/controller/warmpool/gc.go:222-248,:271-290,:359-368.
- **`writeBoundStatus` runs inside `reserveSlot`'s fresh-pod arm immediately after `CreateClaim`** (slotclaimer.go:689-712), so the tier-4 datastore-crossing case's "the per-pod `SandboxClaim` survives at `bound`" is assertable rather than an empty-phase claim.
- **`drainReason` is the ONLY reader of `ShutdownRequest.reason` repo-wide,** at session.go:260. The SDK's `sd.Reason` reads are the frame's reason rather than the RPC field, and the runtime SDKs pass the frame's reason through as an opaque string to `invokeTerminate(reason, deadlineMs)` in all three languages, so a new reason value costs no SDK edit.
- **`pkg/gateway/externalapi/openapi/openapi.json` carries no error-code enumeration at all,** so no gateway error-path change can ever be an OpenAPI edit site. Confirmed by greps for `WARM_POOL_EXHAUSTED`, `STARTING_FAILED` and `no_idle_pods` across its 4301 lines.
- **`applySlotRetryPolicy` has 12 test call sites, not 13** (slotretry_test.go 11 plus slotretry_load_test.go 1). The proposal states no count, so nothing is owed; recorded so a later round does not re-derive it.
- **`sdkwarm.go` has a SECOND writer that clears `s.sdkConnected` besides `DemoteSDK`,** `ShutdownDemoteSDK`'s timeout branch at :104. CODE-2's "which only `DemoteSDK` clears" is loose, and its conclusion is untouched because :104 is a process-shutdown path.
- **`p.addActiveLocked` is at socketruntime.go:184 and :220 and `return p.listener.Close()` at :467.** Hand-counting from a `sed` window is off by one here because of a wrapped comment; use `grep -n` on this file.
- **The whole summary structure was rebuilt and verified by the `f5` cleanup firing.** The required sections stand in the required order, `## Open decisions for human to make` carried entries 9 and 11 at that firing (it now carries 11, 15, 16, 17 and 18 — see the correction below), `## Defects in the shipped tree that this proposal does not stage` carries eight entries, `## Impacts on other proposals` carries nine rows with no duplicate subject (four for 0080, one each for 0073, 0075, 0078, R1b and R12), and `## Deliverable index` is last. Nothing was relocated because nothing was misplaced.
- **The 0078 relationship, settled and re-verified.** 0078 keeps the pod's runtime listener bound across a session teardown, so it does not widen this exposure, and it is sequenced after this proposal for the test-file reason the impacts row records. The overlap in `tests/tier7a_load_local/` is package co-location rather than a file collision, and 0081 opens neither production file 0078 changes (`pkg/adapter/socketruntime.go`, `cmd/lenny-adapter/main.go`) nor DOCS-1's target (`docs/runtime-author-guide/lifecycle.md`). CORRECTED: problem-statement's "0078 exists to make pods survive across sessions, which widens exactly this exposure" was withdrawn and rewritten; the sequencing conclusion is unchanged.
- **The 0078 maintenance obligation is one third of one assertion.** 0078's CODE-1 kills only the LISTENER half of the staged co-tenancy-hazard assertion; its connection-close and child-kill halves still discriminate afterwards, because 0078 leaves the occupancy gate, the shared-connection close and the graced child reap untouched.
- **A hand amendment added a whole new mechanism late, and the earlier sweeps never saw it.** `grep -n "epoch"` over this review log returned zero hits when the `spec.1` lenses opened, so every sentence the bind-epoch amendment (commit 1344047d4) added is unreviewed by the seven sweeps that preceded it. Do not lean on an older "verified clean" verdict for any epoch or reclaim-hold text.
- **DECISION (r6, REVERSING the r5 per-attempt design): the bind epoch is minted PER REGISTRY ENTRY.** The adapter mints it when it creates an entry for a slot identifier it holds none for; NO bind-sequence request carries one; the seven bind-sequence RESPONSES report it; only `ShutdownRequest.expected_bind_epoch` carries one on the wire. The per-attempt form with a seven-request echo was built in r5 and withdrawn in r6, and the inheritance hazard it existed to close (a retry that adopts a surviving entry inherits that entry's epoch, so a lagging reclaim compares EQUAL) is now a recorded accepted residue rather than a defect. CORRECTS the pass-7 Settled entry that recorded the per-attempt form; six lenses across rounds 6, 7 and 8 filed the correction.
- **DECISION: the epoch rules are stated ONCE in SPEC-5 §4.7.1 and published once in SPEC-5 §15.4.** The epoch is a positive integer, so zero is not an epoch: a request whose bind epoch is zero carries none and a response reporting zero reports none. A `Shutdown` carrying an epoch performs the slot release AND the runtime teardown only at that epoch, otherwise neither, answering `superseded`; a `Shutdown` carrying no epoch is the unconditional teardown; a `Shutdown` carries an epoch ONLY when it compensates a bind attempt the caller abandoned. The bind-path admission rules the r5 form carried are gone with the request-side echo; the reclaim hold is what refuses a bind, with `ABORTED`.
- **DECISION: the epoch and the reclaim hold stay two separately named mechanisms.** The epoch governs who may bind while the registry entry LIVES; the hold governs the interval after the entry is GONE and before the cleanup finishes. Collapsing them puts one fail-closed gate on registry state the cleanup no longer has.
- **DECISION: "slot-identifier occupancy" is renamed the **slot-identifier reclaim hold** and re-windowed.** It starts in the critical section that deregisters the entry, ends when the cleanup finishes, and carries no `leaked` clause. BECAUSE "occupancy" is already bound to the pod's Redis slot-counter occupancy a `leaked` slot holds until pod termination (spec/06:160, spec/05:545), and a second sense of the word produced three findings at once. §5.2's append is `**Slot-identifier reclaim hold.**` (spec-changes.md:552), §6.2's sentence says "the adapter holds the slot's identifier" (:614), §15.4's companion block is `**Slot-identifier reclaim hold:**` (:662).
- **DECISION: §7.1 carries ONE leak predicate, "did not complete".** A reclaim did not complete when the adapter did not answer it, or answered `reclaimed` without reporting a clean exit; `superseded` and `absent` are completed reclaims and leave the slot unleaked. §5.2's `**Max retries:**` bullet and its commentary read "did not complete" / "incomplete reclaim" to match. That is exactly CODE-4's discriminator and SPEC-3's §5.2 clause, so spec, staged clause and shipped mapping are one statement.
- **DECISION: §7.1's retry-exclusion rationale is the reclaim hold rather than tree reuse.** The adapter admits no bind onto a held identifier, so a further attempt cannot reuse a tree the reclaim is deleting; it is refused transiently. CORRECTED (`spec.6.fix-G1.1`): the clause that charged such a refusal to the §5.2 retry budget is RETIRED, because the exclusion is exactly what stops a policy-placed retry ever reaching the hold. §7.1 and the Design now read "an attempt that meets the hold is one the retry policy did not place". §5.2's `**Max retries:**` bullet carries no reason of its own, so §7.1 is the only place the exclusion is motivated.
- **DECISION: the `- **§29.4.**` bullet was deleted from `## Spec sections deliberately untouched`.** SPEC-1 genuinely edits §29.4 step 13 and `## Spec files touched` names spec/29, so a section on both lists inverts the list's stated purpose. Nothing is lost: SPEC-1's own block carries the step-12 rationale in full, and the unconditional-form claim stands at spec-changes.md:242 (§4.7 row), :600 (§4.7.1 caller rules) and :622 (adapter rules).
- **The bind sequence is a closed set of SEVEN RPCs:** `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `StartSession`, `ConfigureWorkspace`, `Resume`. Exactly seven adapter sites create-or-resolve through `ensureSlotStateLocked`: staging.go:134,:181,:337; slotcreds.go:26; slotsession.go:75 (from session.go:111); resume.go:50; sdkwarm.go:217.
- **"Creates or resolves a slot registry entry" is MUCH wider than the bind sequence, and must never scope the epoch.** `Attach` (attach.go:41), `Interrupt` and `SignalDeadline` (lifecycle.go:30,:80), `SendMessage` (session.go:186), `Checkpoint` (checkpoint.go:94), `CheckpointBarrier`/`CoordinatorFence` (coordination.go:116,:254), `ExportPaths` (exportpaths.go:45), `ReportUsage` (usage.go:266), `RotateCredentials`/`RevokeCredentials`/`ExtendCredentialLease` (slotcreds.go:70,:105,:125) all resolve the entry. Six lenses derived this independently; it is the ground of the filed §4.7.1-versus-§15.4 finding.
- **Each bind attempt dials its OWN adapter connection,** `b.DialAdapter(addr)` in `connectSlot` and `bindReservedSlot` with `cl.Close()` on every failure branch, so one attempt is one `adapterclient.Client` is one epoch latch and the caller-side echo is free. EVIDENCE: podsession/slotbinder.go:182,:237,:459.
- **`ensureSlotPaths` runs at the TOP of `PrepareWorkspace`, `FinalizeWorkspace` and `RunSetup` and creates the entry when absent** (slot.go:135-148; staging.go:134,:181,:337). Two consequences the amendment's text does not account for: an attempt's own second and third RPCs re-enter the entry-creating path, so a per-entry epoch is re-minted fresh if the entry was deregistered mid-attempt; and a hold that starts at entry creation lands in the MIDDLE of the bind sequence.
- **The free request-field numbers banked for the WITHDRAWN echo, kept only as a record:** `PrepareWorkspaceRequest` 5, `FinalizeWorkspaceRequest` 6, `RunSetupRequest` 5, `AssignCredentialsRequest` 4, `StartSessionRequest` 12, `ConfigureWorkspaceRequest` 5, `ResumeRequest` 16, each message reserving the number one below for `slot_id`. The r6 revert deleted the request side, so SCHEMA-1 stages none of them and "nine fields" is the correct count again. `ShutdownRequest` holds 1,2,3,5,6 with 4 reserved, so SCHEMA-1's 3 and 7 are free.
- **Adding `bind_epoch` to the seven bind REQUESTS raises no §4.1 obligation.** §4.1 derives a request's scope from a top-level `session_id` of type `SessionId`; all seven already carry one, so no classification changes and only the `ShutdownRequest` paragraph needs the staged precondition sentence. EVIDENCE: spec/04:151-157.
- **"epoch" is already bound in `spec/`, but only by §25's remediation-lock outage epoch** (`ops_lock_epoch`, spec/25:2095,:2204,:2224). Different domain, and the staged term is always the two-word "bind epoch", so there is no channel-naming N3 stem collision. `spec/` has no glossary section either, so a new bolded term mints no spec glossary row; the only glossary is `docs/reference/glossary.md`.
- **`Shutdown` appears in NO §28 register row and on no §28.5.1 contract card.** The gateway-to-pod channels on `LNK-POD-GRPC` are CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER and CH-PODHEALTH; the bind and teardown RPCs are carried by no registered channel. So the amendment's "adds no §28 row" claim holds, and SCHEMA-1's two claim-map rows anchored at `#2851-gateway-to-pod` point at a card that states nothing about the epoch.
- **§15.4 is the right home for the published contract.** Its own preamble names `schemas/lenny-adapter.proto` as the gateway↔adapter gRPC surface, so SPEC-5's placement is not a runtime-author/adapter-author mixup. Its two insertion points are real and unambiguous: spec/04:693 (after the Adapter→Gateway table, before `#### 4.7.2` at :695) and spec/15:1469 (after `**SDK-warm demotion contract:**`, before `#### 15.4.1` at :1471). The anchors it mints resolve: `#471-role-and-gateway-rpc-contract` (spec/04:659), `#1542-rpc-lifecycle-state-machine` (spec/15:1686), `#101-horizontal-scaling` (spec/10:3).
- **The specification DOES name gRPC status codes where the gateway's §5.2 category depends on them:** spec/07:208 (`FailedPrecondition`), spec/15:1136 ("every gRPC code other than `FailedPrecondition`"), spec/05:515 (`InvalidArgument`). An argument shaped "the spec never states gRPC codes" is false. UNVERIFIED: `spec.1.review-reliability.1` declined the same finding on the opposite ground (that §4.7 names no codes for any adapter RPC) and neither shard corrects the other; a round wanting to name a code for the hold refusal should settle which reading governs §15.4.
- **Retryability on the bind path is derived from the gRPC STATUS CODE alone,** and the two codes an adapter author would most naturally reach for both classify NON-retryable: `InvalidArgument` → `workspace_validation`, `PermissionDenied`/`FailedPrecondition` outside `workspace_prep` → `policy_rejection`. Everything else defaults transient. A third-party adapter picking either for the hold refusal produces a permanently dead session. EVIDENCE: podsession/slotfailure.go:41-47,:84-102.
- **The proto publishes a structured `Error` envelope with `Category{TRANSIENT,PERMANENT,POLICY}`** (schemas/lenny-adapter.proto:531-552) and NOTHING in the gateway's slot-bind path reads it. Do not assume "transient" in spec prose resolves onto that enum.
- **`claimSessionSlotUnderLock` refuses a second start on `st.started` with `codes.Unavailable`** (slotsession.go:80-85), so the epoch-inheritance hazard bites only when the predecessor failed BEFORE its start was admitted (finalize, setup, credential assignment). Do not dismiss it on the started case.
- **Three refusals now travel out of `ensureSlotStateLocked`** — cleanup hold, superseded epoch, and an epochless request against a STARTED entry — and the five resolve sites re-wrap any error from it as `codes.InvalidArgument` with `tracing.CategoryPermanent`. All three must reach the gateway as `codes.Aborted`. EVIDENCE: staging.go:134-136,:181-184,:337-340; slotcreds.go:28; slotsession.go:77.
- **CODE-6's hold is an in-memory `reclaiming map[string]struct{}` taken at deregistration with an unconditional `defer s.releaseReclaim(sessionID)`,** so any spec sentence saying a `leaked` cleanup keeps the identifier held states something the code lane does not implement. EVIDENCE: non-spec-changes.md:171-181,:892-923.
- **The amendment adds no control-plane or data-plane write of any kind.** The epoch is an in-memory `int64` per registry entry and the hold an in-memory map key: no etcd write, no Postgres row, no Redis key, no informer, no watch, no metric series. SCHEMA-1 is one enum and nine scalar fields. The capacity half of a performance lens is structurally inert on the delta; its cost is a new fail-closed GATE, the first staged rule in this proposal that can refuse a bind the shipped adapter admits today.
- **§15.4.6's conformance categories exercise the RUNTIME BINARY over JSONL against a fake adapter** (spec/15:2038-2075), so they cannot host an adapter obligation and the proposal's reason for leaving them alone holds. §15.4.2's state machine (spec/15:1686-1706) is generic and is not falsified by the new pod-global graceful-signal condition.
- **§24.8's external-adapter compliance suite is schema-driven** ("assertions are generated from the published artifacts rather than hand-coded against prose", spec/24:114), so every prose obligation §15.4 publishes is ungenerable, including the pre-existing SDK-warm demotion contract. Pre-existing; do not file it against this proposal.
- **`sandboxclaim_guard.Decide` rejects any operation but `OpCreate`** and applies per-pod uniqueness over non-terminal claims, reading no phase transition, so neither the withheld DELETE nor the withheld `bound → recycling` patch can produce a rejected write. EVIDENCE: pkg/admission/sandboxclaim_guard/guard.go:127-150.
- **`TestRecycleTriggerCrossRefsResolve_F5215` additionally requires the §4.7 `Shutdown` row to carry the literal link `05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes`.** SPEC-1's replacement adds one of its own, so the link survives even if the retained tail's were reworded. A future edit that trims that tail turns both F5215 tests red.
- **`ShutdownRequest.coordination_generation` is field 6 and the proto comment claims a pod validates the generation on every gateway-to-pod RPC.** Only `CheckpointBarrier` and `CoordinatorFence` do. That matters for §7.2, whose step 4 bumps the generation to fence a stale coordinator: if `Shutdown` ever starts enforcing the gate, a fenced attempt's §7.1 compensation becomes unsendable. Nobody has staged anything here. EXTENDED: spec/10:30 already says "Pods validate the generation on every gateway→pod RPC", so a staged sentence asserting the check restates published spec and the code gap is pre-existing; SPEC-5 now makes it a spec-stated obligation on `Shutdown` rather than a code-lane possibility.
- **`AssignCredentialsResponse` is genuinely an EMPTY message,** `message AssignCredentialsResponse {}` at schemas/lenny-adapter.proto:1033, so SCHEMA-1's `bind_epoch = 1` is correct and free. The `session_id = 1 / leases = 2 / rotation_trigger = 3 / reserved 4 / coordination_generation = 5` field set an earlier entry attributed to it belongs to `RotateCredentialsRequest` (:1035-1053), the next message in the file. Eight lenses corrected this independently.
- **All nine SCHEMA-1 field numbers are free, re-checked mechanically.** `ShutdownRequest` 1,2,3,reserved 4,5,6 (new 7); `ShutdownResponse` 1,2 (new 3); `PrepareWorkspaceResponse` 1,2 (new 3); `FinalizeWorkspaceResponse`, `RunSetupResponse`, `StartSessionResponse`, `ConfigureWorkspaceResponse` 1 (new 2); `ResumeResponse` 1,2,3 (new 4); `AssignCredentialsResponse` empty (new 1).
- **Under per-entry minting the epoch fences ONE ordering, and it is reachable in production.** The adapter's own pre-`Runtime.Start` failure branches call `releaseSessionSlot` (session.go:133,:147,:157; resume.go), which deregisters the entry and removes the tree before the gateway learns the RPC failed, so entry E1 is released, a retry creates E2 at a higher epoch, and the lagging compensation naming E1 answers `superseded`. The UNFENCED ordering is the one where the entry SURVIVES and the retry adopts it. A reviewer who reads "per-entry minting was a MISTAKE" and stops there concludes the fence never fires.
- **DECISION: SPEC-1's §4.1 bind-epoch sentence was DELETED rather than reworded.** `expected_bind_epoch` is a bare proto3 `int64` with no wire presence, exactly as `ShutdownRequest.coordination_generation` (field 6, schemas/lenny-adapter.proto:1630-1635) is, and §4.1's rule is about a field's presence standing in for a SCOPE, which neither engages. §4.1's replacement now delegates both teardown preconditions to §4.7 and states nothing about the epoch. Making the field `optional` would collapse the whole argument.
- **The ordinary session-end `Shutdown` DOES ride the bind connection and therefore holds a latched epoch.** `Binder.ReleaseSlot` sends on `BindResult.Adapter`, the client the bind dialled (slotbinder.go:528-545). It is harmless, because the entry the bind created is the entry the session holds, so the epoch matches; the caller rule scoping the epoch to a compensation is what keeps CODE-6's zero-epoch call sites conforming.
- **`DemoteSDK` removes the registry entry AND removes the tree, synchronously inside the RPC.** `Server.DemoteSDK` → `noteRuntimeClosed` + `releaseSessionSlot(anyRegisteredSession())` → `deregisterSlot` then `removeSlotTree` (sdkwarm.go:294-302; slotsession.go:214-220). That is what makes §4.7.1's caller-drop rule true and what makes §5.2's "a release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers" hold for the demotion fallback. Fifteen lenses have now derived it; do not re-derive.
- **The SHIPPED `DemoteSDK` call site is not the one §4.7:673 describes.** §4.7:673 has the gateway call `DemoteSDK` on a `ConfigureWorkspace` FAILURE and fall back to pod-warm; the tree calls it inside `Binder.Prepare` before any workspace RPC (binder.go:878-897), and `Binder.Launch`'s `ConfigureWorkspace` failure just reclaims and returns (:1008-1013). Pre-existing spec-versus-code divergence, outside 0081, and it makes the caller-drop rule vacuous on the shipped ordering.
- **Only `ResumeRequest` (field 14) and `ShutdownRequest` (field 6) carry `coordination_generation` among the epoch-relevant messages;** the other six bind-sequence requests carry none and no bind-sequence RESPONSE carries one. CORRECTED for the r6 revert: no bind-sequence REQUEST carries a bind epoch any more, so `ShutdownRequest` is the ONLY message carrying both and the correct §4.7.1 wording is the bare "as on `Shutdown`". Do not restore the "as on `Resume` and `Shutdown`" form an earlier pass recorded as the correction. Derive it with `awk '/^message /{m=$2} /coordination_generation = /{print NR": "m}'`.
- **`PrepareWorkspace` is the only client-streaming bind-sequence RPC, and the adapter resolves the slot exactly once per call.** `rpc PrepareWorkspace(stream PrepareWorkspaceRequest) returns (PrepareWorkspaceResponse)` (schemas/lenny-adapter.proto:41); `resolvePrepareStagingDir`'s `if stagingDir == ""` guard resolves from the first frame and later frames are checked only for a non-empty session id (staging.go:44-47,:66-83). All seven bind-sequence RPCs have unary responses, so "reports the entry's current epoch on its response" is well formed for each.
- **The §7.4 mid-session upload reuses `PrepareWorkspace` and `FinalizeWorkspace` on the LIVE binding.** `bind.Adapter` is `BindResult.Adapter`, the connection the successful bind dialled and the caller closes when the SESSION ends, and `upload_to_session.go:128,:134` calls both on it with `midSession=true`. §7.4's own prose never names those RPCs; the ground is the handler, so do not file "§7.4 does not say that".
- **The EXCLUSIVE bind spans TWO adapter connections.** `Binder.Prepare` runs PrepareWorkspace→AssignCredentials then `cl.Close()` (binder.go:956), and `Binder.Launch` re-dials through `b.reconnect` before `StartSession`/`ConfigureWorkspace` (:976-1013), so Launch's first bind RPC is necessarily epochless and re-mints. `prepareAtFinalize` returns nil for `MaxConcurrentSessions > 1`, so the split is the exclusive finalize/launch pair only; the concurrent `materializeSlot` keeps one `cl` open from `PrepareWorkspace` through `StartSession`.
- **Each bind attempt dials its OWN adapter client** (`DialAdapter` → `adapterclient.Dial` → `grpc.NewClient`, at slotbinder.go:237,:459 and binder.go:1152,:1776), so the per-connection epoch latch never multiplexes two sessions and cannot name a co-tenant's epoch on a concurrent pod. Check this first before building any epoch-crossing-attempts finding.
- **The agent podspec sets `RestartPolicy: Never`,** so an adapter process cannot restart in place under a live pod. That, and NOT the no-re-dial rule, is what closes the epoch-ABA question: `adapterclient.Dial` uses `grpc.NewClient`, whose `ClientConn` reconnects transparently, so "the connection the attempt already holds" would in fact survive a process restart. EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:980; pkg/gateway/runtime/adapterclient/client.go:48-50,:72-79.
- **The adapter has THREE shipped bind-path refusals that are not the reclaim hold,** and none is stated in `spec/`: `codes.Unavailable` "pod is not idle: session %s is already bound on this pod" (slotsession.go:69-72), `codes.Unavailable` "session %s has already started on this pod" (:82-85), and the `codes.InvalidArgument` unresolvable-slot wrap (:76-78). §10.1's hold state is a fourth, rejecting every non-`CoordinatorFence` RPC with `UNAVAILABLE` (spec/10:57). Two of this proposal's edge cases lean on the started-session refusal surviving.
- **DECISION: the "This is the only rule under which the adapter refuses a request on the bind path" sentence was DELETED from §5.2** rather than narrowed, and its two pointer restatements in §4.7.1 and §15.4 were re-scoped to their own blocks. Importing a partial refusal list into normative §5.2 would mint normative surface nobody reviewed, because pod-not-idle and started-session have no spec statement anywhere.
- **`removeSlotTree` takes NO `context.Context` on either cleanup path** (`func removeSlotTree(st *slotState) error`, slot.go:208-212) and runs after the grace-bounded `Runtime.Close` (session.go:262-271; holdstate.go:200-201,:254 bound only the close). The `Shutdown` handler blocks on the removal before building its response, so no layer bounds the directory removal and the hold outlasts the close window by it.
- **The reclaim hold's bound is three-way and the removal is outside it.** The cleanup's close is bounded by the graceful window the reclaiming request carries, else by that request's own deadline, else, for a hold taken outside any request, by the termination window of the pass that runs the cleanup; the directory removal runs after the close under no deadline.
- **"A hold taken outside any request" has exactly one referent, and it is unreachable today.** `onHoldTimeout` (holdstate.go:189-205,:227-262) deregisters every started entry in one critical section through `deregisterStartedSessions` (slotsession.go:375-395) and then terminates them serially under one shared 10s context. The §10.1.4 coordinator hold cannot arm in production, so the clause is forward generality rather than a live bound.
- **`ABORTED` occurs nowhere else in `spec/`, `docs/` or `schemas/`,** so §15.4's status-code choice mints it, and there is no gRPC-status classification table on the adapter surface for it to contradict. The client-facing tables at spec/15:1023-1137 are REST error codes on a different surface.
- **DECISION: the shared-entry residue is ACCEPTED and unstaged, and NO lettered or numbered decision is minted for it.** The two "open decision D1's Option B" citations were false (no D1 and no Option A/B exists anywhere) and are replaced by a descriptive statement of what closing would cost: a per-ATTEMPT discriminator carried on every request that can create or resolve a slot entry, which this proposal does not stage. CORRECTED TWICE: the section now carries entries 11, 15, 16, 17 and 18, each keeping its original number and none renumbered; entries 9, 12, 13 and 14 were deleted as resolved. Entry 17 is the re-put of entry 9 against the converged (worse) text and is the only entry carrying a recommendation. Any statement that the section carries "9 and 11", or "11 and 12", or that entry 11 is the sole open decision, is reading a superseded pass.
- **The hold is keyed on the slot identifier, which IS the session identifier,** so N concurrent cleanups on one pod refuse N distinct identifiers and never each other, no other session can collide with a held identifier, and no placement decision is affected. That identifier-scoping is what kills most availability findings against the hold. The proto states the identity too, in `ShutdownRequest`'s `reserved 4` comment (schemas/lenny-adapter.proto:1613-1619) — cite that rather than hunting spec prose.
- **spec/18 still needs no edit after SPEC-5.** Its adapter-conformance anchors are Phase 2/5 (`cmd/lenny-compliance`, the Basic battery over the RUNTIME binary, spec/18:121-122) and Phase 12c (:529-533, the cleanup/scrub split); neither enumerates an admission rule, an RPC field set or a §15.4 obligation, and CONF-1 lands in `tests/tier10_conformance`. Extends the pre-SPEC-5 Settled entry; three lenses derived it.
- **§28 carries no `Shutdown` row and no bind-sequence RPC row,** and §29.2/§29.4 label the bind and teardown RPCs "no register entry, the internal control API". So SCHEMA-1 widening eight messages adds no §28 row and the "deliberately untouched · §28's registers" bullet holds. §28.5.1's gateway-to-pod cards are exactly CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER and CH-PODHEALTH.
- **The adapter proto has no parallel client representation to keep in step.** No SDK, `docs/` or `spec/` file names `ShutdownRequest`, `AssignCredentialsResponse` or any bind-sequence response field, and there is no second copy of `lenny-adapter.proto` outside `schemas/` and `scripts/specshift/testdata/`. SCHEMA-1's nine fields and one enum mirror only `pkg/proto/adapter/v1`, which is generated. CORRECTED: this entry and the client-surface absence sweep beside it both wrote `pkg/gen/adapter/v1`, a directory that does not exist, so that arm of the sweep was vacuous; `schemas/buf.gen.yaml` pins `out: ../pkg/proto` and `go_package` is `.../pkg/proto/adapter/v1;adapterv1`.
- **`lenny_adapter_leaked_slots` IS named in `spec/` twice** — spec/05:545 and spec/06:160 — so SPEC-3's §5.2 mention mints no new metric surface. This corrects the natural misreading of the Settled entry that records the gauge as "absent from spec/16 and docs/reference/metrics.md", which is true of the inventories and is not a claim that `spec/` never names it.
- **The adapter registers no bind-refusal and no slot-admission metric.** `pkg/adapter/metrics.go:15-90` carries only the SO_PEERCRED pair, the LLM in-flight gauge, four credential-rotation series, the control-event counters and the tracing-frame drop counter, so §5.2's "no report and no counter names it" is true against the tree.
- **`examples/runtimes/echo/` DOES NOT EXIST in the tree.** spec/15:1466 calls it "A Go reference implementation of the adapter" while spec/15:1491 and :1812 describe it as a RUNTIME writing JSONL to stdout, and `examples/` is absent entirely. Pre-existing spec fiction; it cannot be an unstaged edit site, which moots the standing Open.
- **The gateway's `/finalize` block has a window in which §7.1's obligation cannot be discharged as written.** Its two "Gap 2" branches — the `finalizing → ready` store Update (sessionserver.go:3167-3181) and the upload-token `ConsumeDigest` (:3188-3197) — fail AFTER `Binder.Prepare` succeeded and CLOSED its connection, and both call `reclaimFinalizedPod` → `ReclaimClaimed`, which deletes the claim and revokes the lease and sends the adapter NO RPC. Staged §7.1 names the creation finalize block, mandates the reclaim "on the connection the failed stage still holds", and forbids re-dialling. FILED.
- **`isTransientPodClaimError` is a closed enumeration of sentinel types** (start.go:3648-3682) that a bare `codes.Aborted` matches on none of, so a §7.3 resume refused by the hold falls through to `s.failSession` and takes the row terminal.
- **The tier-11 §4.7 RPC-row gate harvests backticked first-column table rows.** `spec_47_rpc_row_naming_test.go:32-53` reads §4.7 with `specSection` and holds every `§4.7 table names <RPC>` Go comment to the row set it finds. SPEC-5 inserts prose rather than a table, so it is green; any future §4.7 or §4.7.1 edit that adds a markdown table with a backticked first column silently widens the accepted set.
- **DECISION: both exhaustiveness sentences carve `Shutdown` in explicitly and keep the narrowed universal.** §4.7.1 and §15.4 each close their bind-sequence list with "every other RPC on this contract neither carries nor reports a bind epoch", and each now names `Shutdown` as the exception, because the closed enumeration is what keeps `Attach`, `Interrupt`, `RotateCredentials` and the rest outside the fence. Widening the bind-sequence list to include `Shutdown` was rejected: the bind-sequence admission rules carry a mint-on-epochless arm `Shutdown` must never have.
- **In BOTH SPEC-5 blocks the admission-rules paragraph sits BELOW the RPC-set paragraph.** A cross-reference from the RPC-set paragraph reads "below", never "above". The §15.4 copy must NOT cross-reference "the caller rules below", because §15.4 carries no caller rules; do not re-synchronise the two sentences word for word.
- **DECISION: the `DemoteSDK` epoch drop is stated as part of what a caller HOLDS,** in the §4.7.1 caller-rules paragraph, and §15.4 is untouched, because §15.4 states adapter obligations only and the adapter's behaviour is unchanged (after a demotion it holds no entry, so the epochless-admission rule mints a fresh epoch). Relaxing adapter rule 2 to admit an epoch-bearing request when no entry is held was rejected: it lets a straggler from a dead attempt re-create an entry and a tree with nothing left to reclaim it, which is this proposal's own title.
- **DECISION: SPEC-4's §6.2 pre-`running` paragraph asserts no hold and no admission rule.** It points at §4.7.1 (which binds the adapter admits) and §5.2 (the hold, windowed from the deregistration), and says the `slot_cleanup` sub-state carries no admission rule of its own. Re-windowing §6.2's own sentence was rejected as a third normative copy; deleting it outright loses the protection that a reader of the fence alone does not read `slot_cleanup` as a label on a bindable slot.
- **`slot_cleanup` is entered at the gateway-observable failure, strictly earlier than the adapter's deregistration.** spec/06:154's trigger is "(session completes or fails)" and SPEC-4's new edge's is "the bind is abandoned or fails". The gap between that instant and the deregistration is exactly the interval in which §4.7.1 admission rule 1 REQUIRES a bind to be admitted, which is why any §6.2 sentence of the form "while a slot is in `slot_cleanup`, the adapter does X" claims X for an interval starting before any RPC reaches the adapter.
- **§6.2's per-slot sub-states are gateway-side.** spec/06:150 says "tracked per session, not as pod-level phase", so any staged sentence asking the ADAPTER to evaluate a predicate over them is unimplementable rather than merely mis-windowed. This is the structural reason the pointer form is right and a re-windowed form only accidentally right.
- **The reclaim hold is stated in exactly TWO places and the four sites agree word for word.** §5.2's `**Slot-identifier reclaim hold.**` and §15.4's companion block both window it from the deregistration of the registry entry to the cleanup finishing; §6.2 and §7.1 point at them. A later round that adds a third statement re-opens the two-start-points finding.
- **DECISION: "epochless" gets ONE wire definition per block, and §4.1 says nothing about the epoch.** Each SPEC-5 block's opening paragraph reads "The epoch is a positive integer, so zero is not an epoch: a request whose bind epoch is zero carries none, and a response reporting zero reports none", and §15.4 gained the matching non-conformance case for an adapter that compares a zero epoch or answers `superseded` for one. SPEC-1's §4.1 epoch sentence was DELETED rather than reworded, because `expected_bind_epoch` is a bare proto3 `int64` with no wire presence, exactly as `ShutdownRequest.coordination_generation` (field 6, schemas/lenny-adapter.proto:1630-1635) is, and §4.1's rule is about a field's presence standing in for a SCOPE. Making the field `optional` would collapse the whole argument; the file declares `optional` on no field.
- **Deleting the §4.1 epoch sentence cascaded to three prose sites,** all of which moved in the same edit: the Design narrative repeating the §4.1 justification, summary.md's 0075 impacts row, and summary.md's SPEC-1 one-liner. It also falsified the SPEC-5 §4.7.1 preamble's "the §4.1 paragraph, the §4.7 `Shutdown` row, and §7.1 each point at one statement", which now names the row and §7.1 only and states why §4.1 names nothing.
- **DECISION (`f1.open-decisions.13`): open decision 13 is resolved as staged and deleted.** The applied spec already answers it: §5.2's `**Fresh workspace guarantee**` stands as written and the retry policy is scoped to "the retries this policy places and no others". The question's `superseded` premise is false, because a release runs the per-slot cleanup that removes the tree, so the successor's workspace is materialized fresh. The genuinely-inherited case is adoption of a SURVIVING entry on the create-time-reserved path, which the policy does not place, and it is recorded as a shipped-defect row.
- **DECISION (`f1.open-decisions.14`): the compensating `Shutdown` is NOT exempt from §10.1's generation fence, and entry 14 is deleted.** §4.7.1 already states "Where both appear on one message, as on `Shutdown`, each is checked on its own terms", and a generation-stale rejection is simply a reclaim the adapter did not answer, which §7.1 already dispositions. §10.1's stale-replica rule tells such a replica to cancel its in-flight RPCs and not retry (spec/10:66-68), so an exemption would let a stale replica keep driving the pod. The unenforced per-RPC fence is recorded as a shipped-defect row naming spec/10:30 against pkg/adapter/coordination.go:120,:262.
- **The summary's structure was re-verified by the `f1.cleanup` firing and nothing was relocated.** Required sections in the required order, `## Deliverable index` last with fourteen deliverable lines, no `### Retired` block. The open-decisions preamble was corrected twice by that firing: "the numbering below does not start at 1", and it now records that entries 13 and 14 left with their answers staged and their residues recorded.
- **`## Decisions` names 0076 and 0080 deliberately and neither belongs in `## Impacts on other proposals`.** The 0076 mention is the ground of SCHEMA-1's second proto window and is word-for-word the R1b impacts row's claim; the 0080 mention is the ground of the file-collision discipline. Neither asserts anything about the other proposal's continued validity. A later pass that disagrees should merge into the existing row rather than add one.
- **The `## Spec files touched` list is complete as a set: six files, thirteen edit sites, one entry per staged block.** It is the only place the edit inventory is stated as a set, so a fixer that adds or drops a staged block must sweep it.
- **The §5.2 append and the §15.4 insert each stage a fenced block containing a BLANK LINE,** so each lands as two markdown paragraphs even though the instruction says "Append to that paragraph". That is intended (the second paragraph is `**Slot-identifier reclaim hold.**`) and `## Spec files touched` names it. Do not "fix" the instruction by removing the blank line.
- **The `PrepareWorkspace` admission rule is per RPC CALL, not per frame.** Both blocks state that admission is evaluated once, at the frame from which the adapter first resolves the slot identifier, later frames are neither admitted nor refused on their own, and the single response reports the epoch admission fixed; §15.4 carries the matching non-conformance case for an adapter that resolves the identifier more than once within one call. The shipped adapter already does this, under `resolvePrepareStagingDir`'s `if stagingDir == ""` guard (staging.go:44-47,:66-83).
- **`coordination_generation` is declared on exactly fourteen REQUEST messages and on no response.** Derive it with `awk '/^message /{m=$2} /coordination_generation = /{print NR": "m}' schemas/lenny-adapter.proto`. It rides the forward RPCs the epoch deliberately does not fence, so the two field sets are nearly disjoint, and §4.7.1's both-fields sentence names `Shutdown` alone under the reverted per-entry design (see the correction on the `ResumeRequest`/`ShutdownRequest` entry above) rather than being illustrative.
- **`RotateCredentials`, `RevokeCredentials` and `ExtendCredentialLease` resolve through `slotStateLocked`, not `ensureSlotStateLocked`** (slotcreds.go:70-72,:103-107,:122-127), so they are outside the seven bind-sequence RPCs by construction and a revoke can never be fenced off by an epoch mismatch. Only `assignCredentialsSlot` among the credential RPCs takes the entry-creating path.
- **The escape hatch is stated in `spec/` at three sites, not only in code.** The epochless `Shutdown` is the unconditional teardown at the §4.7 row, §4.7.1's caller rules and §15.4's conformance clause, so every non-bind caller (the §11.4 revoke fan-out, the occupancy-zero recycle edge, `Binder.ReleaseSlot`) stays defined and no mandatory teardown is made conditional on an in-pod value.
- **The §11.4 fan-out is safe under the epoch because peer replicas hold none.** A republished `Shutdown` off Redis pub/sub reaches a replica that received no bind-sequence response, and the handling replica dials fresh, so both send the unconditional form. Anyone proposing to make the epoch mandatory on `Shutdown` breaks §11.4.
- **The whole-pod scrub survives every new fence.** Clause three of the shipped handler runs `startPodScrub` outside the `if bound` block (session.go:288-291), and the §4.7 row's unedited tail states the recycle disposition's scrub unconditionally, so neither an epoch mismatch, nor an `absent` answer, nor the reclaim hold can suppress the tenant-isolation scrub that precedes pod reuse.
- **There is a FOURTH `removeSlotTree` caller nobody's shard names:** the §10.1.4 coordinator-loss hold's self-termination pass (holdstate.go:229-254), which deregisters and cleans up outside any request. The staged §5.2 hold text already covers it, but a reviewer enumerating cleanup paths from `Shutdown` and `releaseSessionSlot` alone will miss it.
- **`isTransientPodClaimError` matches no bare gRPC status and `adapterclient` sets `CoordinationGeneration` only on `CheckpointBarrier`** (client.go:470), so `ShutdownRequest` and `ResumeRequest` always carry zero on the wire today. Both facts together are why the generation fence cannot refuse a compensation in the shipped tree.
- **The shipped adapter derives the scrub outcome from the runtime close alone,** `sessionScrubOutcome(closeErr)` mapping `closeErr == nil` to `RELEASED` with the tree-removal error discarded (sessionscrubreporter.go:38-43,:61-84; session.go:271,:279). So a pre-`running` cleanup would have reported `RELEASED` regardless, and withholding the report withholds only `IncrementSessionsServed`.
- **`grpc.NewClient`'s `ClientConn` reconnects transparently and `Alive()` reads exactly that** (adapterclient/client.go:48-50,:72-79), so "the connection the attempt already holds" survives a server restart. That is why the no-re-dial rule cannot carry the epoch-ABA case and `RestartPolicy: Never` must.
- **DECISION: the no-re-dial rule's stated REASON was corrected at two sites and its force left unchanged.** §7.1 and §4.7.1 both said the epoch "is meaningless without" the connection, which contradicts §4.7.1's own definition of the epoch as process-scoped and invites a per-connection counter, the one implementation the fence cannot survive. Both now use the latch wording three already-correct sites carry (`compensateFailedSlotBind`'s doc comment, summary.md's watch-out bullet, the non-spec deviations bullet): a caller that re-dialled would hold NO epoch and would send the unconditional teardown at a pod that may already hold a successor's slot. The surviving phrase "carrying the bind epoch the attempt observed on that connection" is TRUE and was deliberately left; it says where the caller got the value, not where the value has meaning.
- **The two surviving "create or resolve a registry entry" phrases are the RECLAIM HOLD's admission predicate,** not the epoch-reporting predicate, and they are deliberately broader than the seven bind-sequence RPCs. Narrowing them to the bind sequence would admit `Attach`, `Interrupt`, `ReportUsage` and the rest mid-cleanup and open the hold. Do not "unify" the two predicates.
- **The proposal's line numbers have drifted roughly EIGHTY lines since the hand amendment (1344047d4 → 2aca9851b).** Any finding quoting a line number in the 500s for §5.2 or §6.2 prose is reading that snapshot. The current anchors: SPEC-1's §29.4 block at :334-360, the untouched list at :724-739, the "Spec files touched" §29.4 row at :760, the SPEC-5 blocks at :672-690 (§4.7.1) and :694-722 (§15.4), the §5.2 append at :600 and the hold paragraph at :602. Anchor on quoted strings, never on line numbers.
- **`tests/claim-map.json` is GENERATOR OUTPUT and a tier-0 gate holds it byte-identical.** `TestClaimRegisterIsReproducibleFromItsGenerator` re-runs `scripts/seed-claim-register.py --out` and diffs bytes, reporting per row that a committed row the generator does not emit "has no row source behind it". Rows are emitted `sorted(claims, key=lambda c: c["claim"])`, so no insertion position is authored; rows no status table carries go in the script's `EXPLICIT` list (:169, appended at :378); `SURFACE_OVERRIDES` cannot mint a row (`SystemExit` when it matches none). The generator reproduces the committed 76-row register exactly at HEAD. SCHEMA-1 stages two rows into the JSON and names the generator nowhere, so S8 turns tier 0 red. FILED. EVIDENCE: tests/tier0_static/claim_register_generator_test.go:19-31,:44,:74-122; scripts/seed-claim-register.py:169,:364-368,:378,:398.
- **A second tier-0 gate binds a claim row to the proto.** `claim_register_proto_agreement_test.go:44-50` parses `Message.field` out of a row's `claim` text and requires the proto to declare it, so SCHEMA-1's `ShutdownRequest.expected_bind_epoch` row and the proto edit must land in ONE commit. S8 already lands both. `claim_register_test.go:55-58` resolves `spec_anchor` against §28 headings and requires a WIRED row's surface to carry a path or symbol; `#2851-gateway-to-pod` resolves (spec/28:205) and both staged rows comply.
- **The generated proto package is `pkg/proto/adapter/v1`.** `pkg/gen` does not exist; `schemas/buf.gen.yaml` pins `out: ../pkg/proto` and `go_package` is `github.com/lennylabs/lenny/pkg/proto/adapter/v1;adapterv1`. The three proposal sites that said `pkg/gen/adapter/v1` were corrected by the third reconciliation pass.
- **The shipped tier-3 CLOSED field-set gate is `TestShutdownMessagePostRemovalDescriptor_spec_4_1`.** `assertFieldSet` errors on any declared field number missing from `want`, and the test pins `ShutdownRequest` to {1,2,3,5,6} and `ShutdownResponse` to {1,2}, so SCHEMA-1's field 7 and field 3 turn it red the moment the regenerated stubs land. It is the ONLY closed field-set gate over the nine messages SCHEMA-1 opens; `buf breaking` is silent on an additive edit and is the wrong gate to reason from. The fix belongs in the shipped file, in SCHEMA-1's own step, and its `// diagnosis:` needs the epoch fence and the reclaim outcome named. EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:202-238,:257-278.
- **`checkpoint_stream_wire_test.go` IS a closed pin too, and it does not matter.** `assertFields` (:92) iterates only its `want` slice, but twelve lines below, :151 pins `CheckpointStart` to exactly six fields and `assertOneof` (:77) pins a oneof's arm count. `grep -rn "Fields().Len()" tests/ pkg/` returns only those two sites plus `shutdown_recycle_wire_test.go:226`. Both `checkpoint_stream` pins are over messages SCHEMA-1 does not open. CORRECTS the r1 fix pass, which told an implementor that file can absorb an added field.
- **All nine SCHEMA-1 field numbers are free, verified by at least eight independent lenses.** `ShutdownRequest` 7 (1,2,3,reserved 4,5,6 at :1609-1636); `ShutdownResponse` 3 (:1665); `PrepareWorkspaceResponse` 3 (:699); `FinalizeWorkspaceResponse` 2 (:761); `RunSetupResponse` 2 (:869); `StartSessionResponse` 2 (:958); `ResumeResponse` 4 (:1433); `ConfigureWorkspaceResponse` 2 (:1690); `AssignCredentialsResponse` 1 (the message is genuinely `{}` at :1033). Enum spelling matches the `SessionScrubOutcome` sibling (:438-448). Do not re-derive this.
- **`validate-maps` maps four of this proposal's five test tiers FILE BY FILE.** Only `tests/tier4_integration` has a bare-directory entry (spec-map.json:3523), so extending a tier-4 file is free; `tests/tier3_contract`, `tests/tier7a_load_local`, `tests/tier9_security` and `tests/tier10_conformance` carry only per-file entries plus deeper subdirectory globs (:1997, :2944, :4360). The four NEW test files 0081 stages therefore orphan and turn tier 0 red, and `tests/spec-map.json` is in no edit list. FILED; this closes and widens the two standing Opens that named only the tier-7a file. CORRECTS a note carried from proposal 0076 saying tier7a is outside the walk: it is inside `componentAndAboveTierDirs()`. EVIDENCE: cmd/lenny-test/cmd_validate.go:125-139,:716-793.
- **DECISION: each new spec-map entry lands in the checklist step that CREATES the file it maps** — S9 the tier-3 DIRECTORY entry `tests/tier3_contract/adapter_bind_epoch/...`, S10 the tier-7a and tier-9 file entries, S14 the tier-10 file entry. A trailing registration step (proposal 0073's S22 precedent) leaves tier 0 red on every intermediate commit from S9 to S13, which the checklist's own per-step tier rule forbids; the directory form keeps S10's added tier-3 file covered without a second entry. `tests/tier11_docs` is outside the walk, so the DOCS gates owe no entry.
- **DECISION: DOCS-2 exists, against `docs/reference/adapter-contract.md`, and folds into the EXISTING S6 docs step** with `Depends on:` widened from `S5` to `S2, S5`. A step of its own would renumber S7 through S14 and every `Depends on:` naming them for no separation the docs lane needs. This discharges the standing eleven-lens Deferred.
- **DECISION: DOCS-2 splits into a rewritten one-line `Shutdown` table row plus ONE prose block after `**Scrub responsibilities.**`.** The tier-11 gate reads the row with `lineContaining(page, "| `Shutdown` |")`, so the row must stay one physical line; the epoch contract and the reclaim hold are what §15.4 publishes to third-party authors and belong in prose. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311.
- **The `Shutdown` row at `adapter-contract.md:75` is now falsified in FOUR ways, three of them new since the pre-amendment loops read it.** Pre-amendment: the usage flush and runtime close move under `started`, and the `ReportSessionScrub` moves under `live`. New: CODE-1 moves `drainViaLifecycle` INSIDE the `if started {` block, so the drain signal needs `started && !boundRemains` where the row states only "leaves the pod holding no other bound session"; and a mismatched bind epoch makes the adapter perform NEITHER teardown. A DOCS-2 that covers only the two older halves is incomplete.
- **DECISION: `compensateFailedSlotBind` narrows to scalars,** `(ctx, cl, sessionID string, cleanupTimeoutSeconds int, maxConcurrentSessions int32, sandboxName, slotID string) bool`, because `Binder.Resume` holds a `podsession.ResumeRequest` and no `SlotBindRequest` is in scope there; `ResumeRequest` carries all three fields under the same names (binder.go:605,:645,:668). ALTERNATIVES rejected: a synthetic `SlotBindRequest` on the resume path (invents a conversion the proposal never sanctions), a shared `compensationParams` struct (a third parameter-carrying type for three scalars), and dropping `slotID` (the resume branch names a separately reserved slot id). The precedents are CODE-5's `accountSlotFailure` narrowing and CODE-4's own `slotCleanupBudget`. This closes the five-lens standing Open. The narrowed list is stated in exactly TWO prose sites and S12 keeps no signature at all: two prose copies of one signature is the drift that produced the finding.
- **DECISION: the §7.3 resume classification gap is closed by ONE arm, `case status.Code(err) == codes.Aborted: return true` in `isTransientPodClaimError`, owned by CODE-5.** The refusal crosses a process boundary, so only the status code survives: `errSlotReclaimInProgress` and `isSlotReclaimInProgress` are unexported and adapter-local, and no production file under `pkg/gateway` imports `pkg/adapter`. `status.Code` walks the wrap chain (grpc v1.80.0 `status.FromError` falls back to `errors.As` for `GRPCStatus()`), so the arm fires through the `*SlotBindError` CODE-4 makes `Binder.Resume` return. The function already carries one code-reading arm (`SetupCommandFailure`). Test home is `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go` (`package sessionserver`, `seedResumingRow` fixture at :23, a `holdOrFailOnResumeError` table at :48-110), never `start_test.go`, which is the external package. Only `pkg/adapter/checkpoint.go:115` answers `Aborted` today, so the arm widens nothing unintended.
- **`holdOrFailOnResumeError` and `writePodClaimError` are two different classifiers and only one was broken.** The wire envelope was already right: `writePodClaimError`'s default arm answers a retryable 503 `RESUME_FAILED` for any unrecognised cause (start.go:87,:208-219,:3517). Only the ROW-STATE classifier demoted the row terminal. `SlotBindError.Reason()` is a third classifier on a third path; a fix that touches one must say which.
- **`codes.Aborted` takes `Reason()`'s transient default at EVERY stage.** `Reason()` switches on the code first and only `FailedPrecondition`/`PermissionDenied` refine on `e.Stage`, so the staged start-stage and workspace-stage `Aborted` test rows cover the credential and setup stages too. EVIDENCE: podsession/slotfailure.go:84-101.
- **The credential stage's adapter error is NOT wrapped in `*CredentialAssignmentError`.** `assignSlotCredentials` returns that typed error only for a gateway-side lease-minting failure (`AssignProto`/`MintProto`); the `cl.AssignCredentials` RPC error returns bare and `materializeSlot` wraps it as a plain `*SlotBindError` at the `credential_assignment` stage. So a hold refusal on the credential path does not detour through the typed handling at start.go:89/:3653. EVIDENCE: podsession/slotbinder.go:369-403,:308-313.
- **Go declares a struct's fields in the file declaring the struct,** so `Server.bindEpoch`/`Server.reclaiming` land in `pkg/adapter/server.go` (`type Server struct` at :59) and `slotState.epoch` in `pkg/adapter/slot.go` (`type slotState struct` at :21), whatever file holds their logic. CODE-6's "new file `bindepoch.go`" bullet read as if the fields lived there, and `pkg/adapter/server.go` was in no edit list. CODE-6's file set is enumerated in THREE places that drift independently — the `### CODE-6 ·` heading (deliberately partial), `## Files touched on application (non-spec)`, and summary.md's index line — and only the latter two are maintained as exhaustive.
- **The checklist was RESEQUENCED and RENUMBERED, and the order is load-bearing.** S1=SPEC-5, S2=SPEC-1, S3=SPEC-2, S4=SPEC-3, S5=SPEC-4, S6=DOCS-1 (+DOCS-2), S7=CODE-3, S8=SCHEMA-1, S9=CODE-6, S10=CODE-1, S11=CODE-2, S12=CODE-4, S13=CODE-5, S14=CONF-1. SPEC-5 leads because the §4.7 row, §5.2's reclaim hold and §7.1's obligation each point at the §4.7.1 bind-epoch block. Every `Depends on:` was rewritten onto the new ids (S8→S1,S2; S9→S1,S8; S10→S2,S4,S9; S11→S3,S10; S12→S3,S4,S10,S11; S14→S1,S10). S8's rule S-2 precondition holds because S9-S13 are the only handler steps. This closes the standing Open on S1 citing a §4.7.1 statement landed later; the one citation the order cannot put first is SPEC-5's own pointer at §5.2's hold, which S1's line states.
- **`ensureSlotStateLocked` has exactly FIVE production resolve sites and the hold refusal reaches exactly the seven bind-sequence RPCs.** `ensureSlotPaths` (staging.go:134,:181,:337), `assignCredentialsSlot` (slotcreds.go:26) and `claimSessionSlotUnderLock` (slotsession.go:75). Every other entry-resolving handler goes through `boundSlotState`/`checkSessionBound`, and `RotateCredentials`/`RevokeCredentials`/`ExtendCredentialLease` through `slotStateLocked`. No sixth site. Re-derived by five lenses.
- **The SDK-warm one-session-only refusal runs BEFORE `ensureSlotStateLocked`** (slotsession.go:65-73), so putting the hold refusal at the top of `ensureSlotStateLocked` cannot reorder that control.
- **`deregisterSlot` (the `s.mu`-taking wrapper CODE-6 retires) has exactly two callers left and both are tests,** at `pkg/adapter/podmcp_arming_internal_test.go:88,:245`, a file already in the proposal's test list. Retiring it costs nothing else.
- **The `f1` firing resolved open decision 12 and deleted it: the reclaim hold ends when the cleanup pass that took it RETURNS, whether that cleanup succeeded or failed.** No staged text was edited, because the staging already states it twice in §5.2 and mirrors it in §15.4, and CODE-6 stages an unconditional `defer release()`. The two terminals that looked like they collided are different objects: `leaked` is the pod's Redis slot-counter occupancy held until pod termination, while the hold is adapter-local over the identifier, so a `leaked` slot keeps its occupancy without keeping its identifier held. Shipped behaviour already discards the tree-removal error outright (`_ = removeSlotTree(st)`).
- **The `f1` firing kept open decision 17 (the create-time-reserved retry's residue) with the human and rewrote it in place** so it is answerable in one sitting: harm first, then a low-confidence recommendation to accept as recorded, the ground, the three closures and why each lost. It is the only entry carrying a recommendation, and the section preamble was corrected to say so. Its "recorded under `## Decisions`" citation was FALSE — no such section exists in any file of this proposal; the record is `## Edge cases and accepted failure modes`.
- **The `f1` firing rewrote the 0080 §1.19 impacts row and the sentence it retired was wrong on BOTH halves.** "No bind-sequence RPC gains a refusal, so the class inventory grows by the reclaim hold's `ABORTED` refusal alone" is false: `errSlotReclaimInProgress` is raised at the top of `ensureSlotStateLocked`, which the bind-sequence and workspace RPCs are exactly what reach. The row now states that the hold adds one more producer of an arm §1.19 already enumerates (`boundSlotState` answers absent and unbound alike with one `codes.FailedPrecondition`, slotsession.go:274-283), that the hold's own refusal sits outside §1.19's inventory, and that the class SET is unchanged while membership moves.
- **Retuning `ceil(maxConcurrentSessions/2)` stays out of scope with NO row in the not-staged defects section,** because the proposal is silent on whether the value is right and a tuning question is not a defect it knowingly leaves. The Non-goals bullet is the record. Do not read the call as an assertion that the threshold is correct.
- **`resolvePrepareStagingDir` returns `(string, error)` and drops everything `ensureSlotPaths` gives it except `paths.Staging`** (staging.go:31,:116,:133-138), so any "epoch on the seven responses" work has to widen that helper too, not only `ensureSlotPaths`. FILED.
- **`claimSessionSlot` returns three values today and CODE-6 makes it report the epoch,** so all seven internal-test call sites in `podmcp_arming_internal_test.go` (:74,:93,:138,:157,:179,:187,:224) change arity. Mechanical, but the proposal covers it only obliquely.
- **The compensation is synchronous inside `materializeSlot`,** so the gateway's own §5.2 retry can never race its own attempt's reclaim: `applySlotRetryPolicy` re-enters `BindSlot` only after `materializeSlot` returned. Every racing "retry" in the design and in the tier-4 late-reclaim arm is necessarily a fresh client request or a second replica.
- **`reclaimSlotLocked` inserts a hold only when it removed an entry,** and its `release` is a non-nil idempotent no-op otherwise, which is what stops a concurrent unfenced `Shutdown` (removed=false) from clearing another caller's hold on its deferred release. Subtle and correct as staged; do not "simplify" it.
- **The `budget/2` split's whole chain was verified end to end against the shipped adapter.** `contextWithGraceDeadline(parent, grace)` returns `WithTimeout(parent, grace)` for a positive grace and the parent unchanged for a non-positive one, and `resolveShutdownGrace` prefers that ctx's remaining time over the configured grace and the package default, so `ShutdownReclaim(rctx=budget, deadline=budget/2)` really does give `Runtime.Close` budget/2 and leave budget/2 of RPC deadline for the uninterruptible `removeSlotTree`. EVIDENCE: session.go:327-332; mcpruntime.go:308-323; socketruntime.go:457.
- **The §10.1.4 hold-termination pass cannot leak a reclaim hold, and it can hold several identifiers at once.** `deregisterStartedSessions` (slotsession.go:375-396) builds `members` only from entries it removed, under ONE `s.mu`, so CODE-6's `release func()` must be captured per member inside that loop; `onHoldTimeout` then runs `terminateHeldSession` for every member in a plain loop with no early break under a shared 10s context, and that function holds no `s.mu`, so a `defer release()` there cannot deadlock. The whole set is held from pass 1 until pass 2's last member returns. Nothing in the proposal says the hold set can carry several identifiers; it is correct, but a reader of CODE-6 alone will not expect it.
- **`heldSession` and `deregisterStartedSessions` live in `pkg/adapter/slotsession.go` (:306-313, :375), not `holdstate.go`,** even though CODE-6's prose attributes the latter to holdstate.go. The Files-touched list gets it right; do not spend a verifier pair on the prose.
- **CONF-1 says "Four properties, one per part of the §15.4 contract" while SPEC-5 lands TWO §15.4 blocks,** and all four properties are bind-epoch. The reclaim hold has no CONF-1 property, and neither does §15.4's `PrepareWorkspace` non-conformance clause.
- **`docs/reference/adapter-contract.md` is written for RUNTIME authors, not third-party ADAPTER authors** ("the protocol between the Lenny adapter sidecar and your runtime binary", :10; "Your runtime binary never sees them directly", :53). A finding demanding the full §15.4 bind-epoch and reclaim-hold contract be republished there is over-reach; the defensible scope is the `Shutdown` row's now-false statements.
- **The docs surface this proposal can falsify is small and now fully enumerated.** `grep -rln "Shutdown" docs/` returns three files and only `adapter-contract.md` describes the gateway→adapter RPC; the other two are the runtime-facing JSONL `shutdown` frame. The operator-narrative half is inert: no operator page or runbook enumerates leaked-slot or pod-replacement causes. SPEC-3's two added cleanup actions have no docs mirror. The new §6.2 edge reaches no SVG (`pod-warm-path.svg` and `sdk-warm-path.svg` carry `receiving_uploads` as a POD-level phase and no `slot_cleanup` node at all), so DOCS-1's table row is the whole docs surface for SPEC-4.
- **`spec/06:283`'s `**State storage:**` paragraph says the fine session-lifecycle states are NOT projected onto the CRD,** which is the structural reason no Kubernetes-idiom lens has purchase on the §6.2 fence edit. The Kubernetes lens has now returned zero findings on this proposal in eight consecutive runs.
- **`ClaimSlot` pass 1 filters on the per-pod claim, `claimstate.IsTerminal`, the tenant pin and `expiredByUptime`, with NO `sb.Status.Phase` read; only pass 2 reads the phase.** That is the idiomatically correct answer (do not block a synchronous bind on a level-triggered controller), so `ExcludePods` is right for the right reason. EVIDENCE: podclaim/slotclaimer.go:411-478.
- **`s.podBinder` already satisfies the unexported `slotBinder` interface,** so CODE-5's new `resumeOnPod` caller of `accountSlotFailure(ctx, s.podBinder, …)` compiles without widening it. EVIDENCE: start.go:2725-2737.
- **`RecordFailure` has exactly ONE production call site in the shipped tree, `applySlotRetryPolicy` (start.go:2854),** and CODE-5 adds two more. Any proposal sentence saying "whose only production call site is `applySlotRetryPolicy`" is a pre-amendment fact the amendment falsifies.
- **The §5.2 exclusion ("did not complete") and §7.1's "an attempt that meets the hold is one the retry policy did not place") are bridged by the handler blocking on `removeSlotTree`:** a COMPLETED reclaim's answer arrives only after the cleanup finished, so the hold is released before a retry can be placed. The one interleaving that breaks the bridge needs TWO `Shutdown`s for one session, which the gateway does not send. Under CODE-6 there is a second, narrower producer: an `ABSENT` answer can return while the adapter's OWN pre-`Runtime.Start` rollback still holds the identifier, reachable only when the gateway's context expired mid-handler. Below the bar; the outcome is the accepted transient refusal.

### Traps

- **Do not re-gate the cleanup-outcome report on `st.started`.** "Simplifying" the two predicates back into one reintroduces the double report through CODE-2's rollback and advances `sessions_served` for a session that never ran. The tier-1 "Claimed but not yet recorded" case exists to fail when that happens.
- **Do not gate the runtime teardown on `runtimeLive` either.** The tempting local fix for the mismatched sentence is to move pkg/adapter/session.go to `runtimeLive` so the code matches the spec text. It inverts the fail-closed direction: CODE-2's rollback fires only when `Runtime.Start` returns, so a start that hangs would leave the runtime serving the abandoned session forever. EVIDENCE: pkg/adapter/session.go:155-163.
- **Do not widen §6.2's fence to move `slot_cleanup ──→ leaked` into the either-concurrency block.** It is the obvious way to make a prose terminal claim true, and it changes shipped state-machine semantics, cascades into docs/reference/state-machines.md:251, and breaks the tier-11 matched-pair test. G1 removed the terminal claim from SPEC-4's prose instead, which is the smaller fix.
- **Do not edit spec/06:152 or docs/reference/state-machines.md:235.** The advice holds and the reason pass 1 recorded for it does not. The annotation reads "workspace ready, session dispatched to runtime with its session identifier", which is the runtime-has-been-given boundary (`runtimeLive`) rather than the admitted-start boundary, and SPEC-4's prose anchors to it rather than replacing it, which is what keeps DOCS-1 to the single added row it stages. An agent trusting the old gloss concludes SPEC-4's prose mis-cites the annotation when it does not. The residual "dispatched versus acknowledged" question is the one substantive open boundary item; see the trap on SPEC-4's prose below.
- **Do not move the SPEC-2 block below §7.1's fence.** §7.1's atomicity paragraph physically sits inside the fenced flow listing, between the step-8 line and the line continuing it, so any paragraph inserted after it also lands inside the fence. The staged instruction says so explicitly. Below the fence it would sit among the `uploadToken` prose. EVIDENCE: spec/07:5,23,24,54.
- **Do not put a per-slot scrub rule in §5.2's `**Slot cleanup:**` bullet.** The bullet is under a `maxConcurrentSessions > 1` heading, so on a single-session pod §4.7's unqualified `ReportSessionScrub` row would govern instead. The concurrency-independent home is the `**Scrub model.**` paragraph at spec/05:453.
- **TRIED AND WITHDRAWN: the no-retry rule.** SPEC-2 originally said the failed attempt is not retried. Priced honestly that is roughly eleven mirrored exception clauses across §5.2, §6.2, §15.1, §29 and three error-catalog rows, plus a new `SlotReason` value (§5.2's "Client error on exhaustion" sets `error.category` from the failure reason, and an unacknowledged reclaim is a disposition rather than a reason), and it withdraws availability during exactly the partial pod outage that made the reclaim fail. The placement rule replaced it. Do not re-derive the retryability framing.
- **TRIED AND WITHDRAWN, and do not go the other way either: deleting the no-retry clause outright.** With the retry unconstrained and a lagging `Shutdown` still executing under the same identifier there are two unfenced outcomes. (1) The reclaim lands between the retry's `PrepareWorkspace` and its `StartSession`: `claimSessionSlot` re-creates an empty entry and tree and starts the session on it silently, and CODE-2's survived-the-start confirm does not catch it, because the entry it checks is the one its own claim just recreated. (2) The reclaim lands after the retry is running and tears down a healthy session. EVIDENCE: pkg/adapter/slotsession.go:74-90.
- **TRIED AND WITHDRAWN: fencing the lagging `Shutdown` by generation.** It looks like a free answer because `ShutdownRequest` already carries `coordination_generation`. The generation is per-session-coordinator and does not advance between two attempts of the same session on the same replica, and adding a per-attempt field is blocked by programme rule S-2, which reserves the single `schemas/lenny-adapter.proto` edit to step R1b.
- **MISTAKE: the proposal had already found and mispriced the placement exclusion.** non-spec-changes.md:574-579 rejected it as "the larger alternative ... not staged here", pricing only its code cost and never pricing the spec-contradiction surface the no-retry rule creates. Priced honestly it is four small Go edits and two spec clauses against roughly eleven spec and doc sites plus a new reason value. Do not re-derive that bullet's conclusion; the mispricing is the finding.
- **MISTAKE: the report rule was stated twice with opposite content.** SPEC-2 had the racing `StartSession` report the reclaim outcome while SPEC-3 forbade any report for the same reclaim. It cost a full round, and the cost was structural rather than verbal: each block stated its own version of one rule instead of citing a single statement of it. This is why the rule now lives in one paragraph that the others cite.
- **MISTAKE: a `leaked` `ReportSessionScrub` was used as a "this pod is bricked, retire it" side-channel** (non-spec-changes.md:189-195,:584-588). That is an accounting RPC repurposed as a health signal, and it cost the proposal a self-contradiction with SPEC-3 plus a double `sessions_served` advance. It was reached for because the ledger is not concurrency-gated (see the next trap), so a single `leaked` report really does retire the pod on the spot.
- **The leak ledger is not concurrency-gated.** `UnhealthyThreshold(1) == 1`, so one `leaked` report on a `maxConcurrentSessions: 1` pod stamps `lenny.dev/drain-request` immediately. EVIDENCE: pkg/gateway/session/recycle/scrubreporter_seams.go:184-197; slothealth.go:121-140.
- **The "bricked pod" premise is real but not distinctive to the racing rollback.** `SocketRuntimeProcess.Close` closes the listener whenever the released session was the last active one, and the listener is bound exactly once at adapter start. An ordinary session end reaches the same state through the same call, so retiring only on the racing path fixes no class. This is why deleting the report is safe.
- **`Binder.DrainSandbox` removes nothing from placement synchronously.** It only stamps the `lenny.dev/drain-request` annotation for the WarmPoolController, and `ClaimSlot`'s pass-1 scan of claimed pods reads the per-pod claim and never the Sandbox phase. Any claim that "at `maxConcurrentSessions` 2 the drained pod is no longer a candidate" is wrong. EVIDENCE: podsession/slotbinder.go:601; podclaim/slotclaimer.go:95-107,:417-469.
- **MISTAKE (nearly filed): the staged "not retried" clause does not ban every post-first-RPC retry.** It is grammatically governed by "A reclaim the adapter does not acknowledge", so it bans the retry only for the unacknowledged-reclaim class. Read the sentence's subject before filing on it. The parenthetical this entry used to carry, that `applySlotRetryPolicy` skips the retry when `sbe.Leaked`, is stale under the placement design: CODE-5 and its tier-1 case assert the retry STILL runs on a leaked disposition and only carries `ExcludePod`. Read the current CODE-5 text rather than any older gloss of the retry rule.
- **Dead end: "the adapter cannot see this leak".** `lenny_adapter_leaked_slots` is emitted by the gateway (gatewaymetrics_credential.go:219-223) despite the name and despite §6.2 calling it adapter health metadata, and `applySlotRetryPolicy` already marks a slot leaked without the adapter knowing. Withholding the adapter's report does not blind the gauge; CODE-5's `accountSlotFailure` feeds it from the gateway side. Two lenses chased this and both refuted themselves.
- **Dead end: "the withheld report contradicts §4.7 or §12".** §4.7's `ReportSessionScrub` row, spec/05:453 and spec/12:481 all scope their reporting rule to "a session release", and the proposal deliberately frames the pre-start reclaim as not one, so the whole family of findings gets refuted. What survives is the `leaked` terminal, which spec/06:160 requires to be counted with no scoping to session releases.
- **Dead end: "SPEC-3's insert does not reach an exclusive pool because §5.2's bullet is concurrency-scoped".** spec/05:395 and :453 already make slots and the per-slot cleanup uniform across concurrencies, and the shipped §4.7 `ReportSessionScrub` row generalises the per-slot cleanup to every session release with no concurrency qualifier (spec/04:692). The scoping ambiguity is pre-existing. Do not file it.
- **Dead end: "`releases the slot reservation afterwards` contradicts the leaked hold".** `ReleaseSlot(leaked=true)` skips the decrement and the occupancy-zero disposition, so the two are compatible. Three lenses checked this and dropped the finding.
- **Dead end: "the §7.1 rollback omits credential-lease release".** §7.1 step 23 is the existing spec basis and `failPhase` already cites it for the pre-attached failure path, so the code staging's `releaseCredentials` is anchored. EVIDENCE: podsession/binder.go:1065-1078,:1259-1268.
- **Do not read the reclaim as scoped away from pods that retire.** "The pre-attached disposition governs the pod; this reclaim governs the slot state on a pod that is released or reused rather than terminated" cannot mean that: on the concurrent path a failed bind whose slot was the pod's last occupant also retires the pod (`ReleaseSlot` deletes the claim at `remaining==0`), and the proposal compensates there. EVIDENCE: podclaim/slotclaimer.go:880-885.
- **`§15.4.2` is the repo's established citation for the CH-RUNTIMEOPS `terminate` signal,** even though the frame's own card is §28.5.3 and §15.4.2 only names the DRAINING state. Do not file it as a false citation; the tree already cites it that way. EVIDENCE: pkg/adapter/drain_test.go:12,:53.
- **The `slot_cleanup ──→ leaked` scoping asymmetry is pre-existing.** The same asymmetry already applies to the shipped `running ──→ slot_cleanup` edge, `slotstate` ignores the concurrency scoping entirely, and §5.2 and §6.2's leaked-semantics prose state the outcome unconditionally. Three lenses judged it below the bar. A later round asking why the leaked terminal is unreachable on a single-session pod should know it is structural.
- **The leaked-occupancy hold has no durable backing, and that is pre-existing.** The Redis `active_slots` counter's only rehydration source is `SessionStore.GetActiveSlotsByPod` over Postgres rows with `state='active'`, and a failed bind has no active row. The same hole already exists for the shipped leaked class. A later round wanting to close it should widen §5.2's rehydration text rather than §7.1.
- **Dead end, closed on the refute side: the `concurrent_slots_exhausted` gloss.** spec/05:549 reads "pods exist but all slots are full", which does not literally describe a pool whose only candidate was disqualified. It is ALREADY loose in the shipped tree: `ClaimSlot` returns `ErrNoConcurrentSlot` whenever the pool list is non-empty and no candidate is placeable, `ErrTenantMismatch` maps to the same reason, and candidates skipped by `expiredByUptime` do too. Adding one more skip reason does not falsify a gloss that already over-claims. Five lenses reached it and each refuted itself. EVIDENCE: podclaim/slotclaimer.go:505-522; podclaimerror_internal_test.go:87-88.
- **Citation ranges to normalise, not to file.** Several proposal citations give `SocketRuntimeProcess.Close` as `:441-467` and the listener bind as `:155-161`; the real spans are `:435-467` and `:156-161`. The cited ranges sit inside the real ones rather than being plainly false, so they were left alone. A future citation check should normalise them.
- **Settled, formerly disputed: "On the default disposition the pod is replaced." (spec/04:686) has exactly ONE site.** The pass-1 refutation asserted the sentence also appears verbatim at spec/29:696. It does not: `grep -rn` over spec/ and docs/ returns spec/04:686 alone, and §29 item 12 carries a differently-worded restatement ("the adapter closes the session runtime and the pod is replaced"). Two lenses confirmed this independently. The refutation's conclusion still stands, because the sentence is already loose today for any per-slot `Shutdown` on a co-tenanted pod, so the proposal does not introduce the looseness; its evidence does not. A reviewer arguing a missed §29 edit site from the two-site claim will find only one site. UNVERIFIED: whether the row wants "on the default disposition at the session's release" is still undecided.
- **Disputed, unresolved: is the "workspace-prep handler inserts the entry after the reclaim was answered" race filable?** `spec.1.review-security.1` says no, because both `resolvePrepareStagingDir` and `FinalizeWorkspace` call `ensureSlotPaths` at the top of the handler, before the slow work, so a client-side deadline almost always postdates the insert. `spec.1.review-reliability.1` says the handler runs `ensureSlotPaths` with no ctx check even on an expired deadline and a code-lane reviewer should look. Neither corrects the other; both agree the remedy would be an adapter-side tombstone, which the summary rejects as a non-goal.
- **MISTAKE, refuted at least eight times: "§7.1 says the exclusive pod retires while §6.2:283 and §7.2 step 3 release it back to the pool".** Every dress of this has been built and dropped: false citation, unreachable disposition, the residue riding a pooled pod back into inventory, and the tenant-isolation framing. The §6.2 fence retires a failed pod, spec/05:455 says so outright, and the original refutation quoted spec/06:234's release-to-pool clause and reasoned past it. A later round wanting it needs NEW evidence that a pooled replacement pod actually keeps the residue, which means reading the mid-resume terminal handler's release path in the gateway and showing it is not a drain. Filing it on the same evidence costs two verifiers and closes nothing.
- **MISTAKE, four separate occurrences: the bound/started drift.** Three fixers restated the runtime-teardown precondition as "every bound entry the call removed"; the §29.4 rationale that carried the third is now rewritten onto §29.4's own Preconditions paragraph and that finding is discharged, so do not re-file it. `bound ⊋ started`: `assignCredentialsSlot` binds at AssignCredentials while only `claimSessionSlotUnderLock` sets `started`. The fourth occurrence runs the other way: a sentence that names the predicate correctly but assumes the `exited_cleanly` signal is uniform across a range the predicate splits. Check every new sentence that names the runtime teardown against §4.7's "a session whose start the adapter has admitted", and every new sentence that reasons about `exited_cleanly` against the `started` boundary as well as the `bound` one. In each case the conclusion was right and only the stated ground was wrong, so the fix is the predicate phrase rather than the decision. Do not run the drift the other way either, which a round-4 lens nearly did against CODE-2: `claimSessionSlotUnderLock` sets `st.sessionID` AND `st.started` together (slotsession.go:87-88), so CODE-2's guard `!ok || st.sessionID != sessionID` is satisfied at every `noteRuntimeStarted` call site, SDK-warm (sdkwarm.go:261, on the `fresh` arm after the claim) and `Resume` (resume.go:50 then :144) included. `assignCredentialsSlot` binds EARLIER; it is not the only binder, and the guard drops no record.
- **MISTAKE that cost a round: a narrow conclusion generalised into blanket cover.** A WATCHOUT closed with "The same holds for item 13's `terminate` frame. Do not file it as a missed edit site." What it had actually established was only that item 13 is not falsified by the TWO-TEARDOWN SPLIT, because item 13 is scoped to a session that ran. The co-tenancy gate is a DIFFERENT sentence of SPEC-1 and bites exactly the session ends item 13 enumerates, so item 13 was a real edit site and round 5 had to find it again. When writing a "do not file" note, say which sentence of the change it clears.
- **TRIED AND WITHDRAWN, four alternatives to §5.2's placement rule.** A third carrier that clears the row's §4.6 pod binding (a new gateway store write, and `ClaimSlot` pass 1 still admits a same-tenant claimed pod with free capacity, so it does not even deliver a different pod); a durable per-row exclusion (changes the created-state pod binding from create through start); an adapter tombstone of answered reclaims (touches pkg/adapter/slot.go and slotsession.go, which a later campaign position rewrites, and summary.md:154 already prices it); and deleting the constraint outright, which the standing trap already bars.
- **Do not write that a retried start inherits the first attempt's credential file or its armed §4.9 timers.** Only the slot TREE is inherited. `assignCredentialsSlot` rewrites both wholesale from the request's lease set, and a stale timer no-ops against a re-created entry. A reviewer's suggested fix over-claimed at exactly this point, and landing it would have added two new false claims.
- **Do not write "the retried start re-binds cleanly" in the acknowledged arm.** The reservation was released on failure and the retry re-reserves nothing, so the retried session runs on occupancy the counter no longer counts. That is pre-existing and deliberately outside this proposal, but an arm that asserts cleanliness is next round's finding.
- **`running` is bound vocabulary and every state-word occurrence is backticked.** Do not introduce a second, unbackticked sense (for example "ending a session the gateway believes is running") next to a bullet whose backticked `running` an earlier group is protecting. Say "takes the session off the shared runtime process" instead.
- **"pre-`running`" collides with an established term at a different level:** the session states `created`, `ready`, `starting` and `finalizing`. Do not simplify §5.2's corrected sentence back to a bare "pre-`running`". Three surviving "pre-`running`" phrases in the proposal are LABELS for the rule, the edge or the paragraph rather than range assertions, and a grep-and-replace that rewrites them adds words and no content.
- **Do NOT touch the "A start that races the reclaim" bullet's closing sentence** ("Neither ordering reports a cleanup outcome, because the slot never reached `running`"). Round 3 adjudicated it true-as-scoped and four later lenses re-derived the temptation. It reads like the same defect as the deleted §7.1 sentence and is not. If a round does file on it, the correction is the predicate ("the reclaim finds no session the runtime holds"), never the conclusion.
- **Do NOT touch spec/07:220's "Pre-attach terminal collapse" paragraph.** No replacement pod is claimed on those edges, so "no live workspace or running runtime to seal" stays true, and the §7.2 preamble deletion sharpens the contrast rather than breaking it. Five lenses have swept it as a candidate matching site.
- **Do NOT extend the §29 co-tenancy carve-out to §29.9 item 4b.** That bullet records that the specification does not state at what point of the eviction path the `terminate` frame is sent, which is an ordering claim the co-tenancy gate does not answer.
- **The §29.4 step-13 anchor is NOT unique, and it now occurs FOUR times.** "([§15.4.3](...), §28.5.3)." occurs at spec/29:645, :711, :887 and :982; :711 is the intended site. CORRECTED: earlier passes recorded two sites and then three. The instruction resolves only because it scopes itself to "§29.4's numbered step 13". Do not drop that scoping if the block is reworded, and do not report the anchor as unique in a sweep.
- **Do not file the §5.2 retry-policy trigger reading.** "The policy is triggered by an adapter-emitted slot-failure event for a running session, so SPEC-2's placement constraint reaches no bind failure" is a real tension between §5.2:544-557 and §6.2:283-286, and it is pre-existing: the code settles it the other way (`applySlotRetryPolicy` branches on `*podsession.SlotBindError`) and §5.2's own non-retryable list includes a bind-time reason. Three lenses dropped it.
- **Do not file the cross-replica actor problem in §7.2.** Step 3's "the connection that attempt still holds" against step 4's "fencing any stale coordinator" looks like a mis-assigned actor. Step 1 ("cancel the in-flight restoration RPCs") already presumes the sequence's replica owns those RPCs, and the reclaim is in fact issued by the failing attempt's own goroutine, so step 3 inherits the assumption rather than introducing it. Four lenses.
- **Do not file either `leaked` gloss.** §6.2:148's `slot_cleanup ──→ leaked` annotation and §5.2:562's whole-pod replacement trigger both gloss `leaked` as "cleanup timeout exceeded", which does not describe a reclaim the adapter never answered. §5.2's own bullet already says "If cleanup fails, the slot is leaked", broader than either gloss, and the shipped code already sets `leaked = err != nil || !cleanly` on the ordinary session end. Three lenses.
- **Do not file the `slot_cleanup ──→ released` annotation.** "(slot workspace removed, processes killed, slot released)" is today a word-for-word mirror of §5.2's three-action list, and SPEC-3 widens that list to five, so the annotation becomes an abbreviated gloss of a longer list. An abbreviated annotation states nothing false, and the structurally identical §5.2/§6.4 enumeration finding was already refuted. Three lenses, one of them standing next to the row while editing DOCS-1.
- **Do not file §11.4 revoke step 3.** "The pod's runtime adapter initiates graceful shutdown (SIGTERM to agent, wait up to 10s, then SIGKILL)" sits under a list scoped to "all active sessions", which are `started` by construction, and it is already false for any per-slot `Shutdown` on a co-tenanted pod. Pre-existing looseness this proposal widens rather than creates. A round that widens the revoke's scope should re-check the line.
- **Do not file §4.1's retained second sentence as drift this proposal creates.** Before the edit, sentence 2 already said "per-slot teardown"/"whole-pod teardown" while sentence 3 said "per-session teardown"/"whole-pod scrub". The edit adds a naming mismatch to a pre-existing one. It stays the standing OPEN on vocabulary, and four lenses have declined it.
- **Do not file the §4.7 "the pod's session mode" term collision.** The specification binds "session mode" to `executionMode: session`, while the axis that selects the starting RPC is pod-warm versus SDK-warm, which the same §4.7 table already spells two rows above. Four lenses derived it and all four declined it as wording. The fix is two words and belongs outside the loop; a fifth derivation is waste.
- **MISTAKE, barred and NOT closed: SPEC-4's prose against the untouched `receiving_uploads ──→ running` trigger.** The prose glosses that trigger as naming the moment the runtime "has been given" the session; the trigger's own words are "session dispatched to runtime with its session identifier". The orchestrator's already-fixed list names this contradiction, so filing it is re-litigation, but round 3 recorded it as only PARTLY discharged and rounds 4, 5 and 7 each re-derived it. The reconciling reading is that "a start still in flight" means the window between the adapter ADMITTING the RPC and `Runtime.Start` handing the session over, so nothing has yet been dispatched to the runtime. A round that wants it closed must either edit the fence annotation (and docs/reference/state-machines.md:235 with it) or delete SPEC-4's "which is the moment the trigger above names" clause. It propagates into the report gate, because SPEC-3 keys the withheld report on "before it reaches `running`".
- **Dead end, and here are its five sites: "the withheld report contradicts §4.7 or §12".** spec/04:692, spec/05:453, spec/12:481 and :494, schemas/lenny-adapter.proto:308-319 and docs/reference/adapter-contract.md:81 all scope their reporting rule to "a session release", and the proposal frames the pre-start reclaim as not one. Grepping `ReportSessionScrub` across spec/, docs/ and schemas/ lands on exactly this set. CORRECTED: spec/05:545 was in that list and does not belong there. Its `**Slot cleanup:**` bullet reads "On slot completion or failure ... The adapter reports each slot cleanup outcome (`released` or `leaked`)", which is a trigger rather than a session-release scoping. The dead-end verdict has survived many rounds anyway, so a round that wants to re-open the bullet-versus-scrub-model tension must argue from the bullet's OWN trigger and from SPEC-3's new pointer sentence (spec-changes.md:520), never from the "session release" framing.
- **MISTAKE nearly filed: the recycle-disposition `Shutdown` against §5.2:459.** "The adapter closes the ending session's runtime" is not falsified by SPEC-1's no-op sentence. The two-RPC split is an implementation fact; the spec still models one request carrying the disposition beside the teardown, so the applied spec is self-consistent and the divergence is pre-existing.
- **MISTAKE nearly filed twice: the withheld report as a residual-state relaxation.** Neither "a tenant drives N pre-`running` attempts onto one pod without advancing `recycle.maxSessionsPerPod`" nor "SPEC-3 relaxes the residual-state bound" clears the bar. Today's failed-bind path sends no `Shutdown` at all, so the counter does not advance there either, and the per-slot cleanup the proposal ADDS is what reclaims the residue. The whole-pod scrub and `maxScrubFailures` remain the backstops.
- **MISTAKE nearly filed: the reclaim as a deadlock sink.** "§7.2 step 1 stops the 300s `resuming` watchdog and step 3 then blocks on an unbounded adapter RPC, so `resuming` becomes the deadlock sink §6.2 forbids" does not survive: the code lane bounds the reclaim with `slotCleanupBudget` (5s floor), the Design paragraph names §5.2's figure, and §7.1's own leaked disposition presupposes the gateway gives up. Two lenses.
- **MISTAKE nearly filed: the replica-local leak ledger at multi-replica scale.** `slothealth.Tracker.leaked` and `.events` are in-process maps, so a pod may never reach `ceil(maxConcurrentSessions/2)` while its cluster-wide Redis occupancy stays held. The fact is true and the finding is a close variant of the already-refuted occupancy-hold family, whose refutation granted the other retirement routes (`recycle.maxSessionsPerPod`, `maxPodUptimeSeconds`) as the bound.
- **MISTAKE nearly filed: the create-time-reserved bullet's unscoped `leaked`.** A create-time-reserved slot exists only on a concurrent pool, so the bullet's unqualified `leaked` is correct on its own domain and is not the unscoped-`leaked` defect an earlier round fixed twice. Two lenses.
- **MISTAKE nearly filed: the tree-removal window on the acknowledged branch.** The round-4 bullet attaches "a reclaim whose tree removal is still running when the retry materializes the same tree" to the ACKNOWLEDGED branch, where the removal has completed before the gateway returns the error. It survives on the reading that a client which times out and retries `POST /start` concurrently with the still-in-flight compensation produces exactly that interleaving. Defensible; do not spend a round on it.
- **Read the §7.1 block's long line whole before editing any part of it.** Two sentences roughly 250 words apart on one physical line pull opposite ways, and the same false proposition was stated a second time in the Design copy with "first pod-side RPC" where the staged paragraph says "first such RPC", so a grep anchored on one wording misses the other. Rounds 1 and 2 both fixed this paragraph by deleting one restatement and both left the neighbouring one standing. The paragraph's disease is structural: it derives consequences from rules other sections own, so sweep the WHOLE paragraph rather than removing them one finding at a time.
- **§7.1's obligation has TWO bounds and every delegation names only the lower one.** Any future edit that hands a new path to §7.1 must state what happens on that path once the slot is `running`.
- **`context.WithoutCancel(ctx)` in CODE-4's compensation is load-bearing.** It is what makes the reclaim survive a cancelled resume context; a later edit that "simplifies" it back to the inbound ctx silently deletes the mid-resume cancel case.
- **Do NOT close the §7.2 gap by pointing at §7.3.** The §7.3 appended sentence binds "a step in this flow that fails", and a client, parent or admin cancel is an external terminal trigger rather than a failing step, which is exactly why the proposal edits the §6.2 cancel bullet separately.
- **The imprecisions that were verified and declined, so nobody re-verifies them.** "The row keeps its §4.6 pod binding" (§4.6 is "Pod Lifecycle Controllers" and owns the `SandboxClaim` binding, while the session row's create-time pod is persisted at §7.1 step 5, so the attribution is loose rather than false); the Design's "closes by stating that the client never receives a `session_id`" (that sentence sits mid-paragraph at spec/07:23 with more sentences after it); SPEC-2's §6.2 note "Neither bullet enumerates that sequence" (its load-bearing half, that neither names the step-4 bump, is true). Do not spend a verifier pair on any of them.
- **Round 3 deleted the only text that stated why a §7.3 re-attach cannot land on the reclaiming pod,** and the Design paragraph's "placed by neither mechanism" lost one of its two antecedents with it. The mechanism still holds in code, so this is a record gap rather than a coverage gap. A round asking "what keeps a re-attach off the reclaiming pod" should read the Settled entry on `Claimer.Claim` rather than reopen the question.
- **MISTAKE, a FOURTH occurrence of the unscoped `leaked` terminal, and the most expensive.** A fix round appended a sentence to §5.2's `**Scrub model.**` paragraph asserting the `leaked` disposition, the held occupancy and the whole-pod replacement trigger with no concurrency qualifier, inside the paragraph the proposal itself declares to hold "on a pod of either concurrency", against staged §7.1's explicit "stated for concurrent occupancy and do not apply there". Five lenses filed it in one round. The structural cause is that the scrub-model paragraph is deliberately concurrency-independent, so ANY sentence appended there that names `leaked`, the trigger or the gauge needs its own scope. It was fixed by splitting the sentence by pod concurrency; check every future append against this.
- **Do NOT collapse that finding into the standing "the `slot_cleanup ──→ leaked` scoping asymmetry is pre-existing" trap.** That trap is about shipped §5.2 and §6.2 prose being loose against the fence. The round-8 defect was a contradiction between two sentences THIS proposal stages, created by a fix and against the proposal's own recorded DECISION. A verifier that merges the two refutes a live finding with an unrelated refutation.
- **Do NOT add a bind-path or a start-state qualifier to §5.2's leak-accounting sentence.** The bind-path qualifier is precisely the carve-out by code path that §5.2's trigger forbids, and it would falsify three untouched shipped sentences (spec/05:545, spec/06:160, docs/reference/state-machines.md:251). The path question is answered in the code lane by the third `accountSlotFailure` caller; the start-state question is answered by CODE-1's `live` gate. CITATION RE-POINTED (the `f4` firing deleted the conformance-gap bullet this trap used to cite): the same no-carve-out argument now lives in CODE-5's `accountSlotFailure` doc comment ("which the trigger states with no carve-out by code path", non-spec-changes.md:515) and in its caller list at non-spec-changes.md:537-569.
- **Do NOT surface `treeErr` on the started path unconditionally to close a clean-exit gap.** It reclassifies ordinary session-end slots as `leaked` under `Binder.ReleaseSlot`'s `err != nil || !cleanly`, which is the §6.2 accounting change the proposal explicitly declines. The form that works, and that landed, is gating on the report predicate (`live`) rather than on the teardown predicate.
- **Do NOT simplify CODE-1's three predicates into fewer.** The report gate and the response gate are both `live`; the runtime teardown stays `st.started` and must, because CODE-2's rollback fires only when `Runtime.Start` returns, so a start that hangs under a `runtimeLive` teardown gate would leave the runtime serving an abandoned session forever. Three predicates, two of which are the same one, is the correct shape.
- **MISTAKE that cost a whole open-decisions firing: a scope call resting on shipped behaviour a staged deliverable removes.** An out-of-scope row argued "no staged deliverable adds a second producer" from `BindReservedSlot` swallowing its own reservation-release error, while the same proposal's CODE-4 retires exactly that swallow. The false ground then propagated into the summary, the SPEC-4 exclusion paragraph and two edge-case bullets. When adjudicating a scope call on this proposal, check the ground against the STAGED tree, not the shipped one.
- **MISTAKE, three rounds on one sentence: the tier-7a assertion list.** Round 1 wrote three universals ("`runtimeLive` is empty", "`runtimeIdleLocked` is true", "the runtime was closed exactly once") beside a co-tenanted variant that falsifies the first two and a `FailedPrecondition` invariant that is interleaving-scoped. Round 2 shrank the list and produced a smaller false universal. Round 3 deleted both sentences that described a distribution over interleavings and fixed one interleaving instead. The list itself was the defect: a shared assertion list beside per-variant and per-interleaving axes.
- **Do NOT reintroduce a "the runtime was closed exactly once" assertion, in any form.** As a call count it is two: the reclaim closes under the `started` gate and CODE-2's rollback closes again. As a process-teardown count it is zero on the co-tenanted variant, because `SocketRuntimeProcess.Close` early-returns on `!p.connected` and again while a started co-tenant keeps the active set non-empty. Whether the reclaim's close or the rollback's close tears the process down depends on where the park sits relative to `accept`, which is a fixture decision nothing has made. The effect is pinned instead by the shipped `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` and by the tier-1 co-tenancy case.
- **TRIED AND WITHDRAWN: fixing the tier-7a case by admitting both interleavings.** Dropping the park and running the barrier form makes CODE-2's guard exercised only on the runs that happen to lose the race, which turns the case's own subject into a lottery under a stress budget, and every assertion becomes conditional on an arm the test can only identify by reading the outcome it is asserting.
- **CODE-1's at-most-one-report-across-interleavings rule is stated ONCE,** in the CODE-1 section beside the `live` gate that carries it. The test inventory points at it. Restating it in the tier-7a paragraph is exactly what produced the round-3 contradiction.
- **Do NOT add `noteRuntimeClosed` to CODE-2's rollback.** `noteRuntimeStarted` refused, so the session was never inserted; the call is a no-op and it cannot remove a co-tenant. Adding it to make a mis-scoped test assertion pass would be a code change answering a prose defect.
- **MISTAKE, the one-caller reading a second time: "`StartSession` is the only site the compensation can race".** CODE-4 compensates `Binder.Resume` two pages later, and the proposal's own text says the adapter's `Resume` leaves the identical residue. The two statements sat in one file for several rounds and cost a missing rollback, a missing tier-7a arm and an attribution error in the summary. Any future sentence naming `StartSession` alone on this surface must be checked against `resume.go:50,:140,:144` and `sdkwarm.go:217,:261`.
- **Do NOT put the credential-lease release back inside `compensateFailedSlotBind`,** and do not gate it there with a stage check, a `req.SlotID != ""` check or a nil-credentials check. Every such conditional makes the function's contract depend on which caller it has, which is the coupling that produced the finding. The release belongs to the wrapper that owns the stages that mint a lease.
- **Do NOT make the bind-path lease release conditional on `assignSlotCredentials` having SUCCEEDED.** It mints per provider in a loop and returns on the first error, so a failed credential-assignment stage can already hold minted leases. Unconditional in the wrapper is the fail-closed choice and `ReleaseSession` is a documented no-op for a session holding none.
- **The §7.2 step-2 anchor is a mid-line fragment.** The staged sentence lands BEFORE the `final_workspace_ref` sentence rather than at the end of step 2, because both sit on one physical line. It is deterministic and appliable, so it is not a finding, but an implementor expecting an append-to-end will be surprised, and a reviewer must not "fix" it by re-anchoring.
- **Dead end, and here is its resolution: `docs/reference/adapter-contract.md:75` is the site and `:81` is not.** The `Shutdown` row at :75 is the reader-facing mirror of the §4.7 row SPEC-1 rewrites and is falsified in four ways, only one of which depends on the contested "is a pre-start reclaim a session release" framing; frame any finding on the drain-gate and usage-flush halves. The four sibling sites (`adapter-contract.md:81`, `security-principles.md:33`, `execution-modes.md:68`, `multi-tenancy.md:72`) all say "at each session release", which SPEC-3 widens rather than falsifies. Earlier passes let the DEFERRED and the dead-end trap pull opposite ways on :81; they do not.
- **Dead end: "`SlotID == SessionID` falsifies §5.2's `new slot` and the Fresh workspace guarantee".** Three lenses built and dropped it. "New slot" reads as a new reservation, the placement rule keeps the hazard off every retry §5.2 places, and it is already false in the shipped tree, where no `Shutdown` is sent on a failed bind at all so the tree always survives. The staged change strictly improves it.
- **Do NOT file `spec/12:913` or `spec/15:1795` against SPEC-1's co-tenancy gate.** They are the same class as the §11.4 revoke trap: each is already false for a co-tenanted pod against the shipped `boundRemains` gate, and each states a delivery intent rather than a delivery guarantee. A round that widens the gate's scope re-checks all three together.
- **Do NOT file §5.2's `**Recycling and integration levels.**` sentence** ("recycling runs with no CH-RUNTIMEOPS exchange between sessions") against SPEC-1. `drainViaLifecycle` already fires under `!boundRemains` in the shipped adapter, so the sentence is loose today and SPEC-1 restates rather than widens.
- **Do NOT file the unbound co-tenant against the drain gate any more.** A registered-but-unbound co-tenant has an empty `sessionID` and so does not hold the signal off, but after CODE-1 the signal needs `started` AND no bound entry, and the residual case is an ordinary started session end, which is shipped and pre-existing.
- **The exclusive-pod retirement claim now has THREE staged sites:** §7.1's new paragraph, the accepted-failure-mode bullet, and §5.2's `**Scrub model.**` append. A future edit that adjusts the wording in one must sweep all three, and "Spec files touched" gives no hint that a pod-disposition claim now lives in §5.2.
- **The staged exclusive-pod carve-out fails on the sequential-reuse configuration.** `maxConcurrentSessions: 1` with `recycle.enabled: true` is supported, and §4.6.1 projects `idle` for a claim deleted on a recycling pod, so "the pod retires, so the reclaim's residue does not outlive the pod" is not universal. Do not read spec/06:283 as settling it either way; it asserts both horns. A round that wants this needs the §4.6.1 projection route rather than §7.3 reachability, and it must answer why the occupancy-zero whole-pod scrub does not sweep the residue anyway.
- **MISTAKE nearly filed, five performance dresses.** The SIGTERM→SIGKILL pivot the `deadlineMs: 0` compensation would hand `SocketRuntimeProcess.Close` is guarded on `cmd != nil` and no production wiring sets `SpawnPath`; the §16.5 creation SLO samples successful creates only, so neither the compensation's latency nor its held connection breaches a stated budget; `slothealth`'s never-evicted maps predate the change; and the reserved-branch drain composite is strictly better than today, because today's `leaked=false` release already deletes the claim on a solo-occupancy pod.
- **MISTAKE nearly filed: "the `ExcludePod` pass-2 exclusion is unreachable".** True that a leaked release leaves the claim, so the excluded pod is projected `claimed` and can never be a pass-2 idle candidate; pass 1 is the load-bearing one, because a pod at concurrency 4 holding one leaked slot still has free capacity. A read-only skip on a branch that cannot fire is defense in depth, and the fix would be deleting a harmless filter.
- **Do NOT read `pkg/sandbox/slotstate` as adapter-side.** It is the gateway's in-process registry, constructed in `sessionserver.go`, and `pkg/adapter` does not import it. The summary's Redis-rehydration row cites it for a claim about the adapter; the substance holds, because the adapter's only pod-wide count is the unexported `slotCount` used for output demux and it is on no RPC, but the citation does not support the sentence.
- **Do NOT file the §4.9 timer cancellation as stranding a credential file.** `onSlotLeaseExpired` returns immediately when the slot is no longer in the registry, so leaving the timer armed would scrub nothing either. If a round ever wants the ordering closed, the edit is to condition the cancellation in SPEC-3's sentence, never to widen §4.9.
- **WATCHOUT: `pkg/gateway/podlifecycle/podsession/resume_slot_reservation_test.go` is named nowhere in the proposal.** It holds `TestResumeReleasesItsReservationWhenTheAdapterResumeFails_spec_5_2`, which drives a FAILING `Binder.Resume` against a real `adapter.New(...)` server and asserts the `active_slots` decrement and the claim disposition, which is exactly the branch CODE-4 changes. Traced and green: the adapter holds no entry for the session, so the compensating `Shutdown` answers `ExitedCleanly: true` under both the shipped gate and CODE-1's, `Leaked` is false and `releaseResumeSlot` decrements as today. Do not file it as a broken test; do re-check it if CODE-4's disposition wiring changes. EVIDENCE: that file, :211-261.
- **MISTAKE nearly filed, three dresses in one round: bookkeeping against the deliverable index.** summary.md:58's "the one new field is `ExcludePod`" against CODE-4's second in-process field `SlotBindError.Leaked`; CODE-4's index line naming three files while its own call-site table stages three edits in `pkg/gateway/sessionserver/start.go`; CODE-5's index line naming two files while its Targets name `podsession.SlotBindRequest`. All three are real and verifiable, all three are covered by the deliverable body and by `## Files touched on application (non-spec)`, and none changes what an implementor builds. This loop has now refuted five of that class on the materiality bar; price a bookkeeping candidate against it before spending a verifier pair.
- **MISTAKE nearly filed: S7's tier list omits tier 7a.** CODE-1 moves the `Shutdown` drain gate from `bound` to `started` and `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` is the shipped suite pinning that gate, but every session those tests drive is started through `startDrainSession`, so the moved gate cannot reach them and `boundRemains` is untouched. S8 depends on S7 and runs 7a one step later anyway. EVIDENCE: non-spec-changes.md:874-884,:209-218.
- **MISTAKE nearly filed twice: "the per-stage compensation table omits the workspace-prep stage".** True that `materializeSlot`'s first failure branch is `stageWorkspace` (slotbinder.go:284-289, `slotFailureWorkspacePrep`) and that it is the stage producing the class-1 registered-but-unbound residue, but the compensation is stage-agnostic by construction (one wrapper), `FinalizeWorkspace` reaches `ensureSlotPaths` the same way and so produces the identical state, and the adapter half of that residue class is pinned at tier 1 by the "Registered but unbound" case. CORRECTED, and this is a filed finding rather than a note: the clause this entry used to close with, that the upload-free branch is the one stage failure sending no RPC at all, is false. `stageWorkspace` CANNOT fail on an upload-free plan (`rewriteExtractedSources` returns the plan unchanged, the blob-fetch loop is entered only for `uploadFileRefs`, and `PrepareWorkspace` is the last statement), so every reachable `stageWorkspace` failure carries uploads and Settled #65 is the correct statement. `[non-spec.2.review-mechanism.1]` filed it against non-spec-changes.md:493-498 and the tier-1 case at :847-850. Do NOT collapse the finding into the refuted "over-sending an obligation does not violate it" item, which GRANTED the premise this finding attacks. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1284-1332,:1346-1354,:1408-1420.
- **MISTAKE nearly filed: the tier labels on the `podsession` cases.** `podsession/binder_test.go:174-180` starts envtest while the sibling `slotbinder_test.go` cases are filed tier 1, and `slotbinder_test.go` carries `// diagnosis:` on 12 of its 17 tests where the fake-backed `sessionserver/slotretry_test.go` carries none on 15. No gate turns on it, for the reason in the Settled entry on directory-based tiers, and the tests get written and run either way.
- **MISTAKE nearly filed: `export_test.go` against the Testing section's "No export_test.go change".** non-spec-changes.md:639 contradicts :289 and :998 literally, but the sentence's own justification clause scopes it to "no NEW seam is needed for these cases", and an implementor builds S8 from CODE-2's scope, which names the file and the line.
- **MISTAKE nearly filed: the R1b "No impact" impacts row against the proto `Shutdown` comment.** After SPEC-1 the comment at schemas/lenny-adapter.proto:203-206 is further falsified and R1b is the only step allowed to open that file, but the comment is ALREADY stale for any per-slot `Shutdown` on a co-tenanted pod, so this is the same widening-of-pre-existing-looseness class as the §11.4 revoke trap. Checked in the same pass and NOT falsified: the `ReportSessionScrub` comment (:308-311) and `SessionScrubOutcome` (:436-438) both scope themselves to "every session release", which SPEC-3 widens. The correction that IS owed is the standing Deferred against the proto, which R1b carries.
- **MISTAKE nearly filed, and here is what kills it: "the `exited_cleanly` rule's `live ||` disjunct has no test in either direction".** True as stated, since no listed or shipped case drives a `Shutdown` whose `removeSlotTree` fails, but the exclusion paragraph (non-spec-changes.md:695-701) already records that arm as excluded for a portability reason (`removeSlotTree` calls `slotlayout.RemoveTree` with no injectable ops) that covers the started arm identically even though the paragraph names only the unstarted one. The only cheap discriminating assertion left is already enforced by the shipped `TestShutdownIsANoOpForAnAlreadyReleasedSession_spec_4_7` (session_test.go:469-473): inverting the disjunct to `live && treeErr == nil` turns its `ExitedCleanly` assertion red. The residual is one sentence of scope in the exclusion paragraph.
- **MISTAKE nearly filed: the tier-3 dismissal's attribution.** non-spec-changes.md:789-791 says the no-op clean-exit answer is pinned at tier 1 "by the adapter cases above" and end to end at tier 4 below, and neither is literally true, because no listed adapter case and neither tier-4 case drives `Shutdown` for a session the adapter holds NO entry for. The behaviour is covered anyway by the shipped `TestShutdownIsANoOpForAnAlreadyReleasedSession_spec_4_7`, and it is UNCHANGED by CODE-1 (`removed` false gives `closeErr == nil && (false || true)`, the same answer the shipped `bound` gate gives), so the attribution is loose and no changed behaviour is untested.
- **MISTAKE nearly filed: "tier 5 is reached, because CODE-5's reserved and resume callers newly drain a pod nothing drains today".** `DrainSandbox` itself is untouched, the new callers reach an unchanged annotation stamp whose WarmPoolController chain has its own shipped coverage, their own discriminators are asserted at tier 1, and the kube-visible consequence (the per-pod `SandboxClaim` surviving at `bound`) at tier 4. Same shape as the tier-8 item three rounds have declined.
- **Dead end: "withholding `emitFinalUsage` for a bound-but-unstarted entry strands a delegation-tree budget".** The §12.4 budget return runs from the archive path (`pkg/gateway/sessionserver/usage.go:648-668`, `returnTreeBudget`) rather than from the final usage report, and a never-started session's usage meter is empty anyway. Chased as an unlisted consequence of moving `emitFinalUsage` into the `started` block; there is none.
- **TRIED AND WITHDRAWN: CODE-2's rollback releasing the slot by session identifier.** The staged rollback called `s.releaseSessionSlot(sessionID)`, which re-reads `s.slots[sessionID]` at call time and `RemoveAll`s whatever tree it finds. Because the guard that fires the rollback has already established "absent OR registered-but-unbound", the call can only be a no-op or a destruction of a later attempt's entry and staged uploads, and the window is as wide as a `Runtime.Close`. It was CODE-2's own new instance of a shipped five-site pattern; the round removed the new instance only. Do not reinstate it, and do not reach for the identity-checked deregister here: that fixes a pre-existing class, changes `claimSessionSlot`/`deregisterSlot`/`deregisterSlotLocked` signatures inside a file a later campaign position rewrites, and wants its own problem statement.
- **The reverse-ordering sub-case does NOT reach CODE-2's rollback at all.** If a successor re-claims through `claimSessionSlot`, `st.sessionID == sessionID` and `noteRuntimeStarted` returns TRUE against the successor's entry, so the abandoned start records into `runtimeLive` and returns success. Any test written as "the rollback runs and the bound successor survives" is wrong on the arm it names; only the unbound arm discriminates. The staged tier-1 "The rollback destroys no successor" case says so explicitly.
- **MISTAKE that cost a finding, a design pass and a fix pass: "the pointer change costs no test edits".** Round 5 settled the `ExcludePods` pointer threading and asserted that returning `*podsession.SlotBindRequest` from `slotretry_test.go`'s `req` helper would leave every call site compiling. It would not: the helper feeds `classifySlotBindFailure` at four sites whose callee keeps a by-value parameter. The claim came from grepping ONE callee of the fixture helper rather than every caller of the helper, and it put a false compile-time claim into a staged deliverable. Before asserting a signature change is free, enumerate the helper's callers, not the callee's.
- **TRIED AND WITHDRAWN: making `req` return a pointer.** It compiles for the twelve `applySlotRetryPolicy` sites and breaks the four `classifySlotBindFailure(in, req(...))` sites in the same file. Also withdrawn: a second `reqPtr` fixture (two near-duplicate request fixtures in one file to avoid twelve one-line locals, and the local is what makes per-call ownership legible in the load test's goroutine), and widening the deliverable so `classifySlotBindFailure` takes a pointer (two production callers plus a composite-literal test caller, and a nil-dereference surface on an error-mapping helper).
- **MISTAKE: CODE-2's rollback answering `codes.FailedPrecondition`.** It made a transient reclaim-versus-start race a permanent 422 `SLOT_FAILED{retryable:false, category:"policy_rejection"}` with no `Retry-After`, and `applySlotRetryPolicy` skipped the retry on attempt 0, against the proposal's own "No failure class becomes non-retryable" and staged §7.1's "Nothing about the attempt's retryability changes". The fix is the status code, and it is the reason those two sentences need no rewrite. spec/15:1136 independently makes an adapter `FailedPrecondition` the deterministic-permanent code in the setup window.
- **Do NOT touch `podsession/slotfailure.go`'s `Reason()` switch to carve `slotFailureSessionStart` out.** It is on the files-touched list only for `SlotBindError.Leaked`. The carve-out widens to EVERY start-stage `FailedPrecondition`, including the genuinely deterministic "adapter is not configured with a runtime" at session.go:104, and it leaves the adapter still saying "precondition failed" for a concurrency abort. `codes.Unavailable` is also barred: `claimSessionSlotUnderLock` already mints it twice on the same claim path (slotsession.go:69-71,:82-84).
- **Do NOT mirror the `spanErr` line onto the `Resume` rollback, and do not change its category.** `Server.Resume` opens no span, and the `StartSession` arm's `tracing.CategorizeError(rollbackErr, tracing.CategoryTransient)` is the second carrier of the same transient classification the status code establishes. A future edit that categorises it Permanent re-derives the defect the round closed.
- **The rollback's status code is asserted verbatim in two test descriptions far from CODE-2** — the tier-1 "Start-versus-reclaim rollback, deterministic form" case and the tier-7a start-versus-reclaim case's per-arm sentence — plus the new gateway tier-1 classification bullet. Any future change to the code must grep the proposal for the code name rather than read CODE-2 alone.
- **TRIED AND WITHDRAWN: wiring a `RuntimeOps` into the tier-4 fixture to observe the §15.4.2 signal.** It duplicates roughly seventy lines of `startDrainPeer` across a package boundary (tier-7a's helper is `package tier7a_load_local_test` and not importable) and hard-codes a carol-before-alice ordering nothing else in the case needs, to re-observe a gate this proposal does not touch. Also withdrawn: adding a co-tenanted variant to the tier-1 "Bound but unstarted" case, which contradicts that case's own load-bearing "leave no other bound entry on the pod" condition, and moving the clause to tier 7a, whose section already says no new signal-frame case is added.
- **Do not "fix" the tier-4 carol clause to say the connection survives but not the listener.** `SocketRuntimeProcess.Start` takes the `p.connected` early return for a second session and never rebinds the listener, which reads like a defect. It is not: `Close` tears connection and listener down in the same last-close branch, so a surviving connection witnesses a surviving listener on the shipped tree. Weakening it would also falsify the 0078 impacts row, which asserts the listener half holds independently of 0078.
- **`startRuntimeOps` (runtimeops_test.go:83) is cheap and used by eight tier-1 adapter tests, which makes "just add a co-tenanted tier-1 drain case" look free.** It is not: it re-pins an UNCHANGED gate and collides with the tier-1 "Bound but unstarted" case's stated reason for excluding a co-tenant. The proposal's discipline for an untouched mechanism is to attribute it to the shipped test that owns it and add nothing.
- **`slotTreeProbe` probes the credential FILE (`paths.CredentialsFile`), not the credential directory** (slotsession_test.go:77-85), so a successor created through `ensureSlotPaths` alone has no credential file and a survival assertion on one there is unsatisfiable. The bound sub-case has to run `AssignCredentials` first.
- **The §5.2 append can silently redirect four tier-11 gates, and only case sensitivity is stopping it.** `lineContaining` returns the FIRST matching line, and SPEC-3 appends to the `**Scrub model.**` paragraph at spec/05:453, which precedes every §5.2 bullet a gate anchors on. The gates key on capitalised "Whole-pod replacement trigger", "Session count limit", "Uptime limit" and the retirement-logging line; the append writes "whole-pod replacement trigger stated below" and "the pod's served-session count" in lower case. A fix round that recases either phrase, or that adds any of those anchor strings to the append, turns `TestLeakedSlotCountingLifetimeAgrees_F5231` and its three siblings red by scoping them to the wrong line. The same first-match hazard applies to `requireLine(s72, "per-slot sub-states")`, whose single occurrence is spec/07:162.
- **A later edit that puts a scoped-block edge string into SPEC-4's new prose paragraph would silently pass the negative loop,** because `generalBlock` runs to the end of §6.2 while `scopedBlock` ends at the general header. The current text is safe (single arrow, no "`leaked` slot semantics" phrase), and it is safe by accident rather than by design.
- **`SlotBindError.Stage` is doc-commented as the `lenny_slot_failure_total` error_type label** (slotfailure.go:63-64), which reads as if CODE-4's `Stage: "resume"` mints a metric label value. It does not: `slotFailureConnect = "connect"` is the shipped precedent for a Stage that is explicitly not an error_type value (binder.go:286-299), and the counter is emitted only by the five `recordSlotFailure` calls inside `materializeSlot`. Do not file the label-cardinality candidate.
- **`fakeSlotBinder.BindSlot` discards its request** (`_ podsession.SlotBindRequest`, slotretry_test.go:25-49) while three staged tier-1 cases assert on the `ExcludePods` a `BindSlot` call carries, and non-spec-changes.md:813 says the queue case runs "over the shipped `fakeSlotBinder`". The fake needs a recorded-requests field. Not filed (one struct field, file already in the edit list); a fixer touching that paragraph should add the clause.
- **MISTAKE nearly filed: "the SDK-warm freshness arm leaves `started && !live` reachable, so CODE-1 withholds `ReportSessionScrub` for every SDK-warm session".** `fresh` is false at sdkwarm.go:221/:260 only when `claimSessionSlotUnderLock` short-circuits on `st.started` under `idempotentRepeat` (slotsession.go:80-86), that is, a REPEAT `ConfigureWorkspace` for a session whose first, fresh call already recorded. So `runtimeLive` holds it and `live` is true. The `fresh` guard is what makes the Settled `started && !live` claim hold, not what breaks it.
- **MISTAKE nearly filed twice: "`cancelPodMCPIfRuntimeIdle` moving into the `removed` branch cancels a co-tenant's armed MCP surface".** Double-guarded by `runtimeIdleLocked()` and `mcpArmingHeldLocked()` (slotsession.go:238-260), and the only arm it newly reaches is a reclaim of the session that armed the surface itself.
- **MISTAKE nearly filed, five mechanism dresses in one round.** (1) The design's race analysis misses the middle ordering where the reclaim's deregistration precedes the start's claim while its `removeSlotTree` runs after it — real, but the same window is written out in full in CODE-5's "When it does not fire" bullet. (2) CODE-4's unconditional `releaseCredentials` releases leases a snapshotless resume-rebuild may still hold — the rebuild re-mints on every attempt and the barred "do not make the lease release conditional" trap covers it. (3) CODE-3's doc-comment gloss drops the "or fails" conjunct — an abbreviated gloss in a Go comment states nothing false. (4) The compensation is sent for an upload-free `stageWorkspace` failure where no pod-side RPC was issued — over-sending an obligation does not violate it, and the staged §4.7 no-op sentence makes the extra send safe. (5) The §29.4 step-13 sentence scopes the co-tenancy gate to a pod serving concurrent sessions while §4.7 states it unconditionally — on a one-session pod a bound co-tenant cannot exist, so the scoped statement is vacuously equivalent.
- **MISTAKE nearly filed: "the placement constraint adds a `WARM_POOL_EXHAUSTED` cause `docs/operator-guide/troubleshooting.md` does not carry".** The table at :36-44 enumerates warmup-failure causes only and is ALREADY incomplete against the shipped tree, where `ErrTenantMismatch` and uptime-expired candidates both surface as `WARM_POOL_EXHAUSTED` and appear in no row. `docs/reference/error-catalog.md:143` is loose the same way. One more instance of a pre-existing enumeration gap does not clear the bar.
- **MISTAKE nearly filed: "spec/15:1136's `recovered with a fresh pod` contradicts the create-time-reserved edge-case bullet's `reconnects to the same pod`".** The bullet lands in no spec file, and the divergence is already shipped: `BindReservedSlot` reconnects from the persisted `row.PodAssignment` and nothing in `pkg/gateway/sessionserver` clears it, so spec/15:1136 and docs/reference/error-catalog.md:156 are already loose for the concurrent create-time-reserved `/start` path.
- **MISTAKE nearly filed: `docs/runtime-author-guide/lifecycle.md:319,:330` against SPEC-1's co-tenancy gate.** Same class as the §11.4 revoke, spec/12:913 and spec/15:1795 traps: the shipped `!boundRemains` gate at session.go:259 already makes it false on a co-tenanted pod, so SPEC-1 restates rather than widens, and the remedy is a docs edit.
- **MISTAKE nearly filed: "the §13.1 sequential-reuse credential-isolation guarantee is broken by the staged exclusive-pod carve-out".** Killed twice: the occupancy-zero whole-pod scrub sweeps the credential residue regardless (spec/05:461 step 0, :471 step 6), and CODE-4's unconditional `releaseCredentials` revokes the lease gateway-side. Same evidence base as the already-refuted exclusive-pod family.
- **MISTAKE nearly filed: "the reclaim's `Runtime.Close` on a co-tenanted pod takes the last-close branch and widens the pod-brick exposure to the failed-bind path".** The widening is real and it is availability rather than a security bound; an ordinary session end reaches the identical state through the identical call, and the pod-brick defect already has its own summary row and its own standing Open.
- **MISTAKE nearly filed, three capacity dresses in the round-2 performance lens.** (1) `ExcludePods` head-of-line-blocks the pool FIFO — `waitInQueue` yields the ticket to the TAIL, so an excluded waiter round-robins and merely burns its own `maxQueueWaitSeconds`. (2) The exclusion pushes retries from pass-1 packing onto pass-2 idle pods and drains warm-pool headroom at Tier 3 — `ExcludePods` names only the pods whose own reclaim went unacknowledged, `maxSlotRetries == 1`, and pass 1 still prefers any other same-tenant claimed pod with capacity. (3) A Redis outage turns the `relErr != nil` arm into a pool-wide drain storm — `applySlotRetryPolicy` already does `RecordLeak` plus `DrainSandbox` on that arm today, and the slot path is fail-closed under §12.4 anyway.
- **MISTAKE nearly filed: "`Binder.Resume` returning a `*SlotBindError` in its chain changes `isTransientPodClaimError`'s answer".** `isTransientPodClaimError` (start.go:3648-3682) matches only `*PoolWarmingError`, `*CredentialAssignmentError`, `*SetupCommandFailure` and five sentinels, and `SlotBindError.Unwrap` returns the cause (slotfailure.go:74-75), so every arm still matches through the new link. No classification moves and no test is owed.
- **MISTAKE nearly filed, four test-coverage dresses.** (1) "The `Resume` rollback has no deterministic tier-1 case" — the tier-7a case drives both arms from one rendezvous keyed on the RPC name, so it is listed; preferring a second framing is a preference between equivalent framings. (2) "The `slot_bind_failed` reason and the resulting `session_complete` frame have no assertion" — `drainReason` normalises and is untouched, so the frame's value set is unchanged. (3) The `isTransientPodClaimError` item above. (4) "SPEC-2's §7.2 step-3 reclaim has no listed test" — the §7.2 terminal-collapse handler is a separate HTTP request holding no adapter connection; the reclaim step 3 names is the one CODE-4's failure branch sends, which the staged resume case covers in all three arms.
- **Do not file the `sessionserver.go` collaborator doc-comment narrowing.** `slotReplacement` (:412-416) and the `SlotReplacement` option (:1658-1663) both say the counter increments "when the slot retry policy drains an unhealthy concurrent-mode pod", and after CODE-5 two of the three `accountSlotFailure` callers are not the slot retry policy. Real and verifiable, and it dies on the bar: a Go doc comment is none of the spec/docs/schemas/charts surfaces, the operator-facing descriptions stay true, and nothing an implementor builds changes. The absence of `sessionserver.go` from `## Files touched on application (non-spec)` is the same non-finding, not a second one.
- **Do not "reconcile" `pkg/adapter/session.go:133,:147,:157` with `:156` and `:163`.** The first three are the `releaseSessionSlot` pre-start branches; :156 is `s.Runtime.Start` and :163 is `s.noteRuntimeStarted`. Both citation sets are correct and they are different lines of the same function. Likewise `pkg/gateway/podlifecycle/podsession/slotbinder.go` has two near-identical connect sequences, `bindReservedSlot` at :230-254 (create-time-reserved) and the tail of `connectSlot` at :449-473 (fresh reservation); both use `slotFailureConnect` and the proposal cites each for different claims, so check which one a citation means before calling it drift.
- **Grepping spec prose for a quoted phrase fails SILENTLY when the phrase wraps across a source line.** "the adapter closes the session runtime" is at spec/29:697 but a one-line grep returns nothing, because "On the default disposition the" ends :696. Grep a short fragment that cannot straddle a wrap, or read the neighbourhood. Related: `ReleaseSlotReservation` is three-argument in the tree today and CODE-4 adds the fourth, so the shipped signature is not evidence that the proposal's four-argument call sites are wrong.
- **The whole-pod scrub is NOT a backstop on the failure path SPEC-3's new clause creates,** although the SPEC-3 rationale's "stays the backstop for anything a per-slot cleanup left behind" reads as though it were: a cleanup that does not complete leaves the slot `leaked`, `leaked` holds occupancy, and the whole-pod scrub fires at occupancy zero, so the backstop is unreachable in exactly the case that needs it. Not filed, because the same hole exists for every shipped leaked slot and before this proposal a failed bind left the credential file with no cleanup at all. A later round tempted by the tenant-isolation framing should stop here.
- **"Removes nothing" in SPEC-1's §4.7 no-op sentence looks like a fresh angle on the refuted "the no-op rule suppresses the whole-pod scrub" finding, and it is not.** The occupancy-zero recycle `Shutdown` both names a session the adapter holds no entry for and removes a great deal, but the refuter's ground (the row's untouched remainder and SPEC-1's §4.1 three-operation sentence both state the scrub gated on the disposition alone) covers the "removes nothing" conjunct identically. Close variant, barred.
- **`recycle.maxSessionsPerPod` and `maxPodUptimeSeconds` are both under `sessionPolicy.recycle`,** so the racing-start bullet's bound leaves a non-recycling concurrent pool apparently unbounded. The bullet UNDER-states: in the racing ordering the reclaim is acknowledged, the gateway releases with `leaked=false`, the Redis counter decrements, occupancy reaches zero and a non-recycling pod is retired by the claim DELETE at `remaining==0` (slotclaimer.go:881-885). Do not re-derive it as an unbounded residue.
- **Do NOT weaken summary.md's `ExcludePod(s)` sentences** (the 0080 §1.19 impacts row's placement clause and the CODE-5 line in the Summary's traps list). The queue-re-entry finding falsified them against the OLD single-valued design; the slice-plus-pointer fix makes them TRUE as written. Weakening both because both findings touched the same table cell would leave the proposal under-claiming a guarantee its own staged code provides.
- **Do NOT read the lagging-release summary row as a second copy of open decision 9, or as an obligation on CODE-2.** Decision 9's producer is the compensating `Shutdown` this proposal adds; the row's producer is the abandoned attempt's own shipped rollback with no compensation involved. Decision 9's "the exposure is created here rather than inherited" is scoped to the reclaim and was deliberately not edited. CODE-2 removes the fifth instance of the pattern without opening the four shipped ones, and reading the row as an obligation re-opens the class fix the round-5 design pass rejected.
- **Do NOT widen the 0080 §1.7 impacts row into §1.7's threshold or ledger question.** This proposal changes neither `slothealth.UnhealthyThreshold` nor `drainLedger.RecordLeak`, and its non-goals record that retuning `ceil(maxConcurrentSessions/2)` is out of scope. Adding "and the clamp still drains an exclusive pod" would restate a shipped-tree fact rather than an impact on 0080.
- **`TestValidTransitions_spec_6_2`'s doc comment says "exactly the six edges the spec enumerates"** (slotstate_test.go:10-11) and CODE-3's staged test change says nothing about it. It is a stale-comment nit, not a gate; the count lives in `len(want)`. Do not spend a finding on it.
- **Do NOT edit a ledger entry in place to correct it.** Ledger blocks are the historical record of what a round decided; the repository's mechanism for correcting one is a `CORRECTS` tag in a later round's shard, and the durable claim is corrected here in `## Standing context` instead. Two round-5 entries carrying the superseded "pointer helper costs no test edits" remedy were corrected exactly that way.
- **Do NOT justify the graceful-window split with `SocketRuntimeProcess.Close`'s SIGTERM wait.** In the shipped sidecar wiring `SpawnPath` is never set, so `cmd == nil` and `Close` returns `p.listener.Close()` with no wait at all. A round already filed and withdrew that dress. Justify with `MCPRuntime` (the §5.1 `type: mcp` runtime, which does read the plumbed deadline), the `terminate` frame's required `deadlineMs`, and the §11.4 precedent. EVIDENCE: `grep -rn "SpawnPath" cmd/ pkg/ --include=*.go` outside tests returns only the field declaration and its own doc.
- **MISTAKE: the finding that produced the split cited `InProcessRuntime.Close` blocking on `<-done` as evidence.** It is not evidence: that method's signature is `Close(_ context.Context, …)`, so it discards every deadline and no value of `deadlineMs` changes its behaviour. The same holds for `executor.SubprocessExecutor.Close`. A fixer who copies the finding's evidence list into the proposal lands two false claims. Cite `MCPRuntime` and `SocketRuntimeProcess` instead.
- **Do NOT "simplify" the split back to a single bound.** `contextWithGraceDeadline` returns the PARENT UNCHANGED for a non-positive grace, which is exactly what made the pre-split `deadlineMs: 0` set the close grace to the full remaining RPC budget and produced the confirmed "a completed reclaim can never be reported as cleanly reclaimed" defect. Collapsing the two bounds re-opens it.
- **Any change to the split must also touch the tier-1 per-stage assertion.** The per-stage compensation table asserts a positive `deadlineMs` equal to half the budget and strictly less than the RPC deadline the same pool configuration produces. Changing the call without changing that line leaves the staging self-contradictory, which is how the `deadlineMs: 0` form survived several rounds.
- **MISTAKE nearly filed, three dresses on the split.** (1) "`budget/2` at the 5s floor gives the adapter 2.5s, below §5.2's `minimum 5s enforced at runtime by the adapter`" dies on the reading: §5.2's minimum is on the SLOT CLEANUP timeout while `deadline_ms` bounds only `Runtime.Close`, the RPC deadline still carries the whole figure, and the adapter enforces no per-slot cleanup timeout at all. (2) "the reclaim's `terminate` frame carries a value no operator doc anticipates" dies because no spec or doc pins the value and every `deadlineMs` occurrence in `spec/` and `docs/` is the interrupt frame or an illustrative `10000`. (3) "`SocketRuntimeProcess.Close`'s doc comment names one gateway window while a second caller pins a pool-derived one" is a Go doc comment, killed by the standing collaborator-doc-comment trap.
- **MISTAKE nearly filed: "the residual headroom is the wrong half".** With `budget/2` pinned as the close grace, the adapter's remaining work (`emitFinalUsage`, the frame send, `removeSlotTree`'s four `os.RemoveAll`s, the response) has to fit in the other half, so the harm looks moved from the close to the tree removal. It dies twice over: before the split `Runtime.Close` could consume the WHOLE budget, so the split is strictly better; and on the main residue class, bound-but-unstarted, `started` is false, no close runs at all, and the whole budget goes to the tree removal.
- **MISTAKE nearly filed: "the split makes a slow runtime report unclean, inverting the defect it closed".** Neither runtime `Close` reports the SIGKILL pivot as an error, so `closeErr` is grace-independent and `ExitedCleanly` is unaffected by the halving. The classification for the slow band is identical before and after; what changes is that the fast band now reports a completed cleanup as completed.
- **MISTAKE nearly filed: "the rollback runs `Runtime.Close` on a cancelled inbound ctx, so the teardown is skipped".** Closed by the Settled entry on the three `Close` implementations and re-verified twice this window: a cancelled ctx makes the close harsher, never absent, and the active-set removal the rollback exists for always lands. Do not rebuild it.
- **MISTAKE nearly filed: "a successful retry's later release erases pod A's leak record".** `MarkReleased` has no production caller at all. The residual is real and cosmetic: a second leak for the same session on a different pod publishes the second pod's gauge with the first pod's count, and the drain threshold is keyed by pod and unaffected.
- **MISTAKE nearly filed: the phantom `SandboxClaim` and the stuck Sandbox finalizer.** "The `leaked` release leaves a non-terminal claim holding the Sandbox's session-cleanup finalizer, so the pod wedges in Terminating and trips `FinalizerStuck`." Two things kill it: orphan GC does not merely drain, it drains and then `Delete`s the claim, which releases the finalizer; and while a live co-tenant references the pod the finalizer is legitimately held, because `evaluate` gates on `PodHasActiveSession`. It is also pre-existing, since `leaked = err != nil || !cleanly` fires on every ordinary session end.
- **MISTAKE nearly filed: `DrainSandbox` from the new `resumeOnPod` caller as a gateway write into controller-owned state.** §4.6.3's Gateway ServiceAccount paragraph grants `get`/`patch` on agent Pods precisely for the `lenny.dev/drain-request` annotation, and `Binder.DrainSandbox`'s own doc comment records that routing as the §4.6.3-conformant form. A new caller of an already-blessed mechanism is not an idiom violation.
- **MISTAKE nearly filed twice: CODE-2's `Resume` rollback answering `Aborted` demotes the session terminally.** `isTransientPodClaimError` maps neither `Aborted` nor `FailedPrecondition` to transient, so the resume rollback's code looks load-bearing. It is inert: the rollback fires only after the gateway's own call has failed and issued the compensation, so the code goes to a caller that has already abandoned the RPC, and both candidate codes give the same classification there. The rationale's gloss ("for the classification reason the `StartSession` rollback records above") is inaccurate, because no §5.2 classifier runs on the resume path; the fix is one clause, never a code change.
- **MISTAKE nearly filed: "`Aborted` makes a start refused after a §11.4 revoke retryable, where `FailedPrecondition` would have been terminal".** In the SHIPPED tree `noteRuntimeStarted` has no return value and no rollback exists, so the racing start simply succeeds today. Aborted-plus-retry is strictly more conservative than what ships, and less strict than it could be is not a finding.
- **MISTAKE nearly filed: "CODE-1 widens `removeSlotTree` from `bound` to `removed`, so a lagging reclaim now deletes a SUCCESSOR's workspace and credential directory".** Real, and it is the exact hazard `ExcludePods` is staged for; the one path `ExcludePods` does not reach, a create-time-reserved `BindReservedSlot` retry returning to the same pod through `row.PodAssignment`, is already a recorded accepted failure mode.
- **MISTAKE nearly filed: "an empty session id collapses the path to the slots root and destroys every co-tenant's workspace".** Dies on `st.paths` being resolved from the map key at `ensureSlotStateLocked` rather than from `st.sessionID`, and on `ValidateSlotID` refusing an empty id before an entry can exist. This is the check worth doing on any future edit that moves the `bound` gate.
- **The honest security residual, and why it is not a finding.** A COMPROMISED adapter answering `exited_cleanly: true` on a reclaim it never ran now buys the Redis decrement, exemption from the persistent leak count, and exemption from `ExcludePods`. Each is more permissive than today only if today were stricter, and today is not: `ReleaseSlotReservation` hard-codes `leaked=false`, the reserved and resume paths reach no tracker, and no exclusion exists. A later round needs a path where the lie makes the gateway DO something it does not do today, rather than fail to do something new.
- **MISTAKE nearly filed: "the compensation blocks the client request goroutine for up to `cleanupTimeoutSeconds`".** Real and quantified (at the documented default of 60s a `maxConcurrentSessions: 2` pool gives a 30s block per failed bind, payable twice, and an exclusive pool reached through `Binder.Resume` degenerates to the full 60s), and it does not clear the bar: §16.5's creation SLO excludes the bind, and the startup P95's burn-rate expression divides by the startup histogram's count, which a sub-5% bind-failure rate cannot move.
- **MISTAKE nearly filed: "the third `accountSlotFailure` caller destroys a warm pod per failed §7.3 re-attach, where nothing is destroyed today".** It dies on the shipped baseline: a failed `Binder.Resume` already leaves the replacement pod's claim `bound` with no active session and §4.6.1 orphan GC drains it. The pod is lost either way, so this is a churn-timing change rather than net warm-pool consumption. Anyone re-deriving the Tier-3 correlated-resume-storm framing must answer this baseline first.
- **MISTAKE nearly filed: "the resume path's release runs on the dead caller context, so `Leaked` is true even when the reclaim succeeded".** The mechanism is real, and the caller's ctx being dead IS the design's own primary trigger. It dies on three grounds: the leak is HONEST, because the Redis increment genuinely was not released, so `leaked` is the correct disposition; the stranding is pre-existing at every release site; and the proposal states and tests the arm. What is left is "the release should be detached too", which is hardening on shipped code.
- **MISTAKE nearly filed: "§7.2 step 3's reclaim is ordered before a pod release that step 1's cancellation makes impossible".** Killed by the Settled fact that §7.2 steps 1 and 3 have no implementation on the DELETE path, so the deterministic-cancellation premise is not reachable in the tree and correcting step 3 creates no code obligation.
- **MISTAKE filed twice, and it is still standing: CODE-5's `slotBinder`-interface sentence is wrong.** "The `slotBinder` interface is unchanged … so every implementing type and both test fakes compile as they stand" (non-spec-changes.md:649-651) contradicts CODE-4's own call-site table five hundred lines earlier, which changes `ReleaseSlotReservation` on that very interface. The shipped declaration is the three-parameter form at start.go:2726 and both fakes implement it (slotretry_test.go:52, slotretry_load_test.go:33), so both stop compiling. `fakeSlotBinder.released` is `[][2]string` and cannot carry the `leaked=true` the tier-1 accounting case asserts either. The round that rewrote the neighbouring `req`-helper half of that sentence fixed the pointer clause and left the interface clause standing.
- **Do not file `non-spec-changes.md:442` ("No code under `pkg/`, `cmd/`, or `sdks/` reads the reason value").** It is literally self-contradictory two sentences after the same paragraph names `drainReason` as the reader, and three lenses have verified and declined it. `grep -rn "GetReason()" pkg/ cmd/ sdks/` settles it in one command: one non-test reader. The intended reading is "nothing branches on it beyond the normaliser", and a fixer touching that paragraph should narrow it to those words.
- **`recordingShutdownAdapter` records the request and NOTHING about the RPC context** (binder_test.go:1167), while the new tier-1 assertion pins `deadlineMs` "strictly less than the RPC deadline that same configuration produces". The fixture paragraph enumerates the fake's new capabilities and does not name capturing `ctx.Deadline()`. Three lenses declined it as test mechanics of the same class the material skeptic already refuted; a fixer already editing that paragraph should add the clause, and a reviewer should not spend a finding on it.
- **Do NOT file the reason-value or status-code tables in `docs/`.** `docs/reference/adapter-contract.md:210-216` gives per-value meanings for a `reason` set that already lists `"drain"`, which is not in the four-value enum, and the shipped §11.4 revoke already sends `"USER_REVOKED"` down the same normaliser; `docs/api/internal.md:490-500` is a gRPC status table carrying neither `ABORTED` nor `INVALID_ARGUMENT`, both of which the shipped adapter already returns; `docs/reference/adapter-contract.md:208` says "sends SIGTERM, then SIGKILL after 10 seconds" where `SocketRuntimeProcess.Close` SIGKILLs at the grace deadline with no SIGTERM. All three are pre-existing and untouched by this proposal.
- **`pkg/adapter/sessionscrubreporter.go:79-81` claims "the next release's idempotent re-report are the backstops" for a failed `ReportSessionScrub`.** That is false against `RecordSessionScrub`, which has no per-session dedup key. Pre-existing code comment in a file no deliverable opens; a later reliability lens will find it and should not spend a round on it.
- **A future edit that moves §6.2's scoped block BELOW the general header inverts both tier-11 slices silently,** because `scopedBlock` is `s62[scopedHeader:generalHeader]` and `generalBlock` runs to the end of the section. The current ordering (scoped at :146-148, general at :150-156) is what makes the `generalSlotEdges` addition satisfy the positive loop and the negative loop at once.
- **A future §5.2 gate must anchor BELOW SPEC-3's append.** The append writes the literal `**Slot cleanup:**` into the `**Scrub model.**` paragraph at spec/05:453, ninety lines above the real bullet at :545, so a `lineContaining(s52, "Slot cleanup")` gate would resolve to the append. No gate anchors on it today (`grep -rn "Slot cleanup" tests/ scripts/ pkg/ cmd/` returns only comment prose), which is the only reason it is safe.
- **MISTAKE that cost a whole design round: minting the bind epoch PER REGISTRY ENTRY.** The hand amendment did that and then concluded, in the create-time-reserved edge case and in the summary, that "neither the tree the retry staged nor the session it started can be taken down by the previous attempt's reclaim". With per-entry minting that conclusion is false in the one ordering the amendment exists for: `ensureSlotStateLocked` returns the existing entry, `claimSessionSlotUnderLock` gates only on `st.started`, so the successor inherits the predecessor's epoch and the fence compares EQUAL. Six lenses reached the same hole independently. The fix is per-admitted-attempt minting plus the caller echo; do not re-derive the per-entry form.
- **TRIED AND WITHDRAWN, five alternatives to the per-attempt epoch.** (a) Mint per entry and re-mint on the start claim with a latest-wins latch — a lost `StartSessionResponse` leaves the caller naming the pre-start epoch, so the real started-session residue is answered `superseded` and leaks silently. (b) Bump on every bind RPC with no request fields — same lost-response failure, and the lost response is the COMMON case, because the compensation exists precisely when the caller's context expired mid-RPC. (c) A gateway-minted attempt UUID — carries no order, so an earlier attempt's straggler is adopted back and fences the live successor out; the adapter's monotone counter is what supplies the ordering. (d) Refuse every epochless bind against an existing entry, needing no request fields — refuses the attempt's OWN second RPC, because `ensureSlotPaths` runs at the top of three staging handlers. (e) Accept the inheritance hole as a residue — it is a running session's process group killed.
- **MISTAKE: the §4.7 `Shutdown` row fenced the SLOT RELEASE alone and left the runtime teardown on its own precondition** ("a session whose start the adapter has admitted"), which a superseded successor satisfies. A conforming third-party adapter reading the row literally would flush usage, drain and close a live session's runtime on a mismatch. The staged Go was already right (CODE-1 returns before any teardown); the spec was the defect. The fence must be a precondition on the WHOLE request.
- **WATCHOUT: epoch rule 1 must exclude the cleanup interval, or it contradicts the reclaim hold.** During a cleanup the entry is DEREGISTERED, so "the adapter holds no entry" is true and an epochless bind would be admitted straight into the interval the hold exists to protect. Both staged blocks carry the "no cleanup for the identifier is running" clause; a reword that drops it re-opens the window.
- **WATCHOUT: do NOT make the superseded-attempt refusal permanent** to "stop the dead attempt retrying". The standing Trap on CODE-2's rollback records exactly that mistake: a permanent code turned a transient race into a non-retryable 422 and contradicted staged §7.1's "Nothing about the attempt's retryability changes". What stops a stray retry stealing a running session is the "epochless request against a started entry is refused" clause, not the error category.
- **WATCHOUT: "occupancy" deliberately SURVIVES in two senses and must not be swept to "hold".** The epoch's ownership sense ("the bind attempt that owns a slot identifier") and the pre-existing pod-level Redis slot-counter sense (spec-changes.md:51,:55,:137,:152; non-spec-changes.md:771,:778; summary.md's leaked-occupancy block) both stand. Only the adapter-local interval was renamed. A later round that mass-renames will re-merge the two mechanisms the rename just separated.
- **MISTAKE, the FIFTH occurrence of the unscoped `leaked` terminal, and it was the amendment's.** The new §5.2 hold paragraph closes with an unqualified "a cleanup that exceeds it leaves the slot `leaked`", one paragraph below the sentence pair that an earlier round split by concurrency to fix exactly this, and SPEC-4's :580 exclusivity sentence ("lasting until the cleanup reaches `released` or `leaked`") reintroduces the terminal claim G1 had deleted. Both filed. The pattern rule stands: ANY sentence appended to the concurrency-independent `**Scrub model.**` paragraph that names `leaked`, the trigger or the gauge needs its own scope, and the two sites live in different deliverables so a fixer treating them as one leaves one standing.
- **DELETED AND REPLACED, a MISTAKE that would have broken a correct staging: "`AssignCredentialsResponse` is not empty, so `bind_epoch = 1` is a hard field-number collision; the free number is 6".** It is FALSE. The message is `message AssignCredentialsResponse {}` and field 1 is free; the field set the old entry quoted is `RotateCredentialsRequest`'s. The artifact that produced it is reproducible and worth knowing: `awk '/^message AssignCredentialsResponse/,/^}/'` never terminates on a ONE-LINE message, so the range runs on and prints the NEXT message's body. Use `grep -n "message X"` then `sed -n` when checking a proto field number. Renumbering SCHEMA-1 to 6 on the strength of the old entry lands a defect; eight lenses filed the correction.
- **MISTAKE: spec-changes.md:178-181's reverse-ordering argument has the wrong subject.** It says "the START'S OWN claim re-creates the registry entry" and then fences it with "the epoch its own claim observed", which is the epoch that claim just minted, so the ordering it posits is not refused by the epoch and is past the hold by construction. summary.md:100-102 states the same case correctly ("a RACING claim re-creates the entry a start confirmation reads"). Filed; whoever fixes it must decide whether the bullet meant a SUCCESSOR's claim, in which case the abandoned-start-re-creates ordering is an unrecorded residue.
- **WATCHOUT: dropping the "which is why that refusal is absent from the **Non-retryable failure categories** list below" clause forces a matching edit** to the `## Spec sections deliberately untouched` bullet, which currently says the list is "named only to state that the reclaim-in-progress refusal is absent from the list". Leave the bullet saying the list is untouched and why, without the cross-reference.
- **The glob `*spec-changes.md` in this proposal directory matches BOTH `.spec-changes.md` and `.non-spec-changes.md`,** so `sed -n 'N,Mp' *spec-changes.md` concatenates them and every line number you read is wrong. Use the full filenames. This has bitten at least one round.
- **WATCHOUT: the §4.1 replacement justifies the epoch by analogy to a Kubernetes `DeleteOptions` precondition,** and the analogy is unsound on error semantics: a real precondition mismatch is a 409 Conflict, while a mismatched epoch here is a SUCCESSFUL `superseded` response. Judged below the bar because the sentence claims only the classification. A later lens will notice it; the reasoning that killed it is here so it is not re-litigated.
- **MISTAKE nearly filed: "SPEC-1 defines `reclaimed` as 'held the named entry and released the slot', so a cleanup that fails has no outcome value and the response reports `reclaimed` for a leaked slot".** §15.4 frames the enum as the epoch comparison's outcome and the leak discriminator stays `exited_cleanly`, so the drift is definitional at worst and a verifier reads "released the slot" as "performed the release".
- **MISTAKE nearly filed: "the new adapter-local hold is invisible in the pod's `/healthz` `leaked_slots` count and in the Redis occupancy §6.2:160 defines, so an operator sees free capacity on a pod that refuses binds".** Dies because the hold is per slot identifier and the identifier is the session identifier, so no other session can collide with it and no placement decision is affected.
- **MISTAKE nearly filed: "the epoch fence is unobservable, because the rollback's status code reaches a caller that has already given up".** It is observable on three routes: a §11.4 revoke racing a live `StartSession`, `Binder.Launch`'s exclusive `StartSession` at binder.go:1013 (the same adapter call site), and the staged tier-7a rendezvous. Do not file "the code is unobservable".
- **`pkg/adapter/session.go:163` is reached by BOTH the concurrent `materializeSlot` start stage and the exclusive `Binder.Launch`** (binder.go:1013, the `else if` arm), so CODE-2's rollback also fires on the exclusive path, where `failPhase` retires the pod. The proposal never claims otherwise and the outcome is fail-closed, but a reader who assumes "sdkwarm is the only exclusive site" misreads it; `sdkwarm.go:261` is the OTHER arm of the same if/else.
- **The "rollback destroys no successor" case discriminates on the UNBOUND arm only, and its closing sentence must say so.** In the bound sub-case the successor's `claimSessionSlot` sets `st.sessionID` under the same key, so CODE-2's confirmation succeeds and the rollback body never executes; a re-added `releaseSessionSlot` cannot run there. Only the unbound arm, where `ensureSlotStateLocked` leaves `st.sessionID` empty, turns red if a registry release is added back.
- **The "rolled-back start can close a successor's runtime session" failure mode has TWO open branches, not one.** Bounding it to "the branch on which `ExcludePods` does not fire" is refuted by the proposal's own create-time-reserved carve-out: `bindConcurrentSlot` routes a row with a `PodAssignment` through `BindReservedSlot` and never enters `runWithQueue` or `applySlotRetryPolicy`, so the §5.2 placement constraint does not reach a §15.1 retry onto a create-time-reserved slot at any disposition. The two branches are the policy-placed retry after an acknowledged reclaim with a clean release, and the create-time-reserved retry the policy never places.
- **MISTAKE nearly filed: "the compensation's residual headroom is the wrong half".** With `budget/2` pinned as the close grace the adapter's remaining work has to fit in the other half, so the harm looks moved from the close to the tree removal. It dies twice: before the split `Runtime.Close` could consume the WHOLE budget, so the split is strictly better; and on the main residue class, bound-but-unstarted, `started` is false, no close runs, and the whole budget goes to the tree removal.
- **TRIED AND WITHDRAWN, and this is the one that must survive: the PER-ATTEMPT bind epoch with a request-side echo.** Round 5 minted the epoch per admitted bind attempt, put an `int64 bind_epoch` on all seven bind-sequence REQUESTS, gave §4.7.1 two admission rules (an epochless request admitted only when no cleanup is running and the entry's session has not started, mints and reports a fresh epoch; an epoch-carrying request admitted only at that epoch), and re-scoped the caller echo to the session's later requests on the reporting connection so the §7.4 mid-session upload stayed admissible. Round 6 reverted the whole thing to per-entry minting with no request field. Do not re-derive the per-attempt form as a fix for the inheritance hazard without answering why r6 withdrew it; do not "fix" SCHEMA-1 back to sixteen fields.
- **Do NOT re-file the per-entry inheritance hole as a fresh finding.** Under per-entry minting a retry that reaches the pod before the lagging reclaim's critical section RESOLVES the abandoned attempt's surviving entry and inherits its epoch, so the reclaim compares EQUAL and can tear down the retried session. The proposal now OWNS that in two accepted-failure-mode bullets. What is still filable is a place where the surrounding prose claims the fence closes it.
- **MISTAKE, twice, the same shape: a design reversal leaves behind every sentence a grep on the new vocabulary misses.** The r6 revert rewrote three sites to entry vocabulary (§4.7 row, §7.1's "On superseded" clause, §4.7.1) and left §7.1's `superseded` TRIGGER sentence and the Design in ATTEMPT vocabulary, which diverge exactly on the shared-entry case; the same pass rewrote the "retry meets the hold" edge case to say a policy-placed retry never meets it while leaving §7.1 asserting that it does and spends a policy attempt. When judging a fix here, grep the staged NORMATIVE blocks for the claim, never only the narrative.
- **MISTAKE: the r5 fix that re-based the epoch rewrote the edge-case bullet and left the staged §7.1 paragraph and the Design narrative asserting the opposite.** Same class as the earlier "two rule-stating sites still give the exclusion trigger as an unacknowledged reclaim". A fix round that changes the narrative and leaves the normative text behind is this proposal's most repeated failure.
- **MISTAKE that cost a round: the r1 fix that closed a too-wide-predicate finding overshot into a false universal.** It replaced §4.7.1's wide epoch-reporting predicate with a closed seven-item list PLUS "Every other RPC on this contract neither carries nor reports a bind epoch", which excluded `Shutdown` three paragraphs above the rule requiring the caller to echo an epoch on a `Shutdown`. Five lenses filed it in one round. A conforming third-party adapter following the §15.4 copy would have ignored the fence and torn down a successor's session. When narrowing a predicate, check the narrowing against every mechanism the section already fences.
- **Do NOT file the wide "create or resolve a registry entry" predicate as over-reaching.** §5.2 and §15.4 both use it, and it literally reaches `Attach`, `Interrupt`, `SendMessage`, `Checkpoint`, `CoordinatorFence`, `ExportPaths`, `ReportUsage` and the three credential RPCs while CODE-6 refuses only inside `ensureSlotStateLocked`. It dies because the hold starts at the DEREGISTRATION, so during the hold no entry exists and the "or resolve" half is vacuous: those RPCs find nothing and answer their own not-found, and only the creating half is reachable. Four lenses built it; the refuter's ground is that "create or resolve" is this change's own vocabulary for the bind-sequence RPCs.
- **Do NOT file the §7.1 "spends one of the attempts the retry policy allows" pair against "§5.2 keeps policy-placed retries off that pod".** Four lenses read it as consequence-then-mitigation and declined; round 7 reworded it anyway to "an attempt that meets the hold is one the retry policy did not place". Read the current text before filing.
- **Do NOT file the reclaim hold's bound as unenforced, in any dress.** The published bound is descriptive: the adapter enforces no per-slot cleanup timeout, CODE-6 releases the hold on a plain `defer`, and `removeSlotTree` takes no context. Three rounds refuted it on materiality, and the round-7 fix then went ONE STEP PAST the refutation by declaring the removal deadline-free, which is the reading the refutation rejected and which collides with the `**Slot cleanup:**` bullet the same block says stands verbatim. That collision is the only live form; argue it from the bullet, not from the absence of enforcement.
- **Do NOT file spec/10:30's generation validation against the adapter.** spec/10:30 says pods validate `coordination_generation` on every gateway→pod RPC and the proto comment repeats it, while the adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`. Any staged sentence asserting the check restates published spec; the divergence is pre-existing and unowned. Six lenses reached it.
- **Do NOT file the §4.7 row's "precondition on the whole request" against the whole-pod scrub.** The row enumerates two consequences (slot release, runtime teardown) and leaves the recycle-disposition scrub unnamed, which reads as a granularity mismatch. It is unreachable: only a fenced compensation carries an epoch and it never sets the recycle disposition, and the occupancy-zero recycle is a separate epochless `ShutdownRecycle`. Five lenses; 2:1 against the outlier phrase.
- **Do NOT file "the §4.7 no-op sentence suppresses the occupancy-zero recycle scrub".** The sentence enumerates the two teardowns, the scrub is neither, the row's UNEDITED tail states the scrub unconditionally, and SPEC-1's §4.1 replacement keeps "runs the whole-pod scrub when the recycle disposition is set". Check the unedited tail before building this one.
- **Dead end: the `PrepareWorkspace` per-frame epoch mint.** Because the RPC is client-streaming and a bare `int64` is zero on later frames, an implementer applying the rules per MESSAGE would re-mint per chunk. It dies because the shipped adapter resolves the entry once from the first frame, the caller learns the epoch from the single response, and ownership lands on the last mint. Two lenses.
- **Dead end: "`ConfigureWorkspace` is documented idempotent while the admission rules refuse an epochless request onto a started entry".** It died even under the r5 design (within one attempt the caller echoed its epoch; across attempts `Binder.Launch`'s failure reclaims onto a fresh pod), and the r6 revert dissolves it entirely. Two lenses.
- **Dead end: "the `DemoteSDK` fallback bind meets the reclaim hold".** `releaseSessionSlot` runs synchronously inside `DemoteSDK`, so the hold it opens is over before the RPC answers; and §4.7:673 routes a FAILED `DemoteSDK` to pod failure and a replacement claim, so the fallback runs only after a successful return. Six lenses built and dropped it; the only surviving form is a third-party adapter that cleans up asynchronously.
- **The §5.2 reclaim-hold paragraph is ONE physical line carrying eleven independent normative sentences.** Read the whole line before editing any clause; a grep anchored on one sentence misses its neighbours. This is the same disease already recorded for §7.1's long line, and three findings in one round landed inside it.
- **MISTAKE nearly filed, the security family, five dresses in one round and four in another.** `superseded`/`absent` as pod self-reports exempting a slot from the leak accounting; a compromised adapter refusing every fenced teardown; the epoch as a guessable small integer; the exclusive-pod carve-out returning a recycling pod carrying credential residue; a late `StartSession` re-creating the entry and leaving an orphan holding live credentials. Each dies on the standing bar that the lie must make the gateway DO something it does not do today, on the pre-existing `leaked = err != nil || !cleanly` self-report, or on the cleanup having removed `CredentialsDir` and cancelled the §4.9 timers first.
- **MISTAKE nearly filed: "the hold refusal is accounted as an ordinary transient slot failure, so one refusal drains a healthy pod at `UnhealthyThreshold(2) == 1`".** The mechanism is real end to end through `accountSlotFailure` → `RecordFailure` → `DrainSandbox`. It dies because an ACKNOWLEDGED compensation finishes its cleanup and releases the hold before the gateway returns the error, so the client's retry meets the hold only in the client-timeout race, where the reclaim is usually unanswered and routes to `RecordLeak`/`ExcludePods` instead. Two rounds.
- **MISTAKE nearly filed: "§15.4's `Shutdown` epoch comparison is a TOCTOU".** An epochless successor can mint a fresh epoch between the compare and the deregistration. It dies on the staged wording: "performs the slot release and the runtime teardown only when the two are equal" is a condition on the PERFORMING, so an implementation whose entry changed epoch between compare and act is already non-conformant. Adding "atomically" is clarification.
- **Do NOT use the no-re-dial rule as the ground for the epoch-restart argument.** The rule does not carry the case: a `grpc.ClientConn` reconnects transparently, so the connection survives an adapter process restart. The ground is `RestartPolicy: Never` on the agent podspec. Two rounds recorded the wrong ground and the third caught it.
- **Do NOT "tighten" §4.7.1's two-connection clause into its strong reading.** "Both connections resolve one entry, so both observe one epoch" is true as a claim about the VALUE and false as a claim about both connections having observed it: on the exclusive path `Binder.Launch`'s fresh connection observes nothing when its only bind RPC fails, and its compensation is the unconditional form. The preceding clause already covers that case, and the exclusive Launch failure runs `failPhase` and retires the pod, so the unfenced form has no successor to destroy.
- **Do NOT file the started-session refusal gap inside this proposal.** spec/04:672's `StartSession` row states no refusal while `claimSessionSlotUnderLock` refuses a repeat start with `codes.Unavailable`, and two of this proposal's edge cases depend on that refusal surviving. It is a pre-existing spec gap 0081 neither causes nor repairs, and enumerating the refusals inside §5.2 or §15.4 would mint unreviewed normative surface. File it separately if anyone wants it.
- **Do NOT file a §15.4 completeness finding.** Three have been refuted on the ground that a semantically-stated requirement plus a correct first-party implementation is not a correctness defect: the missing wire spelling of an epochless `Shutdown` (since fixed anyway), the missing status code for the hold refusal, and the missing per-outcome response rule. A fourth lands on the same refutation.
- **Two sub-threshold citation drifts, recorded so nobody re-derives and then files them.** `spec-changes.md` cites spec/29:697 for step 12's "the adapter closes the session runtime", which is on :698; and it says §7.1's atomicity paragraph "closes by stating that the client never receives a `session_id`" when that sentence is mid-paragraph and the same document quotes the real closing sentence correctly elsewhere. Neither changes the argument it supports.
- **MISTAKE that cost SIX groups and produced no edit: findings generated against a stale snapshot.** All six `spec.1.fix-*` groups were handed findings written against the hand-amendment commit 1344047d4 while the tree stood at 2aca9851b, roughly eighty lines later. Three resolved to NO EDIT because the quoted text no longer exists anywhere (`grep -n "tables above"`, `"bounds the occupancy"`, `"Slot-identifier occupancy"`, `"On either incoming edge"` all return nothing outside the review logs), one was a no-op on a bullet already deleted, and two verifiers confirmed a finding against line numbers eighty lines stale before anyone checked the string. The rule: verify a finding's quoted STRING against the current file before designing a fix, and never trust its line number.
- **MISTAKE, the SIXTH filing of the unscoped `leaked` terminal and the SECOND against text already repaired.** Occurrences four and five are recorded above; the sixth was filed by two `spec.1` groups against spec-changes.md:522 and :580, both of which had been rewritten in the interval. The pattern rule still holds and the pair of sites is what is stale. Every remaining `leaked` mention in the staged spec (:51, :390, :600, :624, and the two accepted-failure bullets) has been swept and is concurrency-scoped; the `**Slot-identifier reclaim hold.**` paragraph deliberately names no `leaked` terminal at all.
- **MISTAKE: SPEC-5 round 1 described a field set the proto does not have.** It asserted the epoch and `coordination_generation` "travel on the same messages", on the section that is the normative gateway-adapter contract. Only `ResumeRequest` and `ShutdownRequest` carry both. CORRECTED AGAIN by the r6 revert: with no bind-sequence request carrying an epoch, `ShutdownRequest` is the only message carrying both, and the live form is "Where both appear on one message, as on `Shutdown`, each is checked on its own terms". Do not restore `Resume` to that sentence; the r5-era correction this entry recorded is the stale one.
- **MISTAKE: the round-1 fix that narrowed a too-wide predicate overshot into a false universal, and five lenses filed it in one round.** It replaced §4.7.1's wide epoch-reporting predicate with a closed seven-item list PLUS "Every other RPC on this contract neither carries nor reports a bind epoch", which excluded `Shutdown` three paragraphs above the rule requiring the caller to echo an epoch on a `Shutdown`. A conforming third-party adapter following the §15.4 copy would have ignored the fence and torn down a successor's session. When narrowing a predicate, check the narrowing against every mechanism the section already fences.
- **MISTAKE: the two-start-points defect was fixed at §5.2 and §15.4 and reintroduced at the §6.2 site.** An earlier round converged §5.2 and §15.4 on the deregistration and did not sweep §6.2, whose sentence keyed the hold on the DURATION of `slot_cleanup`, a state entered a whole gateway round trip earlier. The §6.2 sentence looked like a harmless restatement and its own rationale said it minted no edge, so a reader who checks only that passes over it. The defect is the quantifier, not the edge.
- **MISTAKE: the r7 fix went one step PAST the round-6 refutation.** The refuted finding said the hold's stated bound did not cover the tree removal; the refutation's ground was that the cleanup ends at or before the per-slot timeout. The fixer answered by declaring the removal deadline-free, which is the reading the refutation rejected, and it now collides with the `**Slot cleanup:**` bullet the same block says stands verbatim. That collision is the only live form of the family; argue it from the bullet, never from the absence of enforcement.
- **Do NOT re-run the anchor sweep by hand; it is a ten-line script and it has come back clean in every round of this window.** Extract the fenced blocks after `## Staged edits` and `str.count` each across `spec/*.md`: blocks 0,2,5,8,10,12,14,16,18,20,22,24 give count 1 and every replacement or insertion gives 0. At least ten independent lenses reproduced it with identical results.
- **spec/06 carries TWO near-identical per-slot sub-state heading lines inside one fence,** :146 "Per-slot sub-states scoped to concurrent occupancy (tracked per session):" and :150 "Per-slot sub-states (tracked per session, not as pod-level phase; a pod of either concurrency):". SPEC-4 quotes :150 verbatim so it is unambiguous, but a paraphrased anchor here would not be.
- **Dead end, six lenses: "the `DemoteSDK` fallback bind meets the reclaim hold".** `releaseSessionSlot` runs synchronously inside `DemoteSDK`, so the hold it opens is over before the RPC answers, and §4.7:673 routes a FAILED `DemoteSDK` to pod failure and a replacement claim, so the fallback runs only after a successful return. The only surviving form is a third-party adapter that cleans up asynchronously.
- **Dead end, two lenses: "`ConfigureWorkspace` is documented idempotent while the admission rules refuse an epochless request onto a started entry".** It died even under the r5 per-attempt design (within one attempt the caller echoed its epoch; across attempts `Binder.Launch`'s failure reclaims onto a fresh pod), and the r6 revert dissolves it entirely, because no bind-sequence request carries an epoch.
- **Dead end, two lenses: the `PrepareWorkspace` per-frame epoch mint.** Because the RPC is client-streaming and a bare `int64` is zero on later frames, an implementer applying the rules per MESSAGE would re-mint per chunk. It dies because the shipped adapter resolves the entry once from the first frame, the caller learns the epoch from the single response, and ownership lands on the last mint.
- **Do NOT file the §4.7 row's "precondition on the whole request" against the whole-pod scrub, in any of its dresses.** The row enumerates two consequences and leaves the recycle-disposition scrub unnamed, which reads as a granularity mismatch. It is unreachable: only a fenced compensation carries an epoch and it never sets the recycle disposition, the occupancy-zero recycle is a separate epochless RPC, and SPEC-1's §4.1 replacement keeps "runs the whole-pod scrub when the recycle disposition is set". Five lenses; 2:1 against the outlier phrase.
- **Do NOT "tighten" §4.7.1's two-connection clause into its strong reading.** "Both connections resolve one entry, so both observe one epoch" is true as a claim about the VALUE and false as a claim about both connections having observed it: on the exclusive path `Binder.Launch`'s fresh connection observes nothing when its only bind RPC fails, and its compensation is the unconditional form. The preceding clause already covers that case, and the exclusive Launch failure runs `failPhase` and retires the pod, so the unfenced form has no successor to destroy.
- **Do NOT file spec/10:30's generation validation against the adapter, and do NOT file §4.7.1's restatement of it.** spec/10:30 says pods validate the generation on every gateway→pod RPC and the proto comment repeats it, while the adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`. Any staged sentence asserting the check restates published spec; the divergence is pre-existing and unowned, and the `f1` firing recorded it as a shipped-defect row. Six lenses reached it.
- **Do NOT file a §15.4 completeness finding.** Four have now been refuted on the ground that a semantically-stated requirement plus a correct first-party implementation is not a correctness defect: the missing wire spelling of an epochless `Shutdown` (fixed anyway), the missing status code for the hold refusal, the missing per-outcome response rule, and the zero-frame `PrepareWorkspace` response. A fifth lands on the same refutation.
- **Do NOT file the `concurrent_slots_exhausted` gloss against the placement exclusion, again.** spec/05:549 defines the reason as "pods exist but all slots are full" while the exclusion makes a not-full pod produce it, but `ClaimSlot` already returns `ErrNoConcurrentSlot` for every non-tenant-mismatch candidate it skips, and `ErrTenantMismatch` maps to the same reason. Six lenses across the window have now built and dropped this.
- **MISTAKE nearly filed, the security family, five dresses in one round and four in another and five again in a third.** `superseded`/`absent` as pod self-reports exempting a slot from the leak accounting; a compromised adapter refusing every fenced teardown; the epoch as a guessable small integer; the exclusive-pod carve-out returning a recycling pod carrying credential residue; a late `StartSession` re-creating the entry and leaving an orphan holding live credentials; the withheld report relaxing the `maxSessionsPerPod` residual bound; the epoch as a fail-open default because zero is permissive. Each dies on the standing bar that the lie must make the gateway DO something it does not do today, on the pre-existing `leaked = err != nil || !cleanly` self-report, or on the cleanup having removed `CredentialsDir` and cancelled the §4.9 timers first.
- **MISTAKE nearly filed, four capacity dresses in one round and four reliability dresses in another.** The hold refusal counted against the pod's unhealthy threshold; the §10.1 fence making the compensation unsendable after a handoff; the pre-`running` leak losing its occupancy on a Redis reset; the hold's accounting sentence appended to the concurrency-independent scrub-model paragraph; the hold refusing a §5.2-placed retry during the adapter's own cleanup; §5.2's Fresh workspace guarantee falsified by the hold; a replica lost mid-bind refusing an identifier for the pod's life. Each is either recorded as an accepted failure mode in the proposal's own edge cases, or is the pre-existing baseline, or is a barred close variant.
- **Do NOT conclude "nothing changed" from an empty diff against the snapshot the brief names.** In every round of this window `spec-rN` and `spec-rN-start` were byte-identical to the live proposal, because the snapshot is taken at the round's own start. Run `diff -rq` over the WHOLE `scratchpad/cp-snap/0081` directory first and diff against the newest snapshot that actually differs. Worse, that snapshot is sometimes two rounds back: at round 8 the newest differing snapshot was `spec-r6`, and the six real hunks were written in round 7's FIX stage after round 7's own lenses had run, so a round-7 shard saying "there is none" was true when written and misleading afterwards.

- **MISTAKE that nine lenses over seven sweeps repeated: checking proto field-number FREENESS and stopping there.** Every one of them verified that SCHEMA-1's nine numbers are unused, and none of them looked for a test that pins the field SET. `TestShutdownMessagePostRemovalDescriptor_spec_4_1` turns red on exactly those additive fields, and `buf breaking` is silent on an additive edit, which is the gate a reviewer reaches for and the wrong one. When a proposal adds a proto field, grep `tests/` for `Fields().Len()` and `assertFieldSet` over the messages it opens, not only the proto for the number.
- **MISTAKE, reproduced by at least eight lenses in one window: the `awk` range over a one-line proto message.** `awk '/^message X \{/,/^\}/'` and `sed -n "N,/^}/p"` both run straight past `message AssignCredentialsResponse {}` into the NEXT message and print `RotateCredentialsRequest`'s field set, which is how the retired "field 1 collides with `session_id = 1`" trap was manufactured. `grep -n "^message AssignCredentialsResponse "` with a trailing space also MISSES the line. Use `grep -n "^message X\b"` then `sed -n`, or a brace-balanced parse. Renumbering SCHEMA-1 on the strength of the old form lands a defect.
- **Do NOT hand-edit `tests/claim-map.json`.** It is generator output under a tier-0 byte-identity gate; a hand-added row turns tier 0 red on landing and the next seeding run drops it silently. The authoring site is the `EXPLICIT` list in `scripts/seed-claim-register.py`, and the generator must be re-run with `--out tests/claim-map.json` in the SAME commit. Every row in `EXPLICIT` carries a `note`; the validator does not require one, so that is convention rather than a gate.
- **MISTAKE in a finding's own `suggested_fix`: `errors.Is` against an adapter-local sentinel, from the gateway.** `errSlotReclaimInProgress` and `isSlotReclaimInProgress` are unexported and in `pkg/adapter`, which NO production file under `pkg/gateway` imports (only component tests do), and sentinel identity does not survive gRPC. A fixer taking that suggestion literally writes code that cannot compile, or worse a message-string match. The status code is the only signal that crosses the boundary.
- **Do NOT open an `Aborted` case in `SlotBindError.Reason()`** to close the resume classification gap. CODE-2's rollback relies on the same transient default, so an `Aborted` case there would make both the rollback and the hold refusal non-transient on the concurrent-bind path. The arm belongs in `isTransientPodClaimError`, which is a different classifier on a different path.
- **Do NOT put a test for `holdOrFailOnResumeError` or `isTransientPodClaimError` in `start_test.go`.** That file is `package sessionserver_test` and cannot see either unexported function. Two findings named it. The home is `resume_setup_demotion_internal_test.go`.
- **Do NOT delete the `ReportSessionScrub` mention from the `adapter-contract.md` `Shutdown` row.** `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` requires the row to CONTAIN "end-of-session teardown", "recycle disposition", "ReportSessionScrub" and "ReportPodScrub". DOCS-2 must QUALIFY the mention rather than remove it, and must keep the row on ONE physical line, because the gate reads it with `lineContaining(page, "| `Shutdown` |")`. The same four substrings are why the stale row ships green today: no gate catches its absence or a wrong fix.
- **DOCS-2's prose may not cite spec section numbers.** `.claude/rules/doc-content.md` bars `§4.7.1`/`§15.4` in reader-facing docs. The page already violates this once at :393; that is not licence.
- **Do NOT file `adapter-contract.md:64` (the `DemoteSDK` row) as its own finding.** §4.7.1's caller rule makes the demotion the act that drops the caller's epoch, which leaves a one-line RPC summary INCOMPLETE rather than false, and the row is a verbatim mirror of spec/04:674, which this proposal does not edit. Three lenses declined it on that ground. The :75 fix should still pick it up.
- **The checklist renumber left stale prose step references, and they are not findings.** `non-spec-changes.md:1668` still says "Between S4 and S6 the unedited file still passes" where SPEC-4 is now S5. Substantively harmless; a fixer already in that paragraph should correct it. Do not read a step number in non-spec prose as evidence of the current sequence.
- **MISTAKE nearly filed: S9's `Depends on: S1, S8` does not close over S4,** although CODE-6 implements SPEC-3's reclaim-hold paragraph, which lands at S4. Held back because S4 precedes S9 in the one stated execution sequence and §15.4's own hold block (SPEC-5, S1, which IS named) carries the wire-observable requirement CODE-6 implements. A round that wants this must argue from a pipeline that dispatches by `Depends on` rather than by listed order.
- **Do NOT re-file the wide "create or resolve a registry entry" predicate, in its security dress either.** The dresses tried this window: `RevokeCredentials` refused mid-cleanup with `ABORTED` so a mandatory §4.9 revocation is refused; `Attach`/`Interrupt`/`ReportUsage` answering `FailedPrecondition` during a hold where §15.4 calls a permanent status non-conformant; the §11.4 revoke `Shutdown` answered `absent`. All die on the same two grounds: during the hold no entry exists so the resolve half is vacuous, and `removeSlotTree` has already taken `CredentialsDir` while `deregisterSlotLocked` has already cancelled every armed timer, so a revoke has nothing left to revoke. Reopening it means attacking the "create or resolve is this change's own bind-sequence vocabulary" scoping directly.
- **Do NOT file CODE-1's epoch early-return skipping clause three (the whole-pod recycle scrub).** The shipped handler runs clause three unconditionally after clause two (session.go:283-291) and the `ABSENT`/`SUPERSEDED` returns really do skip it, but only a fenced compensation carries a non-zero epoch and it never sets the recycle disposition; the two production senders of that disposition are `ShutdownRecycle` call sites and CODE-6 keeps `ShutdownRecycle` on a zero epoch. Four lenses built it. A future proposal that gives a fenced request a recycle disposition must revisit it.
- **Do NOT file `compensateFailedSlotBind`'s `default: return false` as a fail-open.** `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` plus `exited_cleanly: false` maps to not-leaked, where today's `err != nil || !cleanly` maps it to leaked, so it reads as inverting `slotbinder.go:533-543`'s stated fail-closed posture. It dies twice: CODE-1's handler sets an outcome on every return path so UNSPECIFIED is unreachable for the first-party adapter, and staged §7.1 enumerates exactly two incomplete-reclaim conditions so the default arm is spec-conformant. The only reacher is a hypothetical third-party adapter, a framing the material skeptic has refuted three times. A later round needs a first-party reachability argument.
- **The caller-latch clear diverges between the lanes and it is INERT.** §4.7.1 says a caller holds no epoch once it has issued any RPC that removes the entry, naming `DemoteSDK` and "a `Shutdown` answering `reclaimed`", while CODE-6 clears only on `DemoteSDK` and on `ShutdownReclaim` answering `RECLAIMED`; the plain `Client.Shutdown` and `ShutdownRecycle` keep their signatures, read no outcome and clear nothing. Five lenses reached it and none filed. The direction is fail-open, and it is unreachable because no gateway connection carries a bind sequence after an ordinary `Shutdown`: `BindResult.Adapter` is per attempt and is closed at session end (`defer result.Adapter.Close()`, slotbinder.go:544). Reopening it needs a path where a plain `Shutdown` is followed by a bind-sequence RPC or a compensation on the SAME connection.
- **MISTAKE nearly filed, the security family again, five more dresses in one window.** A lagging `AssignCredentials` re-creating the entry and writing `credentials.json` plus §4.9 timers for an abandoned session (same route as the recorded `StartSession` variant, killed by CODE-4's unconditional `releaseCredentials` and by the occupancy-zero scrub sweeping `/run/lenny/slots/`); the epoch fence stranding credential residue (every adapter deregistration path reaches a `removeSlotTree`, so `superseded` and `absent` both entail the predecessor's tree, credential directory and timers are already gone); `emitFinalUsage` narrowing from `bound` to `started` leaking a delegation-tree budget (best-effort with a stream-close fallback §8.3 tolerates, and no bound-but-unstarted session reaches a `Shutdown` in the shipped tree); the pod-self-reported `leaked` bound as a regression (the adapter's power to force "not leaked" is IDENTICAL on both sides of the change — before, `exited_cleanly`; after, `slot_reclaim` plus `exited_cleanly`); and the §4.7 no-op sentence as a mandatory-credential-purge bypass (killed by the row's unedited tail).
- **Do NOT file the resume-path latency family; it is DISCLOSED rather than open.** On an exclusive pool `Binder.Resume`'s compensation runs with `slotCleanupBudget` degenerating to the whole pool `cleanupTimeoutSeconds` (default 60, documented "Must be `> 0`" with no upper bound) inside the client's request. The proposal names and prices it, and the standing five-dresses trap already declined the family because §16.5 samples successful creates only. A future round must argue from a STATED budget it breaches, never from the wall clock.
- **The "MISTAKE filed twice and still standing" CODE-5 `slotBinder`-interface sentence was filed a THIRD time this window and is still standing.** `non-spec-changes.md:845-847` says the interface is unchanged so both fakes compile as they stand, while CODE-4's own call-site table five hundred lines earlier changes `ReleaseSlotReservation` on that very interface. Verified again against the tree: start.go:2726-2728 declares the three-parameter form, both fakes implement it, and `fakeSlotBinder.released` is `[][2]string` and cannot carry the `leaked=true` the tier-1 accounting case asserts. Three filings, no fix.
- **The snapshot trap has a fourth and a fifth form.** Fourth: this window's `spec-rN` directories REUSE the names of the previous window's, so `spec-r3` … `spec-r9` under `scratchpad/cp-snap/0081.../` belong to a different document generation and ordering the listing by NAME is misleading — and mtime order is misleading too, because `spec-r4` sorts newer than `spec-r9`. Fifth: an empty diff can mean "the previous round produced no fix" rather than "you diffed the wrong snapshot", which is only distinguishable by `diff -rq` across the whole chain. In several rounds the newest differing snapshot was a `-prefix` directory, because the fix stage runs between the prefix snapshot and the next round's. Use `git diff` on the proposal file when the snapshots are ambiguous.
- **Do NOT read `spec/06:160`'s attribution of `lenny_adapter_leaked_slots` to the adapter as a premise.** The gauge is GATEWAY-registered and gateway-emitted (gatewaymetrics_credential.go:219-223) despite the name and despite the spec sentence. Five more lenses built a finding on "the adapter cannot know what the gateway observed" this window; the premise does not exist.
- **`bash` output in this harness truncates at roughly 2KB for large reads,** so the standing context has to be read by writing a `fold`ed copy to /tmp and reading it with offset and limit. Budget for that before planning a round.

### Open

- **CODE-1 comment phrasing** — UNVERIFIED: non-spec-changes.md:72-77 still carries "the runtime teardown runs for a session the pod's shared runtime process has been given" for a gate that reads `st.started`; confirm G2 landed the rewrite. See `spec.1.fix-G1.1`.
- **Tier-4 fixture extension** — OPEN: the staged tier-4 datastore-crossing case needs a second placeable Sandbox, a `MaxConcurrentSessions > 1` pool and a wired session server, none of which `recycle_scrub_path_test.go` has, and extending `recycleCluster` changes a fixture seven cases share. Decide between extending it and splitting the retry-re-binds half down to tier 1. See `non-spec-recheck.1.review-applicability.1` and `non-spec-recheck.1.review-feasibility.1`.
- **Socket runtime on a recycling pool** — OPEN: after any last `Runtime.Close` the listener is gone, so the next session's `Runtime.Start` accepts on a closed listener. Either socket runtimes never land on recycling pools or the recycle path is broken for them. Pre-existing; worth its own finding. See `spec.1.fix-design-G2.1`.
- **§6.2 fence versus §6.2 prose on `leaked` at concurrency 1** — OPEN (filed as a separate finding, not for this proposal): the fence scopes `slot_cleanup ──→ leaked` to concurrent occupancy while §6.2's own leaked-semantics paragraph, §5.2's `ceil(maxConcurrentSessions/2)` threshold at 1, and `slothealth.Tracker` all apply `leaked` at concurrency 1. See `spec.1.fix-design-G2.1`.
- **Excluded-pod exhaustion names the wrong cause** — OPEN: when the excluded pod is the only candidate the retry surfaces as `WARM_POOL_EXHAUSTED` / `concurrent_slots_exhausted`, honest but misnamed. Deliberately not minting a new `details.reason`. See `spec.1.fix-design-G3.1`.
- **`terminate` frame reason value** — OPEN, re-scoped: under the design as it now stands a bound-but-unstarted reclaim runs no runtime teardown, no close and no frame, because §4.7 puts the §15.4.2 signal inside the teardown and CODE-1 moves the drain inside the `started` block. The `reason`-enum question survives only for a reclaim of a session whose start WAS admitted, where `drainReason` maps `"slot_bind_failed"` to `session_complete`. The wire stays valid; nobody has decided whether that is the intended value. Re-scope the item before spending a round on the enum. See `spec.7.review-operational.1`.
- **Mid-start `Shutdown` bricks the pod silently** — UNVERIFIED, and narrower than it reads: `Close(sessionID)` for a session not in `p.active` still takes the last-close teardown whenever `p.active` is empty and `p.connected` is true, closing the listener bound once at adapter start. Reaching `connected && active empty` needs the `Interrupt` path, which closes the conn without clearing `connected`. Pre-existing, reachable only after an Interrupt of the last active session, and the remedy is code-lane. See `spec.1.review-docs-alignment.1` and `spec.2.review-reliability.2`.
- **"Does not acknowledge" versus an unclean answer** — UNVERIFIED: the staged §7.1 predicate is "a reclaim the adapter does not acknowledge", while the gateway's shipped predicate is `err != nil || !cleanly` and CODE-1 deliberately makes `exited_cleanly` false for a not-started reclaim whose tree removal failed. The §7.1 sentence may want "does not acknowledge, or answers that the release did not complete". See `spec.1.review-feasibility.1` and `spec.1.review-kubernetes.1`.
- **§4.1's retired vocabulary** — OPEN: SPEC-1's §4.1 replacement retires "per-session teardown" while the retained second sentence still says "The per-slot teardown and the whole-pod teardown are the same operation on the same address". After the split there are three named operations and the retained sentence names two the new sentence does not define. See `spec.1.review-fresh.1`.
- **`receiving_uploads` on an upload-free plan** — UNVERIFIED: if the §6.2 edge label "workspace materialization begins for this slot" is read as `PrepareWorkspace` rather than as entering the staging stage, a reclaim on the upload-free branch has no legal edge out of `slot_assigned` and SPEC-4 does not cover it. The charitable reading was assumed. UNVERIFIED and disputed: `spec-recheck.3.review-reliability.1` reads SPEC-4's own prose ("Every earlier stage of the §4.7.9 step-5 bind sequence ... leave the slot in `receiving_uploads`") as asserting the charitable reading outright and therefore closing the item, while the later `f2.open-decisions.out-of-scope-defects.slot-assigned-terminal` records it as still unsettled and rests its scope call on the charitable reading. Neither corrects the other. See `spec.1.review-kubernetes.1`.
- **§5.2's bullet at concurrency 1** — UNVERIFIED: whether §5.2's `**Slot failure and cleanup**` bullet is meant to govern the slot release on a `maxConcurrentSessions: 1` pod at all, given the staged §4.7 row defines the slot release as "the §5.2 slot cleanup" with no concurrency scoping. `spec.1.review-feasibility.1` argues it does not over-reach; `spec.1.review-performance.1` still wants it decided.
- **spec/05 versus spec/06 on failed-session pod retirement** — OPEN as a pre-existing wording tension, CLOSED for this proposal's sentence: spec/05:455 and :492 say a session that ends in failure or a crash always retires its pod, while spec/06:283 says the pod "is marked `failed` and released back to the pool (or terminated if unhealthy)". The §6.2 fence resolves it in favour of retirement, which is what makes the staged §7.1 citation hold. Predates this proposal, and a fix elsewhere should not deepen it. See `spec.1.review-security.1` and `spec.2.review-operational.1`.
- **`terminate` frame `deadlineMs`** — UNVERIFIED, pre-existing and code-lane, now NARROWED: the frame requires `deadlineMs` with `minimum: 100`, `RuntimeOps.Terminate` serialises it `omitempty`, and the two shipped gateway callers (`slotbinder.go:542`, `binder.go:2043`) still pass 0, so a Full-level runtime receives a frame missing a required field at an ordinary session end. The `budget/2` split answers it FOR THE COMPENSATION'S CALL SITE ONLY, which becomes the one `Shutdown` in the tree that satisfies the minimum; the item stands everywhere else. Whoever owns the tier-3 runtime-ops contract test should confirm. See `spec.2.review-client-surface.1` and `non-spec-recheck-2.2.fix-design-G1.1`.
- **`/sessions/` and `/artifacts/` in the widened action list** — UNVERIFIED: `RemoveTree` removes four trees and SPEC-3's widened §5.2 list names two of them plus the timers. The omission predates the proposal, so it was judged incompleteness rather than a defect, but a completeness lens may disagree. See `spec.2.review-citations.1`.
- **"incomplete enumeration" under §29's preamble rule** — OPEN: §29 item 12 gains no fourth `Shutdown` trigger from SPEC-2, and the preamble subordinates a trace only where it DISAGREES. Somebody should decide once whether an incomplete enumeration counts as disagreement and record the answer, because the question returns every round. See `spec.2.review-citations.1`.
- **Concurrent resume leaves a freshly claimed pod at occupancy 1** — OPEN, and the earlier narrowing was CORRECTED: the hold is bounded by §4.6.1 orphan GC at `claimOrphanTimeout`, not by the §5.2 replacement trigger. At `maxConcurrentSessions >= 3` the trigger never fires, because `UnhealthyThreshold` is `(maxConcurrent+1)/2`, `slothealth.Tracker` counts per pod, and each §7.3 retry claims a DIFFERENT fresh idle pod, so no replacement pod accumulates two leaks. `PodHasActiveSession` is false on a freshly claimed replacement pod (`bumpRecoveryGeneration` runs only on success, so `row.PodAssignment` still names the OLD pod), which is what makes predicate 1 fire. The proposal itself is not wrong here; its churn bullet scopes the drain claim to `maxConcurrentSessions: 2`. Whether `ClaimSlot` pass 1 treats such a pod as slot-bearing is still unverified. See `spec.2.review-performance.1` and `non-spec.2.review-kubernetes.1`.
- **Does CODE-4 owe a pod release on the resume path?** — UNVERIFIED: `Binder.Resume`'s error branch does `cl.Close()` plus a no-op `releaseResumeSlot`, so §7.1's "the failed attempt releases the pod's claim and the pod retires" is discharged on the §7.3 re-attach only by §6.2's general policy and never by a staged code path. Judged pre-existing and left unfiled; a code-lane reviewer should decide. See `spec.2.review-feasibility.1` and `spec.2.review-reliability.2`.
- **The create-time-reserved retry's exposure has no bound** — OPEN: a lagging reclaim can tear down a running retried session, where §5.2-placed retries are bounded by the placement constraint. The human question already asked about the recorded residue should be re-put with the corrected text in hand, because the honest answer is strictly worse than the version the question was asked about. Any closure must place the retried attempt off the reclaiming pod without `applySlotRetryPolicy` and without a durable per-row exclusion, or persist the exclusion on the row. See `spec.2.fix-G1.1` and `spec.3.fix-design-G2.1`.
- **Does §7.1's exclusive-pod clause need a mid-resume carve-out?** — OPEN now that §7.2 routes that path through §7.1. Both readings survive the evidence, so only a human can adjudicate. See `spec.3.review-feasibility.1`.
- **Whose view of "running" does §7.1 mean?** — OPEN: under a pod-state reading the obligation has ended before §7.2 step 3's reclaim is owed; under an attempt-completion reading it still holds. Round 4 narrowed the upper bound to "ends when that attempt succeeds", which favours the second reading without saying so. See `spec.3.review-fresh.1`.
- **§7.1's trigger noun-phrase** — OPEN: §7.1 says "A gateway bind attempt that fails" while SPEC-3 and SPEC-4 say "abandoned or fails" and §7.2/§6.2 route a client-CANCELLED re-attach through the same obligation. The window sentence probably carries the abandon case. Note that "abandoned" in this proposal means the GATEWAY abandoned the bind by failing it; client abandonment is named out of scope in the problem statement. See `spec.3.review-mechanism.1` and `spec.7.review-reliability.1`.
- **Does §7.3 owe a release step at all?** — UNVERIFIED: the §7.3 appended sentence orders the reclaim "before the replacement pod is released", but §7.3's numbered flow states no replacement-pod release on any branch, and §6.2's three non-terminal `resuming` failure bullets state no disposition for the half-claimed pod either. Only §7.2 step 3 releases it. Four lenses judged it prose imprecision. See `spec.3.review-edit-sites.1` and `spec.5.review-client-surface.5`.
- **Do the other three `resuming` failure bullets need the reclaim clause?** — UNVERIFIED: pod crash, the 300s watchdog and non-retryable errors state no pod release today, so there is nothing for the clause to attach to, but §6.2 calls itself the authoritative enumeration of every edge out of `resuming`. Decide once. See `spec.7.review-mechanism.1`.
- **Transient over-assignment on the acknowledged racing-start ordering** — UNVERIFIED: the gateway releases the reservation with `leaked=false`, decrementing the counter for a slot a live session still occupies, so the pod can transiently carry `maxConcurrentSessions + 1` sessions, which is what §5.2's "Slot assignment atomicity" exists to prevent. The racing-start bullet records the residue but not this consequence. See `spec.5.review-reliability.1`.
- **§6.2:160's two claims about the leak count** — UNVERIFIED: it says the persistent count is the `lenny_adapter_leaked_slots` gauge "equivalently the leaked portion of the pod's Redis slot-counter occupancy" and that the ADAPTER exposes it. Both halves are already wrong against the tree before this proposal. The gauge half is a recorded dead end; whether the Redis-equivalence half is a separate spec finding is undecided and is not 0081's. See `spec.5.review-performance.2`.
- **Should the timer cancellation be conditioned on the credential removal?** — UNVERIFIED: `deregisterSlotLocked` cancels the §4.9 direct-mode timers at the top of `Shutdown` while `removeSlotTree` runs after the drain signal and `Runtime.Close`, so a cleanup whose credential removal fails leaves a direct-mode key with its enforcement point disarmed. Judged near-unreachable and pre-existing. A round wanting to close it should condition the cancellation in SPEC-3's sentence rather than widen §4.9. See `spec.5.review-security.1`.
- **Does the retry re-increment the Redis reservation?** — UNVERIFIED: `BindReservedSlot`'s release-on-failure leaves `PodAssignment` set, so a client retry may bind a reservation the counter no longer holds, while the edge-case bullet reads as though the reservation survives. Pre-existing and code-lane. See `spec.5.review-feasibility.1`.
- **§29.10's "Shared by the whole pod" list** — OPEN, pre-existing: it names neither the pod's shared runtime process nor CH-RUNTIMEOPS. Deliberately not pulled into the §29.4 edit, and a candidate finding for a later round. See `spec.5.fix-G1.1` and `spec.7.review-operational.1`.
- **Should `maxSessionsPerPod` count a bind that reached `RunSetup`?** — UNVERIFIED: §4.7.9 step 5 runs setup commands on the pod before `AssignCredentials`, so a bind abandoned at `ready` has executed code that primes the residual-state vectors §5.2 enumerates, and neither the tree nor the staged spec counts it. Pre-existing; a human or a later proposal should decide. See `spec.7.review-security.1`.
- **Churn from the third accounting caller** — OPEN: on the §7.3 re-attach the accounting is reached for the first time at every concurrency, so at `maxConcurrentSessions: 2` one clean restore failure now drains the freshly claimed replacement pod. The recorded fallback is to account only the leaked arm, stated as a scope in the helper's doc comment. Nobody has priced it against a resume storm. See `spec-recheck.1.fix-G1.1`.
- **Correlated re-attach churn at Tier 3** — OPEN: a whole-node loss puts every slot on those pods into `resume_pending`, and each failed re-attach now drains a fresh replacement pod that never hosted the failed session. The per-instance change is stated in writing; the aggregate against Tier 3's warm-pool headroom is not. A chaos-tier lens is better placed than the performance one. See `non-spec-recheck.1.review-performance.1`.
- **SPEC-3's exclusive-pod arm over-reaches its antecedent** — OPEN: the clause's subject is a bind "abandoned or fails" while §7.1's paragraph binds only "a gateway bind attempt that fails". A READY_TIMEOUT or a revoke retires the pod anyway under spec/05:455; the surviving class is a client terminate at `ready` on a recycling exclusive pool, where the session ends `completed` and the pod is reused. Open decision 11 owns the trigger noun-phrase. See `spec-recheck.3.review-edit-sites.1` and `spec-recheck.3.review-applicability.1`.
- **What disposition the exclusive-pod abandonment arm should name** — UNVERIFIED: §5.2 scrub step 6 marks the scrub failed if `/workspace/slots/` or `/run/lenny/slots/` is non-empty, which looks like the right pointer, but nobody has checked whether a half-completed cleanup's tree trips step 6 or is swept by steps 1-6 first. See `spec-recheck.3.review-applicability.1`.
- **Does §4.6.1 orphan GC reclaim a resume-path leaked slot?** — UNVERIFIED: it drains a `bound` claim whose pod no active session references, so it fires only while the pod carries no other session; once `ClaimSlot` pass 1 places a same-tenant session there the claim is referenced and the leak has no reclaim route. Check `claimOrphanTimeout`'s predicate against a pod at occupancy 1 with one live sibling. See `spec-recheck.1.review-performance.1`.
- **Does any gateway path patch `SandboxClaim.status.phase` to `failed`?** — UNVERIFIED: only a bare DELETE was found on the pre-attached bind-failure path, so the terminal disposition the §4.6.1 projection names may not exist in code. Check the claim-status patch callers before the code lane assumes it. See `spec-recheck.3.review-kubernetes.1`.
- **Does the resume-path slot failure owe an `error_type` value?** — OPEN: `lenny_slot_failure_total` has no series on that path, so a §7.3 re-attach can move the leak gauge and trip the threshold with no slot-failure signal. A new label value mints no §16 or metrics-reference edit, because both rows state the label without enumerating its values. Either answer is legal and the proposal states neither. See `non-spec-recheck.1.review-operational.1`.
- **The tier-7a park placement, and what the co-tenanted variant asserts** — OPEN: where the park sits relative to `SocketRuntimeProcess.Start`'s `accept` decides which of the two closes tears the process down, and the paragraph deliberately asserts nothing about it. The variant now names the pod-level cohort outcome instead. A later round either names a teardown assertion, which needs the placement settled and a real `SocketRuntimeProcess` behind the gate, or drops the variant. See `non-spec-recheck.3.fix-G1.1` and `non-spec-recheck.3.fix-design-G1.1`.
- **Is tier 8 reached?** — UNVERIFIED and declined by three rounds: the change is a failure and recovery path in the plain sense, but the tier-8 examples in `.claude/rules/test-coverage.md` are all infrastructure failure injection and the adapter-refusal and blob-outage cases are covered at tier 4. See `non-spec-recheck.3.review-test-coverage.1`.
- **`binder_test.go` is in no file list** — OPEN: it holds `fakeAssigner` and the `Binder.Resume` fixtures the staged resume case cites, and appears in neither the gateway-tests list nor "Files touched on application". Same package as `slotbinder_test.go`, so nothing fails to compile. See `non-spec-recheck.3.review-applicability.1`.
- **The Decisions bullet names only two adapter files** — UNVERIFIED: summary.md's decision bullet says the adapter edits land in `session.go` and `runtimegeneration.go`, which the CODE-2 extension made incomplete (`resume.go` takes a rollback and `sdkwarm.go` a call-site change). Its load-bearing half, that `slotsession.go` takes doc comments only and `slot.go` is untouched, still holds. See `non-spec-recheck.2.review-fresh.1`.
- **Does a §15.1 retry reach a pod already stamped for drain?** — UNVERIFIED: nothing in `bindReservedSlot` reads `Status.Phase`, so a retry pinned to `row.PodAssignment` may dial a draining pod, succeed and then die with it. The answer decides whether the withdrawn reserved-branch composite is an accepted limitation or a stuck-session bug. See `spec-recheck.3.review-performance.1`.
- **The `**Recycle lifecycle**` paragraph is in no edit list** — OPEN: spec/05:455 is the surface that governs a one-session pod that does NOT retire, and its only retire rule is the failure-or-crash one, which a `completed` terminate does not trigger. Any future sentence asserting "the pod retires" for an exclusive pod has to survive it. See `spec-recheck.3.review-edit-sites.1`.
- **The placement guarantee does not reach a second client request** — OPEN: with the pointer fix the staged §5.2 sentence at spec-changes.md:395 is implemented for every retry the policy places within one client request, and it is still not implemented for a client-driven retry of `/start`, which the proposal records as an accepted failure mode. The sentence currently reads unqualified and nobody has asked whether it should say so. See `non-spec-recheck.5.fix-design-G2.1`.
- **The pod MCP surface a same-pod retry inherits** — UNVERIFIED: after an ACKNOWLEDGED reclaim, `mcpArmingHeldLocked` is true for any entry under the id including the retry's unbound workspace-prep entry, so the retry's `claimPodMCPStartLocked` returns `startMCP == false` (`s.mcpSession == sessionID`) and the retry inherits a surface serving the abandoned attempt's manifest nonce. Looks pre-existing and is outside both findings that raised it; somebody should check whether the nonce mismatch is real. See `non-spec-recheck.5.fix-design-G2.1` (slotsession.go:254-260,:288-292).
- **Nothing at any tier pins that the compensating `Shutdown` stays inside its budget** — OPEN: the budget is the only thing bounding the detached context, and on an exclusive pool reached through `Binder.Resume` it degenerates to the whole pool `cleanupTimeoutSeconds` (up to 60s). Recorded rather than filed, because a prior round refuted the "`slotCleanupBudget` has no listed test" finding as an extra case inside a tier the proposal already exercises. See `non-spec-recheck.5.review-performance.1` and `non-spec-recheck-2.1.review-performance.6`.
- **Can a workspace-prep handler write into the slot directory after `removeSlotTree`?** — UNVERIFIED: the top-of-handler `ensureSlotPaths` makes a post-reclaim ENTRY insert unlikely, but a slow `FinalizeWorkspace` or `RunSetup` still writing into the slot directory after the removal re-creates directories, leaving a tree with no entry. Judged below the bar (the whole-pod scrub's `rm -rf /workspace/slots/*` sweeps it and no residue predicate reads it), but nobody has traced the file-writing path. See `non-spec-recheck.5.review-reliability.1`.
- **CODE-2's replacement doc comment for `noteRuntimeStarted` drops the shipped idempotency sentence** — UNVERIFIED: the BEHAVIOUR is untouched, because the guard lives in `noteRuntimeStartedLocked` (runtimegeneration.go:40-43), which CODE-2 does not edit. Whoever lands CODE-2 should keep the sentence or move it onto the `Locked` form. See `non-spec-recheck.5.review-edit-sites.1`.
- **Do N8's retired spec-line citations reach proposal rationale prose?** — UNVERIFIED: `0081_....spec-changes.md`'s rationale cites spec line numbers, `.claude/rules/channel-naming.md` N8 retires that form, and its `proposals/` escape hatch is written against N3 rather than N8. Moot for the applied spec, since no staged spec text carries a line number. A human or a naming-lens pass should settle it once for the campaign. See `spec-recheck-2.2.review-citations.1`.
- **The open-decisions list is numbered 9 then 11** — UNVERIFIED: entries 1-8 and 10 were resolved and dropped and the preamble explains it, but a CommonMark renderer renumbers the second item to "10.", so a pointer to "entry 11" lands on a "10." in the rendered page. Nobody has decided whether the numbering should be flattened; a human should. See `non-spec-recheck-2.1.review-citations.1`.
- **Does the create-time-reserved edge-case bullet's "reconnects to the same pod" survive §6.2 and §15.1?** — UNVERIFIED: it sits beside spec/06:288 ("Each retry claims a fresh pod") and spec/15:1136 ("recovered with a fresh pod per §6.2"). The reading that separates them (the gateway's internal pre-attached retry loop versus a fresh client call against a row that keeps its pod binding) was assumed rather than proved from the code. A code-lane reviewer with `bindConcurrentSlot`/`PodAssignment` open should settle it. See `spec-recheck-2.2.review-edit-sites.1`.
- **Does the residue's drain-suppression harm deserve any direct observation?** — UNVERIFIED: the round-2 design says no, on the ground that the harm IS the residue and the tier-4 case's first assertion states the residue directly while `boundRemains` is pinned on both arms by the shipped tier-7a test. A reviewer who disagrees should argue against the registry-assertion-is-the-pin claim rather than re-open the fixture wiring. See `non-spec-recheck-2.1.fix-design-G2.1`.
- **The file-collision decision and the 0080 §1.1 impacts row disagree on the adapter file list** — OPEN, and it is the standing "Decisions bullet names only two adapter files" item seen from the impacts side: summary.md:92 names `session.go` and `runtimegeneration.go`, while the §1.1 row at :464 also names `resume.go` and `sdkwarm.go`, which the CODE-2 extension made true. The load-bearing half agrees at both sites, so the row's conclusion stands either way; a fixer correcting one site must sweep the other. See `f3.open-decisions.cleanup`.
- **Does the `spec-changes.md:98-102` blob-store clause still need repair?** — UNVERIFIED, and two entries disagree with no correction between them. The standing Deferred said the spec half stands uncorrected while the non-spec half was repaired; `non-spec-recheck-2.4.review-edit-sites.1` read `spec-changes.md:97-103` directly and reports the bullet ALREADY repaired to the windowed-counter wording, closing with "do not re-file it and do not re-apply it". The newer reading was kept and the Deferred retired. Whoever next opens `spec-changes.md` should confirm which is true before editing that bullet.
- **Does `SocketRuntimeProcess.Close` return within the grace it is handed?** — UNVERIFIED, and the whole margin argument for the `budget/2` split rests on it. `resolveShutdownGrace`'s wait loop was read as a contract rather than traced. A code-lane reviewer with socketruntime.go and mcpruntime.go open should confirm once and record it. See `non-spec.2.review-reliability.1`.
- **Is the `budget/2` window ever shorter than a Full-level runtime needs to quiesce?** — UNVERIFIED: the floor puts it at 2500ms and the reclaim is tearing down an abandoned session, so it was judged inert, but nobody has measured it against a real runtime. A conformance or tier-10 lens is better placed. See `non-spec.1.review-reliability.1`.
- **Does the exclusive-pool resume budget want an upper clamp?** — OPEN and for a human: `max(T/1, 5) == T` gives the path with the least parallelism the LONGEST hold, up to the spec's own example of 60s, on a request goroutine `context.WithoutCancel` has detached from the client, and on that pool nothing is accounted and nothing is released. Three lenses recorded it and none filed it, because no stated budget is breached and the proposal argues the direction explicitly. See `non-spec.1.review-performance.1` and `non-spec-recheck-2.4.review-mechanism.1`.
- **Does any §4.9 or §13 statement bound how long a failed attempt may hold a minted lease?** — UNVERIFIED: the compensation's ordering leaves the §4.9 leases live for up to the whole budget while the client's request is held. Judged not a security finding (the leases are returned unconditionally on the error path, so it fails closed) and not new in the delta. A credential-lifecycle lens should confirm. See `non-spec-recheck-2.4.review-security.1`.
- **Is S5, a `docs`-lane step, permitted to write `tests/tier11_docs/*.go`?** — UNVERIFIED: the proposal puts the tier-11 edits in DOCS-1's step and the checklist line says "Tiers 0, 11". If the implement-proposal write lease is lane-scoped by path rather than by tier, S5 cannot land its own gate. Someone who knows the lease mechanics should confirm. See `non-spec-recheck-2.4.review-docs-alignment.1`.
- **`SLOT_FAILED` appears in no `spec/` or `docs/` file** — OPEN and pre-existing: it is a client-visible 422 emitted at start.go:313 and pinned by a tier-3 test, and `docs/reference/error-catalog.md` and `docs/client-guide/error-handling.md` both lack a row for it. Outside 0081, which mints no new code, but a real documentation gap some proposal should own. See `non-spec-recheck-2.4.review-docs-alignment.1` and `non-spec.1.review-client-surface.1`.
- **`docs/reference/configuration.md:99` is now three effects behind on `cleanupTimeoutSeconds`** — OPEN: the row describes it as "Timeout for `cleanupCommands` plus the scrub-report grace" with the constraint "Must be `> 0`", already omitting the per-slot formula and the CRD rule `cleanupTimeoutSeconds >= maxConcurrentSessions × 5`, and CODE-4 adds a third effect (the gateway's give-up bound, half of it the adapter's graceful window). Not filed, because a docs edit would document a code-only choice the spec deliberately leaves unnumbered. See `non-spec.2.review-docs-alignment.1`.
- **Does `ClaimSlot` pass 1 admit a pod the WarmPoolController is already draining?** — UNVERIFIED and pre-existing: pass 1 reads the per-pod claim and never the Sandbox phase. Outside this proposal, and no lens here has been asked to judge it. A conformance lens after implementation should decide. See `non-spec-recheck-2.4.review-kubernetes.1`.
- **The unscoped `leaked` in the new hold paragraph and in SPEC-4's :580 sentence** — OPEN, FILED as two findings in two deliverables. See `spec.1.review-operational.1` and `spec.1.review-security.1`.
- **Is a third-party adapter that omits the start-side epoch confirmation conforming?** — UNVERIFIED: §15.4 publishes only the mint, report and `Shutdown`-compare obligations, and CONF-1's properties do not cover the start race. Somebody should decide whether CODE-2's `noteRuntimeStarted` confirmation is a published obligation or deliberately first-party-only. See `spec.1.review-docs-alignment.1`.
- **A retry whose predecessor's start the adapter ADMITTED is refused until the reclaim clears** — OPEN: if that reclaim is never answered, the identifier refuses every further attempt until the pod terminates, and on a create-time-reserved slot §5.2's placement constraint does not move the retry elsewhere. Recorded in the accepted failure modes; a human may want a distinct client-visible outcome there. See `spec.1.fix-G1.1`.
- **Is a second `POST /v1/sessions/{id}/start` for the same session admitted while the first is in flight?** — UNVERIFIED: it decides how wide the unfenced first-RPC window is. The finding was reached on the budget-expiry route instead. A code-lane reviewer with the `/start` precondition table open should confirm. See `spec.1.review-performance.1`.
- **Can the gateway placing a §5.2 retry actually see that a pod holds an incomplete reclaim?** — UNVERIFIED: the staged `**Max retries:**` sentence assumes it can and the `ExcludePods` threading is where it is answered; no spec-lane lens traced it. See `spec.1.review-feasibility.1`.
- **Does §15.4 state what the unconditional (no-epoch) `Shutdown` reports on its response?** — OPEN: §4.7's row does (`reclaimed` / `absent`) and the gateway reads one field on every answer, but §15.4 scopes its whole outcome paragraph to "the comparison's outcome" after saying the no-epoch form "is compared against nothing". A third-party adapter author working from §15.4 alone has to derive it. See `spec.1.review-citations.1` and `spec.1.review-client-surface.1`.
- **The `/finalize` Gap-2 window cannot discharge §7.1's reclaim obligation** — OPEN, FILED: both branches fail after `Binder.Prepare` closed its connection and call `ReclaimClaimed`, which sends the adapter no RPC, while §7.1 names the creation finalize block, mandates the reclaim on the connection the failed stage still holds, and forbids re-dialling. The fixer must pick one of two legal answers and make §7.1, §4.7.1's caller rule and the accepted-failure-mode list agree: relax the no-re-dial rule for the epochless form, or record the window as an accepted residue. Keep the finding on the CONNECTION, never on the pod disposition. See `spec.7.review-mechanism.8`.
- **§4.7.1's caller rule mixes per-connection and per-session granularity** — OPEN, FILED: the holding rule defines one per-connection value ("the most recent epoch a response reported to it on one adapter connection") while the naming rule two sentences later reads it per session ("the epoch the caller holds for that session"). Newest, least-examined block. See `spec.8.review-fresh.1`.
- **Does §5.2's hold obligation reach the sixteen non-`Shutdown` release sites?** — UNVERIFIED: the staged hold is written as an obligation on every adapter deregistration-plus-cleanup, while CODE-6 takes it only inside `Shutdown`; `releaseSessionSlot` runs at session.go:133,:147,:157, resume.go:69-141 and sdkwarm.go:236,:241,:251,:298 with `s.mu` released between the deregister and the tree removal. Declined twice on the "the action list is ahead of the code; pre-existing" precedent, and it is decidable in the spec lane by scoping the hold's antecedent to the `Shutdown` slot release. See `spec.3.review-docs-alignment.4` and `spec.5.review-reliability.2`.
- **Does the successor that adopts a surviving entry inherit its still-armed §4.9 timers?** — UNVERIFIED: no cleanup and no `deregisterSlotLocked` runs on adoption. Same session and same tenant, so not an isolation issue, but a stale timer firing `AUTH_EXPIRED` at the successor is a correctness question for the code lane. See `spec.5.review-security.1`.
- **Does the hold's predicate want narrowing to the bind sequence?** — OPEN: §5.2 and §15.4 both refuse "any request that would create or resolve a registry entry" with a normative `ABORTED`, which is wider than the seven bind-sequence RPCs the design means. Declined four times on the vacuous-resolve reading. If a round wants it, the correction is to narrow both predicates to the enumerated seven, and the argument has to start from §4.7.2's Checkpoint/Interrupt operation lock, which QUEUES rather than refuses. See `spec.8.review-edit-sites.1`.
- **Does a `Shutdown` carrying neither a graceful window nor a deadline leave the hold unbounded?** — UNVERIFIED: §5.2 names two bounds for a request-borne hold and none for that case; `resolveShutdownGrace` then falls back to the runtime-configured grace or the package default, so the hold is bounded in the tree by a figure the spec does not name. See `spec.8.review-reliability.1` (pkg/adapter/mcpruntime.go:311-324).
- **§15.4's "reports zero on a bind-sequence response does not conform" versus an empty `PrepareWorkspace` stream** — OPEN: a call with zero frames resolves no entry and would report zero. Unreachable today, because `stageWorkspace` sends the RPC only when the plan carries uploads. The fix is to scope the clause to a call that resolved an entry. See `spec.7.review-edit-sites.1`.
- **Would a caller multiplexing two sessions on one adapter connection name the wrong session's epoch?** — UNVERIFIED: the shipped caller cannot reach it (a fresh dial per bind), and the spec never states the one-session-per-connection premise the per-connection latch rests on. A lens owning the caller contract should decide whether §4.7.1 must state it. See `spec.6.review-applicability.1`.
- **Does §28.3's `LNK-POD-GRPC` multiplicity reach unregistered gateway→adapter RPCs?** — UNVERIFIED: that row says "One connection per gateway replica per pod" while §4.7.1 states a bind attempt may span two connections and production dials a fresh client per connect. §29.2 and §29.4 place the bind and teardown RPCs outside the §28 register, which is why three rounds declined it. A channel-naming lens should settle it. See `spec.6.review-edit-sites.1` and `spec.8.review-fresh.1`.
- **spec/04:672's `StartSession` row states no refusal** — OPEN, pre-existing: `claimSessionSlotUnderLock` refuses a repeat start onto a started session with `codes.Unavailable`, and this proposal depends on that refusal twice. Worth its own finding in a later round; do not repair it inside 0081. See `spec.6.fix-design-G1.1`.
- **Retire the Kubernetes lens for the remaining spec rounds?** — OPEN: it has returned nothing on the spec staging in rounds 3, 5, 6, 7 and 8 against text that changed each round only on the gRPC surface. If a future amendment touches no CRD, status write, finalizer, webhook or reconcile loop, retiring it costs nothing and saves a round's latency. See `spec.8.review-kubernetes.1`.
- **Are the two shared-entry edge-case bullets one ordering stated twice?** — UNVERIFIED: `spec.6.fix-design-G3.1` deliberately did not restructure the section, and the two bullets describe near-identical orderings. If a later round merges them, apply the closing-cost sentence ONCE.
- **Does "termination window" name a quantity the specification defines?** — OPEN: §5.2's hold paragraph bounds a hold taken outside any request by "the termination window of the pass that runs the cleanup", and `grep -rn "termination window" spec/` returns zero hits, on a fail-closed gate. §15.4 points at §5.2 for the whole hold and inherits the undefined bound into the third-party contract. Declined as descriptive. See `spec.6.review-kubernetes.1`.
- **Is the hold refusal "accounted as an ordinary transient slot failure" on the resume path?** — UNVERIFIED: true for the create-time-reserved retry through `materializeSlot` → `recordSlotFailure`, and false for the client-driven §7.3 resume, which emits no `lenny_slot_failure_total` at all and reaches accounting only through CODE-5's new caller. The edge case names that resume as one of three things that can meet the hold. Turns on whether the sentence means the §5.2 classification or the metric. See `spec.6.review-operational.1`.
- **What does a third-party adapter answering `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` mean?** — UNVERIFIED: the zero value has no meaning in either staged block (both state exactly three outcomes) and CODE-4's switch routes it through `default: return false`, that is, not leaked. Conforming, non-conforming or leaked is unstated. Judged below the bar on the proto3 zero-value convention, and `SessionScrubOutcome` has the same shape. See `spec.2.review-client-surface.2`.
- **Does §15.4's zero-response non-conformance clause want scoping?** — OPEN: a `PrepareWorkspace` call with zero frames resolves no entry, so its response would report zero, which the clause calls non-conformant. Unreachable today, because `stageWorkspace` sends the RPC only when the plan carries uploads. The fix is to scope the clause to a call that resolved an entry rather than restate it. See `spec.7.review-edit-sites.1`.
- **Who owns the per-slot sub-state, and when does `slot_cleanup` begin?** — UNVERIFIED: the r3 §6.2 rewrite rests entirely on "§6.2's `slot_cleanup` sub-state begins when the gateway observes the failure", and nothing in §6.2 states a tracker or a start instant, while spec/05:544 attributes the sibling `failed` marking to the ADAPTER. Not filed as a wrong-component attribution because §6.2 itself is silent and `pkg/sandbox/slotstate` is gateway-side. Settle it before the next edit rests on it. See `spec.3.review-citations.1`.
- **Does §7.4's mid-session upload want a rule of its own?** — UNVERIFIED under the reverted per-entry design: the pair runs on the live binding's connection and no bind-sequence request carries an epoch, so nothing refuses it, but the CONF-1 battery still owes a case driving a `FinalizeWorkspace(mid_session=true)` onto a started session. See `spec.5.fix-G1.1`.
- **Is the §7.1 "spends one of the attempts" clause finally settled?** — UNVERIFIED and re-derived in six rounds: round 7 reworded it to "an attempt that meets the hold is one the retry policy did not place", and one lens still reads the pair as reachable only for a conforming adapter that answers `reclaimed` early while its cleanup runs. Read the CURRENT text before filing; four lenses have declined it as consequence-then-mitigation.
- **Should the hold's predicate be narrowed, and is `Shutdown`'s carve-out an exception?** — OPEN, two halves of one question: §5.2 and §15.4 refuse "any request that would create or resolve a registry entry" while §15.4 adds "`Shutdown` is not held". Both are reconcilable on the vacuous-resolve reading (during the hold no entry exists), and the correction, if a round wants one, is one clause in §5.2 rather than in §15.4. See `spec.6.review-edit-sites.1`.
- **Is the epoch fence observable end to end through `slotResolveError`?** — UNVERIFIED: the five `codes.InvalidArgument` resolve wraps must preserve `ABORTED` to the gateway, and nobody has traced it. Code-lane item. See `spec.6.review-fresh.1`.
- **Are the two `WIRED` claim-register rows owed at all?** — OPEN: §28.4 obliges a row only for a normative §28 statement, and the proposal itself argues `Shutdown` carries no §28 row. Dropping the rows is a legal alternative to editing the generator, and it is cheaper. A human or a later round should pick. See `non-spec.1.review-edit-sites.1`.
- **Does CONF-1 owe a fifth property?** — OPEN: SPEC-5 lands two §15.4 blocks and CONF-1's four properties are all bind-epoch, so the reclaim hold has none, and §15.4's `PrepareWorkspace` non-conformance clause (an adapter that mints more than one epoch for one call, or resolves the identifier more than once) has none either. The wanted case asserts a multi-frame call resolves its entry once and answers one non-zero epoch. See `non-spec.1.review-test-coverage.1` and the third reconciliation pass.
- **Who owns the `claimSessionSlot`/`claimSessionSlotUnderLock` epoch-return signature change?** — UNVERIFIED: the prose sits in CODE-2 (S11) while `## Files touched` puts it under `slotsession.go`, which is CODE-6's file (S9). Landing it at S9 stops the tree compiling, because the three callers are in files CODE-6 does not open. The CODE-2 reading is available and correct; somebody should pin it in one sentence. See `non-spec.1.review-applicability.1`.
- **Is the tier-9 credential-fence suite correctly landed at S10 alone?** — OPEN: `tests/tier9_security/slot_credential_reclaim_fence_test.go` is attributed to "CODE-1 and CODE-6" and S9/CODE-6 carries no tier 9. Judged fine because CODE-1 is the later half and S10 follows S9; the same reasoning did NOT rescue the tier-3 suite, which was filed. See `non-spec.1.review-applicability.1`.
- **How far does the reclaim hold actually extend?** — UNVERIFIED: `s.reportSessionScrub(ctx, sessionID, closeErr)` sits above `defer release()` in CODE-1's body, so the hold spans an outbound gateway RPC that no staged sentence names among its bounds. Judged harmless because the hold is keyed on the ending session's own identifier. Whoever revisits the hold's bound should know its real extent includes the scrub report. See `non-spec.1.review-reliability.1`.
- **Does the credential-delivery hold refusal want a tier-9 arm of its own?** — UNVERIFIED: `assignCredentialsSlot` mid-cleanup is pinned at tier 1 (the five-resolve-site table) and at tier 7a, and the staged tier-9 file carries only the two epoch arms, while `.claude/rules/test-coverage.md` routes credential delivery to tier 9. Judged nice-to-have. See `non-spec.1.review-security.1`.
- **Does the adapter's `/healthz` `leaked_slots` count increment when the report is withheld?** — UNVERIFIED and still unanswered after two filings: spec/06:160 makes that count an ADAPTER health-metadata export and names it as one of two sources for the `ceil(maxConcurrentSessions/2)` drain trigger. If it does not increment, one of the two sources never sees these leaks. The staged text names the gateway-side path, so it is not this proposal's defect. See `spec.6.review-security.1` and `non-spec.1.review-security.1`.
- **Does `ensureSlotPaths` widening its return break an unlisted test file?** — UNVERIFIED: `pkg/adapter/exportpaths_test.go:25,:221,:225` is in no file list, while `slotsession_test.go:319` and `export_test.go:62` are. Judged a build-adaptation item of the class this loop has refuted five times on materiality; a fixer opening the files list should add it. See `non-spec.1.review-reliability.1` and `non-spec.1.review-feasibility.1`.
- **summary.md's "Seven residues survive" count** — OPEN: the bullet then enumerates four plus two. Same class as the already-refuted "Four statements are needed" count, and `doc-style.md` would drop the count anyway. Whoever next edits that bullet should name the seventh or drop the count. See `non-spec.1.review-fresh.1`.
- **Is the tier-4 datastore-crossing case written as a second self-issued `BindSlot`?** — UNVERIFIED: `recycleCluster` seeds one idle Sandbox and wires a `podsession.Binder` with no `sessionserver`, and `applySlotRetryPolicy` is unexported, so the "retry" there can only be a second `BindSlot` the case issues itself with `ExcludePods` set. That reading is workable; whoever implements S12/S13 should confirm the case is written that way rather than reaching for the retry policy from an external package. See `non-spec.1.review-test-coverage.1`.
- **Does a mass §7.3 recovery storm cascade through the new `resumeOnPod` accounting caller?** — UNVERIFIED and unmodelled: each failed resume drains its replacement pod at `maxConcurrentSessions: 2`, and the replacements are themselves resumed onto. This sharpens the standing correlated-re-attach-churn item with the specific loop. A capacity reviewer with Tier 3 pod-churn numbers should. See `non-spec.1.review-performance.1`.
- **Do the two SCHEMA-1 claim rows need `note` fields?** — UNVERIFIED: every row in the generator's `EXPLICIT` list carries one and the validator checks only status, surface and anchor, so it is convention rather than a gate. Whoever moves the rows into the generator should match the siblings. See `non-spec.1.review-feasibility.1`.
- **Does DOCS-2 owe a positive tier-11 gate on its new row's content?** — OPEN: the existing gate is substring-presence only and would pass a stale row again the next time §4.7 moves, where DOCS-1 stages its own assertion. See `non-spec.1.review-operational.1`.

### Deferred

- DEFERRED [schemas/lenny-adapter.proto]: the `Shutdown` RPC comment ("asks the adapter to terminate the agent and release the pod ... Returns when the agent process has exited") and the `ReportSessionScrub` / `SessionScrubOutcome` comments ("on every session release") are the proto-side statement of the contract §15.4 says is "kept in sync with the prose". SPEC-1's two-teardown split, its no-op clean-exit answer, and SPEC-3's withheld report all falsify parts of them. GROUND CHANGED: the amendment retracted the S-2 reading this entry rested on, because SCHEMA-1 now opens `schemas/lenny-adapter.proto` under S-2's second window (summary.md:94-95) at S8. Two lenses nonetheless declined to file the comment repair here, on the ground that the `Shutdown` comment is ALREADY false today for a session the adapter holds no entry for and for a per-slot `Shutdown` on a co-tenanted pod, which is the pre-existing-looseness class prior rounds killed. So the correction is now LANDABLE here rather than barred, and whether to land it or leave it to R1b is a live choice rather than a constraint. The R1b attribution below is the older disposition. The `Shutdown` comment is already stale for a co-tenanted pod, so this is a widening rather than a new break. NARROWED: `ShutdownResponse` carries no doc comment at all (:1665-1668 is a bare three-line message), so CODE-1's re-keying of `exited_cleanly` owes nothing here and the entry is the three comments named below and no more. EVIDENCE: schemas/lenny-adapter.proto:203-206,:308-311,:436-438; spec/15:1456.
- DEFERRED [pkg/adapter/session.go, pkg/adapter/resume.go, a later proposal]: the shipped pre-`Runtime.Start` failure branches release the slot by session identifier alone (session.go:133,:147,:157; resume.go:69,:73,:89,:107,:126,:134,:141; the same pattern also stands at sdkwarm.go:236,:241,:251,:298). Under `SlotID == SessionID` a lagging one deletes a later attempt's entry and `RemoveAll`s the tree, uploads and credential directory that attempt staged, exactly as CODE-2's withdrawn rollback release would have. The claim that "`releaseSessionSlot` is correct against an entry a concurrent reclaim removed" is false for all of them. This proposal does not open those branches; the `f3` firing landed the record as a summary out-of-scope row, and the class fix (an identity-checked deregister threading the `*slotState` the claim returned into `deregisterSlotLocked`) wants its own problem statement.
- DEFERRED [proposals/0081_.../0081_....review-log-archive.md, the WATCHOUT at :3785]: it carries the same rule as the standing Trap "Do NOT add a bind-path or a start-state qualifier to §5.2's leak-accounting sentence" and cites its evidence as "summary.md, the `bindConcurrentSlot`'s-reserved-branch bullet under the recorded limits", which the `f4` firing deleted and which no longer resolves. Re-point it to CODE-5's `accountSlotFailure` doc comment (non-spec-changes.md:515) and its caller list (:537-569), which state the same no-carve-out argument. The standing-context half of this correction is applied; the archive is outside every current lane's editable set.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md, the edge-case bullet at :109-111]: it reads "A retry the §5.2 slot retry policy places goes to a different pod; when that pod is the pool's only candidate the retry meets the shipped `WARM_POOL_EXHAUSTED` outcome." The nearest antecedent for "that pod" is the DIFFERENT pod, which makes the sentence backwards, since a retry whose target pod is the only candidate succeeds. The intended subject is the excluded, residue-bearing pod, which the §5.2 rationale states correctly at :406-408. Unapplied prose, so the applied spec is not wrong; a fixer touching that bullet should repair the pronoun rather than reword the rule.
- DEFERRED [pkg/gateway/runtime/adapterclient/client.go, the `Client.Shutdown` doc comment at :797-798]: it says "A zero deadline lets the adapter apply its default grace period". That is false. When the caller's ctx carries a deadline, `resolveShutdownGrace` takes the ctx's remaining time in preference to both the runtime-configured grace and the package default, so a zero deadline yields the CALLER's own remaining budget; the adapter's default applies only when the caller set no deadline at all. OWNER CHANGED: `adapterclient` IS a target now — CODE-6 opens `pkg/gateway/runtime/adapterclient/client.go` for the epoch latch, `BindEpoch` and `ShutdownReclaim` — so the one-sentence repair can be staged under CODE-6 at S9 rather than left to whoever next opens the file. Derived independently by four shards across two windows and still unapplied.
- DEFERRED [spec/06_warm-pod-model.md:156 and docs/reference/state-machines.md:236]: the `slot_cleanup ──→ released` annotation and its docs mirror gloss three actions ("slot workspace removed, processes killed, slot released"), a word-for-word mirror of §5.2's shipped three-action list, while SPEC-3 widens that list to five (the credential directory and the §4.9 direct-mode expiry timers join it). Neither is false, because an abbreviated gloss states nothing wrong, which is the standing trap's reasoning for the identical §6.2:148 annotation, but a reader comparing either to §5.2 after this lands sees a shorter list. If DOCS-1 is ever widened beyond its single added row, this is the sibling row to widen with it.
- DEFERRED [docs/reference/error-catalog.md]: nothing, recorded deliberately. Under the placement-rule design, lines 129, 155 and 156 stay TRUE, because no failure class becomes non-retryable. Recorded explicitly so a later round does not re-file them: they are sites only under the rejected no-retry alternative.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md "Spec files touched"]: the spec/07 half is CLOSED, and the line now reads "the section preamble's premise sentence deleted, a sentence added to step 2, and step 3 replaced". The spec/04 half stands: the list still describes the §4.7 row edit as "(first sentence plus one sentence)" when the replacement swaps two sentences for seven or eight, and the lead-in "Replace the `Shutdown` row's first sentence" is stale in the same way. Six lenses re-derived it and none filed it, because the operative quote-and-replacement pair is exact and no implementor can be misled; one lens argued the parenthetical describes the edit rather than the anchor block and is therefore defensible. A fixer touching the SPEC-1 §4.7 block should correct it in the same edit. A count in that list is a trap that re-arms on every later edit; prefer a named set, as the SPEC-3 entry now uses ("the per-slot cleanup pointer and the cleanup-outcome report rules appended"). The §5.2 entry now also understates: a pod-disposition claim lives in that append and the named set does not hint at it.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :63-66]: "A §15.1 start onto a slot reserved at creation is placed by neither mechanism" has no antecedent for "neither". Round 2 named two placement mechanisms, round 3 deleted the second from the surrounding text, and round 4 did not restore it, so only §5.2's slot retry policy is named. What is true: two mechanisms place a further attempt at the same session, §5.2's slot retry policy and the §7.3 re-attach's whole-pod idle claim through `podclaim.Claimer.Claim`, which cannot select a pod whose occupancy the leaked slot holds; the §15.1 create-time-reserved start is placed by neither. Repair the sentence with that fact rather than by deleting "neither".
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :43-44]: the pointer list ("§7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph and restate nothing") omits the SPEC-2 §7.2 snapshot-close edits entirely, although "Spec files touched" carries them. §7.2 step 3 also restates the reclaim's ordering against the pod release, so the sentence is incomplete rather than wrong. A fixer touching the Design paragraph should fold §7.2 in.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md, edge case "A client retry of the §15.1 start after a failed bind on a create-time-reserved slot"]: the round-4 rewrite says "When the adapter does not acknowledge the reclaim … the adapter's entry survives in whatever state the failed stage left it." That is false for the delivered-but-unanswered case: a `Shutdown` the adapter executed while the response was lost removes the entry, and the gateway still classifies the reclaim as unacknowledged (`err != nil || !cleanly`). What is true instead: an unacknowledged reclaim leaves the gateway unable to tell whether the entry survives, so the retry may find either a surviving entry or none. The later sentences of the same bullet already reason about a lagging reclaim, so the correction is one clause.
- DEFERRED [docs/, four sites that stay TRUE]: `docs/client-guide/session-lifecycle.md:416`, `docs/reference/adapter-contract.md:84`, `docs/runtime-author-guide/index.md:186` and `docs/runtime-author-guide/lifecycle.md:69` also describe the per-slot cleanup and are NOT falsified by SPEC-1 or SPEC-3. Recorded explicitly so the non-spec loop does not widen its docs edit list past the five sites that actually break. Also unchanged: `docs/runtime-author-guide/lifecycle.md:34-43` is a pod-level table in a different vocabulary with no cleanup edge, so SPEC-4 does not reach it.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md, CONF-1's `PrepareWorkspace` coverage]: §15.4 publishes a non-conformance case for an adapter that "evaluates the admission rules per `PrepareWorkspace` frame, or mints more than one epoch for one call", and the battery has no case for it. What CONF-1 wants is one case asserting that every frame of a multi-frame `PrepareWorkspace` call is admitted exactly once and that the single response reports one non-zero epoch. This is what survives of the larger echo-property Deferred the r6 revert dissolved; do not restore the echo half.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md:1719-1722, the hold-refusal accepted-failure bullet]: it says a §7.3 resume refused by the hold "costs the pod one windowed `RecordFailure` … whose only production call site is `applySlotRetryPolicy`". That is false against the STAGED tree: CODE-5 extracts `RecordFailure` into `accountSlotFailure` and gives it three callers, and the resume path reaches it ONLY through the new `resumeOnPod` caller — which is what makes the bullet's own conclusion true. What is true instead: the refusal costs the pod one windowed failure through `accountSlotFailure`'s third caller. The conclusion stands; only the parenthetical attribution is wrong. Same class as the standing MISTAKE about a scope call resting on shipped behaviour a staged deliverable removes.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md:943-945, CODE-6's `deregisterSlotLocked` rationale]: it justifies keeping the signature "for the read-only caller that needs the deregistration without a destroy". After CODE-6 all three production callers go through `reclaimSlotLocked`, so no such caller exists and `deregisterSlotLocked` survives only as that helper's own body. What is true: the signature is kept because `reclaimSlotLocked` calls it, and there is no read-only caller. A fixer already in that paragraph should drop the clause. Sub-threshold on its own; recorded so it is not re-derived.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md:1668, a step reference the renumber staled]: it reads "Between S4 and S6 the unedited file still passes" where SPEC-4 is now S5 under the resequenced checklist (S1=SPEC-5 … S5=SPEC-4). What is true: the interval is between S5 and S7. Substantively harmless, and it is the only stale step reference the renumber sweep missed; a fixer touching that paragraph should correct it.
- DEFERRED [proposals/0081_.../0081_....non-spec-changes.md:1043-1053, CONF-1's property count]: it says "Four properties, one per part of the §15.4 contract" while SPEC-5 lands TWO §15.4 blocks, the bind-epoch contract and the slot-identifier reclaim-hold contract, and all four properties are bind-epoch. What is true: the four properties cover the bind-epoch block alone, and the reclaim hold is unpublished in the battery. Either drop the "one per part of the §15.4 contract" clause or add the hold's property; the standing Open on a fifth property is the same question from the coverage side.

### Retired

Retired in passes 1 and 2, each closed rather than dropped, with the closure reason that keeps it closed: the `coordination_generation` question (`Shutdown` is unfenced, validated only in `CoordinatorFence` and `CheckpointBarrier`); whether `Binder.Resume`'s compensation needs its own §7.3 sentence (answered by lifting the obligation into its own §7.1 paragraph scoped to any bind attempt); the CODE-2 second `ReportSessionScrub` DEFERRED (the call and the `errStartRaceReclaimed` sentinel were deleted); whether the Design paragraph's deferral of the credential file and the §4.9 timers to §5.2 is a false attribution (SPEC-3's first anchor widens the action list to name both); the four condensed checklist DEFERREDs of the pass-1 preamble (superseded by the fuller per-group versions); "Vacuous parenthetical at steps 2-8" (§29.2's mirror covers steps 2-10, which issue no pod-side RPC); "Recycle boundary on a failed bind" (`recycle` rather than `leaked` patches `bound → recycling`, and `ReleaseSlotReservation` passes `recycle=false`, so occupancy zero DELETEs the claim); "SPEC-3's closing note may be stale" (the edit landed and the four-site drift is gone); and the "On the default disposition the pod is replaced." dispute (one site rather than two, rewritten in Traps).

Two pass-2 retirements carry a residue that is still live. The OPEN "Resume path and the exclusion" was closed in the proposal's favour by nine lenses (`Binder.Resume` claims through `b.connect` → `podclaim.Claimer.Claim`, an idle-only whole-pod claim, never through `ClaimSlot`), and `spec.5.review-mechanism.1`'s disagreement is carried as the UNVERIFIED beside the Settled entry rather than dropped. The pass-1 six-anchor sweep entry was DELETED rather than retired, because a warning that a sweep is finished is worse than useless once nine more anchors have been minted; the sixteen-site list in Settled replaces it.

Retired in compaction pass 3, all closed rather than dropped:

- The OPEN "Does the resume compensation feed the slothealth ledger?". Answered NO and fixed: `podsession` reaches no tracker, gauge or registry, and CODE-5 now gives `accountSlotFailure` a third caller on `resumeOnPod`'s `podBinder.Resume` failure branch. The durable statement is in Settled.
- The UNVERIFIED "Is §29.4 scoped to a started session?". Settled by §29.4's own `**Preconditions.**` paragraph, which scopes the whole trace to a completed §29.2 startup. Four lenses read it cold and agreed, and the round-7 pair that filed the opposite is superseded rather than merely outvoted, because the paragraph they did not cite decides it.
- The two tier-7a items, "drain-gate assertion" and "closed exactly once". The first is closed: every `Shutdown` target in the shipped drain-gate tests is driven through `startDrainSession`, so CODE-1's gate move cannot reach them. The second is closed by deletion: the clause was false as a call count and as a teardown count, and the trap that replaces it says why.
- The UNVERIFIED "Does CODE-1's drain move lose a DRAINING obligation?". Answered: §15.4.2 owes none for a bound-but-unstarted session, and the withheld frame is now pinned by a tier-1 assertion.
- The OPEN "No section says positively that a reclaim can find a slot in `running`". Resolved as no-sentence-added: SPEC-4's `**Pre-`running` slot cleanup.**` paragraph already states it.
- The OPEN "Redis rehydration loses leaked occupancy". Resolved as its own proposal and recorded as a row under the summary's shipped-defects section.
- The UNVERIFIED "'on a pod of either concurrency' and the leaked sentence" and the OPEN "Pre-start cleanup failure and the §5.2 threshold". Both closed by the same concurrency split, which states the accounting for a concurrent pod and points at §7.1 for a one-session pod.
- The UNVERIFIED "tier 11 against the adapter-contract drift". Closed: the gate is a substring presence check and passes silently. The fact moved to Settled and the consequence to the Deferred entry it belongs with.
- The two fixture items, "Tier-4 fixture pool size" and "Tier-1 adapter fixtures". Both read: the tier-4 file seeds one Sandbox and `export_test.go:45` is already a `noteRuntimeStarted` seam. What survives is the narrower fixture-extension OPEN.
- The OPEN "0080 §1.19 membership" and its sibling DEFERRED against the summary row. Both discharged by the index-and-checklist reconciliation pass, which rewrote the row to state all three membership cases and the placement consequence.
- The OPEN "Unbound co-tenant and the drain gate". Superseded: after CODE-1 the signal needs `started` and no bound entry, and the residual case is an ordinary started session end. It is now a "do not file" line in Traps rather than an open question.
- The OPEN "This lens has swept itself out". Corrected in place rather than kept: the operational lens returned findings on the non-spec staging.
- Seven checklist and summary DEFERREDs (S1, S2, S3, S7, S8, the older S10 on the no-retry rule, and the 0080 §1.19 row), plus the CODE-2 entry and the blob-store edge-case clause. Each was applied: the first seven by the index-and-checklist reconciliation pass, the CODE-2 residue by round 2 taking the second branch and recording both orderings in the accepted-failure-modes list, and the blob-store clause by the edge-case rewrite that states the windowed-counter arithmetic honestly. The newer S9 and S10 corrections that landed after them are carried in Deferred, and the blob-store clause's SPEC-side twin is carried there too, because that pass could not edit `spec-changes.md`.

Retired in compaction pass 5, all closed rather than dropped:

- The UNVERIFIED "Does `releaseCredentials` cover user-source leases?", which was disputed with no correction between the two readings. Closed affirmative: `credassign.Service.ReleaseSession` iterates `s.leases.LeasesBySession([sessionID])` over the shared lease store and the `UserCredentialAssigner` doc comment states it outright. The reading that `releaseCredentials` touches only `b.Credentials` is right about the call and wrong about the consequence, because `b.Credentials` is the store both minters write into. Two lenses reached it independently.
- The OPEN "CODE-2's rollbacks run on the inbound context". Closed: all three `RuntimeProcess.Close` implementations use the context only to derive the SIGTERM grace and none checks `ctx.Err()`, so a cancelled context makes the close harsher rather than absent and the active-set removal the rollback exists for always lands. `context.WithoutCancel` is therefore not owed there.
- The OPEN "The reserved-branch defects row contradicts its own header". Closed by deletion: the `f4` firing removed the row from `## Defects in the shipped tree that this proposal does not stage`, because CODE-5 stages the fix for the exact defect it named and the section is the inventory a later reader uses to learn what 0081 leaves open. The shipped-tree fact survives in CODE-5's trade paragraph and the no-carve-out argument in its doc comment.
- The UNVERIFIED "Tier-1 resume accounting fixture". Closed on the seam question: `s.podBinder` is a concrete `*podsession.Binder` so no interface seam exists, but the package's own tests already build real `podsession.Binder` values against a fake or envtest client at five named sites and one of them already drives `resumeOnPod`. What remains is a fixture build rather than an open question.
- The OPEN "The operational lens is no longer swept out". It was a correction rather than a question, it has been acted on for several rounds, and the surfaces it named are recorded as Settled facts.
- The disputed DEFERRED on `docs/reference/state-machines.md:251`. Settled STALE by derivation rather than by vote: after application, staged §7.1 and SPEC-3's §5.2 scrub-model append both scope the `leaked` disposition to a pod serving concurrent sessions and give the exclusive pod §7.1's retirement disposition, so the doc prose agrees with the applied spec and is not an edit site. The sibling asymmetry it used to be paired with survives as the standing Open on §6.2's fence versus §6.2's prose at concurrency 1.
- Two claims this window superseded rather than closed, kept here so nobody re-derives the old form. `SlotID == SessionID` is stated in `spec/` (spec/05:395), so §7.1 restates rather than mints it; and `validate-maps` enforces per-file spec-map membership at tier 0, so the three entries that declined the spec-map item on "existence, version and dangling paths only" declined it on incomplete ground. Both corrections are applied in Settled and in Open.

Retired in compaction pass 6, all closed rather than dropped:

- The OPEN "The reclaim's deadline has no margin over the adapter's own cleanup budget". Closed by the fix it produced: `compensateFailedSlotBind` now sends `budget/2` as the graceful window inside an RPC deadline of `budget`, so the adapter's SIGTERM pivot and the gateway's give-up instant are no longer the same moment and a completed reclaim is no longer recorded `Leaked`. The fix shard named the Open as the finding it was filing. Its sibling, "Nothing at any tier pins that the compensating `Shutdown` stays inside its budget", is NOT closed: the tier-1 case now pins the two values' relation and nothing pins the wall-clock wait.
- The UNVERIFIED "Does `spec/18` need an edit?". Answered NO by three lenses on the same derivation: spec/18's phase-12c deliverable lines state the slot and recycle surfaces at the level of the split itself and enumerate no precondition, report rule or state-machine edge, so nothing SPEC-1 through SPEC-4 stages can make a phase deliverable stale. The durable statement is in Settled.
- The UNVERIFIED "Does §5.2's `Client error on exhaustion` want a fourth reason value for a reclaimed-slot race?". Answered NO: `codes.Aborted` passes through `classifySlotBindFailure` unclassified and lands on the existing retryable `STARTING_FAILED` / `SESSION_CREATION_FAILED` fallback in `writePodClaimError`, so no new client-visible code and no `docs/reference/error-catalog.md` row is owed.
- The DEFERRED against `spec-changes.md:98-102` (the blob-store edge-case clause). Retired because two entries disagreed and neither corrected the other: the Deferred said the spec half stood uncorrected while `non-spec-recheck-2.4.review-edit-sites.1`, reading `spec-changes.md:97-103` directly, reports the bullet already repaired to the windowed-counter wording and says not to re-apply it. The newer reading was kept, and the residual question is carried as an UNVERIFIED in Open so a fixer settles it before editing that bullet.
- One clause DELETED rather than retired: the standing Trap on the per-stage compensation table used to close with "the upload-free branch is listed separately only because it is the one stage failure that sends no RPC at all". `stageWorkspace` cannot fail on an upload-free plan at all, so the clause was false, it contradicted Settled #65, and the proposal had followed the Trap rather than the fact. The Trap now carries the correction and the filed finding.

Retired in compaction pass 7, both closed rather than dropped:

- The ground under the tier-7a co-tenanted variant, that it is the only thing exercising `SocketRuntimeProcess.Close`'s active-set early return. Closed: the shipped `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` (socketruntime_test.go:252-300) exercises the sibling-active early return and the last close deterministically, and this proposal's tier-1 "Co-tenancy hazard" case covers the connected-with-empty-active-set state. The `gatedRuntime` fake holds no connection, child or listener, so the teardown effect is unobservable at tier 7a whichever fixture the case swaps in. The park-placement Open above SURVIVES, because it is about what the variant asserts rather than about which test owns the close.
- The pass-6 residual that the `budget/2` split's mechanism was traced by only one shard. Five independent lenses re-derived the whole chain this window — `Client.Shutdown`'s `time.Duration` fourth parameter, `drainViaLifecycle`, `contextWithGraceDeadline`'s parent-unchanged behaviour for a non-positive grace, `resolveShutdownGrace`'s preference for the ctx deadline, and both `Close` implementations returning nil on the kill arm — and each agreed. The Settled entries carry it; do not re-verify.

Two corrections applied in place rather than retired, kept here so the old forms are recognisable. The bind epoch is minted per admitted bind ATTEMPT, not per registry entry, and the per-entry form's conclusion ("neither the tree the retry staged nor the session it started can be taken down by the previous attempt's reclaim") was false in the ordering the mechanism exists for. The adapter-local property the proposal mints is the **slot-identifier reclaim hold**, windowed from deregistration to the end of the cleanup, not a second sense of "occupancy" starting at entry creation. SUPERSEDED IN PASS 8: the first of those two corrections was itself reversed by the r5→r6 fix pass, which restored per-entry minting and deleted the request-side echo. The per-attempt form is now the withdrawn alternative and is carried in Traps.

Retired in compaction pass 8:

- The OPEN "Can an adapter restart produce an epoch collision?". Closed, and the recorded ground was wrong: the agent podspec sets `RestartPolicy: Never` (pkg/controller/sandbox/podspec/podspec.go:980), so an adapter process cannot restart under a live pod. The no-re-dial rule does NOT carry the case, because `grpc.NewClient`'s `ClientConn` reconnects transparently. Two rounds reached the right conclusion from the wrong reason before the third caught it. CODE-6 additionally seeds the counter from `time.Now().UnixNano()`, which is stronger than the spec requires.
- The UNVERIFIED "How is a `Shutdown` carrying no epoch spelled on the wire?". Closed by the fix it produced: both SPEC-5 blocks now state that the epoch is a positive integer and that zero carries none, and §15.4 gained the missing non-conformance case for an adapter that compares a zero epoch or answers `superseded` for one. That clause is what keeps the §11.4 revoke fan-out, the occupancy-zero recycle edge and `Binder.ReleaseSlot` defined against a conforming third-party adapter.
- The OPEN "`AssignCredentialsResponse.bind_epoch = 1` is a hard field-number collision" and its sibling Trap. Both closed as REFUTED rather than resolved: the message is empty and the field number is correct. Eight lenses filed the correction and one recorded the `awk` artifact that produced the error. The correction is applied in Settled and the Trap now warns against acting on the old form.
- The OPEN "Nothing in the staged spec bounds the reclaim hold on an exclusive pool". Discharged by the r6→r8 rewrite: the hold paragraph now states its own three-way bound independent of §5.2's concurrency-scoped formula, and the edge-case bullet states the same three-way bound, so the two sites agree. The lens that opened it retired it.
- The OPEN "The epoch-reporting RPC set disagrees between the two staged blocks". Closed twice over: the r1→r2 fix replaced §4.7.1's wide predicate with the same closed seven-item list §15.4 carries, and the r6 revert then removed the request side entirely.
- The OPEN "The hold is stated with TWO different start points in the same document" and its later third site. Closed: §5.2, §6.2, §7.1 and §15.4 now agree word for word on both endpoints (the deregistration of the registry entry, to the cleanup finishing), and §6.2's own statement was replaced by a pointer so it cannot desynchronise again.
- The OPEN "Is `examples/runtimes/echo/` an adapter rather than a runtime binary?". Moot: `examples/` does not exist in the tree at all, so spec/15:1466 names a path with nothing behind it and there is no unstaged edit site. Pre-existing spec fiction, not 0081's.
- The OPEN "§4.7.1's caller rule may be wider than it needs to be for the two-step create path". Dissolved by the r6 revert, which deleted the request-side epoch the rule was about.
- The DEFERRED on the request side of the bind epoch (seven request fields, seven handlers, the client echo, a tier-3 echo case, and the field counts that would have become sixteen), and its §7.4 mid-session extension. Both dissolved by the r6 revert: no bind-sequence request carries an epoch, so there is nothing to wire, "nine fields" is correct again, and the mid-session upload needs no rule of its own. Recorded here rather than dropped, because the spec text those entries were written against was real for two rounds and the checklist still carries steps written for it.

Retired in compaction pass 9, all closed rather than dropped:

- The OPEN "Does the §7.1 compensation need an explicit exemption from the §10.1 generation fence?". Answered NO by the `f1.open-decisions.14` firing and deleted from the summary as open decision 14. A generation-stale rejection is a reclaim the adapter did not answer, which staged §7.1 already dispositions, and §10.1:66-68 tells such a replica to cancel and not retry, so an exemption would let a stale replica keep driving the pod. The unenforced fence is now a shipped-defect row.
- The OPEN "Does a `superseded` successor running on the predecessor's tree want a sentence?". Answered NO by the `f1.open-decisions.13` firing and deleted as open decision 13. The premise is false for `superseded`, whose definition requires the predecessor's entry to have been RELEASED, and a release runs the cleanup that removes the tree; the genuinely-inherited case is adoption of a surviving entry on the create-time-reserved path, which §5.2's policy does not place, and it is a shipped-defect row.
- The OPEN "Can an adapter restart produce an epoch collision?", re-retired with its ground restated once more because two rounds recorded the wrong one and a third caught it. The no-re-dial rule does NOT carry the case; `grpc.NewClient`'s `ClientConn` reconnects transparently. `RestartPolicy: Never` on the agent podspec is the ground, and CODE-6 additionally seeds the counter from `time.Now().UnixNano()`.
- The OPEN "The epoch-reporting RPC set disagrees between the two staged blocks", closed a second time. The r1→r2 fix gave §4.7.1 the same closed seven-item list §15.4 carries, and the same fix's exhaustiveness clause then wrongly excluded `Shutdown`; both blocks now carve `Shutdown` in. The predicate the later `spec.1` findings quote ("every RPC in the tables above that can create or resolve a slot registry entry") no longer exists anywhere in the proposal.
- The OPEN "How is a `Shutdown` carrying no epoch spelled on the wire?", and the §4.1 presence question with it. Both blocks now state that the epoch is a positive integer and that zero carries none, §15.4 gained the zero-comparison non-conformance case, and §4.1's epoch sentence was deleted outright because a bare proto3 scalar cannot engage a presence rule.
- The FILED "`ConfigureWorkspace` is documented idempotent while the admission rules refuse an epochless request onto a started entry". Dissolved by the r6 revert, which deleted the request-side epoch the admission rules were about. Two lenses had already refuted it under the r5 design.
- The two-start-points hold window, closed at its third site. §5.2, §6.2, §7.1 and §15.4 now agree word for word on both endpoints, and §6.2's own statement was replaced by a pointer so it cannot desynchronise again.
- The DEFERRED against the CODE-4 comment at non-spec-changes.md:558-560, which repeated the withdrawn "the state the failed attempt created is already gone" ground. It was in scope for the same fix that corrected the four spec sites and landed with them; its `return false` conclusion was correct throughout and stands.

Retired in compaction pass 10, all closed rather than dropped:

- The OPEN "Does the hold survive its own timeout?". Answered by the `f1.open-decisions.human-decisions.12` firing and deleted from the summary as open decision 12: the hold ends when the cleanup pass that took it RETURNS, whether that cleanup succeeded or failed. No staged text was edited, because the staging already states the terminal twice in §5.2, mirrors it in §15.4, and stages an unconditional `defer release()`. The two terminals that looked like they collided are different objects, and adding a `leaked` clause to the hold paragraph is barred by this log's own standing DECISION.
- The OPEN "Does CODE-4's `*SlotBindError` fold need a matching `isTransientPodClaimError` arm?". Answered YES and fixed: `non-spec.1.fix-G2.1` adds one arm keyed on the gRPC status code, owned by CODE-5, which also covers CODE-2's `Resume` rollback. The durable statement, including why the sentinel-matching alternative is unimplementable and where the test belongs, is in Settled.
- The OPEN "S1 lands spec text that cites a §4.7.1 statement S5 lands later". Dissolved by the resequence: SPEC-5 now leads at S1, so the §4.7 row's pointer resolves the moment the row lands at S2. The one citation the order cannot put first is SPEC-5's own pointer at §5.2's reclaim hold, which S1's line states so the residual is visible rather than silent.
- The OPEN "`compensateFailedSlotBind`'s signature on the resume path", which five lenses had recorded and none filed. Filed and fixed: the parameters narrow to scalars, matching the precedent CODE-5 had already set for `accountSlotFailure`. The rejected alternatives are in Settled so the synthetic-request form is not re-derived.
- The two spec-map OPENs, "`tests/spec-map.json`" and "The tier-7a case names no target file". Both filed as findings and both widened: the amendment added three more orphaned test surfaces beyond the tier-7a file the Opens named, and the registration is now attributed per creating step. The tier-7a case's filename is named in the staging; the map entry is what was missing.
- The UNVERIFIED "Is CODE-6's `Client.DemoteSDK` latch clear worth landing when no reachable trace exercises it?". Closed as landed: CODE-6 stages the clear on `DemoteSDK` and on a `RECLAIMED` reclaim, and a tier-1 case asserts `BindEpoch` answers zero after each. What survives is the narrower Trap that the clear does NOT fire on a plain `Shutdown`/`ShutdownRecycle`, which five lenses judged inert.
- Eleven Deferred entries applied by the three index-and-checklist reconciliation passes and the `non-spec.1.fix-*` groups: the two checklist entries on S9 and S10 (now S12 and S13, both rewritten), the summary's CODE-4 credential-lease line, the `TestValidTransitions_spec_6_2` "Watch out for" bullet, the `pkg/gen` path at all three sites, the CODE-6 `adapterclient` latch entry, the five-steps checklist entry (one repaired, four already true against the reverted staging), the open-decisions preamble, the design-narrative mirror entry, the CONF-1 and tier-3 entry, and the tier-3 case titled `Shutdown with the field unset`, which no longer survives in the file.
- Two Deferred entries discharged by a staged deliverable rather than by an edit: `docs/reference/adapter-contract.md:75` and its `:64` sibling. DOCS-2 now exists and folds into S6, after eleven lenses filed :75 independently across six loops. It was the single most-filed, least-acted item on the proposal.
- One Deferred dissolved: the summary's open-decision-9 framing entry. Entry 9 has left the section, and only the past-tense closure prose under `## Decisions` remains, which the entry itself states is correct.

One item retired as MOOT rather than closed, kept so nobody re-derives it: the standing Open on `examples/runtimes/echo/` stays retired, and this window added the reason spec/15 is self-inconsistent about it as well as wrong about the tree — :1466 calls it a reference implementation of the ADAPTER while :1491 and :1812 describe it as a RUNTIME writing JSONL to stdout. Pre-existing spec fiction, not 0081's.

## Ledger

### [spec.2.review-applicability.1]

DECISION: Returned an empty findings list — BECAUSE a full mechanical re-run of the applicability
sweep (every verbatim anchor grepped with `grep -cF` for uniqueness, every insertion point read in
the target file, every markdown anchor resolved, both legs checked on all three relocations) came
back clean, and every residual candidate I derived was already adjudicated in the standing context.
ALTERNATIVES: filing the §5.2-vs-§15.4 `Shutdown`-is-not-held predicate mismatch (already refuted
five times, review-log.md:3012); filing the S1/S5 forward reference (already OPEN and barred,
review-log.md:697, and now stale because the checklist puts SPEC-5 at S1).

FACT: the round-2 snapshot diff is EMPTY outside the review log. `diff -ru -x '*review-log*'
scratchpad/cp-snap/.../spec-r2 proposals/0081_...` produces no output, so no spec-staging text
changed between round 1 and round 2 of this loop. Per the standing trap, this is not evidence that
nothing needs finding; it does mean a round-2 lens has no "newest text" to prioritise and must
re-run its own sweep.

FACT: all ten verbatim anchors the staged spec edits quote are present exactly ONCE in their target
files, verified with `grep -cF`. EVIDENCE: spec/04_system-components.md:157 (§4.1 third sentence),
:686 (`Shutdown` row opening), :854 (§4.7.9 step 5); spec/05_runtime-registry-and-pool-model.md:453
(`**Scrub model.**`), :545 (`**Slot cleanup:**` action list), :555 (`**Max retries:**`);
spec/06_warm-pod-model.md:152 (`receiving_uploads ──→ running`), :234 (mid-resume cancel clause);
spec/07_session-lifecycle.md:23 (atomicity parenthetical), :210 (§7.2 preamble), :213 (step-2
fragment), :214 (step 3), :414 (§7.3 list tail).

FACT: every markdown anchor the staged text writes resolves to a live heading. Checked
`#471-role-and-gateway-rpc-contract`, `#479-startup-sequence-for-type-agent-runtimes`,
`#1542-rpc-lifecycle-state-machine`, `#49-credential-leasing-service`, `#47-runtime-adapter`,
`#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#71-normal-flow`,
`#73-retry-and-resume`, `#74-upload-safety`, `#101-horizontal-scaling`, `#151-rest-api`.
`#1542-...` is the one NOT listed in spec/README.md; it is nonetheless live and already used at
spec/15_external-api-surface.md:2017 and :2355, so do not file it.
EVIDENCE: spec/README.md:28,:36; spec/15_external-api-surface.md:1686,:2017.

FACT: the three insertion points are unambiguous in the current tree. SPEC-5's §4.7.1 block lands
between the `*Adapter → Gateway RPCs:*` table (spec/04:688-693) and `#### 4.7.2` (:695), with
nothing else between them. SPEC-5's §15.4 blocks land between the `**SDK-warm demotion contract:**`
paragraph (spec/15:1469) and `#### 15.4.1` (:1471). SPEC-4's prose lands between the fence close
(spec/06:156) and `**`reserved` hold semantics.**` (:158).

FACT: the §7.1 atomicity paragraph (spec/07:23) sits INSIDE the fenced block that spans
spec/07_session-lifecycle.md:5-54, and it already carries rendered-as-literal markdown links. The
new SPEC-2 paragraph therefore lands inside a code fence too. That is pre-existing convention at
this site, not a defect the proposal introduces; do not file it.

FACT: the tier-11 gate that reads the §6.2 per-slot fence
(`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-75`) checks a fixed
four-edge list and asserts the concurrency-scoped block does NOT contain those four. SPEC-4's new
`receiving_uploads ──→ slot_cleanup` edge is in neither list and lands in the general block, so the
gate stays green. `tests/tier11_docs/spec_47_rpc_row_naming_test.go` extracts row names with
`^\|\s*`([A-Za-z]\w*)`\s*\|`, which SPEC-1's replacement row still matches, and SPEC-5's inserted
§4.7.1 prose declares no table row. Neither gate needs a disposition.

FACT: `ShutdownRequest`/`ShutdownResponse`/`exited_cleanly` appear in `spec/` at exactly one site,
spec/04_system-components.md:157, so SPEC-1's §4.1 edit is the whole spec-side surface for the
message's field-set prose. `Shutdown` as an RPC name appears additionally at spec/05:459,
spec/11_policy-and-controls.md:263 and spec/29:696, and `Shutdown` appears in no §28 register row
(grep of spec/28 returns nothing), which confirms the "deliberately untouched" §28 entry.

WATCHOUT: the standing-context Open "S1 lands spec text that cites a §4.7.1 statement S5 lands
later" (review-log.md:697) and the WATCHOUT at review-log.md:2891 are now STALE. The implementation
checklist puts SPEC-5 at S1 and SPEC-1 at S2, with S2 declaring `Depends on: S1`, so the §4.7 row's
pointer at §4.7.1's outcome enumeration resolves the moment the row lands. A future round should
retire those two entries rather than re-derive the forward reference.
EVIDENCE: 0081_...implementation-checklist.md:5-9.


### [spec.2.review-citations.1]

DECISION: returned an EMPTY findings list — BECAUSE every concrete citation in
`.spec-changes.md` resolved against the tree, and the round's own delta is two sentences.
ALTERNATIVES: filing the residual "unacknowledged reclaim" wording at spec-changes.md:192
(rejected: already adjudicated below the bar by a prior refutation, and it is non-normative
narrative, not staged spec text); filing §4.7.1's "the generation ... is validated on the RPCs
that carry it" against spec/10:30's "Pods validate the generation on every gateway→pod RPC"
(rejected as a soft narrowing of a pre-existing spec-vs-proto overstatement, not a contradiction
the edit creates); filing "the row keeps its §4.6 pod binding" at spec-changes.md:167 (rejected:
§4.6 does own claim binding states, the reference is loose rather than false).

FACT: the round-2 delta against `scratchpad/cp-snap/.../spec-r1-prefix` is EXACTLY two
sentences, both the no-re-dial rationale — spec-changes.md:390 (§7.1 staged paragraph) and
:681 (§4.7.1 caller rules). Nothing else in the file moved. Note the brief named `spec-r2` as
the snapshot; `spec-r2` is byte-identical to the live proposal (it is taken at this round's
start), so `spec-r1-prefix` is the snapshot that actually differs. EVIDENCE:
scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/ mtimes.

FACT: the mechanical anchor sweep is still clean after the fix. A 10-line python pass over the
31 fenced blocks of `.spec-changes.md` against `spec/*.md` gives count 1 for the 15 anchor
blocks (indices 0,2,5,8,10,12,14,16,18,20,22,24) and count 0 for every replacement/insertion
block, each anchor unique repo-wide. Re-run it rather than eyeballing.

FACT: the concrete file:line citations inside `.spec-changes.md` are a SHORT list and all
resolve — `schemas/lenny-adapter.proto:1630-1635` (coordination_generation, field 6),
`spec/04_system-components.md:151` (scope derived from field set), `:157` (the ShutdownRequest
paragraph), `spec/29_communication-scenarios.md:586-588` (Preconditions, "so the runtime is
running"), `:589-591` (interrupt-path addition), `:697` ("adapter closes the session runtime"),
`:669-674` (step 10's terminate clause citing §15.1). Derive the list with
`grep -no "(\`[^\`]*:[0-9][^\`]*\`)"`; it is seven sites, not dozens.

FACT: the two shipped-code attributions SPEC-3 leans on are both true.
`slotlayout.RemoveTree` iterates `{slotRoot, Sessions, Artifacts, CredentialsDir}`
(pkg/adapter/slotlayout/tree.go:58-69) and `deregisterSlotLocked` cancels every armed provider
timer before deleting the entry (pkg/adapter/slotsession.go:174-182). spec/05:455's recycle
lifecycle already says `cleanupCommands` run "after every ended session's per-slot tree and
credential lease have been removed", so SPEC-3's first anchor is a catch-up, not a new
obligation.

FACT: "the whole-pod replacement trigger stated below" and "the **Slot cleanup:** bullet below"
in SPEC-3's append are both directionally correct: the `**Scrub model.**` paragraph is
spec/05:453, the `**Slot cleanup:**` bullet is :545, the `**Whole-pod replacement trigger:**`
bullet is :561.

WATCHOUT: the §5.2 exclusion is keyed on "reclaim did not complete", while §7.1 concludes
"an attempt that meets the hold is one that policy did not place". The bridge is that a
COMPLETED reclaim's answer arrives only after the cleanup finished (the handler blocks on
`removeSlotTree`), so the hold is released before the retry can be placed. The one interleaving
that breaks the bridge needs TWO `Shutdown`s for one session — a second one answers `absent`
immediately while the first one's cleanup is still running, which is a completed reclaim that
does not exclude the pod. I judged it unreachable (the gateway sends one compensation and
blocks on it) and did not file. A later round wanting to reopen this must first show a second
reclaim for the same session on the same pod.

USEFUL [Standing context 233/75]: the mechanical anchor-sweep recipe and the "anchor on quoted
strings, never on line numbers" rule saved the whole round; every stale finding in the refuted
list is a line-number quote against a superseded revision.


### [spec.2.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens on the staged spec edits — BECAUSE every client-facing representation the amendment touches is either (i) inside the staged spec text and mutually consistent, or (ii) in a carrier this loop may not edit (proto, SDKs, docs) — ALTERNATIVES: I considered filing the `docs/reference/adapter-contract.md` Shutdown-row drift and the §15.4-omits-`exited_cleanly` completeness gap; the first is out of this loop's scope and the second is a close variant of an already-refuted finding.

FACT: the new epoch/outcome vocabulary has NO parallel representation anywhere outside the staged proto. `grep -rn "expected_bind_epoch\|SlotReclaimOutcome" spec/ docs/ schemas/ sdks/` returns zero hits, so a "mirror the field into surface X" finding has no target. — EVIDENCE: schemas/lenny-adapter.proto (no such symbol), sdks/ (no such symbol)

FACT: there is no `ShutdownRecycle` RPC on the adapter service; the recycle disposition is a field on `ShutdownRequest`, so `Shutdown` is the single RPC the epoch precondition has to cover and the §4.7.1 "one request that carries a bind epoch" sentence is exhaustive. — EVIDENCE: schemas/lenny-adapter.proto:206 (`rpc Shutdown(ShutdownRequest) returns (ShutdownResponse)`); spec/04_system-components.md:686 (the recycle disposition rides the same row)

FACT: the outcome vocabulary is consistent across all four staged sites. `reclaimed` / `superseded` / `absent` are defined identically in the §4.7 `Shutdown` row (spec-changes.md:322), §4.7.1 (:679), §7.1 (:390) and §15.4 (:707), and the epochless escape hatch reports `reclaimed`/`absent` at :322. Do not re-derive this; it was checked value by value. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:322,:390,:679,:707

FACT: §15.4's two insertion points and the artifact list they sit in are real and unedited — §15.4 already names `schemas/lenny-adapter.proto` as the published gateway↔adapter artifact and its bullet enumerates no message field set, so nine additive fields and one enum falsify no bullet there. The `UNIMPLEMENTED` precedent in the SDK-warm demotion paragraph is why `ABORTED` in the new block is locally conventional. — EVIDENCE: spec/15_external-api-surface.md:1462-1470 (artifact list), :1472 (SDK-warm demotion contract), :1686 (#### 15.4.1 boundary)

FACT: §15.4.2's RPC lifecycle state machine is a five-state adapter-PROCESS machine with no per-RPC admission table, so the new mid-cleanup `ABORTED` refusal cannot falsify it and it is not an edit site. — EVIDENCE: spec/15_external-api-surface.md:1686-1706

DEFERRED [docs/reference/adapter-contract.md]: line 75's `Shutdown` row is falsified by SPEC-1 and SPEC-3 and is in no edit list. It states "The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`" as unconditional. After SPEC-1 the runtime teardown runs only for a session whose start the adapter admitted, and after SPEC-3 a cleanup on the pre-`running` path reports no outcome at all; the row also does not mention the bind-epoch precondition the §4.7 row now states. What is true instead: the flush and the runtime close are gated on an admitted start, the slot release is not, and the cleanup-outcome report is owed only by a cleanup reclaiming a slot the shared runtime process was given. The proposal's only docs deliverable is DOCS-1 (`docs/reference/state-machines.md`), so this row has no owner. The standing context already records that no tier-11 gate catches it (review-log.md Settled, `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts four substrings that all survive SPEC-1), so it will not turn red on its own. — EVIDENCE: docs/reference/adapter-contract.md:75; proposals/0081_.../0081_....spec-changes.md:322,:599

USEFUL [Settled: "The adapter proto has no parallel client representation to keep in step"]: it named the exact greps that close the whole mirror question for this lens and saved a full sweep of `sdks/`, `pkg/gateway/externalapi/openapi/openapi.json` and the CRDs. Re-verified rather than trusted; both greps still return empty.

USEFUL [Settled: "`pkg/gateway/externalapi/openapi/openapi.json` carries no error-code enumeration at all"]: this is the reason no gateway error-path change on this proposal can ever be an OpenAPI edit site. Note the path — several lens briefs, including mine, give `pkg/gateway/openapi/openapi.json`, which does not exist.


### [spec.2.review-docs-alignment.2]

DECISION: returned an empty findings list — BECAUSE this loop admits only findings whose fix
lands in `spec-changes.md`, and both of the docs-alignment lens's in-scope shapes are closed
here: the docs-mirror shape's only live site is already owned by the non-spec/docs loop, and
the "accepted failure mode lands in no staged spec text" shape has now been filed twice and
refuted twice by the material skeptic (recorded at `spec.7.review-docs-alignment.1`).
ALTERNATIVES: I re-derived and killed (a) the `lenny_adapter_leaked_slots` mention SPEC-3
adds, (b) `docs/reference/adapter-contract.md:75`/`:64`, (c) the four residue bullets whose
outcome appears only in the proposal's own edge-case narrative.

FACT: this window's snapshot layout is NOT the previous window's. The r1 fix ran between
`spec-r1-prefix` (15:04) and `spec-r2` (15:21); `spec-r2` and `spec-r2-start` are
byte-identical to the live proposal, so the brief's named snapshot yields an empty diff. Diff
against `spec-r1-prefix` instead. EVIDENCE:
scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r1-prefix

FACT: the whole r1→r2 delta is TWO hunks and both are the same rewrite of the no-re-dial
RATIONALE — the staged §7.1 paragraph's clause and the §4.7.1 caller rule's clause both move
from "a compensation that dialled a fresh connection would still hold the epoch it observed"
to "would hold none on that connection and would send the unconditional teardown at a pod a
successor may already hold", plus the matching narrative paragraph under SPEC-2. No new
mechanism, no new identifier, no new default, no new error code, no new metric or alert. Under
this lens the round's delta therefore has no docs surface at all.
EVIDENCE: proposals/0081_.../0081_....spec-changes.md:390,:398-400,:682

WATCHOUT (checked, not filed, and here is why so it is not re-derived): staged §7.1 now gives
the no-re-dial rule a ground — "a reclaim that dialled a fresh connection would hold none and
would send the unconditional teardown at a pod that may already hold a successor's slot" —
two sentences before it SANCTIONS exactly that act: "An attempt that holds none ... sends the
unconditional form." The two are reconcilable (the rule bars manufacturing an avoidable
unfenced teardown; the later sentence covers the unavoidable case, which the "bind that fails
inside its first entry-creating RPC" residue bullet already owns), and this ground has been
rewritten twice already after two findings against its predecessors. A round that wants it
must argue the two sentences cannot both hold, not that the pairing reads oddly.
EVIDENCE: proposals/0081_.../0081_....spec-changes.md:390

USEFUL [Traps, "Do NOT conclude 'nothing changed' from an empty diff against the snapshot the
brief names"]: it is the only reason I found this round's delta at all. It now needs one
extension: in THIS window the newest differing snapshot is a `-prefix` directory rather than a
numbered round, because the fix stage runs between the prefix snapshot and the next round's
snapshot. `ls -la --time-style` over the whole cp-snap directory sorted by mtime is the
reliable way to find it.

USEFUL [standing context, DEFERRED docs/reference/adapter-contract.md:75 and :64]: together
they cover the entire live docs-mirror surface of this change (the `Shutdown` row's
one-teardown contract, and the `DemoteSDK` row after SPEC-5 makes the demotion the act that
drops the caller's epoch). Both are docs-lane. A docs lens in a spec-only loop should read
those two entries first and expect to return empty.


### [spec.2.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site lens on round 2 — BECAUSE
`diff -rq` against the r2 snapshot shows the ONLY file that changed since the last round is
the review log (compaction pass 9); `...spec-changes.md` is byte-identical to the snapshot.
I therefore re-ran the whole lens from scratch rather than reading a diff. ALTERNATIVES:
filing the marginal items listed under WATCHOUT below; each fails the default-to-refuted bar.

FACT: every "reads, verbatim" anchor in the staged spec edits resolves EXACTLY ONCE in the
tree. Verified with `grep -Fc` on all twelve: §4.1 third sentence, the §4.7 `Shutdown` row
opener, the §7.1 rollback parenthetical, the §7.2 preamble premise sentence / step-2 tail /
step 3, the §7.3 list item 4, the §6.2 `resuming → cancelled` clause, the §5.2 `Max retries:`
sentence, the §4.7.9 step 5 line, the §5.2 `Slot cleanup:` action sentence, the §5.2
`**Scrub model.**` paragraph, and the §6.2 `receiving_uploads ──→ running` fence entry.
EVIDENCE: spec/04_system-components.md:157, :686, :151; spec/07_session-lifecycle.md:23,
:210, :213, :214, :414; spec/06_warm-pod-model.md:152; spec/05_runtime-registry-and-pool-model.md:545, :555.

FACT: every insertion point named by the staged edits exists and is unambiguous.
§4.7.1's `*Adapter → Gateway RPCs:*` table closes at spec/04_system-components.md:693 and
`#### 4.7.2` opens at :695. §15.4's `**SDK-warm demotion contract:**` paragraph is at
spec/15_external-api-surface.md:1469 with `#### 15.4.1` at :1471. §6.2's fence closes at
spec/06_warm-pod-model.md:156 with `**`reserved` hold semantics.**` at :159. §29.4 step 13
ends at spec/29_communication-scenarios.md:711 with the cited `§15.4.3 …, §28.5.3).` tail.

FACT: every markdown anchor the staged text mints is already in use elsewhere in spec/, so
none is a fresh guess: `#471-role-and-gateway-rpc-contract`, `#479-startup-sequence-for-type-agent-runtimes`,
`#1542-rpc-lifecycle-state-machine`, `#49-credential-leasing-service`, `#74-upload-safety`,
`#73-retry-and-resume`, `#71-normal-flow`. Checked with a repo-wide `grep -o` count over spec/*.md.

FACT: the "deliberately untouched" claims about §28 hold. `Shutdown` (the gateway→adapter RPC)
appears in NO §28 row; §28's `shutdown`/`terminate` hits are the intra-pod JSONL and
CH-RUNTIMEOPS frames, and neither the message-schema table nor the `**Timing.**` /
`**Degradation.**` bullets state WHEN the adapter writes `terminate`, so the §4.7 + §29.4
co-tenant gating needs no §28 edit. EVIDENCE: spec/28_communication-channels.md:1082, :1100, :1119-1127.

FACT: SPEC-3's clause "and is surfaced on the `lenny_adapter_leaked_slots` gauge" survives
the withheld-report rule. The gauge is NOT fed only by `ReportSessionScrub`: the gateway
drives it from a shared fail/leak tracker that the slot-BIND-failure path also writes.
EVIDENCE: cmd/lenny-gateway/sessionsrv.go:372-383 ("the shared fail/leak tracker so the
slot-bind-failure path and the §4.7 scrub-report drain ledger accumulate in one rolling
window"), pkg/gateway/sessionserver/start.go:2736 (`applySlotRetryPolicy(... s.slotHealth,
... s.slotLeakGauge ...)`). A reviewer who assumes the §4.7 `ReportSessionScrub` row is the
sole input will file a false finding here.

FACT: SPEC-3's `**Slot-identifier reclaim hold.**` sentence "A release that runs its cleanup
inside the RPC that requested it ends the hold before that RPC answers, so the pod-warm bind
sequence that follows an SDK demotion does not meet it" is true of the tree: `DemoteSDK`
calls `noteRuntimeClosed` + `releaseSessionSlot` synchronously before it returns.
EVIDENCE: pkg/adapter/sdkwarm.go:274-302.

WATCHOUT: three marginal items I examined and deliberately did NOT file. Do not re-derive them
from scratch. (1) §5.2's hold says the adapter "admits no request that would create or resolve
a registry entry" while §15.4 carves out `Shutdown` ("`Shutdown` is not held") — reconciled by
reading "create or resolve" as §4.7.1's bind-sequence vocabulary, and by the fact that during
the hold no entry exists to resolve. EVIDENCE: spec-changes.md:601 vs :711. (2) The edge-case
bullet at spec-changes.md:156-158 says a pre-`running` bind failure is recorded by the windowed
failure counter, but §6.2's fence reaches `failed` only via `running ──→ failed`
(spec/06_warm-pod-model.md:147) — the gap is PRE-EXISTING (shipped `recordSlotFailure` at
pkg/gateway/podlifecycle/podsession/slotbinder.go:358 already does this today) and the proposal
neither creates nor is obliged to close it. (3) `superseded` already exists as a
`manifest_reason` enum value on the partial-checkpoint manifest (spec/10_gateway-internals.md:157,
spec/16_observability.md:198) — a different enum on a different message, no collision.

FACT: §4.1's citation `schemas/lenny-adapter.proto:1630-1635` for the `coordination_generation`
precedent is exact: that is the comment block plus `int64 coordination_generation = 6;` inside
`ShutdownRequest` (which opens at :1609). `ShutdownResponse` is at :1665 with only
`exited_cleanly = 1` / `exit_code = 2`.


### [spec.2.review-feasibility.1]

DECISION: returned an EMPTY findings list for the actor-action-feasibility lens on the r2 spec staging — BECAUSE every actor assignment in the staged text resolves against the tree, and every candidate I built collapsed onto a standing Trap or a standing Open — ALTERNATIVES: I considered filing (a) §4.7.1's mixed per-connection / per-session caller-rule granularity, (b) §5.2's hold sentence "a bind refused this way is accounted by the gateway as an ordinary transient slot failure" against the §7.3 client resume that emits no `lenny_slot_failure_total`, and (c) §7.1's "the failed attempt releases the pod's claim" against `Binder.Resume`'s no-op release. All three are already carried as FILED/UNVERIFIED entries in `## Standing context ### Open` (items on the caller rule, on the hold-refusal accounting, and on the resume-path pod release), so re-filing them costs two verifiers and closes nothing.

FACT: every one of the seven bind-sequence RPCs §4.7.1 enumerates exists in the shipped §4.7 Gateway→Adapter table AND has a distinct response message in the proto, so the staged "adds a reporting field to each of the seven bind-sequence responses" is buildable as written — EVIDENCE: spec/04_system-components.md:667-684 (table rows `PrepareWorkspace`…`Resume`); schemas/lenny-adapter.proto:699 `PrepareWorkspaceResponse`, :761 `FinalizeWorkspaceResponse`, :869 `RunSetupResponse`, :958 `StartSessionResponse`, :1033 `AssignCredentialsResponse {}`, :1433 `ResumeResponse`, :1690 `ConfigureWorkspaceResponse`.

FACT: `ShutdownRequest`'s free field number is 7 and `ShutdownResponse`'s is 3. Used: `session_id=1`, `reason=2`, `deadline_ms=3`, `reserved 4` (+`reserved "slot_id"`), `recycle=5`, `coordination_generation=6`; response holds `exited_cleanly=1`, `exit_code=2`. The §4.1 rationale's citation `schemas/lenny-adapter.proto:1630-1635` for the `coordination_generation` comment-plus-field is EXACT — EVIDENCE: schemas/lenny-adapter.proto:1609-1668.

FACT: `RunSetup` really does resolve a registry entry, so it can report an epoch. It calls `s.ensureSlotPaths(sessionID)` at the top of the handler, before the setup work — EVIDENCE: pkg/adapter/staging.go:336-341, and `resolvePrepareStagingDir` does the same at :429-431.

FACT: shipped `PrepareWorkspace` resolves the slot identifier exactly once per call (`if stagingDir == ""` guard inside the recv loop) and sends one response, so §15.4's two new `PrepareWorkspace` non-conformance clauses ("mints more than one epoch for one call", "resolves the slot identifier more than once within one call") are satisfied by the first-party adapter as it stands — EVIDENCE: pkg/adapter/staging.go:48,:75-82,:114-117.

FACT: `DemoteSDK` really removes the registry entry, which is what §4.7.1's caller rule ("It holds none once it has itself issued an RPC on that connection that removes the entry the epoch names") rests on, and it does the release SYNCHRONOUSLY inside the RPC, which is what §5.2's "a release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers" rests on — EVIDENCE: pkg/adapter/sdkwarm.go:296-301 (`anyRegisteredSession` → `noteRuntimeClosed` → `releaseSessionSlot`).

FACT: the shipped `Shutdown` handler returns AFTER `removeSlotTree` and `reportSessionScrub`, so a hold released on a `defer` is released before the response reaches the gateway. That is what makes the edge case "an acknowledged reclaim is answered only after its cleanup finished" true, and therefore what keeps a §5.2-placed retry off the hold — EVIDENCE: pkg/adapter/session.go:236-292 (deregister at :238, `removeSlotTree` at :270, return at :292).

FACT: the §29.4 step-13 anchor is at spec/29_communication-scenarios.md:711 (step body :705-711), and the §29.4 `**Preconditions.**` paragraph the SPEC-1 rationale leans on is at :585-591 with "so the runtime is running" on :586. Step 10's "valid in any non-terminal state" is at :669-674. All three rationale citations resolve.

FACT: `ShutdownRequest`/`ShutdownResponse`/`exited_cleanly` appear in `spec/` and `docs/` at exactly ONE site, spec/04_system-components.md:157, so the added `expected_bind_epoch` field falsifies no other prose enumeration of that message's field set. `grep -rn "ShutdownRequest\|ShutdownResponse\|exited_cleanly" spec/ docs/` returns that one line.

FACT: the mechanical anchor sweep is still clean at r2. Extracting the fenced blocks after `## Staged edits` and counting each across `spec/*.md` gives count 1 for blocks 0,2,5,8,10,12,14,16,18,20,22,24 and 0 for every replacement/insertion. Reproduced again this round; the standing Trap saying not to re-run it by hand is right, the ten-line script is enough.

WATCHOUT: `spec/04_system-components.md`'s `Shutdown` row and the whole Gateway→Adapter RPC table physically sit INSIDE `#### 4.7.1 Role and Gateway RPC Contract` (heading :659, table :665-686, `#### 4.7.2` at :695), not directly under `### 4.7 Runtime Adapter` (:657). The proposal calls it "the §4.7 `Shutdown` row" throughout and links `#47-runtime-adapter`. That is loose rather than false (4.7.1 is inside 4.7) and it has survived every sweep; do not file it, and do not "correct" the links, because SPEC-5's own block lands in the same subsection and the two edits stay adjacent either way — EVIDENCE: spec/04_system-components.md:657,:659,:686,:695.

WATCHOUT: `#### Request Message Scope`, the SPEC-1 §4.1 target, is a subsection of `### 4.1 Edge Gateway Replicas` (:40) even though its subject is the gateway-adapter protocol and its verbs are the ADAPTER's handler. A feasibility reviewer who reads "§4.1" as gateway-only will misread the replacement sentence's subject; the shipped third sentence already says "The handler runs …", so the staged sentence introduces no new actor — EVIDENCE: spec/04_system-components.md:40,:149,:157.

USEFUL [Traps, "the glob `*spec-changes.md` matches BOTH files"]: saved a wasted read; every sed in this shard used the full filename.
USEFUL [Traps, "DELETED AND REPLACED … `AssignCredentialsResponse` is a ONE-LINE message and awk ranges run on"]: I used `grep -n "^message X"` per message instead of an awk range and got the field picture in one command.
USEFUL [Traps, #227 "do NOT conclude nothing changed from an empty diff against the named snapshot"]: `spec-r2` and `spec-r2-start` are byte-identical to the live `spec-changes.md`; the newest snapshot that actually differs is `spec-r2-prefix`. Diff against that one to see round 1's fix hunks. Note also that `spec-r3` … `spec-r9` in `scratchpad/cp-snap/0081.../` belong to the EARLIER (pre-amendment) window and are NOT newer than `spec-r2`; ordering the directory listing by name is misleading.


### [spec.2.review-fresh.1]

DECISION: filed exactly one finding, the `/finalize` Gap-2 window against staged §7.1 — BECAUSE it is
the only OPEN item in the standing context that is (a) unambiguously a defect in staged spec text,
(b) still present verbatim in the current staging, and (c) fixable inside `.spec-changes.md`.
ALTERNATIVES rejected this round: the §5.2-vs-§15.4 `Shutdown`-not-held carve-out (three lenses have
already reconciled it on the vacuous-resolve reading, review-log.md:2666,:3012); §4.7.1's
per-connection-vs-per-session caller granularity (filed by spec.8, unreachable in the tree, fail-closed
direction); §15.4's zero-epoch-response clause versus an empty `PrepareWorkspace` stream (unreachable,
declined twice); the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` "not a second bound on the
hold" sentence versus the unedited `**Slot cleanup:**` bullet's "Cleanup timeout is max(...)" (see
below — I judged it reconcilable and a human-facing open question rather than a defect).

FACT: the whole anchor sweep is still clean after the amendment. A python pass over the 31 fenced
blocks of `.spec-changes.md` against `spec/*.md` gives count 1 for every "text to replace" block
(indices 0,2,5,8,10,12,14,16,18,20,22,24) and count 0 for every replacement/insertion. Every markdown
link inside a staged block resolves to a real heading in the file it lands in. Re-running this costs
two minutes; do it rather than eyeballing. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:279,:315,:370,:413,:436,:448,:477,:496,:523,:552,:572,:593

FACT: `Binder.Prepare` closes its adapter connection ON SUCCESS before returning, and the two `/finalize`
Gap-2 branches fail after that. `pkg/gateway/podlifecycle/podsession/binder.go:841` ("The adapter
connection is closed before Prepare returns; Launch reconnects"), the `cl.Close()` at :957;
`pkg/gateway/sessionserver/sessionserver.go:3171-3181` (the `finalizing → ready` Update branch) and
:3190-3197 (the `ConsumeDigest` branch), both calling `s.reclaimFinalizedPod` (finalize.go:379-387) →
`Binder.ReclaimClaimed` (binder.go:1097-1106), which revokes the lease and DELETEs the per-pod claim and
sends the adapter NO RPC.

WATCHOUT: the summary's out-of-scope row at summary.md:506-522 ("The exclusive path leaves the same
residue by a different trigger") looks like it already covers the Gap-2 window and does not. Its trigger
is a bind that SUCCEEDED with "no failure site to hang off"; the Gap-2 branches are a failure site inside
the creation finalize block that staged §7.1 names as one of its three attempt kinds, and the gateway
already runs a compensation there. Do not let that row refute the finding. — EVIDENCE:
proposals/0081_.../0081_....summary.md:517-519

UNVERIFIED: the `**Slot cleanup:**` bullet (unedited, spec/05:545) states "Cleanup timeout is
`max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds" while the staged hold paragraph says
that figure "is the deployer's budget for that cleanup and is not a second bound on the hold" AND that
"the hold lasts as long as the cleanup it covers". If the figure bounds the cleanup and the hold lasts as
long as the cleanup, the figure bounds the hold, so the denial reads as self-inconsistent inside one
paragraph. I did not file it: it is reconcilable (the formula is a budget the adapter does not enforce
today, pkg/adapter has no per-slot cleanup timeout at all) and the review log already carries it as a
human-facing Open ("Does the hold survive its own timeout?"). Whoever wants it should settle whether the
bullet's figure is normative on the adapter before rewriting either sentence. — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:545; proposals/0081_.../0081_....spec-changes.md:601

USEFUL [Standing context, "The proposal's line numbers have drifted roughly EIGHTY lines"]: correct and
still load-bearing. Every stale finding in the refuted list is a line-number read of the 1344047d4
snapshot. Anchor on quoted strings; `grep -c` the exact sentence before filing anything.

USEFUL [Standing context, "The creation finalize block engages the binder only on an EXCLUSIVE pool"]:
this is what localises the Gap-2 window to the decomposed create/finalize/start lifecycle on an exclusive
pool, and it is why the concurrent path (one `cl` held from `PrepareWorkspace` through `StartSession`)
has no equivalent hole.


### [spec.2.review-kubernetes.1]

DECISION: returned zero findings on the spec staging — BECAUSE the staged edits touch no CRD
field, no status subresource write, no finalizer, no admission webhook, and no reconcile loop.
SPEC-1/2/3/4/5 land entirely on the gateway↔adapter gRPC contract (§4.1, §4.7, §4.7.1, §4.7.9),
the §5.2 pool/scrub/cleanup model, §6.2's per-slot sub-state fence, §7.1-§7.3 session lifecycle,
§15.4's published adapter contract, and §29.4's trace. The bind epoch is an in-process int64 in
the adapter and an in-memory latch in the gateway replica; the reclaim hold is an adapter-local
mutex-scoped identifier hold. Neither reaches etcd. ALTERNATIVES: I considered filing
(a) level-triggered-reconciliation, since the whole remedy is an edge-triggered best-effort
compensating RPC with enumerated residues, and (b) controller-on-the-hot-path for the §5.2
placement exclusion pushing a retry to `WARM_POOL_EXHAUSTED` until PoolScaling provisions.
Both are refuted by the standing Settled entries: the level backstops exist and are named
(§4.6.1 orphan GC drains a `bound` claim whose pod no active session references; `draining ──→
terminated` is not occupancy-gated, so a held `leaked` occupancy wedges nothing), and the
exclusion is a per-request in-memory `ExcludePods` field, not a CRD write.

FACT: this proposal has exactly one Kubernetes-idiom surface and it is clean. The gateway owns
both `SandboxClaim.spec` and `.status`; the unhealthy-threshold drain is routed through the
`lenny.dev/drain-request` annotation rather than a `Sandbox.status` write; no component writes
another's status subresource and no SSA force-ownership or finalizer appears anywhere in the
staging. EVIDENCE: spec/06_warm-pod-model.md:158 (`reserved` hold semantics, gateway patches
the claim binding state), spec/06_warm-pod-model.md:160 (`leaked` slot semantics — Redis
slot-counter occupancy, not etcd).

FACT: the amendment's two new mechanisms are both pod-process-local by construction and the
spec says so, which is what keeps them out of this lens. EVIDENCE:
proposals/0081_.../0081_....spec-changes.md:677 ("unique and strictly increasing within one
adapter process, pod-local, and never persisted: it survives neither a pod restart nor a
re-attach onto a replacement pod"); :601 (the hold is "a property of the identifier rather than
of an entry", held across the adapter's own critical section).

USEFUL [Open "Retire the Kubernetes lens for the remaining spec rounds?", review-log.md:713]:
the entry is right and this round is the sixth zero-finding run on this lens against this
staging. The amendment (bind epoch + reclaim hold + proto edit) changed only the gRPC surface,
which is precisely the axis the entry names as not worth a Kubernetes pass. Recommend retiring
the lens for the remaining spec rounds unless a future amendment touches a CRD field, a status
write, a finalizer, an admission webhook, or a reconcile loop.

FACT: the spec staging did not move between the r2 snapshot and the current tree. `diff -rq
scratchpad/cp-snap/0081_.../spec-r2 proposals/0081_...` reports only the review log as differing,
so `spec-changes.md` is byte-identical to what round 1 reviewed. A later round should run that
diff first: if only the log differs, no fix landed and re-reading the whole document buys nothing
beyond what the previous round's shards already record.


### [spec.2.review-mechanism.1]

DECISION: filed exactly one finding, the `/finalize` Gap-2 window against staged §7.1 —
BECAUSE it is the one place where a staged obligation names a path on which it cannot be
discharged, and it is still unrepaired in the current text. ALTERNATIVES rejected:
(a) the §4.7 row's "a precondition on the whole request" against §4.1's flat "runs the
whole-pod scrub when the recycle disposition is set" — a real three-way wording drift
(§15.4's non-conformance list also names only the two teardowns), but §4.7.1's caller rule
forbids a compensation from carrying the recycle disposition, so the combination is
spec-unreachable on a conforming request and the colon in the §4.7 sentence reads as
definitional; (b) §5.2's hold predicate not carving `Shutdown` out while §15.4 does —
standing Open #96, declined four times on the vacuous-resolve reading; (c) the hold's
literal "any request that would create or resolve a registry entry" reaching `Attach`,
`SendMessage`, `Interrupt` etc. — same declined Open.

FACT: the Gap-2 finding is genuinely LIVE, not stale. It was filed by `spec.7.review-mechanism.8`
in the window that ran 05:00-12:45 on 2026-09-10 and no fix group closed it; the current
window's round-1 fixer explicitly stepped over it ("OPEN (not this group's work, already
filed) ... A fixer editing :390 will be standing on that sentence — leave it alone").
EVIDENCE: review-log.md:4014, review-log.md:701

FACT (re-derived from the tree, do not re-derive): `Binder.Prepare` closes its adapter
connection on the SUCCESS path, before it returns, so the finalize block holds no connection
after it. The two Gap-2 branches fail after that and compensate with `reclaimFinalizedPod`,
which is `ReclaimClaimed`: it revokes the §4.9 lease and DELETEs the per-pod claim and sends
the adapter no RPC at all.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:957, :1097-1106;
pkg/gateway/sessionserver/sessionserver.go:3171-3182, :3189-3199;
pkg/gateway/sessionserver/finalize.go:379-387

FACT that closes the "the attempt already succeeded, so the obligation ended" refutation:
staged §4.7.1 itself frames the prepare-and-launch split as ONE bind attempt spanning two
connections ("A bind attempt whose stages span two connections, as the §4.7.9 step-5 bind
sequence does when the gateway splits it across a prepare and a launch"), so a Gap-2 failure
is a failure of the attempt, inside §7.1's window, with no held connection.
EVIDENCE: spec-changes.md:681, :390

WATCHOUT: the summary's out-of-scope row "The exclusive path leaves the same residue by a
different trigger" (summary.md:506-522) looks like it already covers this and does not. Its
stated ground is "the trigger is a bind that succeeded, so CODE-4's compensation has no
failure site to hang off and no open connection to carry a reclaim". The Gap-2 branches ARE
a failure site inside the finalize block §7.1 names, with a compensation the gateway already
runs there. A fixer that repairs §7.1 must also correct that row's first clause.

FACT: the epoch fence only fires where the abandoned attempt's entry was already removed
before the retry created a new one, which in the tree means the adapter's own pre-`Runtime.Start`
failure branches (`releaseSessionSlot` at session.go:133,:147,:157 and resume.go:69-141). Where
the reclaim is still on the wire and has removed nothing, the retry ADOPTS the surviving entry
and inherits its epoch, which is the shared-entry residue the proposal accepts at
spec-changes.md:231-247. Two lenses have now been refuted for filing that as a defect; it is
disclosed, not missed.

FACT: all seven bind-sequence RPCs named in §4.7.1 are unary-response in the proto
(`PrepareWorkspace` is the only client-streaming one and still returns a single response), and
`RunSetup` really does resolve the registry entry (`ensureSlotPaths` at the top of the handler),
so "each of them reports the entry's current epoch on its response" is implementable as written.
EVIDENCE: schemas/lenny-adapter.proto:41-89,:151; pkg/adapter/staging.go:336-341; pkg/adapter/slot.go:140-148

FACT: the snapshot named in the brief (`scratchpad/cp-snap/.../spec-r2`) was byte-identical to
the live proposal again. The only real delta this round is the uncommitted no-re-dial rationale
rewrite at spec-changes.md:390 and :396-400 and :681. Use `git diff` on the proposal file rather
than the snapshot; the snapshot directory also still holds the PREVIOUS window's spec-r1..r9,
which are a different document generation.


### [spec.2.review-operational.1]

DECISION: returned an empty findings list for the operational lens on the staged spec edits — BECAUSE the amendment adds no metric series, no alert, no condition and no CRD status write, and every observability claim the staged text makes resolves against the tree — ALTERNATIVES: I built and dropped four candidates, each recorded below so the next operational reviewer does not rebuild them.

FACT: the round-1 fix delta on `spec-changes.md` is TWO sentences and nothing else. `git diff` against `e17f2da60` shows only the SPEC-2 §7.1 no-re-dial clause and its Design/§4.7.1 twin being re-grounded on the caller's latch. Snapshot `spec-r2` and `spec-r2-start` are both byte-identical to the live file, so the brief's "read the changed sections hardest" has almost no target this round; use `git diff -- <spec-changes.md>` rather than the snapshot directory, whose names are reused across the two runs (`spec-r2-prefix` is from the FIRST run and diffs ~60KB). EVIDENCE: proposals/0081_.../0081_....spec-changes.md:397-401, :678 (git diff HEAD).

FACT: the operational lens is structurally inert on this proposal, re-confirmed against the tree. No alert rule names any slot metric (`grep slot pkg/alerting/rules/*.go` returns nothing); the three `docs/runbooks/` files matching "slot" are Redis hash slots, a Postgres replication slot and a credential path; the adapter registers no bind-refusal or slot-admission metric; the amendment's epoch is an in-memory `int64` and the hold an in-memory map key. EVIDENCE: pkg/alerting/rules/rules.go; pkg/adapter/metrics.go:15-90.

MISTAKE (mine, nearly filed, four times) — the candidates and why each died:
 (1) "SPEC-3 says the pre-`running` leaked slot is surfaced on `lenny_adapter_leaked_slots`, but §6.2:160 calls that gauge adapter-exposed `/healthz` health metadata and the adapter is never told about this leak." Dead: the gauge is gateway-emitted (gatewaymetrics_credential.go:223) and CODE-5's `accountSlotFailure` feeds it on all three bind paths; the adapter-attribution in spec/06:160 is pre-existing spec-versus-code divergence. Standing Trap "Dead end: the adapter cannot see this leak" already covers it.
 (2) "The §5.2 hold refusal is accounted as an ordinary transient slot failure, which at `maxConcurrentSessions: 2` trips `UnhealthyThreshold = 1` and drains a healthy pod for a millisecond-scale benign refusal." The mechanism is real but the staged sentence states exactly that accounting honestly and asserts nothing false; it is a preference between workable designs. EVIDENCE: spec-changes.md:604 ("accounted by the gateway as an ordinary transient slot failure"); pkg/gateway/runtime/slothealth/slothealth.go:56-67.
 (3) "SPEC-3's leak predicate ('the `Shutdown` response does not report a clean exit') is broader than §7.1's ('answered `reclaimed` without reporting a clean exit'), so a `superseded` answer with `exited_cleanly=false` is leaked under §5.2 and unleaked under §7.1." Dead: SPEC-3's clause is governed by its antecedent "A cleanup on that path", and a `superseded` reclaim runs no cleanup, so the two predicates never meet on one event. EVIDENCE: spec-changes.md:600 vs :390.
 (4) "`docs/reference/adapter-contract.md:75`'s `Shutdown` row becomes wrong under SPEC-1/SPEC-3/SPEC-5 (it states the report is filed at every teardown, states the drain gate, and knows nothing of the epoch or the three outcomes) and DOCS-1 targets only `state-machines.md`." Genuine, but its remedy is a docs edit, which this loop may not land. Filed below as DEFERRED rather than as a finding.

DEFERRED [docs/reference/adapter-contract.md]: the `Shutdown` row at :75 will be false once SPEC-1, SPEC-3 and SPEC-5 land. It reads "The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`. When the release leaves the pod holding no other bound session, the adapter also sends the CH-RUNTIMEOPS drain signal." What is true after the edits: the row conflates the slot release and the runtime teardown, whose preconditions the staged §4.7 row splits (entry-held versus start-admitted); the report is WITHHELD for a cleanup on a slot the shared runtime process was never given (SPEC-3); and the request may carry a bind epoch, on which the adapter performs neither teardown when it mismatches, answering one of `reclaimed`/`superseded`/`absent` (SPEC-1, SPEC-5). The `ReportSessionScrub` row's "at each session release, on a pod of any concurrency" stays true, because the proposal frames a pre-`running` reclaim as not a session release. `tests/tier11_docs` does not catch this: `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts four substrings that all survive. The non-spec staging carries only DOCS-1 (`docs/reference/state-machines.md`), so this is an unstaged docs edit site.

USEFUL [standing context entries 231, 299, 365, 366, 418, 419, 451]: the operational sweep was already complete before this round — no alert on any slot metric, the served-session count on no observability surface, `lenny_adapter_leaked_slots` already named twice in `spec/`, the adapter registering no relevant counter, and the two "withheld report contradicts §4.7/§12" dead ends with their five sites enumerated. Between them they closed every candidate this lens could raise on the pre-amendment text in about twenty minutes, leaving the epoch/hold text as the only unswept surface.

UNVERIFIED: whether a §7.3 checkpoint-restore re-attach refused by the reclaim hold keeps the retryability the staged text promises. `isTransientPodClaimError` (pkg/gateway/sessionserver/start.go:3648-3682) matches no bare gRPC status, so a `codes.Aborted` from the hold falls through to `s.failSession` and takes the session row terminal, while spec-changes.md:190 states "The refusal is transient, so the attempt keeps its §5.2 retryability" and names a client-driven §7.3 resume among the callers that can meet the hold. Standing entry 369 records the code fact without anyone filing on it. Whoever owns the NON-SPEC loop should decide whether CODE-4 owes an `Aborted` arm on that path or whether the staged sentence needs scoping; the remedy is code, so this lens could not close it here.


### [spec.2.review-performance.1]

FACT: the staged spec file is BYTE-IDENTICAL to the r2 snapshot. `diff -u scratchpad/cp-snap/0081_.../spec-r2/0081_....spec-changes.md proposals/0081_.../0081_....spec-changes.md` returns nothing; only the review-log changed between the snapshot and HEAD. A performance/scalability lens re-run on this loop has no new surface to read. — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md (unchanged); scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r2/

DECISION: returned an empty findings list — BECAUSE every angle this lens owns on the amendment is already adjudicated in the standing context, and I re-derived each one against the tree rather than trusting the entry. (1) No pod-wide serialization: the hold is keyed on the slot identifier and `slotId == sessionId`, so N concurrent cleanups on one pod refuse N distinct identifiers. (2) No new control-plane write: the amendment writes no CRD status, no Postgres row and no Redis key; it adds one `int64` to seven response messages and one request, and one extra `Shutdown` RPC per FAILED bind, which multiplies against the failure rate rather than against the request rate. (3) `ReportSessionScrub` volume goes DOWN, because SPEC-3 withholds the report on the pre-`running` path. (4) Failover posture is unchanged: nothing here reads or writes Postgres, the `leaked` occupancy still lives in the shipped Redis slot counter under §6.2's own disposition, and §10.1's handoff is an explicit non-impact because the epoch is pod-local and latched per connection. A gateway replica lost mid-bind sends no compensation, which is exactly the shipped behaviour (no compensation exists today), so it is not a regression. — ALTERNATIVES: I considered filing (a) the reclaim-hold refusal feeding the §5.2 windowed `failed` count, where `ceil(maxConcurrentSessions/2) == 1` at `maxConcurrentSessions: 2` means one refusal retires the pod, and (b) the "not a second bound on the hold" disclaimer against spec/05:545's "Cleanup timeout is `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds (minimum 5s enforced at runtime by the adapter)". Both were dropped: (a) was already considered and dropped by round 7's own performance pass (review-log.md:3326) and the identifier-scoping kills the collision harm, and (b) was verified NOT a contradiction because the per-slot cleanup timeout is enforced nowhere in `pkg/adapter` (review-log.md:2993), was refuted in a close variant, and has survived eight sweeps unchanged.

USEFUL [Standing context, review-log.md:2417 and :2425]: the "neither mechanism serializes anything pod-wide" FACT and the exclusive-pool hold-bound pair are exactly where this lens goes first, and both are correct as written. Re-verifying them cost about ten minutes; taking them on trust would have cost nothing more. Do not re-file either.

USEFUL [review-log.md:3248]: the WATCHOUT on the r7 hold-bound rewrite ("The hold lasts as long as the cleanup it covers" beside "is not a second bound on the hold") states the tension honestly AND states the reason not to file it (the held identifier is the session identifier, so no other session collides). That is the entry that stopped me re-filing the same thing a third time.

FACT: the two quantitative claims in the staged text that this lens owns both check out. `ceil(2/2) == 1`, so the edge-case bullet's "at `maxConcurrentSessions: 2` a single windowed failure already reaches the §5.2 whole-pod replacement threshold" is arithmetically right (spec/05_runtime-registry-and-pool-model.md:561), and SPEC-3's two "below" references resolve in document order: the `**Scrub model.**` paragraph is at spec/05_runtime-registry-and-pool-model.md:453, the `**Slot cleanup:**` bullet at :545, the whole-pod replacement trigger at :561. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,:545,:561

FACT: `lenny_adapter_leaked_slots`, which SPEC-3's appended text names, is real and is published by the GATEWAY rather than by the adapter, despite spec/06:160 saying the adapter exposes it. That spec-vs-code drift is pre-existing and outside this proposal; do not chase it here. — EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223; pkg/sandbox/slotstate/registry.go:10,:98; spec/06_warm-pod-model.md:160


### [spec.2.review-reliability.1]

DECISION: Returned an empty findings list for the reliability lens on this round — BECAUSE every
recovery/retry/restart angle I could build against the staged spec text is already adjudicated in the
standing context (refuted, barred, or filed as an OPEN for the human), and `spec-changes.md` is
byte-identical to the round snapshot. ALTERNATIVES rejected, each with where it died: (a) gateway
replica dies mid-bind so the compensating `Shutdown` is never sent and the co-tenanted-pod entry has no
reclaimer — killed at review-log-archive.md:4595 (§4.6.1 orphan-claim GC plus the whole-pod scrub are
the level-triggered backstops) and again on the refuted list; (b) reclaim unsendable because the failed
stage's connection is already dead, given §7.1's no-re-dial rule — covered by the staged predicate
"a reclaim the adapter does not answer ... did not complete" and its `leaked` disposition; (c) the
hold outliving a failed cleanup forever — this is the live form but it is ALREADY OPEN AND FILED
(review-log.md Open: "Does the hold survive its own timeout?"), not mine to re-file; (d) the hold not
being taken at the sixteen non-`Shutdown` release sites — twice-declined UNVERIFIED in the Open list;
(e) occupancy-zero recycle racing the hold — spec/06_warm-pod-model.md already guards it.

FACT: The occupancy-zero recycle boundary is already guarded against a still-running per-slot cleanup,
so the reclaim hold cannot let a whole-pod scrub start under it. "When occupancy reaches zero **and all
slot cleanup has finished**, the pod leaves `claimed`". EVIDENCE: spec/06_warm-pod-model.md:175. This
kills the "the hold deregisters the entry, occupancy hits zero, the whole-pod scrub races the per-slot
cleanup" dress before it is built.

FACT: The two SPEC-3 action-list additions are true of the shipped code, verified directly rather than
from the proposal. `deregisterSlotLocked` cancels every armed direct-mode expiry timer inside the same
critical section that deletes the entry, and `removeSlotTree` delegates to `slotlayout.RemoveTree`,
which owns the credential directory. EVIDENCE: pkg/adapter/slotsession.go:174-187; pkg/adapter/slot.go:210-212.

FACT: In the SHIPPED `Shutdown`, every destructive step (`emitFinalUsage`, the §15.4.2 drain, `Runtime.Close`,
`removeSlotTree`, `reportSessionScrub`) sits inside `if bound`, so an entry that is present but unbound is
deregistered and then left with its tree on disk. That is the defect CODE-1 exists to fix, and it is also
why "the slot release runs whenever the adapter holds an entry" is a change rather than a restatement.
EVIDENCE: pkg/adapter/session.go:238-280.

WATCHOUT: The reliability lens has now run at least four times on this proposal (spec.2, spec.5, spec.8
and this one) and the standing context's Traps list carries ~230 entries, including a single line naming
seven already-killed reliability dresses. EVIDENCE: review-log.md Traps, "MISTAKE nearly filed, four
capacity dresses in one round and four reliability dresses in another". Read THAT line and the Open list
before building anything; four of the eight angles a fresh reader reaches for are named in it verbatim.

MISTAKE (not mine, recorded so it is not repeated): a round generated its findings against a stale
snapshot and produced six fix groups and no edit. The snapshot at scratchpad/cp-snap/.../spec-r2 is now
identical to the live `spec-changes.md`; an empty `diff -ru` there means the fix round changed nothing in
this file, NOT that the file is unreviewed.


### [spec.2.review-security.1]

DECISION: returned an EMPTY findings list on the spec staging — BECAUSE every security-shaped
candidate I could build against the staged text resolves to one of the standing bars, and the
two live security surfaces (the epoch fence and the reclaim hold) are both fail-closed in the
direction the lens cares about — ALTERNATIVES: I built and dropped six candidates, each named
below with the ground that killed it, so a later security lens does not rebuild them.

FACT: the r1→r2 delta on the spec staging is TWO hunks and neither is security-relevant. The
only differing snapshot is `spec-r1-prefix` (15:04); `spec-r2` and `spec-r2-start` are
byte-identical to the live proposal. Both hunks rewrite the no-re-dial RATIONALE from "the
epoch is meaningless without the connection" to the latch wording, at §7.1's staged paragraph
and at §4.7.1's caller-rules paragraph, plus the matching narrative paragraph. No normative
force changed. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:390,:396-400,:681

FACT: the epoch fence cannot strand credential residue, and this is derivable in one grep
rather than by reading the design. Every adapter deregistration path reaches a
`removeSlotTree`: `releaseSessionSlot` pairs `deregisterSlot` with `removeSlotTree`
(slotsession.go:214-217), `Shutdown` pairs `deregisterSlotLocked` with `removeSlotTree`
(session.go:238,:271), the §10.1.4 hold's self-termination pass does the same
(holdstate.go:254), and there is no fourth caller of `deregisterSlot*`. So a `superseded`
answer's justification ("the entry the adapter now holds was created after the failed
attempt's entry was released") entails the predecessor's tree, credential directory and §4.9
timers are already gone, and an `absent` answer entails the same or that nothing was ever
created. Any security finding shaped "the epoch fence lets a fenced compensation perform
nothing and leave live credentials behind" dies here.
EVIDENCE: pkg/adapter/slotsession.go:214-217; pkg/adapter/session.go:238,:271;
pkg/adapter/holdstate.go:254; pkg/adapter/slotlayout/tree.go:58-70

FACT: the §4.7 row's untouched tail is what keeps the tenant-isolation whole-pod scrub outside
every new fence, and it is verifiable in one read. spec/04_system-components.md:686 states the
recycle-disposition scrub after the sentence SPEC-1 replaces, and SPEC-1's instruction leaves
everything "from 'On the default disposition the pod is replaced.' to the end" unchanged. So
an epoch mismatch, an `absent` answer and the reclaim hold each leave the scrub stated.
EVIDENCE: spec/04_system-components.md:686; spec-changes.md:318-322

WATCHOUT: the six security candidates I built and dropped, with the ground for each, so the bar
is visible rather than re-derived.
(1) "`superseded`/`absent` are pod self-reports that exempt a slot from leak accounting" —
barred by the standing Traps at review-log.md:600 and :624, and independently by the shipped
`leaked = err != nil || !cleanly` self-report already in the tree.
(2) "SPEC-3's withheld `ReportSessionScrub` relaxes the `recycle.maxSessionsPerPod` residual
bound" — barred at review-log.md:453; today's failed bind sends no `Shutdown` at all, so the
counter does not advance today either and the per-slot cleanup this proposal adds is strictly
more reclaim than ships.
(3) "the §15.4 hold makes a conforming adapter refuse `RevokeCredentials` with `ABORTED`, so a
mandatory §4.9 revocation is refused" — this is the security dress of the wide
create-or-resolve predicate declined four times (review-log.md:590,:707,:723). It dies twice:
during the hold no entry exists so the resolve is vacuous, and `removeSlotTree` has already
taken `CredentialsDir` and `deregisterSlotLocked` has already cancelled every armed timer, so
the revoke has nothing left to revoke.
(4) "a §11.4 revoke `Shutdown` arriving during a hold is answered `absent` and the user's
session survives" — the hold opens at the deregistration, so the session is already being torn
down when the revoke lands; the revoke's goal is reached by the cleanup itself.
(5) "the shared-entry residue leaves a running session's process group killed under an
attacker-influenceable value" — the value is the adapter's own epoch and the harm is
availability, not a security bound; it is disclosed at spec-changes.md:231-247 with the cost of
closing it.
(6) "the orphan runtime session at spec-changes.md:248-259 holds live credentials" — barred at
review-log.md:600; the cleanup removed `CredentialsDir` and cancelled the timers before the
late claim re-created the (empty) tree.

FACT: check (2) of this lens (a security bound sourced from a pod self-report or lacking a
§12.4 durable fallback) is structurally inert on this delta, and here is why rather than an
assertion that it is. Neither mechanism the amendment adds bounds a security property: the
epoch bounds WHICH teardown runs (availability of a live session), and the hold bounds WHEN a
bind is admitted onto an identifier that is the session identifier and therefore unique per
session, so no cross-session and no cross-tenant collision is possible on it. Both are
in-memory and non-persisted by design, and neither feeds a quota, a reuse counter or an
isolation limit. The residual-state limits the lens names (`maxSessionsPerPod`,
`maxScrubFailures`) are reached only through `ReportSessionScrub`/`ReportPodScrub`, and SPEC-3
withholds only the first, on a path that sends no report today.
EVIDENCE: spec-changes.md:677 (pod-local, never persisted); spec-changes.md:601 (hold keyed on
the slot identifier); review-log.md:361 (identifier-scoping is what kills availability findings
against the hold)

USEFUL [review-log.md Standing context, Traps :600 and :624]: the two consolidated
security-family entries listing fourteen already-refuted dresses across three rounds saved this
lens most of a round. They are the reason this shard is a decline rather than a filing. Keep
them consolidated; do not let a compaction split them back into individual lines.

USEFUL [review-log.md Settled :249 and :335]: ":249 the staged edits touch NO §10.3 / §13.1 /
§13.2 control, verified by absence" and ":335 the amendment adds no control-plane or data-plane
write of any kind" are still true after SPEC-5 and SCHEMA-1: the staged spec adds no RBAC,
NetworkPolicy, ServiceAccount, admission, tenant-pin or podspec text, and SCHEMA-1 is nine
scalar fields and one enum on an existing gateway-adapter message. Re-derived rather than
assumed.


### [spec.3.review-applicability.1]

DECISION: returned an empty findings list — BECAUSE the mechanical applicability layer is now provably clean and every candidate I developed past that layer is already adjudicated in the standing context or on the refuted list. ALTERNATIVES: (a) §5.2's hold predicate "any request that would create or resolve a registry entry" versus §15.4's "`Shutdown` is not held" — logged OPEN twice already at review-log.md:707 and :723 and declined four times; (b) §7.1's "the slot identifier is the session identifier" versus §5.2's retained "always assigned to a **new slot**" and the **Fresh workspace guarantee** — recorded and dropped at review-log-archive.md:3116 and :2091; (c) the new §7.1 paragraph landing inside §7.1's code fence — pre-existing convention, review-log.md:79,:406.

FACT: every one of the twelve "text to replace" verbatim anchor blocks in the spec staging matches its target spec file EXACTLY and EXACTLY ONCE. Verified mechanically, not by eye: extract the fenced blocks from spec-changes.md with a regex and `count()` each against the six spec files. Re-run that script rather than re-reading anchors by hand. EVIDENCE: spec-changes.md fenced blocks 0,2,5,8,10,12,14,16,18,20,22,24 against spec/04:157, spec/04:686, spec/07:23, spec/07:210, spec/07:213, spec/07:214, spec/07:414, spec/06:234, spec/05:555, spec/04:854, spec/05:545, spec/05:453.

FACT: every markdown link in every staged NEW block resolves — file exists and the anchor slug is derivable from a real heading, intra-file and cross-file alike. Also verified mechanically. No staged block contains an N3 reserved phrase (`lifecycle channel` / `control channel`, either spelling) or an N8 line-number citation. EVIDENCE: the seven anchor targets checked are spec/04:659 (4.7.1), spec/04:657 (4.7), spec/04:848 (4.7.9), spec/04:1099 (4.9), spec/05:365, spec/06:78, spec/07:3/:115/:378/:438, spec/10:3, spec/15:614/:1686/:1707.

FACT: the §29.4 anchor sentence ending "(§15.4.3 ..., §28.5.3)." occurs four times in spec/29 (:645, :711, :887, :982) but only twice inside §29.4 (:645 step 6, :711 step 13), and SPEC-1 names step 13, so the edit site is unique. Do not re-file this as an ambiguous anchor. EVIDENCE: spec/29_communication-scenarios.md:575 (§29.4 heading), :704-711 (step 13).

FACT: all seven RPCs §4.7.1's bind-epoch block enumerates exist in the §4.7 Gateway → Adapter table and in the proto, `PrepareWorkspace` really is the only client-streaming one with a single response, and `ConfigureWorkspace` really does create/claim a slot registry entry (`claimSessionSlot`) and is itself the SDK-warm start (`noteRuntimeStarted`). EVIDENCE: spec/04_system-components.md:661-686; schemas/lenny-adapter.proto:41,48,55,62,72,89,151,206; pkg/adapter/sdkwarm.go:217,:261.

FACT: §7.2's close sequence bumps `coordination_generation` in step 4, i.e. AFTER the step-3 reclaim SPEC-2 inserts, so the compensating `Shutdown` carries the generation the pod still holds and cannot be fenced by the bump. A future round tempted to file a §10.1 generation-fence ordering defect on the resume path should stop here. EVIDENCE: spec/07_session-lifecycle.md:214 (step 3), :215 (step 4 "bumps `coordination_generation` by exactly one").

WATCHOUT: `diff -ru` against the round's own `spec-r3` snapshot returns nothing, because the snapshot is taken at round start. To see what the previous round changed, diff against `spec-r1-start` (spec-r2-start, spec-r2 and spec-r3-start are all identical to the working tree, so the spec staging has not moved since round 2 began). EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/.

USEFUL [review-log.md:424]: "`§15.4.2` is the repo's established citation for the CH-RUNTIMEOPS `terminate` signal ... Do not file it as a false citation" saved a finding I had fully developed against the staged §4.7 row's "[Section 15.4.2] graceful-shutdown signal", on the ground that §15.4.2 is an adapter-PROCESS DRAINING state and the frame's own card is §28.5.3.


### [spec.3.review-citations.1]

FACT: every verbatim anchor in `spec-changes.md` still resolves exactly against the tree at this
HEAD. Verified one by one: §4.1 third sentence (spec/04_system-components.md:157), the §4.1
derivation sentence (:151), the §4.7 `Shutdown` row opening (04:686), the §4.7.1 insertion point
(after the `*Adapter → Gateway RPCs:*` table at 04:688, before `#### 4.7.2` at 04:695), §4.7.9
step 5 (04:853), §5.2 `**Slot cleanup:**` (05:545), §5.2 `**Scrub model.**` (05:453), §5.2
`**Max retries:**` (05:555), §6.2 fence heading + `receiving_uploads ──→ running` (06:150-153),
the `**`reserved` hold semantics.**` paragraph the §6.2 prose inserts before (06:158), §6.2
mid-resume cancel clause (06:234), §7.1 atomicity paragraph and its parenthetical (07:23) sitting
inside the fenced flow listing before the `(executionMode, …)` continuation (07:24), §7.2 preamble
(07:210), step 2 (07:213), step 3 (07:214), §7.3 list item 4 (07:414), §15.4 insertion point
(15:1469 SDK-warm demotion contract → 15:1471 `#### 15.4.1`), §29.4 step 13's closing citation
(29:704-709). EVIDENCE: as listed.

FACT: every explicit line citation in `spec-changes.md` is accurate — `schemas/lenny-adapter.proto:1630-1635`
is exactly the `coordination_generation` comment block plus `int64 coordination_generation = 6;`;
`spec/29_communication-scenarios.md:586-588` is the `**Preconditions.**` sentence ending "so the
runtime is running", `:589-591` the interrupt-path addition, `:697` "adapter closes the session
runtime", `:669-674` step 10's "valid in any non-terminal state". EVIDENCE: spec-changes.md:290,
:301, :306, :352-358.

FACT: the shipped-code claims the staging leans on all hold. `slotlayout.RemoveTree` removes
`p.CredentialsDir` (pkg/adapter/slotlayout/tree.go:58-68); `deregisterSlotLocked` cancels every
armed expiry timer before deleting the entry (pkg/adapter/slotsession.go:174-183); `DemoteSDK`
calls `releaseSessionSlot` and so removes the entry (pkg/adapter/sdkwarm.go:294-300); mid-session
upload really does reuse `PrepareWorkspace`/`FinalizeWorkspace` (pkg/gateway/sessionserver/upload_to_session.go:128,:134);
`stageWorkspace` sends `PrepareWorkspace` only when `len(uploads) > 0`
(pkg/gateway/podlifecycle/podsession/binder.go:1322-1328); the two-connection bind attempt is real
("Prepare and Launch each call it so no phase depends on a connection held open by the phase
before it", pkg/gateway/podlifecycle/podsession/binder.go:1113-1114); SPEC-4's note that the retry
path marks a failed reservation release `leaked` while the create-time path only logs is exact
(pkg/gateway/sessionserver/start.go:2834-2846 vs pkg/gateway/podlifecycle/podsession/slotbinder.go:215-220).

FACT: `Shutdown` appears nowhere in spec/28 (`grep -n Shutdown spec/28_communication-channels.md`
returns nothing) and §28.5.1 is per-channel cards, so the "no §28 register row" claim is sound.
Likewise §15.4.6's categories run the runtime binary against a fake adapter, so they genuinely
cannot observe an adapter obligation (spec/15_external-api-surface.md:2038-2046).

FACT: the pre-attached disposition really does retire the pod — `failed ──→ draining ──→ terminated`
is in the §6.2 fence (spec/06_warm-pod-model.md:104-105), which is what makes the proposal's
repeated "the residue dies with the pod" defensible even though §6.2's prose says "marked `failed`
and released back to the pool". EVIDENCE: spec/06_warm-pod-model.md:283.

MISTAKE: the hand amendment at 1344047d4 added two Design statements (statements 5 and 6) and left
the Design preamble's "Four statements are needed" untouched. Confirmed by counting `^\*\*` in
`git show 2811d55fe:…spec-changes.md` (four) against the working tree (six). Filed as the round's
only finding.

DECISION: did NOT file the §5.2-vs-§15.4 `Shutdown` carve-out. §5.2's hold says "the adapter admits
no request that would create or resolve a registry entry under it" and §15.4 says "`Shutdown` is
not held". BECAUSE a `Shutdown` after the deregistration resolves nothing and creates nothing, so
§5.2's predicate does not reach it, and §15.4 states the boundary explicitly. ALTERNATIVES: filing
it as a contradiction — rejected under the standing rule-plus-stated-exception refutation already
recorded twice in this proposal's history.

DECISION: did NOT file the Design preamble's "§4.1's derivation paragraph and the §4.7 `Shutdown`
row both treat the runtime close and the slot release as one act gated on the binding"
(spec-changes.md:8-10). BECAUSE §4.7's shipped row (04:686) states the one act but states no
gating; only §4.1 (04:157) states the bound-entry gate. The joint attribution is loose rather than
false, it is pre-amendment text that survived seven sweeps, and it stages no spec sentence.

WATCHOUT: `diff -ru scratchpad/cp-snap/…/spec-r3 proposals/…` returns nothing this round, and
spec-changes.md is byte-identical to the `spec-r2-start` snapshot. Round 2 landed no spec-changes
edit, so "read what changed first" gives no reading order here; the only changed text since the
amendment is round 1's fix group (diff against `spec-r1-start`).


### [spec.3.review-client-surface.1]

DECISION: returned an EMPTY findings list for the client-surface lens on the staged spec edits — BECAUSE every externally-consumed representation the amendment touches is either mirrored or provably absent, and the two real defects I found both have their remedy outside spec-changes.md (recorded as DEFERRED below) — ALTERNATIVES: filing the `docs/reference/adapter-contract.md` miss and the `pkg/gen/adapter/v1` mis-citation as findings, rejected because this loop's scope is "findings whose fix lands in the staged spec edits".

FACT: the generated protobuf package is `pkg/proto/adapter/v1`, NOT `pkg/gen/adapter/v1`. `pkg/gen` does not exist in the tree. — EVIDENCE: pkg/proto/adapter/v1/lenny-adapter.pb.go, pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go; `ls -d pkg/gen` → No such file or directory. The Makefile target `generate-proto` at Makefile:91-92 is real.

DEFERRED [0081_....non-spec-changes.md]: SCHEMA-1's paragraph "the regenerated `pkg/gen/adapter/v1` package lands with it" (non-spec-changes.md:1060) and the files-touched line naming `pkg/gen/adapter/v1` are false; the true path is `pkg/proto/adapter/v1`. The same false path is in the implementation-checklist S8 line. Review-log Settled entry ("SCHEMA-1's nine fields and one enum mirror only `pkg/gen/adapter/v1`, which is generated") carries it too. EVIDENCE: pkg/proto/adapter/v1/lenny-adapter.pb.go exists; pkg/gen does not.

DEFERRED [0081_....non-spec-changes.md, `## Staged docs changes`]: `docs/reference/adapter-contract.md`'s `Shutdown` row (docs/reference/adapter-contract.md:75) is the reader-facing mirror of the §4.7 `Shutdown` row SPEC-1 rewrites, and it is in NO edit list — the proposal's only docs deliverable is DOCS-1 (`docs/reference/state-machines.md`), and a grep of the whole proposal directory for "adapter-contract" hits only the review logs. After SPEC-1 the row is wrong on the drain gate ("When the release leaves the pod holding no other bound session, the adapter also sends the CH-RUNTIMEOPS drain signal" is now conditioned differently by the two-teardown split), on the usage flush and runtime close (now gated on "a session whose start the adapter has admitted" rather than on the binding), and on the unconditional `ReportSessionScrub` ("reports the per-slot cleanup outcome through `ReportSessionScrub`", which SPEC-3 withholds on the pre-`running` path). It also does not mention the new epoch precondition or the reclaim hold that §15.4 publishes for third-party adapter authors. The review log already records ":75 is the site and :81 is not" (review-log.md:479) and that no tier-11 gate catches it (review-log.md:168), but no deliverable stages the fix.

FACT: the adapter proto genuinely has no parallel client representation to keep in step, re-verified independently this round. `grep -rn "exited_cleanly\|ShutdownResponse\|expected_bind_epoch\|bind_epoch\|SlotReclaimOutcome" spec/ docs/ schemas/ sdks/ charts/` returns only schemas/lenny-adapter.proto:206,:1665,:1666. The four `scripts/specshift/testdata/**/lenny-adapter.proto` files are 9-10 line fixtures declaring a single toy RPC, not copies. — EVIDENCE: scripts/specshift/testdata/idpass/proto/schemas/lenny-adapter.proto:1-9.

FACT: §28.7's wire-contract artifact register is one row per ARTIFACT under `schemas/`, derived from the directory rather than per message or field, so an additive proto edit adds no §28.7 row. — EVIDENCE: spec/28_communication-channels.md:1759-1765,:1781-1783.

FACT: the §4.7 Gateway→Adapter and Adapter→Gateway RPC tables describe RPCs in prose and enumerate no response fields for any bind-sequence RPC, so SCHEMA-1's seven response fields need no §4.7 table mirror. SPEC-5's stated insertion point (after the `*Adapter → Gateway RPCs:*` table, before `#### 4.7.2`) is correct against the tree. — EVIDENCE: spec/04_system-components.md:659-695.

FACT: `/run/lenny/slots/{sessionId}/credentials.json` is the correct literal for SPEC-3's widened action list, and it is already the corpus-wide spelling (spec/04:793, spec/05:455,:461,:471, spec/13:26,:28,:30, spec/15:2178, spec/28:1081,:1097, spec/29:262, spec/17:54, docs/getting-started/concepts.md:584). SPEC-3 mints no new path literal.

FACT: §5.2's `**Non-retryable failure categories:**` list really does sit under the `**Slot retry policy (`maxConcurrentSessions > 1`)**` heading, so the "deliberately untouched · §5.2's non-retryable failure categories" rationale ("scoped to `maxConcurrentSessions > 1`") is accurate. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:553 (heading), :555-559 (the list).

UNVERIFIED: spec/15_external-api-surface.md:1136 (the `SETUP_COMMAND_FAILED` row) states that any setup-window failure at "every gRPC code other than `FailedPrecondition`" "is recovered with a fresh pod per §6.2". The new reclaim-hold `ABORTED` refusal is exactly such a code, and on the create-time-reserved path the retry demonstrably does NOT get a fresh pod (the row keeps its `PodAssignment`; review-log Settled on `BindReservedSlot`). I judged this pre-existing looseness rather than a defect this proposal creates, because the same sentence is already false for the shipped create-time-reserved retry, and because recovering with a fresh pod would also succeed. A later round that wants it needs to show the staged text, rather than the shipped path, is what makes the sentence false.

WATCHOUT: the diff the brief names (`spec-r3`) is byte-identical to the live proposal, as it has been in nearly every round. The newest snapshot that actually differs from the working tree in `.spec-changes.md` is `spec-r1-prefix`, and the delta against it is exactly two sentences: the no-re-dial rationale in the §7.1 staged block and the matching clause in §4.7.1's caller rules, both rewritten from "the epoch is meaningless without the connection" to the latch wording. — EVIDENCE: `diff -u scratchpad/cp-snap/0081_.../spec-r1-prefix/...spec-changes.md proposals/0081_.../...spec-changes.md` (two hunks, at the §7.1 block and at §4.7.1's third paragraph).


### [spec.3.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE the round-3 delta on the reviewable text
is literally zero and the previous round's docs lens already returned empty against
byte-identical text. `diff -rq scratchpad/cp-snap/.../spec-r3 proposals/0081_...` is silent,
and `diff -rq spec-r2-start spec-r3` differs ONLY in the two review-log files, so
`spec-changes.md`, `non-spec-changes.md`, `summary.md`, the checklist and the deviations file
have not moved since round 2 began. ALTERNATIVES: I re-derived from scratch, rather than
trusting that, (a) the `lenny_adapter_leaked_slots` mention SPEC-3 adds, (b) a gRPC
status-code enumeration in `spec/` that §15.4's new `ABORTED` requirement would leave
incomplete, (c) a `docs/` mirror of §7.2's deleted "no runtime was started on it" premise,
and (d) the `Edge cases and accepted failure modes` roll-call against the six residues the
orchestrator named. All four came back clean or docs-lane.

FACT: `lenny_adapter_leaked_slots` appears in `spec/05:545` and `spec/06:160` and in NO file
under `docs/` — `docs/reference/metrics.md` has never carried it. SPEC-3's new mention
restates a shipped gauge, so it opens no companion-row obligation; the missing metrics-doc row
is pre-existing and outside 0081. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545;
spec/06_warm-pod-model.md:160.

FACT: `spec/` contains no gRPC status-code enumeration at all — `grep -rn
"ABORTED\|FAILED_PRECONDITION\|RESOURCE_EXHAUSTED" spec/*.md` returns nothing. SPEC-5's §15.4
clause naming `ABORTED` for the reclaim-hold refusal therefore falsifies no existing spec
table and needs no sibling edit. (The only status tables are in `docs/api/internal.md:490-500`
and `docs/reference/adapter-contract.md:210-216`, both already loose against shipped
behaviour per the standing trap, both docs-lane.) EVIDENCE: spec/ (empty grep).

FACT: §7.2's deleted premise ("no runtime was started on it", "half-claimed replacement pod",
the skip-seal gloss) has NO mirror anywhere under `docs/` — `grep -rn "no runtime was
started\|half-claimed replacement pod\|skip-seal\|snapshot-close" docs/` is empty. The
mid-resume snapshot-close sequence is spec-only, so SPEC-2's §7.2 rewrite creates no missing
docs edit site. Worth recording because §7.2 is the largest prose deletion in the staging and
the obvious place to hunt for one.

USEFUL [spec.2.review-docs-alignment.2]: its two-line summary of this lens's live surface —
the docs-mirror shape reduces to `docs/reference/adapter-contract.md:75` and `:64`, both owned
by the non-spec/docs loop, and the "accepted failure mode lands in no staged spec text" shape
has been filed twice and refuted twice by the material skeptic — is what let me spend the
round on independent re-derivation instead of re-walking those two. A docs lens landing in a
spec-only loop on 0081 should read that entry first and expect empty.

WATCHOUT: the snapshot-layout trap has a THIRD form now. In this window `spec-r3` and
`spec-r3-start` are both byte-identical to the live proposal AND to `spec-r2`/`spec-r2-start`
on every non-log file, because round 2 produced no fix. So an empty diff here means "round 2
found nothing", not "you diffed the wrong snapshot". Confirm by `diff -rq` across the whole
chain rather than assuming a `-prefix` directory holds the delta.
EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/


### [spec.3.review-edit-sites.1]

FACT: This window's snapshots reuse the `spec-rN` names from the earlier window. `spec-r2`, `spec-r2-start`, `spec-r3` and `spec-r3-start` are byte-identical to the live proposal; `spec-r1-prefix` (15:04) is the newest DIFFERING snapshot and `spec-r9` (12:45) belongs to the PREVIOUS window. Diff against `spec-r1-prefix`, and sort snapshot dirs by mtime rather than by name. EVIDENCE: `ls -ltd scratchpad/cp-snap/0081_.../*/`

FACT: The whole r1-prefix→live spec delta is TWO sentences, both the no-re-dial rationale correction already on the orchestrator's already-fixed list (spec-changes.md:390 inside the §7.1 block, :396-400 rationale, :681 §4.7.1 caller rules). Nothing else in `.spec-changes.md` moved this round. EVIDENCE: `diff -u scratchpad/cp-snap/.../spec-r1-prefix/...spec-changes.md proposals/.../...spec-changes.md`

FACT: The mechanical anchor sweep is CLEAN and I re-ran it at this round's HEAD. Extract the fenced blocks after `## Staged edits` and count each across `spec/*.md`: blocks 0,2,5,8,10,12,14,16,18,20,22,24 give exactly 1 (each in the file the deliverable names) and every other block gives 0. Thirty-one blocks now, not twenty-eight — SPEC-5 added three. Do not re-run it unless spec/ moves.

FACT: Every markdown anchor the staged text mints resolves at HEAD, re-verified this round: `### 7.1`(spec/07:3), `### 7.2`(:115), `### 7.3`(:378), `### 7.4`(:438), `### 10.1`(spec/10:3), `### 15.1`(spec/15:614), `### 15.4`(:1458), `#### 15.4.2`(:1686), `#### 15.4.3`(:1707), `### 6.1`(spec/06:3), `### 6.2`(:78), `### 5.2`(spec/05:365), `### 4.7`(spec/04:657), `#### 4.7.1`(:659), `#### 4.7.9`(:848), `### 4.9`(:1099). Both SPEC-5 insertion points are unambiguous: spec/04:688 `*Adapter → Gateway RPCs:*` table closing before `#### 4.7.2` at :695, and spec/15:1469 `**SDK-warm demotion contract:**` before `#### 15.4.1` at :1471.

FACT: The new-identifier sweep comes back EMPTY at HEAD. `grep -rn "bind epoch\|bind_epoch\|reclaim hold\|slot release\|runtime teardown" spec/ docs/ schemas/ charts/` returns only the two pre-existing `slot released` hits in the §6.2 `slot_cleanup ──→ released` annotation (spec/06:155) and its docs mirror (docs/reference/state-machines.md:237), both already adjudicated as abbreviated glosses. `ABORTED` still occurs nowhere in spec/, docs/ or schemas/. `superseded` IS bound elsewhere, as a `manifest_reason` enum value in the §10.1 partial-checkpoint model (spec/10:148,:157; spec/16:198; spec/29:969) — different mechanism, different message, not a channel identifier, so no N3 stem question arises. Nobody had recorded that occurrence set; check it before filing a term-collision finding.

FACT: SPEC-3's new credential-directory clause spells the path correctly. `/run/lenny/slots/{sessionId}/credentials.json` is the corpus-wide spelling (spec/04:793,:914,:1169; spec/05:455,:461,:471; spec/06:26; spec/13:26,:28,:30; spec/28:1081,:1097; spec/29:262) and `{sessionId}` is the placeholder `tests/tier11_docs/slot_placeholder_literal_sweep_test.go` requires. The staged clause introduces no retired `{slotId}` spelling.

FACT: The tier-11 first-match hazard on the §5.2 append is still safe, re-verified against the current append text. Every `requireLine(t, s52, …)` anchor in `tests/tier11_docs` is `"**Slot (session mode).**"`, `"A service-mode slot is a different thing"`, `"Whole-pod replacement trigger"`, `"Session count limit"`, ``"increments `"+newGateway+"`"``, `"Uptime limit"`, `"The gateway triggers the whole-pod scrub"`, `"**Fresh-guest reprovision:**"` and `"the pod is held for its tenant through the claim's `reserved` state"`. The append writes `whole-pod replacement trigger stated below` in LOWER case and `lineContaining` is `strings.Contains` (case-sensitive, tier11_docs/backup_status_enum_test.go:48-55), so the capitalised anchor still resolves to spec/05:562. It also writes the literal `**Slot cleanup:**` twice; no gate anchors on that string. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:91-96,:154,:221,:297.

FACT: "registry entry" and "the adapter's slot registry" are ALREADY spec vocabulary (spec/16:188, spec/28:861), so SPEC-5's heavy use of "registry entry" mints no undefined term. I nearly filed that and it dies here.

DECISION: I return an EMPTY findings list — BECAUSE every surface my lens owns comes back clean at HEAD: the anchor sweep, the minted-identifier sweep across spec/docs/schemas/charts, the markdown-anchor resolution set, the tier-11 anchor set, the `{sessionId}` placeholder sweep, and the `## Spec files touched` inventory against the staged blocks. ALTERNATIVES rejected: (a) filing the §4.7 `ReportSessionScrub` row (spec/04:692) as falsified by SPEC-3's withheld report — barred five-site dead end; (b) filing `docs/reference/adapter-contract.md:75` — real, but its remedy is a DOCS deliverable and this loop may not land it, and it is already a standing Deferred filed by eleven lenses; (c) filing the `slot_cleanup ──→ released` annotation and its docs mirror against SPEC-3's widened action list — barred abbreviated-gloss trap; (d) filing SCHEMA-1's proto surface — remedy is in the non-spec staging.

WATCHOUT: `## Spec files touched` describes the §4.7 row edit as "first sentence replaced", but the anchor block quotes and the replacement covers TWO sentences (through "…rather than selecting a scope."), which the replacement restates at its end. Deterministic and appliable; below the bar, and do not "fix" it by re-anchoring on one sentence, because the quoted anchor is what makes the edit unique.

DEFERRED [docs/reference/state-machines.md:237, spec/06:155]: SPEC-3 widens §5.2's per-slot cleanup action list from three actions to five, so the `slot_cleanup ──→ released` annotation "(slot workspace removed, processes killed, slot released)" and its docs mirror become abbreviated rather than mirrored. Standing trap 446 rules this below the bar and no edit is owed; recorded only so the next lens that derives it stops here instead of filing.


### [spec.3.review-feasibility.4]

DECISION: returned zero findings — BECAUSE every actor-action claim in the staged spec
resolved against the tree, and the one candidate I built (the reclaim hold's predicate
sweeping in `Shutdown`) is already recorded as declined four-plus times — ALTERNATIVES:
filing it anyway, rejected because review-log.md `### Open` lines 80 and 96 record the
identical derivation with the vacuous-resolve refutation and name the same one-clause
remedy in §5.2.

FACT: the exclusive bind sequence genuinely spans TWO adapter connections, so §4.7.1's
closing two-connection sentence is grounded. `Binder.Prepare` dials via `dialSandbox`,
runs DemoteSDK?/PrepareWorkspace/FinalizeWorkspace/RunSetup/AssignCredentials, then
`cl.Close()`; `Binder.Launch` calls `reconnect` for its own connection and issues
StartSession or ConfigureWorkspace. Both resolve the one entry `ensureSlotPaths` created,
so both observe one epoch. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:843,
:956-960 (`cl.Close()` at the end of Prepare), :976, :1115, :1146-1156;
pkg/gateway/sessionserver/finalize.go:208,:280 (`s.podBinder.Prepare`).

FACT: §5.2's hold clause "for a hold taken outside any request, by the termination window
of the pass that runs the cleanup" has a real referent. `deregisterStartedSessions`
deregisters every started entry outside any RPC and `terminateHeldSession` then runs the
close and `removeSlotTree`. Do not file that clause as naming a pass that does not exist.
— EVIDENCE: pkg/adapter/slotsession.go:375-396; pkg/adapter/holdstate.go:229-265.

FACT: §4.7.1's caller rule "`DemoteSDK` ... removes the entry" and §5.2's "a release that
runs its cleanup inside the RPC that requested it ends the hold before that RPC answers,
so the pod-warm bind sequence that follows an SDK demotion does not meet it" are both
accurate against the handler: `DemoteSDK` calls `noteRuntimeClosed` then
`releaseSessionSlot` synchronously before returning its response. — EVIDENCE:
pkg/adapter/sdkwarm.go:274-303, in particular :296-301.

FACT: `ShutdownRequest` already carries `deadline_ms = 3`, which is the "graceful window
the reclaiming request carries" §5.2's hold paragraph bounds the runtime close by, and
field 7 is the first free number (1,2,3,5,6 used; 4 reserved for the retired `slot_id`).
`coordination_generation = 6` sits at :1635 with its comment at :1630-1634, so SPEC-1's
`schemas/lenny-adapter.proto:1630-1635` citation is exact. — EVIDENCE:
schemas/lenny-adapter.proto:1608-1636.

FACT: `PrepareWorkspaceRequest` carries `session_id` on EVERY frame (field 1), so
§4.7.1's "the adapter resolves the entry once, at the frame from which it first resolves
the slot identifier" and §15.4's "resolves the slot identifier more than once within one
`PrepareWorkspace` call" are both statable obligations rather than descriptions of a
one-shot header. — EVIDENCE: schemas/lenny-adapter.proto:682-696.

FACT: the two "deliberately untouched" justifications I checked are true. §15.4.6 starts
"the runtime against the fake adapter" and probes the RUNTIME's observed level, so it has
no adapter under test. And `grep -n Shutdown spec/28_communication-channels.md` returns
nothing, so the §28-registers row is right. Do not spend a round re-deriving either.
— EVIDENCE: spec/15_external-api-surface.md:2038-2056; spec/28_communication-channels.md.

USEFUL [Open line 72, `spec.1.review-feasibility.1`]: I closed it. "Can the gateway
placing a §5.2 retry see that a pod holds an incomplete reclaim?" — yes, and inside the
one request that places the retry. `applySlotRetryPolicy` reads the compensation's own
`Shutdown` answer through `SlotBindError.Leaked`/`relErr` and appends to the
`ExcludePods` slice carried by pointer on `SlotBindRequest`, which survives the `queue`
pool's re-entry of the same closure. The staged §5.2 sentence is scoped to "the retries
this policy places", which is exactly that scope. No spec-lane defect.

WATCHOUT: the snapshot the brief names (`spec-r3`) is byte-identical to the live
proposal, as are `spec-r2`, `spec-r2-start` and `spec-r3-start`. The real previous-round
text for the spec-changes file is `scratchpad/cp-snap/0081_.../spec-r3-prefix`, and the
r3-prefix → live delta is large: the epoch was re-based from per-ATTEMPT to per-ENTRY,
the bind-sequence request-side epoch and its two admission rules were deleted outright
(no bind-sequence request carries an epoch now), §4.1's second sentence was withdrawn,
and three new accepted-failure-mode bullets were added. Diff against `-prefix`, never
against the round's own name. — EVIDENCE: `diff -rq` over the whole cp-snap directory.

OPEN: nothing new. Every candidate my lens produced this round was already in
review-log.md's `### Open` block (lines 72, 74, 75, 77, 78, 80, 89, 96 of that section's
numbering). A future feasibility lens should read that block BEFORE deriving, because the
lens's natural targets on this proposal are now almost entirely enumerated there.


### [spec.3.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE every candidate I derived independently turned out
to be already filed-and-refuted or already declined-with-reasons in the standing context, and every
citation, anchor and cross-section predicate I checked resolved against the tree.
ALTERNATIVES considered and dropped, with the entry that killed each:
(1) The reclaim hold's "removal of the slot's directories runs after that close under no deadline" /
"the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` figure ... is not a second bound on the
hold" (spec-changes.md:601) against the retained `**Slot cleanup:**` bullet's "Cleanup timeout is
max(...) (minimum 5s enforced at runtime by the adapter)" (spec/05:545). Filed once at
review-log-archive.md:16339, declined at :16261 and again at review-log.md:1318, and the adapter
enforces no per-slot cleanup timeout at all (archive:16006). Do not re-file.
(2) §15.4's "`Shutdown` is not held" (spec-changes.md:711) against §5.2's unqualified "admits no request
that would create or resolve a registry entry under it" (spec-changes.md:601). Killed by
archive:16025 — the hold starts at the DEREGISTRATION, so during it no entry exists for `Shutdown`
to resolve and `Shutdown` never creates one. Open item at review-log.md:723 records the same reading.
(3) §4.7's "which is a precondition on the whole request" (spec-changes.md:322) reading as if it
fenced the recycle whole-pod scrub too, while §4.1's replacement (:285) and §15.4's non-conformance
clause (:705) both enumerate only the slot release, the runtime teardown and the shared-runtime
signal. The colon immediately glosses "the whole request" as exactly those acts, and the phrase
exists to say "precondition, not scope selector" for the §4.1 rule; wording, not a defect.
(4) §4.7.1/§15.4 define `reclaimed` as "the adapter held the entry the request NAMED", which an
epochless `Shutdown` never does, while the §4.7 row says an epochless request "reports `reclaimed`
for an entry it removed". Reconcilable through "the named session"'s entry; too thin to spend two
verifiers on.
(5) A third-party adapter that answers `StartSessionResponse.refusal_reason` or
`ConfigureWorkspaceResponse.refusal_reason` (schemas/lenny-adapter.proto:958-962, :1690-1692) without
having created an entry has no epoch to report, and §15.4:701 makes "reports zero on a bind-sequence
response" non-conformance. The shipped Go adapter never emits either refusal, and two sibling
"third-party completeness gap" findings were already refuted this loop as hypothetical-implementer
arguments.

FACT: the r1→r2 delta in `spec-changes.md` is exactly two hunks, both the no-re-dial RATIONALE
(the §7.1 commentary at :396-400 and the §4.7.1 caller-rule sentence at :681), rewritten off
"connection-scoped epoch" onto "the caller latches it per connection". `spec-r2`, `spec-r2-start`,
`spec-r3` and `spec-r3-start` are all byte-identical to the live file, so round 3 has no delta at all.
EVIDENCE: diff -u scratchpad/cp-snap/0081_.../spec-r1-prefix/...spec-changes.md against the live file.

FACT: re-verified this round against the tree, so a later lens need not: every replacement anchor in
the staged spec edits is byte-unique in `spec/` (spec/04:157 §4.1 third sentence, :686 §4.7 `Shutdown`
row opening, :852-ish §4.7.9 step 5; spec/05:453 `**Scrub model.**`, :545 `**Slot cleanup:**` action
list, :563-ish `**Max retries:**`; spec/06:152 fence entry, :234 `resuming → cancelled` clause;
spec/07:23 atomicity parenthetical, :24 the continuation line, :210 §7.2 preamble, :213 step 2 tail,
:214 step 3, :414 §7.3 list tail; spec/29:704-711 step 13). Both NEW insertion anchors the amendment
added also resolve: the `*Adapter → Gateway RPCs:*` table closes at spec/04:693 with
`#### 4.7.2` at :695, and `**SDK-warm demotion contract:**` is spec/15:1469 with
`#### 15.4.1` at :1471.

FACT: every explicit file:line citation in the staged spec edits verifies. `schemas/lenny-adapter.proto:1630-1635`
is the `coordination_generation` comment plus field on `ShutdownRequest`; `spec/04:151` is the
field-set derivation sentence; `spec/04:157` is the `ShutdownRequest` paragraph; `spec/29:586-588`
is the `**Preconditions.**` "so the runtime is running" clause, `:589-591` the interrupt addition,
`:669-674` step 10's endpoint-precondition restatement, and `:697` step 12's "the adapter closes the
session runtime".

FACT: the §5.2 hold paragraph's "A release that runs its cleanup inside the RPC that requested it ends
the hold before that RPC answers, so the pod-warm bind sequence that follows an SDK demotion does not
meet it" is true of the tree: `Server.DemoteSDK` calls `releaseSessionSlot` synchronously before
returning, and `releaseSessionSlot` is deregister + `removeSlotTree` in immediate succession.
EVIDENCE: pkg/adapter/sdkwarm.go:296-301; pkg/adapter/slotsession.go:213-219.

FACT: all seven bind-sequence responses exist as distinct messages in the proto, so the "adds a
reporting field to each of the seven bind-sequence responses" claim at spec-changes.md:716-718 has
seven real targets: PrepareWorkspaceResponse (:699), FinalizeWorkspaceResponse (:761),
RunSetupResponse (:869), StartSessionResponse (:958), AssignCredentialsResponse (:1033, empty),
ResumeResponse (:1433), ConfigureWorkspaceResponse (:1690).

FACT: §15.4.6 is a RUNTIME-binary suite run by `lenny runtime validate` against a FAKE adapter
(spec/15:2038-2050), so the "deliberately untouched" note that it cannot observe an adapter
obligation is accurate rather than a scope dodge. spec/18 also stays clean under the amendment: its
only proto-related deliverables are "Wire-contract artifacts under `schemas/`" and "Generated Go
stubs" (spec/18:92-93), neither of which enumerates a field set, and its `cmd/lenny-compliance`
battery (:122) is the Basic-level RUNTIME battery, not the tier-10 adapter battery CONF-1 lands in.

WATCHOUT: the standing context is ~830 lines and it is where the answers are. Six of the seven
candidates a fresh reader will generate on this document are already in it, three of them as
explicit "do not re-derive" entries (review-log.md:723, :1318, :1322; archive:16006, :16025, :16261).
Read `## Standing context` end to end before deriving anything; a candidate that survives that read
is worth the two verifiers and one that does not is not.


### [spec.3.review-kubernetes.1]

DECISION: returned an empty findings list — BECAUSE the staged spec edits touch no
Kubernetes API surface at all, and I re-derived each idiom against the tree rather than
trusting the standing context. (1) No CRD status write is staged: a grep of
`…spec-changes.md` for controller/CRD/admission/finalizer/reconcile/status-subresource hits
only two incidental mentions (the untouched `SandboxWarmPool` cleanup-timeout validation rule
at :619, and §6.2 prose at :659), so §4.6.3 field ownership is not engaged. (2) The per-slot
sub-states the SPEC-4 edge lands in are NOT projected onto the CRD: spec/06:283's
`**State storage:**` paragraph says `.status.phase` carries only the coarse pod-occupancy
phase the WarmPoolController writes and "the fine session-lifecycle states are not projected
onto the CRD"; the leaked accounting lives in the Redis slot counter
(`pkg/sandbox/slotstate/registry.go:99` `MarkLeaked`). (3) No controller sits on the
synchronous bind path: the reclaim is a direct `Shutdown` RPC on the connection the failed
stage holds, and the §5.2 placement exclusion is an in-memory `ExcludePods []string` on
`podsession.SlotBindRequest`, per-client-request, never etcd (non-spec-changes.md:815-840).
(4) Pod retirement is requested, not written, by the gateway: `Binder.DrainSandbox` only
calls `podclaim.StampDrainRequest` (pkg/gateway/podlifecycle/podsession/slotbinder.go:601-603),
so the WarmPoolController stays the single writer and the retirement the §7.1 paragraph
relies on is level-triggered rather than a gateway status write.
— ALTERNATIVES: I considered filing (a) SPEC-3's "surfaced on the `lenny_adapter_leaked_slots`
gauge" as attributing an adapter-named gauge to a gateway-observed leak, and dropped it after
confirming the gauge is in fact PUBLISHED BY THE GATEWAY despite its name
(`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223`), so the sentence
is accurate; and (b) the unmodelled `slot_assigned → leaked` producer CODE-4/CODE-5 add while
SPEC-4 declines an edge out of `slot_assigned`, dropped because it is an adjudicated scope
call recorded at review-log.md:177 and :1594 and re-filing it closes nothing.

FACT: `lenny_adapter_leaked_slots` is a GATEWAY-registered gauge, not an adapter one, despite
the name and despite spec/06:160 saying "The adapter exposes a `leaked_slots` count ... and
`lenny_adapter_leaked_slots` gauge". A finding built on "the adapter cannot know what the
gateway observed" therefore has no premise. EVIDENCE:
pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223,
pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:920-936.

FACT: the fine-grained per-slot sub-states this proposal edits are deliberately NOT on the
Sandbox CRD, so no Kubernetes-idiom lens has purchase on the §6.2 fence edit. EVIDENCE:
spec/06_warm-pod-model.md:283 ("**State storage:** ... the fine session-lifecycle states are
not projected onto the CRD").

USEFUL [review-log.md:161]: "Retirement is never a gateway act at the release ...
`DrainSandbox` only stamps `lenny.dev/drain-request`; the WarmPoolController owns the
`draining` write" — this is the entry that pre-empts the whole single-writer family of
findings on this proposal, and it verified against the tree unchanged.

WATCHOUT: the "§7.1 says the exclusive pod retires while spec/06:283 releases it back to the
pool" family is refuted at least eight times and the standing context says so explicitly; a
Kubernetes-idiom dress of it (who writes `draining`, whether the residue rides a pooled pod
back into inventory) is the same finding in new clothes and costs two verifiers.
EVIDENCE: review-log.md:431.


### [spec.3.review-mechanism.1]

DECISION: filed exactly one finding, the `**Slot cleanup:**` timeout versus the reclaim hold's "under no deadline" clause — BECAUSE the standing Trap at review-log.md:592/:613 names it as "the only live form" of that family and directs that it be argued from the bullet rather than from the absence of enforcement, and the collision is now literal after SPEC-3's FIRST anchor moves the directory removals INTO the bullet's own action list, one sentence above the bullet's "Cleanup timeout is max(...)" — ALTERNATIVES: rejected the §6.2-cancel-bullet-asserts-an-unconditional-reclaim drift (dies on Trap :519(4), over-sending an obligation does not violate it and the §4.7 no-op sentence makes the extra send safe); rejected the "`superseded` reclaim against the row's unedited `On the default disposition the pod is replaced`" (same ground as the Settled entry at :428, already loose for any per-slot `Shutdown` on a co-tenanted pod); rejected the Redis over-assignment consequence of the racing-start residue (recorded as Open :653, a consequence of an accepted residue rather than a defect in it).

FACT: the gateway dials a FRESH adapter connection per bind attempt (`b.DialAdapter(addr)` inside `connectSlot`), so §4.7.1's per-CONNECTION epoch latch is per-attempt in practice and the apparent gap between "the most recent epoch a response reported to it on one adapter connection" and "the epoch the caller holds for that session" cannot be exercised by a pod serving two concurrent binds over one connection. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:415,:457.

FACT: `ShutdownRequest` really does carry `int32 deadline_ms = 3`, so the hold paragraph's "the graceful window the reclaiming request carries" is grounded on a real field, and field 7 is genuinely the next free number (1,2,3 used, 4 reserved, 5, 6 used). EVIDENCE: schemas/lenny-adapter.proto:1609-1636.

FACT: the spec contains no other occurrence of `ABORTED` anywhere under spec/ or docs/, so §15.4's new "refused with the gRPC status code `ABORTED`" is the first. It does NOT collide with spec/15's retryability contract: the `SETUP_COMMAND_FAILED` row states that every gRPC code other than `FailedPrecondition` in the setup window stays the retryable `SESSION_CREATION_FAILED`/`STARTING_FAILED`/`RESUME_FAILED` fallback. EVIDENCE: spec/15_external-api-surface.md:1136.

FACT: §5.2's blanket hold predicate ("admits no request that would create or resolve a registry entry under it") and §15.4's explicit "`Shutdown` is not held" do NOT conflict. The hold starts at the deregistration, so for its whole duration no entry exists under the identifier; the "resolve" half is vacuous and `Shutdown` never creates. This is the same ground Trap :590 records; I re-derived it independently and it holds against the current wording.

WATCHOUT: trap :610's "corrected form" for the §4.7.1 both-fields sentence ("as on `Resume` and `Shutdown`") is now STALE. Under the per-entry design no bind-sequence REQUEST carries an epoch, so `ShutdownRequest` is the only message carrying both, and the current text's bare "as on `Shutdown`" is correct. Do not "restore" Resume. EVIDENCE: spec-changes.md:677; non-spec-changes.md:1091-1099.

WATCHOUT: the newest snapshot that actually differs from the live proposal this round was `spec-r1-prefix` (15:04), not `spec-r3`/`spec-r3-start`, which are byte-identical. The only spec-changes hunk in the window is the r2 rewrite of the no-re-dial rationale at spec-changes.md:390 and :681 (from "would still hold the epoch it observed" to "is latched off the responses that connection reported to it"). Both sites now agree with §4.7.1's caller rule and neither reintroduces the connection-scoped-epoch error the compaction pass corrected. EVIDENCE: `diff -u scratchpad/cp-snap/.../spec-r1-prefix/...spec-changes.md proposals/.../...spec-changes.md`.

UNVERIFIED: the §5.2 hold paragraph's clause "for a hold taken outside any request, by the termination window of the pass that runs the cleanup" names a cleanup path that runs outside any RPC. I found no adapter path that deregisters a slot entry outside an RPC handler. Judged below the bar (a defensive completeness clause that binds a third-party adapter which does clean up asynchronously), but somebody should confirm whether the first-party adapter has such a pass before the code lane implements a bound for it.


### [spec.3.review-operational.1]

DECISION: returned an empty findings list for the operational lens on the staged spec edits — BECAUSE the staged text names exactly one metric (`lenny_adapter_leaked_slots`), which resolves in `spec/05:545`, `spec/06:160` and in code; it writes no CRD status and no condition, so §4.6.3 is not engaged; and every §5.2/§6.2 accounting claim it makes checks out against the shipped trigger. ALTERNATIVES rejected: (a) the `concurrent_slots_exhausted` gloss against SPEC-2's placement exclusion — standing trap, refuted six times, and the gloss is already loose in the tree because `ErrTenantMismatch` maps to the same reason; (b) the adapter-vs-gateway attribution of `lenny_adapter_leaked_slots` (spec/06:160 says the adapter exposes it, `gatewaymetrics_credential.go:219-223` emits it) — pre-existing drift, recorded, out of scope; (c) `docs/reference/adapter-contract.md:75` — real, but its remedy is a docs edit this loop may not land, and it is already the standing DEFERRED.

FACT: the spec-changes.md file did not change in rounds 2 or 3 of this loop. `diff -rq spec-r2 spec-r3` and `diff -rq spec-r3 <live>` differ only in the two review-log files. A round-3 reviewer on this proposal is reviewing byte-identical staged text to round 2. — EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r2, spec-r3

FACT: SPEC-3's edge-case claim "at `maxConcurrentSessions: 2` a single windowed failure already reaches the §5.2 whole-pod replacement threshold" is true against the tree, and the windowed counter does fire on a clean-released bind failure: `applySlotRetryPolicy` calls `health.RecordFailure(sbe.Pod)` on the clean-release arm and `health.RecordLeak` on the errored-release arm, then `health.Unhealthy(pod, maxConcurrentSessions)`; the threshold is `ceil(maxConcurrentSessions/2)`. — EVIDENCE: pkg/gateway/sessionserver/start.go:2834-2860; spec/05_runtime-registry-and-pool-model.md:561

FACT: SPEC-3's "counts toward the whole-pod replacement trigger stated below" is positionally correct. The `**Scrub model.**` paragraph SPEC-3 appends to is spec/05:453; the `**Slot cleanup:**` bullet is :545 and the whole-pod replacement trigger is :561, both below it. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,:545,:561

FACT: withholding `ReportSessionScrub` on the pre-`running` path falsifies no observability inventory row. `lenny_pod_session_reuse_count` is glossed as "number of sessions served by a single pod" in both inventories, which the withholding makes more accurate rather than less. — EVIDENCE: spec/16_observability.md:128; docs/reference/metrics.md:165

FACT: neither the pre-attached-failure retirement nor the unhealthy-threshold drain has a `reason` value on `lenny_gateway_pod_retirement_total` (its only values are `session_count_limit` and `scrub_failure_limit`; the drain increments `lenny_slot_pod_replacement_total` instead), so §7.1's exclusive-pod retirement disposition mints no inventory obligation. — EVIDENCE: spec/16_observability.md:12; docs/reference/metrics.md:163

USEFUL [standing context entries 112, 231, 299, 365, 366, 418; review-log.md:1295-1309]: the prior operational round's shard listed its four dead candidates with their killing evidence. Three of the four were candidates I rebuilt independently before reaching it. The operational lens on this proposal is structurally inert; a future operational reviewer should read review-log.md:1295-1309 first and spend the saved budget on the §5.2 accounting arithmetic instead.


### [spec.3.review-performance.1]

DECISION: empty findings list — BECAUSE the spec staging is byte-identical to the round-2 snapshot (only the review logs differ), so nothing my lens owns changed since the last performance pass, and I re-derived that pass's four grounds against the tree rather than trusting them. (1) No new control-plane write: the staging writes no CRD status, no Postgres row, no Redis key; it adds one int64 to seven bind-sequence responses plus `Shutdown`, and one extra `Shutdown` RPC per FAILED bind, so the amplification factor is the bind-failure rate, not the request rate. (2) `ReportSessionScrub` volume goes DOWN, because SPEC-3 withholds the report on the pre-`running` path. (3) No pod-wide serialization: the reclaim hold is keyed on the slot identifier and slotId == sessionId (spec/05:395), so N concurrent cleanups on one pod hold N distinct keys and collide only with a retry of the SAME session. (4) No net-new watch, informer cache, or hot key. — ALTERNATIVES: see the three dropped candidates below.
FACT: `diff -rq scratchpad/cp-snap/.../spec-r3 proposals/0081_.../` returns nothing, and `diff -rq spec-r2 spec-r3` differs only in the two review-log files. Round 2's fix stage made no edit to `*.spec-changes.md`. A lens that already ran on r2's text can scope itself to re-derivation. — EVIDENCE: scratchpad/cp-snap/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/spec-r3
FACT: the §5.2 whole-pod replacement threshold is `ceil(maxConcurrentSessions / 2)` with `failed` counted in a rolling 5-minute window and `leaked` counted persistently, so at `maxConcurrentSessions: 2` the threshold is 1 and a single windowed failure trips it. The proposal's edge-case arithmetic on this is correct. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561; spec/06_warm-pod-model.md:160
FACT: `sessionPolicy.slotRetries` defaults to 1 (two total attempts). Any residue that consumes a retry budget is consuming a budget of two, which is the right order of magnitude to keep in mind when judging "spends an attempt" residues. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:555
USEFUL [review-log-archive.md:14360]: the entry that says a performance/capacity lens should decide once on the `superseded`-plus-`ReleaseSlotReservation` occupancy question. I decided it: NOT a finding. Pre-proposal, a `BindReservedSlot` failure already calls `ReleaseSlotReservation` unconditionally (slotbinder.go:212-221) and `BindReservedSlot` never re-reserves on the client's retry ("The slot is not re-reserved and retried here", slotbinder.go:203-205), so the successor already runs on unaccounted occupancy in the shipped tree. The epoch changes only whether the successor SURVIVES; it does not change the counter arithmetic. Measured against the shipped design, which is the lens's baseline, there is no regression. The pre-existing gap is the already-recorded `BindReservedSlot` accounting defect. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:203-224
USEFUL [review-log-archive.md:16261, review-log.md:1318,:1322]: the standing WATCHOUT on "The hold lasts as long as the cleanup it covers" beside "is not a second bound on the hold" versus spec/05:545's "Cleanup timeout is `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds (minimum 5s enforced at runtime by the adapter)". I reached it independently and re-verified the ground for dropping it: `grep -rn "cleanupTimeout\|CleanupTimeout" pkg/` shows the value carried only to the WHOLE-POD scrub budget and to poolstore CRD validation, never enforced against a per-slot cleanup, and `removeSlotTree(st *slotState) error` takes no context at all. The staged disclaimer therefore agrees with the tree. The bar for a future round is unchanged: argue a harm that the identifier-scoping (held key == session id, so no cross-session collision) does not kill. I could not. — EVIDENCE: pkg/adapter/slot.go:210; pkg/adapter/session.go:262-271; pkg/gateway/runtime/poolstore/poolstore.go:567
UNVERIFIED: epoch ABA across an adapter PROCESS restart on a surviving gRPC ClientConn. The no-re-dial rule's stated ground is the caller's latch ("a caller that dialled a fresh connection would hold none"), not process identity, and grpc-go's ClientConn re-establishes its transport transparently to the same pod IP. If the adapter container restarts in place, the epoch counter restarts, and the client's retry of the same session lands early in the fresh process, a lagging compensation carrying epoch N could compare EQUAL against a different process's entry N and tear down a live session. Nothing in §4.7.1 requires the adapter to reject an epoch minted by a previous incarnation (for example by seeding the counter from a boot nonce or a monotonic clock). I did not file it: the failure degrades to the unfenced teardown the proposal already accepts elsewhere, and I could not establish the ordering is reachable before the §6.2 pre-attached disposition retires the pod. Archive:16339 records an earlier pass dropping the same candidate. A mechanism or concurrency lens with time should settle whether the epoch needs a per-process discriminator. — EVIDENCE: spec-changes.md:677 ("unique and strictly increasing within one adapter process ... never persisted"), :681 (the no-re-dial rationale)


### [spec.3.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability lens on the spec staging — BECAUSE every
recovery-path candidate I derived resolved onto an entry already in `## Standing context` (Settled, Traps,
Open or Deferred) or onto the round-2 refuted list, and the standing bar says a close variant of a refuted
finding costs two verifiers and closes nothing — ALTERNATIVES rejected, each with the entry that killed it:
(a) "a gateway replica crash mid-bind leaves the entry with no reclaimer" — pre-existing baseline; today no
compensation is sent at all, so the staging strictly improves it; (b) "the `superseded` release decrements
the Redis counter for a slot a live successor occupies, and at occupancy zero DELETEs the claim and retires
a serving pod" — the shipped `ReleaseSlotReservation` already hard-codes `leaked=false` on every failed bind
(Settled), so the claim DELETE is the pre-existing safety valve rather than something the staging adds, and
the over-assignment half is already carried as the UNVERIFIED "Transient over-assignment on the acknowledged
racing-start ordering"; (c) "the hold never terminates when the cleanup fails, because §5.2's `**Slot
cleanup:**` bullet says a leaked slot is not reclaimed until pod termination" — already OPEN and FILED as
"Does the hold survive its own timeout?"; (d) "the hold refuses `Attach`/`Interrupt`/`ReportUsage` mid-cleanup"
— declined four times on the vacuous-resolve reading; (e) "the ABORTED refusal is accounted as a transient
slot failure and drains a healthy pod at `UnhealthyThreshold(2)==1`" — MISTAKE-nearly-filed, two rounds.

FACT: SPEC-3's scrub-model append states the incomplete-cleanup accounting as "the adapter's `Shutdown`
response for that reclaim does not report a clean exit", naming only ONE of the two routes §7.1's
"did not complete" predicate carries (the other is a reclaim the adapter never answers). I judged it below
the bar: the colon clause reads as the accounting MECHANISM rather than as a second definition of the
predicate, the gateway's shipped discriminator `err != nil || !cleanly` covers the unanswered route anyway,
and the mirror-image finding ("§7.1's predicate is narrower than §5.2's") was refuted this window on exactly
the ground that all three sites now read on one predicate. Anyone tempted to file it should expect that
refutation. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:600 (the append) against :390 (§7.1).

FACT: the SPEC-5 insertion points are live and unambiguous in the current tree — spec/04_system-components.md:659
(`#### 4.7.1 Role and Gateway RPC Contract`) and :695 (`#### 4.7.2`) bracket the §4.7.1 block, and
spec/15_external-api-surface.md:1469 (`**SDK-warm demotion contract:**`) and :1471 (`#### 15.4.1`) bracket the
§15.4 blocks. The §5.2 anchors SPEC-2 and SPEC-3 quote (`**Slot cleanup:**` action list, `**Max retries:**`
pod-selection sentence, `**Scrub model.**`) all match spec/05_runtime-registry-and-pool-model.md byte for byte
at :545, :553-ish and :453. Re-verified this round; do not re-run the anchor sweep.

USEFUL [Standing context / Traps]: the refuted-family index (the six re-filed defect families, the
exclusive-pod family refuted eight times, the `concurrent_slots_exhausted` gloss refuted six times) is what
turned four hours of candidate generation into a decision not to file. It is the single highest-value part of
this log; keep it at length.

WATCHOUT: the shard-name glob hazard is real and still bites — `sed -n 'N,Mp' *spec-changes.md` in this
directory concatenates `.spec-changes.md` and `.non-spec-changes.md`. Also, `bash` output in this harness is
truncated at ~2KB for large reads, so the standing context has to be read by writing a `fold`ed copy to /tmp
and using the Read tool with offset/limit; budget for that.


### [spec.3.review-security.1]

DECISION: filed exactly one finding, the SPEC-3 "under no deadline" collision with the untouched
`**Slot cleanup:**` bullet — BECAUSE the standing context names it as the ONE live form of the
reclaim-hold-bound family and tells you to argue it from the bullet rather than from the absence of
enforcement (review-log.md standing context, "Do NOT file the reclaim hold's bound as unenforced"
and "MISTAKE: the r7 fix went one step PAST the round-6 refutation") — ALTERNATIVES: every other
security angle I built died on a standing entry, listed below.

FACT: the security lens has almost no live surface left on this proposal, and the standing context
already kills the four obvious angles by name. Do not rebuild them:
  (1) "SPEC-3 sources the leak bound from an in-pod self-report (`exited_cleanly`)" — the spec's
      pod-self-report posture is written only about checkpoint chunk sizing (spec/11:37, spec/10:147,
      spec/13:196) and the adapter is a platform-controlled container, not the untrusted agent.
  (2) "the staged exclusive-pod carve-out breaks §13.1 sequential-reuse credential isolation" —
      killed twice; spec/05:461 step 0 and :471 step 6 sweep the credential residue at the
      occupancy-zero boundary regardless, and CODE-4 revokes the lease gateway-side unconditionally.
  (3) "§5.2's hold predicate `create or resolve a registry entry` swallows the §11.4 revoke
      `Shutdown` while §15.4 carves it out" — the hold starts at the DEREGISTRATION, so during the
      hold no entry exists and the "or resolve" half is vacuous. Four lenses built it.
  (4) any §15.4 completeness finding — five refuted on the same ground.

FACT: `lenny_adapter_leaked_slots` is attributed to the ADAPTER by spec/06:160 ("The adapter exposes
a `leaked_slots` count in the pod's health metadata") and emitted by the GATEWAY in code
(pkg/gateway/.../gatewaymetrics_credential.go:219-223). Pre-existing divergence; SPEC-3 names the
gauge without attributing a component, so it does not widen it. EVIDENCE: spec/06_warm-pod-model.md:160

FACT: §11.4's revoke fan-out really does send `Shutdown` (spec/11_authentication-and-authorization.md:263,
:270), so SPEC-1's claim that it sends the unfenced form is checkable and true under §4.7.1's caller
rule ("A `Shutdown` carries an epoch only when it compensates a bind attempt the caller abandoned").
The revoke path is the one place a security reviewer should re-check if the caller rule ever changes.
EVIDENCE: proposals/0081_.../0081_....spec-changes.md:682

WATCHOUT: the snapshot the brief names (`spec-r3`) is byte-identical to the live proposal, as it has
been in every round of this window. The last real spec-changes delta is `spec-r1-prefix` → live
(15:04 → 15:20): two rewordings of the no-re-dial rationale in the §7.1 paragraph and in §4.7.1's
caller rules. Everything else this round is unchanged text.
EVIDENCE: scratchpad/cp-snap/0081_.../spec-r1-prefix vs proposals/0081_...

OPEN: is the §11.4 revoke `Shutdown` racing an in-flight `StartSession` on the SAME session a
revocation-gate bypass under the accepted residue "an abandoned attempt's start whose claim runs
after the reclaim completed re-creates the entry" (spec-changes.md:248-259)? I judged it
pre-existing (the same late-start re-creation exists today) and out of this proposal's charter, and
did not file. A security reviewer on a later proposal should decide whether §11.4 needs a
start-refusal of its own.

### [f1.open-decisions.human-decisions.12]

DECISION: summary open-decision entry 12 ("does the adapter's slot-identifier reclaim hold end
when a cleanup fails, or is it permanent for the life of the pod on a slot that goes `leaked`?")
is resolved as "the hold ends when the cleanup pass that took it returns, whether that cleanup
succeeded or failed", and the entry is deleted from `## Open decisions for human to make`.
Entries 11 and 15 through 18 keep their identifiers and were not renumbered.

DECISION: no staged spec or non-spec text was edited, and none is owed, because the staging
already states the answer as a requirement. spec-changes.md:601 states the terminal twice
("until the cleanup that reclaims the slot has finished", "The hold lasts as long as the cleanup
it covers") and spec-changes.md:711 mirrors it in the §15.4 companion block; non-spec-changes.md:197
stages `defer release()` unconditionally, taken before `closeErr` is computed and read by no later
branch, with :928 recording that `release` is "always non-nil and idempotent" and that
`reclaimSlotLocked` is "the only site that takes a hold and the only one that ends one". Adding a
`leaked` clause to the hold paragraph is barred by this log's own standing DECISION that the hold
"carries no `leaked` clause".

FACT: the two terminals the entry treated as colliding are different objects. The `leaked`
terminal is the pod's Redis slot-counter occupancy, which spec/06_warm-pod-model.md:160 states a
`leaked` slot "remains counted in" until pod termination and which spec/05_runtime-registry-and-pool-model.md:545
restates as "the slot is not reclaimed until pod termination". The reclaim hold is adapter-local
and in-memory over the slot identifier, and spec-changes.md:601 states it is "a property of the
identifier rather than of an entry". A `leaked` slot therefore keeps its occupancy without keeping
its identifier held.

FACT: shipped behaviour is already at the staged answer, so the resolution preserves it rather
than changing it. The adapter today holds no identifier at all and discards the tree-removal
error outright: `_ = removeSlotTree(st)` at pkg/adapter/session.go:271, and the same discard at
pkg/adapter/slotsession.go:217 and pkg/adapter/holdstate.go:254.

FACT: the summary's `## Open decisions for human to make` preamble was corrected in the same edit,
because deleting entry 12 falsified it twice. The clause "entry 12 was added after the spec loop
converged" is gone, and the sentence recording which entries have left the section now reads
"Entries 12, 13 and 14", with the residue clause scoped to 13 and 14, which are the two that left
a row under `## Defects in the shipped tree that this proposal does not stage`.

FACT: no staged change file carries an `## Open decisions for review` section, so this item had
nothing to delete outside the summary. This confirms the archived WATCHOUT that recorded the same.

FACT: entry 18 rests on a reclaim hold "that never clears". This resolution does not falsify it:
the path entry 18 names is a reclaim the adapter never answers, where the cleanup pass itself
never returns, rather than a cleanup that returns having failed. Entry 18 stands unedited.

### [f1.open-decisions.human-decisions.17]

DECISION: summary open-decision entry 17 (the create-time-reserved retry's residue) stays with
the human and keeps its identifier. It was rewritten in place under
`## Open decisions for human to make` so it is answerable in one sitting: the harm is stated
first in plain terms (a compensating teardown that lands after a second attempt has started its
session and answered its client closes that live session, and the gateway records the reclaim as
a clean `reclaimed`), then a recommendation to accept it as recorded at low confidence, the
ground for that recommendation, the three closures and why each lost, and what each answer costs.

DECISION: no staged spec or non-spec text was edited. The staging already carries acceptance,
and this phase may not stage an answer to an item gated as the human's.

FACT: the residue reaches only a create-time-reserved slot. `bindConcurrentSlot` routes a row
with a non-empty `PodAssignment` on a non-recovery row through `BindReservedSlot`
(pkg/gateway/sessionserver/start.go:2594-2604) and never enters `runWithQueue` or
`applySlotRetryPolicy`, so the staged §5.2 placement constraint does not move that attempt off
the reclaiming pod. Re-verified against the tree for this firing.

FACT: the entry cited the residue as recorded under `## Decisions`, and no such section exists
in any file of this proposal. The record is `## Edge cases and accepted failure modes` in the
staged spec changes, whose bullet "A compensation still on the wire when a retry adopts the
surviving entry is not fenced" states the residue and prices the closure. The false citation was
corrected in the entry and in the section preamble, which repeated it for entry 9's departure.

FACT: the section preamble's sentence "None of them carries a recommendation" was falsified by
this edit and now reads that an entry carries a recommendation where the review derived one.
Entries 11, 15, 16 and 18 were not touched.

OPEN: the recommendation is low-confidence because the review found no metric, alert, or runbook
in the staged deliverable that would surface a session torn down on this path. A human answering
"accept" may want observability filed separately.


### [f1.open-decisions.out-of-scope-defects.threshold-retune]

DECISION: retuning the `ceil(maxConcurrentSessions/2)` unhealthy threshold stays out of scope
and no row for it is added to `## Defects in the shipped tree that this proposal does not stage`.
I wrote nothing into the proposal, because it already carries the item exactly as adjudicated:
the Non-goals bullet stands verbatim at `0081_....summary.md:315-316`, the defects section
(`0081_....summary.md:424-651`) carries no threshold row, and the problem statement's NAMED OUT
OF SCOPE paragraph names the retuning at `0081_....problem-statement.md:141-142`.
FACT: the line numbers in the earlier firing's block above (`[f1.open-decisions.threshold-retune]`)
have drifted. The Non-goals bullet is now at `:315-316` rather than the `:312-313` that block
cites; its content is unchanged. Nothing was edited to restate it, because a log entry records a
firing as it ran.
FACT: no staged deliverable touches the formula, the denominator, or the drain block. The
threshold is `(maxConcurrent+1)/2` clamped to 1 (`pkg/gateway/runtime/slothealth/slothealth.go:214-220`),
read by `Tracker.Unhealthy` (`:136-140`), and its production trigger and drain block sit at
`pkg/gateway/sessionserver/start.go:2858-2873`. CODE-5 factors that block into
`accountSlotFailure` and adds callers to it; it moves no threshold.
FACT: the behavioural cost of new bind paths reaching the unchanged trigger is recorded as a cost
of a staged deliverable rather than as a defect, under `**Watch out for.**` at
`0081_....non-spec-changes.md:1764-1773`, whose closing sentence states that the §7.3 re-attach
reaches the accounting for the first time and so drains a replacement pod at
`maxConcurrentSessions: 2`. The staged spec text states the same at
`0081_....spec-changes.md:156-158`.
WATCHOUT: a later pass must not read this call as an assertion that the threshold is correct. The
proposal is silent on whether the value is right, and that silence is the reason the defects
section carries no row: every entry in that section is a defect the proposal knowingly leaves,
and a tuning question is not one.

### [f1.open-decisions.summary-format]

FACT: the summary format pass for firing 1 changed nothing. `0081_....summary.md` already carried exactly the required sections, in order and with no others: `# Summary: ...`, `## Summary` (a container holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**` and no prose of its own), `## Goals`, `## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, and `## Deliverable index` last.
FACT: no `### Retired` block or equivalent stood inside `## Open decisions for human to make`, and no meta-list of staged items with proposed dispositions stood anywhere in the file. The two earlier applies in this firing had already deleted entry 12 and rewritten entry 17 in place.
FACT: the human's section carries entries 11, 15, 16, 17 and 18, which is exactly the set this firing left with the human: 11, 15, 16 and 18 were gate-refuted as resolvable and 17 was gated as the human's and applied. Entry 12 is absent, matching its applied resolution. Every identifier is verbatim and none was renumbered.
FACT: every out-of-scope marker this firing carried resolves to a row already standing under `## Defects in the shipped tree that this proposal does not stage`, checked one by one against the thirteen bullets at `0081_....summary.md:426-651`. The threshold-retune item correctly has no row there and stands as the Non-goals bullet at `:315-316`.
FACT: the section preambles were read against the entries they describe and none is falsified. The open-decisions preamble's account of entries 9, 12, 13, 14 and 15 through 18 matches the entries below it, and its clause "carries a recommendation where the review derived one" matches entry 17 carrying one and 11, 15, 16 and 18 carrying none. `## Defects in the shipped tree that this proposal does not stage` and `## Impacts on other proposals` open directly on their entries and carry no preamble to correct.
FACT: `## Impacts on other proposals` carries one row per proposal with no duplicate subject, and no prose about another proposal stands outside it. `## Deliverable index` is preserved line for line in last position, including its closing paragraph on tests and CONF-1.
FACT: this pass derived no correction owed to a file it may not edit, so it defers nothing, and it placed no content it could not house, so it opens nothing.



### [non-spec.1.fix-G1.1]
DECISION: `compensateFailedSlotBind`'s request-derived parameters narrow to `sessionID string, cleanupTimeoutSeconds int, maxConcurrentSessions int32` — BECAUSE `Binder.Resume` holds a `podsession.ResumeRequest` and no `SlotBindRequest` is in scope there, and `ResumeRequest` carries all three fields under the same names (pkg/gateway/podlifecycle/podsession/binder.go:605, :645, :668) — ALTERNATIVES: a synthetic `SlotBindRequest` built on the resume path (invents a conversion the proposal never sanctions and carries a mostly-zero struct), and a shared `compensationParams` type (a third parameter type for three scalars, forcing a refactor of two shipped request structs).
FACT: `ResumeRequest` (pkg/gateway/podlifecycle/podsession/binder.go:601-670) carries `SessionID`, `MaxConcurrentSessions` and `CleanupTimeoutSeconds`; `Binder.Resume` is at binder.go:1590. That is why the narrowing works with no conversion. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:605,645,668,1590
WATCHOUT: the staged body reads the request at four places, not one: the budget call, the `ShutdownReclaim` call, and both `log.Printf` lines. Editing only the declaration leaves the block non-compiling in a new way. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:553-583
DECISION: the narrowed list is stated in exactly two prose sites for CODE-4 (the declaration itself and one rationale sentence pointing at CODE-5's identical narrowing), and the implementation checklist's S12 was left unchanged — BECAUSE the design marked the S12 clause optional and a third copy of a parameter list is how the two statements drift apart, which is the mechanism that produced this finding — ALTERNATIVES: mirroring S13's parameter-list clause into S12, rejected for that reason.


### [non-spec.1.fix-G2.1]
DECISION: Closed the §7.3 resume classification gap with one `status.Code(err) == codes.Aborted` arm in `isTransientPodClaimError`, owned by CODE-5 — BECAUSE the refusal crosses a process boundary and only the gRPC status code survives it, and one arm covers both `Aborted` producers on that path (CODE-6's reclaim-hold refusal and CODE-2's start/resume rollback) — ALTERNATIVES: `errors.Is(err, errSlotReclaimInProgress)` or CODE-6's `isSlotReclaimInProgress` from the gateway (not implementable: both identifiers are unexported and adapter-local, and no production file under `pkg/gateway` imports `pkg/adapter`); a gateway-side typed error minted in `adapterclient` (a second mechanism for one bit the status code already carries); deleting the §7.3 resume from the reclaim-hold caller enumeration (it is reachable).
WATCHOUT: `SlotBindError.Reason()` and `isTransientPodClaimError` are two different classifiers on two different paths, and the proposal used to explain the resume caller's retryability with the wrong one. `Reason()` decides whether the §5.2 retry policy places another attempt; `isTransientPodClaimError`, read by `holdOrFailOnResumeError`, decides whether the row lands in `awaiting_client_action` or terminal `failed` once the retry budget is spent. A fix that touches one must state which — EVIDENCE: pkg/gateway/sessionserver/start.go:3609 (holdOrFailOnResumeError), :3648-3681 (isTransientPodClaimError)
FACT: `isTransientPodClaimError` today matches only typed errors and sentinels, with one exception: its `SetupCommandFailure` arm reads `status.Code(setupFail.Cause)`. A bare status error therefore falls to `return false` and demotes the row. Adding a status-code arm extends a pattern the function already carries — EVIDENCE: pkg/gateway/sessionserver/start.go:3672-3680
FACT: `status.Code` resolves through a wrapper: grpc v1.80.0 `status.FromError` falls back to `errors.As` for the `GRPCStatus()` interface, and `*SlotBindError` has `Unwrap`. So the arm fires through the `*SlotBindError` CODE-4 makes `Binder.Resume` return — EVIDENCE: $GOMODCACHE/google.golang.org/grpc@v1.80.0/status/status.go:112-124; pkg/gateway/podlifecycle/podsession/slotfailure.go:74
FACT: `holdOrFailOnResumeError` and `isTransientPodClaimError` are unexported, and `pkg/gateway/sessionserver/start_test.go` is `package sessionserver_test`, so a test for them belongs in `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go` (`package sessionserver`, carries the `seedResumingRow` fixture) — EVIDENCE: pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go:3,:23
FACT: the wire envelope was already right. `writePodClaimError`'s default arm answers a retryable 503 with `Retry-After` for an unrecognised cause, and the resume caller passes `RESUME_FAILED`. Only the row-state classifier disagreed with it — EVIDENCE: pkg/gateway/sessionserver/start.go:208-219, :3517
WATCHOUT: do not open an `Aborted` case in `SlotBindError.Reason()`. CODE-5's own text forbids it, because CODE-2's rollback relies on the same transient default; an `Aborted` case there would make both the rollback and the hold refusal non-transient on the concurrent-bind path — EVIDENCE: non-spec-changes.md, CODE-5's reclaim-in-progress paragraph
CORRECTS [pre-amendment CODE-6 rationale]: "`Aborted` is chosen because `SlotBindError.Reason()` has no case for it" was only half the reason and named the classifier the §7.3 caller never reaches. The rationale now names both classifiers and states that neither reads the adapter-local sentinel.


### [non-spec.1.fix-G3.1]

DECISION: Registered each new `tests/spec-map.json` entry in the checklist step that CREATES the file it maps (S9 the tier-3 directory entry, S10 the tier-7a and tier-9 files, S14 the tier-10 file) — BECAUSE the checklist promises every step is green on the tiers its own line names, and a trailing registration step leaves tier 0 red from S9 through S13 — ALTERNATIVES: proposal 0073's trailing S22 precedent (rejected: 0073 had no owning step at all), `tests/spec-map-exceptions.yaml` (rejected: the validator does not parse per-file exceptions).

DECISION: The tier-3 spec-map entry is the DIRECTORY form `tests/tier3_contract/adapter_bind_epoch/...` registered at S9 — BECAUSE two of that subsection's cases now land at S10, and a per-file registration would need a second entry then; a mapped ancestor directory satisfies `validateTestFilesMapped`.

FACT: `validate-maps` is tier 0 and `validateTestFilesMapped` matches a walked `_test.go` against exact paths and every ancestor directory in the map, after stripping `::TestName` and `/...`. Of the tiers 0081 adds files to, only `tests/tier4_integration` is mapped as a bare directory; tier3_contract, tier7a_load_local, tier9_security and tier10_conformance are mapped file by file, with only deeper subdirectory globs. EVIDENCE: cmd/lenny-test/cmd_validate.go:716-793, :125-139, :711-713; tests/spec-map.json:1997, :2944, :3523.

FACT: `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208` `TestShutdownMessagePostRemovalDescriptor_spec_4_1` pins `ShutdownRequest` and `ShutdownResponse` to a CLOSED field set through `assertFieldSet` (`:259`, error at `:264-267`). SCHEMA-1's two additive fields make it fail, so `buf breaking` silence proves nothing about shipped tests. EVIDENCE: shutdown_recycle_wire_test.go:208,:211-217,:233-238,:259-270.

CORRECTS [design for finding "SCHEMA-1's two new proto fields break the shipped tier-3 exact-field-set gate"]: the design's supporting claim that `checkpoint_stream_wire_test.go:151` is a second descriptor pin reading "CheckpointStart has exactly six fields" is misleading. That file drives `assertFields` (`:92`), which iterates only its `want` slice and explicitly never asserts the total (comment at `:145`), so it is NOT a closed set and an added field would not move it even on a message SCHEMA-1 did open. The staged text now says that instead.

WATCHOUT: the tier-3 mid-session-upload bullet used to read "write it before the schema edit lands", which contradicts the case needing the `bind_epoch` field S8 introduces and contradicts the new S9 attribution. It now reads that the case is written with the rest of S9's tier-3 work rather than deferred. EVIDENCE: non-spec-changes.md, `### Wire-contract tests for SCHEMA-1, CODE-1 and CODE-6, tier 3`.

WATCHOUT: the caller-latch tier-3 bullet is now explicitly stated to drive `Client.ShutdownReclaim` and `Client.BindEpoch` rather than the gateway's `compensateFailedSlotBind`. That is what keeps it at S9 (CODE-6) and keeps S12 off tier 3. Do not re-word it back to "the compensation sends", which silently moves it to a step whose tier line omits tier 3.

FACT: S10 now declares `Tiers 0, 1, 3, 7a, 9`. Any later edit that moves the two `Shutdown` outcome cases must move that tier 3 with them.

UNVERIFIED: the summary's Deliverable index line for SCHEMA-1 still lists only `schemas/lenny-adapter.proto` and `tests/claim-map.json`, and neither the shipped wire test nor `tests/spec-map.json` was added there, on the ground that test files are tracked under `## Testing` and the Deliverable index carries deliverables. A later lens should confirm that convention rather than read the line as an exhaustive file list.


### [non-spec.1.fix-G4.1]

DECISION: DOCS-2 (`docs/reference/adapter-contract.md`) folds into the existing S6 docs step rather than taking a step of its own — BECAUSE a new step renumbers S7 through S14 and every `Depends on:` line naming them, for no separation the docs lane needs; S6's `Depends on:` widened from `S5` to `S2, S5` because DOCS-2 mirrors the §4.7 row SPEC-1 lands at S2 — ALTERNATIVES: a step after S6 (renumbering cost), and putting the whole epoch contract inside the table row (rejected, see WATCHOUT below).

FACT: `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` reads the `Shutdown` row through `lineContaining(page, "| `Shutdown` |")`, so the row must stay ONE physical line. That is why DOCS-2 splits the content: the two teardowns and the epoch precondition stay in the row, and the epoch/reclaim-hold contract goes in a prose block after `**Scrub responsibilities.**`. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-310

FACT: `tests/claim-map.json` is GENERATOR OUTPUT. `TestClaimRegisterIsReproducibleFromItsGenerator` re-runs `scripts/seed-claim-register.py` and fails unless the committed file is byte-identical, and it fails per-row for a committed row the generator does not emit. Rows with no status-table source go in the `EXPLICIT` list; rows are emitted sorted by claim, so no insertion position is authored. `SURFACE_OVERRIDES` cannot mint a row (raises `SystemExit` when it matches no table row). — EVIDENCE: tests/tier0_static/claim_register_generator_test.go:19-27,:44,:74-78,:116-121; scripts/seed-claim-register.py:168-197,:364-368,:377-378,:398

WATCHOUT: any proposal that adds a claim-register row must edit `scripts/seed-claim-register.py` and state the regeneration run. Hand-editing `tests/claim-map.json` turns tier 0 red on landing and the next seeding run drops the row silently. — EVIDENCE: scripts/seed-claim-register.py:11-13

FACT: Go declares a struct's fields in the file declaring the struct, so `Server.bindEpoch`/`Server.reclaiming` land in `pkg/adapter/server.go` (`type Server struct` at :59) and `slotState.epoch` in `pkg/adapter/slot.go` (`type slotState struct` at :21), whatever file holds their logic. CODE-6's "new file `bindepoch.go`" bullet read as though the fields lived there. — EVIDENCE: pkg/adapter/server.go:59; pkg/adapter/slot.go:21

WATCHOUT: CODE-6's file list exists in THREE places and they drift independently: the `### CODE-6 ·` heading (deliberately partial), `## Files touched on application (non-spec)`, and summary.md's Deliverable index line. The first is not maintained as exhaustive; the other two are and both were corrected this round. A future edit to CODE-6's file set must touch both exhaustive lists. — EVIDENCE: non-spec-changes.md `### CODE-6 ·` heading; summary.md Deliverable index CODE-6 line

USEFUL [standing context, DEFERRED docs/reference/adapter-contract.md:75 and :64]: it had already scoped the edit precisely, including the reconciliation that `:81`, `security-principles.md:33`, `execution-modes.md:68` and `multi-tenancy.md:72` are NOT edit sites (SPEC-3 widens them). That saved a full docs-corpus sweep, and DOCS-2 leaves all four unedited.


### [non-spec.1.fix-design-G1.1]

DECISION: narrow `compensateFailedSlotBind` to scalars — `(ctx, cl, sessionID string, cleanupTimeoutSeconds int, maxConcurrentSessions int32, sandboxName, slotID string) bool` — rather than convert a `ResumeRequest` into a `SlotBindRequest` on the §7.3 path — BECAUSE the body reads exactly three fields, all three of which `podsession.ResumeRequest` carries under the same names, and the deliverable already contains two scalar-parameter precedents: CODE-5's `accountSlotFailure(… pool string, maxConcurrentSessions int32 …)` (non-spec-changes.md:745-756) and CODE-4's own `slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions int32)` (:515). ALTERNATIVES: (a) build a synthetic `SlotBindRequest` in `Binder.Resume` — rejected, invents a conversion the proposal never sanctions and puts a bind request on a path that has none; (b) a new shared `compensationParams` struct — rejected, a third parameter-carrying type for three scalars, and it would make CODE-4 and CODE-5 disagree on style inside one proposal; (c) drop `slotID` since `SlotID == SessionID` on the bind paths — rejected, the resume branch names a separately reserved slot id and the parameter is log-only.

FACT: `podsession.ResumeRequest` carries `SessionID` (binder.go:606), `MaxConcurrentSessions int32` (:645) and `CleanupTimeoutSeconds int` (:668); `SlotBindRequest` is a different type at slotbinder.go:26. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:602-668

FACT: nothing outside CODE-4's own code block states this function's parameter list. The other nine proposal mentions (non-spec-changes.md:65,496,521,635,1118,1129,1745,1830; summary.md:107,186) are bare symbol references or behavioural prose that hold under either signature, and no file under spec/, docs/, schemas/ or charts/ names it. So the fix is confined to non-spec-changes.md:553-576 plus the :635 call site plus the :698-706 resume paragraph. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:1826-1837

WATCHOUT: the four `req.` reads inside the function body are not all at :554. They are `req.CleanupTimeoutSeconds`/`req.MaxConcurrentSessions` at :554, `req.SessionID` at :561, and `req.SessionID` again in the two log lines at :566 and :578. A fixer that only edits the signature and the first line leaves the block non-compiling in a way the round after has to catch. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:553-580

WATCHOUT: implementation-checklist.md S13 already writes out `accountSlotFailure`'s narrowed parameter list in prose; S12 writes out no signature at all. Keep it that way or mirror it in one clause, but do not add a second, differently-worded statement of the same parameter list — two prose copies of one signature is exactly the drift that produced this finding.


### [non-spec.1.fix-design-G2.1]

DECISION: Close finding 7 with ONE arm in `isTransientPodClaimError` keyed on the gRPC STATUS CODE — `case status.Code(err) == codes.Aborted: return true` — owned by CODE-5 (`pkg/gateway/sessionserver/start.go` is CODE-5's file). BECAUSE the refusal crosses a process boundary: `errSlotReclaimInProgress` is minted in `pkg/adapter/bindepoch.go` and the gateway sees only a wire status, so the only signal that survives is the code. The staged §15.4 text already declares `ABORTED` the platform's transient wire classification (spec-changes.md:711), so a code-keyed arm makes the gateway's two adapter-failure classifiers (`SlotBindError.Reason()`'s transient default and `isTransientPodClaimError`) agree by construction, and it also covers CODE-2's `Resume` rollback, which answers the same code on the same path. ALTERNATIVES: (a) the reviewer's own suggestion, `errors.Is` against the sentinel or CODE-6's `isSlotReclaimInProgress` — NOT IMPLEMENTABLE, see MISTAKE below; (b) a new gateway-side typed error translated in `adapterclient` — a second mechanism for one bit the status code already carries; (c) deleting the §7.3 resume from the three-caller enumeration — unavailable, the resume carries no pod exclusion and `SlotID == SessionID`, so it can re-place the same session on the same held identifier.

MISTAKE: the finding's `suggested_fix` proposes `errors.Is` against `errSlotReclaimInProgress` or CODE-6's `isSlotReclaimInProgress`. Neither is possible. `pkg/adapter` is imported by NO production file under `pkg/gateway` (only component tests import it), both identifiers are unexported, and sentinel identity does not survive gRPC. A fixer taking that suggestion literally writes code that cannot compile or, worse, a message-string match. EVIDENCE: `pkg/adapter` import grep over `pkg/gateway/**/*.go` returns test files only.

FACT: `status.Code(err)` DOES walk the wrap chain — grpc-go v1.80.0 `status.FromError` falls through to `errors.As` for `interface{ GRPCStatus() *Status }`. EVIDENCE: /home/ec2-user/go/pkg/mod/google.golang.org/grpc@v1.80.0/status/status.go:96-124. So the arm fires through the `*SlotBindError` wrapper both resume branches produce (CODE-4's S12 makes `Binder.Resume` return one too).

FACT: the RESUME row-state classifier and the RESUME WIRE ENVELOPE are two different functions and only one is broken. `writePodClaimError`'s default arm already answers the retryable 503 `RESUME_FAILED` for any unrecognised error, and `writeSetupCommandError`'s doc comment already names `Aborted` among the transient codes. Only `holdOrFailOnResumeError`/`isTransientPodClaimError` demotes the row. EVIDENCE: pkg/gateway/sessionserver/start.go:87 (writePodClaimError), :208 default arm, :3609 holdOrFailOnResumeError, :3648 isTransientPodClaimError.

WATCHOUT: the finding names `pkg/gateway/sessionserver/start_test.go` as the test home. That file is `package sessionserver_test` and cannot see either unexported function. The correct home is the existing internal test `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go` (`package sessionserver`), which already carries the `seedResumingRow` fixture and a `holdOrFailOnResumeError` table at :48-110. EVIDENCE: pkg/gateway/sessionserver/start_test.go:3 vs resume_setup_demotion_internal_test.go:3.

FACT: only one production site in the tree answers `codes.Aborted` today — `pkg/adapter/checkpoint.go:115`, the checkpoint op lock, which is not on a resume bind path. A broad `Aborted` arm therefore widens nothing unintended. EVIDENCE: `grep -rn "codes.Aborted" pkg/ cmd/ --include=*.go | grep -v _test.go`.

FACT: both §7.3 resume branches reach the adapter bind sequence. The snapshotless branch goes `resumeOnPod` → `startOnPod` → `bindConcurrentSlot` → `applySlotRetryPolicy`; the checkpoint-restore branch calls `podBinder.Resume` directly. EVIDENCE: pkg/gateway/sessionserver/start.go:3943-4045, :2353 (bindConcurrentSlot call inside startOnPod), :2594.

DECISION: non-spec-changes.md:1719 is IN SCOPE, not a separate finding. BECAUSE the sentence is true about the retry-policy accounting and silent about the row state, and after the arm lands its "what records it differs by caller" account for the §7.3 resume is the wrong half of the mechanism. It gains the second half rather than being replaced.

OPEN: this design edits spec-changes.md:190-196 (proposal commentary under `## Edge cases and accepted failure modes`, NOT staged spec text) from a non-spec loop. The staged spec text at :601 and :711 stays true and needs no edit. If the loop's write lease refuses spec-changes.md, the :190-196 qualification is the one deliverable that must be deferred to the spec lane.


### [non-spec.1.fix-design-G3.1]

FACT: `validate-maps` is tier 0 and its `validateTestFilesMapped` walks EVERY `_test.go` under
`componentAndAboveTierDirs()` and fails any file no `tests/spec-map.json` entry names by exact
path or by a mapped ancestor directory; per-file exceptions are NOT parsed. Only
`scaffolds_test.go` is exempt by name. — EVIDENCE: cmd/lenny-test/cmd_validate.go:716-793,
:711-713, :125-139.

FACT: of the tiers 0081 adds files to, only `tests/tier4_integration` is mapped as a bare
directory (tests/spec-map.json:3523). `tier3_contract`, `tier7a_load_local`, `tier9_security`
and `tier10_conformance` are mapped file by file, the only directory entries being deeper
subdirectories (`tests/tier3_contract/adapter_checkpointbarrier/...` at :1997,
`tests/tier9_security/pentest` at :2944/:4360). So extending a tier-4 file is free and creating
any file under the other four is a tier-0 failure until its entry lands. — EVIDENCE:
tests/spec-map.json:1997,2944,3523,4360.

FACT: the shipped tier-3 exact-field-set gate on the shutdown messages is
`TestShutdownMessagePostRemovalDescriptor_spec_4_1`. `assertFieldSet` errors on ANY declared
field number missing from `want`, so `ShutdownRequest` field 7 and `ShutdownResponse` field 3
turn it red the moment SCHEMA-1's regenerated stubs land. `buf breaking` is silent on an
additive edit and is the wrong gate to reason from. — EVIDENCE:
tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-238,:257-270.

FACT: it is the ONLY such closed-field-set gate over the nine messages SCHEMA-1 touches. The
other descriptor pin in tier 3, `checkpoint_stream_wire_test.go:151` (`CheckpointStart` has
exactly six fields), is on a message SCHEMA-1 does not open, and a grep for `assertFieldSet` /
`Fields().Len()` across `tests/` returns nothing else on those messages. The round-trip test in
the same file is unaffected because a zero-valued new field serialises to nothing. — EVIDENCE:
tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:77,:151.

DECISION: each new test surface's spec-map entry lands in the SAME checklist step that creates
the file (S9 tier-3 directory entry, S10 tier-7a and tier-9 file entries, S14 tier-10 file
entry), and the tier-3 entry is a DIRECTORY entry `tests/tier3_contract/adapter_bind_epoch/...`
— BECAUSE a trailing step that registers all four at the end leaves tier 0 red on every
intermediate commit from S9 to S13, which the checklist's own per-step tier rule forbids, and a
directory entry keeps S10's added file in that directory covered without a second entry.
ALTERNATIVES: a dedicated final step, as proposal 0073's S22 did — rejected, 0073 needed one
because no step owned the file at all, whereas here every file has an owning step; per-file
tier-3 entries — rejected, they need re-editing when finding 4 moves the two `Shutdown` cases
to S10.

DECISION: finding 4 is closed by adding tier 3 to S10 and annotating the tier-3 subsection
per case, keeping the caller-latch bullet at S9 and stating it drives `Client.ShutdownReclaim`
and `Client.BindEpoch` — BECAUSE `pkg/gateway/runtime/adapterclient/client.go` (the latch,
`BindEpoch`, `ShutdownReclaim`) is CODE-6's, landing at S9, so no tier 3 need be added to S12.
ALTERNATIVES: driving that bullet through `compensateFailedSlotBind` (CODE-4) and adding tier 3
to S12 — rejected as a second tier-3 site for one property; moving the epoch comparison into
CODE-6 so S9 can carry everything — rejected, it merges two deliverables and contradicts
CODE-1's Targets note that CODE-6 "defines none of them".

WATCHOUT: do not answer finding 5 by writing a fresh field-set assertion inside the new
`tests/tier3_contract/adapter_bind_epoch/` directory. That leaves the shipped
`gatewaycontrol_scrub` gate failing and S8 cannot go green on the tier 3 it declares. The edit
belongs in the shipped file, in SCHEMA-1's own step, and its `// diagnosis:` (which today reads
"a field was added or dropped") needs the epoch fence and the reclaim outcome named.
EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:202-207.

DECISION: `tests/spec-map.json` and the shipped wire test go into `## Files touched on
application (non-spec)` and into the step lines, and NOT into summary.md's Deliverable index —
BECAUSE that index carries deliverables and the proposal states tests are not deliverables
(the SCHEMA-1 bullet's `tests/claim-map.json` is a claim register, not a test file). Recording
this so a later round does not re-file it as a missing index entry.

UNVERIFIED: nobody has checked whether tier-1 tests under `pkg/adapter` or
`pkg/gateway/runtime/adapterclient` compare a whole response message by value (`proto.Equal` or
a struct literal) against one of the seven responses gaining `bind_epoch`. The tier-3
descriptor sweep above is complete; the tier-1 value-comparison sweep is not. A fixer or a
later lens should grep those packages for `proto.Equal` and for composite literals of the seven
response types.


### [non-spec.1.fix-design-G4.1]

DECISION: DOCS-2 (`docs/reference/adapter-contract.md`) FOLDS INTO THE EXISTING S6 docs step rather than getting a step of its own — BECAUSE a new step renumbers S7 through S14 and every `Depends on:` line that names them, and the checklist already has a docs lane; S6 only needs its dependency widened to `S2, S5` (SPEC-1 at S2, SPEC-5 at S1 which S2 depends on, SPEC-3 at S4 which S5 depends on) — ALTERNATIVES: a new S7 docs step (rejected: renumbering nine steps for no separation of concerns); putting the adapter-contract edit inside DOCS-1 (rejected: DOCS-1 is scoped to one table row on a different page and its own prose says so).

DECISION: DOCS-2 splits into a REWRITTEN one-line `Shutdown` table row plus ONE new prose block after the Gateway-to-Adapter table — BECAUSE the tier-11 gate reads the row with `lineContaining(page, "| `Shutdown` |")`, i.e. one physical line, and the row already runs four sentences; the epoch contract and the reclaim hold are what §15.4 publishes to third-party authors and belong in prose beside `**Scrub responsibilities.**` — ALTERNATIVES: cramming the epoch, the three outcomes and the hold into the row (rejected: unreadable and it makes the gate's single-line read carry a paragraph); a new `##` section (rejected: larger than the content warrants and it would need its own nav/reading-order thought).
EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-310; docs/reference/adapter-contract.md:75,:84

WATCHOUT: `docs/` prose may not cite spec section numbers (`.claude/rules/doc-content.md`, "Verify against the spec before asserting behavior"). DOCS-2's text must state the epoch precondition and the hold WITHOUT `§4.7.1`/`§15.4`. The page already violates this once at :393 (`[Spec §15.4 -- Translation Fidelity Matrix]`); do not take that as licence. EVIDENCE: docs/reference/adapter-contract.md:393

FACT: `tests/claim-map.json` is generator output. `scripts/seed-claim-register.py` emits it, rows are `sorted(claims, key=lambda c: c["claim"])` so no insertion position needs stating, and hand-authored rows go in the `EXPLICIT` list (each sibling carries a `note`). `TestClaimRegisterIsReproducibleFromItsGenerator` (tier 0) re-runs the generator and requires byte equality. `SURFACE_OVERRIDES` cannot mint a row: an override matching no table row raises `SystemExit`. Verified live at HEAD: the generator reproduces the committed register exactly (76 claims, empty diff). EVIDENCE: scripts/seed-claim-register.py:168,:364-368,:377,:398; tests/tier0_static/claim_register_generator_test.go:19-31,:44,:74-78

FACT: `tests/tier0_static/claim_register_proto_agreement_test.go` parses `Message.field` out of a row's `claim` text and requires the proto to declare it, so SCHEMA-1's first row ("ShutdownRequest.expected_bind_epoch …") binds the register row and the proto edit into ONE commit. S8 already lands both. EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:44-50

FACT: `Server` is declared at pkg/adapter/server.go:59 and `slotState` at pkg/adapter/slot.go:21. Neither `bindEpoch` nor `reclaiming` exists in `pkg/adapter` today. CODE-6 therefore opens `server.go`, which no edit list names — the same defect class as the earlier `slot.go` miss the applier caught. EVIDENCE: pkg/adapter/server.go:59; pkg/adapter/slot.go:21

WATCHOUT: `pkg/adapter/server.go` must be added to BOTH lists that enumerate CODE-6's files — non-spec-changes.md `## Files touched on application (non-spec)` (:1793-1860) AND summary.md:680's deliverable-index line. Fixing only the first leaves :680 as the surviving false exhaustive list.

WATCHOUT: DOCS-2's tier-11 work EXTENDS `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` in `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`; do not add a new tier-11 file. That test file must then join the `Tests:` entry of `## Files touched on application (non-spec)`, which today lists only `per_slot_substate_scope_doc_reconciliation_test.go` from tier 11. `tests/tier11_docs` is outside `componentAndAboveTierDirs()`, so neither choice owes a `tests/spec-map.json` entry.

USEFUL [standing context, DEFERRED docs/reference/adapter-contract.md:75 and :64]: the standing context had already scoped the edit (":75 is the site and :81 is not"; the four sibling "at each session release" sites are widened rather than falsified; `DemoteSDK` at :64 rides along). It saved re-deriving the site set from scratch. EVIDENCE: review-log.md:479,:732,:750,:751

OPEN (restated, not new): review-log.md:690 asks whether a `docs`-lane step's write lease permits writing `tests/tier11_docs/*.go`. DOCS-2 inherits DOCS-1's posture exactly, so it adds no new question, but if the answer is no, BOTH docs gates need rehoming in one edit.


### [non-spec.1.review-applicability.1]

FACT: `tests/claim-map.json` is a GENERATED artifact, not a hand-edited one. `tests/tier0_static/claim_register_generator_test.go:45` (`TestClaimRegisterIsReproducibleFromItsGenerator`) runs `scripts/seed-claim-register.py --out <tmp>` and requires the result byte-identical to the committed file; its own diagnostic string is "carries the row %q and the generator emits no such row, so the row has no row source behind it". The row sources are the §7.1 status table of the root `gateway-runtime-comms.md` (`scripts/seed-claim-register.py:38-39`) and the `EXPLICIT` list at `scripts/seed-claim-register.py:169-378`. SCHEMA-1 stages two rows into the JSON alone, so S8's own tier 0 fails. EVIDENCE: tests/tier0_static/claim_register_generator_test.go:45-82; scripts/seed-claim-register.py:169,378

FACT: `docs/reference/adapter-contract.md` is named NOWHERE in the proposal — not in an edit list, not in a "does not stage" row, not in a non-goal. `grep -n adapter-contract` over all four proposal files returns nothing. Its `Shutdown` row at :75 is the reader-facing mirror of the §4.7 row SPEC-1 rewrites, and the standing review log records eleven independent lens filings of it (review-log.md `### Deferred`, the `DEFERRED [docs/reference/adapter-contract.md]` entry). The shipped tier-11 gate `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only four substrings that all survive, so nothing catches it. EVIDENCE: docs/reference/adapter-contract.md:75

FACT: the proto field numbers in SCHEMA-1's table are all correct and all free. Checked message by message with `grep -n "message X" && sed -n`, never with `awk '/^message X/,/^}/'` (which runs on past a one-line message — the artifact that produced the retired `AssignCredentialsResponse` collision MISTAKE). ShutdownRequest holds 1,2,3,reserved 4,5,6 so 7 is free; `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` on one line and field 1 is free. EVIDENCE: schemas/lenny-adapter.proto ShutdownRequest / AssignCredentialsResponse

MISTAKE: standing Trap "CODE-5's `slotBinder`-interface sentence is wrong … filed twice, and it is still standing" is STILL standing at non-spec-changes.md:845. Verified against the tree: the interface declares the 3-parameter `ReleaseSlotReservation` at pkg/gateway/sessionserver/start.go:2727 and both fakes implement that form (slotretry_test.go:52, slotretry_load_test.go:33, whose `released` is `[][2]string` and cannot carry the `leaked` the tier-1 accounting cases assert). Filed this round. EVIDENCE: proposals/…non-spec-changes.md:845; pkg/gateway/sessionserver/start.go:2727

WATCHOUT: the checklist RENUMBERED this round (SPEC-5 moved to S1 and everything shifted), and two prose step references in non-spec-changes.md were not swept. `:1668` still says "Between S4 and S6 the unedited file still passes" where SPEC-4 is now S5. Substantively harmless, so not filed; a fixer touching that paragraph should correct it. EVIDENCE: proposals/…non-spec-changes.md:1668

UNVERIFIED: who owns the `claimSessionSlot`/`claimSessionSlotUnderLock` epoch-return signature change. The prose describing it sits in CODE-2 (S11, non-spec-changes.md:366) while `## Files touched` puts it under `pkg/adapter/slotsession.go`, which is CODE-6's file (S9). If an implementor lands it at S9 the tree stops compiling, because the three callers are at session.go:111 and resume.go:50 (files CODE-6 does not open) plus sdkwarm.go:217, and CODE-6 claims it "compiles alone". Not filed because the CODE-2 reading is available and correct. Somebody should pin the ownership in one sentence. EVIDENCE: pkg/adapter/slotsession.go:52; pkg/adapter/session.go:111; pkg/adapter/resume.go:50

FACT: `Binder.Resume` has to call `compensateFailedSlotBind(ctx, cl, req SlotBindRequest, …)` while holding a `podsession.ResumeRequest`. Both structs carry `SessionID`, `CleanupTimeoutSeconds` and `MaxConcurrentSessions`, so a synthetic three-field `SlotBindRequest` works and the gap is mechanical. Noted because the proposal narrowed `accountSlotFailure`'s parameters for exactly this reason and left `compensateFailedSlotBind`'s alone; not filed. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:602-676; slotbinder.go:26-108

DECISION: filed five findings, all sequencing/applicability — BECAUSE each is either a gate that hard-fails on the staged text (claim-map generator, tier lists) or a surface the applied edits falsify that no edit list carries (adapter-contract.md) — ALTERNATIVES: rejected the two bookkeeping candidates above (stale S4 reference, `StartSessionResponse.bind_epoch`'s absence from session.go's files-touched line) on the materiality bar this loop has already applied five times to that class.
### [non-spec.1.review-applicability.1]

FACT: `tests/claim-map.json` is a GENERATED artifact, not an authored one. `scripts/seed-claim-register.py` produces it from `gateway-runtime-comms.md` §7.1's status table plus an in-script `EXPLICIT = [...]` list for rows no table carries, and tier-0 `TestClaimRegisterIsReproducibleFromItsGenerator` reruns the generator and requires BYTE-IDENTICAL output. I ran `python3 scripts/seed-claim-register.py --out /tmp/cm.json` at HEAD: identical to the committed file. Any proposal that stages rows into `tests/claim-map.json` alone fails tier 0. — EVIDENCE: tests/tier0_static/claim_register_generator_test.go:19-31,:44,:76; scripts/seed-claim-register.py:169 (`EXPLICIT`)

FACT: `AssignCredentialsResponse` really IS an empty message (`message AssignCredentialsResponse {}` on one line, schemas/lenny-adapter.proto:1033), so SCHEMA-1's `bind_epoch = 1` is correct. A naive `awk '/^message X \{/,/^\}/'` extraction silently runs into the NEXT message for one-line message bodies and makes it look like field 1 is taken by `SessionId session_id = 1`. Do not re-file that. All nine staged field numbers were verified free with a brace-balanced parse: ShutdownRequest 7, ShutdownResponse 3, PrepareWorkspaceResponse 3, FinalizeWorkspaceResponse 2, ResumeResponse 4, ConfigureWorkspaceResponse 2, RunSetupResponse 2, AssignCredentialsResponse 1, StartSessionResponse 2. — EVIDENCE: schemas/lenny-adapter.proto:1033,:1609-1635,:1665-1667,:699-701,:761-767,:1433-1446,:1690-1691,:869-870,:958-961

FACT: every verbatim anchor and cross-file markdown anchor the amendment adds resolves at HEAD. §4.1's third sentence (spec/04_system-components.md:157), the §4.7 `Shutdown` row opening (:686), the §4.7.1/§4.7.2 insertion boundary (:659/:695), the §15.4 `**SDK-warm demotion contract:**`/§15.4.1 boundary (spec/15_external-api-surface.md:1469/:1471), and the four link targets (`#52-...` spec/05:365, `#101-...` spec/10:3, `#74-upload-safety` spec/07:438, `#1542-...` spec/15:1686). The checklist has 14 steps against 14 staged deliverables, one lane each, no duplicate, no forward Depends-on, no ticked box.

FACT: SPEC-4's edge lands in the "a pod of either concurrency" block, which is what the tier-11 gate needs. `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` slices the scoped block as `s62[scopedHeader:generalHeader]`, so the scoped block precedes the general one and the new edge cannot leak into the negative loop. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,:55,:70

WATCHOUT: the standing DEFERRED on `docs/reference/adapter-contract.md:75` is STILL unstaged. Eleven lenses filed it in the spec loop and each deferred it because "the remedy is a docs edit this loop may not land". This IS the loop that owns it (the non-spec staging is where a second DOCS deliverable goes) and the proposal still carries only DOCS-1 against `state-machines.md`. I filed it. If a later round sees it refuted, the refutation must engage the row's unconditional "flushes the session's final usage report, closes its runtime, ... and reports the per-slot cleanup outcome through `ReportSessionScrub`", not the "at each session release" wording that the five-site dead end covers. — EVIDENCE: docs/reference/adapter-contract.md:75; review-log.md:479,:732,:1558

WATCHOUT: the proto's `rpc Shutdown` doc comment (schemas/lenny-adapter.proto:203-205, "terminate the agent and release the pod ... Returns when the agent process has exited") is ALSO loosened further by the epoch fence, and the file is now opened by SCHEMA-1 so the S-2 excuse the review log records is retired. I did NOT file it, because the comment is already false today for a session the adapter holds no entry for, so it is pre-existing looseness rather than a site this proposal breaks. The `ReportSessionScrub` RPC comment at :308-318 and `SessionScrubOutcome` at :436-437 are the barred five-site "session release" dead end; do not file those.

MISTAKE (mine, avoided): I nearly filed S9's `Depends on: S1, S8` as a class-6 defect because CODE-6 implements SPEC-3's (S4) reclaim-hold paragraph and S4 is not in S9's dependency closure ({S1,S8} → S2 → S1 never reaches S4). Held back: S4 precedes S9 in the one stated execution sequence, so nothing breaks, and §15.4's own reclaim-hold block (SPEC-5, S1, which IS named) carries the wire-observable requirement CODE-6 implements. A later round that wants this must argue from a pipeline that dispatches by Depends-on rather than by listed order.

OPEN: is the tier-9 credential-fence suite (`tests/tier9_security/slot_credential_reclaim_fence_test.go`, attributed to "CODE-1 and CODE-6") correctly landed at S10 alone? S9/CODE-6 carries no tier 9. I judged it fine because CODE-1 is the later half and S10 follows S9, but the same reasoning does NOT rescue the tier-3 suite, which I filed.


### [non-spec.1.review-citations.1]

FACT: The citation quality in this proposal is unusually high. I mechanically re-verified ~90
file:line citations across the summary and the non-spec staging against the tree and every one
resolved (worst case a 2-4 line drift that changes no meaning). Verified batches, so a later
citation lens need not redo them: all nine SCHEMA-1 proto field numbers (ShutdownRequest 1,2,3,5,6
used + `reserved 4` so 7 is free at schemas/lenny-adapter.proto:1609-1636; ShutdownResponse 3;
PrepareWorkspaceResponse 3; FinalizeWorkspaceResponse 2; ResumeResponse 4; ConfigureWorkspaceResponse 2;
RunSetupResponse 2; AssignCredentialsResponse empty at :1033 so 1; StartSessionResponse 2); the
`SESSION_SCRUB_OUTCOME_*` sibling enum spelling (:438-448); the `#2851-gateway-to-pod` claim-map anchor
and the UNWIRED `AttachRequest.coordination_generation` precedent (tests/claim-map.json:48-54); all
five `InvalidArgument` resolve sites and their span-category column (staging.go:133/181/337,
slotsession.go:75-78, slotcreds.go:26-28, caller stamp at staging.go:80); every
`noteRuntimeStarted` production and test call site (session.go:163, resume.go:144, sdkwarm.go:261,
export_test.go:45, usage_test.go:233, adapterevents_test.go:95/:184,
podmcp_arming_internal_test.go:84/:185/:230); all six `ReleaseSlotReservation` sites; the tier-7a and
tier-11 fixture line refs; the S-2 second-window rule at gateway-runtime-comms-remediation.md:1883-1890;
and the two new "Defects in the shipped tree" entries (spec/10:30, spec/10:66-68,
pkg/adapter/coordination.go:120/:262 — and `grep GetCoordinationGeneration pkg/adapter/` really does
return exactly those two lines; spec/05:553/:555/:556, pkg/adapter/slot.go:105-124).

FACT: Only three files changed since the r9 snapshot — implementation-checklist.md, review-log.md and
summary.md. `spec-changes.md` and `non-spec-changes.md` are byte-identical to r9. The checklist change
is a pure renumbering (SPEC-5 moves from S5 to S1, everything shifts) plus three content catch-ups; I
re-derived every `Depends on:` edge after the renumber and they are all consistent.
EVIDENCE: diff -ru scratchpad/cp-snap/.../spec-r9 proposals/0081_...

WATCHOUT: `AssignCredentials` lives in `pkg/adapter/credentials.go:65` but its RESPONSE is constructed
in `pkg/adapter/slotcreds.go:52` (`assignCredentialsSlot`). The proposal's file manifest names
`slotcreds.go` and is right; do not "correct" it to credentials.go.
EVIDENCE: pkg/adapter/credentials.go:65-75; pkg/adapter/slotcreds.go:52

WATCHOUT: `PrepareWorkspace` is in `pkg/adapter/staging.go:31` and builds its response at
`staging.go:116` via `stream.SendAndClose`, and it reaches the entry only through
`resolvePrepareStagingDir` (`staging.go:133`), which returns `(string, error)` and drops everything
`ensureSlotPaths` gives it except `paths.Staging`. Any "epoch on the seven responses" work has to widen
that helper too, not just `ensureSlotPaths`. This is the substance of the one finding I filed.
EVIDENCE: pkg/adapter/staging.go:31,:116,:133-138

FACT: `claimSessionSlot` currently returns three values (`_, startMCP, err`) at four internal-test call
sites (podmcp_arming_internal_test.go:74,93,138,157,179,187,224). CODE-6 makes it report the epoch, so
every one of those call sites changes arity. The proposal covers this only obliquely ("Every test caller
becomes `_ = s.noteRuntimeStarted(sessionID, epoch)`, taking the epoch from the claim it already makes").
Not a finding, but it is mechanical work an implementor will hit.
EVIDENCE: pkg/adapter/slotsession.go:60-91; pkg/adapter/podmcp_arming_internal_test.go:74

UNVERIFIED: §4.7.1's caller rule says a caller "holds none once it has itself issued an RPC on that
connection that removes the entry the epoch names: `DemoteSDK` ... and a `Shutdown` answering
`reclaimed`". CODE-6 clears the latch only on `DemoteSDK` and on `ShutdownReclaim` answering
`RECLAIMED`; the plain `Client.Shutdown` and `ShutdownRecycle` keep their signatures, read no outcome,
and clear nothing. I convinced myself this is harmless in the shipped topology (every plain-`Shutdown`
caller either closes the connection right after — `defer result.Adapter.Close()` at slotbinder.go:544 —
or is the §11.4 revoke fan-out on a binding no compensation is ever sent from), so I did not file it.
A later reviewer who finds a path where a plain `Shutdown` is followed by a bind-sequence RPC or a
compensation on the same connection should re-open it.
EVIDENCE: spec-changes.md:682 (caller rule); non-spec-changes.md:~1000 (latch clear set);
pkg/gateway/podlifecycle/podsession/slotbinder.go:542-544; cmd/lenny-gateway/user_revocation.go:129
### [non-spec.1.review-citations.1]

DECISION: filed exactly two findings, both missing edit sites rather than false citations — BECAUSE the ~120 file:line citations in the non-spec staging verified clean (see FACT below) — ALTERNATIVES: filing the `deregisterSlotLocked` "read-only caller" phrase (rejected: after CODE-6 all three callers are destroy sites and the only caller is `reclaimSlotLocked`, so the phrase names a caller that does not exist, but the mechanism is unambiguous and it is prose precision below the bar); filing the `staging.go` files-touched bullet omitting the `PrepareWorkspaceResponse` epoch (rejected under the standing bookkeeping-materiality trap, log:491 — CODE-6, SCHEMA-1 and the tier-3 list all cover it).

FACT: the whole citation set in `0081_....non-spec-changes.md` was verified this round and came back clean. Verified by direct read: the nine SCHEMA-1 field numbers against every message (`ShutdownRequest` 1,2,3,5,6 used + 4 reserved → 7 free; `AssignCredentialsResponse` is `{}` → 1; every other next-free number correct); the five resolve sites and their span categories (staging.go:81 caller-stamped, :183 and :339 stamped beside the wrap, slotsession.go:77 and slotcreds.go:28 unstamped); the three deregister-then-destroy sites (session.go:238, slotsession.go:215 via `deregisterSlot`, slotsession.go:389 in `deregisterStartedSessions`); `drainReason` (session.go:309-321), `deadline_ms` passthrough (:260), spec/28:1082, runtime-ops schema :180-183; `userRevokeReason` (user_revocation.go:45) and the fan-out call (:129); `Reason()`'s FailedPrecondition arm (slotfailure.go:91-99), `NonRetryable` (:41-48), transient default (:100-101); `slotFailureSessionStart` (slotbinder.go:322-324, binder.go:293); start.go :2172/:2594-2605/:2606-2609/:2720/:2761/:2807-2809/:4041; queue.go :103-107/:143-146/:205; the three `podRegistry.Put` callers (start.go:2936, :4057, coordination_seams.go:249); `PodExecutor.Release` (executor/pod.go:312-321) and `handleUploadToSession`'s 502 (upload_to_session.go:128-137); `claimPodMCPStartLocked`'s `len(s.slots) != 1` (slotsession.go:110); socketruntime :156-161/:184/:220/:373-378/:398-417/:435-467; the tier-11 gate's `generalSlotEdges` (:32-37), positive loop (:55), negative loop (:70); `TestSlotBindErrorReason_spec_5_2` (:383) and `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` (:468); the `AttachRequest.coordination_generation` claim-map row's `#2851-gateway-to-pod` anchor and `R16` deferral. EVIDENCE: as listed.

FACT: all twelve verbatim "text to replace" anchor blocks in `0081_....spec-changes.md` still occur EXACTLY ONCE across `spec/` after the hand amendment. Re-derived mechanically this round (python exact-substring count over every `spec/*.md`): blocks at spec-changes.md:278, 314, 369, 412, 435, 447, 476, 495, 522, 551, 571, 592. Do not re-run unless `spec/` moves. USEFUL [review-log Settled #77]: that entry's anchor list is still accurate.

FACT: SPEC-5's two new insertion points resolve. spec/04:659 `#### 4.7.1 Role and Gateway RPC Contract`, :688 `*Adapter → Gateway RPCs:*`, :695 `#### 4.7.2 Checkpoint and Interrupt Mutual Exclusion` — the block lands between :688's table and :695. spec/15:1469 `**SDK-warm demotion contract:**`, :1471 `#### 15.4.1 …` — the two blocks land at :1470. Every minted anchor resolves: `#471-role-and-gateway-rpc-contract` (spec/04:659), `#101-horizontal-scaling` (spec/10:3), `#74-upload-safety` (spec/07:438), `#52-pool-configuration-and-execution-modes` (spec/05:365), `#47-runtime-adapter` (spec/04:657).

FACT: rule S-2's covered-file list is `session.go, lifecycle.go, checkpoint.go, coordination.go, credentials.go, slotcreds.go, attach.go, sdkwarm.go` plus two renamed channel files (`gateway-runtime-comms-remediation.md:1883-1890`). The proposal's "three of S-2's covered handler files (`session.go`, `slotcreds.go`, `sdkwarm.go`)" is exact: `slotsession.go`, `slot.go`, `staging.go`, `resume.go`, `holdstate.go`, `runtimegeneration.go` and the new `bindepoch.go` are all OUTSIDE the covered list. Do not re-derive; a lens counting adapter files touched will think the count is wrong.

WATCHOUT: `tests/spec-map.json` is a TIER-0 gate and it is STILL missing from every edit list — this is review-log Open :669 and :674 unclosed, now filed as a finding. `componentAndAboveTierDirs()` (cmd_validate.go:125-138) covers tier3/7a/9/10 and NONE of those four tiers has a directory-level catch-all entry in `tests/spec-map.json` (checked by script: `tests/tier3_contract`, `tests/tier7a_load_local`, `tests/tier9_security`, `tests/tier10_conformance` are each absent as a bare mapped path). Four new files/dirs the staging adds therefore turn tier 0 red. EVIDENCE: cmd/lenny-test/cmd_validate.go:716-790; cmd/lenny-test/cmd_run.go:761-770.

WATCHOUT: `docs/reference/adapter-contract.md` is named NOWHERE in the proposal outside the review log (`grep -rn adapter-contract` over the proposal directory, excluding the logs, returns nothing), and its `Shutdown` row at :75 is falsified in four ways by SPEC-1/SPEC-3/CODE-1. This is standing DEFERRED :732, which records eleven independent filings and says the non-spec loop owns the fix. Filed. EVIDENCE: docs/reference/adapter-contract.md:75.

MISTAKE (earlier round, still uncorrected): review-log Deferred :732 concluded "it wants a second DOCS deliverable against `adapter-contract.md`. Scope it to :75", and Deferred :750 later widened it to include the `DemoteSDK` row at :64. Neither landed in the staging. The cost is that a converged proposal ships a false reader-facing contract row that no tier-11 gate catches (`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only four substrings, all of which survive).


### [non-spec.1.review-client-surface.1]

DECISION: filed three findings, all client-surface mirror gaps in the NON-SPEC lane — BECAUSE
this loop reads spec+non-spec as one document and all three fixes land in
`0081_....non-spec-changes.md` (a new DOCS-2, a SCHEMA-1 comment clause, one path correction) —
ALTERNATIVES: rejected the incomplete per-file sub-enumeration in `## Files touched on
application (non-spec)` (staging.go's entry names the epoch on `FinalizeWorkspace` and
`RunSetup` but not on `PrepareWorkspace`; session.go's entry omits `StartSessionResponse`), because
CODE-6's "Seven handlers report the epoch on their responses, per the SCHEMA-1 table"
(non-spec-changes.md:988) already binds the applier and nothing lands wrong. Also rejected the
`docs/reference/state-machines.md:251` OPEN at review-log.md:3952: :251 scopes
`slot_cleanup -> leaked` to concurrent pods, which mirrors §6.2's split exactly and which SPEC-4
no longer contradicts (the terminal claim was removed from SPEC-4's prose in an earlier round).
That OPEN is stale and should be retired.

FACT: the generated adapter bindings are at `pkg/proto/adapter/v1`, NOT `pkg/gen/adapter/v1`.
`schemas/buf.gen.yaml` pins `out: ../pkg/proto` and every importer spells
`adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"`; `pkg/gen` does not exist.
The wrong path is in the proposal at non-spec-changes.md:1060, :1797 and
implementation-checklist.md:20, AND in the standing context at review-log.md:363 — so the
standing entry is itself the likely source and should be corrected in the same pass.
EVIDENCE: schemas/buf.gen.yaml:16-24; pkg/proto/adapter/v1/lenny-adapter.pb.go;
pkg/checkpoint/checkpoint.go:21

MISTAKE: review-log.md:1510 records a client-surface absence sweep whose grep set included
`pkg/gen` — a directory that does not exist, so that arm of the sweep was vacuous. The
conclusion (no SDK/OpenAPI/CRD/JSONL mirror) is still right for the other paths, which I
re-derived, but do not cite that entry as evidence about generated code.

FACT: every SCHEMA-1 field number is free, re-verified against the tree. `ShutdownRequest`
holds 1,2,3,(reserved 4),5,6 so 7 is next; `ShutdownResponse` 1,2 → 3; `PrepareWorkspaceResponse`
1,2 → 3; `FinalizeWorkspaceResponse` 1 → 2; `ResumeResponse` 1,2,3 → 4;
`ConfigureWorkspaceResponse` 1 → 2; `RunSetupResponse` 1 → 2; `StartSessionResponse` 1 → 2;
`AssignCredentialsResponse` is `message AssignCredentialsResponse {}` → 1. Nine for nine.
EVIDENCE: schemas/lenny-adapter.proto:1609-1636,:1665-1668,:699-702,:761-767,:1433-1447,
:1690-1691,:869-870,:958-962,:1033

FACT: no tier-0 proto gate fires on SCHEMA-1. `claim_register_proto_agreement_test.go` only
inspects rows whose `surface` CONTAINS `schemas/lenny-adapter.proto`, and both staged rows name
Go files instead, so the `Message.field` arm never runs on them (and the fields are declared
anyway). `adapter_proto_message_scope_test.go` keys on the `SessionId`/`session_id` pairing only.
`adapter_proto_generation_scope_test.go` counts `operationalFenceSentence` occurrences == 12, and
no staged comment carries that sentence. `adapter_session_address/session_address_wire_test.go`
checks `session_id` presence and `reserved 4`, both untouched.
EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:68-76;
tests/tier0_static/adapter_proto_generation_scope_test.go:129-132;
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:94,:121,:141

WATCHOUT: the standing DEFERRED at review-log.md:677 and the OPEN at :3958 both hand the
`schemas/lenny-adapter.proto` doc-comment correction to remediation step R1b, on the ground that
"programme rule S-2 bars this proposal from opening that file". THE AMENDMENT RETRACTED THAT
CONSTRAINT — SCHEMA-1 opens the file under S-2's second window (summary.md:94-95). Both entries
are now reasoning from a dead premise and the correction is owed HERE. Do not re-defer it.
EVIDENCE: proposals/0081_.../0081_....summary.md:94; non-spec-changes.md:1056-1062

USEFUL [standing context, the `docs/reference/adapter-contract.md:75` DEFERRED at
review-log.md:676 and :2331]: it named the exact row, the exact four ways it goes false, the four
sibling sites that stay TRUE (`adapter-contract.md:81`, `security-principles.md:33`,
`execution-modes.md:68`, `multi-tenancy.md:72`), and the fact that
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` passes silently over the drift. It saved
me the whole sweep and kept me from widening the edit list past the one row. Eleven lenses have
now filed it and the non-spec staging still carries no DOCS-2; this is the single most-filed,
least-acted item on the proposal.
### [non-spec.1.review-client-surface.1]

DECISION: filed three findings, all (d) missing-edit-site, all in the non-spec staging — BECAUSE
this is the loop the standing DEFERREDs name as the owner of the docs edit, and two further
generated/gated artifacts turn out to break under SCHEMA-1 that no round has named — ALTERNATIVES:
rejected filing the `default:` arm of `compensateFailedSlotBind` (UNSPECIFIED + `!cleanly` records
not-leaked) because it only bites a non-conforming third-party adapter, the same class the loop has
refuted twice; rejected `docs/api/internal.md:95-160` (pre-existing retired RPC names, standing
Settled entry).

FACT: `tests/claim-map.json` IS A GENERATED FILE and a tier-0 gate enforces it.
`TestClaimRegisterIsReproducibleFromItsGenerator` runs `scripts/seed-claim-register.py --out` and
requires byte-identity with the committed register, naming both directions of divergence ("a row
the committed register carries and the generator omits was added to the file with no row source
behind it"). SCHEMA-1 stages two rows into `tests/claim-map.json` and names the generator nowhere
in the proposal. The row source belongs in the script's `EXPLICIT` list. — EVIDENCE:
tests/tier0_static/claim_register_generator_test.go:20-31,:75-80; scripts/seed-claim-register.py:169,:378
FACT: rows are emitted `sorted(claims, key=lambda c: c["claim"])`, so the two new rows are not
adjacent in the file however the snippet shows them. — EVIDENCE: scripts/seed-claim-register.py:398

FACT: `ShutdownRequest` and `ShutdownResponse` carry an EXACT field-set gate at tier 3.
`assertFieldSet` errors on any field number not in its `want` map, and
`TestShutdownMessagePostRemovalDescriptor_spec_4_1` pins ShutdownRequest to {1,2,3,5,6} and
ShutdownResponse to {1,2}. SCHEMA-1's `expected_bind_epoch = 7` and `slot_reclaim = 3` both trip it.
The proposal stages a NEW tier-3 directory (`adapter_bind_epoch/`) and never mentions this shipped
one. Nine lenses over seven sweeps missed it because they checked field-number FREENESS in the
proto and stopped there. — EVIDENCE:
tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-238,:259-278
FACT: the other seven response messages have NO exact-field-set gate; a grep for
`Descriptor()`/`assertField` over `tests/tier3_contract/` returns only the RecycleScrub, Shutdown,
CheckpointStart/ChunkReady/CheckpointGrant and interceptor sites. `adapter_session_address`'s
`retiredDuplicateNumbers` map is a reserved-number map, not a field count.

FACT: every proto field number SCHEMA-1 claims is genuinely free, re-verified message by message:
ShutdownRequest 1,2,3,5,6 with 4 reserved; ShutdownResponse 1,2; PrepareWorkspaceResponse 1,2;
FinalizeWorkspaceResponse 1; ResumeResponse 1,2,3; ConfigureWorkspaceResponse 1; RunSetupResponse 1;
StartSessionResponse 1; `message AssignCredentialsResponse {}` genuinely empty at :1033. The enum
value spelling matches its sibling `SessionScrubOutcome` (:438-448). — EVIDENCE:
schemas/lenny-adapter.proto:1033,:438-448,:1610-1635,:1665-1668
WATCHOUT: `awk '/^message X \{/{f=1}f{print}f&&/^}/{exit}'` silently runs past an empty
one-line message such as `AssignCredentialsResponse` and prints the NEXT message's fields. That is
how an earlier round attributed `RotateCredentialsRequest`'s field set to it. Use
`if($0=="}") exit` instead.

FACT: rule S-2's second window reads exactly as the proposal quotes it, and the checklist ordering
honours it: S8 (proto) precedes S9-S13, which open `session.go`, `slotcreds.go` and `sdkwarm.go`,
three of S-2's nine covered handler files. — EVIDENCE: gateway-runtime-comms-remediation.md:1883-1890
FACT: the generated Go package is `pkg/proto/adapter/v1`; `pkg/gen/` does not exist. The last fix
round corrected this at three sites. — EVIDENCE: pkg/proto/adapter/v1/lenny-adapter.pb.go
FACT: `#2851-gateway-to-pod` resolves — spec/28_communication-channels.md:205 `#### 28.5.1 Gateway-to-pod`.
The claim-register validator resolves `spec_anchor` against §28 headings and requires a WIRED row's
surface to carry a file path or symbol rather than a bare line number; both staged rows comply.
— EVIDENCE: tests/tier0_static/claim_register_test.go:55-58

USEFUL [standing context, DEFERRED docs/reference/adapter-contract.md:75 and :64]: saved a full
docs sweep. I confirmed :75 is still unstaged (a grep of the proposal directory for
"adapter-contract" hits only the review logs) and filed it here, since this is the loop the
DEFERRED names as its owner. I did NOT fold :64 (`DemoteSDK`) into the finding: §4.7.1 makes the
demotion the act that drops the entry, but the shipped row is incomplete rather than falsified,
which is below the bar. It stays a DEFERRED.

FACT: `schemas/lenny-adapter.proto` has no SDK, `docs/`, `spec/` or second-copy mirror — re-verified
by grepping `sdks/` for `ShutdownRequest`/`AssignCredentialsResponse`/`lenny-adapter.proto` (zero
hits) and by reading §28.7, whose artifact register is per-FILE, so an added message field and an
added enum need no §28.7 row. The JSONL `shutdown` frame
(schemas/lenny-adapter-jsonl.schema.json:109-115) is the adapter↔runtime surface and is untouched.


### [non-spec.1.review-docs-alignment.1]

DECISION: filed exactly one finding, the missing DOCS-2 against `docs/reference/adapter-contract.md:75` — BECAUSE this is the non-spec loop, which is the lane that owns the docs remedy, and the standing context has carried this as a DEFERRED across at least eleven lenses without it ever being staged (review-log.md:676, :2331, :2683). It is still absent: `grep -n "adapter-contract" proposals/0081_*/*.md | grep -v review-log` returns nothing. — ALTERNATIVES: I built and dropped three. (1) A separate finding that §15.4's newly published bind-epoch and reclaim-hold obligations have no mirror on `adapter-contract.md`: dropped as a standalone because the page's stated audience is the runtime author and the adapter container is Lenny-managed (`docs/reference/adapter-contract.md:17`), so a verifier can read the omission as completeness rather than falsity; folded the outcome triple and the epoch precondition into the :75 fix instead. (2) `docs/api/internal.md:490-500`, the adapter gRPC status-code table, which gains no `ABORTED` row for the hold's transient refusal: the standing context explicitly says DO NOT FILE that table (review-log.md:533) because it already omits `ABORTED` and `INVALID_ARGUMENT` that the shipped adapter returns. (3) `docs/reference/adapter-contract.md:64` `DemoteSDK`: SPEC-5's §4.7.1 caller rule says the demotion removes the entry, but spec/04:674's own `DemoteSDK` row is untouched, so the docs row stays a faithful mirror of the spec row and the incompleteness is spec-side, not docs-side.

FACT: the docs surfaces that stay TRUE after SPEC-1/SPEC-3 are `docs/reference/adapter-contract.md:81` and `:84`, `docs/operator-guide/security-principles.md:33`, `docs/reference/execution-modes.md:68`, `docs/operator-guide/multi-tenancy.md:72` (all "at each session release", which SPEC-3 widens rather than falsifies), `docs/client-guide/session-lifecycle.md:416`, `docs/runtime-author-guide/index.md:186`, `docs/runtime-author-guide/lifecycle.md:69`. I re-verified each against the staged §5.2 append. Do not re-grep them. EVIDENCE: proposals/0081_.../spec-changes.md:600

FACT: SPEC-3's added slot-cleanup actions (the credential directory removal and the §4.9 timer cancellation) have NO docs mirror to update. `grep -rn "process group\|workspace directory" docs/ --include=*.md` returns two unrelated hits (`docs/getting-started/concepts.md:172`, `docs/runtime-author-guide/lifecycle.md:226`), neither a per-slot action list. EVIDENCE: proposals/0081_.../spec-changes.md:578

FACT: the new §6.2 `receiving_uploads → slot_cleanup` edge reaches no SVG. `docs/assets/diagrams/pod-warm-path.svg` and `sdk-warm-path.svg` carry `receiving_uploads` as a POD-level phase and carry no `slot_cleanup` node at all, so DOCS-1's table row is the whole docs surface for SPEC-4. EVIDENCE: docs/assets/diagrams/pod-warm-path.svg (one `receiving_uploads` occurrence, zero `slot_cleanup`)

FACT: no metric or alert is added, so no `docs/reference/metrics.md` or `docs/runbooks/` companion is owed. Separately, `lenny_adapter_leaked_slots` — which SPEC-3's append cites — appears NOWHERE under `docs/` (`grep -rn "lenny_adapter_leaked_slots" docs/` is empty). That is a pre-existing gap this proposal neither creates nor widens; I did not file it and neither should the next round.

WATCHOUT: the shipped tier-11 gate on the `Shutdown` docs row is a substring-presence check that survives SPEC-1 intact. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub". Every one of those still appears in the false row, so the drift ships green and only a reviewer catches it. A DOCS-2 needs its own tier-11 clause; the worked pattern for a §4.7-row-to-adapter-contract-row reconciliation already exists. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:48-62

USEFUL [standing context, review-log.md:450 the ":75 is the site and :81 is not" dead-end resolution, and review-log.md:695 the "four sites that stay TRUE" DEFERRED]: together these fenced the whole `docs/` sweep in one read. I verified both against the tree rather than trusting them and both held.
### [non-spec.1.review-docs-alignment.1]

DECISION: filed exactly ONE finding — the missing DOCS deliverable against
`docs/reference/adapter-contract.md:75` — BECAUSE this is the non-spec loop, which is the loop
whose staging owns a docs edit, and the standing context records that eleven earlier lenses
filed the same site as DEFERRED precisely because their loop could not land the fix
(review-log.md:732, :750, :969, :1307, :1558). ALTERNATIVES rejected, each after verifying it
in the tree: (a) `adapter-contract.md:64`'s `DemoteSDK` row against §4.7.1's caller rule — the
row is a verbatim mirror of spec/04:674, which this proposal does NOT edit, so the docs row is
incomplete in exactly the way the spec row is and guardrail (1) bars asking spec to change;
(b) `docs/reference/state-machines.md:251`'s `slot_cleanup -> leaked` gloss ("cleanup timeout
is exceeded") against SPEC-2/SPEC-3's new leak cause (a `Shutdown` answered without a clean
exit) — barred by the standing "do not file either `leaked` gloss" trap (review-log.md:445),
and the docs line mirrors spec/06:148, which is pre-existing looseness; (c) the four
"at each session release" siblings (`adapter-contract.md:81`, `execution-modes.md:68`,
`operator-guide/security-principles.md:33`, `operator-guide/multi-tenancy.md:72`) — SPEC-3
widens rather than falsifies them, per the standing resolution at review-log.md:479; (d) the
shared-entry residue (a serving session torn down by a matching-epoch reclaim) as a
category-1 accepted-failure-mode-with-no-landing-text finding — it is bounded by the §5.2
whole-pod scrub and the exclusive-pod retirement, and no docs page owns that narrative, so it
reads as reasoning-only by design rather than as a missing mirror.

FACT: the docs surface this proposal can falsify is SMALL and now fully enumerated.
`grep -rln "Shutdown" docs/` returns exactly three files, and only
`docs/reference/adapter-contract.md` describes the gateway→adapter `Shutdown` RPC; the other
two (`docs/getting-started/concepts.md:134`, `docs/runtime-author-guide/testing.md:28,:220`)
are the runtime-facing JSONL `shutdown` frame and are untouched. — EVIDENCE:
docs/reference/adapter-contract.md:75

FACT: `docs/reference/adapter-contract.md` is written for RUNTIME authors, not third-party
ADAPTER authors: its own lead says "the protocol between the Lenny adapter sidecar and your
runtime binary" and the gRPC section opens "Your runtime binary never sees them directly".
So a finding demanding the full §15.4 bind-epoch/reclaim-hold contract be republished there is
over-reach; the defensible finding is confined to the `Shutdown` row's now-false statements.
— EVIDENCE: docs/reference/adapter-contract.md:10, :53

FACT: the operator-narrative half of this lens is structurally inert on this proposal.
`grep -rn "leaked\|pod replacement\|drain-request\|whole-pod replacement" docs/operator-guide/
docs/runbooks/` returns three unrelated hits (cert-expiry pod replacement, an upgrade
checklist item, a leaked GitHub PAT). No operator page enumerates leaked-slot or
pod-replacement causes, so the new leak cause and the new reclaim residues have no narrative
page to gain a row in. This confirms the earlier Settled entry at review-log.md:231 from the
docs side rather than the alerting side. — EVIDENCE: docs/runbooks/credential-revocation.md:34

WATCHOUT: the `Shutdown` row at :75 is falsified in FOUR independent ways after the amendment,
and three of them are new since the pre-amendment loops read it. Pre-amendment: the usage
flush and runtime close move under `started` and the `ReportSessionScrub` moves under `live`.
New with the amendment: the CH-RUNTIMEOPS drain sentence, because CODE-1 moves
`drainViaLifecycle` INSIDE the `if started {` block (non-spec-changes.md:201-204), so the
signal now needs `started && !boundRemains` where the docs row states only "leaves the pod
holding no other bound session"; and the whole row, because a mismatched bind epoch makes the
adapter perform NEITHER teardown (spec-changes.md:322). A fixer staging DOCS-2 must cover all
four, not the two the older DEFERRED entries describe. — EVIDENCE:
proposals/0081_.../0081_....non-spec-changes.md:201-213

FACT: no tier-11 gate will catch the fix if it is written wrong, and none will catch its
absence. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts four substrings
("end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub")
that all survive every staged edit. A DOCS-2 deliverable therefore has to stage its own
tier-11 assertion the way DOCS-1 does, or ship ungated. — EVIDENCE:
proposals/0081_.../0081_....review-log.md:168

USEFUL [standing context, Deferred block at review-log.md:732 and :750]: it had already done
the sibling-site triage (:75 is the site, :81/:64 and the four "session release" sites are
not), which saved a full sweep of `docs/` for cleanup-report mirrors. The one thing it is now
stale on is the drain-gate half, which the amendment changed; see the WATCHOUT above.


### [non-spec.1.review-edit-sites.1]

FACT: `tests/claim-map.json` is GENERATOR-PRODUCED and a tier-0 gate enforces byte-identity with the generator's output. Rows a proposal writes by hand go in the `EXPLICIT` list of `scripts/seed-claim-register.py` (:169), and the emitter sorts by `claim` (:399). Editing the JSON alone turns tier 0 red and the next seeding run drops the rows. — EVIDENCE: tests/tier0_static/claim_register_generator_test.go:20-27,:45,:77-80; scripts/seed-claim-register.py:169,:399
WATCHOUT: SCHEMA-1 stages the two `WIRED` rows into `tests/claim-map.json` only (non-spec-changes.md:1114,:1799; checklist S8 :20). `scripts/seed-claim-register.py` is named nowhere in the proposal. Filed as a finding. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:1799

FACT: `make generate-proto` writes `pkg/proto/adapter/v1`, not `pkg/gen/adapter/v1`. `pkg/gen` does not exist in the tree. The output root is pinned in `schemas/buf.gen.yaml` (`out: ../pkg/proto`). — EVIDENCE: Makefile:91-100; schemas/buf.gen.yaml; pkg/proto/adapter/v1/lenny-adapter.pb.go
WATCHOUT: the proposal writes `pkg/gen/adapter/v1` at three sites: non-spec-changes.md:1060, :1797, and implementation-checklist.md:20. Filed.

FACT: `lenny-test validate-maps` is a tier-0 gate that fails for any `_test.go` under tests/tier{2,3,4,5,6,7a,7b,8,9,10,12}_* that no `tests/spec-map.json` `tests` entry names, matching the file path or any ANCESTOR directory. No directory-level entry exists for tier3_contract, tier7a_load_local, tier9_security or tier10_conformance — every one is mapped file by file. — EVIDENCE: cmd/lenny-test/cmd_validate.go:119-139,:716-790
WATCHOUT: 0081 stages four NEW test files at those tiers (`tests/tier3_contract/adapter_bind_epoch/`, `tests/tier7a_load_local/slot_reclaim_hold_race_test.go`, `tests/tier9_security/slot_credential_reclaim_fence_test.go`, `tests/tier10_conformance/bind_epoch_conformance_test.go`) and names `tests/spec-map.json` nowhere. This closes the standing Open at review-log.md:621 and :626. Filed.

USEFUL [Traps, "DELETED AND REPLACED ... AssignCredentialsResponse"]: the awk-runs-past-a-one-line-message artifact is real and I reproduced it. `message AssignCredentialsResponse {}` is one line at schemas/lenny-adapter.proto:1033, and a `sed -n "N,/^}/p"` range from it prints `AssignCredentialsRequest`-shaped fields from the NEXT message. All nine SCHEMA-1 field numbers re-verified free by reading each message body explicitly: ShutdownRequest 7 (1,2,3,5,6 used, 4 reserved, :1609), ShutdownResponse 3 (:1665), PrepareWorkspaceResponse 3 (:699), FinalizeWorkspaceResponse 2 (:761), RunSetupResponse 2 (:869), StartSessionResponse 2 (:958), ResumeResponse 4 (:1433), ConfigureWorkspaceResponse 2 (:1690), AssignCredentialsResponse 1 (:1033). Do not re-run this.

USEFUL [Deferred, docs/reference/adapter-contract.md]: still live and still unlanded. The `Shutdown` row is at :75 (line number unchanged), DemoteSDK at :64. `adapter-contract.md` appears in NO file of the proposal outside the review logs. DOCS-1 (`docs/reference/state-machines.md`) is the only docs deliverable (non-spec-changes.md:1152,:1841). Filed against :75 only; :64 is incomplete-not-false and I did not file it.

FACT: things I checked and found CLEAN, so nobody re-derives them. §28 carries no `Shutdown` register row (`grep -n Shutdown spec/28_communication-channels.md` returns nothing), so "§28's registers untouched" (spec-changes.md:738) is true. `docs/api/internal.md` names no `Shutdown`. `ShutdownRequest`/`exited_cleanly`/`SessionScrubOutcome` appear in spec/ + docs/ + schemas/ + charts/ + sdks/ at exactly two files: schemas/lenny-adapter.proto and spec/04:157, both of which the proposal edits. No chart value and no migration is correct. §15.4.6 is runtime-binary-scoped and §15.4.5's reading order (spec/15:2010-2036) is for binary authors, item 7 explicitly excluding the §4.7 gRPC table, so SPEC-5's §15.4 insertion adds no reading-order entry.

OPEN: `schemas/lenny-adapter.proto`'s `Shutdown` RPC comment (:203-206, "terminate the agent and release the pod ... Returns when the agent process has exited") is further falsified by SPEC-1's two-teardown split and by the `superseded` outcome. The standing DEFERRED hands it to remediation step R1b on the ground that S-2 barred this proposal from opening the file — but the amendment RETRACTED that reading and SCHEMA-1 now opens the file. I did not file it because the comment is already stale today for an `absent` reclaim and for a per-slot `Shutdown` on a co-tenanted pod, which is the pre-existing-looseness class prior rounds killed. A later round should decide whether the reopened window changes the disposition.
### [non-spec.1.review-edit-sites.1]

DECISION: filed three findings, all missing-edit-site under the non-spec staging: (1) `tests/claim-map.json` is generated and its two new rows have no row source; (2) `docs/reference/adapter-contract.md:75` (the standing eleven-lens DEFERRED, still unowned); (3) `tests/spec-map.json` orphans the four new tier-3/7a/9/10 test files. BECAUSE all three land in the non-spec staging, which is this loop's lane, and each turns a tier-0 gate red or falsifies a published reader-facing sentence. ALTERNATIVES rejected: the `docs/api/internal.md` `StartSessionResponse` block (already wrong about the message before this proposal, so it does not *become* wrong); `tests/change-graph.json` (new tier-3 contract dir is not required there — the existing `adapter_generation_fence` and `adapter_reportusage` dirs are not listed either, so it is pre-existing looseness).

FACT: `tests/claim-map.json` is a GENERATED artifact and the hand-added-row escape is `EXPLICIT` in the generator, not the JSON. `scripts/seed-claim-register.py:169` declares `EXPLICIT = [...]` ("Rows the proposal writes explicitly, because no status table carries them"), appended at `:378` (`claims.extend(EXPLICIT)`). `SURFACE_OVERRIDES` (`:145`) cannot mint a row: `:364-368` raises `SystemExit` for an override that matched no table row. Tier 0's `TestClaimRegisterIsReproducibleFromItsGenerator` (`tests/tier0_static/claim_register_generator_test.go:44`) runs the generator and byte-diffs, reporting exactly "the row %q ... has no row source behind it" (`:120`). EVIDENCE: scripts/seed-claim-register.py:169,:364-368,:378; tests/tier0_static/claim_register_generator_test.go:19-27,:44,:114-122

FACT: `validateTestFilesMapped` (cmd/lenny-test/cmd_validate.go:716-790) walks `componentAndAboveTierDirs()` (`:125-139`), which DOES include `tests/tier7a_load_local`. An earlier standing note on proposal 0076 said tier7a is outside the walk; that is WRONG against the current tree. It resolves a file by exact rel path then each parent dir, and `tests/tier3_contract`, `tests/tier7a_load_local`, `tests/tier9_security` and `tests/tier10_conformance` are mapped in `tests/spec-map.json` at FILE level only (only `tests/tier4_integration` and `tests/tier9_security/pentest` have bare-directory entries). So any NEW file under those four orphans unless `tests/spec-map.json` or `tests/spec-map-pending.txt` gains it. EVIDENCE: cmd/lenny-test/cmd_validate.go:125-139,:716-790; tests/spec-map.json

FACT: every proto field number SCHEMA-1 claims is genuinely free, including the two the amendment note flagged. `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` (schemas/lenny-adapter.proto:1033), so field 1 is free — beware, a naive `awk '/^message X \{/,/^\}/'` range swallows the NEXT message for a `{}` one-liner and makes it look like it holds fields 1,2,3,5. Verified free: ShutdownRequest 7 (:1610-1635, 4 reserved), ShutdownResponse 3 (:1666-1667), PrepareWorkspaceResponse 3 (:700-701), FinalizeWorkspaceResponse 2 (:767), ResumeResponse 4 (:1434-1446), ConfigureWorkspaceResponse 2 (:1691), RunSetupResponse 2 (:870), StartSessionResponse 2 (:961). Enum spelling matches the `SessionScrubOutcome` sibling at :436-449. EVIDENCE: schemas/lenny-adapter.proto:1033,:1610-1635,:436-449

FACT: `pkg/proto/adapter/v1` (not `pkg/gen/...`) is the generated package; the round-4 hand fix is correct. EVIDENCE: schemas/lenny-adapter.proto:20 `option go_package = ".../pkg/proto/adapter/v1;adapterv1"`

FACT: none of `bind_epoch`, `expected_bind_epoch`, `slot_reclaim`, `SlotReclaimOutcome`, `SLOT_RECLAIM_*`, `ExcludePods`, `reclaimSlotLocked`, `runtimeHoldsLocked`, `accountSlotFailure` occurs anywhere in spec/, docs/, schemas/, charts/, pkg/, cmd/ or tests/ today, so the minted-identifier sweep finds no collision and no stale sibling. The proposal adds no metric and no alert, so spec/16, docs/reference/metrics.md, pkg/alerting/rules and docs/runbooks/ are untouched surfaces (`grep -rn leaked pkg/alerting/ docs/reference/metrics.md spec/16_observability.md` returns nothing).

FACT: no `sdks/` tree mirrors `schemas/lenny-adapter.proto`, and spec/28.7's artifact-register row (spec/28_communication-channels.md:1782) is per-channel rather than per-field, so a new field on an existing message adds no §28 row. The proposal's "deliberately untouched" §28 bullet is correct.

WATCHOUT: `docs/reference/adapter-contract.md` line numbers are :64 (`DemoteSDK` row) and :75 (`Shutdown` row) at HEAD. The tier-11 gate `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only four substrings that all survive SPEC-1, so this drift will NOT turn red on its own — it has to be staged by hand.

USEFUL [review-log.md:732, :1558, :2530]: the standing DEFERRED on `docs/reference/adapter-contract.md:75`/`:64` is precise about which sibling sites are NOT edit sites (`:81`, `:84`, `security-principles.md:33`, `execution-modes.md:68`, `multi-tenancy.md:72`, `client-guide/session-lifecycle.md:416`, `runtime-author-guide/index.md:186`, `lifecycle.md:69`). It saved a whole sweep and kept the finding scoped to the two rows that actually break.

OPEN: whether the two `WIRED` claim-register rows are owed at all. §28.4 obliges a row only for a normative §28 statement, and the proposal itself argues `Shutdown` carries no §28 row. Dropping the rows is a legal alternative to editing the generator, and it is cheaper. A later round or the human should pick.


### [non-spec.1.review-feasibility.1]

DECISION: filed two findings, both in the non-spec lane, both actor-can't-do-that under this lens — BECAUSE (1) `compensateFailedSlotBind` is specified with a `req SlotBindRequest` parameter while CODE-4 names `Binder.Resume` (which holds a `podsession.ResumeRequest`) as one of its callers, and (2) `tests/claim-map.json` is a generated artifact — ALTERNATIVES: I built and dropped four. (a) The adapterclient latch is cleared only by `DemoteSDK` and `ShutdownReclaim`-RECLAIMED, while §4.7.1's caller rule says any `Shutdown` answering `reclaimed` drops it; harmless because the next bind-sequence response on that connection re-latches and no compensation runs in the gap. (b) §15.4's hold contract says "a request that would create or resolve a registry entry" is refused `ABORTED`, but the code refuses only in `ensureSlotStateLocked`, so `Attach`/`CoordinatorFence`/`ReportUsage` answer `FailedPrecondition` during a hold — this is a close variant of the already-refuted "§15.4 exempts Shutdown; §5.2 does not" finding, whose refutation fixed "create or resolve" as the bind-sequence vocabulary. (c) CODE-1's epoch early-return skips clause three's `startPodScrub`; unreachable, because only the compensation carries a non-zero epoch and `ShutdownReclaim` carries no `RecycleScrub`. (d) The tier-4 late-reclaim arm needs the abandoned attempt's entry to be gone before the retry binds, or the retry adopts it and the reclaim answers `RECLAIMED` rather than `SUPERSEDED`; the fixture can arrange that (the shipped `StartSession` pre-start branches call `releaseSessionSlot`), so it is constructible.

FACT: `tests/claim-map.json` IS GENERATED, and a tier-0 gate enforces byte-identity with the generator's output. `scripts/seed-claim-register.py` parses `gateway-runtime-comms.md` §7.1 ("Status of every mechanism named in this document", gateway-runtime-comms.md:2659) and emits the whole file; `TestClaimRegisterIsReproducibleFromItsGenerator` runs the generator and requires byte-identical output. Hand-added rows are dropped on the next seeding run and turn tier 0 red immediately. Any proposal that stages a claim-register row must also stage its row source. EVIDENCE: tests/tier0_static/claim_register_generator_test.go:20-31,:44-70; scripts/seed-claim-register.py:1-40.

FACT: `podsession.ResumeRequest` and `podsession.SlotBindRequest` are two distinct structs that happen to carry the same four fields the compensation reads (`SessionID`, `Pool`, `MaxConcurrentSessions`, `CleanupTimeoutSeconds`). Go has no structural typing, so any helper both paths call must take scalars. CODE-5 already learned this for `accountSlotFailure` and recorded it (non-spec-changes.md:752-756, implementation-checklist.md:36); CODE-4 did not. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:26-110; pkg/gateway/podlifecycle/podsession/binder.go:602-678.

FACT (verification saved for the next round, do not re-derive): every proto field number SCHEMA-1 stages is free on its message, checked directly. `PrepareWorkspaceResponse` holds 1,2 (schemas/lenny-adapter.proto:699); `FinalizeWorkspaceResponse` 1 (:761); `RunSetupResponse` 1 (:869); `StartSessionResponse` 1 (:958); `AssignCredentialsResponse` is `{}` (:1033); `ResumeResponse` 1,2,3 (:1433); `ShutdownRequest` 1,2,3,5,6 with 4 reserved (:1609); `ShutdownResponse` 1,2 (:1665); `ConfigureWorkspaceResponse` 1 (:1690).

FACT: the five `InvalidArgument` resolve sites are exactly where the proposal says, verified by grep: staging.go:134/:136, :181/:184, :337/:340, slotcreds.go:26/:28, slotsession.go:75/:77. `resolvePrepareStagingDir` runs once per `PrepareWorkspace` stream, guarded by `if stagingDir == ""` (staging.go:76), so the "resolves the entry once, at the frame from which it first resolves the slot identifier" rule matches the shipped loop. EVIDENCE: pkg/adapter/staging.go:76-84.

FACT: every `noteRuntimeStarted` caller the proposal enumerates is exact and complete — three production (session.go:163, resume.go:144, sdkwarm.go:261) and seven test (adapterevents_test.go:95,:184; export_test.go:45; usage_test.go:233; podmcp_arming_internal_test.go:84,:185,:230). No eleventh caller.

FACT: all six `ReleaseSlotReservation` call sites are covered by CODE-4's table — start.go:2834 (applySlotRetryPolicy), start.go:3246 (rollbackClaim), binder.go:1714 (releaseResumeSlot), slotbinder.go:172 (ClaimSlot connect-stage), slotbinder.go:217 (BindReservedSlot), plus the interface declaration at start.go:2727.

USEFUL [standing context, the `docs/reference/adapter-contract.md:75` DEFERRED chain]: the log records eleven lenses filing this site and explicitly hands the remedy to the non-spec loop ("the non-spec lane needs a DOCS-2 ... Scope it to :75", review-log.md:676, :2683). That saved me from re-deriving which sibling sites break: `:81`, `:84`, `security-principles.md:33`, `execution-modes.md:68`, `multi-tenancy.md:72` all read "at each session release" and stay TRUE. It also recorded that `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only four substrings that all survive, so no gate catches the drift. I filed the DOCS-2 gap this round because this loop is the one that owns it and the deliverable is still absent.

OPEN: the DOCS-2 remedy has to decide whether it also covers `adapter-contract.md:64` (the `DemoteSDK` row, incomplete rather than false after §4.7.1's caller rule) and whether the page's §15.4 mirror gains the bind-epoch and reclaim-hold contracts. I scoped the finding to :75, which is the half that is outright false.
### [non-spec.1.review-feasibility.1]

FACT: `tests/claim-map.json` is GENERATOR-PRODUCED, not hand-edited. `scripts/seed-claim-register.py`
derives every row from `gateway-runtime-comms.md` §7.1 plus an in-script `EXPLICIT` list (:169) and
a `GENERATION_FENCE` loop (:378), and the tier-0 gate `TestClaimRegisterIsReproducibleFromItsGenerator`
runs the generator and requires byte-identity with the committed file. SCHEMA-1 stages two rows in the
JSON and stages nothing in the generator, so S8 turns tier 0 red. The authoring source for a
hand-written row is the `EXPLICIT` list. EVIDENCE: tests/tier0_static/claim_register_generator_test.go:20-31,:45-80;
scripts/seed-claim-register.py:169-197,:377. FILED.

FACT: `validate-maps` (tier 0) requires every `_test.go` under `tests/tier{2,3,4,5,6,7a,7b,8,9,10,12}_*`
to be named by a `tests/spec-map.json` entry, matching the file path or ANY ancestor directory. No
directory entry exists for `tests/tier3_contract`, `tests/tier7a_load_local`, `tests/tier9_security` or
`tests/tier10_conformance` — every one is mapped file by file (tier3 also per-subdirectory). So all four
new test surfaces this proposal stages need a spec-map entry, and `tests/spec-map.json` is in no edit
list. This widens the standing Opens (#669, #674) that named only the tier-7a case. EVIDENCE:
cmd/lenny-test/cmd_validate.go:716-792,:125-137; cmd/lenny-test/cmd_run.go:761-770. FILED.

FACT: `Binder.Resume(ctx, req ResumeRequest)` (pkg/gateway/podlifecycle/podsession/binder.go:1590) holds
a `ResumeRequest`, and `ResumeRequest` carries exactly the three fields the compensation reads —
`SessionID`, `MaxConcurrentSessions`, `CleanupTimeoutSeconds` — so the scalar narrowing the proposal
already applied to `accountSlotFailure` (non-spec-changes.md:753-755) works verbatim here. The staged
`compensateFailedSlotBind(… req SlotBindRequest …)` at non-spec-changes.md:553 cannot be called from
that branch. Standing-context Open #661 recorded this and five lenses declined to file it; I filed it,
because it is a type error in a staged signature whose second named caller is in the same deliverable.

FACT: `pkg/gen/adapter/v1` does not exist; the generated proto package is `pkg/proto/adapter/v1`
(`make generate-proto` at Makefile:91-101 runs `buf generate` into `pkg/proto`). A fix round already
corrected both proposal sites in the r4→live delta; `grep pkg/gen` over the proposal now returns nothing
outside the review logs. Do not re-file.

FACT: the r4→live delta on this staging is TWO lines (the `pkg/gen`→`pkg/proto` rename at
non-spec-changes.md:1060 and :1797). `spec-r4` is the newest snapshot that differs at all, and its
`.spec-changes.md` is byte-identical to live. Diff `-rq` the whole snapshot directory first; mtime order
in `scratchpad/cp-snap/0081.../` is NOT round order (`spec-r4` sorts newer than `spec-r9`).

FACT: the nine SCHEMA-1 field numbers and the `SlotReclaimOutcome` enum re-verified mechanically against
`schemas/lenny-adapter.proto` this round: `ShutdownRequest` holds 1,2,3,reserved 4,5,6 (7 free, :1609-1635);
`ShutdownResponse` 1,2 (:1665); `PrepareWorkspaceResponse` 1,2 (:699); `FinalizeWorkspaceResponse` 1 (:761);
`ResumeResponse` 1,2,3 (:1433); `ConfigureWorkspaceResponse` 1 (:1690); `RunSetupResponse` 1 (:869);
`StartSessionResponse` 1 (:958); `AssignCredentialsResponse` is `{}` on one line (:1033). Enum value
spelling matches `SessionScrubOutcome` (:438-448). Clean; do not re-derive.

WATCHOUT: widening `ensureSlotPaths` to return the epoch breaks `pkg/adapter/exportpaths_test.go:25,:221,:225`,
which is in no file list. Judged below the bar (a compile break in a test file, and the loop has refuted
five bookkeeping-against-the-file-list findings on materiality), but a fixer touching CODE-6's file list
should add it. EVIDENCE: pkg/adapter/exportpaths_test.go:25,:221,:225.

DEFERRED [proposals/0081_.../0081_....non-spec-changes.md:1719-1722]: the accepted-failure-mode bullet
says a §7.3 resume refused by the hold "costs the pod one windowed `RecordFailure` … whose only
production call site is `applySlotRetryPolicy`". False against the STAGED tree: CODE-5 extracts
`RecordFailure` into `accountSlotFailure` and gives it three callers, and the resume path reaches it
only through the new `resumeOnPod` caller — which is what makes the bullet's own conclusion true. The
conclusion stands; only the parenthetical attribution is wrong. Same class as standing MISTAKE #469
(a scope call resting on shipped behaviour a staged deliverable removes). Not filed on materiality.

OPEN: the claim-register rows SCHEMA-1 stages carry no `note`, while every row in the generator's
`EXPLICIT` list does. The validator does not require one (tests/tier0_static/claim_register_test.go:275-283
checks only status, surface and anchor), so this is convention rather than a gate. Whoever moves the rows
into the generator should add notes to match the siblings.


### [non-spec.1.review-fresh.1]

FACT: every SCHEMA-1 field number re-verified free against the tree this round, with a
scripted brace-matched scan rather than an awk range (an awk range anchored on
`/^message X \{/,/^\}/` silently swallows the NEXT message when the target is the one-line
`message AssignCredentialsResponse {}`, which is how the false "field 1 collides with
session_id = 1" trap gets re-manufactured). Free numbers confirmed: ShutdownRequest 7
(1,2,3,reserved 4,5,6), ShutdownResponse 3, PrepareWorkspaceResponse 3,
FinalizeWorkspaceResponse 2, ResumeResponse 4, ConfigureWorkspaceResponse 2,
RunSetupResponse 2, AssignCredentialsResponse 1 (message is genuinely empty),
StartSessionResponse 2. EVIDENCE: schemas/lenny-adapter.proto:1609,1665,699,761,1433,1690,869,1033,958

FACT: every verbatim anchor SPEC-1..SPEC-5 quotes still matches the tree byte-for-byte.
Checked: spec/04:157 (§4.1 third sentence), spec/04:686 (§4.7 Shutdown row opening),
spec/04:854 (§4.7.9 step 5), spec/04:688/:695 (the §4.7.1 insertion window),
spec/05:453/:545/:555, spec/06:150-155 fence and :234 cancel bullet, spec/07:23/:210/:213/:214/:414,
spec/15:1469/:1471, spec/29:645. No anchor drift this round; a future citation lens can skip
re-deriving these.

FACT: `ABORTED` is the right transient code and the tree backs it — `SlotBindError.Reason()`
has arms for InvalidArgument, ResourceExhausted, PermissionDenied/FailedPrecondition and a
transient `default`, so `codes.Aborted` takes the default with no switch edit.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:85-100

FACT: the five resolve sites are exhaustive against the tree. Production callers of
`ensureSlotStateLocked` are exactly slotcreds.go:26, slotsession.go:75 and slot.go:143
(from `ensureSlotPaths`), and the only production callers of `ensureSlotPaths` are
staging.go:134, :181, :337. No sixth site. EVIDENCE: pkg/adapter/slot.go:105,:140-148

WATCHOUT: `heldSession` and `deregisterStartedSessions` live in `pkg/adapter/slotsession.go`,
not `holdstate.go`, even though CODE-6's prose attributes `deregisterStartedSessions` to
holdstate.go. The Files-touched list gets it right, so this is prose looseness rather than a
finding — do not spend a verifier pair on it. EVIDENCE: pkg/adapter/slotsession.go:306-312,:375;
pkg/adapter/holdstate.go:190,:229

DECISION: filed the `docs/reference/adapter-contract.md:75` missed edit site — BECAUSE this is
the non-spec loop, which owns the docs lane, and the standing Deferred entries plus eleven
prior lens filings all say the fix has never landed. The page's only staged docs deliverable is
DOCS-1 for state-machines.md (non-spec-changes.md:1152, :1841). Confirmed live this round that
the tier-11 gate is a four-substring presence check that all survive
(tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311), so nothing
catches it. ALTERNATIVES: leaving it to the between-loops pass, rejected because it has now
survived six loops of that.

DECISION: filed the tier-3 step-ordering gap — BECAUSE the tier-3 suite is titled "for
SCHEMA-1, CODE-1 and CODE-6" and two of its cases plus the caller-latch case need CODE-1 (S10)
and CODE-4 (S12), but the only steps naming tier 3 are S8 and S9, both of which land before
S10. S10's tier line reads "Tiers 0, 1, 7a, 9" and S12's "Tiers 0, 1, 4, 7a"; neither names
tier 3. EVIDENCE: implementation-checklist.md:21,:23,:25,:29; non-spec-changes.md:1453,:1472-1479

DECISION: filed the `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` gap in CODE-4's outcome switch — BECAUSE
the switch's `default: return false` swallows an `exited_cleanly:false` answer whose
`slot_reclaim` is unset, which is fail-open on the leak-accounting path and is exactly the
signal the pre-amendment `err != nil || !cleanly` form caught. ALTERNATIVES: treating it as a
version-skew hypothetical, rejected because §15.4 publishes this contract to third-party
adapter authors and proto3's zero value is reachable from any of them.

UNVERIFIED: §4.7.1's caller rule says a caller holds no epoch once it has issued "a `Shutdown`
answering `reclaimed`", but CODE-6 clears the latch only on `DemoteSDK` and on
`ShutdownReclaim`; the plain `Client.Shutdown` and `ShutdownRecycle` also remove the entry and
can answer `reclaimed` and do not clear. I judged it unobservable (nothing reads the latch on
those connections afterwards) and did not file. A later round that changes connection reuse
should re-check. EVIDENCE: spec-changes.md:682; non-spec-changes.md:996-1003;
pkg/gateway/podlifecycle/podsession/slotbinder.go:542, binder.go:2043

UNVERIFIED: CODE-1's fenced early returns (ABSENT / SUPERSEDED) return before clause three, so
a `Shutdown` carrying both a mismatched epoch and a recycle disposition would skip the whole-pod
scrub that staged §4.1 says runs "when the recycle disposition is set". Unreachable today
because `ShutdownRecycle` passes a zero epoch and the compensation carries no recycle. Did not
file. EVIDENCE: spec-changes.md:285; non-spec-changes.md:159-175; pkg/adapter/session.go:288-291

UNVERIFIED: CODE-5 appends to `ExcludePods` under `sbe.Leaked || relErr != nil`, which is wider
than SPEC-2's §5.2 sentence ("a pod ... whose reclaim for this session did not complete"),
because CODE-4 folds a failed reservation release into `Leaked`. Over-exclusion, not
under-exclusion, so I judged it a preference between workable designs. EVIDENCE:
spec-changes.md:530; non-spec-changes.md:765-774,:685
### [non-spec.1.review-fresh.1]

USEFUL [Trap #174, `AssignCredentialsResponse` is a ONE-LINE message]: it saved a false SCHEMA-1
finding. I reproduced the artifact exactly: `grep -n "^message AssignCredentialsResponse "` (with a
trailing space) MISSES `schemas/lenny-adapter.proto:1033` because the line is
`message AssignCredentialsResponse {}`, and an `awk` range then starts at `RotateCredentialsRequest`
(:1035) and prints its five fields. Use `grep -n "^message X\b"` and then `sed -n`. EVIDENCE:
schemas/lenny-adapter.proto:1033,:1035-1059.

FACT: every SCHEMA-1 field number re-verified free this round, message start lines and all.
ShutdownRequest 1,2,3,res4,5,6 → 7 free (:1609-1636); ShutdownResponse 1,2 → 3 (:1665-1668);
PrepareWorkspaceResponse 1,2 → 3 (:699-702); FinalizeWorkspaceResponse 1 → 2 (:761-768);
RunSetupResponse 1 → 2 (:869-871); StartSessionResponse 1 → 2 (:958-962); ResumeResponse 1,2,3 → 4
(:1433-1447); ConfigureWorkspaceResponse 1 → 2 (:1690-1692); AssignCredentialsResponse empty → 1
(:1033). Nobody needs to re-run this unless the proto moves.

FACT: `pkg/gen/adapter/v1` does not exist; the generated package is `pkg/proto/adapter/v1`. The
r4-to-now fix corrected both sites. EVIDENCE: pkg/adapter/session.go:16; `ls pkg/proto`.

FACT: the five `ensureSlotStateLocked`/`ensureSlotPaths` resolve sites CODE-6 enumerates are exactly
the five in the tree, no more. EVIDENCE: `grep -rn "ensureSlotStateLocked\|ensureSlotPaths"
pkg/adapter/*.go` → slot.go:143 (internal), staging.go:134,:181,:337, slotsession.go:75,
slotcreds.go:26.

FACT: `codes.Aborted` really does take `SlotBindError.Reason()`'s transient default at EVERY stage,
including credential_assignment: `Reason()` switches on the code first and only
`FailedPrecondition`/`PermissionDenied` branch on `e.Stage`. So the Testing section's claim that one
workspace-stage `Aborted` row covers the credential stage holds. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotfailure.go:85-101.

FACT: the tier-11 first-match hazard on the SPEC-3 §5.2 append is still inert, re-checked. No gate
anchors on "Slot cleanup" at all, and `lineContaining`/`requireLine` use case-sensitive
`strings.Contains` while the append writes "whole-pod replacement trigger" and "the pod's
served-session count" in lower case. EVIDENCE: tests/tier11_docs/backup_status_enum_test.go:48-56;
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92,:155,:298.

FACT: `deregisterSlot` (the s.mu-taking wrapper CODE-6 retires) has exactly two callers left and both
are tests, in `pkg/adapter/podmcp_arming_internal_test.go:88,:245`, and that file is already in the
proposal's test list. Retiring it costs nothing else. EVIDENCE: `grep -rn "deregisterSlot("
pkg/adapter/`.

MISTAKE (in the proposal, sub-threshold, not filed): CODE-6 justifies keeping
`deregisterSlotLocked`'s signature "for the read-only caller that needs the deregistration without a
destroy" (non-spec-changes.md:943-945). After the change all three production callers go through
`reclaimSlotLocked`, so there is no such caller; `deregisterSlotLocked` survives only as the
helper's own body. A fixer already in that paragraph should drop the clause.

WATCHOUT: `heldSession` is `{sessionID, state}` at pkg/adapter/slotsession.go:310-313 and pass 1 is
`deregisterStartedSessions` at :375-395, which loops `deregisterSlotLocked` under ONE `s.mu`. CODE-6's
`release func()` field therefore has to be captured per member inside that loop, and
`terminateHeldSession` (pkg/adapter/holdstate.go:229) is the deferring site. The whole set is held
from pass 1 until pass 2's last member returns, which can be the full ten seconds of
holdstate.go:201's `closeCtx`. Nothing in the proposal says the hold set can carry several
identifiers at once; it is correct, but a reader of CODE-6 alone will not expect it.

FACT: `holdOrFailOnResumeError` (pkg/gateway/sessionserver/start.go:3609-3620) sends anything
`isTransientPodClaimError` does not match to `s.failSession`, i.e. terminal `failed`, and
`isTransientPodClaimError` (:3648-3681) is a CLOSED enumeration of four typed errors and five
sentinels with no status-code arm. A bare `codes.Aborted` matches none of them. This is the ground
for the filed finding on the reclaim-hold refusal reaching the §7.3 resume caller.

FACT: `handleUploadToSession` really does answer HTTP 502 `UPSTREAM_ERROR` on an adapter error, as
the tier-7a upload arm claims. EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:70,:130,:136.

DECISION: filed the tier-3 gap on the CHECKLIST (S10 runs no tier 3) rather than on the Testing
section — BECAUSE the tier-3 suite is correctly scoped ("SCHEMA-1, CODE-1 and CODE-6"), and the
defect is that the only steps running tier 3 are S8 and S9, both of which land before CODE-1 exists.
ALTERNATIVES: moving the two Shutdown-outcome cases into S9's scope was rejected, because CODE-6
explicitly defines none of the Shutdown comparison (non-spec-changes.md:121-123).

DECISION: filed `docs/reference/adapter-contract.md:75` as a missing edit site — BECAUSE this is the
non-spec loop the standing Deferred names as its owner, and the row is falsified by SPEC-1 and
SPEC-3 in three ways. Verified in the tree this round: the row still states the single undifferentiated
teardown, and `grep -rn adapter-contract` over summary / spec-changes / non-spec-changes /
implementation-checklist returns nothing. Note the tier-11 gate that reads this file
(`adapter_manifest_credentials_path_doc_reconciliation_test.go`) asserts only the Adapter Manifest
section, so nothing catches the stale row.

OPEN: summary.md:117 says "Seven residues survive" and then enumerates four plus two. Not filed
(same class as the already-refuted "Four statements are needed" count), but whoever next edits that
bullet should either name the seventh or drop the count, which `doc-style.md` would anyway.

OPEN: summary.md:158-159 still says "the compensation's own adapter edits land in `session.go` and
`runtimegeneration.go`", which CODE-2's extension to `resume.go` and `sdkwarm.go` falsified. This is
the long-standing "Decisions bullet names only two adapter files" item; its other half (slot.go /
slotsession.go) WAS repaired and now reads correctly at :160-167, so only the first clause is stale.


### [non-spec.1.review-kubernetes.1]

DECISION: returned zero findings under the Kubernetes-idiom lens — BECAUSE the amendment's whole surface (bind epoch, reclaim hold, outcome enum, ExcludePods) is in-process adapter state and in-process gateway request state; it writes no CRD status, adds no finalizer, adds no watch, and puts no controller reconcile on a synchronous request path — ALTERNATIVES: I looked for a status-as-RPC-inbox, a two-manager field race, a CRD-as-message-bus use of SandboxClaim, and a hot-path block on WarmPoolController, and found none.

FACT: the amendment adds NO Kubernetes surface at all. `grep -n "Status\b\|status\."` over the non-spec staging returns exactly one hit, and it is the gRPC `status` package (`status.Errorf(codes.Aborted, ...)`). EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:391

FACT: `Controller` appears exactly once across summary, spec-changes, non-spec-changes and checklist, and the sentence is accurate. `DrainSandbox` stamps the `lenny.dev/drain-request` annotation on the agent **Pod** with `client.FieldOwner(ownership.Gateway)` and never touches `Sandbox.status`, which matches §4.6.3's gateway RBAC paragraph ("`get`/`patch` on `Pods` ... The gateway holds no `sandboxes/status` grant"). EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:880-883; pkg/gateway/podlifecycle/podsession/slotbinder.go:601-603; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:95-108; spec/04_system-components.md:632

FACT: the proposal's justification for `ExcludePods` — "`DrainSandbox` only stamps the annotation the WarmPoolController acts on asynchronously, and `ClaimSlot`'s pass-1 scan of claimed pods reads the per-pod claim rather than the Sandbox phase" — is verified in the tree. Pass 1 filters on `c.podClaim(...)` + `claimstate.IsTerminal` + tenant pin + `expiredByUptime`, with no `sb.Status.Phase` read; only pass 2 reads `sb.Status.Phase != Idle`. This is the idiomatically correct answer (do not block a synchronous bind on a level-triggered controller), so the deliverable is right for the right reason. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:411-467 (pass 1), :472-478 (pass 2)

FACT: `s.podBinder` is the concrete `*podsession.Binder` and already satisfies the unexported `slotBinder` interface (`BindSlot`, `ReleaseSlotReservation`, `DrainSandbox`), so CODE-5's new `resumeOnPod` caller of `accountSlotFailure(ctx, s.podBinder, ...)` compiles without widening the interface, as the deliverable claims. EVIDENCE: pkg/gateway/sessionserver/start.go:2725-2729,:2735-2737

WATCHOUT: do not spend a round re-deriving CRD ownership on this proposal. The standing context's "Ownership is clean" entry is correct and I re-verified it end to end (no `sandboxes/status` write, no SSA force-ownership, no finalizer, the leaked-slot `SandboxClaim` left at `bound` is collected by the §4.6.1 orphan-GC predicate). Nothing in the r9→r10 amendment moved any of it. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:95-108; spec/04_system-components.md:632

USEFUL [Standing context / "Ownership is clean"]: saved me the whole ownership sweep; I only had to confirm the amendment did not add a new k8s write, which one grep settled.
### [non-spec.1.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens on the post-amendment staging — BECAUSE the amendment's new surface (SPEC-5, SCHEMA-1, CODE-6, CONF-1, and the CODE-1/2/4/5 amendments) is entirely adapter-process and gateway-in-process: a pod-local `int64` epoch that is never persisted, an in-adapter mutex-style identifier hold, an additive proto edit, and an in-memory `ExcludePods` request field. No CRD status write, no field-manager contention, no finalizer, no webhook, no reconcile on a synchronous request path, and no use of etcd as a message bus is introduced anywhere in the staging — ALTERNATIVES: I considered filing on (a) the `leaked=true` release leaving the per-pod `SandboxClaim` at `bound`, and (b) the CODE-5 tier-2 placement-filter cases omitting an `assertNotGatewayStatusOwned` assertion; both fail the bar (see FACTs below).

FACT: the epoch is deliberately NOT persisted and NOT a CRD field, which is what keeps this change off the Kubernetes-idiom surface entirely. The whole staged mechanism lives in `pkg/adapter` process memory plus one in-process gateway request field. EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:51-67 ("the adapter mints one when it creates a registry entry … holds it unchanged for that entry's life"), :89-95 ("On the gateway side: one in-process error field, one in-process request field … a per-connection epoch latch").

FACT: `Binder.DrainSandbox` only stamps the `lenny.dev/drain-request` annotation via `podclaim.StampDrainRequest`; it writes no `Sandbox.status`, and its own `// spec:` comment says so. So CODE-5's third `accountSlotFailure` caller cannot make the gateway a second writer of a WarmPoolController-owned status field, and the proposal's claim at non-spec-changes.md:878-883 ("`DrainSandbox` only stamps the `lenny.dev/drain-request` annotation the WarmPoolController acts on asynchronously") is verified true. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:595-603.

FACT: the `leaked=true` release leaving the per-pod `SandboxClaim` at `bound` is NOT a stuck-object footgun. `ClaimGarbageCollector.classify` drains a `bound` claim once its `Status.BindingStateTransitionTime` (falling back to `CreationTimestamp`) ages past `claimOrphanTimeout`, gated on `Sessions.PodHasActiveSession` returning false, so a level-triggered backstop exists and the object stays deletable. Do not re-file the tier-4 assertion at non-spec-changes.md:1520-1524 as an undeletable-object defect. EVIDENCE: pkg/controller/warmpool/gc.go:223-320; review-log.md standing-context entries 75 and 241.

WATCHOUT: the shipped precedent pair for CODE-5's tier-2 placement-filter cases (`TestClaimSlotSkipsOverUptime{Claimed,Idle}Pod_spec_6_2`) carries an explicit `assertNotGatewayStatusOwned` + phase-unchanged assertion, and the proposal's five staged `ExcludePods` cases (non-spec-changes.md:1441-1451) carry no such assertion. This LOOKS like a missing ownership test but is below the bar: `ExcludePods` is a read-only `continue` in each candidate pass and writes nothing, so the assertion would be nice-to-have hardening rather than coverage the change requires. Do not file it. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go:648-707 (the precedent's assertions); proposals/0081_.../0081_....non-spec-changes.md:1441-1451.

USEFUL [standing-context 76, 161, 249]: the three entries recording that ownership is clean, that retirement is never a gateway act at the release, and that the staged edits touch no §10.3/§13.1/§13.2 control were each independently re-verifiable in minutes and correctly predicted that this lens has no surface here. A future Kubernetes-idiom pass on 0081 can start from them and confine itself to whatever new deliverable an amendment adds.


### [non-spec.1.review-mechanism.1]

FACT: the round's real delta is the CHECKLIST and the SUMMARY only. `diff -rq scratchpad/cp-snap/0081/non-spec-r1-start <proposal dir>` is byte-identical, and `spec-r9` differs only in `.implementation-checklist.md` and `.summary.md`. The checklist was RE-ORDERED (SPEC-5 is now S1; SPEC-1 S2, SPEC-2 S3, SPEC-3 S4, SPEC-4 S5) and every `Depends on` was re-pointed; the summary gained open decision 12, a §10.1-fence out-of-scope row, a Fresh-workspace-guarantee out-of-scope row, a rewritten 0080 §1.19 row, and `sdkwarm.go` on the CODE-2 and CODE-6 index lines. EVIDENCE: proposals/0081_.../0081_....implementation-checklist.md:7-52

FACT: every citation in the newly-added summary text checks out. spec/10_gateway-internals.md:30 ("Pods validate the generation on every gateway→pod RPC"), :66-70 (stale-replica steps), schemas/lenny-adapter.proto:1630-1635 (`coordination_generation = 6`), pkg/adapter/coordination.go:120 and :262 (the only two `GetCoordinationGeneration()` reads), pkg/adapter/slotsession.go:274-283 (`boundSlotState`, one `FailedPrecondition` for absent and unbound alike), spec/05:553/:555/:556 (retry policy heading, `**Max retries:**`, `**Fresh workspace guarantee:**`), pkg/adapter/slot.go:105-124 (`EnsureTree` on the create branch only). Do not re-verify these.

FILED [1]: `compensateFailedSlotBind` is staged as `req SlotBindRequest` while `Binder.Resume` holds a `ResumeRequest`, and CODE-4 stages the resume branch to call it. This was standing-context Open #613 ("five lenses recorded it and none filed it"); it is a genuine unassemblable call, not a preference, and the proposal already solved the identical mismatch for `accountSlotFailure` by narrowing to `pool string, maxConcurrentSessions int32`. The scalar remedy works: the body reads only `SessionID`, `CleanupTimeoutSeconds` and `MaxConcurrentSessions`, all present on `ResumeRequest`. EVIDENCE: non-spec-changes.md:553; pkg/gateway/podlifecycle/podsession/binder.go:602-606,:1590

FILED [2]: CODE-5's "The `slotBinder` interface is unchanged ... so every implementing type and both test fakes compile as they stand" (non-spec-changes.md:845-847) is false and was already recorded as "MISTAKE filed twice, and it is still standing". CODE-4's own call-site table stages `ReleaseSlotReservation` on that interface (non-spec-changes.md:687), CODE-5's Targets names the interface (:727) and `## Files touched` names it (:1836). Verified in the tree: start.go:2726-2728 declares the three-parameter form and both fakes implement it (slotretry_test.go:52, slotretry_load_test.go:33); `fakeSlotBinder.released` is `[][2]string` and cannot carry the `leaked=true` the staged tier-1 accounting case asserts.

WATCHOUT: "an acknowledged reclaim is answered only after its cleanup finished" (spec-changes.md:192, restated at non-spec-changes.md:1705) is not strictly true once CODE-6 routes `releaseSessionSlot` through `reclaimSlotLocked`. An `ABSENT` answer can be returned while a DIFFERENT concurrent cleanup for the same identifier (the adapter's own pre-`Runtime.Start` rollback) still holds it, so a §5.2-placed retry can meet the hold. Reachable only when the gateway's `StartSession` context expired while the adapter handler was still unwinding; the consequence is one more producer of the accepted transient refusal. Judged below the bar (enumeration completeness in rationale prose, no staged text falsified, outcome already accepted). Do not spend a verifier pair on it without new evidence of harm.

WATCHOUT: §4.7.1's caller rule says the caller holds no epoch "once it has itself issued an RPC on that connection that removes the entry the epoch names: `DemoteSDK` ... and a `Shutdown` answering `reclaimed`" (spec-changes.md:682), while CODE-6 clears the latch only on `DemoteSDK` and on `ShutdownReclaim` answering `RECLAIMED` — never on the plain `Shutdown`/`ShutdownRecycle` that keep their signatures and send zero. Unreachable in consequence, because `BindResult.Adapter` is closed at session end and both later calls send zero anyway. Not filed.

FACT: CODE-1's epoch early-returns bypass clause three (the recycle scrub, pkg/adapter/session.go:288-292). Unreachable, because only a fenced compensation carries a non-zero epoch and it never sets `recycle`; standing traps 565/566 already own this. Do not re-derive.

USEFUL [Standing context #613, #530]: both filed findings came straight out of the Open/Trap lists. The lists are worth reading in full before hunting; two live defects were sitting there unfiled.
### [non-spec.1.review-mechanism.1]

FACT: every proto field number SCHEMA-1 claims free IS free, verified against the live file. ShutdownRequest holds 1,2,3,5,6 with 4 reserved (7 free); ShutdownResponse 1,2; PrepareWorkspaceResponse 1,2; FinalizeWorkspaceResponse 1; ResumeResponse 1,2,3; ConfigureWorkspaceResponse 1; RunSetupResponse 1; AssignCredentialsResponse empty; StartSessionResponse 1. Do not re-verify. — EVIDENCE: schemas/lenny-adapter.proto (messages as listed)

FACT: the Go structs the epoch mechanism extends live in files the edit list does not name. `slotState` is declared at pkg/adapter/slot.go:21 and `Server` at pkg/adapter/server.go:59. `pkg/adapter/server.go` appears nowhere in `## Files touched on application (non-spec)` (non-spec-changes.md:1793-1846), yet CODE-6 puts `Server.bindEpoch` and `Server.reclaiming` in bindepoch.go (non-spec-changes.md:916-917), which Go forbids. Filed. — EVIDENCE: pkg/adapter/server.go:59

FACT: `compensateFailedSlotBind`'s parameter is `req SlotBindRequest` (non-spec-changes.md:553) but `Binder.Resume` holds a `ResumeRequest` (pkg/gateway/podlifecycle/podsession/binder.go:602) and is told to call the same function (non-spec-changes.md:698). CODE-5 narrowed `accountSlotFailure`'s parameters for exactly this reason (non-spec-changes.md:753-756) and CODE-4 was not given the same treatment. Filed.

WATCHOUT: `codes.Aborted` really does take `SlotBindError.Reason()`'s transient default and `codes.InvalidArgument` really does map to workspace_validation/NonRetryable — both verified. Do not re-derive. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:41-48,:84-101

WATCHOUT: I considered filing that §15.4's staged reclaim-hold clause ("a request that would create or resolve a registry entry under it is refused with ABORTED ... an adapter that refuses one with a permanent status does not conform") is broader than CODE-6, which puts the refusal only in `ensureSlotStateLocked`; every `boundSlotState`-routed session RPC still answers `codes.FailedPrecondition` during a hold. I did NOT file it: during the hold no entry exists, so those RPCs would fail identically without the hold and the hold refuses them nothing. A future filing needs a request that the hold, and only the hold, turns from admitted into permanently refused. — EVIDENCE: pkg/adapter/slotsession.go:264-283; spec-changes.md:708

WATCHOUT: I considered filing that CODE-4's outcome switch `default: return false` folds `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` plus `exited_cleanly: false` into "not leaked", a fail-open regression against the shipped `Binder.ReleaseSlot` posture at pkg/gateway/podlifecycle/podsession/slotbinder.go:533-543 ("fail closed: on doubt the slot stays counted"). I did NOT file it: staged §7.1 enumerates exactly two incomplete-reclaim conditions (unanswered, and `reclaimed` without a clean exit), so the default arm is spec-conformant and the rest is hardening. — EVIDENCE: non-spec-changes.md:580-581; spec-changes.md:390

FACT: the compensation is synchronous inside `materializeSlot`, so the §5.2 in-gateway retry can never race its own attempt's reclaim; the racing "retry" in the design and in the tier-4 late-reclaim arm is necessarily a fresh client request. Anyone reasoning about that ordering should start there. — EVIDENCE: non-spec-changes.md:630-645

FACT: `reclaimSlotLocked` inserts a hold only when it removed an entry, and its `release` is a non-nil idempotent no-op otherwise — which is what stops a concurrent unfenced `Shutdown` (removed=false) from clearing another caller's hold on its deferred release. This is subtle and correct as staged; do not "simplify" it. — EVIDENCE: non-spec-changes.md:924-934


### [non-spec.1.review-operational.1]

DECISION: filed exactly one finding, the missing `docs/reference/adapter-contract.md:75` DOCS-2 deliverable — BECAUSE this loop reads the non-spec lane, which owns the docs deliverables, and the standing DEFERRED that eleven earlier lenses filed is now landable rather than deferrable — ALTERNATIVES: filing `adapter-contract.md:64` (`DemoteSDK` row) and the absent §15.4 bind-epoch/reclaim-hold mirror on the same page, both rejected as incompleteness in a summary row rather than a false statement; filing `:81`, rejected because prior rounds established SPEC-3 keeps the pre-`running` cleanup outside the term "session release".

USEFUL [standing context DEFERRED at review-log.md:676 and :2331]: both entries scope the docs edit correctly to `:75` and explicitly rule the four sibling "at each session release" sites (`adapter-contract.md:81`, `security-principles.md:33`, `execution-modes.md:68`, `multi-tenancy.md:72`) out. That saved a full re-derivation of the docs blast radius.

FACT: the tier-11 gate on that row, `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`, requires the row to CONTAIN the substrings "end-of-session teardown", "recycle disposition", "ReportSessionScrub", and "ReportPodScrub". A DOCS-2 that deletes the `ReportSessionScrub` mention rather than qualifying it turns the gate red. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311.

FACT: the whole observability lane is clean for this change and needs no re-derivation. No alert in `pkg/alerting/rules` mentions a slot at all; `lenny_adapter_leaked_slots` appears in NO docs page and in no spec/16 inventory (only spec/05:545 and spec/06:160 prose), so it has no catalog row this change could falsify; `lenny_slot_failure_total`'s docs row (docs/reference/metrics.md:166, "`maxConcurrentSessions > 1`") stays true because nothing staged touches `recordSlotFailure` and CODE-5's new resume caller is gated on a non-empty slot id, which an exclusive pool never reserves. EVIDENCE: docs/reference/metrics.md:166; pkg/gateway/podlifecycle/podsession/slotbinder.go:353-362,:449-451.

FACT: `Registry.MarkLeaked` does NOT go through `ValidTransitions`, it overwrites the record's state and seeds an untracked slot. So the accepted residue "a slot marked leaked out of `slot_assigned`, an edge §6.2 does not carry" produces no runtime rejection, and it is not a mechanism defect. EVIDENCE: pkg/sandbox/slotstate/registry.go:99-115.

FACT: the r9→now diff touches only the checklist and the summary; `spec-changes.md` and `non-spec-changes.md` are byte-identical to the r9 snapshot. The new summary blocks (§10.1 coordination-generation fence, §5.2 Fresh-workspace-guarantee residue, the reworked 0080 §1.19 row) all cite accurately: spec/10:30 and :66-68, schemas/lenny-adapter.proto:1630-1635, pkg/adapter/coordination.go:120,:262,:116, spec/05:553-556, pkg/adapter/slot.go:105-124, pkg/adapter/slotsession.go:274-283 all read as quoted. Verified, do not re-verify.

OPEN: after DOCS-2 lands, does the tier-11 battery want a positive gate on the new row's content (the two preconditions, the withheld report) the way DOCS-1 gets one? The existing gate is substring-presence only and would pass a stale row again next time §4.7 moves.
### [non-spec.1.review-operational.1]

FACT: `tests/claim-map.json` is GENERATOR-PRODUCED and a tier-0 gate enforces byte-identity with the generator's output. `TestClaimRegisterIsReproducibleFromItsGenerator` runs `scripts/seed-claim-register.py --out <tmp>` and compares bytes; the row sources are the `gateway-runtime-comms.md` §7.1 status table plus the hand-written `EXPLICIT` list in the script. SCHEMA-1 stages two rows into the JSON and names neither producer, so S8 turns tier 0 red. — EVIDENCE: tests/tier0_static/claim_register_generator_test.go:20-27,:44-80; scripts/seed-claim-register.py:11-13,:169-197,:378. Filed.

FACT: `validate-maps` (tier 0) requires a `tests/spec-map.json` entry for every `_test.go` under `tests/tier{2,3,4,5,6,7a,7b,8,9,10,12}_*`, matched by exact path or by any MAPPED ANCESTOR DIRECTORY. None of `tests/tier3_contract`, `tests/tier9_security`, `tests/tier10_conformance`, `tests/tier7a_load_local` is mapped as a bare directory (checked by parsing the map), so all four of this proposal's new test surfaces need entries. `tests/spec-map.json` is in no edit list. — EVIDENCE: cmd/lenny-test/cmd_validate.go:717-790; non-spec-changes.md:1034,:1455,:1585,:1629,:1853-1856. Filed. This WIDENS standing Open items 669/674, which framed the gap as the tier-7a file alone; the amendment added three more.

USEFUL [Standing context, Deferred :732]: the `docs/reference/adapter-contract.md:75` entry saved the whole derivation. It is still unlanded, the file is named nowhere in the proposal outside the log, and the entry says this loop owns the edit. Filed, with the epoch fence added as a fourth falsification the entry predates.

FACT: the alert/metric half of the operational lens really is inert on this proposal, re-verified against the amendment. No new metric, no new label value, no alert rule touches a slot metric, and the only metric name any staged text writes (`lenny_adapter_leaked_slots`) is already in spec/05:545 and spec/06:160, so SPEC-3's third mention creates no spec/16 or docs/reference/metrics.md obligation. — EVIDENCE: grep `lenny_` over both staging files returns two hits; spec/05:545; spec/06:160.

DECISION: I did NOT file `docs/reference/adapter-contract.md:64` (the `DemoteSDK` row) alongside :75 — BECAUSE §4.7.1's caller rule makes the demotion drop the caller's epoch, which leaves the one-line row INCOMPLETE rather than false, and a one-line RPC summary that omits a caller-side consequence is below the bar. ALTERNATIVES: filing both as one finding, rejected because the verifier would have to carry a weak half; the fix for :75 should still pick :64 up, as standing Deferred :750 says.

DECISION: I did not re-file the `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` question (standing Open 717), the hold-refusal-has-no-metric residue (non-spec-changes.md:1729, recorded and accepted), or the `lenny_slot_pod_replacement_total` label divergence (pre-existing, Settled #233).


### [non-spec.1.review-performance.1]

DECISION: Filed two findings, both in the non-spec lane, both about what a reclaim-hold
refusal COSTS rather than about the hold mechanism itself — BECAUSE the hold, the epoch, the
`ExcludePods` filter and the compensation budget all survived a capacity/failure-mode read,
but the two sentences that state the price of a hold refusal disagree with the spec lane and
with the same file's own accepted-failure-mode bullet. ALTERNATIVES: rejected filing pod-churn
amplification (three new `accountSlotFailure` callers × `UnhealthyThreshold(2) == 1` ⇒ one
failure retires a pod) because non-spec-changes.md:1770-1780 states and accepts it; rejected
filing the exclusive-pool compensation budget (`cleanupTimeoutSeconds/1`, 30-60s, held
synchronously in the client's request) because :617-620 states it and argues the trade;
rejected re-filing the Redis-restart durable-fallback hole because summary.md:551-582 records
it as a shipped defect with the four §12.4/§5.2/§10.1.4 restatement sites named.

FACT: `RecordFailure` has exactly one production call site in the shipped tree,
`applySlotRetryPolicy` — EVIDENCE: pkg/gateway/sessionserver/start.go:2854, and
`grep -rn "RecordFailure(" pkg/ | grep -v _test` returns no other `slothealth` caller. CODE-5
adds two more (`bindConcurrentSlot`'s reserved branch and `resumeOnPod`), so any proposal
sentence saying "whose only production call site is applySlotRetryPolicy" is a
pre-amendment fact the amendment falsifies.

FACT: `resumeOnPod` (pkg/gateway/sessionserver/start.go:3943) never routes through
`applySlotRetryPolicy` (:2807); it calls `s.podBinder.Resume` and returns the error at ~:4041.
Any cost attributed to the §7.3 re-attach must be attributed to CODE-5's new caller there.

FACT: `UnhealthyThreshold(maxConcurrent) = (maxConcurrent+1)/2`, so it is 1 at
`maxConcurrentSessions: 2` — EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:215-220.
One windowed `RecordFailure` therefore drains the pod at that concurrency. This is the
multiplier behind every "does a transient refusal retire a pod" question on this proposal.

WATCHOUT: the three deregister-then-destroy sites CODE-6 claims are real and complete — do not
spend a pass re-deriving them. EVIDENCE: pkg/adapter/session.go:238 (`Shutdown`),
pkg/adapter/slotsession.go:215 (`releaseSessionSlot`), pkg/adapter/slotsession.go:389
(`deregisterStartedSessions`) → pkg/adapter/holdstate.go:254 (`removeSlotTree`).
`pkg/adapter/podscrub.go` is not a fourth: it is the only reader of §5.2's
`max(cleanupTimeoutSeconds/maxConcurrentSessions, 5)` figure and deregisters no entry.

WATCHOUT: non-spec-changes.md:900 ("The attempt spends a retry on the refusal instead") is the
LAST surviving copy of a claim the spec loop already killed on the spec side. The killed-list
entry reads "Staged §7.1 says a retry meeting the reclaim hold spends a §5.2
slot-retry-policy attempt"; spec-changes.md:390 now says the opposite. When a fix crosses
lanes, grep the other lane for the same sentence.

UNVERIFIED: whether a mass §7.3 recovery storm (pod-loss fan-out) can cascade through the new
`resumeOnPod` accounting caller — each failed resume drains its replacement pod at
`maxConcurrentSessions: 2`, and the replacements themselves are resumed onto. Nobody has
modelled the loop. A capacity reviewer with the Tier 3 pod-churn numbers should.
### [non-spec.1.review-performance.1]

DECISION: returned zero findings — BECAUSE every perf/scale/failure-mode axis my lens owns
came back either clean or already disclosed in the proposal's own text. ALTERNATIVES:
I built and dropped four candidates, each recorded below so nobody rebuilds them.

FACT: the amendment adds NO store-backed state. The bind epoch is an in-process `int64`
per `slotState`, the reclaim hold is `map[string]struct{}` on `Server`, and `ExcludePods`
is a slice on an in-process request struct. Nothing reaches etcd, Postgres or Redis, so
there is no §12.4 durable-fallback obligation and no new write amplification: the only new
control-plane writes are the two new `accountSlotFailure` callers (reserved branch, resume
branch), which fire on FAILURE rather than per request, and both are disclosed at
non-spec-changes.md:1764-1773. EVIDENCE: non-spec-changes.md:914-933, :816-841.

FACT: the epoch latch is per-CONNECTION and every bind attempt dials its own connection,
so no two sessions share a latch even on a pod at `maxConcurrentSessions: 4`.
`Binder.DialAdapter` is called per attempt at slotbinder.go:237/:459 and binder.go:1152/:1776,
and `podsession.Registry` is keyed by sessionID (registry.go:25,:33). The "latch clobbered
by a co-tenant's bind" finding I started building has no premise.
EVIDENCE: pkg/gateway/podlifecycle/podsession/registry.go:25; slotbinder.go:459.

FACT: `AssignCredentialsResponse` really is an empty message
(schemas/lenny-adapter.proto:1033, one line: `message AssignCredentialsResponse {}`), so
SCHEMA-1's `bind_epoch = 1` is correct. WATCHOUT: an `awk '/^message X \{/,/^\}/'` range
over that file silently swallows the NEXT message when the target is a one-line `{}` decl,
which made it look as though field 1 was taken by `SessionId session_id`. I re-ran with a
brace-balance script; all nine field numbers in SCHEMA-1's table are free.
EVIDENCE: schemas/lenny-adapter.proto:1033, :1609-1635, :1665-1667, :699-701, :761-767,
:1433-1446, :1690-1691, :869-870, :958-961.

MISTAKE (mine, nearly filed): "§5.2's hold sentence says the adapter admits no request that
would create or resolve an entry under a held identifier, while CODE-1's `Shutdown` resolves
one and the tier-1 suite asserts a second `Shutdown` during a hold answers `ABSENT`."
Dead: §15.4's hold block carves it explicitly — "`Shutdown` is not held: a `Shutdown` naming
a session whose cleanup is running removes nothing and answers as Section 4.7 states".
EVIDENCE: spec-changes.md:711; non-spec-changes.md:1195-1197.

MISTAKE (mine, nearly filed): "the SUPERSEDED branch returns `leaked=false`, so
`ReleaseSlotReservation` decrements `active_slots` and can hit occupancy zero, deleting the
per-pod claim while the successor is serving." Dead as a REGRESSION: the shipped tree already
hard-codes `ReleaseSlot(ctx, sandboxName, false, false)` on a failed bind, and the
pre-amendment design reached the same `leaked=false` on a clean `RECLAIMED`. The
re-reserves-nothing half is Settled #122, pre-existing and out of scope.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:493-503.

MISTAKE (mine, nearly filed): "`slotCleanupBudget` divides by `MaxConcurrentSessions` and
the resume path can carry zero." Dead: `resumeRequest` normalizes through
`maxConcurrentSessions(bound)` (start.go:3353-3358, applied at :4029), and `SlotBindRequest`
is built only on `match.MaxConcurrentSessions > 1` pools (:2546). No division by zero.

WATCHOUT: the latency family on the resume path is DISCLOSED, not open. On an exclusive pool
`Binder.Resume`'s compensation runs with `slotCleanupBudget` degenerating to the whole pool
`cleanupTimeoutSeconds` (default 60, documented as "Must be `> 0`" with no upper bound,
docs/reference/configuration.md:99), inside the client's request. The proposal names and
prices this at non-spec-changes.md:615-623, and the standing Trap "five performance dresses"
already declined the family on the ground that §16.5 samples successful creates only. A
future round wanting it must argue from a STATED budget it breaches, not from the wall clock.

UNVERIFIED: `compensateFailedSlotBind`'s signature takes `req SlotBindRequest`
(non-spec-changes.md:553) while `Binder.Resume` holds a `ResumeRequest`. Both structs carry
`Pool`, `SessionID`, `MaxConcurrentSessions` and `CleanupTimeoutSeconds`
(binder.go, `type ResumeRequest struct` fields 39-44 and 60-66 of the block), so the
implementor can narrow the parameters or synthesize a request. I judged it below the bar
(over-specification cuts the other way), but a feasibility lens should decide.

FACT: only five sites call `ensureSlotStateLocked`/`ensureSlotPaths` and the hold therefore
cannot reach the §4.9 rotation path: `RotateCredentials` goes through
`rotateCredentialsSlot`, which resolves nothing through the helper.
EVIDENCE: pkg/adapter/credentials.go:74,:82; pkg/adapter/slotcreds.go:26; slot.go:105,:143;
staging.go:134,:181,:337; slotsession.go:75.


### [non-spec.1.review-reliability.1]

FACT: this round's delta against `spec-r9` is ONLY the implementation checklist (a full
renumber that moves SPEC-5 to S1 and pushes the docs step to S6) and `summary.md` (open
decision 12 added, entries 13/14 retired, two new out-of-scope rows: the §10.1 generation
fence and §5.2's Fresh workspace guarantee, plus the 0080 §1.19 row rewrite and the
CODE-2/CODE-6 index lines gaining `sdkwarm.go`). `non-spec-changes.md` and
`spec-changes.md` are byte-identical to the snapshot. EVIDENCE: `diff -rq
scratchpad/cp-snap/0081_.../spec-r9 proposals/0081_...` returns three files.

FACT: the reordered checklist is coherent as a dependency graph. S1=SPEC-5, S2=SPEC-1,
S3=SPEC-2, S4=SPEC-3, S5=SPEC-4, S6=DOCS-1, S7=CODE-3, S8=SCHEMA-1, S9=CODE-6, S10=CODE-1,
S11=CODE-2, S12=CODE-4, S13=CODE-5, S14=CONF-1. Every `Depends on` names an earlier step;
S8's rule S-2 precondition ("precedes every step that opens a handler file") holds because
S9-S13 are the only handler steps; the one forward citation the order cannot avoid
(§4.7.1 pointing at §5.2's reclaim hold, landed at S4) is declared in S1's own line. This
closes the standing Open "S1 lands spec text that cites a §4.7.1 statement S5 lands later".
Do not re-derive the graph.

FILED: **CODE-4's `b.releaseCredentials(req.SessionID)` is session-keyed and unfenced, so a
failed attempt's wrapper can release a concurrent successor attempt's live §4.9 leases.**
The wrapper (non-spec-changes.md:630-652) runs `compensateFailedSlotBind` first, which
blocks up to `slotCleanupBudget` (>= 5s, up to the whole pool `cleanupTimeoutSeconds`) on a
`context.WithoutCancel`, and only then calls `b.releaseCredentials(req.SessionID)`.
`CredentialAssigner.ReleaseSession` (binder.go:327-332) →
`credassign.Service.ReleaseSession` iterates `s.leases.LeasesBySession([sessionID])` and
`releaseLocked`s every one (credassign.go:400-410), and `assignSlotCredentials` mints per
attempt keyed on `req.SessionID` (slotbinder.go:379). The design's own headline scenario —
"the client's context expired, the client retried, and the retry took the slot"
(non-spec-changes.md:588-589) — puts attempt 2 past `assignSlotCredentials` inside that
window. The epoch fences the pod-side reclaim to `SUPERSEDED`, and the very next statement
in the same wrapper destroys the successor's leases anyway. New in this proposal:
`materializeSlot` releases no lease on any failure branch today.

WATCHOUT: two standing entries look like they refute the above and do not.
(1) The Trap "Do NOT make the bind-path lease release conditional on `assignSlotCredentials`
having SUCCEEDED" bars a STAGE condition. The defect is the release's KEY (session, not the
attempt's own lease set), a different axis; releasing exactly the lease ids this attempt
minted stays unconditional over a partially-succeeded assign, which is what that Trap
protects. (2) The refuted "CODE-4's unconditional `releaseCredentials` releases leases a
snapshotless resume-rebuild may still hold" died on "the rebuild re-mints on every attempt".
A concurrent second `/start` attempt does NOT re-mint after the release; it has already
minted and is proceeding to `StartSession`.

FACT: the proposal states the correct rule for the resume path and does not carry it to the
bind path. non-spec-changes.md:701-704: `Binder.Resume` releases no lease because
"returning them on a retryable failure would leave every later resume of that session
running with leases the gateway has already released." That is the same hazard, same key.

FACT (production reachability of two concurrent attempts at one session): the proposal's own
out-of-scope row records it — "two concurrent calls both reach `BindReservedSlot` on the same
pod with the same slot identifier" (summary.md:366-374). The gateway's OWN §5.2 retry is not
a producer: `applySlotRetryPolicy` re-enters `BindSlot` only after `materializeSlot` returned,
so the release is already done. The producer is a client-driven second `/start` (or a second
replica), which is exactly the retry the compensation's detached context exists for.

FACT: `ensureSlotStateLocked` has exactly FIVE production callers, matching CODE-6's resolve
table: staging.go:134,:181,:337 (via `ensureSlotPaths`), slotcreds.go:26, slotsession.go:75.
`ExportPaths`, `Attach`, `Interrupt`, `SendMessage`, `Checkpoint`, the coordination RPCs and
the three credential-lifecycle RPCs reach it through none of them, so the hold refusal cannot
leak into those handlers. EVIDENCE: `grep -rn "ensureSlotStateLocked\|ensureSlotPaths" pkg/`.

FACT: `ensureSlotPaths` widening its return breaks four TEST call sites, and one of them,
`pkg/adapter/exportpaths_test.go:25,:221,:225`, is in NO file list in the proposal
(`slotsession_test.go:319` and `export_test.go:62` are listed). Not filed here: it is a
build-adaptation/bookkeeping item of the class this loop has refuted five times on
materiality, and it is the applicability lens's, not mine. A fixer opening the files list
should add it.

FACT: the §10.1.4 hold path does not leak a hold. `onHoldTimeout` (holdstate.go:189-205)
runs pass 2 as an unconditional `for _, m := range members` with no early break, and
`terminateHeldSession` defers each member's release, so a shared-context expiry does not
strand a hold. The N-member serial pass does mean member N's identifier is held across
every earlier member's close, which the staged §5.2 bound ("the termination window of the
pass that runs the cleanup") does cover. Do not re-derive.

FACT: an acknowledged reclaim releases its hold BEFORE the gateway sees the answer, because
`defer release()` runs during the handler's return and ahead of the response marshal. That
is what makes the Trap "an acknowledged compensation releases the hold before the gateway
returns the error" true against the staged code rather than only against the prose.

UNVERIFIED: `s.reportSessionScrub(ctx, sessionID, closeErr)` runs inside the hold (it sits
above `defer release()` in CODE-1's body), so the hold spans an outbound gateway RPC that no
staged sentence names among its bounds. Judged harmless because the hold is keyed on the
ending session's own identifier, so nothing else can collide with it. Somebody adjudicating
open decision 12 may want to know the hold's real extent includes the scrub report.
### [non-spec.1.review-reliability.1]

DECISION: returned zero findings — BECAUSE every recovery-path defect I could derive is
already stated in the proposal's own `## Edge cases and accepted failure modes`, in the
summary's open decisions 15-18, or on the standing refuted/open lists — ALTERNATIVES: I
came closest to filing (a) the §15.4 hold-conformance clause ("an adapter that refuses one
with a permanent status ... does not conform") against CODE-6 siting the refusal only in
`ensureSlotStateLocked`, and (b) the `compensateFailedSlotBind(req SlotBindRequest)` vs.
`Binder.Resume`'s `ResumeRequest` signature mismatch. Both are already recorded (see the
two FACTs below), so filing either would have burned two verifiers on a known item.

FACT: the compensation's two-bound split actually works against the shipped adapter, which
I verified end to end rather than trusting the staging. `contextWithGraceDeadline(parent,
grace)` returns `context.WithTimeout(parent, grace)` for a positive grace and the parent
unchanged for a non-positive one, and the runtime's own SIGTERM pivot
(`resolveShutdownGrace`) prefers that ctx's remaining time over the configured grace and
the package default. So `ShutdownReclaim(rctx=budget, …, deadline=budget/2)` really does
give `Runtime.Close` budget/2 and leave budget/2 of RPC deadline for the uninterruptible
`removeSlotTree`. EVIDENCE: pkg/adapter/session.go:327-332; pkg/adapter/mcpruntime.go:308-323;
pkg/adapter/socketruntime.go:457.

FACT: `SlotBindError.Reason()` switches on the gRPC code alone; the only stage refinement is
for `FailedPrecondition` (workspace-prep stays transient). So `codes.Aborted` classifies
transient from EVERY stage, including `credential_assignment` and `setup`, and the staged
test rows (start-stage plus workspace-stage `Aborted`) are sufficient rather than partial.
A reviewer tempted to file "the credential stage's Aborted row is missing" should stop here.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:84-101.

FACT: the credential stage's adapter error is NOT wrapped in `*CredentialAssignmentError`.
`assignSlotCredentials` returns that typed error only for a lease-minting failure
(`AssignProto`/`MintProto`); the `cl.AssignCredentials` RPC error returns bare and is wrapped
by `materializeSlot` as a plain `*SlotBindError` at the `credential_assignment` stage. So the
hold refusal on the credential path does not detour through start.go:89/:3653's typed
handling. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:369-403, :308-313.

FACT: the §10.1.4 hold-termination pass cannot leak a reclaim hold. `deregisterStartedSessions`
builds `members` only from entries it actually removed, and `onHoldTimeout` runs
`terminateHeldSession` for every member in a plain loop with a shared 10s close context;
`terminateHeldSession` holds no `s.mu` when it runs, so a `defer release()` there can take the
lock without deadlocking. EVIDENCE: pkg/adapter/slotsession.go:375-396;
pkg/adapter/holdstate.go:190-205, :229-270.

FACT: the hold refusal reaches exactly the seven bind-sequence RPCs, because
`ensureSlotStateLocked` has precisely three callers — `ensureSlotPaths` (the three workspace
RPCs), `assignCredentialsSlot`, and `claimSessionSlotUnderLock` (StartSession, Resume,
SDK-warm ConfigureWorkspace). Nothing else creates an entry. EVIDENCE: pkg/adapter/slot.go:105,
:143; pkg/adapter/slotcreds.go:26; pkg/adapter/slotsession.go:75.

WATCHOUT: every other entry-RESOLVING handler (`Checkpoint`, `Interrupt`, `Usage`,
`CoordinatorFence`) goes through `boundSlotState`/`checkSessionBound`, not
`ensureSlotStateLocked`, and answers one `codes.FailedPrecondition` for both the absent and
the unbound entry. During a reclaim hold there is no entry, so those answer a PERMANENT code
while §15.4's staged conformance clause says "an adapter that refuses one with a permanent
status ... does not conform". I did not file it: the loop already adjudicated this as a
membership move inside 0080 §1.19's existing refusal class rather than a new class, and the
summary's 0080 row was rewritten for it. Anyone re-deriving it must argue from a harm that
the "no entry exists, so the session is already gone" reading does not kill.
EVIDENCE: pkg/adapter/slotsession.go:274-283; pkg/adapter/coordination.go:116;
0081_....review-log.md:2469-2471.

USEFUL [review-log.md:661]: the standing OPEN on `compensateFailedSlotBind`'s
`req SlotBindRequest` parameter versus `Binder.Resume`'s `ResumeRequest` saved me a wasted
finding — five lenses have recorded it and none filed it, and the scalar remedy
(`accountSlotFailure`'s precedent at non-spec-changes.md:750-757) is already named there.

USEFUL [the refuted list]: six of the seven shapes I re-derived from the amendment text
(the one-sided fence, the shared-entry epoch inheritance, the reverse-ordering self-compare,
the §7.1/§5.2 leak-predicate split) were already refuted as stale against a pre-2aca9851b
snapshot. Diff against the r4 snapshot FIRST and grep the load-bearing quote before deriving
anything from remembered text.


### [non-spec.1.review-security.1]

DECISION: returned an empty findings list for the security lens on the joint spec + non-spec staging — BECAUSE every control the lens owns is untouched or moves conservatively, and each of the six candidates I built died on evidence (recorded below so nobody rebuilds them) — ALTERNATIVES: filing (a) `compensateFailedSlotBind`'s `default: return false` as a fail-open, (b) the §15.4 hold contract's "create or resolve" over-reaching the resolve-only RPCs, (c) the `emitFinalUsage` gate narrowing from `bound` to `started`, (d) a missing tier-9 arm for the reclaim hold, (e) the bind epoch as a pod self-report gating the `leaked` bound, (f) the §4.7 no-op sentence versus the mandatory recycle credential purge. Each is refuted, barred, or below the bar.

FACT: the r9→now delta touches ONLY `summary.md`, `implementation-checklist.md` and `review-log.md`. `spec-changes.md` and `non-spec-changes.md` are byte-identical to the spec-r9 snapshot. Do not spend a call re-diffing those two; the newest text in scope is the summary's two new "Defects in the shipped tree" rows (§10.1 generation fence, §5.2 Fresh workspace guarantee), the rewritten 0080 §1.19 impacts row, open decision 12, and the whole S1..S14 renumber. — EVIDENCE: `diff -ru scratchpad/cp-snap/0081_.../spec-r9 proposals/0081_...` lists three files.

FACT: this is the FIRST security-lens pass on the non-spec lane since the hand amendment. The four in-window security shards (`spec.2`, `spec.5`, `spec.6`, `spec.8`) all read the spec lane only; the non-spec security shards in `review-log-archive.md` (`non-spec.1`, `non-spec.2`, `non-spec-recheck.*`) predate the bind epoch and the reclaim hold entirely. A future compaction should not treat the archive's non-spec security verdict as covering CODE-6.

FACT: every SCHEMA-1 field number is genuinely free, verified by a brace-matching parse rather than by `awk` range. `AssignCredentialsResponse` really is `message AssignCredentialsResponse {}` on ONE line (`schemas/lenny-adapter.proto:1033`), so a naive `awk '/^message AssignCredentialsResponse \{/,/^\}/'` runs past it into `RotateCredentialsRequest` and reports a phantom collision on field 1. That is exactly the false Trap the standing context says eight lenses had to kill. Use a brace counter. Verified free: ShutdownRequest 7, ShutdownResponse 3, PrepareWorkspaceResponse 3, FinalizeWorkspaceResponse 2, ResumeResponse 4, ConfigureWorkspaceResponse 2, RunSetupResponse 2, AssignCredentialsResponse 1, StartSessionResponse 2. — EVIDENCE: schemas/lenny-adapter.proto:1021,1033

FACT: the §11.4 full-revoke fan-out sends the UNCONDITIONAL form and stays a working kill switch. `cmd/lenny-gateway/user_revocation.go:129` calls `bind.Adapter.Shutdown(callCtx, bind.SessionID, reason, userTerminateDeadline)`, and CODE-6 keeps `Shutdown`'s signature with a zero epoch, so no epoch mismatch can refuse a revoke. Same for `binder.go:2043` and `slotbinder.go:542`. — EVIDENCE: cmd/lenny-gateway/user_revocation.go:129; pkg/gateway/podlifecycle/podsession/binder.go:2043; slotbinder.go:542

FACT: the reclaim hold's `codes.Aborted` really does classify transient end to end, so the credential path cannot be bricked by it. `SlotBindError.Reason()` has no `Aborted` case and its `default` returns `SlotReasonTransient` (`pkg/gateway/podlifecycle/podsession/slotfailure.go:100-101`), and `assignSlotCredentials` returns `cl.AssignCredentials`'s status DIRECTLY rather than wrapping it in `CredentialAssignmentError` (`slotbinder.go:400`), so `slotErrCode`'s `errors.As` walk finds the Aborted. The `CredentialAssignmentError` wrap is only for the gateway-side mint failure, which never carries the adapter's status.

FACT: `emitFinalUsage` narrowing from `bound` to `started` in CODE-1 leaks no budget. It is explicitly best-effort with a stream-close fallback the §8.3 contract tolerates (`pkg/adapter/session.go:334-347`), and no bound-but-unstarted session reaches a `Shutdown` in the shipped tree anyway. I built and dropped this as a quota-bound finding.

FACT: `b.releaseCredentials` exists and `Binder.Resume` mints no lease, so CODE-4's split (compensation is pod-side, lease release is gateway-side and only in the `materializeSlot` wrapper) is grounded. `releaseCredentials` at `pkg/gateway/podlifecycle/podsession/binder.go:1263`; `Binder.Resume` (`:1590-1662`) calls neither `assignCredentials` nor `assignSlotCredentials`. The CODE-4 comment's `§7.1 step 23` citation is accurate: `spec/07_session-lifecycle.md:52` reads "23. Gateway: Release credential lease back to pool".

FACT: the credential-lease release on a failed bind IS tested, contrary to a first read of the tier-9 section. The assertion lives in the tier-1 gateway "per-stage compensation table" ("and the gateway-side credential leases released") and its negative twin in the resume case (`fakeAssigner.released` stays empty), not in `tests/tier9_security/`. Check both sections before filing a credential-lease coverage gap.

WATCHOUT: `compensateFailedSlotBind`'s switch ends `default: return false`, so an answer carrying `SLOT_RECLAIM_OUTCOME_UNSPECIFIED` with `exited_cleanly: false` maps to NOT leaked, where today's `leaked = err != nil || !cleanly` maps it to leaked. That looks like a fail-open against `slotbinder.go:533-535`'s stated "fail closed: on doubt the slot stays counted". It is not filable: CODE-1's handler sets the outcome on every return path (ABSENT/SUPERSEDED/RECLAIMED), so UNSPECIFIED is unreachable for the first-party adapter, and the only reacher is a hypothetical third-party adapter — a framing this loop's material skeptic has refuted three separate times. If a later round wants it, it needs a first-party reachability argument. — EVIDENCE: non-spec-changes.md:558-586; pkg/gateway/podlifecycle/podsession/slotbinder.go:533-543

WATCHOUT: §15.4's hold contract says a request that would "create OR RESOLVE a registry entry" under a held identifier is refused with `ABORTED`, while CODE-6 puts the refusal only at the top of `ensureSlotStateLocked`. The resolve-only sites (`slotStateLocked` in `credentials.go:121`, `slotcreds.go:70,105,125,252`, `manifest.go:380`, `tracingcontext.go:47`, `slotsession.go:277,299`) find no entry during a hold and answer a PERMANENT status instead, which §15.4 makes non-conformant on its face. Do NOT file it as written: the evidence skeptic has already ruled that "create or resolve" is the change's own vocabulary for the BIND-SEQUENCE RPCs (`spec-changes.md:679`) and refuted a sibling finding on exactly that reading. Anyone reopening it must attack that scoping directly, and the natural remedy is a §15.4 wording tightening, which is spec-lane and disfavoured here.

WATCHOUT: `awk '/^message X \{/,/^\}/'` is unsafe on this proto because several messages are single-line `{}`. Two of the nine SCHEMA-1 targets are affected. This is the mechanical cause of the historical false collision Trap.

USEFUL [Standing context, changelog item (2), "the `AssignCredentialsResponse` hard field-number collision Trap ... was FALSE and dangerous"]: I reproduced the false positive within one tool call and would have filed it without that warning. Keep this entry.

USEFUL [Traps, "Do NOT file 'the §4.7 no-op sentence suppresses the occupancy-zero recycle scrub'"]: I built exactly this as a mandatory-credential-purge bypass (§5.2 scrub step 0 is the platform's fail-closed credential purge) before finding the bar. The trap's pointer to the row's UNEDITED tail is what killed it.

OPEN: nobody has checked whether the adapter's own `/healthz` `leaked_slots` count (spec/06_warm-pod-model.md:160 makes the gauge an ADAPTER health-metadata export) increments when the adapter withholds `ReportSessionScrub` on the pre-`running` path. `[spec.6.review-security.1]` raised this and it is still unanswered. If it does not, one of the two sources §6.2 names for the `ceil(maxConcurrentSessions/2)` drain trigger does not see these leaks. The staged text names the gateway-side path, so it is not a defect of this proposal, but an observability or conformance lens should settle it.
### [non-spec.1.review-security.1]

DECISION: returned zero findings. BECAUSE every security-shaped angle I derived either
resolved clean against the tree or landed inside a family this loop has already refuted
twice (the §13.1 credential-residue family, the maxSessionsPerPod residual-state family,
the pod-self-reported `leaked` bound). ALTERNATIVES: I held three candidates and dropped
each for the reason recorded below.

FACT: SCHEMA-1's nine field numbers are all genuinely free, verified message by message.
`ShutdownRequest` holds 1,2,3,5,6 with 4 reserved (schemas/lenny-adapter.proto:1627,:1634),
so 7 is next; `AssignCredentialsResponse` really is `{}` on one line
(schemas/lenny-adapter.proto:1033) — an `awk '/^message AssignCredentialsResponse/,/^\}/'`
sweep runs straight on into `RotateCredentialsRequest` and makes it look like a five-field
message. Do not re-derive this with a range awk. EVIDENCE: schemas/lenny-adapter.proto:1033.

FACT: the five-resolve-site table in CODE-6 is exact against the tree. `resolvePrepareStagingDir`
wraps `InvalidArgument` with no span stamp of its own (pkg/adapter/staging.go:133-137),
`FinalizeWorkspace` (:181-185) and `RunSetup` (:337-341) each stamp `tracing.CategoryPermanent`
beside the wrap, `claimSessionSlotUnderLock` wraps with no stamp (pkg/adapter/slotsession.go:75-79),
and `assignCredentialsSlot` wraps with no stamp (pkg/adapter/slotcreds.go:26-28). Five sites,
no sixth: `rotateCredentialsSlot` and `extendCredentialLeaseSlot` read `slotStateLocked`
directly and fail closed on a missing entry (pkg/adapter/slotcreds.go:70-72, :104-107), so the
hold never has to reach them.

FACT: the SDK-warm one-session-only refusal runs BEFORE `ensureSlotStateLocked`
(pkg/adapter/slotsession.go:65-73), so CODE-6 putting the hold refusal at the top of
`ensureSlotStateLocked` cannot reorder that control. EVIDENCE: pkg/adapter/slotsession.go:65.

FACT: the claim-register anchor SCHEMA-1 reuses is real and is the sibling's:
`AttachRequest.coordination_generation` sits at tests/claim-map.json:48-51 with
`"spec_anchor": "#2851-gateway-to-pod"` and `deferral_id: R16`, exactly as the proposal states.

MISTAKE (mine, nearly filed): "the epoch early-return skips clause three, so a `Shutdown`
carrying both an epoch and the recycle disposition would skip the §5.2 whole-pod scrub."
The shipped handler really does run clause three after clause two unconditionally
(pkg/adapter/session.go:283-289) and CODE-1's `return` on ABSENT/SUPERSEDED really does skip
it. It is unreachable: the only two production senders of the recycle disposition are
`ShutdownRecycle` call sites (pkg/gateway/podlifecycle/podsession/slotbinder.go:574,
binder.go:2037) and CODE-6 keeps `ShutdownRecycle` on a zero epoch. Hypothetical hardening,
not a defect. A future proposal that gives a fenced request a recycle disposition must
revisit it.

MISTAKE (mine, nearly filed): the caller-latch clear diverges between the lanes. Staged
§4.7.1 says a caller "holds none once it has itself issued an RPC on that connection that
removes the entry the epoch names: `DemoteSDK` ... and a `Shutdown` answering `reclaimed`
removes it" (spec-changes.md:681), while CODE-6 clears only on `DemoteSDK` and on a
`ShutdownReclaim` answering `RECLAIMED` (non-spec-changes.md:996), leaving a plain
`Shutdown`/`ShutdownRecycle` that removes the entry with the latch standing. The divergence
is real and its direction is fail-open (a stale latch makes the next compensation on that
connection answer `superseded` and leave residue unaccounted — the proposal names that harm
itself at non-spec-changes.md:1342-1345). It is inert today because no gateway connection
carries a bind sequence after an ordinary `Shutdown`: `BindResult.Adapter` is per attempt and
is closed at session end. I dropped it on the no-harm test rather than on the text.

MISTAKE (mine, nearly filed): a lagging `AssignCredentials` whose handler acquires `s.mu`
after the compensation's hold released re-creates the entry and writes
`/run/lenny/slots/{sessionId}/credentials.json` plus §4.9 timers for an abandoned session.
Reachable by the same route as the recorded `StartSession` variant, and the recorded
accepted-failure bullet names only the start (spec-changes.md:248-259). Dropped because both
grounds the loop already used to kill the §13.1 credential-residue family apply verbatim:
CODE-4's unconditional `b.releaseCredentials(req.SessionID)` has already revoked the lease
material, and the occupancy-zero whole-pod scrub sweeps `/run/lenny/slots/`.

UNVERIFIED: whether the reclaim hold's refusal on the credential-delivery path
(`assignCredentialsSlot` mid-cleanup) needs a tier-9 arm of its own. It is pinned at tier 1
(the five-resolve-site table) and at tier 7a, and the staged tier-9 file
(`tests/tier9_security/slot_credential_reclaim_fence_test.go`) carries only the two epoch
arms. `.claude/rules/test-coverage.md` routes "credential delivery" to tier 9. I judged this
nice-to-have rather than required coverage; a later security pass may disagree.

WATCHOUT: the pod-self-reported `leaked` bound is NOT a regression this proposal introduces
and must not be filed as one. Before the amendment the gateway derived `leaked` from
`exited_cleanly`, which the adapter reports; after it, from `slot_reclaim` plus
`exited_cleanly`, which the adapter also reports. The adapter's power to force "not leaked"
is identical on both sides of the change. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:536-559.


### [non-spec.1.review-test-coverage.1]

DECISION: filed exactly two findings, both about the MAPPING between the Testing section's tier headings and the checklist's per-step tier lists, rather than about missing test bodies — BECAUSE the Testing section itself is unusually complete (every deliverable's error, concurrent, boundary and fail-closed path has a named case), so the only test-coverage defects left are at the seams — ALTERNATIVES: rejected filing (a) the sdkwarm.go `_ = noteRuntimeStarted` site as untested (it is a discard whose refusal path the proposal argues no compensation can reach, and the argument is sound), (b) a tier-8 chaos obligation (the compensation's failure paths are already at tiers 1/4/7a and tier 8 is partition/fail-open, which this change does not touch), (c) any "add a coverage percentage" or nice-to-have case.

FACT: the checklist's own contract is "Each implementation step carries the tests for the tiers its line names", so a tier heading in `## Testing` that names a deliverable whose checklist step does not name that tier is a real gap, not a style point. EVIDENCE: implementation-checklist.md:3-4; the tier headings are at non-spec-changes.md:1172,1229,1314,1432,1453,1481,1488,1627,1643,1648 and the tier lines at implementation-checklist.md:7,9,11,13,15,17,19,21,23,25,27,29,31,33.

FACT: CONF-1 says "Four properties, one per part of the §15.4 contract" (non-spec-changes.md:1043) but SPEC-5 lands TWO §15.4 blocks, the bind-epoch contract and the slot-identifier reclaim-hold contract (spec-changes.md:705-711). The four properties are all bind-epoch. EVIDENCE: non-spec-changes.md:1043-1053; spec-changes.md:711.

WATCHOUT: the generated-proto package path in the staging is WRONG and this is NOT a test-coverage finding, so I did not file it — the tree has `pkg/proto/adapter/v1` (`pkg/proto/adapter/v1/lenny-adapter.pb.go`) and no `pkg/gen` directory at all, while the proposal names `pkg/gen/adapter/v1` at three sites. A citation/edit-site lens should file it. EVIDENCE: implementation-checklist.md:20, non-spec-changes.md:1060, non-spec-changes.md:1797; tree: `ls pkg/gen` returns nothing, `tests/tier10_conformance/recycle_scrub_conformance_test.go:46` imports `github.com/lennylabs/lenny/pkg/proto/adapter/v1`.

WATCHOUT: `docs/reference/adapter-contract.md:75` is still in no edit list of the proposal (grep for "adapter-contract" over the proposal directory hits the review log only). The review log records it as a DEFERRED that eleven lenses filed independently and that the non-spec loop owns. It is an edit-site finding, not a test one, and no tier-11 gate catches it. EVIDENCE: review-log.md:676, :2331, :2683; docs/reference/adapter-contract.md:75.

USEFUL [standing context, review-log.md:186]: "the harness's tier is the test's DIRECTORY rather than its infrastructure" saved me from filing a tier mismatch on the `pkg/adapter/...` tier-1 cases that start real servers.
### [non-spec.1.review-test-coverage.1]

DECISION: filed exactly two findings, both about test-harness REGISTRIES the staged test set
requires and the proposal names nowhere, and filed nothing about the per-behaviour test list
— BECAUSE walking every staged behaviour (epoch mint/report, three outcomes, hold refusal at
all five resolve sites, start confirmation and its ABA arm, the six-arm outcome mapping,
ExcludePods, the resume accounting caller, the new §6.2 edge, the credential fence) against
`## Testing` turned up a named concrete case for each, at the tier the change reaches, with
the error/concurrent/boundary arm rather than a happy path alone — ALTERNATIVES: (a) a tier-10
CONF-1 property for the §15.4 reclaim-hold block, rejected because tier 7a already pins the
wire-observable `codes.Aborted` refusal through production callers and the gap is a
tier-placement preference; (b) the deliberately excluded `exited_cleanly:false`-on-tree-removal
assertion, rejected because non-spec-changes.md:1306-1312 excludes it with a portability reason
and names the seam that would restore it; (c) the `ConfigureWorkspace` idempotent-repeat arm,
rejected as derivable from the "one entry, one epoch" case.

FACT: `tests/claim-map.json` is GENERATOR-PRODUCED and a tier-0 gate holds the two byte-for-byte
identical. `TestClaimRegisterIsReproducibleFromItsGenerator` runs `scripts/seed-claim-register.py`
and diffs the output against the committed file; its own diff reporter says a row in the file
with none in the generator "was added to the file with no row source behind it". I ran the
generator: it reproduces the committed register exactly today (76 rows). Hand-adding SCHEMA-1's
two rows therefore turns tier 0 red. The generator carries a hardcoded extras list of row dicts
(scripts/seed-claim-register.py:~170-250) beside the rows it parses out of
`gateway-runtime-comms.md` §7.1, so that list is where the two rows belong.
EVIDENCE: tests/tier0_static/claim_register_generator_test.go:20-33,:45,:76-83,:90;
scripts/seed-claim-register.py:1-32,:180-250

FACT: `validate-maps` is tier 0 and enforces PER-FILE spec-map membership for every `_test.go`
under tier 2 and above. I resolved the four new test files 0081 stages against
`tests/spec-map.json` mechanically: all four are orphans (no file entry, no mapped ancestor
directory). `tests/tier3_contract` and `tests/tier7a_load_local` are NOT mapped as bare
directories; `tests/tier4_integration` IS, which is why extending the two tier-4 files is free.
A new test FUNCTION in a listed file costs nothing.
EVIDENCE: cmd/lenny-test/cmd_validate.go:125-139,:716-793; tests/spec-map.json (mapped dirs
include tests/tier4_integration, tests/tier3_contract/adapter_checkpointbarrier, and no
tier7a/tier9/tier10 directory)

USEFUL [review-log.md:208, :278, :669, :674]: the standing context had already established the
`validate-maps` per-file rule and flagged the spec-map item as OPEN with the tier-7a case's
unnamed file beside it. The file is now named (non-spec-changes.md:1585) but the map entry is
still absent, so the OPEN is half-closed and I filed the remaining half.

WATCHOUT: the `## Files touched on application (non-spec)` list ends with a Tests bullet
(non-spec-changes.md:1850-1856) that reads as complete. It is the natural place to check for
`tests/spec-map.json` and `scripts/seed-claim-register.py`, and neither is there; a reader who
stops at that bullet will conclude the harness registries are handled.
EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:1793-1856

FACT: several Testing-section citations were spot-verified and resolve:
`TestSlotBindErrorReason_spec_5_2` at pkg/gateway/sessionserver/slotretry_test.go:383,
`TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` at :468,
`recordingShutdownAdapter` at pkg/gateway/podlifecycle/podsession/binder_test.go:1150-1180,
`fakeAssigner.released` at :302/:322, `generalSlotEdges` at
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36 with the positive
loop at :55 and the negative at :70. The `concurrentAdapter` fixture description at
non-spec-changes.md:1319-1326 matches pkg/gateway/podlifecycle/podsession/slotbinder_test.go:65-160.

UNVERIFIED: the tier-4 "datastore-crossing case" says the extended
`tests/tier4_integration/recycle_scrub_path_test.go` pool "carries a second placeable pod" and
asserts the §5.2 retry re-binds there. `recycleCluster` seeds exactly ONE idle Sandbox
(`sbx-r`, tests/tier4_integration/recycle_scrub_path_test.go:184-200) and the fixture wires a
`podsession.Binder` with no `sessionserver`, and `applySlotRetryPolicy` is unexported
(pkg/gateway/sessionserver/start.go:2807), so the "retry" there can only be a second
`BindSlot` the case issues itself with `ExcludePods` set. I did not file, because that reading
is workable, but whoever implements S12/S13 should confirm the case is written that way rather
than reaching for the retry policy from an external test package.

OPEN: SCHEMA-1's two claim rows also need a `note`/`surface` wording that survives the
generator's own sorting and JSON emission. Whoever adds them to `scripts/seed-claim-register.py`
must re-run it with `--out tests/claim-map.json` in the same commit rather than editing the
JSON, or the tier-0 gate stays red either way.

## Index and checklist reconciliation (post-spec-loop)

This pass rebuilt the summary's deliverable index from the staged spec and non-spec changes,
rewrote the checklist's spec-lane steps against the current SPEC ids, reconciled the non-spec
steps' `Depends on:`, discharged the deferred corrections whose remedy lands in the four files
it may edit, and carried the spec loop's open decisions into the summary. The staged spec ids
are unchanged (SPEC-1 through SPEC-4), so no non-spec step's `Depends on:` needed renumbering.
The deliverable index needed one change: CODE-1's line now names `pkg/adapter/runtimegeneration.go`
beside `pkg/adapter/session.go`, because the `runtimeHoldsLocked` accessor CODE-1 is the first
caller of lands there.

CORRECTS [DEFERRED, checklist step S1, "SPEC-1's sites are §4.1 and the §4.7 row only"]: appended
the §29.4 session-end step 13 sentence to S1's description. Tiers (0, 11) and `Depends on: —`
unchanged, as the entry directed.

CORRECTS [DEFERRED, checklist step S2, "§7.1's atomicity paragraph gains ... the no-retry rule,
and the racing-start rule"]: S2 rewritten to the round-4 truth. It now says §7.1 gains the
obligation as a paragraph of its own after the atomicity paragraph, covering the creation
finalize block, the §15.1 start transition and the §7.3 re-attach, with the obligation's
boundary, the `leaked` disposition, the exclusive-pod case and the tree hazard; that §7.2 takes
three edits (premise sentence deleted, one sentence added to step 2, step 3 replaced); that
§5.2's `**Max retries:**` bullet states the placement constraint; and that §7.3, §6.2's
mid-resume cancel bullet and §4.7.9 step 5 are pointer edits. No no-retry rule and no
racing-start rule are named.

CORRECTS [DEFERRED, checklist step S3, "§5.2's slot-cleanup bullet covers a bind abandoned before
its session reaches the runtime"]: S3 rewritten to the two anchors SPEC-3 actually lands, with
the range narrowed to a slot that has entered `receiving_uploads`, so it no longer directs an
implementor at the `slot_assigned` range the proposal withdrew.

CORRECTS [DEFERRED, checklist step S7, "the runtime teardown only for a session the shared runtime
process has been given"]: S7 rewritten to the three-clause delivery (slot release for any entry
removed, runtime teardown for a session whose start the adapter has admitted, cleanup-outcome
report only for a session the shared runtime process was given), and its reach now names the
`runtimeHoldsLocked` accessor in `pkg/adapter/runtimegeneration.go`.

CORRECTS [DEFERRED, checklist step S8, "reports the reclaim as leaked, and refuses the start when
it did not"]: S8 rewritten. The deleted report and the retired `errStartRaceReclaimed` sentinel
are gone from the line, the refusal is scoped to a reclaim that landed after the start's claim,
and the reverse ordering is named as an accepted failure mode rather than a deliverable. This is
also the checklist-visible half of the DEFERRED against non-spec-changes CODE-2; the residue
itself is recorded in the accepted-failure-modes list, so no further edit was owed there.

CORRECTS [DEFERRED, checklist step S10, "a bind whose reclaim was not acknowledged is not
retried"]: S10 now says the retry carries `ExcludePod` so `ClaimSlot` places it on a different
pod, and its tiers are 0, 1, 2, 4, the tier 2 being the envtest-backed placement-filter cases in
`pkg/gateway/podlifecycle/podclaim`.

CORRECTS [DEFERRED, summary.md, 0080 §1.19 impacts row]: the row now states three membership
cases rather than two. Added the create-time-reserved case, in which an unacknowledged reclaim
leaves a surviving entry that is present-and-unbound (workspace-preparation residue) or
present-and-bound (credential-assignment residue) for a session the gateway has abandoned, and
the placement consequence, in which `ExcludePod` keeps a §5.2-placed retry from fencing a pod
that may still hold the prior attempt's entry, scoped away from a §15.1 start onto a
create-time-reserved slot. The "what it must do" cell now names all three cases and the narrowed
class.

CORRECTS [DEFERRED, non-spec-changes.md:350-353, the blob-store clause]: the clause now reads that
a clean-exit answer adds nothing to the pod's persistent leak count, and states that the windowed
failure counter still records the failure and reaches the drain threshold on one windowed failure
at `maxConcurrentSessions: 2`. The claim that a blob-store outage is accounted transient is gone.
The same wording at spec-changes.md:98-102 is carried forward below, because that file is outside
this pass's edit set.

No action was owed on three deferred entries. The `docs/reference/error-catalog.md` entry records
that lines 129, 155 and 156 stay true under the placement-rule design and exists so a later round
does not re-file them. The "docs/, four sites that stay TRUE" entry does the same for
`docs/client-guide/session-lifecycle.md:416`, `docs/reference/adapter-contract.md:84`,
`docs/runtime-author-guide/index.md:186` and `docs/runtime-author-guide/lifecycle.md:69`. The
CODE-2 entry's own text records that round 2 took the second branch and put both orderings in the
accepted-failure-modes list, so what remains there is a residue the proposal accepts rather than a
false statement to repair.

OPEN: `docs/reference/adapter-contract.md:75`'s `Shutdown` row still states the shipped
one-teardown contract, which SPEC-1 and SPEC-3 falsify in three ways (the slot release runs for an
unbound entry, the runtime teardown and the usage flush run only for a started session, and a
pre-`running` reclaim reports no outcome), and the row carries no no-op clean-exit answer. The
same cause reaches `docs/reference/adapter-contract.md:81`,
`docs/operator-guide/security-principles.md:33`, `docs/reference/execution-modes.md:68` and
`docs/operator-guide/multi-tenancy.md:72`, each of which says the per-slot cleanup runs at each
session release and reports its outcome. Closing this needs a staged docs deliverable that does
not exist; it lands in the non-spec changes file as a second DOCS deliverable against
`docs/reference/adapter-contract.md` and those four sites.

OPEN: DOCS-1 adds only the `receiving_uploads → slot_cleanup` row, while
`docs/reference/state-machines.md:251` still scopes the `slot_cleanup → leaked` terminal the new
edge needs away from a single-session pod. Closing this needs DOCS-1 widened with a second staged
edit; it lands in the non-spec changes file under DOCS-1 and in
`docs/reference/state-machines.md`.

OPEN: `schemas/lenny-adapter.proto`'s `Shutdown` RPC comment and its `ReportSessionScrub` and
`SessionScrubOutcome` comments state the one-teardown contract and the every-release report, both
falsified by SPEC-1 and SPEC-3. Programme rule S-2 reserves the single proto edit to remediation
step R1b, so the correction lands in `schemas/lenny-adapter.proto` under that step rather than
here.

OPEN: the spec-changes file's "Spec files touched" list describes §7.2's edit as the preamble
premise clause and step 3 both replaced, when it is the premise sentence deleted, one sentence
added to step 2, and step 3 replaced; and it describes the §4.7 row edit as "(first sentence plus
one sentence)" when the replacement swaps two sentences for seven. Both land in
`0081_....spec-changes.md`, and the entry asks for a named set rather than a count so the trap
does not re-arm.

OPEN: the spec-changes file's Design section at :63-66 says a §15.1 start onto a create-time
reserved slot "is placed by neither mechanism" with only one mechanism named, round 3 having
deleted the second. The repair names both, §5.2's slot retry policy and the §7.3 re-attach's
whole-pod idle claim. It lands in `0081_....spec-changes.md`.

OPEN: the spec-changes file's Design section at :43-44 lists §7.3 and §6.2's mid-resume cancel edge
as the pointer edits and omits the SPEC-2 §7.2 snapshot-close edits, which also restate the
reclaim's ordering against the pod release. It lands in `0081_....spec-changes.md`.

OPEN: the spec-changes file's edge case "A client retry of the §15.1 start after a failed bind on a
create-time-reserved slot" says an unacknowledged reclaim leaves the adapter's entry surviving in
whatever state the failed stage left it. That is false for a `Shutdown` the adapter executed whose
response was lost, which the gateway still classifies unacknowledged. The one-clause repair says
the gateway cannot tell whether the entry survives, so the retry may find either. It lands in
`0081_....spec-changes.md`.

OPEN: the spec-changes file's first edge-case bullet at :98-102 still closes "so the failure is
accounted transient and a blob-store outage does not retire healthy pods", which the windowed
`RecordFailure` threshold falsifies. The non-spec half of this entry was repaired in this pass;
the spec half lands in `0081_....spec-changes.md`.

OPEN: `spec/06_warm-pod-model.md:156`'s `slot_cleanup ──→ released` annotation and its mirror at
`docs/reference/state-machines.md:234` gloss three actions, and SPEC-3 widens §5.2's action list to
five. The round-7 lens recorded it without filing, on the ground that an abbreviated annotation
states nothing false. Whoever edits DOCS-1's table stands next to the row; any repair lands in
`spec/06_warm-pod-model.md` and `docs/reference/state-machines.md`.

### [non-spec-recheck.1.fix-G1]

CORRECTS [f2.open-decisions.out-of-scope-defects.slot-assigned-terminal]: its FACT "CODE-5 does not widen the unmodelled transition ... `BindReservedSlot` swallows its own reservation-release error" was true of the shipped tree and false of the staged tree. The same proposal's CODE-4 retires that swallow: its call-site table gives `BindReservedSlot` the value "`sbe.Leaked`, and the method sets `sbe.Leaked = true` on the error it is about to return when its own release fails" (non-spec-changes.md, the CODE-4 `ReleaseSlotReservation` call-site table), and CODE-5's reserved branch then passes that discriminator into `accountSlotFailure`, whose body is today's `MarkLeaked` plus leak gauge plus `RecordLeak` arm. A create-time-reserved connect-stage failure whose own reservation release errors therefore reaches `MarkLeaked` on a slot in `slot_assigned`. The summary row, the SPEC-4 exclusion paragraph, the spec-changes connect-stage edge-case bullet and the non-spec connect-stage edge-case bullet now say the staged lane adds one more producer of the transition, and the scope call rests on the surviving ground: the hole predates the proposal and is widened rather than created, the transition raises no runtime error because `Registry.Assign` and `Registry.Transition` have no production caller and `MarkLeaked` bypasses the edge list, and closing it means an edge out of `slot_assigned` that SPEC-4 deliberately does not add.

DECISION: the disposition stays `out-of-scope-stands` and SPEC-4 still adds the `receiving_uploads → slot_cleanup` edge alone. ALTERNATIVES: adding an edge out of `slot_assigned` was rejected because it cascades into CODE-3, DOCS-1, `TestValidTransitions_spec_6_2`'s exact edge set and §6.2's two-block concurrency scoping, and it half-closes (the same `MarkLeaked` call produces an equally unmodelled `receiving_uploads → leaked`); narrowing CODE-4 so `BindReservedSlot` reports `Leaked` false at the connect stage was rejected because the release runs with `leaked=false`, so a failed release means the Redis decrement did not happen and the slot really is leaked.

FACT: the tier-4 fixture cannot observe a pod retirement. `tests/tier4_integration/recycle_scrub_path_test.go` runs envtest with no WarmPoolController and asserts the `lenny.dev/drain-request` annotation directly, and both its concurrent pools carry `MaxConcurrentSessions: 4` (:807, :1016) against `UnhealthyThreshold = (maxConcurrent+1)/2` (pkg/gateway/runtime/slothealth/slothealth.go:215-220), so one leak reaches 1 of 2 and drains nothing.

FACT: `bound → recycling` is unreachable on the failed-bind release path with either disposition. `Binder.ReleaseSlotReservation` calls `claimer.ReleaseSlot(ctx, sandboxName, false, false)` with `recycle` hard-coded (pkg/gateway/podlifecycle/podsession/slotbinder.go:495-503) and CODE-4 adds only `leaked`; `SlotClaimer.ReleaseSlot` reaches `WriteRecyclingStatus` only inside `if recycle {` (pkg/gateway/podlifecycle/podclaim/slotclaimer.go:850). The distinguishing counterfactual is the claim DELETE at occupancy zero (:881-885), not a recycle patch.

DEFERRED [nothing]. No staged deliverable is added, removed, merged, split or resequenced, so the implementation checklist is unchanged and every box stays unticked.

CORRECTIONS to this same pass, from the post-fix review of its own edits:

- The scope-accounting paragraph in non-spec-changes.md contradicted the CODE-2 call-site scope this pass also wrote. The accounting said CODE-2 breaks one shipped test and that "every other `noteRuntimeStarted` caller already holds a bound entry when it records", while the call-site scope says `adapterevents_test.go:95` and `:184` both build a bare `New("served")` with nothing in `s.slots` and both gain a `bindSessionForTest` call. The tree confirms the call-site scope: `pkg/adapter/adapterevents_test.go:182-184` inserts nothing into `s.slots` before `s.noteRuntimeStarted("sess-fin")`. The intended distinction was between a caller that goes red and a caller whose fixture changes without going red. The paragraph now states that CODE-2 changes both `adapterevents_test.go` fixtures, that only `TestAdapterEventsEmitsControlEvents_spec_4_7` goes red, and that `TestEmitFinalUsageOnShutdownPath_spec_4_7` stays green because it passes its session identifier to `emitFinalUsage` explicitly (`pkg/adapter/adapterevents_test.go:192`) and `emitFinalUsage` forwards it to `EmitFinalUsageReport` (`pkg/adapter/session.go:338-347`), so the `soleSession` fallback at `pkg/adapter/adapterevents.go:153-155` never runs for it.
- The `terminate`-frame assertion this pass added to the "Bound but unstarted" tier-1 case cited `pkg/adapter/session.go:242,259-261` for the `bound` gate. Line 242 is `closeErr := error(nil)`; the gate is `if bound {` at :243, and `if !boundRemains {` is at :259. The claim is unchanged and the citation now reads `pkg/adapter/session.go:243,259-261`, which agrees with the problem statement's own `pkg/adapter/session.go:243-282` anchor for the same branch.

No staged deliverable changes in either correction, so the implementation checklist is unchanged and every box stays unticked.

- Deleting the tier-7a case's "closed exactly once" clause left the co-tenanted variant justified by a close-branch property nothing asserted. The two sentences that justified two variants ("only a pod whose shared runtime holds no other started session exercises the branch where the rollback close is the last close", and the closing "What the co-tenanted variant discriminates is what the rollback close did") now name the property the case does assert, the pod-level `runtimeLive` cohort and `runtimeIdleLocked` outcome, and the co-tenant is kept started because `noteRuntimeStarted` is the only writer of `runtimeLive` (`pkg/adapter/runtimegeneration.go:26-49`), so an unstarted co-tenant would make the two variants identical. The close effect is not restated as an assertion, because the park is the `gatedRuntime` fake (`tests/tier7a_load_local/podmcp_arming_handoff_test.go:93-101`), whose `Close` parks and returns and holds no connection, child or listener, so `SocketRuntimeProcess`'s teardown is unobservable at tier 7a whichever fixture the case swaps in: a real `SocketRuntimeProcess` parks a first `Start` inside `accept` with `connected` still false, so its rollback `Close` early-returns before the last-close branch. The paragraph now says the case asserts nothing about that effect and names where it is pinned instead: the shipped `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` (`pkg/adapter/socketruntime_test.go:252-300`) covers the sibling-active early return and the last close on the real process, and this proposal's tier-1 "Co-tenancy hazard" case covers the connected-with-empty-active-set state. This also retires the standing UNVERIFIED at `[non-spec-recheck.2.fix-design-G1.1]`'s ground that the co-tenanted variant is the only thing exercising `SocketRuntimeProcess.Close`'s active-set early return; the shipped unit test exercises it deterministically.

No staged deliverable changes in this correction either, so the implementation checklist is unchanged and every box stays unticked.

- The "rollback destroys no successor" tier-1 case closed with "Either arm turns red if a registry release is added back to the rollback", which its own bound sub-case refutes two sentences earlier. CODE-2's guard is `if !ok || st.sessionID != sessionID { return false }` and the rollback body runs only when `noteRuntimeStarted` returns false. In the bound sub-case the successor's `claimSessionSlot` sets `st.sessionID = sessionID` under the same key (`pkg/adapter/slotsession.go:87`, and `SlotID == SessionID` makes the successor the same session), so the confirmation succeeds and the body never executes; a re-added `releaseSessionSlot` cannot run there. Only the unbound arm discriminates, where `ensureSlotStateLocked` leaves `st.sessionID` empty (`pkg/adapter/slot.go:105-125`, reached from `ensureSlotPaths` at `:135-148`), the guard refuses, and a re-added release would `delete(s.slots, sessionID)` and `RemoveAll` the tree (`pkg/adapter/slotsession.go:214-220`). The closing sentence now names the unbound arm as the guard and the bound arm as the recorded reverse ordering.
- The "A rolled-back start can close a successor's runtime session" accepted failure mode bounded its window to "the branch on which `ExcludePod` does not fire", which the proposal's own create-time-reserved carve-out refutes. `bindConcurrentSlot` routes a row with a `PodAssignment` through `BindReservedSlot` and never enters `runWithQueue` or `applySlotRetryPolicy` (`pkg/gateway/sessionserver/start.go:2594-2605`), so the §5.2 placement constraint does not reach a §15.1 retry onto a create-time-reserved slot at any disposition, and the retried start reconnects to the same pod under the same slot identifier. The bullet now names both open branches: the policy-placed retry after an acknowledged reclaim with a clean release, and the create-time-reserved retry the policy never places, which SPEC-2's edge-case list already records.
- The placement exclusion was staged as a single-valued `ExcludePod string` whose one set site assigns rather than accumulates, which the same pass's pointer change made observable. On an `onPoolExhausted: "queue"` pool, `waitInQueue` re-enters the closure with the retry budget starting over (`maxSlotRetries = 1` at `pkg/gateway/sessionserver/start.go:2720`, the loop at `:2809`, the re-entry at `pkg/gateway/sessionserver/queue.go:143-146`, `:205`), so a second leaked failure on a second pod would overwrite the first name and free the retry to be placed back on a pod whose reclaim the adapter never acknowledged. The staged §5.2 sentence is universal over such pods, so the field is now `ExcludePods []string`, appended at the same single site, skipped by the same two `continue`s in `ClaimSlot`, with a tier-1 arm driving two leaked failures across a queue re-entry and a tier-2 arm asserting a two-entry list skips both pods. The Settled entries at `applySlotRetryPolicy takes req by value` and `applySlotRetryPolicy wraps binder.BindSlot only` are corrected in place, because the second stated that a single-valued field is sufficient.

No staged deliverable is added, removed, merged, split or resequenced by these three corrections. CODE-5 keeps its one new request field and the implementation checklist is unchanged, with every box unticked.

- CODE-5's "Where it is read" bullet attributed both `classifySlotBindFailure` production call sites to `bindConcurrentSlot`'s own value local. Only `pkg/gateway/sessionserver/start.go:2602` sits in `bindConcurrentSlot` (declared at `:2594`, taking `slotReq` as a by-value parameter). Line 2172 sits in `claimAtCreate` (declared at `:2085`), inside its `route == claimRouteStart` arm, and the request it passes is that function's own local, built at `:2140` by `s.slotBindRequest(...)` for the create-time reservation path; `bindConcurrentSlot` is not on that call path. The underlying claim is unchanged, because both callers pass a value and neither needs an edit, but the create-time-reserved path is the one this pass's `Aborted` reasoning also turns on, so the misattribution pointed an implementor at the wrong function. The bullet now names the two arms separately with their own request locals.

No staged deliverable changes in this correction, so the implementation checklist is unchanged and every box stays unticked.

### [spec.1.fix-bind-epoch]

CORRECTIONS to the bind-epoch pass of spec loop round 1, from the post-fix review of that
pass's own edits. The pass left no shard of its own in this file, so its corrections open
this subsection rather than extending one.

- The rewritten §4.7.1 and §15.4 blocks defined the epoch as belonging to "the bind attempt that owns a slot identifier's occupancy", and the same round's G2 rename removed every definition of a slot identifier's occupancy: §5.2's appended paragraph is now `**Slot-identifier reclaim hold.**` and speaks of the adapter holding the identifier (spec-changes.md:552), §6.2's staged sentence says "the adapter holds the slot's identifier" (:614), and §15.4's companion block is `**Slot-identifier reclaim hold:**` (:662). In the surrounding specification "occupancy" is the pod's slot-counter occupancy (`spec/05_runtime-registry-and-pool-model.md:488`; spec-changes.md:548), so a third-party adapter author was told the epoch names the attempt that owns a pod-level quantity rather than the registry entry the epoch keys on. All four sites now read "owns a slot identifier": spec-changes.md:106 and :132 in the Design section, :630 in SPEC-5's §4.7.1 block, and :654 in SPEC-5's §15.4 bind-epoch contract. summary.md:44 restates the same sentence and is corrected with them.
- CODE-5's section heading (non-spec-changes.md:709) and the CODE-5 deliverable index line (summary.md:540) still gave the placement-exclusion trigger as a reclaim that went unacknowledged, after the round broadened that predicate wherever it is stated as a rule: §5.2's `**Max retries:**` bullet now reads "reclaim for this session did not complete ... such an incomplete reclaim" (spec-changes.md:484), the §7.1 paragraph follows it (:344), and the staged discriminator is `sbe.Leaked || relErr != nil` (non-spec-changes.md:751-752, :804), which fires on a reclaim answered `reclaimed` without a clean exit as well as on an unanswered one. The heading now reads "a retry skips the pod whose reclaim did not complete" and the index line "the retry after an incomplete reclaim carries `ExcludePods`". The narrative and test-case sites that describe an unacknowledged reclaim as one member of the class are unchanged, because each states a case rather than the rule.

No staged deliverable is added, removed, merged, split or resequenced by these two corrections, so the implementation checklist is unchanged and every box stays unticked.

DEFERRED [non-spec-changes.md SCHEMA-1 and CODE-6, the request side of the bind epoch]: SPEC-5 states that each bind-sequence request may carry a bind epoch and that "a request carrying an epoch is admitted only when the entry the adapter holds for the identifier carries that epoch" (spec-changes.md:632, :658), and §15.4 publishes the same with a non-conformance clause (:656). SCHEMA-1 stages `ShutdownRequest.expected_bind_epoch` and `bind_epoch` on seven responses and no bind-sequence request field (non-spec-changes.md:1026-1036), and CODE-6 stages no handler that reads one off a request and no client that sends one on a bind-sequence request (:922-924, :954-966), so the admission rule the specification now mandates has neither a wire field nor a reader. The remedy is a non-spec-lane edit, which the spec lane may not author. It is: seven request-side `int64 bind_epoch` fields, whose free numbers were checked against `schemas/lenny-adapter.proto` in this pass — `PrepareWorkspaceRequest` 5, `FinalizeWorkspaceRequest` 6, `RunSetupRequest` 5, `AssignCredentialsRequest` 4, `StartSessionRequest` 12, `ConfigureWorkspaceRequest` 5, `ResumeRequest` 16, each the lowest number free of a used and a reserved number on its own message; the counts at non-spec-changes.md:90, :995 and :1648 and at summary.md:535, which read "nine fields"; CODE-6 naming where each of the seven handlers reads the request epoch and threads it into `ensureSlotStateLocked`, `claimSessionSlotUnderLock` and `assignCredentialsSlot`, and where `adapterclient` sets the latched epoch on every bind-sequence request; a tier-3 case asserting the echo, which the current list (:1365-1377) does not carry; and the CODE-6 index line at summary.md:541.

DEFERRED [implementation-checklist.md, five steps naming rules this round retired]: S5 (`:14`) names "the published bind-epoch contract and the slot-identifier occupancy contract", where §15.4's second block is now `**Slot-identifier reclaim hold:**` (spec-changes.md:662). S9 (`:22`) says `ensureSlotStateLocked` "gains the hold refusal and the epoch mint", where it now also compares the caller's epoch (non-spec-changes.md:922-924). S11 (`:26`) enumerates the two orderings CODE-2 was rewritten away from: the admission rules refuse a superseded attempt and CODE-2's confirmation now covers only the window between the claim and the runtime record (non-spec-changes.md:42-47, summary.md:58-62). S13 (`:30`) gives the exclusion trigger as an unacknowledged reclaim, corrected above everywhere it states the rule. S14 (`:32`) names two CONF-1 properties that the rewrite replaced with "minted per admitted bind attempt" and the echo and refusal arms (non-spec-changes.md:979-982). The checklist is outside this pass's edit set and is the artifact an implementor executes, so each step must be brought to the round's text before any step is run.

CORRECTIONS to the same pass, from the post-fix review of round 2. These extend this
subsection rather than opening a new one, because each corrects an edit made above.

- The pass made the demotion drop normative in §4.7.1 ("holds none once it has itself issued an RPC that removes the entry that epoch names: `DemoteSDK` ... removes the entry", spec-changes.md:641) and left the deliverable that implements it stating a set-only latch. CODE-6's client paragraph now clears the latch to zero when `DemoteSDK` returns without error, and its carrier rationale no longer claims the latch's lifetime is the attempt's lifetime, because `DemoteSDK` clears it mid-attempt on the same connection (non-spec-changes.md, the `pkg/gateway/runtime/adapterclient/client.go` paragraph and the paragraph after it). The entry removal is real in the tree: `pkg/adapter/sdkwarm.go:296-298` calls `noteRuntimeClosed` and `releaseSessionSlot` on the demote path. CODE-4's staged `compensateFailedSlotBind` comment, which read "the bind epoch this attempt observed", now reads "holds" and names the cleared latch as the second source of a zero. summary.md's gateway bullet and its CODE-6 index line carry the clear.
- The Design narrative still keyed the caller echo rule on what the caller observed (spec-changes.md:113-117), which forbids the epochless form for exactly the caller the §4.7.1 rule now says holds none. It is re-keyed to what the caller holds and names the drop, mirroring :641.
- SPEC-2's staged §7.1 reclaim rule keyed the compensating `Shutdown`'s epoch on "the most recent bind epoch ... that attempt observed on a response of its own, and it may name no other" (spec-changes.md:347), which after the §4.7.1 rewrite contradicts it: a bind that demotes and then fails before the pod-warm sequence answers would be obliged to send the pre-demotion epoch, which the adapter answers `superseded` while the residue stands. The sentence now reads "the bind epoch that attempt holds", cites §4.7.1 by its own heading anchor rather than §4.7, and states both reasons an attempt holds none. The demotion path it turns on is spec-mandated at `spec/04_system-components.md:673`.

No staged deliverable is added, removed, merged, split or resequenced by these three corrections, so the implementation checklist gains no step and every box stays unticked.

DEFERRED [implementation-checklist.md S4, a citation and a dependency the §6.2 rewrite falsified]: the pass added a second forward citation to SPEC-4's staged §6.2 paragraph, "[§4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states which bind requests the adapter admits onto a slot's identifier" (spec-changes.md:617). §4.7.1 carries no admission rule today (`spec/04_system-components.md:659` is the heading; `grep -rn "bind epoch\|bind_epoch" spec/` returns nothing), and SPEC-5 lands it at S5, which runs after S4. S4 (`:12-13`) still describes the paragraph as citing only "the §5.2 sentence S3 lands" and still reads "Depends on: S3". The remedy is to name both citations in S4 and either add S5 to its dependency list or resequence SPEC-5 ahead of SPEC-4. The same ordering holds for SPEC-3: its staged §5.2 reclaim-hold paragraph closes with "[Section 4.7.1] states which bind attempt owns a slot identifier ... and which bind requests the adapter admits onto it" (spec-changes.md:559), while S3 (`:10`) declares no dependency at all, so a resequence that puts SPEC-5 first discharges both. The checklist is outside this pass's edit set.

DEFERRED [implementation-checklist.md S9, the latch clear]: S9 (`:22`) gives the client work as "the adapter client gains the epoch latch, `BindEpoch`, and `ShutdownReclaim`", which no longer names everything CODE-6 stages: the latch is also cleared when `DemoteSDK` returns without error, which is what makes the §4.7.1 caller rule at spec-changes.md:641 implementable. The step needs the clear named alongside the latch. The checklist is outside this pass's edit set.

CORRECTIONS to the same pass, from the post-fix review of round 5. These extend this
subsection rather than opening a new one, because each corrects an edit made above.

- CODE-6's rationale for putting the epoch latch on the connection still read that the connection is "dialled once per bind attempt and closed with that attempt", which the round's own re-scoping of the caller rule withdrew: §4.7.1 now has the caller echo the epoch "on every later bind-sequence request it sends for that session on the connection the epoch was reported on", the [Section 7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload included (spec-changes.md, SPEC-5's §4.7.1 caller rules), so the latch has to outlive the attempt. The premise is also false against the tree: `BindResult.Adapter` is documented as "the live connection to the pod's adapter. The caller owns it and closes it when the session ends" (`pkg/gateway/podlifecycle/podsession/binder.go`, `BindResult`), `slotbinder.go` sets it from the client it dialled, and `pkg/gateway/sessionserver/upload_to_session.go` reaches `bind.Adapter.PrepareWorkspace` and `bind.Adapter.FinalizeWorkspace` on it for the mid-session upload. The rationale now states the retained-for-the-session lifetime and grounds the isolation on a further attempt dialling its own connection. SCHEMA-1's verification-set paragraph, which still read "the attempt's later bind-sequence requests", now reads "the later bind-sequence requests the session sends on that connection".
- The §7.4 mid-session sentences the pass added to §4.7.1 and §15.4 rest on a bind-sequence request carrying the epoch, and nothing in the staged files said so where a reader of the spec staging would meet it. SPEC-5's closing rationale now states that both blocks put the epoch on the request side as well as the response side, that the wire edit landing them carries the field on each of the seven bind-sequence request messages, and that the mid-session upload rests on that field, because with every bind-sequence request epochless the first admission rule refuses one onto an identifier whose session has started. The spec text is unchanged: the two admission rules and the echo rule are the contract, and the wire staging owes the fields the DEFERRED line below enumerates. Exempting the §7.4 pair from the admission rules instead was considered and rejected: the exemption would oblige the adapter to tell a mid-session upload from a bind stage, which it cannot do on the wire (the pair is the same two RPCs), and §4.7.1 states it needs no way to.

No staged deliverable is added, removed, merged, split or resequenced by these two corrections, so the implementation checklist is unchanged and every box stays unticked.

DEFERRED [non-spec-changes.md SCHEMA-1 and CODE-6, the §7.4 mid-session echo]: this extends the request-side DEFERRED line above rather than replacing it. The seven request-side `int64 bind_epoch` fields that line enumerates are also what makes the §7.4 mid-session upload admissible: `pkg/gateway/sessionserver/upload_to_session.go` calls `PrepareWorkspace` and `FinalizeWorkspace` on `bind.Adapter` long after the bind attempt returned, and §15.4 as staged makes it a non-conformance to admit an epochless request onto an identifier whose session has started. Until the fields and the client-side echo land, a conforming adapter refuses every mid-session upload. The remedy therefore also covers the mid-session call sites in `adapterclient` and a tier-3 or tier-10 case asserting a mid-session `PrepareWorkspace` echoing the current epoch is admitted and does not re-mint, beside the seven fields, the counts at non-spec-changes.md and summary.md that read "nine fields", and CONF-1's second property, which states the epochless refusal without stating the mid-session admission. The remedy is a non-spec-lane edit, which the spec lane may not author.

CORRECTIONS to the same pass, from the post-fix review of round 6. These extend this
subsection rather than opening a new one, because they correct an edit made above.

- The pass's replacement clause for the give-up bound asserted a flat disposition, "that is the unanswered reclaim §7.1 already accounts leaked", in two places written this round: the staged `compensateFailedSlotBind` doc comment (non-spec-changes.md) and the give-up-bound bullet (summary.md). SPEC-2's staged §7.1 paragraph accounts an unanswered reclaim `leaked` only on a pod serving concurrent sessions, and states that on a pod serving one session "the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there", the pod retiring under the §6.2 pre-attached failure disposition instead (spec-changes.md, SPEC-2's §7.1 block). The proposal's own edge case is headed "A reclaim the adapter does not acknowledge on a pod serving concurrent sessions". `compensateFailedSlotBind` runs on pods of either concurrency, so the clause stated a disposition §7.1 denies for half its callers. Both sites now read that the unanswered reclaim is the one §7.1 accounts, "which leaves the slot leaked on a pod serving concurrent sessions and retires the pod on a pod serving one", in identical wording so the two read as one statement. No spec text changed; the two sites were the defect.

No staged deliverable is added, removed, merged, split or resequenced by this correction, so the implementation checklist is unchanged and every box stays unticked.

CORRECTIONS to the same pass, from the post-fix review of the resumed loop's round 1. These
extend this subsection rather than opening a new one, because they correct an edit made
above.

- The pass re-keyed the no-re-dial clause in both staged blocks onto the latch, so that a caller dialling a fresh connection holds no epoch on it and sends the unconditional teardown (spec-changes.md:390 in SPEC-2's §7.1 block and :681 in SPEC-5's §4.7.1 caller rules), and left the commentary paragraph that justifies the rule asserting the contrary, that "a compensation that dialled a fresh connection would still hold the epoch it observed" (spec-changes.md:398-399). That paragraph's stated job is to say why the rule is normative, so an implementor was told the fresh-connection case is fenced and the rule covers only an attempt that observed nothing. The paragraph now states the latch formulation the rest of the proposal carries: the epoch is minted for a registry entry inside one adapter process, a caller latches it off the responses one connection reported to it, and a compensation that dialled a fresh connection would hold none on that connection and would send the unconditional teardown at a pod a successor may already hold. The other lanes already read that way and are untouched (summary.md:180-184; non-spec-changes.md:545-548 and the latch-lifetime paragraph at :1013).

No staged deliverable is added, removed, merged, split or resequenced by this correction, so
the implementation checklist is unchanged and every box stays unticked.

## Index and checklist reconciliation (post-spec-loop, second pass)

This pass rebuilt the summary's deliverable index from the staged spec and non-spec changes,
rewrote the checklist's spec-lane steps against the current SPEC ids, reconciled the non-spec
steps' `Depends on:`, discharged the deferred corrections whose remedy lands in the four files
it may edit, and carried the spec loop's open decisions into the summary. The staged deliverable
set is SPEC-1 through SPEC-5, CODE-1 through CODE-6, CONF-1, SCHEMA-1 and DOCS-1, each named in
exactly one step.

The spec-lane block is resequenced. SPEC-5 now leads at S1, because the §4.7 `Shutdown` row
(SPEC-1), §5.2's reclaim-hold paragraph (SPEC-3) and §7.1's obligation (SPEC-2) each point at
the §4.7.1 bind-epoch block, and SPEC-4's paragraph no longer cites §4.7.1 at all. The order is
SPEC-5, SPEC-1, SPEC-2, SPEC-3, SPEC-4 as S1 through S5, and every non-spec step's `Depends on:`
was rewritten onto the new ids: S8 now depends on S1 and S2, S9 on S1 and S8, S10 on S2, S4 and
S9, S11 on S3 and S10, S12 on S3, S4, S10 and S11, and S14 on S1 and S10. S6 and S7 depend on
S5, which is SPEC-4 under either numbering, and S13 still depends on S12.

The deliverable index needed two changes. CODE-2's line now names `pkg/adapter/sdkwarm.go`,
which carries the `noteRuntimeStarted` call site that deliverable edits, and CODE-6's line names
it too, for the epoch on the `ConfigureWorkspace` response. No other index line's files or
description diverged from the staging.

CORRECTS [DEFERRED, implementation-checklist.md, "S10 ... says 'One `accountSlotFailure` helper
serves both concurrent bind paths, the create-time reserved path reaches the §5.2 threshold'"]:
that step is S13 under the new numbering and now reads that the helper serves every bind path
the §7.1 obligation binds, naming `applySlotRetryPolicy`, `bindConcurrentSlot`'s reserved branch
and `resumeOnPod`'s `podBinder.Resume` failure branch, so the create-time reserved path and the
§7.3 re-attach both reach the §5.2 threshold. The line also carries the helper's narrowed
request-derived parameters, `pool string, maxConcurrentSessions int32`, and gives the exclusion
trigger as an incomplete reclaim rather than an unacknowledged one, which is the broadening the
`[spec.1.fix-bind-epoch]` correction landed everywhere the rule is stated.

CORRECTS [DEFERRED, implementation-checklist.md, "S9 ... describes CODE-4 as carrying the outcome
'as the `leaked` disposition through `ReleaseSlotReservation`'"]: that step is S12 under the new
numbering and now also names the `*SlotBindError` that `Binder.Resume`'s failure branch returns
in its error chain, and says it is what S13's third caller reads. An implementor landing S12
from the checklist alone no longer leaves S13 with nothing to read.

CORRECTS [DEFERRED, implementation-checklist.md S4, "a citation and a dependency the §6.2 rewrite
falsified"]: the SPEC-4 half is closed by the staging itself, because the §6.2 paragraph now
cites §5.2 alone and carries no §4.7.1 pointer. The SPEC-3 half is closed by the resequence the
entry recommended: SPEC-3's reclaim-hold paragraph closes with a pointer at §4.7.1, and SPEC-3
is now S4 with `Depends on: S1`, the step that lands §4.7.1. The one citation the order cannot
put first is SPEC-5's own pointer at §5.2's reclaim hold, which S1's line states so the residual
is visible rather than silent.

CORRECTS [DEFERRED, implementation-checklist.md, "five steps naming rules this round retired"]:
S13 is repaired above. The other four are already true against the reverted per-entry staging
and needed no edit: S5's step, now S1, names the `**Slot-identifier reclaim hold:**` block rather
than an occupancy contract; S9 gives `ensureSlotStateLocked` the hold refusal and the mint on its
entry-creating branch alone, with no caller-epoch comparison, which is what the r6 revert left;
S11's two orderings are the two states CODE-2's staged doc comment refuses, the ABA case
included; and S14's four CONF-1 properties match the four the deliverable now stages, with the
per-entry mint right again.

CORRECTS [DEFERRED, implementation-checklist.md S9, "the latch clear"]: already closed. S9 reads
"the per-connection epoch latch with its clear on `DemoteSDK` and on a `RECLAIMED` reclaim",
which is what CODE-6 stages.

CORRECTS [DEFERRED, summary.md, "the open-decisions preamble"]: the preamble no longer says the
bind epoch and the reclaim hold close entry 9 outright. It now says they close the released-entry
ordering and that the shared-entry ordering is accepted and recorded under `## Decisions` with
what closing it would cost, which is what the staged edge cases say after the r6 revert. The
present-tense citations of "open decision 9" the entry named at summary.md:464 and :466 are
already gone; the past-tense closure prose at summary.md:104 stands, as the entry directed.

No action was owed on the remaining deferred entries whose file this pass may edit. The
`adapterclient` latch entries, the CODE-6 latch rationale and verification-set entries, the
CONF-1 and tier-3 entries, the SCHEMA-1 field-table entries, the CODE-4 credential-lease entry,
the design-narrative mirror entries and the "Watch out for" entry were all applied to
`0081_....non-spec-changes.md` and `0081_....summary.md` before this pass ran; the current text
of both files carries the corrected form in each case, so this pass changed neither beyond the
index and the open decisions. The `AssignCredentialsResponse` field-number entry is refuted
rather than owed: the message is empty and SCHEMA-1's `bind_epoch = 1` is correct. The
`docs/reference/error-catalog.md` entry and the "docs/, four sites that stay TRUE" entry record
that nothing is owed. The two request-side entries, `[non-spec-changes.md SCHEMA-1 and CODE-6,
the request side of the bind epoch]` and `[non-spec-changes.md SCHEMA-1 and CODE-6, the §7.4
mid-session echo]`, are dissolved by the r6 revert to a per-entry epoch with no bind-sequence
request field, as the `[spec.6.review-fresh.1]` correction records; acting on either would stage
seven wire fields the current §15.4 makes non-conformant to read.

OPEN: `docs/reference/adapter-contract.md` still carries no bind-epoch contract and no
slot-identifier reclaim hold, and its `DemoteSDK` row at :64 is incomplete against §4.7.1's
caller rule, which makes the demotion the act that drops the caller's held epoch and leaves the
next bind sequence epochless. This widens the standing open entry against the same file's
`Shutdown` row at :75. Closing it needs a staged docs deliverable that does not exist; it lands
in `0081_....non-spec-changes.md` as a second DOCS deliverable against
`docs/reference/adapter-contract.md`, covering :64 and :75 together.

OPEN: `pkg/gateway/runtime/adapterclient/client.go`'s `Client.Shutdown` doc comment at :797-798
says "A zero deadline lets the adapter apply its default grace period", which is false:
`resolveShutdownGrace` prefers the caller context's remaining time over both the
runtime-configured grace and the package default, so a zero deadline yields the caller's own
remaining budget and the adapter's default applies only when the caller set no deadline at all.
CODE-6 opens that file, so the one-sentence repair could be staged there; it lands in
`0081_....non-spec-changes.md` under CODE-6 and in
`pkg/gateway/runtime/adapterclient/client.go`.

OPEN: the shipped pre-`Runtime.Start` failure branches release the slot by session identifier
alone (`pkg/adapter/session.go:133,:147,:157`; `pkg/adapter/resume.go:69,:73,:89,:107,:126,:134,
:141`; the same pattern at `pkg/adapter/sdkwarm.go:236,:241,:251,:298`). Under `SlotID ==
SessionID` a lagging one deletes a later attempt's entry and removes the tree, uploads and
credential directory that attempt staged. The class fix is an identity-checked deregister
threading the `*slotState` the claim returned into `deregisterSlotLocked`, and it wants its own
problem statement; this proposal records the class as a summary out-of-scope row and opens none
of those branches.

OPEN: the archived WATCHOUT at `0081_....review-log-archive.md:3785` cites its evidence as a
summary bullet the `f4` firing deleted, so the citation no longer resolves. It should be
re-pointed at CODE-5's `accountSlotFailure` doc comment and its caller list in
`0081_....non-spec-changes.md`, which state the same no-carve-out argument. The archive is
outside every current lane's editable set, so the repair lands in
`0081_....review-log-archive.md`.

OPEN: the edge-case bullet in `0081_....spec-changes.md` reading "A retry the §5.2 slot retry
policy places goes to a different pod; when that pod is the pool's only candidate the retry meets
the shipped `WARM_POOL_EXHAUSTED` outcome" has the wrong antecedent for "that pod": the intended
subject is the excluded, residue-bearing pod rather than the different one. The applied spec is
not wrong, because the §5.2 rationale states it correctly; the repair is a pronoun and it lands
in `0081_....spec-changes.md`.

Three open decisions the spec loop routed to a human were carried into the summary's
`## Open decisions for human to make` as entries 12, 13 and 14: whether the reclaim hold ends on
a slot whose cleanup never completes, whether the applied spec states what a `superseded`
successor inherits from the predecessor attempt's workspace tree, and whether the compensating
`Shutdown` is exempt from the §10.1 coordination-generation fence. Each carries the ground the
log entry gave and none carries a recommendation, because the loop derived none. The open
entries that remain in this log are unanswered verification questions rather than decisions, so
they stay here.

No staged deliverable is added, removed, merged, split or resequenced by this pass, and every
checklist box stays unticked.

### [f1.open-decisions.threshold-retune]

DECISION: retuning the `ceil(maxConcurrentSessions/2)` unhealthy threshold stays out of scope, and I wrote nothing, because the proposal already carries the item exactly as adjudicated. The Non-goals bullet at `0081_....summary.md:312-313` stands verbatim and `## Defects in the shipped tree that this proposal does not stage` carries no threshold row, which is the recommended state. An earlier firing reached and recorded the same call (`0081_....review-log-archive.md:3734`).
FACT: the threshold's formula and denominator are untouched by every staged deliverable. `UnhealthyThreshold` is `(maxConcurrent+1)/2` clamped to 1 (`pkg/gateway/runtime/slothealth/slothealth.go:215-220`) and the sole production trigger is `health.Unhealthy(sbe.Pod, req.MaxConcurrentSessions)` at `pkg/gateway/sessionserver/start.go:2857`, whose drain block is unchanged at `:2858-2870`. CODE-5 adds callers that reach that trigger; it does not move it.
FACT: the threshold is not asserted as a defect anywhere in the proposal, so a row in the not-staged defects section would state something the proposal does not hold. The behavioural cost of new paths reaching the trigger is carried by the "Faster pod churn at `maxConcurrentSessions >= 3`" bullet under "Watch out for" at `0081_....non-spec-changes.md:1764-1773`, whose last sentence already states that the §7.3 re-attach reaches the accounting for the first time and so drains a replacement pod at `maxConcurrentSessions: 2`.
WATCHOUT: the Non-goals bullet's justification half "adds accounting where there is none" is not the whole of CODE-5. On `applySlotRetryPolicy` the accounting already exists and its discriminator today is `relErr != nil` alone (`pkg/gateway/sessionserver/start.go:2834-2854`); CODE-5 widens it to `sbe.Leaked || relErr != nil`, so an unacknowledged reclaim on that path moves from the windowed arm to the persistent-leak arm. The bullet's operative claim, that no threshold changes, is exactly true, and the retry-path disposition change is stated correctly elsewhere in the proposal, so the falsifier let the bullet stand. A later pass that rewrites this bullet should not restate the "where there is none" half.

### [f1.open-decisions.0080-1.19]

DECISION: updated the `0080 §1.19` row in `## Impacts on other proposals` (`0081_....summary.md:586`) rather than adding one. The membership analysis the row already carried stands and is untouched; I replaced only its closing sentence and its "What it must do" cell. The new closing text states that the reclaim hold adds one more producer of an arm §1.19 already enumerates, because a held slot identifier carries no registry entry and `boundSlotState` answers the absent entry and the unbound entry alike with one `codes.FailedPrecondition`, and that the hold's own refusal sits outside §1.19's inventory. The action cell now reads "Re-derive the membership of §1.19's three refusal classes against the remaining cases and the narrowed ABA class. The class set itself is unchanged."
FACT: the retired sentence, "No bind-sequence RPC gains a refusal, so the class inventory grows by the reclaim hold's `ABORTED` refusal alone", was wrong on both halves. The staged sentinel `errSlotReclaimInProgress` is a `codes.Aborted` raised at the top of `ensureSlotStateLocked` (`0081_....non-spec-changes.md:946-967`), which is reached from `claimSessionSlotUnderLock`, `assignCredentialsSlot` and `ensureSlotPaths`, so the bind-sequence and workspace RPCs are exactly where it lands.
FACT: `CoordinatorFence` resolves its entry through `boundSlotState` at `pkg/adapter/coordination.go:116`, and `boundSlotState` returns one `codes.FailedPrecondition` with one message for both `!ok` and `st.sessionID == ""` (`pkg/adapter/slotsession.go:274-283`). No staged deliverable opens either that function or `checkSessionBound`, so §1.19's class set is unchanged and only membership moves.
FACT: 0080 is unlanded and still owns the section. Its §1.19 heading is at `proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:350` and its ground sentence at `:352` scopes the defect to the three `FailedPrecondition` refusals the adapter returns on the fence path, which is what the row assumes. The row's `Draft` status column is accurate; it carries no date and none was added.
FACT: the problem statement's own §1.19 paragraph (`0081_....problem-statement.md:312-315`) states only that this change alters the membership of the sets `boundSlotState` and `checkSessionBound` read. That is consistent with the corrected row, so no correction to the problem statement was needed.

## Index and checklist reconciliation (post-spec-loop, third pass)

This pass rebuilt the summary's deliverable index from the staged spec and non-spec changes,
checked the checklist's spec-lane steps against the current SPEC ids, reconciled the non-spec
steps' `Depends on:`, discharged the deferred corrections whose remedy lands in the four files
it may edit, and carried the spec loop's open decisions into the summary.

The staged deliverable set is unchanged since the second pass: SPEC-1 through SPEC-5, SCHEMA-1,
CODE-1 through CODE-6, CONF-1 and DOCS-1. Each is named in exactly one checklist step, each
step names one lane, the spec block leads at S1 through S5 in the order SPEC-5, SPEC-1, SPEC-2,
SPEC-3, SPEC-4, and every `Depends on:` resolves to an earlier step id. The index's file lists
were re-derived from `## Spec files touched` and `## Files touched on application (non-spec)`
and diverged nowhere, so steps 1 through 3 owed no change beyond the path repair below. Every
box stays unticked.

CORRECTS [DEFERRED, 0081_....non-spec-changes.md, "SCHEMA-1's paragraph 'the regenerated
`pkg/gen/adapter/v1` package lands with it' ... is false; the true path is
`pkg/proto/adapter/v1`"]: corrected at all three sites the entry names. SCHEMA-1's paragraph
(non-spec-changes.md:1060), the files-touched line (non-spec-changes.md:1797) and the
implementation-checklist S8 line now read `pkg/proto/adapter/v1`. Re-verified against the tree:
`schemas/lenny-adapter.proto`'s `go_package` option is
`github.com/lennylabs/lenny/pkg/proto/adapter/v1;adapterv1`, and `pkg/gen` does not exist.

CORRECTS [DEFERRED, 0081_....summary.md, "Watch out for", "`TestValidTransitions_spec_6_2`
asserts an exact edge count and fatals on a length mismatch. The §6.2 edit and the `slotstate`
edit must land together."]: the bullet is rewritten to the entry's truth. It now says the test
compares a `want` list in `pkg/sandbox/slotstate/slotstate_test.go` against
`ValidTransitions()`, that CODE-3 changes both so the two must move in the same step, and that
nothing compares either against `spec/06_warm-pod-model.md`, so the §6.2 edit is not what the
test gates. The instruction to land the spec edit and the `slotstate` edit together is gone,
because the one-lane-per-step rule puts SPEC-4 at S5 and CODE-3 at S7. The parallel sentence in
`0081_....non-spec-changes.md` under `### Edge-list test for CODE-3, tier 1` already carried the
corrected form and was not touched.

No action was owed on the remaining deferred entries whose file this pass may edit. The CODE-4
credential-lease entry is applied: the deliverable index reads "releases the session's credential
leases on the bind paths" and non-spec-changes.md:26-28 carries the resume carve-out. The
open-decision-9 entry is dissolved, because that entry has left the summary and only the
past-tense closure prose under `## Decisions` remains, which the entry states is correct. The
`Shutdown with the field unset` tier-3 entry is applied: no such case survives in the file. The
CODE-6 `adapterclient` latch entry is applied: non-spec-changes.md:996, :1020 and :1141 state the
clear on `DemoteSDK` and on a `RECLAIMED` reclaim, and :1339 stages the tier-1 case asserting
`BindEpoch` answers zero after each. The CONF-1 and design-narrative-mirror entries are applied
against the reverted per-entry staging. The `docs/reference/error-catalog.md` entry and the
"docs/, four sites that stay TRUE" entry record that nothing is owed. The five checklist entries
and the open-decisions-preamble entry were closed by the second pass.

OPEN: CONF-1 has no case for the `PrepareWorkspace` non-conformance clause §15.4 publishes, which
covers an adapter that mints more than one epoch for one call or resolves the slot identifier more
than once within one call. What the battery wants is a case asserting that a multi-frame
`PrepareWorkspace` call resolves its entry once and answers one non-zero epoch on its single
response. Closing it means authoring a fifth conformance property that no non-spec lens has read;
it lands in `0081_....non-spec-changes.md` under CONF-1 and in
`tests/tier10_conformance/bind_epoch_conformance_test.go`.

OPEN: `docs/reference/adapter-contract.md` carries no bind-epoch contract and no slot-identifier
reclaim hold; its `Shutdown` row at :75 states the shipped one-teardown contract that SPEC-1 and
SPEC-3 falsify in three ways and carries no no-op clean-exit answer, and its `DemoteSDK` row at
:64 is incomplete against §4.7.1's caller rule. Closing this needs a second DOCS deliverable that
does not exist; it lands in `0081_....non-spec-changes.md` and in
`docs/reference/adapter-contract.md`, covering :64 and :75 together. Eleven lenses have filed the
:75 site independently. The four sibling sites (`docs/reference/adapter-contract.md:81`,
`docs/operator-guide/security-principles.md:33`, `docs/reference/execution-modes.md:68`,
`docs/operator-guide/multi-tenancy.md:72`) are not edit sites, because SPEC-3 widens their "at
each session release" wording rather than falsifying it.

OPEN: `schemas/lenny-adapter.proto`'s `Shutdown` RPC comment and its `ReportSessionScrub` and
`SessionScrubOutcome` comments state the one-teardown contract and the every-release report, both
falsified by SPEC-1's two-teardown split, its no-op clean-exit answer and SPEC-3's withheld
report. `ShutdownResponse` carries no doc comment, so CODE-1's re-keying of `exited_cleanly` owes
nothing. Programme rule S-2 gives the comment repair to the step that owns the file, R1b; it lands
in `schemas/lenny-adapter.proto`.

OPEN: `pkg/gateway/runtime/adapterclient/client.go`'s `Client.Shutdown` doc comment at :797-798
says "A zero deadline lets the adapter apply its default grace period", which is false, because
`resolveShutdownGrace` prefers the caller context's remaining time over both the
runtime-configured grace and the package default. CODE-6 opens that file, so the one-sentence
repair could be staged there; it lands in `0081_....non-spec-changes.md` under CODE-6 and in
`pkg/gateway/runtime/adapterclient/client.go`.

OPEN: the shipped pre-`Runtime.Start` failure branches release the slot by session identifier
alone (`pkg/adapter/session.go:133,:147,:157`; `pkg/adapter/resume.go:69,:73,:89,:107,:126,:134,
:141`; the same pattern at `pkg/adapter/sdkwarm.go:236,:241,:251,:298`). Under `SlotID ==
SessionID` a lagging one deletes a later attempt's entry and removes the tree, uploads and
credential directory that attempt staged. The class fix is an identity-checked deregister
threading the `*slotState` the claim returned into `deregisterSlotLocked`, and it wants its own
problem statement; this proposal records the class as a summary out-of-scope row and opens none of
those branches.

OPEN: `0081_....spec-changes.md` carries four unapplied-prose repairs the spec lane owns and this
pass may not make. The edge-case bullet at :109-111 gives "that pod" an antecedent that makes the
sentence backwards, where the intended subject is the excluded residue-bearing pod. The Design
section at :63-66 says a §15.1 start onto a create-time-reserved slot "is placed by neither
mechanism" while naming one, where the two are §5.2's slot retry policy and the §7.3 re-attach's
whole-pod idle claim. The Design section at :43-44 omits the SPEC-2 §7.2 snapshot-close edits from
its pointer list. The `## Spec files touched` list describes the §4.7 row edit as "(first sentence
plus one sentence)" when the replacement swaps two sentences for seven or eight, and that entry
asks for a named set rather than a count so the trap does not re-arm. Each lands in
`0081_....spec-changes.md`.

OPEN: the edge case in `0081_....spec-changes.md` titled "A client retry of the §15.1 start after
a failed bind on a create-time-reserved slot" says an unacknowledged reclaim leaves the adapter's
entry surviving in whatever state the failed stage left it, which is false for a `Shutdown` the
adapter executed whose response was lost. The one-clause repair says the gateway cannot tell
whether the entry survives, so the retry may find either. It lands in `0081_....spec-changes.md`.

OPEN: `spec/06_warm-pod-model.md:156`'s `slot_cleanup ──→ released` annotation and its mirror at
`docs/reference/state-machines.md:236` gloss three actions, where SPEC-3 widens §5.2's action list
to five. Neither is false, because an abbreviated gloss states nothing wrong, so no edit is owed;
recorded so that whoever widens DOCS-1 beyond its single added row widens the sibling row with it.
Any repair lands in `spec/06_warm-pod-model.md` and `docs/reference/state-machines.md`.

OPEN: the archived WATCHOUT at `0081_....review-log-archive.md:3785` cites its evidence as a
summary bullet the `f4` firing deleted, so the citation no longer resolves. It should be
re-pointed at CODE-5's `accountSlotFailure` doc comment and its caller list in
`0081_....non-spec-changes.md`, which state the same no-carve-out argument. The archive is outside
every current lane's editable set, so the repair lands in `0081_....review-log-archive.md`.

Four open decisions the spec loop routed to a human were carried into the summary's `## Open
decisions for human to make` as entries 15 through 18: whether §7.1's exclusive-pod clause needs a
mid-resume carve-out, whether the exclusive-pool resume budget wants an upper clamp, whether the
create-time-reserved retry's exposure is accepted as the converged text now states it, and whether
a retry refused by a reclaim hold that never clears wants a distinct client-visible outcome. Entry
17 is the re-put of entry 9 that the log's own Open item asks for, because the converged text
states a worse residue than the version entry 9 was answered against. Each carries the ground the
log entry gave and none carries a recommendation, because the loop derived none. The numbering
continues past 13 and 14, which earlier passes retired. The open entries that remain in this log
are unanswered verification questions, or findings whose repair lands in the spec-changes file,
rather than decisions.

### [non-spec.1.fix-schema-1]

CORRECTIONS to the non-spec loop round 1 fix pass, from the post-fix review of that pass's own
edits. The pass left no shard of its own in this file, so its corrections open this subsection
rather than extending one.

- The pass's new SCHEMA-1 paragraph closed by asserting that
  `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go` "drives `assertFields`
  (`:92`), which iterates only its `want` slice and never asserts the total, so an added field
  does not move it". The premise about the helper is true and the conclusion about the file is
  false: twelve lines below the `assertFields` call the same file pins `CheckpointStart` to
  exactly six fields (`:151`, `if got := startMD.Fields().Len(); got != 6`), introduced by a
  comment at `:145-150` that names the helper's blind spot as its reason, and `assertOneof`
  pins a oneof's arm count (`:77`). The sentence was written to adjudicate whether that file is
  a second closed descriptor pin, so as written it tells an implementor the file can absorb an
  added field. `grep -rn "Fields().Len()" tests/ pkg/` returns only those two sites and
  `shutdown_recycle_wire_test.go:226`. The paragraph now states what the file does and why the
  nine staged fields still do not move it: both pins are over messages SCHEMA-1 does not open.
  The true half is kept, that `TestShutdownMessagePostRemovalDescriptor_spec_4_1` is the only
  closed field set over the messages SCHEMA-1 does open.

No staged deliverable is added, removed, merged, split or resequenced by this correction, so the
implementation checklist is unchanged and every box stays unticked. No other file restates the
corrected sentence, so no parallel edit was owed.
