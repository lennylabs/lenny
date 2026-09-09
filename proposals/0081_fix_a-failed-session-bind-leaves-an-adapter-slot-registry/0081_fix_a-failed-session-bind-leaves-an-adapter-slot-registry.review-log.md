# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

**Changelog (compaction pass 2).** Pass 1 built this section from an empty start; its own notes are in
Retired below. Pass 2 read the whole ledger for rounds 2 through 7 (the fix and design entries for
`spec.2`, `spec.3` and `spec.5`, plus roughly forty review-lens entries) and lifted its durable residue.
Lifted: fifty-six new Settled facts, thirty-three new Traps, twenty-four new Open items, and six new
Deferred corrections. The Traps additions are mostly MISTAKE entries recording a candidate a lens built
and withdrew, which is the residue the lenses themselves report as saving them a round. Applied every
CORRECTS against the entry it names: the teardown precondition, the placement-carrier decision, the anchor
sweep, the `spec/06:152` gloss, the two-site `spec/29:696` claim, the `terminate` reason OPEN and the
Redis-rehydration remedy were each rewritten in place, and the six-anchor sweep entry was replaced by the
fifteen-site list rather than kept. Retired five items as closed: "Resume path and the exclusion", the
vacuous-parenthetical OPEN, the recycle-boundary OPEN, the stale SPEC-3 closing note, and the disputed
default-disposition item (settled in the corrected form). Two disagreements are recorded rather than
resolved, each with an UNVERIFIED line: whether a leaked slot's held occupancy excludes the reclaiming pod
on the §5.2 placement path as well as on the §7.3 resume path, and whether §29.4's session-end trace is
scoped to a started session. Did NOT reach 200 lines: this section is roughly 300. Seven rounds produced
more durable residue than 200 lines holds, and nothing was dropped to reach the number.

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
- **`applySlotRetryPolicy` takes `req podsession.SlotBindRequest` by value,** so setting `req.ExcludePod` on the retry iteration is scoped to that request's remaining attempts. EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884.
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
- **The anchor sweep is done, and it now covers SIXTEEN sites rather than the six pass 1 named.** Rounds 2 to 6 minted the whole §7.2 block, §7.3's list tail, §6.2's `resuming → cancelled` clause, §5.2's `**Max retries:**` and `**Slot cleanup:**` sentences, and the §29.4 step-13 tail. Every "text to replace" block matches the tree byte for byte and occurs exactly once, and every "replace it with" block occurs zero times: spec/04:157 (§4.1 third sentence), :686 (§4.7 `Shutdown` row opening), :854 (§4.7.9 step 5); spec/05:453 (`**Scrub model.**`), :545 (`**Slot cleanup:**` action list), :555 (`**Max retries:**`); spec/06:150-153 (fence heading and the `receiving_uploads ──→ running` entry), :156-158 (fence close before `**\`reserved\` hold semantics.**`), :234 (`resuming → cancelled` clause); spec/07:23 (atomicity parenthetical) and :24 (the continuation line the new paragraph inserts before), :210 (§7.2 preamble premise), :213 (§7.2 step 2), :214 (§7.2 step 3), :414 (§7.3 list tail); spec/29:706-711 (§29.4 step 13). Eight lenses have now re-derived this mechanically. Do not re-run it unless spec/ moves, and extend this entry when a fix round mints a new anchor rather than leaving the next reviewer to notice the list is stale.
- **Every markdown anchor the staged text mints resolves.** `#47-runtime-adapter` (spec/04:657), `#479-startup-sequence-for-type-agent-runtimes` (:848), `#49-credential-leasing-service` (:1099), `#52-pool-configuration-and-execution-modes` (spec/05:365), `#62-pod-state-machine` (spec/06:78), `#71-normal-flow` (spec/07:3), `#72-interactive-session-model` (:115), `#73-retry-and-resume` (:378), `#151-rest-api` (spec/15:614), `#1542-rpc-lifecycle-state-machine` (:1686). No anchor is minted that spec/ does not already use.
- **§7.1's atomicity paragraph lives inside the fenced code block spanning spec/07:5-54.** Pre-existing, and not a defect of this proposal.
- **USEFUL (hand-off notes).** G1 and G2 each reported which sentences inside the SPEC-2 block they had already rewritten, which is what let G3 relocate and re-scope the block as a unit with their edits intact and append to the "Spec files touched" spec/05 entry rather than rewrite it, preserving SPEC-3's two anchors. It also let a later group discover that the design's line numbers had drifted and anchor on quoted strings instead of re-reading the file. Keep doing this.
- **Three RPCs admit a start, and §4.7's table carries all three.** `StartSession` (pod-warm, spec/04:672), `ConfigureWorkspace` (SDK-warm, :673) and `Resume` (:684) are all rows in the §4.7 Gateway→Adapter table, so the staged row's "which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed" resolves inside the table it names. spec/07:32-34 says SDK-warm pods skip `StartSession` outright, which is why naming that RPC in a predicate excludes every preConnect and resumed session.
- **The §7.3 re-attach claims from idle inventory.** `Binder.Resume` → `b.connect` → `podclaim.Claimer.Claim`, which takes only a Sandbox at `Status.Phase == Idle` (plus a same-tenant `reserved` rebind) and then CREATEs the deterministic per-pod claim; `ErrNoIdlePod` falls through to the §4.6.1 Postgres fallback, which re-reads the live Sandbox and refuses anything past idle. It never calls `ClaimSlot`. A pod holding a leaked slot keeps its `bound` claim, because `ReleaseSlot(leaked=true)` early-returns before `DeleteClaim`, so it is outside that candidate set with no `ExcludePod` needed. Nine lenses derived this independently; it closes the pass-1 OPEN "Resume path and the exclusion". EVIDENCE: podsession/binder.go:1590-1606,:1737-1768; podclaim/claimer.go:118-160.
- **That exclusion does not depend on Redis.** The reclaiming pod's per-pod `SandboxClaim` lives in etcd, so the §7.3 exclusion survives a Redis reset and the §12.4 rehydration hole does not reach it.
- **`ClaimSlot` pass 1 scans same-tenant CLAIMED pods with free capacity; pass 2 scans idle pods.** On the §5.2 bind path a leaked slot's held occupancy therefore filters nothing, and the exclusion there is the in-memory `ExcludePod` field alone. Do not generalise the idle-inventory argument to the bind path. UNVERIFIED: `spec.5.review-mechanism.1` states the stronger claim that held occupancy makes the pod *claimed* and therefore pass 1's own candidate, which reads as contradicting the §7.3 entry above; the two are reconcilable only if the resume path never reaches `ClaimSlot`, which every other lens asserts. Somebody should confirm the two entries are about different paths.
- **The three attempt kinds do not map onto three code paths.** A §7.3 resume-rebuild and a slotless row both reach `applySlotRetryPolicy` through `bindConcurrentSlot`; a create-time-reserved row takes `BindReservedSlot` with no retry loop. Each request builds a fresh `SlotBindRequest`, so `ExcludePod` never crosses a request boundary.
- **`applySlotRetryPolicy` wraps `binder.BindSlot` only,** and branches on `*podsession.SlotBindError`, so it governs BIND failures rather than only adapter-reported failures of a running slot. `BindReservedSlot` (start.go:2596) and `ClaimSlot` (:2148) are called directly and never traverse it. `maxSlotRetries == 1`, so at most two attempts run and a single-valued `ExcludePod` is sufficient.
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
- **`SlotID == SessionID` is stated nowhere in `spec/`.** §29.10:1457 says only that the gateway mints a `slotId`. SPEC-2's §7.1 block is the first spec statement of the identity; it is code-true and §6.4's `/workspace/slots/{sessionId}/` already implies it, so it is a new claim rather than a contradiction.
- **A pod-side reclaim destroys no client-retrievable setup output.** `GET /v1/sessions/{id}/setup-output` is served from the Postgres session row, which is what makes the §15.1 `SETUP_COMMAND_FAILED` promise survive a reclaim on the RunSetup stage.
- **The adapter manifest is pod-global,** at `/run/lenny/adapter-manifest.json` rather than under `/run/lenny/slots/{sessionId}/`, so SPEC-3's credential-directory removal cannot take it.
- **§7.1's three-kind parenthetical is exhaustive over the gateway's bind attempts.** `POST /v1/sessions/{id}/resume` routes through `resume_pending → running`, the same §7.3 flow, and a podless `suspended` session resuming is routed there too; `POST /v1/sessions/start` is the creation path. A fourth kind would be an unowned-residue finding, and there is none.
- **A concurrent pool has no `ready` gap.** `prepareAtFinalize` returns nil for `MaxConcurrentSessions > 1`, so the slot is materialised and launched together at `/start`, and the bound-but-never-started teardown class (READY_TIMEOUT, terminate-at-`ready`) can occur only on an exclusive pod, which spec/05:455 retires anyway.
- **No gate hard-fails on the staged spec text.** The tier-0 citation resolver and the line-citation ratchet target the retired `§X.Y line(s) L` form only and the staged text carries none; `per_slot_substate_scope_doc_reconciliation_test.go` pins four general edges by substring and the two scoped ones by exclusion, so SPEC-4's added edge passes; `recycle_scrub_trigger_consistency_test.go` reads the §4.7 row remainder SPEC-1 leaves untouched; `credential_path_literal_sweep_test.go` sweeps the retired pod-global path rather than the per-slot one; `code_blocks_test.go` parses only language-tagged fences and both the §7.1 and §6.2 fences are untagged.
- **The staged §5.2 sentences have no mirror outside spec/05.** Greps for "always assigned to a **new slot**", "fully saturated or unhealthy", "kills any processes owned by" and "releases the `slotId`" each return a single site.
- **The §5.2 slot retry policy runs on one path:** a row with no live `PodAssignment`, which is the §7.3 resume-rebuild or a slotless row. The create-time reservation and the `/start` reconnect to a create-reserved slot have no retry budget: `classifySlotBindFailure` answers the §5.2 `SLOT_FAILED` envelope, and a create-time `ErrNoConcurrentSlot` maps to `SESSION_CREATION_FAILED` rather than `WARM_POOL_EXHAUSTED`.
- **§5.2's non-retryable list includes `workspace_validation`,** a bind-time (FinalizeWorkspace) failure, so the policy demonstrably spans bind failures.
- **The snapshot diffs have been empty for most rounds.** `scratchpad/cp-snap/0081/spec-rN` was byte-identical to the live proposal in rounds 2, 4, 5 and 7, so "read the changed sections hardest" had no target. The real deltas are r2→r3 (the §7.2 block, the §15.1 create-time edge case, the racing-start rewrite, the `**Max retries:**` re-scoping and the `StartSession` → "start" sweep), r3→r4 (§7.1's "ends when that attempt succeeds", the §7.2 preamble deletion plus the step-2 sentence plus the step-3 rewording, the create-time-reserved bullet rewrite and SPEC-3's complement sentence) and r5→r6 (the §29.4 step-13 block). Diff against the named earlier snapshot rather than the current one; several lenses lost time on an empty diff.

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
- **MISTAKE (nearly filed): the staged "not retried" clause does not ban every post-first-RPC retry.** It is grammatically governed by "A reclaim the adapter does not acknowledge", so it bans the retry only for the unacknowledged-reclaim class, and the non-spec staging confirms that reading (`applySlotRetryPolicy` skips the retry only when `sbe.Leaked`). Read the sentence's subject before filing on it.
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
- **MISTAKE, three separate occurrences: the bound/started drift.** Three fixers have now restated the runtime-teardown precondition as "every bound entry the call removed", most recently in the §29.4 rationale at spec-changes.md:209-211, which is the currently-filed finding. `bound ⊋ started`: `assignCredentialsSlot` binds at AssignCredentials while only `claimSessionSlotUnderLock` sets `started`. Check every new sentence that names the runtime teardown against §4.7's "a session whose start the adapter has admitted". In each case the conclusion the sentence supported was right and only its stated ground was wrong, so the fix is the predicate phrase rather than the decision.
- **MISTAKE that cost a round: a narrow conclusion generalised into blanket cover.** A WATCHOUT closed with "The same holds for item 13's `terminate` frame. Do not file it as a missed edit site." What it had actually established was only that item 13 is not falsified by the TWO-TEARDOWN SPLIT, because item 13 is scoped to a session that ran. The co-tenancy gate is a DIFFERENT sentence of SPEC-1 and bites exactly the session ends item 13 enumerates, so item 13 was a real edit site and round 5 had to find it again. When writing a "do not file" note, say which sentence of the change it clears.
- **TRIED AND WITHDRAWN, four alternatives to §5.2's placement rule.** A third carrier that clears the row's §4.6 pod binding (a new gateway store write, and `ClaimSlot` pass 1 still admits a same-tenant claimed pod with free capacity, so it does not even deliver a different pod); a durable per-row exclusion (changes the created-state pod binding from create through start); an adapter tombstone of answered reclaims (touches pkg/adapter/slot.go and slotsession.go, which a later campaign position rewrites, and summary.md:154 already prices it); and deleting the constraint outright, which the standing trap already bars.
- **Do not write that a retried start inherits the first attempt's credential file or its armed §4.9 timers.** Only the slot TREE is inherited. `assignCredentialsSlot` rewrites both wholesale from the request's lease set, and a stale timer no-ops against a re-created entry. A reviewer's suggested fix over-claimed at exactly this point, and landing it would have added two new false claims.
- **Do not write "the retried start re-binds cleanly" in the acknowledged arm.** The reservation was released on failure and the retry re-reserves nothing, so the retried session runs on occupancy the counter no longer counts. That is pre-existing and deliberately outside this proposal, but an arm that asserts cleanliness is next round's finding.
- **`running` is bound vocabulary and every state-word occurrence is backticked.** Do not introduce a second, unbackticked sense (for example "ending a session the gateway believes is running") next to a bullet whose backticked `running` an earlier group is protecting. Say "takes the session off the shared runtime process" instead.
- **"pre-`running`" collides with an established term at a different level:** the session states `created`, `ready`, `starting` and `finalizing`. Do not simplify §5.2's corrected sentence back to a bare "pre-`running`". Three surviving "pre-`running`" phrases in the proposal are LABELS for the rule, the edge or the paragraph rather than range assertions, and a grep-and-replace that rewrites them adds words and no content.
- **Do NOT touch the "A start that races the reclaim" bullet's closing sentence** ("Neither ordering reports a cleanup outcome, because the slot never reached `running`"). Round 3 adjudicated it true-as-scoped and four later lenses re-derived the temptation. It reads like the same defect as the deleted §7.1 sentence and is not. If a round does file on it, the correction is the predicate ("the reclaim finds no session the runtime holds"), never the conclusion.
- **Do NOT touch spec/07:220's "Pre-attach terminal collapse" paragraph.** No replacement pod is claimed on those edges, so "no live workspace or running runtime to seal" stays true, and the §7.2 preamble deletion sharpens the contrast rather than breaking it. Five lenses have swept it as a candidate matching site.
- **Do NOT extend the §29 co-tenancy carve-out to §29.9 item 4b.** That bullet records that the specification does not state at what point of the eviction path the `terminate` frame is sent, which is an ordering claim the co-tenancy gate does not answer.
- **The §29.4 step-13 anchor is NOT unique.** "([§15.4.3](...), §28.5.3)." occurs at spec/29:645, :711 and :982. The instruction resolves only because it scopes itself to "§29.4's numbered step 13". Do not drop that scoping if the block is reworded, and do not report the anchor as unique in a sweep.
- **Do not file the §5.2 retry-policy trigger reading.** "The policy is triggered by an adapter-emitted slot-failure event for a running session, so SPEC-2's placement constraint reaches no bind failure" is a real tension between §5.2:544-557 and §6.2:283-286, and it is pre-existing: the code settles it the other way (`applySlotRetryPolicy` branches on `*podsession.SlotBindError`) and §5.2's own non-retryable list includes a bind-time reason. Three lenses dropped it.
- **Do not file the cross-replica actor problem in §7.2.** Step 3's "the connection that attempt still holds" against step 4's "fencing any stale coordinator" looks like a mis-assigned actor. Step 1 ("cancel the in-flight restoration RPCs") already presumes the sequence's replica owns those RPCs, and the reclaim is in fact issued by the failing attempt's own goroutine, so step 3 inherits the assumption rather than introducing it. Four lenses.
- **Do not file either `leaked` gloss.** §6.2:148's `slot_cleanup ──→ leaked` annotation and §5.2:562's whole-pod replacement trigger both gloss `leaked` as "cleanup timeout exceeded", which does not describe a reclaim the adapter never answered. §5.2's own bullet already says "If cleanup fails, the slot is leaked", broader than either gloss, and the shipped code already sets `leaked = err != nil || !cleanly` on the ordinary session end. Three lenses.
- **Do not file the `slot_cleanup ──→ released` annotation.** "(slot workspace removed, processes killed, slot released)" is today a word-for-word mirror of §5.2's three-action list, and SPEC-3 widens that list to five, so the annotation becomes an abbreviated gloss of a longer list. An abbreviated annotation states nothing false, and the structurally identical §5.2/§6.4 enumeration finding was already refuted. Three lenses, one of them standing next to the row while editing DOCS-1.
- **Do not file §11.4 revoke step 3.** "The pod's runtime adapter initiates graceful shutdown (SIGTERM to agent, wait up to 10s, then SIGKILL)" sits under a list scoped to "all active sessions", which are `started` by construction, and it is already false for any per-slot `Shutdown` on a co-tenanted pod. Pre-existing looseness this proposal widens rather than creates. A round that widens the revoke's scope should re-check the line.
- **Do not file §4.1's retained second sentence as drift this proposal creates.** Before the edit, sentence 2 already said "per-slot teardown"/"whole-pod teardown" while sentence 3 said "per-session teardown"/"whole-pod scrub". The edit adds a naming mismatch to a pre-existing one. It stays the standing OPEN on vocabulary, and four lenses have declined it.
- **Do not file the §4.7 "the pod's session mode" term collision.** The specification binds "session mode" to `executionMode: session`, while the axis that selects the starting RPC is pod-warm versus SDK-warm, which the same §4.7 table already spells two rows above. Four lenses derived it and all four declined it as wording. The fix is two words and belongs outside the loop; a fifth derivation is waste.
- **MISTAKE, barred and NOT closed: SPEC-4's prose against the untouched `receiving_uploads ──→ running` trigger.** The prose glosses that trigger as naming the moment the runtime "has been given" the session; the trigger's own words are "session dispatched to runtime with its session identifier". The orchestrator's already-fixed list names this contradiction, so filing it is re-litigation, but round 3 recorded it as only PARTLY discharged and rounds 4, 5 and 7 each re-derived it. The reconciling reading is that "a start still in flight" means the window between the adapter ADMITTING the RPC and `Runtime.Start` handing the session over, so nothing has yet been dispatched to the runtime. A round that wants it closed must either edit the fence annotation (and docs/reference/state-machines.md:235 with it) or delete SPEC-4's "which is the moment the trigger above names" clause. It propagates into the report gate, because SPEC-3 keys the withheld report on "before it reaches `running`".
- **Dead end, and here are its five sites: "the withheld report contradicts §4.7 or §12".** spec/04:692, spec/05:453 and :545, spec/12:481 and :494, schemas/lenny-adapter.proto:308-319 and docs/reference/adapter-contract.md:81 all scope their reporting rule to "a session release", and the proposal frames the pre-start reclaim as not one. Grepping `ReportSessionScrub` across spec/, docs/ and schemas/ lands on exactly this set.
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

### Open

- **CODE-1 comment phrasing** — UNVERIFIED: non-spec-changes.md:72-77 still carries "the runtime teardown runs for a session the pod's shared runtime process has been given" for a gate that reads `st.started`; confirm G2 landed the rewrite. See `spec.1.fix-G1.1`.
- **Tier-1 adapter fixtures** — UNVERIFIED: whether `slotPod`, `probeRuntime` or `recordingSessionScrubReporter` can drive a start through `noteRuntimeStarted` without going through the exported `StartSession`; the "Claimed but not yet recorded" and "Bound and started" cases both depend on it. See `spec.1.fix-G2.1` and `spec.1.fix-design-G2.1`.
- **Tier-4 fixture pool size** — UNVERIFIED: the staged tier-4 case asserts the retry re-binds on a second pod, which needs `tests/tier4_integration/recycle_scrub_path_test.go`'s pool to carry a second placeable pod. Nobody has read the fixture. See `spec.1.fix-G3.1`.
- **Tier-7a drain-gate assertion** — UNVERIFIED: whether `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` asserts the drain gate against the binding or against `started`; the CODE-1 split moves the drain inside the `started` block. See `spec.1.fix-design-G1.1`.
- **Tier-7a "closed exactly once"** — UNVERIFIED: the race case asserts one `Runtime.Close` while the design has two in that race; it holds only because `SocketRuntimeProcess.Close` early-returns on `!p.connected`, and `InProcessRuntime` and `MCPRuntime` have no such guard. See `spec.1.review-reliability.1`.
- **0080 §1.19 membership** — OPEN: CODE-1's `bound` → `removed`/`started` split and CODE-4's compensation timing move the membership `boundSlotState`/`checkSessionBound` read; the proposal should state this rather than leave 0080 to discover it. See `spec.1.fix-design-G1.1` and `spec.1.fix-design-G2.1`.
- **Socket runtime on a recycling pool** — OPEN: after any last `Runtime.Close` the listener is gone, so the next session's `Runtime.Start` accepts on a closed listener. Either socket runtimes never land on recycling pools or the recycle path is broken for them. Pre-existing; worth its own finding. See `spec.1.fix-design-G2.1`.
- **§6.2 fence versus §6.2 prose on `leaked` at concurrency 1** — OPEN (filed as a separate finding, not for this proposal): the fence scopes `slot_cleanup ──→ leaked` to concurrent occupancy while §6.2's own leaked-semantics paragraph, §5.2's `ceil(maxConcurrentSessions/2)` threshold at 1, and `slothealth.Tracker` all apply `leaked` at concurrency 1. See `spec.1.fix-design-G2.1`.
- **Excluded-pod exhaustion names the wrong cause** — OPEN: when the excluded pod is the only candidate the retry surfaces as `WARM_POOL_EXHAUSTED` / `concurrent_slots_exhausted`, honest but misnamed. Deliberately not minting a new `details.reason`. See `spec.1.fix-design-G3.1`.
- **Pre-start cleanup failure and the §5.2 threshold** — OPEN and UNVERIFIED: SPEC-3 withholds the report entirely, so a pre-start cleanup that fails on the pod can never be reported `leaked`, reach the threshold, or move the gauge. Today `removeSlotTree`'s error is already discarded, so the rule removes nothing that exists. Decide whether the spec wants suppression only for `released`. See `spec.1.review-security.1` and `spec.1.review-citations.1`.
- **`terminate` frame reason value** — OPEN, re-scoped: under the design as it now stands a bound-but-unstarted reclaim runs no runtime teardown, no close and no frame, because §4.7 puts the §15.4.2 signal inside the teardown and CODE-1 moves the drain inside the `started` block. The `reason`-enum question survives only for a reclaim of a session whose start WAS admitted, where `drainReason` maps `"slot_bind_failed"` to `session_complete`. The wire stays valid; nobody has decided whether that is the intended value. Re-scope the item before spending a round on the enum. See `spec.7.review-operational.1`.
- **Mid-start `Shutdown` bricks the pod silently** — UNVERIFIED, and narrower than it reads: `Close(sessionID)` for a session not in `p.active` still takes the last-close teardown whenever `p.active` is empty and `p.connected` is true, closing the listener bound once at adapter start. Reaching `connected && active empty` needs the `Interrupt` path, which closes the conn without clearing `connected`. Pre-existing, reachable only after an Interrupt of the last active session, and the remedy is code-lane. See `spec.1.review-docs-alignment.1` and `spec.2.review-reliability.2`.
- **§29 coverage** — OPEN, partly closed: §29's session-creation trace states a failure branch only for `ConfigureWorkspace` (step 23), and §29's preamble makes omission not a defect, so the creation half was judged optional. §29 item 12's `Shutdown` trigger enumeration was closed on the same ground. What is now live is item 12's unconditional "the adapter closes the session runtime" against SPEC-1's started-gate for a terminate at `ready`, which round 7 filed as a finding. Round 6 closed only the co-tenancy half of the neighbouring step 13. See `spec.7.review-kubernetes.7` and `spec.7.review-mechanism.1`.
- **"Does not acknowledge" versus an unclean answer** — UNVERIFIED: the staged §7.1 predicate is "a reclaim the adapter does not acknowledge", while the gateway's shipped predicate is `err != nil || !cleanly` and CODE-1 deliberately makes `exited_cleanly` false for a not-started reclaim whose tree removal failed. The §7.1 sentence may want "does not acknowledge, or answers that the release did not complete". See `spec.1.review-feasibility.1` and `spec.1.review-kubernetes.1`.
- **§4.1's retired vocabulary** — OPEN: SPEC-1's §4.1 replacement retires "per-session teardown" while the retained second sentence still says "The per-slot teardown and the whole-pod teardown are the same operation on the same address". After the split there are three named operations and the retained sentence names two the new sentence does not define. See `spec.1.review-fresh.1`.
- **`receiving_uploads` on an upload-free plan** — UNVERIFIED: if the §6.2 edge label "workspace materialization begins for this slot" is read as `PrepareWorkspace` rather than as entering the staging stage, a reclaim on the upload-free branch has no legal edge out of `slot_assigned` and SPEC-4 does not cover it. The charitable reading was assumed. See `spec.1.review-kubernetes.1`.
- **Unbound co-tenant and the drain gate** — OPEN: after the split a registered-but-unbound co-tenant still does not hold off the §15.4.2 drain (`boundRemains` reads the binding), so a `Shutdown` of the last started session can signal the shared runtime to terminate while another session is mid-bind. Named as pre-existing and not staged; nobody has decided whether it stays out of scope once the residue is gone. See `spec.1.review-mechanism.1`.
- **Redis rehydration loses leaked occupancy** — OPEN, with a narrower remedy than pass 1 implied: after a Redis restart the counter is rehydrated from `SessionStore.GetActiveSlotsByPod`, which selects `state = 'active'` rows, and a failed bind never has one, so the gateway can over-assign into unreclaimed resources. §6.2:160 already names the durable substrate it wants, so the two sentences are inconsistent for the SHIPPED leaked class independent of this proposal. Widening §5.2's "Post-recovery rehydration atomicity" paragraph is the fix; widening §7.1 is not. One thing is distinctive to a failed-bind leak: the adapter never learns the slot is leaked, so there is no adapter-side count to re-derive it from. See `spec.1.review-reliability.1` and `spec.5.review-reliability.1`.
- **§5.2's bullet at concurrency 1** — UNVERIFIED: whether §5.2's `**Slot failure and cleanup**` bullet is meant to govern the slot release on a `maxConcurrentSessions: 1` pod at all, given the staged §4.7 row defines the slot release as "the §5.2 slot cleanup" with no concurrency scoping. `spec.1.review-feasibility.1` argues it does not over-reach; `spec.1.review-performance.1` still wants it decided.
- **spec/05 versus spec/06 on failed-session pod retirement** — OPEN as a pre-existing wording tension, CLOSED for this proposal's sentence: spec/05:455 and :492 say a session that ends in failure or a crash always retires its pod, while spec/06:283 says the pod "is marked `failed` and released back to the pool (or terminated if unhealthy)". The §6.2 fence resolves it in favour of retirement, which is what makes the staged §7.1 citation hold. Predates this proposal, and a fix elsewhere should not deepen it. See `spec.1.review-security.1` and `spec.2.review-operational.1`.
- **tier 11 against the adapter-contract drift** — UNVERIFIED: nobody has run the gate on the `docs/reference/adapter-contract.md:75` drift SPEC-1 and SPEC-3 create. Six lenses filed it as a DEFERRED docs site and one reads tier 11's reconciliation as covering the per-slot sub-state table rather than the §4.7 RPC-table prose, so it may pass silently. The non-spec loop should run it. See `spec.2.review-applicability.1` and `spec.7.review-docs-alignment.1`.
- **`terminate` frame `deadlineMs`** — UNVERIFIED, pre-existing and code-lane: the frame requires `deadlineMs` with `minimum: 100`, `RuntimeOps.Terminate` serialises it `omitempty`, and every shipped gateway caller passes 0, so a Full-level runtime receives a frame missing a required field at an ordinary session end. The staged compensation reuses the same call. Whoever owns the tier-3 runtime-ops contract test should confirm. See `spec.2.review-client-surface.1`.
- **`/sessions/` and `/artifacts/` in the widened action list** — UNVERIFIED: `RemoveTree` removes four trees and SPEC-3's widened §5.2 list names two of them plus the timers. The omission predates the proposal, so it was judged incompleteness rather than a defect, but a completeness lens may disagree. See `spec.2.review-citations.1`.
- **"incomplete enumeration" under §29's preamble rule** — OPEN: §29 item 12 gains no fourth `Shutdown` trigger from SPEC-2, and the preamble subordinates a trace only where it DISAGREES. Somebody should decide once whether an incomplete enumeration counts as disagreement and record the answer, because the question returns every round. See `spec.2.review-citations.1`.
- **"on a pod of either concurrency" and the leaked sentence** — UNVERIFIED: whether SPEC-3's append drags the `**Slot cleanup:**` bullet's "If cleanup fails, the slot is leaked" to `maxConcurrentSessions: 1`, where staged §7.1 says the `leaked` sub-state is not available. Four lenses judged the asymmetry structural and below the bar, but the append is new text. See `spec.2.review-fresh.1` and `spec.3.review-edit-sites.1`.
- **Concurrent resume leaves a freshly claimed pod at occupancy 1** — OPEN: on a concurrent pool a failed resume with an unacknowledged reclaim leaves a newly claimed pod holding occupancy 1 with zero live sessions, permanently, and `connect` claimed it as a whole-pod claim rather than through `ClaimSlot`. Whether pass 1 treats such a pod as slot-bearing was not verified. See `spec.2.review-performance.1`.
- **Does CODE-4 owe a pod release on the resume path?** — UNVERIFIED: `Binder.Resume`'s error branch does `cl.Close()` plus a no-op `releaseResumeSlot`, so §7.1's "the failed attempt releases the pod's claim and the pod retires" is discharged on the §7.3 re-attach only by §6.2's general policy and never by a staged code path. Judged pre-existing and left unfiled; a code-lane reviewer should decide. See `spec.2.review-feasibility.1` and `spec.2.review-reliability.2`.
- **No section says positively that a reclaim can find a slot in `running`** — OPEN: it follows from §7.1's "the adapter may have started the session" plus SPEC-4's `running → slot_cleanup` sentence plus §5.2's complement clause. Three sections, one proposition, no restatement. A reviewer who wants it said once should put it in SPEC-4's prose, never in §7.1. See `spec.3.fix-design-G1.1`.
- **The create-time-reserved retry's exposure has no bound** — OPEN: a lagging reclaim can tear down a running retried session, where §5.2-placed retries are bounded by the placement constraint. The human question already asked about the recorded residue should be re-put with the corrected text in hand, because the honest answer is strictly worse than the version the question was asked about. Any closure must place the retried attempt off the reclaiming pod without `applySlotRetryPolicy` and without a durable per-row exclusion, or persist the exclusion on the row. See `spec.2.fix-G1.1` and `spec.3.fix-design-G2.1`.
- **Does §7.1's exclusive-pod clause need a mid-resume carve-out?** — OPEN now that §7.2 routes that path through §7.1. Both readings survive the evidence, so only a human can adjudicate. See `spec.3.review-feasibility.1`.
- **Whose view of "running" does §7.1 mean?** — OPEN: under a pod-state reading the obligation has ended before §7.2 step 3's reclaim is owed; under an attempt-completion reading it still holds. Round 4 narrowed the upper bound to "ends when that attempt succeeds", which favours the second reading without saying so. See `spec.3.review-fresh.1`.
- **§7.1's trigger noun-phrase** — OPEN: §7.1 says "A gateway bind attempt that fails" while SPEC-3 and SPEC-4 say "abandoned or fails" and §7.2/§6.2 route a client-CANCELLED re-attach through the same obligation. The window sentence probably carries the abandon case. Note that "abandoned" in this proposal means the GATEWAY abandoned the bind by failing it; client abandonment is named out of scope in the problem statement. See `spec.3.review-mechanism.1` and `spec.7.review-reliability.1`.
- **Does §7.3 owe a release step at all?** — UNVERIFIED: the §7.3 appended sentence orders the reclaim "before the replacement pod is released", but §7.3's numbered flow states no replacement-pod release on any branch, and §6.2's three non-terminal `resuming` failure bullets state no disposition for the half-claimed pod either. Only §7.2 step 3 releases it. Four lenses judged it prose imprecision. See `spec.3.review-edit-sites.1` and `spec.5.review-client-surface.5`.
- **Does the resume compensation feed the slothealth ledger?** — UNVERIFIED: if `Binder.Resume`'s staged compensation does not call `accountSlotFailure`, a §7.3 re-attach's unacknowledged reclaim leaves a leaked slot that never reaches the `ceil(maxConcurrentSessions/2)` trigger. Whoever owns CODE-4 should check. See `spec.5.review-mechanism.1`.
- **Do the other three `resuming` failure bullets need the reclaim clause?** — UNVERIFIED: pod crash, the 300s watchdog and non-retryable errors state no pod release today, so there is nothing for the clause to attach to, but §6.2 calls itself the authoritative enumeration of every edge out of `resuming`. Decide once. See `spec.7.review-mechanism.1`.
- **Is §29.4 scoped to a started session?** — UNVERIFIED, and two round-7 lenses disagree with no correction between them. `spec.7.review-security.1` and `spec.6.review-edit-sites.1` read §29.4's Preconditions paragraph as scoping the whole trace to a session whose runtime is running, which makes step 12 true and leaves it alone. `spec.7.review-mechanism.1` and `spec.7.review-kubernetes.7` read step 10 plus §15.1's precondition table as admitting a terminate at `ready`, which is bound-but-unstarted, so SPEC-1's started-gate falsifies step 12 and the site is created by this proposal rather than pre-existing. The second pair filed a finding. Somebody should settle whether step 12 needs a condition of its own or only a corrected rationale.
- **Does CODE-1's drain move lose a DRAINING obligation?** — UNVERIFIED: moving `drainViaLifecycle` from under `if bound` to under `if started` removes the graceful §15.4.2 signal for a bound-but-unstarted session. The reasoning that it does not matter (only exclusive pods reach that state, and spec/05:455 retires them) was not checked against §15.4.2's own obligations. A security or runtime-contract lens should close it. See `spec.7.review-performance.1`.
- **Transient over-assignment on the acknowledged racing-start ordering** — UNVERIFIED: the gateway releases the reservation with `leaked=false`, decrementing the counter for a slot a live session still occupies, so the pod can transiently carry `maxConcurrentSessions + 1` sessions, which is what §5.2's "Slot assignment atomicity" exists to prevent. The racing-start bullet records the residue but not this consequence. See `spec.5.review-reliability.1`.
- **§6.2:160's two claims about the leak count** — UNVERIFIED: it says the persistent count is the `lenny_adapter_leaked_slots` gauge "equivalently the leaked portion of the pod's Redis slot-counter occupancy" and that the ADAPTER exposes it. Both halves are already wrong against the tree before this proposal. The gauge half is a recorded dead end; whether the Redis-equivalence half is a separate spec finding is undecided and is not 0081's. See `spec.5.review-performance.2`.
- **Should the timer cancellation be conditioned on the credential removal?** — UNVERIFIED: `deregisterSlotLocked` cancels the §4.9 direct-mode timers at the top of `Shutdown` while `removeSlotTree` runs after the drain signal and `Runtime.Close`, so a cleanup whose credential removal fails leaves a direct-mode key with its enforcement point disarmed. Judged near-unreachable and pre-existing. A round wanting to close it should condition the cancellation in SPEC-3's sentence rather than widen §4.9. See `spec.5.review-security.1`.
- **Does the retry re-increment the Redis reservation?** — UNVERIFIED: `BindReservedSlot`'s release-on-failure leaves `PodAssignment` set, so a client retry may bind a reservation the counter no longer holds, while the edge-case bullet reads as though the reservation survives. Pre-existing and code-lane. See `spec.5.review-feasibility.1`.
- **§29.10's "Shared by the whole pod" list** — OPEN, pre-existing: it names neither the pod's shared runtime process nor CH-RUNTIMEOPS. Deliberately not pulled into the §29.4 edit, and a candidate finding for a later round. See `spec.5.fix-G1.1` and `spec.7.review-operational.1`.
- **Should `maxSessionsPerPod` count a bind that reached `RunSetup`?** — UNVERIFIED: §4.7.9 step 5 runs setup commands on the pod before `AssignCredentials`, so a bind abandoned at `ready` has executed code that primes the residual-state vectors §5.2 enumerates, and neither the tree nor the staged spec counts it. Pre-existing; a human or a later proposal should decide. See `spec.7.review-security.1`.
- **This lens has swept itself out** — the operational-consistency lens returned empty in rounds 3, 5 and 7 against three versions of the text. If it is scheduled again, the cheapest useful thing it can do is re-check the §16 inventory rows and the `ReportSessionScrub` / `sessions_served` chain. See `spec.5.review-operational.1`.

### Deferred

- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S7 (line 18) describes CODE-1 as running "the runtime teardown only for a session the shared runtime process has been given". It is doubly wrong: the teardown runs for a session whose start the adapter has admitted, and the report rather than the teardown is what is owed only for a session the shared runtime process was given. What is true after round 2, which retired the `StartSession` wording as wrong for a resumed session and for an SDK-warm pod: S7 delivers CODE-1 as "releases the slot for any entry the call removed, runs the runtime teardown only for a session whose start the adapter has admitted, and files a cleanup-outcome report only for a session the shared runtime process was given". S7's reach should also name `pkg/adapter/runtimegeneration.go`, whose accessor CODE-1 is the first caller of. The checklist is out of bounds for both fixers under the round's hard constraint.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S3 (line 10) reads "SPEC-3. §5.2's slot-cleanup bullet covers a bind abandoned before its session reaches the runtime and states that the reclaim reports no cleanup outcome." That is now false in both halves. What is true: SPEC-3 lands two anchors in §5.2 — the `**Slot cleanup:**` bullet's action list gains the slot's credential directory and the §4.9 timer cancellation, and the `**Scrub model.**` paragraph covers the cleanup of a bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches `running`, states that the cleanup reports no outcome, and states one cleanup-outcome report per session release. Round 2 narrowed that range in the four sites that hold it (spec-changes.md's Design paragraph and its staged SPEC-3 append, and summary.md's What-changes §5.2 bullet and its SPEC-3 deliverable-index line), because SPEC-4 adds no edge out of `slot_assigned` and no cleanup is owed there. S3's wider "before its session reaches the runtime" ranges over `slot_assigned` too, so it now directs an implementor to land the range the proposal withdrew.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S8 (line 20) reads "…`StartSession` takes the session back off the runtime, reports the reclaim as leaked, and refuses the start when it did not." The report is gone, and the `errStartRaceReclaimed` sentinel no longer appears anywhere in the proposal. What is true after round 2: "`noteRuntimeStarted` confirms the slot survived and reports whether it did, and `StartSession` takes the session back off the runtime and refuses the start when a reclaim landed after its claim, reporting no cleanup outcome. The reverse ordering, in which the claim re-creates the entry the confirmation reads, is an accepted failure mode rather than a deliverable." S8's "refuses the start when it did not" is the unconditional totality claim round 2 withdrew from the staged §7.1 paragraph and restated as two orderings in the racing-start accepted-failure-mode bullet and in CODE-2, so one rewrite closes both this and the deleted `leaked` report.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S2 says "§7.1's atomicity paragraph gains the failed-bind pod-side reclaim obligation, its `leaked` disposition, the no-retry rule, and the racing-start rule; §4.7.9 step 5 points at it." False on three counts. True instead, as of round 4: §7.1 gains the obligation as a paragraph of its own inserted after the atomicity paragraph, covering the creation finalize block, the §15.1 start transition and the §7.3 re-attach. It carries the obligation's begin and end boundary stated as the attempt's own success rather than as the `running` sub-state, the `leaked` disposition, the exclusive-pod case, and the tree hazard with a §5.2 pointer. It states NO placement rule, NO racing-start rule, and NO report rule of any kind: §5.2's slot-retry `**Max retries:**` bullet states the placement constraint itself, scoped to the retries that policy places, and §5.2's `**Scrub model.**` paragraph is the only statement of the report rule, its pre-`running` exception, its complement and the one-report-per-release rule. §7.2 takes THREE edits rather than one pointer edit: the section preamble's premise sentence is deleted outright, step 2 gains a sentence saying the aborted re-attach's pod-side state, including a session the adapter may already have started on the replacement pod, is reclaimed in step 3 rather than sealed, and step 3 is replaced. §7.3's resume flow, §6.2's `resuming` mid-resume cancel bullet and §4.7.9 step 5 are the remaining pointer edits, and all of them land in the same step. This supersedes the round-1 and round-2 "true instead" texts, which named a pod-disqualification rule, a racing-start rule and a no-report rule inside §7.1.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S10 says "a bind whose reclaim was not acknowledged is not retried" and names tiers 0, 1, 4. True instead: the retry iteration carries `ExcludePod` so `ClaimSlot` places it on a different pod, and CODE-5 now also touches `pkg/gateway/podlifecycle/podclaim/slotclaimer.go`, so the step reaches tier 2 (the envtest-backed placement-filter cases) beside 0, 1 and 4.
- DEFERRED [proposals/0081_.../0081_....summary.md]: PARTLY CLOSED. The row for 0080 §1.19 now states both membership cases, the absent-entry one and the present-and-bound one a racing start's own claim leaves, and it no longer claims the change narrows the problem. Still owed is the placement consequence: with `ExcludePod` set after an unacknowledged reclaim, a retry never issues a fence RPC to a pod that may still hold the prior attempt's entry, so that refusal class is narrowed further for the retries §5.2's slot retry policy places. Round 2 moved the placement rule out of §7.1 into §5.2's `**Max retries:**` bullet, so the consequence is scoped to those retries and does not reach a §15.1 start onto a create-time-reserved slot. A THIRD membership case is also owed and is not stated: after a reclaim the adapter did NOT acknowledge on a create-time-reserved slot, a surviving entry is present-and-unbound (workspace-prep residue) or present-and-bound (credential-assignment residue) for a session the gateway has abandoned, so a fence RPC in that window meets the unbound-entry refusal or neither refusal, for a session 0080 would classify as gone. One rewrite of the row should close all three cases together.
- DEFERRED [docs/reference/adapter-contract.md]: line 75's `Shutdown` row states the shipped one-teardown contract, "The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`". After SPEC-1 and SPEC-3 that is false in three ways: the slot release now runs for an unbound entry, the runtime teardown and the usage flush run only for a started session, and a pre-start reclaim reports no outcome. The row also needs the no-op clean-exit answer for a session the adapter holds no entry for. Secondary, same cause: `docs/reference/adapter-contract.md:81` ("at each session release ... The gateway increments the pod's served-session count"), `docs/operator-guide/security-principles.md:33`, `docs/reference/execution-modes.md:68`, and `docs/operator-guide/multi-tenancy.md:72` each say the per-slot cleanup runs "at each session release" and reports its outcome, which SPEC-3 widens (cleanup also runs on an abandoned bind) and narrows (that one reports nothing). The only staged DOCS deliverable is DOCS-1 for `docs/reference/state-machines.md`; `adapter-contract.md` appears in no edit list. The non-spec loop owns these, and tier 11 reconciles them. Five separate lenses filed this independently.
- DEFERRED [non-spec-changes / docs, DOCS-1]: `docs/reference/state-machines.md:251` says the `slot_cleanup -> leaked` edge "appl[ies] only to a pod serving more than one concurrent session". DOCS-1 adds only the new `receiving_uploads -> slot_cleanup` row at :234-237; the prose at :251 still scopes the terminal the new edge needs away from a single-session pod.
- DEFERRED [schemas/lenny-adapter.proto]: the `Shutdown` RPC comment ("asks the adapter to terminate the agent and release the pod ... Returns when the agent process has exited") and the `ReportSessionScrub` / `SessionScrubOutcome` comments ("on every session release") are the proto-side statement of the contract §15.4 says is "kept in sync with the prose". SPEC-1's two-teardown split, its no-op clean-exit answer, and SPEC-3's withheld report all falsify parts of them. Programme rule S-2 bars this proposal from opening that file, so the correction belongs to the step that owns it. The `Shutdown` comment is already stale for a co-tenanted pod, so this is a widening rather than a new break. EVIDENCE: schemas/lenny-adapter.proto:203-206,:308-311,:436-438; spec/15:1456.
- DEFERRED [proposals/0081_.../non-spec-changes.md CODE-2]: the `noteRuntimeStarted` guard does not close the other ordering of the same race. If the compensating `Shutdown` lands BEFORE `claimSessionSlotUnderLock` runs, the reclaim removes nothing durable: the start's own `ensureSlotStateLocked` re-inserts the entry, sets `sessionID` and `started`, `Runtime.Start` succeeds, and `noteRuntimeStarted` then FINDS a matching entry and returns true. No rollback fires, and the pod is left holding exactly the third residue class for a session the gateway has abandoned. The guard as written detects only reclaim-after-claim. What is true after round 2: the second branch was taken. §7.1's promise that the pod holds no state for that session is deleted, and both orderings are stated in the proposal's accepted-failure-modes list and in CODE-2's own text, so the residue is recorded rather than closed. Closing it still needs a discriminator that survives entry re-creation (a per-session reclaim fence or tombstone, or the start re-checking a generation captured before the claim). The window is tight (only two nil checks run before the claim) but the removal is destructive, so the adapter has no record that a reclaim happened. The remedy is code-lane, so the spec loop cannot land it. EVIDENCE: pkg/adapter/slot.go:97-126; pkg/adapter/slotsession.go:75-90; non-spec-changes.md:170-200. Filed independently by `spec.1.review-feasibility.1` and `spec.1.review-mechanism.1`.
- DEFERRED [docs/reference/error-catalog.md]: nothing, recorded deliberately. Under the placement-rule design, lines 129, 155 and 156 stay TRUE, because no failure class becomes non-retryable. Recorded explicitly so a later round does not re-file them: they are sites only under the rejected no-retry alternative.
- DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: step **S1 · spec** (line 6) enumerates SPEC-1's sites as §4.1's scope sentence and the §4.7 `Shutdown` row only. That is now false: SPEC-1 also stages one appended sentence on spec/29_communication-scenarios.md §29.4 session-end step 13. Append to S1's description, after "...and state the no-op answer for a session the adapter holds no entry for.", the sentence "§29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7." S1's tiers (0, 11) and its "Depends on: —" are unchanged, because the added site is spec prose in the same lane.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md "Spec files touched"]: the spec/07 line describes §7.2's edit as "the section preamble's premise clause and step 3, both replaced". After round 4 it is "the section preamble's premise sentence deleted, step 2 gaining one sentence, and step 3 replaced". Separately, the same list describes the §4.7 row edit as "(first sentence plus one sentence)" when the replacement swaps two sentences for seven. Both are in the same file as the blocks they count, so a fixer touching either block should correct them in the same edit rather than treating the file list as out of scope. A count in that list is a trap that re-arms on every later edit; prefer a named set, as the SPEC-3 entry now uses ("the per-slot cleanup pointer and the cleanup-outcome report rules appended").
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :63-66]: "A §15.1 start onto a slot reserved at creation is placed by neither mechanism" has no antecedent for "neither". Round 2 named two placement mechanisms, round 3 deleted the second from the surrounding text, and round 4 did not restore it, so only §5.2's slot retry policy is named. What is true: two mechanisms place a further attempt at the same session, §5.2's slot retry policy and the §7.3 re-attach's whole-pod idle claim through `podclaim.Claimer.Claim`, which cannot select a pod whose occupancy the leaked slot holds; the §15.1 create-time-reserved start is placed by neither. Repair the sentence with that fact rather than by deleting "neither".
- DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :43-44]: the pointer list ("§7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph and restate nothing") omits the SPEC-2 §7.2 snapshot-close edits entirely, although "Spec files touched" carries them. §7.2 step 3 also restates the reclaim's ordering against the pod release, so the sentence is incomplete rather than wrong. A fixer touching the Design paragraph should fold §7.2 in.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md, edge case "A client retry of the §15.1 start after a failed bind on a create-time-reserved slot"]: the round-4 rewrite says "When the adapter does not acknowledge the reclaim … the adapter's entry survives in whatever state the failed stage left it." That is false for the delivered-but-unanswered case: a `Shutdown` the adapter executed while the response was lost removes the entry, and the gateway still classifies the reclaim as unacknowledged (`err != nil || !cleanly`). What is true instead: an unacknowledged reclaim leaves the gateway unable to tell whether the entry survives, so the retry may find either a surviving entry or none. The later sentences of the same bullet already reason about a lagging reclaim, so the correction is one clause.
- DEFERRED [proposals/0081_.../0081_....spec-changes.md, edge-case bullet one, :98-102, and the same wording at non-spec-changes.md:350-353]: the closing clause "so the failure is accounted transient and a blob-store outage does not retire healthy pods" is false as stated. `RecordFailure` plus `Unhealthy` drains at `ceil(maxConcurrentSessions/2)` windowed failures, which is ONE failure at `maxConcurrentSessions: 2`, and a blob-store outage produces exactly the correlated stream that reaches the threshold inside one five-minute window at any concurrency. What is true instead: the clean-exit answer keeps the failure out of the PERSISTENT leak count, which is all the no-op sentence buys. The honest sentence is "so the failure is accounted transient rather than leaked, and a blob-store outage does not add to the pod's persistent leak count".
- DEFERRED [docs/, four sites that stay TRUE]: `docs/client-guide/session-lifecycle.md:416`, `docs/reference/adapter-contract.md:84`, `docs/runtime-author-guide/index.md:186` and `docs/runtime-author-guide/lifecycle.md:69` also describe the per-slot cleanup and are NOT falsified by SPEC-1 or SPEC-3. Recorded explicitly so the non-spec loop does not widen its docs edit list past the five sites that actually break. Also unchanged: `docs/runtime-author-guide/lifecycle.md:34-43` is a pod-level table in a different vocabulary with no cleanup edge, so SPEC-4 does not reach it.

### Retired

- The UNVERIFIED asking whether the compensating `Shutdown`'s `coordination_generation` is validated on the adapter's `Shutdown` path (`spec.1.review-applicability.1`). Closed: `Shutdown` is unfenced today, validated only in `CoordinatorFence` and `CheckpointBarrier`.
- The UNVERIFIED asking whether `Binder.Resume`'s compensation needs its own §7.3 sentence or a widened §7.1 one (`spec.1.review-edit-sites.1`). Closed by the decision to lift the obligation into its own §7.1 paragraph scoped to any bind attempt, with §7.3 carrying a one-sentence pointer.
- The DEFERRED against CODE-2 for sending a second `ReportSessionScrub` in the race (`spec.1.review-applicability.1`). Closed: the second call and the `errStartRaceReclaimed` sentinel were deleted.
- The UNVERIFIED asking whether the Design paragraph's deferral of the credential file and the §4.9 timers to §5.2 is a false attribution (`spec.1.review-performance.1`). Closed by SPEC-3's first anchor, which widens §5.2's action list to name both.
- The four condensed checklist DEFERREDs recorded in the Pass 1 preamble (S7, S3, S2/S10, S8). Superseded by the fuller per-group versions kept above, which name the same checklist lines with the corrected text.

Retired in compaction pass 2:

- The OPEN "Resume path and the exclusion". Closed in the proposal's favour by nine lenses across rounds 2 to 5: `Binder.Resume` claims through `b.connect` → `podclaim.Claimer.Claim`, an idle-only whole-pod claim, and never through `ClaimSlot`, so a pod holding a leaked slot is outside the candidate set. The durable statement is in Settled. `spec.5.review-mechanism.1` disagreed, on the ground that held occupancy makes the pod *claimed* and therefore a `ClaimSlot` pass-1 candidate; that claim is about the §5.2 bind path rather than the resume path, and neither entry corrects the other, so the newer one is carried as an UNVERIFIED beside the pass-1 claim rather than dropped.
- The UNVERIFIED "Vacuous parenthetical at steps 2-8". Refuted: §29.2's mirror of the same clause covers steps 2-10, whose window issues no pod-side RPC, and the parenthetical quantifies over an empty set in §7.1's own window for the same reason. Two lenses recorded the refutation.
- The UNVERIFIED "Recycle boundary on a failed bind". Closed: `ReleaseSlot`'s `recycle` parameter rather than its `leaked` one is what patches the claim `bound → recycling`, and `ReleaseSlotReservation` passes `recycle=false`, so a failed bind that drives occupancy to zero DELETEs the claim and the pod retires. That is a retirement rather than a residue class.
- The trap "SPEC-3's closing note may be stale". Closed: round 1's G2 landed the edit and the note now reads "its trigger, the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula, the CRD validation rule, and the leaked outcome all stand as written". The four-site drift it warned about is gone, and a later round replaced the remaining literal count in "Spec files touched" with a named set.
- The disputed item "does 'On the default disposition the pod is replaced.' contradict the staged §7.1 sentence?". Settled and rewritten in Traps: the sentence has one site rather than two, and the refutation's conclusion survives while its evidence does not.
- The pass-1 six-anchor sweep entry. Deleted rather than kept, and replaced by the sixteen-site list in Settled. A warning that the sweep is finished is worse than useless once nine more anchors have been minted, and five lenses each spent time discovering the list was stale.

## Ledger

### [spec.2.fix-G1.1]

DECISION: the runtime-teardown precondition drops the RPC name entirely and lives in one place — BECAUSE naming `StartSession` was wrong (`claimSessionSlotUnderLock`, the only site that sets `st.started`, has three production callers: session.go:111 StartSession, resume.go:50 Resume, sdkwarm.go:217 ConfigureWorkspace, and spec/07:38-40 states SDK-warm pods skip StartSession outright), and naming all three would duplicate §4.7's own RPC table and go stale on a fourth admitting RPC. The §4.7 `Shutdown` row now reads "a session whose start the adapter has admitted" plus one sentence saying which RPC in that table starts a session depends on the pod's session mode and on whether the session is new or resumed. §4.1 stops restating the predicate and points at §4.7. Every other proposal site says "a start" — ALTERNATIVES: enumerate the three RPCs (the finding's own suggested_fix; rejected as a closed enumeration repeated at six sites); revert to the shipped `bound` predicate (rejected: `assignCredentialsSlot` sets `st.sessionID` without starting, slotcreds.go:30-36, so `Runtime.Close` would run for a session the runtime never got); gate on `runtimeLive` (already a standing trap: inverts the fail-closed direction).

DECISION: the retry-placement rule moved out of §7.1 into §5.2 and is scoped to exactly what §5.2 places — BECAUSE §7.1 was stating a universal ("that pod carries no further attempt at the same session ... the attempt keeps its retry, and the mechanism that retries it places it on another pod") whose carrier enumeration cannot be completed: a `/start` onto a create-time-reserved slot goes through `BindReservedSlot`, pinned to `row.PodAssignment` and `row.ID` and explicitly outside the §5.2 retry policy, so the `ExcludePod` filter is never consulted. §7.1 now keeps the reclaim, its `leaked` disposition, the exclusive-pod case, the tree hazard, and a pointer to §5.2, and states no placement rule. §5.2's `**Max retries:**` bullet states the constraint itself. The uncovered path is recorded as an accepted failure mode — ALTERNATIVES: add a third carrier (rejected: `ClaimSlot` pass 1 still admits a same-tenant claimed pod with free capacity, slotclaimer.go:411-470, so clearing `PodAssignment` does not deliver the guarantee without persisting the exclusion on the row); make the failed reserved-slot start terminal for the client (already recorded as tried and withdrawn, ~eleven mirrored exception sites).

DECISION: §7.1's promise that a racing start leaves the pod holding nothing is deleted rather than scoped, and the residue is recorded in the proposal's accepted-failure-modes list stating both orderings — BECAUSE the promise was a total outcome claim about a mechanism that is partial by construction, and a scoped normative sentence in the obligations paragraph immediately raises the reviewer's next question about the other half. That is how this gap has already been deferred three times — ALTERNATIVES: scope the sentence in place (finding's option a); add a per-session reclaim tombstone (finding's option b; it is the right mechanism if the residue is judged unacceptable, but it touches pkg/adapter/slot.go and slotsession.go, which a later campaign position rewrites, and the proposal already prices a tombstone at summary.md:154).

FACT: `BindReservedSlot` is outside the §5.2 retry policy and its failure is terminal for the request, but the client-visible retry still exists: the row stays `ready` and the handler returns a retryable `STARTING_FAILED` with a `Retry-After`, so the retried `/start` re-enters `bindConcurrentSlot`'s reserved branch against the same `PodAssignment` and the same slot id — EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2604; pkg/gateway/podlifecycle/podsession/slotbinder.go:204-206.

FACT: a retried `/start` onto a reserved slot whose adapter entry survived the reclaim is refused by the adapter, not silently re-admitted. `claimSessionSlotUnderLock` returns `Unavailable` ("session %s has already started on this pod") when `st.started` and `idempotentRepeat` is false, and `StartSession` passes false — EVIDENCE: pkg/adapter/slotsession.go:78-84; pkg/adapter/session.go:111.

WATCHOUT: `ensureSlotStateLocked` creates the registry entry AND the on-disk tree for any slot id the map does not hold, so a completed reclaim is not durable against a start whose claim runs afterwards. The claim re-creates exactly what CODE-2's confirm reads, the confirm returns true, and no rollback fires. Any future guard must read state that survives entry re-creation — EVIDENCE: pkg/adapter/slot.go:105-126; pkg/adapter/slotsession.go:74-88.

CORRECTS [standing context, "DECISION: the disqualification names its carrier per attempt kind." (review-log.md:41)]: superseded. §7.1 now names no carrier and states no placement rule; §5.2's `**Max retries:**` bullet carries the constraint for the retries that policy places; the §15.1 start onto a create-time-reserved slot is a recorded accepted residue rather than a governed attempt kind. The bullet's §7.3 half also rests on an unverified claim about `Binder.connect`'s pod selection (still OPEN at review-log.md:111), and the §7.3 sentence resting on it was removed from the edge-case bullet in this round.

CORRECTS [standing context, "DECISION: the teardown precondition is reworded." (review-log.md:25)]: the reworded predicate named `StartSession`, which is wrong for a resumed session and for an SDK-warm pod. What is true after this round: §4.7's `Shutdown` row is the single home and says "a session whose start the adapter has admitted"; §4.1 points at it and restates nothing.

USEFUL [standing context, "Adapter predicate nesting" (review-log.md:20) and "`st.started` precedes `Runtime.Start`" (:21)]: both were exactly right and saved re-deriving the three-caller structure and the fail-closed direction from scratch.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S8 (line 20) reads "`noteRuntimeStarted` confirms the slot survived and reports whether it did, and `StartSession` takes the session back off the runtime, reports the reclaim as leaked, and refuses the start when it did not." Two things are false: the leaked report was deleted in an earlier round (already carried as a DEFERRED at review-log.md:131), and the guard is not total. What is true after this round: "`noteRuntimeStarted` confirms the slot survived and reports whether it did, and `StartSession` takes the session back off the runtime and refuses the start when the reclaim landed after its claim, reporting no cleanup outcome." One rewrite closes both.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S2 (already carried at review-log.md:132) names "the pod-disqualification rule" and "the racing-start rule" among what §7.1 gains. Both are now false. What is true: §7.1 gains the reclaim obligation as a paragraph of its own covering the creation finalize block, the §15.1 start transition and the §7.3 re-attach, plus the `leaked` disposition, the exclusive-pod case, the tree hazard with a §5.2 pointer, and the no-report rule. §5.2's `**Max retries:**` bullet states the placement constraint. There is no racing-start rule in §7.1.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S7 (already carried at review-log.md:129) says the teardown runs "only for a session whose `StartSession` the adapter has admitted". Now false in the same way §4.7 was. True instead: "a session whose start the adapter has admitted".

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.summary.md]: the "Impacts on other proposals" row for 0080 §1.19 (summary.md:275) still says only that a fence for a session whose bind failed meets the absent-entry refusal rather than the unbound-entry refusal. This round's §4.7 correction does not change that membership, but the orchestrator asked this proposal to state the effect on `boundSlotState`/`checkSessionBound` explicitly, and the row does not yet name the second case: after a reclaim that a racing start's claim re-created the entry for, the fence meets neither refusal, because the entry exists and is bound. My grant on summary.md covers the deliverable index and statements my own edits falsified, and this row is neither.

OPEN: whether the create-time-reserved `/start` residue recorded as an accepted failure mode is acceptable to a human. The constraint any closure must satisfy: it must place the retried attempt off the reclaiming pod WITHOUT relying on `applySlotRetryPolicy` (which that path never enters) and WITHOUT a durable per-row exclusion field, or it must persist the exclusion on the session row and accept that the created-state pod binding no longer holds from create through start.

### [spec.2.fix-G2.1]

DECISION: narrowed the SPEC-3 range to one canonical phrase, "abandoned or fails after its slot enters `receiving_uploads` and before it reaches `running`", written identically into the four sentences that carried the wider "before its slot reaches `running`" — spec-changes.md staged SPEC-3 append, spec-changes.md Design paragraph "**The cleanup-outcome report follows the `running` boundary.**", summary.md's What-changes §5.2 bullet, and summary.md's SPEC-3 deliverable-index line — BECAUSE §5.2's `**Scrub model.**` paragraph is the single home of the withheld-report rule and a rule stated in one place must state its own domain, and that domain is exactly the span SPEC-4's one new edge covers — ALTERNATIVES: (a) have §5.2 name the SPEC-4 edge instead of restating the range, rejected because a §5.2 reader would have to jump to §6.2 to learn when the report is withheld; (b) add a second `slot_assigned → slot_cleanup` edge so the wide wording becomes true, rejected because it reverses a scoping decision the proposal states twice and cascades into DOCS-1 and CODE-3; (c) keep the wide rule and bolt on an exception clause elsewhere, rejected because a rule plus a distant exception is the same two-places failure the round-1 double-statement mistake already cost.

FACT: §4.7.9 step 5 begins at `PrepareWorkspace`, which is exactly §6.2's trigger for `slot_assigned ──→ receiving_uploads` ("workspace materialization begins for this slot"), so the connect stage sits outside the step-5 enumeration and the narrowed range and the §7.1 obligation's "begins with an attempt's first such RPC" name the same boundary. EVIDENCE: spec/04_system-components.md:852; spec/06_warm-pod-model.md:150-151.

FACT: today's per-slot fence still has exactly one edge out of `slot_assigned`, and it goes to `receiving_uploads`. Nothing in the tree asserts the wide range, so this narrowing falsified no tree text and needed no tree-side site. EVIDENCE: spec/06_warm-pod-model.md:150-156.

WATCHOUT: three surviving "pre-`running`" phrases are labels rather than range assertions and must not be swept by a later grep-and-replace. spec-changes.md:185 ("the pre-`running` exception"), spec-changes.md:382 ("the report the pre-`running` reclaim withholds") and non-spec-changes.md:261 (CODE-3's heading) each name the rule or the edge the proposal already scoped to `receiving_uploads`; rewriting them to the long phrase adds words and no content. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:185,382.

WATCHOUT: two other "never reached `running`" sentences are still true under the narrowed rule and were deliberately left. The staged §7.1 sentence (spec-changes.md:216) sits inside an obligation that "begins with an attempt's first such RPC", so its slot is in `receiving_uploads` for the whole window; the racing-start edge-case bullet (spec-changes.md:134) describes a start in flight, which SPEC-4's fence annotation places on the `receiving_uploads` side. Neither needs the range clause. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:134,216.

USEFUL [standing context, Traps, "SPEC-3's closing note may be stale"]: checked it first. Round 1's G2 did land that edit; the closing note now reads "its trigger, the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula, the CRD validation rule, and the leaked outcome all stand as written" and no longer claims the action list is unchanged. The four-site drift that entry warned about is closed. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:356-358.

UNVERIFIED: the standing context's Open item "`receiving_uploads` on an upload-free plan" (review-log.md:120) becomes load-bearing after this narrowing. `stageWorkspace` sends `PrepareWorkspace` only under `if len(uploads) > 0`, so on an upload-free plan `FinalizeWorkspace` is the first adapter RPC; if §6.2's "workspace materialization begins for this slot" is read strictly as `PrepareWorkspace` rather than as entering the staging stage, an upload-free bind has no legal edge out of `slot_assigned` and the narrowed §5.2 rule does not reach it either. The charitable reading was assumed here as it was before. Whoever owns SPEC-4 should settle the trigger's reading rather than the range's wording. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1323-1330; spec/06_warm-pod-model.md:150.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md]: nothing owed. No statement in that file asserted the wide range, so this edit falsified nothing there and the file was not opened for repair. Recorded so a later pass does not go looking.

### [spec.2.fix-G3.1]

DECISION: Closed the §7.2 finding with one new SPEC-2 anchor against spec/07 §7.2, carrying two verbatim replacements (the section preamble's premise clause at spec/07:210 and step 3 at spec/07:214), and kept the staged §6.2 clause replacement exactly as written — BECAUSE §7.2 is the section that OWNS the mid-resume close sequence (both §6.2 `resuming` terminal bullets close by deferring to it), so it is the site that had to stop denying the reclaim, while §7.1 stays the single normative statement of the obligation and §7.2/§7.3/§6.2/§5.2 cite it — ALTERNATIVES: dropping the staged §6.2 clause and stating the reclaim only in §7.2 (smaller, but reverses round 1's recorded decision and leaves the bullet §6.2 declares authoritative for the edge showing a release with no reclaim); attaching the reclaim to §7.2 step 1 instead of step 3 (premise false, see FACT below); restating §7.1's "after the first pod-side RPC" scoping inline in step 3 (creates a second, drifting statement of one rule); rewording the preamble gloss to "the gateway has not completed the re-attach" instead of deleting it (asserts more than the sentence needs and re-opens what the pod holds, which step 3 deliberately does not answer); extending §6.2:235's four-name gloss to name the reclaim (makes the two §6.2 bullets asymmetric and re-files the identical finding against step 4 next round).

WATCHOUT: §7.2 step 3 must NOT claim an outcome for the pod. The staged wording this group was originally handed ended "so the replacement pod holds no state for the abandoned attempt", which is the same universal G1's finding 3 deleted from §7.1 in this same round as unsound without a new adapter discriminator. The resume path is where the falsifying ordering is likeliest, because the adapter's `Resume` admits the session and can reach `Runtime.Start` before the gateway observes the re-attach. Step 3 as landed states an ORDERING and no outcome. — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log.md:138; pkg/adapter/resume.go:50,:140-144

WATCHOUT: the §7.2 preamble at spec/07:210 carries the SAME overturned premise as step 3 four lines below it ("the agent runtime has not been started or reconnected" versus "no runtime was started on it"). Correcting one and leaving the other makes §7.2 contradict itself inside one block. Both are replaced in the one anchor. A later sweep for the phrase must check both. — EVIDENCE: spec/07_session-lifecycle.md:210,:214

FACT: on the mid-resume edge the reclaim rides the aborted attempt's own still-open connection, so step 3 (before the release) is the right place and step 1 is not. `Binder.Resume`'s adapter-RPC failure branch runs the compensation on the still-open connection under a detached context before `cl.Close()` and the release. — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md:378-386

FACT: neither §6.2 `resuming` terminal bullet enumerates §7.2's close sequence; both summarise it and neither names step 4's `coordination_generation` bump. That is what makes the sibling bullet's "abort / skip-seal / release-replacement-pod / run-terminal-handling" gloss a pointer rather than a list that must grow with the sequence, and it is the reason the sibling bullet needs no edit. — EVIDENCE: spec/06_warm-pod-model.md:234,:235; spec/07_session-lifecycle.md:215

FACT: both replaced strings are unique in spec/07_session-lifecycle.md and the phrase "Release the replacement pod" occurs nowhere else in spec/ or docs/, so the two anchors are safe.  — EVIDENCE: spec/07_session-lifecycle.md:210,:214

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: step S2 (line 8) now omits a fourth thing. It reads "§7.1's atomicity paragraph gains the failed-bind pod-side reclaim obligation, its `leaked` disposition, the no-retry rule, and the racing-start rule; §4.7.9 step 5 points at it." What is true after this round: SPEC-2 is its own §7.1 paragraph rather than an addition to the atomicity paragraph; the no-retry rule was withdrawn for §5.2's placement constraint; the racing-start rule moved to the accepted-failure-modes record; and SPEC-2 now also rewrites §7.2's mid-resume snapshot-close sequence (preamble premise clause and step 3). S2 should be rewritten once, naming §7.1, §7.2, §7.3, §6.2, §5.2 and §4.7.9. This adds one omission to a line an existing DEFERRED already owns; close them together.

USEFUL [spec.2.fix-G1, spec.2.fix-G2]: both hand-off notes named the exact sentences they had already rewritten inside the SPEC-2 and SPEC-3 blocks, which is what let this group confirm that the design's line numbers had drifted (the §7.1 block now ends at :220 and the §7.3 heading is at :222, against the design's :205/:206) and anchor on quoted strings instead without re-reading the whole file. G1's finding 3 in particular changed what step 3 was allowed to say, and the note said so. Keep doing this.

### [spec.2.fix-design-G1.1]

DECISION: the teardown precondition names no RPC at all, and is stated in one place. — BECAUSE round 1 moved the boundary from "the runtime has been given the session" to "`StartSession` the adapter has admitted"; the boundary was right and the RPC name was wrong, because `claimSessionSlotUnderLock` (the site that sets `st.started`) has three production callers: `session.go:111` (StartSession), `resume.go:50` (Resume), `sdkwarm.go:217` (ConfigureWorkspace). Naming all three would be the same defect one step weaker and would go stale on a fourth. §4.7's `Shutdown` row becomes the single home ("a session whose start the adapter has admitted", with one sentence saying which RPC starts a session depends on the pod's session mode and on whether the session is new or resumed); §4.1 states it by reference ("under the narrower precondition §4.7 states"); every other site says "a start", never "`StartSession`". — ALTERNATIVES: enumerate the three RPCs (rejected: closed enumeration, the recorded failure mode); revert to the shipped `bound entry` predicate (rejected: it reintroduces the runtime close for a credentials-bound-but-unstarted slot, which is the brick the split exists to stop).

DECISION: the §7.1 disqualification clause is DELETED from §7.1 and the rule lives only in §5.2's `**Max retries:**` bullet, scoped to the retries §5.2 places. — BECAUSE §7.1 stated a universal ("that pod carries no further attempt at the same session") and then enumerated its carriers; the enumeration cannot be completed, since a §15.1 `/start` onto a create-time-reserved slot is placed by neither carrier. §5.2 owns where a retry lands, `ExcludePod` is set at exactly one site inside `applySlotRetryPolicy`, and the rule's scope then equals its enforcement exactly. — ALTERNATIVES: add a third carrier that clears the row's §4.6 pod binding so a retried `/start` re-reserves (rejected: a new gateway store write, and it does not even guarantee a different pod, because `ClaimSlot` pass 1 still admits a claimed pod with free capacity); delete the constraint outright (rejected: barred by the standing-context trap "TRIED AND WITHDRAWN, and do not go the other way either").

DECISION: the §7.1 racing-start promise is DELETED rather than scoped, and the residue is recorded in the proposal's accepted-failure-modes list with its bound. — BECAUSE the sentence is an adapter guarantee living in the gateway's obligation paragraph, and the guarantee is partial: CODE-2's `noteRuntimeStarted` confirm catches only the ordering where the reclaim lands after the start's claim. A scoped promise invites reliance and re-raises the same question about the other half. — ALTERNATIVES: a per-pod tombstone of answered reclaims, checked in `claimSessionSlotUnderLock` (rejected here: new adapter state in files a later campaign position rewrites, and the proposal's own rejected-alternatives list already prices a tombstone at summary.md:154); requiring an admitting RPC to find a pre-existing entry instead of creating one (rejected: `Binder.Resume` sends `cl.Resume` as the FIRST adapter RPC on the replacement pod, so the precondition cannot be universal).

FACT: `Binder.Resume` reserves the slot and then calls `cl.Resume` with no preceding `FinalizeWorkspace`, so the adapter's `Resume` legitimately creates the slot entry. Any rule of the form "an admitting RPC must find an entry the workspace stage created" is false on the resume path. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1589-1607; contrast slotbinder.go:285-320 where `materializeSlot` always runs `FinalizeWorkspace` before `StartSession`.

FACT: a §15.1 `/start` onto a create-time-reserved slot is pinned to `row.PodAssignment` and never enters `applySlotRetryPolicy`; its failure keeps the row `ready` with `PodAssignment` intact, so a client retry re-enters `bindConcurrentSlot`'s reserved branch and returns to the same pod and the same slot id. Nothing in `pkg/gateway/sessionserver` clears `PodAssignment` on a failed start. — EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2604, :1157-1165; podsession/slotbinder.go:204-206, :210-224.

FACT: `SlotBindError` has no `Leaked` field in the tree today; `sbe.Leaked` throughout the proposal is staged, not shipped. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:58-68.

WATCHOUT: `Shutdown` deletes the registry entry under `s.mu` and runs `removeSlotTree` OUTSIDE the lock, so "the entry is gone" and "the tree is gone" are two different moments. Every claim about what a racing or retried bind observes has to say which of the two it means. — EVIDENCE: pkg/adapter/session.go:237-239, :270.

CORRECTS [review-log Standing context, "DECISION: the disqualification names its carrier per attempt kind."]: that bullet enumerates two carriers (§5.2's retry policy, §7.3's idle-inventory claim) and omits the §15.1 create-time-reserved start, which is placed by neither. The corrected decision is that §7.1 names no carrier and states no placement rule; §5.2's `**Max retries:**` bullet carries the rule for the retries it places, and the reserved-slot `/start` retry is recorded as an accepted failure mode rather than governed.

CORRECTS [review-log Standing context, "DECISION: the teardown precondition is reworded."]: "a session whose `StartSession` the adapter has admitted" is wrong on the SDK-warm and resume paths. True instead: "a session whose start the adapter has admitted", with no RPC named.

OPEN: non-spec-changes.md:244-245 says `pkg/adapter/session.go:163` is "the only site the compensation can race", because "`materializeSlot`'s start stage is `cl.StartSession`". §7.1's obligation covers the §7.3 re-attach, whose start stage is `cl.Resume` (binder.go:1607), so the reclaim can race `resume.go:144` too. Deliberately NOT pulled into this edit: adding a rollback to the resume path is a code-behaviour change, not a restatement. File it as its own finding.

UNVERIFIED: "the leaked slot's held occupancy keeps the reclaiming pod outside the pool's idle inventory", used to justify the §7.3 re-attach carrier. This design deletes the clause rather than verifying it; whoever wants it back must read `Binder.connect`'s pod selection.

### [spec.2.fix-design-G2.1]

DECISION: narrow the pre-`running` cleanup range to `receiving_uploads`-onward in FOUR sentences,
not one — spec-changes.md:328 (SPEC-3 append), spec-changes.md:71 (Design rationale),
summary.md:33 and summary.md:286 — using one identical phrase everywhere: "abandoned or fails
after its slot enters `receiving_uploads` and before it reaches `running`". BECAUSE §5.2's
scrub-model paragraph is the single home of the withheld-report rule, and a rule states its own
domain; SPEC-4 adds an edge only out of `receiving_uploads` and both spec-changes.md:353-355 and
the edge-case bullet at :119-122 say the `slot_assigned` case is deliberately left open.
ALTERNATIVES: (a) name the SPEC-4 edge instead of restating the range ("It also runs on the
`receiving_uploads → slot_cleanup` edge §6.2 carries") — rejected because a §5.2 reader would
have to jump to §6.2 to learn when the report is withheld, and §5.2 is where the rule lives;
(b) widen SPEC-4 with a `slot_assigned → slot_cleanup` edge so the wide wording becomes true —
rejected, it closes a hole this proposal explicitly scopes out, contradicts :353-355 and :119-122,
and cascades into DOCS-1 and CODE-3.

FACT: §4.7.9 step 5 is exactly `PrepareWorkspace → FinalizeWorkspace → RunSetup →
AssignCredentials(leases) → StartSession`. The connect stage (slot reservation) is NOT part of
step 5. So SPEC-4's prose paragraph at spec-changes.md:363 ("Every earlier stage of the §4.7.9
step-5 bind sequence ... leave the slot in `receiving_uploads`") is ALREADY correctly scoped and
must NOT be touched. EVIDENCE: spec/04_system-components.md:852; spec/06_warm-pod-model.md:151.

WATCHOUT: "pre-`running`" is an established term of art in this repository at a DIFFERENT level —
it means the session states `created`, `ready`, `starting`, `finalizing`. EVIDENCE:
spec/07_session-lifecycle.md:342; spec/15_external-api-surface.md:1125;
docs/reference/error-catalog.md:152. Naming `receiving_uploads` explicitly in §5.2 removes that
collision at the one place a §5.2 reader meets the rule. Do not "simplify" the corrected sentence
back to a bare "pre-`running`".

FACT: nothing in the tree is falsified by the narrowing. spec/06_warm-pod-model.md:150-156 has
only `slot_assigned ──→ receiving_uploads` out of `slot_assigned` today, and no doc or Go file
carries a "cleanup runs before `running`" claim. Re-verified by grep across spec/, docs/,
schemas/, pkg/ on 2026-09-09.

FACT: this fix has NO effect on the 0080 §1.19 fence-refusal predicate. It changes no code
deliverable and no bound/unbound set membership in `boundSlotState`/`checkSessionBound`
(pkg/adapter/slotsession.go:274-290); it is a scoping correction to §5.2 and proposal prose only.

DECISION: leave spec-changes.md:11 and summary.md:21 ("no edge into `slot_cleanup` from either
pre-`running` state") alone. They state the CURRENT defect, they are true today, and they stay
true after SPEC-4 closes one of the two. Likewise spec-changes.md:169,
implementation-checklist.md:12 and non-spec-changes.md:253, which use "pre-`running`" as the
NAME of the SPEC-4 paragraph or the CODE-3 edge rather than as a range claim.

OPEN: the SPEC-4 paragraph's bolded lead-in is "**Pre-`running` slot cleanup.**" and its closing
clause says "the report the pre-`running` reclaim withholds". Both are labels for a paragraph
whose body fixes the scope precisely, so neither is falsified. A later round may still decide the
label is loose given the session-level meaning of "pre-running" recorded in the WATCHOUT above.

### [spec.2.fix-design-G3.1]

DECISION: close the §7.2 gap with ONE new SPEC-2 anchor against spec/07 §7.2, carrying two verbatim
replacements — step 3 (spec/07:214) and the preamble's em-dash gloss (spec/07:210) — and KEEP the staged
§6.2 clause replacement at spec-changes.md:237 as it stands — BECAUSE §7.1 stays the single normative
statement and §7.2/§6.2/§7.3/§5.2 cite it, which is the round-1 recorded design (three one-sentence
pointers). ALTERNATIVES: (1) drop the §6.2 clause and state the reclaim only in §7.2, since §6.2's bullet
already closes "Full step-by-step sequence: see §7.2" — smaller, but it reverses a recorded round-1
decision and strips the reclaim from the section §6.2 declares authoritative for the edge; (2) the
reviewer's fallback of attaching the reclaim to step 1 and citing it from step 3 — rejected, two
statements of one ordering; (3) restating §7.1's "after the first pod-side RPC" condition inside step 3
— rejected as exception-clause hair, the two scopings would drift.

FACT: §7.2's five-step snapshot-close sequence is only PARTLY implemented. Steps 4 and 5 exist
(`bumpCoordinationGenerationOnSnapshotClose`, `recordSessionCompleted`); steps 1 and 3 have NO
implementation on the DELETE path — the handler writes the terminal state and bumps the generation and
does nothing pod-side. It does not cancel the in-flight resume, and it does not release the replacement
pod. So correcting step 3's text creates no new code obligation, and no code deliverable should be added
for it. EVIDENCE: pkg/gateway/sessionserver/sessionserver.go:2941-2958; pkg/api/v1/session/session.go:318-327;
pkg/gateway/sessionserver/start.go:3483-3510.

FACT: on the resume path the first session-naming pod-side RPC is `cl.Resume`. `b.connect` issues only
`NegotiateVersion` (names no session) and `reserveResumeSlot` is gateway/Redis/k8s-side. So there is a
real window inside `resuming` with a pod claimed and no reclaim owed, which is why §7.2 step 3 must take
§7.1's scoping by reference rather than assert the reclaim unconditionally. EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:1591-1607, :1674-1703.

FACT: the staged §6.2 clause's unconditional "the reclaim runs" is nonetheless safe, because the bullet
scopes itself to a cancel "arriving while the gateway is replaying the snapshot", i.e. the `Resume` RPC is
already in flight. EVIDENCE: spec/06_warm-pod-model.md:234.

FACT: BOTH §6.2 `resuming` bullets already omit step 4 (the `coordination_generation` bump) from their
inline sequences. That is the evidence that the sibling bullet's four-name gloss ("abort / skip-seal /
release-replacement-pod / run-terminal-handling") is a pointer rather than an enumeration, so it needs no
edit when the reclaim joins the sequence. EVIDENCE: spec/06:234, :235 versus spec/07:215.

WATCHOUT: §7.2's preamble carries the SAME overturned premise as step 3 — "the agent runtime has not been
started or reconnected" (spec/07:210). Fixing step 3 alone leaves the section denying, one paragraph
above, the reason step 3 now gives. The chosen edit deletes that em-dash gloss and asserts strictly less.
EVIDENCE: spec/07_session-lifecycle.md:210; pkg/adapter/resume.go:140-144.

WATCHOUT: `CODE-4`'s compensation uses `context.WithoutCancel(ctx)`, which is what makes the reclaim
survive a cancelled resume context. Any later edit that "simplifies" it back to the inbound ctx silently
deletes the mid-resume cancel case. EVIDENCE: non-spec-changes.md:307-312.

FACT: the phrases "no runtime was started on it", "half-claimed" and "has not been started or reconnected"
each appear exactly once in the whole tree (spec/07:214, :210 and spec/06:234's "half-claimed"), and
nothing in docs/, schemas/ or charts/ mirrors the §7.2 sequence. No doc or test cascade. EVIDENCE:
grep over spec/ docs/ schemas/ charts/ pkg/.

DEFERRED [proposals/0081_.../implementation-checklist.md]: S2 (line 8) enumerates SPEC-2's sites and now
also omits §7.2. It is already independently wrong per the standing DEFERRED against it; whoever rewrites
S2 for that DEFERRED should name §7.2's snapshot-close sequence alongside §7.1, §7.3, §6.2, §5.2 and
§4.7.9. Not landed here, to avoid an unreviewed rewrite of a line another correction owns.

USEFUL [Standing context / "The anchor sweep is done"]: saved re-verifying `#71-normal-flow` and
`#62-pod-state-machine`; both are in the verified set the new step-3 text reuses.

### [spec.2.review-applicability.1]

FACT: Nothing in the proposal changed between spec-r1 and spec-r2 except the review log's compaction pass. `diff -u` on every other file returns zero lines. So round 2's "read the changed sections hardest" instruction had no target and the whole staging had to be re-walked. EVIDENCE: scratchpad/cp-snap/0081/spec-r2 versus proposals/0081_.../ (all seven non-log files identical).

FACT: The anchor sweep still holds at commit f2a397b53. Every "text to replace" block in SPEC-1..SPEC-4 is byte-exact and occurs exactly once (`grep -cF` == 1 for all ten). Anchor lines: spec/04:157, :686, :854; spec/05:453, :545, :555; spec/06:152-153, :234; spec/07:23, :414. All nine markdown anchors the staged text mints resolve to real headings (spec/04:657 §4.7, :848 §4.7.9, :1099 §4.9; spec/05:365 §5.2; spec/06:78 §6.2; spec/07:3 §7.1, :115 §7.2, :378 §7.3; spec/15:614 §15.1, :1686 §15.4.2). Do not re-run this sweep.

WATCHOUT: §6.2's mid-resume cancel bullet is NOT the authority for its own action sequence. It ends "Full step-by-step sequence: see [§7.2] 'Mid-resume terminal transitions — snapshot-close semantics'", and §7.2:210-215 carries a five-step numbered close sequence whose step 3 (spec/07:214) reads "The half-claimed replacement pod is released back to the pool via the standard pod release path — no runtime was started on it, so no scrub beyond the pool's default post-session scrub is required." SPEC-2 adds a reclaim step to the §6.2 summary and edits neither §7.2 step 3 nor the step list. §7.2 appears nowhere in the proposal (`grep -n "7\.2" *.spec-changes.md` returns only the "§7.1"/"§7.3" matches). EVIDENCE: spec/06_warm-pod-model.md:234; spec/07_session-lifecycle.md:210,:214; proposals/0081_.../spec-changes.md:237,:377-379.

FACT: `Binder.Resume` claims its replacement pod through `b.connect` → `podclaim.Claimer.Claim`, which skips every Sandbox whose `Status.Phase != state.Idle` (plus a same-tenant `reserved` rebind). It never calls `ClaimSlot`, so it has no pass over already-claimed pods. That is what makes the staged §7.1 sentence "the claim of a replacement pod from the pool's idle inventory, which the leaked slot's held occupancy keeps the reclaiming pod outside" true: a pod holding a leaked slot has nonzero occupancy, is `claimed`, and is therefore not a candidate. Closes the standing UNVERIFIED "Resume path and the exclusion". EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1592,:1737-1768; pkg/gateway/podlifecycle/podclaim/claimer.go:118-128.

FACT: The §6.2 mid-resume cancel edge cannot fire before a pod-side RPC. The bullet defines it as "arriving while the gateway is replaying the snapshot" and its first action is "The in-flight restoration RPCs are cancelled", so the §7.1 obligation's "first pod-side RPC" precondition is satisfied by the edge's own definition. Do not file the §6.2 clause as unscoped relative to §7.1; that is the finding I chased and dropped. EVIDENCE: spec/06_warm-pod-model.md:234; spec/07_session-lifecycle.md:211.

MISTAKE: the standing-context refutation of the "On the default disposition the pod is replaced" finding asserts "The same sentence also appears verbatim at spec/29_communication-scenarios.md:696". It does not. §29 item 12 is a paraphrase ("the graceful end-of-session shutdown of the pod's runtime. On the default disposition the adapter closes the session runtime and the pod is replaced"), and §29's preamble (spec/29:23-25) makes a trace non-normative where it disagrees with the cited section. The refutation's conclusion still stands; its evidence does not. EVIDENCE: spec/29_communication-scenarios.md:693-701,:23-25.

UNVERIFIED: whether tier 11 actually hard-fails on the `docs/reference/adapter-contract.md:75` drift SPEC-1/SPEC-3 create. Five lenses have filed it as a DEFERRED docs site; nobody has run the gate. The non-spec loop should.

### [spec.2.review-citations.1]

FACT: The proposal text did not change at all between round 1 and round 2. `diff -ru scratchpad/cp-snap/0081/spec-r2 proposals/0081_...` shows a single hunk, and it is the review-log compaction pass. Every SPEC-1..SPEC-4 block, the Design section and the edge-case bullets are byte-identical to what round 1 reviewed. A round-2 lens gets no "newest text" to read hardest; the whole document is equally aged. EVIDENCE: /tmp diff over scratchpad/cp-snap/0081/spec-r2.

USEFUL [Settled / "The anchor sweep is done; do not repeat it"]: I re-ran it anyway on the two anchors the standing entry does not name by line (spec/06:234 the mid-resume cancel clause, spec/07:414 the resume-flow item 4) plus all six verbatim blocks, and every one still matches byte for byte at spec/04:157, :686, :853; spec/05:453, :545, :572; spec/06:148-157, :234; spec/07:23, :414. The entry is accurate and the sweep is genuinely finished. Do not spend a fourth agent on it.

FACT: `#49-credential-leasing-service`, `#151-rest-api` and `#73-retry-and-resume` — the three anchors minted after the standing entry's six-anchor sweep — all resolve. `### 4.9 Credential Leasing Service` (spec/04:1099), `### 15.1 REST API` (spec/15:614), `### 7.3 Retry and Resume` (spec/07:378).

FACT: SPEC-3's two new action-list items are anchored in the spec, not only in the code. The credential path `/run/lenny/slots/{sessionId}/credentials.json` is spec/05's own recycle-lifecycle spelling, and the §4.9 direct-mode timer is spec/04:1169: "In direct delivery mode, the adapter MUST set a local timer for each credential lease's `expiresAt`." The code halves also hold: slotlayout.RemoveTree sweeps `p.CredentialsDir` (pkg/adapter/slotlayout/tree.go:59-61) and `deregisterSlotLocked` cancels every armed timer (pkg/adapter/slotsession.go:175-179).

CORRECTS [Open / "Resume path and the exclusion"]: closed in the proposal's favour. `Binder.Resume` claims through `b.connect`, and `connect` builds a `podclaim.Claimer` and calls `claimer.Claim(ctx, podclaim.ClaimRequest{...})` — the whole-pod idle claim — with `podclaim.ErrNoIdlePod` falling through to the Postgres fallback claim. It never calls `ClaimSlot`, so pass 1's same-tenant-claimed-pod scan is not on the resume path and a pod holding a leaked slot (occupancy nonzero, claim `bound`, phase `claimed`) is outside the inventory `Claim` reads. The staged §7.1 clause "the claim of a replacement pod from the pool's idle inventory, which the leaked slot's held occupancy keeps the reclaiming pod outside" is true as written and needs no `ExcludePod` on `connect`. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1737-1768.

DECISION: filed exactly one finding, the unstaged §7.2 mid-resume sequence — BECAUSE §6.2's cancel bullet, which SPEC-2 edits, ends "Full step-by-step sequence: see [§7.2] 'Mid-resume terminal transitions — snapshot-close semantics'", and that sequence's step 3 (spec/07:214) both omits the reclaim SPEC-2 adds and justifies the omission with "no runtime was started on it, so no scrub beyond the pool's default post-session scrub is required" — the exact predicate SPEC-1/SPEC-3/SPEC-4 exist to overturn. `grep -n "7\.2" spec-changes.md summary.md` returns nothing, so §7.2 is in no edit list. ALTERNATIVES: I considered and dropped four others, listed below.

WATCHOUT: §7.2's snapshot-close sequence is the full sequence for BOTH §6.2 mid-resume bullets, not only the cancel one. SPEC-2 deliberately leaves the `resuming → completed` bullet unedited because it says "The same abort / skip-seal / release-replacement-pod / run-terminal-handling sequence applies as for the cancel edge above" — but that phrase names §7.2's numbered steps, so the by-reference argument routes through the same unstaged text. Fixing §7.2 step 3 discharges both bullets at once; editing only §6.2's completed bullet would not. EVIDENCE: spec/06_warm-pod-model.md:234,:235; spec/07_session-lifecycle.md:210-216.

MISTAKE (mine, nearly filed): "§6.2's pre-attached failure disposition says the pod is 'marked `failed` and released back to the pool (or terminated if unhealthy)' (spec/06:283), so the staged §7.1 clause 'the pod retires ... so the reclaim's residue does not outlive the pod' is a false citation and the exclusive-pod carve-out is unsound." It is already refuted: §4.6.1's projection turns a claim recording the terminal disposition `failed` into `draining → terminated`, spec/05:455 says a session that ends in failure always retires its pod, and `failPhase`'s doc comment says the pod "is retired by draining it". The standing-context Open entry "spec/05 versus spec/06 on failed-session pod retirement" already books the wording tension as pre-existing. Do not re-derive this; it costs two verifiers and closes nothing.

MISTAKE (mine, nearly filed): "the Design section says §7.1's atomicity paragraph 'closes by stating that the client never receives a `session_id`'" — that sentence is mid-paragraph and three more sentences follow it (spec/07:23 ends "...regardless of the flag."). The drift does not change the design's argument (that neither the steps-2-8 scoping nor the no-`session_id` statement holds for a re-attach), and SPEC-2's own insertion instruction quotes the real closing sentence correctly. Below the bar.

UNVERIFIED: whether SPEC-3's widened §5.2 action list should also name the `/sessions/{sessionId}` and `/artifacts/{sessionId}` trees. `slotlayout.RemoveTree` removes four trees — slot root, Sessions, Artifacts, CredentialsDir — and the widened list names two of them plus the timers. The omission predates the proposal (the shipped list named only the workspace directory), so I judged it incompleteness rather than a defect, but a completeness lens may disagree. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68; spec-changes.md:307.

OPEN: §29 item 12 (spec/29:693-701) enumerates the `Shutdown` triggers as terminate / DELETE / expiry and gains no fourth trigger from SPEC-2. I did not file it because §29's own preamble subordinates a trace to the section it cites ("Where a trace and a cited section disagree, the cited section is the normative statement and the trace is the defect", spec/29:23-25) and two earlier lenses judged it optional. Somebody should decide once whether "incomplete enumeration" counts as "disagree" under that rule, and record the answer, because it keeps coming back.

### [spec.2.review-client-surface.1]

DECISION: returned an empty findings list — BECAUSE every client-facing parallel of the four staged edits is
either untouched, already staged (DOCS-1), or an already-recorded DEFERRED whose remedy is in a file this
loop cannot edit (`schemas/lenny-adapter.proto` under programme rule S-2, `docs/reference/adapter-contract.md`
in the docs lane) — ALTERNATIVES: I worked up and dropped four candidates, each recorded below with the
evidence that killed it, so the next lens does not re-derive them.

FACT: the staged spec-changes.md is byte-identical to the round-2 snapshot; only the review log changed in the
last fix round. `for f in scratchpad/cp-snap/0081/spec-r2/*; do diff -q ...; done` reports only
`review-log.md`. So round 2's spec text is round 1's text, and every round-1 refutation still applies verbatim.

FACT: the CH-RUNTIMEOPS `terminate` frame carries `type`, `deadlineMs`, `reason` only, with
`additionalProperties: false` and no session field, so SPEC-1's "the signal is pod-global and names no
session" is true on the wire, and `drainReason` normalises any unrecognised `ShutdownRequest.reason` (a
reclaim's `slot_bind_failed`, §11.4's `USER_REVOKED`) to `session_complete`, so no reclaim reason can emit an
out-of-enum frame. EVIDENCE: schemas/runtime-ops-events.schema.json:174-185; pkg/adapter/session.go:309-321.

UNVERIFIED (pre-existing, code lane, NOT this proposal's): the same `terminate` frame requires `deadlineMs`
with `minimum: 100`, but `RuntimeOps.Terminate` serialises it `omitempty`, and every shipped gateway caller
passes 0 (`Shutdown(ctx, sessionID, "", 0)`), so the frame a Full-level runtime receives at an ordinary
session end omits a required field. The staged compensation reuses the same call with 0. Whoever owns the
tier-3 runtime-ops contract test should confirm. EVIDENCE: schemas/runtime-ops-events.schema.json:180;
pkg/adapter/runtimeops.go:65,:486-490; pkg/gateway/podlifecycle/podsession/slotbinder.go:542;
pkg/gateway/podlifecycle/podsession/binder.go:2043.

USEFUL [Settled · "the disqualification names its carrier per attempt kind"]: I nearly filed the §7.1 clause
"for a §7.3 re-attach, the claim of a replacement pod from the pool's idle inventory, which the leaked slot's
held occupancy keeps the reclaiming pod outside" as false, on the theory that `SlotClaimer.ClaimSlot`'s pass 1
places new slots on *claimed* same-tenant pods and a leaked slot leaves spare capacity. It is true: the resume
path never reaches `ClaimSlot`. `Binder.Resume` calls `connect`, which uses `podclaim.Claimer.Claim` (the
whole-pod idle/reserved-hold claim), and only then reserves a counted slot on that already-claimed pod via
`ReserveSlotOnPod`. A pod holding a leaked slot has occupancy > 0, so it is neither `idle` nor `reserved`.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1606,:1700-1720,:1737-1768.

FACT: the §5.2 slot retry policy (`applySlotRetryPolicy`, the only consumer of the staged `ExcludePod`) runs on
one path — a row with no live `PodAssignment` (the §7.3 resume-rebuild or a slotless row) — and returns the
exhaustion sentinels unwrapped for the `WARM_POOL_EXHAUSTED` mapping. The create-time reservation and the
`/start` reconnect to a create-reserved slot have no retry budget at all: `classifySlotBindFailure` answers the
§5.2 `SLOT_FAILED` envelope, and a create-time `ErrNoConcurrentSlot` is translated to `SESSION_CREATION_FAILED`
rather than `WARM_POOL_EXHAUSTED`. Read this before filing on "the attempt keeps its retry" or on which client
error an exclusion-induced exhaustion produces. EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2610,
:2680-2690, :2735-2736, :2807-2825, :2148-2166.

FACT: the `details.reason: "concurrent_slots_exhausted"` gloss ("pods exist but all slots are full") is already
imprecise before this proposal — `ClaimSlot` returns `ErrNoConcurrentSlot` whenever the pool list is non-empty
and no candidate is placeable, including candidates skipped by `expiredByUptime`. Adding one more skip reason
does not falsify a gloss that already over-claims. Confirms the standing trap; do not file it.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:346-357,:429-437,:505-522.

FACT (client-surface sweep, done — do not repeat): nothing the four edits touch reaches a client artifact.
The per-slot sub-states are never returned by REST ("Internal-only states ... are never returned by the REST
API", pkg/api/v1/session/session.go:9-13); `docs/runtime-author-guide/lifecycle.md:34-43` is a *pod-level*
table in a different vocabulary (`finalizing_workspace`, `starting_session`, `attached`) with no cleanup edge,
so SPEC-4 does not reach it; `spec/28` mentions `Shutdown` nowhere and the CH-RUNTIMEOPS card states no send
precondition for `terminate`, so SPEC-1's new condition contradicts no card; `spec/29:696` (step 12) and
`spec/29:220-300` (steps 14-25) state the ordinary session end and the bind sequence without a failure branch,
so SPEC-2 falsifies neither. The credential path SPEC-3 mints is the repo-wide one
(`/run/lenny/slots/{sessionId}/credentials.json`, spec/06:26, spec/13:26) and `slotlayout.RemoveTree` really
removes that directory (pkg/adapter/slotlayout/tree.go:47-68).

FACT: all nine markdown anchors the staged text mints resolve, including the three the earlier anchor sweep did
not list (`#49-credential-leasing-service` from `### 4.9 Credential Leasing Service` spec/04:1099,
`#151-rest-api` spec/15:614, `#73-retry-and-resume` spec/07:378). Every "text to replace" block is present
exactly once in the tree (spec/04:157, :686, §4.7.9 step 5; spec/05 scrub-model, slot-cleanup action list,
`**Max retries:**`; spec/06 mid-resume cancel clause, the `receiving_uploads ──→ running` fence entry;
spec/07:23, :414). Re-run only if the spec moves.

### [spec.2.review-docs-alignment.1]

FACT: Round 2 opened with the spec staging byte-identical to the round-1 snapshot. `diff -u` over
every proposal file except the review log returned empty; only `review-log.md` changed (compaction
pass 1). EVIDENCE: scratchpad/cp-snap/0081/spec-r2/*.md vs proposals/0081_.../*.md
DECISION: filed one finding, on the client-visible outcome when the §7.1 disqualification empties the
candidate set — BECAUSE the staged §7.1 sentence asserts unconditionally that "the mechanism that
retries it places it on another pod" (spec-changes.md:200) while the WARM_POOL_EXHAUSTED outcome for
the no-other-pod case lives only in the proposal's own commentary (spec-changes.md:105-106, :270-274)
— ALTERNATIVES: rejected filing the `concurrent_slots_exhausted` gloss (spec/05:549) on its own,
because the gloss is already loose today (`ErrTenantMismatch` maps to the same reason,
pkg/gateway/sessionserver/podclaimerror_internal_test.go:88) and a gloss-only finding is pre-existing
imprecision.
FACT: §5.2's two stated routes to `WARM_POOL_EXHAUSTED` are both conditioned on capacity, not on
placement policy ("all pods ... have reached their `maxConcurrentSessions` slot limit"; "no warm pods
are available"), and the `**Client error on exhaustion:**` bullet is a closed two-way disjunction
(non-retryable category, or retry budget exhausted). Neither covers a retry that cannot be placed
because its only candidate is disqualified. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:549,
:562
FACT: `Binder.Resume` claims its replacement through `claimer.Claim` (exclusive idle claim), not
through `ClaimSlot`, so the staged §7.1 clause "the claim of a replacement pod from the pool's idle
inventory, which the leaked slot's held occupancy keeps the reclaiming pod outside" matches the code.
Do not file it. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590,:1756
FACT: the tier-11 per-slot gate slices §6.2 as `scopedBlock = [scoped header, general header)` and
`generalBlock = [general header, end of §6.2)`, and only forbids the four *general* edges from
appearing inside the scoped block. SPEC-4's new `receiving_uploads ──→ slot_cleanup` edge lands in the
general block and DOCS-1's row lands in the doc's `Per-slot sub-states` section, so both pass
unchanged. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:56-74,:88-118
FACT: SPEC-1's narrowing of the runtime teardown does NOT collide with §28's `FINAL_USAGE_REPORT`
preconditions. §28 states only ordering ("after every in-flight `ReportUsage` pull has settled", "as
the last message before the stream closes"), never which sessions send one, so no §28 edit site opens.
Two candidate findings died here. EVIDENCE: spec/28_communication-channels.md:441,:456
FACT: spec/29's `Shutdown` mention (§29.4 item 12) is scoped to "a session end triggered by
`terminate`, `DELETE`, or an expiry timer", and §29.2's atomicity restatement (step 10) covers steps
2–10, which precede the first pod-side RPC. Neither is falsified by SPEC-1 or SPEC-2, so §29 is not a
missed edit site. EVIDENCE: spec/29_communication-scenarios.md:199-202,:695-696
USEFUL [standing context / "Dead end" list]: the recorded dead ends on the withheld report, the
`leaked` occupancy hold, and the adapter-side leak visibility each closed a line of enquiry in one
read. Two of them were lines I had already started down.

### [spec.2.review-edit-sites.1]

FACT: `spec-changes.md` is byte-identical to the round-2 snapshot (`diff -ru scratchpad/cp-snap/0081/spec-r2 proposals/0081_.../` returns only review-log additions). The only change this round was the review-log compaction pass. So no fix-stage spec text was newer than the last review; the whole staging had equal age. EVIDENCE: scratchpad/cp-snap/0081/spec-r2/0081_....spec-changes.md vs proposals/0081_.../0081_....spec-changes.md

FACT: every "text to replace" block in SPEC-1..SPEC-4 still matches the tree byte for byte, and all nine minted markdown anchors (`#47-runtime-adapter`, `#52-...`, `#1542-...`, `#73-retry-and-resume`, `#151-rest-api`, `#62-pod-state-machine`, `#71-normal-flow`, `#49-credential-leasing-service`, `#479-...`) already appear in spec/ (1..171 uses each). Re-confirmed on 2026-09-09; do not re-run this sweep unless the spec moves. EVIDENCE: spec/04:157,:686,:853; spec/05:453,:545,:555; spec/06:152,:234; spec/07:23,:414

FACT: the §6.2 fence's column geometry for the new SPEC-4 edge is correct as staged — annotation "(" lands at column 41 on both the existing `receiving_uploads ──→ running` entry and the staged one, and the continuation lines carry 41 leading spaces. EVIDENCE: spec/06_warm-pod-model.md:151-155

FACT: `slotlayout.EnsureTree` creates FIVE directories per slot (`current/`, `staging/`, `/sessions/{sessionId}/`, `/artifacts/{sessionId}/`, `/run/lenny/slots/{sessionId}/`) and `RemoveTree` removes FOUR trees (`/workspace/slots/{sessionId}`, `/sessions/{sessionId}`, `/artifacts/{sessionId}`, `/run/lenny/slots/{sessionId}`). §5.2's action list — the list SPEC-1 makes canonical for the slot release and SPEC-3 rewrites — names only the workspace directory and (after SPEC-3) the credential directory. §6.4 names all three trees and defers their removal to §5.2. Neither the §5.2 whole-pod scrub steps 1-6 nor step 0 sweeps `/sessions/` or `/artifacts/`, so the per-slot cleanup is the only spec'd reclaim for them. EVIDENCE: pkg/adapter/slotlayout/tree.go:24-45,:48-69; spec/06_warm-pod-model.md:365-379,:386; spec/05_runtime-registry-and-pool-model.md:461-471

FACT: §7.2's "Mid-resume terminal transitions — snapshot-close semantics" (spec/07:210-216) is the authoritative five-step sequence for BOTH `resuming → cancelled` and `resuming → completed`, and §6.2's cancel bullet — the one SPEC-2 edits — points at it for "Full step-by-step sequence". Its step 3 rationale ("no runtime was started on it, so no scrub beyond the pool's default post-session scrub is required") is the exact premise this proposal overturns. The proposal never mentions §7.2 in any file. EVIDENCE: spec/07_session-lifecycle.md:210,:214; spec/06_warm-pod-model.md:234-235

FACT: `Binder.Resume` claims through `b.connect`, which calls `podclaim.Claimer.Claim` (whole-pod, `ErrNoIdlePod`) rather than `ClaimSlot`. So the staged §7.1 clause "a §7.3 re-attach ... claims a replacement pod from the pool's idle inventory, which the leaked slot's held occupancy keeps the reclaiming pod outside" IS true against the code: a pod holding a leaked slot has nonzero occupancy, is `claimed`, and is not idle. This closes the standing-context UNVERIFIED "Resume path and the exclusion". EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1737-1768

DECISION: did NOT file "§5.2's `**Slot cleanup:**` bullet still says the adapter reports EACH slot cleanup outcome, contradicting SPEC-3's no-report rule" — BECAUSE it is the general-rule-plus-stated-exception pattern, the exception sits in the same section and explicitly forward-identifies the bullet's cleanup, and a near-variant ("SPEC-3's no-report exception is narrower than the `started` gate") was already refuted as wording-scope polish whose fix changes no code, no test and no behaviour. ALTERNATIVES: filing it as a bookkeeping defect on SPEC-3's closing note (which enumerates the bullet's trigger, formula, CRD rule and leaked outcome as unchanged and silently omits the one component the same edit contradicts) — same refutation risk, dropped.

DECISION: did NOT file "the staged §7.1 says an exclusive pod 'retires under the §6.2 pre-attached failure disposition' while §6.2:283 says the pod is 'marked `failed` and released back to the pool (or terminated if unhealthy)'" — BECAUSE §6.2's own coarse fence at spec/06:95 and :114 does support retirement on a failed claim disposition ("terminal claim disposition released or failed"; "a failed session"), the §6.2-prose-versus-fence conflict is the pre-existing OPEN item the standing context already records, and `failPhase` implements the fence's reading. ALTERNATIVES: filing as a false citation — the section-level citation resolves against the fence, so it would be refuted.

DECISION: did NOT file §29 item 12 (`Shutdown` enumerated only for terminate/DELETE/expiry) or §29:200 ("a failure ... rolls back the pod claim") — BECAUSE §29's own preamble declares a trace non-normative ("Where a trace and a cited section disagree, the cited section is the normative statement and the trace is the defect"). Two earlier lenses reached the same conclusion. EVIDENCE: spec/29_communication-scenarios.md:22-24

WATCHOUT: `docs/reference/adapter-contract.md:75` describes `Shutdown` as unconditionally flushing the final usage report, closing the runtime, removing the slot tree AND reporting through `ReportSessionScrub`, and `:81` says `ReportSessionScrub` is filed "at each session release, on a pod of any concurrency". Both are falsified by SPEC-1 and SPEC-3. It is a docs surface, so it is out of this loop's scope, and it is already the merged standing-context DEFERRED. Do not re-file it here; the non-spec loop owns it.

WATCHOUT: spec/11_policy-and-controls.md:263 step 3 ("The pod's runtime adapter initiates graceful shutdown (SIGTERM to agent, wait up to 10s, then SIGKILL)") sits under a list whose step 1 scopes it to "all active sessions", while the §11.4 Note says "non-terminal sessions". After CODE-1 a bound-but-unstarted session's `Shutdown` runs no runtime teardown. I judged the "active sessions" scoping enough to keep §11.4 true and did not file, but a later round that widens the revoke's scope should re-check this line.

UNVERIFIED: whether SPEC-3's new sentence "That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency" turns the pre-existing `slot_cleanup ──→ leaked` scoping asymmetry (fence scopes it to concurrent occupancy; the bullet's leaked outcome does not) from an implicit ambiguity into an explicit one. Three earlier lenses judged the asymmetry structural and below the bar, and the standing context files it as a separate finding, so I left it. Someone owning that separate finding should decide whether SPEC-3 changes its status.

### [spec.2.review-feasibility.1]

FACT: `st.started` is set by THREE adapter RPCs, not one. `claimSessionSlot` is called from `pkg/adapter/session.go:111` (`StartSession`), `pkg/adapter/resume.go:50` (`Resume`) and `pkg/adapter/sdkwarm.go:217` (SDK-warm `ConfigureWorkspace`), and `claimSessionSlotUnderLock` sets `st.started = true` at `pkg/adapter/slotsession.go:88` on every one. The standing-context entry "`st.started` … set only in `claimSessionSlotUnderLock`" is true but is easy to misread as "only the `StartSession` path". EVIDENCE: pkg/adapter/slotsession.go:52,87-88; resume.go:50; sdkwarm.go:217; session.go:111.
CORRECTS [Settled · "DECISION: the teardown precondition is reworded"]: rewording the teardown precondition to "a session whose `StartSession` the adapter has admitted" narrows a predicate the code implements as `st.started`, which `Resume` and SDK-warm `ConfigureWorkspace` also set. Under the reworded spec a resumed session's `Shutdown` skips the usage flush, the §15.4.2 drain signal and `Runtime.Close` while its runtime is live. Filed this round.

FACT: the §15.1 start transition on a concurrent pool does NOT go through §5.2's slot retry policy. `bindConcurrentSlot` sends a row with a non-empty `PodAssignment` to `BindReservedSlot`, which is explicitly outside the retry policy and pinned to the create-time pod; only a slotless or recovery row reaches `bindSlotWithRetry`. On failure the row stays `ready` and the client retries `/start` onto the same pod and the same slot tree. EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2604, :1160-1164; pkg/gateway/podlifecycle/podsession/slotbinder.go:204-206.
WATCHOUT: the staged §7.1 promise "that pod carries no further attempt at the same session" has exactly one enforcement site in the whole staging — `applySlotRetryPolicy`'s retry iteration (`ExcludePod`, non-spec-changes.md:443). Anything that reaches the pod by another route (BindReservedSlot's pinned `row.PodAssignment`, `Binder.Resume`'s `b.connect`) is unfenced. EVIDENCE: non-spec-changes.md:443; spec-changes.md:200.

FACT: `Claimer.Claim` scans `Status.Phase == Idle` only, and `SlotClaimer.ReleaseSlot(leaked=true)` never deletes the per-pod claim, so §7.1's "the leaked slot's held occupancy keeps the reclaiming pod outside [idle inventory]" does hold for the §7.3 re-attach. Verified, not a finding. EVIDENCE: pkg/gateway/podlifecycle/podclaim/claimer.go:124-128.

FACT: §7.2's "Mid-resume terminal transitions" step 3 (spec/07:214) is the authoritative full sequence that §6.2's mid-resume cancel bullet (spec/06:234) defers to, and it is in no edit list. It states the half-claimed pod "is released back to the pool via the standard pod release path … no runtime was started on it, so no scrub beyond the pool's default post-session scrub is required" — the exact claim SPEC-2's §6.2 clause falsifies. Filed this round.

UNVERIFIED: on the §7.3 re-attach the gateway never releases the replacement pod's claim on an ordinary resume failure. `Binder.Resume`'s error branch does `cl.Close()` + `releaseResumeSlot` (a no-op when `slotID == ""`, i.e. every exclusive pool), `resumeOnPod` returns the error unchanged, and `holdOrFailOnResumeError` only rewrites the row. So §7.1's exclusive-pod sentence ("the failed attempt releases the pod's claim and the pod retires") is discharged only by §6.2's general pre-attached failure retry policy, never by a staged code path. NOT filed — §6.2:283 states the release normatively, so the gap reads as pre-existing and code-lane — but somebody on the code loop should decide whether CODE-4 owes the release. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1621-1630,:1710-1713; pkg/gateway/sessionserver/start.go:4040-4042,:3609-3620; spec/06_warm-pod-model.md:283.

USEFUL [Traps · "Do not read the reclaim as scoped away from pods that retire"]: saved a wasted pass on the "released or reused rather than terminated" sentence.
USEFUL [Settled · "The anchor sweep is done; do not repeat it"]: skipped the whole verbatim-anchor re-derivation.

### [spec.2.review-fresh.1]

FACT: round 2 changed NOTHING outside the review log. `diff -ru scratchpad/cp-snap/0081/spec-r2 proposals/0081_...` reports only `*.review-log.md` (the compaction pass). spec-changes.md, non-spec-changes.md, summary.md, problem-statement.md and the checklist are byte-identical to the r1 snapshot, so "read the changed sections hardest" had no target this round. EVIDENCE: scratchpad/cp-snap/0081/spec-r2/*.md versus proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/*.md.

DECISION: filed two findings, both against SPEC-3's appended scrub-model sentences, both about the append's scope reaching further than the rest of the proposal. BECAUSE the append is the only staged text that positively identifies the §5.2 `**Slot cleanup:**` bullet's cleanup with the new abandoned-bind path, and that identification pulls two of the bullet's/machine's existing statements into contradiction. ALTERNATIVES considered and dropped below the bar: (a) §4.1's retained "per-slot teardown and the whole-pod teardown are the same operation" naming three-way drift after SPEC-1 — vocabulary, no behaviour turns on it; (b) SPEC-4's prose defining `running` by "the runtime has not yet acknowledged" while equating it with the retained fence trigger "session dispatched to runtime" — a two-instruction window between `Runtime.Start` returning and `noteRuntimeStarted`, too fine; (c) the staged §4.7 row losing the slot-release-after-runtime-close ordering the shipped comment calls security-relevant (credential file inside the §15.4.2 grace window) — the spec never stated the ordering normatively, so it is an addition rather than a correction.

FACT: `SocketRuntimeProcess.Start` has no runtime acknowledgement step at all — it adds the session to the active set and returns; nothing waits for the runtime to confirm. So the staged §6.2 prose's "whose session the runtime has not yet acknowledged" names a handshake the platform does not have. It reads coherently only if "acknowledged" is taken as a synonym for "been given". EVIDENCE: pkg/adapter/socketruntime.go:181-220.

FACT: the §7.3 re-attach really does claim from idle inventory, closing the standing OPEN "Resume path and the exclusion". `Binder.Resume` calls `b.connect`, which uses `podclaim.Claimer.Claim`, not `SlotClaimer.ClaimSlot`; `Claim` skips any Sandbox whose `Status.Phase != Idle`. A pod holding a leaked slot has nonzero occupancy and projects `claimed`, so it is outside the candidate set with no `ExcludePod` needed. The staged §7.1 clause "the claim of a replacement pod from the pool's idle inventory, which the leaked slot's held occupancy keeps the reclaiming pod outside" is TRUE. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1600,:1737-1760; pkg/gateway/podlifecycle/podclaim/claimer.go:126.

FACT: §6.2's pre-attached failure disposition ("The pod is marked `failed` and released back to the pool") does not contradict the staged §7.1 concurrent branch, because §5.2's "Slot failure and cleanup (`maxConcurrentSessions > 1`)" heading opens "Slots fail independently — a single slot failure does not terminate the pod or affect other active slots", which scopes §6.2's pod-level disposition away from a concurrent pod. And `failed ──→ draining ──→ terminated` in §6.2's own fence is why "the pod retires" is a fair gloss of "marked failed" on the exclusive branch. Two lenses could burn a round on this pair; it resolves. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:542; spec/06_warm-pod-model.md:101-102,:283.

FACT: `drainReason` maps the compensating `Shutdown`'s reason ("slot_bind_failed") to `session_complete`, because the §28.5.3 `terminate` enum is the closed four-value set and the default arm swallows anything else. So a started-but-abandoned bind tells the runtime the session completed. Left unfiled: no spec sentence becomes false (the enum list and §29 item 13 still describe what goes on the wire), and the standing context already carries it as an undecided OPEN. EVIDENCE: pkg/adapter/session.go:314-321; spec/28_communication-channels.md:1082; spec/29_communication-scenarios.md:705-708.

USEFUL [Standing context / "The anchor sweep is done; do not repeat it"]: I re-ran the six verbatim anchors anyway (one grep, ~30 seconds) and every one still matches byte for byte at spec/04:157,:686,:854; spec/05:453,:545,:555; spec/06:151-156,:234; spec/07:23,:414. The entry is correct and the re-check is cheap enough to keep doing.

UNVERIFIED: whether the SPEC-3 append's "on a pod of either concurrency" also drags the bullet's "If cleanup fails, the slot is leaked" sentence to `maxConcurrentSessions: 1`, where the staged §7.1 says the `leaked` sub-state is not available. I judged this the same pre-existing asymmetry three earlier lenses already dropped and did not file it, but the append is new text and someone should decide whether it changes the answer. EVIDENCE: spec-changes.md:328 versus spec-changes.md:150 (§7.1 sentence 6) and spec/05:545.

### [spec.2.review-kubernetes.2]

DECISION: filed exactly one finding this round — §7.2's mid-resume close sequence is an unstaged edit site
for SPEC-2's §6.2 `resuming → cancelled` clause. BECAUSE §6.2:234 ends with "Full step-by-step sequence: see
[§7.2] 'Mid-resume terminal transitions — snapshot-close semantics'", so §6.2's bullet is a summary and §7.2
is the designated authoritative sequence; SPEC-2 edits the summary and leaves the sequence alone.
ALTERNATIVES: rejected filing on §29 item 12 (already an OPEN judged optional), on the exclusive-pod
retirement claim (see the FACT below, the fence supports the proposal), and on cross-replica carriage of the
§7.1 pod disqualification (speculative, and §5.2 already treats a client resubmit as outside the retry policy).

FACT: §6.2's pod-level fence carries `failed ──→ draining (failed pod reclaimed and replaced)` and
`draining ──→ terminated`, with no `failed → idle` edge. So the staged §7.1 sentence "the pod retires under
the §6.2 pre-attached failure disposition" IS supported by §6.2, even though §6.2:283's prose says the pod
"is marked `failed` and released back to the pool (or terminated if unhealthy)". Do not file the exclusive-pod
residue claim on the strength of :283 alone; the fence is the governing statement and it retires the pod.
EVIDENCE: spec/06_warm-pod-model.md:101-102, :283.

FACT: §7.3's re-attach really does claim from idle inventory, so the staged §7.1 clause "the claim of a
replacement pod from the pool's idle inventory, which the leaked slot's held occupancy keeps the reclaiming
pod outside" holds. `Binder.Resume` claims through `b.connect`, which uses `podclaim.Claimer.Claim` ("claims
an idle pod from the pool"), not `SlotClaimer.ClaimSlot` whose pass 1 prefers a same-tenant *claimed* pod.
`reserveResumeSlot` then reserves on the pod connect already picked. This closes the review log's
"Resume path and the exclusion" UNVERIFIED for the claim direction: the resume never re-picks a claimed pod,
so the reclaiming pod (claim still `bound`, because `ReleaseSlot(leaked=true)` skips the DELETE) is out.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1605, :1729-1757; podclaim/slotclaimer.go:415-425.

WATCHOUT: §7.2 step 3 does not merely omit the reclaim, it argues against it — "no runtime was started on it,
so no scrub beyond the pool's default post-session scrub is required" — which is the exact premise this
proposal refutes. And §7.2 step 1 aborts the in-flight restoration RPCs before step 3 releases the pod, so
the reclaim the §6.2 clause now requires (which §7.1 says rides "the connection the failed stage still
holds") has no place in the ordered sequence. A fixer must add the reclaim to §7.2 step 3 (or between steps
1 and 3) and correct the "no scrub required" clause, not just point §7.2 at §7.1.
EVIDENCE: spec/07_session-lifecycle.md:212, :214; spec/06_warm-pod-model.md:234; spec-changes.md:237.

FACT: the `resuming → completed` sibling bullet names the sequence as four steps
("The same abort / skip-seal / release-replacement-pod / run-terminal-handling sequence applies as for the
cancel edge above") and then points at §7.2 for the full sequence. SPEC-2's decision not to edit it is sound
— the reclaim folds into "release-replacement-pod" — so the §7.2 fix covers both edges and no §6.2 edit to
the completed bullet is needed. EVIDENCE: spec/06_warm-pod-model.md:235.

FACT (anchor re-check, cheap): every SPEC-1..SPEC-4 verbatim anchor still matches byte for byte at
spec/04:157, spec/04:686, spec/04:853-854 (§4.7.9 step 5), spec/05:453 (Scrub model), spec/05:545 (Slot
cleanup action list), spec/05:555 (Max retries), spec/06:146-155 (the two fence blocks), spec/06:234
(resuming cancel clause), spec/07:23 (atomicity parenthetical), spec/07:414 (resume-flow item 4). The
standing context's "anchor sweep is done" entry is still accurate at f2a397b53.

UNVERIFIED: whether `docs/` mirrors the §7.2 mid-resume close sequence anywhere. I did not check; the
non-spec loop should grep for "half-claimed replacement pod" and "snapshot-close" under docs/ once the §7.2
edit is staged. Only spec/06:234, spec/07:214 and spec/07:221 carry the phrase inside spec/.

### [spec.2.review-mechanism.2]

FACT: `st.started` has THREE production setters, not one. `claimSessionSlot` is called from
`pkg/adapter/session.go:111` (StartSession), `pkg/adapter/resume.go:50` (Resume) and
`pkg/adapter/sdkwarm.go:217` (ConfigureWorkspace), and all three run
`claimSessionSlotUnderLock`, which sets `st.started = true`. The standing-context entry
"`st.started` precedes `Runtime.Start`" names only the StartSession caller and misled the
fix round that reworded the teardown precondition to name `StartSession` alone.
EVIDENCE: pkg/adapter/slotsession.go:52-88; pkg/adapter/session.go:111; pkg/adapter/resume.go:50;
pkg/adapter/sdkwarm.go:217.

FACT: `StartSession` is the pod-warm RPC ONLY. spec/04:672 defines it as "(pod-warm mode)" and
spec/04:673 defines `ConfigureWorkspace` as the SDK-warm equivalent; spec/07:32-34 says
"SDK-warm pods: skip this step — session already connected, send ConfigureWorkspace". So any
spec predicate written as "a session whose `StartSession` the adapter has admitted" silently
excludes every preConnect session and every resumed session.
EVIDENCE: spec/04_system-components.md:672-673; spec/07_session-lifecycle.md:32-34.

FACT: the SDK-warm path also reaches `noteRuntimeStarted`. `SDKWarmInProcessRuntime.ConfigureWorkspace`
is the SDK-warm `Runtime.Start`, and sdkwarm.go records the runtime-cohort write on the freshness
arm. So the `running` / cleanup-outcome-report predicate ("the pod's shared runtime process has
been given the session") is correct across all three entry RPCs; only the TEARDOWN predicate is
narrow. Do not "fix" the report predicate too.
EVIDENCE: pkg/adapter/sdkwarm.go:249-259.

FACT: `Binder.Resume` claims through `Binder.connect` → `podclaim.Claimer.Claim`, which only takes
pods whose `Status.Phase == Idle` (plus the same-tenant `reserved` rebind), and the per-pod claim
CREATE rejects a pod that already holds one. A pod holding a leaked slot keeps its `bound` claim
(`ReleaseSlot(leaked=true)` early-returns), so it is genuinely outside the resume path's candidate
set. The staged §7.1 clause "the claim of a replacement pod from the pool's idle inventory, which
the leaked slot's held occupancy keeps the reclaiming pod outside" CHECKS OUT. Closes the standing
OPEN "Resume path and the exclusion".
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1592,:1737-1768;
pkg/gateway/podlifecycle/podclaim/claimer.go:122-160.

FACT: `ClaimSlot` returns `ErrNoConcurrentSlot` whenever the pool list is non-empty, no tenant
mismatch was seen, and no candidate was placeable — broader than both the spec gloss
(spec/05:549 "pods exist but all slots are full") and the code's own comment at
podclaim/slotclaimer.go:510-514. The code is already ahead of the documented reason; the staged
§5.2 disqualification widens the gap. Filed as a finding this round.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:510-522; spec/05:549.

DECISION: filed exactly two findings — the `StartSession`-only teardown predicate, and the
unstaged `concurrent_slots_exhausted` gloss. BECAUSE both are mechanism-level and neither is a
close variant of a refuted item. ALTERNATIVES rejected, each after working the evidence:
 (a) "§6.2's mid-resume cancel bullet says the half-claimed pod 'is then released to the pool'
     while §7.1 says an unacknowledged reclaim holds its occupancy" — real tension, but
     "released to the pool" is pre-existing text and the general-rule-plus-stated-exception
     defence already refuted the sibling finding on §7.1's own two sentences.
 (b) "'On a pod serving one session' vs 'On a pod serving concurrent sessions' is an occupancy
     predicate selecting a `maxConcurrentSessions` disposition, so a solo-occupancy concurrent
     pod takes the wrong branch" — round 1's skeptic already read the concurrent branch as
     covering a solo-occupancy concurrent pod (see the refuted "leaked disposition holds the
     slot's occupancy" entry), so the ambiguity would be judged manufactured.
 (c) "SPEC-3's new clause 'That per-slot cleanup is the one the **Slot cleanup:** bullet below
     states' drags the bullet's unqualified 'The adapter reports each slot cleanup outcome'
     over the new no-report class" — genuine, but the same wording-scope defence refuted the
     sibling finding one round ago.

WATCHOUT: the "already found and fixed" item about SPEC-4's prose contradicting "the untouched
`receiving_uploads → running` trigger" was only PARTLY discharged. The fix aligned the new edge's
annotation, CODE-3 and DOCS-1 with the prose; the retained trigger still reads "session dispatched
to runtime with its session identifier", which on a plain reading puts an in-flight start on the
`running` side while the staged prose puts it on the `receiving_uploads` side. Not refileable this
loop (explicitly listed as fixed), but a human should decide.
EVIDENCE: spec/06_warm-pod-model.md:152-153; spec-changes.md:126-131.

UNVERIFIED: whether CODE-1's staged `started` gate, once the spec predicate is widened to name
Resume and ConfigureWorkspace, needs any code change at all. It probably does not — `st.started`
already covers all three — so the fix should be spec-only. A code-lane reviewer should confirm.

### [spec.2.review-operational.1]

FACT: The proposal is byte-identical to the round-1 snapshot except for the review log. `diff -rq scratchpad/cp-snap/0081/spec-r2 proposals/0081_.../` reports only the review-log file. So round 2 had no fix-stage text to scrutinise; every staged block is round-1 text that has now survived two lenses' worth of review. EVIDENCE: scratchpad/cp-snap/0081/spec-r2 vs the proposal directory.

FACT (the one finding this pass found): §6.2's mid-resume cancel bullet, which SPEC-2 edits, ends "Full step-by-step sequence: see [§7.2] 'Mid-resume terminal transitions — snapshot-close semantics'." That §7.2 sequence is spec/07_session-lifecycle.md:210-217, and its step 3 (spec/07:214) reads "The half-claimed replacement pod is released back to the pool via the standard pod release path ([§6.2]) — no runtime was started on it, so no scrub beyond the pool's default post-session scrub is required." SPEC-2 inserts the reclaim into the §6.2 bullet and does NOT stage §7.2, and the "Spec files touched" spec/07 entry (spec-changes.md:377-379) names only §7.1 and §7.3. Both §6.2 bullets (`resuming → cancelled` at spec/06:234 and `resuming → completed` at spec/06:235) delegate to that same §7.2 sequence. EVIDENCE: spec/06_warm-pod-model.md:234-235; spec/07_session-lifecycle.md:210,214; spec-changes.md:225-245,377-379.

WATCHOUT: the §7.3 appended sentence does NOT cover the mid-resume cancel. It binds "a step in this flow that fails"; a client/parent/admin cancel is an external terminal trigger rather than a failing step, which is exactly why the proposal edits the §6.2 cancel bullet separately. Do not close the §7.2 gap by pointing at §7.3. EVIDENCE: spec-changes.md:218; spec/06_warm-pod-model.md:234.

FACT: the §4.9 direct-mode expiry timers are armed at `AssignCredentials` (pkg/adapter/slotcreds.go:49-51 → `reconcileSlotExpiryTimersLocked`) and cancelled only by `deregisterSlotLocked` (pkg/adapter/slotsession.go:159,178). §4.9's own rule (spec/04_system-components.md:1169) says an unfired timer deletes the credential file and reports `AUTH_EXPIRED` on CH-ADAPTEREVENTS. So a released-without-reclaim replacement pod carries armed timers that later fire a spurious `AUTH_EXPIRED` fallback flow for a cancelled session. That is the operator-visible half of the §7.2 gap.

FACT: `ReleaseSlotReservation` hard-codes `ReleaseSlot(ctx, sandboxName, false /*recycle*/, false /*leaked*/)` today and the proposal keeps `recycle=false`, so a failed bind never takes the occupancy-zero recycle boundary either before or after this change. The "recycle boundary on a failed bind" OPEN in the standing context is genuinely pre-existing and unchanged by this proposal. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:493-503.

FACT: `applySlotRetryPolicy` ALREADY calls `slots.MarkLeaked` + the `lenny_adapter_leaked_slots` gauge when the *reservation release* fails, i.e. for a leak the adapter cannot possibly know about. So "§6.2:160 says the adapter exposes the leaked count but this new leak is gateway-determined" is pre-existing in the strongest sense: the shipped code already does it. EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2850; spec/06_warm-pod-model.md:160.

FACT: `spec/16_observability.md` contains no alert and no metric row for leaked slots, per-slot cleanup, or session scrub reports. Grepping `leaked` in spec/16 returns nothing. The proposal therefore touches no alert-to-metric pairing, and there is no §16 edit site. The only observability inventories that name the surface are spec/16:12 (`lenny_gateway_pod_retirement_total`), :14-15 (`lenny_slot_failure_total`, `lenny_slot_pod_replacement_total`) and :128 (`lenny_pod_session_reuse_count`), and none of them is falsified by the staged edits.

FACT: `lenny_pod_state_transition_duration_seconds` (spec/16:20) is scoped to "the pod lifecycle state machine", i.e. the coarse pod phases, so SPEC-4's new per-slot edge adds no label pair and §16 needs no edit for it.

MISTAKE (nearly filed, do not re-derive): "§5.2's `concurrent_slots_exhausted` gloss ('pods exist but all slots are full') becomes wrong once a pod can be disqualified by the §7.1 reclaim obligation." The gloss is ALREADY loose in the shipped tree: `ErrTenantMismatch` maps to the same reason and a tenant-pin miss is not "all slots full". EVIDENCE: pkg/gateway/sessionserver/podclaimerror_internal_test.go:87-88; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:505-522; spec/05:549. This closes the standing context's "a later round may judge the gloss a site" item on the refute side.

MISTAKE (nearly filed, do not re-derive): "the staged §7.1 sentence 'the pod retires under the §6.2 pre-attached failure disposition' misattributes, because spec/06:283 says the pod is 'marked `failed` and released back to the pool (or terminated if unhealthy)'." The §6.2 pod state machine carries `failed ──→ draining` and `draining ──→ terminated` (spec/06:101-102) and `claimed ──→ draining (terminal claim disposition released or failed ...)` (spec/06:95-97), so "marked `failed`" does retire the pod. The citation holds. This closes the standing-context OPEN "spec/05 versus spec/06 on failed-session pod retirement" as far as THIS proposal's sentence is concerned.

MISTAKE (nearly filed): the `terminate` frame reason. `compensateFailedSlotBind` sends reason `"slot_bind_failed"` and `drainReason` maps every unrecognised value to `session_complete` (pkg/adapter/session.go:309-321), so an abandoned-but-admitted bind tells a Full-level runtime the session completed. The wire stays valid and no staged sentence becomes false, and the standing context already carries this as an OPEN rather than a finding, so it stays below the bar. Whoever adjudicates the open decisions should still decide it.

DEFERRED [docs/reference/state-machines.md and docs/reference/adapter-contract.md]: nothing new beyond what the standing context already records; I re-checked and found no additional docs mirror of the §7.2 mid-resume sequence.

### [spec.2.review-performance.1]

FACT: the spec staging did NOT change between round 1 and round 2. `diff -rq` of the r2
snapshot against the proposal directory reports only the review log (compaction pass 1)
as differing. Every SPEC-1..SPEC-4 block is byte-identical to what round 1 reviewed, so a
round-2 lens on the spec lane is re-reading, not reviewing a rewrite. Budget accordingly.
— EVIDENCE: scratchpad/cp-snap/0081/spec-r2/...spec-changes.md vs
proposals/0081_.../...spec-changes.md (identical)

FACT: **`Binder.Resume`'s failure branch releases NOTHING on an exclusive pool.**
`reserveResumeSlot` returns `""` when `req.MaxConcurrentSessions <= 1`
(binder.go:1675-1676) and `releaseResumeSlot` is a no-op on an empty slot id
(binder.go:1711-1713), and the only caller, `resumeOnPod`, returns the error with no
rollback (`start.go:4041-4043`) — unlike the two branches around it, which call
`s.rollbackBinding`. `handleResume`'s error branch (start.go:3495-3518) touches only the
session row via `holdOrFailOnResumeError` (start.go:3609-3620), and
`sessionNodeReattacher.ReattachNode` (treerecovery.go:29-31) does the same. So a failed
`§7.3` re-attach onto an exclusive replacement pod leaves that pod `claimed` until the
§4.6.1 orphan GC. This is the load-bearing fact behind the round-2 finding.
— EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1674-1677,:1710-1713;
pkg/gateway/sessionserver/start.go:4041-4043,:3495-3518,:3609-3620

FACT: **§6.2's pre-attached failure disposition says "released back to the pool", not
"retires".** spec/06_warm-pod-model.md:283: "The pod is marked `failed` and released back
to the pool (or terminated if unhealthy)." The code's `failPhase` drains instead
(binder.go:1058-1082), so the tree is ahead of §6.2 on the creation/start paths. Any
staged sentence that cites §6.2's pre-attached disposition as authority for "the pod
retires" is citing text that does not say it.
— EVIDENCE: spec/06_warm-pod-model.md:283; pkg/gateway/podlifecycle/podsession/binder.go:1058-1082

WATCHOUT: the exclusive-pod carve-out added by the round-1 fix ("DECISION: the `leaked`
disposition is scoped to the pod class that offers it") rests entirely on "the residue
dies with the pod". That premise is true on the creation finalize block and the §15.1
start transition (`failPhase` → `drain`) and FALSE on the §7.3 re-attach, which the same
sentence names as one of the three attempt kinds it binds. Do not re-derive the carve-out
without re-checking the resume path.
— EVIDENCE: spec-changes.md:200; pkg/gateway/podlifecycle/podsession/binder.go:867-870,:996-999

FACT (write-rate math, so nobody re-derives it): the staged edits add no control-plane or
data-plane write per unit of work. At Tier 3 (200 new sessions/s, 10,000 concurrent —
spec/12_storage-architecture.md:259) the only new traffic is one extra adapter `Shutdown`
RPC per FAILED bind, which is not an etcd, Postgres, or Redis write; SPEC-3 REMOVES a
`ReportSessionScrub` per pre-`running` reclaim. No new watch, no new informer cache, no
new hot key, no new single-key serialization. The `lenny:pod:{id}:active_slots` write rate
is unchanged (one INCR per bind, one DECR per clean release). There is no write-amplification
finding here; spend the budget on failure modes instead.
— EVIDENCE: spec/12_storage-architecture.md:259-270; spec/05_runtime-registry-and-pool-model.md:561

USEFUL [Settled: "The leaked-occupancy hold has no durable backing"]: correct and still
correct, and I re-checked the other half nobody had: the §7.3 exclusion the proposal
attributes to "the leaked slot's held occupancy" does NOT actually depend on Redis.
`Binder.Resume` claims through `connect` → `podclaim.Claimer.Claim`, which claims an IDLE
pod; a leaked release early-returns before `DeleteClaim`, so the pod keeps its per-pod
`SandboxClaim` in etcd and is not idle. The exclusion therefore survives a Redis reset,
and the §12.4 rehydration hole does not reach it. Do not file the §7.3 exclusion as
durably unbacked.
— EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1737-1768;
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836,:800-804

OPEN: on a concurrent pool a failed resume with an unacknowledged reclaim leaves a
FRESHLY CLAIMED idle pod holding occupancy 1 with zero live sessions, permanently, and
`connect` claimed it as a whole-pod claim rather than through `ClaimSlot`. Whether
`ClaimSlot`'s pass-1 scan of same-tenant claimed pods treats such a pod as slot-bearing
(so the leaked slot costs one slot rather than the whole pod) was not verified. A code-lane
reviewer should check `podclaim.Claimer.Claim`'s claim object against `SlotClaimer`'s.

### [spec.2.review-reliability.2]

FACT: Nothing in the proposal changed between round 1 and round 2 except the review log.
`diff -ru scratchpad/cp-snap/0081/spec-r2 proposals/0081_.../ --exclude='*review-log*'` is empty,
so the round-2 snapshot was taken AFTER round 1's fixes landed. Do not spend time hunting for
"what the fixer just wrote"; the whole SPEC-1..SPEC-4 body is round-1-fixed text.
EVIDENCE: scratchpad/cp-snap/0081/spec-r2/

FACT (closes standing OPEN "Resume path and the exclusion"): a §7.3 re-attach CANNOT re-pick the
pod holding an unacknowledged reclaim. `Binder.Resume` → `Binder.connect` → `podclaim.Claimer.Claim`,
which only ever takes a pod at the `idle` phase (`ErrNoIdlePod` otherwise), and the §4.6.1 Postgres
fallback re-reads the live Sandbox and refuses anything past idle. `ReleaseSlot(leaked=true)`
early-returns without deleting the per-pod claim, so the reclaiming pod stays `claimed` and is
outside idle inventory. The staged §7.1 clause "the claim of a replacement pod from the pool's idle
inventory, which the leaked slot's held occupancy keeps the reclaiming pod outside" is TRUE.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1600,:1737-1770; podclaim/slotclaimer.go:830-843.

WATCHOUT: the same is NOT true of the §5.2 placement path. `SlotClaimer.ClaimSlot` pass 1 scans
CLAIMED pods with free counter capacity for the tenant; only pass 2 scans idle pods. A leaked
slot's held occupancy therefore filters nothing on the start path — the exclusion there is the
in-memory `ExcludePod` field alone. Do not generalise the idle-inventory argument to the bind path.
EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:411-470,:478-508.

FACT (the route that defeats the disqualification, filed as a finding): on a concurrent pool the
two-step flow reserves the slot at /create, so `bindConcurrentSlot` takes the
`row.PodAssignment != ""` branch and calls `BindReservedSlot` on THE SAME POD, bypassing
`applySlotRetryPolicy` entirely ("the reserved slot is not re-reserved and retried here").
`handleStart`'s failure comment says in terms: "The row stays `ready` so the client can retry",
and `classifySlotBindFailure` returns a TRANSIENT failure unchanged so it surfaces as the
retryable `STARTING_FAILED` with Retry-After. So the documented client retry lands the next
attempt on exactly the pod §7.1 disqualifies.
EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2604,:1157-1165,:2761-2779; spec/06:290; spec/15:647.

FACT: `SocketRuntimeProcess.Start` returns nil IMMEDIATELY when `p.connected` (a co-tenanted pod),
with no ctx check, so a racing `StartSession` on a cancelled/expired context still starts the
session. This is what makes both orderings of the reclaim-versus-claim race real rather than
theoretical. EVIDENCE: pkg/adapter/socketruntime.go:181-190.

FACT: `ensureSlotStateLocked` re-creates BOTH the registry entry and the on-disk tree for a slot id
it does not hold, so a reclaim that lands before `claimSessionSlotUnderLock` leaves the adapter with
no record that a reclaim ever happened. CODE-2's `noteRuntimeStarted` guard finds the entry its own
claim just recreated and returns true, so no rollback fires. EVIDENCE: pkg/adapter/slot.go:105-126;
pkg/adapter/slotsession.go:64-90; pkg/adapter/session.go:111,163.

USEFUL [standing context / Traps]: the "do not re-gate the report on `st.started`", "do not widen
the §6.2 fence", and "dead end: the adapter cannot see this leak" entries each stopped a line of
enquiry cold. The Dead-end list is the highest-value part of the standing context.

MISTAKE (mine, nearly filed): I twice built a finding on "§6.2's pre-attached failure disposition
releases the pod back to the pool, so §7.1's 'the pod retires' is false". The §6.2 FENCE settles it
the other way: `failed ──→ draining` then `draining ──→ terminated`. The prose sentence at spec/06:283
is loose; the fence is not. EVIDENCE: spec/06_warm-pod-model.md:101-102,:283.

UNVERIFIED: the mid-start brick (standing OPEN "Mid-start `Shutdown` bricks the pod silently") is
REAL but narrow. `Close(sessionID)` for a session not in `p.active` still takes the last-close
teardown whenever `p.active` is empty and `p.connected` is true, and closes the listener that is
bound once at adapter start. Reaching `connected && active empty` needs the `Interrupt` path, which
closes the conn without clearing `connected`. So: pre-existing, reachable only after an Interrupt of
the last active session, and the remedy is code-lane. EVIDENCE: pkg/adapter/socketruntime.go:398-417,
:435-467.

UNVERIFIED: `Binder.Resume`'s failure branch never releases the replacement pod's per-pod claim and
never drains it (only `cl.Close()` + `releaseResumeSlot`, which no-ops on an exclusive pool), so
§7.1's "on a pod serving one session the failed attempt releases the pod's claim and the pod retires"
is not what the resume path does; §4.6.1 orphan-GC is the only reclaimer there. Judged pre-existing
(it predates the proposal and §6.2 already obliges it), so not filed. Somebody on the code loop
should decide whether CODE-4 owes a `failPhase`-equivalent on the resume path.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1618-1630; pkg/gateway/sessionserver/start.go:4040-4043.

UNVERIFIED: the adapter emits `ReportSessionScrub` once with no retry ("attempted then logged on
error"), so SPEC-3's new "at most one cleanup-outcome report per session release" invariant is not
defeated by redelivery even though `RecordSessionScrub` has no dedup key. Checked so a later
reliability pass does not re-derive it. EVIDENCE: pkg/adapter/sessionscrub_emit_test.go:182-205.

### [spec.2.review-security.1]

DECISION: returned an empty findings list — BECAUSE every security-shaped candidate I derived either fails the materiality bar or is already refuted/deferred — ALTERNATIVES: I considered and dropped four, listed below with the reasoning, so a later security pass does not re-derive them.

FACT: the diff against the round-2 snapshot touches ONLY the review log. `diff -ru scratchpad/cp-snap/0081/spec-r2 proposals/0081_.../` reports one changed file (the log's compaction pass). The spec staging is byte-identical to what round 1's fixers left, so "read the changed sections hardest" had nothing to bite on this round.

FACT: SPEC-3's two code claims are TRUE, so the added §5.2 actions really are shipped behaviour and no code deliverable is hiding behind them. `slotlayout.RemoveTree` iterates `{slotRoot, Sessions, Artifacts, CredentialsDir}` and `os.RemoveAll`s each, so the credential directory is already swept. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69. `deregisterSlotLocked` cancels every armed provider timer before deleting the map entry. EVIDENCE: pkg/adapter/slotsession.go:174-189.

FACT: the anchors the earlier sweep did NOT cover all resolve verbatim. The sweep entry in Standing context names six (spec/04:157,:686,:853-854; spec/05:545; spec/06:148-157; spec/07:23) but the staging has four more. Verified this round: spec/05:555 (`**Max retries:**` sentence), spec/05:453 (`**Scrub model.**` paragraph), spec/07:414 (`4. If retries exhausted → ...`), spec/06:234 (`the half-claimed replacement pod is released to the pool`). Also verified that every `#anchor` the staged text mints resolves to a real heading, including the three the sweep entry omits: `#49-credential-leasing-service` (spec/04:1099), `#151-rest-api` (spec/15:614), `#73-retry-and-resume` (spec/07:378).

FACT: the credential residue a `leaked` pre-`running` reclaim leaves is bounded by two shipped backstops, which is why I did not file it. Direct delivery arms a per-lease adapter timer that deletes `/run/lenny/slots/{sessionId}/credentials.json` and reports `AUTH_EXPIRED` (spec/04:1169); proxy delivery carries only a lease token the gateway refuses past `expiresAt` (same row). A leaked slot also holds occupancy, so the pod never reaches the occupancy-zero recycle boundary and never crosses a tenant. EVIDENCE: spec/06:157 (`leaked` slot semantics); spec/05:455.

FACT: §6.2's pre-attached failure disposition reads "The pod is marked `failed` and released back to the pool (or terminated if unhealthy)" (spec/06:284), which LOOKS like it contradicts the staged §7.1 claim that the exclusive pod retires. It does not: the pod-level fence carries `failed ──→ draining` and `draining ──→ terminated` (spec/06:100-101), and §5.2:455 states "A session that ends in failure or a crash always retires its pod regardless of recycle settings." I chased this for a while; do not re-chase it.

WATCHOUT: the §5.2 `**Slot cleanup:**` action list SPEC-3 widens is a flat list with no ordering, but the shipped handler splits it across the §15.4.2 grace window — `deregisterSlotLocked` cancels the expiry timers at the TOP of `Shutdown` under `s.mu`, while `removeSlotTree(st)` runs only after the drain signal and `Runtime.Close`. EVIDENCE: pkg/adapter/session.go:239,:269. So between those two points the §4.9 direct-mode timer is cancelled while the credential file still exists and the agent is still alive. I judged this pre-existing and non-material (the enforcement it defeats is a synthetic TTL on a key the file is about to lose), but an implementer restructuring `Shutdown` for CODE-1 should not widen that window.

WATCHOUT: `claimSessionSlotUnderLock` calls `ensureSlotStateLocked`, which RE-CREATES both the registry entry and the on-disk tree when the reclaim already deleted them. EVIDENCE: pkg/adapter/slot.go:104-125; pkg/adapter/slotsession.go:74-90. This is the same race the round-1 DEFERRED records against CODE-2, and I confirmed it independently. I did NOT file the spec half (the staged §7.1 sentence "so the pod holds no state for that session") because the sentence is a normative requirement on the adapter that a tombstone or a pre-claim generation check can satisfy, so the spec is not the defect; the staged CODE-2 is.

DEFERRED [proposals/0081_.../non-spec-changes.md CODE-4/CODE-5]: the staged §7.1 exempts the exclusive pod from the `leaked` disposition ("the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and neither they nor the disqualification apply there"), and that exemption is load-bearing for security: `SlotClaimer.ReleaseSlot(leaked=true)` returns early with no claim DELETE, so passing `leaked=true` on a `maxConcurrentSessions: 1` pod would leave the pod in the pool holding the abandoned session's tree and credential file, contradicting §5.2:455's "a session that ends in failure or a crash always retires its pod". What is true instead: the code lane must pass `leaked=false` (or bypass the leak disposition entirely) whenever the pod's occupancy is exclusive, whatever the adapter answered. Nothing in the non-spec staging I read states that constraint on `ReleaseSlotReservation`'s new `leaked` parameter. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:487-503; spec/05:455.

MISTAKE (mine, avoided): I nearly filed "the withheld `ReportSessionScrub` lets a tenant drive N pre-`running` attempts onto one pod without advancing `recycle.maxSessionsPerPod`, and the pre-`running` window includes `RunSetup`". It does not clear the bar: today the same path files no report either (no `Shutdown` is sent at all), the per-slot cleanup the proposal ADDS is what reclaims that residue, and the whole-pod scrub plus `maxScrubFailures` remain the backstops. The proposal strictly improves residual-state hygiene here, so the accounting question is a preference between workable models. EVIDENCE: spec/05:488; spec/29:250-252.

### [spec.3.fix-G1.1]

DECISION: §7.1's staged reclaim paragraph now states nothing at all about the cleanup-outcome report — BECAUSE the report is the adapter's obligation and §5.2's `**Scrub model.**` paragraph is the single home the proposal already chose for it, which SPEC-1's own justification says verbatim ("§5.2 states both the report and the pre-`running` exception after SPEC-3", spec-changes.md:186-187) and which §4.7's `Shutdown` row already obeys — ALTERNATIVES: the reviewer's two-branch rewrite of the closing sentence (rejected: it is the same answer one step narrower, which is the shape both prior rewrites at this location took, and it would put §5.2's partition in a third place); a predicate-free pointer at §5.2 (rejected: the paragraph already links §5.2 twice and a third contentless link is noise in a paragraph whose defect is saying too much about other sections' rules).

DECISION: the obligation's scope clause changed from "ends when that attempt has the session running on the pod" to "ends when that attempt succeeds", in both the staged §7.1 paragraph and the Design section's copy — BECAUSE `running` is bound vocabulary pinned by SPEC-4 to "the pod's shared runtime process has been given the session", so the old clause ended the reclaim obligation exactly where the very next sentence says it must not ("the gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session").

WATCHOUT: the same false proposition was stated TWICE in the §7.1 material, in different words, and a grep on the staged wording finds only one of them. The staged paragraph said "ends when that attempt has the session running on the pod"; the Design section's copy said "ends when that attempt has the session running on the pod" after "first pod-side RPC" rather than "first such RPC", so a grep anchored on the staged sentence misses it. Round 3's finding named only the closing sentence. Whoever files at this location again should read the whole paragraph and the Design paragraph together. EVIDENCE: spec-changes.md:44-45 and :216.

DECISION: SPEC-3's §5.2 append gained one sentence stating the complement — "A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome." — BECAUSE after the §7.1 deletion the applied spec stated only when the report is withheld and left the positive case to implication, and that implicit half is exactly the question §7.1 answered wrongly on its own initiative. It is the spec statement of the staged code predicate `live := removed && s.runtimeHoldsLocked(sessionID)` (non-spec-changes.md:104) — ALTERNATIVES: leaving the complement to follow from §5.2's base sentence (rejected: the proposal deliberately frames the pre-`running` reclaim as not a session release, so whether a post-`running` reclaim is one is the open question); stating it in SPEC-4's §6.2 prose (rejected: §6.2 owns the boundary, §5.2 owns the report).

DECISION: §7.2's preamble sentence is DELETED whole rather than trimmed, and the reason for skipping the live seal moves into step 2, which owns the decision — BECAUSE the sentence's conclusion ("there is no live workspace on the pod to seal") is what this change-set falsifies, and after the gloss goes it is inferred from `attached` alone, which no longer entails it — ALTERNATIVES: the reviewer's replacement premise (right content, wrong location: it states step 2's rule and step 2's artifact in the preamble two lines above step 2, and forward-references step 3); delete and add nothing (rejected: leaves a decision with no reason while the sibling pre-attach paragraph gives one, which invites a later round to reinstate a premise).

FACT: both the start path and the resume path reach `Runtime.Start` and `noteRuntimeStarted` before the gateway learns the attempt's outcome, so a reclaimed slot can be in `running` on EITHER path and no section may assert otherwise. EVIDENCE: pkg/adapter/session.go:155-163 (Start then noteRuntimeStarted then `return &adapterv1.StartSessionResponse{}, nil`); pkg/adapter/resume.go:50 (`claimSessionSlot`), :140 (`Runtime.Start`), :144 (`noteRuntimeStarted`).

FACT: the §7.2 preamble anchor is only unique when it carries the words either side of the deleted sentence. "onto a replacement pod. Because the replacement pod has not yet reached `attached` — the agent runtime has not been started or reconnected — there is no live workspace on the pod to seal. The gateway handles" occurs once in spec/07_session-lifecycle.md; the step-2 anchor "— the same artifact that was about to be replayed onto the replacement pod." also occurs once. Both re-verified against the tree this round.

WATCHOUT: do NOT touch the "A start that races the reclaim" accepted-failure-mode bullet's closing sentence ("Neither ordering reports a cleanup outcome, because the slot never reached `running`", spec-changes.md:134). It was adjudicated true-as-scoped in round 3: one ordering undoes the give before recording it, the other's reclaim runs before the start re-creates the entry, so neither ordering reaches `running`. It reads like the same defect as the §7.1 sentence and is not.

WATCHOUT: do NOT touch spec/07_session-lifecycle.md:220's "Pre-attach terminal collapse" paragraph, which says "there is no live workspace or running runtime to seal". No replacement pod is claimed on those edges, so the sentence is true and is not a matching site for the §7.2 fix.

MISTAKE: the staged "Spec files touched" list carried a literal count of the sentences SPEC-3 appends ("three sentences appended"), which this round's own §5.2 addition falsified in the same file, one screen from the block it counted. Replaced with a named set ("the per-slot cleanup pointer and the cleanup-outcome report rules appended") rather than with "four", because a count in that list is a trap that re-arms on every later edit. The §7.2 bullet was reworded the same way ("a sentence added to step 2" rather than a number).

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S2 (line 8) is already recorded as wrong in the standing context, and the replacement text recorded there is now wrong too. It lists what §7.1 carries as "the `leaked` disposition, the exclusive-pod case, the tree hazard with a §5.2 pointer, and the no-report rule". After round 3 §7.1 carries no report rule at all: the staged paragraph's closing sentence was deleted, so §7.1 states the reclaim obligation, its `leaked` disposition, the exclusive-pod case, and the tree hazard with a §5.2 pointer, and nothing about the cleanup-outcome report. The report rule, its pre-`running` exception, its complement (a cleanup reclaiming a slot the shared runtime process was given reports its outcome), and the one-report-per-release rule are all SPEC-3's, appended to §5.2's `**Scrub model.**` paragraph.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: the same recorded replacement text for S2 names "§7.2's mid-resume snapshot-close step 3" as one of the pointer edits. §7.2 now takes three edits rather than one: the section preamble's premise sentence ("Because the replacement pod has not yet reached `attached` ... there is no live workspace on the pod to seal.") is deleted outright, step 2 gains a sentence saying the aborted re-attach's pod-side state, including a session the adapter may already have started on the replacement pod, is reclaimed in step 3 rather than sealed, and step 3 is replaced as already recorded.

USEFUL [spec.1.fix-G1.1 / standing-context "Traps"]: the recorded MISTAKE "the report rule was stated twice with opposite content ... each block stated its own version of one rule instead of citing a single statement of it" is exactly the defect both round-3 findings in this group turned out to be, and it is what made the delete-rather-than-reword remedy obvious without re-deriving it. Keep that entry.

### [spec.3.fix-G2.1]

DECISION: the create-time-reserved retry bullet's "bounded on both sides" claim was deleted rather than repaired, and both arms were re-keyed on whether the adapter acknowledged the reclaim — BECAUSE the two predicates the bullet paired are different mechanisms (the adapter's refusal is gated on `st.started` alone; the gateway's `leaked` is `err != nil || !cleanly` on the reclaim `Shutdown`), so an answered clean reclaim both removes the entry AND is the released case, making the old second arm exactly backwards, while the old first arm is false for every pre-`StartSession` stage. Acknowledgement is the one predicate that partitions the outcomes and it is already the proposal's own §7.1 vocabulary. The bullet's charter (spec-changes.md:63-66) is to RECORD a residue no layer governs, and a record needs no bound — ALTERNATIVES: narrowing only the first arm and keeping "bounded on both sides" (leaves the second arm backwards and still asserts a bound the mechanism does not deliver); extending §5.2's placement constraint or minting a third retry carrier to make the bound true (tried and withdrawn twice, review-log.md standing context); an adapter-side tombstone of answered reclaims (invents adapter state in two files a later campaign position rewrites, and summary.md:154 already prices it as rejected); deleting the bullet (spec-changes.md:63-66 discharges the §5.2 placement gap by pointing at it, so deleting it makes that Design sentence dangle).

FACT: `BindReservedSlot` re-runs the whole `materializeSlot` sequence against the persisted `row.PodAssignment` and `row.ID`, so a retried §15.1 start reaches the same pod under the same slot id and the same on-disk tree. The stages it runs before `StartSession` are workspace preparation/finalize, setup, and credential assignment, and each leaves an adapter entry with `st.started` false. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:210-256, :255-330.

FACT: the adapter's `Shutdown` removes the registry entry under `s.mu` (`deregisterSlotLocked`) and deletes the slot tree afterwards, outside the lock. So the "tree removal still running when the retry materializes the same tree" window belongs to the ACKNOWLEDGED arm, not the unacknowledged one. EVIDENCE: pkg/adapter/session.go:237-239, :270.

WATCHOUT: do not write that the retried start inherits "the first attempt's credential file and armed §4.9 expiry timers". Only the slot TREE is inherited. `assignCredentialsSlot` rewrites `st.creds` and the on-disk credential file wholesale from the request's lease set, then `reconcileSlotExpiryTimersLocked` cancels every timer for a provider absent from the new set and re-arms the rest keyed on lease id; `onSlotLeaseExpired` re-reads the slot state and returns unless the captured lease id still matches, so a stale timer is a no-op against a re-created entry. EVIDENCE: pkg/adapter/slotcreds.go:44-47, :168-233, :250-268.

WATCHOUT: `running` is bound vocabulary in spec-changes.md (§6.2's per-slot sub-state) and every occurrence of it as a state word is backticked. The tempting phrasing for the unacknowledged arm's closing effect, "ending a session the gateway believes is running", introduces a second unbackticked sense one bullet above the racing-start bullet whose backticked `running` group G1 is protecting. Say "takes the session off the shared runtime process, and ends the retried session the adapter had already started" instead. EVIDENCE: spec-changes.md:134 (the neighbouring bullet).

WATCHOUT: the acknowledged arm deliberately does NOT say the retry "re-binds cleanly". `BindReservedSlot` releases the create-time reservation on failure through `ReleaseSlotReservation` → `ReleaseSlot(ctx, name, false, false)`, a real `active_slots` decrement, while the retry reconnects to the persisted binding without re-reserving, so the retried session runs on occupancy the counter no longer counts. That is pre-existing in the tree and outside this proposal. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224, :487-503.

USEFUL [spec.3.fix-design-G2.1]: the design shard's `CORRECTS [spec.2.fix-G1.1, the FACT at review-log.md:161]` entry had already retired the mis-keyed FACT with full evidence, which is why this fixer touched exactly one file and did not rewrite another agent's ledger block. The `doNotDo` list also pre-refuted two of the reviewer's own suggested phrases (the credential-file/timer inheritance and the `AUTH_EXPIRED` firing), which would otherwise have landed as new false claims.

### [spec.3.fix-design-G1.1]

DECISION: close both G1 findings by DELETING the derived claim from the section that does not own the
predicate, rather than by scoping it. §7.1's closing "The reclaim reports no cleanup outcome, because the
slot never reached `running`" is deleted outright (no pointer replaces it), and §7.2's preamble sentence
"Because the replacement pod has not yet reached `attached` ... there is no live workspace on the pod to
seal" is deleted outright. BECAUSE the proposal already decided that the report rule has one home (§5.2's
`**Scrub model.**` paragraph) and that other sections cite rather than restate it — SPEC-1's own
justification says so verbatim ("It does not name `ReportSessionScrub` either, because §5.2 states both the
report and the pre-`running` exception after SPEC-3", spec-changes.md:186-187). §7.1's sentence is a
restatement wearing a citation, and it restates the adapter's obligation inside a paragraph whose whole
subject is the gateway's. Deleting it is the same repair round 2 already applied twice to this paragraph
(the placement rule moved to §5.2; the racing-start promise moved to the edge-case list); this sentence is
the last surviving restatement in it. ALTERNATIVES: (a) the reviewer's suggested two-branch rewrite of the
§7.1 sentence — rejected, it is the failed answer one step narrower and it puts the second half of §5.2's
partition in a third place; (b) replacing it with a bare pointer ("whether that cleanup reports an outcome
follows §5.2") — rejected, the paragraph already links §5.2 twice and the reader who needs the answer is
already sent there; (c) the reviewer's suggested §7.2 replacement sentence — rejected, it restates step 2's
own content in the preamble and forward-references step 3, which is exactly the two-statements-of-one-rule
structure the standing context records as the MISTAKE that cost a full round.

WATCHOUT: the finding names §7.1's LAST sentence, but the same false proposition is stated a second time,
earlier in the same paragraph and again in the Design section, as "The obligation begins with an attempt's
first such RPC and ends when that attempt has the session running on the pod." That clause says the reclaim
obligation ends at the `running` boundary, which is exactly what the next sentence ("the adapter may have
started the session") denies. A fix that deletes only the closing sentence leaves the paragraph asserting
the same thing in its scope clause and hands round 4 the identical finding one sentence over. Replace both
with "...and ends when that attempt succeeds." EVIDENCE:
proposals/0081_.../0081_....spec-changes.md:216 and :44-45 (the Design copy uses "first pod-side RPC", so a
grep on the staged wording misses it).

FACT: the adapter's `Resume` reaches `Runtime.Start` and `noteRuntimeStarted` on its own, after
`claimSessionSlot` and after the workspace replay, all before the gateway records `resuming → attached`.
So a mid-resume terminal genuinely can find a materialised workspace and a started runtime on the
replacement pod, which is what makes §7.2's "no live workspace on the pod to seal" false. EVIDENCE:
pkg/adapter/resume.go:50, :139-143.

FACT: the sibling paragraph spec/07_session-lifecycle.md:220 ("Pre-attach terminal collapse") says "there is
no live workspace or running runtime to seal" and stays TRUE, because no pod is claimed at all on that edge.
Do not sweep it as a matching site.

DECISION: add ONE clause to SPEC-3's §5.2 append stating the complement of the exception — "A cleanup that
reclaims a slot the pod's shared runtime process was given is a session release like any other and reports
its outcome." BECAUSE after the two deletions the applied spec states only when the report is withheld, and
the complement (a `running` reclaim reports once) survives only by implication from the base sentence. The
partition is what §7.1 invented its own half of; stating both halves once, in the one home, is what stops
that recurring. It is also the exact spec statement of the staged code predicate
`live := removed && s.runtimeHoldsLocked(sessionID)` (non-spec-changes.md:104). ALTERNATIVES: leave it
implicit — rejected, a rule stated nowhere is the mirror of the rule stated twice, and this proposal has
already paid for both.

MISTAKE: rounds 1 and 2 both fixed this §7.1 paragraph by deleting a restatement (placement rule → §5.2;
racing-start promise → the edge-case list) and both times left the neighbouring restatement standing. The
paragraph's disease is structural: it derives consequences from rules other sections own. A fixer should
sweep the WHOLE paragraph for derived claims once rather than remove them one finding at a time.

DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: the standing context's S2 DEFERRED says
the §7.1 paragraph "carries the `leaked` disposition, the exclusive-pod case, the tree hazard with a §5.2
pointer, and the no-report rule". After this round the no-report rule is NOT in §7.1 at all. True instead:
S2's §7.1 paragraph carries the reclaim obligation, its scope (creation finalize block, §15.1 start, §7.3
re-attach), the obligation's begin/end boundary stated as the attempt's own success rather than as the
`running` sub-state, the `leaked` disposition, the exclusive-pod case, and the tree hazard with a §5.2
pointer, and it states no report rule. §5.2's `**Scrub model.**` paragraph is the only statement of the
report rule and of its complement.

DEFERRED [proposals/0081_.../0081_....spec-changes.md "Spec files touched"]: line 451-452 reads "§7.2's
mid-resume snapshot-close sequence (the section preamble's premise clause and step 3, both replaced)". After
this design it is "the section preamble's premise sentence deleted, step 2 gaining one sentence, and step 3
replaced". Named here because the fixer may treat the file-list as out of its edit scope; it is in the same
file and should be corrected in the same edit.

UNVERIFIED: the "A start that races the reclaim" edge-case bullet (spec-changes.md:125-134) closes "Neither
ordering reports a cleanup outcome, because the slot never reached `running`." Round 3's reviewer adjudicated
that as true-as-scoped and I did not reopen it, but in the second ordering the runtime IS given the session
after the reclaim, so the causal clause is true only about the instant of the reclaim. If round 4 files on
it, the correction is the predicate ("the reclaim finds no session the runtime holds"), not the conclusion.

OPEN: nothing in the applied spec will say in so many words that a reclaim can find a slot in `running`; it
follows from §7.1's "the adapter may have started the session" plus SPEC-4's `running → slot_cleanup`
sentence plus §5.2's complement clause. Three sections, one proposition, no restatement. A later reviewer who
wants it said once, positively, should put it in SPEC-4's prose (§6.2 owns the boundary), never in §7.1.

### [spec.3.fix-design-G2.1]

DECISION: re-key the create-time-reserved retry bullet's two arms on whether the adapter ACKNOWLEDGED the reclaim, drop the "bounded on both sides" framing entirely, and state the pre-start admission plus the lagging-teardown exposure as the accepted residue — BECAUSE the two predicates the bullet paired are not the same predicate: the adapter's refusal is `st.started` (slotsession.go:79-84) while the gateway's `leaked` is `err != nil || !cleanly` on the reclaim `Shutdown` (slotbinder.go:542-543), so "entry left in place ⟹ refused" is false for the two residue classes this proposal exists to reclaim and "entry removed ⟹ leaked" is backwards (an answered clean Shutdown is exactly the released case). The bullet's job under the Design clause at spec-changes.md:63-66 is to RECORD the residue, not to bound it, so an honest record is the whole fix — ALTERNATIVES: keep "bounded" and narrow the first arm to started-only (rejected: still asserts a bound the mechanism does not deliver, and the surviving second arm is still keyed backwards); add a §5.2-style placement constraint to the create-time-reserved path (rejected: already tried and withdrawn twice, review-log.md:155,221 — the enumeration cannot be completed without a durable per-row exclusion); an adapter tombstone that survives entry re-creation (rejected: touches pkg/adapter/slot.go and slotsession.go, which a later campaign position rewrites, and summary.md:154 already prices it).

DECISION: the Design clause at spec-changes.md:63-66 needs NO edit. Its sentence is "the residue that leaves is recorded among the accepted failure modes below rather than governed by a rule no layer enforces", which stays true when the bullet records the residue honestly. The finding named it as part of the fix; it is not a site. Do not rewrite it.

FACT: the reviewer's own suggested_fix over-claims on two of the three things it says the retry inherits. The retry does NOT run on the first attempt's credential file or its armed §4.9 timers: `assignCredentialsSlot` does `st.creds = leases` and `writeSlotCredentialFile(dir, leases)` (a full rewrite from the request's lease set), then `reconcileSlotExpiryTimersLocked` cancels every timer for a provider absent from the new set and re-arms the rest, keyed on lease id. Only the slot TREE is inherited. EVIDENCE: pkg/adapter/slotcreds.go:44-51,:168-195,:200-213,:218-233.

FACT: a stale timer cannot fire against a session on a RE-CREATED entry either. `onSlotLeaseExpired` re-reads `slotStateLocked(slotID)` and returns unless `st.timers[provider].leaseID` still matches the lease the closure captured, so a fresh entry (empty timers map) or a re-assigned lease makes it a no-op. EVIDENCE: pkg/adapter/slotcreds.go:250-268.

FACT: the adapter's `Shutdown` handler performs no context-expiry check before `deregisterSlotLocked`, so a reclaim whose gateway-side deadline has already lapsed still executes destructively when it eventually reaches the handler: it removes the entry, cancels timers, closes the runtime (for a started entry) and removes the tree. This is what makes the lagging-reclaim exposure on the create-time-reserved retry path real rather than theoretical. EVIDENCE: pkg/adapter/session.go:227-271.

FACT: on the create-time-reserved path all four stages that can fail before `StartSession` are pre-start — `stageWorkspace`, `FinalizeWorkspace` (both `slotFailureWorkspacePrep`), `RunSetup`, `assignSlotCredentials` — so `st.started` is false for every one of them and `claimSessionSlotUnderLock` admits the retry. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:284-322; pkg/adapter/slotsession.go:74-90.

WATCHOUT: do not write "the retried start re-binds cleanly" in the acknowledged arm. `BindReservedSlot` releases the create-time reservation on failure (`ReleaseSlotReservation` → `ReleaseSlot(..., false, false)`, a real active_slots decrement) and the retried `/start` re-enters `BindReservedSlot`, which reconnects to the persisted binding and re-reserves nothing. So the retried session runs on occupancy the counter no longer counts. That is PRE-EXISTING (true in the tree today, unchanged by this proposal) and is deliberately not in this edit, but a corrected arm that asserts cleanliness would be next round's finding. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224,:487-503; pkg/gateway/sessionserver/start.go:2596,:3220-3225.

WATCHOUT: `Shutdown` removes the entry under `s.mu` and the tree afterwards (and today only for a `bound` entry). "The entry is gone" and "the tree is gone" are two moments, so the surviving concession sentence about a tree removal still running belongs to the ACKNOWLEDGED arm, not to the leaked one. Already recorded at review-log.md:231; it decides where that sentence lands after the re-keying.

CORRECTS [spec.2.fix-G1.1, the FACT at review-log.md:161]: "a retried `/start` onto a reserved slot whose adapter entry survived the reclaim is refused by the adapter, not silently re-admitted" is wrong as a general claim, and its evidence range (slotsession.go:78-84) shows why: the refusal is gated on `st.started` alone. What is true: the retry is refused ONLY when the first attempt had already admitted a start on that slot. For the two residue classes this proposal exists to reclaim — an entry `ensureSlotPaths` created during workspace prep (present, unbound) and an entry `assignCredentialsSlot` bound (bound, unstarted) — `st.started` is false, `ensureSlotStateLocked` returns the surviving entry, `StartSession` passes `idempotentRepeat=false`, and the claim falls through and succeeds. Those are exactly the stages `bindReservedSlot` runs before `StartSession`, so the mis-stated FACT is false for the majority of the failure stages on the very path it was written about. This FACT is the provenance of the mis-keyed boundedness arm at spec-changes.md:114-115; it is corrected HERE by this entry rather than by editing another agent's ledger block. EVIDENCE: pkg/adapter/slotsession.go:74-90; pkg/adapter/slot.go:105-126; pkg/adapter/slotcreds.go:26-36; pkg/adapter/session.go:111.

USEFUL [standing context, "Adapter predicate nesting" (review-log.md:20) and "`SlotID == SessionID` on every path" (:48)]: the nesting entry is what let me price the first arm's falsity in one read instead of re-deriving the three writers of `sessionID`/`started`, and :48 is the reason "the retry reuses the first attempt's slot tree" is safe to assert while the credential-file and timer claims are not.

DEFERRED [proposals/0081_.../0081_....summary.md]: the 0080 §1.19 impact row (summary.md:278) states two membership cases, the completed-reclaim absent-entry one and the racing-start present-and-bound one. It does not state the case this bullet is about: after a reclaim the adapter did NOT acknowledge on a create-time-reserved slot, a surviving entry is present-and-unbound (workspace-prep residue) or present-and-bound (credential-assignment residue) for a session the gateway has abandoned, so a fence RPC in that window meets the unbound-entry refusal or neither refusal, for a session 0080 would classify as gone. Correcting this bullet does not CAUSE that omission (an earlier DEFERRED at review-log.md:177 already owes the row a widening), so it was left out of this edit rather than pulled in. Whoever closes review-log.md:177 should close both in one rewrite of the row.

OPEN: the create-time-reserved retry now has a stated exposure (a lagging reclaim can tear down a running retried session) with no bound at all, where §5.2-placed retries are bounded by the `ExcludePod` placement constraint. review-log.md:179 already asks a human whether the recorded residue is acceptable; this correction makes the honest answer strictly worse than the version that question was asked about, so it should be re-put with the corrected text in hand.

### [spec.3.review-applicability.1]

FACT: Every staged anchor in the spec-changes file resolves uniquely against the tree at commit f2a397b53. Verified by grep, one hit each: §4.1 sentence spec/04:157; §4.7 `Shutdown` row spec/04:686; §4.7.9 step 5 spec/04:854; §5.2 `**Scrub model.**` spec/05:453; §5.2 `**Slot cleanup:**` action list spec/05:545; §5.2 `**Max retries:**` spec/05:555; §6.2 fence entries spec/06:150-155; §6.2 `resuming → cancelled` clause spec/06:234; §7.1 parenthetical spec/07:23; §7.2 preamble clause spec/07:210; §7.2 step 3 spec/07:214; §7.3 list item 4 spec/07:414. The insertion-point prose is also right: §7.1's atomicity paragraph is spec/07:23 with its continuation line at :24, and the §6.2 fence closes at spec/06:156 immediately before `**\`reserved\` hold semantics.**` at :158. — EVIDENCE: spec/07_session-lifecycle.md:23-24; spec/06_warm-pod-model.md:150-158

FACT: Every markdown anchor the staged text emits resolves. `#1542-rpc-lifecycle-state-machine` (spec/15:1686), `#479-startup-sequence-for-type-agent-runtimes` (spec/04:848, already used at spec/README.md:36), `#49-credential-leasing-service` (spec/04:1099), `#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#71-normal-flow`, `#73-retry-and-resume`, `#151-rest-api`, `#47-runtime-adapter`. No new anchor is minted. — EVIDENCE: spec/README.md:36

FACT: No existing gate hard-fails between S4 (the §6.2 edge) and S5/S6. `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-75` holds a fixed four-edge `generalSlotEdges` list and asserts presence in the general block plus absence in the concurrency-scoped block; a fifth edge added to the general block trips neither. `requireLine`/`lineContaining` return the FIRST matching line (tests/tier11_docs/backup_status_enum_test.go:48-55), so an inserted paragraph could shadow a later line — checked every `specSection(..., "### 6.2 ")` and `"### 5.2 "` caller: none searches for a substring that SPEC-3's or SPEC-4's inserted text contains. `pkg/sandbox/slotstate` has no tier-11 reconciliation, only the tier-1 `ValidTransitions` test S6 covers. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-75; tests/tier11_docs/backup_status_enum_test.go:48-55

FACT: `slot_cleanup` appears in spec/ only in the §6.2 fence (spec/06:148,154,155), so SPEC-4's edge has no second spec transcription. The reader-facing mirror is docs/reference/state-machines.md:234-235 (DOCS-1's target) and nothing else. — EVIDENCE: spec/06_warm-pod-model.md:148-155; docs/reference/state-machines.md:234-235

FACT: SPEC-3's two added actions are both true of the tree. `slotlayout.RemoveTree` removes `p.CredentialsDir` (pkg/adapter/slotlayout/tree.go:59-68) and `deregisterSlotLocked` cancels every armed expiry timer before deleting the entry (pkg/adapter/slotsession.go:174-188). The path `/run/lenny/slots/{sessionId}/credentials.json` is the spec's own spelling (spec/04:793,914; spec/05:461). — EVIDENCE: pkg/adapter/slotlayout/tree.go:59-68; pkg/adapter/slotsession.go:174-188

WATCHOUT: §7.1's closing sentence "The reclaim reports no cleanup outcome, because the slot never reached `running`" is unconditional, but the reclaim's own trigger (the sentence three earlier: "even when the failing RPC's own context is already cancelled ... because that is the case in which the adapter may have started the session") is exactly the case where the runtime WAS given the session, i.e. the slot DID reach `running`. SPEC-3, SPEC-4, the Design paragraph and CODE-1's `live := removed && s.runtimeHoldsLocked(sessionID)` all condition the withheld report on the pre-`running` boundary; only §7.1 states it flat. Filed this round. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:216; :400; :435; :22-24; non-spec-changes.md:104,145

FACT: The reclaim-after-a-completed-start ordering is reachable, not hypothetical. `cl.StartSession` is the last stage of `materializeSlot`; its error branch is `slotFailureSessionStart`, and the adapter may have returned from `Runtime.Start` and recorded `noteRuntimeStarted` before the response was lost. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:313-324

DECISION: did NOT file the §7.1-covers-"fails"-only-versus-§7.2's-aborted-re-attach mismatch. §7.1's second sentence states the obligation as a window ("begins with an attempt's first such RPC and ends when...") rather than as failure-conditioned, and §7.2 step 3 says explicitly that the aborted re-attach carries it, so an implementor is not left guessing. ALTERNATIVES: filing it as a scope gap; rejected as wording precision the material skeptic has refuted twice on this proposal.

DECISION: did NOT file the checklist (S2/S3/S8/S10 still carry withdrawn rules, S4's tier deferral, unchecked-box sweep). ALTERNATIVES: filing them; the orchestrator's scope note for this loop says checklist and summary-index drift are reconciled between loops and are not findings here.

### [spec.3.review-citations.1]

FACT: every "text to replace" verbatim block in spec-changes.md was machine-checked against
spec/04, spec/05, spec/06 and spec/07 this round. All eleven match byte-for-byte AND occur
exactly once in their target file, so no anchor is ambiguous or stale. Re-running the check is
one script; do not re-verify them by eye. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md
blocks at lines 154, 174, 196, 232, 249, 278, 297, 324, 353, 373, 394; spec/04:157,686,854;
spec/05:453,545,555; spec/06:234,149-152; spec/07:23,210,214,414.

FACT: the round-3 §7.2 edit is the only genuinely new spec surface this round. The other
round-3 changes are the removal of the §7.1 placement rule (moved to §5.2's `**Max retries:**`
bullet), the `StartSession` → "start" rewording, and the two new edge-case bullets. — EVIDENCE:
`diff -u scratchpad/cp-snap/0081/spec-r2/...spec-changes.md proposals/0081_.../...spec-changes.md`
(note: `scratchpad/cp-snap/0081/spec-r3` is IDENTICAL to the live proposal, so the r3 snapshot
shows nothing; diff against `spec-r2` to see this round's edits).

FACT: `Resume` reaches `running` on the replacement pod. pkg/adapter/resume.go:50 claims the
slot (`st.started`), :139 calls `s.Runtime.Start`, :143 calls `s.noteRuntimeStarted` — the same
`runtimeLive` write the start path takes. So "a replacement pod short of `attached`" can hold a
slot in the staged §6.2 `running` sub-state, not merely a start in flight. That is what makes
the §7.2 step-3 delegation to §7.1 reach past §7.1's own upper bound. — EVIDENCE:
pkg/adapter/resume.go:50,139-143; proposals/0081_.../spec-changes.md:216,243,255.

WATCHOUT: §7.1's staged obligation has TWO bounds — "begins with an attempt's first such RPC
and ends when that attempt has the session running on the pod" — and every rationale in the
proposal that scopes a delegation to §7.1 names only the lower bound (spec-changes.md:258-260
for §7.2 step 3; :287 for §7.3). Any future edit that hands a new path to §7.1 must state what
happens on that path once the slot is `running`. — EVIDENCE:
proposals/0081_.../spec-changes.md:216,258-260,284.

WATCHOUT: the §7.2 preamble edit keeps "there is no live workspace on the pod to seal" after
deleting the gloss that justified it, while the same rationale asserts the pod "may hold a
started runtime". I judged that below the bar on its own (step 2's skip-the-seal is a stated
design choice and the proposal declines to reopen it), but it is the same root cause as the
finding I did file, and a fix to step 3 should re-read the preamble in the same pass. —
EVIDENCE: proposals/0081_.../spec-changes.md:237,241-244; spec/07_session-lifecycle.md:210-213.

USEFUL [Standing context → "the 'is a pre-start reclaim a session release?' defence"]: it
stopped me filing on §4.7's `ReportSessionScrub` row (spec/04:692) and spec/12:481
("incremented at each session release (`ReportSessionScrub`)"), both of which read as falsified
by SPEC-3 until you notice the proposal frames the pre-start reclaim as not a session release.
Saved a refuted finding.

FACT (checked so nobody re-checks): every code attribution in spec-changes.md holds.
`slotlayout.RemoveTree` removes `CredentialsDir` (pkg/adapter/slotlayout/tree.go:60);
`deregisterSlotLocked` cancels every armed timer before deleting the entry
(pkg/adapter/slotsession.go:174-183); the §4.9 timers are direct-mode only
(pkg/adapter/slotcreds.go:218-221); `stageWorkspace` sends `PrepareWorkspace` only when the plan
carries uploads (pkg/gateway/podlifecycle/podsession/binder.go:1324-1330); the adapter refuses a
second admitted start on a `started` entry (pkg/adapter/slotsession.go:80-85); the §15.4.2 drain
signal is gated on `!boundRemains` and precedes `Runtime.Close` (pkg/adapter/session.go:259-266).

FACT: the §4.7 Gateway→Adapter RPC table really does carry all three admitting RPCs
(`StartSession` :672, `ConfigureWorkspace` :673, `Resume` :684), so the staged row's "Which RPC
in this table starts a session depends on the pod's session mode and on whether the session is
new or resumed" resolves inside the table it names. — EVIDENCE: spec/04_system-components.md:672-686.

### [spec.3.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site-completeness lens on the round-3 spec staging — BECAUSE every candidate mirror site I found is either pre-existing incompleteness that the staged edits do not falsify, or sits in the refuted set — ALTERNATIVES: I nearly filed spec/29:199-201 (the §29.2 session-start trace's mirror of §7.1's atomicity rollback, "a failure at any of them rolls back the pod claim, persists no session row") as a companion site the SPEC-2 parenthetical widening leaves stale; see the FACT below for why it dissolves.

FACT: spec/29's §29.2 atomicity restatement covers ITS steps 2-10, which end at the `session_id` return (spec/29:176-202). The first pod-side RPC in that trace is step 15 `PrepareWorkspace` (spec/29:226). So the widened §7.1 parenthetical ("and reclaims the state the attempt created on the pod") quantifies over an empty set in §29.2's window exactly as it does in §7.1's own steps-2-8 window, and §29:200 stays accurate unedited. Do not re-file it. EVIDENCE: spec/29_communication-scenarios.md:199-201, :226

FACT: the three tier-11 gates that touch the edited surfaces all still pass under the staged text, so none of them is a hidden spec edit site. `per_slot_substate_scope_doc_reconciliation_test.go` asserts presence of the four general edges and absence of them from the concurrent block (it does not enumerate exhaustively, so the added `receiving_uploads ──→ slot_cleanup` is admitted); `recycle_scrub_trigger_consistency_test.go` requires the §4.7 `Shutdown` row to keep "recycle disposition", "ReportPodScrub", `podId`, `cleanupCommands`, `cleanupTimeoutSeconds`, "does not block the response on the scrub" and the §5.2 link, all of which sit in the row remainder SPEC-1 leaves untouched (and SPEC-1 adds a second §5.2 link); `spec_47_rpc_row_naming_test.go` reads only the row's first-column backticked name, which is unchanged. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:44-82; tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:53-102; spec/04_system-components.md:686

FACT: `/run/lenny/slots/{sessionId}/credentials.json` and the direct-mode "local timer for each credential lease's `expiresAt`" that SPEC-3's widened action list names are both real spec vocabulary, so SPEC-3's two added actions cite live text. The timer is stated in §4.9's provider-TTL table `anthropic_direct` row, not in a paragraph of its own. EVIDENCE: spec/04_system-components.md:1169; spec/04_system-components.md:683 (`ExtendCredentialLease` "expiry timer"); spec/28_communication-channels.md:460

FACT: the §4.7 Gateway → Adapter table carries three RPCs that admit a start — `StartSession` (pod-warm), `ConfigureWorkspace` (SDK-warm), `Resume` (replacement pod) — and all three reach `claimSessionSlot`, which is the single writer of `st.started`. So the new §4.7 row sentence "Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed" is determinable from the table and matches the code. EVIDENCE: spec/04_system-components.md:670-682; pkg/adapter/session.go:111, pkg/adapter/resume.go:50, pkg/adapter/sdkwarm.go:217; pkg/adapter/slotsession.go:88

FACT: §6.4's "Responsibility split" adapter bullet enumerates three per-session trees (`/workspace/slots/{sessionId}/`, `/sessions/{sessionId}/`, `/artifacts/{sessionId}/`) and does NOT name `/run/lenny/slots/{sessionId}/`, while SPEC-3 adds that credential directory to §5.2's action list. The asymmetry is pre-existing in both directions (an earlier round already had the mirror-image finding refuted) and §6.4 defers removal to "slot cleanup ([Section 5.2])", so §6.4 is not an edit site. EVIDENCE: spec/06_warm-pod-model.md:386

WATCHOUT: §6.2's "Pre-attached failure retry policy" (spec/06:283) is stated unscoped — "Failures in any state before `attached` ... The pod is marked `failed` and released back to the pool" — which reads against §5.2's "a single slot failure does not terminate the pod" for a concurrent pod. This looks like a contradiction the new §7.1 concurrent branch creates, and it is not: §7.1's concurrent sentence states only what the slot becomes (`leaked`, occupancy held, counted toward the §5.2 trigger), never that the pod survives, and the standing-context trap "Do not read the reclaim as scoped away from pods that retire" says the same. The §6.2-vs-§5.2 tension is pre-existing. EVIDENCE: spec/06_warm-pod-model.md:283; spec/05_runtime-registry-and-pool-model.md:542

WATCHOUT: the §5.2 slot retry policy's own trigger sentence is the `**Failure isolation:**` bullet, which fires on a slot's SESSION failing ("runtime error, pod-level OOM kill, or unhandled exception") — a post-`running` event. The proposal reads the policy as also placing bind-failure retries, which is what the shipped `applySlotRetryPolicy` does. The reading matches the code but not the bullet's stated trigger. I judged it pre-existing ambiguity rather than a defect this proposal introduces; a later round that wants to close it should widen the §5.2 trigger sentence rather than the §7.1 paragraph. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:544, :553; pkg/gateway/sessionserver/start.go:2807-2884

UNVERIFIED: the §7.3 appended sentence orders the reclaim "before the replacement pod is released", but §7.3's numbered flow never states that the replacement pod is released on any of its branches (3c and 4 both go to `awaiting_client_action`; a retryable failure loops to `resume_pending` and claims a further pod). §7.1 fully determines the reclaim's timing on its own, so I did not file it, but somebody should decide whether §7.3 owes a release step at all. EVIDENCE: spec/07_session-lifecycle.md:402-414

USEFUL [Standing context / "The anchor sweep is done; do not repeat it"]: I spot-checked five of the verbatim anchors anyway (spec/04:157, :686, :853-854; spec/05:545, :553; spec/06:150-157, :234; spec/07:23, :210, :214, :402-414) because the §7.2 and §7.3 anchors are new since that entry was written. All match byte for byte, including the two §7.2 anchors round 2 added. The entry can now be extended to cover the §7.2 preamble clause, §7.2 step 3, and the §7.3 step-4 list item.

### [spec.3.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action-feasibility lens on the round-3 spec staging — BECAUSE every action the staged text assigns is performable by the named actor with data that actor can see, and the two candidates I developed both collapsed on verification (below) — ALTERNATIVES: filing the §7.1-exclusive-pod-versus-§7.2-release-to-pool tension (rejected as a close variant of an already-refuted finding, see MISTAKE-avoidance note below).

FACT: the actor chain for every staged action was checked end to end and holds.
  - Adapter slot release / runtime teardown / whole-pod scrub (SPEC-1 §4.1, §4.7): all three are adapter-local. EVIDENCE: pkg/adapter/session.go:238-291.
  - SPEC-3's two ADDED actions are genuinely adapter-side. `slotlayout.RemoveTree` removes `p.CredentialsDir` (`/run/lenny/slots/{sessionId}/`) as one of its four trees, and `deregisterSlotLocked` cancels every armed per-provider expiry timer before deleting the entry, carrying a `// spec: §4.9; §15.4.2` citation already. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68; pkg/adapter/slotsession.go:174-188.
  - The §4.9 timer really is adapter-owned in direct mode ("In direct delivery mode, the adapter MUST set a local timer for each credential lease's `expiresAt`"; "the adapter expiry timer is the enforced lease deadline"), and §4.9 says proxy mode needs no adapter timer, so SPEC-3's "direct-delivery-mode" qualifier is exactly right. EVIDENCE: spec/04_system-components.md:1169, :1466.
  - The anchor `#49-credential-leasing-service` that SPEC-3 mints resolves: `### 4.9 Credential Leasing Service` at spec/04_system-components.md:1099. This is a SEVENTH anchor beyond the six the standing context's anchor sweep certified; it was added in round 2 and nobody had checked it. It is fine.

FACT: the §4.7 row's "Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed" is exactly true of the tree. The three RPCs that set `st.started` and call `noteRuntimeStarted` are `StartSession` (pod-warm), `ConfigureWorkspace` (SDK-warm; `SDKWarmInProcessRuntime.ConfigureWorkspace` is the runtime `Start`), and `Resume`. All three are rows in the §4.7 Gateway→Adapter table. EVIDENCE: pkg/adapter/session.go:111,:163; pkg/adapter/sdkwarm.go:217,:261; pkg/adapter/resume.go:50,:144; spec/04_system-components.md:670,:674,:684.

FACT: `maxSlotRetries == 1`, so `applySlotRetryPolicy` runs at most two attempts. The single-valued `ExcludePod` field is therefore sufficient to carry the staged §5.2 `**Max retries:**` constraint; there is no "exclude two prior pods" gap to chase. EVIDENCE: pkg/gateway/sessionserver/start.go:2720,:2809.

FACT: `Binder.Resume`'s adapter client is per-attempt, built by `b.connect` and closed with `cl.Close()` on the failure arm before `releaseResumeSlot`. So the reclaim and the pod release in staged §7.2 step 3 land in the SAME goroutine as the aborted attempt, which is what makes "sends on the connection that attempt still holds ... before the ... pod is released" implementable. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1591,:1602,:1621-1630.

FACT (chased, then dropped): the §7.2 close sequence and the aborted re-attach can in principle run on different gateway replicas — §7.2 step 4 and spec/04:167 both speak of "fencing any stale coordinator still attempting resume". I considered filing that step 3 assigns the terminal handler an action on a connection it does not hold. Dropped: the pre-existing step 3 already attributes the pod release to the close sequence while the code does it in the resume goroutine, so the attribution looseness predates the edit and the edit does not widen it. EVIDENCE: spec/07_session-lifecycle.md:214,:217; spec/04_system-components.md:167.

FACT (chased, then dropped): I looked for a spec-versus-code predicate drift where SPEC-3 withholds the report for "never reached `running`" while the implementation gates on CURRENT `runtimeLive` membership, which `noteRuntimeClosed` can clear before a `Shutdown` arrives. Not reachable: both non-`Shutdown` callers of `noteRuntimeClosed` also remove the registry entry in the same block (`removeSlotTree` at holdstate.go:252, `releaseSessionSlot` at sdkwarm.go:298), so a later `Shutdown` finds no entry and files nothing regardless. EVIDENCE: pkg/adapter/holdstate.go:251-253; pkg/adapter/sdkwarm.go:296-299.

FACT: both spec sites carrying the "no runtime was started on the replacement pod" premise that SPEC-1/3/4 overturn are in the proposal's edit list. A repo-wide grep for `no runtime was started|runtime has not been started|not yet reached \`attached\`` returns exactly spec/07_session-lifecycle.md:210 and :214, and SPEC-2 replaces both. There is no third site and no docs/ mirror (grep for `mid-resume|snapshot-close|half-claimed` across docs/ returns nothing).

WATCHOUT: spec/29_communication-scenarios.md:696 item 12 is the one un-edited near-mirror of the §4.7 `Shutdown` row. It is NOT falsified by the two-teardown split, because item 12 is scoped to "a session end triggered by `POST /terminate`, `DELETE`, or an expiry timer" — a session that ran. Do not file it as a missed edit site. The same holds for item 13's `terminate` frame. EVIDENCE: spec/29_communication-scenarios.md:695-703,:705-712.

WATCHOUT: staged §7.1 says an exclusive pod "retires under the §6.2 pre-attached failure disposition, so the reclaim's residue does not outlive the pod", while staged §7.2 step 3 and the staged §6.2 `resuming → cancelled` bullet both say the half-claimed replacement pod is "released back to the pool". On an exclusive replacement pod aborted mid-resume with an unacknowledged reclaim, neither §7.1 disposition (concurrent `leaked`, exclusive retire) discharges. I did NOT file this: `spec.2.review-*`'s "§7.1's exclusive-pod disposition is unreachable on the §7.3 re-attach it binds" was refuted, and the refutation explicitly weighed spec/06:234's release-to-pool clause and judged it not a contradiction. Anyone tempted to file it must first show why that refutation was wrong; the §7.2 edit sharpens the tension but does not change the argument. EVIDENCE: spec-changes.md:216 ("the pod retires under the [§6.2] pre-attached failure disposition"); spec-changes.md:255 ("released back to the pool"); spec/06_warm-pod-model.md:234.

OPEN: nobody has decided whether §7.1's exclusive-pod clause needs a carve-out for the mid-resume terminal path now that §7.2 routes that path through §7.1. It is either genuinely fine (the refuter's reading) or a real gap that only a human can adjudicate, because both readings survive the evidence.

### [spec.3.review-fresh.1]

FACT: The round-3 diff is small and concentrated. `diff -u scratchpad/cp-snap/0081/spec-r2/...spec-changes.md proposals/.../spec-changes.md` is the only useful diff (`spec-r3` is byte-identical to the current tree, so the orchestrator-named snapshot shows nothing). What round 3 added: the whole SPEC-2 §7.2 block (spec-changes.md:222-270), the §15.1-create-time-slot edge case (:110-119), the racing-start edge case rewrite (:125-134), the §5.2 `**Max retries:**` re-scoping, and the `StartSession` → "start" vocabulary sweep. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:222-270.

FACT: Every "text to replace" anchor still matches the tree byte-for-byte and is unique, including the two new §7.2 anchors. Verified programmatically against spec/04 (:157, :686, :854), spec/05 (:453, :545, the `**Max retries:**` sentence), spec/06 (:152-153, :234) and spec/07 (:23, :210, :214, :414). Do not re-run this sweep unless the spec moves.

FACT: The §4.7 row's new "Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed" is accurate against the tree: `StartSession` (pod-warm, session.go:111/163), `ConfigureWorkspace` (SDK-warm, sdkwarm.go:217/261), `Resume` (resume.go:50/144) are the three admission sites, and all three are rows in the §4.7 table. EVIDENCE: spec/04_system-components.md:672,674,684; pkg/adapter/sdkwarm.go:217,261.

WATCHOUT: §7.1's closing sentence "The reclaim reports no cleanup outcome, because the slot never reached `running`" is unconditional, but the same paragraph says the reclaim is sent "even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session". `noteRuntimeStarted` runs at pkg/adapter/session.go:163 before the RPC returns, so a deadline that expires one instant later leaves a `running` slot that the reclaim then cleans, and CODE-1's `live` gate reports for it ("a reclaim after `noteRuntimeStarted` recorded reports exactly once", non-spec-changes.md:145). SPEC-3's exception is correctly scoped (`receiving_uploads`→ before `running`); §7.1's is not. Filed this round. EVIDENCE: spec-changes.md:216; spec-changes.md:400; non-spec-changes.md:104,145.

WATCHOUT: SPEC-2's §7.2 preamble edit deletes the gloss "the agent runtime has not been started or reconnected" while keeping "there is no live workspace on the pod to seal", and the block's own rationale says the deleted clause is a premise the proposal overturns ("a replacement pod short of `attached` may hold a started runtime"). The retained conclusion is the thing the premise supported, so after the edit §7.2 asserts an absence its sibling edits deny, and step 2's skip-the-seal decision rests on it. Filed this round. EVIDENCE: spec-changes.md:238,241-244; spec/07_session-lifecycle.md:210,213.

OPEN: §7.1's obligation window "ends when that attempt has the session running on the pod" versus §7.2 step 3, which binds the obligation to an aborted re-attach whose adapter-side `Resume` may already have started the runtime. Under a pod-state reading the obligation has ended and step 3's reclaim is not owed; under an attempt-completion reading it still holds. Not filed (the attempt-completion reading is the natural one and the behaviour stays determinate because step 3 states it affirmatively), but a later round may want §7.1 to say whose view of "running" it means. EVIDENCE: spec-changes.md:216,255.

USEFUL [Standing context / Settled]: the "anchor sweep is done" entry and the "`Shutdown` is unfenced" entry each saved a full verification pass. The `RecordSessionScrub` has-no-per-session-dedup entry is what made the §7.1 no-report overreach legible as a real consequence rather than a wording point.

CORRECTS [Standing context / Traps, "Do not edit spec/06:152"]: that entry glosses the `receiving_uploads ──→ running` annotation as stating "the admitted-start boundary". It does not. The annotation reads "workspace ready, session dispatched to runtime with its session identifier", which is the runtime-has-been-given boundary (`runtimeLive`), and that is what SPEC-4's prose anchors to. The entry's advice (do not edit :152) is right; its reason is mis-stated, and a later agent trusting the gloss would conclude SPEC-4's prose mis-cites the annotation when it does not.

DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section]: the Design section ("as the spec must state it", :6-94) never mentions the SPEC-2 §7.2 mid-resume snapshot-close edit, which round 3 added as a staged deliverable. The "Spec files touched" list does carry it (:449-452). Below the finding bar on its own, but the Design paragraph on the reclaim obligation ("§7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph and restate nothing", :43-44) is now incomplete: §7.2 step 3 also restates the reclaim's ordering against the pod release.

### [spec.3.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens on round 3 — BECAUSE every idiom the lens owns checks out against the staged text and the tree, and the two candidates I developed furthest both reduce to prose completeness, which this loop's refutation history (11 refutations, almost all "wording/completeness, not correctness") puts firmly below the bar — ALTERNATIVES: (a) file the §7.2 preamble residue (see WATCHOUT below), rejected because the edit makes the sentence *less* wrong rather than more, so the ask is completeness; (b) file the §7.3 re-attach placement gap (see FACT below), rejected because the mechanism holds in code and no staged sentence is false.

FACT: the Kubernetes-idiom surface of this proposal is clean and I re-derived it end to end, so a later Kubernetes-lens pass can start from here rather than repeat it. (1) Ownership: §4.6.3 gives `SandboxClaim.spec`+`.status` to the gateway and `Sandbox.status` solely to the WarmPoolController; every write the staged text implies (slot reservation release, pod-claim release, `lenny.dev/drain-request`) is on the gateway's side of that line, and the gateway holds no `sandboxes/status` grant and no `patch`/`watch` on the `Sandbox` main resource. (2) No SSA force-ownership and no second manager on any field. (3) No finalizer is introduced, so no stuck-finalizer footgun. (4) No status field is used as an RPC inbox: the reclaim is a direct adapter `Shutdown`, and the leak accounting rides Redis occupancy plus the in-process ledger. (5) No controller sits on the synchronous path the staged text adds to. (6) A level-triggered backstop exists for the residue this design accepts: §4.6.1 orphan GC drains a `bound` claim older than `claimOrphanTimeout` whose pod no active session references. EVIDENCE: spec/04_system-components.md:606-620 (ownership table), :640 (gateway RBAC grants paragraph); spec/07_session-lifecycle.md:227 ("the gateway writes no `Sandbox.status` field, and the WarmPoolController is the sole writer").

FACT: a `leaked` slot cannot wedge the pod's retirement, which is the obvious Kubernetes-shaped worry about the staged `leaked` disposition and is worth not re-deriving. `draining ──→ terminated` is triggered by "pod replacement provisioned from warm pool" and is NOT gated on occupancy reaching zero, so a slot whose occupancy is held forever delays nothing. EVIDENCE: spec/06_warm-pod-model.md:102.

FACT: `Binder.Resume`'s pod acquisition goes through `podclaim.Claimer.Claim` (the whole-pod idle claim, with the §4.6.1 Postgres fallback on `ErrNoIdlePod`), NOT through `podclaim.SlotClaimer.ClaimSlot`. So a §7.3 re-attach genuinely cannot land on a pod whose occupancy is nonzero, and the reclaiming pod (occupancy held by the leaked slot, claim `bound`) is outside its candidate set. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1603, :1737-1768.

WATCHOUT: round 3 deleted the only text that STATED the fact above, and left the hazard sentence general. Round 2's §7.1 and its edge-case bullet both said a §7.3 re-attach "claims its replacement pod from the pool's idle inventory, which the leaked slot's held occupancy keeps the reclaiming pod outside"; round 3 removed that clause from §7.1, from the Design paragraph, and from the concurrent-pod edge-case bullet. What survives is §7.1's general hazard ("a further attempt at the same session on that pod would reuse a tree the lagging reclaim may still be deleting") plus a §5.2 constraint scoped to "the retries its slot retry policy places". §7.1 binds three attempt kinds; §5.2 covers one, the §15.1 create-time-reserved start has its own accepted-failure-mode bullet, and the §7.3 re-attach is now covered by nothing written down. I did not file it because the mechanism still holds (see the FACT above) and no staged sentence is false, but a later round asking "what keeps a re-attach off the reclaiming pod" should read this rather than re-open the question. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:56-66, :103-109; the deleted text is visible in `diff -u scratchpad/cp-snap/0081/spec-r2/…spec-changes.md proposals/…/…spec-changes.md`.

WATCHOUT: the Design paragraph's "A §15.1 start onto a slot reserved at creation is placed by neither mechanism" is now a dangling reference. Round 2 named two placement mechanisms (§5.2's slot retry policy and the §7.3 idle-inventory claim); round 3 deleted the second, so only one mechanism is named in the surrounding text and "neither" has no antecedent. Cosmetic on its own, but it is the visible symptom of the coverage deletion above. EVIDENCE: proposals/0081_…/0081_….spec-changes.md:63-66.

WATCHOUT: the §7.2 preamble edit deletes the false gloss but keeps the conclusion the gloss existed to support. Applied text becomes "Because the replacement pod has not yet reached `attached`, there is no live workspace on the pod to seal", while the same proposal argues at spec-changes.md:241-244 that "a replacement pod short of `attached` may hold a started runtime" and deletes "no runtime was started on it" from step 3 as an overturned premise. The surviving `Because` clause now asserts the implication directly with nothing behind it, and step 2's "Skip the live seal" rests on it. I judged this completeness rather than a new contradiction, because the conclusion predates the proposal and the edit strictly reduces the amount of overturned premise in the section. A later round that wants to close it should widen the SPEC-2 §7.2 preamble replacement rather than file it as a §7.1 defect. EVIDENCE: spec/07_session-lifecycle.md:210, :212; proposals/0081_…/0081_….spec-changes.md:238-244.

FACT: all seven verbatim anchors this round's staging depends on, including the two NEW §7.2 anchors the standing-context anchor sweep predates, match the tree byte for byte and are unique. spec/04:157 (§4.1 third sentence), spec/04:686 (`Shutdown` row opening), spec/04:854 (§4.7.9 step 5), spec/05:545 (`**Slot cleanup:**` action list), spec/05:555 (`**Max retries:**` pod-selection sentence), spec/06:234 ("the half-claimed replacement pod is released to the pool"), spec/07:23 (atomicity parenthetical), spec/07:210 (§7.2 preamble premise clause), spec/07:214 (§7.2 step 3), spec/07:414 (§7.3 list item 4). The §4.9 anchor SPEC-3 mints resolves (spec/04:1099 `### 4.9 Credential Leasing Service`), and the credential path `/run/lenny/slots/{sessionId}/credentials.json` SPEC-3 writes matches spec/04:914, spec/05:461 and spec/13:26.

USEFUL [standing context, "Ownership is clean"]: correct and saved the whole first half of this pass. I re-verified it against spec/04:606-640 and spec/07:227 and found nothing to correct; the round-3 edits (§7.2 step 3, §6.2 cancel bullet, §5.2 `**Max retries:**`) add no new writer to any CRD field and no new controller dependency, so the entry still holds after this round.

USEFUL [standing context, Traps, "Do not read the reclaim as scoped away from pods that retire"]: stopped me filing the §7.1-retires-versus-§7.2-releases-to-pool contradiction on the resume path, which reads like a live inconsistency until you know the material skeptic already adjudicated it.

### [spec.3.review-mechanism.1]

FACT: `spec-r3` under scratchpad/cp-snap/0081 is byte-identical to the live proposal directory, so the "what changed since last round" diff has to be taken against `spec-r2`, not `spec-r3`. EVIDENCE: `diff -rq scratchpad/cp-snap/0081/spec-r3 proposals/0081_.../` is empty.

FACT: the round-2→3 fixer collapsed SPEC-2's two racing-start sentences in the §7.1 block into one flat sentence, "The reclaim reports no cleanup outcome, because the slot never reached `running`". In r2 that no-report claim was scoped to the racing-start pair; unscoped it now covers every reclaim. EVIDENCE: spec-changes.md:216; snapshot spec-r2 same block.

WATCHOUT: the §7.1 block contains two sentences that pull opposite ways and they are 250 words apart on one physical line. "The gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session" admits the reclaim can land on a slot the runtime already holds; the final sentence then denies it. Read that line whole before editing any part of it. EVIDENCE: spec-changes.md:216.

FACT: the adapter refuses a start only on `st.started`, never on entry presence or on `st.sessionID != ""`. So an unacknowledged reclaim that leaves a bound-but-unstarted entry does NOT make a retried start fail. Every pre-`StartSession` failure stage (workspace prep, setup, credential assignment) is in that class, and `bindReservedSlot` runs all of them on the create-time-reserved slot path. EVIDENCE: pkg/adapter/slotsession.go:80-86; pkg/gateway/podlifecycle/podsession/slotbinder.go:229-325.

FACT: the leak disposition is keyed on whether the GATEWAY got an acknowledgement, not on what the adapter did with the entry: `cleanly, err := result.Adapter.Shutdown(...); leaked = err != nil || !cleanly`. An entry the adapter removed and acknowledged is released, not leaked. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543.

FACT (checked, no finding): the §7.3 re-attach path needs no explicit "keep the retry off the reclaiming pod" rule, even though r3 deleted the sentence that gave one. `Binder.Resume` claims through `podclaim.Claimer.Claim`, which takes an idle pod, and a pod holding a leaked slot has a live claim, so it is not idle. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1606,:1737-1767.

FACT (checked, no finding): the §7.2 anchors SPEC-2 mints this round match the tree byte for byte — the preamble gloss at spec/07:210 and step 3 at spec/07:213 — and both §6.2 `resuming` bullets do defer their step-by-step sequence to §7.2, so the proposal's justification for editing §7.2 once and leaving the sibling bullet alone holds. EVIDENCE: spec/07_session-lifecycle.md:210,213; spec/06_warm-pod-model.md:234-235.

FACT (checked, no finding): `ConfigureWorkspace` also calls `claimSessionSlot`, so the §4.7 row's "which RPC starts a session" sentence is true of all three admitting RPCs (StartSession, ConfigureWorkspace, Resume). EVIDENCE: pkg/adapter/sdkwarm.go:217; resume.go:50; session.go:111.

OPEN: the §4.7 row's new sentence says which RPC starts a session "depends on the pod's session mode", but `spec/05` binds "session mode" to the execution mode (`session` vs `service`), and the axis that actually decides is pod-warm versus SDK-warm (`preConnect`). Judged below the bar this round because the table's own annotations disambiguate, but a later round may want the term corrected. EVIDENCE: spec-changes.md:180; spec/05_runtime-registry-and-pool-model.md:385,:395; spec/06_warm-pod-model.md:69.

OPEN: §7.1's trigger noun-phrase is "A gateway bind attempt that fails", while SPEC-3 and SPEC-4 say "abandoned or fails" and §7.2/§6.2 route a client-CANCELLED re-attach through the same obligation. The window sentence that follows ("begins with an attempt's first such RPC and ends when that attempt has the session running") probably carries the abandon case, which is why it was not filed. EVIDENCE: spec-changes.md:216,:400,:435; spec/07:213.

### [spec.3.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens — BECAUSE every
observability surface the staged spec edits touch stays consistent after application, and the two
candidates that looked live (the `leaked` terminal's route into the §5.2 ledger, and the
`concurrent_slots_exhausted` gloss) are already on the refuted list — ALTERNATIVES: I considered
filing the `slot_cleanup ──→ leaked` fence gloss ("cleanup timeout exceeded", spec/06:148) and the
whole-pod replacement trigger's identical gloss (spec/05:562) as newly falsified by the
unacknowledged-reclaim leak route, and dropped both: §5.2's own `**Slot cleanup:**` bullet already
says "If cleanup fails, the slot is leaked", which is broader than the gloss, so the gloss is
pre-existing imprecision of exactly the kind the `concurrent_slots_exhausted` refutation rejected.

FACT: the proposal touches NO metric, alert, or CRD condition, and spec/16 needs no edit. Verified
directly: the staged spec text contains no metric name, no alert name and no condition name (grep
over spec-changes.md for metric/alert/gauge/operator returns only the served-session-count clause at
spec-changes.md:400). — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:400

FACT: there is NO alert on any slot metric, so the change cannot orphan one. `lenny_slot_failure_total`
and `lenny_slot_pod_replacement_total` appear in the §16 inventory and in docs/reference/metrics.md
with no alert-catalog row, and `lenny_adapter_leaked_slots` is named only in spec/06 and spec/05 —
it is not in spec/16's inventory at all (pre-existing gap, unrelated to 0081). — EVIDENCE:
spec/16_observability.md:14,:15; docs/reference/metrics.md:166-167; spec/06_warm-pod-model.md:160

FACT: `ReportSessionScrub` has NO missing-report timeout, unlike `ReportPodScrub`. So SPEC-3's
withheld report trips no gateway-side watchdog and produces no spurious retire. Its three spec sites
are spec/05:453, spec/05:545 and spec/04:692, plus the `sessions_served` column contract at
spec/12:481; only `ReportPodScrub`'s §4.7 row carries "a missing report is bounded by a gateway-side
timeout ... after which the pod is retired". — EVIDENCE: spec/04_system-components.md:692,:693

FACT: every markdown anchor the staged text mints resolves, including the three the round-1 anchor
sweep did not cover because later rounds added them: `#49-credential-leasing-service`
(spec/04:1099 `### 4.9 Credential Leasing Service`), `#73-retry-and-resume` (spec/07:378) and
`#151-rest-api` (spec/15:614). The standing-context sweep entry lists only six anchors; these three
are extra and are now checked. — EVIDENCE: spec/04_system-components.md:1099;
spec/07_session-lifecycle.md:378; spec/15_external-api-surface.md:614

FACT: SPEC-3's two supporting claims about §5.2 check out verbatim. The credential path
`/run/lenny/slots/{sessionId}/credentials.json` is §5.2 scrub step 0's own path, and §5.2's recycle
paragraph does already presuppose the per-slot credential lease is gone before `cleanupCommands`
run ("after every ended session's per-slot tree and credential lease have been removed"). The §4.9
direct-mode timer the new action-list clause cancels is the one at spec/04:1169 (adapter arms a
local timer per lease `expiresAt`; on fire it deletes the credential file and reports `AUTH_EXPIRED`),
and cancelling it at slot cleanup contradicts nothing there. — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:455,:461,:471; spec/04_system-components.md:1169

FACT: §29 is NOT a missing edit site for SPEC-1. §29 item 12 is the only `Shutdown` occurrence in
spec/29 and it is explicitly scoped "On a session end triggered by `POST .../terminate`, by
`DELETE ...`, or by an expiry timer" — a started session, for which the staged runtime-teardown
precondition holds — so the sentence stays true after the two-teardown split. The failed-bind
reclaim simply has no §29 trace, and an absent trace is not a contradiction. This closes the §29
half of the standing OPEN. — EVIDENCE: spec/29_communication-scenarios.md:692-701

WATCHOUT: `receiving_uploads` names two different machines. It is a per-slot sub-state in §6.2's
fence AND, per spec/15:672, a "fine session state" said to be "tracked solely in the Postgres
session model ([Section 7.2], 8.8)" — yet the string appears nowhere in spec/07 at all. Do not read
SPEC-4's "every earlier stage ... leaves the slot in `receiving_uploads`" as a claim about the
session model, and do not file the §15:672 dangling reference against 0081: it predates the
proposal. — EVIDENCE: spec/15_external-api-surface.md:672; spec/07_session-lifecycle.md (no match)

WATCHOUT: the whole snapshot diff for round 3 was empty (`diff -rq scratchpad/cp-snap/0081/spec-r3
proposals/0081_.../` returns nothing), so the "read the changed sections first" instruction had no
target this round. Do not assume a fix round landed edits; check the diff before budgeting effort on it.

USEFUL [Standing context, "Dead end" bullets]: the four dead-end entries (adapter cannot see the
leak; withheld report contradicts §4.7/§12; SPEC-3's insert and the concurrency-scoped bullet;
`releases the slot reservation` versus the leaked hold) each cut a candidate of mine before I spent
a verification pair on it. They are the highest-value part of the section for this lens.

### [spec.3.review-reliability.1]

FACT: the adapter's refusal of a repeat start is gated on `st.started`, NOT on entry presence or on the binding. `claimSessionSlotUnderLock` calls `ensureSlotStateLocked` (which returns an existing entry, or creates one) and only then checks `if st.started`; `StartSession` passes `idempotentRepeat=false`. So a residue entry that is present-unbound (class 1) or bound-unstarted (class 2) admits a second start silently — EVIDENCE: pkg/adapter/slotsession.go:75-89; pkg/adapter/session.go:111; pkg/adapter/slot.go:105-125; pkg/adapter/slotcreds.go:26-38.

FACT: `BindReservedSlot` re-runs the WHOLE post-reservation sequence (`materializeSlot`: stage, finalize, setup, AssignCredentials, StartSession) against the persisted `row.PodAssignment` and `row.ID`, so a retried §15.1 `/start` onto a create-time-reserved slot re-enters every stage on the same pod under the same slot id — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:192-224,:230-256,:259-265.

FILED: the create-time-reserved edge-case bullet (spec-changes.md:110-119) claims "when the reclaim left the adapter's entry in place the adapter refuses the second admitted start outright". True only when the first attempt reached `st.started`; false for the two residue classes the proposal exists to fix. That kills half of the bullet's "bounded on both sides" claim and leaves the one path the round deliberately carved out of §5.2's placement constraint with no bound at all.

FACT (closes standing-context OPEN "Resume path and the exclusion", review-log.md:111): a §6.2 `resuming` retry CANNOT re-pick the pod holding an unacknowledged reclaim. `Binder.Resume` claims through `b.connect`, which calls `podclaim.Claimer.Claim` — an IDLE-pod claim, never `ClaimSlot`'s claimed-pod pass. A pod whose slot was released `leaked=true` keeps its per-pod claim (no `DeleteClaim`) and is therefore not idle. The §7.1 sentence round 2 deleted ("the leaked slot's held occupancy keeps the reclaiming pod outside") was TRUE; its deletion loses a true statement but opens no hole, so the §7.3 half is prose incompleteness rather than a defect. Do not file it — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1590-1592,:1737-1770; pkg/gateway/podlifecycle/podclaim/slotclaimer.go (leaked early return).

WATCHOUT: the §7.2 mid-resume terminal path (staged step 3) releases the replacement pod "back to the pool via the standard pod release path", while §7.1's exclusive-pod sentence says the pod "retires under the §6.2 pre-attached failure disposition". They look contradictory, but §6.2:283 states the pre-attached disposition itself as "marked `failed` and released back to the pool (or terminated if unhealthy)", and a materially identical finding was already refuted this loop ("§7.1's exclusive-pod disposition is unreachable on the §7.3 re-attach it binds"). Do not re-file — EVIDENCE: spec/06_warm-pod-model.md:283; spec/07_session-lifecycle.md:214; spec-changes.md:120-124,:255.

WATCHOUT: the §7.2 preamble edit deletes the gloss "the agent runtime has not been started or reconnected" but keeps the conclusion "there is no live workspace on the pod to seal", which the proposal's own rationale falsifies. It looks filable and is not: the seal skip is a pre-existing design decision (§7.2 step 2 sets `final_workspace_ref` to the checkpoint and tags `workspaceSnapshotSource: "checkpoint"`), the proposal changes no seal behaviour, and the pre-proposal sentence was already wrong for the same reason (`pkg/adapter/resume.go` reaches `Runtime.Start` today). Premise polish — EVIDENCE: spec/07_session-lifecycle.md:210,:213; spec-changes.md:241-244.

FACT: the shipped `Shutdown` handler already answers `ExitedCleanly: true` for a session it holds no entry for (`bound` is false, `closeErr` stays nil, the response is built unconditionally at the bottom), so SPEC-1's no-op clean-exit sentence codifies shipped behaviour and the adapter-restart residue it hides is pre-existing. Two lenses have now chased this — EVIDENCE: pkg/adapter/session.go:238-241,:290.

### [spec.4.review-applicability.1]

FACT: The round-4 anchor sweep is DONE and clean, including the three NEW §7.2 anchors round 4
minted. A scripted check extracted every fenced block from spec-changes.md and counted its
occurrences across spec/04, /05, /06, /07: every "text to replace" block matches exactly ONCE and
every "replace it with" block matches zero times (i.e. not yet applied). The new §7.2 anchors are
spec/07_session-lifecycle.md:210 (preamble premise sentence, quoted with the "onto a replacement
pod." lead-in and the "The gateway handles" tail so the deletion is unambiguous), :213 ("— the same
artifact that was about to be replayed onto the replacement pod."), and :214 (step 3). Re-run the
count only if spec/ moves. EVIDENCE: spec/07_session-lifecycle.md:210,213,214.

FACT: Every markdown anchor the staged text mints resolves against a real heading:
`#71-normal-flow` (spec/07:3), `#73-retry-and-resume` (spec/07:378), `#62-pod-state-machine`
(spec/06:78), `#52-pool-configuration-and-execution-modes` (spec/05:365), `#47-runtime-adapter`
(spec/04:657), `#479-startup-sequence-for-type-agent-runtimes` (spec/04:848),
`#49-credential-leasing-service` (spec/04:1099), `#151-rest-api` (spec/15:614),
`#1542-rpc-lifecycle-state-machine` (spec/15:1686). Insertion points also resolve: the §7.1 block
lands between spec/07:23 and :24; the SPEC-4 edge after the two-line `receiving_uploads ──→ running`
entry at spec/06:152-153; the SPEC-4 paragraph between the fence close at :156 and
`**`reserved` hold semantics.**` at :158; the §7.3 sentence between :414 and :416.

FACT: the standing-context claim that "On the default disposition the pod is replaced." also appears
verbatim at spec/29_communication-scenarios.md:696 is WRONG. `grep -rn` over spec/ and docs/ returns
exactly one occurrence, spec/04_system-components.md:686. §29:696 carries a differently-worded
restatement ("the adapter closes the session runtime and the pod is replaced"). A reviewer relying on
the two-site claim to argue a missed §29 edit site will find only one site.
CORRECTS [standing context, refuted-finding rationale for the §4.7 default-disposition item].

FACT: the deleted §7.2 premise has exactly two spec sites and BOTH are staged: spec/07:210 (the
preamble sentence) and spec/07:214 (step 3's "no runtime was started on it"). spec/07:220-221's
"no live workspace or running runtime to seal" belongs to the pre-attach collapse, where no pod is
claimed at all, and stays true. So the §7.2 relocation loses no content and needs no third edit.

FACT: `removeSlotTree` runs synchronously inside `Shutdown`, before the response is built
(`_ = removeSlotTree(st)` then `return &adapterv1.ShutdownResponse{...}`), and
`compensateFailedSlotBind` blocks on `cl.Shutdown` inside `materializeSlot`'s failure wrapper. So an
ACKNOWLEDGED reclaim has already finished its tree removal by the time the gateway returns the error
to the client. EVIDENCE: pkg/adapter/session.go:270,291; non-spec-changes.md:315-327,335-347.

MISTAKE (nearly filed, do not re-file): the round-4 create-time-reserved edge-case bullet attaches
"what is not bounded there is a reclaim whose tree removal is still running when the retry
materializes the same tree" to the ACKNOWLEDGED branch, where the fact above says the removal has
completed. It survives because a client that times out and retries POST /start concurrently with the
still-in-flight compensation produces exactly that interleaving, and the reclaim is acknowledged
afterwards. The branch assignment is defensible on that reading; do not spend a round on it.
EVIDENCE: spec-changes.md:113-116; pkg/adapter/session.go:270.

FACT: the earlier "SPEC-4 prose puts a dispatched-but-unreturned StartSession in `receiving_uploads`"
contradiction is genuinely CLOSED, and the closure turns on a reading worth recording so nobody
reopens it. The unedited trigger annotation (spec/06:152-153) reads "session dispatched to runtime
with its session identifier"; SPEC-4's prose glosses that as the runtime "has been given" the
session. The two agree once "a start still in flight" is read as the window between the adapter
ADMITTING `StartSession` and `Runtime.Start` handing the session over — the gateway→adapter RPC is in
flight, so nothing has been dispatched to the runtime yet. Under that reading the new edge
annotation, the SPEC-4 prose, and the untouched trigger all name the same boundary.
USEFUL [standing context, "Do not edit spec/06:152 or docs/reference/state-machines.md:235"].

DECISION: returned an empty findings list — BECAUSE the mechanical application is clean end to end
(anchors unique and verbatim, insertion points unambiguous, no forward reference, SPEC-4's dependency
on SPEC-3's §5.2 sentence is the only cross-deliverable order and the checklist carries it at S4
Depends-on S3, files-touched and the staged blocks are in exact bijection, no staged edit lands in a
generated artifact). ALTERNATIVES: filed nothing on §4.1's retained "per-slot teardown" vocabulary
(pre-existing drift, the edit swaps "per-session teardown" out and does not create it), nothing on
the §6.2 `resuming → completed` sibling gloss (demonstrably already non-exhaustive: it omits §7.2
step 4's `coordination_generation` bump), and nothing on §29 item 12's trigger enumeration (§29's own
preamble makes a trace non-normative and it is scoped to "a session end", which a failed bind is not).

### [spec.4.review-fresh.1]

FACT: Every "text to replace" block in SPEC-1..SPEC-4 still matches the tree byte for byte AND is unique repo-wide across spec/ (mechanically checked: 12 anchor blocks, each found exactly once). All ten markdown fragments the staged text mints resolve (`#47-runtime-adapter`, `#49-credential-leasing-service`, `#479-startup-sequence-for-type-agent-runtimes`, `#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#71-normal-flow`, `#73-retry-and-resume`, `#151-rest-api`, `#1542-rpc-lifecycle-state-machine`). Do not re-run the sweep; re-run only if spec/ moves. — EVIDENCE: spec/04:157,:686,:848-853,:1099; spec/05:453,:545,:557; spec/06:148-157,:234; spec/07:23,:210,:212,:213,:391

FACT: The round-4 diff against scratchpad/cp-snap/0081/spec-r4 is EMPTY — spec-r4 and spec-r4-start are both identical to the current proposal. To see what round 3 changed, diff against `spec-r3`, not `spec-r4`. Cost me a confused minute. — EVIDENCE: scratchpad/cp-snap/0081/spec-r3 vs proposals/0081_.../*.spec-changes.md

FACT: §5.2's `**Slot cleanup:**` CRD validation rule really is concurrency-gated IN CODE, not just in prose. `poolstore.validateSessionPolicy` puts the `cleanupTimeoutSeconds >= maxConcurrentSessions*5` check inside `if sp.MaxConcurrentSessions > 1 {`. So any spec sentence that generalises that bullet across the concurrency boundary generalises an admission rule the code does not apply at concurrency 1. — EVIDENCE: pkg/gateway/runtime/poolstore/poolstore.go:561,:567-570

DECISION: Filed exactly one finding — SPEC-3's appended clause "on a pod of either concurrency" generalises the WHOLE `**Slot cleanup:**` bullet, not just its action list, colliding with the staged §7.1 exclusive-pod carve-out, with §6.2's unchanged fence scoping of `slot_cleanup ──→ leaked`, and with the code-side concurrency gate on the CRD rule — BECAUSE the proposal's own rationale (spec-changes.md:434-438) says the pointer is meant to carry only the action list and that "the CRD validation rule, and the leaked outcome all stand as written", so the staged sentence over-reaches its stated intent. ALTERNATIVES: I considered and rejected filing (a) the §4.1 retained-sentence vocabulary drift ("per-slot teardown" undefined after the split — the sentence is about addressing and stays true), (b) the missing relative ordering of slot release vs runtime teardown in the §4.7 row (the code order is load-bearing at pkg/adapter/session.go:262-271 but the spec never stated it before either), (c) §5.2:459's "The adapter closes the ending session's runtime" being falsified for the concurrent recycle `Shutdown` by SPEC-1's no-op sentence (pre-existing: the recycle `Shutdown` is already a second RPC on a session the adapter no longer holds, slotbinder.go:574), and (d) §7.1's "the pod retires ... the residue does not outlive the pod" against §7.2 step 3's "released back to the pool" (close variant of an already-refuted finding).

WATCHOUT: Do NOT file "the §7.2 aborted re-attach releases the replacement pod to the pool, so §7.1's exclusive-pod 'residue dies with the pod' is false there". It is a close variant of the refuted "§7.1's exclusive-pod disposition is unreachable on the §7.3 re-attach" and burns two verifiers. — EVIDENCE: spec-changes.md:224,:277; spec/06:234

FACT: The session-mode (exclusive) release sends ONE `ShutdownRecycle`, which the adapter DOES hold an entry for; the concurrent release sends a plain `Shutdown` and then a SECOND `ShutdownRecycle` reusing the just-released session id. That asymmetry is why SPEC-1's no-op sentence reads differently on the two paths. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:2033-2042; slotbinder.go:542,:572-578

UNVERIFIED: §29 item 12 still enumerates `Shutdown`'s triggers as terminate / DELETE / expiry and does not name the failed-bind reclaim. §29's preamble makes a trace non-normative ("Where a trace and a cited section disagree, the cited section is the normative statement and the trace is the defect", spec/29:23-25), which is why three rounds have judged it optional. Someone should decide once whether an added trace step is owed, rather than re-deriving the preamble each round.

### [spec.4.review-mechanism.1]

DECISION: returned an empty findings list for the end-to-end-mechanism lens on round 4 — BECAUSE every flow I traced from origin to final effect (the two-teardown split, the §7.1 obligation window, the §5.2 placement constraint, the §7.2/§7.3/§6.2 resume pointers, the SPEC-3 report boundary, the SPEC-4 edge) resolves to a determinate behaviour that the tree supports, and the three candidates I developed furthest are each either explicitly marked non-refileable, a close variant of an existing refutation, or prose completeness — ALTERNATIVES: (a) the "dispatched" boundary clash (see MISTAKE below), barred; (b) §7.1's "the slot identifier is the session identifier" against §5.2's retained "always assigned to a **new slot**" and the **Fresh workspace guarantee**, dropped because the placement rule keeps the hazard off every retry §5.2 places and the residual clash is nomenclature; (c) §6.2's mid-resume cancel clause stating the reclaim unconditionally while §7.1 scopes it to a post-first-RPC failure, dropped because the clause names §7.1 and resolves through it.

FACT: `bindReservedSlot` runs the WHOLE pod-side sequence in one call — resolve, dial, negotiate, then `materializeSlot` (stage → finalize → setup → credentials → StartSession). So on the concurrent-slot path the "creation finalize block" and the "§15.1 start transition" are NOT two separate pod-side attempts leaving residue between them, and round 4's narrowing of the §7.1 window from "has the session running on the pod" to "ends when that attempt succeeds" opens no gap. It also makes the edge-case bullet "A bind abandoned at the connect stage … the adapter holds nothing" true: the connect stage precedes every workspace RPC on this path. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:210-256, :265-300.

FACT: all three session-start admission points call `claimSessionSlot`, which sets `st.started`: `StartSession` (session.go:111), `Resume` (resume.go:50), and SDK-warm `ConfigureWorkspace` (sdkwarm.go:217). All three also reach `noteRuntimeStarted` (session.go:163, resume.go:144, sdkwarm.go:261, the last guarded on `fresh`). So SPEC-1's generalised precondition ("which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed") is accurate, and neither the `started` gate nor the `runtimeLive` report gate is unreachable for SDK-warm or resumed sessions. This closes the r3 UNVERIFIED asking whether CODE-1 needs a code change once the predicate names Resume and ConfigureWorkspace: it does not.

FACT: `Binder.Resume` acquires its replacement pod through `podclaim.Claimer.Claim`, which skips any Sandbox whose `Status.Phase != Idle` and then CREATEs the deterministic per-pod claim, treating AlreadyExists/Forbidden as "already claimed, skip". A pod holding an unacknowledged reclaim has a `bound` claim and nonzero occupancy, so it is outside the candidate set. The r3 WATCHOUT ("the §7.3 re-attach is now covered by nothing written down") is a record gap rather than a mechanism gap. EVIDENCE: pkg/gateway/podlifecycle/podclaim/claimer.go:92-140; pkg/gateway/podlifecycle/podsession/binder.go:1590-1592, :1737-1768.

FACT: `applySlotRetryPolicy` already applies §5.2's slot retry policy to BIND failures (it branches on `*podsession.SlotBindError` returned from `BindSlot`), not only to adapter-reported slot failures of a running session. So SPEC-2's placement constraint lands in the policy that actually places the retry it constrains; the "§5.2's trigger is an adapter-emitted slot-failure event, so the constraint reaches nothing" reading is wrong. EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884.

FACT: `deregisterSlotLocked` runs UNCONDITIONALLY in the shipped `Shutdown` handler and already removes the entry and cancels the §4.9 timers for an unbound entry; only `removeSlotTree`, the drain signal, the runtime close and the scrub report sit behind `bound := removed && st.sessionID != ""`. So CODE-1's split moves the tree removal, not the deregistration. EVIDENCE: pkg/adapter/session.go:238-241, :270; pkg/adapter/slotsession.go:174-188.

MISTAKE (do not re-derive, and do not file): the staged SPEC-4 prose asserts that the unedited `receiving_uploads → running` trigger names the moment "the pod's shared runtime process has been given the session", while that trigger reads "workspace ready, session dispatched to runtime with its session identifier" and a start still in flight has been dispatched. `spec.3.review-*` recorded this as only PARTLY discharged by the round-1 fix and as non-refileable because the orchestrator's fixed-list names it. I re-derived it independently and reached the same place. It is the one substantive open boundary question in the proposal and a human should settle it. EVIDENCE: spec/06_warm-pod-model.md:152-153; docs/reference/state-machines.md:235; spec-changes.md:462.

DEFERRED [proposals/0081_.../0081_....spec-changes.md Design section, :63-66]: "A §15.1 start onto a slot reserved at creation is placed by neither mechanism" still has no antecedent for "neither". Round 3 deleted the second placement mechanism (the §7.3 idle-inventory claim) from the surrounding text and round 4 did not restore it, so only §5.2's slot retry policy is named. What is true: two mechanisms place a further attempt at the same session — §5.2's slot retry policy, and the §7.3 re-attach's whole-pod idle claim through `podclaim.Claimer.Claim`, which cannot select a pod whose occupancy the leaked slot holds. The §15.1 create-time-reserved start is placed by neither. Cosmetic on its own; recorded so the sentence is repaired with the fact rather than by deleting "neither".

DEFERRED [proposals/0081_.../0081_....spec-changes.md, edge case "A client retry of the §15.1 start after a failed bind on a create-time-reserved slot"]: the round-4 rewrite says "When the adapter does not acknowledge the reclaim … the adapter's entry survives in whatever state the failed stage left it." That is false for the delivered-but-unanswered case: a `Shutdown` the adapter executed while the response was lost removes the entry, and the gateway still classifies the reclaim as unacknowledged (`err != nil || !cleanly`). What is true instead: an unacknowledged reclaim leaves the gateway unable to tell whether the entry survives, so the retry may find either a surviving entry or none. The later sentences of the same bullet already reason about a lagging reclaim, so the correction is one clause.

USEFUL [Standing context / Traps, "Do not edit spec/06:152" and its CORRECTS]: the pair of them is what stopped me spending a verification budget on the dispatched-versus-given clash and then filing a barred finding. The "anchor sweep is done" entry saved a full pass again.

USEFUL [Standing context / Settled, "`SlotClaimer.ReleaseSlot(leaked=true)` returns early"]: it is the fact that makes "releases the slot reservation afterwards" and "the leaked disposition holds occupancy" compatible, and it is the first thing a mechanism lens reaches for. Keep it.

### [spec.4.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE every reliability-lens hazard I could still
construct against the staged spec edits is either already on the refuted list, already recorded in
Standing context as pre-existing, or has its only remedy in the code lane this loop may not edit —
ALTERNATIVES: I built and then dropped four candidates, listed below with why, so the next round
does not rebuild them.

FACT: the round-4 snapshot is byte-identical to the proposal. `diff -rq
/home/ec2-user/lenny/scratchpad/cp-snap/0081/spec-r4
proposals/0081_.../` returns nothing, so there was no "what changed since last round" hunk to read
hardest. The reading-order instruction was inert for this pass; do not spend time hunting for the
diff. EVIDENCE: scratchpad/cp-snap/0081/spec-r4 vs the proposal directory, 2026-09-09.

FACT: `Binder.Resume` claims its replacement pod through `connect` → `podclaim.Claimer.Claim`
(IDLE-pod claim) on BOTH exclusive and concurrent pools, and only then calls `reserveResumeSlot`
on that already-claimed pod. It never goes through `ClaimSlot`'s candidate passes. EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:1590-1605,:1663-1700,:1737-1755. This CLOSES the
Standing-context OPEN "Resume path and the exclusion": a pod holding an unacknowledged reclaim
keeps a live `SandboxClaim` (`ReleaseSlot` returns early on `leaked`, no `DeleteClaim`), so it is
not idle inventory and `Claimer.Claim` cannot re-pick it. The DECISION's rationale ("the leaked
slot's held occupancy keeps the reclaiming pod outside") holds for the resume path as written.

FACT: `SlotClaimer.ReleaseSlot`'s `recycle` parameter, not the `leaked` one, is what patches the
claim `bound → recycling`, arms the missing-report timeout and returns `recycled`. `Binder.
ReleaseSlotReservation` passes `recycle=false`, so a failed bind that drives occupancy to zero on a
recycling pool DELETEs the claim and the pod retires rather than recycling. That is a retirement
rather than a leak, so the Standing-context OPEN "Recycle boundary on a failed bind" is bounded and
not a residue class. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:819-887.

FACT: §7.1's "On a pod serving one session the failed attempt releases the pod's claim and the pod
retires" is accurate for the exclusive §15.1 start path in code: `Binder.Launch`'s `reclaim()`
calls `failPhase`, which drains the pod. EVIDENCE: podsession/binder.go:994-999,:1072-1082. The
one exclusive route that returns the pod to the POOL rather than draining it is `Launch`'s
reconnect failure via `ReclaimClaimed` (binder.go:977-991), and that failure precedes the attempt's
first session RPC, so §7.1 owes no reclaim there. Do not file the reconnect branch.

FACT: "direct-delivery-mode lease-expiry timers" in SPEC-3's widened action list is the correct
qualifier. spec/04_system-components.md:1169 states the adapter arms a local timer per lease in
direct mode only and that proxy mode needs no adapter-side timer. The staged sentence's scoping is
right and needs no widening.

WATCHOUT: the four candidates I built and dropped, so nobody rebuilds them.
  (1) "§7.1 says an exclusive pod retires, but staged §7.2 step 3 and §6.2:234 release the
      half-claimed replacement pod BACK TO THE POOL, so on a mid-resume abort the residue re-enters
      warm inventory with no reclaimer and no `leaked` disposition (which §7.1 denies to exclusive
      pods)." This is a close variant of the already-refuted "§7.1's exclusive-pod disposition is
      unreachable on the §7.3 re-attach it binds"; that refutation quotes spec/06:234's
      release-to-pool clause verbatim and reasons past it. Filing the residue half on the same
      evidence would be refuted the same way. If a later round wants it, it needs NEW evidence that
      a pooled replacement pod actually keeps the residue (i.e. read the mid-resume terminal
      handler's release path in the gateway and show it is not a drain).
  (2) "No deadline is stated for the reclaim, so a hung adapter stalls §7.2 step 3 and delays the
      step-4 `coordination_generation` bump that fences a stale coordinator." The spec deliberately
      delegates the figure (spec-changes.md:227-228) and the code lane stages a `slotCleanupBudget`
      helper (non-spec-changes.md:278). Filing it would be over-specification.
  (3) "`ReportSessionScrub` is at-least-once and `RecordSessionScrub` has no per-session dedup, so
      SPEC-3's new 'at most one cleanup-outcome report per session release' invariant is
      unenforceable under report retry." The absent dedup is pre-existing and unchanged by this
      proposal; the invariant is a statement about how many cleanups file, not about wire delivery.
  (4) "The reclaim's runtime teardown for a start still in flight double-closes the runtime against
      the refused start's own take-off." Real (Standing context "Tier-7a closed exactly once"), but
      the remedy is an idempotent take-off in `pkg/adapter`, which is code-lane and out of this
      loop's scope.

USEFUL [Standing context / Traps]: the trap list saved me from re-filing at least three of the
recurring families (leaked-holds-occupancy, withheld-report-blinds-the-leak-signal,
`releases the slot reservation` vs the leaked hold). Every one of them looked filable on first
reading of the staged §7.1 paragraph. Read the Traps section before drafting anything about
`leaked`.

### [spec.5.fix-G1.1]

DECISION: closed the §29 contradiction by adding a third SPEC-1 block staging one appended sentence on spec/29_communication-scenarios.md §29.4 step 13, and listing spec/29 in `## Spec files touched` — BECAUSE SPEC-1's §4.7 `Shutdown` row is the first place any `spec/` text states the drain gate (`grep -rn "graceful-shutdown signal" spec/` returns only spec/12_storage-architecture.md:913), so before SPEC-1 step 13 is merely stale against the code and after SPEC-1 it positively contradicts §4.7 on a `maxConcurrentSessions > 1` pod, which §29.10 explicitly scopes §29.2 through §29.9 to cover — ALTERNATIVES: putting the condition in §29.10's shared list (rejected: that list names no shared runtime process and no CH-RUNTIMEOPS, so it would first have to establish the runtime process is pod-shared, a broader claim, and step 13 would still read unconditionally); doing nothing on §29's preamble rule that the cited section governs (rejected: the preamble resolves a reader's ambiguity and is not a licence to stage a contradiction deliberately); deleting the co-tenancy clause from the §4.7 row (rejected: it is shipped behaviour at pkg/adapter/session.go:259-260 and is the second half of the two-teardown split SPEC-1 exists to state); hoisting the exception into step 13's opening condition clause (rejected: step 13 puts its integration-level exception in the body, so one hoisted and one in the body would read inconsistently).

MISTAKE: `[spec.?]` WATCHOUT at review-log.md:1139 closes with "The same holds for item 13's `terminate` frame", meaning item 13 needs no edit. That generalisation is over-broad and it cost this round a finding. What the entry actually establishes is true: item 13 is not falsified by the TWO-TEARDOWN SPLIT, because item 13 is scoped to a session that ran. What falsifies item 13 is a DIFFERENT sentence of SPEC-1, the co-tenancy gate on the graceful-shutdown signal, which the entry never considered, and that gate bites exactly the session ends item 13 does enumerate.

CORRECTS [the review-log.md:1139 WATCHOUT on spec/29 items 12 and 13]: its conclusion holds for item 12 and does not hold for item 13. Item 12 stays un-edited, because it states that the adapter closes the session runtime and `s.Runtime.Close` runs for every bound entry the call removed with no co-tenancy gate (pkg/adapter/session.go:264). Item 13 now carries an appended co-tenancy sentence under SPEC-1.

FACT: the drain gate and the runtime close have different preconditions in shipped code, which is why the carve-out lands on the frame step and nowhere else. `Shutdown` sends the CH-RUNTIMEOPS drain only under `if !boundRemains`, then closes the runtime unconditionally for a bound entry. EVIDENCE: pkg/adapter/session.go:238 (`boundRemains` from `deregisterSlotLocked`), :259-260 (`if !boundRemains { s.drainViaLifecycle(...) }`), :264 (`s.Runtime.Close`).

FACT: no `spec/` text stated the drain gate before this proposal. It lives only at docs/reference/adapter-contract.md:75 ("When the release leaves the pod holding no other bound session, the adapter also sends the CH-RUNTIMEOPS drain signal.") and in code. EVIDENCE: `grep -rn "graceful-shutdown signal" spec/` returns only spec/12_storage-architecture.md:913 (tenant disable).

WATCHOUT: do NOT extend this carve-out to §29.9 item 4b (the eviction trace's terminate-frame mention). It records that the specification does not state at what point of the eviction path the frame is sent, which is an ordering claim the co-tenancy gate does not answer. EVIDENCE: spec/29_communication-scenarios.md:1412-1416.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: step **S1 · spec** (line 6) now enumerates SPEC-1's sites as §4.1's scope sentence and the §4.7 `Shutdown` row only. That is false: SPEC-1 now also stages one appended sentence on spec/29_communication-scenarios.md §29.4 session-end step 13, restating the graceful-shutdown signal's co-tenancy condition and citing §4.7. Append to S1's description, after "...and state the no-op answer for a session the adapter holds no entry for.", the sentence "§29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7." S1's tiers (0, 11) and its "Depends on: —" are unchanged, because the added site is spec prose in the same lane.

### [spec.5.fix-design-G1.1]

DECISION: file the §29 edit site as real and stage ONE sentence inside §29.4 item 13, added to SPEC-1, plus `spec/29_communication-scenarios.md` in the "Spec files touched" list — BECAUSE SPEC-1's §4.7 `Shutdown` row is the first place any `spec/` text states the drain gate (grep for "graceful-shutdown signal" over spec/ returns only spec/12:913), so before SPEC-1 item 13 is merely stale against the code and after SPEC-1 it positively contradicts §4.7 on a `maxConcurrentSessions > 1` pod — ALTERNATIVES: (a) a bullet in §29.10's "Shared by the whole pod" list instead — rejected, that list currently names no shared runtime process at all, so adding the frame there is a broader claim than this fix owns and it still leaves item 13's sentence reading unconditionally; (b) do nothing on §29's preamble subordination rule — rejected, the preamble calls such a trace "the defect" rather than licensing a knowingly-staged contradiction; (c) delete the co-tenancy condition from SPEC-1's §4.7 row — rejected, it is shipped behaviour at pkg/adapter/session.go:259-260 and is the second half of the two-teardown split SPEC-1 exists to state.

WATCHOUT: the standing review-log WATCHOUT at review-log.md:1139 ends "The same holds for item 13's `terminate` frame. Do not file it as a missed edit site." Read narrowly it is correct and read as written it is over-general, and a fixer that takes it as blanket cover will refuse this edit. Its reasoning is that item 13 is scoped to "a session end triggered by terminate / DELETE / expiry", which answers the *two-teardown split* (the failed-bind reclaim is not such an end). It does not touch the *co-tenancy gate*, which is a different sentence of SPEC-1 and bites exactly the ends item 13 does enumerate. EVIDENCE: proposals/0081_.../0081_....review-log.md:1139; spec/29_communication-scenarios.md:705-712.

CORRECTS [spec.3 WATCHOUT at review-log.md:1139]: its final sentence generalises a conclusion drawn about the two-teardown split onto item 13's `terminate` frame. Item 13 IS a missed edit site, for the co-tenancy gate rather than for the split.

FACT: the drain gate is shipped and unambiguous. `Shutdown` deregisters under the lock, and `drainViaLifecycle` (which sends the CH-RUNTIMEOPS `terminate` frame) runs only under `if !boundRemains`; `s.Runtime.Close` runs whenever the removed entry was bound, with no co-tenancy gate. So the co-tenancy carve-out belongs on item 13 (the frame) and NOT on item 12 (the `Shutdown` RPC and the runtime close). EVIDENCE: pkg/adapter/session.go:238,259-260,264.

FACT: §4.7's staged phrase "the [§15.4.2] graceful-shutdown signal" IS §29 item 13's `terminate` frame. §28.5.3's CH-RUNTIMEOPS message-schema row for `terminate` carries the Notes "Graceful shutdown signal.", and §15.4.2's `DRAINING` row is the state the frame drives. Do not re-derive this equivalence; it is the whole basis of the contradiction. EVIDENCE: spec/28_communication-channels.md:1082; spec/15_external-api-surface.md:1698-1700.

FACT: §29.9 item 4b is NOT a companion site. It says the specification does not state at what point of the eviction path the adapter sends the `terminate` frame, which is an ordering claim the co-tenancy gate does not answer. EVIDENCE: spec/29_communication-scenarios.md:1412-1416.

OPEN: §29.10's "Shared by the whole pod" list names the transport, the message plane, the process namespace, the occupancy primitives, the shared asset tree, the pod phase, the egress identity, the MCP surface, and the coordinator-loss hold, and names neither the pod's shared runtime process nor CH-RUNTIMEOPS. That is a pre-existing gap of its own and a candidate finding for a later round; it is deliberately NOT pulled into this edit. EVIDENCE: spec/29_communication-scenarios.md:1478-1533.

FACT: this particular fix has no effect on the 0080 §1.19 fence-refusal predicates. `boundSlotState` / `checkSessionBound` read the bound/unbound membership, and the membership change in this proposal comes from SPEC-1's slot-release precondition (entry present rather than bound), not from the §29 trace sentence. EVIDENCE: pkg/adapter/slotsession.go:274-290; pkg/adapter/coordination.go:116.

### [spec.5.review-applicability.1]

DECISION: returned an empty findings list for round 5 — BECAUSE every staged spec edit applies deterministically against the current tree — ALTERNATIVES: filing the §4.1 retired-vocabulary drift (open item #119) and the "the pod's session mode" vocabulary slip in the staged §4.7 row; both are wording, neither blocks application, and neither is in classes 1-6 of this lens.

FACT: the round-5 snapshot is byte-identical to the live proposal. `diff -ru /home/ec2-user/lenny/scratchpad/cp-snap/0081/spec-r5 <proposal dir>` produced no output, so there was no "what changed" hunk to read first; the whole document had to be re-read cold. EVIDENCE: scratchpad/cp-snap/0081/spec-r5 (all nine files, same sizes as the proposal dir).

FACT: the anchor sweep now covers FIFTEEN edit sites, not the six the standing-context entry names, and all fifteen were re-verified byte-for-byte this round. Confirmed unique and matching: spec/04:157 (§4.1 third sentence), spec/04:686 (§4.7 `Shutdown` row opening), spec/04:854 (§4.7.9 step 5), spec/05:453 (`**Scrub model.**` paragraph), spec/05:545 (`**Slot cleanup:**` action list), spec/05:555 (`**Max retries:**` pod-selection sentence), spec/06:150-153 (fence heading + `receiving_uploads ──→ running` entry), spec/06:156-158 (fence close, then `**`reserved` hold semantics.**`), spec/06:234 (`resuming → cancelled` clause), spec/07:23 (atomicity parenthetical + paragraph tail "…regardless of the flag."), spec/07:24 (the `(executionMode, isolationProfile, scrubPolicy summary)` continuation line the new §7.1 paragraph inserts before), spec/07:210 (§7.2 preamble premise), spec/07:213 (§7.2 step 2 sentence), spec/07:214 (§7.2 step 3), spec/07:414 (§7.3 numbered-list tail).

FACT: every markdown anchor the staged text mints resolves. Newly checked this round beyond the six in the standing context: `#49-credential-leasing-service` (spec/04:1099), `#151-rest-api` (spec/15:614), `#479-startup-sequence-for-type-agent-runtimes` (spec/04:848), `#1542-rpc-lifecycle-state-machine` (spec/15:1686), `#73-retry-and-resume` (spec/07:378), `#72-interactive-session-model` (spec/07:115).

FACT: no tier-0 or tier-11 gate hard-fails on the staged spec text. The citation resolver and the line-citation ratchet target the retired `§X.Y line(s) L` form only (tests/tier0_static/spec_citation_resolution_test.go:17-23), and the staged text carries no line citation. `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37` pins the four general per-slot edges by substring and the two concurrency-scoped ones by exclusion, so SPEC-4's added `receiving_uploads ──→ slot_cleanup` in the general block passes untouched. `tests/tier11_docs/credential_path_literal_sweep_test.go` sweeps for the RETIRED pod-global `/run/lenny/credentials.json` (constant at adapter_manifest_credentials_path_doc_reconciliation_test.go:40), not the per-slot `/run/lenny/slots/{sessionId}/` SPEC-3 adds. S4's checklist line already records its own tier-11/tier-1 deferral to S5/S6, so that is a recorded disposition rather than an undisposed gate.

FACT: the §7.2 step-2 instruction is a mid-paragraph insert, not an append to the end of step 2. Its verbatim anchor "— the same artifact that was about to be replayed onto the replacement pod." is followed in the live file by the `final_workspace_ref` / `workspaceSnapshotSource` sentence, so the new sentence lands between them. The "Giving:" block makes the result explicit, so it is deterministic rather than ambiguous. Do not file it. EVIDENCE: spec/07_session-lifecycle.md:213.

FACT: `claimSessionSlotUnderLock` refuses a repeat claim on `st.started` alone, which is exactly the predicate the create-time-reserved edge-case bullet states ("The adapter refuses the retried start only when the first attempt had already admitted a start on that slot"). EVIDENCE: pkg/adapter/slotsession.go:79-86.

WATCHOUT: SPEC-2 edits §5.2's `**Max retries:**` bullet at spec/05:555 while SPEC-3 edits §5.2's `**Slot cleanup:**` bullet at :545 and `**Scrub model.**` at :453. Three anchors in one section across two deliverables that land in two different checklist steps (S2 and S3). None overlaps, but a fixer widening any one of them must re-check the other two's verbatim blocks before declaring the sweep clean. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,545,555.

WATCHOUT: the §5.2 `**Slot retry policy**` block SPEC-2 edits sits under the heading `**Slot retry policy (`maxConcurrentSessions > 1`)**` at spec/05:553, the same concurrency scoping the standing context already records for the `**Slot cleanup:**` bullet at :542. §7.1's staged pointer ("[Section 5.2] keeps the retries its slot retry policy places off that pod") is stated two sentences after the "On a pod serving concurrent sessions" scoping, so the pointer and the target agree — but only because of that ordering. Moving the pointer sentence would break the agreement silently. EVIDENCE: spec-changes.md:224; spec/05:553.

UNVERIFIED: the four-site agreement standing-context item 38 asks a later reviewer to confirm is still not closed on its fourth site. SPEC-4's fence annotation and SPEC-4's prose now agree with each other ("a start still in flight included" / "a start still in flight, whose session the runtime has not yet acknowledged, leave the slot in `receiving_uploads`"), but the untouched `receiving_uploads ──→ running` trigger still reads "workspace ready, session dispatched to runtime with its session identifier", and trap item 73 glosses that as the ADMITTED-start boundary. If that gloss is right the two boundaries are the same moment and SPEC-4's prose is wrong; if "dispatched" means the adapter's `Runtime.Start` call, a dispatched-but-unacknowledged start sits on both sides. This is the "already found and fixed" item, so it is out of bounds to re-file, but nobody has actually reconciled the trigger's words with the prose's claim that they name the same moment. EVIDENCE: spec/06_warm-pod-model.md:152-153; spec-changes.md:447-449,462; review-log.md:38,73.

USEFUL [standing context "The anchor sweep is done; do not repeat it"]: correct for the six sites it names and it saved the byte-for-byte re-derivation of those. It is now INCOMPLETE, because rounds 2-4 added nine more anchors (the whole §7.2 block, §7.3, the §6.2 `resuming` clause, §5.2 `**Max retries:**`, and §5.2's `**Slot cleanup:**` action list). See the fifteen-site FACT above; a future round should trust that list rather than the six.

### [spec.5.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every concrete citation in spec-changes.md verifies, and the three or four residual imprecisions I found are already recorded as OPEN in the standing context or sit inside classes the material skeptic has refuted in earlier rounds — ALTERNATIVES: filing the "§4.6 pod binding" attribution, the "closes by stating" mis-position, and the "Neither bullet enumerates that sequence" tension; all rejected as wording-level and below the bar (details below so nobody re-derives them).

FACT: the proposal document did not change between rounds 4 and 5. `diff -rq scratchpad/cp-snap/0081/spec-r5 proposals/0081_.../` is empty and `diff -rq scratchpad/cp-snap/0081/spec-r4 ...` differs only in the review log. Read-the-diff-first guidance produces nothing this round; the whole document is equally old. EVIDENCE: scratchpad/cp-snap/0081/spec-r4, spec-r5.

FACT: the anchor sweep still passes byte for byte on the current tree. All fourteen verbatim "text to replace" blocks in SPEC-1..SPEC-4 occur exactly once. Current line numbers: spec/04:157 (§4.1), spec/04:686 (§4.7 Shutdown row), spec/04:854 (§4.7.9 step 5), spec/05:453 (Scrub model), spec/05:545 (Slot cleanup bullet), spec/05:555 (Max retries), spec/06:150 (fence heading), spec/06:158 (`reserved` hold semantics, the SPEC-4 prose insertion point), spec/06:234 (resuming cancel bullet), spec/07:23 (§7.1 atomicity paragraph, both the parenthetical and its closing sentence), spec/07:210/213/214 (§7.2 preamble, step 2, step 3), spec/07:414 (§7.3 list item 4). Anchors `#47-runtime-adapter`, `#479-startup-sequence-for-type-agent-runtimes`, `#49-credential-leasing-service`, `#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#71-normal-flow`, `#73-retry-and-resume`, `#151-rest-api`, `#1542-rpc-lifecycle-state-machine` all resolve to real headings.

FACT: SPEC-3's two shipped-behaviour claims both hold. `slotlayout.RemoveTree` removes `p.CredentialsDir` alongside the slot root, `/sessions` and `/artifacts` (pkg/adapter/slotlayout/tree.go:60), and `deregisterSlotLocked` cancels every armed provider timer before deleting the entry (pkg/adapter/slotsession.go:174-181). §5.2's recycle-lifecycle paragraph really does presuppose the wider list ("after every ended session's per-slot tree and credential lease have been removed", spec/05:455) and scrub step 0 is the whole-pod credential purge (spec/05:461). The "direct-delivery-mode" qualifier on the §4.9 timers is exact: spec/04:1169 arms the adapter-side timer in direct mode only and says proxy mode needs none.

FACT: the §4.7 sentence "which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed" is complete and correct. `claimSessionSlotUnderLock` has exactly three production callers — session.go:111 (`StartSession`), sdkwarm.go:217 (`ConfigureWorkspace`), resume.go:50 (`Resume`) — and all three RPCs are rows in the §4.7 Gateway→Adapter table (spec/04:672, :673, :683). `stageWorkspace` gates `PrepareWorkspace` on `len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1323-1328), and `claimSessionSlotUnderLock` refuses only on `st.started` (slotsession.go:78-84), so both edge-case bullets that rest on those facts are accurate.

WATCHOUT: three imprecisions I verified and deliberately did NOT file. (1) The edge-case bullet's "The row keeps its §4.6 pod binding" — §4.6 is "Pod Lifecycle Controllers" and owns the `SandboxClaim` binding state (spec/04:412), while the session row's create-time pod is persisted at §7.1 step 5; the attribution is loose rather than false. (2) The Design's "That paragraph ... closes by stating that the client never receives a `session_id`" — that sentence sits mid-paragraph at spec/07:23, followed by two `persistDeriveFailureRows` sentences; position is wrong, meaning is not. (3) SPEC-2's §6.2 note "Neither bullet enumerates that sequence" sits oddly beside SPEC-2's own edit inserting a clause into the cancel bullet's action list (spec/06:234), but the load-bearing half of the argument — that neither bullet names the step-4 `coordination_generation` bump, so neither is a complete enumeration — is true (spec/07:216). Do not spend a verifier pair on any of these.

WATCHOUT: the Design section's pointer list ("§7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph and restate nothing", spec-changes.md:43) omits the §7.2 snapshot-close edits entirely, even though "Spec files touched" lists them. It is an incompleteness in the narrative rather than a contradiction, and the §7.2 block carries its own rationale, so I judged it below the bar. A later fixer touching the Design paragraph should fold §7.2 in.

USEFUL [standing context, "The anchor sweep is done; do not repeat it." (review-log.md:64)]: correct, and re-running it mechanically cost about two minutes with a Python substring/count script over the six spec files. Worth re-running only as a count-must-equal-1 check, which is what catches a spec move.

USEFUL [standing context, "`§15.4.2` is the repo's established citation for the CH-RUNTIMEOPS `terminate` signal" (review-log.md:92)]: saved me filing it. §15.4.2 as it stands (spec/15:1686-1706) is the RPC lifecycle state machine table and states no graceful-shutdown-signal ordering, so the §4.7 row's "[Section 15.4.2] graceful-shutdown signal precedes that close" reads as a false citation until you know the tree already cites it that way (pkg/adapter/session.go:250-258 carries the same citation).

### [spec.5.review-client-surface.5]

DECISION: returned an empty findings list under the client-facing-surface lens — BECAUSE every client-facing
representation the staged spec edits touch is either already recorded as a DEFERRED (schemas/lenny-adapter.proto
under programme rule S-2, docs/reference/adapter-contract.md, docs/reference/state-machines.md:251) or has no
parallel representation at all. ALTERNATIVES: I considered filing the §4.7 "the pod's session mode" phrase and the
§4.1 half-rename (both below) and judged both below the bar / out of lens.

FACT: the round-4 fixer changed only spec-changes.md. `diff -rq scratchpad/cp-snap/0081/spec-r4
proposals/0081_.../` reports the review log as the sole difference, and spec-r5 == spec-r5-start == current. So the
"what changed since last round" diff for round 5 is EMPTY; use spec-r3 as the baseline to see the last real edits.
EVIDENCE: scratchpad/cp-snap/0081/spec-r5 (byte-identical to the proposal directory).

FACT: all fifteen "text to replace" verbatim anchors in spec-changes.md still match exactly once across
spec/04, spec/05, spec/06 and spec/07, including the three anchors round 4 minted (§7.2 preamble
"onto a replacement pod. Because ... The gateway handles", §7.2 step 2 "— the same artifact that was about to
be replayed onto the replacement pod.", and the §7.2 step 3 line). I re-ran the substring/uniqueness sweep
mechanically rather than by eye. Note the step-2 anchor is NOT at the end of step 2 in the tree: the appended
sentence lands before "The session record's `final_workspace_ref` is set to ..." (spec/07:213). That is legal
and unique, but a reader expecting an end-of-step append will be surprised. EVIDENCE: spec/07_session-lifecycle.md:210-215.

FACT: the §6.2 mid-resume anchor "the half-claimed replacement pod is released to the pool" is unique to
spec/06:234 (the `resuming → cancelled` bullet). The sibling `resuming → completed` bullet at spec/06:235 carries
only the gloss "The same abort / skip-seal / release-replacement-pod / run-terminal-handling sequence applies",
so SPEC-2's single-bullet edit does not leave an unedited twin of the same clause. EVIDENCE: spec/06_warm-pod-model.md:234-235.

FACT: three RPCs admit a start on the adapter, so the staged §4.7 "which RPC starts a session" wording is not
over-general: `StartSession` (session.go:111 → claimSessionSlot, :163 noteRuntimeStarted), `ConfigureWorkspace`
(sdkwarm.go:217 → claimSessionSlot(…,true,true), :261 noteRuntimeStarted), and `Resume` (resume.go:50, :144).
EVIDENCE: pkg/adapter/session.go:111,163; pkg/adapter/sdkwarm.go:217,261; pkg/adapter/resume.go:50,144.

FACT: the create-time-reserved-slot edge case (spec-changes.md:110-127) is TRUE against the tree.
`bindConcurrentSlot` reconnects through `BindReservedSlot(ctx, slotReq, row.PodAssignment, row.ID)` when the row
carries a non-recovery `PodAssignment`, does not retry, and never clears the binding, so a client retry of
`POST /v1/sessions/{id}/start` really does land on the same pod under the same slot identifier. Do not re-file
that bullet as contradicting §6.2's "Each retry claims a fresh pod": that sentence governs the gateway-internal
pre-attached retry loop, which this branch bypasses by construction.
EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2606; spec/06_warm-pod-model.md:287.

FACT: `ShutdownRequest.reason` is a plain `string`, not an enum, so the code lane's new `"slot_bind_failed"`
reason mints no wire value and needs no proto edit. This closes one obvious S-2 worry.
EVIDENCE: schemas/lenny-adapter.proto:1609-1612.

FACT: the CH-RUNTIMEOPS `terminate` frame carries `type`, `deadlineMs`, `reason` and no session identifier, and
its socket `@lenny-runtime-ops` is pod-wide, so the staged §4.7 clause "the signal is pod-global and names no
session" is accurate. EVIDENCE: spec/28_communication-channels.md:1082, :1051-1053.

FACT: the credential path and the direct-mode timer SPEC-3 adds to §5.2's action list are both stated the same
way in the sections that own them, so the widened list introduces no new claim.
EVIDENCE: spec/04_system-components.md:914 (`/run/lenny/slots/{sessionId}/credentials.json`, mode 0440),
spec/04_system-components.md:1169 ("In direct delivery mode, the adapter MUST set a local timer … In proxy
delivery mode … no adapter-side timer is needed").

UNVERIFIED: the staged §4.7 row says "Which RPC in this table starts a session depends on **the pod's session
mode** and on whether the session is new or resumed." In this specification "session mode" is bound to
`executionMode: session` (spec/05:395, :521; spec/06:69 "SDK-warm mode (`preConnect: true`) is admitted only in
session mode with `maxConcurrentSessions: 1`"), and both `StartSession` and `ConfigureWorkspace` occur inside
session mode. The axis that actually selects the RPC is pod-warm versus SDK-warm, which the same §4.7 table
already names two rows above ("Start the agent runtime with final `cwd` (pod-warm mode)" / "Point a
pre-connected session at the finalized `cwd` (SDK-warm mode)"). I did not file it: the charitable reading ("the
mode in which the pod runs sessions") is available and the operative content of the sentence is the clause after
the semicolon. A later round or the human may want the word swapped to "whether the pod is pod-warm or
SDK-warm". EVIDENCE: spec-changes.md:189; spec/04_system-components.md:672-673; spec/06_warm-pod-model.md:69.

UNVERIFIED: the staged §7.3 append reads "… carries the [§7.1] pod-side reclaim obligation for the session
**before the replacement pod is released**", but §7.3's numbered resume flow (spec/07:405-410) and §6.2's
`resuming` failure bullets (spec/06:230-232) state no replacement-pod release on the retry or
`awaiting_client_action` branches — only §7.2's step 3 (a terminal edge) releases it. The temporal clause
therefore presupposes a step the flow it is appended to does not state. Judged prose imprecision rather than a
defect, since the sentence's operative content is the obligation itself. Someone should decide whether §7.3
wants "before the replacement pod is released or reused".

USEFUL [standing context / Traps]: "Dead end: `releases the slot reservation afterwards` contradicts the leaked
hold" and "Dead end: the adapter cannot see this leak" each saved me from re-deriving a refuted line. The
`ReleaseSlot(leaked=true)` early-return fact is the one that closes both.

### [spec.5.review-docs-alignment.1]

FACT: the r5 snapshot is byte-identical to the current proposal, and spec-r4 differs from the
current tree only in the review log. Round 4's spec-changes deltas are visible only by diffing
against `scratchpad/cp-snap/0081/spec-r3`. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0081/spec-r5 vs proposals/0081_.../ (diff -r returns nothing)

FACT: no docs/ page mirrors the §7.2 mid-resume snapshot-close sequence or the §5.2 `**Max
retries:**` placement sentence, so SPEC-2's §7.2 and §5.2 edits have no downstream docs site at
all. A grep for "snapshot-close|half-claimed|mid-resume" over docs/ returns nothing, and
`slotRetries` appears in docs only as a config-table row. EVIDENCE: docs/reference/configuration.md:103; docs/reference/execution-modes.md:35

FACT: the per-slot sub-state machine has exactly two mirrors outside spec/06 — DOCS-1's
`docs/reference/state-machines.md:234-235` table and `pkg/sandbox/slotstate`. There is no second
spec/ enumeration of `receiving_uploads`, so SPEC-4 needs no companion spec edit. EVIDENCE: spec/15_external-api-surface.md:672 (names the state but enumerates no edges); spec/06_warm-pod-model.md:151-152

FACT: `ConfigureWorkspace` is the third RPC that sets `st.started` and calls `noteRuntimeStarted`
(SDK-warm), beside `StartSession` and `Resume`, and all three sit in the §4.7 Gateway → Adapter
table, so SPEC-1's "Which RPC in this table starts a session" clause resolves for every mode.
EVIDENCE: pkg/adapter/sdkwarm.go:217,:260-261; spec/04_system-components.md:673

DECISION: filed two findings, both under the lens's "accepted failure mode whose observable
outcome lives only in the proposal's reasoning" category, and both with a spec-side remedy so they
are in this loop's scope — BECAUSE the docs-side residue on this proposal is already a standing
DEFERRED (adapter-contract.md et al.) whose remedy is docs-only and therefore out of scope here.
ALTERNATIVES: rejected re-filing the adapter-contract.md family (docs-only remedy, five lenses have
already recorded it), and rejected the §4.1 "per-slot teardown" vocabulary drift (wording).

WATCHOUT: the proposal's Design paragraph says outright that the §15.1-start-retry residue "is
recorded among the accepted failure modes below rather than governed by a rule no layer enforces"
(spec-changes.md:63-65). That sentence is the proposal telling you the outcome lands nowhere in
spec/. Do not read the neighbouring §5.2 commentary ("a §15.1 start onto a slot reserved at
creation ... is not governed by it", spec-changes.md:366-368) as staged spec text: it is prose
about the edit, and the staged replacement at spec-changes.md:357 says none of it.

FACT: a §15.1 start retry really does land on the same pod under the same slot id — the row's
persisted `PodAssignment` routes it to `BindReservedSlot`, and `SlotID == row.ID`. EVIDENCE: pkg/gateway/sessionserver/start.go:2595-2596

FACT: the adapter refuses a retried start only on `st.started`, so an entry left by
`PrepareWorkspace`/`FinalizeWorkspace`/`AssignCredentials` admits it. EVIDENCE: pkg/adapter/slotsession.go:80-85

DEFERRED [docs/reference/adapter-contract.md]: unchanged from the standing entry at
review-log.md:135; re-verified line 75 and line 81 this round and both are still false after
SPEC-1 and SPEC-3. Nothing new to add.

USEFUL [Standing context "Dead end" bullets]: the four dead ends (adapter-cannot-see-this-leak,
withheld-report-vs-§4.7/§12, SPEC-3-insert-vs-exclusive-pool, releases-vs-leaked-hold) each saved a
full verification pass. I re-derived none of them.

### [spec.5.review-edit-sites.1]

FACT: `spec/29_communication-scenarios.md` is a spec file that mirrors the RPC/frame contracts as
numbered trace steps, and it is in NO edit list of this proposal. Two of its steps restate §4.7's
`Shutdown` contract: §29.4 item 12 (spec/29:693-701, the `Shutdown` RPC and "On the default
disposition the adapter closes the session runtime and the pod is replaced") and §29.4 item 13
(spec/29:704-711, the CH-RUNTIMEOPS `terminate` frame). Item 12 survives SPEC-1 unharmed because its
trigger is a session end of a started session. Item 13 does not: it asserts the frame unconditionally
on a session end, while SPEC-1's staged row adds "it goes out only when the deregistration leaves the
adapter holding no bound entry". EVIDENCE: spec/29_communication-scenarios.md:704-708;
proposals/0081_.../0081_....spec-changes.md:189.

FACT: `spec/29` §29.4 is NOT scoped to a single-session pod. Its own interrupt path names a
"concurrent-session pod" (spec/29:627 area), and §29.10 (spec/29:1428) is a dedicated co-tenancy
subsection, so the trace covers `maxConcurrentSessions > 1`. Do not refute the item-13 finding by
claiming §29 traces one session on an exclusive pod.

FACT: the drain-signal condition SPEC-1 lands in `spec/04` is today stated ONLY in
`docs/reference/adapter-contract.md:75` ("When the release leaves the pod holding no other bound
session, the adapter also sends the CH-RUNTIMEOPS drain signal") and in code. Nothing in `spec/`
states it before this proposal, which is why applying SPEC-1 creates a NEW spec-internal
contradiction rather than exposing an old one.

FACT (checked, no finding): the lens's metric/alert/chart/CRD half is vacuous for 0081. The staged
spec text mints no metric, alert, error code, reason value, flag, Helm value or CRD field.
`grep -rn "leaked\|SlotLeak" spec/16_observability.md` returns nothing, and `slotRetries` has no
spec mirror outside spec/05:416 and :555. Do not re-run these sweeps.

FACT (checked, no finding): the new per-slot edge `receiving_uploads → slot_cleanup` has exactly the
two mirrors the standing context already names. `grep -rn receiving_uploads spec/ docs/ schemas/`
finds only spec/06:151-152, spec/15:672 (which lists it as a Postgres-only fine SESSION state, a
different machine) and docs/reference/state-machines.md:234-235. No third spec surface.

WATCHOUT: SPEC-3 widens §5.2's slot-release action list from three actions to five, and
`spec/06_warm-pod-model.md:155`'s fence annotation `slot_cleanup ──→ released (slot workspace
removed, processes killed, slot released)` is today a word-for-word mirror of the pre-edit
three-action list. After SPEC-3 the two stop agreeing. I did NOT file it: the annotation is a gloss
of a transition, everything it names still happens, so it becomes incomplete rather than incorrect,
and the material skeptic has already refuted the structurally identical §6.4 tree-enumeration
finding on exactly that ground. A later round that wants it should argue the mirror, not the
omission. EVIDENCE: spec/06_warm-pod-model.md:155; spec/05_runtime-registry-and-pool-model.md:545.

FACT: `ConfigureWorkspace` (SDK-warm), `StartSession` and `Resume` all call `claimSessionSlot`, so
all three set `started`. SPEC-1's "Which RPC in this table starts a session depends on the pod's
session mode and on whether the session is new or resumed" is satisfiable for every mode; there is
no mode in which no RPC admits a start. EVIDENCE: pkg/adapter/sdkwarm.go:217; pkg/adapter/session.go:111;
pkg/adapter/resume.go:50.

FACT: the shipped §5.2 slot retry policy really does govern a failed BIND, not only an
adapter-reported failure of a running slot: `applySlotRetryPolicy` loops on `binder.BindSlot`
errors, releases the reservation and retries. So SPEC-2's placement constraint landing in §5.2's
`**Max retries:**` bullet reaches the right mechanism, and §6.2's pre-attached policy ("Each retry
claims a fresh pod", spec/06:286) already satisfies the same constraint on its own path. EVIDENCE:
pkg/gateway/sessionserver/start.go:2809-2833; pkg/gateway/sessionserver/start.go:2594-2609.

FACT: the create-time-reserved path really does bypass the retry policy — `bindConcurrentSlot`
returns `classifySlotBindFailure(err, slotReq)` directly when `row.PodAssignment != ""`, with no
retry loop. The proposal's edge-case bullet about a §15.1 start onto a create-time-reserved slot is
accurate about the code. EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2605.

FACT: all six markdown anchors the staged text mints still resolve, and every "text to replace"
block still matches the tree byte for byte at commit f2a397b53 (spec/04:157, :686, :853-854;
spec/05:545, :453, :555; spec/06:148-155, :234; spec/07:23, :210, :214, :412). The r4 fixes did not
disturb any anchor. Re-verified independently this round; the standing context's anchor-sweep entry
still holds.

DEFERRED [docs/reference/adapter-contract.md]: nothing new. The five-lens DEFERRED already recorded
there covers the same row; this round only confirms that its drain-signal sentence is the one place
in the tree that already states SPEC-1's condition, which is a reason to keep the docs row rather
than a new correction.

### [spec.5.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action-feasibility lens — BECAUSE every action the
staged spec assigns is performed by a component that already performs it in the tree, and no staged edit
touches a §4.6.3-owned status subresource, agent-pod RBAC, §13.2 egress, webhook purity, or a spec/18 phase
boundary. ALTERNATIVES: three candidates were worked and dropped as close variants of already-refuted
findings or as wording precision (listed below).

FACT: the round-5 snapshot is byte-identical to the live proposal, and `spec-changes.md` has not changed
since `spec-r4-start`. `diff -rq scratchpad/cp-snap/0081/spec-r5 proposals/0081_.../` is empty and
`diff -q scratchpad/cp-snap/0081/spec-r4-start/...spec-changes.md <live>` is empty too. Round 4's fix
therefore landed BEFORE the r4 "start" snapshot was taken. Use `spec-r3` as the diff base to see what round
4 actually changed (the §7.2 preamble deletion + step-2 sentence + step-3 replacement, "ends when that
attempt succeeds", the SPEC-3 `was given → reports its outcome` sentence, and the create-time-reserved
edge-case rewrite).

FACT: every actor claim in the staged text checks out against the tree.
- The adapter arms and cancels the §4.9 direct-mode lease-expiry timers itself, so SPEC-3's added
  cancellation action is the adapter's to perform. EVIDENCE: spec/04_system-components.md:1169 ("the adapter
  MUST set a local timer for each credential lease's `expiresAt`"); pkg/adapter/slotsession.go:174-188
  (`deregisterSlotLocked` cancels every armed timer on removal).
- The credential directory path SPEC-3 names is exact: `/run/lenny/slots/{sessionId}/` plus
  `credentials.json`. EVIDENCE: pkg/adapter/slotlayout/slotlayout.go:100-103,:58.
- The §4.7 row's "Which RPC in this table starts a session depends on the pod's session mode and on whether
  the session is new or resumed" is true of the tree: `claimSessionSlot` is called from all three of
  `StartSession`, `Resume` and the SDK-warm `ConfigureWorkspace`. EVIDENCE: pkg/adapter/session.go:111;
  pkg/adapter/resume.go:50; pkg/adapter/sdkwarm.go:217; slotsession.go:13-15 says so in its doc comment.
- The §4.7 row's `!boundRemains` condition on the §15.4.2 signal matches the shipped gate. EVIDENCE:
  pkg/adapter/session.go:238-241,:259.
- The create-time-reserved edge-case bullet's "the adapter refuses the retried start only when the first
  attempt had already admitted a start on that slot" is exactly `claimSessionSlotUnderLock`'s `if st.started`
  arm. EVIDENCE: pkg/adapter/slotsession.go:80-86.

FACT: every verbatim anchor in SPEC-1..SPEC-4 still matches the tree byte for byte after round 4's new
§7.2 anchors were added, and the two new ones are unique. EVIDENCE: spec/07_session-lifecycle.md:210 (the
preamble sentence and the step-2 "— the same artifact that was about to be replayed onto the replacement
pod."), :213 (step 3); spec/06_warm-pod-model.md:234 (`grep -c` returns 1 for the cancel bullet's clause).
All markdown anchors the staged text mints resolve: spec/04:149 `#### Request Message Scope`, :657 §4.7,
:848 §4.7.9, :1099 §4.9; spec/05:365 §5.2; spec/06:78 §6.2; spec/07:3 §7.1, :378 §7.3; spec/15:614 §15.1,
:1686 §15.4.2. The standing-context "anchor sweep is done" entry still holds; I re-ran it only for the two
anchors round 4 added.

WATCHOUT: `applySlotRetryPolicy` and `ReleaseSlotReservation` are reachable ONLY on the
`match.MaxConcurrentSessions > 1` branches, so the standing-context worry that
`SlotClaimer.ReleaseSlot(leaked=true)`'s early return would suppress the exclusive pod's claim DELETE and
falsify §7.1's "the pod retires" sentence does NOT arise: the exclusive path goes through `failPhase`
instead. EVIDENCE: pkg/gateway/sessionserver/start.go:2139,:2351,:2465 (the three `MaxConcurrentSessions > 1`
gates); podclaim/slotclaimer.go:830-837 (the leaked early return). I chased this for a while before the
gates settled it; do not re-derive.

MISTAKE (nearly filed, three times) — all three dropped as close variants of entries already on the refuted
list, recorded so the next agent does not spend the same hour:
1. "§7.2 step 3 releases the replacement pod back to the pool while §7.1 says an exclusive pod retires, so
   an unacknowledged reclaim's residue rides a pod back into inventory." Close variant of the refuted
   "§7.1's exclusive-pod disposition is unreachable on the §7.3 re-attach". The refuter's ground (§6.2:234
   already releases the half-claimed pod to the pool, and the residue is a pre-existing spec-vs-code
   conformance gap bounded by §4.6.1 orphan-claim collection) covers this framing too.
2. "§7.2 step 3 pins the reclaim to 'the connection that attempt still holds', which the replica running the
   close sequence may not hold, because §7.2 step 4 itself contemplates 'fencing any stale coordinator still
   attempting resume'." EVIDENCE for the premise: spec/07_session-lifecycle.md:214;
   spec/10_gateway-internals.md:68 ("Cancel all in-flight RPCs for that session. Do not retry — the session
   now belongs to a different coordinator"). Dropped because §7.2's whole close sequence is already
   coordinator-local by construction (step 1 cancels in-flight restoration RPCs, which only the coordinator
   can do), so step 3 inherits an assumption the shipped section already makes. It is a real looseness, but
   pre-existing and not one this proposal introduces.
3. "§7.1's 'releases the slot reservation afterwards' names an object an exclusive pod does not have."
   Wording; the next sentence gives the exclusive case its own disposition (pod-claim release).

UNVERIFIED: whether `BindReservedSlot`'s shipped release-on-failure (podsession/slotbinder.go:210-222,
which calls `ReleaseSlotReservation` and then leaves `PodAssignment` set) leaves a client retry of
`POST /v1/sessions/{id}/start` binding a reservation that no longer exists in the Redis counter. It is
pre-existing and orthogonal to this proposal's edits, but the create-time-reserved edge-case bullet asserts
the retried start "reconnects to the same pod under the same slot identifier", which reads as though the
reservation survives. Whoever owns the code lane should confirm the counter is re-incremented on that path.

FACT (negative sweep, so nobody repeats it): the §4.1 and §4.7 `Shutdown` preconditions have exactly two
statements in `spec/`, both staged. `grep -rn "per-session teardown\|per-slot teardown\|releases the
session's slot\|closes that session's runtime" spec/ docs/ schemas/` returns only
spec/04_system-components.md:157 and :686. `spec/28_communication-channels.md` mentions `Shutdown` nowhere,
so no §28.3 register row describes the teardown contract. The only other `sessions_served` statement is
spec/12_storage-architecture.md:494 ("gateway-written at each session release"), which SPEC-3 leaves true
because the pre-`running` reclaim is framed as not a session release.

### [spec.5.review-fresh.1]

DECISION: returned an empty findings list for the fresh-holistic lens on round 5 — BECAUSE the
spec staging is byte-identical to the round-4 snapshot (round 4's single finding was refuted, so no
fixer ran), every anchor still resolves uniquely, and the four candidates I developed independently
each collapsed on evidence in the tree — ALTERNATIVES: the four are recorded below so nobody
rebuilds them.

FACT: `diff -rq scratchpad/cp-snap/0081/spec-r5 proposals/0081_.../` is EMPTY, and r4 vs r5 differs
only in the review log. spec-changes.md has not changed since round 3's fix. To see the last real
spec-staging change, diff against `spec-r3`. The "read the changed sections hardest" instruction is
inert for r5 as it was for r4. EVIDENCE: scratchpad/cp-snap/0081/spec-r4 vs spec-r5.

FACT: mechanical anchor sweep re-run and clean at commit-time state. A script extracted all 27
fenced blocks from spec-changes.md and counted occurrences across spec/*.md: every "text to replace"
block occurs exactly ONCE repo-wide in spec/, every "replace it with" block occurs zero times. The
three §7.2 anchors are spec/07_session-lifecycle.md:210 (preamble), :213 (step 2 tail), :214 (step
3); §7.1's parenthetical is :23 and the insertion point is between :23 and :24; §4.1 is
spec/04:157, the §4.7 `Shutdown` row is spec/04:686, §4.7.9 step 5 is spec/04:853; §5.2's three are
spec/05:453, :545, :557; §6.2's are spec/06:152-153 (edge insert point), :156/:158 (paragraph
insert point) and :234 (resuming-cancel clause). Do not re-run unless spec/ moves.

MISTAKE (nearly filed, do not re-file): "the create-time-reserved edge-case bullet states the
`leaked` disposition without the concurrency scope §7.1 and its two sibling bullets carry". It is
wrong because a create-time-reserved SLOT exists only on a concurrent pool: `bindConcurrentSlot` is
the sole caller of `BindReservedSlot`, and the exclusive create-time binding goes through
`Binder.Launch`'s reconnect instead. The bullet's unscoped `leaked` is therefore correct on its own
domain. EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2605; problem-statement.md:252-254.

WATCHOUT: the tempting "§5.2's slot retry policy is triggered by an adapter-emitted slot-failure
event for a RUNNING session, so SPEC-2's placement constraint never reaches a failed BIND, which
§6.2's pre-attached retry policy owns (and which already guarantees a fresh pod per retry)" is a
real spec-level tension between §5.2:553-557 and §6.2:283-284, but it is PRE-EXISTING and the code
settles it the other way (`applySlotRetryPolicy` branches on `*podsession.SlotBindError`). Round 4's
mechanism lens recorded the same. Filing it costs two verifiers and closes nothing.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:544,553,557; spec/06_warm-pod-model.md:283-286;
pkg/gateway/sessionserver/start.go:2807-2884.

WATCHOUT: "the §7.3 appended sentence presupposes a replacement-pod release that §7.3's own numbered
flow (steps 1-4, spec/07:405-414) never states". True as stated, but §6.2:234 and staged §7.2 step 3
both state the release, and the appended sentence is a pointer, so it reads as a cross-section
presupposition rather than a contradiction. Judged below the bar.

FACT: SPEC-3's two shipped-behaviour claims both check out against the tree. `slotlayout.RemoveTree`
removes the slot root, `/sessions/{id}`, `/artifacts/{id}` and `CredentialsDir`
(pkg/adapter/slotlayout/tree.go:58-68); `deregisterSlotLocked` cancels every armed §4.9 timer before
deleting the entry (pkg/adapter/slotsession.go:174-181). §5.2:455's recycle-lifecycle sentence does
already assume the credential lease is gone before `cleanupCommands`. So the widened action list
adds no new obligation, as the proposal claims.

FACT: the §4.7 `Shutdown` row's staged ordering (final usage flush → §15.4.2 signal → runtime close)
matches the handler exactly, and the drain signal's inhibitor really is the BINDING
(`boundRemains` reads `other.sessionID != ""`) while the teardown's enabler becomes `started`, so
the edge-case bullet "the signal stays gated on the binding rather than on `started`" is accurate.
The spec still states no ordering between the slot release and the runtime teardown, and the code
order IS load-bearing (`removeSlotTree` runs after `Runtime.Close` so the agent is not reading a
credential file inside the §15.4.2 grace window). Round 4 rejected filing that silence as
pre-existing; I agree, but a code-lane reviewer should keep the order.
EVIDENCE: pkg/adapter/session.go:238-241,:243-259,:262-271,:183-188.

USEFUL [Standing context / Traps, whole section]: it removed at least five candidate findings before
I spent evidence on them (leaked-holds-occupancy, the withheld report vs §4.7's `ReportSessionScrub`
row, `releases the slot reservation` vs the leaked hold, the `slot_cleanup ──→ leaked` concurrency
asymmetry, the §7.2 preamble's surviving seal-skip conclusion). Read it before drafting anything.

USEFUL [spec.4.review-applicability.1, the "On the default disposition the pod is replaced." CORRECTS]:
confirmed independently — `grep -rn` over spec/ returns that string only at spec/04:686;
spec/29:696 carries a differently-worded restatement, so there is no second edit site there.

### [spec.5.review-kubernetes.5]

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE every K8s-API surface the staged edits touch was re-derived and is clean: no component writes another's status subresource, no finalizer is introduced, no CRD is used as a message bus or a per-request database, the admission webhook keeps a real object to admit, and no synchronous request path is made to block on a controller reconcile or leader election. ALTERNATIVES: I worked up and dropped four candidates, each recorded below so a later round does not re-derive them.

FACT: round 4's fixers changed nothing in the spec staging. `diff -rq scratchpad/cp-snap/0081/spec-r5-start scratchpad/cp-snap/0081/spec-r5` is empty and `diff -rq spec-r5 proposals/0081_.../` is empty; the only file that differs between r4 and r5 snapshots is the review log. The last substantive spec-changes edits are the r3→r4 hunks (the §7.2 preamble-deletion/step-2/step-3 restructure, the "ends when that attempt succeeds" rewrite, the create-time-reserved edge-case rewrite, and SPEC-3's "reports its outcome" sentence). EVIDENCE: scratchpad/cp-snap/0081/spec-r4 vs spec-r5.

FACT: the orphan-claim GC backstop for a leaked-but-unretired pod drains rather than returns to idle, so the `leaked` disposition leaves no wedged or undeletable object and no unscrubbed pod re-entering inventory. Predicate 1 selects a `bound`/`recycling` claim older than `claimOrphanTimeout` whose pod no active session references and "is reclaimed by draining the pod, regardless of the pool's recycle settings", explicitly fail-closed for the scrub-before-idle invariant. This is what closes the "a failed bind writes no active session row, so the claim is orphaned" worry for good. EVIDENCE: spec/04_system-components.md:517.

FACT: all three RPCs the staged §4.7 row's "Which RPC in this table starts a session" clause ranges over are in the §4.7 Gateway→Adapter table and all three set the adapter's `started` flag through `claimSessionSlot`, so the reworded precondition is implementable on every start path. EVIDENCE: spec/04_system-components.md:672 (`StartSession`), :673 (`ConfigureWorkspace`), :684 (`Resume`); pkg/adapter/sdkwarm.go:217; pkg/adapter/resume.go:50; pkg/adapter/slotsession.go:88.

FACT: the create-time-reserved slot path is concurrency-gated, so the edge-case bullet that states the `leaked` disposition unqualified for a §15.1 start onto a create-time-reserved slot is NOT the unscoped-`leaked` defect an earlier round fixed twice. Both dispatch sites route to `bindConcurrentSlot`/`BindReservedSlot` only under `match.MaxConcurrentSessions > 1`, and both `ClaimSlot` and `BindReservedSlot` reach the compensation through `materializeSlot`. EVIDENCE: pkg/gateway/sessionserver/start.go:2351,:2465; pkg/gateway/podlifecycle/podsession/slotbinder.go:133,:254,:265.

WATCHOUT: do not file "§7.2 step 3 asserts the replacement pod is released while §7.1 holds its occupancy on an unacknowledged reclaim". It is the same general-act-plus-stated-exception structure as the already-refuted "SPEC-2 both releases the slot reservation and holds the slot's occupancy" finding, and the proposal's own commentary on step 3 says the step states no outcome for the pod. EVIDENCE: spec-changes.md:282,:285-294.

WATCHOUT: do not file the cross-replica actor problem in §7.2 ("the gateway sends on the connection that attempt still holds", while step 4 fences "any stale coordinator still attempting resume"). The same actor assumption is already in the untouched step 1 ("Cancel the in-flight restoration RPCs"), so the staged step 3 inherits it rather than introducing it, and the reclaim is in fact issued by the failing attempt itself (`Binder.Resume`'s failure branch), not by the terminal handler. EVIDENCE: spec/07_session-lifecycle.md:211,:214; non-spec-changes.md:396-405.

WATCHOUT: do not file "§5.2's slot retry policy is triggered by a runtime slot failure, so the staged `**Max retries:**` placement constraint reaches no bind-time case". §5.2's own non-retryable list includes `workspace_validation`, which is a bind-time (FinalizeWorkspace) failure, so the policy demonstrably spans bind failures. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:544,:553,:557.

USEFUL [Standing context / Settled — "Ownership is clean"]: it named exactly the three things this lens has to check (gateway owns `SandboxClaim.spec`/`.status`, the unhealthy drain goes through the `lenny.dev/drain-request` annotation rather than a `Sandbox.status` write, no SSA force-ownership or finalizer). I re-verified the load-bearing half — spec/04_system-components.md:409 "the gateway does not write `Sandbox.status`" and the WPC-owned level-triggered occupancy projection — and it holds. That entry saved the whole ownership-table sweep.

USEFUL [Standing context / Traps — the four "Dead end" bullets]: three of my four working candidates were already refuted there before I priced them.

### [spec.5.review-mechanism.1]

FACT: the proposal directory is byte-identical to the round-4 snapshot except for the review
log, so `diff -ru scratchpad/cp-snap/0081/spec-r4 proposals/0081_.../` reports only
`review-log.md`. Round 5 had no fix-stage text to read hardest; the whole document is
round-≤4 text. EVIDENCE: scratchpad/cp-snap/0081/spec-r4 vs the proposal directory.

FACT: `ClaimSlot`'s pass 1 places a new slot on any **claimed**, same-tenant, non-uptime-expired
pod with free Redis capacity. A pod holding a `leaked` slot is exactly such a pod, so "the
leaked slot's held occupancy keeps the reclaiming pod outside idle inventory" (the retired
design argument for why a §7.3 re-attach cannot re-pick it) is false: held occupancy makes the
pod *claimed*, which is pass 1's own candidate set, not pass 2's. EVIDENCE:
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:410-467.

FACT: on a concurrent pool a §7.3 resume-rebuild reaches `applySlotRetryPolicy` through
`bindConcurrentSlot`'s second branch (stale/absent PodAssignment → `bindSlotWithRetry`), while
a create-time-reserved row takes `BindReservedSlot` with no retry loop. So the three attempt
kinds §7.1 binds do NOT map onto three code paths: two of them share
`applySlotRetryPolicy`, but each *request* builds a fresh `SlotBindRequest`, so `ExcludePod`
never crosses a request boundary. EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2610;
proposal non-spec-changes.md:422-423 ("the exclusion lives for the remaining iterations of this
request and reaches no other request").

FACT: the staged compensation's leak discriminator is `err != nil || !cleanly`, i.e. it fires
on an answer the adapter *did* give when that answer says the release did not complete; and
`applySlotRetryPolicy` sets `ExcludePod` under `sbe.Leaked || relErr != nil`, a third arm the
spec sentence names nowhere. EVIDENCE: non-spec-changes.md:319-321, :419-421.

DECISION: filed two findings — the §5.2 placement predicate being narrower than both the leak
disposition and the staged code, and the §7.3 re-attach falling outside both the constraint and
the accepted-failure-mode record. BECAUSE both are gaps a reader can check against staged text
and both have their remedy inside spec-changes.md. ALTERNATIVES: I also derived that SPEC-4's
prose cites the untouched `receiving_uploads ──→ running` trigger (spec/06:152-153, "session
dispatched to runtime with its session identifier") as naming the moment the runtime *has been
given* the session, while the same paragraph places a dispatched-but-unacknowledged start in
`receiving_uploads`. I did NOT file it: the orchestrator's already-fixed list carries that exact
contradiction ("...and the untouched `receiving_uploads → running` trigger"), so filing it is
re-litigation. See the WATCHOUT below.

WATCHOUT: the `running`-boundary tension with spec/06:152-153 is NOT closed, it was only
made one-sided. SPEC-3's two staged sentences are mutually consistent only under the
"acknowledged" reading of `running`; the fence's own words supply the "dispatched" reading, and
the standing-context trap forbids editing :152. A future round that wants to close it must
either edit the fence annotation (and docs/reference/state-machines.md:235 with it) or delete
SPEC-4's "which is the moment the `receiving_uploads → running` trigger above names" clause.
EVIDENCE: spec/06_warm-pod-model.md:152-153; spec-changes.md:427, :462.

UNVERIFIED: whether `Binder.Resume`'s staged compensation feeds `accountSlotFailure`/the
slothealth ledger at all. If it does not, a §7.3 re-attach's unacknowledged reclaim leaves a
leaked slot that never reaches the `ceil(maxConcurrentSessions/2)` trigger, which would widen
finding B's exposure below `maxConcurrentSessions: 3`. Whoever owns CODE-4 should check.

USEFUL [Settled: "`SlotID == SessionID` on every path"]: this is what makes both filed findings
concrete rather than theoretical — a retry back onto the reclaiming pod is the *same* adapter
registry key and the *same* on-disk tree, not a fresh slot.

### [spec.5.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens, for the second
consecutive time this lens has run — BECAUSE round 4's edits (the §7.2 preamble deletion, the step-2
sentence, the step-3 replacement, §7.1's "ends when that attempt succeeds", the create-time-reserved
edge-case rewrite, and SPEC-3's new "reports its outcome" sentence) touch no metric, no alert, no CRD
condition and no §16 inventory row, and every observability surface the staged text names still says
what the proposal says it says — ALTERNATIVES: I worked up and dropped four candidates, listed below,
each of which fails the materiality bar or duplicates a refuted entry.

FACT: round 4's diff is the ONLY content change since round 3. `diff -rq scratchpad/cp-snap/0081/spec-r4
proposals/0081_.../` reports only the review-log file, and `spec-r5` is byte-identical to the current
proposal, so the "read the changed sections first" instruction points at the r3→r4 spec-changes diff,
not at an r5 diff. Use `diff -U 15 scratchpad/cp-snap/0081/spec-r3/...spec-changes.md
scratchpad/cp-snap/0081/spec-r4/...spec-changes.md` to see what a round-5 reviewer must scrutinise.

FACT: the two new SPEC-2 §7.2 anchors round 4 minted both match the tree byte for byte and both are
unique. The preamble deletion anchor is spec/07_session-lifecycle.md:210 ("onto a replacement pod.
Because the replacement pod has not yet reached `attached` — ... The gateway handles"), and the step-2
append anchor is the single occurrence of "the same artifact that was about to be replayed onto the
replacement pod" at spec/07_session-lifecycle.md:213. The step-2 anchor is NOT the last sentence of
step 2 (the `final_workspace_ref` sentence follows it), so the appended sentence lands mid-step; the
staged "Giving:" block makes that unambiguous. EVIDENCE: spec/07_session-lifecycle.md:210,:213

FACT: deleting the §7.2 preamble premise does not falsify the "Pre-attach terminal collapse" paragraph
that contrasts with it. That paragraph's contrast is "Unlike the `resuming → cancelled` / `resuming →
completed` edges, there are no in-flight restoration RPCs to abort and no half-claimed replacement pod
to release", which is about pod acquisition rather than about a started runtime, so it survives the
deletion untouched. EVIDENCE: spec/07_session-lifecycle.md:219-220

FACT (dropped candidate 1): SPEC-3's new sentence ("A cleanup that reclaims a slot the pod's shared
runtime process was given is a session release like any other and reports its outcome") plus §7.1's
widened "ends when that attempt succeeds" does make a bind that fails AFTER `StartSession` succeeded
file a `ReportSessionScrub`, advancing `sessions_served` on that pod; §5.2's `**Max retries:**` bullet
then places the retry on a NEW SLOT ON THE SAME POD whenever the reclaim WAS acknowledged, so one
logical session can advance one pod's served count twice — textually the "double-count a session a §5.2
retry re-binds" harm the Design paragraph names. Dropped: the identical double-count already exists in
the shipped spec for any slot that fails after running and is retried on the same pod, and the counting
is consistent with the proposal's stated principle that the count records sessions the pod ran. It is a
deliberate position rather than an inconsistency. EVIDENCE: spec-changes.md:78-84,:400;
spec/05_runtime-registry-and-pool-model.md:554

FACT (dropped candidate 2): the create-time-reserved edge-case bullet states the `leaked` disposition
and the §5.2 replacement trigger with NO concurrency qualifier, while the staged §7.1 paragraph says
the `leaked` sub-state and that trigger "are stated for concurrent occupancy and do not apply" on a
one-session pod. Dropped: the bullet is proposal rationale rather than staged spec text, the same
unqualified wording predates round 4, and on an exclusive pod `failPhase` retires the pod so the
bullet's own premise (the retry reconnects to the same pod) does not arise there. A scoping-precision
finding of exactly this shape has already been refuted twice on this proposal. EVIDENCE:
spec-changes.md:112-131 versus spec-changes.md:216

FACT (dropped candidate 3): §5.2's whole-pod replacement trigger glosses `leaked` as "cleanup timeout
exceeded" (spec/05:562) and the §6.2 fence glosses it the same way (spec/06:148), neither of which
describes a reclaim the adapter never answered. Dropped for the same reason `spec.3.review-operational.1`
dropped it: §5.2's `**Slot cleanup:**` bullet already says "If cleanup fails, the slot is leaked", which
is broader than either gloss, so the imprecision is pre-existing.

FACT (dropped candidate 4): §11.4's full-revoke step 3 says "The pod's runtime adapter initiates
graceful shutdown (SIGTERM to agent, wait up to 10s, then SIGKILL)" for every session it `Shutdown`s,
which after SPEC-1's split is false for a revoked session whose start the adapter never admitted.
Dropped: the sentence is already false for any per-slot `Shutdown` on a co-tenanted pod (one session's
release does not SIGTERM the shared agent), so it is pre-existing looseness this proposal widens rather
than creates, and the same family of "§4.7/§11/§12 say the report/teardown happens at every release"
findings is on the refuted list. EVIDENCE: spec/11_policy-and-controls.md:264

FACT: `spec/16_observability.md` still needs no edit after round 4. Re-verified against the changed
text: the staged spec blocks name no metric, no alert and no condition; the four §16 rows that touch
this surface (`lenny_gateway_pod_retirement_total` :12, `lenny_slot_failure_total` :14,
`lenny_slot_pod_replacement_total` :15, `lenny_pod_session_reuse_count` :128) are all still true, and
`lenny_slot_pod_replacement_total`'s `maxConcurrentSessions > 1` scoping is exactly the scoping §7.1's
exclusive-pod carve-out preserves. There is no alert on any slot metric, so nothing can be orphaned.

USEFUL [Standing context, "Dead end" bullets and the refuted list]: they cut four more candidates of
mine before I spent verifier pairs on them (the adapter-cannot-see-the-leak route, the withheld report
versus §4.7/§12, `releases the slot reservation` versus the leaked hold, and the
`concurrent_slots_exhausted` gloss). Keep them.

USEFUL [spec.3.review-operational.1]: its enumeration of which §16 rows touch this surface, and its
finding that `ReportSessionScrub` has no missing-report watchdog (unlike `ReportPodScrub`, spec/04:693),
meant I only had to re-check the rows against round 4's new text rather than re-derive the inventory.

OPEN: this lens has now returned empty twice (rounds 3 and 5) against two different versions of the
staged text. If it is scheduled again, the cheapest useful thing it can do is re-check only the §16
inventory rows named above and the `ReportSessionScrub` / `sessions_served` chain; everything else on
this surface has been swept three times.

### [spec.5.review-performance.2]

DECISION: returned an empty findings list for the performance / scalability / failure-mode lens on the round-5 staging — BECAUSE the write-rate half of the lens is genuinely empty and every failure-mode candidate I could substantiate is already refuted, parked as pre-existing, or a close variant of a refuted finding — ALTERNATIVES: (1) the §7.2 unbounded-reclaim-before-terminal finding and (2) a replica-local-leak-ledger finding, both dropped for reasons recorded below.

FACT: the write-rate math for this proposal is empty and a future round need not redo it. The change adds ZERO control-plane writes, zero new informer watches, zero new Redis keys, and zero new Postgres queries. Per failed bind it adds exactly one data-plane gRPC (`Shutdown`) on an already-open connection. Redis writes go DOWN, not up: `SlotClaimer.ReleaseSlot(leaked=true)` early-returns before the decrement and the claim DELETE, so an unacknowledged reclaim performs fewer writes than today's hard-coded `ReleaseSlot(ctx, name, false, false)`. The `ExcludePod` placement filter is one `continue` in each of `ClaimSlot`'s two existing candidate loops and reads no new store. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-843,:882-885; pkg/gateway/podlifecycle/podsession/slotbinder.go:493-503; non-spec-changes.md:316-322.

FACT: `materializeSlot` is shared by `BindSlot` (fresh slot) AND `BindReservedSlot` (create-time-reserved slot), and it re-runs the FULL sequence stageWorkspace → FinalizeWorkspace → RunSetup → AssignCredentials → StartSession. So the §15.1-start-retry edge-case bullet's "the retried start materializes it afresh" is TRUE for the concurrent path, and the reclaim's tree deletion does not orphan the client's uploads. I chased this as a data-loss regression (upload token is single-use, `410 UPLOAD_TOKEN_CONSUMED`, §7.1 uploadToken bullet) and refuted myself. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:265-326.

FACT: `materializeSlot` returns success immediately after `cl.StartSession`, with no gateway-side stage after it inside the bind. So the round-4 widening of the §7.1 upper bound ("ends when that attempt succeeds") does NOT open a reachable path where a bind-failure reclaim finds the slot in `running` on the creation path. SPEC-3's new sentence ("A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome") is reachable only on the resume path and on the ordinary session end. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:313-330.

WATCHOUT: the round-5 snapshot is IDENTICAL to the proposal (`diff -ru scratchpad/cp-snap/0081/spec-r5 proposals/0081_...` is empty), and spec-changes.md has not changed since 03:46, i.e. since round 4's fixes. Diff against `spec-r3` (or `spec-r4`) instead to see what round 4 actually changed. I lost time on the empty r5 diff. EVIDENCE: scratchpad/cp-snap/0081/spec-r4 mtime 03:53 vs spec-changes.md mtime 03:46.

FACT: all NINE staged "text to replace" anchors are still byte-exact and unique in the tree at this commit, including the THREE new §7.2 anchors round 4 minted (the preamble sentence with its surrounding "onto a replacement pod. … The gateway handles" collar, the step-2 "— the same artifact that was about to be replayed onto the replacement pod." fragment, and step 3). I re-ran the substring/uniqueness check with python because the §7.2 anchors were new since the sweep recorded in Standing context. Do not re-run unless the spec moves. EVIDENCE: spec/07_session-lifecycle.md:210,213,214,23,414; spec/06_warm-pod-model.md:234; spec/05_runtime-registry-and-pool-model.md:421,545,555; spec/04_system-components.md:157,686,853.

FACT: the step-2 append lands MID-PARAGRAPH. In the tree, the anchored fragment is followed by " The session record's `final_workspace_ref` is set to this checkpoint's object key…", so the applied step 2 reads "…replayed onto the replacement pod. The aborted re-attach's pod-side state … is reclaimed in step 3 rather than sealed. The session record's `final_workspace_ref` is set…". That is coherent; do not "fix" it by moving the new sentence to the end of the step. EVIDENCE: spec/07_session-lifecycle.md:213.

FACT: the §7.2 preamble deletion has no mirrored site outside §7.2. `grep -rn "no runtime was started\|has not yet reached \`attached\`\|no live workspace" spec/ docs/` returns only spec/07:210, :214 (both edited by SPEC-2) and :220, whose "no pod is attached at all" sentence is the pre-attach family's OWN distinguishing property and stays true — the deletion makes that contrast sharper rather than breaking it. Not a missing edit site.

MISTAKE (nearly filed, do not re-derive): "the staged §7.2 step 3 inserts a blocking pod RPC after step 1 has stopped the only watchdog, with §7.1 explicitly stating no deadline, so a hung replacement pod makes `resuming` the deadlock sink §6.2 forbids." It does not survive the materiality bar. §7.1's own "a reclaim the adapter does not acknowledge leaves the slot `leaked`" presupposes the gateway gives up, the code lane bounds it (`compensateFailedSlotBind` wraps `context.WithoutCancel(ctx)` in `slotCleanupBudget`, floor 5s), and the proposal's rationale names §5.2's `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` as the figure. That leaves prose incompleteness, which this proposal's skeptic has refuted repeatedly. EVIDENCE: spec/07:212 (step 1 stops the 300s watchdog); spec/06:229,240 (the no-deadlock-sink obligation); non-spec-changes.md:289-322; spec/05:545 (the formula).

MISTAKE (nearly filed, do not re-derive): "the leaked bound the staged §7.1 sentence leans on is replica-local, so at multi-replica scale a pod never reaches `ceil(maxConcurrentSessions/2)` while its cluster-wide Redis occupancy stays held." The fact is true — `slothealth.Tracker.leaked` and `.events` are in-process maps and `slothealth.New()` is called once per gateway process — but it is a close variant of the already-refuted "the staged `leaked` disposition holds the slot's occupancy, suppressing the pod retirement that today destroys the residue", whose refutation explicitly granted the other retirement routes (`recycle.maxSessionsPerPod` drain, `maxPodUptimeSeconds` drain) as the bound. EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:56-67,:118-140; cmd/lenny-gateway/sessiondeps.go:261.

UNVERIFIED: §6.2:160 asserts the persistent leak count is "equivalently the leaked portion of the pod's Redis slot-counter occupancy" and that the ADAPTER exposes `leaked_slots` in its health metadata, while the implementation keeps the count in the replica-local `slothealth.Tracker` and emits the gauge from the GATEWAY (gatewaymetrics_credential.go:219-223). Both halves of that spec sentence are already wrong against the tree BEFORE this proposal; the proposal raises the leak rate without touching either. Standing context already records the gateway-emits-the-gauge half as a dead end. Somebody should decide whether the Redis-equivalence half is a separate spec finding, but it is not 0081's.

USEFUL [Standing context, Settled #47 and #48]: `ReleaseSlot(leaked=true)` early-returns (no decrement, no claim DELETE, no occupancy-zero disposition) and today's `ReleaseSlotReservation` hard-codes `false,false`. Those two facts are the whole write-rate answer for this lens and saved me from re-deriving the etcd/Redis amplification question from scratch.

USEFUL [Standing context, Traps: "Dead end: `releases the slot reservation afterwards` contradicts the leaked hold" and "Dead end: the adapter cannot see this leak"]: both are exactly where a performance/reliability lens naturally goes first. Having them pre-refuted let me skip straight to the durability and multi-replica questions.

### [spec.5.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE every reliability-lens angle I could construct against the round-4 staged text either lands in code/docs (out of this loop's scope), reproduces a refuted finding, or resolves to a pre-existing condition the standing context already records — ALTERNATIVES: filed nothing on the four candidates below, each of which I worked to a conclusion and rejected on the materiality bar.

FACT: the round-4 fix stage changed exactly six things in spec-changes.md and nothing in non-spec-changes.md (md5 identical across r3/r4/r5). The six: §7.1's obligation upper bound "has the session running on the pod" → "succeeds"; the create-time-reserved edge case expanded with the lagging-reclaim-kills-the-retried-session paragraph; §7.2's preamble premise sentence deleted outright (r3 had shortened it); a new sentence appended to §7.2 step 2; the step-3 rationale reworded; and SPEC-3 gained "A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome." Round 5's snapshot is byte-identical to the live proposal, so `diff -ru cp-snap/0081/spec-r5 proposals/0081_...` is empty — diff spec-r4 against spec-r5 (also empty) and then spec-r3 against spec-r4 to see the round-4 fix. EVIDENCE: scratchpad/cp-snap/0081/spec-r{3,4,5}.

FACT: `ensureSlotStateLocked` calls `slotlayout.EnsureTree` BEFORE it registers `s.slots[slotID]`, and `EnsureTree` creates five directories in sequence with no rollback, so a failure on the third leaves the first two on disk with no registry entry. The staged §4.7 no-op rule ("a request naming a session the adapter holds no entry for removes nothing ... and answers with a clean-exit response") then certifies that residue as clean. I did NOT file it: the residue is empty directories (EnsureTree fails before any content is written), `MkdirAll` is idempotent on a retry, and the same residue already survives today because the compensating `Shutdown` is not sent at all. Harm is inert. EVIDENCE: pkg/adapter/slot.go:105-125; pkg/adapter/slotlayout/tree.go:24-46; spec-changes.md:203.

FACT: the reclaim's deadline is NOT unbounded in the applied spec, contrary to how §7.1's "The paragraph states no deadline for the reclaim" reads in isolation. SPEC-1's §4.7 row defines the slot release as "the §5.2 slot cleanup for that slot", and SPEC-3's scrub-model append carries the `**Slot cleanup:**` bullet — including its `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` timeout — across the concurrency boundary ("That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency"). The code applies it (`slotCleanupBudget` + `context.WithTimeout(context.WithoutCancel(ctx), ...)`). I chased "§7.2 step 1 stops the 300s `resuming` watchdog and step 3 then blocks on an unbounded adapter RPC before the terminal write, making `resuming` the deadlock sink §6.2 says it cannot be" and dropped it on this. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; spec-changes.md:198,427; non-spec-changes.md:289-294,:316-319; spec/07_session-lifecycle.md:212-216; spec/06_warm-pod-model.md:238.

FACT: SPEC-3's new round-4 sentence ("A cleanup that reclaims a slot the pod's shared runtime process was given ... reports its outcome") is reachable on the ordinary failed-bind path, not only on resume: `materializeSlot`'s last stage is `cl.StartSession`, and a `StartSession` whose response is lost after the adapter already ran `noteRuntimeStarted` leaves the slot in `running`, so the reclaim reports and `IncrementSessionsServed` fires for a bind that will be retried. Because that reclaim IS acknowledged, §5.2's new placement constraint does not exclude the pod, so the retry may land on the same pod and count the same session twice on it. I did NOT file it: the pod genuinely started the runtime twice, double-counting is conservative (retires the pod sooner), and "at most one report per session release" is satisfied because there are two releases. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:311-322; pkg/adapter/session.go:156,163; spec-changes.md:427,443.

WATCHOUT: the racing-start edge case's stated reason is weaker than its conclusion, and a future reviewer will be tempted to file it. It says "Neither ordering reports a cleanup outcome, because the slot never reached `running`." In ordering 1 the reclaim lands between `Runtime.Start` returning and `noteRuntimeStarted`, so under §6.2's own shipped trigger words ("workspace ready, session dispatched to runtime with its session identifier") the slot HAS reached `running`; only under the bound `runtimeLive`/cohort-membership reading has it not. The standing context settles the vocabulary in favour of cohort membership and the Design paragraph says the report "under-approximates toward not counting", so the conclusion is right and only the stated ground is loose. It is wording, not a contradiction. EVIDENCE: spec-changes.md:141-142,:20-23; spec/06_warm-pod-model.md:152; pkg/adapter/session.go:111,156,163.

WATCHOUT: the staged §6.2 `resuming → cancelled` clause states the reclaim unconditionally ("the §7.1 pod-side reclaim for the session runs on the half-claimed replacement pod, which is then released to the pool"), while §7.1 conditions it on the attempt having issued its first pod-side RPC and §7.2's step-3 rationale says so explicitly. A cancel arriving between the replacement-pod claim and the first restoration RPC therefore reads as owing a reclaim in §6.2 and none in §7.1/§7.2. I judged it below the bar because the §6.2 bullet closes with "Full step-by-step sequence: see §7.2", which is where the scoping lives. Do not file it without a stronger consequence than the reading. EVIDENCE: spec-changes.md:311-315,:284-288; spec/06_warm-pod-model.md:234.

UNVERIFIED: §7.3's appended sentence conditions the reclaim on a release ("carries the §7.1 pod-side reclaim obligation for the session before the replacement pod is released"), but §6.2's three non-terminal `resuming` failure bullets (pod crash / gRPC error, the 300s watchdog, non-retryable errors) state no disposition at all for the half-claimed replacement pod — no release, no reclaim. The obligation still binds through §7.1 directly, since each of those is a failed bind attempt past its first pod-side RPC, so I did not file it. Whether the spec should state the replacement pod's disposition on the retry branch is a pre-existing gap somebody should price separately. EVIDENCE: spec/06_warm-pod-model.md:231-233; spec-changes.md:305; spec-changes.md:224.

CORRECTS [spec.1.review-reliability.1, "Redis rehydration loses leaked occupancy"]: still true and still OPEN, but the remedy is narrower than that entry implies. §6.2 already names the durable substrate it wants — "the persistent count is the `lenny_adapter_leaked_slots` gauge, equivalently the leaked portion of the pod's Redis slot-counter occupancy" — and §5.2's "Post-recovery rehydration atomicity" rebuilds the counter from `SessionStore.GetActiveSlotsByPod`, which selects `state = 'active'` rows. A failed bind never has one, so the two sentences are already inconsistent for the shipped leaked class, independent of this proposal. Widening §5.2's rehydration paragraph is the fix; widening §7.1 is not. I did not re-file, because the inconsistency predates 0081 and this loop has refuted three findings on that ground. EVIDENCE: spec/06_warm-pod-model.md:160; spec/05_runtime-registry-and-pool-model.md:551.

USEFUL [standing context, Traps]: the "Dead end" entries saved me from filing three findings I had independently reconstructed — the adapter-cannot-see-this-leak gauge argument, the `releases the slot reservation afterwards` versus the leaked hold, and the §5.2-bullet-is-concurrency-scoped reading of SPEC-3's insert. Each cost me ten to twenty minutes to reach and would have cost two verifiers apiece. Keep writing them.

### [spec.5.review-security.1]

DECISION: returned an empty findings list for the security lens on round 5 — BECAUSE every
established control the staged edits touch is either unchanged, tightened, or already on the
refuted/barred list, and the two candidates I developed furthest are (a) explicitly barred by a
standing WATCHOUT and (b) hardening on shipped behaviour with a near-unreachable trigger.
ALTERNATIVES: see the two entries below.

FACT: the round-5 snapshot is byte-identical to the current proposal (`diff -ru
scratchpad/cp-snap/0081/spec-r5 proposals/0081_...` is empty), and `spec-r4` differs from `spec-r5`
only in the review log. Nothing in the staged spec text changed between rounds 4 and 5, so the
"read the changed sections hardest" instruction has no target this round. Diff against `spec-r3`
to see the last substantive change.

FACT: the drain-signal gate really does narrow, and it narrows in the safe direction. Shipped
`Shutdown` sends the §15.4.2 signal under `bound && !boundRemains` (pkg/adapter/session.go:241,259);
CODE-1 moves it inside the `started` block, so the subject-side gate becomes `started &&
!boundRemains` (non-spec-changes.md:108-112). The staged §4.7 row states exactly that by placing the
signal inside the runtime teardown. This FIXES the hazard CODE-1's doc-comment work names (a
bound-but-unstarted teardown signalling the shared runtime to terminate), so it is not a control
regression. The Design edge case "the signal stays gated on the binding rather than on `started`"
is about the `boundRemains` half only; do not read it as contradicting CODE-1.
EVIDENCE: pkg/adapter/session.go:238-261; non-spec-changes.md:108-112,:183-186; spec-changes.md:189.

FACT: the racing-start residue fails CLOSED at the recycle boundary, which is better than the
proposal's edge-case bullet claims. When the reclaim's answer precedes the start's admission the
adapter re-creates `/workspace/slots/{sessionId}`; §5.2 scrub step 6 stat-checks `/workspace/slots/`,
`/tmp`, `/dev/shm` and `/run/lenny/slots/` and marks the scrub FAILED if any is non-empty, so an
occupancy-zero recycle on a pod carrying that residue cannot silently hand the pod to the next
tenant. On a non-recycling pod occupancy zero terminates the pod. No tenant-isolation gap to file.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:461,:471; spec-changes.md:133-142.

FACT: `MaxConcurrentSessions > 1` is the only route into `bindConcurrentSlot`; a
`maxConcurrentSessions: 1` pool takes the exclusive `Prepare`/`Launch` path. So §7.1's "on a pod
serving one session ... the pod retires" is grounded in the pool setting rather than in momentary
occupancy, and §13.1's "MUST set `maxConcurrentSessions: 1` for strict credential-lease isolation"
guarantee is not reached by the concurrent-path `leaked` disposition.
EVIDENCE: pkg/gateway/sessionserver/start.go:2351-2353,:2465-2479; spec/13_security-model.md:30.

UNVERIFIED (developed, judged below the bar, recorded so nobody re-derives it from scratch): the
§4.9 direct-mode expiry timer is the ENFORCED lease deadline — spec/04_system-components.md:1466
says "because the adapter expiry timer is the enforced lease deadline (its expiry deletes the
provider's credential-file entry) ... the direct-mode key never outlives the current lease", and
:1169 says the same for `anthropic_direct`. SPEC-3's staged action list writes the cancellation of
those timers into §5.2 for the first time (spec-changes.md:406), and in the shipped handler the
cancellation happens in `deregisterSlotLocked` BEFORE `removeSlotTree`
(pkg/adapter/session.go:238,270; pkg/adapter/slotsession.go:174-181). So a cleanup whose credential
removal fails leaves a direct-mode key on the pod's tmpfs with its enforcement point already
disarmed, and a `leaked` slot holds occupancy above zero so the §5.2 scrub-step-0 purge the proposal
names as the backstop never runs; the bound is pod drain at `ceil(maxConcurrentSessions/2)`.
I did NOT file it: `os.RemoveAll` on an adapter-owned tmpfs directory is a near-unreachable failure
(slotlayout/tree.go:60-70), the behaviour is shipped and pre-existing, the session is over so the
key has no consumer inside its own slot, and cross-slot readability is already an accepted
`acknowledgeProcessLevelIsolation` property (spec/13:30). A future round wanting to close it should
condition the timer cancellation on the credential removal, in SPEC-3's sentence, rather than widen
anything in §4.9.

WATCHOUT: the strongest remaining security-shaped candidate is barred. SPEC-2's §7.2 step 3 deletes
the "no runtime was started on it" premise but keeps "The pod needs no scrub beyond the pool's
default post-session scrub" and keeps the release back to the pool, so an UNACKNOWLEDGED reclaim on
that path returns a pod carrying slot tree and credential file to inventory. The standing WATCHOUT
from `spec.4.review-fresh.1` bars this as a close variant of the refuted "§7.1's exclusive-pod
disposition is unreachable on the §7.3 re-attach", and re-dressing it in tenant-isolation language
does not change the substance. Do not file it; if a human wants it settled, settle it as a decision.
EVIDENCE: spec-changes.md:282,:288-294; spec/07_session-lifecycle.md:214; spec/06_warm-pod-model.md:234.

USEFUL [Standing context / Traps, "Dead end: 'the adapter cannot see this leak'"] and [Settled,
"`SlotClaimer.ReleaseSlot(leaked=true)` returns early"]: together they close the whole family of
"the withheld report blinds the leak accounting" security findings in one read. The gateway derives
`leaked` from the `Shutdown` response, not from `ReportSessionScrub`, so withholding the report
never suppresses a degraded-state signal. Keep both.

### [spec.6.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site-completeness lens on the staged spec edits — BECAUSE every spec/ surface that mirrors a changed identifier or concept is either in the staged edit list or is silent rather than wrong — ALTERNATIVES: I considered and rejected filing four candidates, each recorded below with why it fails the bar.

FACT: the spec-side mirror inventory for this proposal is now closed, and it is small. Each changed concept has exactly the sites below and no others.
  - `Shutdown` row prose: `spec/04_system-components.md:686` only. It does NOT recur at `spec/29_communication-scenarios.md:696` (an earlier refutation entry claims it does; §29.4 step 12 is a paraphrase, not the same sentence). EVIDENCE: spec/04_system-components.md:686; spec/29_communication-scenarios.md:696
  - "per-session teardown" / "bound entry" phrasing: `spec/04_system-components.md:157` only. EVIDENCE: spec/04_system-components.md:157
  - per-slot sub-state edges (`slot_assigned`, `receiving_uploads`, `slot_cleanup`): `spec/06_warm-pod-model.md:146-156` plus `docs/reference/state-machines.md:234-237,251` and `pkg/sandbox/slotstate`. No OpenAPI, CRD, chart or proto mirror. EVIDENCE: spec/06_warm-pod-model.md:151-155; docs/reference/state-machines.md:234-237
  - `terminate` frame trigger: stated nowhere normatively except the §4.7 row and §29.4 step 13. `spec/28_communication-channels.md:1082,1100` state the frame's schema and its deadline, not when it is sent; `spec/29_communication-scenarios.md:1412` says explicitly that the specification does not state at what point the eviction path sends it; §15.4.2 names only the DRAINING state. EVIDENCE: spec/28_communication-channels.md:1082,1100-1110; spec/29_communication-scenarios.md:1412-1415; spec/15_external-api-surface.md:1686-1700
  - the credential path the SPEC-3 action list adds is spelled identically everywhere it appears (`/run/lenny/slots/{sessionId}/credentials.json`), so no new spelling is introduced. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455,461,471; spec/13_security-model.md:26,30; spec/04_system-components.md:793,914,1169

FACT: every markdown anchor the staged text mints resolves, including the two the recorded anchor sweep does NOT cover. `#49-credential-leasing-service` (new in SPEC-3's action list) resolves to `### 4.9 Credential Leasing Service` and is already used that way at spec/29:718; `#73-retry-and-resume` resolves to `### 7.3 Retry and Resume`. The four verbatim anchors added or re-anchored since the sweep (§29.4 step 13's trailing sentence, §5.2's `**Max retries:**` sentence, §5.2's `**Slot cleanup:**` action-list sentence, §7.3's list terminator) all match the tree byte for byte. EVIDENCE: spec/04_system-components.md:1099; spec/07_session-lifecycle.md:378,410; spec/29_communication-scenarios.md:706-711; spec/05_runtime-registry-and-pool-model.md:545,555

MISTAKE (nearly filed, do not re-derive): "§29.4 step 12 asserts the runtime close unconditionally and SPEC-1 makes it conditional, so step 12 is a missed edit site." It is not. §29.4's own **Preconditions** paragraph scopes the whole trace to a session that has completed the §29.2 startup sequence, "so the runtime is running", which satisfies the admitted-start predicate for every step in the trace. EVIDENCE: spec/29_communication-scenarios.md:588-593

WATCHOUT: the SPEC-1 §29.4 commentary justifies leaving step 12 alone with the wrong predicate — it says the runtime close "runs for every bound entry the call removed", while the staged §4.7 row says the runtime teardown "runs only for a session whose start the adapter has admitted". The conclusion (step 12 needs no edit) is right for a different reason than the one given. I did not file it, because applying the edits leaves the spec correct either way, but a fixer touching that block should not propagate "bound" as the runtime-teardown predicate. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:210-211 versus :189

DEFERRED [schemas/lenny-adapter.proto]: the `Shutdown` RPC doc comment reads "Shutdown asks the adapter to terminate the agent and release the pod. The adapter forwards a JSONL `shutdown` to the agent with the supplied deadline. Returns when the agent process has exited." After SPEC-1 that is false for two of the three cases the row now names: a request naming a session the adapter holds no entry for forwards nothing and returns a clean-exit response, and a request for a bound-but-unstarted session runs the slot release with no agent to terminate and no `shutdown` frame. What is true instead is the two-teardown split with two preconditions plus the no-entry no-op. The remedy is a proto doc-comment edit, which programme rule S-2 reserves to step R1b and which this proposal correctly declines to open (non-spec-changes.md "Staged schema, chart, and migration changes" states "None"). EVIDENCE: schemas/lenny-adapter.proto:203-206

MISTAKE (nearly filed, do not re-derive): "§6.2's fence gives `slot_cleanup ──→ leaked` exactly one trigger, `cleanup timeout exceeded`, and SPEC-2 adds a second (an unacknowledged reclaim) without widening it." The fence annotation is already narrower than shipped spec today: `spec/05_runtime-registry-and-pool-model.md:545` states "If cleanup fails, the slot is leaked" with no timeout qualifier, and `pkg/gateway/podlifecycle/podsession/slotbinder.go` already sets `leaked = err != nil || !cleanly` on the ordinary session end. SPEC-2 adds a third instance to a pre-existing gloss gap rather than creating a contradiction. EVIDENCE: spec/06_warm-pod-model.md:148; spec/05_runtime-registry-and-pool-model.md:545

MISTAKE (nearly filed, do not re-derive): "§4.1's second sentence names `per-slot teardown` and `whole-pod teardown`, which SPEC-1's replacement leaves undefined." The vocabulary mismatch inside that paragraph is pre-existing: before the edit, sentence 2 already said "per-slot teardown"/"whole-pod teardown" while sentence 3 said "per-session teardown"/"whole-pod scrub" — three names for two or three things. The edit does not create the drift. This is the `§4.1's retired vocabulary` OPEN in the standing context; it stays a wording question. EVIDENCE: spec/04_system-components.md:157

USEFUL [Standing context, "The anchor sweep is done; do not repeat it"]: it saved me re-deriving six anchors, and I only had to check the two minted since. Please keep extending that entry when a fix round mints a new anchor, rather than leaving the next reviewer to spot that the list is stale.

USEFUL [Standing context, "Dead end: 'the withheld report contradicts §4.7 or §12'"]: it correctly predicted where I would land after grepping `ReportSessionScrub` across spec/, docs/ and schemas/. All five sites (spec/04:692, spec/05:453,545, spec/12:481,494, schemas/lenny-adapter.proto:308-319,451-474, docs/reference/adapter-contract.md:81) scope their rule to "a session release", so none of them is falsified by SPEC-3's exception.

### [spec.7.review-applicability.1]

FACT: The round-6 fix changed exactly one thing in the spec staging: it added the `SPEC-1 · spec/29_communication-scenarios.md § 29.4 (session-end step 13)` block and one line to "Spec files touched". `diff -rq scratchpad/cp-snap/0081/spec-r6 proposals/0081_.../` shows only the review log differs from the current tree, so r6→r7 was a no-op on spec-changes.md. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:196-211,:497

FACT: Every "text to replace" fenced block in SPEC-1..SPEC-4 still matches its target file EXACTLY ONCE, and every "replace it with" block matches nothing (so no edit is already applied). Verified mechanically this round with a python pass over the sixteen fenced blocks against spec/04, spec/05, spec/06, spec/07 and spec/29. The standing-context anchor sweep is still good; re-run only if the spec moves. EVIDENCE: spec/04:157,:686,:854; spec/05:453,:545,:555; spec/06:152,:158,:234; spec/07:23,:210,:213,:214,:414

FACT: The new §29.4 anchor is the sentence ending "([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3)." at spec/29:711. That exact string ALSO ends §29.4 step 6 at spec/29:645, so the instruction is only unambiguous because it scopes itself to "numbered step 13". Do not drop that scoping if the block is ever reworded. EVIDENCE: spec/29_communication-scenarios.md:645,:711

FACT: No gate this proposal reaches hard-fails on the staged spec text. Checked concretely: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go (its `generalSlotEdges` list is presence-only and does not enumerate the new `receiving_uploads ──→ slot_cleanup` edge, and the scoped-block negative check iterates the same four-edge list, so SPEC-4's added edge is invisible to it); recycle_scrub_trigger_consistency_test.go (the retained half of the §4.7 `Shutdown` row still carries "recycle disposition", "ReportPodScrub", the three recycle params, "does not block the response on the scrub" and the §5.2 link, and SPEC-1's new opening adds a second §5.2 link); credential_path_literal_sweep_test.go (SPEC-3 writes `/run/lenny/slots/{sessionId}/` and `credentials.json` as two separate tokens, so it never forms the banned `retiredPodGlobalCredentialPath` = "/run/lenny/" + "credentials.json"); code_blocks_test.go (only language-tagged fences are parsed, and both the §7.1 flow fence and the §6.2 state fence are untagged, so inserting markdown prose into them trips nothing). EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-36,:66-74; tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65-100; tests/tier11_docs/adapter_manifest_credentials_path_doc_reconciliation_test.go:33,:40; tests/tier11_docs/code_blocks_test.go:66-72

FACT: The staged §5.2 `**Max retries:**` sentence and the §5.2 `**Slot cleanup:**` action-list sentence have NO mirror anywhere in tests/, docs/, pkg/ or spec/. Greps for "always assigned to a **new slot**", "fully saturated or unhealthy", "kills any processes owned by" and "releases the `slotId`" return the single spec/05 site each. So neither SPEC-2's nor SPEC-3's first anchor drags an unstaged surface with it.

FACT: All cross-reference anchors the staged text mints resolve to live headings: `#73-retry-and-resume` (spec/07:378), `#49-credential-leasing-service` (spec/04:1099), `#1542-rpc-lifecycle-state-machine` (spec/15:1686), `#479-startup-sequence-for-type-agent-runtimes` (spec/04:848), plus the previously-swept four.

FACT: SPEC-3's two rationale claims about §4.9 and §5.2 check out. spec/04:1169 states the direct-delivery-mode adapter-side per-lease expiry timer that deletes `/run/lenny/slots/{sessionId}/credentials.json`, and spec/05:455 (recycle lifecycle) and spec/05:461 (scrub step 0) both already presuppose the credential file is per-slot and purged, so the widened action list is a catch-up rather than a new obligation.

FACT: §28.5.3's `terminate` message-schema row and its Timing/Degradation bullets state nothing that the SPEC-1 co-tenancy gate on the graceful-shutdown signal contradicts; the row's "Receipt always means process exit" is the reason the gate exists. §15.4.2 states DRAINING generically with no per-session claim. So spec/29 §29.4 step 13 was the only unstaged site that needed the condition, and the round-6 fix closed it. EVIDENCE: spec/28_communication-channels.md:1082,:1100-1130; spec/15_external-api-surface.md:1686-1705

WATCHOUT: §29.4's `resume_pending` sibling at spec/07:220-221 ("no half-claimed replacement pod to release") is NOT falsified by SPEC-2's §7.2 step-3 rewrite — the §7.1 obligation begins at the first pod-side RPC and `resume_pending` has issued none, so the contrast still holds. A later round tempted to add a pointer there is adding narration, not closing a gap. EVIDENCE: spec/07_session-lifecycle.md:220-221

UNVERIFIED: the §29.4 block's closing rationale sentence reads "Step 12 is untouched: it states that the adapter closes the session runtime, which runs for every bound entry the call removed and carries no co-tenancy condition." Under the proposal's own DECISION the runtime teardown runs for a session whose start the adapter ADMITTED (`st.started`), which is a strict subset of bound entries, so the gloss states the predicate the design rejected. The CONCLUSION is still right (in §29.4's trace the session is running, so started holds and step 12 needs no edit), and the sentence is proposal rationale rather than staged spec text, so I judged it below the materiality bar and did not file it. If a later round wants it corrected, the fix is one clause: "for every entry whose start the adapter admitted". EVIDENCE: proposals/0081_.../0081_....spec-changes.md:209-211 against :18-21 and :189

DECISION: returned an empty findings list — BECAUSE every staged edit's anchor is present, unique and byte-exact; every created artifact's text is given verbatim; no edit references an artifact a later sub-step creates (SPEC-3 lands the §5.2 statement SPEC-4's prose cites, and the checklist orders S3 before S4); the two-leg check on the only relocations (§7.2 preamble deletion and step-3 replacement) shows both legs staged with the surviving no-scrub clause re-scoped; and no existing gate fails on the applied text — ALTERNATIVES: considered filing the §29 step-12 gloss (above, judged rationale-only) and the §4.1 retained-second-sentence vocabulary drift already recorded as OPEN in the standing context (also rationale/wording, and the standing context records it as unresolved rather than as a defect this loop introduced).

### [spec.7.review-citations.1]

FACT: The round-6 fix added exactly one new block, SPEC-1 · spec/29 §29.4 step 13, and nothing else in
spec-changes.md moved since r5-prefix. `diff scratchpad/cp-snap/0081/spec-r5-prefix/... proposals/...`
is the whole delta. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:196-211

FACT: Every "text to replace" block in SPEC-1..SPEC-4 still matches its target file exactly once, including
the new §29 anchor. Re-verified mechanically by substring-counting all 28 fenced blocks against
spec/04, spec/05, spec/06, spec/07 and spec/29. Anchors land at spec/04:157, :686, :854; spec/05:453,
:545, :555; spec/06:148-157, :234; spec/07:23, :210, :213, :214, :414; spec/29:706-711. Every markdown
anchor the staged text mints resolves, including the two intra-file ones (`#47-runtime-adapter` inside
spec/04's §4.1 replacement and `#71-normal-flow`/`#73-retry-and-resume` inside spec/07). Do not repeat
this sweep unless the spec moves.

FACT: The §29.4 step-13 edit's substance checks out against the tree. The `terminate` frame carries
`type`, `deadlineMs`, `reason` and no session field (spec/28_communication-channels.md:1082), the
CH-RUNTIMEOPS endpoint is one per-pod abstract Unix socket `@lenny-runtime-ops`
(spec/28_communication-channels.md:1051), and the shipped adapter gates the send on `!boundRemains` with a
comment that is nearly the staged §4.7 wording (pkg/adapter/session.go:259, :250-258). Step 13 already
carries an inline Basic/Standard non-occurrence exception, so the added one follows the step's form
(spec/29_communication-scenarios.md:706-711).

FACT: §29.10 is NOT a missed edit site for the §29.4 co-tenancy sentence. Its "Shared by the whole pod"
list is not exhaustive-claiming, it carries no CH-RUNTIMEOPS bullet, and its opening explicitly hands the
addressing mechanisms back to "the sections that state them" with §4.7 named among the owners.
EVIDENCE: spec/29_communication-scenarios.md:1430-1439, :1477-1531

WATCHOUT: the §29 rationale's claim that the operative clause "reuses §4.7's own wording verbatim" IS
true of the operative clause itself ("it goes out only when the deregistration leaves the adapter holding
no bound entry" is byte-identical in both). Only the lead-in differs ("The frame is" vs "the signal is")
and §29 adds "on a pod serving concurrent sessions", which is behaviourally vacuous at
`maxConcurrentSessions: 1` because a single-slot pod's deregistration always leaves no bound entry. I
started to file this and dropped it. EVIDENCE: spec-changes.md:189 vs :203

MISTAKE: the same §29 rationale block reintroduced the `bound` predicate for the runtime close — "it
states that the adapter closes the session runtime, which runs for every bound entry the call removed".
That is the third instance in this proposal's history of the bound/started drift the whole deliverable
turns on (the first two are in the "already found and fixed" list). Filed as the single finding of this
pass. The conclusion the sentence supports (leave step 12 alone) is right; only its stated ground is
wrong, so the fix is the predicate phrase, not the decision. EVIDENCE: spec-changes.md:209-211 versus
spec-changes.md:18-20 and :189

UNVERIFIED: the Design paragraph says §7.1's atomicity paragraph "closes by stating that the client never
receives a `session_id`", while SPEC-2's own insertion instruction correctly names the paragraph's real
last sentence ("...roll-back-without-persist regardless of the flag"). Both describe spec/07:23. The
sentence the Design names sits mid-paragraph. Judged below the bar (the argument it supports does not
depend on position), but a later citation pass may disagree. EVIDENCE: spec-changes.md:40-42, :231-232;
spec/07_session-lifecycle.md:23

UNVERIFIED: "The row keeps its §4.6 pod binding" (spec-changes.md:111). The session-to-pod binding lives
in the session row's `pod_assignment` column, which §29.4 step 3 attributes to §4.2 and §4.6.1 rather
than to §4.6. §4.6 is a real heading ("Pod Lifecycle Controllers") containing §4.6.1, so the citation is
loose rather than false. Not filed. EVIDENCE: spec/29_communication-scenarios.md:608-611;
spec/04_system-components.md:334,:338

### [spec.7.review-client-surface.7]

FACT: the round-7 snapshot is byte-identical to the live proposal directory — `diff -ru /home/ec2-user/lenny/scratchpad/cp-snap/0081/spec-r7 <proposal dir>` returns nothing, so there is no "what changed this round" reading order for round 7. Read the whole staged file. EVIDENCE: scratchpad/cp-snap/0081/spec-r7 (all nine files, same sizes as the proposal directory).

FACT: `terminate`'s `reason` is a closed four-value enum ONLY on the intra-pod CH-RUNTIMEOPS frame (spec/28:1082: `"session_complete" | "budget_exhausted" | "eviction" | "operator"`). `ShutdownRequest.reason` on the gateway→adapter wire is a bare `string` with no enumeration anywhere, so the staged code's `"slot_bind_failed"` mints no wire value and needs no proto or SDK edit. This closes the round-1 `terminate` frame reason OPEN as "wire stays valid"; what is still undecided is only whether `session_complete` is the intended frame value, which is a code-lane question. EVIDENCE: schemas/lenny-adapter.proto `message ShutdownRequest` (`string reason = 2;`); spec/28_communication-channels.md:1082.

FACT: §28.5.3's CH-RUNTIMEOPS card states no per-session-end emission rule for the `terminate` frame — only "Graceful shutdown signal ... Receipt always means process exit" in the message table, plus the SIGTERM-on-deadline degradation. So SPEC-1's new no-bound-entry condition on the signal creates NO missing edit site in §28, and §15.4.2/§15.4.3 state only level capabilities, not emission conditions. Do not re-derive this; it costs a full read of §28.5.3 and §15.4.3. EVIDENCE: spec/28_communication-channels.md:1063-1130; spec/15_external-api-surface.md:1686-1706,:1780.

FACT: `GET /v1/sessions/{id}/setup-output` is served from the Postgres session row (`row.SetupOutput`), not from the pod, so the mandatory pod-side reclaim on a failed bind destroys no client-retrievable setup output. The §15.1 `SETUP_COMMAND_FAILED` row promises that endpoint after a RunSetup failure, which is exactly a stage the new reclaim covers. EVIDENCE: pkg/gateway/sessionserver/session_subresources.go:191-225; spec/15_external-api-surface.md:1136.

FACT: the adapter manifest is pod-global at `/run/lenny/adapter-manifest.json`, NOT under `/run/lenny/slots/{sessionId}/`, so SPEC-3's added "removes the slot's credential directory `/run/lenny/slots/{sessionId}/`" cannot take the manifest with it. `slotlayout.RemoveTree` removes exactly slotRoot, Sessions, Artifacts and CredentialsDir. EVIDENCE: spec/04_system-components.md:723; pkg/adapter/slotlayout/tree.go:58-70.

FACT: the §4.9 timer citation in SPEC-3 checks out and is correctly qualified. The lease-expiry timer is stated only in the `anthropic_direct` row of §4.9's provider-default table, and only "In direct delivery mode", where the adapter deletes `/run/lenny/slots/{sessionId}/credentials.json` and reports `AUTH_EXPIRED`. EVIDENCE: spec/04_system-components.md:1169.

FACT: all three session-start RPCs set `st.started` through the one accessor, so the staged §4.7 wording "which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed" is implementable with a single predicate: `claimSessionSlot` is called from `StartSession` (session.go:111), `Resume` (resume.go:50) and `ConfigureWorkspace` (sdkwarm.go:217), and `claimSessionSlotUnderLock` sets `st.started = true` at slotsession.go:88. The earlier worry that an SDK-warm or resumed session would skip the runtime teardown is closed. EVIDENCE: pkg/adapter/slotsession.go:64-90; pkg/adapter/sdkwarm.go:217; pkg/adapter/resume.go:50; pkg/adapter/session.go:111.

WATCHOUT: §29 self-declares that a trace which disagrees with its cited section is the defect ("Where a trace and a cited section disagree, the cited section is the normative statement and the trace is the defect", spec/29:23-25). That cuts both ways: it is not a licence to leave a trace stale — this proposal already staged a §29.4 step-13 edit rather than relying on the resolution rule — so an argument that "§29 is only a restatement, no edit needed" has to explain why step 13 was edited and a sibling step was not.

FILED: the SPEC-1 §29 note at spec-changes.md:209-211 justifies leaving §29.4 step 12 unedited with "the adapter closes the session runtime, which runs for every bound entry the call removed", which is the pre-split `bound` predicate SPEC-1 replaces with "only for a session whose start the adapter has admitted" (spec-changes.md:189, :18-20). One finding, kind `contradiction`.

UNVERIFIED: whether §29.4 step 12's unconditional "the adapter closes the session runtime" needs a condition of its own. §15.1:649 lets `POST /terminate` fire from `created`, `finalizing`, `ready` and `starting`, so a session end can reach step 12 with a bound-but-unstarted entry, where the staged split runs no runtime teardown. The defensible reading is that §29.4 traces the end of a started session (step 11 seals a live workspace, step 14 expects `FINAL_USAGE_REPORT`), which would make step 12 true for its traced scenario — but nothing in §29.4 says so. Somebody should decide whether the fix is a step-12 condition or a corrected note.

USEFUL [Standing context / Settled]: "`ShutdownResponse` is `{exited_cleanly, exit_code}` ... `exited_cleanly` is defined nowhere in spec/ or docs/" and "programme rule S-2 bars widening the response" saved this lens from re-filing the clean-exit-response vocabulary gap that was already refuted twice.

### [spec.7.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE every docs-alignment defect I could substantiate is a
docs/ edit, and this loop's scope bars a finding whose only remedy lands outside the staged spec edits.
ALTERNATIVES: filing the `adapter-contract.md` drift again (already the merged standing-context DEFERRED at
review-log.md:135, plus WATCHOUTs at :516 and :1739); filing under-enumeration of the slot-cleanup action
list in the §6.2 fence annotation (rejected — same class as the already-refuted §5.2/§6.4 enumeration
finding).

FACT: the round-6 fix (SPEC-1 · spec/29 §29.4 step 13) is the only text that changed since the r5 snapshot;
`diff -rq scratchpad/cp-snap/0081/spec-r6 <proposal dir>` shows spec-changes.md and summary.md identical to
the current text, so round 6 landed no further spec-staging edits. EVIDENCE: scratchpad/cp-snap/0081/spec-r6.

FACT: I verified the whole §29.4 edit against the tree and it holds. Step 13's anchor sentence ends
"([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3)." exactly as staged
(spec/29_communication-scenarios.md:706-712). The `terminate` frame really does carry only
`type`/`deadlineMs`/`reason` and names no session, so "pod-global and names no session" is accurate
(spec/28_communication-channels.md:1082). The shipped adapter gates the signal on `!boundRemains` inside the
`bound` branch, which is what the sentence restates (pkg/adapter/session.go:238-241,:259). The operative
clause is byte-identical to the §4.7 staged clause, so the two cannot paraphrase apart.

FACT: `terminate` (the CH-RUNTIMEOPS frame) appears in spec/ outside §28.5.3 only at
spec/10_gateway-internals.md:58 (hold-timeout self-termination), spec/15:2179 and :2203 (SDK contract), and
spec/29:706 and :1412. None of them asserts the frame goes out at every session end, so §29.4 step 13 was
the only spec site the co-tenancy condition falsified. The sweep is done; do not repeat it.

FACT: §5.2's report trigger is mirrored in two spec sites the proposal does NOT edit and does NOT need to:
spec/04_system-components.md:692 (the §4.7 `ReportSessionScrub` row, "at a session release") and
spec/12_storage-architecture.md:481 ("incremented at each session release (`ReportSessionScrub`)"). Both
survive SPEC-3 because SPEC-3 keeps "session release" as the reporting trigger and classes a pre-`running`
reclaim as not one ("A cleanup that reclaims a slot the pod's shared runtime process was given is a session
release like any other and reports its outcome"). Checked so a later round does not file them.

FACT: no docs/ page mirrors §7.2's mid-resume snapshot-close sequence or the §5.2 slot retry policy.
`grep -rn "half-claimed\|snapshot-close\|slot retry\|new slot"` over docs/ returns nothing, so SPEC-2's §7.2
preamble deletion and its `**Max retries:**` widening create no docs site. This confirms the earlier
re-check at review-log.md:702 independently.

DEFERRED [docs/reference/adapter-contract.md]: unchanged from the standing entry at review-log.md:135. I
re-read line 75 and line 81 and both are still falsified by SPEC-1 and SPEC-3 in the ways that entry states.
Nothing to add.

DEFERRED [docs/client-guide/session-lifecycle.md:416, docs/reference/adapter-contract.md:84,
docs/runtime-author-guide/index.md:186, docs/runtime-author-guide/lifecycle.md:69]: these four also describe
the per-slot cleanup, and unlike the five sites the standing DEFERRED names they stay TRUE after SPEC-1 and
SPEC-3. `:416` and `:84` say the per-slot cleanup runs and the whole-pod scrub runs at occupancy zero
without claiming a report on every path; `index.md:186` and `lifecycle.md:69` say only that both are
adapter-executed and gateway-coordinated. Recorded explicitly so the non-spec loop does not widen its docs
edit list past the five sites that actually break.

DEFERRED [docs/reference/state-machines.md:234 and spec/06_warm-pod-model.md:156]: the
`slot_cleanup ──→ released` annotation reads "(slot workspace removed, processes killed, slot released)",
which is today an exact three-way mirror of §5.2's action list. SPEC-3 widens that list to five actions (the
credential directory and the §4.9 expiry timers), so after application the annotation and its docs row are
an abbreviated gloss of a longer list rather than a mirror of it. I did NOT file it: an abbreviated
annotation states nothing false, and a materially identical enumeration finding against §5.2/§6.4 was
already refuted this loop. Recorded because whoever edits DOCS-1's table is standing next to the row.

WATCHOUT: the racing-start bullet closes "Neither ordering reports a cleanup outcome, because the slot never
reached `running`", while the same bullet's second ordering says "the pod then holds a started session",
i.e. that slot does reach `running` after the reclaim. The sentence is defensible read as "neither reclaim
files a report", and it sits in the proposal's reasoning rather than in landing spec text, so I did not file
it; the closely-related "racing-start residue lands in no staged spec text" finding is already on the
refuted list. EVIDENCE: spec-changes.md racing-start bullet (lines 133-142 of the rendered file).

UNVERIFIED: still nobody has run tier 11 against the `docs/reference/adapter-contract.md:75` drift, which
review-log.md:362 already flags. My reading of DOCS-1 is that tier 11's reconciliation covers the per-slot
sub-state table against the §6.2 fence, not the §4.7 RPC-table prose, so the drift may well pass the gate
silently and be caught only by a reader. The non-spec loop should run it rather than assume either way.

### [spec.7.review-edit-sites.1]

FACT: Round 6 changed exactly ONE thing in the spec staging — it added the SPEC-1 §29.4 step-13 block (spec-changes.md:196-211 plus the `spec/29_...` line in "Spec files touched"). `diff` of scratchpad/cp-snap/0081/spec-r5 against the live folder is that hunk and nothing else; spec-r6 and spec-r7 are byte-identical to the live folder. EVIDENCE: scratchpad/cp-snap/0081/spec-r5 vs proposals/0081_.../0081_....spec-changes.md.

FACT: `terminate` is the ONLY §29 site that emits the graceful-shutdown signal. `grep -n "terminate\b" spec/29_communication-scenarios.md` puts the frame at step 13 alone (spec/29:704-711); §29.7 (drain) and §29.9 (eviction) carry checkpoint frames, not `terminate`. So SPEC-1's single §29 site is the complete §29 mirror for the new no-bound-entry condition. Do not go hunting for a second one. EVIDENCE: spec/29_communication-scenarios.md:704-711.

FACT: the Basic/Standard stdin `shutdown` frame is ALSO pod-global and names no session, so §4.7's new "the signal is pod-global and names no session" holds at every integration level. §28.5.3's CH-MSGSOCK card enumerates the session-addressed frames as `message`, `tool_result`, `response`, `tool_call`, `set_tracing_context`, and `status` — `heartbeat` and `shutdown` are excluded by name. A lens tempted to file "the claim is false for Basic/Standard" should stop here. EVIDENCE: spec/28_communication-channels.md:549-553, :618-623.

FACT: the shipped `Shutdown` handler already gates the §15.4.2 signal on `!boundRemains` inside the `bound` block, with a comment that is nearly verbatim the staged §4.7 sentence. The §29.4 and §4.7 co-tenancy clauses are restatements of shipped behaviour, not new obligations. EVIDENCE: pkg/adapter/session.go:238-262.

WATCHOUT: §29's preamble makes a trace that DISAGREES with its cited section a defect, but says nothing about a trace that merely omits. "A trace restates behaviour the specification states elsewhere... Where a trace and a cited section disagree, the cited section is the normative statement and the trace is the defect." So §29.2's silence on the finalize-block failure branch (steps 15-22 carry no reclaim) and §29.6 step 4's `RESUME_FAILED` branch are omissions, not defects, and are NOT edit sites under §29's own rule. Two lenses have now chased this. EVIDENCE: spec/29_communication-scenarios.md:23-25, :216-285, :1045-1050.

WATCHOUT: §29.2 step 10 mirrors §7.1's atomicity clause ("a failure at any of them rolls back the pod claim, persists no session row"). It does NOT become an edit site under SPEC-2's widened parenthetical, because the steps-2-10 window issues no pod-side RPC — the same vacuity that got the "vacuous parenthetical" finding refuted. EVIDENCE: spec/29_communication-scenarios.md:196-201.

DECISION: filed one finding — the §29.4 block's rationale sentence "Step 12 is untouched: it states that the adapter closes the session runtime, which runs for every bound entry the call removed" (spec-changes.md:209-211) contradicts SPEC-1's own §4.7 replacement, which says the runtime teardown "runs only for a session whose start the adapter has admitted" (spec-changes.md:189) and whose whole point is that bound ⊋ started. BECAUSE this is the same wrong precondition the loop already paid a round to correct in checklist S7, reintroduced in the newest text. ALTERNATIVES: I did not file the conclusion (leaving step 12 alone is right, because §29.4's preconditions put the session in `running`, so the admitted-start precondition holds in that trace) — only the stated reason is false.

DECISION: did NOT file three near-miss candidates. BECAUSE each is prose incompleteness rather than a contradiction, and each has a close refuted precedent. ALTERNATIVES considered and rejected:
  (a) §4.1's retained second sentence still names "per-slot teardown" and "whole-pod teardown" after the replacement retires that vocabulary for "slot release"/"runtime teardown"/"whole-pod scrub" (spec/04:157). Logged as OPEN by `spec.1.review-fresh.1` and still open. Rejected because the "same operation on the same address" tension is PRE-EXISTING (the pre-edit sentence 3 already named two operations while sentence 2 called them one), so the edit adds a naming mismatch rather than a new contradiction.
  (b) §6.2's `slot_cleanup ──→ released` annotation, "(slot workspace removed, processes killed, slot released)" (spec/06:155), is a verbatim three-item mirror of the §5.2 action-list sentence SPEC-3 widens to five, and SPEC-4 already edits that same fence block, so the fix would land in staged spec text. Rejected because the annotation is an edge gloss rather than a normative list, and the material skeptic already refuted the structurally identical "SPEC-3's action list omits /sessions/ and /artifacts/" finding as completeness polish.
  (c) "Spec files touched" describes the §4.7 row edit as "(first sentence plus one sentence)" (spec-changes.md:485) when the replacement swaps two sentences for seven. Bookkeeping, trivially small, and the loop exempts index drift in the neighbouring files.

FACT: `slotId` is nowhere in spec/ defined as equal to the session id — §29.10:1457 says "identified by a `slotId` the gateway mints" and §5.2 only ever "releases the `slotId`". The staged §7.1 sentence "The slot identifier is the session identifier" is therefore a NEW spec claim, but it is code-true (standing context: `SlotID == SessionID` on every path) and §6.4's `/workspace/slots/{sessionId}/` already implies it, so it is not a contradiction. Recorded so a later lens does not re-derive it. EVIDENCE: spec/29_communication-scenarios.md:1457; spec/05_runtime-registry-and-pool-model.md:545.

USEFUL [Standing context / "The anchor sweep is done; do not repeat it"]: I spot-checked five of the six anchors anyway (spec/04:157, :686, :848-854; spec/05:545, :453, :555; spec/06:155, :234; spec/07:23, :210, :214, :414) plus the new spec/29:711 one, and every "text to replace" block still matches byte for byte and is unique. The sweep entry is accurate; the §29 anchor added in round 6 is now verified too and can be folded into it.

### [spec.7.review-feasibility.1]

DECISION: returned an empty findings list for round 7 under the actor-action-feasibility lens — BECAUSE every action the staged spec text assigns is performed by a component that exists under that name and can see the data its check needs, and I could not derive a fresh feasibility defect. ALTERNATIVES: the two candidates I built and dropped are named below; both were already considered and declined by earlier lenses.

FACT: the r7 snapshot is byte-identical to the current proposal, and `diff -rq` against `spec-r6` differs only in the review log. The last CONTENT change was r5→r6: the SPEC-1 §29.4 step-13 block plus the `spec/29_...` line in "Spec files touched". Anything else in the document has now survived at least two rounds. EVIDENCE: scratchpad/cp-snap/0081/spec-r6 vs proposals/0081_.../ (only the review log differs).

FACT: the actor/visibility check passes on every staged block, and here is the check itself so a later round need not redo it. Adapter-local predicates (entry present / start admitted / runtime-holds) are all computable inside the adapter under one `s.mu` hold: `s.slots` is keyed by the SESSION id (`deregisterSlotLocked` reads `s.slots[sessionID]`), so "the adapter holds an entry for the named session" is a map lookup even for an entry `AssignCredentials` never bound. Gateway-side predicates (`leaked`, the §5.2 placement exclusion) are in-process on the replica that ran the failed bind. No staged sentence asks the adapter to read gateway state or the gateway to read adapter-internal state, and no staged sentence assigns a CRD status write: the exclusive-pod sentence has the gateway release the pod CLAIM and lets §6.2/WPC project the phase, which matches §4.6.3. EVIDENCE: pkg/adapter/slotsession.go:174-188; pkg/adapter/slot.go:104-124; pkg/adapter/slotcreds.go:23-38; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-885; spec/04_system-components.md:409 ("the gateway does not write `Sandbox.status`").

FACT: the §7.1 paragraph's three-kind parenthetical (creation finalize block / §15.1 start / §7.3 re-attach) is EXHAUSTIVE over the gateway's bind attempts, which I checked because a fourth kind would be a real unowned-residue finding. `POST /v1/sessions/{id}/resume` is not a fourth kind: §15.1's precondition table routes it through `resume_pending → running`, i.e. the same §7.3 flow. A podless `suspended` session resuming is also routed to `resume_pending` before it reacquires a pod. `POST /v1/sessions/start` is the creation path. EVIDENCE: spec/15_external-api-surface.md:633,650; spec/06_warm-pod-model.md:204,220; spec/07_session-lifecycle.md:185.

FACT: the SDK-warm start really does set the adapter's `started` flag, so SPEC-1's "a session whose start the adapter has admitted" is reachable on all three admitting RPCs and needs no carve-out for `ConfigureWorkspace`. `ConfigureWorkspace` calls `claimSessionSlot(sessionID, true, true)` and, on the freshness arm, `noteRuntimeStarted`. EVIDENCE: pkg/adapter/sdkwarm.go:217,:261.

FACT: SPEC-3's two added actions are adapter-feasible and the §4.9 vocabulary is right. §4.9 states the direct-delivery-mode timer obligation verbatim ("In direct delivery mode, the adapter MUST set a local timer for each credential lease's `expiresAt`") and names the file as `/run/lenny/slots/{sessionId}/credentials.json`; in proxy mode no timer is armed, so the added clause is vacuous rather than wrong there. EVIDENCE: spec/04_system-components.md:1169; pkg/adapter/slotsession.go:174-188.

FACT: the §29.4 step-13 edit's factual premise holds. The `terminate` frame's schema row is `type`, `deadlineMs`, `reason` with no session field, and CH-RUNTIMEOPS is one per-pod socket (`@lenny-runtime-ops`), so "pod-global and names no session" is accurate, and the appended operative clause is byte-identical to the one in the staged §4.7 row. The shipped gate is the same (`if !boundRemains { s.drainViaLifecycle(...) }`). EVIDENCE: spec/28_communication-channels.md:1082,:125,:157; pkg/adapter/session.go:259-260.

FACT: all 14 "text to replace" anchors in the staged edits still match the tree exactly once, INCLUDING the four added or re-anchored since the recorded anchor sweep. I re-ran the check mechanically (extract every fenced block, substring-count it against spec/04, 05, 06, 07, 29). Do not spend a round on this again unless the spec moves. EVIDENCE: spec/04:157,:672-686,:686 area; spec/05:545,:438-area,:368-area; spec/06:148-157,:234; spec/07:23,:210,:212,:214,:410; spec/29:706-711.

MISTAKE (nearly filed, do not re-derive): "the recycle-disposition `Shutdown` names a session the adapter no longer holds, so SPEC-1's no-op sentence stops §5.2's 'The adapter closes the ending session's runtime' from being true." The two-RPC split is an IMPLEMENTATION fact (`Binder.ReleaseSlot` sends the per-slot `Shutdown` then, on `recycled`, a second `ShutdownRecycle` reusing the same session id); the SPEC still models one request carrying the disposition beside the teardown, so the applied spec is self-consistent and the divergence is pre-existing. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:528-582; spec/05_runtime-registry-and-pool-model.md:459.

USEFUL [spec.6 entries at review-log.md:2195 and :2197]: they saved me from filing both of my only two candidates. :2195 kills "§29.4 step 12 is a missed edit site" (the §29.4 Preconditions paragraph scopes the whole trace to a session whose runtime is running, so the admitted-start predicate holds at every step). :2197 records the same "runs for every bound entry the call removed" wrong-predicate note I re-derived independently, already judged below the bar. A round-8 lens should read both before opening spec-changes.md:210-211.

USEFUL [review-log.md:1205, :1507, :1554]: three separate lenses have now considered the staged §4.7 phrase "the pod's session mode" (which `spec/05:385,:395,:521` binds to `executionMode: session` vs `service`, while the axis that actually selects the starting RPC is pod-warm vs SDK-warm, spelled "(pod-warm mode)" / "(SDK-warm mode)" in §4.7's own table at spec/04:672-673) and all three declined to file it as wording. I make it four. It is a genuine term collision and the fix is two words, but it is not worth another two verifiers; if a human wants it corrected, correct it outside the loop.

OPEN: the standing OPEN at review-log.md:1205 is now four-times-deferred. Somebody with authority should either close it ("wording, will not fix") or hand the fixer the two-word correction directly, so a fifth lens does not spend the same half hour re-deriving it.

### [spec.7.review-fresh.1]

FACT: Round 6 changed no proposal file except the review log. The only spec-staging delta since r5 is the new
`SPEC-1 · spec/29_communication-scenarios.md § 29.4 (session-end step 13)` block plus its `Spec files touched`
entry. EVIDENCE: `diff -u scratchpad/cp-snap/0081/spec-r5/...spec-changes.md proposals/.../...spec-changes.md`
returns exactly those two hunks; `diff -q` r6 vs current shows only the review log differing.

FACT: the §29.4 step-13 anchor is genuine and the appended sentence's factual premise checks out. Step 13's
last sentence ends `([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3).` at
spec/29:704, the `terminate` frame's field set is `type`, `deadlineMs`, `reason` with no session id
(spec/28:1082), and the shipped gate is `if !boundRemains { s.drainViaLifecycle(...) }`
(pkg/adapter/session.go:259). The `([§15.4.3](...), §28.5.3).` string occurs 3× in spec/29, so the anchor is
unique only because the instruction names §29.4 step 13; do not drop that qualifier.

MISTAKE: the same new SPEC-1 block's closing commentary reintroduces the retired predicate. It says the
adapter's runtime close "runs for every bound entry the call removed" (spec-changes.md:210) while SPEC-1's own
§4.7 replacement says the runtime teardown "runs only for a session whose start the adapter has admitted"
(spec-changes.md:189). Bound ⊃ started (slotcreds.go:32-34 binds at AssignCredentials; slotsession.go:87-88
sets `started` only in `claimSessionSlotUnderLock`). Filed. This is the third time in this proposal's history
that a fixer restated the teardown precondition in the wrong vocabulary; check every new sentence that names
the runtime teardown against §4.7's "start the adapter has admitted".

WATCHOUT: `§29.4` step 12 does NOT become false under the split, so the fix is to correct the *reason* rather
than to stage an edit to step 12. The traced session end is of a `running` session, so its runtime close is
owed under the narrower predicate too. EVIDENCE: spec/29:696-697.

OPEN (filed): SPEC-4's prose asserts that §6.2's untouched `receiving_uploads → running` trigger names "the
moment the pod's shared runtime process has been given the session" (spec-changes.md:479, and the same claim
at :86). The trigger's own words are "workspace ready, session dispatched to runtime with its session
identifier" (spec/06:152-153) — the dispatch, not the acknowledgement — while the proposal's bound vocabulary
puts "has been given" at `runtimeLive`, set only after `Runtime.Start` returns
(pkg/adapter/runtimegeneration.go:36-49; session.go:156,163). The standing context's UNVERIFIED asking a later
reviewer to confirm the four sites agree is answered: the four STAGED sites agree with each other; the
untouched trigger is the one that does not. This propagates to the report gate, because SPEC-3 keys the
withheld report on "before it reaches `running`" (spec-changes.md:444).

FACT: `SlotID == SessionID` is fixed in the tree and stated nowhere in `spec/`. `podclaim.SlotRequest.SessionID`
"is also used as the slot's SlotID" (slotclaimer.go:210-215), `SlotID: req.SessionID` (slotclaimer.go:760),
and `applySlotRetryPolicy` re-calls `BindSlot` with the same `req` (start.go:2808-2812). SPEC-2's §7.1 block is
the FIRST spec statement of that identity.

DECISION: I did NOT file §5.2:555-556 ("The retry is always assigned to a **new slot**" / "**Fresh workspace
guarantee**") as contradicted by that identity — BECAUSE "new slot" reads defensibly as a new *reservation*
(the code releases and re-reserves the same id), and §5.2:556's no-inheritance promise is about stale content
rather than about a concurrent removal, so it is not the negation of §7.1's tree-reuse hazard. ALTERNATIVES:
filing it as a spec-vs-spec contradiction; rejected because the material skeptic has already ruled the
§5.2:556 tension pre-existing ("neither created nor cured by this sentence") in the refuted list, and the
identity claim alone does not flip that.

FACT: `Binder.ReleaseSlot` sends TWO `Shutdown` RPCs on the occupancy-zero concurrent path — the per-slot one,
then `ShutdownRecycle` reusing the just-released session id over the same connection
(slotbinder.go:541-580). Standing-context item 60 is correct; `pkg/adapter/session.go:227-292` handles both in
one handler only because the second call finds no entry. Anyone re-checking the §4.7 no-op sentence should
read `ReleaseSlot`, not just the adapter.

FACT: `connectSlot` reserves the slot and then does only `resolveSandbox`, `DialAdapter` and
`NegotiateVersion` (slotbinder.go:415-477). `NegotiateVersion` is pod-scoped and creates no adapter entry, so
the "bind abandoned at the connect stage owes no reclaim" edge-case bullet is sound.

FACT: every "text to replace" block still matches the tree byte for byte, including the new §29 anchor. Ran
`grep -F` on all eleven; all unique except the §29 one discussed above. The four markdown anchors the new and
old text mint resolve (`### 4.7 Runtime Adapter` spec/04:657, `#### 4.7.9 ...` :848, `### 4.9 ...` :1099).
Do not re-run this sweep unless the spec moves.

DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: unchanged from the r6 list; nothing this
round makes those entries staler or fresher.

### [spec.7.review-kubernetes.7]

FACT: the ONLY change to the spec staging since round 5 is the new SPEC-1 §29.4 step-13 block
(spec-changes.md:196-211 plus the `spec/29_...` line in "Spec files touched"). `diff -q`
between `scratchpad/cp-snap/0081/spec-r6` and the live directory is empty, and r5→current is
that single hunk. Everything else in the document is round-≤5 text that this lens already
cleared. EVIDENCE: scratchpad/cp-snap/0081/spec-r5 vs proposals/0081_.../*.spec-changes.md.

DECISION: the Kubernetes-idiom lens found nothing new — BECAUSE the r5→r7 delta is an intra-pod
CH-RUNTIMEOPS frame condition with no apiserver, CRD, controller, webhook or field-manager
surface at all, and the four idiom checks the r5 entry recorded (ownership, finalizers,
CRD-as-bus, controller-on-the-hot-path) are re-verified unchanged: gateway owns
`SandboxClaim.spec`/`.status`, the unhealthy drain still routes through the
`lenny.dev/drain-request` annotation, `Sandbox.status.phase` is a WPC level-triggered
projection of claim state (spec/04_system-components.md:409), and the leaked-occupancy hold
lives in Redis rather than etcd. ALTERNATIVES: I priced "the §5.2 placement constraint is
stated as a durable property of a pod while `ExcludePod` is request-scoped in memory" and
dropped it — the staged sentence is self-scoped to "a retry that policy places", which is the
within-request retry loop, so the granularity matches.

FACT (the one finding I did file, and it is NOT a Kubernetes finding): §29.4's session-end
trace asserts the runtime close (step 12, spec/29:697) and the `terminate` frame (step 13,
spec/29:704-711) for a trigger set that includes `POST /terminate` and `DELETE` against a
session in `ready`. `ready` is bound-but-unstarted at the adapter (the credential lease is
assigned at §7.1 step 13, `StartSession` is step 14; `assignCredentialsSlot` sets
`st.sessionID` with `st.started` false), and `POST /terminate` is valid in `ready`
(spec/15_external-api-surface.md:649). SPEC-1 narrows the runtime teardown from `bound` to
"a session whose start the adapter has admitted", so both steps become false on that trigger.
Round 6's §29 edit closed only the co-tenancy half of the same step. EVIDENCE:
spec-changes.md:189,:203,:209-211; spec/29:697,:704-711; spec/07:33-35; spec/15:649;
pkg/adapter/slotcreds.go:33-34; pkg/adapter/session.go:239,:243,:259.

WATCHOUT: the proposal's own rationale for leaving §29 step 12 unedited reads "it states that
the adapter closes the session runtime, which runs for every bound entry the call removed".
`bound` is exactly the predicate SPEC-1 replaces, so that sentence is a leftover of the
pre-SPEC-1 contract. A future round reading it will conclude step 12 is safe for a reason the
same deliverable falsifies. EVIDENCE: spec-changes.md:209-211 versus :189.

WATCHOUT: do NOT extend this to §4.7's `ReportSessionScrub` row (spec/04:692) or to SPEC-3's
"abandoned" wording. The standing-context dead end and the round-6 refutation of "SPEC-3's
no-report exception is narrower than the `started` gate" both considered terminate-at-ready
explicitly and judged it inside SPEC-3's "abandoned" clause. The report half is settled; only
the runtime-teardown half (close plus `terminate` frame) is open.

USEFUL [Standing context / Settled — "Ownership is clean"] and [spec.5.review-kubernetes.5]:
between them they carry the whole Kubernetes sweep for this proposal. Re-verifying the
load-bearing halves (spec/04:409 occupancy projection, spec/04:405 webhook reads no phase,
spec/04:515 orphan-claim GC) took minutes instead of a full ownership-table pass.

### [spec.7.review-mechanism.1]

FACT: §29's own preamble makes a stale trace a spec defect by name: "A trace restates behaviour the
specification states elsewhere and cites the section that states it. Where a trace and a cited section
disagree, the cited section is the normative statement and the trace is the defect." That kills the
standing "§29 is only a restatement, so §29 coverage is optional" reading whenever a staged edit changes
a rule a §29 step restates AND cites. EVIDENCE: spec/29_communication-scenarios.md:23-25.

FACT: §29.4's session-end funnel is NOT scoped to a running session. Step 10 admits `POST
/terminate` "valid in any non-terminal state", and §15.1's precondition table lists `created`,
`finalizing`, `ready`, `starting`, ... with the explicit note "For `finalizing` and `ready`, the gateway
aborts the in-progress setup or dequeues the waiting session, releases the pod". So an ordinary
terminate at `ready` is a bound-but-unstarted session end inside §29.4, and every §29.4 step that
asserts a per-session runtime teardown has to survive it. EVIDENCE: spec/29:670-671; spec/15:649.

WATCHOUT: the CURRENT tree makes §29.4 step 12 ("the adapter closes the session runtime") TRUE for a
terminate at `ready`, because the shipped handler gates the whole teardown on `bound := removed &&
st.sessionID != ""` and `AssignCredentials` sets `sessionID` before `StartSession`. SPEC-1's
started-gate is what falsifies it. Do not dismiss the site as pre-existing: it is created by this
proposal. EVIDENCE: pkg/adapter/session.go:238-241,:262-265; pkg/adapter/slotcreds.go:33-34;
pkg/adapter/slotsession.go:87-88.

MISTAKE: the round-6/7 fixer that added the SPEC-1 §29.4 step-13 edit explicitly cleared step 12 on a
retired predicate: "Step 12 is untouched: it states that the adapter closes the session runtime, which
runs for every bound entry the call removed". "every bound entry the call removed" is exactly the gate
SPEC-1 replaces with "a session whose start the adapter has admitted". The conclusion (leave step 12
alone) does not survive the corrected predicate. EVIDENCE: spec-changes.md:209-211 vs :19 and :189.

FACT (checked, do not re-derive): the recycle-disposition `Shutdown` really is a SECOND RPC
(`ShutdownRecycle`) sent after the first `Shutdown` already deregistered the entry, over the same held
connection. §5.2's prose describes it as one request that also "closes the ending session's runtime",
which is a pre-existing spec/code divergence this proposal does not create and does not deepen.
EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:538-582; spec/05:459.

FACT: the §29.4 append anchor string "([§15.4.3](...#1543-runtime-integration-levels), §28.5.3)." occurs
three times in the file (spec/29:645, :711, :982). The staged instruction disambiguates by naming
"§29.4's numbered step 13", so it resolves, but a future anchor sweep should not report it as unique.

DECISION: filed one finding (§29.4 step 12) and nothing else. BECAUSE every other tension I traced was
already on the refuted list or is pre-existing. ALTERNATIVES rejected: (a) filing §4.1's retained "The
per-slot teardown and the whole-pod teardown are the same operation" as vocabulary drift — the same
tension exists pre-edit between sentences 2 and 3, so it is pre-existing; (b) filing the ordering of
§29.4 steps 12/13/14 against SPEC-1's "the signal precedes that close" and "flushes ... then closes" —
§29.4 lists the gateway RPC first and its intra-pod consequences after, and TWO of the orderings invert
under that reading, which shows it is the trace's convention rather than a defect; (c) filing §29.4 step
14 (`FINAL_USAGE_REPORT` moving under the started gate) — step 14 and §8.3 already treat a stream close
as an equivalent terminal signal, so nothing is falsified.

UNVERIFIED: whether the `resuming` failure enumeration's other three bullets (pod crash, 300s watchdog,
non-retryable) need the §7.1 reclaim clause the cancel bullet gains. They state no pod release today, so
there is nothing for the clause to attach to, but §6.2 calls itself the authoritative enumeration of
every edge out of `resuming`. Somebody should decide once. EVIDENCE: spec/06:229-231.

### [spec.7.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens — BECAUSE every
observability surface the staged spec edits could disturb checks out, and the three candidates that
looked live all reduce to already-refuted findings or to pre-existing conditions. ALTERNATIVES:
(1) refiling the "withheld report leaves the pre-`running` `leaked` terminal with no spec-stated route
into the §5.2 ledger" tension — this is the already-refuted `spec.*.review-*` finding under a different
sentence (spec/04:692 "`leaked` outcomes feed the unhealthy-threshold ledger behind the
`lenny.dev/drain-request` annotation" versus SPEC-3's no-report rule and §7.1's "counts it toward the
§5.2 whole-pod replacement trigger"); (2) filing §29.10's "Shared by the whole pod" list for not
gaining the now-pod-global `terminate` frame — pure incompleteness, the list claims no exhaustiveness;
(3) filing `SandboxClaimOrphanRateHigh` (spec/04:521) as newly misleading because the staged `leaked`
disposition leaves a `bound` claim with no active session that §4.6.1 predicate 1 reclaims after
`claimOrphanTimeout` and counts in `lenny_orphaned_claims_total` "indicating potential gateway
instability" — the identical end state already exists for every shipped leaked slot, so it is
frequency rather than a new inconsistency.

FACT: there is no alert anywhere in spec/16 §16.5 on leaked slots or on slot cleanup, so the SPEC-3
report suppression cannot orphan an alert. The §16.5 catalog's slot-adjacent entries are
`WarmPoolExhausted` (keyed on the `lenny_warmpool_idle_pods` gauge, NOT on the client-visible
`WARM_POOL_EXHAUSTED` error code, so the new exclusion route to that error fires no alert) and
`SandboxClaimOrphanRateHigh`. EVIDENCE: spec/16_observability.md:415; spec/04_system-components.md:521.

FACT: the observability inventory entries the change touches all stay true after application, and I
checked each against the staged text rather than assuming. `lenny_slot_failure_total` and
`lenny_slot_pod_replacement_total` are glossed "on session-mode pods with `maxConcurrentSessions > 1`"
and CODE-5's `accountSlotFailure` is called only from the two concurrent bind paths, so the gloss holds
(spec/16:14-15; non-spec-changes.md:427). `lenny_pod_session_reuse_count` is "Observed per-pod at
session end" and `sessions_served` is "incremented at each session release (`ReportSessionScrub`)", both
of which the proposal's framing of a pre-start reclaim as not a session release leaves intact
(spec/16:128; spec/12_storage-architecture.md:481). No staged spec sentence names a metric, an alert,
or a CRD condition, and no CRD status write crosses the §4.6.3 boundary: §7.1's "releases the pod's
claim" is `SandboxClaim` (gateway-owned) and the pod's `failed` marking is delegated to §6.2's
disposition rather than asserted of the gateway.

FACT: the `terminate` frame carries `{type, deadlineMs, reason}` and no session identifier, so SPEC-1's
"the signal is pod-global and names no session" is exactly what §28.5.3's message-schema table already
says. Nothing in §28's CH-RUNTIMEOPS card, §15.4.2, or §15.4.3 states the frame is emitted per session
end unconditionally, so §29.4 step 13 was the only mirror needing the co-tenancy condition and SPEC-1
stages it. EVIDENCE: spec/28_communication-channels.md:1082; spec/29_communication-scenarios.md:704-711.

CORRECTS [standing context, "Open: `terminate` frame reason value"]: that entry says "the compensating
`Shutdown` on a bound-but-unstarted exclusive-pod slot fires the §15.4.2 `terminate` frame, whose
`reason` is a closed four-value enum with no value for an abandoned bind". Under the design as it now
stands that frame is not sent at all on that path. The staged §4.7 row puts the signal inside the
runtime teardown ("The [§15.4.2] graceful-shutdown signal precedes that close"), the runtime teardown
runs "only for a session whose start the adapter has admitted", and CODE-1 moves the drain inside the
`started` block (review-log.md "Tier-7a drain-gate assertion"). A bound-but-unstarted reclaim therefore
runs no runtime teardown, no close, and no frame, so the enum question does not arise for it. The
`reason`-enum question survives only for a reclaim of a session whose start WAS admitted. Whoever owns
that OPEN should re-scope it before spending a round on the enum.

FACT: every verbatim "text to replace" block in the current staging matches the tree exactly once,
including the seven anchors the standing context's earlier anchor sweep did NOT cover (§7.1's
parenthetical, §7.2's preamble/step-2/step-3, §7.3's list tail, §6.2's `resuming → cancelled` clause,
§5.2's `**Max retries:**` sentence, §5.2's `**Scrub model.**` paragraph, §4.7.9 step 5, and §29.4
step 13's tail). Verified with `grep -F -c`. The §29.4 tail string
"([§15.4.3](...), §28.5.3)." occurs at three lines (645, 711, 982) but only 711 is inside §29.4 step 13,
which the instruction names, so the ambiguity is not an anchor defect. Do not re-run this sweep unless
the spec moves.

WATCHOUT: `diff -ru scratchpad/cp-snap/0081/spec-r7 proposals/0081_.../` returned nothing this round,
so the "read the changed sections hardest" instruction had no changed sections to point at. Round 6
produced no edits. A future reviewer should read that as evidence of convergence rather than assume the
snapshot is stale.

### [spec.7.review-performance.1]

DECISION: returned an empty findings list under the performance / scalability /
failure-mode-reliability lens — BECAUSE the proposal creates no net-new control-plane write
(no etcd status write, no new Redis key, no new Postgres write, no new watch or informer),
adds one adapter `Shutdown` per FAILED bind rather than per session, and its only structural
change to write volume is in the *reducing* direction (a `leaked` release skips the Redis
decrement and the claim DELETE). The reliability surface that remains is the `leaked`
disposition, which has now been filed and refuted twice by the material skeptic and is
recorded in Standing context as a deliberate decision — ALTERNATIVES: I worked up and
discarded four candidates, each listed below with why, so a later performance reviewer does
not spend the round re-deriving them.

FACT: the whole-pod replacement trigger is `(maxConcurrent+1)/2` over a 5-minute rolling
window for `failed` slots plus a persistent (never-ageing) count for `leaked` slots, and both
counters are in-process maps on ONE gateway replica. EVIDENCE:
pkg/gateway/runtime/slothealth/slothealth.go:56-67,:214-220;
pkg/gateway/sessionserver/start.go:2833-2872 (`health.RecordFailure` / `health.RecordLeak` /
`health.Unhealthy` / `DrainSandbox` / `slots.ForgetPod`). At `maxConcurrentSessions: 2` the
threshold is 1, so a SINGLE transient slot-bind failure drains the pod. This is shipped and
unchanged by 0081, but it is the number any future perf finding on this proposal has to
reason against.

FACT: `applySlotRetryPolicy` wraps `binder.BindSlot` ONLY. `BindReservedSlot` (the §15.1 start
onto a create-time-reserved slot) and `ClaimSlot` are called directly and never traverse the
retry policy. EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884 (loop body is
`binder.BindSlot`), :2596 (`s.podBinder.BindReservedSlot(...)` direct), :2148
(`s.podBinder.ClaimSlot(...)` direct). This confirms the staged §5.2 sentence's scoping claim
("it reaches the retries this policy places and no others") — I verified it rather than
trusting it, and it holds.

FACT: a concurrent-workspace pool has NO `ready` gap. `prepareAtFinalize` returns nil for
`match.MaxConcurrentSessions > 1`, so the slot is materialised and launched together at
/start. EVIDENCE: pkg/gateway/sessionserver/finalize.go:236-239. Consequence: the
"bound-but-never-started teardown" class (READY_TIMEOUT, terminate-at-ready) can only occur on
an EXCLUSIVE pod, where spec/05:455 retires the pod on a failed session anyway. That is what
kills the otherwise-attractive finding that CODE-1's report gate loses a `sessions_served`
increment (and therefore weakens the `recycle.maxSessionsPerPod` residual-state bound) for
those teardowns: the bound it would weaken does not govern the only pods that can reach the
state.

WATCHOUT: do not file "the `leaked` disposition holds occupancy / suppresses the retirement
today's unconditional `leaked=false` release performs". Two separate framings of it have been
refuted (occupancy-hold-vs-immediate-retirement, and the acknowledged-but-unclean predicate
width), and Standing context records the disposition as decided. A third framing built on
false-leak amplification (a transport blip during the first bind RPC leaks a slot on a pod
that holds nothing, and leaks never age out) is the same finding with new arithmetic and will
be refuted on the shipped-consistency argument: `Binder.ReleaseSlot` already sets
`leaked = err != nil || !cleanly` for the session-end path. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:536-543.

DEFERRED [proposals/0081_.../0081_....spec-changes.md, edge-case bullet one, line 98-102]: the
bullet's closing clause "so the failure is accounted transient and a blob-store outage does
not retire healthy pods" is false as stated, and I deliberately did NOT file it because its
remedy is a qualification of rationale narration rather than of any staged spec line. What is
true instead: the clean-exit answer keeps the failure OUT OF the persistent leak count, which
is all the no-op sentence buys. Transient accounting still retires pods — `RecordFailure` plus
`Unhealthy` drains at `ceil(maxConcurrentSessions/2)` windowed failures, which is ONE failure
at `maxConcurrentSessions: 2`, and a blob-store outage produces exactly the correlated stream
that reaches the threshold inside one 5-minute window at any concurrency. The honest sentence
is "so the failure is accounted transient rather than leaked, and a blob-store outage does not
add to the pod's persistent leak count". EVIDENCE for the falsity:
pkg/gateway/sessionserver/start.go:2856-2872; slothealth.go:214-220. EVIDENCE for the claim:
spec-changes.md:98-102, and the same wording in non-spec-changes.md:350-353.

UNVERIFIED: whether CODE-1 moving `drainViaLifecycle` from under `if bound` to under
`if started` (non-spec-changes.md:104-113 versus shipped pkg/adapter/session.go:238-262) loses
a graceful §15.4.2 DRAINING signal that anything depends on for a bound-but-unstarted session.
I convinced myself it does not matter — the only pods that can reach that state are exclusive
pods, which spec/05:455 retires anyway, so the runtime takes pod-deletion SIGTERM instead of a
drain frame, and a session that never started has nothing to flush — but I did not read
§15.4.2 to confirm the DRAINING frame carries no other obligation. A security or
runtime-contract lens should close it rather than a performance one.

USEFUL [Standing context, "The churn change starts at concurrency 3"]: this entry saved me
from filing the pod-churn amplification finding. It is correct and I re-derived its arithmetic
independently from slothealth.go:214-220 and start.go:2856-2872.

USEFUL [Standing context, "`SlotClaimer.ReleaseSlot(leaked=true)` returns early"]: this is the
fact that makes "releases the slot reservation" and "the leaked disposition holds occupancy"
compatible, and without it the SPEC-2 paragraph reads as self-contradictory on first pass. It
is the single highest-value entry in the ledger for this lens.

### [spec.7.review-reliability.1]

DECISION: returned an empty findings list for the reliability lens on round 7 — BECAUSE every
recovery/retry mechanism the staged edits add traces cleanly through crash, restart and
failover, and the remaining rough edges are all either pre-existing, already refuted, or
already recorded as OPEN/DEFERRED by earlier rounds — ALTERNATIVES: I seriously weighed and
rejected five candidates, listed below, so a later round does not re-derive them.

FACT: round 6 changed nothing in the proposal. `diff -rq scratchpad/cp-snap/0081/spec-r7
proposals/0081_.../` is byte-identical across all nine files, so the "read the changed
sections hardest" instruction had no target this round and the whole document is equally
aged. EVIDENCE: scratchpad/cp-snap/0081/spec-r7 vs the proposal directory.

FACT: the racing-start residue is bounded on every pool topology, which is what makes the
accepted-failure-mode bullet honest. On a recycling concurrent pool the abandoned started
session reports no scrub, but later legitimate sessions still advance `sessions_served`, and
spec/05:488 drains the pod "on the session release that drives the served-session count to
`maxSessionsPerPod`, decoupled from the whole-pod scrub because a persistently `leaked` slot
can hold total occupancy above zero indefinitely". On a NON-recycling concurrent pool the
gateway's own counter decremented at the acknowledged reclaim, so occupancy reaches zero, the
claim is deleted and the pod drains (spec/06:143-144). EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488;
spec/06_warm-pod-model.md:143-144.

WATCHOUT: the compensation's deadline is NOT an unbounded outbound RPC. `slotCleanupBudget`
wraps `context.WithoutCancel(ctx)` in a `WithTimeout` of `max(cleanupTimeoutSeconds /
maxConcurrentSessions, 5)`, so an unset `cleanupTimeoutSeconds` still yields a 5s floor. Do
not file "the reclaim has no deadline": the staged Design paragraph names the figure and the
code lane implements it. EVIDENCE: non-spec-changes.md:288-321; spec/05:545.

WATCHOUT: `ReclaimClaimed` sends the adapter no `Shutdown` — it only deletes the per-pod
`SandboxClaim` and revokes the §4.9 lease. So the created-expiry / terminate-at-`ready`
reclaimer leaves the adapter's slot entry untouched on a concurrent pod. Before filing that
as a hole SPEC-3's "a bind abandoned or fails" leaves untriggered, note that "abandoned" in
this proposal means the GATEWAY abandoned the bind by failing it, not client abandonment: the
problem statement uses it that way at :72 ("a session the gateway has abandoned") and names
the client-abandonment class out of scope at :134 and :453. EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:1097-1107; pkg/gateway/sessionserver/usage.go:581-596;
problem-statement.md:72,:134,:453.

UNVERIFIED (weighed, not filed): the staged §7.1 bound on an unacknowledged reclaim rests on
two records that are BOTH ephemeral and, for this leak class alone, have no durable
reconstruction source. The occupancy hold is the un-decremented Redis counter, and spec/05's
"Post-recovery rehydration atomicity" rebuilds that counter from
`SessionStore.GetActiveSlotsByPod` after a Redis restart; a failed bind never persists a
session row (spec/07:23 "does NOT persist the session row"), so the leaked occupancy is
unrecoverable, and §6.2's promise that a leaked slot prevents "the gateway from over-assigning
new slots that would conflict with the leaked slot's unreleased resources" lapses. The
threshold count is `slothealth.Tracker`, an in-process replica-local map. I did not file it
because the identical gap already holds for every shipped leaked slot (an ordinary leaked
slot's session row is terminal, so rehydration loses it too) and `spec.1.review-reliability.1`
already judged it pre-existing and left it OPEN. Whoever adjudicates that OPEN should note the
one thing that IS distinctive: for a failed-bind leak the adapter never learns the slot is
leaked, so unlike an ordinary leak there is no adapter-side `leaked_slots` health count to
re-derive it from. EVIDENCE: spec/05_runtime-registry-and-pool-model.md "Post-recovery
rehydration atomicity" paragraph; spec/06_warm-pod-model.md:160; spec/07_session-lifecycle.md:23.

UNVERIFIED (weighed, not filed): on the acknowledged-reclaim racing-start ordering the gateway
releases the reservation with `leaked=false`, decrementing the counter for a slot a live
session still occupies, so the pod can transiently carry `maxConcurrentSessions + 1` sessions —
the exact over-assignment §5.2's "Slot assignment atomicity" exists to prevent. The proposal's
racing-start bullet (spec-changes.md:133-142) records the residue but not this consequence. Not
filed: the fix is one more sentence in an accepted-failure-mode bullet, and the round-6 skeptic
refuted a structurally identical request ("the racing-start residue ... has its outcome in no
staged spec text") as additive prose about a deliberately-unclosed mode.

UNVERIFIED (weighed, not filed): §7.2's close sequence sends the reclaim at step 3 and bumps
`coordination_generation` at step 4, so a stale coordinator's in-flight start can land after
the destructive removal and before the fence. I did not file it because step 1 ("Cancel the
in-flight restoration RPCs") already presumes the sequence's replica owns those RPCs, so the
cross-replica case is the pre-existing stale-coordinator case step 4 exists for, and the
ordering (release-pod-then-bump) predates this proposal. EVIDENCE: spec/07_session-lifecycle.md:212-216.

MISTAKE (mine, avoided): I nearly filed "the reclaim is sent on the connection the failed stage
still holds, so a transport-failure class has no connection to send on". It is covered: such a
reclaim is one "the adapter does not acknowledge" and takes the staged `leaked` disposition.
EVIDENCE: spec-changes.md:241.

### [spec.7.review-security.1]

DECISION: returned an empty findings list for the security lens on the round-7 staging — BECAUSE every security-shaped candidate I derived either (a) reduces to a class the material skeptic already refuted, (b) is pre-existing and recorded in Standing context, or (c) is "less strict than it could be", which the lens bar excludes. ALTERNATIVES: I came closest to filing the `recycle.maxSessionsPerPod` residual-state relaxation (below) and rejected it on the evidence.

FACT: the only spec-changes delta since the r5 snapshot is the new `SPEC-1 · spec/29 §29.4 step 13` block plus its `Spec files touched` entry. `diff -q` shows spec-changes.md identical between spec-r6, spec-r7 and the live proposal, so round 6's fixers changed nothing in this file. EVIDENCE: scratchpad/cp-snap/0081/spec-r5/...spec-changes.md vs proposals/0081.../...spec-changes.md
FACT: the §29.4 step-13 anchor is real and the append lands cleanly. Step 13 ends `([§15.4.3](...), §28.5.3).` and already carries the Basic/Standard "this step does not occur" exception the new sentence copies. Step 12 (the `Shutdown` step) is scoped to a session end from terminate/DELETE/expiry, where the preconditions guarantee the runtime is running, so leaving it unedited is sound even though the proposal's rationale words it as "every bound entry the call removed" (which SPEC-1 narrows to `started`). EVIDENCE: spec/29_communication-scenarios.md:693-711, :586-591
FACT: `#49-credential-leasing-service` (minted by SPEC-3's first anchor, not covered by the Standing-context anchor sweep) resolves — `### 4.9 Credential Leasing Service` at spec/04_system-components.md:1099. So does `#73-retry-and-resume` (spec/07:378). EVIDENCE: spec/04_system-components.md:1099; spec/07_session-lifecycle.md:378
FACT: the two actions SPEC-3 adds to §5.2's action list are shipped and correctly attributed. `slotlayout.RemoveTree` removes `slotRoot`, `Sessions`, `Artifacts` and `CredentialsDir`; `deregisterSlotLocked` cancels every armed per-provider expiry timer under `s.mu` before deleting the entry. The credential path `/run/lenny/slots/{sessionId}/credentials.json` in the staged sentence matches spec/13:26,:30, spec/04:914, spec/06:26 and spec/05:455,:461,:471 verbatim. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68; pkg/adapter/slotsession.go:174-188
FACT: the adapter's `Resume` DOES set `st.started` — `resume.go:50` calls `claimSessionSlot`, whose critical section sets `st.sessionID` and `st.started` before `Runtime.Start` at `resume.go:140`. So SPEC-1's generalised precondition ("the adapter's admission of that RPC", covering new and resumed starts) has a code counterpart on both paths and opens no fail-open window where a live runtime survives a `Shutdown`. EVIDENCE: pkg/adapter/resume.go:50,:140-144; pkg/adapter/slotsession.go:86-89
FACT: §11.4 full revoke is NOT regressed by the teardown split. Its step 3 ("the pod's runtime adapter initiates graceful shutdown") addresses "all active sessions", which are `started` by construction, and the slot release (which SPEC-1 widens to any entry) still removes the credential directory for a bound-but-unstarted session. EVIDENCE: spec/11_policy-and-controls.md:250-270; pkg/adapter/session.go:238-241,:271

WATCHOUT: `SlotClaimer.ReleaseSlot(leaked=true)` early-returns before the claim DELETE, so "the pod retires" is FALSE for any release that passes leaked. The staged §7.1 exclusive-pod sentence survives this only because the exclusive path never reaches `ReleaseSlot` at all — `match.MaxConcurrentSessions > 1` gates every slot route in start.go, and an exclusive failure goes through `failPhase`'s claim DELETE. Do not "simplify" the exclusive sentence into the leaked disposition. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-843,:882-885; pkg/gateway/sessionserver/start.go:2139,:2351,:2465

MISTAKE (nearly filed, do not re-derive): "SPEC-3 relaxes the `recycle.maxSessionsPerPod` residual-state bound." §5.2 frames that field as the deployer's residual-state cap ("the deployer must make an explicit choice based on the workload's sensitivity and the residual state vectors enumerated above", spec/05:488) and it is advanced only by `ReportSessionScrub`, which SPEC-3 withholds for every pre-`running` reclaim. It does not clear the bar: the class that regresses is only a `Shutdown` landing while `st.sessionID != ""` and `runtimeLive == false`, i.e. the transient window between `AssignCredentials`/`claimSessionSlot` and `noteRuntimeStarted`. Today's failed-bind path sends no `Shutdown` at all, so the counter does not advance there either and the change adds no relaxation on the path it exists to fix. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:404,:488; spec/12_storage-architecture.md:481,:494; pkg/adapter/session.go:279
MISTAKE (nearly filed): "the §7.2 step-3 rewrite keeps 'no scrub beyond the pool's default post-session scrub' after deleting the premise that justified it, so an aborted re-attach can return a pod holding a live runtime and a credential file to the pool." §7.1's two dispositions do close it: on a concurrent pod the unacknowledged reclaim leaves the slot `leaked`, whose held occupancy keeps the pod out of idle inventory, and on an exclusive pod the pod retires. Tenant pinning (spec/05:442) also keeps a concurrent or recycling pod on one tenant for its lifetime, so the cross-tenant version of the exposure does not arise.

UNVERIFIED: whether `maxSessionsPerPod`'s residual-state framing SHOULD count a bind that reached `RunSetup` (§4.7.9 step 5 runs workspace-plan setup commands on the pod before `AssignCredentials`, so a bind abandoned at `ready` has executed code that primes exactly the DNS/TIME_WAIT/page-cache vectors §5.2 enumerates) while never advancing the counter. Neither today's tree nor the staged spec counts it, so this is a pre-existing question rather than a defect of 0081. A human or a later proposal should decide; do not file it against this proposal.

USEFUL [Standing context "The anchor sweep is done"]: saved a full re-verification pass. I spot-checked five anchors (spec/04:157, :686, :853-854; spec/05:453, :545) plus the two anchors the sweep predates (`#49-credential-leasing-service`, §29.4 step 13) and all matched byte for byte.
USEFUL [Standing context #50 and #91]: the pairing of "today's `ReleaseSlotReservation` hard-codes `leaked=false`" with "do not read the reclaim as scoped away from pods that retire" is what let me price the exclusive-vs-concurrent disposition question in one read instead of three.

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
