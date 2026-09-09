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

### [spec-recheck.1.fix-G1.1]

DECISION: the `leaked` disposition carries exactly ONE qualifier in §5.2's `**Scrub model.**` append, and that qualifier is concurrency — BECAUSE the three findings in this group all pointed at the same sentence and the temptation was to hang a concurrency qualifier, a start-state qualifier and a bind-path qualifier on it. The concurrency arm stays in the spec (§6.2 offers `leaked` only under concurrent occupancy and §5.2's trigger sits inside a `maxConcurrentSessions > 1` heading), and the other two are made true in the code lane instead — ALTERNATIVES: widening §6.2's fence so `leaked` holds at concurrency 1 (barred by a standing trap: shipped state-machine semantics, docs/reference/state-machines.md:251, the tier-11 matched-pair test); moving the whole append into the `**Slot cleanup:**` bullet so the heading supplies the scope (barred by a standing trap and it separates the withheld-report rule from the accounting rule that qualifies it).

DECISION: CODE-1's `ShutdownResponse` gate moves from `started` to the `live` it already computes — BECAUSE `st.started` is set in `claimSessionSlotUnderLock` before `Runtime.Start`, so a start still in flight is `started` and pre-`running`, and SPEC-4's own prose puts that case in `receiving_uploads`; gating the response on `started` therefore discarded the tree-removal error for exactly the reclaim §7.1 sends on a cancelled context. `live` is already in scope at the response site, so this adds no state, no accessor and no branch — ALTERNATIVES: narrowing the §5.2 sentence to "a session whose start the adapter had not admitted" and recording the start-in-flight case as a residual (writes a hole into the spec on the interleaving the mechanism exists for); making the gate uniform `closeErr == nil && treeErr == nil` (rejected only because it reclassifies ordinary session-end slots as `leaked`, a §6.2 accounting change this proposal declines — worth its own proposal).

FACT: `started && !live` is unreachable at an ordinary session end, which is what makes the `live` gate safe for the session-end classification. Every path that admits a start reaches `noteRuntimeStarted` unconditionally on success, and the two `noteRuntimeClosed` callers outside `Shutdown` each remove the registry entry in the same pass, so a later `Shutdown` for that session has `removed == false` and both predicates are false. EVIDENCE: pkg/adapter/session.go:156-163; pkg/adapter/resume.go:140-144; pkg/adapter/sdkwarm.go:250-261 (the `fresh` arm; a non-fresh idempotent repeat returns early from claimSessionSlotUnderLock at pkg/adapter/slotsession.go:80-84 with `live` already true from the fresh call); pkg/adapter/holdstate.go:208-253 ("pass 1 deregistered"); pkg/adapter/sdkwarm.go:296-298.

DECISION: the §7.3 re-attach's leak accounting is closed in the code lane with a THIRD `accountSlotFailure` caller rather than by scoping the §5.2 sentence to the paths the gateway's accounting reaches — BECAUSE the proposal's own ground for CODE-5 existing is that §5.2's trigger obliges counting every failed or leaked slot "with no carve-out by code path" (summary.md, the conformance-gap bullet), and scoping the sentence down would falsify three untouched shipped sentences (spec/05:545, spec/06:160, docs/reference/state-machines.md:251) and ship a known unbounded capacity leak — ALTERNATIVES: option (b), the carve-out plus an accepted-failure bullet; accounting only the leaked arm on the re-attach (trades a carve-out by code path for one by disposition; it is the fallback if a human rejects the churn change at `maxConcurrentSessions: 2`); injecting the slot-health tracker into `pkg/gateway/podlifecycle/podsession` (duplicates a concern that lives in `pkg/gateway/sessionserver` and inverts the release/account layering).

FACT: `accountSlotFailure`'s body reads only `req.Pool` and `req.MaxConcurrentSessions` off its request, which is why the staged signature now takes `pool string, maxConcurrentSessions int32`. The resume caller holds a `podsession.ResumeRequest` and would otherwise have had to synthesise a bind request. EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884 (the tail being extracted).

FACT: `Binder.Resume`'s failure branch today returns `fmt.Errorf("podsession: resume session on pod %s: %w", sb.Name, err)` after `cl.Close()` and `releaseResumeSlot`, and `resumeOnPod` returns that error unexamined. `SlotBindError.Unwrap` returns the cause, so wrapping a `*SlotBindError` into that chain leaves `isTransientPodClaimError`'s `errors.As`/`errors.Is` classification of a resume failure unchanged. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1620-1630; pkg/gateway/podlifecycle/podsession/slotfailure.go:74; pkg/gateway/sessionserver/start.go:4041-4043,:3648-3682.

FACT: `reserveResumeSlot` returns an empty slot id on an exclusive pool and `releaseResumeSlot` no-ops on one, so the non-empty-slot-id guard on the new accounting call is exactly `MaxConcurrentSessions > 1`. That is the same concurrency scope the §5.2 sentence now carries, which is why the two lanes state one rule. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go, `releaseResumeSlot`'s doc comment and its `if slotID == ""` guard.

WATCHOUT: do NOT add a bind-path qualifier to the §5.2 leak-accounting sentence. It is the obvious local fix for the re-attach gap and it installs precisely the carve-out by code path the proposal's own conformance-gap bullet says §5.2's trigger forbids. The path question is answered in the code lane. EVIDENCE: proposals/0081_.../0081_....summary.md, the `bindConcurrentSlot`'s-reserved-branch bullet under the recorded limits.

WATCHOUT: do NOT "simplify" CODE-1 back to one predicate now that the report gate and the response gate are both `live`. The RUNTIME TEARDOWN still gates on `st.started` and must: CODE-2's rollback fires only when `Runtime.Start` returns, so a start that hangs under a `runtimeLive` teardown gate would leave the runtime serving an abandoned session forever. Three predicates, two of which are the same one, is the correct shape. EVIDENCE: pkg/adapter/session.go:155-163; the standing trap "Do not gate the runtime teardown on `runtimeLive` either".

MISTAKE: this round's own earlier fix stated the leak accounting unscoped in §5.2's concurrency-independent paragraph, which is the third occurrence of the same defect class on this proposal ("the `leaked` terminal cited from either-concurrency text but landing in `maxConcurrentSessions > 1`-scoped blocks"). Cost: one round and three findings. The structural cause is that the scrub-model paragraph is deliberately concurrency-independent, so ANY sentence appended there that names `leaked` or the whole-pod replacement trigger needs its own scope. Check every future append to that paragraph against it.

CORRECTS [f1.open-decisions.human-decisions.1]: its FACT "a failed pre-`running` cleanup still reaches the §5.2 whole-pod replacement trigger and the leaked-slot gauge, through the `Shutdown` response" was false in two sub-ranges when written, and the formula it quotes is superseded. It is true after this round: the response gate is now `closeErr == nil && (live || treeErr == nil)`, which closes the start-in-flight hole, and `accountSlotFailure` gains a `resumeOnPod` caller, which closes the §7.3 re-attach hole. The FACT's quoted formula `(started || treeErr == nil)` should be updated to the `live` form wherever compaction carries it forward.

CORRECTS [standing context, Open: "Does the resume compensation feed the slothealth ledger?"]: answered and closed. It did not, and CODE-5 now gives `accountSlotFailure` a third caller on `resumeOnPod`'s `podBinder.Resume` failure branch so it does. The related Open "Concurrent resume leaves a freshly claimed pod at occupancy 1" is narrowed rather than closed: the pod is now counted toward the §5.2 threshold and drained, so the occupancy hold is bounded by the replacement trigger instead of being permanent.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S10 (line 24) says "One `accountSlotFailure` helper serves both concurrent bind paths, the create-time reserved path reaches the §5.2 threshold". That is now incomplete. What is true: the helper serves every bind path the §7.1 obligation binds — `applySlotRetryPolicy`, `bindConcurrentSlot`'s reserved branch, and `resumeOnPod`'s `podBinder.Resume` failure branch — so the create-time reserved path AND the §7.3 checkpoint-restore re-attach reach the §5.2 threshold. The helper's request-derived parameters also narrow from `req podsession.SlotBindRequest` to `pool string, maxConcurrentSessions int32`, which changes both existing call sites.

DEFERRED [proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S9 (line 22) describes CODE-4 as carrying the outcome "as the `leaked` disposition through `ReleaseSlotReservation`". Still true and now incomplete: `Binder.Resume`'s failure branch also returns its error with a `*SlotBindError` in the chain (pod, reserved slot id, stage `resume`, and the compensation's `Leaked`), wrapped inside today's message, which is what carries the re-attach's disposition to S10's third caller. An implementor landing S9 from the checklist alone would leave S10 with nothing to read.

OPEN: whether the churn change the third caller introduces is acceptable. On the §7.3 re-attach the accounting is reached for the first time at every concurrency, so at `maxConcurrentSessions: 2` a single clean restore failure now drains the replacement pod. If a human rejects that, the recorded fallback is to account only the leaked arm on the re-attach, stated as a scope in the helper's doc comment rather than left implicit.

UNVERIFIED: whether `pkg/gateway/sessionserver`'s existing test fakes let a tier-1 case drive `podBinder.Resume` to a failure carrying a `*SlotBindError`. The staged resume accounting case assumes they do or that the deliverable extends them; nobody read `start_test.go`'s resume fixture. Whoever owns the non-spec loop should check before the case is written.


### [spec-recheck.1.fix-design-G1.1]

DECISION: one rule, stated once with its concurrency scope, and the code lane made to carry it on every bind path §7.1 binds — BECAUSE all three G1 findings attack one sentence (spec-changes.md:465) and the only rewrite that satisfies all three is (a) scope the `leaked`/trigger/gauge half to a pod serving concurrent sessions and point the exclusive case at §7.1, (b) leave the clean-exit predicate half unqualified and fix CODE-1 so it is true across the whole pre-`running` range, and (c) close the §7.3 re-attach accounting gap in code rather than carving the spec sentence down to the paths the code happens to reach — ALTERNATIVES: scoping the §5.2 sentence to "the creation and start paths" and recording the re-attach leak as an accepted failure mode (rejected: summary.md:391 already states that §5.2's trigger "obliges the gateway to count every failed or leaked slot on a pod with no carve-out by code path", so a path carve-out contradicts the proposal's own ground for CODE-5, and it would force a carve-out into three shipped unqualified sentences at spec/05:545, spec/06:160 and docs/reference/state-machines.md:251); moving §7.1 and edge-case bullet four to say the `leaked` disposition DOES apply at concurrency 1 (rejected: it re-opens the §6.2 fence scoping the review log bars at Traps "Do not widen §6.2's fence", and contradicts standing DECISION at Settled 43).

DECISION: CODE-1's response gate becomes `ExitedCleanly: closeErr == nil && (live || treeErr == nil)`, replacing `started` with the `live` the same critical section already computes — BECAUSE `live` IS the §6.2 `running` boundary the whole proposal installs, so the rule stops being "two meanings of `exited_cleanly`" and becomes one rule keyed on one boundary: at or past `running` the response carries the runtime close (shipped behaviour, unchanged), before `running` it carries the slot release. It costs one token and no new state — ALTERNATIVES: narrowing the §5.2 sentence to "a session whose start the adapter had not admitted" (rejected: it writes a hole into the spec on exactly the interleaving §7.1's reclaim exists for, per staged §7.1's "the case in which the adapter may have started the session"); making the gate uniform `closeErr == nil && treeErr == nil` (rejected here, but it is the smallest rule and it would make §5.2:545's shipped "If cleanup fails, the slot is leaked" true for the first time — it reclassifies ordinary session-end slots as leaked, which non-spec-changes.md:157-162 declines as a §6.2 accounting change).

FACT: the `live` widening is safe for the ordinary session end, and this is what makes the whole predicate fix cheap. Every path that admits a start also reaches `noteRuntimeStarted` unconditionally, so `runtimeLive` holds for every session that reaches an ordinary session-end `Shutdown`: StartSession at pkg/adapter/session.go:163, Resume at pkg/adapter/resume.go:144, SDK-warm at pkg/adapter/sdkwarm.go:261 (guarded on `fresh`, but the first, fresh call already recorded membership and a non-fresh ConfigureWorkspace is a repeat for a session already in the set). The only two `noteRuntimeClosed` callers outside `Shutdown`, pkg/adapter/holdstate.go:251 and pkg/adapter/sdkwarm.go:297, both deregister the slot in the same pass, so a later `Shutdown` finds `removed == false` and computes neither `started` nor `treeErr`. There is therefore no path on which `started && !live` holds at a genuine session end. EVIDENCE: pkg/adapter/session.go:156-163; pkg/adapter/resume.go:140-144; pkg/adapter/sdkwarm.go:217,:261,:296-298; pkg/adapter/holdstate.go:228-252.

FACT: the §7.3 accounting gap is narrower than "the resume path". `resumeOnPod` has two branches. The snapshotless resume-rebuild branch calls `startOnPod` → `bindConcurrentSlot` → `bindSlotWithRetry` → `applySlotRetryPolicy`, so it already reaches the accounting. Only the checkpoint-restore branch, `s.podBinder.Resume` at pkg/gateway/sessionserver/start.go:4005 with `if err != nil { return "", err }` at :4041, reaches none. Say "the checkpoint-restore re-attach", not "the resume path". EVIDENCE: pkg/gateway/sessionserver/start.go:3943-4042.

FACT: `resumeOnPod` already holds every collaborator `accountSlotFailure` needs, so the fix needs no plumbing. `s.slotHealth`, `s.slotStates`, `s.slotReplacement`, `s.slotLeakGauge` are Server fields (start.go:2736) and `match.Pool` plus `maxConcurrentSessions(match.MaxConcurrentSessions)` are already computed at the `podBinder.Resume` call site. `applySlotRetryPolicy`'s tail reads only `req.Pool` and `req.MaxConcurrentSessions` off its request, so `accountSlotFailure` should take those two values rather than a whole `podsession.SlotBindRequest`; building a synthetic `SlotBindRequest` on the resume path to satisfy the staged signature is the hair to avoid. EVIDENCE: pkg/gateway/sessionserver/start.go:2736,:2844-2872,:4005-4041.

FACT: `*podsession.SlotBindError` unwraps to its cause (pkg/gateway/podlifecycle/podsession/slotfailure.go:74), so wrapping `Binder.Resume`'s adapter-RPC failure in one is transparent to `isTransientPodClaimError`, which classifies purely by `errors.As`/`errors.Is` on `PoolWarmingError`, `CredentialAssignmentError`, `SetupCommandFailure` and the podclaim sentinels. Keep the existing outer message by wrapping the SlotBindError inside today's `fmt.Errorf("podsession: resume session on pod %s: %w", ...)` rather than replacing it. EVIDENCE: pkg/gateway/sessionserver/start.go:3648-3680; slotfailure.go:59-74.

WATCHOUT: the resume-path accounting must be gated on a slot actually having been reserved. `reserveResumeSlot` returns `""` on an exclusive pool and `releaseResumeSlot` no-ops on an empty slot id, so an unguarded `accountSlotFailure` on that path would `RecordLeak` a pod at `UnhealthyThreshold(1) == 1` and drain a pod that nothing drains today, on a slotless pool. Gate on `slotID != ""`, which is exactly `MaxConcurrentSessions > 1` and exactly the concurrency scope the §5.2 sentence now carries. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1600-1605,:1707-1717.

WATCHOUT: the tempting local edit on finding 2 is to scope the §5.2 clause to the paths the code reaches. Do not. summary.md:389-392 makes "no carve-out by code path" the stated ground for CODE-5's existence, and the same edit would falsify three untouched shipped sentences (spec/05:545, spec/06:160, docs/reference/state-machines.md:251) that state the leaked→threshold rule unconditionally.

MISTAKE: this round reintroduced, in new text, the defect the loop already fixed twice. spec-changes.md:465 asserts the `leaked` sub-state and the whole-pod replacement trigger inside §5.2's concurrency-independent `**Scrub model.**` paragraph, while staged §7.1 (:262) and edge-case bullet four (:130-134) both say those are stated for concurrent occupancy alone. The standing decision at review-log Settled 43 already settled it the other way. The cost is a third fix of the same class; the structural cause is that the scoping lives in three sentences in two files instead of one.

OPEN: does §7.1's exclusive-pod sentence ("the failed attempt releases the pod's claim and the pod retires") hold on the checkpoint-restore re-attach? `Binder.Resume`'s failure branch releases nothing on an exclusive pool and `holdOrFailOnResumeError` touches only the session row, so the replacement pod stays `claimed` until §4.6.1 orphan GC. Already recorded as review-log Open "Does CODE-4 owe a pod release on the resume path?"; this design does not close it and deliberately does not pull it in.

UNVERIFIED: the churn consequence of the third `accountSlotFailure` caller. Passing the real disposition (both arms, as the other two callers do) means a cleanly-reclaimed checkpoint-restore failure now takes `RecordFailure`, so at `maxConcurrentSessions: 2` the first failed restore drains the freshly claimed replacement pod. Chosen for consistency with summary.md:391; nobody has priced it against a resume storm. The alternative, accounting only the leaked arm, is a carve-out by disposition rather than by code path and was rejected on that ground.

CORRECTS [f1.open-decisions.human-decisions.1]: the FACT at review-log.md:2984 ("a failed pre-`running` cleanup still reaches the §5.2 whole-pod replacement trigger and the leaked-slot gauge") is stated as fact but was false in two places under the formula it quotes. It was false for a start still in flight, because `started || treeErr == nil` discards `treeErr` there, and false for the §7.3 checkpoint-restore re-attach, which reaches neither `accountSlotFailure` caller. Under this design both holes are closed in code (`live` gate; a third `accountSlotFailure` caller on `resumeOnPod`), so the FACT becomes true, but the formula it quotes is superseded.

USEFUL [Settled 43]: "DECISION: the `leaked` disposition is scoped to the pod class that offers it" is what made finding 0 a two-minute adjudication instead of a re-derivation of the whole §6.2 fence argument. USEFUL [Settled 80] "the slot routes are concurrency-gated end to end" and [Settled 73] "the §7.3 re-attach claims from idle inventory" between them bounded which pod classes each remedy reaches.


### [spec-recheck.1.review-applicability.1]

DECISION: returned EMPTY. The r8→now delta in spec-changes.md is three prose additions (SPEC-1's §4.1 rationale, SPEC-1's §29.4 step-12 rationale, SPEC-3's new "cleanup that does not complete is still accounted" sentence plus its rationale paragraph) and one edge-case rewording. None of them mints an anchor, a heading, an identifier or a file, so the applicability surface is unchanged from the round the anchor sweep last certified. BECAUSE every "text to replace" block still matches the tree exactly once and every "replace it with" block occurs zero times, every markdown anchor the staged text mints resolves to a live heading, and no staged edit references an artifact a later staged edit creates. ALTERNATIVES: I considered filing the two spec-changes.md DEFERREDs the log already carries (see below) and declined on materiality.

FACT: I re-ran the anchor sweep mechanically rather than trusting the Settled entry, and it is still clean. `grep -Fc` of every verbatim block returns 1 for: spec/04:157 (§4.1 third sentence), spec/04:686 (`| \`Shutdown\` |` row opening), spec/04:854 (§4.7.9 step 5), spec/05:453 (whole `**Scrub model.**` paragraph), spec/05:545 (action-list sentence), spec/05:555 (`**Max retries:**` pod-selection sentence), spec/06:150 (fence heading), spec/06:158 (`**\`reserved\` hold semantics.**`), spec/06:234 (resuming-cancel clause), spec/07:23 (atomicity parenthetical), spec/07:210 (§7.2 preamble premise, full three-part string), spec/07:213 (step-2 tail), spec/07:214 (step-3 whole item), spec/07:414 (§7.3 list item 4). Only spec/29's step-13 tail is non-unique (3 sites: :645, :711, :982, and :645 is ALSO inside §29.4, in step 6's interrupt branch), and the instruction's "In §29.4's numbered step 13" scoping is what resolves it. EVIDENCE: spec/29_communication-scenarios.md:645, :711.

FACT: the two citations the delta's §4.1 rationale adds both check out. `spec/04_system-components.md:151` is the paragraph deriving a message's scope from its field set, and `:157` is the shipped `ShutdownRequest` paragraph whose second sentence says "per-slot teardown"/"whole-pod teardown" against the third sentence's "per-session teardown"/"whole-pod scrub". EVIDENCE: spec/04_system-components.md:151,:157.

FACT: the delta's §29.4 step-12 rationale citations all check out. `**Preconditions.**` is spec/29:586-591 and reads "so the runtime is running"; the interrupt-path addendum starts mid-:589; step 10's "valid in any non-terminal state" is at :671 inside the :669-674 step; "the adapter closes the session runtime" is at :697. EVIDENCE: spec/29_communication-scenarios.md:586-591,:669-674,:697.

FACT: the delta's new SPEC-3 clause resolves. `lenny_adapter_leaked_slots` is already named at spec/05:545 and spec/06:160 and is registered in gateway metrics, and "the whole-pod replacement trigger stated below" resolves to the single `**Whole-pod replacement trigger:**` bullet at spec/05:561, which is below the `**Scrub model.**` paragraph at :453 and still inside §5.2 (§5.3 starts at :666). EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,:561,:666.

FACT: no existing gate hard-fails on the staged spec text, re-derived rather than taken from the log. `recycle_scrub_trigger_consistency_test.go` reads the `| \`Shutdown\` |` line for "recycle disposition", "ReportPodScrub", the three recycle params and "does not block the response on the scrub", all of which sit in the row remainder SPEC-1 leaves untouched. `per_slot_substate_scope_doc_reconciliation_test.go` asserts only the four `generalSlotEdges` are present in the general block and absent from the scoped block; `receiving_uploads ──→ slot_cleanup` is in neither list, so SPEC-4's added edge passes. `credential_path_literal_sweep_test.go` sweeps the RETIRED pod-global `/run/lenny/credentials.json`, so SPEC-3's added `/run/lenny/slots/{sessionId}/` literal is the surviving path and not a sweep hit. `adapter_metric_catalog_test.go` reads pkg/adapter/metrics.go → catalogs, never spec → catalog, so naming the gauge a third time in spec/05 triggers nothing. The tier-0 line-citation ratchet's read domain explicitly excludes `proposals/`, so the delta's `file:line` rationale citations are not ratchet sites. EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65-100; tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-73; tests/tier11_docs/credential_path_literal_sweep_test.go:1-19; tests/tier11_docs/adapter_metric_catalog_test.go:26,:52-60; tests/tier0_static/line_citation_ratchet_test.go:421-423.

FACT: `UnhealthyThreshold` is `(maxConcurrent+1)/2` in integer arithmetic, which is 1 at `maxConcurrentSessions: 2`, so the delta's new edge-case clause ("a single windowed failure already reaches the §5.2 whole-pod replacement threshold") is arithmetically true. EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:215-220.

WATCHOUT: the §7.2 step-2 anchor is a mid-line fragment. "— the same artifact that was about to be replayed onto the replacement pod." is followed on the SAME line by "The session record's `final_workspace_ref` is set to this checkpoint's object key...", so applying the "Giving:" block inserts the new sentence BEFORE the `final_workspace_ref` sentence rather than at the end of step 2. That is deterministic and appliable, so it is not a finding, but an implementor who expects an append-to-end will be surprised. EVIDENCE: spec/07_session-lifecycle.md:213.

MISTAKE (half-closed, still open): the "Spec files touched" DEFERRED in the Standing context named TWO stale counts. A fix round closed the spec/07 half (it now reads "the section preamble's premise sentence deleted, a sentence added to step 2, and step 3 replaced") and left the spec/04 half standing: the entry still describes the §4.7 row edit as "(first sentence plus one sentence)" while the staged block replaces two sentences with eight. I did not file it: it changes no applied spec text and no behaviour, so it fails the materiality bar the skeptic has applied to fourteen findings on this proposal. The next fixer touching the SPEC-1 §4.7 block should correct it in the same edit. EVIDENCE: spec-changes.md "Spec files touched" spec/04 entry; spec-changes.md SPEC-1 §4.7 replacement block.

MISTAKE (still open, same reason): the Design's "A §15.1 start onto a slot reserved at creation is placed by neither mechanism" (spec-changes.md Design, the §7.1 paragraph) still has ONE antecedent, not two. Only §5.2's slot retry policy is named in the surrounding text; the §7.3 whole-pod idle claim through `podclaim.Claimer.Claim`, which round 3 deleted, was not restored. Not filed for the same materiality reason, and because a verifier pair already refuted a finding on this exact sentence (the "existential, not exhaustive" refutation).

UNVERIFIED: the delta's new SPEC-3 sentence states the `leaked` terminal, the occupancy hold and the whole-pod-replacement count UNQUALIFIED, inside the paragraph the same append declares to hold "on a pod of either concurrency", while the staged §7.1 says "the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there" on an exclusive pod. That is the standing Open item "'on a pod of either concurrency' and the leaked sentence", and it is now asserted by the proposal's own new text rather than merely implied by an adjunct. I did not file it because it is a close variant of a refuted finding, but it has moved from "the append might drag the bullet" to "the append states it outright", and a consistency lens should decide once. EVIDENCE: spec-changes.md SPEC-3 second anchor (the "still accounted" sentence); spec-changes.md SPEC-2 §7.1 block ("do not apply there"); spec/05_runtime-registry-and-pool-model.md:553,:561 (the trigger bullet lives inside the `maxConcurrentSessions > 1` retry-policy block).

USEFUL [Settled, "The anchor sweep is done, and it now covers SIXTEEN sites"]: correct and complete. Re-running it cost me one scripted `grep -Fc` pass and confirmed every entry. The one thing it understates is that the non-unique spec/29 anchor is non-unique WITHIN §29.4 as well (:645 is in step 6), not just repo-wide; the Traps entry has that right.


### [spec-recheck.1.review-citations.1]

FACT: every verbatim anchor in the current spec staging still resolves. Checked one by one:
§4.1 third sentence (spec/04_system-components.md:157), §4.7 `Shutdown` row opening (:686),
§4.7.9 step 5 (:854), §7.1 atomicity parenthetical (spec/07_session-lifecycle.md:23), §7.2
preamble premise (:210), step 2 tail (:213), step 3 (:214), §7.3 list item 4 (:414), §6.2
`resuming → cancelled` clause (spec/06_warm-pod-model.md:234), §5.2 `**Max retries:**`
sentence (spec/05_runtime-registry-and-pool-model.md:555), §5.2 `**Scrub model.**` (:453),
§5.2 `**Slot cleanup:**` action list (:545), §6.2 fence heading (:150) and
`receiving_uploads ──→ running` (:152-153), §6.2 `**`reserved` hold semantics.**` (:158),
§29.4 step 13 tail (spec/29_communication-scenarios.md:711). No anchor drift.

FACT: every citation the round-8 delta introduced checks out. `spec/04_system-components.md:151`
does say the classification "is derived from the message's field set"; `:157` does carry the
"per-session teardown"/"whole-pod scrub" mismatch the rationale attributes to it;
`spec/29_communication-scenarios.md:586-588` is the whole-trace `**Preconditions.**` ("so the
runtime is running"), `:589-591` is the interrupt path's additional requirement, `:697` is
"the adapter closes the session runtime", `:669-674` is step 10's "valid in any non-terminal
state" cited to §15.1, and §15.1's own table at spec/15_external-api-surface.md:649 says the
same. The `lenny_adapter_leaked_slots` gauge exists in shipped spec at spec/06:160 and
spec/05:545. The new edge-case arithmetic is right: ceil(2/2)=1, and the shipped windowed
counter does fire on this path (`health.RecordFailure` at pkg/gateway/sessionserver/start.go:2854
on a clean `ReleaseSlotReservation`).

WATCHOUT: §5.2's `**Scrub model.**` paragraph at spec/05_runtime-registry-and-pool-model.md:453
is a TOP-LEVEL, concurrency-independent paragraph ("uniform across session-mode
configurations"). It is not under `**Slot failure and cleanup (`maxConcurrentSessions > 1`).**`
(:542) nor `**Slot retry policy (`maxConcurrentSessions > 1`).**` (:553). Anything landed
there reaches a `maxConcurrentSessions: 1` pod. The round-8 fix appended a `leaked` /
occupancy-hold / whole-pod-replacement-trigger sentence to exactly that paragraph, and staged
§7.1 (spec-changes.md:262) says that disposition "do[es] not apply" on a single-session pod.
EVIDENCE: proposals/.../0081_....spec-changes.md:465 vs :262, :55, :133.

MISTAKE: the round-8 fix for the OPEN at review-log.md:206 ("a pre-start cleanup that fails can
never be reported `leaked`") re-introduced, in new text, the same unscoped-`leaked` defect two
earlier rounds had already removed from §7.1 and from the Design section. The concurrency
qualifier ("On a pod serving concurrent sessions, ...") has to travel with every new statement
of the `leaked` terminal, in every anchor, or the next round pays for it again.

FACT: the `leaked` scoping in the tree is real and load-bearing, not a proposal artefact.
`slot_cleanup ──→ leaked` sits under "Per-slot sub-states scoped to concurrent occupancy"
(spec/06_warm-pod-model.md:146-148) while the four edges SPEC-4 touches sit under "a pod of
either concurrency" (:150-155); the whole-pod replacement trigger is a bullet under a
`maxConcurrentSessions > 1` heading (spec/05:561 under :553).

USEFUL [standing context, Open, ":221 'on a pod of either concurrency' and the leaked sentence"]:
that UNVERIFIED item pointed straight at the delta's defect. It was framed as "does the append
DRAG the bullet's leaked sentence"; round 8 removed the ambiguity by stating the leaked
disposition outright in the either-concurrency paragraph, which upgrades the UNVERIFIED into a
filed contradiction. Whoever fixes it can close :221 in the same edit.

FACT: the code attributions the staging leans on all hold. `slotlayout.RemoveTree` removes
`p.CredentialsDir` (pkg/adapter/slotlayout/tree.go:60-68); `deregisterSlotLocked` cancels every
armed expiry timer before deleting the entry (pkg/adapter/slotsession.go:174-181);
`claimSessionSlotUnderLock` refuses a repeat start only on `st.started`
(pkg/adapter/slotsession.go:78-85); `stageWorkspace` calls `PrepareWorkspace` only under
`if len(uploads) > 0` (pkg/gateway/podlifecycle/podsession/binder.go:1322-1329).
§5.2:455 does state "after every ended session's per-slot tree and credential lease have been
removed", and §4.9's direct-delivery timer rule is spec/04_system-components.md:1169.

DEFERRED [nothing]: no correction derived that lands outside the spec staging.


### [spec-recheck.1.review-client-surface.1]

FACT: The delta in this recheck is ONE sentence of staged spec text (SPEC-3's §5.2 scrub-model append gains the "A cleanup on that path that does not complete is still accounted..." sentence naming `lenny_adapter_leaked_slots`), plus four blocks of proposal-internal RATIONALE (the §4.1 second-sentence justification, the §29.4 step-12 justification, the SPEC-3 "adds no accounting" paragraph, and the rewritten edge-case bullet 1). Everything else in the diff is checklist/summary/non-spec. EVIDENCE: diff of scratchpad/cp-snap/0081/spec-r8 vs the proposal dir, spec-changes.md:465.

FACT: Every line citation added in this round's rationale checks out. spec/04_system-components.md:151 is the field-set derivation sentence; :157 is the `ShutdownRequest` paragraph whose second and third sentences do disagree on vocabulary ("per-slot teardown"/"whole-pod teardown" vs "per-session teardown"/"whole-pod scrub"). spec/29_communication-scenarios.md:586-588 carries "so the runtime is running", :589-591 the interrupt path's additional requirement, :669-674 step 10's §15.1 restatement, :697 "adapter closes the session runtime". Nobody needs to re-verify these.

FACT: All nine markdown anchors used in the staged text resolve: `#49-credential-leasing-service` (spec/04:1099), `#479-startup-sequence-for-type-agent-runtimes` (spec/04:848), `#73-retry-and-resume` (spec/07:378), `#71-normal-flow` (spec/07:3), `#52-pool-configuration-and-execution-modes` (spec/05:365), `#62-pod-state-machine` (spec/06:78), `#151-rest-api` (spec/15:614), `#1542-rpc-lifecycle-state-machine` (spec/15:1686), `#47-runtime-adapter`. All five verbatim replace-anchors match the tree exactly (spec/04:157, :686, :853; spec/05:545, :555; spec/06:234; spec/07:210, :212, :214).

FACT: `schemas/lenny-adapter.proto` ALREADY carries the widened cleanup list SPEC-3 stages. `SESSION_SCRUB_OUTCOME_RELEASED`'s comment reads "the slot's runtime, **credential timers**, and per-slot directory tree were torn down cleanly", and the `ReportSessionScrub` RPC comment says the same. So SPEC-3's first anchor (adding the credential directory and the §4.9 timers to §5.2's three-action list) brings spec INTO line with the shipped proto rather than away from it. Do not file the §5.2 widening as a proto/SDK mirror gap. EVIDENCE: schemas/lenny-adapter.proto:307-319, :436-443.

FACT: No client-facing parallel representation exists for anything this proposal changes, and I checked each one. Per-slot sub-states: spec/15:672 states the fine states are "never returned in external API responses", and `grep slot_cleanup|slot_assigned` over spec/ docs/ schemas/ charts/ sdks/ returns only spec/06:146-155, docs/reference/state-machines.md:234-251 (DOCS-1) and one proto comment. `ShutdownRequest.reason` is a plain string. `ShutdownResponse` needs no field for the clean-exit answer. `WARM_POOL_EXHAUSTED`/`concurrent_slots_exhausted` are reused as they stand. §15.3/§15.4 defer the RPC surface to §4.7 and state no Shutdown semantics of their own. §28 names no `Shutdown` at all. spec/16 has no leaked-slot metric or alert row. So the client-surface lens has nothing to mirror here beyond the docs deliverable, which is the other loop's.

WATCHOUT: `spec/28_communication-channels.md:1082` (the `terminate` frame register row) and the §15.4.3 integration-level table row for graceful drain both survive SPEC-1's co-tenancy gate unchanged, because they state what receipt of the frame means and which level has the channel, never that the frame is sent at every session end. Do not file either as a missed edit site; four surfaces were checked (§15.4.2 state table, §15.4.3 level table, §15.4.5 roadmap item 3, §28's Timing/Degradation bullets) and none asserts unconditional emission. EVIDENCE: spec/28_communication-channels.md:1082, :1100-1130; spec/15_external-api-surface.md:1697-1700, :1780.

MISTAKE (this round's fix introduced it): SPEC-3's new sentence states the `leaked` outcome, the held occupancy and the whole-pod replacement trigger with NO concurrency qualifier, inside the paragraph the proposal itself designates concurrency-independent, while SPEC-2's §7.1 block says in so many words that "the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there". This is the third occurrence of a defect class the loop already fixed twice on §7.1 and on SPEC-3's earlier text. Filed. EVIDENCE: spec-changes.md:465 vs :262; spec/06_warm-pod-model.md:146-148; spec/05_runtime-registry-and-pool-model.md:553-559.

DEFERRED [schemas/runtime-ops-events.schema.json, pkg/gateway/podlifecycle/podsession/slotbinder.go]: the compensating `Shutdown` is staged as `cl.Shutdown(rctx, req.SessionID, "slot_bind_failed", 0)`, so on a reclaim of a session whose start the adapter DID admit, `drainReason` normalises the reason to `session_complete` and the frame carries `deadlineMs: 0` against the schema's `minimum: 100`. Both are code/schema-lane and both are already recorded (review-log Standing context, the `terminate` frame reason OPEN and the `deadlineMs` UNVERIFIED). I re-derived both and did not file: the reason value is an OPEN routed to a human, and the deadline is pre-existing on every shipped caller. What is true and worth stating once: neither is made worse by the staged spec text, because §4.7 now puts the §15.4.2 signal inside the runtime teardown, so a bound-but-unstarted reclaim writes no frame at all.

USEFUL [Standing context, "No per-slot sub-state is client-visible" and "`ShutdownRequest.reason` is a plain `string` with no enumeration"]: these two entries removed the whole first half of this lens's search space in one read. Without them I would have re-derived the OpenAPI/SDK/CRD sweep from scratch.


### [spec-recheck.1.review-docs-alignment.1]

DECISION: Returned an empty findings list for the spec lane — BECAUSE every defect my lens found this round has its remedy in `docs/` or in the non-spec staging, which this loop's scope bars, and the two in-spec candidates I built were close variants of already-refuted findings — ALTERNATIVES: I built and dropped (i) "SPEC-3's new leaked/threshold clause asserts the `leaked` terminal in a paragraph the same proposal declares holds `on a pod of either concurrency`, contradicting staged §7.1's 'the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there'" — dropped as a close variant of the already-refuted "SPEC-3's 'on a pod of either concurrency' clause generalises the whole bullet"; (ii) "§4.7's `ReportSessionScrub` row (spec/04:692) still says the outcome is reported 'at a session release' with no pre-`running` exception" — dropped because the staged §5.2 text frames the pre-`running` reclaim as an ADDITIONAL trigger ("It also runs when...") rather than as a session release, so the row stays true, and because it is a close variant of the refuted `**Slot cleanup:**`-bullet finding.

DEFERRED [0081...non-spec-changes.md]: `docs/reference/adapter-contract.md:75` is the reader-facing mirror of the §4.7 `Shutdown` row SPEC-1 rewrites, and it becomes false when SPEC-1 lands. It states unconditionally: "The adapter flushes the session's final usage report, closes its runtime, removes its slot tree, and reports the per-slot cleanup outcome through `ReportSessionScrub`." After SPEC-1 a `Shutdown` for a registered-but-unbound or bound-but-unstarted session runs the slot release only: no final usage report, no runtime close, and after SPEC-3 no `ReportSessionScrub`. The staged docs list holds only DOCS-1 (`docs/reference/state-machines.md`), so this page is in no edit list. What is true instead: the row must state the two teardowns with their two preconditions and the no-entry clean-exit no-op, mirroring the staged §4.7 text. EVIDENCE: docs/reference/adapter-contract.md:75; spec-changes.md SPEC-1 §4.7 replacement; non-spec-changes.md:506 (DOCS-1 is the only staged docs edit).

DEFERRED [0081...non-spec-changes.md]: three further docs sentences state the cleanup-outcome report unconditionally and are candidates for the same edit once the §5.2 exception lands: `docs/reference/adapter-contract.md:81` ("Report the per-slot cleanup outcome ... at each session release, on a pod of any concurrency and any recycle setting"), `docs/operator-guide/security-principles.md:33` and `docs/reference/execution-modes.md:68` / `docs/operator-guide/multi-tenancy.md:72` ("A per-slot cleanup runs at each session release ... and the adapter reports its outcome to the gateway"). These are weaker than the `Shutdown` row: the staged §5.2 text presents the pre-`running` reclaim as an additional trigger rather than as a session release, so a reader can hold both. Decide them in the non-spec loop rather than assuming they are falsified.

FACT: `docs/reference/metrics.md` does not list `lenny_adapter_leaked_slots` at all (its slot block is `lenny_slot_failure_total` / `lenny_slot_pod_replacement_total`). SPEC-3's new clause names that gauge, but the gap is pre-existing — the gauge is spec'd in §6.2 and §5.2 today and was deliberately kept out of the §16.1 typed catalog. Do not file it against 0081. EVIDENCE: docs/reference/metrics.md:158-167; spec/06_warm-pod-model.md:160; BUILD-GAPS.md:4963.

FACT: every line citation the r8→r9 delta added checks out verbatim: spec/04_system-components.md:151 (field-set derivation) and :157 (the `ShutdownRequest` paragraph); spec/29_communication-scenarios.md:586-588 ("so the runtime is running"), :589-591 (the interrupt path's additional requirement), :669-674 (step 10's §15.1 restatement), :697 ("the adapter closes the session runtime"). The step-12 argument the delta adds is sound on its own terms. EVIDENCE: spec/29_communication-scenarios.md:586-591, :669-674, :694-702.

FACT: the delta's new arithmetic claim is correct and is pre-existing shipped behaviour rather than something 0081 introduces. `applySlotRetryPolicy` calls `health.RecordFailure` on every cleanly-released failed bind, and §6.2:160 sets the drain threshold at `ceil(maxConcurrentSessions/2)`, so at `maxConcurrentSessions: 2` one windowed failure trips it. The deleted "a blob-store outage does not retire healthy pods" claim was the false half. EVIDENCE: pkg/gateway/sessionserver/start.go:2851-2857 (`health.RecordFailure`), :2858-2860 (`health.Unhealthy` → drain); spec/06_warm-pod-model.md:160.

FACT: the SPEC-3 gauge clause is consistent with the staged code rather than an unbacked assertion. The adapter sets `ExitedCleanly: closeErr == nil && (started || treeErr == nil)`, the gateway maps `err != nil || !cleanly` to `Leaked`, and the staged `accountSlotFailure` takes a `leakGauge` argument, so a pre-`running` reclaim whose tree removal fails does reach `lenny_adapter_leaked_slots`. EVIDENCE: non-spec-changes.md:115, :277, :414-427; pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543.

WATCHOUT: the scope line for this loop ("report only findings whose fix lands in the staged spec edits") retires most of the docs-alignment lens for the spec lane. A missing or falsified `docs/` page is out of scope here even though the lens names it as a finding class. Write it as DEFERRED and let the non-spec loop own it; filing it costs two verifiers and closes nothing.


### [spec-recheck.1.review-edit-sites.1]

FACT: the r8→r9 delta in spec-changes.md is exactly three hunks — the edge-case bullet-one
rewrite (the DEFERRED :260 correction, now honest about `ceil(2/2)=1`), the §4.1 rationale
expansion, and TWO new sentences in SPEC-3 (one inside the staged §5.2 append at :465, one
rationale paragraph at :478-482). Everything else is byte-identical.
EVIDENCE: diff -ru scratchpad/cp-snap/0081/spec-r8 proposals/0081_.../ (85 lines)

MISTAKE: the round-8 fixer reintroduced the unscoped-`leaked` defect the orchestrator's
already-fixed list names twice. The new SPEC-3 sentence at spec-changes.md:465 states the
`leaked` disposition, the held occupancy and the whole-pod replacement trigger with NO
concurrency qualifier, inside the paragraph whose own first sentence says "on a pod of either
concurrency", while staged §7.1 (:262) and the exclusive-pod edge-case bullet (:132-134) say
categorically that the sub-state and the trigger "are stated for concurrent occupancy and do
not apply there". Filed. The distinguishing fact against the already-refuted "the pointer
clause drags the bullet" family: this is an ASSERTION written into the append, not an
implication carried by an adjunct, so the refutation's "where a specific meets a general
pointer the specific governs" reasoning does not reach it.
EVIDENCE: spec-changes.md:465 vs :262 and :132-134; spec/06_warm-pod-model.md:146-148;
spec/05_runtime-registry-and-pool-model.md:553,561

FACT: the same new sentence asserts "the adapter's `Shutdown` response for that reclaim does
not report a clean exit" over the WHOLE pre-`running` range, but CODE-1's response is
`ExitedCleanly: closeErr == nil && (started || treeErr == nil)`
(non-spec-changes.md:154), which discards the tree-removal error whenever `started` is true.
A start still in flight is `started == true` and is explicitly placed in `receiving_uploads`
by SPEC-4, so it is inside the range. The proposal documents the gap itself at
non-spec-changes.md:157-161 and :716-719 ("`exited_cleanly` carries two meanings"). Filed;
the remedy is a scope clause in the staged §5.2 sentence, so it lands in the spec staging.

WATCHOUT: `lenny_adapter_leaked_slots` is NOT a new edit site. It already appears in
spec/05:545 and spec/06:160, and neither spec/16_observability.md nor
docs/reference/metrics.md carries any leaked-slot row (grep returns nothing). The new
mention adds no orphan.

WATCHOUT: the two new §29.4 rationale citations verify clean — the Preconditions paragraph is
spec/29_communication-scenarios.md:586-591 ("so the runtime is running" on :587), step 12's
"the adapter closes the session runtime" is on :697, step 10's §15.1 citation is on :673. The
"three occurrences of the bound/started drift" MISTAKE entry is discharged: the new step-12
rationale no longer says "every bound entry the call removed". Do not re-file it.

USEFUL [Settled: "The staged §5.2 sentences have no mirror outside spec/05"]: saved a full
grep sweep of "always assigned to a new slot" / "kills any processes owned by" / "releases the
slotId"; re-confirmed, still single-site.

USEFUL [review-log.md:1509 and :2605-2606]: two earlier lenses already built and dropped
"§7.1's `SlotID == SessionID` falsifies §5.2's retained 'new slot' and the Fresh workspace
guarantee". I rebuilt it independently and dropped it on their reasoning ("new slot" reads as
a new reservation; the placement rule keeps the hazard off every retry §5.2 places). A third
derivation is waste — the entry is worth promoting into Traps.


### [spec-recheck.1.review-feasibility.1]

FACT: the r8→recheck delta in spec-changes.md is exactly FIVE hunks and nothing else — the upload-free edge-case bullet's closing clause (:101-104), the §4.1 rationale paragraph (:173-184), the §29.4 step-12 rationale paragraph (:221-232), one new sentence inside SPEC-3's §5.2 `**Scrub model.**` append (:465), and a new rationale paragraph justifying it (:478-483). `diff -ru` the whole directory returns nothing else in this file. — EVIDENCE: scratchpad/cp-snap/0081/spec-r8 vs the live directory.

FACT: every new citation in the delta resolves. `spec/04_system-components.md:151` is the field-set derivation sentence and `:157` is the `ShutdownRequest` paragraph; `spec/29_communication-scenarios.md:586-588` is §29.4's Preconditions base clause ("so the runtime is running"), `:589-591` the interrupt path's additional requirement, `:669-674` step 10's "valid in any non-terminal state" plus its §15.1 cite, `:697` step 12's "the adapter closes the session runtime". Nobody needs to re-verify these.

FACT: the §5.2 whole-pod replacement trigger has exactly TWO evaluation sites in the tree, and SPEC-3 closes one of them by construction. `slothealth.Tracker.Unhealthy` is called from `applySlotRetryPolicy` (pkg/gateway/sessionserver/start.go:2858) and from `drainLedger.RecordLeak` (pkg/gateway/session/recycle/scrubreporter_seams.go:184-190), the latter driven only by `ReportSessionScrub`. SPEC-3 withholds that report on the pre-`running` path, so `accountSlotFailure` is the ONLY route a pre-`running` leak has to the trigger and to the `lenny_adapter_leaked_slots` gauge. Any future sentence asserting leak accounting on a pre-`running` reclaim must name a path that reaches `accountSlotFailure`. — EVIDENCE: pkg/gateway/sessionserver/start.go:2844-2860; pkg/gateway/session/recycle/scrubreporter_seams.go:184-190.

FACT: `pkg/gateway/podlifecycle/podsession` holds ZERO references to `slothealth`, `slotstate` or any leak gauge (`grep -n "slothealth\|slotstate\|leakGauge" pkg/gateway/podlifecycle/podsession/*.go` is empty), and `Binder.Resume`'s failure branch returns `fmt.Errorf("podsession: resume session on pod %s: %w", ...)` rather than a `*SlotBindError` (binder.go:1621-1630), which `resumeOnPod` then returns unexamined (start.go:4041-4043). So on the §7.3 re-attach the leak disposition cannot cross the package boundary at all: neither layer can see it. This is the mechanical ground under the standing OPEN "Does the resume compensation feed the slothealth ledger?" — it is now answered NO, and the answer is architectural rather than an oversight in CODE-4's prose.

WATCHOUT: the delta's new SPEC-3 sentence is the first spec text in this proposal to assert the leak ACCOUNTING (threshold + gauge) for a pre-`running` reclaim, and its companion rationale asserts "The clause on a cleanup that does not complete adds no accounting. It names the route the accounting already takes". That rationale is true on the two concurrent bind paths (`applySlotRetryPolicy`, `bindConcurrentSlot`'s reserved branch, both wired to `accountSlotFailure` by CODE-5 at non-spec-changes.md:434-444) and false on the §7.3 re-attach, which staged §7.1 (spec-changes.md:262), staged §7.2 step 3 (:320) and staged §7.3 (:349) all bind to the same reclaim obligation. Filed as this round's one finding. — EVIDENCE: spec-changes.md:465,478-483; non-spec-changes.md:393-401,432-444,620-621.

MISTAKE (nearly filed, and here is why it was dropped): the new SPEC-3 sentence states the `leaked` disposition unscoped inside a paragraph that opens "on a pod of either concurrency", while staged §7.1 says "the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there" for an exclusive pod. That looks like a self-contradiction between two staged edits, but it is a close variant of the already-refuted "'on a pod of either concurrency' clause generalises the whole bullet" finding, whose refutation turns on specific-governs-general plus SPEC-2's explicit §7.1 carve-out — reasoning that disposes of the new sentence identically. Do not re-file it on this evidence; new evidence would have to show a reader with no access to §7.1 reaching a different required behaviour.

MISTAKE (nearly filed, second): the new sentence says the pre-`running` leak "is surfaced on the `lenny_adapter_leaked_slots` gauge", while spec/06_warm-pod-model.md:160 attributes that gauge to the ADAPTER's health metadata and the adapter is never told about this leak class (the gateway derives it from the `Shutdown` answer and SPEC-3 withholds the report). That attribution is already wrong against the tree before this proposal — the gauge is emitted by the gateway at pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223 — and the review log carries it as a recorded dead end plus an OPEN that is explicitly "not 0081's". Dropped as pre-existing.

FACT: no §16 or docs mirror exists for `lenny_adapter_leaked_slots`. `grep -rn "lenny_adapter_leaked_slots" spec/ docs/ schemas/` returns only spec/05:545 and spec/06:160. The new SPEC-3 sentence therefore opens no observability edit site.

FACT: the adapter has NO process-group kill anywhere (`grep -rn "Setpgid\|Pgid\|process group" pkg/adapter/*.go` is empty), so SPEC-3's widened action list keeps a shipped action the code does not perform. Pre-existing — the sentence being widened already carried it — and outside this proposal. Recorded so the next lens does not spend a pass deriving it.


### [spec-recheck.1.review-fresh.1]

FACT: the r8→now delta in the spec-changes file is exactly four hunks — edge-case bullet one
(blob-store wording, the recorded DEFERRED landed), the §4.1 rationale expansion, the §29.4
step-12 rationale rewrite, and ONE new sentence inside SPEC-3's §5.2 `**Scrub model.**` append
plus a rationale paragraph for it. Everything else is byte-identical.
EVIDENCE: diff -u scratchpad/cp-snap/0081/spec-r8/...spec-changes.md vs the live file, 85 lines.

FACT: the new SPEC-3 sentence is the only new NORMATIVE text this round, at
spec-changes.md:465 ("A cleanup on that path that does not complete is still accounted: the
adapter's `Shutdown` response for that reclaim does not report a clean exit, and the slot is
`leaked` … counts toward the whole-pod replacement trigger stated below, and is surfaced on
the `lenny_adapter_leaked_slots` gauge."). Both findings this round are against it. Any later
round should read that one sentence against staged §7.1 (:262) and against CODE-1's
`ExitedCleanly` expression (non-spec-changes.md:154) before reading anything else.
EVIDENCE: spec-changes.md:465; :262; non-spec-changes.md:154.

WATCHOUT: the new sentence re-opens the concurrency-scoping defect the orchestrator's
already-fixed list records TWICE. Rounds past made §7.1's `leaked` disposition concurrency-
scoped ("the `leaked` sub-state and the whole-pod replacement trigger are stated for
concurrent occupancy and do not apply there", :262) and wrote the same carve-out into the
exclusive-pod edge-case bullet (:131-134). The new sentence states the same terminal
UNSCOPED, in the paragraph the proposal itself calls concurrency-uniform (:465 opens "on a
pod of either concurrency"; the shipped paragraph opens "The scrub is uniform across
session-mode configurations", spec/05:453). This is NOT the refuted "on a pod of either
concurrency generalises the whole bullet" item: that one turned on an adjunct dragging the
bullet's rules, and this is the proposal's own sentence asserting the terminal outright.
EVIDENCE: spec-changes.md:465 vs :262 and :131-134; spec/06_warm-pod-model.md:146-148
(`slot_cleanup ──→ leaked` under "scoped to concurrent occupancy");
spec/05_runtime-registry-and-pool-model.md:553,:562 (the "trigger stated below" sits under
`**Slot retry policy (`maxConcurrentSessions > 1`)**`).

WATCHOUT: "the adapter's `Shutdown` response for that reclaim does not report a clean exit"
is false for the started-but-not-yet-`runtimeLive` half of the very path SPEC-3 names.
CODE-1 returns `ExitedCleanly: closeErr == nil && (started || treeErr == nil)`, so once
`st.started` is true the tree-removal error is DISCARDED, and the proposal states that as a
deliberate choice with a reason (it would reclassify existing session-end slots as leaked).
SPEC-4's own prose puts "a start still in flight" in `receiving_uploads`, i.e. inside "that
path". So the new spec sentence and the staged code contradict on a window the proposal
names explicitly in three places.
EVIDENCE: spec-changes.md:465; non-spec-changes.md:154,:157-162,:716-719; spec-changes.md
SPEC-4 prose ("a start still in flight, whose session the runtime has not yet acknowledged,
leave the slot in `receiving_uploads`").

FACT: `lenny_adapter_leaked_slots` has NO row in spec/16 or docs/reference/metrics.md
(`grep -rn leaked` in both returns nothing), and it is already named unscoped in shipped
spec/05:545 and spec/06:160. So the new sentence's gauge clause creates no §16 edit site.
Do not file it. EVIDENCE: spec/05:545; spec/06:160; spec/16 (no hit).

FACT: the gauge is set from exactly one production site, `cmd/lenny-gateway/sessionsrv.go:382`
→ `SlotLeakGauge`, fed by `applySlotRetryPolicy`'s `slots.MarkLeaked` arm
(start.go:2846-2850). The proposal routes the create-time-reserved branch into the same
accounting (`bindConcurrentSlot`'s reserved branch calls `accountSlotFailure`,
non-spec-changes.md:442), so the create-time path is NOT a gauge gap. The resume path is the
only remaining question and it is already the standing Open.
EVIDENCE: cmd/lenny-gateway/sessionsrv.go:378-383; pkg/gateway/sessionserver/start.go:2833-2852;
non-spec-changes.md:442.

FACT: every "reads, verbatim" anchor in the staging still matches the tree exactly once,
re-checked mechanically this round for the six newest ones (§7.2 preamble/step2/step3, §7.3
list tail, §6.2 cancel clause, §7.1 parenthetical, §4.7.9 step 5, §4.7 row opening, §5.2
Max-retries and Scrub-model). The §29.4 step-13 anchor sits at spec/29:711. No anchor drift.

FACT: the new §29.4 step-12 rationale's line citations all resolve. §29.4 Preconditions is
spec/29:585-591 ("so the runtime is running" at :586); the interrupt-path addition is
:589-591; step 10's "valid in any non-terminal state" is :671 inside :669-674; step 12's
"the adapter closes the session runtime" spans :696-697. The §4.1 rationale's `:151` and
`:157` also resolve. Nobody needs to re-verify these.

MISTAKE (mine, withdrawn): I nearly filed the §4.1 rationale's new claim "this edit retires
no vocabulary" as false, because "per-session teardown" has exactly one site in spec/ and
docs/ (spec/04:157) and the replacement removes it. It is rationale prose that changes
nothing an implementor applies, the standing Open on §4.1 vocabulary already carries the
substance, and four lenses have declined the neighbouring sentence. Not worth two verifiers.

MISTAKE (mine, withdrawn): I nearly re-filed the refuted "§5.2 Max retries excludes only an
unacknowledged reclaim, not an answered-but-unclean one", on the new ground that the delta
now makes the answered-but-unclean case an explicit `leaked` class INSIDE §5.2, two
paragraphs from the Max-retries bullet. The prior refutation's ground ("an acknowledgement of
a reclaim is an acknowledgement that it completed") re-applies verbatim to the new spelling,
so this would be re-litigation on the same evidence.

UNVERIFIED: whether the fixer that scopes the new §5.2 sentence to concurrent occupancy will
then leave the exclusive-pod pre-`running` cleanup failure with NO stated disposition at all.
Staged §7.1's exclusive branch covers an UNACKNOWLEDGED reclaim; it says nothing about an
acknowledged reclaim whose cleanup failed on an exclusive pod. Whoever lands the scoping fix
should check that the exclusive branch's disposition reaches both.


### [spec-recheck.1.review-kubernetes.1]

DECISION: filed two findings, both on the ONE sentence this round added to SPEC-3's §5.2 scrub-model append ("A cleanup on that path that does not complete is still accounted: ... does not report a clean exit ... the slot is `leaked` ... counts toward the whole-pod replacement trigger stated below ... surfaced on the `lenny_adapter_leaked_slots` gauge", spec-changes.md:465) — BECAUSE that sentence is the entire spec-side delta of this round and it asserts a wire-level mechanism and a concurrency-independent disposition that the proposal's own staged code and its own staged §7.1 both deny — ALTERNATIVES: filing nothing on the Kubernetes-idiom lens proper (status ownership, finalizers, CRD-as-bus, controller-on-hot-path) which I re-swept and which is clean, exactly as the standing context's "Ownership is clean" entry records.

FACT: the new §5.2 sentence's clean-exit premise is false for the start-in-flight subrange, which is the case the whole proposal exists for. CODE-1 stages `ExitedCleanly: closeErr == nil && (started || treeErr == nil)` and the paragraph under it says outright "The started path keeps discarding the tree-removal error". `started := removed && st.started`, and `st.started` is set in `claimSessionSlotUnderLock` BEFORE `Runtime.Start`, so a start still in flight — which SPEC-4 puts in `receiving_uploads`, inside SPEC-3's own range — has `started == true` and its failed tree removal never reaches the response. EVIDENCE: non-spec-changes.md:154,:157-161; pkg/adapter/slotsession.go:87-88; pkg/adapter/session.go:111,:156,:163; spec-changes.md:465,:507.

FACT: the same sentence states the `leaked` terminal and the whole-pod replacement trigger with no concurrency qualifier, inside the paragraph whose own opening clause is "on a pod of either concurrency", while staged §7.1 says in terms "the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there". The trigger it points at with "stated below" sits under `**Slot retry policy (`maxConcurrentSessions > 1`)**` at spec/05:553-562. EVIDENCE: spec-changes.md:262,:465; spec/05_runtime-registry-and-pool-model.md:542,:553,:562.

DEFERRED [proposals/0081_.../non-spec-changes.md CODE-4/CODE-5]: the same new sentence's "counts toward the whole-pod replacement trigger ... and is surfaced on the `lenny_adapter_leaked_slots` gauge" is not delivered on the §7.3 re-attach, one of the three attempt kinds §7.1 binds. `accountSlotFailure`'s stated callers are exactly `applySlotRetryPolicy` and `bindConcurrentSlot`'s reserved branch (non-spec-changes.md:434-443); `Binder.Resume`'s staged compensation carries the disposition only into `releaseResumeSlot`, and `resumeOnPod` returns the error with no `MarkLeaked`, no leak gauge and no `RecordLeak` (pkg/gateway/sessionserver/start.go:4041-4043). Occupancy IS held on that path (leaked=true early-returns in `ReleaseSlot`), so only the two accounting halves are missing. I did not file it: this loop's scope is the spec staging and the honest remedy is one more `accountSlotFailure` caller. It sharpens the standing OPEN "Does the resume compensation feed the slothealth ledger?" from unverified to verified-absent, which is what the non-spec loop needs.

USEFUL [standing context, Settled "Adapter predicate nesting" and "`st.started` precedes `Runtime.Start`, and it has THREE setters"]: these two entries are what made finding 1 a five-minute derivation instead of a re-read of the whole adapter. The started ⊋ runtimeLive gap is the load-bearing fact and the entry states it exactly.

USEFUL [standing context, Traps, "MISTAKE, three separate occurrences: the bound/started drift"]: the same drift has now recurred a fourth time, in a different direction. This round's new sentence does not misname the predicate; it assumes the `exited_cleanly` signal is available across a range the predicate splits. A future round should check every NEW sentence that reasons about `exited_cleanly` against the `started` boundary as well as against the `bound` one.

WATCHOUT: do not "fix" finding 1 by making the adapter surface `treeErr` on the started path unconditionally. That reclassifies ordinary session-end slots as leaked under `Binder.ReleaseSlot`'s `err != nil || !cleanly` rule, which is the §6.2 accounting change non-spec-changes.md:158-160 explicitly declines. The code-side form that works is gating on the report predicate rather than the teardown predicate (`closeErr == nil && (live || treeErr == nil)`), which surfaces the error across the whole pre-`running` range and leaves session-end alone. The spec-side form is to scope the sentence's clean-exit premise to a reclaim of a session whose start the adapter has not admitted. EVIDENCE: non-spec-changes.md:104,:154,:158-160.

FACT: the §29.4 rationale added this round cites accurately. `spec/29_communication-scenarios.md:586-588` does read "so the runtime is running", `:589-591` is the interrupt path's additional requirement, `:697` is "the adapter closes the session runtime and the pod is replaced", and `:669-674` is step 10's "valid in any non-terminal state". The round-7 disagreement recorded at standing context "Is §29.4 scoped to a started session?" is now answered in the preconditions-scope direction and the citations hold, so that OPEN can be closed rather than re-litigated.


### [spec-recheck.1.review-mechanism.1]

FACT: The r8→now delta in `spec-changes.md` is exactly four hunks: the edge-case bullet on the
upload-free compensation (:98-104), the SPEC-1 §4.1 rationale rewrite (:173-184), the §29.4
step-12 rationale rewrite (:221-232), and SPEC-3's new "A cleanup on that path that does not
complete is still accounted" clause plus its rationale paragraph (:465, :478-483). Three of the
four are rationale prose; only the SPEC-3 one lands in `spec/`. — EVIDENCE:
proposals/0081.../0081...spec-changes.md:465

FACT: The new SPEC-3 clause reintroduces the unscoped-`leaked` defect the loop already fixed
twice, in a third site. It states the `leaked` disposition, the occupancy hold and the whole-pod
replacement trigger for the pre-`running` cleanup path inside the `**Scrub model.**` paragraph
whose immediately preceding staged sentence scopes that cleanup "on a pod of either concurrency",
while SPEC-2's §7.1 paragraph says of an exclusive pod "the `leaked` sub-state and the whole-pod
replacement trigger are stated for concurrent occupancy and do not apply there". — EVIDENCE:
spec-changes.md:465 vs :262; spec/06_warm-pod-model.md:146-148; spec/05_runtime-registry-and-pool-model.md:542,553,562

WATCHOUT: This is NOT the standing-context trap "the `slot_cleanup ──→ leaked` scoping asymmetry
is pre-existing" (review-log Traps). That trap is about shipped §5.2/§6.2 prose being loose
against the fence. The new finding is a contradiction between two sentences THIS proposal stages,
created by the round-8 fix, against the proposal's own recorded DECISION ("the `leaked`
disposition is scoped to the pod class that offers it"). Do not let a verifier collapse the two.

FACT: The staged CODE-1 response predicate is `ExitedCleanly: closeErr == nil && (started ||
treeErr == nil)`, and the staging says so deliberately: "The started path keeps discarding the
tree-removal error". `st.started` is set before `Runtime.Start`, so a reclaim of a start still in
flight has `started == true` while the slot is still `receiving_uploads` (SPEC-4's own words). So
on that sub-case of the pre-`running` path a failed tree removal still answers clean, and the new
SPEC-3 clause's "the adapter's `Shutdown` response for that reclaim does not report a clean exit"
is false there. The staging even excludes the test for it and says the assertion applies to a
"not-started reclaim". — EVIDENCE: non-spec-changes.md:154, :156-160, :571-575; spec-changes.md:465, :507

DEFERRED [non-spec-changes.md]: `accountSlotFailure`'s callers are `applySlotRetryPolicy` and
`bindConcurrentSlot`'s reserved branch only (non-spec-changes.md:434-443). `Binder.Resume`'s
`releaseResumeSlot` threads the disposition into `ReleaseSlot(leaked)` but reaches no
`MarkLeaked`, no leak gauge and no `RecordLeak`. So on the §7.3 re-attach the new SPEC-3 clause's
"counts toward the whole-pod replacement trigger ... and is surfaced on the
`lenny_adapter_leaked_slots` gauge" has no code path behind it. Code-lane; not filed here.

USEFUL [Traps]: "Do not put a per-slot scrub rule in §5.2's `**Slot cleanup:**` bullet" and the
Settled entry on the two fence blocks (spec/06:146-148 vs :150-156) are what make the
concurrency-scoping finding checkable in two greps. Both saved a round.


### [spec-recheck.1.review-operational.1]

CORRECTS [Open, "This lens has swept itself out"]: the operational lens is no longer empty. The r8→r9
delta added a leak-accounting sentence to SPEC-3's §5.2 scrub-model append, and that sentence is the
first staged spec text in this proposal that asserts a metric and a health-threshold consequence. Two
findings came out of it. The advice in that entry (re-check the §16 rows and the ReportSessionScrub /
sessions_served chain) was right about where to look and wrong about the answer being empty.

FACT: the `lenny_adapter_leaked_slots` gauge has exactly ONE producer in the tree,
`applySlotRetryPolicy`'s failed-release arm. The `ReportSessionScrub(leaked=true)` handler feeds
`drainLedger.RecordLeak` and never touches the gauge. So the gauge is NOT the general leaked-slot
signal §5.2:545 and §6.2:160 describe; it is the retry-policy path's signal only.
EVIDENCE: pkg/gateway/sessionserver/start.go:2844-2848; pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481; grep for MarkLeaked/SetAdapterLeakedSlots returns no third site.

FACT: `Binder.Resume`'s failure branch reaches no health ledger, no slot registry and no gauge. It
calls `releaseResumeSlot` → `ReleaseSlotReservation` and returns. `slothealth.Tracker`,
`slotstate.Registry` and the leak gauge all live in `pkg/gateway/sessionserver`, which `podsession`
does not reach. CODE-5 names exactly two `accountSlotFailure` callers (`applySlotRetryPolicy` and
`bindConcurrentSlot`'s reserved branch) and neither is the resume path, so the §7.3 re-attach leak
this proposal creates is accounted nowhere.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1620-1630,:1706-1717; pkg/gateway/sessionserver/start.go:4041-4043; non-spec-changes.md:432-444.

USEFUL [Open, "Does the resume compensation feed the slothealth ledger?"]: that UNVERIFIED item is
now VERIFIED as "no". It was cheap to close (two greps) and it is what turned the new SPEC-3 sentence
from prose into a filable defect. Whoever compacts should promote it from Open to Settled with the
answer.

WATCHOUT: `UnhealthyThreshold(maxConcurrent) = (maxConcurrent+1)/2` in Go integer arithmetic, so it is
1 at concurrency 1 AND at concurrency 2. Any argument that "the trigger cannot fire at concurrency 1"
is about the SPEC's `maxConcurrentSessions > 1` heading scope, never about the code.
EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:215-221.

FACT: the adapter implements no process-group kill at all (`grep -rn "Setpgid|syscall.Kill|pgid"
pkg/adapter/*.go` is empty), while §5.2's action list names one and SPEC-3 keeps it. So the new
"does not report a clean exit" sentence over-claims for that action. Judged pre-existing and below
the bar; do not re-derive it as a finding.

OPEN: SPEC-3's new sentence puts the `leaked` disposition and the whole-pod replacement trigger into
§5.2's concurrency-independent `**Scrub model.**` paragraph while staged §7.1 carves both out at
concurrency 1. This is the same subject as the retired Open item "'on a pod of either concurrency' and
the leaked sentence", but the earlier refutation was about a POINTER clause, and this is a standalone
assertion. Filed this round; if it is refuted again, record which of the two readings won so the
question stops returning.



### [spec-recheck.1.review-performance.1]

FACT: the r8→r9 delta in `spec-changes.md` is exactly three hunks: the upload-free edge-case
bullet's transient/leak-count correction (:104-107), the §4.1 rationale rewrite (:173-185),
the §29.4 step-12 rationale rewrite (:218-229), and SPEC-3's new "does not complete is still
accounted" clause plus its rationale paragraph (:465, :478-483). Everything else is
byte-identical. — EVIDENCE: `diff -u scratchpad/cp-snap/0081/spec-r8/...spec-changes.md
proposals/0081_.../...spec-changes.md` is 85 lines.

FACT: `applySlotRetryPolicy` is the ONLY production site that feeds `slotstate.MarkLeaked`,
the `lenny_adapter_leaked_slots` gauge and `slothealth.RecordLeak` today. Nothing else in
`pkg/gateway/sessionserver` touches them. — EVIDENCE: pkg/gateway/sessionserver/start.go:2844-2860;
`grep -n "slotLeakGauge\|MarkLeaked" pkg/gateway/sessionserver/*.go` returns only :2736 and :2844.

FACT: the §7.3 re-attach's slot release has no accounting caller at all, before or after this
proposal. `Binder.Resume`'s failure branch calls `releaseResumeSlot` → `ReleaseSlotReservation`
and nothing else; `resumeOnPod` does `return "", err` on that error with no tracker call.
So a `leaked` disposition on that path holds Redis occupancy with no route to the §5.2
threshold and no gauge series. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1620-1629,
:1710-1718; pkg/gateway/sessionserver/start.go:4041-4043; podclaim/slotclaimer.go:830-836.

FACT: `ExitedCleanly: closeErr == nil && (started || treeErr == nil)` discards the tree-removal
error whenever `started` is true, and `started` is set before `Runtime.Start` while `running`
(SPEC-4) is `runtimeLive`. So the started-but-not-`runtimeLive` window is inside SPEC-3's
pre-`running` range yet reports a clean exit on a failed tree removal. The proposal states the
discard deliberately. — EVIDENCE: non-spec-changes.md:154, :158-160.

MISTAKE: the fix round that landed the SPEC-3 clause recorded the resume-path wiring gap as its
own OPEN and dismissed it as "an implementor-level wiring gap ... rather than a reason to file a
report" (review-log.md, `[f1.open-decisions.human-decisions.1]`), then wrote a spec sentence
that asserts the accounting universally over the pre-`running` range, plus a rationale paragraph
claiming the clause "adds no accounting. It names the route the accounting already takes."
Both are false on the §7.3 path. Filed.

WATCHOUT: the SPEC-3 clause and the §7.1 sentence at spec-changes.md:262 use two different
predicates for the same disposition ("does not report a clean exit" versus "the adapter does not
acknowledge"), and the clause is unscoped by concurrency while §7.1 explicitly denies the
`leaked` sub-state and the replacement trigger on an exclusive pod. The predicate-width half is
a standing OPEN and was left alone deliberately; the concurrency half has been declined by four
lenses as structural. I filed neither, but the new clause states the leaked disposition
DIRECTLY rather than by pointer, which is stronger than the text those declines were about.
— EVIDENCE: spec-changes.md:262, :465.

UNVERIFIED: whether §4.6.1 orphan GC actually reclaims a pod left holding a resume-path leaked
slot. It drains a `bound` claim whose pod no active session references, so it fires only while
the pod carries no other session; once `ClaimSlot` pass 1 places a same-tenant session there the
claim is referenced and the leak has no reclaim route at all. Somebody should check
`claimOrphanTimeout`'s predicate against a pod at occupancy 1 with one live sibling.


### [spec-recheck.1.review-reliability.1]

FACT: the r8→r9 delta in the spec staging is four hunks only — the edge-case bullet 1 rewrite
(blob-store outage / windowed counter), the §4.1 rationale expansion, the §29.4 step-12 rationale,
and ONE new sentence appended inside SPEC-3's §5.2 `**Scrub model.**` block plus its rationale
paragraph. Everything else is byte-identical. — EVIDENCE: diff -u scratchpad/cp-snap/0081/spec-r8/...spec-changes.md
proposals/0081_.../...spec-changes.md (85 lines total)

FACT: the new SPEC-3 sentence is the whole delta worth a reliability pass. It reads "A cleanup on that
path that does not complete is still accounted: the adapter's `Shutdown` response for that reclaim does
not report a clean exit, and the slot is `leaked` under §6.2, so it holds its occupancy, counts toward
the whole-pod replacement trigger stated below, and is surfaced on the `lenny_adapter_leaked_slots`
gauge." — EVIDENCE: proposals/0081_.../...spec-changes.md:465

DECISION: filed two findings against that one sentence and no others — BECAUSE (1) its clean-exit half is
false for the started-but-pre-`running` sub-case, which staged CODE-1 deliberately answers clean
(`ExitedCleanly: closeErr == nil && (started || treeErr == nil)`, non-spec-changes.md:154, justified at
:158-161), and that sub-case is exactly the racing start §7.1's reclaim exists for; (2) its leaked/trigger
half sits in the paragraph §5.2 declares "uniform across session-mode configurations" (spec/05:453) and
points at a trigger stated only inside `maxConcurrentSessions > 1` (spec/05:553,:561), contradicting staged
§7.1's "the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy
and do not apply there" (spec-changes.md:261) — ALTERNATIVES: filing the resume-path accounting gap (see
DEFERRED below; remedy is code, out of this loop's scope); re-filing any of the standing dead ends
(occupancy hold, Redis rehydration, replica-local ledger, `concurrent_slots_exhausted` gloss), all of which
I re-derived and dropped on the standing-context entries.

WATCHOUT: `st.started` is TRUE for a start still in flight, and SPEC-4's own staged prose puts a start still
in flight on the `receiving_uploads` side. So "pre-`running`" and "unstarted" are NOT the same set, and any
sentence that keys a signal on the pre-`running` range while the code keys it on `started` is wrong for
their difference. This is the bound/started drift in a new dress: the standing context records three earlier
occurrences of it, and the delta added a fourth. — EVIDENCE: pkg/adapter/slotsession.go:87-88;
pkg/adapter/session.go:156,:163; spec-changes.md:507

FACT: `removeSlotTree` really can fail (`slotlayout.RemoveTree`, an os.RemoveAll chain), so the treeErr arm
is not a theoretical branch. — EVIDENCE: pkg/adapter/slot.go:210-212

DEFERRED [proposals/0081_.../...non-spec-changes.md, CODE-4/CODE-5]: the new §5.2 sentence asserts that a
failed pre-`running` cleanup "counts toward the whole-pod replacement trigger ... and is surfaced on the
`lenny_adapter_leaked_slots` gauge", and the spec-changes rationale at :478-483 claims the clause "adds no
accounting" because it "names the route the accounting already takes". That route exists on exactly two of
the three bind paths §7.1 binds. `applySlotRetryPolicy` and `bindConcurrentSlot`'s reserved branch both call
the new `accountSlotFailure` (non-spec-changes.md:414-444), which is what does `MarkLeaked` + leak gauge +
`RecordLeak`. `Binder.Resume`'s staged compensation does not: it compensates, closes, and releases through
`releaseResumeSlot` carrying the disposition (non-spec-changes.md:393-396, and the call-site table at :387-392
lists `releaseResumeSlot` with no accounting caller). So on a §7.3 re-attach an unacknowledged reclaim holds
occupancy forever and reaches neither the `ceil(maxConcurrentSessions/2)` trigger nor the gauge. What is true:
the clause DOES add an accounting obligation on the resume path, and CODE-4 or CODE-5 owes an
`accountSlotFailure` call there. This upgrades the standing Open item "Does the resume compensation feed the
slothealth ledger?" (review-log.md:230) from a code-lane question to a spec-level obligation with nothing
staged to discharge it. Remedy is code, so this loop cannot land it.

USEFUL [standing context, Traps, "Dead end: 'the adapter cannot see this leak'" and "Do not read the reclaim
as scoped away from pods that retire"]: both saved a full re-derivation of the gauge's emitter and of the
concurrent last-occupant case. The gauge entry is also what let me see that the delta's gauge claim is true
on two paths and false on the third rather than false everywhere.


### [spec-recheck.1.review-security.1]

FACT: the r8→now delta in spec-changes.md is exactly four hunks: the blob-store edge-case bullet rewrite (:98-104, closing DEFERRED :258), the §4.1 rationale expansion (:173-184), the §29.4 step-12 rationale block (:221-232), and SPEC-3's new "a cleanup on that path that does not complete is still accounted" clause plus its rationale (:465, :478-483). Everything else in the file is byte-identical. — EVIDENCE: diff -u scratchpad/cp-snap/0081/spec-r8/...spec-changes.md proposals/0081_.../...spec-changes.md

FACT: the §29.4 rationale block's four line citations all resolve as written: Preconditions "so the runtime is running" at spec/29_communication-scenarios.md:586-588, the interrupt path's additional requirement at :589-591, step 10's "valid in any non-terminal state" with its §15.1 cite at :669-674, and step 12's "the adapter closes the session runtime" at :697. The §4.1 rationale's two cites also resolve (spec/04_system-components.md:151 field-set derivation, :157 the ShutdownRequest paragraph). Nobody needs to re-verify these.

FACT: the started-vs-runtimeLive gap is inside the SPEC-3 range, and it is what the new leak clause misses. `st.started = true` at pkg/adapter/slotsession.go:88 (inside claimSessionSlotUnderLock), `Runtime.Start` at pkg/adapter/session.go:156, `noteRuntimeStarted` at :163. SPEC-4's staged prose puts "a start still in flight" in `receiving_uploads`, so the pre-`running` range contains entries with `started == true`, for which staged CODE-1's `ExitedCleanly: closeErr == nil && (started || treeErr == nil)` (non-spec-changes.md:154) discards the tree-removal error. Filed as finding 1.

WATCHOUT: when reading SPEC-3's new clause, do NOT equate "pre-`running`" with "not started". Three of the proposal's own predicates live in that window (entry present ⊃ bound ⊃ started ⊃ runtimeLive) and the clause's guarantee only holds for the two below `started`. A fixer that scopes the clause by saying "not started" rather than "the reclaim of a session whose start the adapter had not admitted" is saying the same thing; a fixer that scopes it by saying "pre-`running`" has changed nothing. — EVIDENCE: proposals/0081_.../...spec-changes.md:465,:507; non-spec-changes.md:154,:157-162

DECISION: filed the concurrency-scoping of the NEW `leaked` sentence even though the standing OPEN records four lenses declining the older "does the pointer drag the leaked sentence to concurrency 1" question — BECAUSE the earlier question was about a pointer clause reaching into a bullet with its own `maxConcurrentSessions > 1` heading, whereas this round's sentence asserts the disposition directly, in a paragraph the append's own first sentence declares to hold "on a pod of either concurrency", against staged §7.1's explicit "the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there". Both sentences are this proposal's own staged text, so it is a proposal-internal contradiction rather than the pre-existing structural asymmetry. ALTERNATIVES: leaving it to the recorded OPEN, rejected because the OPEN itself flags "the append is new text" as the unresolved part and this round made the text more explicit rather than less.

FACT: `SlotClaimer.ReleaseSlot`'s leaked branch early-returns before the counter decrement and before the occupancy-zero disposition (pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836), which is why a `leaked` disposition on an exclusive pod would withhold the claim DELETE that retires the pod today. That is the concrete cost of the unscoped sentence, not merely a wording asymmetry.

UNVERIFIED: the new clause also asserts the pre-`running` leak "counts toward the whole-pod replacement trigger" and "is surfaced on the `lenny_adapter_leaked_slots` gauge". On the create/retry paths CODE-5's `accountSlotFailure` supplies both, but the staged §7.3 re-attach compensation (`Binder.Resume`) is not listed among `accountSlotFailure`'s callers (non-spec-changes.md:428-440), so on that path the release holds occupancy while nothing feeds slothealth or the gauge. This is standing OPEN "Does the resume compensation feed the slothealth ledger?", now promoted from an unstated gap to a spec assertion. Not filed here because the remedy is code-lane; the non-spec loop should close it or the spec sentence needs a scope.

MISTAKE (nearly filed, withdrawn): the racing-start ordering where the reclaim answers clean, the gateway decrements occupancy with `leaked=false`, and the start's own claim then re-creates the entry, leaving a live session the counter does not count. It is a real relaxation of §5.2's slot-assignment atomicity and it is standing OPEN "Transient over-assignment on the acknowledged racing-start ordering", but the refuted list already carries the close variant "the racing-start residue ... has its outcome in no staged spec text" and the remedy is one more sentence in an accepted-failure-mode bullet. Do not spend a round on it.


### [spec-recheck.2.review-citations.1]

DECISION: returned EMPTY. BECAUSE every concrete citation in the staged spec edits resolves and every attributed behaviour checks out against the tree; the only candidates I built were close variants of already-refuted findings or proposal-internal prose imprecision below the materiality bar. ALTERNATIVES rejected: (a) the "(first sentence plus one sentence)" stale count in "Spec files touched" plus the matching lead-in "Replace the `Shutdown` row's first sentence" — the quoted anchor block replaces TWO sentences with EIGHT, so both descriptions are stale, but the operative quote/replacement pair is exact and an implementor cannot be misled; already recorded as Deferred and routed to the between-loops pass. (b) Design :8-10 "§4.1's derivation paragraph and the §4.7 `Shutdown` row both treat the runtime close and the slot release as one act gated on the binding" — the §4.7 row states NO gate at all (spec/04:686), only §4.1:157 does; a half-false shared predicate whose conclusion is right. (c) Design :63-66 "placed by neither mechanism" still has one antecedent (Deferred 257 unlanded).

FACT: the delta this round is confined to SPEC-3. The r2 snapshot dir was byte-identical to the live proposal (both stamped 09:41), so `diff` against it is empty; the real delta is `spec-recheck-r1-prefix` → live, and inside spec-changes.md it is exactly two hunks: the staged §5.2 append gained an exclusive-pod arm ("On a pod serving one session the disposition is the one [Section 7.1] states: the failed attempt releases the pod's claim and the pod retires"), and its rationale paragraph was rewritten to scope the `leaked` half to concurrent occupancy and to add "the gateway's slot-failure accounting covers the retry-placed bind path today, and the code lane extends the same helper to the create-time reserved path and to the §7.3 re-attach". EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r1-prefix vs proposals/0081_.../*.spec-changes.md:465,478-489

FACT: both halves of that new rationale sentence are code-true. `applySlotRetryPolicy` already does `MarkLeaked` + leak gauge + `RecordLeak` / `RecordFailure` + `Unhealthy → DrainSandbox` today; `bindConcurrentSlot`'s reserved branch returns straight through `classifySlotBindFailure` with no accounting; `resumeOnPod`'s `podBinder.Resume` failure branch is a bare `return "", err`. So "covers the retry-placed path today" and "the code lane extends it to the other two" are both accurate. EVIDENCE: pkg/gateway/sessionserver/start.go:2834-2873 (today's accounting), :2596-2604 (reserved branch, none), :4041-4043 (resume branch, none)

FACT: the anchor sweep re-ran mechanically and is still clean, including after the SPEC-3 rewrite. All 16 "text to replace" fenced blocks occur exactly once across `spec/`; all "replace it with" blocks occur zero times. Script: extract ```-fenced blocks from spec-changes.md, count occurrences in every spec/*.md. Do not re-run unless spec/ moves or a new anchor is minted.

FACT: the §29.4 rationale's four line citations all resolve and say what is claimed. `spec/29:586-588` is the Preconditions paragraph carrying "so the runtime is running"; `:589-591` is the interrupt path's additional requirement stated as an addition; `:669-674` is step 10 with "is valid in any non-terminal state" at :671 and the §15.1 citation at :673; `:697` carries "adapter closes the session runtime" (the sentence begins "On the default disposition the" at :696 and wraps). The round-6/7 §29 block is citation-clean.

FACT: §4.9's direct-delivery-mode expiry timer is stated generally, not only in the `anthropic_direct` provider row. spec/04:1169 is the per-provider row an earlier reviewer might read as the only site, but spec/04:1466 states it as the general enforcement point ("the adapter expiry timer is the enforced lease deadline"). SPEC-3's "cancels the [Section 4.9] direct-delivery-mode lease-expiry timers armed for that session" is exact, and the `#49-credential-leasing-service` anchor resolves to spec/04:1099.

WATCHOUT: the non-spec lane changed CODE-1's response gate from `st.started` to `live` (runtimeLive) AFTER this loop's spec fix landed (non-spec-changes.md edited 09:40, spec-changes.md 09:38). That change makes the staged §5.2 sentence "the adapter's `Shutdown` response for that reclaim does not report a clean exit" MORE exact, because the response predicate is now the same `running` boundary SPEC-3 and SPEC-4 install. A later citation lens checking that sentence against `ExitedCleanly: closeErr == nil && (started || treeErr == nil)` is reading a stale snapshot; the current expression is `(live || treeErr == nil)`. EVIDENCE: non-spec-changes.md:154, :182-186

WATCHOUT: the delta sharpens, rather than creates, the tension between SPEC-3's opening pointer ("That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency") and the bullet's own "If cleanup fails, the slot is leaked" (spec/05:545), because the new arm says a one-session pod's failed cleanup is NOT leaked. This is the already-refuted "generalises the whole bullet" family and the standing structural-asymmetry trap; the refutation reads the adjunct as attaching to the identification rather than to every rule in the bullet. Do not re-file it on this evidence.

WATCHOUT: the new §5.2 sentence "the failed attempt releases the pod's claim and the pod retires" is not discharged by any staged code path on the §7.3 re-attach onto an EXCLUSIVE pool — `reserveResumeSlot` returns "" there, `releaseResumeSlot` no-ops, and `resumeOnPod` returns with no rollback, so the replacement pod stays `claimed` until §4.6.1 orphan GC. This is Open 223 plus the already-refuted "§7.1's exclusive-pod disposition is unreachable on the §7.3 re-attach" finding, now restated on a second surface. Same evidence, same refutation; a round wanting it needs NEW evidence, and its remedy is code-lane.

USEFUL [Settled 68, the sixteen-anchor sweep]: saved a full re-derivation. It is still accurate after the SPEC-3 rewrite because that rewrite changed only a "replace it with" block, minting no new anchor.
USEFUL [Settled 128, "the snapshot diffs have been empty for most rounds"]: told me to diff against the named earlier snapshot rather than the current one. The r2 snapshot was again empty; `spec-recheck-r1-prefix` is the one that carries the delta.
USEFUL [Trap 161, 180, 155, 157]: four candidates I built and dropped without spending verifier pairs.


### [spec-recheck.2.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens on the r8→now delta and on the whole staging — BECAUSE the staged spec edits touch no REST/OpenAPI/MCP/A2A/SDK/CRD/JSONL surface, mint no client-visible enum, error code, reason value or field name, and every externally-consumed identifier they name resolves in the tree — ALTERNATIVES: rejected filing the `docs/reference/adapter-contract.md:75` drift (remedy is in the non-spec staging, already DEFERRED five times at review-log.md:250,:461), the `ShutdownResponse.exited_cleanly` semantic widening (no proto edit is staged and the field is documented nowhere in spec/ or docs/), and the §29.2 step-10 atomicity restatement (an incomplete enumeration in a trace, and spec/29:23-25 makes the cited section normative).

FACT: the delta this round is exactly what review-log.md:438 records — one staged sentence in SPEC-3's §5.2 scrub-model append plus four blocks of proposal-internal rationale (§4.1 second-sentence justification, §29.4 step-12 justification, SPEC-3's "adds no accounting" paragraph, edge-case bullet 1). `diff -ru scratchpad/cp-snap/0081/spec-recheck-r2 <proposal dir>` is EMPTY: the r2 snapshot was taken at the same instant as this round's start, so diff against `spec-r8` instead. EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r2 mtimes vs proposal dir mtimes.

FACT: every delta citation resolves. `spec/04_system-components.md:151` is the field-set derivation sentence and `:157` is the shipped `ShutdownRequest` paragraph carrying "per-session teardown"/"whole-pod scrub"; `spec/29_communication-scenarios.md:586-588` is the `**Preconditions.**` sentence ("so the runtime is running"), `:589-591` the interrupt path's additional requirement, `:669-674` step 10's "valid in any non-terminal state" clause, and `:697` step 12's "the adapter closes the session runtime". Nobody needs to re-open these.

FACT: no client-facing parallel exists for anything this proposal changes. `receiving_uploads` as a per-slot sub-state appears only at spec/06:151-152, docs/reference/state-machines.md:234-235 and two SVG diagrams; spec/15:672 declares the fine states internal-only and never returned in external API responses. `exited_cleanly` appears exactly once outside code, at schemas/lenny-adapter.proto:1666, with no doc comment. `lenny_adapter_leaked_slots` is in no §16 table and in no docs/reference/metrics.md row, so naming it in §5.2 orphans nothing. EVIDENCE: grep across spec/ docs/ schemas/ sdks/ charts/.

FACT: SPEC-3's two new §5.2 action-list items are verified against the surfaces that own them. `/run/lenny/slots/{sessionId}/credentials.json` is the spelling used by spec/04:793 (`credentialsPath` manifest field), spec/13:26,:30, spec/05:461,:471, docs/reference/adapter-contract.md:523 and all three runtime SDKs; spec/04:1169 confirms the expiry timer is direct-delivery-mode only ("In proxy delivery mode ... no adapter-side timer is needed"), so SPEC-3's "direct-delivery-mode lease-expiry timers" qualifier is exact rather than decorative.

FACT: `schemas/lenny-adapter.proto:203-206`'s `rpc Shutdown` doc comment ("asks the adapter to terminate the agent and release the pod ... Returns when the agent process has exited") is already false for a co-tenanted pod in the SHIPPED tree, before any of these edits. It is not a drift this proposal creates, and programme rule S-2 bars opening the file, so it is not an edit site to add. EVIDENCE: pkg/adapter/session.go:259-260 gates the drain on `!boundRemains`.

WATCHOUT: the `§29.4 step-13` staged sentence scopes the no-frame rule with "on a pod serving concurrent sessions" while the §4.7 row states it unscoped, and the proposal claims it "reuses §4.7's own wording verbatim". The added scope is harmless (on a single-session pod the deregistration always leaves no bound entry), so this is wording, not a contradiction. Do not file it. EVIDENCE: spec-changes.md §29.4 block; spec/04:686 staged replacement.

WATCHOUT: spec/06:283-286's pre-attached retry policy says "Each retry claims a fresh pod", which reads against the proposal's create-time-reserved edge-case bullet ("the retried start reconnects to the same pod"). It is a PRE-EXISTING spec-versus-code tension already recorded (review-log.md:113 "Pre-existing and outside this proposal", :190 the declined-imprecision list). Do not spend a verifier pair on it.

USEFUL [Standing context :60]: "No per-slot sub-state is client-visible" saved the whole SDK/OpenAPI/CRD sweep for SPEC-4; re-verified and it holds.
USEFUL [Standing context :59]: the `ShutdownResponse` / `exited_cleanly` fact closed the only plausible wire-contract finding this lens had.


### [spec-recheck.2.review-edit-sites.1]

FACT: the round-2 recheck snapshot is byte-identical to the live proposal, so `diff -ru scratchpad/cp-snap/0081/spec-recheck-r2 proposals/0081_.../` produces NOTHING and the orchestrator's "read the delta first" instruction has no target from that snapshot. The real delta for this round is against `spec-recheck-r1-start`, and it is 36 diff lines confined to SPEC-3: the leak-accounting clause in the §5.2 `**Scrub model.**` append is now concurrency-split (concurrent pod → `leaked` + trigger + gauge; single-session pod → §7.1's pod-claim release and retirement), and the rationale paragraph under it was rewritten to say the same thing and to name the three code paths CODE-5 now serves. EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r1-start/...spec-changes.md vs proposals/0081_.../...spec-changes.md:462,475-489.
FACT: that delta is clean under the edit-site lens. The concurrent arm's three claims each resolve to shipped §5.2/§6.2 text (`spec/05_runtime-registry-and-pool-model.md:545` names the `lenny_adapter_leaked_slots` gauge, `:562` is the "whole-pod replacement trigger stated below", `spec/06_warm-pod-model.md:160` is the `leaked` semantics paragraph), and the single-session arm restates staged §7.1 verbatim in substance. The rewritten rationale's "covers the retry-placed bind path today, and the code lane extends the same helper to the create-time reserved path and to the §7.3 re-attach" now matches CODE-5's own caller list exactly (non-spec-changes.md:481,:486,:488), closing the earlier round's contradiction.
FACT: all sixteen "text to replace" anchors still resolve uniquely and byte-for-byte at commit-time HEAD. I re-ran the sweep rather than trusting the Settled entry: each of the nine long anchors returns exactly one file with count 1 under `grep -rFc`, and `**Scrub model.**`, §4.7.9 step 5, `receiving_uploads ──→ running` and `**`reserved` hold semantics.**` each return one line. EVIDENCE: spec/04:157,:686,:854; spec/05:453,:545,:556; spec/06:152,:158,:234; spec/07:23,:210,:213,:214,:415; spec/29:703-710.
FACT: the "Spec files touched" list at spec-changes.md:517-531 is complete and correct against the staged blocks — five files, and every staged anchor appears in it with the right description (spec/07's §7.2 entry already carries the round-4 three-part form). No missing or phantom entry.
FACT: the edit-site sweep for every identifier the staging adds or changes comes back empty outside the already-recorded DEFERRED docs/schemas sites. `receiving_uploads` has exactly one spec/ site (spec/06:151-152); "per-session teardown" exactly one (spec/04:157); "runtime teardown"/"slot release" are new vocabulary with no prior spec/docs occurrence; `/run/lenny/slots/{sessionId}/credentials.json` and the §4.9 direct-mode timer both resolve (spec/04:914,:1169); `concurrent_slots_exhausted` resolves (spec/05:549); `lenny_adapter_leaked_slots` resolves (spec/05:545, spec/06:160). No spec/ file carries a generation header, and `charts/` matches none of these strings at all (`grep -rln "ReportSessionScrub\|slot cleanup\|maxConcurrentSessions" charts/` is empty), so there is no authored-vs-generated chain to get wrong here.
WATCHOUT: `spec/12_storage-architecture.md:913` ("Send graceful-shutdown signals to all active sessions", tenant-disabling step 2) and `spec/15_external-api-surface.md:1795` ("Basic-level runtimes receive only the `shutdown` message at expiry with no advance notice") are the same class as the §11.4-revoke-step-3 trap already in Standing context: SPEC-1's co-tenancy gate makes each loose on a co-tenanted pod, each is already false against the shipped `boundRemains` gate, and each is a delivery-intent sentence rather than a delivery guarantee. I derived both, checked both, and declined both on the trap's own reasoning. A future round that widens the co-tenancy gate's scope should re-check all three together. EVIDENCE: pkg/adapter/session.go:259; spec/11_policy-and-controls.md:264; spec/12_storage-architecture.md:913; spec/15_external-api-surface.md:1795.
FACT: `spec/28_communication-channels.md` is NOT an edit site for the co-tenancy gate. Its `terminate` row (:1082) and its Timing/Degradation bullets (:1100,:1126) state the frame's schema and its deadline behaviour and never state when a session end emits it, and `grep -n "Shutdown" spec/28 spec/12 spec/10` returns nothing for the RPC. Five minutes saved for the next lens that wonders.
USEFUL [Standing context / Traps]: the trap list did real work this round. "Do not file §11.4 revoke step 3", "Do not file either `leaked` gloss", "Do not file the `slot_cleanup ──→ released` annotation" and "the anchor sweep now covers SIXTEEN sites" each stopped a candidate I had already half-built. The sixteen-site list in particular let me verify the sweep mechanically in one shell loop instead of re-deriving it.
DECISION: returned an empty findings list — BECAUSE every candidate I built this round resolved either to a Standing-context trap, to one of the seventeen already-refuted findings, or to prose precision on text whose applied meaning is determinate. ALTERNATIVES: filing the §12:913 / §15:1795 co-tenancy pair (rejected: identical in class and evidence to the §11.4 trap, so it would spend two verifiers to reproduce a refutation already on the record); filing the §16 inventory's silence on `lenny_adapter_leaked_slots` (rejected: pre-existing, and the Standing entry that §16 needs no edit was re-verified — the gauge has no §16 row before or after these edits).


### [spec-recheck.2.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE every actor-action assignment in the staged
spec text resolves to a component that can perform it against the tree, and the three candidates I
built all collapsed into recorded dead ends or pre-existing conditions — ALTERNATIVES: rejected
filing (a) "the adapter cannot surface a leak it never learned about" (the gauge is gateway-emitted,
verified below), (b) "§5.2's widened slot-release action list obliges a process-group kill the
adapter never performs" (pre-existing for bound entries too), (c) "the creation finalize block that
§7.1 binds reaches no accounting caller in CODE-5" (vacuous: no slot is bound there).

WATCHOUT: the orchestrator's named snapshot for this round is EMPTY. `diff -ru
scratchpad/cp-snap/0081/spec-recheck-r2 proposals/0081_.../` returns nothing, and so does
`spec-recheck-r2-start`. The real delta since the last converged review is against
`scratchpad/cp-snap/0081/spec-r8`, and inside spec-changes.md it is exactly four hunks: the
edge-case bullet-one rewrite (:98-104), the §4.1 rationale expansion (:170-187), the §29.4 step-12
rationale block (:216-232), and SPEC-3's leak-accounting clause plus its rationale (:462-490).
EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0081/spec-r8

FACT: `lenny_adapter_leaked_slots` is a gateway-side Prometheus GaugeVec, registered in
`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-238` and set through
`Metrics.SetAdapterLeakedSlots` (`gatewaymetrics.go:1208-1217`), reached from
`sessionserver.slotLeakGauge` (`sessionserver.go:417-421,:1875`). SPEC-3's new clause "is surfaced
on the `lenny_adapter_leaked_slots` gauge" is therefore TRUE against the tree for a leak the
adapter never learns about. The pre-existing falsehood is spec/06_warm-pod-model.md:160's "The
adapter exposes a `leaked_slots` count ... (`/healthz` response and `lenny_adapter_leaked_slots`
gauge)". Do not file the new clause on that pre-existing sentence; three lenses have now confirmed
the gauge's real emitter. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-223

FACT: the §5.2 `**Slot cleanup:**` action list's "kills any processes owned by the slot's process
group" has NO implementation anywhere in `pkg/adapter/`. `removeSlotTree` is one line
(`slotlayout.RemoveTree(st.paths)`, pkg/adapter/slot.go:210-212) and greps for `Setpgid`,
`syscall.Kill`, `pgid` and "process group" over `pkg/adapter/*.go` return nothing. SPEC-3 replaces
that sentence and keeps the clause verbatim, so the gap is pre-existing on both the bound and the
widened unbound branch and is not created by widening the slot release to unbound entries.
EVIDENCE: pkg/adapter/slot.go:210-212; spec/05_runtime-registry-and-pool-model.md:545

FACT: `prepareAtFinalize` returns `(nil, nil)` for `match.MaxConcurrentSessions > 1`
(pkg/gateway/sessionserver/finalize.go:238-240), so of the three attempt kinds §7.1's staged
paragraph binds, the creation finalize block engages the binder only on an EXCLUSIVE pool, where
no slot reservation exists at all. That is why CODE-5's three accounting callers
(`applySlotRetryPolicy`, `bindConcurrentSlot`'s reserved branch, `resumeOnPod`) map onto only two
of §7.1's three kinds without leaving a gap: both `applySlotRetryPolicy` and `BindReservedSlot` are
the §15.1 start transition. A lens counting callers against §7.1's parenthetical will think one is
missing; it is not. EVIDENCE: pkg/gateway/sessionserver/finalize.go:236-240

FACT: `reserveResumeSlot` DOES land a Redis increment (`SlotClaimer.ReserveSlotOnPod`,
pkg/gateway/podlifecycle/podsession/binder.go:1685-1703), and `releaseResumeSlot`'s own doc comment
says an exclusive pool "reserved nothing". So SPEC-3's "it holds its occupancy" is true on the §7.3
re-attach path as well as on the two start paths, and the leaked hold there is a real held
increment rather than a notional one. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1674-1714

FACT: §29.4's `**Preconditions.**` paragraph is at spec/29_communication-scenarios.md:586-591 and
reads "The session is attached to a claimed pod and the startup sequence §29.2 traces has
completed, so the runtime is running ...", with the interrupt path's extra requirement introduced
as an addition. The delta's step-12 rationale quotes it accurately, and step 10's "valid in any
non-terminal state" is at :671 inside a clause explicitly cited to §15.1's endpoint table
(:669-674). Step 12's "the adapter closes the session runtime" is at :696-697. Every line citation
in the new §29.4 rationale block resolves. EVIDENCE: spec/29_communication-scenarios.md:586-591,:671,:696-697

FACT: the §4.1 rationale's two new citations resolve. `spec/04_system-components.md:151` is "Each
request message on the gateway-adapter protocol is either session-scoped or pod-scoped, and the
classification is derived from the message's field set", and `:157` is the `ShutdownRequest`
paragraph whose second and third sentences carry the "per-slot teardown"/"per-session teardown"
mismatch the rationale calls pre-existing. `grep -rn` over spec/, docs/ and schemas/ finds
"per-session teardown" at spec/04:157 and nowhere else. EVIDENCE: spec/04_system-components.md:151,:157

FACT: the edge-case bullet-one arithmetic in the delta is now correct.
`slothealth.UnhealthyThreshold` is `(maxConcurrent+1)/2`, which is 1 at `maxConcurrentSessions: 2`,
matching §5.2:561's `ceil(maxConcurrentSessions / 2)`. The round-8 DEFERRED against that bullet's
old "a blob-store outage does not retire healthy pods" clause is discharged by the rewrite.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561

USEFUL [Standing context / Traps]: the "Dead end: 'the adapter cannot see this leak'" entry and the
sixteen-site anchor sweep each saved a full derivation. I re-verified six of the sixteen anchors by
`grep -F` (spec/07:210, :214, :414; spec/06:234; spec/05:555; spec/04:854) and all six still match
byte for byte after the round-8 edits, which touched no anchor block. Do not re-run the sweep.


### [spec-recheck.2.review-fresh.1]

DECISION: returned empty — BECAUSE the only delta since the last converged review is SPEC-3's leak-accounting clause (concurrency-split) plus its rationale rewrite, and every load-bearing claim in it checks out against the tree; nothing else in the staging moved. ALTERNATIVES: I built and dropped four candidates (below).

FACT: the round-2 delta is exactly two hunks in spec-changes.md and nothing else in the directory changed. `diff -u scratchpad/cp-snap/0081/spec-recheck-r1-start/...spec-changes.md proposals/.../...spec-changes.md` is the only useful diff; `spec-recheck-r2` is byte-identical to the live proposal (it was snapshotted after the fixes), so diffing against it returns nothing and wastes a step. EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r2/ vs proposals/0081_.../ (diff -ru silent).

FACT: the SPEC-3 rationale's new claim "the code lane extends the same helper to the create-time reserved path and to the §7.3 re-attach" is TRUE as staged. CODE-5 lists three `accountSlotFailure` callers: `applySlotRetryPolicy` (today's tail, verified at pkg/gateway/sessionserver/start.go:2828-2873), `bindConcurrentSlot`'s `BindReservedSlot` branch, and `resumeOnPod`'s `podBinder.Resume` failure branch. EVIDENCE: non-spec-changes.md:481-489; pkg/gateway/sessionserver/start.go:2807-2885.

FACT: the earlier "unclean answer across the whole pre-`running` range" defect IS closed. CODE-1's response is now `ExitedCleanly: closeErr == nil && (live || treeErr == nil)`, keyed on `live` (runtimeLive membership), so a start still in flight — `started` but not `live` — surfaces the tree-removal error. That is what makes SPEC-3's "the adapter's `Shutdown` response for that reclaim does not report a clean exit" true across the whole staged range. EVIDENCE: non-spec-changes.md:152-175.

FACT: §5.2's `**Scrub model.**` anchor is byte-exact at spec/05_runtime-registry-and-pool-model.md:453, the whole-pod replacement trigger is at :561 and the `**Slot cleanup:**` bullet at :545, so the appended clause's two "below" pointers both resolve forward. Re-verified independently this round.

FACT: §29.4's `**Preconditions.**` paragraph (spec/29_communication-scenarios.md:586-591) does scope the WHOLE trace — interrupt and session-end alike — to a session whose §29.2 startup sequence completed ("so the runtime is running"), and the interrupt sentence is explicitly an addition ("additionally requires"). This settles standing OPEN "Is §29.4 scoped to a started session?" in the proposal's favour: step 12 stays true under SPEC-1's started-gate and needs no edit. I read the paragraph cold before reading the proposal's rationale and reached the same reading.

FACT: killing the slot's process group — one of the five actions SPEC-3's widened §5.2 list names — has NO implementation in the adapter's `Shutdown` path. The only `Setpgid`/`syscall.Kill` in pkg/adapter/ is the setup-command timeout killer. So `treeErr` is the whole of the cleanup signal in practice and the spec's action list is ahead of the code on that clause. Pre-existing (the sentence predates the proposal) and not a defect of this staging, but a code-lane reviewer verifying "the response reports whether the cleanup completed" should know the process-group half cannot fail because it does not run. EVIDENCE: pkg/adapter/workspace/setup.go:202-215; pkg/adapter/slotlayout/tree.go:57-70.

WATCHOUT: `lenny_adapter_leaked_slots`, which the new SPEC-3 clause names, appears in spec/05:545 and spec/06:160 but in NEITHER docs/reference/metrics.md NOR pkg/alerting/rules. Naming it a third time in spec/05 creates no new docs edit site (the omission is pre-existing), so do not file it as one — but do not assume the metric catalog covers it either. EVIDENCE: `grep -rn lenny_adapter_leaked_slots docs/ pkg/alerting/` returns nothing.

MISTAKE (nearly filed, dropped): "§4.1's rationale says 'this edit retires no vocabulary' while `per-session teardown` occurs ONLY in the sentence being replaced, so the edit retires it from spec/ entirely." The claim is literally false (`grep -rn "per-session teardown" spec/ docs/` returns spec/04:157 alone), but the remedy is one word of proposal rationale, the applied spec is unaffected, and the neighbouring vocabulary item is a standing OPEN four lenses have declined. Below the bar.

MISTAKE (nearly filed, dropped): the "Spec files touched" entry describing the §4.7 row edit as "(first sentence plus one sentence)" when the staged block replaces the row's first TWO sentences with EIGHT. This is the still-open half of the DEFERRED at review-log Deferred item on the file list; a fixer closed the spec/07 §7.2 half of that same DEFERRED and left this one. It is real bookkeeping drift, but it changes nothing in the applied spec, so I left it. If a later fixer touches the §4.7 block, replace the count with a named set as the SPEC-3 entry already does.

MISTAKE (nearly filed, dropped): "the new §5.2 single-session sentence ('the pod retires') contradicts §5.2:545's 'If cleanup fails, the slot is leaked — the pod continues'." This is a close variant of the already-refuted "'on a pod of either concurrency' generalises the whole bullet" finding: the refutation holds that the adjunct attaches to the identification of the cleanup, not to the bullet's other rules, and the bullet's own heading at :542 scopes it to `maxConcurrentSessions > 1`. Do not re-derive.

MISTAKE (nearly filed, dropped): "§7.1's closing sentence ('this reclaim governs the slot state on a pod that is released or reused rather than terminated') scopes the reclaim away from the exclusive pod, whose own sentence two clauses earlier states that reclaim's residue disposition." Trap #24 in Standing context already bars the scoping reading, and the material skeptic granted the boundary imprecision as non-contradictory. Two lenses have now walked into this sentence pair.

USEFUL [Standing context / Traps]: the trap list saved me from filing at least three of the above outright. In particular "MISTAKE, refuted at least eight times: §7.1 says the exclusive pod retires while §6.2:283 and §7.2 step 3 release it back to the pool" caught me mid-derivation on the §7.2-step-3-versus-§7.1-exclusive tension, which the new SPEC-3 sentence makes newly tempting because it restates the retire claim in a third section.

OPEN: the new SPEC-3 sentence puts the exclusive-pod retire claim in a THIRD spec section (§5.2, beside §7.1 and the edge-case list). Every future round that wants to revisit the exclusive-pod disposition now has three sites to keep in step, not two. If a human ever settles the §6.2:283-versus-§6.2-fence question against the proposal, three sentences move rather than one.


### [spec-recheck.2.review-kubernetes.1]

DECISION: returned EMPTY under the Kubernetes-idiom lens — BECAUSE every idiom this lens owns is either
respected by the staging or is a pre-existing condition the staging does not widen. Checked and cleared:
(1) single-writer status ownership — no staged sentence has any component write another's status
subresource; `Sandbox.status.phase` stays WarmPoolController-computed as a level-triggered projection of
`SandboxClaim` existence/binding/disposition (`spec/06_warm-pod-model.md:80`), and the proposal's pod
dispositions are all expressed as claim releases the gateway performs on an object it owns.
(2) status-as-command-channel — the reclaim rides an adapter gRPC (`Shutdown`) and the placement exclusion
rides an in-memory request field, so nothing turns a CRD field into an RPC inbox.
(3) CRD-as-message-bus — `ExcludePod` is deliberately NOT persisted on the session row or on the claim
(non-spec-changes.md:522, and the four rejected alternatives in the review log's Traps), so no per-request
state lands in etcd.
(4) finalizers — none staged; the `leaked` residue leaves a `bound` claim that §4.6.1 orphan GC drains, so
there is no undeletable-object footgun.
(5) admission-webhook coherence — the only webhook the staging touches by reference is the
`SandboxWarmPool` `cleanupTimeoutSeconds / maxConcurrentSessions` rule (`spec/05:545`), which SPEC-3
explicitly leaves as written.
(6) controller-on-hot-path — the compensating `Shutdown` is a gateway→adapter RPC bounded by
`slotCleanupBudget`; it blocks on no reconcile, no work-queue and no leader election.
ALTERNATIVES: I built and dropped three candidates, each recorded below.

FACT: the delta this round is exactly four hunks against `spec-r8`, and the two `spec-recheck-r1-*`
snapshots differ from the live proposal only in the SPEC-3 leak-accounting clause and its rationale
paragraph. `diff -ru scratchpad/cp-snap/0081/spec-recheck-r2 <proposal>` is EMPTY (the r2 snapshot was
taken at the same instant as the live tree), so diff against `spec-recheck-r1-start` or `spec-r8` instead.
EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r1-start/, scratchpad/cp-snap/0081/spec-r8/.

FACT: the round-1 finding "SPEC-3's new clause promises an unclean `Shutdown` answer across the whole
pre-`running` range, but CODE-1 discards the tree-removal error for a start still in flight" IS fixed in
the code lane, not only in the spec lane. The staged response is now
`ExitedCleanly: closeErr == nil && (live || treeErr == nil)` with `live := removed && s.runtimeHoldsLocked(sessionID)`,
so a start still in flight (`started` true, `live` false) surfaces the tree error and the staged §5.2
sentence "the adapter's `Shutdown` response for that reclaim does not report a clean exit" holds across
the whole pre-`running` range. Do not re-derive it as a spec/code drift.
EVIDENCE: non-spec-changes.md:104, :154, :160-172.

FACT: the new SPEC-3 rationale's claim that "the code lane extends the same helper to the create-time
reserved path and to the §7.3 re-attach" is true against the staging: `accountSlotFailure` gains callers
at `bindConcurrentSlot`'s reserved branch and at `resumeOnPod`'s `podBinder.Resume` failure branch.
EVIDENCE: non-spec-changes.md:481-489, :443-465.

FACT: every line citation the delta minted resolves. `spec/29_communication-scenarios.md:586-588` is the
`**Preconditions.**` paragraph ("so the runtime is running"), `:589-591` is the interrupt path's
additional requirement, `:669-674` is step 10's §15.1 citation, `:697` carries "the adapter closes the
session runtime". `spec/04_system-components.md:151` is the field-set derivation paragraph and `:157` is
the `ShutdownRequest` paragraph whose second and third sentences the §4.1 rationale contrasts; the
"per-slot teardown"/"whole-pod teardown" versus "per-session teardown"/"whole-pod scrub" mismatch the
rationale calls pre-existing IS pre-existing at :157.

MISTAKE (built and withdrawn, this lens): "the new SPEC-3 sentence 'On a pod serving one session the
failed attempt releases the pod's claim and the pod retires' is falsified by `spec/06:80`, which projects
`idle` for a claim deleted on a recycling pod under its limits, so the residue rides a pooled pod back
into inventory." Withdrawn: this is the eight-times-refuted family recorded in the review log's Traps
("the residue riding a pooled pod back into inventory" is named there as one of the dresses already
dropped), and the new sentence only relocates §7.1's existing claim into §5.2. Filing it in the new
location would be the same claim on the same evidence.

MISTAKE (built and withdrawn, this lens): "the §6.2 `resuming → cancelled` clause returns a pod holding a
`leaked` slot to the pool, so a pod the leak accounting should be draining re-enters idle inventory."
Withdrawn on two grounds: the held Redis occupancy is exactly what prevents over-assignment into the
unreclaimed resources, so the pod re-enters inventory with reduced rather than phantom capacity; and the
replica-local `slothealth.Tracker` limitation that would make the drain unreachable is already recorded
as a nearly-filed MISTAKE.

MISTAKE (built and withdrawn, this lens): "§7.1 replaces level-triggered reconciliation with an
edge-triggered compensating transaction that has no controller backstop when the gateway replica dies
mid-bind." Withdrawn: the gateway-death case is already on the refuted list, and §4.6.1 orphan-claim GC
plus the whole-pod scrub are the level-triggered backstops for the claim and the on-pod residue
respectively.

USEFUL [Settled: "Ownership is clean"]: this entry plus the `spec/06:80` projection sentence let me clear
the entire single-writer / status-ownership half of the lens in one read instead of walking §4.6.3.

USEFUL [Traps: "MISTAKE, refuted at least eight times"]: it names "the residue riding a pooled pod back
into inventory" explicitly, which is the exact dress I had built. It saved a two-verifier round.

UNVERIFIED: whether a `maxConcurrentSessions: 1` pod with `recycle.enabled: true` can reach the staged
SPEC-3 pre-`running` clause at all. Standing context says the exclusive path goes through `failPhase`
(a drain) and that `prepareAtFinalize` returns nil only for concurrency > 1, so the READY_TIMEOUT and
terminate-at-`ready` classes are the reachable exclusive cases and `spec/05:455` retires their pod. If a
reachable exclusive path exists that does NOT route through `failPhase`, the new sentence's "the pod
retires" arm is the place it would break. A code-lane or mechanism reviewer should confirm the
`failPhase` claim directly rather than from the log.


### [spec-recheck.2.review-mechanism.1]

DECISION: returned EMPTY on the delta and on a full re-sweep of the staging — BECAUSE every
mechanism trace I built out of the round's four new blocks either resolved cleanly against the
tree or landed on an item the standing Traps/Open sections already bar. ALTERNATIVES considered
and dropped: (1) the §5.2 leak clause's unclean-answer route versus §5.2 `**Max retries:**`
excluding only an UNACKNOWLEDGED reclaim — barred, it is the close variant of the already-refuted
"does not acknowledge versus an unclean answer" finding, and the adjudicated reading is that an
unclean answer is not an acknowledgement of the reclaim; (2) §7.2 step 3's unconditional "released
back to the pool" against §7.1's leaked-occupancy hold on a concurrent pool — already carried as
Open items 222 and 229 and judged prose imprecision by four lenses; (3) the terminate-at-`ready`
case falling in SPEC-3's range while the new one-session branch points at §7.1's *failed attempt*
disposition — dissolved by Open 228, which fixes "abandoned" in this proposal as GATEWAY
abandonment, so the branch's antecedent is a failed attempt by construction.

FACT: the new SPEC-3 clause's normative claim ("the adapter's `Shutdown` response for that reclaim
does not report a clean exit") is consistent with staged CODE-1 in the direction the clause states.
CODE-1's response is `ExitedCleanly: closeErr == nil && (live || treeErr == nil)`, and on the
pre-`running` path `live` is false by construction, so `treeErr` is surfaced. The converse is NOT
tight: for a start still in flight `started` is true, so `Runtime.Close` runs and a `closeErr`
alone makes the answer unclean even when the slot release completed, which the gateway then
classifies `leaked` and holds occupancy for a slot the pod did release. That over-classification
is the shipped session-end behaviour extended (`leaked = err != nil || !cleanly`), so it is below
the bar rather than absent. EVIDENCE: non-spec-changes.md:104,154; pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543.

FACT: the delta's arithmetic claim checks out. `UnhealthyThreshold` is `(maxConcurrent+1)/2` in
integer division, so at `maxConcurrentSessions: 2` a single windowed `RecordFailure` trips
`Unhealthy` and drains the pod inside `applySlotRetryPolicy`. The rewritten edge-case bullet one
now says exactly that and the old "a blob-store outage does not retire healthy pods" clause is
gone, closing the standing DEFERRED at review-log.md:260. EVIDENCE:
pkg/gateway/runtime/slothealth/slothealth.go:210-221; pkg/gateway/sessionserver/start.go:2849-2873.

FACT: the new §5.2 rationale's claim that "the gateway's slot-failure accounting covers the
retry-placed bind path today" is true as written. `applySlotRetryPolicy` already runs
`MarkLeaked` + leak gauge + `RecordLeak` on a failed release and `RecordFailure` on a clean one,
then `Unhealthy → DrainSandbox → replacement → Forget → ForgetPod → zero-gauge`. EVIDENCE:
pkg/gateway/sessionserver/start.go:2834-2873.

FACT: every line citation the round's two new rationale blocks mint resolves. spec/04:151 is the
field-set derivation sentence and :157 the `ShutdownRequest` paragraph; spec/29:586-588 is the
`**Preconditions.**` paragraph ("so the runtime is running"), :589-591 the interrupt path's
additional requirement, :669-674 step 10's "valid in any non-terminal state" plus its §15.1
citation, and :697 carries "adapter closes the session runtime and the pod is replaced" (the
words "the adapter" straddle the 696/697 wrap; not a false citation).

FACT: all fifteen "text to replace" blocks in the staging still match their target spec file
exactly once, and every "replace it with" block occurs zero times. Re-derived mechanically this
round with a script over the fenced blocks in spec-changes.md against spec/04, 05, 06, 07 and 29.
The tree has not moved under the anchors since the last sweep.

WATCHOUT: `diff -ru scratchpad/cp-snap/0081/spec-recheck-r2 <proposal>` is EMPTY this round — the
named snapshot is byte-identical to the live proposal. The delta this loop exists for is only
visible against `scratchpad/cp-snap/0081/spec-r8`. review-log.md:128 already records that several
lenses lost time on an empty diff; the snapshot the prompt names is not always the one that
differs. EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r2/.

USEFUL [review-log.md Standing context, Traps 148/149/157/161/175/181]: the dead-end list did real
work this round. Three candidates I built (the adapter cannot see the pre-start leak; the withheld
report versus §4.7/§12; the `leaked` gloss at §5.2:561 and §6.2:148 not describing an
unacknowledged reclaim) were each already refuted there with the evidence attached, so none cost a
verification pair.

USEFUL [review-log.md Open 228]: "'abandoned' in this proposal means the GATEWAY abandoned the
bind by failing it" is the single sentence that dissolves the strongest candidate I had against the
new §5.2 clause. Keep it near the top of Standing context; without it the one-session branch reads
as asserting a disposition for client-terminate-at-`ready`, which §7.1 does not govern.


### [spec-recheck.2.review-operational.1]

DECISION: returned an empty findings list for the operational lens on this recheck — BECAUSE the delta (edge-case bullet 1's windowed-failure rewrite, SPEC-1's §4.1 and §29.4 step-12 rationales, SPEC-3's new leak-accounting clause and its rationale) introduces no condition, no alert, and no metric, and the one metric it names is one the shipped spec already names twice. ALTERNATIVES: three candidates built and dropped, recorded below.

FACT: there is NO alert anywhere on slots. `grep -rn "slot" pkg/alerting/rules/rules.go` returns nothing, and `grep -rn "leaked" spec/16_observability.md docs/reference/metrics.md` returns nothing. The alert-catalog / runbook surface is untouched by anything 0081 stages, so an operational lens has no alert work on this proposal. EVIDENCE: pkg/alerting/rules/rules.go (1786 lines, no slot match); spec/16_observability.md.

FACT: `lenny_adapter_leaked_slots` is absent from §16's metric inventory while §5.2:545 and §6.2:160 both name it. That gap is pre-existing and 0081's new SPEC-3 clause is the third spec site to name the gauge, not the first, so it is not a missing-edit-site under (d). EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; spec/06_warm-pod-model.md:160; spec/16_observability.md (no match).

FACT: `Metrics.IncSlotFailure` (the `lenny_slot_failure_total` emitter, spec/16:14) has ZERO production callers — only its own definition. `applySlotRetryPolicy` feeds the health ledger and `lenny_slot_pod_replacement_total`, never the per-slot failure counter. Pre-existing and outside 0081; a later operational round should not read "the slot-failure metric covers this path" into any staging. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go:1175; grep IncSlotFailure across pkg/ returns the definition only.

FACT: `lenny_slot_pod_replacement_total` is registered with labels `{pool}` alone while spec/16:15 declares `pool` and `k8s_pod_name`. Pre-existing spec/code label divergence, not created by 0081. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:212-216; spec/16_observability.md:15.

MISTAKE (nearly filed, withdrawn): "the applied spec states the leaked arm's accounting but leaves the CLEAN arm's windowed-failure accounting unstated, so §5.2's trigger (which counts slots that are `failed`) never reaches a cleanly-reclaimed pre-`running` bind while the code's `RecordFailure` trips the pod at `maxConcurrentSessions: 2`." Withdrawn as pre-existing plus prose incompleteness: §6.2's only route into `failed` is `running ──→ failed` (spec/06_warm-pod-model.md:147), so a pre-`running` bind failure had no `failed` route BEFORE the proposal either, and SPEC-4 removes nothing. The proposal's own edge-case bullet 1 concedes the behaviour explicitly, so it is recorded rather than hidden. EVIDENCE: spec/06_warm-pod-model.md:147; spec/05_runtime-registry-and-pool-model.md:561; spec-changes.md:98-104.

MISTAKE (nearly filed, withdrawn): "§6.2:160 makes the gauge adapter-sourced (`/healthz` health metadata), so the pre-`running` leak — which the adapter cannot know about — cannot reach it." Withdrawn: the gateway already emits the gauge for gateway-determined leaks on the shipped `applySlotRetryPolicy` path, and the standing Traps entry ("Dead end: 'the adapter cannot see this leak'") records two lenses refuting it. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-225; pkg/gateway/sessionserver/start.go:2844.

FACT (verified, saves a round): every citation the DELTA mints resolves. spec/04_system-components.md:151 is the field-set derivation sentence and :157 is the `ShutdownRequest` paragraph; spec/29_communication-scenarios.md:586-588 is the `**Preconditions.**` base ("so the runtime is running"), :589-591 is the interrupt path's additional requirement, :669-674 is step 10's "valid in any non-terminal state", and :697 is step 12's "the adapter closes the session runtime". The §29.4 step-12 rationale's argument holds on the text as written.

FACT: the drain-gate split is spec-consistent. Staged CODE-1 moves `drainViaLifecycle` inside the `started` block, and §15.4.2's own state machine carries an `ACTIVE ──→ TERMINATED (session ends normally)` edge beside `ACTIVE ──→ DRAINING`, so skipping DRAINING for a never-started session contradicts nothing in §15.4.2. Do not file the drain gate against §15.4.2. EVIDENCE: spec/15_external-api-surface.md:1691-1697; pkg/adapter/session.go:259.

FACT: spec/12's two `sessions_served` statements both scope to "a session release (`ReportSessionScrub`)", so SPEC-3's withheld report leaves them true verbatim. Adds two sites to the standing five-site "withheld report" dead end. EVIDENCE: spec/12_storage-architecture.md:481, :494.

FACT: SPEC-3's new action-list item (cancel the §4.9 direct-delivery lease-expiry timers) is consistent with §4.9's only statement of those timers, the `anthropic_direct` row, which arms a per-lease timer that on firing deletes `/run/lenny/slots/{sessionId}/credentials.json` and reports `AUTH_EXPIRED` on CH-ADAPTEREVENTS. Cancelling on slot cleanup prevents a spurious `AUTH_EXPIRED` (rotationTrigger `fault_auth_expired`) for a dead session, so the edit tightens the operational story rather than breaking it. EVIDENCE: spec/04_system-components.md:1169, :1439.

UNVERIFIED: whether `docs/reference/metrics.md` should gain a `lenny_adapter_leaked_slots` row and whether §16's inventory should carry it. Out of scope for the spec loop (docs remedy) and pre-existing; the non-spec loop or a separate proposal owns it.


### [spec-recheck.2.review-performance.1]

DECISION: Returned an empty findings list for the performance/scalability/failure-mode lens on the
spec-r8 → current delta — BECAUSE every candidate I built either resolved as pre-existing, was already
routed to a human as an open decision, or failed the default-to-refuted test — ALTERNATIVES: I built and
withdrew four candidates, listed below as MISTAKE entries so the next perf lens does not rebuild them.

FACT: The delta this round is exactly four hunks in the spec-changes file and nothing else in the
directory changed. `diff -ru scratchpad/cp-snap/0081/spec-recheck-r2 proposals/0081...` returns EMPTY
because that snapshot was taken at 09:41, identical to the live tree; the real delta is
`diff -u scratchpad/cp-snap/0081/spec-r8/...spec-changes.md proposals/0081/...spec-changes.md`.
EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r2 (mtimes all 09:41), spec-r8 (06:01)

FACT: The whole-pod replacement threshold is `ceil(maxConcurrentSessions/2)`, so at
`maxConcurrentSessions: 2` it is 1 — one windowed failure or one leak drains the pod. The new edge-case
bullet's arithmetic is correct. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561 (trigger),
pkg/gateway/runtime/slothealth/slothealth.go:136-140 (`Unhealthy` >= `UnhealthyThreshold`)

FACT: Every §29.4 citation in the new step-12 rationale resolves.
`spec/29_communication-scenarios.md:586` carries "completed, so the runtime is running", `:697` carries
"adapter closes the session runtime and the pod is replaced", `:671` carries "valid in any non-terminal
state". The §4.1 rationale's `spec/04_system-components.md:151` (scope derived from the field set) and
`:157` (the ShutdownRequest paragraph, whose second and third sentences already use different names)
also resolve. Do not re-open these.

FACT: `lenny_adapter_leaked_slots` is real and is published by the GATEWAY, not the adapter, despite
§6.2 attributing it to the adapter's `/healthz`. `applySlotRetryPolicy` marks the leak and calls the
gauge only on the `ReleaseSlotReservation` error arm today; the code lane extends it to the
`sbe.Leaked` arm and to the create-time-reserved and §7.3 re-attach callers, so SPEC-3's new gauge
clause is backed rather than aspirational. EVIDENCE:
pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223,
pkg/gateway/sessionserver/start.go:2836-2856, non-spec-changes.md:443-490

FACT: The reclaim is bounded in the code lane at the §5.2 per-slot cleanup budget
(`max(cleanupTimeoutSeconds/maxConcurrentSessions, 5)`, minimum 5s), taken on a
`context.WithoutCancel` derivative, so "the spec states no deadline" is a deliberate delegation with a
concrete figure rather than an unbounded blocking call. EVIDENCE: non-spec-changes.md:304-335

FACT: The §16.5 session-creation latency SLO (P99 < 500ms) is measured "receipt through `session_id`
response", so a bind that fails never emits a sample and the reclaim's added ≤5s cannot burn that
SLO's budget. This closes the obvious "the reclaim breaches an SLO" line of attack.
EVIDENCE: spec/16_observability.md:625

MISTAKE: I built a finding that the reclaim's synchronous `Shutdown` adds up to
`2 × slotCleanupBudget` to a failed create at the top tier and breaches the creation-latency SLO. It
does not: the SLO samples only successful creates (see the FACT above), and the phase-breakdown metric
`lenny_session_creation_duration_seconds` has no phase the reclaim lands in. Withdrawn.

MISTAKE: I built a finding that the leaked-slot accounting is per-gateway-replica in memory
(`slothealth.Tracker`, `slotstate.Registry`), so at Tier 3 (~20 replicas) reaching `ceil(mCS/2)` leaks
on one replica needs ~20× the fleet-wide leaks, diluting the bound this proposal leans on. Withdrawn:
§6.2:160 names "the leaked portion of the pod's Redis slot-counter occupancy" as the equivalent
persistent count, so the APPLIED SPEC is coherent and the divergence is a pre-existing code
conformance gap whose remedy is not in the staged spec edits.

MISTAKE: I built a finding that a cleanly-compensated pre-`running` bind failure cannot reach the §5.2
whole-pod replacement trigger, because SPEC-4 routes that slot `receiving_uploads → slot_cleanup →
released` and the trigger counts only `failed` and `leaked` slots, while §6.2's fence reaches `failed`
only from `running`. That would falsify the new edge-case bullet's "the windowed failure counter still
records it, and at `maxConcurrentSessions: 2` a single windowed failure already reaches the §5.2
whole-pod replacement threshold" (spec-changes.md:102-104). Withdrawn on the counter-reading: §5.2's
slot retry policy is triggered by "when a slot fails" with no post-`running` restriction, the whole-pod
replacement trigger is a bullet inside that same policy, and this proposal's own §5.2 `Max retries:`
edit already treats a failed bind as a slot failure that policy handles. Both readings are available,
so default-to-refuted applies. A future lens that wants to revive this needs a spec sentence that
excludes a bind failure from the `failed` category, not just the fence's `running ──→ failed` edge.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:553 ("When a slot fails"), :561 (trigger),
spec/06_warm-pod-model.md:146-147 (fence `running ──→ failed`)

MISTAKE: I built a finding that a §7.2 mid-resume cancel returns the half-claimed replacement pod "back
to the pool" carrying an unacknowledged reclaim's residue, unbounded at `maxConcurrentSessions: 1`
where no `leaked` sub-state exists. Withdrawn: the surviving clause in the staged step 3 keeps "the
pool's default post-session scrub", which is the bound, and the release-vs-retire reading of §6.2's
pre-attached disposition was already adjudicated against an earlier finding in this loop.

WATCHOUT: Open decision 7 in the summary (`summary.md:243-267`) already owns the Redis-rehydration
hole — a failed bind's leaked slot has no active session row, so a Redis restart rehydrates the counter
from `GetActiveSlotsByPod` and silently frees the held occupancy. That is the single strongest
durability defect this lens finds, and it is routed to a human, so it is NOT reportable. Do not rebuild
it. EVIDENCE: proposals/0081.../summary.md:243-267,
spec/05_runtime-registry-and-pool-model.md:551 ("Post-recovery rehydration atomicity")

UNVERIFIED: §5.2's whole-pod replacement trigger enumerates the `leaked` cause as "(cleanup timeout
exceeded)" while the staged §5.2 and §7.1 add two further routes into `leaked` (an unacknowledged
reclaim, and a `Shutdown` answer that does not report a clean exit). The trigger bullet is in no edit
list. I judged this pre-existing under-enumeration (§5.2:545 already says "If cleanup fails, the slot
is leaked", which is broader than the trigger's parenthetical) and did not file it. A later lens with
an appetite for enumeration-completeness findings should note the material skeptic has already refuted
one of these in this proposal on exactly that ground.


### [spec-recheck.2.review-reliability.1]

DECISION: returned EMPTY. — BECAUSE the only delta since spec-recheck-r1-start is the SPEC-3 §5.2 append's
concurrency split plus its rationale rewrite, and every proposition in it verifies against the tree; every
other reliability angle I could construct is already in the Settled/Traps/refuted lists. — ALTERNATIVES:
four candidates built and withdrawn, each recorded below.

FACT: the delta is ONE hunk. `diff -u scratchpad/cp-snap/0081/spec-recheck-r1-start/...spec-changes.md` against
the live file returns exactly two changed blocks, both in SPEC-3: the appended §5.2 paragraph's
"cleanup that does not complete" clause split by pod concurrency, and its following rationale paragraph.
`spec-recheck-r2` is byte-identical to the live proposal (taken at 09:41 alongside it), so diffing against
it returns nothing — use `spec-recheck-r1-start` instead. EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r2/
(mtimes equal to proposals/.../ mtimes).

FACT: the delta's every citation resolves. `lenny_adapter_leaked_slots` and the persistent-count semantics
are spec/06_warm-pod-model.md:160; the whole-pod replacement trigger and "If cleanup fails, the slot is
leaked" are spec/05_runtime-registry-and-pool-model.md:545 (concurrency-scoped bullet), both BELOW the
`**Scrub model.**` paragraph at spec/05:453, so "stated below" is accurate. EVIDENCE: spec/06:160;
spec/05:453,:545.

FACT: the delta's rationale claim "the code lane extends the same helper to the create-time reserved path
and to the §7.3 re-attach, so the clause holds on all three" is TRUE against the staged code. The shipped
`applySlotRetryPolicy` tail already does MarkLeaked/leakGauge/RecordLeak on `relErr != nil` and
`RecordFailure` otherwise (pkg/gateway/sessionserver/start.go:2833-2856), and CODE-5 stages three callers of
the extracted `accountSlotFailure`: `applySlotRetryPolicy` (`sbe.Leaked || relErr != nil`),
`bindConcurrentSlot`'s `BindReservedSlot` branch (`sbe.Leaked`, with `BindReservedSlot` itself setting
`Leaked=true` when its own release fails), and `resumeOnPod` (`sbe.Leaked`, with `Binder.Resume` folding
`compensation leaked || relErr != nil` into it). All three discriminators fold the release failure in.
EVIDENCE: non-spec-changes.md:403,:481-489; pkg/gateway/sessionserver/start.go:2833-2856.

MISTAKE (earlier round, now FIXED — do not re-file): "SPEC-3's clause promises an unclean `Shutdown` answer
across the whole pre-`running` range but CODE-1 discards the tree-removal error for a start still in flight".
CODE-1 now reads `ExitedCleanly: closeErr == nil && (live || treeErr == nil)` with `live := removed &&
s.runtimeHoldsLocked(sessionID)`, so `treeErr` is surfaced for the WHOLE pre-`running` range including a
start in flight. EVIDENCE: non-spec-changes.md:104,:154,:157-176.

FACT that closes a whole family of candidates: `exited_cleanly` is read on the CONCURRENT slot-release path
ONLY. `Binder.ReleaseSlot` computes `leaked = err != nil || !cleanly`
(pkg/gateway/podlifecycle/podsession/slotbinder.go:541-543), but session-mode `Binder.Release` — the
exclusive-pod session end — calls `b.shutdownAdapter(...)` best-effort and never reads the response
(binder.go:1957-2008). So CODE-1's widened `exited_cleanly` CANNOT mark a slot `leaked` on a
`maxConcurrentSessions: 1` pod, which is what makes the delta's "On a pod serving one session §6.2 offers no
`leaked` sub-state" consistent with the code rather than merely asserted. EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:536-543; binder.go:1957-2008.

MISTAKE (candidate built and withdrawn): "the exclusive arm asserts a pod retirement for a pre-`running`
cleanup class that has no failed attempt (READY_TIMEOUT, terminate-at-`ready`, §11.4 revoke), so residue
survives onto a recycled exclusive pod." Withdrawn: on a recycling exclusive pool the session end drives
occupancy to zero, `Release` patches `bound → recycling` and sends the recycle `Shutdown`, and §5.2's
whole-pod scrub step 0 purges every `/run/lenny/slots/{sessionId}/credentials.json` while step 2 does
`rm -rf /workspace/slots/*`. The residue is swept by the recycle boundary, so "does not outlive the pod" is
true even where the stated ground is not. EVIDENCE: spec/05:455,:461,:467; binder.go:1974-1995.

MISTAKE (candidate built and withdrawn): "a crash between the reclaim and `releaseSlotReservation` leaves
phantom Redis occupancy no path reclaims." Withdrawn: today's code already releases after the failure, so
the window is pre-existing; the ordering change only widens it by the `slotCleanupBudget`, and that is the
performance lens's capacity math rather than a recovery-mechanism defect.

MISTAKE (candidate built and withdrawn): "the §7.3 exclusive-pool re-attach is the one reachable §7.1
reclaim on an exclusive pod, and there `resumeOnPod` releases no pod claim, so the delta's 'the failed
attempt releases the pod's claim and the pod retires' is false there." Withdrawn as a close variant of the
already-refuted §7.1 finding ("§7.1's exclusive-pod disposition is unreachable on the §7.3 re-attach"): the
delta delegates to §7.1 verbatim, so the same refutation transfers and the residue is bounded by §4.6.1
orphan-claim GC. EVIDENCE: standing-context Settled "Binder.Resume's failure branch releases nothing on an
exclusive pool"; non-spec-changes.md:485-489 (the accounting guard is the non-empty slot id, i.e. concurrent
pools only).

USEFUL [standing context / Settled + Traps]: the Settled block's predicate-nesting entry
(entry ⊃ bound ⊃ started ⊃ runtimeLive) and the `SlotClaimer.ReleaseSlot(leaked=true)` early-return entry
between them killed three of my four candidates before I opened a file. The Traps block's
"Do not file either `leaked` gloss" and "the replica-local leak ledger" entries killed two more.

OPEN (unchanged by this round, restated so it is not lost): the delta narrows but does not close the
standing item "'on a pod of either concurrency' and the leaked sentence". It now scopes the leak accounting
explicitly by pod concurrency, which is what that item asked for; whoever compacts next can retire it.


### [spec-recheck.2.review-security.1]

DECISION: Returned an empty findings list for the security lens on the spec-recheck-2 delta — BECAUSE every candidate I built dissolved when I read the tree, and the four hunks in the delta (edge-case bullet 1's threshold arithmetic, the §4.1 rationale, the §29.4 step-12 rationale, SPEC-3's leak-accounting clause + rationale) all verify. ALTERNATIVES: I considered and dropped five candidates, each recorded below so the next agent does not re-derive them.

FACT: `onSlotLeaseExpired` bails immediately when the slot is no longer in the registry (`st, ok := s.slotStateLocked(slotID); if !ok { return }`), so leaving a §4.9 direct-mode expiry timer armed after `deregisterSlotLocked` would NOT scrub a credential file whose `RemoveTree` failed. The "cancel the timers" action SPEC-3 adds to the §5.2 list is therefore not what defeats §4.9's MUST; the entry deletion is, and that is shipped on every teardown path. Do not file "cancelling the timer strands the credential file" — the remedy does not work as stated. EVIDENCE: pkg/adapter/slotcreds.go:250-256; pkg/adapter/slotsession.go:174-189

FACT: `ExitedCleanly` in the staging is `closeErr == nil && (live || treeErr == nil)` with `live := removed && s.runtimeHoldsLocked(sessionID)`, NOT `started`. A start still in flight is `started` but not `live`, so its tree-removal error IS surfaced and SPEC-3's new sentence ("the adapter's `Shutdown` response for that reclaim does not report a clean exit") holds for all three pre-`running` residue classes including the in-flight start. The staging argues this explicitly. EVIDENCE: non-spec-changes.md:104, :154, :160-172

FACT: `ReleaseSlotReservation` calls `SlotClaimer.ReleaseSlot(ctx, name, recycle=false, leaked=false)`, and at `remaining == 0` with `recycle=false` that DELETES the per-pod claim, which the §4.6.1 occupancy projection turns into `claimed → draining`. So on a sole-occupancy pod the failed-bind release retires the pod. This is why the racing-start bullet's "until the pod retires" bound is not false on the sole-occupancy path. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:493-503; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:845-885

FACT: §5.2 whole-pod scrub step 1 is `kill -9 -1` as the sandbox user and step 2 is `rm -rf /workspace/slots/*`, so the orphaned runtime the racing-start ordering leaves behind is killed at the next occupancy-zero recycle boundary, not carried into the pod's reuse. Recycling pods are also tenant-pinned. The "orphan survives into another tenant's session" escalation of the racing-start bullet does not hold. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:465-467, :473; spec/06_warm-pod-model.md:158

FACT: the §5.2 whole-pod credential purge (step 0) runs only at the occupancy-zero recycle boundary, and a `leaked` slot holds occupancy until pod termination, so step 0 is structurally unreachable on a pod carrying a leaked slot. SPEC-3's rationale sentence "the whole-pod credential purge at §5.2 scrub step 0 ... stays the backstop for anything a per-slot cleanup left behind" is therefore loose for the leaked case. I did NOT file it: the sentence is rationale prose that predates r8, the specific threat step 0 names (deployer `cleanupCommands` reading a stale credential file) cannot fire on a pod that never reaches the recycle boundary, and the residue dies at pod termination. A future agent who wants to tighten it should do so as a DEFERRED prose correction rather than a finding. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455, :461; pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836; spec/06_warm-pod-model.md:160

WATCHOUT: the §15.4.2 graceful-shutdown signal is gated on `boundRemains`, which reads `other.sessionID != ""`. A co-tenant still in the workspace-prep stage has an empty `sessionID`, so it does NOT hold the signal off. The proposal's own non-spec staging names this as a harm ("a registered-but-unbound co-tenant about to call `StartSession` does not hold off") while the staged §4.7 sentence keeps the bound-entry gate. It is NOT a live defect after the change, because the staging moves `drainViaLifecycle` inside `if started`, so the signal now needs started AND no-bound-entry, and the residual case is an ordinary started session end, which is shipped and pre-existing. Do not file it. EVIDENCE: pkg/adapter/slotsession.go:182-187; non-spec-changes.md:108-119, :197-200

FACT: the concurrent slot bind path performs NO §4.9 credential-lease revoke on failure — `materializeSlot` closes the connection and records the failure counter at each of the five stages and never calls `releaseCredentials`, unlike the exclusive path's `failPhase`. A `StartSession` failure after `assignSlotCredentials` therefore strands the lease's active-session slot. This is a pre-existing code gap that 0081's staged §7.1 reclaim (pod-side `Shutdown` + slot-reservation release) does not close and does not claim to. I judged it out of scope for this loop, but it is a real credential-pool-exhaustion bound and a candidate for a separate proposal. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:284-325 vs pkg/gateway/podlifecycle/podsession/binder.go:1072-1082

FACT: every line citation the delta added checks out — spec/29_communication-scenarios.md:586-588 ("so the runtime is running"), :589-591 (interrupt path's additional requirement), :669-674 (step 10), :697 ("the adapter closes the session runtime"); spec/04_system-components.md:151 (scope derived from field set) and :157 (the shipped third sentence's "per-session teardown"/"whole-pod scrub"). The `lenny_adapter_leaked_slots` gauge SPEC-3 names is real (spec/06_warm-pod-model.md:160) and the whole-pod replacement trigger really is stated below the Scrub model paragraph (:561 vs :453). The `maxConcurrentSessions: 2` arithmetic in edge-case bullet 1 is right: `ceil(2/2) = 1`.

USEFUL [the orchestrator's standing hazard note]: "verify absence by reading the tree" paid off three times here — the credential-timer, the ExitedCleanly-vs-started, and the racing-start-orphan candidates all read as findings from the proposal text alone and all died on the first file I opened.


### [spec-recheck.3.review-applicability.1]

FACT: EVERY "reads, verbatim" anchor block in the spec staging matches its target file exactly once. Verified mechanically by extracting all 28 fenced blocks from spec-changes.md and counting occurrences across spec/04, 05, 06, 07, 15, 29: the 12 anchor blocks each returned COUNT 1 and every replacement block returned NOT FOUND (as expected). — EVIDENCE: proposals/0081_.../0081_....spec-changes.md fenced blocks 0,2,5,8,10,12,14,16,18,20,22,24 against spec/04_system-components.md:157,686,853; spec/05_runtime-registry-and-pool-model.md:453,545,555; spec/06_warm-pod-model.md:234; spec/07_session-lifecycle.md:23,210,213,214,414.
FACT: every markdown anchor the staged spec text links to already resolves in the tree. `#1542-rpc-lifecycle-state-machine` and `#479-startup-sequence-for-type-agent-runtimes` are the only two with a single existing user each, and both are real headings. — EVIDENCE: spec/15_external-api-surface.md:1686,2017; spec/04_system-components.md:848; spec/README.md:36.
FACT: the §29.4 step-13 anchor string occurs TWICE inside §29.4 itself (step 6 and step 13), not only at the four repo-wide sites the standing context lists. The instruction resolves only because it says "In §29.4's numbered step 13". — EVIDENCE: spec/29_communication-scenarios.md:645 (step 6) and :711 (step 13).
FACT: no existing tier-11 gate breaks on the SPEC-4 fence edit. `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` checks a fixed four-edge `generalSlotEdges` list is present in the either-concurrency block and absent from the concurrent-occupancy block; a fifth edge added to the general block is invisible to it. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-36,:55-75.
FACT: `/run/lenny/slots/{sessionId}/credentials.json` as SPEC-3 spells it is the established path at eleven spec sites, and §5.2's recycle-lifecycle paragraph and scrub step 6 already presuppose per-slot credential removal, so SPEC-3's widened action list mints no new path literal. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455,461,471; spec/04_system-components.md:793,914,1169.

DECISION: filed exactly one finding, on SPEC-3's NEW exclusive-pod arm (spec-changes.md:465, "On a pod serving one session the disposition is the one [Section 7.1] states: the failed attempt releases the pod's claim and the pod retires"). BECAUSE the sentence's antecedent is "a cleanup on that path", and "that path" is defined two sentences earlier as a bind ABANDONED OR failed, while §7.1's paragraph binds only "a gateway bind attempt that fails". ALTERNATIVES: I considered and dropped (a) the §7.2 cancel-versus-fail predicate question (a cancelled restoration RPC is a failing RPC, and §7.1 explicitly covers a cancelled context), (b) the §6.2 `resuming → completed` sibling bullet as a missed edit site (it inherits "the same sequence" from the cancel bullet the proposal does edit), and (c) the checklist (out of scope this loop).

WATCHOUT: the scope of "a bind abandoned" is NOT open. This loop's own accepted refutation of the "no-report exception is narrower than the started gate" finding adjudicated that it reaches the READY_TIMEOUT, terminate-at-ready and §11.4-revoke teardowns. Anyone reading S3-S5 of the §5.2 append as implicitly scoped to a §7.1 reclaim is reading against that adjudication, and if they take that reading instead then the abandonment arm's incomplete-cleanup accounting is stated nowhere for an exclusive pod. Both readings leave a hole; only the placement of the hole changes. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:465 (S2 "abandoned or fails", S3 "A cleanup on that path", S5 "the failed attempt").
WATCHOUT: the false half of S5 is narrower than it first looks. On an exclusive pod a READY_TIMEOUT or a revoke DOES retire the pod, via spec/05:455 "A session that ends in failure or a crash always retires its pod regardless of recycle settings". The surviving class is a client terminate at `ready` on a recycling exclusive pool, where the session ends `completed`, the pod is held through `reserved` and serves the next session. Build the finding on that class alone; the other two are covered. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455.

UNVERIFIED: whether the occupancy-zero whole-pod scrub is the disposition S5 should name for the exclusive-pod abandonment arm. §5.2 scrub step 6 marks the scrub failed if `/workspace/slots/` or `/run/lenny/slots/` is non-empty, which routes the residue into `maxScrubFailures` rather than into a leak count, so it looks like the right pointer — but nobody has checked that a per-slot tree left by a half-completed cleanup actually trips step 6 rather than being swept by scrub steps 1-6 first. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:461,471.

USEFUL [Standing context / Traps]: the "§29.4 step-13 anchor is NOT unique" trap and the "do not file either `leaked` gloss" trap each saved a candidate write-up. The `slot_cleanup ──→ leaked` scoping-asymmetry trap is what kept me from filing the exclusive-pod leak gap as its own finding rather than as the narrower falsity in S5.


### [spec-recheck.3.review-citations.1]

DECISION: filed one finding only, on the stale "(first sentence plus one sentence)" gloss for the SPEC-1 §4.7 edit in "Spec files touched" — BECAUSE every anchor, every file:line citation and every section attribution in the staging verified accurate against the tree, and the only self-contradiction left in this file is that count — ALTERNATIVES: rejected filing the spec/05 named-set entry ("the per-slot cleanup pointer and the cleanup-outcome report rules appended") as also incomplete after the round-8 leak-accounting clause, because "rules" is loose enough to read as covering it; rejected filing the SPEC-3 exclusive-pod pointer's reach over READY_TIMEOUT / terminate-at-`ready` teardowns (see OPEN below).

FACT: the snapshot named in this round's brief (`scratchpad/cp-snap/0081/spec-recheck-r3`) is byte-identical to the live proposal, and so is `spec-recheck-r2`. The only spec-changes delta since a reviewer last read it is `spec-recheck-r1-prefix` → now: the SPEC-3 §5.2 append's leak-accounting clause was concurrency-split (concurrent pod → `leaked`/occupancy/trigger/gauge; single-session pod → §7.1's claim-release-and-retire), and its rationale paragraph was rewritten and gained the "reaches every bind path §7.1 binds" sentence. Diff against `spec-recheck-r1-prefix`, not against the named snapshot. EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r1-prefix/…spec-changes.md vs proposals/0081_…/…spec-changes.md:465,478-489

FACT: every anchor and citation in the current staging re-verified clean this round, so a later citation lens can skip them. §4.1 third sentence (spec/04:157) and the derivation sentence it cites (:151); §4.7 `Shutdown` row opening (:686); §4.7.9 step 5 (:854); §5.2 `**Scrub model.**` (:453), `**Slot cleanup:**` action list (:545), `**Max retries:**` (:555), whole-pod replacement trigger (:562, under the `maxConcurrentSessions > 1` retry-policy heading, so "stated below" and "no trigger at concurrency 1" both hold); §6.2 fence heading and `receiving_uploads ──→ running` (:150-153), `**\`leaked\` slot semantics.**` (:160, occupancy hold + threshold + `lenny_adapter_leaked_slots` gauge all present), pre-attached failure policy (:283), `resuming → cancelled` clause (:234), sibling gloss (:236); §7.1 atomicity parenthetical (:23) and the continuation line (:24); §7.2 preamble premise, step 2 tail, step 3 (:210,:213,:214); §7.3 list tail (:414); §29.4 Preconditions (:586-591), step 10 (:669-674), step 12 (:697), step 13 tail (:710-711). The §29.4 rationale's three line citations (`:586-588`, `:589-591`, `:697`) and §15.1's terminate row (spec/15:649, "Valid in any non-terminal state") all say what the proposal says they say.

FACT: the round-8 fix to CODE-1's response (`ExitedCleanly: closeErr == nil && (live || treeErr == nil)`, non-spec-changes.md:154) is what makes SPEC-3's new "the adapter's `Shutdown` response for that reclaim does not report a clean exit" true across the WHOLE pre-`running` range, including a start still in flight. An earlier round filed the opposite when the gate read `started`. Do not re-file it; check the `live` gate first. EVIDENCE: non-spec-changes.md:154,:160-172

FACT: the SPEC-3 rationale's "the gateway's slot-failure accounting covers the retry-placed bind path today" is code-true. `applySlotRetryPolicy` already carries the whole MarkLeaked / leakGauge / RecordLeak / RecordFailure / Unhealthy → DrainSandbox tail. EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884

OPEN: does SPEC-3's new exclusive-pod pointer over-reach? Its antecedent is "a bind abandoned or fails after its slot enters `receiving_uploads` and before it reaches `running`", and a `maxReadyTimeoutSeconds` teardown or a terminate at `ready` on an exclusive pod is in that range on the reading an earlier verifier adopted, yet §7.1's paragraph binds only "a gateway bind attempt that fails", so §7.1 states nothing for those. The CONSEQUENCE is still right (spec/05:455 retires a pod whose session ends in failure), only the stated ground is narrow. Not filed because the §7.1 trigger noun-phrase is already a recorded OPEN for a human ("§7.1's trigger noun-phrase") and the reading is unsettled. EVIDENCE: spec-changes.md:465; spec-changes.md:262; spec/05_runtime-registry-and-pool-model.md:455

USEFUL [Traps, "The snapshot diffs have been empty for most rounds"]: exactly right again, and it saved the round. Two of the three snapshots this brief and the loop point at are byte-identical to the live proposal.


### [spec-recheck.3.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens on the spec staging — BECAUSE every externally-consumed representation the staged edits touch is either unchanged, already spec'd before this proposal, or has its only remaining mirror in a file this loop may not edit (proto barred by programme rule S-2; docs owned by the non-spec loop) — ALTERNATIVES: considered filing the `lenny_adapter_leaked_slots` attribution, the "connection the failed stage still holds" lifetime, and the §4.7 `exited_cleanly` gap; each is refuted below.

FACT: the delta in this recheck is confined to spec-changes.md and is exactly two hunks: SPEC-3's scrub-model append splits its "cleanup did not complete" clause into a concurrent arm (`leaked`, occupancy, replacement trigger, `lenny_adapter_leaked_slots`) and a single-session arm (points at §7.1's pod-retires disposition), and the SPEC-3 rationale paragraph below it is rewritten to match. `diff -u scratchpad/cp-snap/0081/spec-recheck-r1-prefix/...spec-changes.md` against the current file is the whole delta; spec-recheck-r2 and spec-recheck-r3 snapshots are byte-identical to the current file. — EVIDENCE: spec-changes.md:465, :478-489.

FACT: every "verbatim" anchor in the staging resolves against the current tree. §4.1 third sentence spec/04:157; §4.7 `Shutdown` row spec/04:686; §4.7.9 step 5 spec/04:854; §5.2 Scrub model spec/05:453, Slot cleanup bullet spec/05:545, Max retries bullet spec/05:555 (under the `Slot retry policy` heading at :553); §6.2 fence spec/06:151-152, `resuming → cancelled` bullet spec/06:234; §7.1 atomicity paragraph spec/07:23, §7.2 preamble spec/07:210, step 2 spec/07:213, step 3 spec/07:214, §7.3 list tail spec/07:414; §29.4 step 13 tail spec/29:704-706. Nobody needs to re-verify these.

FACT: the §7.1 atomicity paragraph sits INSIDE a fenced code block (fence opens spec/07:5, closes spec/07:54), so SPEC-2's new `**Pod-side reclaim on a failed bind.**` paragraph lands inside a fence and its markdown links will not render as links. The shipped atomicity paragraph already carries unrendered links there, so this matches the section's existing structure and is not a defect of this proposal. Do not file it; do not "fix" it by moving the paragraph out of the fence, which would reorder the flow listing.

FACT: no client-facing parallel representation exists for anything the staged spec edits change. `receiving_uploads` and the per-slot sub-states are never returned externally (spec/15:672); no `receiving_uploads`/`slot_cleanup`/`exited_cleanly`/`ReportSessionScrub` token appears anywhere under `sdks/`; `WARM_POOL_EXHAUSTED` and `concurrent_slots_exhausted` appear in neither `pkg/gateway/externalapi/openapi/openapi.json` nor `docs/`; the CRDs under `charts/lenny/crds/` carry no slot-cleanup or per-slot-state description; and `schemas/runtime-ops-events.schema.json:174-185` describes the `terminate` frame's receipt semantics with no send guarantee, so SPEC-1's new send gate falsifies no schema text. — EVIDENCE: spec/15:672; schemas/runtime-ops-events.schema.json:174-185.

MISTAKE (nearly filed, and the same one an earlier round dropped): SPEC-3's new clause says a pre-`running` leak "is surfaced on the `lenny_adapter_leaked_slots` gauge", while spec/06:160 says the ADAPTER exposes that gauge from its `/healthz` health metadata and the gateway is the party that reads the `Shutdown` answer. It is NOT a spec-internal contradiction: the adapter builds the `ShutdownResponse`, so it knows its own cleanup failed and can record the slot leaked in its own metadata. The gauge being gateway-emitted in the tree (pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-223) is a pre-existing spec-vs-code divergence, not something SPEC-3 creates.

MISTAKE (nearly filed): staged §7.1 says the gateway sends the reclaim "on the connection the failed stage still holds, before that connection closes", while §28.3's `LNK-POD-GRPC` register row states "One connection per gateway replica per pod" — a pooled connection that does not close when one bind attempt fails, so the stated upper bound may never trigger. Withdrawn: the proposal separately states the reclaim has no spec deadline and reuses §5.2's per-slot cleanup timeout in code, so the loose clause bounds nothing that another statement relies on. — EVIDENCE: spec/28_communication-channels.md:106; spec-changes.md:262, :265-266.

USEFUL [Standing context, "No per-slot sub-state is client-visible" and "`ShutdownResponse` is `{exited_cleanly, exit_code}` ... defined nowhere in spec/ or docs/"]: both held on re-verification and between them retire most of this lens's search space in one read. Keep them.

USEFUL [Standing context, "Dead end, and here are its five sites: 'the withheld report contradicts §4.7 or §12'"]: the five sites (spec/04:692, spec/05:453 and :545, spec/12:481 and :494) plus schemas/lenny-adapter.proto:308-319 and docs/reference/adapter-contract.md:81 are the exact grep result for `ReportSessionScrub` across spec/, docs/ and schemas/. Confirmed; the list is complete and nothing new has landed.

UNVERIFIED: the delta's single-session arm ("the failed attempt releases the pod's claim and the pod retires") is stated inside a §5.2 sentence whose subject is a bind "abandoned OR fails". An ABANDONED bind that is not a failed attempt — a `POST /terminate` at `ready` on a recycling exclusive pod — reaches `completed` rather than a pre-attached failure, so the pod is recycled and reused rather than retired, and the arm's "the residue does not outlive the pod" does not obviously hold there. Not a client-surface question, so this lens did not pursue it; the mechanism or coverage lens should. — EVIDENCE: spec-changes.md:465 ("abandoned or fails"); spec/06:283 (pre-attached disposition is a FAILURE disposition); spec/05:455 (a recycling pod is reused at occupancy zero).


### [spec-recheck.3.review-docs-alignment.1]

DECISION: returned an empty findings list for the docs-alignment lens on this recheck — BECAUSE the only
content delta since the last converged review is confined to SPEC-3's §5.2 scrub-model append and its
rationale, and that delta names no new metric, alert, error code, endpoint, flag or lifecycle step, so it
opens no docs/reference or docs/runbooks companion obligation; every other docs-alignment candidate on this
proposal is either already refuted or has its only remedy in the docs lane, which this loop may not edit —
ALTERNATIVES: filing the `lenny_adapter_leaked_slots` route for a pre-`running` leak (barred, standing trap
"Dead end: the adapter cannot see this leak"); filing the docs/reference/adapter-contract.md drift (docs
lane, already six-times DEFERRED); filing the `slot_cleanup ──→ leaked` concurrency asymmetry (standing trap
plus a separate OPEN).

FACT: the recheck snapshot `scratchpad/cp-snap/0081/spec-recheck-r3` is BYTE-IDENTICAL to the live proposal
directory, and `spec-recheck-r2` differs only in `.review-log.md`. The last real spec-changes delta is
`spec-recheck-r1-start` → now, and it is 36 diff lines, all inside SPEC-3 (the §5.2 append gains the
concurrency split "On a pod serving concurrent sessions the slot is then `leaked` … On a pod serving one
session the disposition is the one §7.1 states", and its rationale paragraph is rewritten). Diff against
`spec-recheck-r1-start`, not against the named snapshot, or you get an empty diff and lose the round.
EVIDENCE: `diff -rq scratchpad/cp-snap/0081/spec-recheck-r3 proposals/0081_.../` returns nothing.

FACT: no new metric or alert obligation exists on this proposal. `lenny_adapter_leaked_slots` is already
spec text at spec/05_runtime-registry-and-pool-model.md:545 and spec/06_warm-pod-model.md:160, and there is
no row for it (nor for leaked slots, per-slot cleanup, or session scrub) in `docs/reference/metrics.md` or
`pkg/alerting/rules` today. Greps for `leaked_slots|leaked slot` over `docs/`, `spec/16*` and
`pkg/alerting/rules` return only `docs/reference/state-machines.md:248` and `:251`, both of which DOCS-1's
row and the standing DEFERRED already cover. So SPEC-3 naming the gauge in §5.2 mints nothing.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; spec/06_warm-pod-model.md:160;
docs/reference/state-machines.md:248,:251.

FACT: the tier-11 gates that read the two anchors SPEC-3 edits are LINE-scoped, so the append cannot break
them. `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go` pulls
`requireLine(t, s52, "Whole-pod replacement trigger")` and `requireLine(t, s52, "Session count limit")` and
asserts substrings on those single lines; the `**Scrub model.**` paragraph is a different line and the
`**Slot cleanup:**` bullet's action-list sentence is not one of the pinned substrings. Nobody needs to
re-derive this. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:80-133.

FACT: the SPEC-1 §29.4 rationale's line citations resolve. `spec/29_communication-scenarios.md:585-590` is
the `**Preconditions.**` paragraph ("completed, so the runtime is running" is on :586, the interrupt-path
addition runs :588-590); step 12's "adapter closes the session runtime" is on :697; step 10's "valid in any
non-terminal state" is on :671 inside the :669-674 span the proposal cites. The proposal's `:589-591` for
the interrupt sentence is off by one at each end (the sentence starts on :588 and ends on :590). That is a
normalisation, not a finding, and it belongs with the existing "Citation ranges to normalise" trap.
EVIDENCE: spec/29_communication-scenarios.md:585-590,:669-674,:696-697.

WATCHOUT: the §5.2 paragraph two below the `**Scrub model.**` anchor, `**Recycling and integration levels.**`,
says recycling runs "with no CH-RUNTIMEOPS exchange between sessions", while SPEC-1's §4.7 row puts the
§15.4.2 graceful-shutdown frame inside the runtime teardown and lets it out whenever the deregistration
leaves no bound entry — which on a recycling concurrent pod is exactly the occupancy-zero release. It reads
like a fresh contradiction SPEC-1 creates. It is not: `drainViaLifecycle` already fires under `!boundRemains`
in the shipped adapter, so the §5.2 sentence is loose today and SPEC-1 restates rather than widens. Do not
file it. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:457; pkg/adapter/session.go:259.

USEFUL [standing context, Settled: "The anchor sweep is done, and it now covers SIXTEEN sites"]: spot-checked
spec/04:157, spec/04:686, spec/05:453, spec/05:545, spec/06:150-156 against the staged verbatim blocks and
all four still match byte for byte. The entry is current; the sweep does not need re-running.


### [spec-recheck.3.review-edit-sites.1]

DECISION: filed exactly one finding, on the exclusive-pod arm of SPEC-3's new leak-accounting clause (spec-changes.md:465) — BECAUSE the clause's antecedent is the broad "a bind abandoned or fails ... before it reaches `running`" range while its one-session consequent presupposes a failed gateway attempt, and a terminate-at-`ready` on a `maxConcurrentSessions: 1, recycle.enabled: true` pool sits in the antecedent but recycles rather than retires (spec/15:649 target state is `completed`; spec/06:74 admits that configuration; spec/05:455 holds the pod through `reserved`) — ALTERNATIVES: I built and dropped candidates on the `lenny_adapter_leaked_slots` gauge (recorded dead end, review-log.md:506/:544), on the §5.2 action list omitting `/sessions/` and `/artifacts/` (refuted), on §4.7:692's `ReportSessionScrub` row (SPEC-3's "It **also** runs" makes the pre-`running` path not a session release, so :692 stays true), and on §6.2:235's `resuming → completed` sibling bullet (a pointer at §7.2, which SPEC-2 corrects).

FACT: EVERY verbatim anchor in the staged spec edits resolves against the tree at HEAD. I checked all twelve: spec/04:157, :686, :854; spec/05:453, :545, :559 (`**Max retries:**`); spec/06:151-155 fence, :234; spec/07:23, :210, :213, :214, :387 (`4. If retries exhausted`); spec/29:710. Every markdown anchor in the staged text also resolves (`#71-normal-flow`, `#73-retry-and-resume`, `#62-pod-state-machine`, `#52-pool-configuration-and-execution-modes`, `#47-runtime-adapter`, `#49-credential-leasing-service`, `#1542-rpc-lifecycle-state-machine`, `#151-rest-api`, `#479-startup-sequence-for-type-agent-runtimes`). A future round need not re-derive this. — EVIDENCE: spec/04_system-components.md:157,686,854; spec/05_runtime-registry-and-pool-model.md:453,545,559; spec/07_session-lifecycle.md:23,210,213,214.

FACT: the per-slot sub-state fence has exactly TWO mirrors outside spec/06 and neither is spec-side. `grep -rn "slot_cleanup\|slot_assigned\|receiving_uploads" spec/ docs/ schemas/ charts/` returns spec/06:148-155, docs/reference/state-machines.md:234-237,251 (DOCS-1), schemas/lenny-adapter.proto:442 (one comment naming `slot_cleanup → released`, unaffected by an added edge), and spec/15:672, which names `receiving_uploads` as a *session-model* state in a different machine. SPEC-4 therefore opens no unlisted spec-side edit site. — EVIDENCE: spec/15_external-api-surface.md:672; docs/reference/state-machines.md:234.

FACT: the teardown vocabulary SPEC-1 introduces collides with nothing. `grep -rn "per-session teardown\|per-slot teardown\|whole-pod teardown\|slot release\|runtime teardown" spec/ docs/ schemas/` returns only spec/04:157 (the sentence SPEC-1 replaces, plus the second sentence it deliberately leaves) and two `slot released` hits in state-machine trigger text. No orphaned term. — EVIDENCE: spec/04_system-components.md:157.

FACT: §5.2's `**Recycle lifecycle**` paragraph is the surface that governs a one-session pod that does NOT retire, and it is in no edit list. It states the `reserved` hold and next-session reuse for a `standard`/`in-place` pod, and its only retire rule is "A session that ends in failure or a crash always retires its pod regardless of recycle settings" — which a `completed` terminate does not trigger. Any future sentence asserting "the pod retires" for an exclusive pod has to survive this paragraph. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455; spec/06_warm-pod-model.md:74.

FACT: `spec/28` carries NO trigger claim for the CH-RUNTIMEOPS `terminate` frame. Its message-schema row (:1082) and its Timing/Degradation bullets (:1100,:1126) bound the frame's reply and its deadline only, so SPEC-1's new co-tenancy gate on the graceful-shutdown signal falsifies nothing in §28 and §29.4 step 13 is the sole trace-side edit site. — EVIDENCE: spec/28_communication-channels.md:1082,1100,1126.

MISTAKE (nearly filed): "SPEC-3 leaves §4.7:692's `ReportSessionScrub` row asserting a report at every session release." It does not. SPEC-3's appended text says the per-slot cleanup "**also** runs" on the pre-`running` path, which makes that path something other than a session release, and its closing sentence ("A session release produces at most one cleanup-outcome report") is scoped to session releases. :692 survives verbatim. Cost: about fifteen minutes.

UNVERIFIED: whether a `maxReadyTimeoutSeconds` / ready-watchdog abandonment terminates the session as `failed` (retiring an exclusive pod) or as something else. I established the terminate-at-`ready` case from spec/15:649 and did not chase the watchdog. Whoever fixes finding 1 should check it, because if the watchdog also yields a non-failure terminal it widens the same hole. — EVIDENCE: spec/15_external-api-surface.md:649.


### [spec-recheck.3.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action feasibility lens — BECAUSE every action the staged spec text assigns is one its named actor can perform on the shipped surfaces, and the four candidates that looked live all reduce to standing traps or to prose imprecision with no wrong outcome. ALTERNATIVES rejected, with the reason each died: (a) "the `lenny_adapter_leaked_slots` gauge in SPEC-3's new clause names an actor (the adapter, per spec/06:160) that never learns of a pre-`running` leak" — the standing dead end on adapter-side leak visibility already covers it, and the clause names no actor; (b) "SPEC-3's new single-session sentence ('the failed attempt releases the pod's claim and the pod retires') is false for a terminate-at-`ready` on an exclusive RECYCLING pool, where the pod recycles rather than retires and no attempt failed" — a cousin of the eight-times-refuted exclusive-pod-retires family, and the residue is reclaimed by the occupancy-zero whole-pod scrub anyway, so no wrong outcome; (c) "SPEC-3's rationale enumerates three CODE paths (retry-placed, create-time reserved, §7.3 re-attach) while claiming to reach §7.1's three ATTEMPT KINDS (creation finalize block, §15.1 start, §7.3 re-attach), and the finalize block is in neither list" — defensible because `prepareAtFinalize` returns nil for `MaxConcurrentSessions > 1`, so the finalize block issues no pod-side RPC and holds no slot on a concurrent pool, and the exclusive arm of the same clause covers it; (d) the §4.1 rationale's new "this edit retires no vocabulary" — the edit does remove spec/'s only occurrence of "per-session teardown", but nothing in spec/ or docs/ else uses the phrase, so no edit site opens.

FACT: the snapshot the orchestrator names for this round (`scratchpad/cp-snap/0081/spec-recheck-r3`) is BYTE-IDENTICAL to the live proposal, and so is `spec-recheck-r2`. `spec-changes.md` last changed between `spec-recheck-r1-start` and `spec-recheck-r2`. md5 the file across every snapshot before diffing; the r3 diff is empty and costs a round to discover. The real delta for this recheck loop is `diff -u scratchpad/cp-snap/0081/spec-r8/...spec-changes.md <live>`, which is four hunks: the blob-store edge-case bullet, the §4.1 rationale expansion, the §29.4 step-12 rationale, and SPEC-3's leak-accounting clause plus its rationale. EVIDENCE: scratchpad/cp-snap/0081/ (md5 0ca78ae6… for r2/r2-start/r3-start/r3/live; b7adcf97… for spec-r8).

FACT: every citation in the round-2 fix text is accurate against the tree. spec/04:151 is "Each request message on the gateway-adapter protocol is either session-scoped or pod-scoped, and the classification is derived from the message's field set"; spec/04:157 is the `ShutdownRequest` paragraph and does carry both "per-session teardown" and "whole-pod scrub"; spec/29:586-588 is the Preconditions sentence ending "so the runtime is running"; :589-591 is the interrupt path's additional requirement; :671 carries step 10's "valid in any non-terminal state" with the §15.1 citation at :673; :697 carries "adapter closes the session runtime". Nobody needs to re-verify these.

FACT: all twelve "text to replace" anchors across SPEC-1..SPEC-4 still match the tree byte for byte and occur exactly once, re-checked mechanically on 2026-09-09 against spec/04, spec/05, spec/06, spec/07. The sixteen-site Settled entry is current.

FACT: §4.9's adapter-side timer obligation that SPEC-3's widened action list cites is real and exactly scoped. spec/04_system-components.md:1170 (inside §4.9, which spans :1099 onward) states "In direct delivery mode, the adapter MUST set a local timer for each credential lease's `expiresAt`" and "In proxy delivery mode ... no adapter-side timer is needed", so the staged "direct-delivery-mode lease-expiry timers" qualifier is exact and the adapter is the actor that can cancel them. `slotlayout.RemoveTree` (pkg/adapter/slotlayout/tree.go:59-70) removes `p.CredentialsDir`, so the staged credential-directory action is shipped behaviour too.

FACT: the adapter CAN find an unbound entry from a `ShutdownRequest`'s session id, which is what makes SPEC-1's widened slot-release precondition implementable. `deregisterSlotLocked` indexes `s.slots[sessionID]` directly (pkg/adapter/slotsession.go:174-176) and the registry is keyed by slot id, which equals the session id on every path. No lookup by binding is involved.

FACT: "the gateway's slot-failure accounting covers the retry-placed bind path today" is true as written. `applySlotRetryPolicy` already runs `MarkLeaked` → `leakGauge` → `RecordLeak` on `relErr != nil` and `RecordFailure` otherwise, then `Unhealthy` → `DrainSandbox` → `replacement` → `Forget` → `ForgetPod` → zero-gauge. EVIDENCE: pkg/gateway/sessionserver/start.go:2807-2884.

WATCHOUT: the temptation on SPEC-3's newest clause is to file "the gauge/threshold has no feed on the §7.3 re-attach". That was the round-2 finding and it is fixed: the rationale now says the code lane EXTENDS the helper to that path, and CODE-5 stages the third `accountSlotFailure` caller in `resumeOnPod`'s `podBinder.Resume` failure branch. Read non-spec-changes.md:485-500 before re-deriving it. EVIDENCE: proposals/0081_.../non-spec-changes.md:485-500.

UNVERIFIED: whether SPEC-3's new single-session sentence ("the failed attempt releases the pod's claim and the pod retires") holds for an exclusive RECYCLING pool, where a bind abandoned at `ready` recycles the pod through `ReleaseSlot(recycle=true)` rather than retiring it. I judged it below the bar because the occupancy-zero whole-pod scrub reclaims the residue either way and the "pod retires" proposition is the eight-times-refuted family. A round that wants it needs to show the recycle path leaves residue the whole-pod scrub does not sweep.


### [spec-recheck.3.review-fresh.1]

FACT: the snapshot named in this round's brief (`scratchpad/cp-snap/0081/spec-recheck-r3`) is BYTE-IDENTICAL to the live proposal, so `diff -ru` against it is empty and gives this lane no delta. The real delta for the recheck lane is `spec-recheck-r1-start` → current, and it touches ONE file: the SPEC-3 §5.2 `**Scrub model.**` append (the leaked-accounting sentence split into a concurrent arm and an exclusive arm) plus its rationale paragraph. `spec-recheck-r2` → current differs only in the review log. EVIDENCE: `diff -rq scratchpad/cp-snap/0081/spec-recheck-r2 proposals/0081_.../` returns only the review-log row. This is the fifth round in which the named snapshot was empty (Settled already records rounds 2, 4, 5, 7); the standing entry should now be read as "always diff against the previous NAMED-DIFFERENT snapshot, and find it by `diff -rq` over the whole `cp-snap/0081` directory first".

FACT: the anchor sweep is still mechanically clean after the delta. A script that extracts every fenced block from spec-changes.md and counts occurrences across `spec/*.md` gives exactly one hit for each of the twelve "text to replace" blocks and zero for every "replace it with" block. The one prose-anchored edit (§29.4 step 13) is the only site not covered by that check, and its non-uniqueness is already a standing trap. Re-running this script is ~30 seconds and is cheaper than reading the sixteen-site Settled list.

FACT: every markdown link inside every staged spec block resolves. Cross-file anchors were checked by slugifying the target file's headings; the two same-file anchors (`#71-normal-flow`, `#73-retry-and-resume`) exist at spec/07:3 and spec/07:378.

FACT: the §29.4 rationale's bound/started drift IS fixed. spec-changes.md's SPEC-1 §29.4 block now argues step 12 from §29.4's own Preconditions paragraph rather than from "every bound entry the call removed", and both its line citations hold: `spec/29_communication-scenarios.md:586-588` carries "so the runtime is running", `:589-591` carries "The interrupt path additionally requires", `:669-674` carries step 10's "valid in any non-terminal state" with its §15.1 citation, and `:697` carries "adapter closes the session runtime". The standing Traps entry naming spec-changes.md:209-211 as "the currently-filed finding" is now stale and should be retired on the next compaction.

DECISION: returned an EMPTY findings list — BECAUSE the delta's only staged change (splitting the leak-accounting clause into "On a pod serving concurrent sessions ... `leaked`" and "On a pod serving one session ... §7.1's disposition") is consistent with staged §7.1 word for word, cites §7.1 accurately, and puts the concurrency-scoped `leaked` terminal and the `ceil(maxConcurrentSessions/2)` trigger back inside the scope §6.2 and §5.2 actually state them in, which is the third-occurrence defect class the round's own MISTAKE entry names — ALTERNATIVES: filing the new exclusive-pod arm as contradicting §5.2's `**Slot cleanup:**` bullet ("If cleanup fails, the slot is leaked — the pod continues"), rejected because the bullet sits under a `maxConcurrentSessions > 1` heading and the "on a pod of either concurrency" pointer-clause reading was already refuted; filing it as contradicting the staged §6.2 `resuming → cancelled` bullet and §7.2 step 3, which release the replacement pod to the pool rather than retiring it, rejected because that is the eight-times-refuted "§7.1 says the exclusive pod retires" family and I have no NEW evidence of the kind the standing trap demands.

WATCHOUT: the exclusive-pod retirement claim now has THREE staged sites, not two. It is in §7.1's new paragraph, in the accepted-failure-mode bullet, and as of this delta in §5.2's `**Scrub model.**` append. A future round that ever does find the new evidence the standing trap asks for must edit all three, and the "Spec files touched" spec/05 entry names the §5.2 append only as "the per-slot cleanup pointer and the cleanup-outcome report rules appended", which does not hint that a pod-disposition claim now lives there. EVIDENCE: spec-changes.md SPEC-3 append ("On a pod serving one session the disposition is the one [Section 7.1] states"); spec-changes.md staged §7.1 block; spec-changes.md edge-case bullet "A reclaim the adapter does not acknowledge on a pod serving one session".

FACT: SPEC-3's second rationale claim checks out against the code lane. `applySlotRetryPolicy` already carries the MarkLeaked / RecordLeak / RecordFailure / Unhealthy → DrainSandbox tail (pkg/gateway/sessionserver/start.go:2841-2869), and CODE-5 adds the `bindConcurrentSlot` reserved branch and the `resumeOnPod` branch as the second and third callers, so "the clause holds on all three" is true as written.

UNVERIFIED: whether "(first sentence plus one sentence)" in the "Spec files touched" spec/04 entry is actually wrong. The standing Deferred entry says the §4.7 replacement "swaps two sentences for seven", but the edit as instructed replaces the row's FIRST sentence and ADDS the no-op sentence, retaining the second verbatim inside the replacement block. I judged the description defensible and did not file it. If a later round wants it closed, the honest reading is that the parenthetical describes the edit rather than the anchor block, and the Deferred entry should be retired rather than actioned.


### [spec-recheck.3.review-kubernetes.1]

FACT: The §4.6.1 occupancy projection retires a pod on a claim RELEASE only when the pool is
non-recycling. Verbatim: "`idle` when the claim is deleted on a recycling pod under its limits"
and "`draining`, then `terminated`, when the claim records a terminal disposition (`released` or
`failed`) or is deleted on a non-recycling pod ... the projection returns a pod from `claimed` to
`idle` only on a recycling pool." — EVIDENCE: spec/04_system-components.md:415-416; the same rule
restated at spec/06_warm-pod-model.md:80.

FACT: `failPhase` (the pre-attached bind-failure reclaim) calls `b.drain`, and `drain` is a bare
`podclaim.DeleteClaim` with no terminal-disposition status patch. So the code performs exactly the
release that projects `idle` on a recycling pool. — EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:1200-1202; :1072-1082 (failPhase, whose own comment
claims "retired by draining it"); contrast pkg/gateway/podlifecycle/podsession/binder.go:1090-1096
(ReclaimClaimed's comment: DeleteClaim "returning the pod to the pool per the §4.6.1 occupancy
projection"). The two comments describe the same call oppositely.

FACT: `maxConcurrentSessions: 1` with `recycle.enabled: true` is a first-class supported
configuration (the sequential-reuse path), so "one session per pod" does not imply "one session per
pod lifetime". — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:449; the config table at :423.

WATCHOUT: The staged exclusive-pod carve-out ("the failed attempt releases the pod's claim and the
pod retires, so the reclaim's residue does not outlive the pod", spec-changes.md:262 and the new
:465 clause) is load-bearing twice over: it is the reason SPEC-3 withholds the `leaked` accounting
at concurrency 1, and it is the reason the edge-case list treats the exclusive path as needing no
disposition. It fails on the sequential-reuse configuration. Do not read §6.2:283 as settling it in
the proposal's favour: that sentence says "The pod is marked `failed` and released back to the pool
(or terminated if unhealthy)", which asserts both horns. — EVIDENCE:
spec/06_warm-pod-model.md:283.

WATCHOUT: The proposal's own staged §7.2 step 3 and staged §6.2 `resuming → cancelled` clause both
say the half-claimed replacement pod is "released ... to the pool" after the reclaim, which is the
opposite of "the pod retires". A round-2 finding on the §7.3 half of this was REFUTED (the skeptic
read spec/06:234's "released to the pool" as compatible with retirement), so if you want to raise
the resume path, do it through the §4.6.1 projection rule rather than through §7.3 reachability. —
EVIDENCE: spec-changes.md:320, :368; spec/07_session-lifecycle.md:214; spec/06_warm-pod-model.md:234.

FACT: Ownership is clean throughout this proposal. Nothing staged writes another component's status
subresource, no finalizer is introduced, the `leaked` occupancy lives in Redis rather than etcd, and
the §7.1 reclaim is a direct gRPC on the failing connection rather than a controller reconcile on
the hot path. The one CRD-adjacent mechanism the staging depends on is the claim-existence
projection above. — EVIDENCE: spec/04_system-components.md:606-620 (4.6.3 table);
spec/06_warm-pod-model.md:160.

UNVERIFIED: whether any gateway path patches `SandboxClaim.status.phase` to the terminal `failed`
binding state on a pre-attached bind failure. I found only the bare DELETE. Someone should grep the
claim-status patch callers before the code lane assumes the terminal disposition exists.


### [spec-recheck.3.review-mechanism.1]

DECISION: returned EMPTY — BECAUSE every mechanism defect I derived from the round-3 delta lands in one of four already-closed buckets: the refuted list, the barred traps, an already-fixed item, or summary.md's "Open decisions for human to make" (entries 11 and 12). ALTERNATIVES: I built and dropped four candidates, listed below, rather than filing a speculative one.

FACT: the r3 snapshot is a no-op for this lens. `scratchpad/cp-snap/0081/spec-recheck-r3` is byte-identical to the live proposal, and `spec-recheck-r2-start` and `spec-recheck-r2` are too. The only delta since the last converged review is `spec-recheck-r1-prefix` → live, and it is confined to ONE hunk of spec-changes.md: the SPEC-3 §5.2 scrub-model append gained a concurrency split ("On a pod serving concurrent sessions the slot is then `leaked` … On a pod serving one session the disposition is the one §7.1 states"), and its rationale paragraph was rewritten to match and to add the three-bind-path accounting sentence. Diff `spec-recheck-r1-prefix`, not `spec-recheck-r3`. EVIDENCE: scratchpad/cp-snap/0081/spec-recheck-r1-prefix/*.spec-changes.md vs proposals/0081_.../*.spec-changes.md:465,478-489

FACT: the round-3 fix is correctly grounded on both halves. §5.2's whole-pod replacement trigger is at spec/05_runtime-registry-and-pool-model.md:561, physically below the `**Scrub model.**` paragraph at :453 (so "stated below" resolves) AND inside the `**Slot retry policy (maxConcurrentSessions > 1)**` block opened at :553 (so the new "On a pod serving concurrent sessions" scope is required, not decorative). `lenny_adapter_leaked_slots` is already named in unedited §5.2 at :545 and in §6.2 at :160, so naming it in the append mints no new metric reference and creates no spec/16 edit site.

FACT: the previously-filed "unclean answer discarded for a start in flight" defect is genuinely closed. CODE-1's staged response is now `ExitedCleanly: closeErr == nil && (live || treeErr == nil)` with `live := removed && s.runtimeHoldsLocked(sessionID)`, so every pre-`running` reclaim (never-admitted OR still-in-flight) surfaces `treeErr`. SPEC-3's promise "the adapter's `Shutdown` response for that reclaim does not report a clean exit" now holds across the whole staged range. EVIDENCE: non-spec-changes.md:100-104,:152-154

FACT: the rewritten rationale's three-path accounting claim checks out against the tree and the code lane. `applySlotRetryPolicy` already does MarkLeaked + leakGauge + RecordLeak on `relErr != nil` (pkg/gateway/sessionserver/start.go:2833-2848), and CODE-5 extends the extracted `accountSlotFailure` to `bindConcurrentSlot`'s reserved branch and to `resumeOnPod`'s `Binder.Resume` failure branch. EVIDENCE: non-spec-changes.md:472-489; pkg/gateway/sessionserver/start.go:2807,:2833-2848

MISTAKE (nearly filed, withdrawn ×4). Recording these so the next mechanism lens does not rebuild them:
  1. "The new exclusive-pod branch mis-assigns §7.1's failed-attempt disposition to the terminate-at-`ready` / READY_TIMEOUT class, which is a pre-`running` cleanup on an exclusive pod but is not a failed bind attempt." Withdrawn: it is the already-refuted "SPEC-3's no-report exception is narrower than the `started` gate" finding wearing a disposition costume, and open decision 11 (summary.md:293-297) owns the "fails" vs "abandoned or fails" trigger question.
  2. "The new exclusive-pod branch is a third site of the eight-times-refuted 'exclusive pod retires while §7.2 step 3 releases it to the pool' claim." Withdrawn: the standing trap bars it absent NEW evidence from the mid-resume terminal handler's release path, which I did not obtain, and open decision 11's sibling (the §7.1 mid-resume carve-out) is an unresolved OPEN routed to a human.
  3. "SPEC-4's prose puts an upload-free plan's `FinalizeWorkspace`-first bind in `receiving_uploads`, but the fence's entry trigger reads 'workspace materialization begins for this slot'." Withdrawn: this is the standing UNVERIFIED "`receiving_uploads` on an upload-free plan"; four lenses assumed the charitable reading and `FinalizeWorkspace` does materialize the workspace, so the reading holds without an edit.
  4. "§5.2's `**Fresh workspace guarantee**` (spec/05:556) is falsified by the staged placement rule's same-pod arm, because SlotID == SessionID means the retry reuses the same tree." Withdrawn: it is false in the SHIPPED tree today (no `Shutdown` is sent at all on a failed bind, so the tree always survives), and the staged change strictly improves it by deleting the tree on an acknowledged reclaim. Pre-existing, already in Standing context.

WATCHOUT: the concurrency split the fixer landed in SPEC-3 makes THREE sites now state the exclusive-pod disposition — staged §7.1 (spec-changes.md:262), the edge-case bullet (:130-134), and now SPEC-3's append (:465). A later fix that adjusts the exclusive-pod wording in one of them must sweep all three or it re-opens the predicate-drift class this round closed. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:130-134,:262,:465

USEFUL [Standing context, "The snapshot diffs have been empty for most rounds"]: saved me from reviewing an empty diff. It is now true for the recheck lane too; I extended the fact above with the r1-prefix→r2-start delta so the next agent has the pointer.

USEFUL [Standing context, Traps]: the barred-family entries ("MISTAKE, refuted at least eight times", "Do not file either `leaked` gloss", "Dead end: the adapter cannot see this leak") each killed a candidate of mine before I spent a verifier pair on it. This is the highest-value block in the log.


### [spec-recheck.3.review-operational.1]

DECISION: returned EMPTY under the operational-consistency lens against the r8→current delta (the edge-case
bullet-one rewrite, the §4.1 rationale paragraph, the §29.4 step-12 rationale paragraph, and SPEC-3's new
leak-accounting clause plus its rationale) — BECAUSE every metric, threshold and report chain the delta
names resolves in the tree and in the applied spec, and the two residual mismatches I found are both
recorded pre-existing dead ends — ALTERNATIVES: filing the `lenny_adapter_leaked_slots` gauge as absent from
§16's inventory (pre-existing at spec/05:545 and spec/06:160, and BUILD-GAPS.md:4963 records the deliberate
decision to keep it out of the §16.1 typed catalog); filing the adapter-`/healthz`-versus-gateway-gauge split
(standing trap "Dead end: the adapter cannot see this leak").

FACT: no alert rule in `pkg/alerting/rules` keys on any slot, scrub, leak, retirement or session-reuse
series — `grep -n "slot\|scrub\|leaked\|retirement\|session_reuse" pkg/alerting/rules/rules.go` returns
nothing — so no staged spec edit on this surface can orphan an alert or break tier-11 alert-to-runbook
resolution. This closes the §16 half of the standing note "This lens has swept itself out" for the current
text. EVIDENCE: pkg/alerting/rules/rules.go (no matches); spec/16_observability.md:9-15,:128.

FACT: the §16 rows that touch this surface all stay true after application, and each is true for a reason
worth recording rather than by luck. `lenny_gateway_pod_retirement_total` counts `session_count_limit` "at
the per-release `maxSessionsPerPod` drain" (:12) and the withheld `ReportSessionScrub` withholds the
`IncrementSessionsServed` that drives that drain, which is the intended behaviour rather than a lost count;
`lenny_pod_session_reuse_count` is "Observed per-pod at session end" (:128) and a pre-`running` reclaim is
not a session end; `lenny_slot_failure_total` (:14) is emitted by `Binder.BindSlot` per failing bind stage
and is untouched; `lenny_slot_pod_replacement_total` (:15) is scoped to `maxConcurrentSessions > 1` and both
new `accountSlotFailure` callers are concurrency-gated (`bindConcurrentSlot` sits behind
`match.MaxConcurrentSessions > 1`; the resume caller's non-empty-slot-id guard is exactly that condition).
EVIDENCE: spec/16_observability.md:12,:14,:15,:128; pkg/gateway/sessionserver/start.go:2807-2884.

FACT: the delta's new §5.2 clause ("...is surfaced on the `lenny_adapter_leaked_slots` gauge") has a
complete route on all three bind paths the proposal binds. `applySlotRetryPolicy` reaches
`slots.MarkLeaked` + `leakGauge` + `health.RecordLeak` under `sbe.Leaked || relErr != nil`, and the staged
`accountSlotFailure` gives the create-time-reserved branch (`sbe.Leaked`) and `resumeOnPod` (`sbe.Leaked`,
with `Binder.Resume` folding its own release outcome in) the same body. I verified the shipped arm rather
than trusting the staging. EVIDENCE: pkg/gateway/sessionserver/start.go:2833-2850; non-spec-changes.md:452-501.

FACT: `ReportSessionScrub` arms no gateway-side watchdog, so withholding it trips nothing. The
missing-report timeout in this area belongs to `ReportPodScrub` and is armed by `ReleaseSlot`'s `recycle`
parameter at the occupancy-zero recycle boundary; `ReleaseSlotReservation` passes `recycle=false`, so a
failed bind never arms it. This is the operational half of the standing Settled entry and it holds against
the tree. EVIDENCE: pkg/gateway/podlifecycle/podclaim/slotclaimer.go:866;
pkg/gateway/podlifecycle/podsession/slotbinder.go:497,:554.

FACT: the delta's two new line citations resolve. `spec/04_system-components.md:151` is the field-set
derivation sentence and `:157` is the `ShutdownRequest` paragraph the §4.1 edit lands in;
`spec/29_communication-scenarios.md:586-588` is the `**Preconditions.**` sentence ending "so the runtime is
running", `:589-591` is the interrupt path's additional requirement, `:697` carries "the adapter closes the
session runtime", and `:669-674` is step 10's "valid in any non-terminal state" clause with its §15.1
citation. All five are in proposal rationale prose rather than in staged spec text, so the N8 line-citation
prohibition is not engaged by them.

WATCHOUT: the arithmetic in the rewritten edge-case bullet one is right and looks wrong. "at
`maxConcurrentSessions: 2` a single windowed failure already reaches the §5.2 whole-pod replacement
threshold" is correct because `UnhealthyThreshold` is `(maxConcurrent+1)/2` in integer arithmetic, which is
1 at 2, matching `ceil(2/2)`. Do not file it as an off-by-one. EVIDENCE:
pkg/gateway/runtime/slothealth/slothealth.go:56-67; spec/05_runtime-registry-and-pool-model.md:561.

WATCHOUT: the snapshot named in this round's brief,
`scratchpad/cp-snap/0081/spec-recheck-r3`, is byte-identical to the live proposal (`diff -rq` exits 0), so
the "read the delta first" instruction has no target there. The delta that actually exists is
`spec-r8` → current. Diff against `spec-r8`, as the standing context's snapshot entry already warns.

USEFUL [standing context, Open: "This lens has swept itself out"]: it named the two cheapest checks (the §16
inventory rows and the `ReportSessionScrub` / `sessions_served` chain) and both were the right places to
spend the pass. Following it took the sweep from open-ended to about four greps.

USEFUL [standing context, Traps: "Dead end: the adapter cannot see this leak"]: the delta's new clause
asserts a gateway-determined leak is "surfaced on the `lenny_adapter_leaked_slots` gauge" while §6.2:160
says the ADAPTER exposes that gauge and a `/healthz` `leaked_slots` count. That reads as a fresh
operational contradiction and is the pre-existing one this entry records. It saved a finding.


### [spec-recheck.3.review-performance.1]

DECISION: returned an empty findings list for the performance / scalability / failure-mode lens on the r3 spec staging — BECAUSE the delta (SPEC-3's leak-accounting clause split into a concurrent arm and an exclusive arm, plus its rewritten rationale) adds no control-plane or data-plane write per unit of work, and every scale hazard I could build reduced to one of the already-refuted families — ALTERNATIVES: I built and withdrew three candidates, listed below.

FACT: the proposal creates NO per-session write amplification. The only new store-touching act on a failed bind is the drain-request annotation patch that already existed (`DrainSandbox`, gateway stamps `lenny.dev/drain-request`, WPC writes the phase). Successful binds gain nothing. No net-new informer watch, no new Redis key, no new Postgres row, no new etcd status write. A future perf lens can stop re-deriving this. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:585-605 (DrainSandbox doc comment, §4.6.3 annotation route); pkg/gateway/sessionserver/start.go:2807-2880 (applySlotRetryPolicy: the only accounting/drain site).

FACT: the adapter's `Shutdown` handler holds `s.mu` only across `deregisterSlotLocked` and releases it before the usage flush, the drain signal, `Runtime.Close`, `removeSlotTree` and `reportSessionScrub`. So the new pre-`running` reclaim introduces no pod-global serialization against co-tenant RPCs, and the "reclaim as a hot lock on a shared adapter" candidate dies here. EVIDENCE: pkg/adapter/session.go:237-241 (lock/unlock pair), :242-281 (teardown outside the lock).

MISTAKE (mine, withdrawn): "the staged leaked disposition converts transient adapter failures into persistent leak counts, so a correlated outage drains the whole pool." It does not clear the bar. The proposal's own edge case one routes the common correlated case (blob-store outage, upload-free plan) to a clean-exit no-op response, hence a windowed `RecordFailure` rather than `RecordLeak`, and §5.2:561 already counts leaked slots toward the trigger with no carve-out by code path. EVIDENCE: spec-changes.md:99-106 (edge case one); spec/05_runtime-registry-and-pool-model.md:561 (trigger text).

MISTAKE (mine, withdrawn and the closest of the three): "at `maxConcurrentSessions: 2` the create-time-reserved branch's first accounted leak drains the pod, while the session's row keeps its §4.6 pod binding, so the client's §15.1 retry reconnects to a pod that is being retired and has no re-placement mechanism." Both halves are real and neither is refuted anywhere — `bindReservedSlot` reads `Status.PodIP` and dials without any phase check (pkg/gateway/podlifecycle/podsession/slotbinder.go:230-254) — but the proposal has ALREADY accepted each half separately and in writing, so the composite reads as a consequence of two recorded limitations rather than a new defect. Do not re-file without a new argument that the COMPOSITION is worse than either half. EVIDENCE: non-spec-changes.md:508-518 ("at `maxConcurrentSessions: 2` its first accounted bind failure of either kind drains a pod that nothing drains today. That is the conformance gap closing"); spec-changes.md:110-127 (edge case three: "a start pinned to its create-time pod binding has no equivalent ... which this proposal does not stage").

FACT: the leaked disposition on the create-time-reserved branch is a strict IMPROVEMENT to the occupancy arithmetic there, not a regression. Today `BindReservedSlot`'s failure release passes `leaked=false` and decrements while the retry re-reserves nothing, so the retried session runs on occupancy the counter no longer counts; with `leaked=true` the decrement is withheld and the retry reuses the count. This is the opposite direction from the standing-context note that frames it purely as a pre-existing hole. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224 and :493-505 (`ReleaseSlot(ctx, sandboxName, false, false)`); pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836 (the `leaked` early return).

USEFUL [Standing context: "Dead end: the adapter cannot see this leak", "MISTAKE nearly filed: the replica-local leak ledger", "MISTAKE nearly filed: the reclaim as a deadlock sink"]: these three saved me from filing the three most obvious perf-lens candidates. They are the highest-value entries in the section for this lens and should survive compaction.

UNVERIFIED: whether a §15.1 retry that reconnects through `bindReservedSlot` onto a Sandbox already stamped for drain succeeds, fails at dial, or succeeds and then dies with the pod. Nothing in `bindReservedSlot` reads `Status.Phase`. A later reviewer or the code lane should settle it, because the answer decides whether the withdrawn composite above is an accepted limitation or a stuck-session bug. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:231-241.


### [spec-recheck.3.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE every reliability angle I could build against the
current staging maps onto an already-refuted family or a recorded pre-existing OPEN, and I could not
construct one that clears the materiality bar without re-litigating settled ground —
ALTERNATIVES: I built and dropped five candidates, each listed below as a MISTAKE so the next
reliability lens does not rebuild them.

FACT: the snapshot the orchestrator names for this round is byte-identical to the live proposal.
`diff -rq scratchpad/cp-snap/0081/spec-recheck-r3 proposals/0081_.../` exits 0, and so do
`spec-recheck-r2`, `spec-recheck-r2-start` and `spec-recheck-r3-start` against
`...spec-changes.md`. The last spec-changes.md delta is `spec-r8` → current, four hunks:
(1) the upload-free-plan edge-case bullet's closing clause rewritten to the honest windowed-counter
statement, (2) the SPEC-1 §4.1 rationale expanded with the `:151` / `:157` citations, (3) the
§29.4 step-12 "needs no condition of its own" rationale, (4) SPEC-3's leak-accounting clause plus
its rationale paragraph. Diff against `spec-r8`, not against the named snapshot.
EVIDENCE: scratchpad/cp-snap/0081/spec-r8/...spec-changes.md vs the live file.

FACT: every citation minted in that delta resolves. `spec/04_system-components.md:151` is the
field-set derivation sentence and `:157` is the shipped `ShutdownRequest` paragraph;
`spec/29_communication-scenarios.md:586-588` is the Preconditions sentence ending "so the runtime
is running", `:589-591` is the interrupt path's additional requirement, `:669-674` is step 10's
`terminate` validity clause citing §15.1, and `:697` carries "the adapter closes the session
runtime". The threshold arithmetic in hunk 1 is right: `UnhealthyThreshold` is `(maxConcurrent+1)/2`
in integer arithmetic, so one windowed failure trips at `maxConcurrentSessions: 2`.
EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:215-220; spec/05_runtime-registry-and-pool-model.md:561.

FACT: §15.4.2 states a pod-level machine `INIT → READY → ACTIVE → DRAINING → TERMINATED` and no
obligation that a DRAINING signal accompany every session end. A bound-but-unstarted session on an
exclusive pod never put the adapter in `ACTIVE` for that session, so CODE-1 moving `drainViaLifecycle`
inside the `started` block removes a signal §15.4.2 never owed. This closes the standing OPEN
"Does CODE-1's drain move lose a DRAINING obligation?" in the proposal's favour, on §15.4.2's own
text rather than on the spec/05:455 retirement argument the OPEN said was unchecked.
EVIDENCE: spec/15_external-api-surface.md:1691-1705; pkg/adapter/session.go:238-241,259-261.

FACT: SPEC-4's prose closes the standing UNVERIFIED "`receiving_uploads` on an upload-free plan".
It says "Every earlier stage of the §4.7.9 step-5 bind sequence ... leave the slot in
`receiving_uploads`", which asserts the charitable reading directly rather than relying on it, so a
`FinalizeWorkspace`-first (upload-free) bind has a legal edge. No `slot_assigned` hole is opened.
EVIDENCE: spec-changes.md:513; spec/06_warm-pod-model.md:151.

MISTAKE (built and withdrawn): "the reclaim in §7.2 step 3 runs before step 4's
`coordination_generation` bump, so a stale coordinator can re-create the entry the reclaim removed."
The same-replica form of this is the accepted racing-start failure mode; the cross-replica form
reduces to the gateway-death case, whose finding ("§7.1 states no outcome for a bind the gateway
never compensates") is already refuted. Trap on the cross-replica actor in §7.2 also bars the
adjacent framing. EVIDENCE: spec/07_session-lifecycle.md:215; review-log.md Traps ("Do not file the
cross-replica actor problem in §7.2").

MISTAKE (built and withdrawn): "SPEC-3's new clause asserts the leaked slot holds its occupancy
inside the very section whose Post-recovery rehydration paragraph rebuilds the counter from
`state='active'` Postgres rows, which a failed bind never has." §5.2 already states the
occupancy hold twice, at :545 and :561, so the new clause adds a third instance of a pre-existing
tension rather than creating one. The recorded remedy is widening §5.2's rehydration paragraph, which
is out of this proposal's staged scope. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545,:551,:561.

MISTAKE (built and withdrawn): "sentence 3 of SPEC-3's append makes `exited_cleanly` load-bearing
in §5.2 while §4.7 states only the clean-exit case, so the wire signal carrying the leak
determination is named nowhere." This is the already-refuted §4.7-completeness finding in mirror
image; its suggested fix is the same sentence a prior round proposed and the skeptic called polish.

MISTAKE (built and withdrawn): "the ordinary post-`running` release reports `released` when only its
tree removal failed, because `ExitedCleanly` is `closeErr == nil` and `reportSessionScrub` takes
`closeErr` alone." True in the tree and untouched by this proposal: `_ = removeSlotTree(st)` discards
the error today and the staged CODE-1 response deliberately keeps the `live` arm reading `closeErr`
alone. Pre-existing, and outside the staged spec edits.
EVIDENCE: pkg/adapter/session.go:270-279,290.

MISTAKE (built and withdrawn): "the 300s `resuming` watchdog bullet at spec/06:232 carries no
reclaim clause, and a hung workspace restoration is exactly the case in which the adapter may have
started the session." §7.1's trigger is "A gateway bind attempt that fails", which a watchdog-driven
abort is, so the obligation reaches it without a §6.2 pointer; the standing OPEN on the other three
`resuming` bullets already records that they state no pod release for a clause to attach to.

USEFUL [Settled: "The snapshot diffs have been empty for most rounds"]: saved the whole round. The
named snapshot was again identical and the entry told me to diff the earlier one instead.
USEFUL [Traps: the sixteen-site anchor sweep]: I spot-checked three anchors (spec/07:210, spec/06:234,
spec/05:545) and all three matched byte for byte, so I did not re-run the sweep.


### [spec-recheck.3.review-security.1]

Returned EMPTY. Swept the whole staging under the security lens; nothing cleared the bar.

FACT: the r3 recheck snapshot is byte-identical again — `diff -ru scratchpad/cp-snap/0081/spec-recheck-r3 proposals/0081_.../` is empty. The
real delta for this round is against `scratchpad/cp-snap/0081/spec-r8`, and it is four hunks in
spec-changes.md: (1) edge-case bullet one's blob-store accounting rewrite, (2) the §4.1 rationale
expansion, (3) the §29.4 step-12 rationale expansion, (4) SPEC-3's new leak-accounting clause plus
its rationale paragraph. Diff against `spec-r8`, not `spec-recheck-r3`. EVIDENCE: scratchpad/cp-snap/0081/

FACT: every citation in the four new hunks resolves. §29.4 preconditions at spec/29:586-588 ("so the
runtime is running"), the interrupt-path addition at :589-591, step 10's clause at :669-674 (and it
IS a verbatim restatement of §15.1's precondition table row, spec/15:649 "Valid in any non-terminal
state"), step 12's "the adapter closes the session runtime" at :697. §4.1's field-set derivation at
spec/04:151 and the `ShutdownRequest` paragraph at :157. The blob-store arithmetic is right:
`UnhealthyThreshold = (maxConcurrent+1)/2`, so 2 → 1. EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:212-221.

FACT: CODE-1's staged response is `ExitedCleanly: closeErr == nil && (live || treeErr == nil)`, gated
on `live` (runtimeLive), NOT on `st.started`. I built a candidate that the new SPEC-3 clause ("a
cleanup on that path that does not complete is still accounted: the `Shutdown` response ... does not
report a clean exit") is false for a start-in-flight reclaim whose tree removal fails, on the reading
that the gate was `started`. It is not; `started` is true and `live` is false there, so treeErr does
surface. Withdrawn. EVIDENCE: non-spec-changes.md:154 and :157-176.

MISTAKE (mine, nearly filed): I re-derived OPEN #234 (transient over-assignment on the acknowledged
racing-start ordering) as a §5.2 "Slot assignment atomicity" violation — the counter is the mandatory
intra-pod capacity gate (spec/05:549) and the racing ordering leaves a live session on the pod with
the reservation released `leaked=false`. Withdrawn on two grounds. First, it is strictly PRE-EXISTING
and the proposal narrows it: today every post-`StartSession` failed bind decrements the counter while
the runtime keeps the abandoned session, so today's exposure is unconditional and after the change it
is a race window. Second, the remedy is a record sentence in the racing-start bullet, which is the
exact class the material skeptic already refuted ("additive prose about an accepted, deliberately-
unclosed failure mode").

FACT, closing OPEN #233 ("Does CODE-1's drain move lose a DRAINING obligation?") on the security side:
no. §15.4.2 is an ADAPTER-process state machine, and DRAINING's own obligation is "the adapter
finishes the current exchange and signals the agent to stop" (spec/15:1700). A bound-but-unstarted
session has no exchange and no runtime session to stop, so moving `drainViaLifecycle` under the
`started` gate drops no obligation §15.4.2 states. It also removes a hazard rather than adding one:
today's `bound`-gated path calls `Runtime.Close(sessionID)` for a session `Runtime.Start` never
received, which is the "mid-start `Shutdown` bricks the pod" route (OPEN #208). Nothing else in the
staging carries a §15.4.2 obligation for an unstarted session.

FACT: the compensation DOES revoke credentials gateway-side — `compensateFailedSlotBind` calls
`b.releaseCredentials(req.SessionID)` unconditionally after `cl.Shutdown`, so a failed concurrent
bind returns the §4.9 leases even when the adapter never answers. A security lens looking for an
unrevoked lease on the leaked path should stop here rather than re-derive it. EVIDENCE:
non-spec-changes.md:337-340 (staged), pkg/gateway/podlifecycle/podsession/binder.go:1065-1078 for the
existing helper.

FACT: §4.9's adapter-side expiry timer is direct-delivery-mode only and its job is to DELETE
`/run/lenny/slots/{sessionId}/credentials.json` and report `AUTH_EXPIRED`; proxy mode enforces expiry
gateway-side with no adapter timer. SPEC-3's "direct-delivery-mode lease-expiry timers" qualifier is
exact. EVIDENCE: spec/04_system-components.md:1169 (the `anthropic_direct` row).

WATCHOUT: the one live security-shaped residue in SPEC-3's action list is OPEN #236, and it is
pre-existing so do not file it. The shipped `Shutdown` cancels the §4.9 timers inside
`deregisterSlotLocked` at the TOP of the handler while `removeSlotTree` runs at the bottom and its
error is discarded, so a cleanup whose credential removal fails leaves a direct-mode credentials.json
on the pod with its expiry enforcement point already disarmed. 0081 does not create this: it widens
the tree removal from `bound` to `removed`, which is strictly more removal, and it leaves the
ordering alone. If a round ever does want it closed, the edit is to condition the cancellation in
SPEC-3's action-list sentence, never to widen §4.9. EVIDENCE: pkg/adapter/session.go:237-238 (the
unconditional deregister) versus :271 (`_ = removeSlotTree(st)` under `if bound`).

FACT: the lens's trust-boundary check is satisfied and does not need re-deriving. The new SPEC-3
clause sources `leaked` from `ShutdownResponse.exited_cleanly`, which is an adapter (in-pod) answer,
but that is the SHIPPED channel for the same determination on every ordinary session end
(`leaked = err != nil || !cleanly` at pkg/gateway/podlifecycle/podsession/slotbinder.go:541-543), and
the authoritative COUNT stays gateway-side in `slothealth.Tracker`, which the gateway maintains from
its own observations rather than from the pod's `/healthz` `leaked_slots` self-report. The proposal
adds no bound sourced from a pod self-report.

USEFUL [Settled "The slot routes are concurrency-gated end to end"] and [Traps "MISTAKE, refuted at
least eight times"]: between them they killed three candidates before I spent a verifier pair on
them (the exclusive-pod retirement claim now restated inside SPEC-3's new clause, the residue riding
a pooled replacement pod back into inventory, and the tenant-isolation dress of the same).

DEFERRED [nothing]. I derived no correction whose remedy sits in a file this loop may not edit.

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
